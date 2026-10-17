//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/checkpoint"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
)

// COMPONENT ONLY. Both optional interfaces are genuinely implemented, but the
// actual Restore receives the production expiryRecoveryReaderV2 mask. No Host,
// provider response, authentication, timeout or context is simulated as Native.
// The Read fault uses a valid slot reference to reach the existing exact-read
// API; no claim is made that its deliberately unavailable checkpoint is valid.
type expiryRecoveryFailureReader struct {
	attempt journalv2.StoreRevisionV2
	slot journalv2.RecoveryRecordV2
	site, mode string
	terminal, transient error
	captures, scans, reads, verifies, many, snapshots int
	trace []string
	faults []error
}

var (
	_ journalrestore.ReaderV2 = (*expiryRecoveryFailureReader)(nil)
	_ journalrestore.ReadManyReaderV2 = (*expiryRecoveryFailureReader)(nil)
	_ journalrestore.RecoverySnapshotReaderV2 = (*expiryRecoveryFailureReader)(nil)
)

func (r *expiryRecoveryFailureReader) fault(site string) error {
	if site != r.site { return nil }
	var err error
	switch r.mode {
	case "first_error_identity":
		err = r.terminal
	case "two_prior_restarts_then_first_terminal":
		if r.captures <= 2 { err = r.transient } else { err = r.terminal }
	case "retry_budget_exhaustion":
		err = r.transient
	default:
		panic("unknown component fault mode")
	}
	r.faults = append(r.faults, err) // includes every actual dispatch, never inferred
	return err
}
func (r *expiryRecoveryFailureReader) CaptureRevisionV2(context.Context) (journalv2.StoreRevisionV2, error) {
	r.captures++
	r.trace = append(r.trace, "Capture")
	if err := r.fault("capture"); err != nil { return journalv2.StoreRevisionV2{}, err }
	return r.attempt, nil
}
func (r *expiryRecoveryFailureReader) VerifyRevisionV2(_ context.Context, attempt journalv2.StoreRevisionV2) error {
	r.verifies++
	r.trace = append(r.trace, "Verify")
	if attempt != r.attempt { return errors.New("component verify capture changed") }
	return r.fault("verify")
}
func (r *expiryRecoveryFailureReader) ScanV2(_ context.Context, request journalv2.ScanRequestV2) (journalv2.RecoveryPageV2, error) {
	r.scans++
	r.trace = append(r.trace, "Scan:"+string(request.Target().Kind()))
	if request.AttemptRevision() != r.attempt || len(request.Cursor()) != 0 {
		return journalv2.RecoveryPageV2{}, errors.New("component scan capture/cursor changed")
	}
	if err := r.fault("scan"); err != nil { return journalv2.RecoveryPageV2{}, err }
	var records []journalv2.RecoveryRecordV2
	if r.site == "read" && request.Target().Kind() == journalv2.ScanCheckpointSlots {
		records = []journalv2.RecoveryRecordV2{r.slot}
	}
	return journalv2.NewRecoveryPageV2(r.attempt.CurrentRevision(), "2026-01-01T00:00:00Z", records, nil, false)
}
func (r *expiryRecoveryFailureReader) ReadV2(_ context.Context, request journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	r.reads++
	r.trace = append(r.trace, "Read:"+string(request.Address().Kind()))
	if request.AttemptRevision() != r.attempt || request.Address().Kind() != journalv2.AddressCheckpointRecord || request.Address().CheckpointID() != "checkpoint-expiry-fault" {
		return journalv2.RecoveryRecordV2{}, errors.New("component exact-read request changed")
	}
	if err := r.fault("read"); err != nil { return journalv2.RecoveryRecordV2{}, err }
	return journalv2.RecoveryRecordV2{}, errors.New("unexpected successful read route")
}
func (r *expiryRecoveryFailureReader) ReadManyV2(context.Context, journalv2.ReadManyRequestV2) (journalv2.RecoveryReadManyResultV2, error) {
	r.many++
	return journalv2.RecoveryReadManyResultV2{}, errors.New("FORBIDDEN optional ReadMany dispatch")
}
func (r *expiryRecoveryFailureReader) ReadRecoverySnapshotV2(context.Context, journalv2.RecoverySnapshotRequestV2) (journalv2.RecoverySnapshotV2, error) {
	r.snapshots++
	return journalv2.RecoverySnapshotV2{}, errors.New("FORBIDDEN optional Snapshot dispatch")
}

func expiryNewRecoveryFailureReader(t *testing.T, site, mode string) *expiryRecoveryFailureReader {
	t.Helper()
	attempt, err := journalv2.NewStoreRevisionV2("database-expiry-component", 2, 2)
	if err != nil { t.Fatal(err) }
	address, err := journalv2.NewCheckpointSlotAddressV2("slot-expiry-fault")
	if err != nil { t.Fatal(err) }
	value, err := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"schema_version":2,"slot_id":"slot-expiry-fault","checkpoint_id":"checkpoint-expiry-fault","checkpoint_sha256":%q,"through_record_revision":1}`, strings.Repeat("1", 64))))
	if err != nil { t.Fatal(err) }
	if _, err := checkpoint.DecodeSlotV2(value); err != nil { t.Fatal("Read placement would be unreachable:", err) }
	slot, err := journalv2.NewRecoveryRecordV2(address, value, 2, "")
	if err != nil { t.Fatal(err) }
	return &expiryRecoveryFailureReader{attempt: attempt, slot: slot, site: site, mode: mode,
		terminal: errors.New("FIRST terminal selected ReaderV2 failure/"+site),
		transient: &journalv2.Error{Kind: journalv2.ErrorRevisionMoved, Field: "component-selected-"+site}}
}

func expiryRunRecoveryFailure(t *testing.T, reader *expiryRecoveryFailureReader) (journalrestore.ResultV2, error) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil { t.Fatal(err) }
	var masked journalrestore.ReaderV2 = expiryRecoveryReaderV2{ReaderV2: reader}
	if _, ok := masked.(journalrestore.ReadManyReaderV2); ok { t.Fatal("mask exposes ReadMany") }
	if _, ok := masked.(journalrestore.RecoverySnapshotReaderV2); ok { t.Fatal("mask exposes Snapshot") }
	// No new durations, cancellation tricks, reduced timeout or enlarged caps.
	return journalrestore.RestoreV2(context.Background(), journalrestore.InputV2{Reader: masked,
		EventRegistry: registry, Reducers: reducers, HostAuthorityID: "host-expiry-component",
		RecoveryAttemptID: "attempt-expiry-component", Budgets: journalrestore.DefaultBudgetsV2()})
}

func TestExpiryRecoverySelectedReaderFailuresNoOptionalFallback(t *testing.T) {
	for _, site := range []string{"capture", "scan", "read", "verify"} {
		for _, mode := range []string{"first_error_identity", "two_prior_restarts_then_first_terminal", "retry_budget_exhaustion"} {
			t.Run(site+"/"+mode, func(t *testing.T) {
				r := expiryNewRecoveryFailureReader(t, site, mode)
				result, err := expiryRunRecoveryFailure(t, r)
				if !reflect.ValueOf(result).IsZero() { t.Fatal("failed Restore emitted a result") }
				attempts := 1
				if mode != "first_error_identity" { attempts = journalrestore.DefaultBudgetsV2().MaximumRestarts }
				if attempts != 1 && attempts != 3 { t.Fatal("original restart bound drift") }
				if mode == "retry_budget_exhaustion" {
					var exhausted *journalrestore.Error
					if !errors.As(err, &exhausted) || exhausted.Kind != journalrestore.ErrorUnstableStore || exhausted.Field != "restarts" || exhausted.Attempts != attempts {
						t.Fatalf("wrong bounded retry outcome: %#v", err)
					}
				} else if err != r.terminal {
					t.Fatalf("FIRST terminal error identity lost: got=%#v want=%p", err, r.terminal)
				}
				if r.captures != attempts || len(r.faults) != attempts || r.many != 0 || r.snapshots != 0 {
					t.Fatalf("attempt/fallback census capture=%d faults=%d optional=%d/%d trace=%v", r.captures, len(r.faults), r.many, r.snapshots, r.trace)
				}
				for i, fault := range r.faults {
					want := r.terminal
					if mode == "retry_budget_exhaustion" || mode == "two_prior_restarts_then_first_terminal" && i < 2 { want = r.transient }
					if fault != want { t.Fatalf("dispatch %d first error identity changed", i) }
				}
				var one []string
				switch site {
				case "capture": one = []string{"Capture"}
				case "scan": one = []string{"Capture", "Scan:checkpoint_slots"}
				case "read": one = []string{"Capture", "Scan:checkpoint_slots", "Read:"+string(journalv2.AddressCheckpointRecord)}
				case "verify": one = []string{"Capture", "Scan:checkpoint_slots", "Scan:all_aggregate_heads", "Scan:all_operation_records", "Verify"}
				}
				var wantTrace []string
				for i := 0; i < attempts; i++ { wantTrace = append(wantTrace, one...) }
				if !reflect.DeepEqual(r.trace, wantTrace) { t.Fatalf("dispatch order=%v want=%v", r.trace, wantTrace) }
			})
		}
	}
}

func TestExpiryRecoveryMaskedEmptyComponentSuccess(t *testing.T) {
	r := expiryNewRecoveryFailureReader(t, "", "")
	result, err := expiryRunRecoveryFailure(t, r)
	if err != nil { t.Fatal(err) }
	admission, err := result.FreshEpochAdmissionRequestV2()
	if err != nil || admission.DatabaseID() != r.attempt.DatabaseID() || admission.ThroughRecordRevision() != 2 {
		t.Fatal("actual component Restore did not seal its captured coordinate", err)
	}
	if r.captures != 1 || r.scans != 3 || r.verifies != 1 || r.reads != 0 || r.many != 0 || r.snapshots != 0 || len(r.faults) != 0 {
		t.Fatalf("unexpected empty component dispatch: %+v", r)
	}
}

func expirySourceFunctionBody(t *testing.T, path, name string) string {
	t.Helper()
	var source []byte
	if path == "expiry_qualification.go" {
		source = []byte(expiryCompatReadSelected(t))
	} else {
		var err error
		source, err = os.ReadFile(path)
		if err != nil { t.Fatal(err) }
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, source, 0)
	if err != nil { t.Fatal(err) }
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Body != nil {
			return string(source[set.Position(fn.Body.Pos()).Offset:set.Position(fn.Body.End()).Offset])
		}
	}
	t.Fatal("source function missing:", name)
	return ""
}

func expirySourceCompact(value string) string {
	return strings.Map(func(r rune) rune { if unicode.IsSpace(r) { return -1 }; return r }, value)
}

func TestExpiryRootBootstrapSourcePropertiesNotNativeFaults(t *testing.T) {
	// These assertions characterize actual source predicates only. Authenticated
	// TEST cannot honestly return contradictory bootstrap values on request;
	// no fabricated response/callback is introduced to clear Native obligations.
	selected := expiryCompatReadSelected(t)
	if err := expiryBootstrapW5Freshness(selected); err != nil { t.Fatal(err) }
	if err := expiryBootstrapW5Cleanup(selected); err != nil { t.Fatal(err) }
	// Every original row remains mandatory, on its exact extracted owner.
	// No inline/helper fallback and no token-only approval of the extraction.
	for _, tc := range []struct { name, source string }{
		{"database_nonempty", `p.DatabaseID == ""`},
		{"data_root_matches", `p.DataDirectory != controlRoot`},
		{"supervisor_matches", `p.SupervisorRunID != supervisor`},
		{"executed_SHA_matches", `p.ExecutedImage.SHA256 != identity.ExecutableSHA256`},
		{"database_status_matches_owner", `p.Observation.Status.DatabaseID != p.DatabaseID`},
		{"owner_pid_matches_process", `p.Observation.Status.OwnerPID != p.Process.PID`},
		{"ready_lifecycle", `p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady`},
		{"initial_revision_zero", `p.Observation.Status.CurrentRevision != 0`},
		{"initial_durable_zero", `p.Observation.Status.DurableThroughRevision != 0`},
		{"initial_count_zero", `p.Observation.Status.RecordCount != 0`},
		{"initial_binding_and_current_absent", `for _, key := range []string{authorityBindingKey, runtimeCurrentKey}`},
		{"absence_authenticated_not_found", `!storegapdb.IsKind(err, storegapdb.OutcomeNotFound)`},
		{"successor_database_original", `database != l.database`},
		{"successor_binding_original", `binding != l.binding`},
		{"actual_helper_initialize_from_original", `ensureAuthorityBinding(ctx, adapter, binding, initial)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := "startGeneration"
			switch tc.name {
			case "database_nonempty", "data_root_matches", "supervisor_matches", "executed_SHA_matches", "database_status_matches_owner", "owner_pid_matches_process", "ready_lifecycle":
				owner = "expiryBootstrapRefused"
			case "initial_revision_zero", "initial_durable_zero", "initial_count_zero":
				owner = "expiryBootstrapNotZero"
			}
			body := expirySourceFunctionBody(t, "expiry_qualification.go", owner)
			if !strings.Contains(expirySourceCompact(body), expirySourceCompact(tc.source)) {
				t.Fatal("source property drift; NOT a Native result:", tc.source)
			}
		})
	}
}

func TestExpiryBindingSuccessorNoOverwriteSourceBoundary(t *testing.T) {
	body := expirySourceCompact(expirySourceFunctionBody(t, "recovery.go", "ensureAuthorityBinding"))
	guard := expirySourceCompact(`if !storegapdb.IsKind(readErr, storegapdb.OutcomeNotFound) || !initialize { return &Error{Kind: ErrorStartup, State: HostRecovering, Field: "authority_binding_missing"} }`)
	mismatch := expirySourceCompact(`if record.Revision == 0 || !bytes.Equal(record.Value, canonical) { return &Error{Kind: ErrorStartup, State: HostRecovering, Field: "authority_binding_mismatch"} }`)
	for _, tc := range []struct { name, fragment string }{{"missing_existing_only", guard}, {"mismatch_existing_only", mismatch}} {
		t.Run(tc.name, func(t *testing.T) {
			position := strings.Index(body, tc.fragment)
			write := strings.Index(body, "port.ConditionalMutate(")
			if position < 0 || write < 0 || position >= write { t.Fatal("refusal no longer precedes sole create-only write") }
			if strings.Count(body, "port.ConditionalMutate(") != 1 { t.Fatal("new mutation seam; no native no-overwrite claim transfers") }
		})
	}
	// Reuse, do not replicate/relabel the existing 124 actual-helper leaves in
	// expiry_qualification_stored_binding_test.go. Their level stays component.
}
