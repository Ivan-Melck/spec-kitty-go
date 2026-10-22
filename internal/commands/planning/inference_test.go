package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const (
	wp1 = "kitty-specs/$M/tasks/WP01-subtract-helper.md"
	wp2 = "kitty-specs/$M/tasks/WP02-multiply.md"
)

// TestDiffFinalizeOwnershipInference covers the bootstrap's ownership
// inference (infer_ownership) for WPs without execution_mode / owned_files /
// authoritative_surface, the code_change + explicit `owned_files: []`
// contradiction, the confined planning_artifact exemption from the
// kitty-specs ban, the planning_artifact consistency warning, the
// post-integration acceptance warning and the charter activation gate.
func TestDiffFinalizeOwnershipInference(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) { missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s)) })
	reset := `git checkout -q -- kitty-specs; git clean -qfd kitty-specs; `
	shellSteps(t, p, g, o,
		// contradiction
		`sed -i 's/^owned_files: .*/owned_files: []/' `+wp1+`; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		// confined planning_artifact WP owning its mission's specs (exempt)
		reset+`sed -i -e 's/^execution_mode: .*/execution_mode: "planning_artifact"/' -e 's#^owned_files: .*#owned_files: ["kitty-specs/'"$M"'/notes.md"]#' -e 's#^authoritative_surface: .*#authoritative_surface: "kitty-specs/'"$M"'/"#' -e 's#^create_intent: .*#create_intent: ["kitty-specs/'"$M"'/notes.md"]#' `+wp2+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		// a planning_artifact WP owning code: warning + kitty-specs ban
		reset+`sed -i -e 's/^execution_mode: .*/execution_mode: "planning_artifact"/' -e 's#^owned_files: .*#owned_files: ["kitty-specs/'"$M"'/notes.md", "src/calc_multiply.py"]#' `+wp2+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		reset+`sed -i -e 's/^execution_mode: .*/execution_mode: "planning_artifact"/' -e 's#^owned_files: .*#owned_files: ["docs/x.md", "src/calc_multiply.py"]#' -e 's#^authoritative_surface: .*#authoritative_surface: "docs/"#' -e 's#^create_intent: .*#create_intent: ["docs/x.md", "src/calc_multiply.py"]#' `+wp2+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		// post-integration acceptance criteria
		reset+`printf '\n## Acceptance Criteria\n\n- Works after the merge and once deployed.\n### Notes\n- Post-merge smoke test.\n## Other\n- after merge\n' >> `+wp1+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		// charter activation gate
		reset+`cp .kittify/config.yaml $TMPDIR/cfg-$M; printf 'activated_agent_profiles:\n- architect\n' >> .kittify/config.yaml; $SK agent mission finalize-tasks --mission $M --json; $SK agent mission finalize-tasks --mission $M; cp $TMPDIR/cfg-$M .kittify/config.yaml`,
		`cp .kittify/config.yaml $TMPDIR/cfg-$M; printf 'activated_agent_profiles: []\n' >> .kittify/config.yaml; $SK agent mission finalize-tasks --mission $M --json; cp $TMPDIR/cfg-$M .kittify/config.yaml`,
		// inference: no execution_mode / owned_files / authoritative_surface
		reset+`sed -i -e '/^execution_mode:/d' -e '/^owned_files:/d' -e '/^authoritative_surface:/d' `+wp1+` `+wp2+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		`printf '\nSee docs/guide and kitty-specs/x/spec.md.\n' >> `+wp2+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`sed -i -e 's#src/calc_multiply.py#the multiply module#g' -e 's#tests/test_multiply.py#its tests#g' -e 's#docs/guide#the guide#' -e 's#kitty-specs/x/spec.md#the spec#' `+wp2+`; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --json`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
