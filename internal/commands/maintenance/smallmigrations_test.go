package maintenance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffMigrateRebaselineDossierHashes(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes"},
		step{"migrate", "rebaseline-dossier-hashes", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(".kittify/dossiers/040-a/snapshot-latest.json", `{"mission_slug": "040-a", "parity_hash_sha256": "sha256:abc"}`)
		s.WriteFile("kitty-specs/041-b/.kittify/dossiers/041-b/snapshot-latest.json", `{"parity_hash_sha256": "sha256:def"}`)
		s.WriteFile("kitty-specs/042-c/.kittify/dossiers/x/snapshot-latest.json", `not json`)
	})
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes", "--dry-run"},
		step{"migrate", "rebaseline-dossier-hashes", "--json"},
	)
	// valid JSON that is not an object: upstream's data.get raises.
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/043-d/.kittify/dossiers/043-d/snapshot-latest.json", `[1, 2]`)
	})
	g, o := p.Run("migrate", "rebaseline-dossier-hashes", "--json")
	if g.Code != o.Code || g.Stdout != o.Stdout || lastLine(g.Stderr) != lastLine(o.Stderr) {
		t.Errorf("non-object snapshot: go=%d %q %q oracle=%d %q %q", g.Code, g.Stdout, lastLine(g.Stderr), o.Code, o.Stdout, lastLine(o.Stderr))
	}
}

// lastLine is the final non-blank line (an uncaught exception's message).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n "), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// legacyDossierCorpus writes recorded snapshots in the retired bare-hex form
// over missions of several types, so rebaseline re-indexes each source tree.
func legacyDossierCorpus(s *testkit.Sandbox) {
	old := `{"mission_slug": "%s", "parity_hash_sha256": "` + strings.Repeat("ab", 32) + `"}`
	snap := func(slug string) {
		s.WriteFile("kitty-specs/"+slug+"/.kittify/dossiers/"+slug+"/snapshot-latest.json", strings.Replace(old, "%s", slug, 1))
	}
	a := "kitty-specs/043-legacy/"
	s.WriteFile(a+"meta.json", `{"slug": "043-legacy", "mission_type": "software-dev"}`+"\n")
	s.WriteFile(a+"spec.md", "# Spec\n")
	s.WriteFile(a+"plan.md", "# Plan\n")
	s.WriteFile(a+"tasks.md", "# Tasks\n")
	s.WriteFile(a+"research.md", "r\n")
	s.WriteFile(a+"notes.txt", "unclassified\n")
	s.WriteFile(a+"a b_c.md", "spaces\n")
	s.WriteFile(a+"sub/test_thing.py", "x = 1\n")
	s.WriteFile(a+"sub/deep/requirements.txt", "req\n")
	s.WriteFile(a+"bin.dat", "\xff\xfe\x00")
	s.WriteFile(a+".hidden", "h\n")
	s.WriteFile(a+"checklists/.keep/x.md", "hidden dir\n")
	s.WriteFile(a+"tasks/WP01-a.md", "---\nwork_package_id: WP01\ntitle: A\nlane: done\nshell_pid: 12\n---\nbody\n")
	s.WriteFile(a+"tasks/WP02-stub.md", "no frontmatter\n")
	s.WriteFile(a+"tasks/README.md", "readme\n")
	snap("043-legacy")
	b := "kitty-specs/044-typeless/"
	s.WriteFile(b+"spec.md", "typeless\n")
	snap("044-typeless")
	c := "kitty-specs/045-research/"
	s.WriteFile(c+"meta.json", `{"slug": "045-research", "mission_type": "research"}`+"\n")
	s.WriteFile(c+"spec.md", "s\n")
	s.WriteFile(c+"findings.md", "f\n")
	snap("045-research")
	d := "kitty-specs/046-custom/"
	s.WriteFile(d+"meta.json", `{"slug": "046-custom", "mission_type": "custom-thing"}`+"\n")
	s.WriteFile(d+"report.md", "r\n")
	snap("046-custom")
	// a recorded snapshot whose dossier dir differs from the mission slug
	s.WriteFile("kitty-specs/047-doc/meta.json", `{"mission_type": "documentation"}`+"\n")
	s.WriteFile("kitty-specs/047-doc/spec.md", "d\n")
	s.WriteFile("kitty-specs/047-doc/.kittify/dossiers/other/snapshot-latest.json", `{"parity_hash_sha256": "deadbeef"}`)
}

func TestDiffMigrateRebaselineReindex(t *testing.T) {
	p := newOraclePair(t)
	p.Both(legacyDossierCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes", "--dry-run", "--json"},
	)
	compareRebaselineHashes(t, p)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes"},
	)
	if os.Getenv("KITTY_TEST_TRACE") != "" {
		for _, slug := range []string{"043-legacy", "044-typeless", "045-research", "046-custom"} {
			t.Logf("oracle %s:\n%s", slug, p.Oracle.ReadFile("kitty-specs/"+slug+"/.kittify/dossiers/"+slug+"/snapshot-latest.json"))
		}
	}
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes", "--json"},
	)
}

// compareRebaselineHashes compares the re-baselined hashes unmasked (they
// are content-addressed, so equal source trees must give equal hashes).
func compareRebaselineHashes(t *testing.T, p *testkit.Pair) {
	t.Helper()
	g, o := p.Run("migrate", "rebaseline-dossier-hashes", "--dry-run", "--json")
	hashes := func(s *testkit.Sandbox, out string) []string {
		obj, err := jsonx.DecodeObject([]byte(out))
		if err != nil {
			t.Fatalf("bad json: %v\n%s", err, out)
		}
		var hs []string
		rs, _ := obj.Get("results")
		for _, r := range rs.([]any) {
			ro := r.(*jsonx.Object)
			e, _ := ro.Get("error")
			hs = append(hs, ro.GetString("new_hash")+" "+strings.ReplaceAll(jsonx.Dumps(e, jsonx.Pretty), s.Dir, "<PROJ>"))
		}
		return hs
	}
	if gh, oh := strings.Join(hashes(p.Go, g.Stdout), "\n"), strings.Join(hashes(p.Oracle, o.Stdout), "\n"); gh != oh {
		t.Errorf("re-baselined hashes differ:\n%s", testkit.Diff(oh, gh))
	}
}

// TestDiffMigrateRebaselineReindexFailures covers per-mission re-index
// failures: an invalid artifact key, a schema-invalid WP, and org-pack
// manifest overrides (valid, schema-invalid, malformed).
func TestDiffMigrateRebaselineReindexFailures(t *testing.T) {
	p := newOraclePair(t)
	old := `{"parity_hash_sha256": "` + strings.Repeat("cd", 32) + `"}`
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/050-badkey/a+b.md", "x\n")
		s.WriteFile("kitty-specs/050-badkey/.kittify/dossiers/050-badkey/snapshot-latest.json", old)
		s.WriteFile("kitty-specs/051-badwp/tasks/WP01-x.md", "---\nwork_package_id: WP1\nlane: sideways\nbogus: 1\n---\n")
		s.WriteFile("kitty-specs/051-badwp/tasks/WP02-y.md", "---\ntitle: '   '\ndependencies: 5\nshell_pid: abc\n---\n")
		s.WriteFile("kitty-specs/051-badwp/.kittify/dossiers/051-badwp/snapshot-latest.json", old)
		s.WriteFile("kitty-specs/051-typeerr/tasks/WP01-x.md", "---\nwork_package_id: WP01\nshell_pid: [1]\n---\n")
		s.WriteFile("kitty-specs/051-typeerr/.kittify/dossiers/051-typeerr/snapshot-latest.json", old)
		s.WriteFile("kitty-specs/052-org/meta.json", `{"mission_type": "software-dev"}`)
		s.WriteFile("kitty-specs/052-org/spec.md", "s\n")
		s.WriteFile("kitty-specs/052-org/.kittify/dossiers/052-org/snapshot-latest.json", old)
		s.WriteFile("kitty-specs/053-orgbad/meta.json", `{"mission_type": "research"}`)
		s.WriteFile("kitty-specs/053-orgbad/.kittify/dossiers/053-orgbad/snapshot-latest.json", old)
		s.WriteFile("kitty-specs/054-orgmal/meta.json", `{"mission_type": "plan"}`)
		s.WriteFile("kitty-specs/054-orgmal/goals.md", "g\n")
		s.WriteFile("kitty-specs/054-orgmal/.kittify/dossiers/054-orgmal/snapshot-latest.json", old)
		s.WriteFile("kitty-specs/055-orgnull/meta.json", `{"mission_type": "documentation"}`)
		s.WriteFile("kitty-specs/055-orgnull/.kittify/dossiers/055-orgnull/snapshot-latest.json", old)
		s.WriteFile("orgpack/missions/software-dev/expected-artifacts.yaml", "mission_type: software-dev\nrequired_always:\n  - artifact_key: input.spec.main\n    artifact_class: input\n    path_pattern: spec.md\n    blocking: true\n  - artifact_key: evidence.ghost\n    artifact_class: evidence\n    path_pattern: ghost.md\n    blocking: 'no'\n")
		s.WriteFile("orgpack/missions/research/expected-artifacts.yaml", "schema_version: 1.0\nrequired_always:\n  - artifact_key: ''\n    artifact_class: weird\n    blocking: 7\n    extra: x\n  - just-a-string\nrequired_by_step: []\noptional_always: null\nbogus: true\n")
		s.WriteFile("orgpack/missions/plan/expected-artifacts.yaml", "- a\n- b\n")
		s.WriteFile("orgpack/missions/documentation/expected-artifacts.yaml", "")
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", cfg+"charter_packs:\n  org:\n    packs:\n      - name: acme\n        local_path: orgpack\n")
	})
	compareSteps(t, p, "", "",
		step{"migrate", "rebaseline-dossier-hashes", "--json"},
		step{"migrate", "rebaseline-dossier-hashes"},
	)
	compareRebaselineHashes(t, p)
	p.CompareTree("kitty-specs", true)
}

func TestDiffMigrateRepinHooks(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile(".git/hooks/pre-commit", "#!/bin/sh\necho foreign\n") })
	g, o := p.Run("migrate", "repin-hooks", "--json")
	if g.Code != 0 || o.Code != 0 {
		t.Fatalf("exit go=%d oracle=%d\n%s%s", g.Code, o.Code, g.Stderr, o.Stderr)
	}
	gj, _ := jsonx.DecodeObject([]byte(g.Stdout))
	oj, _ := jsonx.DecodeObject([]byte(o.Stdout))
	if strings.Join(gj.Keys(), ",") != strings.Join(oj.Keys(), ",") || gj.GetString("result") != oj.GetString("result") ||
		p.Go.Normalize(gj.GetString("hook_path")) != p.Oracle.Normalize(oj.GetString("hook_path")) {
		t.Errorf("payload differs:\n%s\n%s", g.Stdout, o.Stdout)
	}
	// a foreign hook is backed up once; ours is replaced without a backup
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		backups, _ := filepath.Glob(filepath.Join(s.Dir, ".git/hooks/pre-commit.*Z"))
		if len(backups) != 1 {
			t.Errorf("%s: backups %v", s.Dir, backups)
		}
		st, _ := os.Stat(filepath.Join(s.Dir, ".git/hooks/pre-commit"))
		if st == nil || st.Mode().Perm() != 0o700 {
			t.Errorf("hook mode %v", st)
		}
	}
	g, o = p.Run("migrate", "repin-hooks")
	if g.Code != o.Code {
		t.Errorf("second run: go=%d oracle=%d", g.Code, o.Code)
	}
}

func TestRepinHooksWithoutOracle(t *testing.T) {
	s := missionfx.NewProject(t)
	r := s.Run("migrate", "repin-hooks")
	if r.Code != 0 || !strings.Contains(r.Stdout, "repin-hooks summary") {
		t.Fatalf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	hook := s.ReadFile(".git/hooks/pre-commit")
	if !strings.Contains(hook, "# Generated by spec-kitty.") || !strings.Contains(hook, "commit-guard-hook") {
		t.Errorf("hook:\n%s", hook)
	}
}
