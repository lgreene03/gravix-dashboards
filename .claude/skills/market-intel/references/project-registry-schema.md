# Project Registry Schema

The project registry lives at `market-intel/registry/projects.json`.
It tracks which opportunities have been greenlit and their build status.

```json
{
  "version": "1.0",
  "last_updated": "2026-05-21T00:00:00Z",
  "max_concurrent_projects": 2,
  "projects": [
    {
      "id": "proj-001",
      "opportunity_id": "opp-007",
      "name": "SubconTrack",
      "repo_path": "/Users/lgreene/factory/subcontrack",
      "repo_remote": "lgreene03/subcontrack",
      "status": "building",
      "greenlit_date": "2026-05-25",
      "last_build_activity": "2026-05-28",
      "roadmap_path": "docs/ROADMAP.md",
      "stack": "Next.js / PostgreSQL / Claude API",
      "milestones": {
        "scaffolded": "2026-05-25",
        "mvp_core_complete": null,
        "landing_page_live": null,
        "first_user_test": null,
        "first_revenue": null
      },
      "health": {
        "last_ci_status": "passing",
        "days_since_progress": 0,
        "blocked": false,
        "block_reason": null
      },
      "validation": {
        "landing_page_url": null,
        "signups": 0,
        "outreach_sent": 0,
        "outreach_replies": 0,
        "interviews_done": 0,
        "demand_signal": "untested"
      },
      "kill_criteria": {
        "max_days_no_progress": 14,
        "max_days_no_validation": 30,
        "auto_pause_if_blocked_days": 7
      }
    }
  ]
}
```

## Status values

- `queued` — greenlit but waiting for a build slot (max_concurrent_projects reached)
- `scaffolding` — repo being created and initial code generated
- `building` — active development via the build pipeline
- `validating` — MVP complete, testing with real users
- `paused` — temporarily stopped (blocked, or deprioritised)
- `launched` — live and generating revenue or active users
- `killed` — cancelled during build (with reason)
- `completed` — fully shipped and handed off

## Health tracking

- `days_since_progress`: calculated from last_build_activity vs today
- `blocked`: true if the build pipeline has been unable to advance for > 3 consecutive runs
- `block_reason`: human-readable explanation of what's blocking progress

## Validation tracking

These fields are updated manually by the user or by the validation task.
They feed into the kill criteria — a project with zero validation signals
after 30 days should be paused for review.

## Kill criteria

- `max_days_no_progress`: if no build activity for this many days, auto-pause and flag
- `max_days_no_validation`: if no demand validation for this many days, auto-pause and flag
- `auto_pause_if_blocked_days`: if blocked for this many days, auto-pause
