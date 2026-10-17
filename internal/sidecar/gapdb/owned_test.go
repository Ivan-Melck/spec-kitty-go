package gapdb

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

// FIRST actual source results remain distinct: full close cannot repair a
// failed response write or write-half shutdown. Peer disconnects in deliberate
// cancellation/refusal cases are diagnostics, not positive fixture evidence.
type strictFixtureSourceOutcome struct {
	writeAttempted, shutdownAttempted, closeAttempted bool
	writeErr, shutdownErr, closeErr error
}

func (s strictFixtureSourceOutcome) report(t *testing.T) {
	t.Helper()
	if first := errors.Join(s.writeErr, s.shutdownErr, s.closeErr); first != nil {
		t.Logf("FIRST actual fixture response outcomes retained: attempted(write/shutdown/close)=%t/%t/%t write=%v shutdown=%v close=%v union=%v; peer-disconnect diagnostics are not positive source evidence or a cancellation attribution", s.writeAttempted, s.shutdownAttempted, s.closeAttempted, s.writeErr, s.shutdownErr, s.closeErr, first)
	}
	for _, err := range []error{s.writeErr, s.shutdownErr} {
		if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, syscall.ENOTCONN) {
			t.Errorf("FIRST fixture response source failure: %v", err)
		}
	}
	if s.closeErr != nil { t.Errorf("FIRST fixture connection close failed; residual/unqualified: %v", s.closeErr) }
}

// Privileged package-local lifecycle fixture only. No public caller Anchor,
// admission record, receipt, production Host or census claim is constructed.
func ownedLifecycleFixture(t *testing.T) *Lifecycle {
	t.Helper()
	root, err := os.MkdirTemp("", "lo-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	path := filepath.Join(root, "s")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := processStartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	image, digest, err := processExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	socket, err := socketPathIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	process := ProcessIdentity{PID: os.Getpid(), StartTicks: ticks, Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
	status := gapdbapi.StatusResult{Lifecycle: gapdbapi.LifecycleReady, DatabaseID: "private-fixture-db", OwnerPID: process.PID, OwnerStartedAt: "2026-01-01T00:00:00Z", Limits: gapdbapi.DefaultOptions().Limits}
	directoryInfo, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := identityFromInfo(directoryInfo)
	if err != nil {
		t.Fatal(err)
	}
	l := &Lifecycle{runtimeID: directory, pidfd: fd, process: process, processImage: image, socketID: socket, ownedEndpoint: path, ownedBootstrap: status, processes: systemProcessAPI,
		provenance: SidecarProvenance{RuntimeDirectory: root, RuntimeDevice: directory.device, RuntimeInode: directory.inode, ConnectedSocket: path, ConfiguredSocket: path, DatabaseID: status.DatabaseID, Process: process, Observation: CompositeObservation{Status: status}, Readiness: ReadinessPredicates{Admitted: true}}}
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				var source strictFixtureSourceOutcome
				defer func() { source.closeAttempted = true; source.closeErr = conn.Close(); source.report(t) }()
				if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil { t.Error("fixture server deadline", err); return }
				raw, err := readWireFrame(conn, storegapdb.MaxWireFrameBytes)
				if err != nil {
					return
				}
				var req wireRequest
				if json.Unmarshal(raw, &req) != nil {
					return
				}
				response, err := json.Marshal(map[string]any{"schema_version": storegapdb.AdmittedWireVersion, "ok": true, "database_id": status.DatabaseID, "request_id": req.RequestID, "operation": req.Operation, "result": status})
				if err != nil { t.Error("fixture response encoding", err); return }
				source.writeAttempted = true
				source.writeErr = writeWireFrame(conn, response)
				if source.writeErr == nil { source.shutdownAttempted = true; source.shutdownErr = conn.CloseWrite() }
			}()
		}
	}()
	t.Cleanup(func() {
		l.revokeOwned()
		if lifetime := l.retained.Load(); lifetime != nil {
			_ = lifetime.Close()
		}
		_ = unix.Close(fd)
		_ = listener.Close()
		<-done
		workers.Wait()
	})
	return l
}
func TestOwnedLifecycleFactoryImmutableOriginalBootstrapAndNoRevival(t *testing.T) {
	l := ownedLifecycleFixture(t)
	original := l.OriginalProcessIdentity()
	// A returned provenance copy is not the authority supplied to the factory.
	copy := l.Provenance()
	copy.Process.PID++
	copy.Observation.Status.DatabaseID = "foreign"
	copy.ConnectedSocket = "/untrusted/proxy"
	if l.OriginalProcessIdentity() != original {
		t.Fatal("process identity mutable")
	}
	adapter, closer, err := l.DialOwned(t.Context(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	first := l.retained.Load()
	if first == nil {
		t.Fatal("factory did not retain lifetime")
	}
	if _, err := adapter.Get(t.Context(), "k"); err == nil {
		t.Fatal("fixture status masqueraded as record")
	}
	// Private mutable readiness/provenance isn't bootstrap input either. Production
	// leaves this status frozen; the separate original capture is what is trusted.
	l.mu.Lock()
	l.provenance.Observation.Status.DatabaseID = "foreign"
	l.mu.Unlock()
	_, next, err := l.DialOwned(t.Context(), time.Second)
	if err != nil {
		t.Fatal("bootstrap reconstructed from provenance", err)
	}
	defer next.Close()
	if l.retained.Load() != first {
		t.Fatal("factory minted a replacement lifetime")
	}
	l.revokeOwned()
	if _, _, err := l.DialOwned(t.Context(), time.Second); !IsFailure(err, FailureIdentityUnprovable) {
		t.Fatal("revoked factory revived", err)
	}
	if err := first.Check(t.Context()); !errors.Is(err, retainedkernel.ErrRevoked) {
		t.Fatal(err)
	}
}
func TestOwnedLifecycleStopBeginCancelledAndFailedAreTerminal(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancelled", false: "failed"}[cancelled], func(t *testing.T) {
			l := ownedLifecycleFixture(t)
			before := ownedPidfds(t)
			_, closer, err := l.DialOwned(t.Context(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer closer.Close()
			if after := ownedPidfds(t); after != before+1 {
				t.Fatalf("factory pidfds %d -> %d", before, after)
			}
			lifetime := l.retained.Load()
			processes := systemProcessAPI
			processes.startTicks = func(int) (uint64, error) {
				if !l.ownedRevoked.Load() {
					t.Error("shutdown identity inspected before revocation")
				}
				if err := lifetime.Check(context.Background()); !errors.Is(err, retainedkernel.ErrRevoked) {
					t.Error("shutdown inspection can still observe", err)
				}
				return l.process.StartTicks, nil
			}
			processes.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error {
				if !cancelled {
					return errors.New("forced shutdown signal failure")
				}
				return nil
			}
			l.processes = processes
			l.exit = make(chan error, 1)
			l.config.StopTimeout = time.Second
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			err = l.Stop(ctx)
			if err == nil {
				t.Fatal("expected cancelled/failed shutdown")
			}
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if after := ownedPidfds(t); after != before {
				t.Fatalf("terminal Stop leaked observation pidfd: %d -> %d", before, after)
			}
			_ = closer.Close()
			_ = closer.Close()
			_ = l.Stop(ctx)
			if after := ownedPidfds(t); after != before {
				t.Fatalf("repeated cleanup leaked: %d -> %d", before, after)
			}
			if l.pidfd < 0 {
				t.Fatal("failed Stop closed supervisor descriptor")
			}
			if l.stopped {
				t.Fatal("fixture should remain not stopped on failure")
			}
			if _, _, err := l.DialOwned(t.Context(), time.Second); !IsFailure(err, FailureIdentityUnprovable) {
				t.Fatal("failed/cancelled Stop factory revived", err)
			}
		})
	}
}
func TestOwnedLifecycleFactoryBoundsInitialCapture(t *testing.T) {
	l := ownedLifecycleFixture(t)
	for _, timeout := range []time.Duration{0, -time.Second} {
		if _, _, err := l.DialOwned(t.Context(), timeout); !IsFailure(err, FailureInvalidConfiguration) {
			t.Fatal("invalid timeout accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := l.DialOwned(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// No caller deadline: the factory's own timeout must cover initial hashing,
	// before a retained lifetime or transport exists. No RPC needs to be started.
	if _, _, err := l.DialOwned(context.Background(), time.Nanosecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("initial capture escaped factory timeout", err)
	}
	if l.retained.Load() != nil {
		t.Fatal("expired acquisition published a lifetime")
	}
}

func ownedPidfds(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		target, _ := os.Readlink("/proc/self/fd/" + entry.Name())
		if strings.Contains(target, "pidfd") {
			n++
		}
	}
	return n
}

func TestOwnedLifecycleFailedSignalReleasesObservation(t *testing.T) {
	for _, unclean := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "unclean"}[unclean], func(t *testing.T) {
			l := ownedLifecycleFixture(t)
			before := ownedPidfds(t)
			_, closer, err := l.DialOwned(t.Context(), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if after := ownedPidfds(t); after != before+1 {
				t.Fatalf("factory pidfds %d -> %d", before, after)
			}
			l.processes.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error { return errors.New("forced signal failure") }
			l.exit = make(chan error, 1)
			for i := 0; i < 2; i++ {
				if unclean {
					err = l.stopOwnedUnclean(t.Context())
				} else {
					err = l.Stop(t.Context())
				}
				if err == nil {
					t.Fatal("forced failure absent")
				}
				_ = closer.Close()
				if after := ownedPidfds(t); after != before {
					t.Fatalf("terminal observation FD leaked: %d -> %d", before, after)
				}
				if l.pidfd < 0 {
					t.Fatal("supervisor descriptor released on failure")
				}
				if _, _, err := l.DialOwned(t.Context(), time.Second); !IsFailure(err, FailureIdentityUnprovable) {
					t.Fatal("old factory revived", err)
				}
			}
		})
	}
}

func TestOwnedLifecycleFactoryRefusesFallbackAndAlternateRouting(t *testing.T) {
	l := ownedLifecycleFixture(t)
	fd := l.pidfd
	l.pidfd = -1
	if _, _, err := l.DialOwned(t.Context(), time.Second); !IsFailure(err, FailureIdentityUnprovable) {
		t.Fatal("pidfd fallback admitted", err)
	}
	l.pidfd = fd
	l.provenance.ConnectedSocket = l.ownedEndpoint + "-proxy"
	if _, _, err := l.DialOwned(t.Context(), time.Second); !IsFailure(err, FailureIdentityUnprovable) {
		t.Fatal("proxy routed factory admitted", err)
	}
}
