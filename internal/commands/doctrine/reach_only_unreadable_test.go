package doctrine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// #6454: an unreadable synthesis manifest falls back to the created-artifact
// record, and fails closed (SYNTHESIS_MEMBERSHIP_UNREADABLE, rendered with
// its body by charter context) when the record cannot stand in.
func TestParityReachOnlyUnreadableManifest(t *testing.T) {
	const manifest = ".kittify/charter/synthesis-manifest.yaml"
	const record = ".kittify/charter/synthesized-artifacts.yaml"
	// A synthesized directive -> tactic edge, so the tactic is reach-only
	// and charter context consults the synthesis membership.
	const tacticEdge = "- source: directive:DIRECTIVE_030\n  target: tactic:review-policy-tactic\n  relation: suggests\n" +
		"  reason: Synthesized tactic 'review-policy-tactic' is how this project applies the directive\n"
	corrupt := func(s *testkit.Sandbox) { s.WriteFile(manifest, "artifacts: [\n  {kind: broken\n") }
	scenarios := map[string]func(s *testkit.Sandbox){
		"corrupt-record-stands-in": corrupt,
		"corrupt-no-record": func(s *testkit.Sandbox) {
			corrupt(s)
			os.Remove(filepath.Join(s.Dir, record))
		},
		"dangling-no-record": func(s *testkit.Sandbox) {
			p := filepath.Join(s.Dir, manifest)
			os.Remove(p)
			if err := os.Symlink(filepath.Join(s.Dir, "no-such-target.yaml"), p); err != nil {
				t.Fatal(err)
			}
			os.Remove(filepath.Join(s.Dir, record))
		},
		"corrupt-record-lists-no-tactic": func(s *testkit.Sandbox) {
			corrupt(s)
			s.WriteFile(record, "created_tactics: []\ncreated_styleguides:\n  - other-style\n")
		},
	}
	for name, damage := range scenarios {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newSynthPair(t)
			p.Both(func(s *testkit.Sandbox) {
				for name, content := range synthFiles {
					s.WriteFile(name, content)
				}
			})
			compareSynth(t, p, false, "charter", "synthesize", "--json", "--skip-code-evidence", "--skip-corpus")
			p.Both(func(s *testkit.Sandbox) {
				s.WriteFile(".kittify/charter-packs/graph.yaml", s.ReadFile(".kittify/charter-packs/graph.yaml")+tacticEdge)
				damage(s)
			})
			compareCharter(t, p, "charter", "context", "--action", "implement", "--mission-type", "software-dev", "--json")
			compareCharter(t, p, "charter", "context", "--action", "implement", "--mission-type", "software-dev")
			// charter generate is not compared: upstream refuses a corrupt
			// manifest earlier on its own (a pre-existing, separate gap).
		})
	}
}
