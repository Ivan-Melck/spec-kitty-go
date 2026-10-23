package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// protectTarget declares the target branch protected in .kittify/config.yaml.
func protectTarget(t *testing.T, p *testkit.Pair, branch string) {
	p.Both(func(s *testkit.Sandbox) {
		cfg := s.ReadFile(".kittify/config.yaml")
		if strings.Contains(cfg, "\nprotection:") {
			t.Fatal("config already declares protection")
		}
		s.WriteFile(".kittify/config.yaml", strings.TrimRight(cfg, "\n")+"\nprotection:\n  protected_branches:\n  - "+branch+"\n")
		s.Git("add", ".kittify/config.yaml")
		s.Git("commit", "-q", "-m", "protect "+branch)
	})
}

// TestDiffConsolidateMissionRefusals covers the consolidate-mission
// refusals evaluated before the first lane merge: the approved bound (#5668,
// LANE_MOVED_AFTER_APPROVAL with preflight_error_code(s)) and the protected
// status target (#5385, PROTECTED_BRANCH_REFUSED on a lanes mission).
func TestDiffConsolidateMissionRefusals(t *testing.T) {
	t.Run("approved-bound", func(t *testing.T) {
		t.Parallel()
		p := nativeApproval(t)
		step2M(t, p, api, "accept-mission", "--mission", "$M", "--actor", "rev")
		p.Both(func(s *testkit.Sandbox) {
			s.Git("add", "-A")
			s.Git("commit", "-q", "-m", "record acceptance")
			wt := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-lane-a")
			s.WriteFile(filepath.Join(wt, "src", "late.py"), "LATE = 1\n")
			s.Exec(wt, "git", "add", "src/late.py")
			if r := s.Exec(wt, "git", "commit", "-q", "-m", "late commit"); r.Code != 0 {
				t.Fatalf("commit: %s", r.Stderr)
			}
		})
		step2M(t, p, api, "consolidate-mission", "--mission", "$M")
		compareGit(t, p, "after approved-bound refusal")
	})
	t.Run("protected-target", func(t *testing.T) {
		t.Parallel()
		p := nativeApproval(t, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
		step2M(t, p, api, "accept-mission", "--mission", "$M", "--actor", "rev")
		p.Both(func(s *testkit.Sandbox) {
			s.Git("add", "-A")
			s.Git("commit", "-q", "-m", "record acceptance")
		})
		protectTarget(t, p, "feat/calc-ops")
		step2M(t, p, api, "consolidate-mission", "--mission", "$M")
		compareGit(t, p, "after protected refusal")
	})
}
