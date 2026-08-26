package beads

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestWriterLockCrossProcessContentionAndKilledHolderRecovery(t *testing.T) {
	if os.Getenv("GO_WANT_BEADS_LOCK_HELPER") == "1" {
		writerLockHelper()
		return
	}
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	for iteration := 0; iteration < 25; iteration++ {
		command := exec.Command(os.Args[0], "-test.run=TestWriterLockCrossProcessContentionAndKilledHolderRecovery")
		command.Env = append(os.Environ(), "GO_WANT_BEADS_LOCK_HELPER=1", "BEADS_LOCK_WORKSPACE="+bound.binding.WorkspacePath, "BEADS_LOCK_IDENTITY="+bound.binding.WorkspaceIdentity, "BEADS_LOCK_HOLD_MS=5000")
		stdout, err := command.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil || line != "locked\n" {
			_ = command.Process.Kill()
			t.Fatalf("helper: %q %v", line, err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		_, err = filesystemWriterLocker{}.acquire(ctx, bound.binding)
		cancel()
		if !tracker.IsKind(err, tracker.ErrorLockContended) {
			_ = command.Process.Kill()
			t.Fatalf("iteration %d contention=%v", iteration, err)
		}
		if err := command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = command.Wait()
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		lock, err := filesystemWriterLocker{}.acquire(ctx, bound.binding)
		cancel()
		if err != nil {
			t.Fatalf("iteration %d recovery=%v", iteration, err)
		}
		lock.release()
	}
}

func writerLockHelper() {
	workspace := os.Getenv("BEADS_LOCK_WORKSPACE")
	identity := os.Getenv("BEADS_LOCK_IDENTITY")
	milliseconds, _ := strconv.Atoi(os.Getenv("BEADS_LOCK_HOLD_MS"))
	binding := tracker.TrackerBinding{WorkspacePath: workspace, WorkspaceIdentity: identity}
	lock, err := filesystemWriterLocker{}.acquire(context.Background(), binding)
	if err != nil {
		os.Exit(3)
	}
	defer lock.release()
	fmt.Println("locked")
	time.Sleep(time.Duration(milliseconds) * time.Millisecond)
}

func TestWriterLocksForDifferentWorkspacesDoNotContend(t *testing.T) {
	first, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	second, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-2")
	lock1, err := filesystemWriterLocker{}.acquire(context.Background(), first.binding)
	if err != nil {
		t.Fatal(err)
	}
	defer lock1.release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lock2, err := filesystemWriterLocker{}.acquire(ctx, second.binding)
	if err != nil {
		t.Fatal(err)
	}
	lock2.release()
}

func TestWriterLockCancellationIsBoundedAndPermissionRestricted(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	first, err := filesystemWriterLocker{}.acquire(context.Background(), bound.binding)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	started := time.Now()
	_, err = filesystemWriterLocker{}.acquire(ctx, bound.binding)
	cancel()
	if !tracker.IsKind(err, tracker.ErrorLockContended) || time.Since(started) > time.Second {
		t.Fatalf("error=%v duration=%s", err, time.Since(started))
	}
	info, err := os.Stat(writerLockPath(bound.binding))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestQualifiedServerMutationBypassesLocalWriterLock(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	locker := &countingLocker{err: fmt.Errorf("must not acquire")}
	bound.locker = locker
	request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	response, err := bound.executeMutation(context.Background(), readiness, request)
	if err != nil {
		t.Fatal(err)
	}
	if locker.calls != 0 || !response.result.started || len(runner.calls) != before+1 {
		t.Fatalf("lock=%d response=%#v calls=%v", locker.calls, response, runner.calls[before:])
	}
}

func TestUnqualifiedServerMutationRefusesBeforeLockOrCommand(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	info, err := os.Stat(bound.binding.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	readiness := Readiness{binding: bound.binding, executableSize: info.Size(), executableMTime: info.ModTime()}
	locker := &countingLocker{}
	bound.locker = locker
	request, _ := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	before := len(runner.calls)
	_, err = bound.executeMutation(context.Background(), readiness, request)
	if !tracker.IsKind(err, tracker.ErrorUnavailable) || locker.calls != 0 || len(runner.calls) != before {
		t.Fatalf("error=%v lock=%d calls=%v", err, locker.calls, runner.calls[before:])
	}
}
