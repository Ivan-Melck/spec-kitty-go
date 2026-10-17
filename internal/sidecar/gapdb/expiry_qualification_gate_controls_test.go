//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

// Real self pidfds, real owned filesystem/socket metadata, simulated protocol.
// This is NOT authenticated TEST, a provider adapter, or a genuine Host root.
type expiryGateControlFixture struct {
	q *ExpiryQualification
	peer *net.UnixConn
	lifecycle *Lifecycle
	ledger *expiryDescriptorControlLedger
	closePeer func()
	closeLifetime func()
	closeGate func() error
	cancelRun context.CancelFunc
}
func expiryGateControlNew(t *testing.T) *expiryGateControlFixture {
	t.Helper()
	ledger := expiryDescriptorControlLedgerNew(t)
	root := expiryDescriptorControlRoot(t, ledger)
	path := filepath.Join(root, "gapdb.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil { t.Fatal(err) }
	listener.SetUnlinkOnClose(false) // preserve fresh fixture residual; no unknown cleanup
	t.Cleanup(func() { expiryDescriptorControlCleanupResult(t, ledger, "listener.close", listener.Close()) })
	image, digest := expiryDescriptorControlProcessImage(t, ledger, os.Getpid())
	ticks := expiryDescriptorControlStartTicks(t, ledger, "self", os.Getpid())
	socket, err := socketPathIdentity(path); if err != nil { t.Fatal(err) }
	dir, err := os.Open(root); if err != nil { t.Fatal(err) }
	closeDir := expiryDescriptorControlOwnFile(t, ledger, "fixture.directory", dir)
	directory, err := identityOf(dir); closeDir(); if err != nil { t.Fatal(err) }
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0); if err != nil { t.Fatal(err) }
	t.Cleanup(func() { expiryDescriptorControlCleanupResult(t, ledger, "supervisor.pidfd.close", unix.Close(pidfd)) })
	p := ProcessIdentity{PID: os.Getpid(), StartTicks: ticks, Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
	s := gapdbapi.StatusResult{Lifecycle: gapdbapi.LifecycleReady, DatabaseID: "component-selfpidfd-not-Host", OwnerPID: p.PID, OwnerStartedAt: "2026-01-01T00:00:00Z"}
	v := &VerifiedExecutable{expiry: verifiedExpiryState{identity: ExpiryTechnicalIdentity{InputRecordSHA256: "component-NOT-R", ExecutableSHA256: digest, ExecutableBytes: image.size}, report: new(expiryCleanupReport)}}
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(cancelRun)
	var gateOnce sync.Once
	var firstGateClose error
	closeGate := func() error { gateOnce.Do(func() { v.expiryCommandStarted(); firstGateClose = (&ExpiryQualification{state: v.expiry.gate}).Close(); expiryDescriptorControlCleanupResult(t, ledger, "gate.close", firstGateClose) }); return firstGateClose }
	t.Cleanup(func() { closeGate() })
	if err := v.makeExpiryPair(runCtx); err != nil { t.Fatal(err) }
	q := &ExpiryQualification{state: v.expiry.gate}
	connection, err := net.FileConn(v.expiry.child)
	expiryDescriptorControlRecord(t, ledger, "child.FileConn", err)
	if err != nil { t.Fatal(err) }
	peer, ok := connection.(*net.UnixConn)
	if !ok { expiryDescriptorControlCleanupResult(t, ledger, "unexpected.connection.close", connection.Close()); t.Fatal("not UnixConn") }
	var peerOnce sync.Once
	closePeer := func() { peerOnce.Do(func() { expiryDescriptorControlCleanupResult(t, ledger, "fixture.peer.close", peer.Close()) }) }
	t.Cleanup(closePeer)
	v.expiryCommandStarted() // actual FIRST close of parent's child-original
	l := &Lifecycle{pidfd: pidfd, process: p, processImage: image, processes: systemProcessAPI, runtimeID: directory, socketID: socket,
		command: &exec.Cmd{Process: &os.Process{Pid: p.PID}}, ownedEndpoint: path, ownedBootstrap: s,
		expiry: lifecycleExpiryState{gate: q.state, report: q.state.report},
		provenance: SidecarProvenance{Process: p, DatabaseID: s.DatabaseID, RuntimeDirectory: root, ConfiguredSocket: path, ConnectedSocket: path,
			Executable: ExecutableProvenance{ContractRevision: expiryQualificationContract}, ExecutedImage: ExecutedImageProvenance{SHA256: digest, Size: image.size},
			Observation: CompositeObservation{Status: s}, Readiness: ReadinessPredicates{Admitted: true}}}
	q.state.lifecycle.Store(l)
	q.state.phase = expiryStarted
	life, err := retainedkernel.New(t.Context(), retainedkernel.Anchor{PID: p.PID, StartTicks: p.StartTicks, ImageDevice: p.Device, ImageInode: p.Inode, ImageSize: p.Size, ImageSHA256: p.SHA256,
		SocketPath: path, SocketDevice: socket.device, SocketInode: socket.inode, DirectoryPath: root, DirectoryDevice: directory.device, DirectoryInode: directory.inode})
	expiryDescriptorControlRecord(t, ledger, "retained-lifetime.acquire", err)
	if err != nil { t.Fatal(err) }
	l.retained.Store(life)
	// Fixture owns this lifetime itself; no fabricated product claim/report close.
	var lifetimeOnce sync.Once
	closeLifetime := func() { lifetimeOnce.Do(func() { expiryDescriptorControlCleanupResult(t, ledger, "retained-lifetime.close", life.Close()) }) }
	t.Cleanup(closeLifetime)
	return &expiryGateControlFixture{q: q, peer: peer, lifecycle: l, ledger: ledger, closePeer: closePeer, closeLifetime: closeLifetime, closeGate: closeGate, cancelRun: cancelRun}
}
func expiryGateControlSend(t *testing.T, f *expiryGateControlFixture, b ...byte) {
	t.Helper()
	if err := f.peer.SetWriteDeadline(time.Now().Add(time.Second)); err != nil { t.Fatal(err) }
	n, err := f.peer.Write(b)
	if err != nil || n != len(b) { t.Fatalf("fixture write %d/%d: %v", n, len(b), err) }
}
func expiryGateControlHold(t *testing.T, f *expiryGateControlFixture) {
	t.Helper()
	expiryGateControlSend(t, f, 'H')
	if err := f.q.WaitHeld(t.Context()); err != nil { t.Fatal(err) }
}
func expiryGateControlTerminal(t *testing.T, f *expiryGateControlFixture) {
	t.Helper()
	if !f.q.state.invalid.Load() || f.q.state.ctx.Err() == nil { t.Error("refusal did not irreversibly cancel generation") }
	before := f.q.state.report.counts[expiryMetadata].required.Load()
	callbacks := f.q.state.report.counts[expiryCallback].required.Load()
	for _, op := range []func(context.Context) error{f.q.WaitHeld, f.q.Release, f.q.CheckHeld} {
		if err := op(t.Context()); err == nil { t.Error("terminal gate renewed work") }
	}
	if f.q.state.report.counts[expiryMetadata].required.Load() != before || f.q.state.report.counts[expiryCallback].required.Load() != callbacks { t.Error("terminal methods acquired new metadata/callback work") }
	// Deadline is only a failure bound, never EOF/liveness authority.
	if err := f.peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		if !errors.Is(err, net.ErrClosed) { t.Error(err) }
	} else {
		var b [1]byte
		_, err := f.peer.Read(b[:])
		if !errors.Is(err, io.EOF) && !errors.Is(err, unix.ECONNRESET) && !errors.Is(err, net.ErrClosed) { t.Error("writer not released by actual terminal teardown", err) }
	}
}

func TestExpiryGateControlPositiveHRAAndRepeatedCheck(t *testing.T) {
	f := expiryGateControlNew(t)
	expiryGateControlHold(t, f)
	for i := 0; i < 2; i++ { if err := f.q.CheckHeld(t.Context()); err != nil { t.Fatal(err) } }
	done := make(chan error, 1)
	go func() {
		var b [1]byte
		if err := f.peer.SetDeadline(time.Now().Add(time.Second)); err != nil { done <- err; return }
		_, err := io.ReadFull(f.peer, b[:])
		if err == nil && b[0] != 'R' { err = errors.New("fixture expected exact R") }
		if err == nil { _, err = f.peer.Write([]byte{'A'}) }
		done <- err
	}()
	err := f.q.Release(t.Context())
	expiryDescriptorControlRecord(t, f.ledger, "positive.Release", err)
	writerErr := <-done
	expiryDescriptorControlRecord(t, f.ledger, "positive.writer", writerErr)
	if err != nil || writerErr != nil { t.Fatal(err, writerErr) }
	if f.q.state.phase != expiryAcknowledged || f.q.state.invalid.Load() || f.q.state.ctx.Err() != nil { t.Fatal("intentional parent close revoked independent context or failed acknowledgment") }
	// R/A is ONLY component protocol evidence, not durable expiry or Host proof.
}

func TestExpiryGateControlIndependentProtocolCases(t *testing.T) {
	for _, name := range []string{"wrong-H", "early-A", "H-before-start", "duplicate-H", "H-then-early-A", "EOF-before-H", "missing-H-deadline", "caller-canceled", "run-canceled", "gate-context-canceled", "gate-revoked", "release-before-H", "check-before-H", "repeat-WaitHeld", "wrong-A", "extra-A", "EOF-before-A", "missing-A-deadline", "unread-A-close", "expired-local-budget", "repeat-Release", "close-without-A", "closed-terminal"} {
		t.Run(name, func(t *testing.T) {
			f := expiryGateControlNew(t)
			ctx := t.Context()
			var err error
			switch name {
			case "wrong-H", "early-A", "duplicate-H", "H-then-early-A":
				b := map[string][]byte{"wrong-H": {'X'}, "early-A": {'A'}, "duplicate-H": {'H','H'}, "H-then-early-A": {'H','A'}}[name]
				expiryGateControlSend(t, f, b...); err = f.q.WaitHeld(ctx)
			case "H-before-start": f.q.state.phase = expiryCreated; expiryGateControlSend(t, f, 'H'); err = f.q.WaitHeld(ctx)
			case "EOF-before-H": f.closePeer(); err = f.q.WaitHeld(ctx)
			case "missing-H-deadline":
				var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond); defer cancel(); err = f.q.WaitHeld(ctx)
			case "caller-canceled":
				var cancel context.CancelFunc; ctx, cancel = context.WithCancel(ctx); cancel(); err = f.q.WaitHeld(ctx)
			case "run-canceled": f.cancelRun(); err = f.q.WaitHeld(ctx)
			case "gate-context-canceled": f.q.state.cancel(); err = f.q.WaitHeld(ctx)
			case "gate-revoked": f.q.state.revoke(); err = f.q.WaitHeld(ctx)
			case "release-before-H": err = f.q.Release(ctx)
			case "check-before-H": err = f.q.CheckHeld(ctx)
			case "repeat-WaitHeld": expiryGateControlHold(t, f); err = f.q.WaitHeld(ctx)
			case "expired-local-budget": expiryGateControlHold(t, f); f.q.state.heldAt = time.Now().Add(-expiryHeldBudget); err = f.q.CheckHeld(ctx)
			case "unread-A-close":
				expiryGateControlHold(t, f); expiryGateControlSend(t, f, 'A')
				// Actual queued A left unread: Close is teardown, never Release.
				if e := f.closeGate(); e != nil { t.Error(e) }; err = f.q.Release(ctx)
			case "close-without-A", "closed-terminal":
				if name == "close-without-A" { expiryGateControlHold(t, f) }
				if e := f.closeGate(); e != nil { t.Error(e) }; err = f.q.CheckHeld(ctx)
			default:
				expiryGateControlHold(t, f)
				if name == "missing-A-deadline" { var cancel context.CancelFunc; ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond); defer cancel() }
				done := make(chan error, 1)
				go func() {
					var b [1]byte
					if e := f.peer.SetReadDeadline(time.Now().Add(time.Second)); e != nil { done <- e; return }
					_, e := io.ReadFull(f.peer, b[:])
					if e == nil && b[0] != 'R' { e = errors.New("actual R not received") }
					if e == nil {
						switch name {
						case "wrong-A": _, e = f.peer.Write([]byte{'X'})
						case "extra-A": _, e = f.peer.Write([]byte{'A','A'})
						case "EOF-before-A": f.closePeer()
						case "repeat-Release": _, e = f.peer.Write([]byte{'A'})
						}
					}
					done <- e // actual read/write completion, NOT observer fabrication
				}()
				err = f.q.Release(ctx)
				if e := <-done; e != nil { t.Error("FIRST simulated writer", e) }
				if name == "repeat-Release" {
					if err != nil || f.q.state.phase != expiryAcknowledged || f.q.state.ctx.Err() != nil { t.Fatal("first actual R/A did not acknowledge with independent context live", err) }
					err = f.q.Release(ctx)
				}
			}
			expiryDescriptorControlRecord(t, f.ledger, "required-boundary", err)
			if err == nil { t.Error("required independent refusal absent") }
			expiryGateControlTerminal(t, f)
		})
	}
}

// A test-only Context rendezvous stops the FIRST public call at its actual
// contextLive inside begin, AFTER the real TryLock and pending reservation.
// No new production callback, sleep, yield, polling or fake watch completion.
type expiryGateControlRendezvousContext struct {
	context.Context
	once sync.Once
	entered chan struct{}
	proceed chan struct{}
}
func (c *expiryGateControlRendezvousContext) Err() error {
	c.once.Do(func() { close(c.entered); <-c.proceed })
	return c.Context.Err()
}
func TestExpiryGateControlEveryOrderedPublicOverlap(t *testing.T) {
	for _, first := range []string{"WaitHeld", "Release", "CheckHeld"} {
		for _, second := range []string{"WaitHeld", "Release", "CheckHeld"} {
			t.Run(first+"-overlap-"+second, func(t *testing.T) {
				f := expiryGateControlNew(t)
				ops := map[string]func(context.Context) error{"WaitHeld": f.q.WaitHeld, "Release": f.q.Release, "CheckHeld": f.q.CheckHeld}
				if first != "WaitHeld" { expiryGateControlHold(t, f) }
				ctx := &expiryGateControlRendezvousContext{Context: t.Context(), entered: make(chan struct{}), proceed: make(chan struct{})}
				var unblock sync.Once
				resume := func() { unblock.Do(func() { close(ctx.proceed) }) }
				t.Cleanup(resume)
				done := make(chan error, 1)
				go func() { done <- ops[first](ctx) }()
				select { case <-ctx.entered: case <-time.After(time.Second): resume(); t.Fatal("first public operation never reached reserved boundary") }
				secondErr := ops[second](t.Context())
				resume()
				var firstErr error
				select { case firstErr = <-done: case <-time.After(time.Second): t.Fatal("first actual operation did not finish after overlap revoke") }
				expiryDescriptorControlRecord(t, f.ledger, "first."+first, firstErr)
				expiryDescriptorControlRecord(t, f.ledger, "second."+second, secondErr)
				if firstErr == nil || secondErr == nil { t.Error("overlap earned success", firstErr, secondErr) }
				if f.q.state.report.operationPending.Load() { t.Error("actual first operation not quiescent") }
				expiryGateControlTerminal(t, f)
			})
		}
	}
}

func TestExpiryGateControlDuplicateParentEndpointPreventsEOF(t *testing.T) {
	f := expiryGateControlNew(t)
	duplicate, err := f.q.state.parent.File()
	expiryDescriptorControlRecord(t, f.ledger, "parent.duplicate", err)
	if err != nil { t.Fatal(err) }
	closeDuplicate := expiryDescriptorControlOwnFile(t, f.ledger, "parent.fixture-duplicate", duplicate)
	if err := f.closeGate(); err != nil { t.Fatal(err) }
	if err := f.peer.SetReadDeadline(time.Now().Add(20*time.Millisecond)); err != nil { t.Fatal(err) }
	var b [1]byte
	_, first := f.peer.Read(b[:])
	expiryDescriptorControlRecord(t, f.ledger, "peer.read-with-duplicate", first)
	if e, ok := first.(net.Error); !ok || !e.Timeout() { t.Fatal("owned duplicate should suppress EOF; timeout is negative control, not liveness", first) }
	closeDuplicate()
	if err := f.peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil { t.Fatal(err) }
	_, last := f.peer.Read(b[:])
	expiryDescriptorControlRecord(t, f.ledger, "peer.read-after-duplicate-close", last)
	if !errors.Is(last, io.EOF) { t.Fatal("last actual parent close did not release writer", last) }
	expiryGateControlTerminal(t, f)
}

func TestExpiryGateControlRegularAndTCPChildSubstitution(t *testing.T) {
	for _, kind := range []string{"regular", "TCP-SOCK_STREAM-not-AF_UNIX"} {
		t.Run(kind, func(t *testing.T) {
			ledger := expiryDescriptorControlLedgerNew(t)
			root := expiryDescriptorControlRoot(t, ledger)
			v := expiryDescriptorControlImage(t, ledger, root)
			var replacement *os.File
			if kind == "regular" {
				f, err := os.OpenFile(filepath.Join(root, "wrong-gate"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
				if err != nil { t.Fatal(err) }; replacement = f
			} else {
				listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127,0,0,1)})
				if err != nil { t.Fatal(err) }
				t.Cleanup(func() { expiryDescriptorControlCleanupResult(t, ledger, "TCP.listener.close", listener.Close()) })
				connection, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
				if err != nil { t.Fatal(err) }
				t.Cleanup(func() { expiryDescriptorControlCleanupResult(t, ledger, "TCP.client.close", connection.Close()) })
				accepted, err := listener.AcceptTCP(); if err != nil { t.Fatal(err) }
				t.Cleanup(func() { expiryDescriptorControlCleanupResult(t, ledger, "TCP.accepted.close", accepted.Close()) })
				replacement, err = accepted.File(); if err != nil { t.Fatal(err) }
				raw, err := replacement.SyscallConn(); if err != nil { t.Fatal(err) }
				var domain, socketType int
				var domainErr, typeErr error
				if err := raw.Control(func(fd uintptr) { domain, domainErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_DOMAIN); socketType, typeErr = unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE) }); err != nil { t.Fatal(err) }
				if domainErr != nil || typeErr != nil || domain != unix.AF_INET || socketType != unix.SOCK_STREAM { t.Fatal("TCP control not independent", domain, socketType, domainErr, typeErr) }
			}
			expiryDescriptorControlOwnFile(t, ledger, "replacement", replacement)
			original := v.expiry.child
			v.expiry.child = replacement
			_, err := v.command(nil, nil)
			v.expiry.child = original // restore only the known owned descriptor
			expiryDescriptorControlRecord(t, ledger, "actual.command.substitution", err)
			// Required refusal assertion, not skip-green. The parent has written
			// fixedCommand's AF_UNIX/SOCK_STREAM guard as a code-only change;
			// no refusal/GREEN has been observed. SO_TYPE alone is not AF_UNIX.
			if err == nil { t.Errorf("required %s refusal missing in actual command; existing-seam product gap", kind) }
		})
	}
}

// UNTESTED kernel/component control: AF_UNIX passes the new domain check,
// while SOCK_DGRAM must independently fail the type check. Not authentic Host.
func TestExpiryGateControlRejectsUnixDatagram(t *testing.T) {
	ledger := expiryDescriptorControlLedgerNew(t)
	v := expiryDescriptorControlImage(t, ledger, expiryDescriptorControlRoot(t, ledger))
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	expiryDescriptorControlRecord(t, ledger, "datagram.socketpair", err)
	if err != nil { t.Fatal(err) }
	// Socketpair success transfers exactly these two descriptors to this
	// fixture. Register both actual FIRST closes before any subsequent check.
	left := os.NewFile(uintptr(pair[0]), "fixture-owned-unix-datagram-left")
	right := os.NewFile(uintptr(pair[1]), "fixture-owned-unix-datagram-right")
	expiryDescriptorControlOwnFile(t, ledger, "datagram.left", left)
	expiryDescriptorControlOwnFile(t, ledger, "datagram.right", right)
	domain, domainErr := unix.GetsockoptInt(int(left.Fd()), unix.SOL_SOCKET, unix.SO_DOMAIN)
	expiryDescriptorControlRecord(t, ledger, "datagram.fixture-domain-query", domainErr)
	if domainErr != nil || domain != unix.AF_UNIX { t.Fatal("fixture is not AF_UNIX", domain, domainErr) }
	kind, typeErr := unix.GetsockoptInt(int(left.Fd()), unix.SOL_SOCKET, unix.SO_TYPE)
	expiryDescriptorControlRecord(t, ledger, "datagram.fixture-type-query", typeErr)
	if typeErr != nil || kind != unix.SOCK_DGRAM { t.Fatal("fixture is not SOCK_DGRAM", kind, typeErr) }
	original := v.expiry.child
	v.expiry.child = left
	command, commandErr := v.command(nil, nil)
	v.expiry.child = original // immediately restore the known product-owned child
	expiryDescriptorControlRecord(t, ledger, "datagram.actual-command", commandErr)
	if command != nil { t.Error("Unix datagram substitution escaped an executable command") }
	if !IsFailure(commandErr, FailureIdentityUnprovable) { t.Error("required Unix datagram type refusal missing", commandErr) }
	if !v.expiry.gate.invalid.Load() || v.expiry.gate.ctx.Err() == nil { t.Error("datagram refusal did not revoke original gate") }
}

func TestExpiryGateControlOriginalGenerationAndNoRenewedWork(t *testing.T) {
	for _, change := range []string{"owned-revoked", "stopped", "wrong-original-gate", "wrong-process", "wrong-bootstrap", "closed-lifetime", "runtime-mode", "socket-replaced"} {
		t.Run(change, func(t *testing.T) {
			f := expiryGateControlNew(t)
			expiryGateControlHold(t, f)
			l := f.lifecycle
			switch change {
			case "owned-revoked": l.revokeOwned()
			case "stopped": l.stopped = true
			case "wrong-original-gate": l.expiry.gate = new(expiryGateState)
			case "wrong-process": l.provenance.Process.StartTicks++
			case "wrong-bootstrap": l.ownedBootstrap.DatabaseID = "foreign"
			case "closed-lifetime": f.closeLifetime()
			case "runtime-mode": if err := os.Chmod(l.provenance.RuntimeDirectory, 0750); err != nil { t.Fatal(err) }
			case "socket-replaced":
				// Rename only this fixture's original pathname; preserve both.
				if err := os.Rename(l.ownedEndpoint, l.ownedEndpoint+".original"); err != nil { t.Fatal(err) }
				replacement, err := os.OpenFile(l.ownedEndpoint, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); if err != nil { t.Fatal(err) }
				close := expiryDescriptorControlOwnFile(t, f.ledger, "replacement.socket-decoy", replacement); close()
			}
			calls := 0
			l.processes.executable = func(int) (fileIdentity, string, error) { calls++; return fileIdentity{}, "", errors.New("forbidden renewed image hash") }
			l.processes.startTicks = func(int) (uint64, error) { calls++; return 0, errors.New("forbidden renewed proc check") }
			if err := f.q.CheckHeld(t.Context()); err == nil { t.Error("changed generation accepted") }
			if calls != 0 { t.Error("postterminal scalar guard renewed process work", calls) }
			expiryGateControlTerminal(t, f)
		})
	}
}

func TestExpiryGateControlCancellationCallbackActualJoin(t *testing.T) {
	f := expiryGateControlNew(t)
	f.q.state.cancel() // actual registered AfterFunc must either run or be stopped
	if err := f.closeGate(); err != nil { t.Fatal(err) }
	watch := f.q.state.watch
	select { case <-watch.done: default: t.Fatal("actual callback/stopped-callback completion absent") }
	if !watch.finished.Load() { t.Fatal("callback body completion not published") }
	r := f.q.state.report
	if r.counts[expiryCallback].required.Load() != r.counts[expiryCallback].completed.Load() { t.Fatal("owned callbacks incomplete") }
	if err := r.gateTeardownError(); err != nil { t.Fatal(err) }
	expiryGateControlTerminal(t, f)
}

func TestExpiryGateControlSourceOwnershipAndFixedIO(t *testing.T) {
	ledger := expiryDescriptorControlLedgerNew(t)
	file, err := os.Open("expiry_qualification_launch.go"); if err != nil { t.Fatal(err) }
	close := expiryDescriptorControlOwnFile(t, ledger, "launch.source", file)
	b, err := io.ReadAll(file); expiryDescriptorControlRecord(t, ledger, "launch.source.read", err); close(); if err != nil { t.Fatal(err) }
	s := string(b)
	for _, required := range []string{"context.AfterFunc(ctx, func() { defer w.finish(); g.revoke() })", "if w.stop()", "case <-w.done:", "g.report.closed(expiryCallback, nil)", "g.closeOnce.Do(", "g.childOnce.Do(", "g.invalid.Store(true)", "g.closeParent() // independent", "g.phase = expiryAcknowledged"} {
		if !strings.Contains(s, required) { t.Errorf("actual callback/descriptor ownership site missing: %s", required) }
	}
	_, revoke, _ := strings.Cut(s, "func (g *expiryGateState) revoke()")
	revoke, _, _ = strings.Cut(revoke, "type expiryCancelWatch")
	if strings.Contains(revoke, ".Lock()") || strings.Contains(revoke, ".TryLock()") { t.Error("revocation waits for state locks") }
	for _, method := range []string{"WaitHeld", "Release", "CheckHeld"} {
		_, body, ok := strings.Cut(s, "func (q *ExpiryQualification) "+method+"(")
		if !ok { t.Fatal(method) }
		body, _, _ = strings.Cut(body, "\nfunc ")
		if !strings.Contains(body, "g.begin(ctx)") { t.Error("public operation bypasses overlap/context gate", method) }
		if method != "CheckHeld" && !strings.Contains(body, "g.watchContext(ctx)") { t.Error("I/O missing actual cancel watch", method) }
	}
	t.Log("source/component guards only; no native Host assertion")
}
