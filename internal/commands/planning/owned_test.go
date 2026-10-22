package planning_test

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// maskCounts hides issue_counts (a frozenset-ordered dict upstream: its key
// order is PYTHONHASHSEED-dependent).
const maskCounts = ` | sed -E 's/"issue_counts": \{[^}]*\}/"issue_counts": <C>/'`

// ownedGitState is gitState plus the owned checkout's status.
func ownedGitState(s *testkit.Sandbox) string {
	r := s.Exec(missionfx.OwnedDir(s), "git", "status", "--short", "--untracked-files=all")
	return gitState(s) + s.Normalize("$ git -C <owned> status --short\n"+r.Stdout)
}

func compareOwnedGit(t *testing.T, p *testkit.Pair) {
	t.Helper()
	if g, o := ownedGitState(p.Go), ownedGitState(p.Oracle); g != o {
		t.Errorf("git state differs:\n%s", testkit.Diff(o, g))
	}
}

// ownedPlanningSteps are the planning-family owned commands over the calc
// mission in P; $OWN is "--owned-checkout ../owned" (flag) or "" (flagless
// adoption from P, $CD = "cd ../owned &&").
func ownedPlanningSteps(own, cd string) []string {
	r := strings.NewReplacer("$OWN", own, "$CD", cd)
	steps := []string{
		`$CD $SK agent mission check-prerequisites $OWN --mission $M --json`,
		`$CD $SK agent mission check-prerequisites $OWN --mission $M --json --paths-only`,
		`$CD $SK agent mission check-prerequisites $OWN --mission $M`,
		`$CD $SK agent mission check-prerequisites $OWN --mission $M --resume-probe --json`,
		`$SK agent mission check-prerequisites --owned-checkout . --mission $M --json`,
		`$SK agent mission check-prerequisites --owned-checkout . --mission $M`,
		`$CD $SK agent decision open --mission $M --flow specify --input-key k1 --question Which --options '["a","b"]' $OWN`,
		`$CD $SK agent decision open --mission $M --flow specify --input-key k1 --question Which $OWN --dry-run`,
		`$CD $SK agent decision list --mission $M $OWN`,
		`$CD $SK agent decision resolve $(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["entries"][0]["decision_id"])' ../owned/kitty-specs/$M/decisions/index.json) --mission $M --final-answer a $OWN`,
		`$CD $SK agent decision verify --mission $M $OWN`,
		`$SK agent decision list --mission $M --owned-checkout .`,
		`$SK agent decision repair-runtime-lock --mission $M --owned-checkout ../owned --dry-run`,
		`$SK agent decision repair-runtime-lock --mission $M`,
		`git -C ../owned add -A && git -C ../owned commit -qm "track decisions incl lock" && $SK agent decision repair-runtime-lock --mission $M --owned-checkout ../owned --dry-run`,
		`$SK agent decision repair-runtime-lock --mission $M --owned-checkout ../owned`,
		`$SK agent decision repair-runtime-lock --mission $M --owned-checkout ../owned`,
		`$CD $SK research --mission $M $OWN`,
		`$CD $SK agent mission finalize-tasks $OWN --mission $M --json`,
		`$CD $SK agent tasks map-requirements --wp WP01 --refs FR-001 --mission $M $OWN --json`,
		`$CD $SK agent tasks map-requirements --wp WP01 --refs FR-001 --mission $M $OWN`,
		`$SK agent tasks map-requirements --wp WP09 --refs FR-001 --mission $M --owned-checkout /nonexistent-owned --json`,
		`$CD $SK agent mission record-analysis --mission $M $OWN --input-file $TD/calc/analysis-report.md --json` + maskCounts,
		`$CD $SK agent mission record-analysis --mission $M $OWN --input-file $TD/calc/analysis-report.md --report-only --json` + maskCounts,
		`$SK agent mission record-analysis --mission $M --owned-checkout . --input-file $TD/calc/analysis-report.md --json`,
	}
	for i, s := range steps {
		steps[i] = r.Replace(s)
	}
	return steps
}

// TestDiffOwnedPlanningCommands covers OWN-03/OWN-04 for the planning
// family (check-prerequisites incl. --resume-probe refusal, decision
// open/list/resolve/verify, repair-runtime-lock, research, finalize-tasks,
// map-requirements, record-analysis incl. --report-only) with
// --owned-checkout from R and by flagless adoption from P.
func TestDiffOwnedPlanningCommands(t *testing.T) {
	cases := []struct{ name, own, cd string }{
		{"flag-from-R", "--owned-checkout ../owned", ""},
		{"adopt-from-P", "", "cd ../owned &&"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOwnedPair(t)
			g, o := missionfx.OwnedCalcMission(t, p)
			shellSteps(t, p, g, o, ownedPlanningSteps(c.own, c.cd)...)
			compareOwnedGit(t, p)
			shellSteps(t, p, g, o, "cd ../owned && "+sortCountsFile+` && sed -i -E 's/^(report_transaction: )[0-9a-f]{32}$/\1<T>/' kitty-specs/$M/analysis-report.md`)
			p.CompareTree(missionfx.OwnedRel+"/kitty-specs", true)
		})
	}
}
