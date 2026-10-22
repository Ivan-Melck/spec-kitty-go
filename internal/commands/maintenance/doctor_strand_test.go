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

// strandMission strands WP01 at `done` on the coordination branch (a status
// commit after the captured tip) and records a pending_coord_reconcile
// marker for it, as a rolled-back consolidation leaves behind.
func strandMission(t *testing.T, s *testkit.Sandbox, slug string, recordSHAs bool, worktree string) {
	t.Helper()
	wt := coordWorktree(t, s)
	git := func(args ...string) string {
		r := s.Exec(wt, "git", args...)
		if r.Code != 0 {
			t.Fatalf("git %v: %s", args, r.Stderr)
		}
		return strings.TrimSpace(r.Stdout)
	}
	captured := git("rev-parse", "HEAD")
	branch := strings.TrimPrefix(git("symbolic-ref", "HEAD"), "refs/heads/")
	events := filepath.Join(wt, "kitty-specs", slug, "status.events.jsonl")
	b, err := os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	row := `{"actor": "merge", "at": "2026-01-01T00:00:00+00:00", "event_id": "01KZZZZZZZZZZZZZZZZZZZZZZ1", "evidence": null, "execution_mode": "worktree", "force": true, "from_lane": "planned", "mission_slug": "` + slug + `", "reason": "done bookkeeping", "review_ref": null, "to_lane": "done", "wp_id": "WP01"}` + "\n"
	os.WriteFile(events, append(b, []byte(row)...), 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "done bookkeeping")
	strand := git("rev-parse", "HEAD")
	meta, _ := jsonx.DecodeObject([]byte(s.ReadFile("kitty-specs/" + slug + "/meta.json")))
	mid := meta.GetString("mission_id")
	if worktree == "" {
		worktree = wt
	}
	marker := jsonx.NewObject("coord_ref", branch, "captured_sha", captured, "coord_worktree", worktree, "stranded_wp_ids", []any{"WP01"})
	if recordSHAs {
		marker.Set("strand_shas", []any{strand})
	}
	state := jsonx.NewObject("mission_id", mid, "mission_slug", slug, "target_branch", "main", "wp_order", []any{"WP01", "WP02"},
		"completed_wps", []any{}, "started_at", "2026-01-01T00:00:00+00:00", "updated_at", "2026-01-01T00:00:00+00:00",
		"pending_coord_reconcile", marker)
	s.WriteFile(".kittify/runtime/merge/"+mid+"/state.json", jsonx.Dumps(state, jsonx.Pretty))
}

func TestDiffDoctorCoordinationStrand(t *testing.T) {
	p := newOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	p.Go.Env = append(p.Go.Env, "SPEC_KITTY_FAKE_NOW=2026-02-01T00:00:00Z")
	strandMission(t, p.Go, g, true, "")
	strandMission(t, p.Oracle, o, true, "")
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--json"},
		step{"doctor", "coordination"},
		step{"doctor", "coordination", "--fix"},
		step{"doctor", "coordination", "--json"},
	)
	compareFiles(t, p, g, o, "kitty-specs/$M/status.events.jsonl")
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		wt := coordWorktree(t, s)
		r := s.Exec(wt, "git", "log", "-1", "--format=%s")
		if !strings.HasPrefix(strings.TrimSpace(r.Stdout), `Revert "done bookkeeping"`) {
			t.Errorf("%s: strand not reverted: %q", s.Dir, r.Stdout)
		}
	}
	gs, os_ := stateMarker(t, p.Go, g), stateMarker(t, p.Oracle, o)
	if gs != "null" || os_ != "null" {
		t.Errorf("marker not cleared: go=%s oracle=%s", gs, os_)
	}
}

// TestDiffDoctorCoordinationStrandRefusals: a legacy marker (no recorded
// strand SHAs) and a pruned coordination worktree are refused by --fix.
func TestDiffDoctorCoordinationStrandRefusals(t *testing.T) {
	p := newOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	strandMission(t, p.Go, g, false, "")
	strandMission(t, p.Oracle, o, false, "")
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--fix", "--json"},
		step{"doctor", "coordination", "--fix"},
	)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		meta, _ := jsonx.DecodeObject([]byte(s.ReadFile("kitty-specs/" + slug + "/meta.json")))
		st := ".kittify/runtime/merge/" + meta.GetString("mission_id") + "/state.json"
		s.WriteFile(st, strings.Replace(s.ReadFile(st), `"coord_worktree": "`, `"coord_worktree": "/nonexistent`, 1))
	})
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--json"},
		step{"doctor", "coordination", "--fix"},
	)
}

// TestDiffDoctorCoordinationStrandBranchMismatch: --fix refuses to revert
// in a coordination worktree checked out on a foreign branch, and refuses
// a range holding a status commit the marker did not record.
func TestDiffDoctorCoordinationStrandBranchMismatch(t *testing.T) {
	p := newOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	strandMission(t, p.Go, g, true, "")
	strandMission(t, p.Oracle, o, true, "")
	p.Both(func(s *testkit.Sandbox) { s.Exec(coordWorktree(t, s), "git", "switch", "-q", "-c", "scratch") })
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--fix", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		wt := coordWorktree(t, s)
		s.Exec(wt, "git", "switch", "-q", "-")
		slug := missionfx.MissionSlug(t, s)
		f := filepath.Join(wt, "kitty-specs", slug, "status.events.jsonl")
		b, _ := os.ReadFile(f)
		os.WriteFile(f, append(b, []byte(`{"actor": "reviewer", "at": "2026-01-02T00:00:00+00:00", "event_id": "01KZZZZZZZZZZZZZZZZZZZZZZ2", "evidence": null, "execution_mode": "worktree", "force": true, "from_lane": "done", "mission_slug": "`+slug+`", "reason": "reopen", "review_ref": null, "to_lane": "done", "wp_id": "WP01"}`+"\n")...), 0o644)
		s.Exec(wt, "git", "commit", "-qam", "later status event")
	})
	compareSteps(t, p, g, o,
		step{"doctor", "coordination", "--fix", "--json"},
	)
}

func stateMarker(t *testing.T, s *testkit.Sandbox, slug string) string {
	meta, _ := jsonx.DecodeObject([]byte(s.ReadFile("kitty-specs/" + slug + "/meta.json")))
	st, err := jsonx.DecodeObject([]byte(s.ReadFile(".kittify/runtime/merge/" + meta.GetString("mission_id") + "/state.json")))
	if err != nil {
		return "unreadable"
	}
	v, _ := st.Get("pending_coord_reconcile")
	return jsonx.Dumps(v, jsonx.Line)
}
