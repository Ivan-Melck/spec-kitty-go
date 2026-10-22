//go:build unix

package maintenance_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// TestDiffDoctorMissionStateFixLock: the repair lock is held as an advisory
// flock (contention refuses), its record is truncated on release, and a
// symlink planted at the lock path is refused rather than followed.
func TestDiffDoctorMissionStateFixLock(t *testing.T) {
	p := newOraclePair(t)
	p.Both(repairCorpus)
	lock := func(s *testkit.Sandbox) string { return join(s.Dir, ".git", "spec-kitty-mission-state.lock") }
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--fix", "--json", "--allow-dirty"},
	)
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		if b, err := os.ReadFile(lock(s)); err != nil || len(b) != 0 {
			t.Errorf("%s: lock file after release: %q %v", s.Dir, b, err)
		}
	}
	// a held lock refuses a second repair
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		f, err := os.OpenFile(lock(s), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		defer f.Close()
	}
	compareSteps(t, p, "", "",
		step{"doctor", "mission-state", "--fix", "--json", "--allow-dirty"},
		step{"doctor", "mission-state", "--fix", "--allow-dirty"},
	)
	// a planted symlink is never followed
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		os.Remove(lock(s))
		os.WriteFile(join(s.Base, "victim"), []byte("keep"), 0o644)
		if err := os.Symlink(join(s.Base, "victim"), lock(s)); err != nil {
			t.Fatal(err)
		}
	}
	g, o := p.Run("doctor", "mission-state", "--fix", "--json", "--allow-dirty")
	if g.Code != o.Code || g.Stdout != o.Stdout || p.Go.Normalize(lastLine(g.Stderr)) != p.Oracle.Normalize(lastLine(o.Stderr)) {
		t.Errorf("symlinked lock: go=%d %q %q oracle=%d %q %q", g.Code, g.Stdout, lastLine(g.Stderr), o.Code, o.Stdout, lastLine(o.Stderr))
	}
	for _, s := range []*testkit.Sandbox{p.Go, p.Oracle} {
		if b, _ := os.ReadFile(join(s.Base, "victim")); string(b) != "keep" {
			t.Errorf("%s: symlink target modified: %q", s.Dir, b)
		}
	}
}
