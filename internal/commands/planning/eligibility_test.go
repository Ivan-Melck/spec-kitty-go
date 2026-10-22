package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffFinalizeCanceledAndFrozenLanes covers the lane-input projection
// (canceled WPs leave the lanes; executable work still depending on a
// canceled WP is refused with STALE_CANCELED_DEPENDENCIES) and the frozen
// lane membership preflight (#5573): a re-finalize that would move a
// started WP across the lane-planning boundary is refused with
// LANE_MEMBERSHIP_FROZEN before any status write, --validate-only included.
func TestDiffFinalizeCanceledAndFrozenLanes(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	frozenEdit := `sed -i -e '/^owned_files:/,/^[a-z_]*:/{/^- /d}' -e '/^create_intent:/,/^[a-z_]*:/{/^- /d}' -e 's/^execution_mode: .*/execution_mode: planning_artifact/' -e 's#^owned_files:.*#owned_files: ["docs/notes.md"]#' -e 's#^authoritative_surface: .*#authoritative_surface: "docs/"#' -e 's#^create_intent:.*#create_intent: ["docs/notes.md"]#' kitty-specs/$M/tasks/WP01-subtract-helper.md; `
	shellSteps(t, p, g, o,
		`$SK agent tasks move-task WP01 --to claimed --mission $M --agent claude --json >/dev/null; `+frozenEdit+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)

	q := missionfx.NewOraclePair(t)
	g2, o2 := missionfx.OracleCalcMission(t, q)
	shellSteps(t, q, g2, o2,
		// a canceled WP (authored lane) leaves the lanes; WP02 still depends on it
		`sed -i 's/^title: .*/&\nlane: "canceled"/' kitty-specs/$M/tasks/WP01-subtract-helper.md; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		`sed -i 's/^dependencies: .*/dependencies: []/' kitty-specs/$M/tasks/WP02-multiply.md; sed -i 's/Depends on WP01 (uses .*)/None/' kitty-specs/$M/tasks.md; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --json`,
	)
	q.CompareTree("kitty-specs", true)
	compareGit(t, q)
}
