package planning_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// TestDiffLifecycle covers the top-level specify / plan / tasks wrappers
// (incl. the non-interactive widen interviews) and research.
func TestDiffLifecycle(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	other := `M=$(ls kitty-specs | grep other); `
	deep := `M=$(ls kitty-specs | grep deep); `
	shellSteps(t, p, "", "",
		`$SK specify`,
		`$SK specify '  '`,
		`$SK specify 'Ünï'`,
		`$SK plan`,
		`$SK plan --json`,
		`$SK plan --mission ' ' --json`,
		`$SK plan --mission nope`,
		`$SK plan --mission nope --json`,
		`$SK tasks`,
		`$SK tasks --mission nope --json`,
		`$SK tasks --mission nope`,
		`$SK research`,
		`$SK research --mission nope`,
		`$SK research --mission ../x`,
		`mkdir -p ../nowhere && cd ../nowhere && $SK specify foo --json`,
		`mkdir -p ../nowhere && cd ../nowhere && $SK plan --mission x`,
		`$SK specify 'Calc Ops!' --json`,
		`$SK specify 'third' --mission software-dev --json`,
		`$SK specify 'fourth' --mission software-dev --mission-type research --json`,
		`git switch -q -c feat/x; $SK specify 'other thing' --topology lanes`,
		`cat kitty-specs/other-thing-*/decisions/index.json`,
		`$SK plan --mission other-thing`,
		other+`cp $TD/calc/authored/spec.md kitty-specs/$M/spec.md; git add kitty-specs/$M/spec.md; git commit -qm spec; $SK plan --mission $M`,
		other+`$SK plan --mission $M --json`,
		other+`$SK tasks --mission $M --json`,
		other+`$SK research --mission $M`,
		other+`cp $TD/calc/authored/plan.md kitty-specs/$M/plan.md; $SK research --mission $M`,
		`$SK research --mission other-thing`,
		`$SK specify 'deep dive' --mission-type research --topology lanes`,
		deep+`$SK research --mission $M`,
		deep+`$SK research --mission $M`,
		deep+`echo extra >> kitty-specs/$M/research.md; $SK research --mission $M --force; tail -c 40 kitty-specs/$M/research.md`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
}

// TestDiffLifecycleInteractive drives the widen interviews through their
// prompt path (forced interactive, answers piped on stdin) and the
// name-validation errors under --json.
func TestDiffLifecycleInteractive(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	fi := `export SPEC_KITTY_FORCE_INTERACTIVE=1; `
	shellSteps(t, p, "", "",
		`$SK specify '  ' --json`,
		`$SK specify 'auth日本' --json`,
		fi+`printf 'the problem\n!cancel\nd\n' | $SK specify 'alpha one' --topology lanes`,
		`cat kitty-specs/alpha-one-*/decisions/index.json`,
		fi+`printf 'only one\n' | $SK specify 'beta two' --topology lanes`,
		`cat kitty-specs/beta-two-*/decisions/index.json`,
		fi+`M=$(ls kitty-specs | grep alpha); cp $TD/calc/authored/spec.md kitty-specs/$M/spec.md; git add -A kitty-specs; git commit -qm spec; printf '\nrisky\n' | $SK plan --mission $M`,
		`cat kitty-specs/alpha-one-*/decisions/index.json`,
	)
	p.CompareTree("kitty-specs", true)
	compareGit(t, p)
}
