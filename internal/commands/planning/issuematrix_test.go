package planning_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffIssueMatrix covers the finalize scaffold, `agent issue-verdict`,
// `issue-matrix migrate` and the move-task approval blocker.
func TestDiffIssueMatrix(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		missionfx.AuthorCalc(t, s, slug)
		spec := filepath.Join("kitty-specs", slug, "spec.md")
		s.WriteFile(spec, s.ReadFile(spec)+"\n## Issues\n\n- Implements #4321 (subtract).\n- Follow-up: #777 later.\n- See PR #88 for context.\n- https://github.com/spec-kitty/spec-kitty/issues/5150 tracks multiply.\n")
		s.Git("commit", "-qam", "issues")
	})
	shellSteps(t, p, g, o,
		`$SK agent mission finalize-tasks --mission $M --json >/dev/null; echo rc=$?`,
		`$SK agent tasks move-task WP01 --to approved --force --mission $M --json`,
		`$SK agent tasks move-task WP01 --to approved --force --mission $M`,
		`$SK agent issue-verdict --mission $M --issue 4321 --verdict bogus --actor me --json`,
		`$SK agent issue-verdict --mission $M --issue 4321 --verdict fixed --actor ' ' --json`,
		`$SK agent issue-verdict --mission $M --issue 4321 --verdict fixed --actor me --wp WP01 --evidence-ref abc123 --json`,
		`$SK agent issue-verdict --mission $M --issue '#5150' --verdict in-mission --actor me --evidence-ref WP02`,
		`$SK agent issue-verdict --mission $M --issue '#9999' --verdict deferred-with-followup --actor me --json`,
		`$SK agent tasks move-task WP01 --to approved --force --mission $M --json`,
		`$SK agent issue-verdict --mission nope --issue 1 --verdict fixed --actor me --json`,
		`$SK issue-matrix migrate --json`,
		`$SK issue-matrix migrate`,
		`$SK issue-matrix >/dev/null; echo rc=$?`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}

// TestDiffIssueMatrixMigrate migrates a legacy issue-matrix.md (lanes topology).
func TestDiffIssueMatrixMigrate(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p, "--topology", "lanes")
	shellSteps(t, p, g, o,
		`printf '# Issues\n\n| issue | verdict | evidence_ref |\n|---|---|---|\n| #12 | fixed | abc |\n| [#34](https://x/34) | deferred-with-followup | none |\n' > kitty-specs/$M/issue-matrix.md; git add -A; git commit -qm md; $SK issue-matrix migrate --mission $M --json`,
		`$SK agent issue-verdict --mission $M --issue 12 --verdict verified-already-fixed --actor me --json`,
		`printf '| a | b |\n|---|---|\n' > other.md; $SK issue-matrix migrate --mission nope --json`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
}
