package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffRecordAnalysisDecisionsLock covers #6628: the decision service's
// untracked decisions/index.json.lock is self-bookkeeping, never listed as
// dirt, and the DIRTY_WORKTREE remediation says not to commit it.
func TestDiffRecordAnalysisDecisionsLock(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		missionfx.AuthorCalc(t, s, slug)
		missionfx.RunOracle(t, s, "agent", "mission", "finalize-tasks", "--mission", slug, "--json")
	})
	rep := `--input-file $TD/calc/analysis-report.md`
	shellSteps(t, p, g, o,
		`mkdir -p kitty-specs/$M/decisions && echo '{}' > kitty-specs/$M/decisions/index.json && git add kitty-specs && git commit -qm decisions && touch kitty-specs/$M/decisions/index.json.lock stray.txt; git status --porcelain`,
		`$SK agent mission record-analysis --mission $M `+rep+` --json`,
		`$SK agent mission record-analysis --mission $M `+rep,
		`rm stray.txt; $SK agent mission record-analysis --mission $M `+rep+` --json | grep -o '"success": [a-z]*'`,
	)
}
