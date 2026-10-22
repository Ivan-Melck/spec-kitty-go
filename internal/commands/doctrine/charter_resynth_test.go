package doctrine_test

import (
	"strings"
	"testing"
)

// TestParityCharterActivateResynthesize covers `--resynthesize` (preflight +
// full generate/synthesize) on activate, deactivate and --preset, and the
// activate-time tension warnings (DOC-15).
func TestParityCharterActivateResynthesize(t *testing.T) {
	t.Parallel()
	p := newOraclePair(t)
	for _, args := range [][]string{
		{"charter", "activate", "tactic", "acceptance-test-first", "--resynthesize"},
		{"charter", "activate", "directive", "025-boy-scout-rule", "--no-compile"},
		{"charter", "deactivate", "directive", "reconcile-change-scope-tensions", "--no-compile"},
		{"charter", "activate", "directive", "024-locality-of-change", "--no-compile"},
		{"charter", "activate", "--preset", "minimal", "--force", "--resynthesize", "--json"},
		{"charter", "interview", "--defaults"},
		{"charter", "activate", "tactic", "acceptance-test-first", "--resynthesize", "--cascade", "all"},
		{"charter", "activate", "--preset", "default", "--force", "--resynthesize", "--json"},
		{"charter", "deactivate", "directive", "024-locality-of-change", "--resynthesize"},
	} {
		compareCharter(t, p, args...)
	}
}

func TestCharterActivateResynthesizeGoOnly(t *testing.T) {
	t.Parallel()
	s := newProject(t)
	r := s.Run("charter", "activate", "tactic", "acceptance-test-first", "--resynthesize")
	if r.Code != 1 || !strings.Contains(r.Stdout, "No charter interview answers found") {
		t.Fatalf("preflight: %d %s", r.Code, r.Stdout)
	}
	if strings.Contains(s.ReadFile(".kittify/config.yaml"), "activated_tactics") {
		t.Fatalf("preflight must not write the activation")
	}
}
