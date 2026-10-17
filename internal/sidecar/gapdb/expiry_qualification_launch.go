//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"golang.org/x/sys/unix"
)

const expiryQualificationContract = "gapdb-sidecar-lifecycle/expiry-qualification-v1"
const expiryHeldBudget = 12 * time.Second
const expiryCleanupBudget = 5 * time.Second

// ExpiryTechnicalIdentity is a comparable copy, never admission or a resource.
type ExpiryTechnicalIdentity struct {
	InputRecordSHA256, TechnicalTupleSHA256, ModifiedInventorySHA256 string
	ClientModule, ClientVersion, ClientModuleH1, ClientGoModH1       string
	ExecutableSHA256                                                 string
	ExecutableBytes                                                  int64
}

type verifiedExpiryState struct {
	identity    ExpiryTechnicalIdentity
	child       *os.File
	gate        *expiryGateState
	transferred bool
	report      *expiryCleanupReport
}

type lifecycleExpiryState struct {
	gate   *expiryGateState
	report *expiryCleanupReport
	work   *expiryLifecycleWork
	claim  atomic.Pointer[expiryLifetimeClaim]
}

func (v verifiedExpiryState) cleanupReport() *expiryCleanupReport { return v.report }

// Fixed categories have no retained resource/history. A completed nil is distinct
// from an unattempted/in-flight close. Actual resource owners supply each outcome.
const (
	expirySource = iota
	expiryImage
	expiryParent
	expiryChild
	expiryTemporary
	expiryMetadata
	expiryCallback
	expiryLifecycleMetadata
	expirySupervisor
	expiryLifetime
	expiryCleanupCategories
)

type expiryCloseCounts struct {
	required  atomic.Uint64
	recorded  atomic.Uint64
	completed atomic.Uint64
}

type expiryCleanupFailure struct {
	category int
	err      error
}
type expiryCleanupReport struct {
	counts           [expiryCleanupCategories]expiryCloseCounts
	first            atomic.Pointer[expiryCleanupFailure]
	operationPending atomic.Bool // work may still acquire/close a local descriptor
	startupPending   atomic.Bool
	lifecyclePending atomic.Uint64
	teardownPending  atomic.Bool
	lifecycleBound   atomic.Bool
	lifecycleRetired atomic.Bool
	cleanupCutoff atomic.Pointer[time.Time] // original absolute cleanup envelope, never refreshed
}
func (r *expiryCleanupReport) ownCleanupCutoff(ctx context.Context, proposed time.Time) time.Time {
	proposed = intersectCleanupDeadline(ctx, proposed)
	if r == nil { return proposed }
	r.cleanupCutoff.CompareAndSwap(nil, &proposed)
	owned := *r.cleanupCutoff.Load()
	if proposed.Before(owned) { return proposed }
	return owned
}

func (r *expiryCleanupReport) fail(category int, err error) {
	if r != nil && err != nil {
		r.first.CompareAndSwap(nil, &expiryCleanupFailure{category: category, err: err})
	}
}

func (r *expiryCleanupReport) increment(counter *atomic.Uint64, category int) {
	for {
		before := counter.Load()
		if before == ^uint64(0) {
			r.fail(category, errors.New("cleanup counter overflow"))
			return
		}
		if counter.CompareAndSwap(before, before+1) {
			return
		}
	}
}

func (r *expiryCleanupReport) acquired(category int) {
	if r != nil {
		r.increment(&r.counts[category].required, category)
	}
}
func (r *expiryCleanupReport) closing(category int) {
	if r != nil {
		r.increment(&r.counts[category].recorded, category)
	}
}
func (r *expiryCleanupReport) closed(category int, err error) {
	if r != nil {
		r.fail(category, err)
		r.increment(&r.counts[category].completed, category)
	}
}

// These two hooks are called by the shared image helper at the actual first
// File.Close, before its pointer is discarded. Successful fixed verification
// reserves their required outcomes before any launch can succeed.
func (r *expiryCleanupReport) sourceClosed(err error) {
	r.closing(expirySource)
	r.closed(expirySource, err)
}
func (r *expiryCleanupReport) imageClosed(err error) {
	r.closing(expiryImage)
	r.closed(expiryImage, err)
}
func (r *expiryCleanupReport) sourceOpened()   { r.acquired(expirySource) }
func (r *expiryCleanupReport) imageOpened()    { r.acquired(expiryImage) }
func (r *expiryCleanupReport) metadataOpened() { r.acquired(expiryMetadata) }
func (r *expiryCleanupReport) metadataClosed(err error) {
	r.closing(expiryMetadata)
	r.closed(expiryMetadata, err)
}

func (r *expiryCleanupReport) firstError() error {
	if r == nil {
		return nil
	}
	if first := r.first.Load(); first != nil {
		return failure(FailureCleanup, "expiry_cleanup", fmt.Sprintf("first close failed in fixed category %d", first.category), first.err)
	}
	return nil
}

// gateTeardownError excludes intentionally retained lifecycle ownership. Sticky
// actual failures in ANY category remain fatal at this early boundary.
func (r *expiryCleanupReport) gateTeardownError() error {
	if r == nil {
		return nil
	}
	if err := r.firstError(); err != nil {
		return err
	}
	if r.operationPending.Load() {
		return failure(FailureCleanup, "expiry_cleanup", "gate operation not quiescent", nil)
	}
	for i := expirySource; i <= expiryCallback; i++ {
		c := &r.counts[i]
		if c.required.Load() != c.recorded.Load() || c.recorded.Load() != c.completed.Load() {
			return failure(FailureCleanup, "expiry_cleanup", "required gate first close or callback incomplete", nil)
		}
	}
	return r.firstError()
}

// Quiescence is separate from historical close failure: completed EIO does
// not authorize skipping safe attributed termination/reap/descriptor closure.
func (r *expiryCleanupReport) gateQuiescenceError() error {
	if r == nil { return nil }
	if r.operationPending.Load() { return failure(FailureCleanup, "expiry_cleanup", "gate operation not quiescent", nil) }
	for i := expirySource; i <= expiryCallback; i++ {
		c := &r.counts[i]
		if c.required.Load() != c.recorded.Load() || c.recorded.Load() != c.completed.Load() {
			return failure(FailureCleanup, "expiry_cleanup", "required gate first close or callback incomplete", nil)
		}
	}
	return nil
}

func (r *expiryCleanupReport) cleanupError() error {
	if r == nil {
		return nil
	}
	if err := r.firstError(); err != nil {
		return err
	}
	if r.operationPending.Load() || r.startupPending.Load() || r.lifecyclePending.Load() != 0 || r.teardownPending.Load() ||
		(r.lifecycleBound.Load() && !r.lifecycleRetired.Load()) {
		return failure(FailureCleanup, "expiry_cleanup", "owned work not quiescent", nil)
	}
	for i := range r.counts {
		c := &r.counts[i]
		if c.required.Load() != c.recorded.Load() || c.recorded.Load() != c.completed.Load() {
			return failure(FailureCleanup, "expiry_cleanup", "required first close or callback incomplete", nil)
		}
	}
	// Recheck the sticky publication after the counters. This is a cooperative
	// snapshot; final callers must first quiesce all owned work.
	if first := r.first.Load(); first != nil {
		return failure(FailureCleanup, "expiry_cleanup", "first close failed", first.err)
	}
	return nil
}

const (
	expiryCreated uint8 = iota
	expiryStarted
	expiryAwaitingH
	expiryHeld
	expiryReleasing
	expiryAcknowledged
	expiryClosed
	expiryFailed
)

type expiryGateState struct {
	operationMu sync.Mutex
	invalid     atomic.Bool
	phase       uint8         // operationMu; never needed by revocation
	parent      *net.UnixConn // immutable, closed only inside closeOnce
	closeOnce   sync.Once
	childOnce   sync.Once
	childDevice uint64 // immutable original socketpair child, captured before publication
	childInode  uint64
	lifecycle   atomic.Pointer[Lifecycle] // callback-visible association, initialized before Store
	identity    ExpiryTechnicalIdentity
	ctx         context.Context
	cancel      context.CancelFunc
	heldAt      time.Time
	report      *expiryCleanupReport
	watch       *expiryCancelWatch
}

// ExpiryQualification can only be made live by the closed fixed launch below.
// Its error-only operations expose neither a descriptor nor retained authority.
type ExpiryQualification struct{ state *expiryGateState }

// One E-only package-init notification, not a per-request resource. Empty
// handles must never suggest a live gate through a nil/open notification.
var expiryCanceledNotification <-chan struct{} = func() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

// Done exposes only the existing independent gate cancellation notification.
// Intentional R/A connection closure leaves it open. It grants no authority,
// cancellation function, context, descriptor or replacement for CheckHeld.
func (q *ExpiryQualification) Done() <-chan struct{} {
	if q == nil || q.state == nil || q.state.ctx == nil {
		return expiryCanceledNotification
	}
	done := q.state.ctx.Done()
	if done == nil {
		return expiryCanceledNotification
	}
	return done
}

func (q *ExpiryQualification) Identity() ExpiryTechnicalIdentity {
	if q == nil || q.state == nil {
		return ExpiryTechnicalIdentity{}
	}
	return q.state.identity
}

// CleanupError is only an atomic report snapshot. It performs no closure, I/O,
// callback, cancellation or lock acquisition and grants no liveness credential.
func (q *ExpiryQualification) CleanupError() error {
	if q == nil || q.state == nil || q.state.report == nil {
		return failure(FailureCleanup, "expiry_cleanup", "fixed cleanup report is absent", nil)
	}
	return q.state.report.cleanupError()
}

// RecordedCleanupError observes only the existing report's first failure at one
// atomic scalar load. Nil means no recorded failure observed at that load, not
// liveness, readiness, cleanup completeness or absence of in-flight/future work.
// It performs no closure, I/O, wait, cancellation, lock acquisition or callback;
// full post-rundown cleanup still requires CleanupError. Query/error-formatting
// costs remain owned and unqualified.
func (q *ExpiryQualification) RecordedCleanupError() error {
	if q == nil || q.state == nil || q.state.report == nil {
		return failure(FailureCleanup, "expiry_cleanup", "fixed cleanup report is absent", nil)
	}
	return q.state.report.firstError()
}

func StartExpiryQualification(ctx context.Context, config LifecycleConfig) (_ *Lifecycle, _ *ExpiryQualification, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	report := new(expiryCleanupReport)
	closeFailedVerified := func(v *VerifiedExecutable) error {
		report.ownCleanupCutoff(ctx, time.Now().Add(expiryCleanupBudget))
		return v.Close()
	}
	report.startupPending.Store(true)
	defer func() {
		report.startupPending.Store(false)
		if resultErr != nil {
			resultErr = withStartupCustody(resultErr, report.cleanupError())
		}
	}()
	if err := normalizeLifecycleConfigWithReport(&config, report); err != nil {
		return nil, nil, withStartupCustody(err, report.firstError())
	}
	verified, err := verifyExpiryQualificationExecutableWithReport(VerifyConfig{
		ExecutablePath: config.ExecutablePath, AdmissionRecordPath: config.AdmissionRecordPath,
		// The fixed verifier ignores Admission, including a nonzero caller value.
		Admission: config.Admission,
	}, report)
	if err != nil {
		return nil, nil, err
	}
	// The exact report pointer survives all early verified.Close calls.
	if verified.expiry.report != report || verified.expiry.identity == (ExpiryTechnicalIdentity{}) {
		err = failure(FailureAdmissionMismatch, "expiry_start", "fixed verified state is absent", nil)
		return nil, nil, withStartupCustody(err, closeFailedVerified(verified))
	}
	if err := verified.makeExpiryPair(ctx); err != nil {
		closeErr := closeFailedVerified(verified)
		return nil, nil, withStartupCustody(err, joinStartupCustody(closeErr, report.gateTeardownError()))
	}
	lifecycle, startErr := startVerified(ctx, config, verified)
	var closeErr error
	if startErr != nil { closeErr = closeFailedVerified(verified) } else { closeErr = verified.Close() }
	if startErr != nil {
		gateErr := (&ExpiryQualification{state: verified.expiry.gate}).Close()
		return nil, nil, withStartupCustody(startErr, joinStartupCustody(closeErr, joinStartupCustody(gateErr, report.gateTeardownError())))
	}
	// Required live parent/callback resources intentionally remain open. Early
	// source/image/child errors are sticky and cannot be repaired by later nil.
	if first := report.first.Load(); first != nil {
		q := &ExpiryQualification{state: verified.expiry.gate}
		stopCtx, cancel := context.WithDeadline(context.Background(), report.ownCleanupCutoff(ctx, time.Now().Add(expiryCleanupBudget)))
		defer cancel()
		return nil, nil, errors.Join(closeErr, lifecycle.Stop(stopCtx), q.Close(), report.gateTeardownError())
	}
	return lifecycle, &ExpiryQualification{state: verified.expiry.gate}, nil
}

func (v *VerifiedExecutable) makeExpiryPair(parent context.Context) error {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return failure(FailureStart, "expiry_pair", "fixed socket pair unavailable", err)
	}
	// Socketpair succeeded: both descriptors now have exactly one file owner.
	temporary := os.NewFile(uintptr(pair[0]), "expiry-gate-temporary")
	v.expiry.child = os.NewFile(uintptr(pair[1]), "expiry-gate-child")
	r := v.expiry.report
	r.acquired(expiryTemporary)
	r.acquired(expiryChild)
	gateCtx, cancel := context.WithCancel(parent)
	g := &expiryGateState{identity: v.expiry.identity, ctx: gateCtx, cancel: cancel, report: r}
	v.expiry.gate = g
	connection, connErr := net.FileConn(temporary)
	r.closing(expiryTemporary)
	r.closed(expiryTemporary, temporary.Close())
	if connErr != nil {
		g.revoke()
		return failure(FailureStart, "expiry_pair", "fixed gate connection cannot be owned", connErr)
	}
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		// This returned connection is owned even on the impossible type mismatch.
		r.acquired(expiryParent)
		r.closing(expiryParent)
		r.closed(expiryParent, connection.Close())
		g.revoke()
		return failure(FailureStart, "expiry_pair", "fixed gate is not Unix", nil)
	}
	g.parent = unixConnection
	r.acquired(expiryParent)
	// Retain the acquired child socket identity before its parent-side file is
	// closed after Start. A different socket, including a Unix stream socket,
	// must not become the attributed child's gate after command construction.
	childIdentity, childErr := identityOf(v.expiry.child)
	if childErr != nil || childIdentity.mode&unix.S_IFMT != unix.S_IFSOCK {
		g.revoke()
		return failure(FailureIdentityUnprovable, "expiry_pair", "original child socket identity unavailable", childErr)
	}
	g.childDevice, g.childInode = childIdentity.device, childIdentity.inode
	g.watch = g.watchContext(gateCtx)
	if err := parent.Err(); err != nil {
		g.revoke()
		return err
	}
	if first := r.first.Load(); first != nil {
		g.revoke()
		return failure(FailureCleanup, "expiry_pair", "first temporary close failed", first.err)
	}
	return nil
}

func (v *VerifiedExecutable) fixedCommand(arguments, environment []string) (*exec.Cmd, error) {
	if v.expiry.gate == nil {
		command := exec.Command("/proc/self/fd/3", arguments...)
		command.ExtraFiles = []*os.File{v.execution}
		command.Env = append([]string(nil), environment...)
		return command, nil
	}
	g := v.expiry.gate
	if g.invalid.Load() || g.ctx.Err() != nil || v.expiry.child == nil || g.identity != v.expiry.identity {
		g.revoke()
		return nil, failure(FailureIdentityUnprovable, "expiry_exec", "fixed gate custody unavailable", nil)
	}
	childIdentity, err := identityOf(v.expiry.child)
	if err != nil || childIdentity.mode&unix.S_IFMT != unix.S_IFSOCK ||
		childIdentity.device != g.childDevice || childIdentity.inode != g.childInode {
		g.revoke()
		return nil, failure(FailureIdentityUnprovable, "expiry_exec", "fixed child socket descriptor differs", err)
	}
	// Socket file mode alone also admits TCP and other socket domains. The
	// fixed gate requires the original AF_UNIX stream transport, not merely
	// a socket-shaped descriptor. These are separate scalar custody checks.
	childFD := int(v.expiry.child.Fd())
	domain, err := unix.GetsockoptInt(childFD, unix.SOL_SOCKET, unix.SO_DOMAIN)
	if err != nil || domain != unix.AF_UNIX {
		g.revoke()
		return nil, failure(FailureIdentityUnprovable, "expiry_exec", "fixed child socket descriptor differs", err)
	}
	kind, err := unix.GetsockoptInt(childFD, unix.SOL_SOCKET, unix.SO_TYPE)
	if err != nil || kind != unix.SOCK_STREAM {
		g.revoke()
		return nil, failure(FailureIdentityUnprovable, "expiry_exec", "fixed child socket descriptor differs", err)
	}
	// command() already performed the original full identity/seals/hash recheck.
	command := exec.Command("/proc/self/fd/4", arguments...)
	command.ExtraFiles = []*os.File{v.expiry.child, v.execution}
	command.Env = []string{"LANG=C", "TZ=UTC", "SPEC_KITTY_EXPIRY_GATE_FD=3"}
	return command, nil
}

func (v *VerifiedExecutable) expiryCommandStarted() {
	if v == nil || v.expiry.gate == nil {
		return
	}
	v.expiry.gate.closeChild(v.expiry.child)
}
func (g *expiryGateState) closeChild(child *os.File) {
	if child == nil {
		return
	}
	g.childOnce.Do(func() {
		g.report.closing(expiryChild)
		g.report.closed(expiryChild, child.Close())
	})
}
func (v *VerifiedExecutable) closeExpiryUnconsumed() error {
	if v == nil || v.expiry.gate == nil {
		return nil
	}
	g := v.expiry.gate
	g.closeChild(v.expiry.child)
	if !v.expiry.transferred {
		// Original image/source closes follow this hook. Do not fabricate an
		// incomplete final snapshot before those required attempts can occur.
		return errors.Join(g.teardown(), g.report.firstError())
	}
	return nil
}
func (v *VerifiedExecutable) expiryStartFailed() {
	if v != nil && v.expiry.gate != nil {
		v.expiry.gate.revoke()
	}
}
func (l *Lifecycle) initExpiry(v *VerifiedExecutable) {
	if v.expiry.gate == nil {
		return
	}
	l.expiry = lifecycleExpiryState{gate: v.expiry.gate, report: v.expiry.report, work: newExpiryLifecycleWork()}
	v.expiry.report.lifecycleBound.Store(true)
	v.expiry.gate.lifecycle.Store(l)
	// A callback that ran before association could not cut off this owner.
	// Recheck after Store: either it sees this owner or this check sees revoke.
	if v.expiry.gate.invalid.Load() || v.expiry.gate.ctx.Err() != nil { l.ownedRevoked.Store(true) }
	v.expiry.transferred = true
}
func (l *Lifecycle) revokeExpiry() {
	if l != nil && l.expiry.gate != nil {
		l.expiry.gate.revoke()
	}
}
func (l *Lifecycle) legacyEvidenceAllowed() bool  { return l == nil || l.expiry.gate == nil }
func (l *Lifecycle) ordinaryRestartAllowed() bool { return l.expiry.gate == nil }
func expiryAttachAllowed(recorded SidecarProvenance) bool {
	return recorded.Executable.ContractRevision != expiryQualificationContract
}

func (l *Lifecycle) expiryStarted(v *VerifiedExecutable) error {
	g := l.expiry.gate
	if g == nil {
		return nil
	}
	if g.lifecycle.Load() != l || g.identity != v.expiry.identity || l.command == nil || l.command.Process == nil ||
		l.command.Process.Pid != l.process.PID || l.provenance.Executable.ContractRevision != expiryQualificationContract {
		return failure(FailureIdentityUnprovable, "expiry_started", "original fixed child identity differs", nil)
	}
	// Bracket only the exact original child's fd4. Parent descriptor integer 4
	// is irrelevant. These proc magic links are followed intentionally.
	if err := l.checkExpiryStartedProcess(v.executed); err != nil {
		return err
	}
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/fd/4", l.process.PID), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return failure(FailureIdentityUnprovable, "expiry_started", "child image descriptor unavailable", err)
	}
	image := os.NewFile(uintptr(fd), "expiry-child-image-check")
	g.report.metadataOpened()
	imageErr := verifySealedExecution(image, v.executed)
	imageCloseErr := image.Close()
	g.report.metadataClosed(imageCloseErr)
	if imageErr != nil {
		return withStartupCustody(failure(FailureIdentityUnprovable, "expiry_started", "child fd4 image differs", imageErr), imageCloseErr)
	}
	if imageCloseErr != nil { return startupCleanupError("child fd4 metadata close failed", imageCloseErr) }
	var socketStat unix.Stat_t
	if err := unix.Stat(fmt.Sprintf("/proc/%d/fd/3", l.process.PID), &socketStat); err != nil ||
		socketStat.Mode&unix.S_IFMT != unix.S_IFSOCK || uint64(socketStat.Dev) != g.childDevice || socketStat.Ino != g.childInode {
		return failure(FailureIdentityUnprovable, "expiry_started", "child fd3 differs from original gate socket", err)
	}
	if err := l.checkExpiryStartedProcess(v.executed); err != nil {
		return err
	}
	if g.ctx.Err() != nil || g.invalid.Load() {
		return failure(FailureOwnership, "expiry_started", "fixed gate revoked", nil)
	}
	g.operationMu.Lock()
	defer g.operationMu.Unlock()
	if g.phase != expiryCreated {
		return failure(FailureOwnership, "expiry_started", "fixed gate phase differs", nil)
	}
	g.phase = expiryStarted
	return nil
}

// This additional startup bracket owns its proc descriptors directly. Shared
// default process helpers separately report their own actual acquisitions and
// closes; these extra checks do not stand in for those existing routes.
// This fixed original-child check is never used in the retained-proof interval.
func (l *Lifecycle) checkExpiryStartedProcess(expected ExecutedImageProvenance) error {
	fd, err := unix.Open(fmt.Sprintf("/proc/%d/stat", l.process.PID), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return failure(FailureIdentityUnprovable, "expiry_started", "original child start unavailable", err)
	}
	stat := os.NewFile(uintptr(fd), "expiry-child-start-check")
	l.expiry.report.metadataOpened()
	encoded, readErr := io.ReadAll(io.LimitReader(stat, (64<<10)+1))
	closeErr := stat.Close()
	l.expiry.report.metadataClosed(closeErr)
	if readErr != nil || closeErr != nil || len(encoded) > 64<<10 {
		return failure(FailureIdentityUnprovable, "expiry_started", "original child start cannot be checked", errors.Join(readErr, closeErr))
	}
	closeIndex := bytes.LastIndexByte(encoded, ')')
	if closeIndex < 0 || closeIndex+2 >= len(encoded) {
		return failure(FailureIdentityUnprovable, "expiry_started", "original child start malformed", nil)
	}
	fields := strings.Fields(string(encoded[closeIndex+2:]))
	if len(fields) <= 19 {
		return failure(FailureIdentityUnprovable, "expiry_started", "original child start incomplete", nil)
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 || ticks != l.process.StartTicks {
		return failure(FailureOwnership, "expiry_started", "original child start changed", err)
	}
	fd, err = unix.Open(fmt.Sprintf("/proc/%d/exe", l.process.PID), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return failure(FailureIdentityUnprovable, "expiry_started", "original child image unavailable", err)
	}
	image := os.NewFile(uintptr(fd), "expiry-original-image-check")
	l.expiry.report.metadataOpened()
	imageErr := verifySealedExecution(image, expected)
	closeErr = image.Close()
	l.expiry.report.metadataClosed(closeErr)
	if imageErr != nil || closeErr != nil || l.process.Device != expected.Device || l.process.Inode != expected.Inode ||
		l.process.Size != expected.Size || l.process.SHA256 != expected.SHA256 {
		return failure(FailureIdentityUnprovable, "expiry_started", "original child image changed", errors.Join(imageErr, closeErr))
	}
	return nil
}

func (g *expiryGateState) closeParent() {
	g.closeOnce.Do(func() {
		if g.parent != nil {
			g.report.closing(expiryParent)
			g.report.closed(expiryParent, g.parent.Close())
		}
	})
}
func (g *expiryGateState) revoke() {
	if g == nil {
		return
	}
	g.invalid.Store(true) // association's post-Store check cannot miss this cutoff
	if l := g.lifecycle.Load(); l != nil { l.ownedRevoked.Store(true) }
	if g.cancel != nil {
		g.cancel()
	}
	g.closeParent() // independent of operationMu, Lifecycle.mu and lifetime.mu
}

type expiryCancelWatch struct {
	stop     func() bool
	done     chan struct{}
	finished atomic.Bool
	report   *expiryCleanupReport
}

func (g *expiryGateState) watchContext(ctx context.Context) *expiryCancelWatch {
	w := &expiryCancelWatch{done: make(chan struct{}), report: g.report}
	g.report.acquired(expiryCallback)
	w.stop = context.AfterFunc(ctx, func() { defer w.finish(); g.revoke() })
	return w
}
func (w *expiryCancelWatch) finish() {
	if w.finished.CompareAndSwap(false, true) {
		w.report.closing(expiryCallback)
		close(w.done)
		w.report.closed(expiryCallback, nil)
	}
}
func (w *expiryCancelWatch) stopAndWait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if w.stop() {
		w.finish()
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return failure(FailureCleanup, "expiry_cleanup", "cancellation callback incomplete", ctx.Err())
	}
}

func (q *ExpiryQualification) gate() (*expiryGateState, error) {
	if q == nil || q.state == nil {
		return nil, failure(FailureOwnership, "expiry_gate", "fixed gate absent", nil)
	}
	return q.state, nil
}
func (g *expiryGateState) begin(ctx context.Context) error {
	if !g.operationMu.TryLock() {
		g.revoke()
		return failure(FailureOwnership, "expiry_gate", "overlapping fixed gate operation", nil)
	}
	if g.report != nil && !g.report.operationPending.CompareAndSwap(false, true) {
		g.operationMu.Unlock()
		g.revoke()
		return failure(FailureOwnership, "expiry_gate", "overlapping fixed operation cleanup", nil)
	}
	if err := g.contextLive(ctx); err != nil {
		g.operationFinished()
		g.operationMu.Unlock()
		g.revoke()
		return err
	}
	return nil
}
func (g *expiryGateState) operationFinished() {
	if g.report != nil {
		g.report.operationPending.Store(false)
	}
}
func (g *expiryGateState) contextLive(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil || g.ctx == nil || g.ctx.Err() != nil || g.invalid.Load() {
		return failure(FailureOwnership, "expiry_gate", "independent context or gate ended", nil)
	}
	return nil
}
func (g *expiryGateState) heldLive(ctx context.Context) error {
	if err := g.contextLive(ctx); err != nil {
		return err
	}
	if g.heldAt.IsZero() || !time.Now().Before(g.heldAt.Add(expiryHeldBudget)) {
		return failure(FailureOwnership, "expiry_gate", "local held budget ended", nil)
	}
	if err := g.checkOriginalLifecycle(ctx); err != nil {
		return err
	}
	if !time.Now().Before(g.heldAt.Add(expiryHeldBudget)) {
		return failure(FailureOwnership, "expiry_gate", "local held budget ended during closure", nil)
	}
	return g.contextLive(ctx)
}

// checkOriginalLifecycle is the nonlocking gate-operation helper. It takes only
// the original lifecycle lock, never invokes CheckHeld recursively and never
// performs a retained RPC or full image hash.
func (g *expiryGateState) checkOriginalLifecycle(ctx context.Context) error {
	l := g.lifecycle.Load()
	if l == nil {
		return failure(FailureOwnership, "expiry_gate", "original lifecycle absent", nil)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := g.contextLive(ctx); err != nil {
		return err
	}
	if l.expiry.gate != g || l.expiry.report != g.report || l.ownedRevoked.Load() || l.stopped || l.pidfd < 0 ||
		l.command == nil || l.command.Process == nil || l.command.Process.Pid != l.process.PID ||
		l.provenance.Process != l.process || l.provenance.Executable.ContractRevision != expiryQualificationContract ||
		l.provenance.ExecutedImage.SHA256 != g.identity.ExecutableSHA256 || l.provenance.ExecutedImage.Size != g.identity.ExecutableBytes ||
		!l.provenance.Readiness.Admitted || l.ownedEndpoint == "" || l.ownedEndpoint != l.provenance.ConfiguredSocket ||
		l.ownedEndpoint != l.provenance.ConnectedSocket || l.ownedBootstrap.DatabaseID == "" ||
		l.ownedBootstrap.DatabaseID != l.provenance.DatabaseID || l.ownedBootstrap.OwnerPID != l.process.PID ||
		l.ownedBootstrap.OwnerStartedAt != l.provenance.Observation.Status.OwnerStartedAt || l.ownedBootstrap.Lifecycle != gapdbapi.LifecycleReady {
		return failure(FailureOwnership, "expiry_gate", "original lifecycle scalars differ", nil)
	}
	fd, err := openDirectoryNoFollow(l.provenance.RuntimeDirectory)
	if err != nil {
		return failure(FailureOwnership, "expiry_gate", "original runtime unavailable", err)
	}
	file := os.NewFile(uintptr(fd), "expiry-runtime-check")
	g.report.metadataOpened()
	closed := false
	defer func() {
		if !closed {
			g.report.metadataClosed(file.Close())
		}
	}()
	identity, err := identityOf(file)
	if err != nil || identity.device != l.runtimeID.device || identity.inode != l.runtimeID.inode ||
		identity.mode != l.runtimeID.mode || identity.uid != l.runtimeID.uid || identity.gid != l.runtimeID.gid {
		return failure(FailureOwnership, "expiry_gate", "original runtime metadata differs", err)
	}
	var socket unix.Stat_t
	if err := unix.Fstatat(fd, "gapdb.sock", &socket, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		socket.Mode&unix.S_IFMT != unix.S_IFSOCK || uint64(socket.Dev) != l.socketID.device || socket.Ino != l.socketID.inode {
		return failure(FailureOwnership, "expiry_gate", "original socket metadata differs", err)
	}
	if err := verifySocketPathIdentity(l.ownedEndpoint, l.socketID); err != nil {
		return err
	}
	lifetime := l.retained.Load()
	if lifetime == nil {
		return failure(FailureOwnership, "expiry_gate", "original retained lifetime absent", nil)
	}
	if err := lifetime.CheckExpiryQualificationLive(ctx); err != nil {
		return err
	}
	closeErr := file.Close()
	closed = true
	g.report.metadataClosed(closeErr)
	if closeErr != nil {
		return failure(FailureCleanup, "expiry_gate", "local metadata descriptor close failed", closeErr)
	}
	if err := g.contextLive(ctx); err != nil {
		return err
	}
	if l.ownedRevoked.Load() || l.stopped {
		return failure(FailureOwnership, "expiry_gate", "original lifecycle revoked", nil)
	}
	return nil
}

func (q *ExpiryQualification) CheckHeld(ctx context.Context) (resultErr error) {
	g, err := q.gate()
	if err != nil {
		return err
	}
	if err := g.begin(ctx); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			g.phase = expiryFailed
			g.revoke()
		}
		g.operationMu.Unlock()
		g.operationFinished()
	}()
	if g.phase != expiryHeld {
		return failure(FailureOwnership, "expiry_gate", "gate is not held", nil)
	}
	return g.heldLive(ctx)
}

func expiryDeadline(ctx context.Context, deadline time.Time) time.Time {
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

// noPending rejects already queued out-of-order or repeated bytes without
// consuming another protocol byte. No SO_PEERCRED claim authenticates the pair.
func (g *expiryGateState) noPending() error {
	raw, err := g.parent.SyscallConn()
	if err != nil {
		return err
	}
	var readErr error
	var n int
	if err := raw.Control(func(fd uintptr) {
		var b [1]byte
		n, _, readErr = unix.Recvfrom(int(fd), b[:], unix.MSG_PEEK|unix.MSG_DONTWAIT)
	}); err != nil {
		return err
	}
	if errors.Is(readErr, unix.EAGAIN) || errors.Is(readErr, unix.EWOULDBLOCK) {
		return nil
	}
	if readErr != nil {
		return readErr
	}
	if n == 0 {
		return io.EOF
	}
	return failure(FailureProtocol, "expiry_gate", "out-of-order gate byte", nil)
}

// finishIO releases operationMu BEFORE stopping/waiting on callback completion.
func (g *expiryGateState) finishIO(watch *expiryCancelWatch, resultErr *error) {
	if *resultErr != nil {
		g.phase = expiryFailed
		g.revoke()
	}
	g.operationMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), expiryCleanupBudget)
	defer cancel()
	if err := watch.stopAndWait(ctx); err != nil {
		g.revoke()
		*resultErr = errors.Join(*resultErr, err)
	}
	g.operationFinished()
}

func (q *ExpiryQualification) WaitHeld(ctx context.Context) (resultErr error) {
	g, err := q.gate()
	if err != nil {
		return err
	}
	if err := g.begin(ctx); err != nil {
		return err
	}
	watch := g.watchContext(ctx)
	defer g.finishIO(watch, &resultErr)
	if g.phase != expiryStarted {
		return failure(FailureOwnership, "expiry_gate", "H is out of phase", nil)
	}
	g.phase = expiryAwaitingH
	deadline := expiryDeadline(g.ctx, expiryDeadline(ctx, time.Now().Add(30*time.Second)))
	if err := g.parent.SetDeadline(deadline); err != nil {
		return err
	}
	var b [1]byte
	if _, err := io.ReadFull(g.parent, b[:]); err != nil {
		return failure(FailureProtocol, "expiry_gate", "H not received", err)
	}
	if b[0] != 'H' {
		return failure(FailureProtocol, "expiry_gate", "invalid H byte", nil)
	}
	g.heldAt = time.Now() // the independent local 12s budget starts at receipt
	g.phase = expiryHeld
	if err := g.noPending(); err != nil {
		return err
	}
	return g.heldLive(ctx)
}

func (q *ExpiryQualification) Release(ctx context.Context) (resultErr error) {
	g, err := q.gate()
	if err != nil {
		return err
	}
	if err := g.begin(ctx); err != nil {
		return err
	}
	watch := g.watchContext(ctx)
	defer g.finishIO(watch, &resultErr)
	if g.phase != expiryHeld {
		return failure(FailureOwnership, "expiry_gate", "R is out of phase", nil)
	}
	if err := g.heldLive(ctx); err != nil {
		return err
	}
	if err := g.noPending(); err != nil {
		return err
	}
	g.phase = expiryReleasing
	deadline := expiryDeadline(g.ctx, expiryDeadline(ctx, g.heldAt.Add(expiryHeldBudget)))
	if err := g.parent.SetDeadline(deadline); err != nil {
		return err
	}
	if n, err := g.parent.Write([]byte{'R'}); err != nil || n != 1 {
		return failure(FailureProtocol, "expiry_gate", "R not delivered", err)
	}
	var b [1]byte
	if _, err := io.ReadFull(g.parent, b[:]); err != nil {
		return failure(FailureProtocol, "expiry_gate", "A not received", err)
	}
	if b[0] != 'A' {
		return failure(FailureProtocol, "expiry_gate", "invalid A byte", nil)
	}
	if err := g.noPending(); err != nil {
		return err
	}
	if err := g.heldLive(ctx); err != nil {
		return err
	}
	g.closeParent() // intentional release does NOT cancel gate/run contexts
	if err := g.contextLive(ctx); err != nil {
		return err
	}
	g.phase = expiryAcknowledged
	return nil
}

func (q *ExpiryQualification) Close() error {
	if q == nil || q.state == nil {
		return nil
	}
	return errors.Join(q.state.teardown(), q.state.report.gateTeardownError())
}

func (g *expiryGateState) teardown() error {
	outer := context.Background()
	cutoff := g.report.ownCleanupCutoff(outer, time.Now().Add(expiryCleanupBudget))
	ctx, cancel := context.WithDeadline(outer, cutoff)
	defer cancel()
	return g.teardownBefore(ctx)
}
func (g *expiryGateState) teardownBefore(ctx context.Context) error {
	cutoff := g.report.ownCleanupCutoff(ctx, time.Now().Add(expiryCleanupBudget))
	ctx, cancel := context.WithDeadline(ctx, cutoff)
	defer cancel()
	g.revoke() // actual close/Once can block: no hard-wall containment claim
	watchErr := g.watch.stopAndWait(ctx)
	// Concurrent operation watches must finish outside every state lock. The
	// fixed counters retain unknown results if a callback does not quiesce.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for g.report != nil && (g.report.operationPending.Load() ||
		g.report.counts[expiryCallback].required.Load() != g.report.counts[expiryCallback].completed.Load()) {
		select {
		case <-ctx.Done():
			return withStartupCustody(failure(FailureCleanup, "expiry_cleanup", "gate rundown cutoff elapsed", ctx.Err()), watchErr)
		case <-ticker.C:
		}
	}
	if g.operationMu.TryLock() {
		g.phase = expiryClosed
		g.operationMu.Unlock()
	}
	// Only callback/operation rundown here. Unconsumed Verified.Close invokes
	// this BEFORE its source/image defers; those are not yet missing outcomes.
	return watchErr
}

// CrashExpiryQualification requires the original marked lifecycle/control
// identity, not a live gate: controlled restart revokes that gate FIRST. Only
// the existing pidfd-only, exact-owned stop implementation may signal.
func (l *Lifecycle) CrashExpiryQualification(ctx context.Context) error {
	if l == nil || l.expiry.gate == nil || l.expiry.gate.lifecycle.Load() != l ||
		l.expiry.report == nil || l.expiry.gate.report != l.expiry.report {
		return failure(FailureOwnership, "expiry_crash", "original marked control absent", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return l.stopOwnedUnclean(ctx)
}
