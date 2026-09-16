// Copyright 2026 The Gravix Authors
// SPDX-License-Identifier: Apache-2.0

package governance

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const boardPath = "docs-site/docs/roadmap.md"

func board(t *testing.T) string {
	t.Helper()
	return repoFile(t, "docs-site", "docs", "roadmap.md")
}

// generate runs the real generator and returns its output and exit code.
func generate(t *testing.T, args ...string) (string, int) {
	t.Helper()

	root := filepath.Join("..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	cmd := exec.Command("python3", append([]string{filepath.Join(abs, "scripts", "gen_roadmap_board.py")}, args...)...)
	cmd.Dir = abs
	out, err := cmd.CombinedOutput()

	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("run the generator: %v\n%s", err, out)
	}
	return string(out), code
}

// AC-1: the board contains all six sections, in order.
//
// The order is the argument. "What we are deliberately not building" sits third
// because most roadmap questions turn out to be that question, and burying it
// under a wishlist is how a project keeps answering it one issue at a time.
func TestBoardSectionsInOrder(t *testing.T) {
	text := board(t)

	sections := []string{
		"## What we are working on now",
		"## What is next",
		"## What we are deliberately not building",
		"## Goals we considered and rejected",
		"## What the community has asked for",
		"## How this page is made",
	}

	last := -1
	for _, s := range sections {
		i := strings.Index(text, s)
		if i < 0 {
			t.Errorf("the board has no %q section", s)
			continue
		}
		if i < last {
			t.Errorf("%q appears out of order", s)
		}
		last = i
	}
}

// AC-2: the board is generated, never hand-edited.
func TestBoardIsGenerated(t *testing.T) {
	text := board(t)

	first, _, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(first, "<!-- GENERATED") {
		t.Errorf("the board's first line is %q, want a generated marker", first)
	}
	if !strings.Contains(first, "do not edit") {
		t.Error("the marker does not tell a reader not to edit the file")
	}
	if !containsProse(text, "Nothing on this page is typed by hand; to change it, change the document it comes from.") {
		t.Error("the board does not say how to change it")
	}

	// Regenerating from the same inputs reproduces it exactly, or
	// `make roadmap-check` would be a coin toss.
	out, code := generate(t, "--check")
	if code != 0 {
		t.Errorf("the committed board is not what the generator produces (exit %d):\n%s", code, out)
	}
}

// AC-3: a stale board fails the check.
func TestStaleBoardFails(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	path := filepath.Join(root, boardPath)

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the board: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatalf("restore the board: %v", err)
		}
	})

	stale := strings.Replace(string(original), "## What is next", "## What is coming soon", 1)
	if stale == string(original) {
		t.Fatal("could not make the board stale")
	}
	if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
		t.Fatalf("write the stale board: %v", err)
	}

	out, code := generate(t, "--check")
	if code == 0 {
		t.Fatalf("a stale board passed the check:\n%s", out)
	}
	if !strings.Contains(out, "stale") {
		t.Errorf("the failure does not say the board is stale:\n%s", out)
	}
	// The diff is printed, so the failure is diagnosable from the CI log alone.
	if !strings.Contains(out, "What is coming soon") {
		t.Errorf("the failure does not show what differs:\n%s", out)
	}
}

// AC-4: every non-goal on the board names an alternative tool.
//
// GOVERNANCE.md promises to "say so kindly, cite the section, and name the tool
// that does do it". Being told no is annoying; being told no with nowhere to go
// is worse, and it is avoidable.
func TestNonGoalsNameAlternatives(t *testing.T) {
	text := board(t)

	section := text[strings.Index(text, "## What we are deliberately not building"):]
	section = section[:strings.Index(section, "## Goals we considered and rejected")]

	headings := regexp.MustCompile(`(?m)^### §(\d+)\. `).FindAllStringSubmatch(section, -1)
	if len(headings) != 7 {
		t.Fatalf("the board lists %d non-goals, want 7", len(headings))
	}

	uses := strings.Count(section, "- **Use instead:**")
	if uses != 7 {
		t.Errorf("%d of 7 non-goals name an alternative", uses)
	}

	// A real tool, not a gesture at one. Each of these is a product somebody
	// can go and use today.
	for _, tool := range []string{"Jaeger", "Grafana Tempo", "Loki", "Prometheus", "ClickHouse", "Trino", "DuckDB", "Datadog"} {
		if !strings.Contains(section, tool) {
			t.Errorf("no non-goal points at %s", tool)
		}
	}

	// And the generator refuses to build a board where one is missing.
	src := repoFile(t, "scripts", "gen_roadmap_board.py")
	if !strings.Contains(src, `names no alternative tool`) {
		t.Error("the generator has no failure path for a non-goal with no alternative")
	}
}

// AC-5: no date appears in the generated board.
//
// Phase durations are estimates. Published as commitments they generate one
// kind of feedback and no useful planning.
func TestBoardHasNoDates(t *testing.T) {
	dates := regexp.MustCompile(`\b(20\d{2}-\d{2}|Q[1-4]\s+20\d{2}|(January|February|March|April|May|June|July|August|September|October|November|December)\s+20\d{2}|\b20\d{2}\b)`)

	for _, line := range strings.Split(board(t), "\n") {
		if strings.Contains(line, "github.com") || strings.HasPrefix(line, "<!-- GENERATED") {
			continue
		}
		if m := dates.FindString(line); m != "" {
			t.Errorf("the board contains a date %q in: %s", m, strings.TrimSpace(line))
		}
	}

	if !containsProse(board(t), "There are no dates on this page.") {
		t.Error("the board does not say that it carries no dates")
	}

	// The generator enforces it rather than relying on nobody typing one.
	src := repoFile(t, "scripts", "gen_roadmap_board.py")
	if !strings.Contains(src, "generated board contains a date; phase durations are") {
		t.Error("the generator has no date guard")
	}
}

// AC-6: every community input carries a disposition.
func TestEveryInputHasDisposition(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	inputs := []map[string]any{
		{"number": 101, "title": "Slow queries over 90 days", "reactions": 12},
		{"number": 102, "title": "Add distributed tracing", "disposition": "declined — non-goal §1", "reactions": 30},
		{"number": 103, "title": "Export to S3 on a schedule", "disposition": "accepted as SPEC GRVX-1107", "reactions": 5},
		{"number": 104, "title": "A thing we thought about", "disposition": "declined — the cost is in storage, not queries", "reactions": 1},
	}
	path := writeInputs(t, inputs)

	out := filepath.Join(t.TempDir(), "roadmap.md")
	msg, code := generate(t, "--input-json", path, "--out", out)
	if code != 0 {
		t.Fatalf("generation failed (exit %d):\n%s", code, msg)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read the generated board: %v", err)
	}
	text := string(raw)

	section := text[strings.Index(text, "## What the community has asked for"):]
	section = section[:strings.Index(section, "## How this page is made")]

	for _, want := range []string{
		"under consideration",
		"declined — non-goal §1",
		"accepted as SPEC GRVX-1107",
		"declined — the cost is in storage, not queries",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the board does not show the disposition %q", want)
		}
	}

	// Ranked by reactions, and nothing sits unlabelled: the item with no
	// disposition in the fixture defaults to `under consideration` rather than
	// appearing blank.
	if i, j := strings.Index(section, "#102"), strings.Index(section, "#101"); i < 0 || j < 0 || i > j {
		t.Error("inputs are not ranked by reaction count")
	}
	_ = root

	// An unknown disposition is refused rather than printed.
	bad := writeInputs(t, []map[string]any{{"number": 105, "title": "x", "disposition": "maybe later"}})
	msg, code = generate(t, "--input-json", bad, "--out", filepath.Join(t.TempDir(), "r.md"))
	if code == 0 {
		t.Errorf("an unknown disposition was accepted:\n%s", msg)
	}
	if !strings.Contains(msg, "which is not one of") {
		t.Errorf("the failure does not name the allowed dispositions:\n%s", msg)
	}
}

// AC-7: an item past two ticks fails generation.
//
// Indefinite consideration is a decline that nobody has to defend, and it is
// the outcome that wastes the most of a requester's hope.
func TestIndefiniteConsiderationFails(t *testing.T) {
	path := writeInputs(t, []map[string]any{
		{"number": 201, "title": "Something nobody decided", "disposition": "under consideration", "ticks": 3},
	})

	out, code := generate(t, "--input-json", path, "--out", filepath.Join(t.TempDir(), "r.md"))
	if code != 1 {
		t.Fatalf("exit = %d, want 1\n%s", code, out)
	}
	if want := "roadmap: input #201 has been under consideration for 3 ticks; decide it"; !strings.Contains(out, want) {
		t.Errorf("message does not match §6.1:\n%s", out)
	}

	// Two ticks is still allowed; the limit is "at most two".
	ok := writeInputs(t, []map[string]any{
		{"number": 202, "title": "Recently arrived", "disposition": "under consideration", "ticks": 2},
	})
	if out, code := generate(t, "--input-json", ok, "--out", filepath.Join(t.TempDir(), "r.md")); code != 0 {
		t.Errorf("two ticks was refused (exit %d):\n%s", code, out)
	}
}

// AC-8: the voting statement appears verbatim.
func TestVotingStatementVerbatim(t *testing.T) {
	const statement = `Reactions on a roadmap issue tell us what people want. They do not decide what
gets built.

We read them at the monthly goal review, alongside our own incident data, the
adoption funnel, and what the charter allows. A heavily-supported request that
crosses a non-goal is still declined, and we will say so in that issue rather
than leaving it open to rot.

What voting genuinely changes: the order of things we were already going to
build, and our sense of which problem is most painful. That is a real influence,
and it is the honest description of it.`

	if !strings.Contains(repoFile(t, "docs", "oss", "roadmap-input.md"), statement) {
		t.Error("docs/oss/roadmap-input.md does not contain the voting statement verbatim")
	}
}

// AC-9: the input template asks for the problem, not the feature.
func TestInputTemplateAsksForProblem(t *testing.T) {
	text := repoFile(t, ".github", "ISSUE_TEMPLATE", "roadmap_input.yml")

	if !containsProse(text, "Tell us the problem, not the feature.") {
		t.Error("the template does not ask for the problem")
	}
	if !strings.Contains(text, "label: What is going wrong?") {
		t.Error("the first required field is not the problem")
	}

	// There is no "proposed feature" field. Asking for one invites the guess
	// instead of the problem, which is the whole thing this template avoids.
	for _, forbidden := range []string{"Proposed feature", "Feature request", "Describe the solution"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("the template asks for %q", forbidden)
		}
	}

	// It sets a disposition on arrival, so nothing is unlabelled from minute one.
	if !strings.Contains(text, `"under consideration"`) {
		t.Error("the template does not label new input `under consideration`")
	}
	// And it says up front that reactions do not decide.
	if !containsProse(text, "They do not decide what gets built") {
		t.Error("the template does not say what voting does")
	}
}

// AC-10: rejected goals from the goal tree appear on the board.
func TestRejectedGoalsShown(t *testing.T) {
	text := board(t)

	section := text[strings.Index(text, "## Goals we considered and rejected"):]
	section = section[:strings.Index(section, "## What the community has asked for")]

	// Every row of the goal tree's table, with its reason.
	goals := repoFile(t, "docs", "oss", "12-goal-tree.md")
	rejected := goals[strings.Index(goals, "## Goals deliberately NOT set"):]
	if i := strings.Index(rejected[1:], "\n## "); i > 0 {
		rejected = rejected[:i]
	}

	var rows int
	for _, line := range strings.Split(rejected, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.Contains(line, "---") || strings.Contains(line, "| Not a goal |") {
			continue
		}
		name := strings.TrimSpace(strings.Split(strings.Trim(line, "|"), "|")[0])
		if name == "" {
			continue
		}
		rows++
		if !strings.Contains(section, name) {
			t.Errorf("the board omits the rejected goal %q", name)
		}
	}
	if rows == 0 {
		t.Fatal("no rejected goals parsed from the goal tree")
	}

	// The reason travels with the goal. A list of rejected goals with no
	// reasons is a list that gets re-litigated.
	if !strings.Contains(section, "Vanity.") {
		t.Error("the board shows rejected goals without their reasons")
	}
	if !containsProse(section, "stops them being re-proposed every quarter as though they were new") {
		t.Error("the board does not say why rejected goals are recorded")
	}
}

// The board is reachable. A page nobody can navigate to is a page that does not
// exist, and eight of them were orphaned from the sidebar before this.
func TestBoardIsInTheSidebar(t *testing.T) {
	sidebar := repoFile(t, "docs-site", "sidebars.js")
	if !strings.Contains(sidebar, "'roadmap'") {
		t.Error("the roadmap is not in the sidebar")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "docs-site", "docs"))
	if err != nil {
		t.Fatalf("read docs-site/docs: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		if !strings.Contains(sidebar, "'"+id+"'") {
			t.Errorf("%s is not reachable from the sidebar", e.Name())
		}
	}
}

// writeInputs writes a roadmap-input fixture and returns its path.
func writeInputs(t *testing.T, inputs []map[string]any) string {
	t.Helper()

	raw, err := json.Marshal(inputs)
	if err != nil {
		t.Fatalf("encode inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "roadmap-input.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write inputs: %v", err)
	}
	return path
}

// Every row of the spec index must be readable by the board generator, and the
// generator must fail rather than skip when one is not.
//
// It skipped fifteen of eighty-eight for months, every Phase 13 ee/ spec among
// them, because its row expression could backtrack across a cell boundary: a row
// it could not read either vanished or came back with a placement taken from the
// middle of its title. `make roadmap-check` passed throughout, because it
// compares a board generated by that parser against a board generated by that
// parser. See F-045.
//
// This checks the data the generator depends on, independently of the generator,
// so the two cannot agree with each other and be wrong together.
func TestEverySpecIndexRowIsWellFormed(t *testing.T) {
	index := repoFile(t, "docs", "oss", "specs", "SPEC-INDEX.md")

	link := regexp.MustCompile(`^\[(GRVX-\d{3,4})\]\([^)]*\)$`)
	rows := 0
	for _, line := range strings.Split(index, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "| [GRVX-") {
			continue
		}
		rows++
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(cells) < 4 {
			t.Errorf("a spec row has %d columns; it needs id, title, placement and goal:\n  %s",
				len(cells), line)
			continue
		}
		m := link.FindStringSubmatch(cells[0])
		if m == nil {
			t.Errorf("cannot read the spec link in %q", cells[0])
			continue
		}
		id := m[1]
		if cells[1] == "" {
			t.Errorf("%s has no title", id)
		}
		// The placement decides which licence a spec's code ships under, so a
		// value the generator has to guess at is not acceptable here. `boundary`
		// is the third legitimate value: GRVX-702, 703 and 710 build the
		// core/ee boundary itself and contain no product code either side of it.
		switch placement := strings.TrimRight(strings.Trim(cells[2], "`"), "/"); placement {
		case "core", "ee", "boundary":
		default:
			t.Errorf("%s has placement %q; it must be core, ee or boundary", id, placement)
		}
	}
	if rows < 80 {
		t.Errorf("only %d spec rows found in the index; there should be 88", rows)
	}
}

// GRVX-1104. Grafana treats plugin.json's `executable` as a PREFIX and execs
// "<executable>_<goos>_<goarch>". A build command that writes the bare name
// produces a plugin Grafana loads as a frontend and then cannot start:
//
//	Could not start plugin backend ... fork/exec
//	.../gpx_gravix_datasource_linux_amd64: no such file or directory
//
// That is how this shipped in the first commit of the spec — the install guide,
// the spec's own §6 step 7, and the e2e test all wrote the bare name, and five
// passing acceptance criteria did not notice, because none of them ran Grafana.
//
// AC-7 catches it, but only where there is a Docker daemon. This is the cheap
// guard that runs everywhere: whatever the docs tell a reader to type, the
// output name must be one Grafana will actually exec.
func TestPluginBuildCommandProducesAnExecutableGrafanaCanFind(t *testing.T) {
	root := repoRoot(t)

	var executable struct {
		Executable string `json:"executable"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "grafana-plugin", "gravix-datasource", "plugin.json"))
	if err != nil {
		t.Fatalf("reading plugin.json: %v", err)
	}
	if err := json.Unmarshal(raw, &executable); err != nil {
		t.Fatalf("parsing plugin.json: %v", err)
	}
	if executable.Executable == "" {
		t.Fatal("plugin.json has no `executable`; Grafana needs one to start the backend")
	}

	// Every `go build -o <path> ./cmd` in the plugin's published docs.
	buildLine := regexp.MustCompile(`go build -o (\S*` + regexp.QuoteMeta(executable.Executable) + `\S*)`)

	for _, page := range []string{"grafana-plugin.md", "grafana-plugin-publishing.md"} {
		body, err := os.ReadFile(filepath.Join(root, "docs-site", "docs", page))
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}

		matches := buildLine.FindAllStringSubmatch(string(body), -1)
		if len(matches) == 0 {
			t.Errorf("%s: no `go build -o ...%s` command found; if the build instructions moved, "+
				"move this check with them", page, executable.Executable)
			continue
		}

		suffixed := regexp.MustCompile(`^` + regexp.QuoteMeta(executable.Executable) + `_[a-z0-9]+_[a-z0-9]+$`)

		for _, m := range matches {
			// A shell expansion that builds the suffix is correct too, and is
			// checked before filepath.Base because expansions like ${t%/*}
			// contain a slash that Base would split on.
			if strings.Contains(m[1], "$") {
				continue
			}

			target := filepath.Base(m[1])
			if !suffixed.MatchString(target) {
				t.Errorf("%s: `go build -o ...%s` writes %q, but Grafana execs "+
					"%q_<goos>_<goarch>. A reader following this builds a plugin whose "+
					"backend cannot start.", page, executable.Executable, target, executable.Executable)
			}
		}
	}
}
