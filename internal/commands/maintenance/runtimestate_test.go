package maintenance_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/migrate"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
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

// toleranceCorpus exercises verify_backfill's tolerated seed shapes: a
// pre-#2985 full-payload claim carrier, a planned subtask seed superseded
// by legitimate later history, and a tampered subtask seed (still red).
func toleranceCorpus(s *testkit.Sandbox) {
	const mid = "01KKKKKKKKKKKKKKKKKKKKKKKK"
	row := func(fields string) string { return "{" + fields + "}\n" }
	m := "kitty-specs/073-carrier/"
	s.WriteFile(m+"meta.json", `{"mission_id": "`+mid+`", "slug": "073-carrier"}`+"\n")
	s.WriteFile(m+"tasks/WP01-a.md", "---\nwork_package_id: WP01\ntitle: A\nagent: claude\nshell_pid: '4242'\n---\nbody\n")
	s.WriteFile(m+"status.events.jsonl",
		row(`"actor": "x", "at": "2025-07-01T10:00:00+00:00", "event_id": "01KKKKKKKKKKKKKKKKKKKKKKK1", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "073-carrier", "policy_metadata": {"shell_pid": 4242}, "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP01"`)+
			row(`"actor": "migration:backfill_runtime_state", "at": "2025-07-01T10:00:00+00:00", "event_id": "`+migrate.DeterministicID(mid+"|WP01|claim")+`", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_id": "`+mid+`", "mission_slug": "073-carrier", "policy_metadata": {"shell_pid": 4242, "agent": "claude"}, "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "WP01"`))
	n := "kitty-specs/074-subtasks/"
	const mid2 = "01MMMMMMMMMMMMMMMMMMMMMMMM"
	s.WriteFile(n+"meta.json", `{"mission_id": "`+mid2+`", "slug": "074-subtasks"}`+"\n")
	s.WriteFile(n+"tasks.md", "# Tasks\n\n## WP01 First\n\n- [x] T001 do a\n- [x] T002 do b\n\n## WP02 Second\n\n- [x] T003 do c\n")
	s.WriteFile(n+"tasks/WP01-a.md", "---\nwork_package_id: WP01\ntitle: A\n---\nbody\n")
	s.WriteFile(n+"tasks/WP02-b.md", "---\nwork_package_id: WP02\ntitle: B\n---\nbody\n")
	ann := func(actor, at, id, wp, subtasks string) string {
		return row(`"actor": "` + actor + `", "at": "` + at + `", "delta": {"subtasks": ` + subtasks + `}, "event_id": "` + id + `", "kind": "annotation", "wp_id": "` + wp + `"`)
	}
	claim := func(id, wp string) string {
		return row(`"actor": "x", "at": "2025-07-01T09:00:00+00:00", "event_id": "` + id + `", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "074-subtasks", "reason": null, "review_ref": null, "to_lane": "claimed", "wp_id": "` + wp + `"`)
	}
	s.WriteFile(n+"status.events.jsonl",
		claim("01MMMMMMMMMMMMMMMMMMMMMMM1", "WP01")+claim("01MMMMMMMMMMMMMMMMMMMMMMM2", "WP02")+
			ann("migration:backfill_runtime_state", "2025-07-01T09:00:00+00:00", migrate.DeterministicID(mid2+"|WP01|subtasks"), "WP01", `{"T001": "done", "T002": "planned"}`)+
			ann("agent:claude", "2025-07-02T00:00:00+00:00", "01MMMMMMMMMMMMMMMMMMMMMMM3", "WP01", `{"T001": "done", "T002": "done"}`)+
			ann("migration:backfill_runtime_state", "2025-07-01T09:00:00+00:00", migrate.DeterministicID(mid2+"|WP02|subtasks"), "WP02", `{"T003": "in_progress"}`))
	s.Git("add", "-A")
	s.Git("commit", "-q", "-m", "tolerance corpus")
}

func TestDiffMigrateBackfillRuntimeStateTolerances(t *testing.T) {
	p := newOraclePair(t)
	p.Both(toleranceCorpus)
	compareSteps(t, p, "", "",
		step{"migrate", "backfill-runtime-state", "--dry-run", "--json"},
		step{"migrate", "backfill-runtime-state", "--json"},
	)
	p.CompareTree("kitty-specs", true)
}

// TestDiffMigrateBackfillRuntimeStateFromWorktree runs the cutover from the
// coordination worktree: the project root resolves to the primary checkout,
// so the write-intent verify reads an already-canonical feature dir and the
// foreign-checkout refusal does not fire (as upstream).
func TestDiffMigrateBackfillRuntimeStateFromWorktree(t *testing.T) {
	p := newOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	gw, ow := coordWorktree(t, p.Go), coordWorktree(t, p.Oracle)
	for _, a := range [][]string{
		{"migrate", "backfill-runtime-state", "--dry-run", "--json"},
		{"migrate", "backfill-runtime-state", "--json"},
	} {
		gr := p.Go.RunIn(gw, a...)
		or := p.Oracle.Exec(ow, testkit.Oracle(t), a...)
		if gr.Code != or.Code {
			t.Errorf("%v: exit go=%d oracle=%d\n%s%s\n%s%s", a, gr.Code, or.Code, gr.Stdout, gr.Stderr, or.Stdout, or.Stderr)
		}
		if gs, os_ := p.Go.Normalize(strings.ReplaceAll(gr.Stdout, g, "<SLUG>")), p.Oracle.Normalize(strings.ReplaceAll(or.Stdout, o, "<SLUG>")); gs != os_ {
			t.Errorf("%v: stdout differs\n%s", a, testkit.Diff(os_, gs))
		}
	}
	compareFiles(t, p, g, o, "kitty-specs/$M/meta.json", "kitty-specs/$M/status.events.jsonl")
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
