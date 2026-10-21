package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// sortCounts canonicalises issue_counts (upstream iterates a frozenset, so
// the key order is PYTHONHASHSEED-dependent) in JSON output and in the
// written report.
const sortCountsJSON = ` | python3 -c 'import json,sys
for line in sys.stdin:
    try:
        d=json.loads(line)
    except Exception:
        print(line, end=""); continue
    if isinstance(d,dict) and isinstance(d.get("issue_counts"),dict):
        d["issue_counts"]=dict(sorted(d["issue_counts"].items()))
    print(json.dumps(d))'`

const sortCountsFile = `python3 -c 'import re,sys
p=sys.argv[1]; t=open(p).read()
m=re.search(r"issue_counts:\n((?:  .*\n)+)", t)
if m:
    t=t.replace(m.group(1), "".join(sorted(m.group(1).splitlines(True))))
open(p,"w").write(t)' kitty-specs/$M/analysis-report.md`

// TestDiffRecordAnalysis covers TRACE 4.4-4.6: the DIRTY_WORKTREE refusal,
// recovery through safe-commit and the committed report.
func TestDiffRecordAnalysis(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := missionfx.OracleCalcMission(t, p)
	shellSteps(t, p, g, o,
		`$SK agent mission record-analysis --mission $M --input-file nope.md --json`,
		`echo dirty > stray.txt; $SK agent mission record-analysis --mission $M --input-file $TD/calc/analysis-report.md --json`,
		`$SK agent mission record-analysis --mission $M --input-file $TD/calc/analysis-report.md`,
		`rm stray.txt; printf '  \n' | $SK agent mission record-analysis --mission $M --json`,
		`$SK agent mission record-analysis --mission nope --input-file $TD/calc/analysis-report.md --json`,
		`$SK agent mission record-analysis --mission $M --input-file $TD/calc/analysis-report.md --json`+sortCountsJSON,
		sortCountsFile,
		`git checkout -q -- . ; printf '## Free-form report\n\nAll good.' | $SK agent mission record-analysis --mission $M --agent claude --json`+sortCountsJSON,
		`printf -- '---\nschema: analysis-findings/v1\nfindings:\n  - id: X\n    severity: bogus\n---\nbody\n' | $SK agent mission record-analysis --mission $M --json`,
		`printf -- '---\nschema: analysis-findings/v1\nfindings:\n  - id: X\n    severity: high\n    summary: bad\nverdict_hint: ready\n---\nbody\n' | $SK agent mission record-analysis --mission $M --json`,
		`printf -- '---\nschema: analysis-findings/v1\nfindings:\n  - id: X\n    severity: high\n    summary: bad\n---\nbody\n' | $SK agent mission record-analysis --mission $M`,
		sortCountsFile,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
	_ = testkit.Diff
}
