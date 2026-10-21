package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// decisionID extracts the "decision_id" of the last decision opened in s.
const decisionIDCmd = `python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["entries"][-1]["decision_id"])' kitty-specs/$M/decisions/index.json`

// TestDiffDecisions covers TRACE 1.6-1.8 and 9.9 plus list/defer/cancel,
// idempotent re-open, terminal conflicts and validation errors.
func TestDiffDecisions(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	slugs := []string{}
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
		slugs = append(slugs, missionfx.MissionSlug(t, s))
	})
	g, o := slugs[0], slugs[1]
	shellSteps(t, p, g, o,
		`$SK agent decision >/dev/null; echo rc=$?`,
		`$SK agent decision open --mission $M --flow bogus --input-key k --question q --slot-key s`,
		`$SK agent decision open --mission $M --flow specify --input-key k --question q --slot-key s --options 'nope'`,
		`$SK agent decision open --mission $M --flow specify --input-key k --question q --slot-key s --options '{"a":1}'`,
		`$SK agent decision open --mission $M --flow specify --input-key k --question q`,
		`$SK agent decision open --mission ../x --flow specify --input-key k --question q --slot-key s`,
		`$SK agent decision open --mission no-such --flow specify --input-key k --question q --slot-key s`,
		`$SK agent decision open --mission $M --flow specify --input-key k --question q --slot-key s --dry-run`,
		`$SK agent decision open --mission $M --flow specify --slot-key specify.scope.error-handling --input-key error_handling --question 'Should subtract/multiply validate argument types or rely on Python semantics?' --options '["validate","python-semantics","Other"]'`,
		`$SK agent decision open --mission $M --flow specify --slot-key specify.scope.error-handling --input-key error_handling --question 'again?'`,
		`$SK agent decision resolve $(`+decisionIDCmd+`) --mission $M --final-answer python-semantics`,
		`$SK agent decision resolve $(`+decisionIDCmd+`) --mission $M --final-answer python-semantics`,
		`$SK agent decision resolve $(`+decisionIDCmd+`) --mission $M --final-answer other`,
		`$SK agent decision open --mission $M --flow specify --slot-key specify.scope.error-handling --input-key error_handling --question 'again?'`,
		`$SK agent decision verify --mission $M`,
		`$SK agent decision resolve 01ZZZZZZZZZZZZZZZZZZZZZZZZ --mission $M --final-answer x`,
		`$SK agent decision resolve 01ZZZZZZZZZZZZZZZZZZZZZZZZ --mission $M --final-answer ' '`,
		`$SK agent decision open --mission calc-ops --flow plan --step-id plan.q1 --input-key arch --question 'Which architecture?' --actor claude`,
		`$SK agent decision defer $(`+decisionIDCmd+`) --mission $M --rationale ' '`,
		`$SK agent decision defer $(`+decisionIDCmd+`) --mission $M --rationale 'later'`,
		`$SK agent decision verify --mission $M`,
		`$SK agent decision verify --mission $M --no-fail-on-stale`,
		`echo "x [NEEDS CLARIFICATION: arch] <!-- decision_id: $(`+decisionIDCmd+`) -->" >> kitty-specs/$M/spec.md; echo "[NEEDS CLARIFICATION: y] <!-- decision_id: 01ZZZZZZZZZZZZZZZZZZZZZZZZ -->" >> kitty-specs/$M/plan.md; $SK agent decision verify --mission $M`,
		`$SK agent decision resolve $(`+decisionIDCmd+`) --mission $M --final-answer hexagonal --rationale because --other-answer --resolved-by me`,
		`$SK agent decision open --mission $M --flow charter --slot-key c1 --input-key k3 --question 'Q3?'`,
		`$SK agent decision cancel $(`+decisionIDCmd+`) --mission $M --rationale 'obsolete'`,
		`$SK agent decision cancel $(`+decisionIDCmd+`) --mission $M --rationale 'obsolete'`,
		`$SK agent decision defer $(`+decisionIDCmd+`) --mission $M --rationale 'x'`,
		`$SK agent decision list --mission $M`,
		`$SK agent decision list --mission $M --status resolved`,
		`$SK agent decision list --mission $M --status bogus`,
		`$SK agent decision list --mission no-such`,
		`$SK agent decision verify --mission $M`,
		`$SK agent decision repair-runtime-lock --mission $M`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
