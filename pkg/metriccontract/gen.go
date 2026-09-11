// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package metriccontract

import (
	"fmt"
	"strings"
)

// GeneratedMarker is the first line of every generated document. A reader who
// opens the file and starts typing should be told immediately that their edit
// will be overwritten.
const GeneratedMarker = "<!-- GENERATED FROM contracts/*.yaml BY 'make contracts' — DO NOT EDIT -->"

// Render produces the Markdown documentation for a registry. The output is
// deterministic: contracts appear in registry order, and nothing about the
// machine or the moment reaches the page.
func Render(r *Registry) string {
	var b strings.Builder

	b.WriteString(GeneratedMarker)
	b.WriteString("\n")
	b.WriteString(`
# Derived Metrics

Every metric Gravix reports has a published contract: its formula, the facts and
fields it consumes, its grain, how exact it is, how it may be aggregated, what
happens to late data, and the command that rebuilds it.

The contract is the promise. Changing a shipped metric's meaning requires a new
version rather than an edit in place, because someone who stored last month's
numbers must still be able to find out what they meant.

Two fields are worth reading before any of the definitions:

- **Exactness** is ` + "`exact`" + ` (computed from raw facts), ` + "`sketch`" + ` (a bounded-error
  summary, with the bound stated), or ` + "`approximate`" + ` (an error that is **not**
  bounded). An ` + "`approximate`" + ` metric is a defect this project owes a fix on, not a
  design choice, and every one of them is listed under [Known defects](#known-defects).
- **Mergeability** says whether two grains may be combined at all. ` + "`none`" + ` means
  any query that aggregates the metric across grains is reporting a number with no
  defined meaning.

`)

	b.WriteString("## Metrics\n")
	for _, c := range r.Contracts {
		b.WriteString(renderContract(c))
	}

	b.WriteString(renderDefects(r))
	return b.String()
}

// renderContract emits one metric's full contract.
func renderContract(c Contract) string {
	var b strings.Builder

	fmt.Fprintf(&b, "\n### `%s` — %s\n\n", c.Name, c.Title)
	fmt.Fprintf(&b, "| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| **Version** | `%s` |\n", c.Version)
	fmt.Fprintf(&b, "| **Formula** | %s |\n", inlineCell(c.Formula))
	fmt.Fprintf(&b, "| **Grain** | %s |\n", inlineCell(c.Grain))
	fmt.Fprintf(&b, "| **Input facts** | %s |\n", codeList(c.InputFacts))
	fmt.Fprintf(&b, "| **Input fields** | %s |\n", codeList(c.InputFields))
	fmt.Fprintf(&b, "| **Dimensions** | %s |\n", codeList(c.Dimensions))
	fmt.Fprintf(&b, "| **Exactness** | `%s` |\n", c.Exactness)
	fmt.Fprintf(&b, "| **Error bound** | %s |\n", inlineCell(c.ErrorBound))
	fmt.Fprintf(&b, "| **Mergeability** | `%s` |\n", c.Mergeability)
	if c.Supersedes != "" {
		fmt.Fprintf(&b, "| **Supersedes** | `%s` |\n", c.Supersedes)
	}
	if c.Deprecated {
		fmt.Fprintf(&b, "| **Deprecated** | yes |\n")
	}

	if note := strings.TrimSpace(c.MergeNote); note != "" {
		fmt.Fprintf(&b, "\n**Aggregating it.** %s\n", collapse(note))
	}
	if late := strings.TrimSpace(c.LateData); late != "" {
		fmt.Fprintf(&b, "\n**Late data.** %s\n", collapse(late))
	}
	if defect := strings.TrimSpace(c.KnownDefect); defect != "" {
		fmt.Fprintf(&b, "\n> **Known defect.**\n>\n")
		for _, line := range strings.Split(defect, "\n") {
			if strings.TrimSpace(line) == "" {
				fmt.Fprintf(&b, ">\n")
				continue
			}
			fmt.Fprintf(&b, "> %s\n", line)
		}
	}

	fmt.Fprintf(&b, "\n**Rebuild it.**\n\n```bash\n%s\n```\n", strings.TrimSpace(c.RecomputeCmd))
	return b.String()
}

// renderDefects lists every approximate metric, or says plainly that there are
// none. The section is always present: its absence could be read as nobody having
// looked.
func renderDefects(r *Registry) string {
	var b strings.Builder
	defects := r.KnownDefects()

	b.WriteString("\n## Known defects\n\n")
	if len(defects) == 0 {
		b.WriteString("None. Every metric above is `exact` or is a `sketch` with a stated error bound.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "%s of the metrics above are `approximate`: their error is not bounded. "+
		"They are listed here because an undisclosed approximation is worse than a missing "+
		"metric — a reader cannot tell a wrong number from a right one.\n\n", countWord(len(defects)))

	b.WriteString("| Metric | Error bound | Fixed by |\n|---|---|---|\n")
	for _, c := range defects {
		fmt.Fprintf(&b, "| `%s@%s` | %s | %s |\n", c.Name, c.Version, inlineCell(c.ErrorBound), fixedBy(c))
	}
	return b.String()
}

// fixedBy pulls the spec id out of a defect note, so the table says who owes the
// fix rather than leaving the reader to search for it.
func fixedBy(c Contract) string {
	for _, word := range strings.Fields(strings.ReplaceAll(c.KnownDefect, ",", " ")) {
		trimmed := strings.Trim(word, ".,;:()")
		if strings.HasPrefix(trimmed, "GRVX-") {
			return "`" + trimmed + "`"
		}
	}
	return "not yet assigned"
}

func countWord(n int) string {
	switch n {
	case 1:
		return "One"
	case 2:
		return "Two"
	case 3:
		return "Three"
	default:
		return fmt.Sprintf("%d", n)
	}
}

// inlineCell makes a value safe for a Markdown table cell: one line, with pipes
// escaped so they do not split the row.
func inlineCell(v string) string {
	v = collapse(v)
	if v == "" {
		return "—"
	}
	return strings.ReplaceAll(v, "|", `\|`)
}

// codeList renders a slice as inline code, comma separated.
func codeList(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = "`" + it + "`"
	}
	return strings.Join(quoted, ", ")
}

// collapse folds a YAML folded block into a single line.
func collapse(v string) string {
	return strings.Join(strings.Fields(v), " ")
}
