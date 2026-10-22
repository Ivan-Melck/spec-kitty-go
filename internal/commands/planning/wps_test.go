package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// wpsCalc is a wps.yaml outline for the calc mission: WP01 declares its
// dependencies explicitly, WP02 omits the key (frontmatter fallback, #5448).
const wpsCalc = `work_packages:
  - id: WP01
    title: Subtraction and shared helper
    dependencies: []
    owned_files: [src/calc.py, tests/test_subtract.py]
    requirement_refs: [FR-001, SC-001]
    subtasks: [T001, T002, T003]
    prompt_file: tasks/WP01-subtract-helper.md
  - id: WP02
    title: Multiplication
    owned_files: [src/calc_multiply.py, tests/test_multiply.py]
    requirement_refs: [FR-002]
    subtasks: [T004, T005]
    plan_concern_refs: [IC-02]
`

func writeWps(body string) string {
	return "cat > kitty-specs/$M/wps.yaml <<'EOF'\n" + body + "EOF\n"
}

// TestDiffFinalizeWpsOutline covers the tasks-outline manifest stage
// (wps.yaml, no WP prompt files yet) and the wps.yaml load errors.
func TestDiffFinalizeWpsOutline(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s))
		s.Exec("", "bash", "-c", "rm -f kitty-specs/*/tasks/WP*.md kitty-specs/*/tasks.md && git add -A && git commit -qm outline")
	})
	shellSteps(t, p, g, o,
		writeWps(wpsCalc)+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		`$SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		// missing requirement refs in the outline
		writeWps("work_packages:\n  - id: WP01\n    title: A\n    requirement_refs: [FR-001]\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		// schema errors (pydantic ValidationError text)
		writeWps("work_packages:\n  - id: X1\n    title: 3\n    dependencies: [WP1]\n    owned_files: a\n    subtasks: [1, b]\n    prompt_file: 7\n    plan_concern_refs: [IC-1]\n    cross_cutting: maybe\n  - plain\n  - title: no id\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		writeWps("work_packages:\n  - id: WP01\n    title: 'a very long title that is longer than fifty characters in total'\n    cross_cutting: 2\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		writeWps("other: 1\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		writeWps("work_packages: 5\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		// AttributeError shapes
		writeWps("- a\n- b\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`: > kitty-specs/$M/wps.yaml; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		writeWps("hello\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		// non-UTF-8 bytes (WpsManifestReadError)
		`printf 'work_packages: []\n\xff\n' > kitty-specs/$M/wps.yaml; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		// an empty outline
		writeWps("work_packages: []\n")+`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
}

// TestDiffFinalizeWps covers finalize with wps.yaml plus WP prompt files and
// no hand-written tasks.md: dependency resolution (explicit manifest value,
// frontmatter fallback), tasks.md regeneration (stale report under
// --validate-only), the concern-coverage warnings and the commit.
func TestDiffFinalizeWps(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s))
		s.Exec("", "bash", "-c", "rm -f kitty-specs/*/tasks.md && git add -A && git commit -qm wps")
	})
	shellSteps(t, p, g, o,
		writeWps(wpsCalc)+`git add -A; git commit -qm wps; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		// IC-## headings in plan.md turn on the concern-coverage warnings
		`printf '\n## IC-01 Subtraction\n\n## IC-02 Multiplication\n' >> kitty-specs/$M/plan.md; $SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only`,
		`git add -A; git commit -qm concerns; $SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M --validate-only --json`,
		`$SK agent mission finalize-tasks --mission $M`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}

// TestDiffFinalizeWpsHuman runs the human-mode wps.yaml finalize and the
// manifest-driven refusals (cycle across manifest and frontmatter values,
// dependency disagreement).
func TestDiffFinalizeWpsHuman(t *testing.T) {
	t.Parallel()
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.AuthorCalc(t, s, missionfx.MissionSlug(t, s))
		s.Exec("", "bash", "-c", "rm -f kitty-specs/*/tasks.md && git add -A && git commit -qm wps")
	})
	shellSteps(t, p, g, o,
		writeWps("work_packages:\n  - id: WP01\n    title: S\n    dependencies: [WP02]\n  - id: WP02\n    title: M\n")+`$SK agent mission finalize-tasks --mission $M --json`,
		`$SK agent mission finalize-tasks --mission $M`,
		writeWps("work_packages:\n  - id: WP01\n    title: S\n  - id: WP02\n    title: M\n    dependencies: [WP03]\n")+`$SK agent mission finalize-tasks --mission $M --json`,
		writeWps("work_packages:\n  - id: WP01\n    title: S\n    dependencies: []\n  - id: WP02\n    title: M\n    dependencies: []\n")+`$SK agent mission finalize-tasks --mission $M`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}
