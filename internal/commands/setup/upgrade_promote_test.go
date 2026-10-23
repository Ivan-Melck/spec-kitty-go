package setup_test

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// writeAnswersOnlySelections records interview selections never mirrored
// into config.yaml: unify_charter_activation_promote_answers (3.2.6rc1)
// promotes them, seeding an absent activated_<kind> key from the
// effective set first (absent-key parity) and appending to a present one.
// DIRECTIVE_010 resolves through the directive's declared id; the
// interview-only answers.yaml (no compiled charter) also pins the number
// of encoding-provenance records the profile gate writes per dry run.
func writeAnswersOnlySelections(s *testkit.Sandbox) {
	s.WriteFile(".kittify/charter/interview/answers.yaml", "mission: software-dev\nprofile: minimal\n"+
		"selected_directives:\n- 024-locality-of-change\n- DIRECTIVE_010\n- not-a-directive\n"+
		"selected_tactics:\n- acceptance-test-first\n")
	cfg := s.ReadFile(".kittify/config.yaml")
	s.WriteFile(".kittify/config.yaml", cfg+"activated_tactics:\n- tdd-red-green-refactor\n")
}

func TestUpgradePromoteAnswersMatchesOracle(t *testing.T) {
	t.Parallel()
	runCorpus(t, corpus{name: "promote-answers", ai: "claude", version: "3.2.5", mutate: writeAnswersOnlySelections})
}

// With a declared-but-unfetched org pack the effective set is unresolved:
// the absent key is left absent and reported (never a bare list).
func TestUpgradePromoteAnswersUnfetchedPackMatchesOracle(t *testing.T) {
	t.Parallel()
	runCorpus(t, corpus{name: "promote-answers-unfetched", ai: "claude", version: "3.2.5", mutate: func(s *testkit.Sandbox) {
		writeAnswersOnlySelections(s)
		cfg := s.ReadFile(".kittify/config.yaml")
		s.WriteFile(".kittify/config.yaml", cfg+"charter_packs:\n  org:\n    packs:\n    - name: acme\n      local_path: packs/acme\n")
	}})
}
