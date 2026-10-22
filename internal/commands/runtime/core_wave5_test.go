package runtime_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// gitConfigState renders the repository-local git config (sorted) and the
// info/attributes file, the merge-driver activation's observable surface.
func gitConfigState(s *testkit.Sandbox) string {
	lines := strings.Split(strings.TrimSpace(s.Exec("", "git", "config", "--local", "--list").Stdout), "\n")
	sort.Strings(lines)
	attrs := s.Exec("", "sh", "-c", "cat \"$(git rev-parse --git-common-dir)/info/attributes\" 2>/dev/null || echo '<no info/attributes>'").Stdout
	return s.Normalize(strings.Join(lines, "\n") + "\n--\n" + attrs)
}

// TestDiffMergeDriverActivation covers CORE-10: merging an approved
// dependency lane tip into a dependent lane runs under the ephemeral
// merge-driver activation (driver git-config definitions persisted,
// info/attributes seeding torn down).
func TestDiffMergeDriverActivation(t *testing.T) {
	p := newAnalyzedPair(t)
	steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{
		"src/calc.py": "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
	})
	oracleBoth(t, p, "agent", "status", "emit", "WP01", "--to", "approved", "--actor", "rev", "--force", "--reason", "ok", "--mission", "$M")
	steps(t, p, false, []string{"agent", "action", "implement", "WP02", "--agent", "claude", "--mission", "$M"})
	if g, o := gitConfigState(p.Go), gitConfigState(p.Oracle); g != o {
		t.Errorf("git config differs:\n%s", testkit.Diff(o, g))
	}
	compareGit(t, p)
}

// reportOnlyPair is the calc mission analysed with a report-only
// (receipt-qualified, material-manifest) analysis report by the oracle.
func reportOnlyPair(t *testing.T) *testkit.Pair {
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p)
	oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--report-only", "--input-file", missionfx.AnalysisReport(), "--json")
	return p
}

// TestDiffAnalysisFreshnessReportOnly covers the report-only receipt and
// recorded material inputs of check_analysis_report_current: a qualified
// report is current; a changed material input or a missing receipt makes it
// stale for the implement gate.
func TestDiffAnalysisFreshnessReportOnly(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		t.Parallel()
		p := reportOnlyPair(t)
		steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
		compareGit(t, p)
	})
	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		p := reportOnlyPair(t)
		p.Both(func(s *testkit.Sandbox) {
			s.WriteFile(".kittify/config.yaml", s.ReadFile(".kittify/config.yaml")+"# material change\n")
			s.Git("commit", "-qam", "material change")
		})
		steps(t, p, false,
			[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"},
			[]string{"next", "--agent", "claude", "--mission", "$M", "--result", "success", "--json"},
		)
		p.Both(func(s *testkit.Sandbox) {
			s.Git("revert", "--no-edit", "HEAD")
			s.Exec("", "sh", "-c", `rm -rf "$(git rev-parse --absolute-git-dir)/spec-kitty-report-transactions"`)
		})
		steps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M"})
	})
}

// reviewRemotePair is WP01 implemented and moved to for_review by the
// oracle, with every branch pushed to a local bare origin; teammate pushes
// one more commit onto the lane on origin.
func reviewRemotePair(t *testing.T, localCommit bool) *testkit.Pair {
	p := newAnalyzedPair(t)
	oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
	laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{
		"src/calc.py": "def add(a, b):\n    return a + b\n\n\ndef subtract(a, b):\n    return a - b\n",
	})
	oracleBoth(t, p, "agent", "tasks", "mark-status", "T001", "T002", "T003", "--status", "done", "--mission", "$M")
	oracleBoth(t, p, "agent", "tasks", "move-task", "WP01", "--to", "for_review", "--mission", "$M", "--agent", "claude", "--note", "Ready")
	p.Both(func(s *testkit.Sandbox) {
		bare := filepath.Join(s.Base, "remote.git")
		mate := filepath.Join(s.Base, "teammate")
		s.Exec("", "git", "init", "-q", "--bare", bare)
		s.Git("remote", "add", "origin", bare)
		s.Git("push", "-q", "origin", "--all")
		lane := "kitty/mission-" + missionfx.MissionSlug(t, s) + "-lane-a"
		s.Exec("", "git", "clone", "-q", "--branch", lane, bare, mate)
		os.WriteFile(filepath.Join(mate, "TEAM.md"), []byte("teammate work\n"), 0o644)
		for _, args := range [][]string{{"add", "TEAM.md"}, {"commit", "-q", "-m", "teammate on lane"}, {"push", "-q", "origin", lane}} {
			if r := s.Exec(mate, "git", args...); r.Code != 0 {
				t.Fatalf("teammate git %v: %s", args, r.Stderr)
			}
		}
		if localCommit {
			wt := filepath.Join(".worktrees", missionfx.MissionSlug(t, s)+"-lane-a")
			s.WriteFile(filepath.Join(wt, "LOCAL.md"), "local work\n")
			s.Exec(wt, "git", "add", "LOCAL.md")
			s.Exec(wt, "git", "commit", "-q", "-m", "local on lane")
		}
	})
	return p
}

// TestDiffReviewLaneOriginFreshness covers CORE-12: `agent action review`
// fast-forwards a lane strictly behind its origin copy and refuses a
// diverged one (warn mode keeps it with a warning).
func TestDiffReviewLaneOriginFreshness(t *testing.T) {
	t.Run("behind", func(t *testing.T) {
		t.Parallel()
		p := reviewRemotePair(t, false)
		steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
		compareGit(t, p)
	})
	t.Run("diverged", func(t *testing.T) {
		t.Parallel()
		p := reviewRemotePair(t, true)
		steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
		p.Both(func(s *testkit.Sandbox) { s.Env = append(s.Env, "SPEC_KITTY_ORIGIN_CHECK=warn") })
		steps(t, p, false, []string{"agent", "action", "review", "WP01", "--agent", "claude", "--mission", "$M"})
		compareGit(t, p)
	})
}

var reBuiltInRef = regexp.MustCompile(`\((/[^)]*?)(/built-in/[^)]*)\)`)

// TestDiffNextWPPromptGovernance covers CORE-13: the WP prompt carries the
// mission-type governance payload (when the status surface has meta.json),
// the WP agent_profile's cited directives/tactics and the mission-type-aware
// charter context. Built-in asset paths are masked (accepted presentation
// difference: site-packages vs the Go pack cache).
func TestDiffNextWPPromptGovernance(t *testing.T) {
	for _, topo := range []string{"coord", "lanes"} {
		topo := topo
		t.Run(topo, func(t *testing.T) {
			t.Parallel()
			p := missionfx.NewOraclePair(t)
			args := []string{"--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops"}
			if topo == "lanes" {
				args = append(args, "--topology", "lanes")
			}
			missionfx.OracleCalcMission(t, p, args...)
			p.Both(func(s *testkit.Sandbox) {
				missionfx.RunOracle(t, s, "charter", "generate", "--no-from-interview", "--json")
				missionfx.RunOracle(t, s, "charter", "synthesize", "--json")
				s.Git("add", "-A")
				s.Git("commit", "-qm", "charter")
			})
			oracleBoth(t, p, "agent", "mission", "record-analysis", "--mission", "$M", "--input-file", missionfx.AnalysisReport(), "--json")
			prompt := func(s *testkit.Sandbox, exe string) string {
				r := s.Exec("", exe, "next", "--agent", "claude", "--mission", missionfx.MissionSlug(t, s), "--result", "success", "--json")
				var d struct {
					PromptFile string `json:"prompt_file"`
				}
				if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil || d.PromptFile == "" {
					t.Fatalf("next: %v\n%s%s", err, r.Stdout, r.Stderr)
				}
				b, err := os.ReadFile(d.PromptFile)
				if err != nil {
					t.Fatal(err)
				}
				return s.Normalize(reBuiltInRef.ReplaceAllString(string(b), "(<BUILTIN>$2)"))
			}
			g, o := prompt(p.Go, testkit.Binary(t)), prompt(p.Oracle, testkit.Oracle(t))
			if g != o {
				t.Errorf("WP prompt differs:\n%s", testkit.Diff(o, g))
			}
		})
	}
}

// TestDiffMissionCloseDiscard covers `mission close --discard`: the loss
// announcement, coordination teardown with a runtime_abandoned
// retrospective, guarded lane worktree / branch deletion (a lane holding
// the only copy of commits is refused), the kept discarded tip, the
// residual-artifact verification, flatten + discarded_at marker and the
// bookkeeping commit.
func TestDiffMissionCloseDiscard(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		p := newAnalyzedPair(t)
		steps(t, p, false, []string{"mission", "close", "--mission", "$M", "--discard", "--force"})
		compareGit(t, p)
		compareTree(t, p, "kitty-specs", "analysis-report.md", "meta.json")
	})
	t.Run("unique-lane-commits", func(t *testing.T) {
		t.Parallel()
		p := newAnalyzedPair(t)
		oracleBoth(t, p, "agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$M")
		laneCommit(t, p, "lane-a", "feat(WP01): subtract", map[string]string{"src/calc.py": "def sub(a, b):\n    return a - b\n"})
		steps(t, p, false,
			[]string{"mission", "close", "--mission", "$M", "--discard", "--force"},
			[]string{"mission", "close", "--mission", "$M", "--discard", "--force", "--json"},
		)
		compareGit(t, p)
	})
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
