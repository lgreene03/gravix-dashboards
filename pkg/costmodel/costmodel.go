// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Package costmodel turns an event volume into a monthly Gravix cost on each
// supported deployment shape. It reports measured and modelled inputs
// separately, because conflating them is how cost claims stop being believed.
//
// The API has one deliberate omission: there is no way to estimate a single
// deployment. EstimateAll returns all three, always, because publishing the
// $20 bootstrap figure without the at-scale figure beside it is the kind of
// half-truth that costs more credibility than it buys attention. That is the
// thesis's Axis 4 commitment, enforced here in the type signature rather than
// left to whoever renders the output.
package costmodel

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Deployment is a supported shape.
type Deployment string

const (
	DeploymentBootstrapVPS Deployment = "bootstrap_vps" // single VPS, DuckDB, local disk
	DeploymentAWSSingle    Deployment = "aws_single"    // EKS, S3, one region
	DeploymentAWSMulti     Deployment = "aws_multi"     // EKS, S3, three regions
)

// MaxPriceAgeDays is how long price data stands before it must be re-checked.
const MaxPriceAgeDays = 90

var (
	ErrNoPrices           = errors.New("costmodel: no price data")
	ErrStalePrices        = errors.New("costmodel: price data older than 90 days")
	ErrMissingMeasurement = errors.New("costmodel: BytesPerEvent must come from a bench result")
)

// Inputs describe the workload.
type Inputs struct {
	EventsPerMonth int64
	RetentionDays  int
	Services       int
	DashboardUsers int
	// BytesPerEvent is the total steady-state footprint per ingested event —
	// raw plus warehouse plus manifests. It must come from a bench result; see
	// MeasurementSource.
	BytesPerEvent float64
	// MeasurementSource names the bench result BytesPerEvent was read from.
	// Empty means the caller invented the model's dominant input, which is
	// refused: a cost model whose largest term is a guess is a guess.
	MeasurementSource string
}

// LineItem is one cost component.
type LineItem struct {
	Name      string  `json:"name"`
	USDMonth  float64 `json:"usd_month"`
	Basis     string  `json:"basis"`  // "measured" | "list_price" | "estimate"
	Source    string  `json:"source"` // a URL, or the bench result file
	Retrieved string  `json:"retrieved"`
}

// Basis values. A number's basis travels with it everywhere, so a reader never
// has to ask which kind of number they are looking at.
const (
	BasisMeasured  = "measured"
	BasisListPrice = "list_price"
	BasisEstimate  = "estimate"
)

// Estimate is one deployment's cost.
type Estimate struct {
	Deployment          Deployment `json:"deployment"`
	LineItems           []LineItem `json:"line_items"`
	TotalUSDMonth       float64    `json:"total_usd_month"`
	USDPerMillionEvents float64    `json:"usd_per_million_events"`
	Caveats             []string   `json:"caveats"`
}

// The mandatory caveats, verbatim. Each is attached to its deployment by
// EstimateAll and asserted by TestMandatoryCaveatsPresent, so one cannot be
// dropped by an edit that only touches rendering.
const (
	CaveatBootstrap = "You operate this yourself. There is no SLA, no on-call rotation but yours, " +
		"and no managed backups. That labour is a real cost this figure does not include."
	CaveatAWSSingle = "This is the shape Gravix moves to at scale. It is roughly 10x the bootstrap " +
		"figure and is the honest number for a team past a few million events a month."
	CaveatAWSMulti = "Multi-region adds a full deployment per region. Choose it for latency or " +
		"residency, not for cost."
	CaveatProvenance = `Line items marked "list_price" are the vendor's published rate on the date ` +
		"shown, not a negotiated rate and not a measurement."
)

// CaveatEstimatedPrices is added whenever any line item is an estimate rather
// than a read list price, which is currently every infrastructure item. Without
// it the provenance caveat would imply the prices had been read off a page.
const CaveatEstimatedPrices = "Infrastructure prices in this model are marked \"estimate\": they " +
	"are the right order of magnitude but none has been read off a vendor's pricing page and " +
	"recorded with a date. Treat every total as indicative."

// priced is one number from prices.yaml, carrying how it was obtained.
type priced struct {
	Value  float64 `yaml:"value"`
	Basis  string  `yaml:"basis"`
	Source string  `yaml:"source"`
}

// Prices is prices.yaml.
type Prices struct {
	Version   int    `yaml:"version"`
	Retrieved string `yaml:"retrieved"`
	Currency  string `yaml:"currency"`

	BootstrapVPS struct {
		InstanceUSDMonth       priced `yaml:"instance_usd_month"`
		IncludedStorageGB      priced `yaml:"included_storage_gb"`
		ExtraStorageUSDGBMonth priced `yaml:"extra_storage_usd_gb_month"`
		EgressUSDGB            priced `yaml:"egress_usd_gb"`
		IncludedEgressGB       priced `yaml:"included_egress_gb"`
	} `yaml:"bootstrap_vps"`

	AWSSingle struct {
		EKSControlPlaneUSDMonth priced `yaml:"eks_control_plane_usd_month"`
		NodeUSDMonth            priced `yaml:"node_usd_month"`
		Nodes                   priced `yaml:"nodes"`
		S3StorageUSDGBMonth     priced `yaml:"s3_storage_usd_gb_month"`
		S3PutUSDPer1000         priced `yaml:"s3_put_usd_per_1000"`
		S3GetUSDPer1000         priced `yaml:"s3_get_usd_per_1000"`
		EgressUSDGB             priced `yaml:"egress_usd_gb"`
		LoadBalancerUSDMonth    priced `yaml:"load_balancer_usd_month"`
	} `yaml:"aws_single"`

	AWSMulti struct {
		Regions                  priced `yaml:"regions"`
		CrossRegionTransferUSDGB priced `yaml:"cross_region_transfer_usd_gb"`
	} `yaml:"aws_multi"`
}

// LoadPrices reads prices.yaml and refuses data too old to publish.
func LoadPrices(path string) (*Prices, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoPrices, err)
	}
	var p Prices
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoPrices, err)
	}
	if err := p.Validate(time.Now().UTC()); err != nil {
		return nil, err
	}
	return &p, nil
}

// Validate refuses price data older than MaxPriceAgeDays.
func (p *Prices) Validate(now time.Time) error {
	if p.Version == 0 {
		return ErrNoPrices
	}
	retrieved, err := time.Parse("2006-01-02", p.Retrieved)
	if err != nil {
		return fmt.Errorf("%w: unparseable retrieved date %q", ErrNoPrices, p.Retrieved)
	}
	if age := int(now.Sub(retrieved).Hours() / 24); age > MaxPriceAgeDays {
		return fmt.Errorf("costmodel: price data retrieved %s is older than %d days; "+
			"re-verify before publishing: %w", p.Retrieved, MaxPriceAgeDays, ErrStalePrices)
	}
	return nil
}

// anyEstimated reports whether any price is an estimate rather than a read rate.
func (p *Prices) anyEstimated() bool {
	for _, v := range p.all() {
		if v.Basis != BasisListPrice {
			return true
		}
	}
	return false
}

func (p *Prices) all() []priced {
	return []priced{
		p.BootstrapVPS.InstanceUSDMonth, p.BootstrapVPS.IncludedStorageGB,
		p.BootstrapVPS.ExtraStorageUSDGBMonth, p.BootstrapVPS.EgressUSDGB,
		p.BootstrapVPS.IncludedEgressGB,
		p.AWSSingle.EKSControlPlaneUSDMonth, p.AWSSingle.NodeUSDMonth, p.AWSSingle.Nodes,
		p.AWSSingle.S3StorageUSDGBMonth, p.AWSSingle.S3PutUSDPer1000,
		p.AWSSingle.S3GetUSDPer1000, p.AWSSingle.EgressUSDGB, p.AWSSingle.LoadBalancerUSDMonth,
		p.AWSMulti.Regions, p.AWSMulti.CrossRegionTransferUSDGB,
	}
}

// StorageGB is the steady-state footprint for these inputs, in gibibytes.
func (in Inputs) StorageGB() float64 {
	months := float64(in.RetentionDays) / 30.0
	bytes := float64(in.EventsPerMonth) * in.BytesPerEvent * months
	return bytes / (1 << 30)
}

// EstimateAll returns one Estimate per deployment, always all three, so a
// caller cannot render the cheapest in isolation.
func EstimateAll(in Inputs, prices *Prices) ([]Estimate, error) {
	if prices == nil {
		return nil, ErrNoPrices
	}
	if err := prices.Validate(time.Now().UTC()); err != nil {
		return nil, err
	}
	if in.BytesPerEvent <= 0 || in.MeasurementSource == "" {
		return nil, fmt.Errorf("costmodel: BytesPerEvent must come from a bench result, "+
			"not a constant: %w", ErrMissingMeasurement)
	}
	if in.EventsPerMonth <= 0 || in.RetentionDays <= 0 {
		return nil, fmt.Errorf("costmodel: EventsPerMonth and RetentionDays must be positive")
	}

	shared := []string{CaveatProvenance}
	if prices.anyEstimated() {
		shared = append(shared, CaveatEstimatedPrices)
	}

	estimates := []Estimate{
		bootstrapEstimate(in, prices, shared),
		awsSingleEstimate(in, prices, shared),
		awsMultiEstimate(in, prices, shared),
	}

	// CaveatAWSSingle says the at-scale figure is "roughly 10x the bootstrap
	// figure". It is not a constant, and at the volumes most readers of that
	// sentence are at it is far higher — the AWS baseline is dominated by fixed
	// costs (control plane, nodes, load balancer) that do not scale down, while
	// the bootstrap VPS is flat until its included storage runs out. Measured
	// against this model the multiple runs from ~44x at a million events a
	// month to ~4x at a billion.
	//
	// The mandatory sentence still prints verbatim, because §5.2 requires it.
	// The computed multiple prints beside it, because a reader who acts on
	// "roughly 10x" at small volume would under-budget by four times over, and
	// that is the direction this caveat exists to protect against.
	estimates = withComputedMultiple(estimates)
	return estimates, nil
}

// withComputedMultiple appends the real bootstrap-to-AWS multiple to the
// at-scale estimates. See SD-021.
func withComputedMultiple(estimates []Estimate) []Estimate {
	var bootstrap float64
	for _, e := range estimates {
		if e.Deployment == DeploymentBootstrapVPS {
			bootstrap = e.TotalUSDMonth
		}
	}
	if bootstrap <= 0 {
		return estimates
	}
	for i, e := range estimates {
		if e.Deployment == DeploymentBootstrapVPS {
			continue
		}
		estimates[i].Caveats = append(e.Caveats, fmt.Sprintf(
			"For these inputs the multiple is %.1fx, not the \"roughly 10x\" the caveat above "+
				"quotes. That sentence describes one point on a curve: the at-scale baseline is "+
				"mostly fixed cost, so the multiple is highest at low volume and falls as volume "+
				"grows. Budget from this figure, not from the multiple.",
			e.TotalUSDMonth/bootstrap))
	}
	return estimates
}

func finish(e Estimate, in Inputs) Estimate {
	var total float64
	for _, li := range e.LineItems {
		total += li.USDMonth
	}
	e.TotalUSDMonth = round2(total)
	millions := float64(in.EventsPerMonth) / 1e6
	if millions > 0 {
		e.USDPerMillionEvents = round2(total / millions)
	}
	return e
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func item(name string, usd float64, p priced, retrieved string) LineItem {
	basis := p.Basis
	if basis == "" {
		basis = BasisEstimate
	}
	return LineItem{
		Name: name, USDMonth: round2(usd),
		Basis: basis, Source: p.Source, Retrieved: retrieved,
	}
}

func bootstrapEstimate(in Inputs, p *Prices, shared []string) Estimate {
	vps := p.BootstrapVPS
	storage := in.StorageGB()
	extra := math.Max(0, storage-vps.IncludedStorageGB.Value)

	// Dashboard traffic, modelled crudely and labelled as such: each user pulls
	// roughly 5 MB of chart data a day.
	egressGB := float64(in.DashboardUsers) * 5 * 30 / 1024
	billableEgress := math.Max(0, egressGB-vps.IncludedEgressGB.Value)

	e := Estimate{
		Deployment: DeploymentBootstrapVPS,
		LineItems: []LineItem{
			item("VPS instance", vps.InstanceUSDMonth.Value, vps.InstanceUSDMonth, p.Retrieved),
			item(fmt.Sprintf("Extra block storage (%.1f GiB over the %.0f GiB included)", extra, vps.IncludedStorageGB.Value),
				extra*vps.ExtraStorageUSDGBMonth.Value, vps.ExtraStorageUSDGBMonth, p.Retrieved),
			item("Egress", billableEgress*vps.EgressUSDGB.Value, vps.EgressUSDGB, p.Retrieved),
			{
				Name:     fmt.Sprintf("Storage footprint: %.1f GiB at %.1f bytes/event", storage, in.BytesPerEvent),
				USDMonth: 0, Basis: BasisMeasured, Source: in.MeasurementSource,
			},
		},
		Caveats: append([]string{CaveatBootstrap}, shared...),
	}
	return finish(e, in)
}

func awsSingleEstimate(in Inputs, p *Prices, shared []string) Estimate {
	aws := p.AWSSingle
	storage := in.StorageGB()

	// Ingestion rotates a buffer file per minute per service; the rollup and
	// compaction read and rewrite them. Modelled, not measured, and labelled.
	putsPerMonth := float64(in.Services) * 60 * 24 * 30 * 3
	getsPerMonth := putsPerMonth * 4
	egressGB := float64(in.DashboardUsers) * 5 * 30 / 1024

	e := Estimate{
		Deployment: DeploymentAWSSingle,
		LineItems: []LineItem{
			item("EKS control plane", aws.EKSControlPlaneUSDMonth.Value, aws.EKSControlPlaneUSDMonth, p.Retrieved),
			item(fmt.Sprintf("Nodes (%.0f)", aws.Nodes.Value), aws.Nodes.Value*aws.NodeUSDMonth.Value, aws.NodeUSDMonth, p.Retrieved),
			item("Load balancer", aws.LoadBalancerUSDMonth.Value, aws.LoadBalancerUSDMonth, p.Retrieved),
			item(fmt.Sprintf("S3 storage (%.1f GiB)", storage), storage*aws.S3StorageUSDGBMonth.Value, aws.S3StorageUSDGBMonth, p.Retrieved),
			item("S3 PUT requests", putsPerMonth/1000*aws.S3PutUSDPer1000.Value, aws.S3PutUSDPer1000, p.Retrieved),
			item("S3 GET requests", getsPerMonth/1000*aws.S3GetUSDPer1000.Value, aws.S3GetUSDPer1000, p.Retrieved),
			item("Egress", egressGB*aws.EgressUSDGB.Value, aws.EgressUSDGB, p.Retrieved),
			{
				Name:     fmt.Sprintf("Storage footprint: %.1f GiB at %.1f bytes/event", storage, in.BytesPerEvent),
				USDMonth: 0, Basis: BasisMeasured, Source: in.MeasurementSource,
			},
		},
		Caveats: append([]string{CaveatAWSSingle}, shared...),
	}
	return finish(e, in)
}

func awsMultiEstimate(in Inputs, p *Prices, shared []string) Estimate {
	regions := p.AWSMulti.Regions.Value
	single := awsSingleEstimate(in, p, shared)
	storage := in.StorageGB()

	items := make([]LineItem, 0, len(single.LineItems)+1)
	for _, li := range single.LineItems {
		if li.Basis == BasisMeasured {
			items = append(items, li)
			continue
		}
		li.Name = fmt.Sprintf("%s x%.0f regions", li.Name, regions)
		li.USDMonth = round2(li.USDMonth * regions)
		items = append(items, li)
	}
	items = append(items, item(
		fmt.Sprintf("Cross-region replication (%.1f GiB to %.0f regions)", storage, regions-1),
		storage*(regions-1)*p.AWSMulti.CrossRegionTransferUSDGB.Value,
		p.AWSMulti.CrossRegionTransferUSDGB, p.Retrieved))

	e := Estimate{
		Deployment: DeploymentAWSMulti,
		LineItems:  items,
		Caveats:    append([]string{CaveatAWSMulti}, shared...),
	}
	return finish(e, in)
}
