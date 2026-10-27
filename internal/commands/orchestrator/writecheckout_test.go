package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/testkit"
	"github.com/spec-kitty/go-kitty3/internal/testkit/missionfx"
	"github.com/spec-kitty/go-kitty3/internal/writecheckout"
)

// singleBranchPair builds two finalized single_branch calc missions
// (calc-ops and calc-two) sharing the repository-root write checkout on
// feat/calc-ops.
func singleBranchPair(t *testing.T) *testkit.Pair {
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
	})
	return p
}

// TestDiffSingleBranchWriteCheckoutRefusals drives start-implementation's
// single_branch write-checkout refusals: wrong branch, dirty (with
// spec-kitty's own paths exempt), the resume exemption and cross-Mission
// occupancy.
func TestDiffSingleBranchWriteCheckoutRefusals(t *testing.T) {
	t.Parallel()
	p := singleBranchPair(t)
	start := func(m string) {
		t.Helper()
		step(t, p, api, "start-implementation", "--mission", m, "--wp", "WP01", "--actor", "claude", "--policy", testPolicy)
	}
	p.Both(func(s *testkit.Sandbox) { s.Git("switch", "-q", "-c", "elsewhere") })
	start("calc-ops")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("switch", "-q", "feat/calc-ops")
		s.WriteFile("src/new.py", "x = 1\n")
		s.WriteFile("README.md", "changed\n")
		s.WriteFile(".kittify/runtime/x.json", "{}\n")
		s.WriteFile("kitty-ops/op.jsonl", "{}\n")
	})
	start("calc-ops")
	p.Both(func(s *testkit.Sandbox) {
		s.Git("checkout", "-q", "--", "README.md")
		os.Remove(filepath.Join(s.Dir, "src", "new.py"))
	})
	start("calc-ops")
	compareGit(t, p, "after single_branch claim")
	// resume: a dirty checkout never refuses the WP already in_progress
	// (the short handle never matches the scan's directory names, so it
	// reads its own claim as occupancy — upstream's exclude quirk)
	p.Both(func(s *testkit.Sandbox) { s.WriteFile("work.py", "y = 2\n") })
	start("calc-ops")
	stepM(t, p, api, "start-implementation", "--mission", "$M", "--wp", "WP01", "--actor", "claude", "--policy", testPolicy)
	p.Both(func(s *testkit.Sandbox) { os.Remove(filepath.Join(s.Dir, "work.py")) })
	// another single_branch Mission's WP in the same checkout: occupied
	start("calc-two")
	compareGit(t, p, "after occupied refusal")
}

var reCheckoutLock = regexp.MustCompile(`__checkout-[0-9a-f]{16}__`)

// holdCheckoutLock takes the checkout claim lock of s's project (as another
// claimant would) with a fixed holder record.
func holdCheckoutLock(t *testing.T, s *testkit.Sandbox) {
	t.Helper()
	lock, err := writecheckout.LockPath(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := filelock.TryLockFile(f); !ok || err != nil {
		t.Fatal(ok, err)
	}
	t.Cleanup(func() { f.Close() })
	holder := `{"acquired_at": "2026-01-01T00:00:00+00:00", "pid": 4242, "thread": "MainThread"}`
	if err := os.WriteFile(lock+".holder", []byte(holder), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDiffSingleBranchClaimLockHeld claims while another claimant holds the
// write-checkout claim lock: both CLIs wait out the 120 s bound (run
// concurrently) and fail with STATUS_LOCK_HELD.
func TestDiffSingleBranchClaimLockHeld(t *testing.T) {
	t.Parallel()
	p := singleBranchPair(t)
	p.Both(func(s *testkit.Sandbox) { holdCheckoutLock(t, s) })
	args := []string{api, "start-implementation", "--mission", "calc-ops", "--wp", "WP01", "--actor", "claude", "--policy", testPolicy}
	var g, o testkit.Result
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); g = p.Go.Run(args...) }()
	go func() { defer wg.Done(); o = p.Oracle.RunOracle(args...) }()
	wg.Wait()
	if g.Code != o.Code {
		t.Errorf("exit code go=%d oracle=%d\n--- go:\n%s%s\n--- oracle:\n%s%s", g.Code, o.Code, g.Stdout, g.Stderr, o.Stdout, o.Stderr)
	}
	gs := reCheckoutLock.ReplaceAllString(normEnvelope(p.Go, g.Stdout), "__checkout-<D>__")
	os_ := reCheckoutLock.ReplaceAllString(normEnvelope(p.Oracle, o.Stdout), "__checkout-<D>__")
	if gs != os_ {
		t.Errorf("stdout differs\n%s", testkit.Diff(os_, gs))
	}
	if !strings.Contains(gs, "STATUS_LOCK_HELD") {
		t.Errorf("expected STATUS_LOCK_HELD, got %s", gs)
	}
	// a lanes-topology claim never takes the checkout lock
	step(t, p, api, "mission-state", "--mission", "calc-ops")
}
