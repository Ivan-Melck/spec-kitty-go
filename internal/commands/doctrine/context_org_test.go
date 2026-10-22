package doctrine_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

const cleanAcmeFragment = `nodes:
  - id: acme-rule
    kind: directives
    title: Acme Rule
edges:
  - source: acme-rule
    target: tactic:acme-tactic
    relation: requires
`

// TestParityCharterContextOrgSelectionsAndActivations covers the bootstrap
// selection block's org provenance suffix (DOC-01) and the
// "Selected activations:" stanza from charter.yaml governance.activations
// and org-charter activations (DOC-02), plus a malformed org entry.
func TestParityCharterContextOrgSelectionsAndActivations(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	compareCharter(t, p, "charter", "interview", "--defaults")
	compareCharter(t, p, "charter", "generate")
	p.Both(func(s *testkit.Sandbox) {
		writeOrgPack(s, cleanAcmeFragment)
		s.WriteFile("orgpack/org-charter.yaml", "schema_version: \"2\"\norg_name: Acme\nrequired_tactics: [acme-tactic]\nactivations:\n  - activation_context: {mission_type: software-dev, action: implement}\n    charter_pack_id: acme\n    artifact_id: acme-tactic\n  - activation_context: {action: write_comment}\n    charter_pack_id: acme\n    artifact_id: DIRECTIVE_010\n    artifact_kind: directive\n")
		appendConfig(s, orgPackConfig)
		y := s.ReadFile(".kittify/charter/charter.yaml")
		y = strings.Replace(y, "    selected_directives: []\n", "    selected_directives:\n    - DIRECTIVE_010\n    - DIRECTIVE_001\n", 1)
		y = strings.Replace(y, "    selected_tactics: []\n", "    selected_tactics:\n    - acme-tactic\n    - acceptance-test-first\n", 1)
		y = strings.Replace(y, "  activations: []\n", "  activations:\n  - activation_context:\n      mission_type: any\n    charter_pack_id: built-in\n    artifact_id: acceptance-test-first\n  - activation_context:\n      mission_type: research\n    charter_pack_id: built-in\n    artifact_id: DIRECTIVE_001\n", 1)
		s.WriteFile(".kittify/charter/charter.yaml", y)
	})
	for _, args := range [][]string{
		{"charter", "context", "--action", "implement", "--mission-type", "software-dev", "--no-mark-loaded", "--json"},
		{"charter", "context", "--action", "review", "--mission-type", "software-dev", "--no-mark-loaded", "--json"},
		{"charter", "context", "--action", "specify", "--no-mark-loaded", "--json"},
	} {
		compareCharter(t, p, args...)
	}
	// A malformed org activations entry fails the render.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/org-charter.yaml", "schema_version: \"2\"\nactivations:\n  - activation_context: {action: dance}\n    charter_pack_id: acme\n    artifact_id: x\n    bogus: 1\n")
	})
	compareCharter(t, p, "charter", "context", "--action", "implement", "--mission-type", "software-dev", "--no-mark-loaded", "--json")
	compareCharter(t, p, "charter", "context", "--action", "implement", "--mission-type", "software-dev", "--no-mark-loaded")
}
