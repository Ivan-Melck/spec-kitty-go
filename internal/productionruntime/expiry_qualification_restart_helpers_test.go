//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"os"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// SOURCE ONLY / UNRUN. These tests require the candidate callsite extraction.
// Guard component inputs are NOT provenance emitted by a genuine process,
// constructor fault placement, reached Native faults, or whole-clause evidence.
// ProcessIdentity and SidecarProvenance have no public constructors: the actual
// exported comparable structs are used, never a fabricated SDK constructor.
type expiryProposalFreshnessInputs struct {
	readErr error
	current runtimeCurrentV2
	predecessorEpoch journalv2.RuntimeEpochID
	p, predecessor sidecargapdb.SidecarProvenance
	identity, predecessorIdentity sidecargapdb.ExpiryTechnicalIdentity
}

func expiryProposalFreshnessInputsValid(t *testing.T) expiryProposalFreshnessInputs {
	t.Helper()
	oldEpoch, err := journalv2.NewRuntimeEpochID("epoch-guard-old")
	if err != nil { t.Fatal(err) }
	newEpoch, err := journalv2.NewRuntimeEpochID("epoch-guard-new")
	if err != nil { t.Fatal(err) }
	host, err := journalv2.NewHostAuthorityID("host-guard")
	if err != nil { t.Fatal(err) }
	revision, err := journalv2.NewStoreRevisionV2("database-guard", 17, 17)
	if err != nil { t.Fatal(err) }
	operation, err := journalv2.NewOperationID("runtime-admit-guard-new")
	if err != nil { t.Fatal(err) }
	attempt, err := journalv2.NewOperationID("restore-guard-new")
	if err != nil { t.Fatal(err) }
	technical := sidecargapdb.ExpiryTechnicalIdentity{
		InputRecordSHA256: expiryInputSHA, TechnicalTupleSHA256: expiryTupleSHA,
		ModifiedInventorySHA256: expiryInventorySHA, ExecutableSHA256: expiryExecutableSHA,
		ExecutableBytes: 5491681, ClientModule: "github.com/spec-kitty/gapdb",
		ClientVersion: "v0.0.0-20260907114832-006e9d469d40",
		ClientModuleH1: "h1:vTBY6qolbiwbtAX/CFGAgNlmoQ9VpxGPAce+84BjSbM=",
		ClientGoModH1: "h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ=",
	}
	// These are source-emittable typed values only. PID/start numbers are not
	// looked up, opened, signaled, or represented as authenticated/living.
	old := sidecargapdb.SidecarProvenance{
		DatabaseID: string(revision.DatabaseID()),
		RuntimeDirectory: "/component/guard/old", ConfiguredSocket: "/component/guard/old/gapdb.sock",
		Process: sidecargapdb.ProcessIdentity{PID: 41001, StartTicks: 11001,
			Device: 7, Inode: 31, Size: 5491681, SHA256: expiryExecutableSHA},
	}
	next := old
	next.RuntimeDirectory, next.ConfiguredSocket = "/component/guard/new", "/component/guard/new/gapdb.sock"
	next.Process.PID, next.Process.StartTicks = 41002, 11002
	return expiryProposalFreshnessInputs{
		current: runtimeCurrentV2{SchemaVersion: 2, RuntimeEpochID: newEpoch,
			PredecessorEpochID: oldEpoch, HostAuthorityID: host, DatabaseID: revision.DatabaseID(),
			RecoveredStoreRevision: revision.CurrentRevision(), ProjectionSHA256: expiryInputSHA,
			CreatedOperationID: string(operation), RecoveryAttemptID: string(attempt), Status: "ready"},
		predecessorEpoch: oldEpoch, p: next, predecessor: old,
		identity: technical, predecessorIdentity: technical,
	}
}

func TestExpiryProposalRestartFreshnessGuardComponent(t *testing.T) {
	// Only the extracted ORIGINAL predicate is called. No predicate copy or
	// fake lifecycle/provider is used as the system under test.
	for _, name := range []string{"valid_control", "same_epoch", "same_process_identity",
		"same_PID_only", "same_start_only", "same_socket", "same_runtime_directory",
		"wrong_predecessor_link", "wrong_database", "wrong_technical_identity", "read_error"} {
		t.Run(name, func(t *testing.T) {
			f := expiryProposalFreshnessInputsValid(t)
			if !expiryIdentityValid(f.identity) || !expiryIdentityValid(f.predecessorIdentity) {
				t.Fatal("fixture technical values are not the original valid technical identity")
			}
			if expiryRestartFreshnessRefused(f.readErr, f.current, f.predecessorEpoch,
				f.p, f.predecessor, f.identity, f.predecessorIdentity) {
				t.Fatal("otherwise-valid control refused")
			}
			switch name {
			case "same_epoch": f.current.RuntimeEpochID = f.predecessorEpoch
			case "same_process_identity":
				// Full struct equality necessarily also triggers PID and start
				// equality. Those disjuncts are redundant for this input; this
				// is NOT an independently isolatable full-equality OR atom.
				f.p.Process = f.predecessor.Process
			case "same_PID_only": f.p.Process.PID = f.predecessor.Process.PID
			case "same_start_only": f.p.Process.StartTicks = f.predecessor.Process.StartTicks
			case "same_socket": f.p.ConfiguredSocket = f.predecessor.ConfiguredSocket
			case "same_runtime_directory": f.p.RuntimeDirectory = f.predecessor.RuntimeDirectory
			case "wrong_predecessor_link":
				other, err := journalv2.NewRuntimeEpochID("epoch-guard-unrelated")
				if err != nil { t.Fatal(err) }
				f.current.PredecessorEpochID = other
			case "wrong_database":
				other, err := journalv2.NewStoreRevisionV2("database-guard-other", 17, 17)
				if err != nil { t.Fatal(err) }
				f.p.DatabaseID = string(other.DatabaseID())
			case "wrong_technical_identity": f.identity.TechnicalTupleSHA256 = expiryInputSHA
			case "read_error": f.readErr = &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_current_read"}
			}
			got := expiryRestartFreshnessRefused(f.readErr, f.current, f.predecessorEpoch,
				f.p, f.predecessor, f.identity, f.predecessorIdentity)
			if got != (name != "valid_control") { t.Fatalf("guard component %s: refused=%t", name, got) }
		})
	}
}

// This obtains a real error only from a known, owned *os.File already closed
// through that SAME object. No raw-fd close, reused integer, foreign descriptor,
// fake closer, process Stop, signal, wait or reap is involved.
func expiryProposalOwnedClosedFile(t *testing.T) (*os.File, error) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "owned-guard-")
	if err != nil { t.Fatal(err) }
	if err := file.Close(); err != nil { t.Fatal(err) }
	failure := file.Close()
	var path *os.PathError
	if !errors.Is(failure, os.ErrClosed) || !errors.As(failure, &path) || path.Op != "close" {
		t.Fatal("expected actual owned closed-file Close error", failure)
	}
	return file, failure
}

func TestExpiryProposalRestartCleanupPropagationGuardComponent(t *testing.T) {
	if expiryRestartCleanupUnion(nil, nil, nil, nil) != nil { t.Fatal("nil union changed") }
	for _, name := range []string{"prior_stop_input", "Host_close_input", "owner_outcomes_input", "FIRST_cleanup_outcomes_input", "cleanup_context_input"} {
		t.Run(name, func(t *testing.T) {
			_, actual := expiryProposalOwnedClosedFile(t)
			var crashErr, hostErr, outcomesErr, contextErr error
			switch name {
			case "prior_stop_input": crashErr = actual
			case "Host_close_input": hostErr = actual
			case "owner_outcomes_input", "FIRST_cleanup_outcomes_input": outcomesErr = actual
			case "cleanup_context_input": contextErr = actual
			}
			got := expiryRestartCleanupUnion(crashErr, hostErr, outcomesErr, contextErr)
			var want, seen *os.PathError
			if !errors.As(actual, &want) || !errors.As(got, &seen) || seen != want ||
				!errors.Is(got, actual) || !errors.Is(got, os.ErrClosed) || got.Error() != actual.Error() {
				t.Fatal("propagation guard component lost Is/As/text", got)
			}
			// The slot labels describe error propagation ONLY. A File.Close
			// return is NOT an actual Stop/Host.Close/owner constructor fault.
		})
	}
}

func TestExpiryProposalRestartCleanupUnionOrderComponent(t *testing.T) {
	_, crashErr := expiryProposalOwnedClosedFile(t)
	_, hostErr := expiryProposalOwnedClosedFile(t)
	_, ownerErr := expiryProposalOwnedClosedFile(t)
	primary := &Error{Kind: ErrorShutdown, State: HostClosing, Field: "owner_cleanup"}
	outcomesErr := errors.Join(primary, ownerErr)
	got := expiryRestartCleanupUnion(crashErr, hostErr, outcomesErr, context.Canceled)
	for _, want := range []error{crashErr, hostErr, outcomesErr, ownerErr, primary, context.Canceled} {
		if !errors.Is(got, want) { t.Fatal("independent error disappeared", want, got) }
	}
	var path, firstPath *os.PathError
	if !errors.As(crashErr, &firstPath) || !errors.As(got, &path) || path != firstPath { t.Fatal("As traversal order changed") }
	var classified *Error
	if !errors.As(got, &classified) || classified != primary { t.Fatal("primary classification identity lost") }
	if got.Error() != crashErr.Error()+"\n"+hostErr.Error()+"\n"+outcomesErr.Error()+"\n"+context.Canceled.Error() {
		t.Fatal("Join order/text changed", got)
	}
	children, ok := got.(interface{ Unwrap() []error })
	if !ok { t.Fatal("not original Join multi-error") }
	list := children.Unwrap()
	if len(list) != 4 || list[0] != crashErr || list[1] != hostErr || list[2] != outcomesErr || list[3] != context.Canceled {
		t.Fatal("original four-argument union order/identity changed")
	}
}

func TestExpiryProposalFIRSTCleanupPropagationGuardComponent(t *testing.T) {
	// Use the ORIGINAL private FIRST cleanup implementation, not a new model.
	var report expiryRootCleanup
	file, _ := expiryProposalOwnedClosedFile(t)
	report.acquiredFile(expiryRootMetadata)
	report.closeFile(file, expiryRootMetadata)
	first := report.first.Load()
	if first == nil || !errors.Is(first.err, os.ErrClosed) { t.Fatal("FIRST actual close lost") }
	good, err := os.CreateTemp(t.TempDir(), "later-nil-")
	if err != nil { t.Fatal(err) }
	report.acquiredFile(expiryRootMetadata)
	report.closeFile(good, expiryRootMetadata) // actual later successful Close
	report.record(nil)
	if report.first.Load() != first { t.Fatal("later nil replaced FIRST error") }
	got := expiryRestartCleanupUnion(nil, nil, report.outcome(true), nil)
	var path *os.PathError
	if !errors.Is(got, first.err) || !errors.Is(got, os.ErrClosed) || !errors.As(got, &path) {
		t.Fatal("FIRST actual close absent from propagation guard", got)
	}
	// An earlier successful close must not mask a later failed resource either.
	var nilThenError expiryRootCleanup
	goodFirst, err := os.CreateTemp(t.TempDir(), "first-nil-")
	if err != nil { t.Fatal(err) }
	nilThenError.acquiredFile(expiryRootMetadata)
	nilThenError.closeFile(goodFirst, expiryRootMetadata)
	if nilThenError.outcome(true) != nil { t.Fatal("actual first nil failed") }
	badLater, _ := expiryProposalOwnedClosedFile(t)
	nilThenError.acquiredFile(expiryRootMetadata)
	nilThenError.closeFile(badLater, expiryRootMetadata)
	if !errors.Is(expiryRestartCleanupUnion(nil, nil, nilThenError.outcome(true), nil), os.ErrClosed) {
		t.Fatal("first nil masked independent later failure")
	}
}

func TestExpiryProposalOwnerFIRSTPropagationGuardComponent(t *testing.T) {
	l := &expiryHostLease{}
	r := &expiryHostRun{lease: l}
	file, _ := expiryProposalOwnedClosedFile(t)
	o := &productionOwner{expiry: ownerExpiryState{run: r}, closer: file}
	firstReturn := o.Close(context.Background()) // actual owner.Close, nil lifecycle
	first := r.firstOwnerCloseErr
	if !r.ownerCloseRecorded || first != firstReturn || !l.failed || !errors.Is(first, os.ErrClosed) {
		t.Fatal("actual owner closer outcome not retained", firstReturn)
	}
	if o.Close(context.Background()) != nil || r.firstOwnerCloseErr != first { t.Fatal("repeat nil repaired owner FIRST") }
	got := expiryRestartCleanupUnion(nil, nil, first, nil)
	var path *os.PathError
	if !errors.Is(got, first) || !errors.As(got, &path) { t.Fatal("owner propagation guard lost actual closer error", got) }
	// No lifecycle was supplied: this is NOT Stop/reap-error evidence.
}

func TestExpiryProposalHostFIRSTPropagationGuardComponent(t *testing.T) {
	// Reuse the EXISTING private fixture helper, not a copied Host/first model.
	// Its owner and Host are component objects, with no process or authority.
	l, c, r, h := errorCustodyExpiryModel()
	file, _ := expiryProposalOwnedClosedFile(t)
	r.owner.closer = file
	firstReturn := h.Close(context.Background()) // actual Host.Close, no lifecycle
	firstHost, firstOwner := r.firstHostCloseErr, r.firstOwnerCloseErr
	var primary *Error
	if !r.hostCloseRecorded || !r.ownerCloseRecorded || firstHost != firstReturn || !l.failed ||
		!errors.As(firstHost, &primary) || primary.Kind != ErrorShutdown || primary.State != HostClosing || primary.Field != "owner_cleanup" ||
		!errors.Is(firstOwner, os.ErrClosed) {
		t.Fatal("actual component Host/owner FIRST missing", firstReturn, firstOwner)
	}
	if h.Close(context.Background()) != nil || r.firstHostCloseErr != firstHost || r.firstOwnerCloseErr != firstOwner {
		t.Fatal("repeat nil changed FIRST Host/owner results")
	}
	// Move the existing component controller through its ORIGINAL permitted
	// transition before asking for a rollback-only report. Sticky failure and
	// actual FIRST receipts remain untouched; this is not an actual restart.
	if err := l.moveControl(c, expiryControlRollback, expiryControlValidating); err != nil { t.Fatal(err) }
	// Existing construction-aware FIRST helper is appropriate: there is no
	// installed gate and no fabricated gate completeness report.
	outcomes := r.rollbackCleanupOutcomes()
	got := expiryRestartCleanupUnion(nil, firstReturn, outcomes, nil)
	var seen *Error
	var path *os.PathError
	if !errors.Is(got, firstHost) || !errors.Is(got, firstOwner) || !errors.Is(got, os.ErrClosed) ||
		!errors.As(got, &seen) || seen != primary || !errors.As(got, &path) {
		t.Fatal("actual component FIRST outcomes lost from propagation guard", got)
	}
	// Ordinary Host.Close is deliberately sanitized: ONLY the separate owner
	// outcome carries PathError. No widened Host.Close API/error is proposed.
	if errors.Is(firstReturn, os.ErrClosed) { t.Fatal("Host.Close unexpectedly unwrapped owner cause") }
}

func TestExpiryProposalPredecessorQuiescenceGuardComponent(t *testing.T) {
	for _, name := range []string{"drained_control", "live_window", "retired_operation_pending", "retired_metadata_pending", "not_completed", "uncleared_operation", "nondrained_metadata_count", "uncleared_metadata_link", "missing_window"} {
		t.Run(name, func(t *testing.T) {
			l := &expiryHostLease{}
			r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
			w := r.window
			l.mu.Lock()
			// The original method publishes a retired/completed window. These
			// are synthetic lease-state inputs, NOT process-liveness evidence.
			l.retireLocked(r)
			if !l.quiescentLocked(r) { l.mu.Unlock(); t.Fatal("drained control refused") }
			switch name {
			case "live_window": r.window = newExpiryWorkWindow()
			case "retired_operation_pending":
				r.window = newExpiryWorkWindow()
				r.window.operation = &expiryOperationWork{run: r, window: r.window}
				l.retireLocked(r)
			case "retired_metadata_pending":
				r.window = newExpiryWorkWindow()
				r.window.metadata = &expiryMetadataWork{run: r, window: r.window}
				r.window.metadataCount = 1
				l.retireLocked(r)
			// The next four are independent stale-bookkeeping component
			// fields, not a claim that the scheduler can construct them after
			// completion. They isolate the original pure method's conjuncts.
			case "not_completed": w.completed = false
			case "uncleared_operation": w.operation = &expiryOperationWork{run: r, window: w}
			case "nondrained_metadata_count": w.metadataCount = 1
			case "uncleared_metadata_link": w.metadata = &expiryMetadataWork{run: r, window: w}
			case "missing_window": r.window = nil
			}
			got := l.quiescentLocked(r) // ORIGINAL pure method, never copied
			l.mu.Unlock()
			if got != (name == "drained_control") { t.Fatalf("quiescence guard component %s accepted=%t", name, got) }
		})
	}
}

func TestExpiryProposalQuiescenceOldControllerBoundaryComponent(t *testing.T) {
	l := &expiryHostLease{}
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
	old := &expiryLeaseControl{lease: l, predecessor: r, kind: expiryControlRestartClean, stage: expiryControlChecking}
	l.mu.Lock()
	l.current, l.control = r, old
	l.retireLocked(r)
	// quiescentLocked does not examine l.control. An old controller ALONE is
	// not a false condition of that method; do not invent such coverage.
	quiet := l.quiescentLocked(r)
	copied := *old
	copiedAccepted := l.controlLocked(&copied, expiryControlChecking)
	originalAccepted := l.controlLocked(old, expiryControlChecking)
	l.mu.Unlock()
	if !quiet || copiedAccepted || !originalAccepted { t.Fatal("controller identity and quiescence boundaries conflated") }
}
