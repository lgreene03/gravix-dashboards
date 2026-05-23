# Gravix Service Level Agreement (SLA)

**Effective Date:** 2026-03-23
**Last Updated:** 2026-03-23

## 1. Service Availability

Gravix commits to the following monthly uptime targets based on your subscription plan:

| Plan | Monthly Uptime SLA | Monthly Allowed Downtime |
|------|-------------------|-------------------------|
| Free | No SLA | N/A |
| Team | 99.5% | ~3.6 hours |
| Business | 99.9% | ~43 minutes |
| Scale | 99.9% | ~43 minutes |
| Enterprise | 99.95% | ~22 minutes |

**Uptime** is measured as the percentage of minutes in a calendar month during which the Gravix Ingestion API (`POST /api/v1/facts`) returns a 2xx or 429 response within 5 seconds.

## 2. Exclusions

The following are excluded from uptime calculations:

- Scheduled maintenance windows (announced 48 hours in advance)
- Force majeure events (natural disasters, war, government actions)
- Issues caused by customer network, infrastructure, or SDK misconfiguration
- Abuse, DDoS attacks, or activity exceeding rate limits by more than 10x
- Third-party service outages (cloud provider, DNS, Stripe)

## 3. Service Credits

If Gravix fails to meet the SLA in a given month, eligible customers may request service credits:

| Monthly Uptime | Credit (% of Monthly Fee) |
|---------------|--------------------------|
| 99.0% - 99.9% | 10% |
| 95.0% - 99.0% | 25% |
| < 95.0% | 50% |

### How to Claim

1. Submit a support request within 30 days of the incident
2. Include the affected time period and impact description
3. Credits are applied to the next billing cycle (not refundable as cash)

## 4. Support Response Times

| Severity | Business Plan | Scale Plan | Enterprise Plan |
|----------|-------------|-----------|----------------|
| P1 (Service Down) | 4 hours | 2 hours | 1 hour |
| P2 (Degraded) | 8 hours | 4 hours | 2 hours |
| P3 (Minor Issue) | 2 business days | 1 business day | 4 hours |
| P4 (Question) | 5 business days | 2 business days | 1 business day |

- **Enterprise** includes a named Technical Account Manager (TAM)
- **Support hours:** Business days, 09:00-18:00 UTC (P1/P2 monitored 24/7 for Enterprise)

## 5. Data Durability

- Ingested data is persisted to durable object storage within 60 seconds of acknowledgment
- Raw fact data is retained per your plan's retention policy
- Aggregated metrics are recomputable from raw facts at any time

## 6. Maintenance Windows

- **Planned maintenance:** Tuesdays 02:00-04:00 UTC (announced 48h in advance)
- **Emergency patches:** Applied as needed with best-effort advance notice
- Maintenance that requires downtime will not exceed the SLA allowance per month

## 7. Changes to This SLA

Gravix may update this SLA with 30 days written notice. Existing Enterprise contract terms take precedence over changes during the contract period.
