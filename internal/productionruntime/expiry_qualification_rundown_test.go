//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Model setup only: deliberately NO healthy gate, provider, Host, or launch.
// It permits testing cleanup after failure without inventing a live authority.
func expiryC3Model(t *testing.T) (*expiryHostLease, *expiryHostRun) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	l := &expiryHostLease{ctx: ctx, parent: context.Background(), cancel: cancel, phase: expiryLeaseRunning}
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	r := &expiryHostRun{lease: l, ctx: runCtx, cancel: runCancel, window: newExpiryWorkWindow()}
	l.current = r
	return l, r
}
func expiryC3PendingOperation(l *expiryHostLease, r *expiryHostRun) *expiryOperationWork {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := &expiryOperationWork{run: r, window: r.window}
	r.window.operation = w
	return w
}
func expiryC3PendingMetadata(l *expiryHostLease, r *expiryHostRun) *expiryMetadataWork {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := &expiryMetadataWork{run: r, window: r.window, next: r.window.metadata}
	r.window.metadata = w
	r.window.metadataCount++
	return w
}
func expiryC3AssertOpen(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("completion signaled before retirement and final publication")
	default:
	}
}
func TestExpiryHostC3FreshControllerWithoutGateAndFailureKeepsOwner(t *testing.T) {
	l := &expiryHostLease{phase: expiryLeaseFresh}
	c, err := l.reserveControl(nil, expiryControlFresh, nil)
	if err != nil || c == nil || c.predecessor != nil || c.stage != expiryControlReserved {
		t.Fatal("fresh incorrectly requires gate")
	}
	if other, err := l.reserveControl(nil, expiryControlFresh, nil); err == nil || other != nil {
		t.Fatal("second controller")
	}
	if l.control != c || !l.failed || c.stage != expiryControlReserved {
		t.Fatal("sticky failure replaced/advanced owner")
	}
	forged := *c
	if err := l.moveControl(&forged, expiryControlAcquiringRoot, expiryControlReserved); err == nil {
		t.Fatal("copied controller accepted")
	}
	if err := l.moveControl(c, expiryControlAcquiringRoot, expiryControlReserved); err != nil {
		t.Fatal("winner lost cleanup authority", err)
	}
}
func TestExpiryHostC3TwoControllersAndWorkerFailureStaleClose(t *testing.T) {
	l, r := expiryC3Model(t)
	work := expiryC3PendingOperation(l, r)
	var wg sync.WaitGroup
	controls := make(chan *expiryLeaseControl, 2)
	for i := 0; i < 2; i++ {
		wg.Go(func() { c, _ := l.reserveControl(r, expiryControlFinal, context.Background()); controls <- c })
	}
	wg.Wait()
	close(controls)
	var winner *expiryLeaseControl
	count := 0
	for c := range controls {
		if c != nil {
			winner = c
			count++
		}
	}
	if count != 1 {
		t.Fatal("not exactly one controller", count)
	}
	first := errors.New("late deferred worker failure")
	_ = r.fail(first)
	if err := r.close(context.Background()); err == nil {
		t.Fatal("stale closer succeeded")
	}
	l.mu.Lock()
	if l.control != winner || winner.stage != expiryControlRetired || l.current != r || !l.failed {
		t.Fatal("worker/stale closer replaced winner")
	}
	l.mu.Unlock()
	work.complete()
	if err := l.drainControl(context.Background(), winner); err != nil {
		t.Fatal(err)
	}
	if winner.scenario || !l.failed {
		t.Fatal("failed cleanup repaired scenario")
	}
}
func TestExpiryHostC3PausedOutsideLockJoinAndErrorBeforeZero(t *testing.T) {
	l, r := expiryC3Model(t)
	work := expiryC3PendingOperation(l, r)
	r.operationMu.Lock()
	r.operationMu.Unlock() // actual old mutex-idle interval
	c, err := l.reserveControl(r, expiryControlFinal, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	drained := make(chan error, 1)
	go func() { close(started); drained <- l.drainControl(context.Background(), c) }()
	<-started
	expiryC3AssertOpen(t, r.window.done)
	select {
	case <-drained:
		t.Fatal("final did not wait for deferred join token")
	default:
	}
	// Both restart kinds refuse while the token survives; no teardown by losers.
	for _, kind := range []expiryControlKind{expiryControlRestartClean, expiryControlRestartUnclean} {
		if other, err := l.reserveControl(r, kind, context.Background()); err == nil || other != nil {
			t.Fatal("restart while join pending")
		}
	}
	first := errors.New("final publication after join")
	_ = r.fail(first)
	l.cleanup.record(first)
	expiryC3AssertOpen(t, r.window.done)
	work.complete()
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(l.cleanup.outcome(false), first) || !l.failed || l.control != c {
		t.Fatal("zero preceded sticky error")
	}
	// Duplicate discharge cannot decrement or signal twice.
	work.complete()
	if !r.window.completed || r.window.operation != nil || !l.failed {
		t.Fatal("duplicate completion corrupted window")
	}
}
func TestExpiryHostC3WindowNeverSignalsTransientIdleOrReopens(t *testing.T) {
	l, r := expiryC3Model(t)
	l.mu.Lock()
	l.signalWindowLocked(r.window)
	l.mu.Unlock()
	expiryC3AssertOpen(t, r.window.done)
	work := expiryC3PendingOperation(l, r)
	work.complete()
	expiryC3AssertOpen(t, r.window.done)
	l.mu.Lock()
	l.retireLocked(r)
	l.mu.Unlock()
	<-r.window.done
	old := r.window
	successor := newExpiryWorkWindow()
	if successor == old || successor.done == old.done || successor.retired {
		t.Fatal("successor reused retired window")
	}
	if _, _, err := r.admitMetadata(context.Background()); err == nil {
		t.Fatal("retired generation reopened")
	}
}
func TestExpiryHostC3MetadataActualCloseErrorBeforeRetiredCompletion(t *testing.T) {
	l, r := expiryC3Model(t)
	outer := expiryC3PendingOperation(l, r)
	metadata := expiryC3PendingMetadata(l, r)
	file, err := os.CreateTemp(t.TempDir(), "actual-metadata-")
	if err != nil {
		t.Fatal(err)
	}
	l.cleanup.acquiredFile(expiryRootMetadata)
	c, err := l.reserveControl(r, expiryControlFinal, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.moveControl(c, expiryControlValidating, expiryControlRetired); err != nil {
		t.Fatal(err)
	}
	if err := l.closeDescriptors(c); err == nil {
		t.Fatal("root closed under metadata/operation")
	}
	// Real externally broken resource; no callback/Close fault seam.
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	l.cleanup.closeFile(file, expiryRootMetadata)
	first := l.cleanup.outcome(false)
	if first == nil {
		t.Fatal("actual close error disappeared")
	}
	expiryC3AssertOpen(t, r.window.done)
	metadata.complete(first)
	expiryC3AssertOpen(t, r.window.done) // nested completion precedes outer
	outer.complete()
	<-r.window.done
	if !l.failed || !errors.Is(l.cleanup.outcome(true), first) {
		t.Fatal("metadata error not sticky before zero")
	}
}
func TestExpiryHostC3InvalidCurrentNoRootOpen(t *testing.T) {
	l, r := expiryC3Model(t)
	// Root pointer/path contents must never be consulted for an invalid current.
	l.controlRoot = filepath.Join(t.TempDir(), "does-not-exist")
	if err := r.checkCurrent(context.Background()); err == nil {
		t.Fatal("invalid current accepted")
	}
	for i := range l.cleanup.categories {
		if l.cleanup.categories[i].acquired.Load() != 0 {
			t.Fatal("invalid current acquired metadata")
		}
	}
	if r.window.metadataCount != 0 || r.window.metadata != nil {
		t.Fatal("failed admission left work")
	}
}
func TestExpiryHostC3ControllerMetadataSeparateFromExternalDrain(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "root")})
	if err != nil {
		t.Fatal(err)
	}
	c := l.control
	before := l.cleanup.categories[expiryRootMetadata].completed.Load()
	if err := l.checkPathsForControl(c); err != nil {
		t.Fatal("controller self-wait/check", err)
	}
	if c.metadata != nil || l.cleanup.categories[expiryRootMetadata].completed.Load() != before+1 {
		t.Fatal("actual controller metadata not complete")
	}
	if err := l.rollbackControl(c, errors.New("test disposal")); err == nil {
		t.Fatal("rollback repaired failure")
	}
	if c.stage != expiryControlTerminalClosed || l.cleanup.outcome(true) != nil {
		t.Fatal("quiescent root not actually closed")
	}
}
func TestExpiryHostC3TimeoutRetainsCanonicalControllerAndFDs(t *testing.T) {
	parent := h06ConstructorDirectory(t)
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "root")})
	if err != nil {
		t.Fatal(err)
	}
	c := l.control
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
	l.mu.Lock()
	c.candidate = r
	c.stage = expiryControlRollback
	l.mu.Unlock()
	work := expiryC3PendingOperation(l, r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.drainControl(ctx, c); err == nil {
		t.Fatal("timeout succeeded")
	}
	if l.control != c || c.stage != expiryControlTerminalResidual || l.lock == nil {
		t.Fatal("timeout lost canonical residual")
	}
	if _, _, err := expiryRootStat(l.lock); err != nil {
		t.Fatal("root FD closed under pending work", err)
	}
	for _, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
		if l.cleanup.categories[kind].attempted.Load() != 0 {
			t.Fatal("timeout attempted named close")
		}
	}
	work.complete()
	if err := l.closeDescriptors(c); err == nil {
		t.Fatal("later completion retried residual")
	}
	if err := l.rollbackControl(c, errors.New("retry")); err == nil {
		t.Fatal("rollback repaired residual")
	}
	if other, err := l.reserveControl(nil, expiryControlFresh, nil); err == nil || other != nil || l.control != c {
		t.Fatal("residual replaced")
	}
 // Strong canonical residual survives all assertions; NO test-only disposal
 // of unsafe roots, even after pending work eventually completes.
}
func TestExpiryHostC3ConstructorFailureStagesUseSameController(t *testing.T) {
	for _, stage := range []expiryControlStage{expiryControlReserved, expiryControlAcquiringRoot, expiryControlChecking, expiryControlConstructing, expiryControlValidating} {
		for acquired := 0; acquired <= 3; acquired++ {
			// Model each prefix of the real parent/data/lease acquisition set with
			// actual File.Close results. Not a launch or a synthetic success report.
			l := &expiryHostLease{phase: expiryLeaseFresh}
			_, l.cancel = context.WithCancel(context.Background())
			c, err := l.reserveControl(nil, expiryControlFresh, nil)
			if err != nil {
				t.Fatal(err)
			}
			c.stage = stage
			files := []**os.File{&l.parentDirectory, &l.directory, &l.lock}
			kinds := []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock}
			for i := 0; i < acquired && acquired < 3; i++ {
				f, err := os.CreateTemp(t.TempDir(), "owned-prefix-")
				if err != nil {
					t.Fatal(err)
				}
				*files[i] = f
				l.cleanup.acquiredFile(kinds[i])
			}
			if acquired == 3 {
				// Full-root positive requires original reservation metadata, not
				// arbitrary temp files with manufactured root identity.
				l.cancel()
				parent := h06ConstructorDirectory(t)
				l, err = reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "root")})
				if err != nil { t.Fatal(err) }
				c = l.control
				c.stage = stage // stage-control component, no live gate
			}
			cause := errors.New("constructor prefix failure")
			if err := l.rollbackControl(c, cause); !errors.Is(err, cause) {
				t.Fatal("constructor failure lost")
			}
			if l.control != c || c.stage != expiryControlTerminalClosed || !l.failed || c.firstErr != cause {
				t.Fatal("constructor bypassed canonical controller")
			}
			for i := 0; i < acquired; i++ {
				if l.cleanup.categories[kinds[i]].completed.Load() != 1 {
					t.Fatal("owned prefix not closed exactly once")
				}
			}
		}
	}
}
func TestExpiryHostC3FailedFinalReservationAndIndependentBudget(t *testing.T) {
	l, r := expiryC3Model(t)
	_ = r.fail(errors.New("prior failure"))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c, err := l.reserveControl(r, expiryControlFinal, canceled)
	if err != nil || c == nil || c.scenario || !l.failed {
		t.Fatal("failure/closed gate barred cleanup or repaired success")
	}
	if err := l.drainControl(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	cleanup, stop := expiryCleanupContext()
	defer stop()
	if cleanup.Err() != nil {
		t.Fatal("cleanup derived from canceled run/caller")
	}
	deadline, ok := cleanup.Deadline()
	if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
		t.Fatal("not fixed ten second independent budget")
	}
}

func expiryC3FunctionBody(t *testing.T, path, name string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return string(raw[set.Position(fn.Body.Pos()).Offset:set.Position(fn.Body.End()).Offset])
		}
	}
	t.Fatal("function missing", name)
	return ""
}
func TestExpiryHostC3SourceAdmissionCompleteLastAndScalarCutoffs(t *testing.T) {
	for _, name := range []string{"observeS1Refusal", "release", "observeMatchedAfterExpiry"} {
		body := expiryC3FunctionBody(t, "expiry_qualification_observation.go", name)
		admission := strings.Index(body, "r.admitOperation(")
		complete := strings.Index(body, "defer work.complete()")
		try := strings.Index(body, "r.operationMu.TryLock()")
		if admission < 0 || complete < admission || try < complete {
			t.Fatal("token not before TryLock/first defer", name)
		}
	}
	body := expiryC3FunctionBody(t, "expiry_qualification_observation.go", "observeS1Refusal")
	if strings.Index(body, "defer work.complete()") > strings.Index(body, "defer session.Close()") || !strings.Contains(body, "r.window.retired") || !strings.Contains(body, "r.lease.control != nil") {
		t.Fatal("negative complete-last/publication regression")
	}
	for _, name := range []string{"release", "observeMatchedAfterExpiry"} {
		if !strings.Contains(expiryC3FunctionBody(t, "expiry_qualification_observation.go", name), "!r.currentLocked(ctx)") {
			t.Fatal("retired success bit", name)
		}
	}
	reservation := expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "reserveControlLocked")
	cutoff := strings.Index(reservation, "kind != expiryControlFinal && !expiryOriginalDoneOpen(r)")
	retire := strings.Index(reservation, "r.controlledTeardown.Store(true)")
	if cutoff < 0 || retire < cutoff || !strings.Contains(reservation, "r.window.operation != nil") {
		t.Fatal("restart cutoff/work guard")
	}
	fail := expiryC3FunctionBody(t, "expiry_qualification.go", "fail")
	for _, forbidden := range []string{"invalidate(", ".Close(", "l.control =", "l.current =", "l.phase ="} {
		if strings.Contains(fail, forbidden) {
			t.Fatal("losing failure gained teardown/ownership", forbidden)
		}
	}
	current := expiryC3FunctionBody(t, "expiry_qualification_observation.go", "checkCurrent")
	if strings.Index(current, "r.admitMetadata(ctx)") > strings.Index(current, "l.checkPathSnapshot(snapshot)") || !strings.Contains(current, "work.complete(result)") {
		t.Fatal("metadata not admitted/completed")
	}
	admission := expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "admitMetadata")
	if strings.Index(admission, "!r.currentLocked(ctx)") > strings.Index(admission, "l.snapshotPathsLocked()") {
		t.Fatal("invalid current pointer use")
	}
	closeBody := expiryC3FunctionBody(t, "expiry_qualification.go", "closeDescriptors")
	if !strings.Contains(closeBody, "!l.quiescentLocked(c.predecessor)") || !strings.Contains(closeBody, "!l.quiescentLocked(c.candidate)") || strings.Index(closeBody, "expiryControlRootClosing") > strings.Index(closeBody, "l.cleanup.closeFile(") {
		t.Fatal("FD embargo missing")
	}
}
