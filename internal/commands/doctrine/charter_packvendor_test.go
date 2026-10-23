package doctrine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestParityPackVendor(t *testing.T) {
	p := testkit.NewPair(t)
	const descriptor = "pack_id: 01M00000000000000000000000\npack_version: '1.0.0'\nname: Fixture\npack_level: built-in\nrequires_core: '>=4.0.0rc1,<5'\n"
	p.Both(func(s *testkit.Sandbox) {
		s.Env = append(s.Env, "SPEC_KITTY_PACKS_ROOT="+filepath.Join(s.Dir, "packs"), "GIT_AUTHOR_DATE=2026-10-23T12:00:00Z", "GIT_COMMITTER_DATE=2026-10-23T12:00:00Z")
		for _, root := range []string{"remote/built-in", "packs/built-in"} {
			s.WriteFile(root+"/pack.yaml", descriptor)
			s.WriteFile(root+"/README.md", "Fixture tree.\n")
		}
		for _, args := range [][]string{{"-C", "remote", "init", "-q", "-b", "fixture"}, {"-C", "remote", "add", "."}, {"-C", "remote", "commit", "-qm", "fixture"}} {
			result := s.Exec("", "git", args...)
			if result.Code != 0 {
				t.Fatalf("git: %s", result.Stderr)
			}
		}
		s.WriteFile("packs/built-in.source.yaml", "repository: file://"+filepath.Join(s.Dir, "remote")+"\ncommit: "+strings.Repeat("0", 40)+"\nsubtree: built-in\ntree_digest:\n  algorithm: clean-tree-sha256\n  value: initial\n  level: built-in\nsnapshot_sha256: initial\n")
	})
	for _, args := range [][]string{
		{}, {"--check", "--ref", "fixture"}, {"--pack", "Bad_Name"}, {"--subtree", "../escape"}, {"--check"}, {"--ref", "missing"}, {"--ref", "fixture"}, {"--check"},
	} {
		p.Compare(false, append([]string{"charter", "pack", "vendor"}, args...)...)
	}
	for _, rel := range []string{"packs/built-in.source.yaml", "packs/built-in/pack.yaml", "packs/built-in/README.md"} {
		if g, o := p.Go.Normalize(p.Go.ReadFile(rel)), p.Oracle.Normalize(p.Oracle.ReadFile(rel)); g != o {
			t.Errorf("%s: %s", rel, testkit.Diff(o, g))
		}
	}
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("packs/built-in/README.md", "Operator edit.\n") })
	p.Compare(false, "charter", "pack", "vendor", "--check")
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("packs/built-in/__pycache__/ignored.pyc", "bytecode") })
	p.Compare(false, "charter", "pack", "vendor", "--ref", "fixture")
	p.Both(func(s *testkit.Sandbox) {
		os.Remove(filepath.Join(s.Dir, "packs/built-in/README.md"))
		if err := os.Symlink("pack.yaml", filepath.Join(s.Dir, "packs/built-in/README.md")); err != nil {
			t.Fatal(err)
		}
	})
	p.Compare(false, "charter", "pack", "vendor", "--check")
	if g, o := p.Go.Normalize(p.Go.ReadFile("packs/built-in.source.yaml")), p.Oracle.Normalize(p.Oracle.ReadFile("packs/built-in.source.yaml")); g != o {
		t.Errorf("pin: %s", testkit.Diff(o, g))
	}
}
