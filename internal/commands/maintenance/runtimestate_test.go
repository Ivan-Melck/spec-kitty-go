package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func runtimeCorpus(s *testkit.Sandbox) {
	m := "kitty-specs/070-legacy-runtime/"
	s.WriteFile(m+"meta.json", `{"mission_id": "01JJJJJJJJJJJJJJJJJJJJJJJJ", "slug": "070-legacy-runtime", "created_at": "2025-07-01T09:00:00+00:00"}`+"\n")
	s.WriteFile(m+"tasks.md", "# Tasks\n\n## WP01 First\n\n- [x] T001 do a\n- [ ] T002 do b\n\n## WP02 Second\n\n- [ ] T003 do c\n")
	s.WriteFile(m+"tasks/WP01-a.md", "---\nwork_package_id: WP01\ntitle: A\nagent: claude\nshell_pid: '4242'\nassignee: alice\ntracker_refs: GH-1, GH-2\n---\nbody\n")
	s.WriteFile(m+"tasks/WP02-b.md", "---\nwork_package_id: WP02\ntitle: B\nreview_artifact_override_at: '2025-07-02T00:00:00+00:00'\nreview_artifact_override_actor: bob\nreview_artifact_override_reason: manual\n---\nbody\n")
	s.WriteFile(m+"status.events.jsonl", `{"actor": "x", "at": "2025-07-01T10:00:00+00:00", "event_id": "01JJJJJJJJJJJJJJJJJJJJJJJ1", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "070-legacy-runtime", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP01"}`+"\n")
	n := "kitty-specs/071-plain/"
	s.WriteFile(n+"meta.json", `{"slug": "071-plain"}`+"\n")
	s.WriteFile(n+"tasks/WP01-x.md", "---\nwork_package_id: WP01\n---\n")
	s.WriteFile("kitty-specs/072-notasks/meta.json", `{"slug": "072-notasks", "status_phase": "1"}`+"\n")
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "runtime corpus")
}

func TestDiffMigrateBackfillRuntimeState(t *testing.T) {
	p := newOraclePair(t)
	p.Both(runtimeCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-runtime-state", "--dry-run", "--json"},
		step{"migrate", "backfill-runtime-state", "--dry-run"},
		step{"migrate", "backfill-runtime-state", "--mission", "nope"},
	)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-runtime-state", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-runtime-state"},
		step{"migrate", "backfill-runtime-state", "--mission", "070-legacy-runtime", "--json"},
	)
}
