package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// The migrate walkers leave a kitty-specs/ residue folder (a committed child
// with no spec.md / meta.json) untouched and name it (#6627, #6669): an
// oracle-initialised project with one Mission-shaped folder and one residue.
func TestDiffMigrateWalkersLeaveResidueAlone(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(func(s *testkit.Sandbox) {
		if r := s.RunOracle("init", ".", "--ai", "codex", "--non-interactive"); r.Code != 0 {
			t.Fatalf("oracle init: %s%s", r.Stdout, r.Stderr)
		}
		s.WriteFile("kitty-specs/001-demo/spec.md", "# Demo\n")
		s.WriteFile("kitty-specs/001-demo/meta.json", `{"friendly_name": "demo", "mission_id": "01ABCDEFGHJKMNPQRSTVWXYZ00", "mission_number": "001", "mission_slug": "001-demo", "mission_type": "software-dev", "slug": "001-demo", "target_branch": "main"}`+"\n")
		s.WriteFile("kitty-specs/old-notes/tasks.md", "# Tasks\n\n- [ ] WP01 something\n")
		s.WriteFile("kitty-specs/old-notes/tasks/WP01-x.md", "---\nwork_package_id: WP01\ntitle: x\nlane: planned\n---\n# WP01\n")
		// A baseline-fabricated identity (#6668): allow-listed files only,
		// baseline slug fields, and a mission_id contradicting the slug mid8.
		s.WriteFile("kitty-specs/old-plan-01ABCDEF/meta.json", `{"friendly_name": "plan 01ABCDEF", "mission_id": "01ZZZZZZZZZZZZZZZZZZZZZZZZ", "mission_number": "", "mission_slug": "old-plan-01ABCDEF", "slug": "old-plan-01ABCDEF"}`+"\n")
		s.WriteFile("kitty-specs/old-plan-01ABCDEF/decisions/a.md", "x\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "fixture")
	})
	p.Compare(false, "doctor", "mission-state", "--audit")
	for _, args := range [][]string{
		{"migrate", "normalize-lifecycle", "--dry-run", "--json"},
		{"migrate", "backfill-runtime-state", "--dry-run", "--json"},
		{"migrate", "backfill-wp-status", "--dry-run"},
		{"doctor", "cutover", "--json"},
		{"doctor", "cutover"},
		{"doctor", "mission-state", "--audit", "--json"},
		{"migrate", "backfill-wp-status", "--json"},
		{"migrate", "backfill-runtime-state", "--json"},
		{"migrate", "normalize-lifecycle", "--json"},
	} {
		p.Compare(false, args...)
	}
	p.CompareTree("kitty-specs", true)
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		if s.Exists("kitty-specs/old-notes/meta.json") || s.Exists("kitty-specs/old-notes/status.events.jsonl") {
			t.Errorf("%s: a migrate walker wrote into the residue folder", s.Dir)
		}
	}
}
