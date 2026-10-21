package maintenance_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffDoctorReviewCycleReconcile(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "review-cycle-reconcile"},
		step{"doctor", "review-cycle-reconcile", "--json"},
	)
	g, o := missionfx.OracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		s.WriteFile("kitty-specs/"+slug+"/tasks/WP01-subtract-helper/review-cycle-1.md", "---\nverdict: approved\n---\n")
		s.WriteFile("kitty-specs/zz-old-coord/meta.json", `{"mission_id": "01JMMMMMMMMMMMMMMMMMMMMMMM", "mission_slug": "zz-old-coord", "coordination_branch": "kitty/mission-zz-old-coord-01JMMMMM"}`+"\n")
		s.WriteFile("kitty-specs/zz-old-coord/tasks/WP02/review-cycle-1.md", "x\n")
		s.WriteFile("kitty-specs/zz-old-coord/tasks/WP02/arbiter-override-1.json", "{}\n")
		s.WriteFile("kitty-specs/zz-old-coord/tasks/WP02-extra/review-cycle-2.md", "y\n")
		s.WriteFile("kitty-specs/zz-plain/meta.json", `{"mission_slug": "zz-plain"}`+"\n")
	})
	compareSteps(t, p, g, o,
		step{"doctor", "review-cycle-reconcile"},
		step{"doctor", "review-cycle-reconcile", "--json"},
		step{"doctor", "review-cycle-reconcile", "--mission", "$M", "--json"},
		step{"doctor", "review-cycle-reconcile", "--mission", "zz-old-coord"},
		step{"doctor", "review-cycle-reconcile", "--mission", "nope"},
		step{"doctor", "review-cycle-reconcile", "--mission", "nope", "--json"},
	)
}
