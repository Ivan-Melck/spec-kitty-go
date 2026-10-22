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
	// Strict WPMetadata validation (read_authored_wp_frontmatter).
	s.WriteFile(a+"tasks/WP04-deps.md", "---\nwork_package_id: WP04\ndependencies: 5\n---\n")
	s.WriteFile(a+"tasks/WP05-lane.md", "---\nwork_package_id: WP05\nlane: sideways\n---\n")
	s.WriteFile(a+"tasks/WP06-wptitle.md", "---\nwork_package_id: WP06\nwork_package_title: Six\ndependencies: WP01, WP02\nlane: doing\n---\n")
	s.WriteFile(a+"tasks/WP07-ts.md", "---\nwork_package_id: WP07\ncreated_at: 2025-01-01T00:00:00Z\n---\n")
	s.WriteFile(a+"tasks/WP08-dup.md", "---\nwork_package_id: WP08\ntitle: a\ntitle: b\n---\n")
	s.WriteFile(a+"tasks/WP10-list.md", "---\n- a\n- b\n---\n")
	s.WriteFile(a+"tasks/WP11-nofm.md", "no frontmatter\n")
	s.WriteFile(a+"tasks/WP12-pid.md", "---\nwork_package_id: WP12\nshell_pid: abc\n---\n")
	s.WriteFile(a+"tasks/WP13-pidok.md", "---\nwork_package_id: WP13\nshell_pid: '42'\nphase: 3\nsubtasks: [T001]\nagent: {tool: claude}\nbase_commit: unknown\n---\n")
	s.WriteFile(a+"tasks/WP14-badid.md", "---\nwork_package_id: wp14\n---\n")
	s.WriteFile(a+"tasks/WP15-latin1.md", "---\nwork_package_id: WP15\ntitle: caf\xe9\n---\n")
	s.WriteFile(a+"tasks/WP16-reftypes.md", "---\nwork_package_id: WP16\nrequirement_refs: FR-001 FR-002\ntracker_refs: [1]\n---\n")
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

// TestDiffMigrateBackfillWPStatusTypeError: WPMetadata's shell_pid
// coercion raises TypeError for a sequence, which upstream does not catch.
func TestDiffMigrateBackfillWPStatusTypeError(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/070-x/meta.json", `{"slug": "070-x"}`+"\n")
		s.WriteFile("kitty-specs/070-x/tasks/WP01-a.md", "---\nwork_package_id: WP01\nshell_pid: [1]\n---\n")
	})
	for _, args := range [][]string{{"migrate", "backfill-wp-status"}, {"migrate", "backfill-wp-status", "--json"}} {
		g, o := p.Run(args...)
		if g.Code != o.Code || g.Stdout != o.Stdout || lastLine(g.Stderr) != lastLine(o.Stderr) {
			t.Errorf("%v: go=%d %q %q oracle=%d %q %q", args, g.Code, g.Stdout, lastLine(g.Stderr), o.Code, o.Stdout, lastLine(o.Stderr))
		}
	}
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
