package runtime_test

import (
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

// markMerged stamps a merge baseline (merged_at) on the mission's primary
// meta.json and commits it, so `mission close` passes its precondition.
func markMerged(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		rel := filepath.Join("kitty-specs", missionfx.MissionSlug(t, s), "meta.json")
		meta, err := jsonx.DecodeObject([]byte(s.ReadFile(rel)))
		if err != nil {
			t.Fatal(err)
		}
		meta.Set("merged_at", "2026-01-01T00:00:00+00:00")
		s.WriteFile(rel, jsonx.Dumps(meta, jsonx.Options{Indent: 2, SortKeys: true, EnsureASCII: true})+"\n")
		s.Git("commit", "-qam", "merged marker")
	})
}

// TestDiffNextGuardFailurePaths covers CORE-14: guard_failure_paths names
// the repo-relative path each genuinely missing artifact guard read (keyed
// by the family's real artifact tag), for every advancing decision.
func TestDiffNextGuardFailurePaths(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-ops", "--json", "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops")
	})
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
	)
	p.Both(func(s *testkit.Sandbox) {
		s.Exec("", "rm", "-f", filepath.Join("kitty-specs", missionfx.MissionSlug(t, s), "spec.md"))
	})
	steps(t, p, false,
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "failed", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
	)
}

// TestDiffMissionCloseMerged covers CORE-15: closing a merged mission
// persists the retrospective (record + RetrospectiveCaptured event,
// committed) and tears down the coordination worktree with its report line;
// a re-run is idempotent.
func TestDiffMissionCloseMerged(t *testing.T) {
	p := newAnalyzedPair(t)
	markMerged(t, p)
	steps(t, p, true,
		[]string{"mission", "close", "--mission", "$M"},
		[]string{"mission", "close", "--mission", "$M", "--json"},
		[]string{"mission", "close", "--mission", "$M"},
	)
	compareGit(t, p)
	compareTree(t, p, "kitty-specs", "analysis-report.md")
}
