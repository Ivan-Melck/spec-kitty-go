package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffMapRequirements covers TRACE 3.3 plus individual/replace/tracker
// modes and every validation refusal.
func TestDiffMapRequirements(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	shellSteps(t, p, g, o,
		`$SK agent tasks map-requirements --mission $M --json`,
		`$SK agent tasks map-requirements --mission $M --batch '{}' --wp WP01 --json`,
		`$SK agent tasks map-requirements --mission $M --batch '{}' --tracker-ref X`,
		`$SK agent tasks map-requirements --batch '{"WP01":["FR-001"]}' --json`,
		`$SK agent tasks map-requirements --mission $M --batch 'nope' --json`,
		`$SK agent tasks map-requirements --mission $M --batch '[1]' --json`,
		`$SK agent tasks map-requirements --mission $M --batch '{"WP01":[1]}' --json`,
		`$SK agent tasks map-requirements --mission $M --batch '{"WP09":["FR-001"]}' --json`,
		`$SK agent tasks map-requirements --mission $M --batch '{"WP09":["FR-001"]}'`,
		`$SK agent tasks map-requirements --mission $M --wp WP01 --refs bogus,FR_1 --json`,
		`$SK agent tasks map-requirements --mission $M --wp WP01 --refs bogus`,
		`$SK agent tasks map-requirements --mission $M --wp WP01 --refs FR-099 --json`,
		`$SK agent tasks map-requirements --mission $M --wp WP01 --refs FR-099`,
		`$SK agent tasks map-requirements --batch '{"WP01":["FR-001","SC-001"],"WP02":["FR-002"]}' --mission $M --json`,
		`$SK agent tasks map-requirements --batch '{"WP01":["FR-001","SC-001"],"WP02":["FR-002"]}' --mission $M`,
		`$SK agent tasks map-requirements --wp wp02 --refs 'fr-001, other-mission#FR-003' --mission $M --json --no-auto-commit`,
		`$SK agent tasks map-requirements --wp WP02 --refs FR-002 --replace --mission $M --json`,
		`$SK agent tasks map-requirements --wp WP02 --refs FR-002 --replace --mission $M`,
		`$SK agent tasks map-requirements --wp WP01 --tracker-ref '#12' --tracker-ref JIRA-1 --mission $M --json`,
		`sed -i 's/requirement_refs: .*/requirement_refs: ["FR-777"]/' kitty-specs/$M/tasks/WP02-multiply.md; $SK agent tasks map-requirements --wp WP01 --refs FR-001 --mission $M --json`,
		`$SK agent tasks map-requirements --wp WP01 --refs FR-001 --mission $M`,
		`$SK agent tasks map-requirements --mission nope --wp WP01 --refs FR-001 --json`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
