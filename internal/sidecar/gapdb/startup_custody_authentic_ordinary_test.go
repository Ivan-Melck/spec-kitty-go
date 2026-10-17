//go:build linux

package gapdb

// UNRUN authentic ORDINARY sidecar fixture controls, SEPARATE from provider-free
// components. startTestLifecycle uses the existing safeAdmittedCopy admission;
// it may skip without a future admitted fixture. No admission is added/widened.
// TempDir residuals and automatic fixture cleanup prove NO durable retention.
import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"testing"
)

func TestAbortOrdinaryOwnedChildSuccessRetainsExpectedExitStatus(t *testing.T) {
	l := startTestLifecycle(t, "first-custody-abort")
	defer stopTestLifecycle(t, l)
	if err := l.abortStart(); err != nil { t.Fatal(err) }
	f := l.startupAbort.facts
	var status *exec.ExitError
	if !f.signalAttempted || !f.waitObserved || !f.reapProven || f.waitTimedOut || f.runtimeDeletionWithheld || !f.runtimeCleanupAttempted || f.residualPossible || !errors.As(f.waitErr, &status) { t.Fatalf("ordinary abort changed: %+v", f) }
	if _, err := os.Lstat(l.provenance.RuntimeDirectory); !errors.Is(err, os.ErrNotExist) { t.Fatal("attributed cleanup changed", err) }
	if err := l.abortStart(); err != nil { t.Fatal("repeat changed FIRST", err) }
}

func TestAbortReapedChildStillCannotDeleteReplacementSocket(t *testing.T) {
	l := startTestLifecycle(t, "first-custody-socket")
	var first error
	defer func() {
		got := l.Stop(context.Background())
		if first != nil { if got != first { t.Error("later Stop lost abort FIRST", got) } } else if got != nil { t.Error(got) }
	}()
	path := l.provenance.ConfiguredSocket
	if err := os.Rename(path, path+"-original"); err != nil { t.Fatal(err) }
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil { t.Fatal(err) }
	defer replacement.Close()
	before, err := os.Lstat(path)
	if err != nil { t.Fatal(err) }
	err = l.abortStart()
	first = err
	var outcome *startupAbortError
	if !errors.As(err, &outcome) || !outcome.facts.reapProven || !outcome.facts.runtimeCleanupAttempted || !outcome.facts.residualPossible { t.Fatal("replacement socket cleanup not refused", err) }
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) { t.Fatal("replacement socket removed", err) }
}
