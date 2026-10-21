package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func oracleCreate(t *testing.T, p *testkit.Pair, args ...string) (string, string) {
	t.Helper()
	if len(args) == 0 {
		args = []string{"--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops"}
	}
	slugs := []string{}
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, append([]string{"agent", "mission", "create", "calc-ops", "--json"}, args...)...)
		slugs = append(slugs, missionfx.MissionSlug(t, s))
	})
	return slugs[0], slugs[1]
}

// TestDiffSetupPlan covers TRACE 2.3/2.4 plus the spec gate, the
// requirement-ID lint and detection errors.
func TestDiffSetupPlan(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	shellSteps(t, p, "", "",
		`$SK agent mission setup-plan --json`,
		`$SK agent mission setup-plan --mission nope --json`,
		`$SK agent mission setup-plan --mission nope`,
	)
	g, o := oracleCreate(t, p)
	shellSteps(t, p, g, o,
		`$SK agent mission setup-plan --mission $M --json`,
		`$SK agent mission setup-plan --mission $M`,
		`cp $TD/calc/authored/spec.md kitty-specs/$M/spec.md; git add kitty-specs/$M/spec.md; git commit -qm spec; $SK agent mission setup-plan --json`,
		`$SK agent mission setup-plan --mission $M`,
		`echo '- **Language/Version**: Python 3.12' >> kitty-specs/$M/plan.md; $SK agent mission setup-plan --mission $M --json`,
		`cp $TD/calc/authored/plan.md kitty-specs/$M/plan.md; $SK agent mission setup-plan --mission $M --json`,
		`$SK agent mission setup-plan --mission $M --json`,
		`$SK agent mission setup-plan --mission $M`,
		`echo 'See FR-009 for more.' >> kitty-specs/$M/spec.md; git commit -qam w; $SK agent mission setup-plan --mission $M --json`,
		`echo '| FR-00N | Placeholder | x |' >> kitty-specs/$M/spec.md; git commit -qam w2; $SK agent mission setup-plan --mission $M --json`,
		`$SK agent mission setup-plan --mission $M`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
