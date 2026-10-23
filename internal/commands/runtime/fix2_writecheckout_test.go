package runtime_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
	"github.com/spec-kitty/go-kitty3/internal/writecheckout"
)

// singleBranchRuntimePair builds two finalized, analyzed single_branch calc
// missions (calc-ops and calc-two) sharing the repository-root write
// checkout on feat/calc-ops.
func singleBranchRuntimePair(t *testing.T) *testkit.Pair {
	t.Helper()
	p := missionfx.NewOraclePair(t)
	missionfx.OracleCalcMission(t, p, "--pr-bound", "--branch-strategy", "already-confirmed", "--start-branch", "feat/calc-ops", "--topology", "single_branch")
	p.Both(func(s *testkit.Sandbox) {
		missionfx.RunOracle(t, s, "agent", "mission", "create", "calc-two", "--json", "--pr-bound", "--branch-strategy", "already-confirmed",
			"--start-branch", "feat/calc-ops", "--topology", "single_branch")
		slug := ""
		entries, _ := os.ReadDir(filepath.Join(s.Dir, "kitty-specs"))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "calc-two") {
				slug = e.Name()
			}
		}
		if slug == "" {
			t.Fatal("calc-two mission not created")
		}
		missionfx.AuthorCalc(t, s, slug)
		missionfx.RunOracle(t, s, "agent", "mission", "finalize-tasks", "--mission", slug, "--json")
		for _, m := range []string{"calc-ops", "calc-two"} {
			missionfx.RunOracle(t, s, "agent", "mission", "record-analysis", "--mission", m, "--input-file", missionfx.AnalysisReport(), "--json")
		}
	})
	return p
}

// sbSubst replaces "$calc-ops" / "$calc-two" with the side's full slug.
func sbSubst(t *testing.T, s *testkit.Sandbox, args []string) []string {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "kitty-specs"))
	out := make([]string, len(args))
	for i, a := range args {
		for _, e := range entries {
			for _, pre := range []string{"calc-ops", "calc-two"} {
				if strings.HasPrefix(e.Name(), pre+"-") {
					a = strings.ReplaceAll(a, "$"+pre, e.Name())
				}
			}
		}
		out[i] = a
	}
	return out
}

// sbSteps is steps with the two-mission handle substitution.
func sbSteps(t *testing.T, p *testkit.Pair, withStderr bool, cmds ...[]string) {
	t.Helper()
	for _, c := range cmds {
		g := p.Go.Run(sbSubst(t, p.Go, c)...)
		o := p.Oracle.RunOracle(sbSubst(t, p.Oracle, c)...)
		if os.Getenv("KITTY_TEST_TRACE") != "" {
			t.Logf("%v\n== go %d ==\n%s%s== oracle %d ==\n%s%s", c, g.Code, g.Stdout, g.Stderr, o.Code, o.Stdout, o.Stderr)
		}
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\ngo:\n%s%s\noracle:\n%s%s", c, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
			continue
		}
		if gs, os_ := norm(p.Go, g.Stdout), norm(p.Oracle, o.Stdout); gs != os_ {
			t.Errorf("%v: stdout differs\n%s", c, testkit.Diff(os_, gs))
		}
		if withStderr {
			if ge, oe := norm(p.Go, g.Stderr), norm(p.Oracle, o.Stderr); ge != oe {
				t.Errorf("%v: stderr differs\n%s", c, testkit.Diff(oe, ge))
			}
		}
	}
}

// TestDiffSingleBranchClaimRefusalsImplement drives the single_branch
// write-checkout refusals (wrong branch, dirty with spec-kitty's own paths
// exempt, cross-Mission occupancy) through `implement` and `agent action
// implement`, and the claim base a repo-root claim records.
func TestDiffSingleBranchClaimRefusalsImplement(t *testing.T) {
	t.Parallel()
	p := singleBranchRuntimePair(t)
	claim := func(m string) {
		t.Helper()
		sbSteps(t, p, true,
			[]string{"implement", "WP01", "--mission", "$" + m},
			[]string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$" + m},
		)
	}
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "elsewhere") })
	claim("calc-ops")
	compareGit(t, p)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("switch", "-q", "feat/calc-ops")
		s.WriteFile("src/new.py", "x = 1\n")
		s.WriteFile("README.md", "changed\n")
		s.WriteFile(".kittify/runtime/x.json", "{}\n")
		s.WriteFile("kitty-ops/op.jsonl", "{}\n")
	})
	claim("calc-ops")
	compareGit(t, p)
	p.Both(func(s *testkit.Sandbox) {
		s.Git("checkout", "-q", "--", "README.md")
		os.Remove(filepath.Join(s.Dir, "src", "new.py"))
	})
	sbSteps(t, p, false, []string{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$calc-ops"})
	compareGit(t, p)
	// another single_branch Mission's WP in the same checkout: occupied
	claim("calc-two")
	compareGit(t, p)
}

// TestDiffSingleBranchImplementTopClaim: a clean single_branch claim via
// the top-level `implement` records the claim base before its claim.
func TestDiffSingleBranchImplementTopClaim(t *testing.T) {
	t.Parallel()
	p := singleBranchRuntimePair(t)
	sbSteps(t, p, true, []string{"implement", "WP01", "--mission", "$calc-ops"})
	compareGit(t, p)
}

var reCheckoutLockKey = regexp.MustCompile(`__checkout-[0-9a-f]{16}__`)

// TestDiffSingleBranchClaimLockHeldImplement claims while another claimant
// holds the write-checkout claim lock: `implement` and `agent action
// implement` wait out the 120 s bound and fail with STATUS_LOCK_HELD.
func TestDiffSingleBranchClaimLockHeldImplement(t *testing.T) {
	t.Parallel()
	p := singleBranchRuntimePair(t)
	p.Both(func(s *testkit.Sandbox) {
		lock, err := writecheckout.LockPath(s.Dir)
		if err != nil {
			t.Fatal(err)
		}
		os.MkdirAll(filepath.Dir(lock), 0o700)
		f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		os.WriteFile(lock+".holder", []byte(`{"acquired_at": "2026-01-01T00:00:00+00:00", "pid": 4242, "thread": "MainThread"}`), 0o644)
	})
	cmds := [][]string{
		{"implement", "WP01", "--mission", "$calc-ops"},
		{"agent", "action", "implement", "WP01", "--agent", "claude", "--mission", "$calc-ops"},
	}
	res := make([][2]testkit.Result, len(cmds))
	var wg sync.WaitGroup
	for i, c := range cmds {
		gc, oc := sbSubst(t, p.Go, c), sbSubst(t, p.Oracle, c)
		wg.Add(2)
		go func() { defer wg.Done(); res[i][0] = p.Go.Run(gc...) }()
		go func() { defer wg.Done(); res[i][1] = p.Oracle.RunOracle(oc...) }()
	}
	wg.Wait()
	for i, c := range cmds {
		g, o := res[i][0], res[i][1]
		if g.Code != o.Code {
			t.Errorf("%v: exit go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", c, g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
		}
		gs := reCheckoutLockKey.ReplaceAllString(norm(p.Go, g.Stdout), "__checkout-<D>__")
		os_ := reCheckoutLockKey.ReplaceAllString(norm(p.Oracle, o.Stdout), "__checkout-<D>__")
		if gs != os_ {
			t.Errorf("%v: stdout differs\n%s", c, testkit.Diff(os_, gs))
		}
		if !strings.Contains(gs, "STATUS_LOCK_HELD") {
			t.Errorf("%v: expected STATUS_LOCK_HELD, got %s", c, gs)
		}
	}
}
