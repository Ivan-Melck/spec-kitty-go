//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"golang.org/x/sys/unix"
)

// H06 component controls only. No input/admission authority, provider workload,
// genuine Host, or Native qualification is manufactured by these fixtures.
// Resources and FIRST results survive failures; no automatic recursive cleanup.
func h06FaultDirectory(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err != nil { t.Fatal(err) }
	root, err := os.MkdirTemp(cache, "h06-")
	if err != nil { t.Fatal(err) }
	if err := os.Chmod(root, 0700); err != nil { t.Fatal(err) }
	t.Logf("H06 private component residual (manual retirement only): %s", root)
	return root
}

func h06FaultWrite(t *testing.T, path string, raw []byte, mode os.FileMode) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil { t.Fatal(err) }
	n, writeErr := file.Write(raw)
	firstClose := file.Close()
	if writeErr != nil || n != len(raw) || firstClose != nil {
		t.Fatalf("fixture write=%v bytes=%d first close=%v", writeErr, n, firstClose)
	}
}

func h06FaultCounts(t *testing.T, r *expiryCleanupReport, category int, acquired, attempted, completed uint64) {
	t.Helper()
	c := &r.counts[category]
	got := [3]uint64{c.required.Load(), c.recorded.Load(), c.completed.Load()}
	want := [3]uint64{acquired, attempted, completed}
	if got != want { t.Fatalf("category=%d acquired/attempted/completed=%v want=%v", category, got, want) }
	t.Logf("category=%d acquired/attempted/completed=%v", category, got)
}

func h06FaultReport(t *testing.T, r *expiryCleanupReport) {
	t.Helper()
	for i := range r.counts {
		c := &r.counts[i]
		t.Logf("category=%d acquired=%d attempted=%d completed=%d", i, c.required.Load(), c.recorded.Load(), c.completed.Load())
	}
	t.Logf("FIRST=%v full=%v pending(operation/startup/lifecycle/teardown)=%t/%t/%d/%t bound/retired=%t/%t; process kill/wait/reap not inferred from report",
		r.firstError(), r.cleanupError(), r.operationPending.Load(), r.startupPending.Load(), r.lifecyclePending.Load(), r.teardownPending.Load(), r.lifecycleBound.Load(), r.lifecycleRetired.Load())
}

func TestExpiryH06ArtifactReadFaultSites(t *testing.T) {
	for _, placement := range []string{"missing", "symlink", "mode", "hardlink", "size", "digest"} {
		t.Run(placement, func(t *testing.T) {
			root := h06FaultDirectory(t)
			path := filepath.Join(root, "input")
			raw := []byte("component artifact, never authentic input\n")
			if placement != "missing" { h06FaultWrite(t, path, raw, 0600) }
			switch placement {
			case "symlink":
				if err := os.Rename(path, path+".held"); err != nil { t.Fatal(err) }
				if err := os.Symlink(path+".held", path); err != nil { t.Fatal(err) }
			case "mode":
				if err := os.Chmod(path, 0620); err != nil { t.Fatal(err) }
			case "hardlink":
				if err := os.Link(path, path+".link"); err != nil { t.Fatal(err) }
			}
			length, digest := uint64(len(raw)), expiryRawSHA256(raw)
			if placement == "size" { length++ }
			if placement == "digest" { digest = strings.Repeat("0", 64) }
			r := new(expiryCleanupReport)
			got, primary := readExpiryArtifact(path, length, digest, r)
			if primary == nil || got != nil { t.Fatalf("actual artifact site accepted fault: bytes=%d primary=%v", len(got), primary) }
			want := uint64(1)
			if placement == "missing" || placement == "symlink" || placement == "mode" { want = 0 }
			h06FaultCounts(t, r, expiryMetadata, want, want, want)
			if r.cleanupError() != nil { t.Fatal("actual artifact close incomplete", r.cleanupError()) }
			t.Logf("primary=%v transferred=none process=not-started paths preserved", primary)
		})
	}
}

func TestExpiryH06ImageVerificationFaultSites(t *testing.T) {
	for _, placement := range []string{"missing", "mode", "hardlink", "size", "digest"} {
		t.Run(placement, func(t *testing.T) {
			root := h06FaultDirectory(t)
			path, raw := filepath.Join(root, "not-an-executable-image"), []byte("H06 non-ELF component bytes\n")
			if placement != "missing" { h06FaultWrite(t, path, raw, 0500) }
			if placement == "mode" { if err := os.Chmod(path, 0400); err != nil { t.Fatal(err) } }
			if placement == "hardlink" { if err := os.Link(path, path+".link"); err != nil { t.Fatal(err) } }
			size, digest := int64(len(raw)), expiryRawSHA256(raw)
			if placement == "size" { size++ }
			if placement == "digest" { digest = strings.Repeat("0", 64) }
			r := new(expiryCleanupReport)
			v, primary := verifyExecutableImage(path, digest, size, ExecutableProvenance{}, r)
			if primary == nil || v != nil { t.Fatal("verification fault escaped", primary) }
			want := uint64(1)
			if placement == "missing" { want = 0 }
			h06FaultCounts(t, r, expirySource, want, want, want)
			h06FaultCounts(t, r, expiryImage, 0, 0, 0)
			if r.cleanupError() != nil { t.Fatal(r.cleanupError()) }
			t.Logf("primary=%v transferred=none process=not-started", primary)
		})
	}
}

// Exercise real verifier, memfd construction and pair owners, but NEVER the
// fixed input verifier or authentic TEST image. ENOEXEC is deliberately expected.
func h06FaultVerified(t *testing.T) *VerifiedExecutable {
	t.Helper()
	root := h06FaultDirectory(t)
	path, raw := filepath.Join(root, "non-ELF"), []byte("H06 component non-ELF; not a provider\n")
	h06FaultWrite(t, path, raw, 0500)
	r := new(expiryCleanupReport)
	v, err := verifyExecutableImage(path, expiryRawSHA256(raw), int64(len(raw)), ExecutableProvenance{}, r)
	if err != nil { t.Fatal(err) }
	v.expiry = verifiedExpiryState{report: r, identity: ExpiryTechnicalIdentity{InputRecordSHA256: "component-only-not-authenticated"}}
	if err := v.makeExpiryPair(context.Background()); err != nil {
		first := v.Close()
		t.Fatalf("pair=%v first verified close=%v", err, first)
	}
	return v
}

func TestExpiryH06StartVerifiedFaultSites(t *testing.T) {
	for _, placement := range []string{"runtime-mode", "data-mode", "command-closed-image", "start-enoexec"} {
		t.Run(placement, func(t *testing.T) {
			v := h06FaultVerified(t)
			closed := false
			defer func() { if !closed { first := v.Close(); t.Logf("emergency FIRST verified close=%v", first) } }()
			root := h06FaultDirectory(t)
			runtime, data := filepath.Join(root, "runtime"), filepath.Join(root, "data")
			if err := os.Mkdir(runtime, 0700); err != nil { t.Fatal(err) }
			if err := os.Mkdir(data, 0700); err != nil { t.Fatal(err) }
			if placement == "runtime-mode" { if err := os.Chmod(runtime, 0750); err != nil { t.Fatal(err) } }
			if placement == "data-mode" { if err := os.Chmod(data, 0750); err != nil { t.Fatal(err) } }
			if placement == "command-closed-image" {
				// Close the wrapper, not just its integer: startVerified opens
				// metadata before command, so a stale raw fd could be reused.
				if first := v.execution.Close(); first != nil { t.Fatal(first) }
				t.Log("independent fixture FIRST image close=nil; original command sees closed wrapper, product cleanup must retain ErrClosed, not infer a first owned close failure")
			}
			l, primary := startVerified(context.Background(), LifecycleConfig{RuntimeRoot: runtime, DataDirectory: data,
				StartTimeout: time.Second, StopTimeout: time.Second, processes: systemProcessAPI}, v)
			if l != nil || primary == nil { t.Fatal("fault did not refuse", primary) }
			if !v.expiry.gate.invalid.Load() || v.expiry.gate.ctx.Err() == nil { t.Fatal("startup failure did not revoke gate") }
			transferred := placement == "command-closed-image" || placement == "start-enoexec"
			if v.expiry.transferred != transferred { t.Fatal("transfer point differs") }
			if placement == "start-enoexec" && !errors.Is(primary, unix.ENOEXEC) { t.Fatal("actual Cmd.Start ENOEXEC not reached", primary) }
			firstVerified := v.Close(); closed = true
			firstGate := (&ExpiryQualification{state: v.expiry.gate}).Close()
			r := v.expiry.report
			h06FaultReport(t, r)
			for _, cat := range []int{expirySource, expiryImage, expiryChild, expiryTemporary, expiryParent} { h06FaultCounts(t, r, cat, 1, 1, 1) }
			if placement == "command-closed-image" {
				if !errors.Is(firstVerified, os.ErrClosed) || !errors.Is(r.firstError(), os.ErrClosed) { t.Fatal("closed-wrapper cleanup diagnostic lost", firstVerified, r.firstError()) }
			} else if firstVerified != nil || firstGate != nil { t.Fatal("first cleanup failed", firstVerified, firstGate) }
			// Lifecycle transfer happened before command. Its unavailable final
			// report is NOT repaired by gate-only closure or v.Close returning nil.
			if transferred && r.cleanupError() == nil { t.Fatal("unretired transferred lifecycle falsely complete") }
			t.Logf("primary=%v FIRST verified=%v FIRST gate=%v transferred=%t kill=not-needed wait=not-started reap=not-applicable; no Host credit", primary, firstVerified, firstGate, transferred)
		})
	}
}

func TestExpiryH06FirstOwnedFileCloseFaultSites(t *testing.T) {
	for _, placement := range []string{"source", "image", "child", "local-file", "local-fd"} {
		t.Run(placement, func(t *testing.T) {
			if placement == "local-file" || placement == "local-fd" {
				root := h06FaultDirectory(t)
				file, err := os.Open(root)
				if err != nil { t.Fatal(err) }
				r := new(expiryCleanupReport)
				r.localOpened()
				fd := int(file.Fd())
				if placement == "local-fd" {
					// Transfer raw ownership away from os.File before injecting.
					dup, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
					firstWrapper := file.Close()
					if err != nil || firstWrapper != nil { t.Fatal(err, firstWrapper) }
					fd = dup
				}
				var first, want error
				if placement == "local-file" {
					// File.Close relinquishes its numeric descriptor before a later
					// wrapper call. No reused raw descriptor can be closed here.
					if err := file.Close(); err != nil { t.Fatal(err) }
					first, want = r.closeLocalFile(file), os.ErrClosed
				} else {
					// Returned-error component control, NOT a failed kernel close:
					// close the owned duplicate exactly once, then inject a result.
					want = errors.New("H06 component returned close error")
					var actual error
					first = r.closeLocalFDWith(fd, func(owned int) error {
						actual = unix.Close(owned)
						return errors.Join(actual, want)
					})
					if actual != nil { t.Fatal("owned duplicate FIRST close failed", actual) }
				}
				if !errors.Is(first, want) || !errors.Is(r.firstError(), want) { t.Fatal("FIRST returned-close failure not retained", first, r.firstError()) }
				h06FaultCounts(t, r, expiryLifecycleMetadata, 1, 1, 1)
				return
			}
			v := h06FaultVerified(t)
			file := v.source
			if placement == "image" { file = v.execution }
			if placement == "child" { file = v.expiry.child }
			// Safe closed-object component control. The numeric fd is retired by
			// its os.File; a later product wrapper cannot close a new fd owner.
			if err := file.Close(); err != nil { t.Fatal(err) }
			first := v.Close() // FIRST product wrapper outcome, not first kernel close
			if !errors.Is(first, os.ErrClosed) || !errors.Is(v.expiry.report.firstError(), os.ErrClosed) { t.Fatal("closed-object diagnostic missing", first) }
			sticky := v.expiry.report.first.Load()
			_ = v.Close() // permitted repeat semantics, NOT cleanup evidence
			if v.expiry.report.first.Load() != sticky || v.expiry.report.cleanupError() == nil { t.Fatal("later nil repaired FIRST failure") }
			h06FaultReport(t, v.expiry.report)
		})
	}
}

// Existing processAPI failures at the real establishment transition. The only
// PID here is self; it is NEVER signalled or Waited. Success is not attempted.
func TestExpiryH06EstablishProcessFaultSites(t *testing.T) {
	for _, placement := range []string{"start-first", "image-first", "pidfd-open", "start-second", "image-second", "metadata", "supervisor-close"} {
		t.Run(placement, func(t *testing.T) {
			image, digest, err := processExecutable(os.Getpid())
			if err != nil { t.Fatal(err) }
			expected := ExecutedImageProvenance{Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
			r := new(expiryCleanupReport)
			api := systemProcessAPI
			fault, closeFault := errors.New("H06 actual processAPI boundary"), errors.New("H06 closeFD returned after actual FIRST close")
			startCalls, imageCalls, opens, closes := 0, 0, 0, 0
			api.startTicks = func(pid int) (uint64, error) {
				startCalls++
				if placement == "start-first" && startCalls == 1 || (placement == "start-second" || placement == "supervisor-close") && startCalls == 2 { return 0, fault }
				return systemProcessAPI.startTicks(pid)
			}
			api.executable = func(pid int) (fileIdentity, string, error) {
				imageCalls++
				if placement == "image-first" && imageCalls == 1 || placement == "image-second" && imageCalls == 2 { return fileIdentity{}, "", fault }
				return systemProcessAPI.executable(pid)
			}
			api.pidfdOpen = func(pid, flags int) (int, error) {
				opens++
				if placement == "pidfd-open" { return -1, fault }
				return unix.PidfdOpen(pid, flags)
			}
			api.executableIdentity = func(int) (fileIdentity, error) { return fileIdentity{}, fault }
			api.closeFD = func(fd int) error {
				closes++
				first := unix.Close(fd)
				if first != nil { t.Errorf("actual pidfd first close=%v", first) }
				if placement == "supervisor-close" { return errors.Join(first, closeFault) }
				return first
			}
			l := &Lifecycle{command: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}, processes: api, pidfd: -1, expiry: lifecycleExpiryState{report: r}}
			primary := l.establishStartedProcess(expected)
			if primary == nil || !errors.Is(primary, fault) || l.process.PID != 0 || l.pidfd != -1 || l.exit != nil { t.Fatal("partial attribution escaped", primary) }
			acquired := uint64(0)
			if placement == "start-second" || placement == "image-second" || placement == "metadata" || placement == "supervisor-close" { acquired = 1 }
			h06FaultCounts(t, r, expirySupervisor, acquired, acquired, acquired)
			if closes != int(acquired) { t.Fatal("owned pidfd close count differs", opens, closes) }
			if placement == "supervisor-close" {
				if !errors.Is(r.firstError(), closeFault) || !errors.Is(primary, closeFault) || !l.pendingCustody.supervisorCloseAttempted || !errors.Is(l.pendingCustody.supervisorCloseErr, closeFault) { t.Fatal("actual rollback custody absent outward/pending/report", primary, r.firstError()) }
			} else if r.cleanupError() != nil { t.Fatal(r.cleanupError()) }
			t.Logf("primary=%v FIRST cleanup=%v union=%v transferred=none kill=never wait=never reap=not-applicable self-only", primary, r.firstError(), errors.Join(primary, r.cleanupError()))
		})
	}
}

// Four original probe placements, each independently reaches the actual frame
// read and deferred connection close. This local EOF server authenticates no
// provider workload and grants no Host/count/recovery credit.
func TestExpiryH06EveryProbeConnectionFaultSite(t *testing.T) {
	for _, slot := range []string{"status-before", "health", "describe_config", "status-after"} {
		t.Run(slot, func(t *testing.T) {
			root := h06FaultDirectory(t)
			path := filepath.Join(root, "probe.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil { t.Fatal(err) }
			listener.SetUnlinkOnClose(false)
			defer func() { if first := listener.Close(); first != nil { t.Errorf("FIRST listener close=%v", first) } }()
			if err := listener.SetDeadline(time.Now().Add(2*time.Second)); err != nil { t.Fatal(err) }
			socket, err := socketPathIdentity(path)
			if err != nil { t.Fatal(err) }
			image, digest, err := processExecutable(os.Getpid())
			if err != nil { t.Fatal(err) }
			ticks, err := processStartTicks(os.Getpid())
			if err != nil { t.Fatal(err) }
			process := ProcessIdentity{PID: os.Getpid(), StartTicks: ticks, Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
			done := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil { done <- err; return }
				deadline := conn.SetDeadline(time.Now().Add(time.Second))
				_, readErr := readWireFrame(conn, 4096)
				firstClose := conn.Close() // actual EOF, no fabricated response
				done <- errors.Join(deadline, readErr, firstClose)
			}()
			operation, destination := "status", any(new(gapdbapi.StatusResult))
			if slot == "health" { operation, destination = "health", new(gapdbapi.HealthResult) }
			if slot == "describe_config" { operation, destination = "describe_config", new(gapdbapi.ConfigResult) }
			r := new(expiryCleanupReport)
			database, primary := observeStrictUnaryWithReport(context.Background(), systemProcessAPI, path, socket, process, operation, destination, time.Second, r)
			serverErr := <-done
			var typed *Error
			if database != "" || !errors.As(primary, &typed) || typed.Operation != operation || typed.Reason != "strict response frame is incomplete" || serverErr != nil { t.Fatal("actual probe frame boundary not reached", primary, serverErr) }
			h06FaultCounts(t, r, expiryLifecycleMetadata, 1, 1, 1)
			if r.cleanupError() != nil { t.Fatal(r.cleanupError()) }
			t.Logf("slot=%s primary=%v FIRST connection close=nil transferred=none process=self-never-signalled; component direct placement, not complete composite", slot, primary)
		})
	}
}

func TestExpiryH06OwnedDialAndTeardownFaultSites(t *testing.T) {
	for _, placement := range []string{"dial-context", "dial-no-pidfd", "stop-foreign-start", "stop-no-pidfd", "stop-signal", "stop-canceled", "unclean-reap-timeout"} {
		t.Run(placement, func(t *testing.T) {
			v := h06FaultVerified(t)
			l := &Lifecycle{pidfd: -1, config: LifecycleConfig{StopTimeout: time.Millisecond}, processes: systemProcessAPI}
			l.initExpiry(v)
			firstVerified := v.Close()
			if firstVerified != nil { t.Fatal(firstVerified) }
			image, digest, err := processExecutable(os.Getpid())
			if err != nil { t.Fatal(err) }
			expected := ExecutedImageProvenance{Device: image.device, Inode: image.inode, Size: image.size, SHA256: digest}
			l.process, l.pidfd, err = observeStartedProcessWithReport(systemProcessAPI, os.Getpid(), expected, l.expiry.report)
			if err != nil { t.Fatal(err) }
			l.processImage = image
			// No real process signal is ever delegated by these component controls.
			signals := 0
			fault := errors.New("H06 existing signal seam")
			l.processes.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error { signals++; if placement == "stop-signal" { return fault }; return nil }
			l.exit = make(chan error, 1) // no actual child/Wait: cannot establish reap
			if placement == "stop-foreign-start" { l.process.StartTicks++ }
			if placement == "dial-no-pidfd" || placement == "stop-no-pidfd" { l.closePidfd() }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if placement == "dial-context" || placement == "stop-canceled" { cancel() }
			var primary error
			if strings.HasPrefix(placement, "dial-") {
				adapter, closer, err := l.DialOwned(ctx, time.Second)
				primary = err
				if adapter != nil || closer != nil { t.Fatal("dial exposed partial resource") }
			} else if placement == "unclean-reap-timeout" { primary = l.stopOwnedUnclean(ctx) } else { primary = l.Stop(ctx) }
			if primary == nil { t.Fatal("fault accepted") }
			if placement == "stop-signal" && !errors.Is(primary, fault) { t.Fatal("primary signal failure lost", primary) }
			if placement == "dial-context" || placement == "stop-canceled" { if !errors.Is(primary, context.Canceled) { t.Fatal("context cause lost", primary) } }
			wantSignals := 0
			if placement == "stop-signal" || placement == "unclean-reap-timeout" { wantSignals = 1 }
			if signals != wantSignals { t.Fatalf("attempted signals=%d want=%d (actual OS signals always zero)", signals, wantSignals) }
			if !strings.HasPrefix(placement, "dial-") {
				if !v.expiry.gate.invalid.Load() || !l.ownedRevoked.Load() { t.Fatal("admission cutoff absent") }
				wantRetired := placement != "stop-canceled"
				if l.expiry.report.lifecycleRetired.Load() != wantRetired { t.Fatal("admission cutoff confused with lifecycle retirement") }
			}
			if placement == "stop-canceled" {
				// Policy: an expired/canceled cleanup cannot bypass unsafe rundown
				// to signal, retire or certify reap. Original successful-retirement
				// obligation remains OPEN, not waived by this refusal component.
				if l.stopped || l.expiry.work.retired || l.shutdownWait.attempted || l.pidfd < 0 || errors.Is(primary, fault) { t.Fatal("unattempted body/signal outcome manufactured", primary) }
				<-l.expiry.work.done
				if l.teardownCustodyErr != primary || !errors.Is(l.expiry.report.firstError(), context.Canceled) || l.expiry.report.cleanupError() == nil { t.Fatal("residual/FIRST cause lost", primary) }
				if again := l.Stop(context.Background()); again != primary { t.Fatal("later grace repaired FIRST", again) }
			}
			h06FaultReport(t, l.expiry.report) // snapshot BEFORE fixture retirement
			// Retire only the fixture's own, known self pidfd. Never signal self,
			// retry Stop, close an unowned integer or certify a fabricated reap.
			l.closePidfd()
			firstGate := (&ExpiryQualification{state: v.expiry.gate}).Close()
			h06FaultCounts(t, l.expiry.report, expirySupervisor, 1, 1, 1)
			if l.expiry.report.lifecyclePending.Load() != 0 { t.Fatal("admitted work not completed") }
			t.Logf("primary=%v FIRST gate=%v signals attempted=%d completed OS=0 kill=not-performed wait=not-performed reap=UNKNOWN/no-child; manual owned pidfd retired AFTER snapshot", primary, firstGate, signals)
		})
	}
}

func TestExpiryH06RuntimeCleanupFilesystemFaultSites(t *testing.T) {
	for _, placement := range []string{"parent-symlink", "directory-replaced", "socket-replaced", "foreign-contents", "unattributed-socket", "non-socket"} {
		t.Run(placement, func(t *testing.T) {
			root := h06FaultDirectory(t)
			parent, runtime := filepath.Join(root, "runtime"), filepath.Join(root, "runtime", "owned")
			if err := os.Mkdir(parent, 0700); err != nil { t.Fatal(err) }
			if err := os.Mkdir(runtime, 0700); err != nil { t.Fatal(err) }
			info, err := os.Lstat(runtime)
			if err != nil { t.Fatal(err) }
			directory, err := identityFromInfo(info)
			if err != nil { t.Fatal(err) }
			path := filepath.Join(runtime, "gapdb.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil { t.Fatal(err) }
			listener.SetUnlinkOnClose(false)
			socket, identityErr := socketPathIdentity(path)
			firstListener := listener.Close()
			if identityErr != nil || firstListener != nil { t.Fatal(identityErr, firstListener) }
			r := new(expiryCleanupReport)
			l := &Lifecycle{pidfd: -1, runtimeID: directory, socketID: socket, provenance: SidecarProvenance{RuntimeDirectory: runtime}, expiry: lifecycleExpiryState{report: r}}
			requireSocket := true
			switch placement {
			case "parent-symlink":
				if err := os.Rename(parent, parent+".held"); err != nil { t.Fatal(err) }
				if err := os.Symlink(parent+".held", parent); err != nil { t.Fatal(err) }
			case "directory-replaced":
				if err := os.Rename(runtime, runtime+".held"); err != nil { t.Fatal(err) }
				if err := os.Mkdir(runtime, 0700); err != nil { t.Fatal(err) }
			case "socket-replaced":
				if err := os.Rename(path, path+".held"); err != nil { t.Fatal(err) }
				replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
				if err != nil { t.Fatal(err) }
				replacement.SetUnlinkOnClose(false)
				if first := replacement.Close(); first != nil { t.Fatal(first) }
			case "foreign-contents": h06FaultWrite(t, filepath.Join(runtime, "foreign-component-marker"), []byte("preserve"), 0600)
			case "unattributed-socket": l.socketID = fileIdentity{}; requireSocket = false
			case "non-socket":
				if err := os.Rename(path, path+".held"); err != nil { t.Fatal(err) }
				h06FaultWrite(t, path, []byte("not a socket"), 0600)
			}
			primary := l.cleanupRuntime(requireSocket)
			if !IsFailure(primary, FailureCleanup) { t.Fatal("unsafe filesystem cleanup accepted", primary) }
			want := uint64(2)
			if placement == "parent-symlink" { want = 0 }
			if placement == "directory-replaced" { want = 1 }
			h06FaultCounts(t, r, expiryLifecycleMetadata, want, want, want)
			if r.cleanupError() != nil { t.Fatal("actual metadata closes incomplete", r.cleanupError()) }
			if placement == "foreign-contents" {
				if _, err := os.Lstat(filepath.Join(runtime, "foreign-component-marker")); err != nil { t.Fatal("foreign component marker removed", err) }
			} else {
				preserved := path
				if placement == "directory-replaced" { preserved = filepath.Join(runtime+".held", "gapdb.sock") }
				if _, err := os.Lstat(preserved); err != nil { t.Fatal("unattributed/replaced component path removed", err) }
			}
			t.Logf("primary=%v all owned metadata FIRST closes complete; attributable socket may be removed before foreign-content refusal; process=none, no signal/reap inference", primary)
		})
	}
}

func TestExpiryH06ProcReadRealFailureSites(t *testing.T) {
	for _, placement := range []string{"stat-read", "exe-open", "exe-metadata-open", "fixed-start-stat-open"} {
		t.Run(placement, func(t *testing.T) {
			r := new(expiryCleanupReport)
			var primary error
			switch placement {
			case "stat-read": _, primary = processStartTicks(-1)
			case "exe-open": _, _, primary = processExecutableWithReport(-1, r)
			case "exe-metadata-open": _, primary = processExecutableIdentityWithReport(-1, r)
			case "fixed-start-stat-open":
				l := &Lifecycle{process: ProcessIdentity{PID: -1}, expiry: lifecycleExpiryState{report: r}}
				primary = l.checkExpiryStartedProcess(ExecutedImageProvenance{})
			}
			if !errors.Is(primary, os.ErrNotExist) { t.Fatal("actual unavailable proc path not reached", primary) }
			h06FaultCounts(t, r, expiryMetadata, 0, 0, 0)
			h06FaultCounts(t, r, expiryLifecycleMetadata, 0, 0, 0)
			t.Logf("primary=%v acquired/transferred=none PID=-1 deliberately nonexistent, never signalled; not genuine started-child attribution", primary)
		})
	}
}

// Historical source-test identity, revised to positive FIRST custody and actual
// Wait ownership. This is SOURCE ONLY, not successful cleanup/Host/Native proof.
func TestExpiryH06AbortStartDiagnosticLossSource(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "lifecycle.go", nil, 0)
	if err != nil { t.Fatal(err) }
	var body *ast.BlockStmt
	for _, decl := range parsed.Decls { if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "abortStart" { body = fn.Body } }
	if body == nil || len(body.List) == 0 { t.Fatal("abortStart missing") }
	// Entry computes the original absolute cleanup cutoff; shared firstCustody
	// publishes admission retirement/revocation BEFORE claim or state locks.
	raw, err := os.ReadFile("startup_custody.go")
	if err != nil { t.Fatal(err) }
	for _, required := range []string{"l.startChildWaitWithCall(command, (*exec.Cmd).Wait)", "w.err = waitCall(command)", "w.state = command.ProcessState", "close(w.done)", "f.pending = l.pendingCustody", "f.signalErr = l.signalLocked(unix.SIGKILL)", "f.signalErr = killChild(command.Process)", "context.WithDeadline(ctx, deadline)", "f.timedOut = true", "f.reapProven = f.state != nil", "f.runtimeDeletionWithheld", "if !f.runtimeDeletionWithheld"} {
		if !strings.Contains(string(raw), required) { t.Error("FIRST custody boundary lost", required) }
	}
	for _, fn := range parsed.Decls {
		if decl, ok := fn.(*ast.FuncDecl); ok {
			ast.Inspect(decl.Body, func(node ast.Node) bool {
				if a, ok := node.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 {
					if blank, yes := a.Lhs[0].(*ast.Ident); yes && blank.Name == "_" {
						if c, yes := a.Rhs[0].(*ast.CallExpr); yes { if sel, yes := c.Fun.(*ast.SelectorExpr); yes && sel.Sel.Name == "signalLocked" { t.Error("signal discard reintroduced") } }
					}
				}
				return true
			})
		}
	}
	lifecycle, err := os.ReadFile("lifecycle.go")
	if err != nil { t.Fatal(err) }
	for _, required := range []string{"l.firstCustodyBefore(ctx, deadline, false,", "return l.startupAbort.result", "l.abortStartBeforeWithKill(ctx, deadline, (*os.Process).Kill)"} {
		if !strings.Contains(string(lifecycle), required) { t.Error("immutable FIRST abort owner missing", required) }
	}
	t.Log("H06 SOURCE ONLY: signal/wait/close errors retained; channel/stopped/pidfd-close alone NEVER proves reap; actual-fault/Host/Native obligations remain OPEN.")
}
