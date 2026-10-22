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

// formerCheckout builds a nested git checkout shaped like a Spec Kitty
// source tree (pyproject + origin + tracked built-in mission.yaml).
func formerCheckout(s *testkit.Sandbox, rel, name, origin string, track, keepFile bool) string {
	mission := rel + "/packs/built-in/missions/software-dev/mission.yaml"
	s.WriteFile(rel+"/pyproject.toml", "[project]\nname = \""+name+"\"\nversion = \"1.0\"\n\n[project.urls]\nRepository = \"https://github.com/spec-kitty/spec-kitty\"\n")
	s.WriteFile(mission, "mission: software-dev\n")
	dir := filepath.Join(s.Dir, rel)
	git := func(args ...string) { s.Exec(dir, "git", args...) }
	git("init", "-q", "-b", "main")
	git("remote", "add", "origin", origin)
	if track {
		git("add", "-A")
	}
	if !keepFile {
		os.Remove(filepath.Join(s.Dir, mission))
	}
	return filepath.Join(s.Dir, mission)
}

func TestDiffDoctorProvenanceFormerCheckout(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		good := formerCheckout(s, "vendor/sk-good", "spec-kitty-cli", "git@github.com:spec-kitty/spec-kitty.git", true, true)
		missing := formerCheckout(s, "vendor/sk-missing", "spec-kitty-cli", "https://github.com/spec-kitty/spec-kitty", true, false)
		badName := formerCheckout(s, "vendor/sk-name", "other-cli", "https://github.com/spec-kitty/spec-kitty.git", true, true)
		badOrigin := formerCheckout(s, "vendor/sk-origin", "spec-kitty-cli", "https://example.com/spec-kitty/spec-kitty.git", true, true)
		untracked := formerCheckout(s, "vendor/sk-untracked", "spec-kitty-cli", "https://github.com/spec-kitty/spec-kitty.git", false, true)
		ref := func(sp string) string {
			return "  - id: TEMPLATE_SET:default\n    kind: template_set\n    source_path: '" + sp + "'\n"
		}
		s.WriteFile(".kittify/charter/charter.yaml", "catalog:\n  mission: software-dev\n  template_set: default\n  references:\n"+
			ref(good)+ref(missing)+ref(badName)+ref(badOrigin)+ref(untracked)+
			ref(`C:\old\packs\built-in\missions\software-dev\mission.yaml`)+ref(`\\srv\share\packs\built-in\missions\software-dev\mission.yaml`))
	})
	compareSteps(t, p, "", "",
		step{"doctor", "provenance"},
		step{"doctor", "provenance", "--json"},
	)
}

// TestDiffDoctorProvenanceBadPacksRoot: a SPEC_KITTY_PACKS_ROOT that is not
// a directory is ignored (with a warning) before classification.
func TestDiffDoctorProvenanceBadPacksRoot(t *testing.T) {
	p := newOraclePair(t)
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		s.Env = append(s.Env, "SPEC_KITTY_PACKS_ROOT=/nonexistent-packs-root")
	}
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/charter/charter.yaml", "catalog:\n  mission: software-dev\n  template_set: default\n  references:\n"+
			"  - id: DIRECTIVE:x\n    kind: directive\n    source_path: /nonexistent-packs-root/built-in/directives/x.yaml\n")
	})
	// stdout + exit only: upstream's UserWarning lines carry site-packages
	// source frames (irreducible).
	for _, args := range [][]string{{"doctor", "provenance"}, {"doctor", "provenance", "--json"}} {
		p.Compare(false, args...)
	}
}

func TestDiffDoctorProvenanceFindings(t *testing.T) {
	p := newOraclePair(t)
	root := oraclePacksRoot(t)
	// classify against the same pack root on both sides (the Go binary
	// embeds its packs; it has no installed tree of its own)
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		s.Env = append(s.Env, "SPEC_KITTY_PACKS_ROOT="+filepath.Dir(root))
	}
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
