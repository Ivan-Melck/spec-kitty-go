package doctrine_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

var (
	reLintScanned  = regexp.MustCompile(`"scanned_at": "[^"]*"`)
	reLintDuration = regexp.MustCompile(`"duration_seconds": [0-9.e-]+`)
	reLintIn       = regexp.MustCompile(`in [0-9]+\.[0-9]+s`)
)

func maskLint(s string) string {
	s = reLintScanned.ReplaceAllString(s, `"scanned_at": "<T>"`)
	s = reLintDuration.ReplaceAllString(s, `"duration_seconds": <D>`)
	return reLintIn.ReplaceAllString(s, "in <D>s")
}

func compareLint(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g, o := p.Run(args...)
	if g.Code != o.Code {
		t.Errorf("%v: exit go=%d oracle=%d\n%s\n%s", args, g.Code, o.Code, g.Stdout, o.Stdout)
	}
	if gs, os_ := maskLint(p.Go.Normalize(g.Stdout)), maskLint(p.Oracle.Normalize(o.Stdout)); gs != os_ {
		t.Errorf("%v: stdout differs\n%s", args, testkit.Diff(os_, gs))
	}
	gr, or := maskLint(p.Go.Normalize(p.Go.ReadFile(".kittify/lint-report.json"))), maskLint(p.Oracle.Normalize(p.Oracle.ReadFile(".kittify/lint-report.json")))
	if gr != or {
		t.Errorf("%v: lint-report.json differs\n%s", args, testkit.Diff(or, gr))
	}
}

const lintProjectGraph = `schema_version: "1.0"
generated_at: "2024-01-01T00:00:00Z"
generated_by: test
nodes:
  - urn: directive:DIRECTIVE_001
    kind: directive
    label: Orphan One
  - urn: directive:DIRECTIVE_002
    kind: directive
  - urn: glossary_scope:a
    kind: glossary_scope
    label: Term
  - urn: glossary_scope:b
    kind: glossary_scope
    label: " term "
  - urn: action:x/specify
    kind: action
edges:
  - source: action:x/specify
    target: directive:DIRECTIVE_002
    relation: requires
  - source: action:x/specify
    target: tactic:missing
    relation: suggests
  - source: action:x/specify
    target: glossary_scope:a
    relation: vocabulary
`

func TestParityCharterLint(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	compareLint(t, p, "charter", "lint")
	compareLint(t, p, "charter", "lint", "--json")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter-packs/graph.yaml", lintProjectGraph) })
	for _, args := range [][]string{
		{"charter", "lint"}, {"charter", "lint", "--json"}, {"charter", "lint", "--orphans"},
		{"charter", "lint", "--contradictions", "--json"}, {"charter", "lint", "--stale"},
		{"charter", "lint", "--severity", "high"}, {"charter", "lint", "--mission", "001-x", "--json"},
	} {
		compareLint(t, p, args...)
	}
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter-packs/graph.yaml", "bad: [\n") })
	compareLint(t, p, "charter", "lint")
}

func TestParityCharterLintOrg(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\npack_level: org\n")
		s.WriteFile("orgpack/drg/fragment.yaml", "nodes:\n  - id: acme-rule\n    kind: directives\nedges:\n  - source: acme-rule\n    target: tactic:acme-tactic\n    relation: requires\n")
		s.WriteFile("orgpack/directives/010-x.directive.yaml", "id: DIRECTIVE_010\nschema_version: \"1.0\"\ntitle: Overridden\nintent: Org override\nenforcement: required\n")
		s.WriteFile("orgpack/org-charter.yaml", "governance_policies:\n  - field: testing_philosophy\n    value: tdd\n  - field: absent_field\n    value: x\n")
		s.WriteFile(".kittify/charter/interview/answers.yaml", "answers:\n  testing_philosophy: bdd\n")
		appendConfig(s, orgPackConfig)
	})
	compareLint(t, p, "charter", "lint")
	compareLint(t, p, "charter", "lint", "--json")
	// A bare id against the empty built-in is an unresolved endpoint.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/drg/fragment.yaml", "nodes:\n  - id: acme-rule\n    kind: directives\nedges:\n  - source: acme-rule\n    target: DIRECTIVE_010\n    relation: requires\n")
	})
	compareLint(t, p, "charter", "lint")
	compareLint(t, p, "charter", "lint", "--json")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("orgpack/drg/fragment.yaml", "") })
	p.Both(func(s *testkit.Sandbox) { s.Exec("", "rm", "-rf", s.Dir+"/orgpack/drg") })
	compareLint(t, p, "charter", "lint")
	compareLint(t, p, "charter", "lint", "--json")
}

func TestCharterLintGoOnly(t *testing.T) {
	s := newProject(t)
	r := s.Run("charter", "lint", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"graph_state": "built_in_only"`) {
		t.Fatalf("lint: %d %s", r.Code, r.Stdout)
	}
	s.WriteFile(".kittify/charter-packs/graph.yaml", lintProjectGraph)
	r = s.Run("charter", "lint", "--json")
	for _, want := range []string{`"graph_state": "merged"`, `"orphaned_directive"`, `"duplicate_glossary_sense"`, `"dangling_edge"`, `"finding_count": 4`} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("missing %s:\n%s", want, r.Stdout)
		}
	}
	if !s.Exists(".kittify/lint-report.json") {
		t.Fatal("lint report not persisted")
	}
}
