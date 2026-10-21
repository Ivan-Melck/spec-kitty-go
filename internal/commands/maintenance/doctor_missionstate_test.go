package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffDoctorMissionStateAudit(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state"},
		step{"doctor", "mission-state", "--audit", "--fix"},
		step{"doctor", "mission-state", "--audit"},
		step{"doctor", "mission-state", "--audit", "--json"},
		step{"doctor", "mission-state", "--audit", "--fail-on", "bogus"},
	)
	g, o := missionfx.OracleCalcMission(t, p)
	p.Both(legacyCorpus)
	p.Both(lifecycleCorpus)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/090-events/meta.json", `{"mission_id": "01JNNNNNNNNNNNNNNNNNNNNNNN", "feature_slug": "x", "extra_key": 1}`+"\n")
		s.WriteFile("kitty-specs/090-events/status.events.jsonl", `{"actor": "Bad Actor", "at": "2025-01-01T00:00:00+00:00", "event_id": "01JNNNNNNNNNNNNNNNNNNNNNN1", "evidence": null, "execution_mode": "worktree", "force": false, "from_lane": "planned", "mission_slug": "090-events", "reason": null, "review_ref": null, "to_lane": "done", "wp_id": "WP01", "work_package_id": "WP01", "event_type": "x"}`+"\nnot json\n[1]\n")
		s.WriteFile("kitty-specs/090-events/status.json", `{"mission_slug": "090-events", "weird": true}`)
		s.WriteFile("kitty-specs/090-events/mission-events.jsonl", `{"type": "a", "zzz": 1}`+"\n")
		s.WriteFile("kitty-specs/090-events/tasks/WP01-x.md", "---\nwork_package_id: WP01\nfeature_slug: y\nfoo: bar\n---\n")
		s.WriteFile("kitty-specs/091-residue/notes.lock", "x")
		s.Git("add", "-A", "kitty-specs/090-events")
		s.Git("commit", "-q", "-m", "events")
	})
	compareSteps(t, p, g, o,
		step{"doctor", "mission-state", "--audit"},
		step{"doctor", "mission-state", "--audit", "--json"},
		step{"doctor", "mission-state", "--audit", "--mission", "$M", "--json"},
		step{"doctor", "mission-state", "--audit", "--mission", "nope", "--json"},
		step{"doctor", "mission-state", "--audit", "--mission", "nope"},
		step{"doctor", "mission-state", "--audit", "--fail-on", "warning"},
		step{"doctor", "mission-state", "--audit", "--fail-on", "teamspace-blocker", "--mission", "$M"},
	)
}
