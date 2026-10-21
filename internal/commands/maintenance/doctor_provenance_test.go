package maintenance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// oraclePacksRoot is the oracle install's built-in pack root (the root
// upstream's is_built_in_pack_path classifies against).
func oraclePacksRoot(t *testing.T) string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(testkit.Oracle(t))), "oracle-venv", "lib", "python3*", "site-packages", "packs", "built-in"))
	if len(m) == 0 {
		t.Skip("oracle pack root not found")
	}
	return m[0]
}

func TestDiffDoctorProvenanceFindings(t *testing.T) {
	p := newOraclePair(t)
	root := oraclePacksRoot(t)
	directive := filepath.Join(root, "missions", "software-dev", "mission.yaml")
	if _, err := os.Stat(directive); err != nil {
		t.Skip("oracle pack layout differs")
	}
	charter := "catalog:\n  mission: software-dev\n  template_set: default\n  references:\n" +
		"  - id: DIRECTIVE:x\n    kind: directive\n    source_path: " + directive + "\n" +
		"  - id: DIRECTIVE:y\n    kind: directive\n    source_path: /gone/install/packs/built-in/directives/y.yaml\n" +
		"  - id: DIRECTIVE:z\n    kind: directive\n    source_path: ${SPEC_KITTY_PACKS_ROOT}/built-in/directives/z.yaml\n" +
		"  - id: TEMPLATE_SET:default\n    kind: template_set\n    source_path: " + directive + "\n" +
		"  - id: TEMPLATE_SET:default\n    kind: template_set\n    source_path: /old/checkout/packs/built-in/missions/software-dev/mission.yaml\n" +
		"  - id: TEMPLATE_SET:other\n    kind: template_set\n    source_path: /x/packs/built-in/missions/software-dev/mission.yaml\n"
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter/charter.yaml", charter) })
	compareSteps(t, p, "", "",
		step{"doctor", "provenance"},
		step{"doctor", "provenance", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".kittify/charter/charter.yaml", "catalog: [unclosed\n") })
	compareSteps(t, p, "", "",
		step{"doctor", "provenance"},
		step{"doctor", "provenance", "--json"},
	)
}
