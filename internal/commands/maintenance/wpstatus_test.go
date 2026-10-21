package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func wpStatusCorpus(s *testkit.Sandbox) {
	a := "kitty-specs/060-gap/"
	s.WriteFile(a+"meta.json", `{"mission_id": "01JHHHHHHHHHHHHHHHHHHHHHHH", "slug": "060-gap", "created_at": "2025-05-01T12:00:00Z"}`+"\n")
	s.WriteFile(a+"tasks/WP01-a.md", "---\nwork_package_id: WP01\ntitle: A\n---\nbody\n")
	s.WriteFile(a+"tasks/WP02-b.md", "---\nwork_package_id: WP02\ntitle: B\n---\nbody\n")
	s.WriteFile(a+"tasks/WP03-bad.md", "---\nwork_package_id: WP03\nbogus_key: 1\n---\nbody\n")
	s.WriteFile(a+"status.events.jsonl", `{"actor": "x", "at": "2025-05-02T00:00:00+00:00", "event_id": "01JHHHHHHHHHHHHHHHHHHHHHH1", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "060-gap", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP01"}`+"\n"+
		`{"actor": "x", "at": "2025-05-02T00:00:00+00:00", "event_id": "01JHHHHHHHHHHHHHHHHHHHHHH2", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "060-gap", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP09"}`+"\n")
	s.WriteFile(a+"status.json", "{}\n")
	b := "kitty-specs/061-merged/"
	s.WriteFile(b+"meta.json", `{"slug": "061-merged", "merged_at": "2025-06-01T00:00:00+00:00"}`+"\n")
	s.WriteFile(b+"tasks/WP01-x.md", "---\nwork_package_id: WP01\n---\n")
	c := "kitty-specs/062-manifest/"
	s.WriteFile(c+"meta.json", `{"slug": "062-manifest"}`+"\n")
	s.WriteFile(c+"tasks/WP01-x.md", "---\nwork_package_id: WP01\n---\n")
	s.WriteFile("kitty-specs/063-notasks/meta.json", `{"slug": "063-notasks"}`+"\n")
	s.WriteFile("evidence.yaml", "missions:\n  062-manifest:\n    reason: \"PR #1 merged\"\n  060-gap:\n    reason: late\n")
	s.WriteFile("bad.yaml", "missions:\n  nope-mission:\n    reason: x\n  062-manifest: {}\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "wp-status corpus")
}

func TestDiffMigrateBackfillWPStatus(t *testing.T) {
	p := newOraclePair(t)
	p.Both(wpStatusCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-wp-status", "--evidence-manifest", "bad.yaml"},
		step{"migrate", "backfill-wp-status", "--evidence-manifest", "bad.yaml", "--json"},
		step{"migrate", "backfill-wp-status", "--evidence-manifest", "missing.yaml", "--json"},
		step{"migrate", "backfill-wp-status", "--mission", "nope"},
		step{"migrate", "backfill-wp-status", "--dry-run", "--json"},
		step{"migrate", "backfill-wp-status", "--dry-run", "--evidence-manifest", "evidence.yaml"},
		step{"migrate", "backfill-wp-status", "--mission", "061-merged", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-wp-status", "--evidence-manifest", "evidence.yaml", "--json"},
		step{"migrate", "backfill-wp-status"},
	)
	p.CompareTree("kitty-specs", true)
}
