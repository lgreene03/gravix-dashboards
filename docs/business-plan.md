# Gravix Business Plan & Cost Model

*Internal planning document — March 2026*

---

## 1. Executive Summary

**Gravix** is a managed HTTP service health monitoring platform for engineering teams that need basic observability (error rates, latency percentiles, throughput) without the cost and complexity of full-stack APM platforms.

**Target market**: Small-to-mid engineering teams (5-50 engineers) running HTTP services who are either:
- Paying too much for Datadog/New Relic and only using 10% of the features
- Running self-hosted Prometheus/Grafana and drowning in operational overhead
- Using nothing and flying blind

**Value proposition**: "See your service health in 5 minutes. No agents. No PromQL. 80% cheaper than Datadog."

**Business model**: SaaS managed service — customers send HTTP request events via a simple API, Gravix handles storage, aggregation, and dashboards.

---

## 2. Market Positioning

### Competitive Landscape

| Platform | Pricing Model | Typical Cost (10 hosts) | Strengths | Weaknesses |
|----------|--------------|------------------------|-----------|------------|
| **Datadog** | Per host ($15-23/mo infra + $31-40/mo APM) | $460-630/mo | Full APM, traces, logs, 600+ integrations | Expensive, complex pricing, bill shock |
| **New Relic** | Per GB ingested ($0.30/GB after free tier) | $50-200/mo | Generous free tier (100GB/mo), full platform | Costs spike with volume, complex |
| **Grafana Cloud** | Usage-based (free tier + $8/host) | $80-160/mo | Open ecosystem, Prometheus-native | Requires PromQL knowledge, operational overhead |
| **Self-hosted Prom/Grafana** | Infrastructure only | $50-100/mo (EC2) | Free software, full control | High ops burden, scaling is painful |
| **Gravix** | Per events/month | **$29-249/mo** | Simple, cheap, no agents, no lock-in | No traces, no logs, no real-time |

### Where Gravix Wins

Gravix targets the **"good enough" observability** segment — teams that need answers to three questions:
1. **Is my service healthy?** (error rate, status codes)
2. **Is it fast enough?** (p50/p95/p99 latency)
3. **What changed?** (deploy events, traffic shifts)

Teams that need distributed tracing, log search, or real-time alerting are **not** our customers. We don't compete with Datadog — we compete with "nothing" and "DIY Prometheus."

### Anti-Positioning (What We Don't Do)

These constraints are architectural, not roadmap items. They keep the product simple and the costs low:

- No distributed tracing or span collection
- No log aggregation or text search
- No host-level agents or sidecars
- No real-time dashboards (5-15 min visibility latency is expected)
- No high-cardinality dimensions (user_id, request_id)
- No custom query language — standard SQL only

---

## 3. Infrastructure Cost Model

All costs based on AWS us-east-1 pricing. Gravix runs on Kubernetes (EKS) with S3 for storage.

### AWS Component Pricing (current rates)

| Component | Unit | Price |
|-----------|------|-------|
| EKS control plane | per cluster/month | $73.00 |
| EC2 m5.large (2 vCPU, 8 GB) | per instance/month (on-demand) | $69.12 |
| EC2 m5.xlarge (4 vCPU, 16 GB) | per instance/month (on-demand) | $138.24 |
| EC2 t3.medium (2 vCPU, 4 GB) | per instance/month (on-demand) | $30.37 |
| S3 Standard storage | per GB/month | $0.023 |
| S3 PUT requests | per 1,000 requests | $0.005 |
| S3 GET requests | per 1,000 requests | $0.0004 |
| ALB (Application Load Balancer) | per hour + LCU | ~$22/month base |
| EBS gp3 | per GB/month | $0.08 |
| Data transfer (out to internet) | per GB | $0.09 |

### Resource Requirements by Tier

Based on actual Helm chart values (`values-prod.yaml`):

**Per-Customer Resource Footprint (shared infrastructure):**

| Component | CPU Request | Memory Request | CPU Limit | Memory Limit | Notes |
|-----------|-------------|---------------|-----------|-------------|-------|
| Ingestion (shared, 2 replicas) | 500m | 512 Mi | 2 | 2 Gi | Scales via HPA |
| Trino (shared, 1 replica) | 250m | 1 Gi | 1 | 3 Gi | Memory-intensive query engine |
| Cube.js (shared, 1 replica) | 100m | 256 Mi | 500m | 512 Mi | Semantic layer |
| Dashboard (shared, 1 replica) | 50m | 32 Mi | 100m | 64 Mi | Static nginx |
| Rollup CronJobs (4 jobs) | 350m | 448 Mi | 1.75 | 1.79 Gi | Run periodically, not always |
| **Total (steady-state)** | **~1.1 CPU** | **~2.4 Gi** | **~5.35 CPU** | **~8.5 Gi** | Excluding CronJob burst |

### Infrastructure Tiers

| Tier | Customers | Total QPS | Nodes | Monthly Infra Cost | Per-Customer Cost |
|------|-----------|-----------|-------|--------------------|--------------------|
| **Seed** | 1-5 | 5-25 | 1x m5.large | $164 | $33-164 |
| **Growth** | 5-20 | 25-100 | 2x m5.large | $233 | $12-47 |
| **Scale** | 20-50 | 100-500 | 1x m5.xlarge + 1x m5.large | $302 | $6-15 |
| **Expand** | 50-100 | 500-2,000 | 2x m5.xlarge + 1x m5.large | $441 | $4-9 |

**Cost breakdown for Growth tier (target steady-state):**

| Component | Monthly Cost |
|-----------|-------------|
| EKS control plane | $73.00 |
| 2x m5.large (compute) | $138.24 |
| ALB (load balancer) | $22.00 |
| S3 storage (~60 GB for 20 customers) | $1.38 |
| S3 API calls (~260M PUT/GET) | $3.50 |
| EBS (2x 1Gi gp3 for ingestion PVCs) | $0.16 |
| Data transfer (dashboard, ~10 GB/mo) | $0.90 |
| **Total** | **~$239/mo** |

### Storage Cost per Customer

At 5 QPS average per customer (typical small-to-mid team):

| Data Type | Daily Volume | 30-Day Retention | Monthly S3 Cost |
|-----------|-------------|-----------------|-----------------|
| Raw JSONL (request facts) | ~86 MB | ~2.6 GB | $0.06 |
| Warehouse Parquet (rollup output) | ~5 MB | ~150 MB | $0.003 |
| Service events | ~1 MB | ~30 MB | $0.001 |
| S3 PUT/GET API calls | 432K PUT + ~50K GET/day | ~14.5M calls/mo | $0.08 |
| **Total per customer** | | **~2.8 GB** | **~$0.14/mo** |

Storage is essentially free. Even at 100 QPS, storage costs are under $3/mo per customer.

### Scaling Bottleneck: Trino

Trino is the most expensive component (3 GB memory minimum) and does not horizontally scale in the current architecture. Options as customer count grows:

| Approach | Pros | Cons | Cost Impact |
|----------|------|------|------------|
| **Shared single Trino** (current) | Simple, cheap | Query contention at scale | Included in node cost |
| **Trino per tenant group** (5-10 tenants each) | Better isolation | 3 GB memory per group | +$70/mo per group |
| **AWS Athena (serverless)** | Zero management, auto-scales | Per-query pricing, higher latency | ~$5/TB scanned |
| **DuckDB embedded** | No separate process, fast | Single-node only, no multi-tenant | Near-zero |

**Recommendation**: Start with shared Trino. Migrate shared tier to DuckDB at Phase 5 (Month 10-14). Retain Trino/Athena for Enterprise dedicated compute.

### Cost Evolution by Phase

As the product evolves through the 6-phase roadmap (see [PRODUCT_ROADMAP.md](../PRODUCT_ROADMAP.md)), infrastructure costs change:

| Phase | Month | New AWS Services | Additional Cost | Cumulative Fixed Infra |
|-------|-------|-----------------|-----------------|----------------------|
| **1: SaaS Foundation** | 1-2 | RDS db.t4g.micro, SES, Route53 | +$13/mo | $177/mo |
| **2: Analytics & Alerting** | 3-5 | SES increase (alert emails) | +$5/mo | $182/mo |
| **3: Developer Experience** | 5-7 | CloudFront (docs site) | +$2/mo | $184/mo |
| **4: Enterprise Readiness** | 7-10 | Cognito, dedicated Trino (per Enterprise) | +$3-73/mo | $187-257/mo |
| **5: Scale & Performance** | 10-14 | ElastiCache Redis; DuckDB replaces shared Trino | -$58 to +$176/mo | $129-433/mo |
| **6: Platform** | 14-18 | API Gateway (optional) | +$1-5/mo | $130-438/mo |

Key insight: **Phase 5 can reduce costs** — replacing shared Trino with embedded DuckDB eliminates the 3Gi memory footprint (~$70/mo savings). Multi-region deployment is the main cost driver at scale (+$164/mo per additional region).

---

## 4. Pricing Strategy

### Pricing Model: Events Per Month

Price by events ingested (not hosts, not seats). This aligns cost with value — more events = more data = more infrastructure.

| Plan | Events/Month | Avg QPS | Price | Per-Event Cost |
|------|-------------|---------|-------|----------------|
| **Free** | 1M | ~0.4 | $0 | Self-hosted only |
| **Starter** | 10M | ~4 | $29/mo | $2.90/M events |
| **Pro** | 50M | ~19 | $99/mo | $1.98/M events |
| **Business** | 200M | ~77 | $249/mo | $1.25/M events |
| **Enterprise** | Custom | Custom | Custom | ~$1.00/M events |

### Why Events, Not Hosts

- **Predictable**: Customers know how many requests their services handle
- **Fair**: A team with 2 high-traffic services pays more than a team with 20 idle services
- **No bill shock**: Unlike Datadog's high-watermark billing, events are straightforward
- **No gaming**: Hosts can be consolidated; events directly measure value delivered

### Competitive Pricing Comparison

For a team with 10 hosts, each handling ~1,000 requests/minute (combined ~17 QPS, ~44M events/month):

| Platform | Monthly Cost | What You Get |
|----------|-------------|-------------|
| Datadog (Infra Pro) | $150/mo | Infrastructure monitoring only |
| Datadog (Infra + APM) | $460/mo | Full APM + infrastructure |
| New Relic | $50-150/mo | Depends on GB ingested |
| Grafana Cloud | $80/mo | Requires PromQL expertise |
| **Gravix Pro** | **$99/mo** | Error rates, latency percentiles, throughput, events |

Gravix is **34-78% cheaper** than Datadog for the monitoring features most teams actually use daily.

### Pricing Evolution by Phase

Pricing tiers expand as the product matures:

| Timeline | Tiers Available | Changes |
|----------|----------------|---------|
| Month 1-2 | Free, Starter ($29), Pro ($99) | Launch tiers. Validate PMF. |
| Month 3-6 | + Business ($249) | Add after alerting ships (Phase 2). |
| Month 7-10 | + Enterprise ($500+ custom) | RBAC/SSO unlock enterprise sales. Annual billing at 10% discount. |
| Month 10-14 | Overage charges introduced | $1.50/M events (Starter), $1.25/M (Pro), $1.00/M (Business). Annual discount increases to 15%. |
| Month 14-18 | Add-on pricing | White-label (+$100-200/mo), data export (+$50/mo). Enterprise add-ons only. |

### Per-Tenant Rate Limits by Plan

| Plan | Ingestion Rate | Events/Month | Dashboard Users |
|------|---------------|-------------|-----------------|
| Free | 5 req/s | 1M | 1 |
| Starter | 20 req/s | 10M | 5 |
| Pro | 100 req/s | 50M | 20 |
| Business | 500 req/s | 200M | Unlimited |
| Enterprise | Custom | Custom | Unlimited + SSO |

---

## 5. Unit Economics

### Cost of Goods Sold (COGS) per Customer

| Plan | Revenue | Infra COGS | Gross Profit | Gross Margin |
|------|---------|------------|-------------|-------------|
| **Starter** ($29/mo) | $29 | $5-8 | $21-24 | 72-83% |
| **Pro** ($99/mo) | $99 | $12-20 | $79-87 | 80-88% |
| **Business** ($249/mo) | $249 | $25-45 | $204-224 | 82-90% |
| **Enterprise** ($500+/mo) | $500+ | $73-120 | $380-430+ | 76-86% |

COGS includes: proportional share of compute, S3 storage, S3 API calls, ALB, EKS control plane, data transfer. Enterprise COGS includes dedicated Trino instance ($70/mo) and Cognito SSO ($2.75/mo).

### COGS Evolution by Phase

As the platform matures, per-customer COGS decreases and gross margins improve — particularly at Phase 5 when DuckDB replaces Trino for shared-tier customers:

| Phase | Shared Tier COGS (Pro) | Enterprise COGS | Blended Gross Margin | Key Cost Driver |
|-------|----------------------|-----------------|---------------------|----------------|
| **1: SaaS Foundation** | $12-20 | — | 72-85% | Trino 3Gi memory overhead |
| **2: Analytics & Alerting** | $12-20 | — | 75-85% | SES alert emails (+$5/mo shared) |
| **3: Developer Experience** | $12-20 | — | 78-87% | Customer count improves fixed-cost spread |
| **4: Enterprise Readiness** | $12-20 | $73-120 | 80-88% | Dedicated Trino (+$70/mo per Enterprise) |
| **5: Scale & Performance** | $5-10 | $73-120 | 85-92% | DuckDB eliminates shared Trino (-$70/mo) |
| **6: Platform** | $5-10 | $73-120 | 85-92% | Marginal cost of add-ons near zero |

Phase 5 is the critical margin inflection point: replacing shared Trino (3Gi memory, dedicated process) with embedded DuckDB (reads Parquet from S3 directly, no separate process) reduces per-customer compute COGS by approximately 40-50%.

### Key Metrics

| Metric | Target (Phase 1-3) | Target (Phase 4-6) |
|--------|--------------------|--------------------|
| Gross margin | 75-85% | 85-92% |
| Infrastructure cost per customer | $5-45/mo | $5-120/mo (Enterprise higher, shared lower) |
| Customer acquisition cost (CAC) | $0 (open-source funnel) | $50-200 (content + SDKs) |
| LTV at 12-mo retention | $350 (S), $1,190 (P), $2,990 (B) | $350 (S), $1,190 (P), $2,990 (B), $6,000+ (E) |
| Break-even customers | ~15-20 (Pro mix) | ~8-10 (with Enterprise) |
| MRR at break-even | ~$1,500-2,000 | ~$1,000-1,500 (lower due to DuckDB savings) |

### Fixed vs Variable Costs

| Cost Type | Components | Phase 1-3 | Phase 4-6 |
|-----------|-----------|-----------|-----------|
| **Fixed** | EKS control plane, ALB, base node | ~$177/mo | ~$130-190/mo |
| **Variable** | S3 storage, S3 API calls, additional nodes | ~$0.15-5/customer | ~$0.10-3/customer |
| **Step-function** | Additional EC2 nodes (every ~20 customers) | ~$70-140/step | ~$30-70/step (DuckDB) |
| **Enterprise-fixed** | Dedicated Trino, Cognito, per-tenant infra | — | ~$73/Enterprise customer |

The high fixed cost ($164-177/mo EKS + base node) means the first 5-10 customers are critical to reach profitability. After Phase 5 DuckDB migration, fixed costs drop and shared-tier marginal costs approach near-zero.

---

## 6. Multi-Tenancy Architecture

### Current State (Single-Tenant)

The current architecture serves one deployment per environment. To offer SaaS, we need tenant isolation.

### Recommended SaaS Architecture (Phase 1)

**Shared infrastructure, isolated data:**

```
Customer A ─┐                    ┌─ S3: gravix-prod/tenants/acme/raw/
Customer B ─┼─→ Shared Ingestion ├─ S3: gravix-prod/tenants/beta/raw/
Customer C ─┘   (tenant header)  └─ S3: gravix-prod/tenants/gamma/raw/
                     │
              Shared Rollup (per-tenant prefix)
                     │
              Shared Trino (per-tenant schema)
                     │
              Shared Cube.js (tenant context)
                     │
              Shared Dashboard (tenant login)
```

**Changes needed:**
1. **Ingestion**: Add `X-Tenant-ID` header, route to per-tenant S3 prefix
2. **Rollup**: Process per-tenant data independently
3. **Trino**: One schema per tenant (`gravix.tenant_acme.request_metrics_minute`)
4. **Cube.js**: Tenant context injection (security context)
5. **Dashboard**: Login + tenant-scoped queries
6. **Authentication**: API key per tenant, dashboard auth per tenant

**Estimated effort**: 2-4 weeks of engineering work.

### Phase 2: Dedicated Compute (Enterprise)

For Enterprise customers needing isolation guarantees:
- Dedicated Trino instance (3 GB memory reserved)
- Dedicated S3 bucket
- Dedicated ingestion namespace
- SLA-backed query performance

---

## 7. Go-to-Market Strategy

Aligned with the 6-phase product roadmap (see [PRODUCT_ROADMAP.md](../PRODUCT_ROADMAP.md)).

### Phase 1: SaaS Launch (Month 1-2)

- Deploy multi-tenant platform at gravix.io
- Publish Gravix open-source on GitHub
- Launch with Free (1M events), Starter ($29), Pro ($99) tiers
- "Why we built Gravix" blog post on Hacker News, Reddit r/devops, r/sre
- Direct outreach to 20-30 engineering leads at target companies
- **Target**: 100 GitHub stars, 5-10 paying customers
- **Revenue**: $150-500 MRR

### Phase 2: Content & Alerting (Month 3-5)

- Add Business tier ($249) — unlocked by alerting features
- "Datadog costs $X, here's what we do for $29" comparison articles
- Case study from first paying customers
- Slack app directory listing (drives organic discovery)
- Dev-focused content: "Monitor your Go/Python/Node service in 5 minutes"
- **Target**: 15-25 paying customers
- **Revenue**: $1,500-2,500 MRR

### Phase 3: Developer Flywheel (Month 5-7)

- SDK launches (Go, Python, Node.js) with package manager listings
- GitHub Actions marketplace listing (`gravix/deploy-event`)
- Documentation site at docs.gravix.io
- Conference lightning talks (GopherCon, PyCon, KubeCon)
- Open-source community building (Discord, GitHub Discussions)
- **Target**: 30-50 customers (SDKs drive adoption velocity)
- **Revenue**: $3,000-5,000 MRR

### Phase 4: Enterprise Sales Motion (Month 7-10)

- Launch Enterprise tier ($500+ custom) — RBAC, SSO, audit logs
- Annual billing option (10% discount for commitment)
- SOC 2 Type II certification process
- Direct enterprise sales outreach (CTO/VP Eng at 50-200 person companies)
- Partner program with DevOps consultancies
- **Target**: 50-80 customers including 5-10 Enterprise
- **Revenue**: $8,000-15,000 MRR

### Phase 5: Scale & Efficiency (Month 10-14)

- DuckDB migration reduces COGS — pass savings to customers via competitive pricing
- Overage charges for power users ($1-1.50/M events above plan)
- Multi-region presence unlocks EU/APAC customers
- Annual discount increases to 15%
- Customer success program for Business and Enterprise accounts
- **Target**: 80-150 customers, 10-20 Enterprise
- **Revenue**: $15,000-25,000 MRR

### Phase 6: Platform & Ecosystem (Month 14-18)

- Terraform provider drives GitOps-native Enterprise adoption
- Marketplace integrations (PagerDuty, OpsGenie, Teams) expand use cases
- White-label option for MSPs and platform teams (+$100-200/mo)
- Data export add-on (+$50/mo) for compliance-heavy customers
- Public API enables third-party tooling
- **Target**: 150-300 customers, 20-40 Enterprise
- **Revenue**: $25,000-40,000 MRR

---

## 8. Financial Projections (18 Months)

### Revenue Model (Phase-Aligned)

| Month | Phase | Free | Starter ($29) | Pro ($99) | Business ($249) | Enterprise ($500+) | MRR |
|-------|-------|------|---------------|-----------|----------------|-------------------|-----|
| 2 | 1 | 5 | 2 | 0 | 0 | 0 | $58 |
| 4 | 2 | 15 | 5 | 2 | 0 | 0 | $343 |
| 6 | 2 | 30 | 8 | 5 | 1 | 0 | $976 |
| 8 | 4 | 50 | 12 | 8 | 3 | 1 | $2,389 |
| 10 | 4 | 70 | 15 | 12 | 5 | 2 | $3,868 |
| 12 | 5 | 100 | 18 | 18 | 8 | 4 | $6,306 |
| 14 | 5 | 130 | 20 | 25 | 12 | 7 | $9,555 |
| 16 | 6 | 160 | 22 | 30 | 15 | 10 | $12,883 |
| 18 | 6 | 200 | 25 | 35 | 18 | 15 | $17,710 |

*Enterprise pricing averages $650/mo (mix of $500 base + add-ons). MRR excludes annual billing discounts and overage charges.*

### Cost Model (Phase-Aligned)

| Month | Phase | AWS Infra | Enterprise Dedicated | Domain/TLS/DNS | SOC 2/Compliance | Total Costs | Net (MRR - Costs) |
|-------|-------|-----------|---------------------|---------------|-----------------|-------------|-------------------|
| 2 | 1 | $177 | $0 | $15 | $0 | $192 | -$134 |
| 4 | 2 | $182 | $0 | $15 | $0 | $197 | +$146 |
| 6 | 2 | $182 | $0 | $15 | $0 | $197 | +$779 |
| 8 | 4 | $190 | $70 | $15 | $500 | $775 | +$1,614 |
| 10 | 4 | $190 | $140 | $15 | $500 | $845 | +$3,023 |
| 12 | 5 | $135 | $280 | $15 | $500 | $930 | +$5,376 |
| 14 | 5 | $135 | $490 | $15 | $500 | $1,140 | +$8,415 |
| 16 | 6 | $140 | $700 | $15 | $500 | $1,355 | +$11,528 |
| 18 | 6 | $140 | $1,050 | $15 | $500 | $1,705 | +$16,005 |

*Phase 5 (Month 12): shared infra drops from $190 to $135/mo after DuckDB migration eliminates Trino. SOC 2 costs amortize to ~$500/mo ($5-15K annually). Enterprise dedicated Trino at $70/mo per customer.*

*Note: Does not include engineering salaries, marketing spend, or customer support costs. These are infrastructure-only projections.*

### Cumulative Revenue & Profit

| Milestone | Month | Cumulative Revenue | Cumulative Infra Costs | Cumulative Net |
|-----------|-------|-------------------|----------------------|---------------|
| First dollar | 2 | $58 | $192 | -$134 |
| Infra break-even | 4 | $743 | $586 | +$157 |
| $5K MRR | 11 | $27,200 | $5,900 | +$21,300 |
| $10K MRR | 14 | $58,600 | $9,800 | +$48,800 |
| Month 18 total | 18 | $115,400 | $15,500 | +$99,900 |

### Break-Even Analysis

| Scenario | Break-Even Point | Notes |
|----------|-----------------|-------|
| All Starter ($29) customers | ~7 customers | Covers $192/mo Phase 1 infra |
| 50/50 Starter/Pro mix | ~4 customers | Most likely early scenario |
| With 1 Enterprise ($500) customer | ~2 additional shared customers | Enterprise covers most fixed costs |
| Post-DuckDB (Phase 5+) | ~2-3 Pro customers | Reduced shared infra ($135/mo) |

The product reaches infrastructure profitability at Month 4 (~$343 MRR vs $197 costs). Enterprise customers dramatically improve economics — a single Enterprise customer at $500/mo covers 60%+ of shared infrastructure costs.

---

## 9. Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-----------|------------|
| **Trino scaling bottleneck** | Can't serve 50+ tenants on single node | Medium | Plan Athena/DuckDB migration path |
| **Grafana Cloud free tier** | Teams choose free over $29/mo | High | Differentiate on simplicity (no PromQL, 5-min setup) |
| **Datadog cuts prices** | Pricing advantage erodes | Low | Stay 5-10x cheaper, focus on simplicity |
| **Low switching costs** | Easy to churn | Medium | Build stickiness via dashboard customization, Slack integration |
| **Multi-tenancy security** | Tenant data leakage | Low | Schema isolation, S3 prefix isolation, security review |
| **Operational burden** | SaaS ops takes time from product | Medium | Automate everything, keep architecture simple |
| **Market too small** | Not enough teams in "good enough" segment | Medium | Validate with open-source adoption numbers first |

---

## 10. Engineering Investment

### Total Effort by Phase

| Phase | Timeline | Effort | Cumulative | Key Deliverables |
|-------|----------|--------|-----------|-----------------|
| 1: SaaS Foundation | Month 1-2 | 10 pw | 10 pw | Multi-tenancy, Stripe billing, onboarding |
| 2: Analytics & Alerting | Month 3-5 | 10.5 pw | 20.5 pw | Path drill-down, threshold alerts, Slack |
| 3: Developer Experience | Month 5-7 | 9.5 pw | 30 pw | Go/Python/Node SDKs, GitHub Action, CLI |
| 4: Enterprise Readiness | Month 7-10 | 15 pw | 45 pw | RBAC, SSO (Cognito), audit logs, SOC 2 |
| 5: Scale & Performance | Month 10-14 | 15 pw | 60 pw | DuckDB migration, multi-region, Redis cache |
| 6: Platform | Month 14-18 | 15 pw | 75 pw | Custom dashboards, public API, Terraform provider |

**Total: ~75 person-weeks** (pw)

### Staffing Scenarios

| Team Size | Duration | Monthly Burn (est.) | 18-Month Total |
|-----------|----------|--------------------|--------------|
| 1 engineer (founder) | 18 months | $0 (sweat equity) | $0 |
| 1 engineer + 1 contractor | 12 months | $8-12K/mo | $96-144K |
| 2 full-time engineers | 9 months | $25-35K/mo | $225-315K |

### ROI Analysis

At the conservative revenue projection ($17,710 MRR at Month 18 = $212K ARR):

| Scenario | 18-Month Investment | 18-Month Cumulative Revenue | ROI |
|----------|--------------------|-----------------------------|-----|
| Solo founder | ~$0 (time only) | $115K | ∞ (time vs money) |
| 1 eng + 1 contractor | $96-144K | $115K | -20% to +20% (break-even) |
| 2 full-time engineers | $225-315K | $115K | -49% to -63% (needs growth) |

The solo founder scenario is profitable from Month 4. The 2-engineer scenario requires reaching ~$25K MRR to justify the investment — achievable by Month 16-18 in the optimistic case.

### Build Order Rationale

Phases are sequenced for maximum revenue impact per engineering week:

1. **Phase 1 first** — no revenue without multi-tenancy (prerequisite for everything)
2. **Phase 2 second** — alerting is the #1 feature request that converts free→paid and Starter→Pro
3. **Phase 3 third** — SDKs reduce time-to-value and drive organic adoption (growth multiplier)
4. **Phase 4 fourth** — Enterprise tier requires RBAC/SSO but generates 5-10x revenue per customer
5. **Phase 5 fifth** — DuckDB migration improves margins but doesn't unlock new revenue directly
6. **Phase 6 last** — platform features build ecosystem moat but require large customer base to justify

---

## 11. Key Decisions Needed

1. **Domain & branding**: gravix.io? gravix.dev? Something else?
2. **Multi-tenancy timeline**: Build before or after first paying customer?
3. **Free tier scope**: 1M events enough to demonstrate value?
4. **AWS region**: Start single-region (us-east-1) or multi?
5. **Payment processor**: Stripe? LemonSqueezy?
6. **Support model**: Email only? Discord community? Paid support tiers?
7. **Open-source license**: MIT (maximum adoption) or AGPL (protect SaaS)?

---

## Appendix: Data Sources

- Infrastructure resource requirements from `deploy/gravix/values.yaml` and `deploy/gravix/values-prod.yaml`
- Local resource requirements from `docker-compose.yml` (13 services, 7 GB total memory)
- AWS pricing for us-east-1 (EC2, S3, EKS, ALB, EBS, data transfer, Cognito, ElastiCache)
- Datadog pricing from datadoghq.com/pricing (March 2026): $15-23/host infra, $31-40/host APM
- Non-goals and competitive positioning from `docs/04-non-goals.md`
- 6-phase product roadmap from `PRODUCT_ROADMAP.md` (effort, infra costs, timelines)
- DuckDB cost savings analysis based on Trino 3Gi memory footprint vs embedded DuckDB zero-overhead model
