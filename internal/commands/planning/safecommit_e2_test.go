package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// publishMeta marks the mission consolidated and published (E2): a
// baseline_merge_commit and a mission_number in meta.json.
const publishMeta = `python3 -c "import json; p='kitty-specs/$M/meta.json'; d=json.load(open(p)); d['baseline_merge_commit']='0'*40; d['mission_number']=7; open(p,'w').write(json.dumps(d, indent=2, sort_keys=True)+'\n')"; git commit -qam "consolidate"; `

// TestDiffSafeCommitPublishedMission covers the E2 (published) CONSOLIDATED
// write: a mission-aware safe-commit resolves to the Primary Branch and
// asserts POST_CONSOLIDATION_WRITE (allowed on protected main), refuses with
// CONSOLIDATED_CONTENT_ABSENT off-checkout, and an explicit --to-branch of
// the Primary Branch is recognised the same way.
func TestDiffSafeCommitPublishedMission(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	shellSteps(t, p, g, o,
		// off-checkout: published, but main does not carry the content
		publishMeta+`git switch -q -c other; git branch -q -D feat/calc-ops; echo r > kitty-specs/$M/research.md; $SK safe-commit -m research kitty-specs/$M/research.md --json; echo rc=$?`,
		`$SK safe-commit -m research kitty-specs/$M/research.md; echo rc=$?`,
		// consolidated onto main: the write lands on protected main
		`git switch -q main; git merge -q --no-ff other -m merge; git branch -q -D other; $SK safe-commit -m research kitty-specs/$M/research.md --json; echo rc=$?; git log -1 --format=%s --name-only`,
		`echo r2 >> kitty-specs/$M/research.md; $SK safe-commit -m again --to-branch main kitty-specs/$M/research.md --json; echo rc=$?`,
		// a non-mission file on protected main stays STANDARD (refused)
		`echo x > notes.txt; $SK safe-commit -m notes --to-branch main notes.txt --json; echo rc=$?`,
	)
	compareGit(t, p)
}
