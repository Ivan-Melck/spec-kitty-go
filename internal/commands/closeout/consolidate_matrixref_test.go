package closeout_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
)

func setMergeGatesBlock(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		cfg := strings.TrimRight(s.ReadFile(".kittify/config.yaml"), "\n") + "\npolicy:\n  merge_gates:\n    mode: block\n"
		s.WriteFile(".kittify/config.yaml", cfg)
		s.Git("add", ".kittify/config.yaml")
		s.Git("commit", "-q", "-m", "gate policy block")
	})
}

// coordWorktree is the coordination worktree path relative to s.Dir.
func coordWorktree(t *testing.T, s *testkit.Sandbox) string {
	m, _ := filepath.Glob(filepath.Join(s.Dir, ".worktrees", "*-coord"))
	if len(m) != 1 {
		t.Fatalf("coord worktree: %v", m)
	}
	rel, _ := filepath.Rel(s.Dir, m[0])
	return rel
}

// removeCoordWorktree unmaterializes the coordination worktree, keeping
// its branch.
func removeCoordWorktree(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		if r := s.Exec("", "git", "worktree", "remove", "--force", coordWorktree(t, s)); r.Code != 0 {
			t.Fatalf("worktree remove: %s", r.Stderr)
		}
	})
}

const inMissionMatrix = `{
  "rows": {
    "#12": {
      "evidence_ref": "",
      "verdict": "in-mission"
    }
  },
  "schema_version": 1
}
`

// commitPrimaryMatrix commits a stale in-mission issue-matrix.json on the
// primary checkout (residue the coordination reads must never consult).
func commitPrimaryMatrix(t *testing.T, p *testkit.Pair) {
	p.Both(func(s *testkit.Sandbox) {
		rel := filepath.Join("kitty-specs", slugOf(t, s), "issue-matrix.json")
		s.WriteFile(rel, inMissionMatrix)
		s.Git("add", rel)
		s.Git("commit", "-q", "-m", "stale primary issue matrix")
	})
}

var reCorrID = regexp.MustCompile(`corr-[0-9a-f]{32}`)

// compareEnvelope compares an orchestrator-api envelope (correlation id
// masked) and the exit code.
func compareEnvelope(t *testing.T, p *testkit.Pair, args ...string) {
	t.Helper()
	g := p.Go.Run(subst(args, slugOf(t, p.Go))...)
	o := p.Oracle.RunOracle(subst(args, slugOf(t, p.Oracle))...)
	if g.Code != o.Code {
		t.Errorf("exit go=%d oracle=%d", g.Code, o.Code)
	}
	gs := reCorrID.ReplaceAllString(p.Go.Normalize(g.Stdout), "corr-X")
	ostd := reCorrID.ReplaceAllString(p.Oracle.Normalize(o.Stdout), "corr-X")
	if gs != ostd {
		t.Errorf("%s: stdout differs\n%s", strings.Join(args, " "), testkit.Diff(ostd, gs))
	}
}

// TestDiffConsolidateIssueMatrixCoordRef covers the merge gates on a coord
// mission whose coordination worktree is not materialized. consolidate
// materializes it from its branch before reading status (the write
// accessor); orchestrator-api consolidate-mission does not, so its
// issue-matrix gates read the matrix from the coordination ref
// (resolve_issue_matrix_partition), never the primary residue: an
// in-mission row there blocks; a ref with no matrix (NOT_AUTHORED) or an
// empty one passes even when the primary carries a stale in-mission row.
func TestDiffConsolidateIssueMatrixCoordRef(t *testing.T) {
	verdict := []string{"agent", "issue-verdict", "--mission", "$M", "--issue", "#12", "--verdict", "in-mission", "--actor", "claude", "--json"}
	api := []string{"orchestrator-api", "consolidate-mission", "--mission", "$M"}
	cases := map[string]func(t *testing.T, p *testkit.Pair){
		"consolidate": func(t *testing.T, p *testkit.Pair) {
			oracleBoth(t, p, verdict...)
			removeCoordWorktree(t, p)
			comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
		},
		"api-in-mission": func(t *testing.T, p *testkit.Pair) {
			oracleBoth(t, p, verdict...)
			removeCoordWorktree(t, p)
			compareEnvelope(t, p, api...)
		},
		"api-not-authored": func(t *testing.T, p *testkit.Pair) {
			commitPrimaryMatrix(t, p)
			removeCoordWorktree(t, p)
			compareEnvelope(t, p, api...)
		},
		"api-no-rows": func(t *testing.T, p *testkit.Pair) {
			oracleBoth(t, p, verdict...)
			p.Both(func(s *testkit.Sandbox) {
				wt := coordWorktree(t, s)
				rel := filepath.Join("kitty-specs", slugOf(t, s), "issue-matrix.json")
				s.WriteFile(filepath.Join(wt, rel), "{\n  \"rows\": {},\n  \"schema_version\": 1\n}\n")
				s.Exec(wt, "git", "add", rel)
				if r := s.Exec(wt, "git", "commit", "-q", "-m", "empty matrix"); r.Code != 0 {
					t.Fatalf("commit: %s", r.Stderr)
				}
			})
			commitPrimaryMatrix(t, p)
			removeCoordWorktree(t, p)
			compareEnvelope(t, p, api...)
		},
	}
	for name, run := range cases {
		name, run := name, run
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := acceptedPair(t)
			setMergeGatesBlock(t, p)
			run(t, p)
			compareGit(t, p)
		})
	}
}

// TestDiffReviewIssueMatrixCoordRef covers the post-merge mission-review
// issue-matrix gate once the spec cites a gating issue (it used to report
// not_applicable unconditionally). After consolidate the mission is
// flattened and the matrix is read from the primary dir. After a PR-mode
// acceptance (baseline recorded, still coordination-routed) whose
// coordination worktree lacks the mission dir (EMPTY, so status reads fall
// back to the primary), the matrix is read from the coordination ref, where
// a ref without a matrix is ISSUE_MATRIX_REF_READ_FAILED. The report
// records issue_matrix_present. (With the worktree removed outright,
// upstream's review-artifact check raises CoordinationWorktreeUnmaterialized
// before this gate.)
func TestDiffReviewIssueMatrixCoordRef(t *testing.T) {
	for _, name := range []string{"consolidated-fixed", "consolidated-no-matrix", "pr-fixed", "pr-no-matrix"} {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := approvedPair(t, "approved")
			recordVerdictsAndDocs(t, p, true)
			if strings.HasSuffix(name, "-fixed") {
				oracleBoth(t, p, "agent", "issue-verdict", "--mission", "$M", "--issue", "#12", "--verdict", "fixed", "--evidence-ref", "abc123", "--actor", "claude", "--json")
			}
			if strings.HasPrefix(name, "pr-") {
				p.Both(func(s *testkit.Sandbox) {
					oracleArgs := subst([]string{"accept", "--mission", "$M", "--mode", "pr", "--merge-commit", scaffoldSHA(t, s), "--attest-first-landing-commit", "--json"}, slugOf(t, s))
					missionfx.RunOracle(t, s, oracleArgs...)
					dir := filepath.Join(s.Dir, coordWorktree(t, s), "kitty-specs", slugOf(t, s))
					if err := os.RemoveAll(dir); err != nil {
						t.Fatal(err)
					}
				})
			} else {
				oracleBoth(t, p, "agent", "mission", "accept", "--mission", "$M", "--json")
				oracleBoth(t, p, "consolidate", "--mission", "$M", "--yes")
			}
			p.Both(func(s *testkit.Sandbox) {
				rel := filepath.Join("kitty-specs", slugOf(t, s), "spec.md")
				s.WriteFile(rel, s.ReadFile(rel)+"\nThis mission fixes #12.\n")
			})
			comparePair(t, p, "review", "--mission", "$M")
			p.Both(func(s *testkit.Sandbox) {
				rel := filepath.Join("kitty-specs", slugOf(t, s), "mission-review-report.md")
				b, _ := os.ReadFile(filepath.Join(s.Dir, rel))
				s.WriteFile(rel, regexp.MustCompile(`reviewed_at: .*`).ReplaceAllString(string(b), "reviewed_at: <TS>"))
			})
			compareMissionTree(t, p, false)
		})
	}
}
