package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffTracerAndRepair covers `agent tracer-append` (coord surface) and
// `agent mission repair` (clean / fast-forward / divergent / no coord).
func TestDiffTracerAndRepair(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	g, o := oracleCreate(t, p)
	shellSteps(t, p, g, o,
		`$SK agent tracer-append --mission $M --category approach --entry 'Use a helper' --actor claude --json`,
		`$SK agent tracer-append --mission $M --category approach --entry 'Use a helper' --actor claude --json`,
		`$SK agent tracer-append --mission $M --category tooling-friction --entry 'slow tests' --actor claude | sed -E 's/committed [0-9a-f]{7}/committed SHA/'`,
		`$SK agent tracer-append --mission $M --category bogus --entry x --actor claude --json`,
		`$SK agent tracer-append --mission $M --category approach --entry x --actor ' ' --json`,
		`$SK agent tracer-append --mission $M --category approach --entry x --actor ' '`,
		`$SK agent tracer-append --mission nope --category approach --entry x --actor me --json`,
		`$SK agent mission repair --mission nope`,
		`$SK agent mission repair --mission $M | sed -E "s/, [0-9a-f]{8}\\)/, H)/g; /^index /d"`,
		`git -C .worktrees/$M-coord merge -q --ff-only feat/calc-ops 2>/dev/null; $SK agent mission repair --mission $M | sed -E "s/, [0-9a-f]{8}\\)/, H)/g; /^index /d"`,
		`echo x >> README.md; git commit -qam adv; $SK agent mission repair --mission $M | sed -E "s/, [0-9a-f]{8}\\)/, H)/g; /^index /d"`,
		`$SK agent mission repair --mission $M | sed -E "s/, [0-9a-f]{8}\\)/, H)/g; /^index /d"`,
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".worktrees", true)
	compareGit(t, p)
}

// TestDiffIntake covers `intake` (path, stdin, handoff packet, --show,
// --force, --auto and the refusals).
func TestDiffIntake(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	shellSteps(t, p, "", "",
		`$SK intake`,
		`$SK intake --show`,
		`$SK intake nope.md`,
		`$SK intake --auto`,
		`printf '# Plan\n\nDo things.\n' > plan.md; $SK intake plan.md`,
		`$SK intake plan.md`,
		`$SK intake --show | sed 's/[0-9a-f]\{64\}/HASH/; s/ingested from [^ ]* at [^ ]*/X/'`,
		`printf -- '---\nhandoff_packet: 1\nsource_tool: claude-code\nrequirements:\n  - id: R1\n    statement: do it\nconstraints:\n  - id: K1\n---\nbody\n' | $SK intake - --force`,
		`cat .kittify/brief-source.yaml | sed 's/[0-9a-f]\{64\}/HASH/; s/ingested_at: .*/ingested_at: X/'`,
		`mkdir -p .cursor/plans; echo '# c' > .cursor/plans/a.md; $SK intake --auto --force`,
		`echo '# g' > .cursor/plans/b.md; $SK intake --auto --force`,
		`$SK intake plan.md --auto`,
	)
}
