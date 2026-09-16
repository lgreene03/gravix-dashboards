// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

// Cost model for dashboards/tco.html, mirroring pkg/costmodel.
//
// The two implementations exist because the page has no build step and cannot
// call Go, and they are held together by a shared fixture set: tco.test.js
// checks every case against the totals pkg/costmodel produced for the same
// inputs. A calculator that disagrees with the model behind it is worse than
// having neither.
//
// Like the Go side, there is deliberately no way to compute one deployment.
// estimateAll returns all three, because showing the bootstrap figure alone is
// the half-truth the thesis exists to refuse.

export const DEPLOYMENT_BOOTSTRAP_VPS = 'bootstrap_vps';
export const DEPLOYMENT_AWS_SINGLE = 'aws_single';
export const DEPLOYMENT_AWS_MULTI = 'aws_multi';

export const BASIS_MEASURED = 'measured';
export const BASIS_LIST_PRICE = 'list_price';
export const BASIS_ESTIMATE = 'estimate';

export const MAX_PRICE_AGE_DAYS = 90;

export const CAVEAT_BOOTSTRAP =
    'You operate this yourself. There is no SLA, no on-call rotation but yours, ' +
    'and no managed backups. That labour is a real cost this figure does not include.';
export const CAVEAT_AWS_SINGLE =
    'This is the shape Gravix moves to at scale. It is roughly 10x the bootstrap ' +
    'figure and is the honest number for a team past a few million events a month.';
export const CAVEAT_AWS_MULTI =
    'Multi-region adds a full deployment per region. Choose it for latency or ' +
    'residency, not for cost.';
export const CAVEAT_PROVENANCE =
    'Line items marked "list_price" are the vendor\'s published rate on the date ' +
    'shown, not a negotiated rate and not a measurement.';
export const CAVEAT_ESTIMATED_PRICES =
    'Infrastructure prices in this model are marked "estimate": they are the right ' +
    'order of magnitude but none has been read off a vendor\'s pricing page and ' +
    'recorded with a date. Treat every total as indicative.';

function round2(v) { return Math.round(v * 100) / 100; }

/** storageGB is the steady-state footprint for these inputs, in gibibytes. */
export function storageGB(inputs) {
    const months = inputs.retentionDays / 30.0;
    const bytes = inputs.eventsPerMonth * inputs.bytesPerEvent * months;
    return bytes / (1024 ** 3);
}

function item(name, usd, priced, retrieved) {
    return {
        name,
        usdMonth: round2(usd),
        basis: priced.basis || BASIS_ESTIMATE,
        source: priced.source || '',
        retrieved,
    };
}

function measuredItem(inputs, storage) {
    return {
        name: `Storage footprint: ${storage.toFixed(1)} GiB at ${inputs.bytesPerEvent.toFixed(1)} bytes/event`,
        usdMonth: 0,
        basis: BASIS_MEASURED,
        source: inputs.measurementSource,
        retrieved: '',
    };
}

function finish(estimate, inputs) {
    const total = estimate.lineItems.reduce((sum, li) => sum + li.usdMonth, 0);
    estimate.totalUSDMonth = round2(total);
    const millions = inputs.eventsPerMonth / 1e6;
    estimate.usdPerMillionEvents = millions > 0 ? round2(total / millions) : 0;
    return estimate;
}

function anyEstimated(prices) {
    const all = [
        prices.bootstrap_vps.instance_usd_month, prices.bootstrap_vps.included_storage_gb,
        prices.bootstrap_vps.extra_storage_usd_gb_month, prices.bootstrap_vps.egress_usd_gb,
        prices.bootstrap_vps.included_egress_gb,
        prices.aws_single.eks_control_plane_usd_month, prices.aws_single.node_usd_month,
        prices.aws_single.nodes, prices.aws_single.s3_storage_usd_gb_month,
        prices.aws_single.s3_put_usd_per_1000, prices.aws_single.s3_get_usd_per_1000,
        prices.aws_single.egress_usd_gb, prices.aws_single.load_balancer_usd_month,
        prices.aws_multi.regions, prices.aws_multi.cross_region_transfer_usd_gb,
    ];
    return all.some(p => p.basis !== BASIS_LIST_PRICE);
}

function bootstrapEstimate(inputs, prices, shared) {
    const vps = prices.bootstrap_vps;
    const storage = storageGB(inputs);
    const extra = Math.max(0, storage - vps.included_storage_gb.value);
    const egressGB = inputs.dashboardUsers * 5 * 30 / 1024;
    const billableEgress = Math.max(0, egressGB - vps.included_egress_gb.value);

    return finish({
        deployment: DEPLOYMENT_BOOTSTRAP_VPS,
        lineItems: [
            item('VPS instance', vps.instance_usd_month.value, vps.instance_usd_month, prices.retrieved),
            item(`Extra block storage (${extra.toFixed(1)} GiB over the ${vps.included_storage_gb.value.toFixed(0)} GiB included)`,
                extra * vps.extra_storage_usd_gb_month.value, vps.extra_storage_usd_gb_month, prices.retrieved),
            item('Egress', billableEgress * vps.egress_usd_gb.value, vps.egress_usd_gb, prices.retrieved),
            measuredItem(inputs, storage),
        ],
        caveats: [CAVEAT_BOOTSTRAP, ...shared],
    }, inputs);
}

function awsSingleEstimate(inputs, prices, shared) {
    const aws = prices.aws_single;
    const storage = storageGB(inputs);
    const putsPerMonth = inputs.services * 60 * 24 * 30 * 3;
    const getsPerMonth = putsPerMonth * 4;
    const egressGB = inputs.dashboardUsers * 5 * 30 / 1024;

    return finish({
        deployment: DEPLOYMENT_AWS_SINGLE,
        lineItems: [
            item('EKS control plane', aws.eks_control_plane_usd_month.value, aws.eks_control_plane_usd_month, prices.retrieved),
            item(`Nodes (${aws.nodes.value.toFixed(0)})`, aws.nodes.value * aws.node_usd_month.value, aws.node_usd_month, prices.retrieved),
            item('Load balancer', aws.load_balancer_usd_month.value, aws.load_balancer_usd_month, prices.retrieved),
            item(`S3 storage (${storage.toFixed(1)} GiB)`, storage * aws.s3_storage_usd_gb_month.value, aws.s3_storage_usd_gb_month, prices.retrieved),
            item('S3 PUT requests', putsPerMonth / 1000 * aws.s3_put_usd_per_1000.value, aws.s3_put_usd_per_1000, prices.retrieved),
            item('S3 GET requests', getsPerMonth / 1000 * aws.s3_get_usd_per_1000.value, aws.s3_get_usd_per_1000, prices.retrieved),
            item('Egress', egressGB * aws.egress_usd_gb.value, aws.egress_usd_gb, prices.retrieved),
            measuredItem(inputs, storage),
        ],
        caveats: [CAVEAT_AWS_SINGLE, ...shared],
    }, inputs);
}

function awsMultiEstimate(inputs, prices, shared) {
    const regions = prices.aws_multi.regions.value;
    const single = awsSingleEstimate(inputs, prices, shared);
    const storage = storageGB(inputs);

    const items = single.lineItems.map(li => {
        if (li.basis === BASIS_MEASURED) return li;
        return {
            ...li,
            name: `${li.name} x${regions.toFixed(0)} regions`,
            usdMonth: round2(li.usdMonth * regions),
        };
    });
    items.push(item(
        `Cross-region replication (${storage.toFixed(1)} GiB to ${(regions - 1).toFixed(0)} regions)`,
        storage * (regions - 1) * prices.aws_multi.cross_region_transfer_usd_gb.value,
        prices.aws_multi.cross_region_transfer_usd_gb, prices.retrieved));

    return finish({
        deployment: DEPLOYMENT_AWS_MULTI,
        lineItems: items,
        caveats: [CAVEAT_AWS_MULTI, ...shared],
    }, inputs);
}

/** withComputedMultiple appends the real bootstrap-to-AWS multiple. See SD-021. */
function withComputedMultiple(estimates) {
    const bootstrap = estimates.find(e => e.deployment === DEPLOYMENT_BOOTSTRAP_VPS);
    if (!bootstrap || bootstrap.totalUSDMonth <= 0) return estimates;
    for (const e of estimates) {
        if (e.deployment === DEPLOYMENT_BOOTSTRAP_VPS) continue;
        const multiple = e.totalUSDMonth / bootstrap.totalUSDMonth;
        e.caveats = [...e.caveats, `For these inputs the multiple is ${multiple.toFixed(1)}x, not the ` +
            `"roughly 10x" the caveat above quotes. That sentence describes one point on a curve: ` +
            `the at-scale baseline is mostly fixed cost, so the multiple is highest at low volume ` +
            `and falls as volume grows. Budget from this figure, not from the multiple.`];
    }
    return estimates;
}

/**
 * estimateAll returns one estimate per deployment, always all three.
 * Throws when BytesPerEvent did not come from a bench result, when prices are
 * missing or stale, or when the workload makes no sense.
 */
export function estimateAll(inputs, prices, now = new Date()) {
    if (!prices) throw new Error('costmodel: no price data');
    validatePrices(prices, now);
    if (!(inputs.bytesPerEvent > 0) || !inputs.measurementSource) {
        throw new Error('costmodel: BytesPerEvent must come from a bench result, not a constant');
    }
    if (!(inputs.eventsPerMonth > 0) || !(inputs.retentionDays > 0)) {
        throw new Error('costmodel: EventsPerMonth and RetentionDays must be positive');
    }

    const shared = [CAVEAT_PROVENANCE];
    if (anyEstimated(prices)) shared.push(CAVEAT_ESTIMATED_PRICES);

    return withComputedMultiple([
        bootstrapEstimate(inputs, prices, shared),
        awsSingleEstimate(inputs, prices, shared),
        awsMultiEstimate(inputs, prices, shared),
    ]);
}

/** validatePrices refuses price data older than MAX_PRICE_AGE_DAYS. */
export function validatePrices(prices, now = new Date()) {
    if (!prices || !prices.version) throw new Error('costmodel: no price data');
    const retrieved = Date.parse(prices.retrieved + 'T00:00:00Z');
    if (Number.isNaN(retrieved)) {
        throw new Error(`costmodel: unparseable retrieved date "${prices.retrieved}"`);
    }
    const ageDays = Math.floor((now.getTime() - retrieved) / 86400000);
    if (ageDays > MAX_PRICE_AGE_DAYS) {
        throw new Error(`costmodel: price data retrieved ${prices.retrieved} is older than ` +
            `${MAX_PRICE_AGE_DAYS} days; re-verify before publishing`);
    }
}
