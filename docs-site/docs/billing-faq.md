---
title: Billing FAQ
sidebar_position: 6
---

# Billing FAQ

## What Counts as an Event?

One **event** is one `RequestFact` record successfully ingested and acknowledged by the Gravix API. A fact represents a single HTTP request observed by your service.

Events that are **rejected** (invalid schema, duplicate `event_id`, malformed JSON) do not count toward your quota.

Events sent by the load generator or test scripts count. Use a separate workspace or the `X-Gravix-Test: true` header to exclude test traffic from billing.

## Plans

| Plan       | Included Events/Month | Retention | Alert Rules | Price        |
|------------|-----------------------|-----------|-------------|--------------|
| Hobby      | 5 million             | 7 days    | 3           | Free         |
| Starter    | 50 million            | 30 days   | 25          | $29/month    |
| Pro        | 500 million           | 90 days   | Unlimited   | $149/month   |
| Enterprise | Custom                | Custom    | Unlimited   | Custom       |

All paid plans include unlimited services, API keys, team members, and Slack/PagerDuty notification channels.

## What Happens When I Go Over My Limit?

**Hobby** plan: ingestion is paused for the remainder of the billing month once you hit 5 million events. Your dashboard and historical data remain accessible.

**Starter and Pro** plans: overage events are billed at a per-million rate:

| Plan    | Overage Rate         |
|---------|----------------------|
| Starter | $1.50 per million    |
| Pro     | $0.80 per million    |

You will receive an email warning at 80% and 100% of your included quota. You can set a hard cap in **Settings → Billing → Overage Cap** to stop ingestion rather than incur overage charges.

## Annual Billing

All paid plans are available on annual billing at a 20% discount:

| Plan    | Monthly Billing | Annual Billing (per month) |
|---------|-----------------|----------------------------|
| Starter | $29/month       | $23/month ($276/year)      |
| Pro     | $149/month      | $119/month ($1,428/year)   |

Annual plans are invoiced upfront. Unused months are non-refundable but can be applied as credits toward an upgrade.

## Free Trial

Every new account starts with a 14-day free trial of the **Pro** plan — no credit card required. At the end of the trial:

- If you add a payment method, your plan activates immediately.
- If you do not, your workspace downgrades to **Hobby** and data beyond the 7-day retention window is purged after a 48-hour grace period.

Trial events do not count toward your first paid month's quota.

## Changing Plans

You can upgrade or downgrade at any time from **Settings → Billing → Change Plan**.

- **Upgrades** take effect immediately. You are prorated for the remainder of the current billing period.
- **Downgrades** take effect at the start of the next billing period. Retention policies are enforced at that point (data beyond the new plan's retention window is scheduled for deletion after a 7-day grace period).

## How Is Retention Enforced?

Raw JSONL files and aggregated Parquet files older than your plan's retention window are deleted on a nightly job. The dashboard will show a "data not available" message for time ranges outside your retention window.

Data export is available on Pro and Enterprise plans before deletion. See **Settings → Data → Export** to download Parquet files.

## Do SDK Retries Count as Multiple Events?

No. Each fact has a unique `event_id` (UUIDv7). Duplicate `event_id` values are deduplicated at ingestion time. Retries from the SDK are idempotent and will not double-count events.

## Enterprise and Custom Contracts

For volumes above 500 million events/month, custom data residency, SSO/SAML, or SLA requirements, contact [sales@gravix.io](mailto:sales@gravix.io).
