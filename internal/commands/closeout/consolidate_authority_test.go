package closeout_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func targetBranch(s *testkit.Sandbox) string {
	return strings.TrimSpace(s.Git("branch", "--show-current"))
}

// foreignTargetCommit lands a commit on the target branch (checked out in
// the primary checkout) as another actor would.
func foreignTargetCommit(t *testing.T, p *testkit.Pair, file string) {
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile(file, "foreign\n")
		s.Git("add", file)
		s.Git("commit", "-q", "-m", "foreign: "+file)
	})
}

// editState rewrites every merge record of the sandbox with fn.
func editState(t *testing.T, p *testkit.Pair, fn func(o *jsonx.Object, s *testkit.Sandbox)) {
	p.Both(func(s *testkit.Sandbox) {
		matches, _ := filepath.Glob(filepath.Join(s.Dir, ".kittify", "runtime", "merge", "*", "state.json"))
		if len(matches) == 0 {
			t.Fatalf("no merge record in %s", s.Dir)
		}
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			o, err := jsonx.DecodeObject(b)
			if err != nil {
				t.Fatal(err)
			}
			fn(o, s)
			os.WriteFile(m, []byte(jsonx.Dumps(o, jsonx.Pretty)), 0o644)
		}
	})
}

// TestDiffConsolidateRollbackAuthority covers the record-anchored rollback
// authority: a between-attempts move of a settled branch re-anchors its
// restore target (ADR A2), a move of an unsettled branch the record cannot
// explain refuses (UNEXPLAINED_BRANCH_MOVE), --abort reports it NOT
// restored and keeps the record, and an operator release keeps it.
func TestDiffConsolidateRollbackAuthority(t *testing.T) {
	p := acceptedPair(t)
	teammateCommit(t, p, map[string]string{"src/teammate.py": "T = 1\n"}, "teammate: new file")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	p.CompareTree(".kittify/runtime", true)
	foreignTargetCommit(t, p, "foreign1.txt")
	comparePair(t, p, "consolidate", "--mission", "$M", "--resume")
	compareGit(t, p)
	p.CompareTree(".kittify/runtime", true)
	editState(t, p, func(o *jsonx.Object, s *testkit.Sandbox) {
		o.Set("unsettled_refs", []any{targetBranch(s)})
	})
	foreignTargetCommit(t, p, "foreign2.txt")
	comparePair(t, p, "consolidate", "--mission", "$M", "--resume")
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	comparePair(t, p, "consolidate", "--mission", "$M", "--abort")
	compareGit(t, p)
	p.CompareTree(".kittify/runtime", true)
	tb := targetBranch(p.Go)
	comparePair(t, p, "consolidate", "--mission", "$M", "--abort", "--release-branch", tb, "--release-reason", "keep the foreign commit")
	compareGit(t, p)
	p.CompareTree(".kittify/runtime", true)
}

var reLaneHead = regexp.MustCompile(`"lane_head": "[0-9a-f]{40}"(, )?`)

// TestDiffConsolidateAttestApproved covers APPROVAL_STAMP_MISSING at claim
// time and --attest-approved-reviewed recording the operator attestation
// (a forced approved -> approved transition) before the claim, which then
// passes; plus the sparse-checkout preflight.
func TestDiffConsolidateAttestApproved(t *testing.T) {
	p := acceptedPair(t)
	p.Both(func(s *testkit.Sandbox) {
		matches, _ := filepath.Glob(filepath.Join(s.Dir, ".worktrees", "*-coord", "kitty-specs", "*", "status.events.jsonl"))
		if len(matches) != 1 {
			t.Fatalf("event log: %v", matches)
		}
		b, _ := os.ReadFile(matches[0])
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, `"wp_id": "WP01"`) && strings.Contains(l, `"to_lane": "approved"`) {
				l = reLaneHead.ReplaceAllString(l, "")
				l = strings.ReplaceAll(l, `, }`, `}`)
			}
			out = append(out, l)
		}
		os.WriteFile(matches[0], []byte(strings.Join(out, "\n")), 0o644)
		wt := filepath.Dir(filepath.Dir(filepath.Dir(matches[0])))
		if r := s.Exec(wt, "git", "commit", "-q", "-am", "drop WP01 approval stamp"); r.Code != 0 {
			t.Fatalf("commit: %s%s", r.Stdout, r.Stderr)
		}
	})
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	comparePair(t, p, "consolidate", "--mission", "$M", "--attest-approved-reviewed", "WP02", "--attest-reason", "checked")
	comparePair(t, p, "consolidate", "--mission", "$M", "--attest-canceled-superseded", "WP02", "--attest-reason", "checked")
	p.Both(func(s *testkit.Sandbox) { s.Git("config", "core.sparseCheckout", "true") })
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes")
	// the override proceeds (to the claim refusal); its WARNING log record goes to stderr
	comparePair(t, p, "consolidate", "--mission", "$M", "--yes", "--allow-sparse-checkout")
	p.Both(func(s *testkit.Sandbox) { s.Git("config", "--unset", "core.sparseCheckout") })
	comparePair(t, p, "consolidate", "--mission", "$M", "--attest-approved-reviewed", "WP01", "--attest-reason", "checked by hand")
	compareGit(t, p)
	compareMissionTree(t, p, false)
}
