package maintenance_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffDoctorWorkspacesSparse(t *testing.T) {
	p := newOraclePair(t)
	compareSteps(t, p, "", "",
		step{"doctor", "workspaces"},
		step{"doctor", "workspaces", "--json"},
		step{"doctor", "sparse-checkout"},
		step{"doctor", "sparse-checkout", "--fix"},
	)
	g, o := missionfx.OracleCalcMission(t, p)
	p.Both(func(s *testkit.Sandbox) {
		os.MkdirAll(filepath.Join(s.Dir, ".worktrees", "empty-husk"), 0o755)
		s.WriteFile(".worktrees/busy-husk/notes.txt", "keep me\n")
		s.Git("worktree", "add", "-q", ".worktrees/real-wt", "-b", "real-wt")
		os.MkdirAll(filepath.Join(s.Dir, ".worktrees", "gone-wt"), 0o755)
		s.Git("worktree", "add", "-q", ".worktrees/gone-wt2", "-b", "gone-wt2")
		os.Remove(filepath.Join(s.Dir, ".worktrees", "gone-wt2", ".git"))
	})
	compareSteps(t, p, g, o,
		step{"doctor", "workspaces"},
		step{"doctor", "workspaces", "--json"},
		step{"doctor", "workspaces", "--fix", "--json"},
		step{"doctor", "workspaces", "--fix"},
		step{"doctor", "workspaces"},
	)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("config", "core.sparseCheckout", "true")
		s.WriteFile(".git/info/sparse-checkout", "/*\n# c\n!/docs\n")
	})
	compareSteps(t, p, g, o,
		step{"doctor", "sparse-checkout"},
		step{"doctor", "sparse-checkout", "--fix"},
	)
}
