package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffFinalizeRefreshPlanningCommit covers --refresh-planning-commit
// (+ --allow-orphaned): the pre-execution refresh preview and commit, the
// status preflight refusal, the branch-contract refusal, and, once execution
// has begun, the advanced / orphaned / foreign pin decisions.
func TestDiffFinalizeRefreshPlanningCommit(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		missionfx.AuthorCalc(t, s, slug)
		missionfx.RunOracle(t, s, "agent", "mission", "finalize-tasks", "--mission", slug, "--json")
	})
	lanesSHA := `python3 -c "import json,sys; d=json.load(open('kitty-specs/$M/lanes.json')); d['planning_commit_sha']=sys.argv[1]; open('kitty-specs/$M/lanes.json','w').write(json.dumps(d, indent=2, sort_keys=True)+'\n')" `
	shellSteps(t, p, g, o,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit`,
		// a planning amendment advances the target branch
		`echo "Amended." >> kitty-specs/$M/spec.md; git commit -qam "amend spec"; $SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only`,
		// pending changes block the mutating refresh
		`touch stray.txt; $SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit; rm stray.txt`,
		// a target-branch override is refused
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --target-branch main --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --json`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
	// execution begins
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", missionfx.MissionSlug(t, s), "--agent", "claude", "--json")
	})
	shellSteps(t, p, g, o,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --json`,
		`echo "Amended again." >> kitty-specs/$M/spec.md; git commit -qam "amend spec 2"; $SK agent mission finalize-tasks --mission $M --refresh-planning-commit --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit`,
		// an orphaned pin (a mid-mission rebase)
		lanesSHA+`$(git commit-tree HEAD^{tree} -m orphan); git commit -qam "orphan pin"; $SK agent mission finalize-tasks --mission $M --refresh-planning-commit --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit`,
		`$SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --allow-orphaned --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit --allow-orphaned --json`,
		// a foreign pin
		lanesSHA+`aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa; git commit -qam "foreign pin"; $SK agent mission finalize-tasks --mission $M --refresh-planning-commit --allow-orphaned --json`,
		`$SK agent mission finalize-tasks --mission $M --refresh-planning-commit`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
}
