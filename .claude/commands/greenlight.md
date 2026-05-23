# Greenlight — Scaffold a new project from an opportunity

The user has decided to build an opportunity. Your job is to go from "idea in a database" to "repo with code, CI, roadmap, and wired into the build pipeline."

## Input

The user provides an opportunity ID (e.g., `greenlight opp-007`) or a name. The argument is: $ARGUMENTS

## Step 1 — Validate the decision

1. Read `market-intel/db/opportunities.json` and find the opportunity.
2. Read `market-intel/registry/projects.json` and check:
   - How many projects have status "building" or "scaffolding"?
   - Is that >= max_concurrent_projects?
   - If so, STOP. Tell the user: "You have [N] projects in flight. Pause or complete one before greenlighting another." List the active projects.
3. Check the opportunity has:
   - status = "active"
   - overall_weighted_score >= 3.0
   - A premortem field (has survived at least one pre-mortem)
   - An execution prompt at `market-intel/reports/execution-prompts/{id}.md`
4. If any check fails, warn the user but let them override.

## Step 2 — Read the execution prompt

Read `market-intel/reports/execution-prompts/{opportunity-id}.md` thoroughly. This is your build spec.

Extract:
- Project name (derive a short kebab-case repo name)
- Tech stack
- Core features for 4-week MVP
- Data model
- API requirements
- AI usage

## Step 3 — Create the repo

```bash
mkdir -p /Users/lgreene/factory/{project-name}
cd /Users/lgreene/factory/{project-name}
git init
```

Create the foundational files based on the execution prompt's tech stack:

### For Next.js / TypeScript projects:
- `package.json` with dependencies
- `tsconfig.json`
- `next.config.js`
- `src/app/layout.tsx` — root layout
- `src/app/page.tsx` — landing/home page
- `src/app/api/` — API route stubs for core endpoints
- `src/lib/db.ts` — database connection setup
- `src/lib/ai.ts` — Claude API wrapper if AI is used
- `.env.example` — all required env vars with placeholder values
- `Dockerfile`
- `.github/workflows/ci.yml` — basic CI (lint, typecheck, test)

### For Go projects:
- `go.mod`
- `cmd/{name}/main.go` — entry point
- `internal/` — core packages based on data model
- `Makefile` — build, test, lint targets
- `.github/workflows/ci.yml`
- `Dockerfile`

### For Python projects:
- `pyproject.toml`
- `src/{name}/` — package structure
- `tests/`
- `.github/workflows/ci.yml`
- `Dockerfile`

Adapt based on what the execution prompt specifies. Don't over-scaffold — create enough that the build pipeline can start making progress, not a finished app.

### Always create:
- `README.md` — project name, one-paragraph description, setup instructions, link back to the opportunity ID
- `docs/ROADMAP.md` — generated from the execution prompt's development plan, formatted as phases with checkboxes matching the nightly-roadmap-advance format
- `CLAUDE.md` — project-specific instructions for Claude Code, including the tech stack, key commands, architecture overview, and any constraints from the execution prompt
- `.claude/hooks/whats-next.sh` — a script that reads ROADMAP.md and outputs the next unchecked deliverable (match the format used by the Norse repos)
- `.gitignore` — appropriate for the stack

## Step 4 — Database schema

If the execution prompt includes a data model, generate:
- Migration files or schema definitions appropriate to the database choice
- Seed data script if useful for development

## Step 5 — Initial commit and push

```bash
git add -A
git commit -m "Initial scaffold from opportunity {opp-id}

Generated from market-intel execution prompt.
Tech stack: {stack}
MVP scope: {4-week description}

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>"
```

Ask the user if they want to create the GitHub repo and push:
- `gh repo create lgreene03/{project-name} --private --source . --push`

## Step 6 — Register the project

Update `market-intel/registry/projects.json`:
- Add a new project entry following the schema in `.claude/skills/market-intel/references/project-registry-schema.md`
- Set status = "building"
- Set greenlit_date = today
- Set milestones.scaffolded = today
- Set the repo_path and repo_remote

Update `market-intel/db/opportunities.json`:
- Set the opportunity's status to "in_build"
- Add a note: "Greenlit on {date}, project ID: proj-XXX"

## Step 7 — Summary

Tell the user:
- What was created and where
- The repo URL (if pushed)
- The first 3 roadmap items the build pipeline will pick up
- Any setup steps they need to do manually (env vars, API keys, database)

## Quality bar

The scaffold should be good enough that:
1. `npm install && npm run dev` (or equivalent) works immediately
2. CI passes on the initial commit
3. The nightly build pipeline can read ROADMAP.md and start advancing deliverables
4. A human engineer could open the repo and understand what they're building and why
