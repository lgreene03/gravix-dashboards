## {{.Version}}

{{.Summary}}
{{with breakingChanges}}
## Upgrade notes

**Read this before upgrading.** These changes require action.
{{range .}}
- **{{.Title}}** {{ref .}}

  {{.UpgradeNote}}
{{end}}{{end}}{{range kinds}}{{$kind := .}}{{with changesOfKind $kind}}
### {{heading $kind}}
{{range .}}
- {{.Title}}{{if .SpecID}} ({{.SpecID}}){{end}} {{ref .}} — {{creditList .Authors}}
{{- end}}
{{end}}{{end}}{{with eeChanges}}
### Pro (source-available)

These changes are under `ee/`, licensed BUSL-1.1. They are **source-available**, not open source.
{{range .}}
- {{.Title}}{{if .SpecID}} ({{.SpecID}}){{end}} {{ref .}} — {{creditList .Authors}}
{{- end}}
{{end}}
## Contributors

Everyone whose work is in this release, alphabetically. No ranking, no tiers — a one-line fix and a
subsystem count the same here, because they are worth the same amount of thank you.
{{range .Contributors}}
- {{credit .}}{{end}}
{{- if .Anonymous}}

…and {{.Anonymous}} who asked not to be credited. No reason is required; see
[docs/oss/no-credit.md](docs/oss/no-credit.md).
{{- end}}
{{with .FirstTimers}}
### First contribution

Welcome. Merging someone's first pull request is worth marking, and we are glad you are here.
{{range .}}
- {{credit .}}{{end}}
{{end}}
---

**Full changelog:** {{.CompareURL}}

_Semver: this is a **{{.SemverBump}}** release, derived from the changes above._
