// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lgreene/gravix-dashboards/pkg/custody"
)

// GRVX-1503. The succession plan's eleven criteria.
//
// Two of them are about what must never appear in these files, and those run on
// every pull request — they are the only part of this spec a diff can break.
// The rest are about whether the plan describes the project honestly, which is
// harder to test and more likely to rot, because the pressure is always toward
// a page that reads better than the situation.

func registerAssets(t *testing.T) []custody.Asset {
	t.Helper()
	assets, err := custody.LoadRegister(filepath.Join("..", "..", filepath.FromSlash(custody.RegisterPath)))
	if err != nil {
		t.Fatalf("load %s: %v", custody.RegisterPath, err)
	}
	return assets
}

func successionFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range []string{custody.RegisterPath, custody.DrillPath} {
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		out[rel] = string(raw)
	}
	return out
}

func messages(f []custody.Finding) string {
	var b strings.Builder
	for _, x := range f {
		b.WriteString(x.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// asset is one row, built inline for the fixtures below.
func asset(name string, primary, secondary, verified string) custody.Asset {
	return custody.Asset{
		Name:        name,
		Location:    "somewhere real",
		Primary:     primary,
		Secondary:   secondary,
		Recovery:    "the provider's documented account recovery",
		Verified:    verified,
		BlastRadius: "**total** — everything",
	}
}

// -------------------------------------------------------------------- AC-1 --

// TestNoSecretsInSuccessionPlan — the one thing that must never happen.
//
// A plan about where credentials live is the single most likely document to end
// up containing one, because every sentence in it is adjacent to the thought.
func TestNoSecretsInSuccessionPlan(t *testing.T) {
	for name, body := range successionFiles(t) {
		if found := custody.ScanSecrets(name, body); len(found) > 0 {
			t.Errorf("%s contains a credential:\n%s", name, messages(found))
		}
	}

	// And the scanner is not inert. A guard that has never refused anything is
	// indistinguishable from a guard that cannot.
	for _, planted := range []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"token: ghp_0123456789abcdefghijklmnopqrstuvwxyz",
		"AKIAIOSFODNN7EXAMPLE",
		"npm_0123456789abcdefghijklmnopqrstuvwxyz",
		"the recovery code: 4821-9930-1174",
		"passphrase = correct-horse-battery-staple",
	} {
		if found := custody.ScanSecrets("fixture.md", "custody notes\n"+planted+"\n"); len(found) == 0 {
			t.Errorf("the scanner accepted %q", planted)
		}
	}
}

// -------------------------------------------------------------------- AC-2 --

// TestTwoCustodiansPerAsset — every asset needs two people who can recover it.
//
// This project has one, on every asset, and no amount of implementation changes
// that. GRVX-1503 §10 is explicit about the case: report it as an open risk,
// escalate, and do not pretend it has two. So this checks the two things that
// are actually in an implementer's gift — that the rule is enforced rather than
// described, and that every shortfall is written where somebody deciding
// whether to depend on Gravix would find it. See SD-048.
func TestTwoCustodiansPerAsset(t *testing.T) {
	assets := registerAssets(t)
	rep := custody.Audit(assets, time.Now().UTC())

	short := map[string]bool{}
	for _, a := range assets {
		if a.Provisioned() && a.Custodians() < custody.RequiredCustodians {
			short[a.Name] = true
		}
	}

	// Enforced, not described: each shortfall is a FATAL finding, so the audit
	// fails rather than noting it.
	for name := range short {
		fatal := false
		for _, f := range rep.Findings {
			if f.Asset == name && f.Fatal {
				fatal = true
			}
		}
		if !fatal {
			t.Errorf("%s has fewer than %d custodians and the audit does not fail on it",
				name, custody.RequiredCustodians)
		}
	}
	if len(short) > 0 && rep.OK() {
		t.Error("assets are below the custodian threshold and the audit still passes")
	}

	// Recorded where it will be read, per §10.
	if len(short) > 0 {
		maintainers := strings.Join(strings.Fields(repoFile(t, "MAINTAINERS.md")), " ")
		if !strings.Contains(maintainers, "no asset of this project's identity has a second custodian") {
			t.Error("MAINTAINERS.md does not record the custody shortfall as an open risk")
		}
		decisions := strings.Join(strings.Fields(repoFile(t, "docs", "oss", "open-decisions.md")), " ")
		if !strings.Contains(decisions, "a second custodian") {
			t.Error("open-decisions.md does not escalate the custody shortfall to a person")
		}
	}

	// And two custodians genuinely satisfies it, so the rule is a threshold
	// rather than an assertion that nothing ever passes.
	ok := custody.Audit([]custody.Asset{
		asset("Example", "chair — @ann", "deputy — @bob", time.Now().UTC().Format("2006-01-02")),
	}, time.Now().UTC())
	if !ok.OK() {
		t.Errorf("an asset with two recently verified custodians failed:\n%s", messages(ok.Findings))
	}
}

// -------------------------------------------------------------------- AC-3 --

// TestSingleCustodianFails — and the failure names the asset and what breaks,
// because "custody: failed" tells a reader nothing about whether to care.
func TestSingleCustodianFails(t *testing.T) {
	rep := custody.Audit([]custody.Asset{
		asset("Example", "chair — @ann", "— none", time.Now().UTC().Format("2006-01-02")),
	}, time.Now().UTC())

	if rep.OK() {
		t.Fatal("a single-custodian asset passed the audit")
	}
	got := messages(rep.Findings)
	if !strings.Contains(got, "Example has 1 custodian(s)") {
		t.Errorf("the finding does not name the asset and the count:\n%s", got)
	}
	if !strings.Contains(got, "blast radius:") {
		t.Errorf("the finding does not say what breaks:\n%s", got)
	}
}

// -------------------------------------------------------------------- AC-4 --

// TestStaleCustodyFails — a recovery procedure nobody has executed is a
// hypothesis, and one nobody has executed in over a year is an old hypothesis.
func TestStaleCustodyFails(t *testing.T) {
	now := time.Now().UTC()
	stale := now.AddDate(0, 0, -400).Format("2006-01-02")
	fresh := now.AddDate(0, 0, -200).Format("2006-01-02")

	rep := custody.Audit([]custody.Asset{
		asset("Example", "chair — @ann", "deputy — @bob", stale),
	}, now)
	if rep.OK() {
		t.Error("custody unverified for 400 days passed the audit")
	}
	if got := messages(rep.Findings); !strings.Contains(got, "run the annual drill") {
		t.Errorf("the finding does not say what to do about it:\n%s", got)
	}

	if rep := custody.Audit([]custody.Asset{
		asset("Example", "chair — @ann", "deputy — @bob", fresh),
	}, now); !rep.OK() {
		t.Errorf("custody verified 200 days ago failed:\n%s", messages(rep.Findings))
	}

	// A missing date is not a pass. It is the same claim with the evidence
	// removed, which is the direction this would rot in.
	if rep := custody.Audit([]custody.Asset{
		asset("Example", "chair — @ann", "deputy — @bob", "—"),
	}, now); rep.OK() {
		t.Error("an asset with no verification date passed; custody was asserted, not demonstrated")
	}
}

// -------------------------------------------------------------------- AC-5 --

// TestBlastRadiusRecorded — every asset says what breaks if it is lost.
func TestBlastRadiusRecorded(t *testing.T) {
	for _, a := range registerAssets(t) {
		if strings.TrimSpace(a.BlastRadius) == "" {
			t.Errorf("%s records no blast radius", a.Name)
		}
		if len(strings.Fields(a.BlastRadius)) < 3 {
			t.Errorf("%s's blast radius is %q, which does not describe a consequence",
				a.Name, a.BlastRadius)
		}
	}

	if rep := custody.Audit([]custody.Asset{{
		Name: "Example", Location: "somewhere real",
		Primary: "chair — @ann", Secondary: "deputy — @bob",
		Verified: time.Now().UTC().Format("2006-01-02"),
	}}, time.Now().UTC()); rep.OK() {
		t.Error("an asset with no blast radius passed the audit")
	}
}

// -------------------------------------------------------------------- AC-6 --

// TestOrgIdentifiedAsHighestRisk — exactly one asset carries everything, the
// register says which, and the plan explains why rather than leaving the
// concentration to be inferred from twelve rows that all look alike.
func TestOrgIdentifiedAsHighestRisk(t *testing.T) {
	assets := registerAssets(t)

	var total []string
	for _, a := range assets {
		if a.Total() {
			total = append(total, a.Name)
		}
	}
	if len(total) != 1 {
		t.Fatalf("assets marked **total** blast radius = %v, want exactly one", total)
	}
	if !strings.Contains(strings.ToLower(total[0]), "github") {
		t.Errorf("the highest-blast-radius asset is %q; GRVX-1503 §5.3 says keyless signing "+
			"concentrates everything into the GitHub account", total[0])
	}
	if assets[0].Name != total[0] {
		t.Errorf("the register opens with %q rather than the asset that carries everything (%q)",
			assets[0].Name, total[0])
	}

	plan := strings.Join(strings.Fields(successionFiles(t)[custody.RegisterPath]), " ")
	for _, required := range []string{
		"The single highest-blast-radius asset is the **GitHub account**",
		"there is no private key to escrow, split or lose",
	} {
		if !strings.Contains(plan, strings.Join(strings.Fields(required), " ")) {
			t.Errorf("the plan does not explain the concentration; missing: %q", required)
		}
	}
}

// -------------------------------------------------------------------- AC-7 --

// TestTriggerConditionsDefined — all four, each with what confirms it.
//
// The confirmation is the part that matters. "90 days unreachable" without a
// named attestation is a mechanism for taking a project from somebody who is
// on holiday.
func TestTriggerConditionsDefined(t *testing.T) {
	plan := strings.Join(strings.Fields(successionFiles(t)[custody.RegisterPath]), " ")

	for _, trigger := range []struct{ name, confirmation string }{
		{"Planned handover", "The departing custodian confirms"},
		{"90 days unreachable", "Two council members attest publicly"},
		{"Incapacity or death", "A council decision, publicly minuted"},
		{"Compromise", "`security-engineer` role declares it"},
	} {
		if !strings.Contains(plan, trigger.name) {
			t.Errorf("trigger %q is not defined", trigger.name)
			continue
		}
		if !strings.Contains(plan, strings.Join(strings.Fields(trigger.confirmation), " ")) {
			t.Errorf("trigger %q does not say what confirms it (looked for %q)",
				trigger.name, trigger.confirmation)
		}
	}

	// The long threshold is deliberate, and the plan says why. A number with no
	// reasoning beside it is a number the next person shortens.
	if !strings.Contains(plan, "deliberately long and deliberately public") {
		t.Error("the plan does not explain why the unreachable threshold is 90 days")
	}
}

// -------------------------------------------------------------------- AC-8 --

// TestCustodyStatementVerbatim — GRVX-1503 §5.2 gives the statement to publish
// word for word.
//
// It is also, today, false about this project — so the plan publishes it as the
// rule it is audited against and contradicts it in the next sentence. Both
// halves are checked here: a later edit that quietly drops the contradiction
// would turn an honest page into a claim.
func TestCustodyStatementVerbatim(t *testing.T) {
	body := successionFiles(t)[custody.RegisterPath]

	statement := `Every asset here has at least two people who can recover it.

Not two people who know about it — two people who have independently
demonstrated, in the last twelve months, that they can actually get in. We test
this annually, because a recovery procedure nobody has executed is a hypothesis.`

	if !strings.Contains(body, statement) {
		t.Error("the §5.2 statement does not appear verbatim in " + custody.RegisterPath)
	}

	flat := strings.Join(strings.Fields(body), " ")
	if !strings.Contains(flat, "**Today, not one asset meets that rule.**") {
		t.Error("the plan publishes the rule without saying the project does not meet it, " +
			"which turns a rule into a claim")
	}
}

// -------------------------------------------------------------------- AC-9 --

// TestFirstDrillRecorded — including the failure, which is the whole result.
func TestFirstDrillRecorded(t *testing.T) {
	drill := successionFiles(t)[custody.DrillPath]
	flat := strings.Join(strings.Fields(drill), " ")

	if !strings.Contains(drill, "## 2026") {
		t.Fatal("no 2026 drill section")
	}
	if !strings.Contains(flat, "**Status: NOT COMPLETED.**") {
		t.Error("the 2026 drill does not state its outcome")
	}
	if !strings.Contains(flat, "no secondary custodian exists") {
		t.Error("the drill record does not say why it could not be run")
	}
	if !strings.Contains(flat, "A published failed drill is more reassuring than an unpublished successful one") {
		t.Error("the drill page does not say why failures are published")
	}

	// "Nobody attempted it" has to count as a failure, or every future year can
	// be filed under not-applicable.
	if !strings.Contains(flat, "The last one is still a failure") {
		t.Error("the procedure does not treat an unattempted drill as a failure")
	}

	// Every provisioned asset appears in the year's table. A drill that silently
	// omits a row is indistinguishable from one that passed it.
	for _, a := range registerAssets(t) {
		if a.Provisioned() && !strings.Contains(drill, a.Name) {
			t.Errorf("%s is provisioned but absent from the 2026 drill record", a.Name)
		}
	}
}

// ------------------------------------------------------------------- AC-10 --

// TestNoPersonalDetailsPublished — a custodian agreed to hold an account, not
// to be reachable at home. Role addresses are exactly what the plan should
// name; a personal mailbox is not.
func TestNoPersonalDetailsPublished(t *testing.T) {
	for name, body := range successionFiles(t) {
		if found := custody.ScanPersonalDetails(name, body); len(found) > 0 {
			t.Errorf("%s publishes a personal detail:\n%s", name, messages(found))
		}
	}

	for _, planted := range []string{
		"reachable on +44 7700 900123",
		"post to 42 Sycamore Street",
		"write to someone.personal@gmail.com",
	} {
		if found := custody.ScanPersonalDetails("fixture.md", planted); len(found) == 0 {
			t.Errorf("the scanner accepted %q", planted)
		}
	}

	// And the role addresses the plan is supposed to name still pass.
	if found := custody.ScanPersonalDetails("fixture.md",
		"security@gravix.io, conduct@gravix.io and trademark@gravix.io"); len(found) > 0 {
		t.Errorf("a role address was refused:\n%s", messages(found))
	}
}

// ------------------------------------------------------------------- AC-11 --

// TestNoCompanyDependency — nothing here needs a legal entity to keep existing.
//
// A company would be a tidier home for some of this and a new single point of
// failure with a registrar, a bank and a jurisdiction attached. The plan says
// which, and points at the alternative that is actually built to outlive its
// founders.
func TestNoCompanyDependency(t *testing.T) {
	plan := successionFiles(t)[custody.RegisterPath]
	flat := strings.Join(strings.Fields(plan), " ")

	if !strings.Contains(flat, "No legal entity holds any asset in this register") {
		t.Error("the plan does not state that no company is load-bearing")
	}
	if !strings.Contains(flat, "GRVX-1506") {
		t.Error("the plan does not point at the foundation evaluation, which is the version " +
			"of this question worth asking")
	}

	// No custodian or recovery path routes through a corporate entity, which is
	// how a dependency on one gets in: not as a policy, as a row.
	for _, a := range registerAssets(t) {
		for _, cell := range []string{a.Primary, a.Secondary, a.Recovery} {
			for _, marker := range []string{" Ltd", " Inc", " LLC", " GmbH", " Limited", " Pty"} {
				if strings.Contains(cell, marker) {
					t.Errorf("%s routes custody through a legal entity (%q in %q)", a.Name, marker, cell)
				}
			}
		}
	}
}
