package docs

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const index = `# GENERATED — do not edit by hand; run scripts/docs/docs_index.py --write to refresh.
# Common Docs retrieval index: title / heading-anchor / abstract per page.
# Sorted alphabetically by ` + "`path`" + ` for a deterministic, byte-stable diff.
pages:
  - path: "docs/explanation/missions-and-work-packages-in-a-very-long-directory-name/overview.md"
    title: "Missions, Work Packages and the Lane State Machine — a guided explanation for new contributors"
    divio_type: "explanation"
    abstract: "How missions decompose into work packages and how lanes move."
    anchors:
    - {slug: "lanes", text: "Lanes", level: 2}
    - {slug: "work-packages", text: "Work packages", level: 3}
    - {slug: "review-gate", text: "Review gate for packages", level: "x"}
  - path: "docs/how-to/install.md"
    title: "Install Spec Kitty"
    divio_type: "how-to"
    abstract: "Install the CLI with pipx or uv. Mentions packages."
    anchors: []
  - path: "docs/reference/cli.md"
    title: "CLI reference"
    divio_type: "reference"
    abstract: "Every command, flag and exit code."
    anchors:
    - {slug: "spec-kitty-next", text: "spec-kitty next", level: 2}
    - {slug: "lanes", text: "Lane commands", level: 2}
  - path: 42
    title: "Ünïcode “quoted” page"
    divio_type: "tutorial"
    abstract: "Non-ASCII text."
  - "not a mapping"
`

func withIndex(s *testkit.Sandbox) {
	s.WriteFile("docs/development/docs-retrieval-index.yaml", index)
}

func TestDocsQueryParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Compare(true, "docs", "query", "lanes")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("docs/README.md", "# docs\n") })
	p.Compare(true, "docs", "query", "lanes")
	p.Compare(true, "docs", "query", "   ")
	p.Compare(true, "docs", "query", "x", "--divio-type", "guide")
	p.Compare(true, "docs", "query")
	p.Both(withIndex)
	for _, args := range [][]string{
		{"lanes"},
		{"LANES", "--json"},
		{"packages", "--json"},
		{"packages"},
		{"packages", "--divio-type", "how-to", "--json"},
		{"lane", "--section", "lanes", "--json"},
		{"cli", "--section", "lanes"},
		{"ünïcode", "--json"},
		{"ünïcode"},
		{"nothing-matches"},
		{"nothing-matches", "--json"},
		{" review "},
	} {
		p.Compare(true, append([]string{"docs", "query"}, args...)...)
	}
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("docs/development/docs-retrieval-index.yaml", "pages: [\n") })
	g, o := p.Compare(false, "docs", "query", "x")
	const pre = "Error: could not read or parse the docs retrieval index at docs/development/docs-retrieval-index.yaml: "
	if !strings.HasPrefix(g.Stderr, pre) || !strings.HasPrefix(o.Stderr, pre) {
		t.Errorf("go=%q oracle=%q", g.Stderr, o.Stderr)
	}
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("docs/development/docs-retrieval-index.yaml", "- a\n") })
	p.Compare(true, "docs", "query", "x", "--json")
}

func TestDocsQueryE2E(t *testing.T) {
	s := testkit.NewSandbox(t)
	withIndex(s)
	r := s.Run("docs", "query", "cli", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"path": "docs/reference/cli.md"`) {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
}
