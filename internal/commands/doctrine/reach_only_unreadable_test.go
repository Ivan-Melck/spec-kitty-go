package doctrine_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// #6454: an unreadable synthesis manifest falls back to the created-artifact
// record, and fails closed (SYNTHESIS_MEMBERSHIP_UNREADABLE, rendered with
// its body by charter context) when the record cannot stand in. charter
// generate additionally fails on the manifest restamp after charter.yaml is
// rewritten (an "Unexpected error:" carrying ruamel's parse error).
func TestParityReachOnlyUnreadableManifest(t *testing.T) {
	const manifest = ".kittify/charter/synthesis-manifest.yaml"
	const record = ".kittify/charter/synthesized-artifacts.yaml"
	// A synthesized directive -> tactic edge, so the tactic is reach-only
	// and charter context consults the synthesis membership.
	const tacticEdge = "- source: directive:DIRECTIVE_030\n  target: tactic:review-policy-tactic\n  relation: suggests\n" +
		"  reason: Synthesized tactic 'review-policy-tactic' is how this project applies the directive\n"
	corrupt := func(s *testkit.Sandbox) { s.WriteFile(manifest, "artifacts: [\n  {kind: broken\n") }
	scenarios := map[string]func(s *testkit.Sandbox){
		"intact":                   func(*testkit.Sandbox) {},
		"corrupt-record-stands-in": corrupt,
		// Valid YAML, invalid schema: a pydantic ValidationError (a
		// ValueError), which generate renders as "Error:".
		"invalid-record-stands-in": func(s *testkit.Sandbox) { s.WriteFile(manifest, "artifacts: 5\n") },
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
			// charter generate rewrites charter.yaml, then restamps the
			// manifest's bundle_content_hash (#6366); a corrupt manifest
			// fails that restamp with ruamel's error, unstaged and with
			// nothing after it (no .gitignore, no staging) done.
			goManifest, oracleManifest := p.Go.ReadFile(manifest), p.Oracle.ReadFile(manifest)
			compareSynth(t, p, false, "charter", "generate", "--json")
			if g, o := p.Go.ReadFile(manifest) != goManifest, p.Oracle.ReadFile(manifest) != oracleManifest; g != o {
				t.Errorf("generate restamped the manifest: go=%v oracle=%v", g, o)
			}
			compareSynth(t, p, false, "charter", "generate")
			for _, rel := range []string{".kittify/charter/charter.md", manifest, ".gitignore"} {
				compareSynthFile(t, p, rel)
			}
			if g, o := maskCharter(p.Go.ReadFile(".kittify/charter/charter.yaml")), maskCharter(p.Oracle.ReadFile(".kittify/charter/charter.yaml")); g != o {
				t.Errorf("charter.yaml differs\n%s", testkit.Diff(o, g))
			}
			if g, o := p.Go.Git("status", "--porcelain"), p.Oracle.Git("status", "--porcelain"); g != o {
				t.Errorf("git status differs\n%s", testkit.Diff(o, g))
			}
		})
	}
}
