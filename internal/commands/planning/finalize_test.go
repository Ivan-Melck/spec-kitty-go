package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffFinalizeTasks covers TRACE 3.4/3.5 (coord topology): the
// validate-only preview, the finalize commit (lanes.json, frontmatter,
// status bootstrap commits on the coordination branch, acceptance matrix)
// and an idempotent re-run.
func TestDiffFinalizeTasks(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		missionfx.AuthorCalc(t, s, slug)
		missionfx.RunOracle(t, s, "agent", "tasks", "map-requirements", "--batch", `{"WP01":["FR-001","SC-001"],"WP02":["FR-002"]}`, "--mission", slug, "--json")
	})
	shellSteps(t, p, g, o,
		`$SK agent mission finalize-tasks --json`,
		`$SK agent mission finalize-tasks --mission nope --json`,
		`$SK agent mission finalize-tasks --validate-only --mission $M --json`,
		`$SK agent mission finalize-tasks --validate-only --mission $M`,
		`$SK agent mission finalize-tasks --mission $M --json`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
	shellSteps(t, p, g, o,
		`$SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		`$SK agent mission finalize-tasks --validate-only --mission $M --json`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	p.CompareTree(".kittify/derived", true)
	compareGit(t, p)
}

// TestDiffFinalizeTasksHuman runs the human-mode finalize on a fresh mission
// and the main refusal paths.
func TestDiffFinalizeTasksHuman(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	shellSteps(t, p, g, o,
		`sed -i 's/requirement_refs: .*/requirement_refs: []/' kitty-specs/$M/tasks/WP02-multiply.md; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		`git checkout -q -- kitty-specs; sed -i 's/dependencies: \[\]/dependencies: ["WP02"]/' kitty-specs/$M/tasks/WP01-subtract-helper.md; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		`git checkout -q -- kitty-specs; sed -i 's#owned_files: \["src/calc.py"#owned_files: ["src/nothere.py"#' kitty-specs/$M/tasks/WP01-subtract-helper.md; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		// mask the short SHA here: the normalizer skips all-digit SHAs.
		`git checkout -q -- kitty-specs; $SK agent mission finalize-tasks --mission $M | sed -E 's/committed [0-9a-f]{7}/committed SHA/'`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}

// TestDiffTasksFinalizeTasks covers the legacy `agent tasks finalize-tasks`.
func TestDiffTasksFinalizeTasks(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	shellSteps(t, p, g, o,
		`$SK agent tasks finalize-tasks --json`,
		`$SK agent tasks finalize-tasks --mission $M --validate-only --json`,
		`$SK agent tasks finalize-tasks --mission $M --validate-only`,
		`$SK agent tasks finalize-tasks --mission $M --json`,
		`$SK agent tasks finalize-tasks --mission $M`,
		`mv kitty-specs/$M/tasks.md $TMPDIR/x-$M.md; $SK agent tasks finalize-tasks --mission $M --json; mv $TMPDIR/x-$M.md kitty-specs/$M/tasks.md`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
