package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// maskSelections hides the template-selection digests: upstream hashes the
// absolute site-packages path of the packaged template into them
// (irreducible), every other material input is compared byte for byte.
const maskSelections = `sed -E 's/("?template-selection:[a-z]+"?: *(\{"path": null, "sha256": "|\n *path: null\n *sha256: )?)[0-9a-f]{64}/\1<SEL>/g'`

// TestDiffRecordAnalysisReportOnly covers `agent mission record-analysis
// --report-only`: the dirty / untracked material-input refusal, carrier
// errors, the committed report transaction (material inputs, receipt,
// report-only commit), the unchanged re-run and unrelated dirty work.
func TestDiffRecordAnalysisReportOnly(t *testing.T) {
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
		`echo "x" >> kitty-specs/$M/spec.md; $SK agent mission record-analysis --mission $M --report-only `+rep+` --json`,
		`$SK agent mission record-analysis --mission $M --report-only `+rep+`; git checkout -q -- kitty-specs`,
		`touch kitty-specs/$M/tasks/notes.md; $SK agent mission record-analysis --mission $M --report-only `+rep+` --json; rm kitty-specs/$M/tasks/notes.md`,
		`printf -- '---\nschema: analysis-findings/v1\nfindings: 3\n---\nbody\n' | $SK agent mission record-analysis --mission $M --report-only --json`,
		`printf -- '---\nschema: analysis-findings/v1\nfindings: 3\n---\nbody\n' | $SK agent mission record-analysis --mission $M --report-only`,
		`printf '   \n' | $SK agent mission record-analysis --mission $M --report-only --json`,
		`set -o pipefail; $SK agent mission record-analysis --mission $M --report-only `+rep+` --json | `+maskSelections+sortCountsJSON,
		`set -o pipefail; $SK agent mission record-analysis --mission $M --report-only `+rep+` --json | `+maskSelections+sortCountsJSON,
		`set -o pipefail; $SK agent mission record-analysis --mission $M --report-only `+rep+` | `+maskSelections+` | sed -E "s/'issue_counts': \\{[^}]*\\}/'issue_counts': <C>/"`,
		`touch stray.txt; printf '# Report\n\nNo carrier.\n' | $SK agent mission record-analysis --mission $M --report-only --agent claude --json | `+maskSelections+sortCountsJSON+`; rm stray.txt`,
		`git log -1 --format=%s%n%b --name-status; ls .git/spec-kitty-report-transactions | wc -l`,
		`sed -i -E 's/^(    sha256: )[0-9a-f]{64}$/\1X/; s/^report_transaction: [0-9a-f]{32}$/report_transaction: T/' kitty-specs/$M/analysis-report.md; grep -c template-selection kitty-specs/$M/analysis-report.md`,
		sortCountsFile,
	)
	p.CompareTree("kitty-specs", true)
}
