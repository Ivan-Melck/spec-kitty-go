package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffCheckPrerequisites covers TRACE 1.4 (resume probe), 3.2 and 4.3
// plus detection errors and the hidden `agent check-prerequisites` alias.
func TestDiffCheckPrerequisites(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	shellSteps(t, p, "", "",
		`$SK agent mission check-prerequisites --mission calc-ops --resume-probe --json`,
		`$SK agent mission check-prerequisites --mission calc-ops --resume-probe`,
		`$SK agent mission check-prerequisites --resume-probe --json`,
		`$SK agent mission check-prerequisites --mission x --resume-probe --paths-only`,
		`$SK agent mission check-prerequisites --json`,
		`$SK agent mission check-prerequisites`,
		`$SK agent mission check-prerequisites --mission nope --json`,
		`$SK agent mission check-prerequisites --mission nope`,
	)
	slugs := []string{}
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
		slugs = append(slugs, missionfx.MissionSlug(t, s))
	})
	g, o := slugs[0], slugs[1]
	shellSteps(t, p, g, o,
		`$SK agent mission check-prerequisites --mission calc-ops --resume-probe --json`,
		`$SK agent mission check-prerequisites --mission $M --resume-probe --json`,
		`$SK agent mission check-prerequisites --mission $M --json`,
		`$SK agent mission check-prerequisites --mission $M`,
		`$SK agent mission check-prerequisites --mission $M --json --paths-only --include-tasks`,
		`$SK agent mission check-prerequisites --mission $M --include-tasks`,
		`$SK agent mission check-prerequisites --mission calc-ops --json --paths-only`,
		`$SK agent check-prerequisites --mission $M --json --require-tasks`,
		`$SK agent check-prerequisites --mission $M --require-tasks`,
		`$SK agent mission check-prerequisites --json`,
		`$SK agent mission check-prerequisites`,
	)
	p.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	shellSteps(t, p, g, o,
		`$SK agent mission check-prerequisites --mission calc-ops --resume-probe --json`,
		`mkdir -p kitty-specs/$M/contracts kitty-specs/$M/checklists; echo r > kitty-specs/$M/research.md; echo q > kitty-specs/$M/quickstart.md; $SK agent mission check-prerequisites --json --paths-only --include-tasks --mission $M`,
		`$SK agent mission check-prerequisites --json --include-tasks --mission $M`,
		`$SK agent mission check-prerequisites --include-tasks --mission $M`,
		`mkdir -p kitty-specs/calc-ops-x; echo s > kitty-specs/calc-ops-x/spec.md; $SK agent mission check-prerequisites --mission calc-ops --resume-probe --json`,
		`$SK agent mission check-prerequisites --mission calc-ops --resume-probe`,
		`$SK agent mission check-prerequisites --mission calc-ops-x --resume-probe --json`,
		`$SK agent mission check-prerequisites --json`,
		`$SK agent mission check-prerequisites --mission ../x --resume-probe --json`,
	)
}
