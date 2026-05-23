# Opportunity Database Schema

The master database lives at `market-intel/db/opportunities.json`.

```json
{
  "version": "1.0",
  "last_updated": "2026-05-21T00:00:00Z",
  "opportunities": [
    {
      "id": "opp-001",
      "metadata": {
        "date_discovered": "2026-05-21",
        "last_updated": "2026-05-21",
        "industry": "Construction",
        "geography": "Northern Ireland",
        "category": "Invoice Reconciliation",
        "tags": ["compliance", "admin-automation", "trades", "SME"],
        "status": "active"
      },

      "scoring": {
        "market": {
          "market_size": 4,
          "pain_severity": 5,
          "frequency": 4,
          "willingness_to_pay": 3
        },
        "build": {
          "mvp_simplicity": 4,
          "data_availability": 3,
          "ai_leverage": 3,
          "competition_saturation": 4,
          "maintenance_complexity": 4
        },
        "sales": {
          "ease_of_reaching_buyers": 3,
          "sales_cycle_length": 4,
          "founder_led_sales_viability": 4,
          "speed_to_first_revenue": 4
        },
        "composite": {
          "opportunity_score": 4.0,
          "buildability_score": 3.6,
          "revenue_velocity_score": 3.75,
          "overall_weighted_score": 3.79
        },
        "history": [
          {
            "date": "2026-05-21",
            "overall_weighted_score": 3.79,
            "rank": 3,
            "confidence": "medium"
          }
        ],
        "rank": 3,
        "previous_rank": null,
        "rank_delta": null,
        "confidence": "medium"
      },

      "business_analysis": {
        "problem_statement": "Small construction firms manually reconcile subcontractor invoices against WhatsApp/text message job approvals, spending 3-5 hours per week.",
        "target_buyer": "Owner/office manager of construction firms with 5-50 employees",
        "trigger_event": "Monthly invoice reconciliation, HMRC reporting deadlines",
        "current_workaround": "Spreadsheets, paper files, scrolling through WhatsApp",
        "competitor_summary": "Xero (too generic), Procore (too expensive/enterprise), manual processes",
        "market_maturity": "early",
        "regulatory_complexity": "low"
      },

      "technical_analysis": {
        "suggested_architecture": "Web app with mobile-friendly interface, OCR pipeline for invoice scanning",
        "ai_components": ["Invoice data extraction via document AI", "Matching engine for approvals-to-invoices"],
        "required_integrations": ["WhatsApp Business API or message import", "Xero/QuickBooks export"],
        "data_sources": ["User-uploaded invoices", "Message exports", "Accounting software sync"],
        "infrastructure_complexity": "low",
        "estimated_monthly_infra_cost": "50-150 GBP",
        "maintenance_expectations": "Low — mainly keeping OCR models current and accounting integrations maintained"
      },

      "mvp_definition": {
        "core_features": [
          "Invoice upload and OCR extraction",
          "Manual or imported job approval matching",
          "Dashboard showing matched/unmatched invoices",
          "Export to CSV/accounting format"
        ],
        "features_to_fake_manually": [
          "Complex OCR failures handled by human review queue",
          "WhatsApp integration initially via screenshot upload"
        ],
        "four_week_scope": "Invoice upload, basic extraction, manual matching UI, export",
        "eight_week_scope": "Add AI matching suggestions, WhatsApp import, Xero integration",
        "suggested_tech_stack": "Next.js, PostgreSQL, Claude API for document extraction, Vercel"
      },

      "commercial_analysis": {
        "pricing_model": "Monthly subscription per company",
        "estimated_acv_gbp": 1200,
        "likely_first_customers": "NI construction firms found via Federation of Master Builders, local trade associations",
        "distribution_strategy": "Direct outreach to trade associations, LinkedIn, local networking",
        "sales_difficulty": "medium"
      }
    }
  ]
}
```

## Field notes

- `id`: Use format `opp-NNN` with zero-padded numbers. Never reuse an ID.
- `status`: One of `active`, `declining`, `archived`, `superseded`.
- `confidence`: One of `low`, `medium`, `high`. Based on evidence quality.
- `history`: Append-only array. Add a new entry each week. Never modify old entries.
- `rank_delta`: Positive means improved (moved up), negative means dropped. Null on first entry.
- `estimated_acv_gbp`: Annual contract value in GBP. Use realistic SME pricing.
- `market_maturity`: One of `nascent`, `early`, `growing`, `mature`, `declining`.
