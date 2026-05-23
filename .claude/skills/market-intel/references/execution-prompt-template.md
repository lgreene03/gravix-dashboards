# Execution Prompt Template

Generate one of these for every opportunity with an overall weighted score >= 3.5.
Save to `market-intel/reports/execution-prompts/{idea-id}.md`.

The prompt must be detailed enough that an engineering team or AI coding agent
(Claude Code, Cursor, Codex) could begin implementation planning immediately.

---

## Template structure

```markdown
# {Idea Name} — Execution Brief

> {One-sentence elevator pitch}

## Product Vision

### What it does
{2-3 sentences describing the core product}

### Who it serves
{Primary audience with specifics — industry, company size, geography}

### Why it matters
{The operational pain this solves and why now is the right time}

## User Personas

### Primary: {Role title}
- **Context**: {Their day-to-day situation}
- **Pain points**: {Specific frustrations, 3-5 bullets}
- **Success looks like**: {What changes for them}

### Secondary: {Role title}
- **Context**: {How they interact with the primary user's workflow}
- **Pain points**: {Their specific frustrations}

## MVP Requirements (4-week scope)

### Core features
{Numbered list of features, prioritised}

### User flows
{Step-by-step for the 2-3 most important workflows}

### Required screens/pages
{List with brief description of each}

### Admin functionality
{What the operator/admin needs}

### Authentication
{Auth requirements — keep it simple for MVP}

### Billing
{Stripe integration, free trial, pricing tier}

## Technical Architecture

### Frontend
{Framework, key libraries, deployment}

### Backend
{Language, framework, API style}

### Database
{Database choice, key design decisions}

### API requirements
{External APIs needed, rate limits, costs}

### AI model usage
{Specific Claude/OpenAI calls, expected volumes, cost estimate}

### Hosting
{Recommended hosting, estimated monthly cost}

### Scalability
{What assumptions hold for MVP, what breaks at scale}

## Data Model

### Core entities
{Entity name, key fields, relationships — enough to start a schema}

### Important metadata
{Audit fields, soft deletes, tenant isolation if multi-tenant}

## AI Usage Details

### Where AI adds value
{Specific workflows where AI is the differentiator}

### Where deterministic systems are better
{Don't use AI for everything — be explicit about what should be rules-based}

### Prompting suggestions
{Example prompts for key AI features, model recommendations}

## Development Plan

### Week 1: {Focus area}
{Key deliverables, 3-5 bullets}

### Week 2: {Focus area}
{Key deliverables}

### Week 3: {Focus area}
{Key deliverables}

### Week 4: {Focus area}
{Key deliverables}

### Engineer allocation
{How to split work across 2-3 engineers}

### Fastest route to MVP
{The absolute minimum to get in front of a paying customer}

## Go-To-Market

### First 10 customers
{Specific strategy — names of associations, communities, channels}

### Outreach method
{Cold email templates, LinkedIn approach, in-person events}

### Pricing
{Specific price points with rationale}

### Validation before building
{How to test demand before writing code — landing pages, interviews, pre-sales}
```

## Quality bar

A good execution prompt should make the reader think "I could start building
this on Monday." A bad one reads like a business school case study — abstract
and full of jargon with no actionable specifics.

Test yourself: could you hand this to a contractor who knows nothing about the
market and have them build a working prototype? If not, add more detail to the
technical sections. Could you hand the go-to-market section to a non-technical
cofounder and have them start selling? If not, add more specifics about who to
contact and what to say.
