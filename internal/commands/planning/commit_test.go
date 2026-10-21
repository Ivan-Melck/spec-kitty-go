package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffSafeCommit covers safe-commit: protected refusal, deprecation
// warning, unknown paths, directory expansion, HEAD mismatch, empty message,
// paragraphs, unrelated staged content, .worktrees policy.
func TestDiffSafeCommit(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	shellSteps(t, p, "", "",
		`$SK safe-commit -m x`,
		`$SK safe-commit README.md -m x --to-branch main --json`,
		`echo hi >> README.md; $SK safe-commit README.md -m 'readme' --to-branch main --json`,
		`$SK safe-commit README.md -m 'readme'`,
		`git switch -q -c feat/x; $SK safe-commit README.md -m 'readme'`,
		`$SK safe-commit nope.txt -m x --to-branch feat/x`,
		`$SK safe-commit nope.txt -m x --to-branch feat/x --json`,
		`mkdir -p d/e; echo 1 > d/a.txt; echo 2 > d/e/b.txt; $SK safe-commit d -m 'dir' --to-branch feat/x`,
		`mkdir -p d2; echo 1 > d2/a.txt; $SK safe-commit d2/ -m 'dir' --to-branch feat/x --json`,
		`mkdir -p d3; $SK safe-commit d3 -m 'dir' --to-branch feat/x --json`,
		`echo x > f.txt; $SK safe-commit f.txt -m 'f' --to-branch other --json`,
		`$SK safe-commit f.txt -m 'f' --to-branch refs/heads/feat/x --json`,
		`$SK safe-commit f.txt -m ' ' --to-branch feat/x --json`,
		`$SK safe-commit f.txt -m ' ' --to-branch feat/x`,
		`echo x2 > f.txt; $SK safe-commit f.txt -m 'a' -m 'b' --to-branch feat/x`,
		`echo y > g.txt; git add g.txt; echo z > h.txt; $SK safe-commit h.txt -m 'h' --to-branch feat/x; git status --short`,
		`mkdir -p .worktrees/x; echo 1 > .worktrees/x/a; $SK safe-commit .worktrees/x/a -m 'h' --to-branch feat/x --json`,
		`git rm -q --cached g.txt; git mv f.txt ff.txt; $SK safe-commit ff.txt f.txt -m 'mv' --to-branch feat/x --json`,
		`mkdir -p sub; echo 1 > sub/a; cd sub && $SK safe-commit sub/a -m 'from subdir' --to-branch feat/x --json`,
		`git checkout -q --detach; echo 3 > d/a.txt; $SK safe-commit d/a.txt -m x --json`,
	)
	compareGit(t, p)
}

// TestDiffSafeCommitMission covers the mission-aware target resolution (no
// --to-branch), a commit inside the coordination worktree (TRACE 7.3) and
// spec-commit (TRACE 1.9 / 2.5).
func TestDiffSafeCommitMission(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	slugs := []string{}
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
		slugs = append(slugs, missionfx.MissionSlug(t, s))
	})
	g, o := slugs[0], slugs[1]
	shellSteps(t, p, g, o,
		`$SK spec-commit -m x`,
		`cp $TD/calc/authored/spec.md kitty-specs/$M/spec.md; $SK spec-commit --mission $M --message "Add spec for mission $M" kitty-specs/$M/spec.md kitty-specs/$M/meta.json`,
		`$SK spec-commit --mission $M -m again kitty-specs/$M/spec.md --json`,
		`echo r > kitty-specs/$M/research.md; mkdir -p kitty-specs/$M/contracts; echo c > kitty-specs/$M/contracts/api.md; $SK spec-commit -m 'support' kitty-specs/$M/research.md kitty-specs/$M/contracts/api.md --json`,
		`$SK spec-commit -m 'dir' kitty-specs/$M/contracts`,
		`$SK spec-commit -m 'dir' kitty-specs/$M/contracts --json`,
		`$SK spec-commit -m x --mission nope-mission kitty-specs/$M/spec.md --json`,
		`$SK spec-commit -m x --mission nope-mission kitty-specs/$M/spec.md`,
		`echo p > kitty-specs/$M/plan.md; $SK safe-commit kitty-specs/$M/plan.md -m 'plan via safe-commit' --json`,
		`echo q > kitty-specs/$M/quickstart.md; $SK spec-commit -m q kitty-specs/$M/quickstart.md kitty-specs/$M/nothere.md --json`,
		`cd .worktrees/$M-coord && echo '{}' > kitty-specs/$M/mission-events.jsonl && $SK safe-commit kitty-specs/$M/mission-events.jsonl -m 'chore: coord residue' --to-branch kitty/mission-$M --json`,
		`git switch -q main; echo s2 >> kitty-specs/$M/spec.md; $SK spec-commit -m 'on main' kitty-specs/$M/spec.md --json`,
	)
	compareGit(t, p)
}
