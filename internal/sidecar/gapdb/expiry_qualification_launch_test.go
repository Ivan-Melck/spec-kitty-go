//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

// These are provider-free package-local controls. Self pidfds and simulated
// H/R/A do NOT establish a genuine Host, fixed-image execution or qualification.
func expiryLaunchUnitGate(t *testing.T) (*ExpiryQualification, *net.UnixConn, *Lifecycle) {
	t.Helper()
	root, err := os.MkdirTemp("", "expiry-launch-unit-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gapdb.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	image, digest, err := processExecutable(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	ticks, err := processStartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	socket, err := socketPathIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Lstat(root)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := identityFromInfo(dirInfo)
	if err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	p := ProcessIdentity{PID: os.Getpid(), StartTicks: ticks, Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
	s := gapdbapi.StatusResult{Lifecycle: gapdbapi.LifecycleReady, DatabaseID: "expiry-unit-db", OwnerPID: p.PID, OwnerStartedAt: "2026-01-01T00:00:00Z"}
	identity := ExpiryTechnicalIdentity{InputRecordSHA256: "unit-not-input-authority", ExecutableSHA256: digest, ExecutableBytes: image.size}
	v := &VerifiedExecutable{expiry: verifiedExpiryState{identity: identity, report: &expiryCleanupReport{}}}
	if err := v.makeExpiryPair(context.Background()); err != nil {
		t.Fatal(err)
	}
	conn, err := net.FileConn(v.expiry.child)
	if err != nil {
		t.Fatal(err)
	}
	peer := conn.(*net.UnixConn)
	v.expiryCommandStarted() // product closes original child, fixture owns duplicate
	l := &Lifecycle{pidfd: pidfd, process: p, processImage: image, processes: systemProcessAPI, runtimeID: directory, socketID: socket,
		command: &exec.Cmd{Process: &os.Process{Pid: p.PID}}, ownedEndpoint: path, ownedBootstrap: s,
		provenance: SidecarProvenance{Process: p, DatabaseID: s.DatabaseID, RuntimeDirectory: root, ConfiguredSocket: path, ConnectedSocket: path,
			Executable:    ExecutableProvenance{ContractRevision: expiryQualificationContract},
			ExecutedImage: ExecutedImageProvenance{SHA256: digest, Size: image.size}, Observation: CompositeObservation{Status: s}, Readiness: ReadinessPredicates{Admitted: true}}}
	l.initExpiry(v)
	l.expiry.report.supervisorOpened() // fixture's successful local pidfd above
	g := v.expiry.gate
	g.phase = expiryStarted
	life, err := retainedkernel.New(t.Context(), retainedkernel.Anchor{PID: p.PID, StartTicks: p.StartTicks, ImageDevice: p.Device, ImageInode: p.Inode, ImageSize: p.Size, ImageSHA256: p.SHA256,
		SocketPath: path, SocketDevice: socket.device, SocketInode: socket.inode, DirectoryPath: root, DirectoryDevice: directory.device, DirectoryInode: directory.inode})
	if err != nil {
		t.Fatal(err)
	}
	l.registerExpiryLifetime(life)
	l.retained.Store(life)
	q := &ExpiryQualification{state: g}
	t.Cleanup(func() {
		_ = q.Close()
		_ = peer.Close()
		l.closeOwned()
		l.mu.Lock()
		l.closePidfd()
		l.mu.Unlock()
		_ = listener.Close()
		_ = os.RemoveAll(root)
	})
	return q, peer, l
}

func expiryLaunchUnitSend(t *testing.T, peer *net.UnixConn, bytes ...byte) {
	t.Helper()
	if err := peer.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := peer.Write(bytes); err != nil || n != len(bytes) {
		t.Fatalf("fixture write: %d %v", n, err)
	}
}

func TestExpiryLaunchUnitGateHRAAndIndependentReleaseContext(t *testing.T) {
	q, peer, _ := expiryLaunchUnitGate(t)
	notification := q.Done()
	select {
	case <-notification:
		t.Fatal("new gate notification already canceled")
	default:
	}
	expiryLaunchUnitSend(t, peer, 'H')
	if err := q.WaitHeld(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckHeld(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := q.CheckHeld(t.Context()); err != nil {
		t.Fatal("non-reentrant local closure must be repeatable while held", err)
	}
	done := make(chan error, 1)
	go func() {
		var b [1]byte
		_, err := io.ReadFull(peer, b[:])
		if err == nil && b[0] != 'R' {
			err = errors.New("fixture did not receive exact R")
		}
		if err == nil {
			_, err = peer.Write([]byte{'A'})
		}
		done <- err
	}()
	if err := q.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if q.state.ctx.Err() != nil || q.state.invalid.Load() || q.state.phase != expiryAcknowledged {
		t.Fatal("intentional A release canceled independent context or lacked acknowledgment")
	}
	if q.Done() != notification {
		t.Fatal("notification identity changed at release")
	}
	select {
	case <-notification:
		t.Fatal("intentional R/A canceled notification")
	default:
	}
	if err := q.CleanupError(); err == nil {
		t.Fatal("live cancellation callback falsely counted complete")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal("repeat close", err)
	}
	if err := q.state.report.gateTeardownError(); err != nil {
		t.Fatal(err)
	}
	if q.CleanupError() == nil {
		t.Fatal("gate teardown certified still-owned lifecycle resources")
	}
	select {
	case <-notification:
	default:
		t.Fatal("final Close did not cancel original notification")
	}
	if err := q.CheckHeld(t.Context()); err == nil {
		t.Fatal("closed gate revived")
	}
}

func TestExpiryLaunchUnitGateMalformedHAndPhases(t *testing.T) {
	for _, tc := range []struct {
		name  string
		bytes []byte
	}{
		{"wrongH", []byte{'X'}}, {"earlyA", []byte{'A'}}, {"repeatedH", []byte{'H', 'H'}}, {"HThenEarlyA", []byte{'H', 'A'}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, peer, _ := expiryLaunchUnitGate(t)
			expiryLaunchUnitSend(t, peer, tc.bytes...)
			if err := q.WaitHeld(t.Context()); err == nil {
				t.Fatal("malformed sequence accepted")
			}
			if !q.state.invalid.Load() || q.state.phase != expiryFailed {
				t.Fatal("malformed sequence was not terminal")
			}
			select {
			case <-q.Done():
			default:
				t.Fatal("protocol failure did not cancel notification")
			}
			if err := q.Release(t.Context()); err == nil {
				t.Fatal("failure revived as release")
			}
		})
	}
	for _, operation := range []string{"releaseBeforeH", "checkBeforeH", "repeatWait"} {
		t.Run(operation, func(t *testing.T) {
			q, peer, _ := expiryLaunchUnitGate(t)
			var err error
			switch operation {
			case "releaseBeforeH":
				err = q.Release(t.Context())
			case "checkBeforeH":
				err = q.CheckHeld(t.Context())
			case "repeatWait":
				expiryLaunchUnitSend(t, peer, 'H')
				if err := q.WaitHeld(t.Context()); err != nil {
					t.Fatal(err)
				}
				err = q.WaitHeld(t.Context())
			}
			if err == nil || !q.state.invalid.Load() {
				t.Fatal("out-of-order/replay accepted", err)
			}
		})
	}
}

func TestExpiryLaunchUnitGateEOFCancelTimeoutAndBadA(t *testing.T) {
	for _, stage := range []string{"EOFBeforeH", "timeoutH", "cancelH", "wrongA", "EOFBeforeA", "timeoutA", "local12s"} {
		t.Run(stage, func(t *testing.T) {
			q, peer, _ := expiryLaunchUnitGate(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if stage == "EOFBeforeH" {
				_ = peer.Close()
			}
			if stage == "timeoutH" {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
				defer cancel()
			}
			if stage == "cancelH" {
				cancel()
			}
			if stage == "EOFBeforeH" || stage == "timeoutH" || stage == "cancelH" {
				if err := q.WaitHeld(ctx); err == nil {
					t.Fatal("missing H accepted")
				}
			} else {
				expiryLaunchUnitSend(t, peer, 'H')
				if err := q.WaitHeld(ctx); err != nil {
					t.Fatal(err)
				}
				if stage == "local12s" {
					q.state.heldAt = time.Now().Add(-expiryHeldBudget)
					if err := q.CheckHeld(ctx); err == nil {
						t.Fatal("expired held budget accepted")
					}
				} else {
					done := make(chan struct{})
					go func() {
						defer close(done)
						var b [1]byte
						_, _ = io.ReadFull(peer, b[:])
						switch stage {
						case "wrongA":
							_, _ = peer.Write([]byte{'X'})
						case "EOFBeforeA":
							_ = peer.Close()
						}
					}()
					if stage == "timeoutA" {
						cancel()
						ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
						defer cancel()
					}
					if err := q.Release(ctx); err == nil {
						t.Fatal("missing/wrong A accepted")
					}
					<-done
				}
			}
			if !q.state.invalid.Load() || q.state.phase == expiryAcknowledged {
				t.Fatal("failed exchange earned acknowledgment")
			}
		})
	}
}

func TestExpiryLaunchUnitOverlapAndRevocationBeforeLocks(t *testing.T) {
	q, peer, l := expiryLaunchUnitGate(t)
	q.state.operationMu.Lock()
	if err := q.CheckHeld(t.Context()); err == nil {
		t.Fatal("overlap accepted")
	}
	q.state.operationMu.Unlock()
	if !q.state.invalid.Load() {
		t.Fatal("overlap did not revoke")
	}
	var b [1]byte
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(b[:]); !errors.Is(err, io.EOF) {
		t.Fatal("revocation did not release writer", err)
	}

	q, _, l = expiryLaunchUnitGate(t)
	l.mu.Lock()
	done := make(chan struct{})
	go func() { l.revokeOwned(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		l.mu.Unlock()
		t.Fatal("revoke waited for lifecycle lock")
	}
	if !q.state.invalid.Load() || !l.ownedRevoked.Load() {
		l.mu.Unlock()
		t.Fatal("shutdown entry did not revoke")
	}
	l.mu.Unlock()
}

func TestExpiryLaunchUnitCancellationDuringHUnblocksAndQuiesces(t *testing.T) {
	q, _, _ := expiryLaunchUnitGate(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.WaitHeld(ctx) }()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for q.state.report.counts[expiryCallback].required.Load() < 2 {
		select {
		case <-deadline.C:
			t.Fatal("fixed I/O cancellation watch not registered")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled H operation succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not unblock fixed I/O")
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if err := q.state.report.gateTeardownError(); err != nil {
		t.Fatal("gate callback completion not quiescent", err)
	}
}

func TestExpiryLaunchUnitEntryRefusalsPrecedeNormalizationAndProjection(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	l.mu.Lock() // both refusals must precede ordinary lock/config/projection work
	_, restartErr := l.RestartOwned(nil, RestartMode("bad"), "")
	_, evidenceErr := l.Evidence()
	l.mu.Unlock()
	if !IsFailure(restartErr, FailureAdmissionMismatch) || !IsFailure(evidenceErr, FailureAdmissionMismatch) {
		t.Fatal(restartErr, evidenceErr)
	}
	_, attachErr := Attach(nil, LifecycleConfig{}, SidecarProvenance{Executable: ExecutableProvenance{ContractRevision: expiryQualificationContract}})
	if !IsFailure(attachErr, FailureAdmissionMismatch) {
		t.Fatal("entry refusal occurred after invalid configuration", attachErr)
	}
	if q.state.invalid.Load() {
		t.Fatal("unsupported entry calls mutated ownership")
	}
}

func TestExpiryLaunchUnitCrashUsesOriginalControlAfterGateRevocation(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	q.state.revoke()
	forced := errors.New("existing process seam refuses actual signaling")
	var calls int
	l.processes.pidfdSignal = func(fd int, signal unix.Signal, _ *unix.Siginfo, _ int) error {
		calls++
		if fd != l.pidfd || signal != unix.SIGKILL || !l.ownedRevoked.Load() || !q.state.invalid.Load() {
			t.Error("not original pidfd/control or revoke order")
		}
		return forced
	}
	if err := l.CrashExpiryQualification(t.Context()); !errors.Is(err, forced) {
		t.Fatal("crash improperly required already revoked gate liveness", err)
	}
	if calls != 1 {
		t.Fatal("existing stopOwnedUnclean signal path not used once", calls)
	}
	if err := (*Lifecycle)(nil).CrashExpiryQualification(t.Context()); err == nil {
		t.Fatal("nil marked control accepted")
	}
}

func TestExpiryLaunchUnitLocalMetadataAndLifetimeGuard(t *testing.T) {
	for _, change := range []string{"runtimeMode", "socketReplacement", "revokedLifetime", "stopped", "bootstrapDB", "ownerStarted", "process", "identity", "callerCancel"} {
		t.Run(change, func(t *testing.T) {
			q, peer, l := expiryLaunchUnitGate(t)
			expiryLaunchUnitSend(t, peer, 'H')
			if err := q.WaitHeld(t.Context()); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch change {
			case "runtimeMode":
				if err := os.Chmod(l.provenance.RuntimeDirectory, 0750); err != nil {
					t.Fatal(err)
				}
			case "socketReplacement":
				if err := os.Remove(l.ownedEndpoint); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(l.ownedEndpoint, []byte("not socket"), 0600); err != nil {
					t.Fatal(err)
				}
			case "revokedLifetime":
				l.retained.Load().Revoke()
			case "stopped":
				l.stopped = true
			case "bootstrapDB":
				l.ownedBootstrap.DatabaseID = "other"
			case "ownerStarted":
				l.ownedBootstrap.OwnerStartedAt = "other"
			case "process":
				l.provenance.Process.StartTicks++
			case "identity":
				l.provenance.ExecutedImage.Size++
			case "callerCancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			// These existing seams prove CheckHeld does not perform a proc image
			// full hash or process callback. The retained guard is poll-only.
			l.processes.executable = func(int) (fileIdentity, string, error) {
				t.Error("renewed image hash")
				return fileIdentity{}, "", errors.New("forbidden")
			}
			if err := q.CheckHeld(ctx); err == nil {
				t.Fatal("local substitution/revocation accepted")
			}
			if !q.state.invalid.Load() {
				t.Fatal("refusal was not terminal")
			}
		})
	}
}

func expiryLaunchUnitImage(t *testing.T, report *expiryCleanupReport) *VerifiedExecutable {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image")
	bytes := []byte("unit-only-not-an-executed-provider")
	if err := os.WriteFile(path, bytes, 0500); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	v, err := verifyExecutableImage(path, hex.EncodeToString(digest[:]), int64(len(bytes)), ExecutableProvenance{ContractRevision: contractRevision}, report)
	if err != nil {
		t.Fatal(err)
	}
	v.expiry.report = report
	t.Cleanup(func() { _ = v.Close() })
	return v
}

func TestExpiryLaunchUnitCommandMappingsAndCommonRecheck(t *testing.T) {
	ordinary := expiryLaunchUnitImage(t, nil)
	environment := []string{"LANG=C", "TZ=UTC"}
	cmd, err := ordinary.command([]string{"-unit"}, environment)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != "/proc/self/fd/3" || !reflect.DeepEqual(cmd.ExtraFiles, []*os.File{ordinary.execution}) || !reflect.DeepEqual(cmd.Env, environment) {
		t.Fatal("ordinary descriptor/environment changed")
	}
	environment[0] = "mutated"
	if cmd.Env[0] != "LANG=C" {
		t.Fatal("ordinary environment not cloned")
	}

	r := &expiryCleanupReport{}
	marked := expiryLaunchUnitImage(t, r)
	marked.expiry.identity = ExpiryTechnicalIdentity{InputRecordSHA256: "unit-only", ExecutableSHA256: marked.executed.SHA256, ExecutableBytes: marked.executed.Size}
	if err := marked.makeExpiryPair(t.Context()); err != nil {
		t.Fatal(err)
	}
	cmd, err = marked.command([]string{"-unit"}, []string{"arbitrary=ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != "/proc/self/fd/4" || !reflect.DeepEqual(cmd.ExtraFiles, []*os.File{marked.expiry.child, marked.execution}) ||
		!reflect.DeepEqual(cmd.Env, []string{"LANG=C", "TZ=UTC", "SPEC_KITTY_EXPIRY_GATE_FD=3"}) {
		t.Fatal("fixed fd3/fd4/env mapping differs")
	}
	marked.executed.SHA256 = hex.EncodeToString(make([]byte, sha256.Size))
	if _, err := marked.command(nil, nil); !IsFailure(err, FailureIdentityUnprovable) {
		t.Fatal("common full image recheck bypassed", err)
	}
}

func TestExpiryLaunchUnitAdditionalStartupChecksOwnEveryProcFile(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	before := q.state.report.counts[expiryMetadata].required.Load()
	expected := ExecutedImageProvenance{Device: l.process.Device, Inode: l.process.Inode, Size: l.process.Size, SHA256: l.process.SHA256, Mode: l.processImage.mode}
	// The self Go test executable is deliberately not a sealed qualification
	// image. This is only descriptor-closure/refusal control, never child exec.
	if err := l.checkExpiryStartedProcess(expected); err == nil {
		t.Fatal("unsealed self image accepted as fixed child")
	}
	counts := &q.state.report.counts[expiryMetadata]
	if counts.required.Load() != before+2 || counts.required.Load() != counts.recorded.Load() || counts.recorded.Load() != counts.completed.Load() {
		t.Fatal("additional proc stat/exe descriptors lacked actual first close results")
	}
	if err := q.state.report.firstError(); err != nil {
		t.Fatal("normal proc descriptor closes failed", err)
	}
}

func TestExpiryLaunchUnitZeroHandleAndWrongChildDescriptor(t *testing.T) {
	for _, q := range []*ExpiryQualification{nil, {}} {
		if q.Identity() != (ExpiryTechnicalIdentity{}) || q.WaitHeld(t.Context()) == nil || q.CheckHeld(t.Context()) == nil || q.Release(t.Context()) == nil || q.CleanupError() == nil {
			t.Fatal("zero handle granted live state or cleanup certificate")
		}
		if err := q.Close(); err != nil {
			t.Fatal("nil teardown changed", err)
		}
	}
	r := &expiryCleanupReport{}
	v := expiryLaunchUnitImage(t, r)
	v.expiry.identity = ExpiryTechnicalIdentity{InputRecordSHA256: "unit-only"}
	if err := v.makeExpiryPair(t.Context()); err != nil {
		t.Fatal(err)
	}
	original := v.expiry.child
	decoy, err := os.CreateTemp(t.TempDir(), "not-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer decoy.Close()
	v.expiry.child = decoy
	_, commandErr := v.command(nil, nil)
	v.expiry.child = original // restore the actual private owner for cleanup
	if !IsFailure(commandErr, FailureIdentityUnprovable) || !v.expiry.gate.invalid.Load() {
		t.Fatal("regular-file child descriptor accepted", commandErr)
	}
}

func TestExpiryLaunchUnitUnconsumedAndTransferredCloseCustody(t *testing.T) {
	r := &expiryCleanupReport{}
	v := expiryLaunchUnitImage(t, r)
	v.expiry.identity = ExpiryTechnicalIdentity{InputRecordSHA256: "unit-only"}
	if err := v.makeExpiryPair(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal("clean unconsumed close prematurely demanded source/image completion", err)
	}
	if err := r.cleanupError(); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	for _, category := range []int{expirySource, expiryImage, expiryChild, expiryParent, expiryTemporary} {
		if r.counts[category].recorded.Load() != 1 {
			t.Fatal("owned resource closed more than once", category)
		}
	}

	r = &expiryCleanupReport{}
	v = expiryLaunchUnitImage(t, r)
	v.expiry.identity = ExpiryTechnicalIdentity{InputRecordSHA256: "unit-only"}
	if err := v.makeExpiryPair(t.Context()); err != nil {
		t.Fatal(err)
	}
	l := &Lifecycle{}
	l.initExpiry(v)
	v.expiryCommandStarted()
	_ = v.Close() // same discarded immediate post-exec close site, no process exec
	if v.expiry.report != r || l.expiry.report != r || l.expiry.gate.report != r {
		t.Fatal("report pointer was copied/lost during early close/transfer")
	}
	if l.expiry.gate.invalid.Load() || r.counts[expiryParent].recorded.Load() != 0 {
		t.Fatal("verified.Close closed transferred parent")
	}
	if err := (&ExpiryQualification{state: l.expiry.gate}).Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExpiryLaunchUnitFirstCloseReportsAndNoDoubleClose(t *testing.T) {
	for _, category := range []string{"source", "image", "child", "parent", "metadata", "temporary"} {
		t.Run(category, func(t *testing.T) {
			r := &expiryCleanupReport{}
			var first error
			if category == "source" || category == "image" {
				v := expiryLaunchUnitImage(t, r)
				index, alreadyClosed := expirySource, v.source
				if category == "image" {
					index, alreadyClosed = expiryImage, v.execution
				}
				if err := alreadyClosed.Close(); err != nil {
					t.Fatal("fixture's deliberate owned File.Close failed", err)
				}
				// Closed-object diagnostic control, NOT a failed physical FIRST
				// syscall witness. Product still owns exactly one attempt per File.
				first = v.Close()
				sticky := r.first.Load()
				if !errors.Is(first, os.ErrClosed) || sticky == nil || sticky.category != index || sticky.err != first {
					t.Fatal("actual first owner closed-object cause/report missing", first, sticky)
				}
				if again := v.Close(); again != first {
					t.Fatal("repeat Close replaced cached FIRST", again)
				}
				if v.expiry.report != r || r.first.Load() != sticky || sticky.err != first || v.source != nil || v.execution != nil {
					t.Fatal("repeat Close changed report/FIRST or retained closed File")
				}
				for _, acquired := range []int{expirySource, expiryImage} {
					counts := &r.counts[acquired]
					if counts.required.Load() != 1 || counts.recorded.Load() != 1 || counts.completed.Load() != 1 {
						t.Fatal("source/image product attempt/completion not exactly once", acquired)
					}
				}
				if err := r.cleanupError(); !errors.Is(err, first) || !errors.Is(err, os.ErrClosed) {
					t.Fatal("report lost immutable FIRST closed-object ancestry", err)
				}
			} else if category == "parent" {
				q, _, _ := expiryLaunchUnitGate(t)
				r = q.state.report
				_ = q.state.parent.Close()
				first = q.Close()
				_ = q.Close()
			} else if category == "child" {
				v := &VerifiedExecutable{expiry: verifiedExpiryState{report: r, identity: ExpiryTechnicalIdentity{InputRecordSHA256: "unit"}}}
				if err := v.makeExpiryPair(t.Context()); err != nil {
					t.Fatal(err)
				}
				_ = v.expiry.child.Close()
				v.expiryCommandStarted()
				v.expiryCommandStarted()
				first = (&ExpiryQualification{state: v.expiry.gate}).Close()
				if r.counts[expiryChild].recorded.Load() != 1 {
					t.Fatal("child actually closed twice")
				}
			} else {
				index := expiryMetadata
				if category == "temporary" {
					index = expiryTemporary
				}
				f, err := os.CreateTemp(t.TempDir(), "close-")
				if err != nil {
					t.Fatal(err)
				}
				r.acquired(index)
				_ = f.Close()
				r.closing(index)
				first = f.Close()
				r.closed(index, first)
			}
			if first == nil || r.cleanupError() == nil {
				t.Fatal("repeat nil repaired first error", first)
			}
			if !IsFailure(r.cleanupError(), FailureCleanup) {
				t.Fatal(r.cleanupError())
			}
		})
	}
	r := &expiryCleanupReport{}
	v := expiryLaunchUnitImage(t, r)
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if r.cleanupError() != nil || r.counts[expirySource].completed.Load() != 1 || r.counts[expiryImage].completed.Load() != 1 {
		t.Fatal("nil first close not explicit or repeated close recorded", r.cleanupError())
	}
}

func TestExpiryLaunchUnitCleanupAtomicOnlyStickyCompleteness(t *testing.T) {
	var nilReport *expiryCleanupReport
	nilReport.sourceClosed(nil)
	nilReport.imageClosed(errors.New("ignored ordinary nil report"))
	if nilReport.cleanupError() != nil {
		t.Fatal("nil ordinary hook changed")
	}
	for category := 0; category < expiryCleanupCategories; category++ {
		r := &expiryCleanupReport{}
		r.acquired(category)
		if r.cleanupError() == nil {
			t.Fatal("unattempted close counted success", category)
		}
		r.closing(category)
		if r.cleanupError() == nil {
			t.Fatal("in-flight close counted success", category)
		}
		r.closed(category, nil)
		if r.cleanupError() != nil {
			t.Fatal("explicit nil outcome missing", r.cleanupError())
		}
		r.acquired(category)
		r.closing(category)
		failure := errors.New("later owned resource failed")
		r.closed(category, failure)
		if !errors.Is(r.cleanupError(), failure) {
			t.Fatal("prior category success hid later error", category)
		}
		r.acquired(category)
		r.closing(category)
		r.closed(category, nil)
		if !errors.Is(r.cleanupError(), failure) {
			t.Fatal("later nil repaired first error", category)
		}
	}
	r := &expiryCleanupReport{}
	failure := errors.New("concurrent first error")
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.acquired(expiryMetadata)
			r.closing(expiryMetadata)
			if i == 7 {
				r.closed(expiryMetadata, failure)
			} else {
				r.closed(expiryMetadata, nil)
			}
		}(i)
	}
	wg.Wait()
	if !errors.Is(r.cleanupError(), failure) {
		t.Fatal("concurrent nil hid first nonnil", r.cleanupError())
	}
	r = &expiryCleanupReport{}
	r.counts[expiryMetadata].required.Store(^uint64(0))
	r.acquired(expiryMetadata)
	if r.cleanupError() == nil || r.counts[expiryMetadata].required.Load() != ^uint64(0) {
		t.Fatal("overflow was not terminal")
	}
}

func TestExpiryLaunchUnitDoneClosedFallbackAndReadOnlyAliases(t *testing.T) {
	for _, q := range []*ExpiryQualification{nil, {}, {state: &expiryGateState{}}, {state: &expiryGateState{ctx: context.Background()}}} {
		if q.Done() == nil || q.Done() != expiryCanceledNotification {
			t.Fatal("uninitialized gate did not return closed singleton")
		}
		select {
		case <-q.Done():
		default:
			t.Fatal("uninitialized handle suggested liveness")
		}
	}
	if reflect.TypeOf((*ExpiryQualification).Done).Out(0).ChanDir() != reflect.RecvDir {
		t.Fatal("Done grants a writable channel")
	}
	q, _, l := expiryLaunchUnitGate(t)
	alias := *q
	notification := q.Done()
	if notification != q.state.ctx.Done() || alias.Done() != notification {
		t.Fatal("notification is not existing shared gate context")
	}
	q.state.operationMu.Lock()
	l.mu.Lock()
	if q.Done() != notification {
		t.Error("accessor changed existing notification")
	}
	l.mu.Unlock()
	q.state.operationMu.Unlock()
	// Simulates only the required context direction, not actual Consume/Host
	// classification (those controls belong to the runtime author).
	outer, cancelOuter := context.WithCancel(t.Context())
	defer cancelOuter()
	inner, cancelInner := context.WithCancel(outer)
	cancelInner()
	if inner.Err() == nil || outer.Err() != nil {
		t.Fatal("inner cancellation propagated upward")
	}
	select {
	case <-notification:
		t.Fatal("unrelated inner cancellation canceled gate")
	default:
	}
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if alias.Done() != notification {
				t.Error("alias channel changed")
			}
			<-alias.Done()
		}()
	}
	l.revokeOwned()
	workers.Wait()
	select {
	case <-notification:
	default:
		t.Fatal("revoke did not cancel shared notification")
	}
}

func TestExpiryLaunchUnitDoneStopAndAbortPrecedeLifecycleLock(t *testing.T) {
	for _, operation := range []string{"stop", "abort"} {
		t.Run(operation, func(t *testing.T) {
			q, _, l := expiryLaunchUnitGate(t)
			r := q.state.report
			if l.childWait != nil || l.startupPublished.Load() != nil || l.exit != nil || r.first.Load() != nil {
				t.Fatal("synthetic fixture unexpectedly owns actual Wait/publication or prior failure")
			}
			var signals int
			l.processes.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error {
				signals++
				return errors.New("self-pidfd fixture must never signal")
			}
			l.mu.Lock()
			// Keep the original notification-before-held-mutex schedule. Stop's
			// stopped guard cannot turn this synthetic command into Wait custody.
			// Abort's command-less nonzero identity independently refuses reap.
			l.stopped = true
			if operation == "abort" {
				l.command = nil
			}
			done := make(chan error, 1)
			go func() {
				if operation == "stop" {
					done <- l.Stop(t.Context())
				} else {
					done <- l.abortStart()
				}
			}()
			select {
			case <-q.Done():
			case <-time.After(time.Second):
				l.mu.Unlock()
				t.Fatal("shutdown did not cancel notification before lifecycle lock")
			}
			l.mu.Unlock()
			var first error
			select {
			case first = <-done:
			case <-time.After(time.Second):
				t.Fatal("fixture teardown blocked")
			}
			if !IsFailure(first, FailureCleanup) || !l.ownedRevoked.Load() || !q.state.invalid.Load() {
				t.Fatal("missing-Wait fixture lacked cutoff/refusal", first)
			}
			claim := l.teardownCustody.Load()
			if claim == nil || claim.err != first || l.teardownCustodyErr != first {
				t.Fatal("whole FIRST custody identity not recorded", first)
			}
			select {
			case <-claim.done:
			default:
				t.Fatal("FIRST result lacks completion publication")
			}
			sticky := r.first.Load()
			var counts [expiryCleanupCategories][3]uint64
			for category := range r.counts {
				c := &r.counts[category]
				counts[category] = [3]uint64{c.required.Load(), c.recorded.Load(), c.completed.Load()}
			}
			if operation == "stop" {
				var refusal *Error
				if !errors.As(first, &refusal) || refusal.Kind != FailureCleanup || refusal.Operation != "child_wait" || refusal.Reason != "actual child wait owner is absent; runtime preserved" {
					t.Fatal("Stop failed without explicit missing actual-Wait custody refusal", first)
				}
				if !l.shutdownWaitChecked || l.shutdownWait != (actualWaitObservation{}) || sticky == nil || sticky.category != expiryLifecycleMetadata || sticky.err != first {
					t.Fatal("missing Wait manufactured observation/reap or lost report FIRST", first)
				}
				if again := l.Stop(t.Context()); again != first {
					t.Fatal("repeat Stop replaced immutable FIRST", again)
				}
				// drain's work/admission retirement flags are NOT actual reap or
				// successful cleanup/retirement credit: full report must still fail.
				if err := q.Close(); !errors.Is(err, first) { t.Fatal("gate teardown lost Stop FIRST", err) }
				if err := q.CleanupError(); !errors.Is(err, first) { t.Fatal("full report lost Stop FIRST", err) }
			} else {
				var custody *startupAbortError
				if !errors.As(first, &custody) || custody.facts.signalAttempted || custody.facts.waitAttempted || custody.facts.waitObserved || custody.facts.waitTimedOut || custody.facts.reapProven || custody.facts.waitState != nil || custody.facts.waitErr != nil || custody.facts.runtimeCleanupAttempted || !custody.facts.runtimeDeletionWithheld || !custody.facts.residualPossible {
					t.Fatal("command-less fixture manufactured Wait/reap/cleanup success", first)
				}
				if again := l.abortStart(); again != first { t.Fatal("FIRST refusal replaced", again) }
				// Failed-start custody uses retireExpiry=false: it does NOT publish
				// its refusal through finishExpiryTeardown/report.fail like Stop.
				if l.startupAbort.result != first || sticky != nil || l.expiry.work.owner.Load() || l.expiry.work.retired || r.lifecycleRetired.Load() || l.shutdownWaitChecked {
					t.Fatal("abort acquired Stop's report path or synthetic retirement")
				}
				if err := q.Close(); err != nil { t.Fatal("abort gate-only close unexpectedly failed", err) }
				if q.CleanupError() == nil { t.Fatal("unretired abort fixture certified full cleanup") }
			}
			if signals != 0 || l.childWait != nil || l.startupPublished.Load() != nil || l.exit != nil || l.shutdownWait != (actualWaitObservation{}) || (l.command != nil && l.command.ProcessState != nil) {
				t.Fatal("synthetic fixture acquired actual signal/Wait/reap credit")
			}
			if !l.pidfdCloseAttempted || l.pidfd != -1 || counts[expirySupervisor] != ([3]uint64{1, 1, 1}) || counts[expiryLifetime] != ([3]uint64{1, 1, 1}) {
				t.Fatal("safe owned descriptor release lacked one actual outcome")
			}
			if q.state.report != r || l.expiry.report != r || r.first.Load() != sticky || claim.err != first || l.teardownCustodyErr != first {
				t.Fatal("repeat/gate close changed report or FIRST identity")
			}
			for category := range r.counts {
				c := &r.counts[category]
				if category == expiryCallback {
					// Abort only revoked; q.Close legitimately joins that callback.
					if c.required.Load() != 1 || c.recorded.Load() != 1 || c.completed.Load() != 1 { t.Fatal("gate callback completion missing") }
					continue
				}
				if counts[category] != ([3]uint64{c.required.Load(), c.recorded.Load(), c.completed.Load()}) {
					t.Fatal("repeat/gate close repeated actual resource work", category)
				}
			}
			if _, err := os.Lstat(l.provenance.RuntimeDirectory); err != nil { t.Fatal("unproven reap lost runtime residual", err) }
			if _, err := os.Lstat(l.ownedEndpoint); err != nil { t.Fatal("unproven reap lost socket residual", err) }
		})
	}
}

func TestExpiryLaunchUnitDoneParentCancelAndStartFailure(t *testing.T) {
	for _, operation := range []string{"parentCancel", "startFailure", "finalClose"} {
		t.Run(operation, func(t *testing.T) {
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			v := &VerifiedExecutable{expiry: verifiedExpiryState{identity: ExpiryTechnicalIdentity{InputRecordSHA256: "unit-only"}, report: &expiryCleanupReport{}}}
			if err := v.makeExpiryPair(parent); err != nil {
				t.Fatal(err)
			}
			v.expiryCommandStarted()
			q := &ExpiryQualification{state: v.expiry.gate}
			t.Cleanup(func() { _ = q.Close() })
			switch operation {
			case "parentCancel":
				cancel()
			case "startFailure":
				v.expiryStartFailed()
			case "finalClose":
				if err := q.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-q.Done():
			case <-time.After(time.Second):
				t.Fatal("existing gate context not canceled", operation)
			}
			if err := q.Close(); err != nil {
				t.Fatal(err)
			}
			if err := q.CleanupError(); err != nil {
				t.Fatal("required callbacks/first closes incomplete", err)
			}
		})
	}
}

func TestExpiryLaunchUnitActualCancellationFirstCloseErrorConcurrentFinalCleanup(t *testing.T) {
	q, _, _ := expiryLaunchUnitGate(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	operationDone := make(chan error, 1)
	go func() { operationDone <- q.WaitHeld(ctx) }()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for q.state.report.counts[expiryCallback].required.Load() < 2 {
		select {
		case <-timer.C:
			t.Fatal("actual I/O callback not registered")
		case <-ticker.C:
		}
	}
	// Close the actual owned connection out-of-band as an error control. The
	// production closeOnce then makes its first owner attempt, records the real
	// nonnil result, and cannot repair it by a later nil repeat. No fault callback
	// or production seam is introduced.
	if err := q.state.parent.Close(); err != nil {
		t.Fatal(err)
	}
	cancel()
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- q.Close() }()
	select {
	case err := <-operationDone:
		if err == nil {
			t.Fatal("canceled exchange succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("actual callback did not release operation")
	}
	select {
	case err := <-cleanupDone:
		if !IsFailure(err, FailureCleanup) {
			t.Fatal("first actual close error absent", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent final cleanup did not quiesce")
	}
	if err := q.Close(); !IsFailure(err, FailureCleanup) {
		t.Fatal("repeat cleanup repaired actual first close failure", err)
	}
	if err := q.CleanupError(); !IsFailure(err, FailureCleanup) {
		t.Fatal(err)
	}
	callbacks := &q.state.report.counts[expiryCallback]
	if callbacks.required.Load() != 2 || callbacks.recorded.Load() != 2 || callbacks.completed.Load() != 2 {
		t.Fatal("actual callbacks not completed exactly once")
	}
	if q.state.report.counts[expiryParent].recorded.Load() != 1 || q.state.report.counts[expiryParent].completed.Load() != 1 {
		t.Fatal("parent actual owner close repeated or lost")
	}
}

func TestExpiryLaunchUnitFinalCleanupWaitsForActualLocalGuardWork(t *testing.T) {
	q, peer, l := expiryLaunchUnitGate(t)
	expiryLaunchUnitSend(t, peer, 'H')
	if err := q.WaitHeld(t.Context()); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	guardDone := make(chan error, 1)
	go func() { guardDone <- q.CheckHeld(t.Context()) }()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !q.state.report.operationPending.Load() {
		select {
		case <-timer.C:
			l.mu.Unlock()
			t.Fatal("actual local guard not registered")
		case <-ticker.C:
		}
	}
	cleanupDone := make(chan error, 1)
	go func() { cleanupDone <- q.Close() }()
	select {
	case <-q.Done():
	case <-time.After(time.Second):
		l.mu.Unlock()
		t.Fatal("final cleanup did not revoke before local guard lock")
	}
	if q.CleanupError() == nil {
		l.mu.Unlock()
		t.Fatal("pending local guard granted a cleanup certificate")
	}
	select {
	case err := <-cleanupDone:
		l.mu.Unlock()
		t.Fatal("cleanup returned before actual local guard quiesced", err)
	default:
	}
	l.mu.Unlock()
	select {
	case err := <-guardDone:
		if err == nil {
			t.Fatal("revoked local guard succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("local guard did not complete")
	}
	select {
	case err := <-cleanupDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("final cleanup did not join local guard work")
	}
	if q.state.report.operationPending.Load() || q.state.report.gateTeardownError() != nil {
		t.Fatal("completed guard remained unknown")
	}
}

func TestExpiryLaunchUnitCancellationCleanupQuiescentOutsideAllLocks(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	q.state.operationMu.Lock()
	l.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- q.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(time.Second):
		t.Error("callback teardown acquired gate/lifecycle lock")
	}
	l.mu.Unlock()
	q.state.operationMu.Unlock()
	if err := q.state.report.gateTeardownError(); err != nil {
		t.Fatal(err)
	}
	if q.state.report.counts[expiryParent].recorded.Load() != 1 {
		t.Fatal("parent connection closed more than once")
	}
}
