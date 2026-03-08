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
| **Gravix** | Per events/month | **$29-499/mo** | Simple, cheap, no agents, no lock-in | No traces, no logs, no real-time |

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

### Bootstrap Tier ($0-20/mo) — Phase 0-3

Before spending on AWS, Gravix runs on a single cheap VPS using the bootstrap stack (`docker-compose.bootstrap.yml`). DuckDB replaces Trino; local disk replaces MinIO.

| Component | Bootstrap Stack | Full AWS Stack |
|-----------|----------------|---------------|
| Query engine | DuckDB (embedded, ~300MB) | Trino (3GB JVM) |
| Object storage | Local disk | MinIO / S3 |
| Monitoring | None (add later) | Prometheus + Grafana |
| Total RAM | ~800MB | ~7.8GB |

**Bootstrap hosting options:**

| Hosting | Monthly Cost | RAM | Notes |
|---------|-------------|-----|-------|
| Oracle Cloud Always Free | $0 | 24 GB ARM | Best free option |
| Hetzner CX22 | €4 (~$4) | 4 GB | Cheapest paid VPS |
| DigitalOcean Basic | $6 | 1 GB | Tight but workable |
| AWS t3.small | $15 | 2 GB | If AWS-preferred |

**Bootstrap-era add-ons (Phase 1-3):**

| Service | Monthly Cost | When Needed |
|---------|-------------|-------------|
| Managed Postgres (Neon free / Supabase free / RDS db.t4g.micro) | $0-12 | Phase 1 (tenant metadata) |
| SES (transactional email) | ~$0.50 | Phase 1 (onboarding) |
| Route 53 hosted zone | $0.50 | Phase 1 (DNS) |
| Domain registration | ~$12/yr ($1/mo) | Phase 1 |
| CloudFront (docs site) | ~$2 | Phase 3 |

**Total bootstrap-era cost: $0-20/mo** (Phase 0), scaling to **$13-33/mo** (Phase 1-3) depending on hosting choice and managed DB selection.

**Upgrade trigger:** Migrate to AWS EKS at ~Phase 4 when Enterprise customers justify the cost (~$3K+ MRR, ~50+ customers).

### AWS Full Stack Pricing (Phase 4+)

All costs based on AWS us-east-1 pricing. Gravix runs on Kubernetes (EKS) with S3 for storage and DuckDB as the query engine (no Trino).

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

**Per-Customer Resource Footprint (shared infrastructure, DuckDB):**

| Component | CPU Request | Memory Request | CPU Limit | Memory Limit | Notes |
|-----------|-------------|---------------|-----------|-------------|-------|
| Ingestion (shared, 2 replicas) | 500m | 512 Mi | 2 | 2 Gi | Scales via HPA |
| Cube.js + DuckDB (shared, 1 replica) | 200m | 512 Mi | 1 | 1 Gi | DuckDB embedded, no separate process |
| Dashboard (shared, 1 replica) | 50m | 32 Mi | 100m | 64 Mi | Static nginx |
| Rollup CronJobs (4 jobs) | 350m | 448 Mi | 1.75 | 1.79 Gi | Run periodically, not always |
| **Total (steady-state)** | **~0.95 CPU** | **~1.5 Gi** | **~4.85 CPU** | **~5 Gi** | Excluding CronJob burst |

*DuckDB replaces Trino (Phase 0), saving ~1 Gi steady-state memory and eliminating the 3 Gi memory-intensive JVM process.*

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

### Query Engine: DuckDB (from Day 1)

DuckDB is the query engine from Phase 0 onward. It was chosen over Trino to minimize memory requirements and eliminate operational overhead:

| Query Engine | Pros | Cons | Cost Impact |
|-------------|------|------|------------|
| **DuckDB** (Phase 0+) | No separate process, reads Parquet directly, fast for small-to-medium scans, ~300MB embedded | Single-node only, newer ecosystem | Near-zero |
| **Trino** (available in full stack) | Mature, SQL-standard, horizontal scaling | 3 GB memory, separate JVM process, query contention | ~$70/mo (node capacity) |

**Approach**: DuckDB is the default for all customers from day one. The Cube.js models auto-detect the engine via `CUBEJS_DB_TYPE` env var, so switching to Trino is a config change if needed at extreme scale. No per-customer engine selection, no dedicated compute tiers.

### Scaling Path

At Phase 5 (50+ tenants), DuckDB performance is optimized via connection pooling, Parquet compaction, and Redis caching. Trino remains available in `docker-compose.yml` as a fallback if DuckDB single-node limits are hit, but this is not expected until 200+ concurrent tenants.

### Cost Evolution by Phase

As the product evolves through the 7-phase roadmap (see [PRODUCT_ROADMAP.md](../PRODUCT_ROADMAP.md)), infrastructure costs change:

| Phase | Month | Infrastructure | Additional Cost | Cumulative Infra |
|-------|-------|---------------|-----------------|-----------------|
| **0: Bootstrap** | Week 0 | VPS + DuckDB (no Trino, no MinIO) | $0-20/mo | **$0-20/mo** |
| **1: SaaS Foundation** | 1-2 | + Managed Postgres, SES, Route53 | +$13/mo | **$13-33/mo** |
| **2: Analytics & Alerting** | 3-5 | + SES increase (alert emails) | +$5/mo | **$18-38/mo** |
| **3: Developer Experience** | 5-7 | + CloudFront (docs site) | +$2/mo | **$20-40/mo** |
| **4: Enterprise Readiness** | 7-10 | Migrate to AWS EKS + Cognito | step up | **$187/mo** |
| **5: Scale & Performance** | 10-14 | + ElastiCache Redis | +$12/mo | **$199/mo** |
| **6: Platform** | 14-18 | + API Gateway (optional) | +$1-5/mo | **$200-204/mo** |

Key insights:
- **Phase 0-3 runs under $40/mo** on a cheap VPS with DuckDB and local disk. No AWS spend required until Enterprise customers justify EKS.
- **Phase 4 is the step-up** — migrating to AWS EKS when MRR supports it (~$3K+). DuckDB is already the query engine, so no migration needed.
- **Multi-region deployment** (Phase 5+) is the main cost driver at scale (+$164/mo per additional region).

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
| **Enterprise** | 500M | ~193 | $499/mo | $1.00/M events |

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
| Month 7-10 | + Enterprise ($499) | RBAC/SSO unlock enterprise sales. Annual billing at 10% discount. |
| Month 10-14 | Overage charges introduced | $1.50/M events (Starter), $1.25/M (Pro), $1.00/M (Business). Annual discount increases to 15%. |
| Month 14-18 | Add-on pricing | Data export (+$50/mo). Enterprise add-on. |

### Per-Tenant Rate Limits by Plan

| Plan | Ingestion Rate | Events/Month | Dashboard Users |
|------|---------------|-------------|-----------------|
| Free | 5 req/s | 1M | 1 |
| Starter | 20 req/s | 10M | 5 |
| Pro | 100 req/s | 50M | 20 |
| Business | 500 req/s | 200M | Unlimited |
| Enterprise | 1,000 req/s | 500M | Unlimited + SSO |

---

## 5. Unit Economics

### Cost of Goods Sold (COGS) per Customer

All customers run on the same shared infrastructure — Enterprise differentiates on features (RBAC, SSO, audit logs), not dedicated compute.

| Plan | Revenue | Infra COGS | Gross Profit | Gross Margin |
|------|---------|------------|-------------|-------------|
| **Starter** ($29/mo) | $29 | $5-8 | $21-24 | 72-83% |
| **Pro** ($99/mo) | $99 | $12-20 | $79-87 | 80-88% |
| **Business** ($249/mo) | $249 | $25-45 | $204-224 | 82-90% |
| **Enterprise** ($499/mo) | $499 | $25-45 | $454-474 | 91-95% |

COGS includes: proportional share of compute, S3 storage, S3 API calls, ALB, EKS control plane, data transfer. Enterprise COGS is similar to Business since all customers share the same infrastructure — Enterprise margins are significantly higher because revenue is higher on the same cost base.

### COGS Evolution by Phase

DuckDB is the query engine from Phase 0. Per-customer COGS improves as the customer base grows across a shared, low-cost infrastructure:

| Phase | COGS per Customer (Pro) | Gross Margin | Key Cost Driver |
|-------|------------------------|-------------|----------------|
| **0: Bootstrap** | $2-5 | 85-95% | Cheap VPS shared across early customers |
| **1: SaaS Foundation** | $3-8 | 82-92% | Managed Postgres + SES add small shared cost |
| **2: Analytics & Alerting** | $3-8 | 85-92% | Customer count improves fixed-cost spread |
| **3: Developer Experience** | $2-6 | 88-94% | More customers, same infrastructure |
| **4: Enterprise Readiness** | $8-15 | 80-88% | EKS migration increases base cost |
| **5: Scale & Performance** | $5-10 | 88-94% | Redis cache, more customers per node |
| **6: Platform** | $5-10 | 88-94% | Marginal cost of add-ons near zero |

**Phase 0-3 has the best unit economics** because the bootstrap stack costs near-zero. Phase 4 (EKS migration) temporarily increases per-customer COGS but is justified by Enterprise revenue ($499/mo). By Phase 5-6, customer density and Redis caching restore margins to 88-94%.

### Key Metrics

| Metric | Target (Phase 1-3) | Target (Phase 4-6) |
|--------|--------------------|--------------------|
| Gross margin | 75-85% | 88-94% |
| Infrastructure cost per customer | $5-45/mo | $5-10/mo (same for all tiers) |
| Customer acquisition cost (CAC) | $0 (open-source funnel) | $50-200 (content + SDKs) |
| LTV at 12-mo retention | $350 (S), $1,190 (P), $2,990 (B) | $350 (S), $1,190 (P), $2,990 (B), $6,000+ (E) |
| Break-even customers | ~15-20 (Pro mix) | ~8-10 (with Enterprise) |
| MRR at break-even | ~$1,500-2,000 | ~$1,000-1,500 (lower due to DuckDB savings) |

### Fixed vs Variable Costs

| Cost Type | Components | Phase 0-3 (Bootstrap) | Phase 4-6 (AWS) |
|-----------|-----------|----------------------|-----------------|
| **Fixed** | VPS or EKS + ALB + base node | ~$0-20/mo | ~$187-199/mo |
| **Variable** | Disk/S3 storage, API calls | ~$0.05-1/customer | ~$0.10-3/customer |
| **Step-function** | Larger VPS or additional EC2 nodes | ~$5-15/step (VPS) | ~$30-70/step (EC2) |

No per-customer infrastructure costs. All customers share the same stack. Enterprise customers pay more for features, but their infra COGS is identical to Business customers.

The bootstrap stack's near-zero fixed cost ($0-20/mo) means **even the first paying customer generates profit**. The Phase 4 migration to AWS EKS increases fixed costs to ~$187/mo but is only triggered when MRR supports it (~$3K+, ~50 customers).

---

## 6. Multi-Tenancy Architecture

### Current State (Single-Tenant)

The current architecture serves one deployment per environment. To offer SaaS, we need tenant isolation.

### Recommended SaaS Architecture (Phase 1)

**Shared infrastructure, isolated data:**

```
Customer A ─┐                    ┌─ data/tenants/acme/raw/   (bootstrap: local disk)
Customer B ─┼─→ Shared Ingestion ├─ data/tenants/beta/raw/   (or S3 at scale)
Customer C ─┘   (tenant header)  └─ data/tenants/gamma/raw/
                     │
              Shared Rollup (per-tenant prefix)
                     │
              Shared DuckDB (embedded in Cube.js, reads per-tenant Parquet)
                     │
              Shared Cube.js (tenant context via securityContext)
                     │
              Shared Dashboard (tenant login)
```

**Changes needed:**
1. **Ingestion**: Add `X-Tenant-ID` header, route to per-tenant storage prefix
2. **Rollup**: Process per-tenant data independently
3. **Cube.js**: Tenant context injection via `securityContext` + DuckDB `read_parquet` with tenant path prefix
4. **Dashboard**: Login + tenant-scoped queries
5. **Authentication**: API key per tenant, dashboard auth per tenant

**Estimated effort**: 2-4 weeks of engineering work.

### Design Principle: One Architecture for All

Enterprise customers run on the **same shared infrastructure** as all other plans. There is no dedicated compute, no per-customer query engines, no per-customer S3 buckets. Enterprise differentiates on **features** (RBAC, SSO, audit logs, higher rate limits, team namespaces), not infrastructure.

This keeps the platform simple to operate, deploy, and scale — one Helm chart, one set of monitoring, one upgrade path.

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

- Launch Enterprise tier ($499) — RBAC, SSO, audit logs (same shared infra, premium features)
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
- Tenant branding (Enterprise) — custom logo, colors, email templates (config-only, no separate infra)
- Data export add-on (+$50/mo) for compliance-heavy customers
- Public API enables third-party tooling
- **Target**: 150-300 customers, 20-40 Enterprise
- **Revenue**: $25,000-40,000 MRR

---

## 8. Financial Projections (18 Months)

### Revenue Model (Phase-Aligned)

| Month | Phase | Free | Starter ($29) | Pro ($99) | Business ($249) | Enterprise ($499) | MRR |
|-------|-------|------|---------------|-----------|----------------|-------------------|-----|
| 2 | 1 | 5 | 2 | 0 | 0 | 0 | $58 |
| 4 | 2 | 15 | 5 | 2 | 0 | 0 | $343 |
| 6 | 2 | 30 | 8 | 5 | 1 | 0 | $976 |
| 8 | 4 | 50 | 12 | 8 | 3 | 1 | $2,386 |
| 10 | 4 | 70 | 15 | 12 | 5 | 2 | $3,866 |
| 12 | 5 | 100 | 18 | 18 | 8 | 4 | $6,292 |
| 14 | 5 | 130 | 20 | 25 | 12 | 7 | $9,536 |
| 16 | 6 | 160 | 22 | 30 | 15 | 10 | $12,333 |
| 18 | 6 | 200 | 25 | 35 | 18 | 15 | $16,157 |

*Enterprise is a fixed $499/mo (same shared infrastructure as all tiers). MRR excludes annual billing discounts and overage charges.*

### Cost Model (Phase-Aligned)

One shared infrastructure for all customers — bootstrap stack (Phase 0-3) then AWS EKS (Phase 4+).

| Month | Phase | Infra | Domain/TLS/DNS | SOC 2/Compliance | Total Costs | Net (MRR - Costs) |
|-------|-------|-------|---------------|-----------------|-------------|-------------------|
| 0 | 0 | $4 | $1 | $0 | $5 | -$5 |
| 2 | 1 | $33 | $1 | $0 | $34 | +$24 |
| 4 | 2 | $38 | $1 | $0 | $39 | +$304 |
| 6 | 2 | $38 | $1 | $0 | $39 | +$937 |
| 8 | 4 | $187 | $15 | $500 | $702 | +$1,684 |
| 10 | 4 | $187 | $15 | $500 | $702 | +$3,164 |
| 12 | 5 | $199 | $15 | $500 | $714 | +$5,578 |
| 14 | 5 | $199 | $15 | $500 | $714 | +$8,822 |
| 16 | 6 | $204 | $15 | $500 | $719 | +$11,614 |
| 18 | 6 | $204 | $15 | $500 | $719 | +$15,438 |

*Phase 0-3 (Month 0-7): Bootstrap stack on cheap VPS (~$4-38/mo). Phase 4 (Month 8): Migrate to AWS EKS when Enterprise customers justify the cost. SOC 2 costs amortize to ~$500/mo ($5-15K annually). Node scaling adds ~$30-70/mo per step at ~20 customer increments (not shown).*

*Note: Does not include engineering salaries, marketing spend, or customer support costs. These are infrastructure-only projections.*

### Cumulative Revenue & Profit

| Milestone | Month | Cumulative Revenue | Cumulative Infra Costs | Cumulative Net |
|-----------|-------|-------------------|----------------------|---------------|
| Bootstrap deployed | 0 | $0 | $5 | -$5 |
| First dollar | 2 | $58 | $73 | -$15 |
| Infra break-even | 3 | $300 | $112 | +$188 |
| $5K MRR | 11 | $25,500 | $3,700 | +$21,800 |
| $10K MRR | 15 | $57,200 | $6,800 | +$50,400 |
| Month 18 total | 18 | $105,000 | $9,600 | +$95,400 |

*The bootstrap stack saves ~$900 in cumulative infra costs over the first 7 months vs starting on AWS EKS.*

### Break-Even Analysis

| Scenario | Break-Even Point | Notes |
|----------|-----------------|-------|
| Bootstrap stack (Phase 0-3) | ~1 Starter customer | $29 MRR covers $5-34/mo infra |
| All Starter ($29) on AWS | ~7 customers | Covers $192/mo Phase 4 infra |
| 50/50 Starter/Pro mix on AWS | ~4 customers | Most likely Phase 4+ scenario |
| With 1 Enterprise ($500) customer | ~1 additional customer | Enterprise covers most fixed costs |

The bootstrap stack reaches infrastructure profitability with **a single paying customer** ($29/mo vs $5-34/mo infra). This changes the economics dramatically: instead of needing 4-7 customers to break even on $192/mo AWS costs, the first dollar is profitable.

After migrating to AWS EKS (Phase 4, ~Month 8), break-even requires ~4 customers at the Starter/Pro mix. By that point, MRR should be $2,000+ with 50+ customers, so the migration is well-funded.

---

## 9. Risks & Mitigations

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-----------|------------|
| **DuckDB single-node limit** | Single-node DuckDB may not handle 200+ tenants | Low | Parquet compaction, Redis cache, query optimization; revisit at scale |
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
| 0: Bootstrap | Week 0 | 0.5 pw | 0.5 pw | DuckDB, bootstrap compose, local disk storage |
| 1: SaaS Foundation | Month 1-2 | 10 pw | 10.5 pw | Multi-tenancy, Stripe billing, onboarding |
| 2: Analytics & Alerting | Month 3-5 | 10.5 pw | 21 pw | Path drill-down, threshold alerts, Slack |
| 3: Developer Experience | Month 5-7 | 9.5 pw | 30.5 pw | Go/Python/Node SDKs, GitHub Action, CLI |
| 4: Enterprise Readiness | Month 7-10 | 12 pw | 42.5 pw | RBAC, SSO (Cognito), audit logs, SOC 2 |
| 5: Scale & Performance | Month 10-14 | 8 pw | 50.5 pw | DuckDB tuning, multi-region, Redis cache |
| 6: Platform | Month 14-18 | 14.5 pw | 65 pw | Custom dashboards, public API, Terraform provider |

**Total: ~65 person-weeks** (pw)

*Reduced from ~68.5 pw by moving DuckDB to Phase 0 (saves 4 pw from Phase 5). Bootstrap runs under $20/mo for 6+ months before any AWS spend is needed.*

### Staffing Scenarios

| Team Size | Duration | Monthly Burn (est.) | 18-Month Total |
|-----------|----------|--------------------|--------------|
| 1 engineer (founder) | 18 months | $0 (sweat equity) | $0 |
| 1 engineer + 1 contractor | 12 months | $8-12K/mo | $96-144K |
| 2 full-time engineers | 9 months | $25-35K/mo | $225-315K |

### ROI Analysis

At the conservative revenue projection ($16,157 MRR at Month 18 = $194K ARR):

| Scenario | 18-Month Investment | 18-Month Cumulative Revenue | Cumulative Net | ROI |
|----------|--------------------|-----------------------------|----------------|-----|
| Solo founder | ~$0 (time only) | $105K | +$95.4K | ∞ (time vs money) |
| 1 eng + 1 contractor | $96-144K | $105K | +$95.4K | -33% to +0% |
| 2 full-time engineers | $225-315K | $105K | +$95.4K | -57% to -69% (needs growth) |

The solo founder scenario is profitable from **Month 2** (bootstrap stack costs $5-34/mo vs $58 MRR). The bootstrap approach improves ROI over starting on AWS — cumulative infra costs are ~$9.6K (vs ~$10.5K starting on AWS), and the first 7 months of near-zero infra spend means cash-flow positive earlier.

### Build Order Rationale

Phases are sequenced for maximum revenue impact per engineering week:

0. **Phase 0 first** — run the entire stack under $20/mo. DuckDB upfront means no costly Trino migration later
1. **Phase 1 second** — no revenue without multi-tenancy (prerequisite for everything)
2. **Phase 2 third** — alerting is the #1 feature request that converts free→paid and Starter→Pro
3. **Phase 3 fourth** — SDKs reduce time-to-value and drive organic adoption (growth multiplier)
4. **Phase 4 fifth** — Enterprise tier requires RBAC/SSO but generates 5-10x revenue per customer; triggers AWS migration
5. **Phase 5 sixth** — DuckDB performance tuning and caching at scale
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

- Bootstrap resource requirements from `docker-compose.bootstrap.yml` (6 services, ~800 MB total memory)
- Full stack resource requirements from `docker-compose.yml` (11 services, ~7.8 GB total memory)
- Infrastructure resource requirements from `deploy/gravix/values.yaml` and `deploy/gravix/values-prod.yaml`
- AWS pricing for us-east-1 (EC2, S3, EKS, ALB, EBS, data transfer, Cognito, ElastiCache)
- VPS pricing from Hetzner, Oracle Cloud, DigitalOcean (March 2026)
- Datadog pricing from datadoghq.com/pricing (March 2026): $15-23/host infra, $31-40/host APM
- Non-goals and competitive positioning from `docs/04-non-goals.md`
- 7-phase product roadmap from `PRODUCT_ROADMAP.md` (Phase 0 bootstrap + Phases 1-6)
- DuckDB as default query engine from Phase 0 — replaces Trino upfront, not as a migration
