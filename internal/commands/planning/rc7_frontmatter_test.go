package planning_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffRC7FinalizeFrontmatterErrors(t *testing.T) {
	testkit.Oracle(t)
	p := &testkit.Pair{T: t, Go: missionfx.NewLocalMission(t), Oracle: missionfx.NewLocalMission(t)}
	for _, content := range []string{"No frontmatter\n", "---\ntitle: missing fence\n", "---\n[]\n---\n", "---\n1.5\n---\n", "---\n|\n  hello\n---\n", "---\n!custom hello\n---\n"} {
		p.Both(func(s *testkit.Sandbox) {
			s.WriteFile(filepath.Join("kitty-specs", missionfx.LocalSlug, "tasks", "WP01-subtract-helper.md"), content)
		})
		for _, jsonMode := range []bool{true, false} {
			args := []string{"agent", "mission", "finalize-tasks", "--mission", missionfx.LocalSlug, "--validate-only"}
			if jsonMode {
				args = append(args, "--json")
			}
			p.Compare(true, args...)
		}
		p.Compare(true, "agent", "action", "implement", "WP01", "--agent", "codex", "--mission", missionfx.LocalSlug)
	}
}
