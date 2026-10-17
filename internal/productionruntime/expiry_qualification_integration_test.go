//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// This file PREPARES native experiments; it is not an execution receipt. Do not
// run until independent actual-source review authorizes the exact source digest.
// No old native receipt transfers. All run roots/lease artifacts are preserved.
type expiryActualACKObserver struct {
	acks []journalv2.AcknowledgedPublicationV2
}

func (o *expiryActualACKObserver) ObservePublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) {
	if snapshot.Stage == journalv2.PublicationAckCreatedBeforeProjectionV2 && snapshot.Ack != nil {
		o.acks = append(o.acks, *snapshot.Ack)
	}
}

func expiryNativeConfig(t *testing.T, observer *expiryActualACKObserver) Config {
	t.Helper()
	if !hexDigest(os.Getenv("SPEC_KITTY_EXPIRY_HOST_SOURCE_REVIEW_SHA256")) {
		t.Skip("native fixture requires independent actual-source review; NOT executed/qualified")
	}
	binary, input, parent := os.Getenv("SPEC_KITTY_EXPIRY_HOST_TEST_IMAGE"), os.Getenv("SPEC_KITTY_EXPIRY_HOST_INPUT"), os.Getenv("SPEC_KITTY_EXPIRY_HOST_RUN_PARENT")
	if binary == "" || input == "" || parent == "" {
		t.Fatal("reviewed native invocation must supply exact TEST/input/private supported-filesystem run parent")
	}
	root, err := os.MkdirTemp(parent, "expiry-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("preserved qualification root: %s; source review: %s", root, os.Getenv("SPEC_KITTY_EXPIRY_HOST_SOURCE_REVIEW_SHA256"))
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0700); err != nil {
		t.Fatal(err)
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return Config{ControlRoot: filepath.Join(root, "authority"), HostAuthorityID: "host-expiry-native", AuthorityBindingOperationID: "operation-expiry-native-binding",
		M1AdoptionRecordPath: filepath.Join(repository, "docs/programs/evidence/m1-adoption.json"),
		Sidecar:              sidecargapdb.LifecycleConfig{ExecutablePath: binary, AdmissionRecordPath: input, RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(root, "authority"), SupervisorRunID: "supervisor-expiry-native-initial", ProductPolicySHA256: repeatByte('e', 64)},
		CapabilityReadiness:  absentReadinessSource{}, DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{},
		PublicationBoundaryObserver: observer, MonitorInterval: 250 * time.Millisecond}
}

// New real coordinator on the ORIGINAL adapter, seeded from independently
// restored durable truth just like the shared suffix. No fake ACK/commit port.
func expiryNativeCoordinator(t *testing.T, h *Host, observer *expiryActualACKObserver) *journalv2.PublicationCoordinatorV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := randomIdentity[string]("restore-expiry-fixture-")
	if err != nil {
		t.Fatal(err)
	}
	owner := h.owner.(*productionOwner)
	restored, err := journalrestore.RestoreV2(t.Context(), journalrestore.InputV2{Reader: expiryRecoveryReaderV2{ReaderV2: owner.adapter}, EventRegistry: registry, Reducers: reducers, HostAuthorityID: owner.binding.HostAuthorityID, PriorRuntimeEpochID: h.RuntimeEpochID(), RecoveryAttemptID: attempt, Budgets: productionRecoveryBudgetsV2()})
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	states := restored.Playback().States()
	entries := make([]journalv2.ProjectionRestoreEntryV2, len(states))
	for i, state := range states {
		entries[i] = journalv2.ProjectionRestoreEntryV2{Coordinate: state.Coordinate(), LogicalVersion: state.LogicalVersion(), LastEventID: state.LastEventID(), CanonicalState: state.CanonicalState(), StateDigest: state.StateDigest(), Tombstone: state.Tombstone()}
	}
	if err := tracker.Restore(journalv2.ProjectionRestoreSeedV2{ThroughRecordRevision: restored.StoreRevision().CurrentRevision(), RollingPrefixDigest: restored.RollingPrefixDigest(), CanonicalProjectionDigest: restored.Playback().CanonicalProjectionDigest(), Entries: entries}); err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorWithObserverAndSinkV2(owner.adapter, tracker, observer, &liveProofSink{host: h})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

// Fixture-only census of actual sealed semantic row addresses. Counts are never
// supplied to production proof logic; latest overwrites occupy the same address.
func expiryFixtureFinalPublicationRows(acks []journalv2.AcknowledgedPublicationV2) uint64 {
	type address struct {
		role   journalv2.SealedRecordRole
		target journalv2.SealedRecordTargetV2
	}
	rows := make(map[address]struct{})
	for _, ack := range acks {
		for _, row := range ack.Plan().Records() {
			rows[address{row.Role(), row.Target()}] = struct{}{}
		}
	}
	return uint64(len(rows))
}

// The fixed initial+continuation fixture has one binding, two journal events,
// one overwritten operation head, one permanent claim and two witnesses. Private
// metadata rows are separate and must not be charged as publication records.
const expiryFixtureContinuationPublicationRows uint64 = 7

func expiryNativeTTLClosure(t *testing.T, h *Host, run *expiryHostRun, acks []journalv2.AcknowledgedPublicationV2, privateRows, expectedPublicationRows uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	proof, err := h.VerifyRetainedRecords(ctx, acks)
	if err != nil {
		t.Fatal("baseline matched", err)
	}
	publicationRows := expiryFixtureFinalPublicationRows(acks)
	if publicationRows != expectedPublicationRows {
		t.Fatalf("fixture final publication rows=%d expected=%d ACKs=%d", publicationRows, expectedPublicationRows, len(acks))
	}
	if proof.MatchedPrivateRecords() != privateRows || proof.MatchedPublicationRecords() != publicationRows || proof.MatchedRecords() != privateRows+publicationRows {
		t.Fatalf("actual baseline closure private=%d publication=%d total=%d; expected %d+%d", proof.MatchedPrivateRecords(), proof.MatchedPublicationRecords(), proof.MatchedRecords(), privateRows, publicationRows)
	}
	owner := h.owner.(*productionOwner)
	expires := time.Now().UTC().Add(2 * time.Second)
	inserted, err := owner.adapter.ConditionalMutate(ctx, "expiry-host-extra-insert", storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: "spk/expiry-evidence/extra", Value: []byte("expiry-host-extra-v1"), Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, ExpiresAt: &expires}, storegapdb.AckDurable)
	if err != nil || inserted.Revision == 0 || inserted.Ack != storegapdb.AckDurable || inserted.DurableThrough < inserted.Revision || inserted.MutationCount != 1 {
		t.Fatalf("actual future TTL insertion: %v %+v", err, inserted)
	}
	if err := h.refreshProjection(ctx); err != nil {
		t.Fatal(err)
	}
	if err := run.gate.WaitHeld(ctx); err != nil {
		t.Fatal("actual H", err)
	}
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
	public, publicErr := h.VerifyRetainedRecords(ctx, acks)
	refusal, sanitized := publicErr.(*ProgressError)
	if !reflect.ValueOf(public).IsZero() || !sanitized || refusal == nil || refusal.Kind != ProgressIncomplete || refusal.Field != "retained_records" || publicErr.Error() != proofIncomplete("retained_records").Error() {
		t.Fatalf("held public refusal not exact zero/sanitized retained_records: %T %v", publicErr, publicErr)
	}
	// NEW canonical armed observation: no reuse of the failed public session.
	if err := run.observeS1Refusal(ctx, acks); err != nil {
		t.Fatal("complete private S1/S2/terminal/Consume/outer closure", err)
	}
	if err := run.release(ctx); err != nil {
		t.Fatal("actual R/A", err)
	}
	// A is not expiry publication. Require real revision advancement separately.
	for {
		status, err := owner.adapter.Inspect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.DatabaseID != p.DatabaseID || status.OwnerPID != p.Process.PID || status.OwnerStartedAt != p.Observation.Status.OwnerStartedAt || status.Lifecycle != "ready" || !status.Healthy {
			t.Fatal("post-A original owner lost")
		}
		if status.CurrentRevision > inserted.Revision && status.DurableThrough == status.CurrentRevision {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no real durable expiry revision", ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := owner.adapter.Get(ctx, "spk/expiry-evidence/extra"); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		t.Fatal("post-expiry extra exists", err)
	}
	if err := h.refreshProjection(ctx); err != nil {
		t.Fatal(err)
	}
	if err := run.observeMatchedAfterExpiry(ctx, acks, inserted.Revision); err != nil {
		t.Fatal("fresh refreshed matched closure", err)
	}
	// Separate post-release fixture assertion (not renewal of the consumed
	// private observation); its additional work remains owned/unqualified.
	refreshed, err := h.VerifyRetainedRecords(ctx, acks)
	if err != nil || refreshed.MatchedPrivateRecords() != privateRows || refreshed.MatchedPublicationRecords() != publicationRows || refreshed.MatchedRecords() != privateRows+publicationRows {
		t.Fatalf("refreshed exact closure counts: %v", err)
	}
}

func TestExpiryHostNativeRequiredInitialCleanUncleanCrossProduct(t *testing.T) {
	for _, mode := range []string{"initial", "clean", "unclean"} {
		for _, actual := range []bool{false, true} {
			name := mode + "/private_only"
			if actual {
				name = mode + "/actual_ACK"
			}
			t.Run(name, func(t *testing.T) {
				observer := &expiryActualACKObserver{}
				config := expiryNativeConfig(t, observer)
				h, run, err := startExpiryHostQualification(t.Context(), config)
				if err != nil {
					t.Fatal(err)
				}
				closed := false
				t.Cleanup(func() {
					if !closed {
						if err := run.close(context.Background()); err != nil {
							t.Errorf("failed-run cleanup/residual (never PASS): %v", err)
						}
					}
				})
				var first journalv2.AcknowledgedPublicationV2
				if actual {
					first = publishLiveProofOperation(t, h, expiryNativeCoordinator(t, h, observer), nil)
				}
				privateRows := uint64(6)
				if mode != "initial" {
					oldHost, oldRun := h, run
					if mode == "clean" {
						h, run, err = run.restartClean(t.Context(), "supervisor-expiry-native-clean")
					} else {
						h, run, err = run.restartUnclean(t.Context(), "supervisor-expiry-native-unclean")
					}
					if err != nil {
						run = oldRun
						t.Fatal(err)
					}
					privateRows = 9
					if proof, err := oldHost.VerifyRetainedRecords(t.Context(), observer.acks); err == nil || !reflect.ValueOf(proof).IsZero() {
						t.Fatal("stale predecessor earned public proof")
					}
					if err := oldRun.gate.CheckHeld(t.Context()); err == nil {
						t.Fatal("stale gate held authority")
					}
					if oldRun.ctx.Err() == nil || run.ctx.Err() != nil || run.lease.parent.Err() != nil {
						t.Fatal("successor derived from revoked predecessor context")
					}
				}
				if actual {
					_ = publishLiveProofOperation(t, h, expiryNativeCoordinator(t, h, observer), &first)
					if len(observer.acks) != 2 {
						t.Fatal("real initial+continuation ACKs not captured")
					}
				}
				publicationRows := uint64(0)
				if actual {
					publicationRows = expiryFixtureContinuationPublicationRows
				}
				expiryNativeTTLClosure(t, h, run, observer.acks, privateRows, publicationRows)
				if err := run.close(context.Background()); err != nil {
					closed = true
					t.Fatal("final FIRST cleanup outcome", err)
				}
				closed = true
			})
		}
	}
}

func TestExpiryHostNativeReconcilerActualACKsIncluded(t *testing.T) {
	observer := &expiryActualACKObserver{}
	config := expiryNativeConfig(t, observer)
	config.Reconciler = &missionCreatingReconciler{}
	h, run, err := startExpiryHostQualification(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			if err := run.close(context.Background()); err != nil {
				t.Errorf("failed-run cleanup: %v", err)
			}
		}
	})
	if len(observer.acks) == 0 {
		t.Fatal("genuine reconciliation ACK missing")
	}
	publicationRows := expiryFixtureFinalPublicationRows(observer.acks)
	if publicationRows == 0 {
		t.Fatal("genuine reconciliation published no sealed rows")
	}
	expiryNativeTTLClosure(t, h, run, observer.acks, 6, publicationRows)
	if err := run.close(context.Background()); err != nil {
		closed = true
		t.Fatal(err)
	}
	closed = true
}
