package doctrine_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func seedGlossary(t *testing.T, s *testkit.Sandbox) {
	t.Helper()
	for _, f := range []string{"team_domain.yaml", "spec_kitty_core.yaml", "bad.yaml"} {
		s.WriteFile(".kittify/glossaries/"+f, td(t, "glossary/"+f))
	}
	s.WriteFile(".kittify/events/glossary/m1.events.jsonl", td(t, "glossary/m1.events.jsonl"))
	s.WriteFile("docs/development/docs-retrieval-index.yaml", td(t, "docs-index.yaml"))
}

func TestGlossaryGoOnly(t *testing.T) {
	s := newProject(t)
	if r := s.Run("glossary", "list", "--json"); r.Code != 1 || !strings.Contains(r.Stdout, "glossary_not_initialized") {
		t.Fatalf("uninitialized: %d %s", r.Code, r.Stdout)
	}
	seedGlossary(t, s)
	r := s.MustRun("glossary", "list", "--json")
	if !strings.Contains(r.Stdout, `"surface": "runtime"`) || !strings.Contains(r.Stdout, `"confidence": 0.25`) {
		t.Fatalf("list: %s", r.Stdout)
	}
	r = s.MustRun("glossary", "conflicts", "--unresolved", "--json")
	if !strings.Contains(r.Stdout, "11111111-2222-3333-4444-555555555555") || strings.Contains(r.Stdout, "66666666") {
		t.Fatalf("conflicts: %s", r.Stdout)
	}
	r = s.Run("glossary", "validate", ".kittify/glossaries/bad.yaml", "--json")
	if r.Code != 1 || !strings.Contains(r.Stdout, "must have 'surface' key") {
		t.Fatalf("validate: %d %s", r.Code, r.Stdout)
	}
	r = s.MustRun("docs", "query", "worktree", "--json")
	if !strings.Contains(r.Stdout, `"path": "docs/adr/`) {
		t.Fatalf("docs query: %s", r.Stdout)
	}
}

func TestParityGlossaryAndDocs(t *testing.T) {
	p := newOraclePair(t)
	compareMasked(t, p, true, "glossary", "list", "--json")
	compareMasked(t, p, true, "docs", "query", "lane")
	p.Both(func(s *testkit.Sandbox) { seedGlossary(t, s) })
	for _, args := range [][]string{
		{"glossary", "list"},
		{"glossary", "list", "--json"},
		{"glossary", "list", "--scope", "team_domain", "--json"},
		{"glossary", "list", "--status", "active"},
		{"glossary", "list", "--scope", "nope", "--json"},
		{"glossary", "conflicts"},
		{"glossary", "conflicts", "--json"},
		{"glossary", "conflicts", "--mission", "m2", "--json"},
		{"glossary", "conflicts", "--strictness", "bogus"},
		{"glossary", "show", "workspace"},
		{"glossary", "show", "glossary:lane"},
		{"glossary", "show", "nope"},
		{"glossary", "validate", ".kittify/glossaries/team_domain.yaml", "--json"},
		{"glossary", "validate", ".kittify/glossaries/bad.yaml"},
		{"glossary", "validate", ".kittify/glossaries/bad.yaml", "--json"},
		{"glossary", "validate", ".kittify/glossaries"},
		{"glossary", "validate", ".kittify/glossaries", "--json"},
		{"glossary", "resolve", "00000000-0000-0000-0000-000000000000"},
		{"docs", "query", "worktree"},
		{"docs", "query", "worktree", "--json"},
		{"docs", "query", "branch", "--divio-type", "none", "--json"},
		{"docs", "query", "zzzz"},
	} {
		compareMasked(t, p, false, args...)
	}
}
