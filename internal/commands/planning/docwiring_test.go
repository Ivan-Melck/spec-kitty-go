package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const setIterationMode = `python3 -c "import json,sys; p='kitty-specs/$M/meta.json'; d=json.load(open(p)); d['documentation_state']['iteration_mode']=sys.argv[1]; open(p,'w').write(json.dumps(d, indent=2, sort_keys=True)+'\n')" `

// maskGap hides the wall-clock analysis date / audit stamp.
const maskGap = ` | sed -E 's/Analysis Date\*\*: [0-9: -]+/Analysis Date**: X/'`

// TestDiffSetupPlanDocumentationWiring covers setup-plan's documentation
// mission wiring: generator detection (persisted to meta.json and
// committed), the missing-docs warning, the gap analysis for gap_filling /
// mission_specific missions (report, audit metadata, commit) and the
// complete-coverage failure upstream reports as a warning.
func TestDiffSetupPlanDocumentationWiring(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p, "--mission-type", "documentation", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/docs")
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		s.WriteFile("kitty-specs/"+slug+"/spec.md", missionfx.Read(t, "calc/authored/spec.md"))
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "spec")
	})
	shellSteps(t, p, g, o,
		`$SK agent mission setup-plan --mission $M --json`,
		`$SK agent mission setup-plan --mission $M`,
		setIterationMode+`gap_filling; git commit -qam mode; $SK agent mission setup-plan --mission $M --json`,
		`$SK agent mission setup-plan --mission $M`,
		`mkdir -p docs/guides docs/api; printf '# Getting started\n\nStep 1: install. Next, run it.\n' > docs/guides/start.md; printf -- '---\ntype: reference\n---\n# API\n' > docs/api/ref.md; printf '# Why\n\nBackground and architecture.\n' > docs/why.md; git add -A; git commit -qm docs; $SK agent mission setup-plan --mission $M --json`,
		`cat kitty-specs/$M/gap-analysis.md`+maskGap,
		`python3 -c "import json; d=json.load(open('kitty-specs/$M/meta.json'))['documentation_state']; d.pop('last_audit_date'); print(json.dumps(d, sort_keys=True))"`,
		setIterationMode+`feature_specific; git commit -qam mode2; $SK agent mission setup-plan --mission $M`+maskGap,
		// complete coverage: upstream's report renderer fails (warning only)
		`rm -rf docs/*; mkdir -p docs/x; for t in tutorial how-to reference explanation; do printf -- "---\ntype: $t\n---\n# $t\n" > docs/x/$t.md; done; git add -A; git commit -qm full; $SK agent mission setup-plan --mission $M; git status --short`,
	)
	compareGit(t, p)
}
