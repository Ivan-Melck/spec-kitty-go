package runtime_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

const (
	sourceDocumented    = `{"type": "source_documented"}` + "\n"
	publicationApproved = `{"type": "gate_passed", "name": "publication_approved"}` + "\n"
)

// newComposedPair creates a built-in research or documentation mission
// (oracle) in both sandboxes with files committed. A name is relative to the
// primary mission directory; a "status/" prefix places it in the status
// (coordination worktree) mission directory instead, where the guards read
// unclassified files, the mission event log and docs/.
func newComposedPair(t *testing.T, missionType string, files map[string]string) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", missionType+"-demo", "--mission-type", missionType, "--json")
		slug := missionfx.MissionSlug(t, s)
		dir := filepath.Join("kitty-specs", slug)
		statusDir := filepath.Join(".worktrees", slug+"-coord", "kitty-specs", slug)
		if _, err := os.Stat(filepath.Join(s.Dir, statusDir)); err != nil {
			t.Fatalf("no coordination worktree: %v", err)
		}
		for name, body := range files {
			if rest, ok := strings.CutPrefix(name, "status/"); ok {
				s.WriteFile(filepath.Join(statusDir, rest), body)
				continue
			}
			s.WriteFile(filepath.Join(dir, name), body)
		}
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "mission artifacts")
	})
	return p
}

// TestDiffResearchDocumentationComposedGuards: the research and
// documentation families run a post-composition artifact guard per action;
// a missing artifact (or an unmet event-log fact) blocks the run with the
// guard failures and where each artifact was looked for.
func TestDiffResearchDocumentationComposedGuards(t *testing.T) {
	next := []string{"next", "--agent", "codex", "--mission", "$M", "--result", "success", "--json"}
	text := []string{"next", "--agent", "codex", "--mission", "$M", "--result", "success"}
	query := []string{"next", "--agent", "codex", "--mission", "$M", "--json"}
	// walk advances n times in JSON, once more in text, then queries.
	walk := func(t *testing.T, p *testkit.Pair, n int) {
		t.Helper()
		cmds := make([][]string, n)
		for i := range cmds {
			cmds[i] = next
		}
		steps(t, p, false, cmds...)
		steps(t, p, false, text, query)
		compareTree(t, p, "kitty-ops")
		compareTree(t, p, "kitty-specs", "mission-events.jsonl")
	}
	full := map[string]string{
		"spec.md":                     "# Research question\n",
		"plan.md":                     "# Methodology\n",
		"findings.md":                 "# Findings\n",
		"report.md":                   "# Report\n",
		"status/source-register.csv":  "id,citation\n",
		"status/mission-events.jsonl": strings.Repeat(sourceDocumented, 3) + publicationApproved,
	}
	without := func(src map[string]string, drop ...string) map[string]string {
		out := map[string]string{}
		for k, v := range src {
			out[k] = v
		}
		for _, d := range drop {
			delete(out, d)
		}
		return out
	}
	t.Run("research-scoping-missing", func(t *testing.T) {
		t.Parallel()
		walk(t, newComposedPair(t, "research", nil), 2)
	})
	t.Run("research-gathering-unmet", func(t *testing.T) {
		t.Parallel()
		// The register is unclassified: a primary copy is not read.
		files := without(full, "status/source-register.csv", "status/mission-events.jsonl")
		files["source-register.csv"] = "id,citation\n"
		files["status/mission-events.jsonl"] = sourceDocumented + "not json\n[1]\n" + sourceDocumented
		walk(t, newComposedPair(t, "research", files), 4)
	})
	t.Run("research-output-unapproved", func(t *testing.T) {
		t.Parallel()
		files := without(full, "report.md", "status/mission-events.jsonl")
		files["status/mission-events.jsonl"] = strings.Repeat(sourceDocumented, 3) + `{"type": "gate_passed", "name": "other"}` + "\n"
		walk(t, newComposedPair(t, "research", files), 6)
	})
	t.Run("research-complete", func(t *testing.T) {
		t.Parallel()
		// Every guard passes up to the issued accept step (completing it
		// writes the terminal retrospective, ported separately).
		walk(t, newComposedPair(t, "research", full), 5)
	})
	docs := map[string]string{
		"spec.md":            "# Documentation scope\n",
		"gap-analysis.md":    "# Gaps\n",
		"plan.md":            "# Design\n",
		"status/docs/a/b.md": "# Guide\n",
		"audit-report.md":    "# Audit\n",
		"release.md":         "# Release\n",
	}
	t.Run("documentation-audit-missing", func(t *testing.T) {
		t.Parallel()
		walk(t, newComposedPair(t, "documentation", without(docs, "gap-analysis.md")), 3)
	})
	t.Run("documentation-generate-missing", func(t *testing.T) {
		t.Parallel()
		// docs/ is read from the status directory only.
		files := without(docs, "status/docs/a/b.md")
		files["docs/guide.md"] = "# Guide\n"
		files["status/docs/notes.txt"] = "not markdown\n"
		walk(t, newComposedPair(t, "documentation", files), 5)
	})
	t.Run("documentation-complete", func(t *testing.T) {
		t.Parallel()
		walk(t, newComposedPair(t, "documentation", docs), 6)
	})
}
