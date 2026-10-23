package doctrine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestParityPresetResolution covers `charter activate --preset`'s id
// resolution: anti-pattern ids checked against the merged, validated DRG
// (and the refusal when that DRG cannot be loaded), mission types against
// the layered roster plus the empty-sequence refusal, the strict org-pack
// chain, and the org charter's required ids unioned into the written keys.
func TestParityPresetResolution(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/pack.yaml", "pack_id: 01KAAAAAAAAAAAAAAAAAAAAAAA\nname: acme\npack_level: org\npack_version: 1.2.3\n")
		s.WriteFile("orgpack/presets/ap.yaml", "name: ap\ndescription: anti-patterns\nactivated_anti_patterns:\n  - anemic-domain-model\n  - nope-ap\nactivated_tactics: [acceptance-test-first, nope-tactic]\n")
		s.WriteFile("orgpack/presets/ok.yaml", "name: ok\ndescription: ok\nactivated_anti_patterns: [anemic-domain-model]\nactivated_directives: [010-specification-fidelity-requirement]\nactivated_kinds: [directives, anti_patterns]\n")
		s.WriteFile("orgpack/presets/mt.yaml", "name: mt\ndescription: mission types\nmission_type_activations: [software-dev, bare, nope-mt]\n")
		s.WriteFile(".kittify/charter-packs/README.md", "project pack\n")
		s.WriteFile(".kittify/missions/mission_types/bare.yaml", "schema_version: 1\nid: bare\ndisplay_name: Bare\n")
		appendConfig(s, "charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: orgpack\n")
	})
	run := func(preset string) {
		t.Helper()
		compareCharter(t, p, "charter", "activate", "--pack", "acme", "--preset", preset, "--force", "--no-compile")
		compareCharter(t, p, "charter", "activate", "--pack", "acme", "--preset", preset, "--force", "--no-compile", "--json")
	}
	run("ap")
	run("mt")
	run("ok")
	// A dangling project DRG edge: the anti-pattern check cannot load the DRG.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter-packs/zz.graph.yaml", "schema_version: '1.0'\ngenerated_at: x\ngenerated_by: y\nnodes:\n  - urn: tactic:zz-local\n    kind: tactic\nedges:\n  - source: tactic:zz-local\n    target: tactic:zz-missing\n    relation: requires\n")
	})
	run("ok")
	p.Both(func(s *testkit.Sandbox) { os.Remove(filepath.Join(s.Dir, ".kittify/charter-packs/zz.graph.yaml")) })
	// A malformed mission-type file: the roster cannot be read.
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/missions/mission_types/zz.yaml", "- a\n") })
	run("mt")
	p.Both(func(s *testkit.Sandbox) { os.Remove(filepath.Join(s.Dir, ".kittify/missions/mission_types/zz.yaml")) })
	// The org charter's required ids are unioned into the written keys.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("orgpack/org-charter.yaml", "schema_version: \"2\"\norg_name: Acme\nrequired_directives: [DIRECTIVE_024, 010-specification-fidelity-requirement]\nrequired_tactics: [acceptance-test-first]\n")
	})
	run("ok")
	// A declared org pack that is not fetched: ids cannot be checked.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/config.yaml", strings.Replace(s.ReadFile(".kittify/config.yaml"), "      local_path: orgpack\n", "      local_path: orgpack\n    - name: ghost\n      local_path: nowhere\n", 1))
	})
	run("ok")
	run("mt")
}
