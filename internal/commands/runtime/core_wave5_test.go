package runtime_test

import (
	"path/filepath"
	"strings"
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

// charterPair is an analyzed calc mission whose project charter was
// generated and synthesized by the oracle (committed).
func charterPair(t *testing.T) *testkit.Pair {
	p := newAnalyzedPair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "charter", "generate", "--no-from-interview", "--json")
		missionfx.RunOracle(t, s, "charter", "synthesize", "--json")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "charter")
	})
	return p
}

// TestDiffCharterPreflightFreshness covers CORE-17: a stale charter bundle
// blocks advancing `next` (human and JSON envelope) and both implement
// surfaces, while query mode logs and continues.
func TestDiffCharterPreflightFreshness(t *testing.T) {
	p := charterPair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.Exec("", "rm", "-f", ".kittify/charter/synthesis-manifest.yaml")
		s.WriteFile(".kittify/charter-packs/graph.yaml", "nodes: []\nedges: []\n")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "stale drg")
	})
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success"},
		[]string{"implement", "WP01", "--mission", "$M"},
		[]string{"implement", "WP01", "--mission", "$M", "--json"},
		[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
	)
	compareGit(t, p)
}

// TestDiffCharterPreflightDefaultProfile covers the deactivated
// mission-default-profile advisories of the passing preflight.
func TestDiffCharterPreflightDefaultProfile(t *testing.T) {
	p := charterPair(t)
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "charter", "activate", "agent-profile", "python-pedro")
		missionfx.RunOracle(t, s, "charter", "deactivate", "agent-profile", "reviewer-renata")
		s.Git("add", "-A")
		s.Git("commit", "-qm", "deactivate")
	})
	steps(t, p, true,
		[]string{"next", "--agent", "claude", "--mission", "$M"},
		[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
	)
}

// TestDiffReconcileSnapshotLoadErrors covers CORE-16: the recorded
// snapshot's load failures carry upstream's exception text (json decode,
// UnicodeDecodeError, ** mapping TypeError, pydantic ValidationError).
func TestDiffReconcileSnapshotLoadErrors(t *testing.T) {
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "lanes")
	hex64 := strings.Repeat("a", 64)
	for _, body := range []string{
		"not json",
		"[1, 2]",
		"{}",
		`{"mission_slug": 5, "parity_hash_sha256": "abc"}`,
		`{"mission_slug": "x", "parity_hash_sha256": "` + hex64 + `", "total_artifacts": -1, "required_artifacts": "3", "optional_present": 1.5, "required_missing": "x", "parity_hash_components": ["a", 1], "artifact_summaries": [1], "computed_at": 5}`,
		`{"mission_slug": "x", "parity_hash_sha256": "sha256:` + strings.Repeat("g", 64) + `", "total_artifacts": true, "snapshot_id": null, "computed_at": "nope"}`,
		"\xff\xfe",
	} {
		p.Both(func(s *testkit.Sandbox) {
			slug := missionfx.MissionSlug(t, s)
			s.WriteFile(filepath.Join("kitty-specs", slug, ".kittify", "dossiers", slug, "snapshot-latest.json"), body)
		})
		steps(t, p, false, []string{"reconcile", "--mission", "$M", "--json"}, []string{"reconcile", "--mission", "$M"})
	}
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
