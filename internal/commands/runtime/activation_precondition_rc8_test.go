package runtime_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func TestDiffDeniedWPProfileRC8(t *testing.T) {
	p := newAnalyzedPair(t)
	p.Both(func(s *testkit.Sandbox) {
		slug := missionfx.MissionSlug(t, s)
		md := filepath.Join("kitty-specs", slug, "tasks")
		entries, _ := filepath.Glob(filepath.Join(s.Dir, md, "WP01*.md"))
		if len(entries) != 1 {
			t.Fatal(entries)
		}
		text := s.ReadFile(entries[0])
		text = strings.Replace(text, "---\n", "---\nagent_profile: implementer-ivan\n", 1)
		s.WriteFile(entries[0], text)
		s.WriteFile(".kittify/charter/activation.yaml", "deactivated_agent_profiles: [implementer-ivan]\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "deny assigned profile")
	})
	steps(t, p, true, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
}
