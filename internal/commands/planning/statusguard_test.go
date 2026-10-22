package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffFinalizeRefusalRestoresStatusSurface covers the FR-015 write-scope
// guard over the status surface (#5641): a finalize refused after its first
// status writes (an orphaned planning pin) leaves the coordination status
// log, the mission directory and every branch exactly as it found them.
func TestDiffFinalizeRefusalRestoresStatusSurface(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "tasks", "move-task", "WP01", "--to", "claimed", "--mission", missionfx.MissionSlug(t, s), "--agent", "claude", "--json")
	})
	lanesSHA := `python3 -c "import json,sys; d=json.load(open('kitty-specs/$M/lanes.json')); d['planning_commit_sha']=sys.argv[1]; open('kitty-specs/$M/lanes.json','w').write(json.dumps(d, indent=2, sort_keys=True)+'\n')" `
	shellSteps(t, p, g, o,
		lanesSHA+`$(git commit-tree HEAD^{tree} -m orphan); git commit -qam "orphan pin"; echo "Amended." >> kitty-specs/$M/spec.md; git commit -qam amend; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		`git -C .worktrees/*-coord status --short; git -C .worktrees/*-coord log --format=%s | head -5`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)

	// A first finalize refused by the risk policy (block mode) after the
	// canonical bootstrap committed status on the coordination branch.
	q := testkitPair(t)
	g2, o2 := oracleCreate(t, q)
	q.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	shellSteps(t, q, g2, o2,
		`printf 'policy:\n  risk:\n    mode: block\n    threshold: 0.05\n' >> .kittify/config.yaml; git commit -qam policy; sed -i 's/^dependencies: .*/dependencies: []/' kitty-specs/$M/tasks/WP02-multiply.md; sed -i 's/Depends on WP01 (uses .*)/None/' kitty-specs/$M/tasks.md; git commit -qam indep; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		`git -C .worktrees/*-coord status --short 2>&1; git -C .worktrees/*-coord log --format=%s 2>&1 | head -5; git status --short`,
	)
	q.CompareTree("kitty-specs", true)
	q.CompareTree(".worktrees", true)
	compareGit(t, q)
}

func testkitPair(t *testing.T) *testkit.Pair { return missionfx.NewOraclePair(t) }
