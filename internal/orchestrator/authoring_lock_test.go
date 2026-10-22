//go:build unix

package orchestrator

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestAuthoringLock pins the cooperative authoring lock (PLAN-12): a real
// flock on <git-common-dir>/spec-kitty-locks/design-<sha256(slug)>.lock,
// reentrant in-process, and DESIGN_LOCK_TIMEOUT when another holder keeps
// it past the timeout. Contention needs two processes, so this is Go-only.
func TestAuthoringLock(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	rel1, err := AcquireAuthoringLock(root, "m-1")
	if err != nil {
		t.Fatal(err)
	}
	rel2, err := AcquireAuthoringLock(root, "m-1") // reentrant
	if err != nil {
		t.Fatal(err)
	}
	path, _ := AuthoringLockPath(root, "m-1")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("lock file: %v", err)
	}
	other, _ := os.OpenFile(path, os.O_RDWR, 0)
	defer other.Close()
	if syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		t.Fatal("lock not held while acquired")
	}
	rel2()
	if syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
		t.Fatal("inner release dropped the outer hold")
	}
	rel1()
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock still held after release: %v", err)
	}
	saved := authoringLockTimeout
	authoringLockTimeout = 150 * time.Millisecond
	defer func() { authoringLockTimeout = saved }()
	_, err = AcquireAuthoringLock(root, "m-1")
	de, ok := err.(*DesignError)
	if !ok || de.Code != "DESIGN_LOCK_TIMEOUT" || de.Message != "The mission authoring lock is busy" {
		t.Fatalf("contended acquire: %v", err)
	}
}
