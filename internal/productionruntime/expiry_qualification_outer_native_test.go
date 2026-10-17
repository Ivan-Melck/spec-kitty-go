//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"golang.org/x/sys/unix"
)

// PREPARED ONLY: independent source review and fresh native evidence are required.
// This does not rerun the accepted native13 or EOF/daemon-timeout campaigns.
const (
	expiryOuterSourceSHA256     = "8d9ae57f6f0687aebe1eabe0c75d999e2fa513e4d58789010773537427f4e0c1"
	expiryOuterFunction         = "github.com/spec-kitty/go-kitty3/internal/productionruntime.(*expiryHostRun).observeS1Refusal"
	expiryOuterLine             = 195
	expiryOuterHeldSourceSHA256 = "ac1c630c6a964c7b6cf6abd7f0a5f29872b31aefbd336d88befca8ef43b99bad"
)

type expiryOuterSourceGuard struct {
	file string
	line int
}

// Pin the WHOLE observation source, then resolve the direct parent.Err call in
// the top-level statement immediately following successful canonical Consume.
// No occurrence count, elapsed-time fallback, product hook, or inferred refusal.
func expiryOuterResolveSource(file string, source []byte) (expiryOuterSourceGuard, error) {
	if fmt.Sprintf("%x", sha256.Sum256(source)) != expiryOuterSourceSHA256 {
		return expiryOuterSourceGuard{}, errors.New("outer frontier observation source digest drift")
	}
	fs := token.NewFileSet()
	parsed, err := parser.ParseFile(fs, file, source, 0)
	if err != nil {
		return expiryOuterSourceGuard{}, err
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "observeS1Refusal" || fn.Recv == nil || len(fn.Recv.List) != 1 || fn.Body == nil {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		receiver, ok := star.X.(*ast.Ident)
		if !ok || receiver.Name != "expiryHostRun" {
			continue
		}
		for i, stmt := range fn.Body.List {
			consume, ok := stmt.(*ast.IfStmt)
			if !ok || i+1 >= len(fn.Body.List) {
				continue
			}
			assignment, ok := consume.Init.(*ast.AssignStmt)
			if !ok || len(assignment.Rhs) != 1 {
				continue
			}
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok || !expiryOuterSelector(call.Fun, "session", "ConsumeExpiryQualificationRefusal") {
				continue
			}
			outer, ok := fn.Body.List[i+1].(*ast.IfStmt)
			if !ok || outer.Init != nil {
				return expiryOuterSourceGuard{}, errors.New("outer frontier is not the next direct check")
			}
			// The leftmost operand must itself be parent.Err() != nil.
			condition := outer.Cond
			for {
				binary, ok := condition.(*ast.BinaryExpr)
				if !ok || binary.Op != token.LOR {
					break
				}
				condition = binary.X
			}
			comparison, ok := condition.(*ast.BinaryExpr)
			if !ok || comparison.Op != token.NEQ {
				return expiryOuterSourceGuard{}, errors.New("outer frontier comparison drift")
			}
			nilValue, ok := comparison.Y.(*ast.Ident)
			if !ok || nilValue.Name != "nil" {
				return expiryOuterSourceGuard{}, errors.New("outer frontier nil operand drift")
			}
			parentErr, ok := comparison.X.(*ast.CallExpr)
			if !ok || len(parentErr.Args) != 0 || !expiryOuterSelector(parentErr.Fun, "parent", "Err") {
				return expiryOuterSourceGuard{}, errors.New("outer frontier direct caller drift")
			}
			line := fs.Position(parentErr.Pos()).Line
			if line != expiryOuterLine {
				return expiryOuterSourceGuard{}, errors.New("outer frontier line drift")
			}
			return expiryOuterSourceGuard{file: filepath.Clean(file), line: line}, nil
		}
	}
	return expiryOuterSourceGuard{}, errors.New("post-Consume outer frontier missing")
}

func expiryOuterSelector(expr ast.Expr, object, method string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != method {
		return false
	}
	id, ok := selector.X.(*ast.Ident)
	return ok && id.Name == object
}

func expiryOuterPinnedSource(t *testing.T) expiryOuterSourceGuard {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve outer test source")
	}
	file := filepath.Join(filepath.Dir(self), "expiry_qualification_observation.go")
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := expiryOuterResolveSource(file, source)
	if err != nil {
		t.Fatal(err)
	}
	return guard
}

// Derive the EXISTING sidecar held budget from its pinned declaration, not a
// new test SLO. Fail closed on drift instead of silently extending the window.
func expiryOuterResolveHeldBudget(file string, source []byte) (time.Duration, error) {
	if fmt.Sprintf("%x", sha256.Sum256(source)) != expiryOuterHeldSourceSHA256 {
		return 0, errors.New("outer frontier sidecar held-budget source digest drift")
	}
	fs := token.NewFileSet()
	parsed, err := parser.ParseFile(fs, file, source, 0)
	if err != nil {
		return 0, err
	}
	for _, decl := range parsed.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}
		for _, spec := range group.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "expiryHeldBudget" || len(value.Values) != 1 {
				continue
			}
			product, ok := value.Values[0].(*ast.BinaryExpr)
			if !ok || product.Op != token.MUL || !expiryOuterSelector(product.Y, "time", "Second") || fs.Position(value.Pos()).Line != 25 {
				return 0, errors.New("sidecar held-budget declaration drift")
			}
			literal, ok := product.X.(*ast.BasicLit)
			if !ok || literal.Kind != token.INT {
				return 0, errors.New("sidecar held-budget literal drift")
			}
			seconds, err := strconv.ParseInt(literal.Value, 10, 32)
			if err != nil || seconds <= 0 {
				return 0, errors.New("invalid sidecar held budget")
			}
			return time.Duration(seconds) * time.Second, nil
		}
	}
	return 0, errors.New("sidecar held-budget declaration missing")
}

func expiryOuterPinnedHeldBudget(t *testing.T, guard expiryOuterSourceGuard) time.Duration {
	t.Helper()
	file := filepath.Join(filepath.Dir(guard.file), "..", "sidecar", "gapdb", "expiry_qualification_launch.go")
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := expiryOuterResolveHeldBudget(file, source)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

// Embedding forwards the REAL Deadline/Done/Value without modification. Err
// returns only the underlying REAL Err. Only the exact direct production caller
// can pause; all other context calls are transparent. Cancellation does NOT
// release the barrier: the test explicitly resumes after its action completes.
// This wrapper is supplied solely to the new observeS1Refusal worker.
type expiryOuterFrontierContext struct {
	context.Context
	guard           expiryOuterSourceGuard
	reached, resume chan struct{}
	once            sync.Once
}

func (c *expiryOuterFrontierContext) Err() error {
	pc, file, line, ok := runtime.Caller(1)
	if ok && filepath.Clean(file) == c.guard.file && line == c.guard.line {
		if fn := runtime.FuncForPC(pc); fn != nil && fn.Name() == expiryOuterFunction {
			c.once.Do(func() {
				close(c.reached)
				<-c.resume
			})
		}
	}
	return c.Context.Err()
}

// Nonprovider checks only. Actual arrival is deliberately NOT simulated here;
// each native case (including unchanged) must prove real arrival.
func TestExpiryHostOuterFrontierSourceAndContext(t *testing.T) {
	guard := expiryOuterPinnedSource(t)
	if budget := expiryOuterPinnedHeldBudget(t, guard); budget <= 0 {
		t.Fatal("existing sidecar held budget did not resolve")
	}
	heldFile := filepath.Join(filepath.Dir(guard.file), "..", "sidecar", "gapdb", "expiry_qualification_launch.go")
	heldSource, err := os.ReadFile(heldFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expiryOuterResolveHeldBudget(heldFile, append(append([]byte(nil), heldSource...), '\n')); err == nil {
		t.Fatal("held-budget source drift accepted")
	}
	source, err := os.ReadFile(guard.file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expiryOuterResolveSource(guard.file, append(append([]byte(nil), source...), '\n')); err == nil {
		t.Fatal("source drift accepted")
	}
	type key struct{}
	parent, cancel := context.WithTimeout(context.WithValue(context.Background(), key{}, "real-value"), time.Minute)
	defer cancel()
	wrapped := &expiryOuterFrontierContext{Context: parent, guard: guard, reached: make(chan struct{}), resume: make(chan struct{})}
	deadline, hasDeadline := wrapped.Deadline()
	wantDeadline, wantHasDeadline := parent.Deadline()
	if deadline != wantDeadline || hasDeadline != wantHasDeadline || wrapped.Done() != parent.Done() || wrapped.Value(key{}) != parent.Value(key{}) || wrapped.Err() != parent.Err() {
		t.Fatal("wrapper changed real context semantics")
	}
	cancel()
	if wrapped.Err() != parent.Err() || !errors.Is(wrapped.Err(), context.Canceled) {
		t.Fatal("wrapper did not return actual cancellation")
	}
	select {
	case <-wrapped.reached:
		t.Fatal("off-frontier Err call paused")
	default:
	}
}

func TestExpiryHostNativeOuterTerminalControls(t *testing.T) {
	for _, mode := range []string{"unchanged", "directHostClose", "directLifecycleStop", "callerCancel", "endpointSubstitution"} {
		t.Run(mode, func(t *testing.T) {
			guard := expiryOuterPinnedSource(t)
			heldBudget := expiryOuterPinnedHeldBudget(t, guard)
			config := expiryNativeConfig(t, &expiryActualACKObserver{})
			// Startup/root caller is NOT the independently cancelable operation caller.
			h, run, err := startExpiryHostQualification(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			finalized := false
			finalize := func() {
				if !finalized {
					finalized = true // never retry teardown, including after a failed close
					expiryOuterFinalRundown(t, run, mode == "unchanged")
				}
			}
			t.Cleanup(finalize)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			// Exact initial/private-only prefix from expiryNativeTTLClosure. Do
			// NOT call that helper: it already consumes and releases its session.
			proof, err := h.VerifyRetainedRecords(ctx, nil)
			if err != nil || proof.MatchedPrivateRecords() != 6 || proof.MatchedPublicationRecords() != 0 || proof.MatchedRecords() != 6 {
				t.Fatal("actual initial private-only baseline closure differs", err)
			}
			owner := run.owner
			expires := time.Now().UTC().Add(2 * time.Second)
			inserted, err := owner.adapter.ConditionalMutate(ctx, "expiry-host-extra-insert", storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/expiry-evidence/extra", Value: []byte("expiry-host-extra-v1"), Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, ExpiresAt: &expires}, storegapdb.AckDurable)
			if err != nil || inserted.Revision == 0 || inserted.Ack != storegapdb.AckDurable || inserted.DurableThrough < inserted.Revision || inserted.MutationCount != 1 {
				t.Fatalf("actual future TTL insertion: %v %+v", err, inserted)
			}
			if err := h.refreshProjection(ctx); err != nil {
				t.Fatal(err)
			}
			// Monotonic conservative start is BEFORE WaitHeld; the real gate
			// starts its own budget only upon H receipt. This is never later.
			heldStart := time.Now()
			cutoff := heldStart.Add(heldBudget)
			if err := run.gate.WaitHeld(ctx); err != nil {
				t.Fatal("actual H", err)
			}
			frontierWait, stopFrontierWait := context.WithDeadline(context.Background(), cutoff)
			defer stopFrontierWait()
			t.Logf("conservative held cutoff: existing_budget=%s sidecar_source_sha256=%s start_before_WaitHeld=true", heldBudget, expiryOuterHeldSourceSHA256)
			attribution := h.projection.Attribution()
			before, err := owner.adapter.Inspect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := owner.adapter.Get(ctx, "spk/expiry-evidence/extra"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				t.Fatal("held expired extra not authenticated not-found", err)
			}
			after, err := owner.adapter.Inspect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			p := owner.lifecycle.Provenance()
			for _, status := range []storegapdb.Inspection{before, after} {
				if !status.Healthy || status.Lifecycle != "ready" || status.DatabaseID != string(h.databaseID) || status.OwnerPID != p.Process.PID || status.OwnerStartedAt != p.Observation.Status.OwnerStartedAt || status.CurrentRevision != inserted.Revision || status.DurableThrough != status.CurrentRevision || attribution.Revision != status.CurrentRevision {
					t.Fatal("held coordinate/projection disagreement")
				}
			}
			public, publicErr := h.VerifyRetainedRecords(ctx, nil)
			refusal, sanitized := publicErr.(*ProgressError)
			if !reflect.ValueOf(public).IsZero() || !sanitized || refusal == nil || refusal.Kind != ProgressIncomplete || refusal.Field != "retained_records" || publicErr.Error() != proofIncomplete("retained_records").Error() {
				t.Fatalf("held public refusal not exact zero/sanitized retained_records: %T %v", publicErr, publicErr)
			}

			wrapped := &expiryOuterFrontierContext{Context: ctx, guard: guard, reached: make(chan struct{}), resume: make(chan struct{})}
			var resumeOnce sync.Once
			var resumedAt, joinedAt time.Time
			resume := func() {
				resumeOnce.Do(func() {
					resumedAt = time.Now()
					close(wrapped.resume)
				})
			}
			done := make(chan struct{})
			var observedErr error // published by close(done), never read before it
			go func() {
				defer close(done)
				observedErr = run.observeS1Refusal(wrapped, nil)
			}()
			joined := false
			var workerErr error
			join := func() {
				resume()
				if !joined {
					<-done
					workerErr = observedErr
					joinedAt = time.Now()
					joined = true
					t.Logf("workerjoined: mode=%s error=%v before_cutoff=%t remaining=%s", mode, workerErr, joinedAt.Before(cutoff), cutoff.Sub(joinedAt))
				}
			}
			// Defer runs before t.Cleanup(finalize), even on fatal/early return.
			// Never start canonical final cleanup with a paused/live worker.
			defer join()
			select {
			case <-wrapped.reached:
				t.Logf("frontierreached: mode=%s function=%s source=%s line=%d sha256=%s", mode, expiryOuterFunction, guard.file, guard.line, expiryOuterSourceSHA256)
			case <-done:
				workerErr = observedErr
				joined = true
				t.Logf("workerjoined: mode=%s error=%v", mode, workerErr)
				t.Fatal("worker returned without reaching real post-Consume frontier")
			case <-frontierWait.Done():
				t.Fatal("real post-Consume frontier missed existing held cutoff", frontierWait.Err())
			case <-ctx.Done():
				t.Fatal("real post-Consume frontier not reached", ctx.Err())
			}
			arrivedAt := time.Now()
			if !arrivedAt.Before(cutoff) || frontierWait.Err() != nil || ctx.Err() != nil {
				t.Fatal("frontier arrived after existing held cutoff or caller deadline", ctx.Err())
			}
			run.lease.mu.Lock()
			premature := run.negativeClosed || run.released || run.matchedAfterExpiry
			run.lease.mu.Unlock()
			if premature {
				t.Fatal("scenario bits published before outer frontier")
			}

			// REAL outer entry points only, with independent cleanup context.
			// No run.close/r.fail/controlledTeardown/manual scenario mutation.
			var actionErr error
			var endpoint *expiryOuterEndpointFixture
			var restoreEndpoint func() error
			switch mode {
			case "directHostClose", "directLifecycleStop":
				cleanup, cleanupCancel := context.WithDeadline(context.Background(), cutoff)
				if mode == "directHostClose" {
					actionErr = h.Close(cleanup)
					t.Logf("FIRST direct Host.Close return: %v", actionErr)
				} else {
					actionErr = owner.lifecycle.Stop(cleanup)
					t.Logf("FIRST direct Lifecycle.Stop return: %v", actionErr)
				}
				actionErr = errors.Join(actionErr, cleanup.Err())
				cleanupCancel()
			case "endpointSubstitution":
				endpoint = &expiryOuterEndpointFixture{directoryFD: -1}
				reported := false
				restoreEndpoint = func() error {
					err := endpoint.restore()
					if !reported {
						reported = true
						t.Logf("endpoint fixture restoration: original=%s parked=%s restored=%t FIRST listener.Close=%v unlink=%v rename=%v directory.Close=%v outcome=%v; residual paths preserved on failure",
							endpoint.original, endpoint.parked, endpoint.restored, endpoint.listenerCloseErr, endpoint.unlinkErr, endpoint.renameErr, endpoint.directoryCloseErr, err)
					}
					return err
				}
				// Registered AFTER the canonical finalizer: LIFO restores before
				// first teardown. defer join runs BEFORE either cleanup callback.
				// No deferred restorer may run ahead of the worker join.
				t.Cleanup(func() {
					if err := restoreEndpoint(); err != nil {
						t.Errorf("endpoint fixture residual; no PASS and no restoration retry: %v", err)
					}
				})
				actionErr = endpoint.substitute(ctx, config.Sidecar.RuntimeRoot, p)
				t.Logf("actual endpoint substitution: original=%s parked=%s original_identity=%+v synthetic_identity=%+v PID=%d start_ticks=%d error=%v; no Accept/respond/RPC/signals",
					endpoint.original, endpoint.parked, endpoint.originalID, endpoint.syntheticID, p.Process.PID, p.Process.StartTicks, actionErr)
			case "callerCancel":
				cancel()
				select {
				case <-ctx.Done():
				default:
					t.Fatal("actual operation cancellation did not complete")
				}
				if !errors.Is(ctx.Err(), context.Canceled) || run.lease.parent.Err() != nil {
					t.Fatal("cancellation did not remain independent of startup/root caller")
				}
			}
			actionCompletedAt := time.Now()
			t.Logf("actioncompleted: mode=%s error=%v before_cutoff=%t remaining=%s (before resume)", mode, actionErr, actionCompletedAt.Before(cutoff), cutoff.Sub(actionCompletedAt))
			// On any missed cutoff still explicitly resume and JOIN, but never
			// credit emergency teardown or deadline-driven refusal as evidence.
			join()
			if !arrivedAt.Before(cutoff) || !actionCompletedAt.Before(cutoff) || !resumedAt.Before(cutoff) || !joinedAt.Before(cutoff) || frontierWait.Err() != nil {
				t.Fatal("arrival/action/resume/join missed conservative existing held cutoff; no control credit")
			}
			if actionErr != nil || errors.Is(workerErr, context.DeadlineExceeded) {
				t.Fatal("actual action/deadline failure; not a qualifying control", actionErr, workerErr)
			}
			if mode != "callerCancel" && ctx.Err() != nil {
				t.Fatal("unchanged/Host.Close/Stop operation caller did not stay live through observer completion", ctx.Err())
			}
			if mode == "callerCancel" && !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("callerCancel did not retain real cancellation through observer completion", ctx.Err())
			}

			run.lease.mu.Lock()
			negativeClosed, released, matched := run.negativeClosed, run.released, run.matchedAfterExpiry
			failed := run.lease.failed
			run.lease.mu.Unlock()
			if mode != "unchanged" {
				if workerErr == nil || negativeClosed || released || matched || !failed || !run.invalid.Load() {
					t.Fatalf("outer control failed: error=%v negativeClosed=%t released=%t matchedAfterExpiry=%t stickyfailed=%t invalid=%t", workerErr, negativeClosed, released, matched, failed, run.invalid.Load())
				}
				if mode == "callerCancel" && !errors.Is(workerErr, context.Canceled) {
					t.Fatal("caller cancellation lost error identity", workerErr)
				}
				if mode == "endpointSubstitution" {
					// Independent /proc metadata only: endpoint refusal must not be
					// process termination, caller cancellation, or a controller probe.
					if err := expiryOuterOriginalProcessAlive(ctx, p.Process); err != nil {
						t.Fatal("endpoint control lost original live PID/start ticks after join", err)
					}
					t.Logf("originalalive after workerjoined: PID=%d start_ticks=%d caller_alive=%t", p.Process.PID, p.Process.StartTicks, ctx.Err() == nil)
					if err := restoreEndpoint(); err != nil || !endpoint.restored {
						t.Fatal("endpoint fixture restoration failed before FIRST canonical teardown; no PASS", err)
					}
					run.lease.mu.Lock()
					sticky := run.lease.failed && run.invalid.Load() && !run.negativeClosed && !run.released && !run.matchedAfterExpiry
					noPriorPrimaryClose := !run.ownerCloseRecorded && !run.hostCloseRecorded
					run.lease.mu.Unlock()
					t.Logf("original socket restored before FIRST teardown: stickyflags=%t no_prior_primary_Close=%t caller_alive=%t", sticky, noPriorPrimaryClose, ctx.Err() == nil)
					if !sticky || !noPriorPrimaryClose || ctx.Err() != nil {
						t.Fatal("fixture restoration renewed qualification or lost independent endpoint control")
					}
				}
				finalize()
				return
			}
			if workerErr != nil || !negativeClosed || released || matched || failed || run.invalid.Load() {
				t.Fatal("unchanged real post-Consume control did not succeed", workerErr)
			}
			if err := run.release(ctx); err != nil {
				t.Fatal("actual R/A", err)
			}
			// A is not expiry publication: require the original owner's real
			// durable advance, then fresh projection and public matched proof.
			var expiredRevision uint64
			for {
				status, err := owner.adapter.Inspect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if status.DatabaseID != p.DatabaseID || status.OwnerPID != p.Process.PID || status.OwnerStartedAt != p.Observation.Status.OwnerStartedAt || status.Lifecycle != "ready" || !status.Healthy {
					t.Fatal("post-A original owner lost")
				}
				if status.CurrentRevision > inserted.Revision && status.DurableThrough == status.CurrentRevision {
					expiredRevision = status.CurrentRevision
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("no real durable expiry revision", ctx.Err())
				case <-time.After(time.Millisecond): // post-A polling, NEVER frontier discovery
				}
			}
			if _, err := owner.adapter.Get(ctx, "spk/expiry-evidence/extra"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				t.Fatal("post-expiry extra exists", err)
			}
			if err := h.refreshProjection(ctx); err != nil {
				t.Fatal(err)
			}
			if err := run.observeMatchedAfterExpiry(ctx, nil, inserted.Revision); err != nil {
				t.Fatal("fresh refreshed matched closure", err)
			}
			refreshed, err := h.VerifyRetainedRecords(ctx, nil)
			if err != nil || refreshed.MatchedPrivateRecords() != 6 || refreshed.MatchedPublicationRecords() != 0 || refreshed.MatchedRecords() != 6 {
				t.Fatal("refreshed exact private-only closure differs", err)
			}
			t.Logf("actual outer unchanged expiry: held_revision=%d; expired_revision=%d; private=%d; publication=%d; explicitRA=true", inserted.Revision, expiredRevision, refreshed.MatchedPrivateRecords(), refreshed.MatchedPublicationRecords())
			finalize()
		})
	}
}

// Endpoint fixture only: no provider, accept loop, RPC, signal, or authority.
// All mutations are confined to the newly owned runtime directory. Restoration
// is ONE shot and is separate from (and precedes) FIRST canonical teardown.
type expiryOuterEndpointIdentity struct {
	device, inode  uint64
	mode, uid, gid uint32
}

func expiryOuterEndpointIdentityOf(stat *unix.Stat_t) expiryOuterEndpointIdentity {
	return expiryOuterEndpointIdentity{uint64(stat.Dev), stat.Ino, stat.Mode, stat.Uid, stat.Gid}
}

func expiryOuterEndpointStatAt(fd int, name string) (expiryOuterEndpointIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return expiryOuterEndpointIdentity{}, err
	}
	return expiryOuterEndpointIdentityOf(&stat), nil
}

type expiryOuterEndpointFixture struct {
	directory, original, parked                               string
	directoryFD                                               int
	directoryID, originalID, syntheticID                      expiryOuterEndpointIdentity
	listener                                                  *net.UnixListener
	setupAttempted, moved, syntheticBound, restored           bool
	restoreOnce                                               sync.Once
	listenerCloseErr, unlinkErr, renameErr, directoryCloseErr error
	restoreErr                                                error
}

func (f *expiryOuterEndpointFixture) directoryMatches() error {
	var opened, named unix.Stat_t
	if err := unix.Fstat(f.directoryFD, &opened); err != nil {
		return err
	}
	if err := unix.Lstat(f.directory, &named); err != nil {
		return err
	}
	if expiryOuterEndpointIdentityOf(&opened) != f.directoryID || expiryOuterEndpointIdentityOf(&named) != f.directoryID {
		return errors.New("endpoint fixture runtime directory identity changed; preserve paths")
	}
	return nil
}

func (f *expiryOuterEndpointFixture) substitute(ctx context.Context, runtimeRoot string, p sidecargapdb.SidecarProvenance) error {
	if f.setupAttempted {
		return errors.New("endpoint fixture substitution cannot be retried")
	}
	f.setupAttempted = true
	f.directory, f.original = p.RuntimeDirectory, p.ConfiguredSocket
	f.parked = filepath.Join(f.directory, "expiry-outer-original.sock")
	if !filepath.IsAbs(f.directory) || filepath.Dir(f.directory) != runtimeRoot ||
		f.original != filepath.Join(f.directory, "gapdb.sock") || p.ConnectedSocket != f.original ||
		p.RuntimeInode == 0 || p.SocketInode == 0 {
		return errors.New("endpoint fixture is not the original newly owned runtime/socket")
	}
	if err := expiryOuterOriginalProcessAlive(ctx, p.Process); err != nil {
		return err
	}
	fd, err := unix.Open(f.directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f.directoryFD = fd // transferred to the one-shot restorer, even on setup failure
	var directory unix.Stat_t
	if err := unix.Fstat(fd, &directory); err != nil {
		return err
	}
	f.directoryID = expiryOuterEndpointIdentityOf(&directory)
	if f.directoryID.device != p.RuntimeDevice || f.directoryID.inode != p.RuntimeInode ||
		f.directoryID.mode != unix.S_IFDIR|0700 || f.directoryID.uid != uint32(os.Geteuid()) || f.directoryID.gid != uint32(os.Getegid()) {
		return errors.New("endpoint fixture runtime metadata differs from original private directory")
	}
	if err := f.directoryMatches(); err != nil {
		return err
	}
	f.originalID, err = expiryOuterEndpointStatAt(fd, filepath.Base(f.original))
	if err != nil {
		return err
	}
	if f.originalID.mode&unix.S_IFMT != unix.S_IFSOCK || f.originalID.device != p.SocketDevice || f.originalID.inode != p.SocketInode ||
		f.originalID.uid != uint32(os.Geteuid()) || f.originalID.gid != uint32(os.Getegid()) {
		return errors.New("endpoint fixture original socket identity differs from provenance")
	}
	if _, err := expiryOuterEndpointStatAt(fd, filepath.Base(f.parked)); !errors.Is(err, unix.ENOENT) {
		return errors.New("endpoint fixture parked sibling not absent; preserve foreign path")
	}
	// Atomic NOREPLACE: never overwrite even an unexpected same-directory path.
	if err := unix.Renameat2(fd, filepath.Base(f.original), fd, filepath.Base(f.parked), unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	f.moved = true
	if parked, err := expiryOuterEndpointStatAt(fd, filepath.Base(f.parked)); err != nil || parked != f.originalID {
		return errors.New("endpoint fixture parked original identity differs; preserve paths")
	}
	if err := f.directoryMatches(); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.original, Net: "unix"})
	if err != nil {
		return err
	}
	// Close must NEVER auto-unlink an unknown replacement path. The restorer
	// checks our exact synthetic inode and performs one explicit unlink itself.
	listener.SetUnlinkOnClose(false)
	f.listener, f.syntheticBound = listener, true
	f.syntheticID, err = expiryOuterEndpointStatAt(fd, filepath.Base(f.original))
	if err != nil {
		return err
	}
	if f.syntheticID.mode&unix.S_IFMT != unix.S_IFSOCK || f.syntheticID == f.originalID ||
		f.syntheticID.device != f.originalID.device || f.syntheticID.inode == f.originalID.inode ||
		f.syntheticID.uid != uint32(os.Geteuid()) || f.syntheticID.gid != uint32(os.Getegid()) {
		return errors.New("endpoint fixture did not bind a distinct own socket inode")
	}
	if err := f.directoryMatches(); err != nil {
		return err
	}
	return expiryOuterOriginalProcessAlive(ctx, p.Process)
}

func (f *expiryOuterEndpointFixture) restore() error {
	f.restoreOnce.Do(func() {
		var validationErr error
		defer func() {
			if f.directoryFD >= 0 {
				f.directoryCloseErr = unix.Close(f.directoryFD)
				f.directoryFD = -1
			}
			f.restoreErr = errors.Join(validationErr, f.listenerCloseErr, f.unlinkErr, f.renameErr, f.directoryCloseErr)
		}()
		if f.listener != nil {
			f.listenerCloseErr = f.listener.Close() // FIRST only; auto-unlink disabled
			if f.listenerCloseErr != nil {
				return // retain both known paths; no removal after failed Close
			}
		}
		if !f.moved {
			return // setup never moved the original; do not mutate any path
		}
		if err := f.directoryMatches(); err != nil {
			validationErr = err
			return
		}
		if parked, err := expiryOuterEndpointStatAt(f.directoryFD, filepath.Base(f.parked)); err != nil || parked != f.originalID {
			validationErr = errors.New("restore refuses changed/missing parked original; preserve paths")
			return
		}
		if f.syntheticBound {
			current, err := expiryOuterEndpointStatAt(f.directoryFD, filepath.Base(f.original))
			if err != nil || current != f.syntheticID || f.syntheticID.inode == 0 {
				validationErr = errors.New("restore refuses unlink of unknown synthetic-path identity; preserve paths")
				return
			}
			f.unlinkErr = unix.Unlinkat(f.directoryFD, filepath.Base(f.original), 0) // own inode ONLY, ONCE
			if f.unlinkErr != nil {
				return
			}
		}
		if _, err := expiryOuterEndpointStatAt(f.directoryFD, filepath.Base(f.original)); !errors.Is(err, unix.ENOENT) {
			validationErr = errors.New("restore refuses occupied original path; preserve paths")
			return
		}
		// NOREPLACE also guards the final destination check-to-rename interval.
		f.renameErr = unix.Renameat2(f.directoryFD, filepath.Base(f.parked), f.directoryFD, filepath.Base(f.original), unix.RENAME_NOREPLACE)
		if f.renameErr != nil {
			return
		}
		original, err := expiryOuterEndpointStatAt(f.directoryFD, filepath.Base(f.original))
		if err != nil || original != f.originalID {
			validationErr = errors.New("restored original socket identity differs; preserve paths")
			return
		}
		if _, err := expiryOuterEndpointStatAt(f.directoryFD, filepath.Base(f.parked)); !errors.Is(err, unix.ENOENT) {
			validationErr = errors.New("parked path remains after restoration; preserve paths")
			return
		}
		f.restored = true
	})
	return f.restoreErr // immutable FIRST outcome, never another close/unlink/rename
}

// Read /proc metadata only, no kill(0), pidfd signal, owner RPC, or gate probe.
func expiryOuterOriginalProcessAlive(ctx context.Context, original sidecargapdb.ProcessIdentity) error {
	if ctx == nil || ctx.Err() != nil || original.PID <= 0 || original.StartTicks == 0 {
		return errors.New("endpoint original-process caller/identity unavailable")
	}
	file, err := os.Open(fmt.Sprintf("/proc/%d/stat", original.PID))
	if err != nil {
		return err
	}
	// This native fixture owns the proc file too: preserve its FIRST Close,
	// rather than using os.ReadFile's deliberately discarded close outcome.
	stat, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if len(stat) > 4096 {
		return errors.New("endpoint original-process stat exceeds fixture bound")
	}
	ticks, err := expiryOuterLiveProcStartTicks(stat, original.PID)
	if err != nil || ticks != original.StartTicks {
		return errors.New("endpoint original PID is dead or start ticks changed")
	}
	return ctx.Err()
}

func expiryOuterLiveProcStartTicks(stat []byte, expectedPID int) (uint64, error) {
	open, closing := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
	if open <= 0 || closing <= open || closing+2 >= len(stat) {
		return 0, errors.New("malformed proc stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(stat[:open])))
	if err != nil || pid != expectedPID || pid <= 0 {
		return 0, errors.New("proc stat PID differs")
	}
	fields := strings.Fields(string(stat[closing+1:]))
	if len(fields) <= 19 || len(fields[0]) != 1 || !strings.ContainsAny(fields[0], "RSDTtWKPI") {
		return 0, errors.New("proc stat lacks live state/start ticks")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return 0, errors.New("proc stat start ticks invalid")
	}
	return ticks, nil
}

// Pure parser controls: do not bind sockets, start providers, or execute the
// endpoint mutation/restoration method before independent design/source review.
func TestExpiryHostOuterEndpointProcStat(t *testing.T) {
	stat := func(pid int, name, state, ticks string) []byte {
		return []byte(fmt.Sprintf("%d (%s) %s %s%s 0\n", pid, name, state, strings.Repeat("0 ", 18), ticks))
	}
	for _, tc := range []struct {
		name string
		stat []byte
		ok   bool
	}{
		{"live", stat(42, "gapdb", "S", "77"), true},
		{"comm_parentheses_spaces", stat(42, "gap) db (worker)", "R", "77"), true},
		{"zombie", stat(42, "gapdb", "Z", "77"), false},
		{"dead", stat(42, "gapdb", "X", "77"), false},
		{"dead_lowercase", stat(42, "gapdb", "x", "77"), false},
		{"unknown_state", stat(42, "gapdb", "?", "77"), false},
		{"different_pid", stat(43, "gapdb", "S", "77"), false},
		{"zero_ticks", stat(42, "gapdb", "S", "0"), false},
		{"bad_ticks", stat(42, "gapdb", "S", "invalid"), false},
		{"short", []byte("42 (gapdb) S 0"), false},
		{"malformed", []byte("42 gapdb S 0"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ticks, err := expiryOuterLiveProcStartTicks(tc.stat, 42)
			if tc.ok && (err != nil || ticks != 77) || !tc.ok && err == nil {
				t.Fatalf("proc stat live identity: ticks=%d error=%v valid=%t", ticks, err, tc.ok)
			}
		})
	}
}

// Scenario acceptance/rejection is separate from the full first-error cleanup
// union. Preserve every native root and never repair a failed first teardown.
func expiryOuterFinalRundown(t *testing.T, run *expiryHostRun, success bool) {
	t.Helper()
	finalErr := run.close(context.Background()) // exactly ONCE per fresh case
	t.Logf("FIRST final close: expected scenario success=%t; actual error=%v", success, finalErr)
	if success && finalErr != nil || !success && finalErr == nil {
		t.Errorf("canonical final scenario outcome differs: success=%t error=%v", success, finalErr)
	}
	cleanupErr := run.cleanupOutcomes()
	rootErr := run.lease.cleanup.outcome(true)
	t.Logf("POST-RUNDOWN actual full owner/Host/gate error=%v; full root error=%v", cleanupErr, rootErr)
	l := run.lease
	l.mu.Lock()
	c := l.control
	canonicalClosed := c != nil && c.lease == l && c.kind == expiryControlFinal &&
		c.predecessor == run && c.candidate == nil && l.current == run &&
		c.stage == expiryControlTerminalClosed && l.phase == expiryLeaseClosed &&
		c.metadata == nil && run.window != nil && l.quiescentLocked(run)
	ownerRecorded, hostRecorded := run.ownerCloseRecorded, run.hostCloseRecorded
	ownerErr, hostErr := run.firstOwnerCloseErr, run.firstHostCloseErr
	gateRecorded := run.gateCloseRecorded.Load()
	var gateErr error
	if first := run.firstGateCloseErr.Load(); first != nil {
		gateErr = first.err
	}
	var rootCounts [3][3]uint64
	for i, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
		category := &l.cleanup.categories[kind]
		rootCounts[i] = [3]uint64{category.acquired.Load(), category.attempted.Load(), category.completed.Load()}
	}
	l.mu.Unlock()
	t.Logf("POST-RUNDOWN canonical final closed/quiescent=%t; first owner recorded=%t error=%v; first Host recorded=%t error=%v; first gate recorded=%t error=%v",
		canonicalClosed, ownerRecorded, ownerErr, hostRecorded, hostErr, gateRecorded, gateErr)
	for i, name := range []string{"parent", "data", "lock"} {
		t.Logf("POST-RUNDOWN required root %s acquired/attempted/completed=%v", name, rootCounts[i])
		if rootCounts[i] != [3]uint64{1, 1, 1} {
			t.Errorf("required root %s first closes not complete: %v", name, rootCounts[i])
		}
	}
	if cleanupErr != nil || rootErr != nil || !canonicalClosed || !ownerRecorded || !hostRecorded || !gateRecorded || ownerErr != nil || hostErr != nil || gateErr != nil {
		t.Error("scenario outcome masked actual first-error/incomplete cleanup; roots preserved")
	}
}
