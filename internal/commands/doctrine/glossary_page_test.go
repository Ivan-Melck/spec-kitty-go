package doctrine_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const entityGraph = `schema_version: "1.0"
generated_at: "2026-01-01T00:00:00+00:00"
generated_by: test
nodes:
- urn: glossary:lane
  kind: glossary
  label: Lane
  provenance: mission-042
- urn: glossary:work-package
  kind: glossary
- urn: directive:DIRECTIVE_024
  kind: directive
  label: Locality
- urn: action:software-dev/implement
  kind: action
edges:
- source: directive:DIRECTIVE_024
  target: glossary:lane
  relation: vocabulary
- source: action:software-dev/implement
  target: glossary:lane
  relation: vocabulary
`

// TestParityGlossaryShowEntityPages covers compiled entity pages (DOC-14):
// page file contents and the rich Markdown render.
func TestParityGlossaryShowEntityPages(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/graph.yaml", entityGraph)
		s.WriteFile(".kittify/events/glossary/m.events.jsonl", `{"event_type": "semantic_check_evaluated", "term": "lane", "severity": "high", "conflict_type": "ambiguous", "checked_at": "2026-01-01T00:00:00Z"}`+"\n")
	})
	for _, args := range [][]string{
		{"glossary", "show", "lane"},
		{"glossary", "show", "glossary:work-package"},
		{"glossary", "show", "Work-Package"},
		{"glossary", "show", "nope"},
	} {
		compareMasked(t, p, true, args...)
	}
	p.CompareTree(".kittify/charter/compiled", true)
}
