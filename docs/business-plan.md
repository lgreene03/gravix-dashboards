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

**Recommendation**: Start with shared Trino. Plan migration to Athena or DuckDB at 50+ customers.

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

---

## 5. Unit Economics

### Cost of Goods Sold (COGS) per Customer

| Plan | Revenue | Infra COGS | Gross Profit | Gross Margin |
|------|---------|------------|-------------|-------------|
| **Starter** ($29/mo) | $29 | $5-8 | $21-24 | 72-83% |
| **Pro** ($99/mo) | $99 | $12-20 | $79-87 | 80-88% |
| **Business** ($249/mo) | $249 | $25-45 | $204-224 | 82-90% |

COGS includes: proportional share of compute, S3 storage, S3 API calls, ALB, EKS control plane, data transfer.

### Key Metrics

| Metric | Target |
|--------|--------|
| Gross margin | 75-85% |
| Infrastructure cost per customer | $5-45/mo (scales with usage) |
| Customer acquisition cost (CAC) | $0 initially (open-source funnel) |
| Lifetime value (LTV) at 12-mo retention | $350 (Starter), $1,190 (Pro), $2,990 (Business) |
| Break-even customers | ~15-20 (Pro mix) |
| MRR at break-even | ~$1,500-2,000 |

### Fixed vs Variable Costs

| Cost Type | Components | Monthly |
|-----------|-----------|---------|
| **Fixed** | EKS control plane, ALB, base node | ~$164/mo |
| **Variable** | S3 storage, S3 API calls, additional nodes | ~$0.15-5/mo per customer |
| **Step-function** | Additional EC2 nodes (every ~20 customers) | ~$70-140 per step |

The high fixed cost ($164/mo EKS + base node) means the first 5-10 customers are critical to reach profitability. After that, marginal costs are very low.

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

### Phase 1: Open Source Foundation (Month 1-3)

- Publish Gravix on GitHub (already done)
- Write "Why we built Gravix" blog post
- Post on Hacker News, Reddit r/devops, r/sre
- Target: 100 GitHub stars, 10 self-hosted users
- Revenue: $0

### Phase 2: Managed Beta (Month 3-6)

- Launch hosted version at gravix.io (or similar)
- Free tier: 1M events/month (attracts users, provides feedback)
- Starter plan: $29/mo
- Target: 5-10 paying customers
- Revenue: $150-500/mo

### Phase 3: Growth (Month 6-12)

- Add Pro and Business tiers
- Build integrations (Slack alerts, GitHub deploy tracking)
- Content marketing: "Datadog costs $X, here's what we do for $29"
- Target: 20-50 paying customers
- Revenue: $2,000-8,000/mo

### Phase 4: Enterprise (Month 12-18)

- RBAC, SSO (OIDC/SAML), audit logs
- Dedicated compute option
- Compliance (SOC 2 Type II readiness)
- Target: 50-200 customers including 5-10 Enterprise
- Revenue: $15,000-25,000/mo

---

## 8. Financial Projections (18 Months)

### Revenue Model

| Month | Free Users | Starter ($29) | Pro ($99) | Business ($249) | MRR |
|-------|-----------|---------------|-----------|----------------|-----|
| 3 | 10 | 2 | 0 | 0 | $58 |
| 6 | 30 | 5 | 3 | 0 | $442 |
| 9 | 60 | 10 | 8 | 2 | $1,580 |
| 12 | 100 | 15 | 15 | 5 | $3,145 |
| 15 | 150 | 20 | 25 | 8 | $5,047 |
| 18 | 200 | 25 | 35 | 12 | $7,170 |

### Cost Model

| Month | Infra (AWS) | Domain/TLS/DNS | Total Costs | Net (MRR - Costs) |
|-------|-------------|---------------|-------------|-------------------|
| 3 | $164 | $15 | $179 | -$121 |
| 6 | $233 | $15 | $248 | +$194 |
| 9 | $302 | $15 | $317 | +$1,263 |
| 12 | $370 | $15 | $385 | +$2,760 |
| 15 | $441 | $15 | $456 | +$4,591 |
| 18 | $510 | $15 | $525 | +$6,645 |

*Note: Does not include engineering time, marketing spend, or customer support costs.*

### Break-Even Analysis

| Scenario | Break-Even Point |
|----------|-----------------|
| All Starter ($29) customers | ~8 customers (covers $233/mo infra) |
| 50/50 Starter/Pro mix | ~5 customers |
| All Pro ($99) customers | ~3 customers |

The product reaches infrastructure profitability very quickly (3-8 paying customers). The real question is whether engineering time and customer acquisition justify the effort — that depends on growth rate.

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

## 10. Key Decisions Needed

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
- AWS pricing for us-east-1 (EC2, S3, EKS, ALB, EBS, data transfer)
- Datadog pricing from datadoghq.com/pricing (March 2026): $15-23/host infra, $31-40/host APM
- Non-goals and competitive positioning from `docs/04-non-goals.md`
