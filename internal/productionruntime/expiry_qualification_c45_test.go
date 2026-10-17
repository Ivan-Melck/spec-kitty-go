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

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// These controls are provider-free models/source checks, NOT a genuine healthy
// gate/Host, callback timeout schedule, or native/finalizer/GC experiment.
func expiryC45RollbackModel() (*expiryHostLease, *expiryLeaseControl, *expiryHostRun) {
	l := &expiryHostLease{phase: expiryLeaseFresh}
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
	c := &expiryLeaseControl{lease: l, candidate: r, kind: expiryControlFresh, stage: expiryControlRollback}
	l.control = c
	return l, c, r
}

func TestExpiryHostC45RollbackConstructionAwareFirstOutcomes(t *testing.T) {
	for _, prefix := range []string{"absent", "owner_unrecorded", "owner_nil", "owner_error", "host_without_owner", "host_owner_unrecorded", "host_unrecorded", "host_nil", "host_error", "gate_unrecorded", "gate_first_error_unrecorded", "gate_recorded_missing_full"} {
		t.Run(prefix, func(t *testing.T) {
			_, _, r := expiryC45RollbackModel()
			first := errors.New("MODEL actual first owner/Host outcome")
			wantError, wantFirst := false, false
			switch prefix {
			case "owner_unrecorded", "owner_nil", "owner_error":
				r.owner = &productionOwner{}
				r.ownerCloseRecorded = prefix != "owner_unrecorded"
				wantError = prefix != "owner_nil"
				if prefix == "owner_error" {
					r.firstOwnerCloseErr, wantFirst = first, true
				}
			case "host_without_owner", "host_owner_unrecorded", "host_unrecorded", "host_nil", "host_error":
				r.host = &Host{}
				if prefix != "host_without_owner" {
					r.owner, r.ownerCloseRecorded = &productionOwner{}, prefix != "host_owner_unrecorded"
				}
				r.hostCloseRecorded = prefix != "host_unrecorded"
				wantError = prefix != "host_nil"
				if prefix == "host_error" {
					r.firstHostCloseErr, wantFirst = first, true
				}
			case "gate_unrecorded", "gate_first_error_unrecorded", "gate_recorded_missing_full":
				r.gate = &sidecargapdb.ExpiryQualification{}
				r.gateCloseRecorded.Store(prefix == "gate_recorded_missing_full")
				if prefix == "gate_first_error_unrecorded" {
					r.firstGateCloseErr.Store(&expiryRootCloseError{first})
					wantFirst = true
				}
				wantError = true // even recorded nil cannot replace FULL real report
			}
			got := r.rollbackCleanupOutcomes()
			if (got != nil) != wantError || wantFirst && !errors.Is(got, first) {
				t.Fatalf("prefix %s: missing/absent/first outcome distinction: %v", prefix, got)
			}
			if prefix == "absent" && r.cleanupOutcomes() == nil {
				t.Fatal("ordinary full-run helper was weakened for absent construction")
			}
		})
	}
}

func TestExpiryHostC45FirstGateErrorModelLaterNilRetainsRoots(t *testing.T) {
	parent := h06ConstructorDirectory(t)
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "root")})
	if err != nil {
		t.Fatal(err)
	}
	c := l.control
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow(), gate: &sidecargapdb.ExpiryQualification{}}
	first := errors.New("MODEL first gate callback timeout; not an executed timeout")
	// Explicit MODEL first actual-close bookkeeping; no fabricated live gate.
	r.gateCloseOnce.Do(func() {
		r.firstGateCloseErr.Store(&expiryRootCloseError{first})
		r.gateCloseRecorded.Store(true)
	})
	l.mu.Lock()
	c.candidate, c.stage = r, expiryControlRollback
	l.mu.Unlock()
	// Late completeness nil is MODEL-ONLY. The real zero gate's FULL report
	// remains missing/non-nil; this test cannot manufacture a healthy report.
	var modeledLaterFull error
	union := errors.Join(r.rollbackCleanupOutcomes(), modeledLaterFull)
	if !errors.Is(union, first) {
		t.Fatal("saved first gate error lost against model later nil", union)
	}
	// The helper above observes rollback state. The actual rollback entry below
	// must start at a permitted construction stage, not re-enter rollback.
	l.mu.Lock()
	c.stage = expiryControlConstructing
	l.mu.Unlock()
	cause := errors.New("MODEL constructor failed")
	got := l.rollbackControl(c, cause)
	if !errors.Is(got, first) || !errors.Is(got, cause) || !errors.Is(l.cleanup.outcome(false), first) {
		t.Fatal("rollback lost actual-first bookkeeping reason", got)
	}
	if l.control != c || c.stage != expiryControlTerminalResidual || !l.residualPinned || c.candidate != r {
		t.Fatal("same canonical graph not retained")
	}
	for _, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
		if l.cleanup.categories[kind].attempted.Load() != 0 {
			t.Fatal("first gate failure released a root")
		}
	}
	for _, f := range []*os.File{l.lock, l.directory, l.parentDirectory} {
		if _, _, err := expiryRootStat(f); err != nil {
			t.Fatal("actual root FD no longer retained", err)
		}
	}
 // Canonical residual deliberately retains all actual roots until process
 // exit; no automatic/manual disposal after metadata/unknown-custody failure.
}

func TestExpiryHostC45ResidualCanonicalOnceAndStrongChain(t *testing.T) {
	l, c, r := expiryC45RollbackModel()
	first := errors.New("MODEL residual")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() { _ = l.residual(c, first); _ = l.rollbackControl(c, first) })
	}
	wg.Wait()
	l.mu.Lock()
	next := l.residualNext
	if !l.residualPinned || l.control != c || c.candidate != r || c.firstErr != first || c.stage != expiryControlTerminalResidual {
		t.Fatal("canonical graph changed")
	}
	forged := *c
	if l.pinResidualLocked(&forged) || l.residualNext != next {
		t.Fatal("pinned flag bypassed canonical identity")
	}
	l.mu.Unlock()
	second, other, _ := expiryC45RollbackModel()
	_ = second.residual(other, first)
	// TEST-ONLY traversal, holding ONLY custody mutex; never acquires lease.mu.
	expiryResidualCustody.Lock()
	count, secondCount := 0, 0
	for p := expiryResidualCustody.head; p != nil; p = p.residualNext {
		if p == l {
			count++
		}
		if p == second {
			secondCount++
		}
	}
	expiryResidualCustody.Unlock()
	if count != 1 || secondCount != 1 || second.residualNext == nil {
		t.Fatal("new head lost earlier strongly rooted lease or inserted twice")
	}
	foreign, fc, _ := expiryC45RollbackModel()
	foreign.mu.Lock()
	fc.stage = expiryControlTerminalResidual
	if foreign.pinResidualLocked(c) || foreign.residualPinned {
		t.Fatal("foreign controller inserted another lease")
	}
	foreign.mu.Unlock()
}

func TestExpiryHostC45AlreadyResidualRollbackPinsBeforeReturn(t *testing.T) {
	l, c, _ := expiryC45RollbackModel()
	// MODEL legacy/unpinned terminal observation, not a production publication.
	c.stage = expiryControlTerminalResidual
	first := errors.New("MODEL first residual")
	c.firstErr = first
	if !errors.Is(l.rollbackControl(c, errors.New("later refusal")), first) || !l.residualPinned || l.control != c {
		t.Fatal("already-residual return lost pin/first reason")
	}
}

func TestExpiryHostC45SourceScalarCutoffsAndFullRollbackUnion(t *testing.T) {
	body := expiryC3FunctionBody(t, "expiry_qualification.go", "startGeneration")
	query := strings.Index(body, "r.gate.RecordedCleanupError()")
	handoff := strings.Index(body, "l.current, l.phase, c.stage, l.control =")
	if query < strings.LastIndex(body, "l.mu.Lock()") || query > handoff || handoff < 0 {
		t.Fatal("candidate same-gate cutoff not inside final validating handoff")
	}
	body = expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "admitMetadata")
	query = strings.Index(body, "r.gate.RecordedCleanupError()")
	if query < strings.Index(body, "!r.currentLocked(ctx)") || query > strings.Index(body, "work := &expiryMetadataWork") || query > strings.Index(body, "l.snapshotPathsLocked()") {
		t.Fatal("query not before metadata admission/root pointer snapshot")
	}
	body = expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "reserveControlLocked")
	query = strings.Index(body, "r.gate.RecordedCleanupError()")
	if query < strings.Index(body, "!expiryOriginalDoneOpen(r)") || query > strings.Index(body, "r.controlledTeardown.Store(true)") || !strings.Contains(body, "if kind != expiryControlFinal {") {
		t.Fatal("original restart Done/query cutoff or final failure eligibility lost")
	}
	for name, bit := range map[string]string{"observeS1Refusal": "r.negativeClosed = true", "release": "r.released = true", "observeMatchedAfterExpiry": "r.matchedAfterExpiry, r.expiryRevision ="} {
		body = expiryC3FunctionBody(t, "expiry_qualification_observation.go", name)
		query = strings.Index(body, "r.gate.RecordedCleanupError()")
		if query < strings.LastIndex(body, "r.lease.mu.Lock()") || query > strings.Index(body, bit) || query < 0 {
			t.Fatal("actual publication lacks scalar cutoff", name)
		}
	}
	body = expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "rollbackCleanupOutcomes")
	unlock := strings.Index(body, "l.mu.Unlock()")
	for _, required := range []string{"r.gateCloseRecorded.Load()", "r.firstGateCloseErr.Load()", "gate.CleanupError()", "errors.Join("} {
		if strings.Index(body, required) < unlock {
			t.Fatal("full/first outcomes absent or performed under lease lock", required)
		}
	}
	body = expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "rollbackControl")
	union := strings.Index(body, "r.rollbackCleanupOutcomes()")
	if union < strings.Index(body, "r.host.Close(cleanup)") || union > strings.Index(body, "l.closeDescriptors(c)") || union > strings.Index(body, "l.checkPathsForControl(c)") {
		t.Fatal("direct + first/FULL union not before roots")
	}
}

func TestExpiryHostC45SourceAllResidualRoutesPinned(t *testing.T) {
	residual := expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "residual")
	stage := strings.Index(residual, "c.stage = expiryControlTerminalResidual")
	pin := strings.Index(residual, "l.pinResidualLocked(c)")
	unlock := strings.Index(residual, "l.mu.Unlock()")
	if stage < 0 || pin < stage || unlock < pin || strings.Index(residual, "errors.Join(") < unlock {
		t.Fatal("terminal residual publication not pinned before unlock/result")
	}
	rollback := expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "rollbackControl")
	already := strings.Index(rollback, "if c.stage == expiryControlTerminalResidual")
	if already < 0 {
		t.Fatal("already-residual route missing")
	}
	pin = strings.Index(rollback[already:], "l.pinResidualLocked(c)")
	unlock = strings.Index(rollback[already:], "l.mu.Unlock()")
	if already < 0 || pin < 0 || unlock < pin {
		t.Fatal("already-residual early return not pinned")
	}
	body := expiryC3FunctionBody(t, "expiry_qualification_rundown.go", "pinResidualLocked")
	if strings.Index(body, "!l.controlLocked(c, expiryControlTerminalResidual)") > strings.Index(body, "if l.residualPinned") || strings.Count(body, "expiryResidualCustody.Lock()") != 1 {
		t.Fatal("pin canonical-before-idempotence/lock order regression")
	}
	for _, forbidden := range []string{"errors.Join(", ".Close(", ".cancel(", "for ", "l.mu.Lock()", "go "} {
		if strings.Contains(body, forbidden) {
			t.Fatal("pin acquired traversal/work/reverse lock", forbidden)
		}
	}
	// Exhaust all assignment forms (including tuples) in the three owned files,
	// not just one spelling. Each new publication requires an explicit census.
	assignments := 0
	for _, path := range []string{"expiry_qualification.go", "expiry_qualification_rundown.go", "expiry_qualification_observation.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				assignment, ok := node.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, rhs := range assignment.Rhs {
					if id, ok := rhs.(*ast.Ident); ok && id.Name == "expiryControlTerminalResidual" {
						assignments++
						if fn.Name.Name != "residual" {
							t.Fatal("uncensused residual publication", fn.Name.Name)
						}
					}
				}
				return true
			})
		}
	}
	if assignments != 1 {
		t.Fatal("new residual publication requires explicit pin census", assignments)
	}
}
