package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	restore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// This test is deliberately the only GapDB/projection join in WP11. The
// production packages remain directionally independent; WP13's composition
// root will own the corresponding runtime wiring.
func TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	reducers := outboxProjectionReducersV2(t, registry)
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	memory := newV2AtomicMemory()
	// The execution fixture is conditioned on a real finalized-work eligibility
	// observation at revision 17. Seed that authoritative prerequisite exactly;
	// the source and outbox publications themselves still travel through the
	// production GapDB adapter and coordinator below.
	executionSource, source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
	memory.revision = 17
	for _, guard := range executionSource.EligibilityGuards() {
		key, keyErr := v2EligibilityGuardKey(guard.MissionID, guard.ShardID)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		memory.records[key.String()] = gapdbapi.NewRecord(key.String(), guard.Canonical, gapdbapi.Revision(guard.ExpectedRecordRevision), nil)
	}
	provider := memory.provider()
	commit := provider.batchFn
	calls := 0
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls++
		result, commitErr := commit(ctx, batch)
		if commitErr == nil && calls == 4 {
			return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF}
		}
		return result, commitErr
	}
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "db-outbox-restore", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	provider.scanFn = func(_ context.Context, prefix string, limit int, cursor string) (gapdbapi.ScanPage, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		keys := make([]string, 0, len(memory.records))
		for key := range memory.records {
			if strings.HasPrefix(key, prefix) && key > cursor {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		truncated := len(keys) > limit
		if truncated {
			keys = keys[:limit]
		}
		records := make([]gapdbapi.Record, len(keys))
		for index, key := range keys {
			records[index] = memory.records[key].Clone()
		}
		next := ""
		if truncated {
			next = keys[len(keys)-1]
		}
		return gapdbapi.ScanPage{ObservedRevision: memory.revision, AsOf: time.Date(2026, 9, 5, 12, 30, 0, 0, time.UTC), Records: records, Cursor: next, Truncated: truncated}, nil
	}
	adapter, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := coordinator.Publish(t.Context(), executionSource); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	wrongIntent, err := controlstore.BindDurableOutboxIntentV2(source, 99, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	wrongCommand := bindGapDBOutboxCommandV2(t, controlstore.OutboxCommandV2{Action: controlstore.OutboxStartAttemptV2, Intent: wrongIntent, AttemptID: "attempt-wrong-proof", RuntimeEpochID: "epoch-outbox", OperationEventID: "event-wrong-proof-operation", OutboxEventID: "event-wrong-proof-outbox"}, "wrong-proof")
	wrongCapability, err := controlstore.BuildOutboxAuthorityPlanV2(wrongCommand, registry)
	if err != nil {
		t.Fatal(err)
	}
	wrongPlan, _ := wrongCapability.Publication()
	if _, err := adapter.CommitV2(t.Context(), wrongPlan); !IsKind(err, OutcomeConflict) || memory.revision != 19 {
		t.Fatalf("unproven source intent committed: revision=%d err=%v", memory.revision, err)
	}
	durable, err := controlstore.BindDurableOutboxIntentV2(source, 19, coordinates)
	if err != nil {
		t.Fatal(err)
	}
	command := controlstore.OutboxCommandV2{
		Action: controlstore.OutboxStartAttemptV2, Intent: durable,
		AttemptID: "attempt-restorable", RuntimeEpochID: "epoch-outbox",
		OperationEventID: "event-outbox-operation-1", OutboxEventID: "event-outbox-1",
	}
	command = bindGapDBOutboxCommandV2(t, command, "restorable")
	capability, err := controlstore.BuildOutboxAuthorityPlanV2(command, registry)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newOutboxAuthorityV2(adapter, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := authority.PublishOutbox(t.Context(), capability)
	if err != nil || ack.AuthorityRevision != 20 || coordinator.UnresolvedCount() != 0 || memory.batchCalls != 4 {
		t.Fatalf("ack=%+v unresolved=%d batches=%d err=%v", ack, coordinator.UnresolvedCount(), memory.batchCalls, err)
	}
	exact, err := authority.ResolveOutboxOperation(t.Context(), command.OperationBinding())
	if err != nil || exact.Kind != controlstore.OutboxOperationExactV2 || exact.State.Status() != journalv2.StatusResolved {
		t.Fatalf("exact replay=%+v err=%v", exact, err)
	}
	changed := command
	changed.AttemptID = "attempt-substituted"
	changed = bindGapDBOutboxCommandV2(t, changed, "restorable")
	conflict, err := authority.ResolveOutboxOperation(t.Context(), changed.OperationBinding())
	if err != nil || conflict.Kind != controlstore.OutboxOperationConflictV2 {
		t.Fatalf("changed binding=%+v err=%v", conflict, err)
	}

	recovered, err := restore.RestoreV2(t.Context(), restore.InputV2{
		Reader: adapter, EventRegistry: registry, Reducers: reducers,
		HostAuthorityID: "host-outbox", RecoveryAttemptID: "restore-outbox-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	readModel, err := projection.NewAuthorityV2("db-outbox-restore", registry, reducers)
	if err != nil {
		t.Fatal(err)
	}
	if err := readModel.Restore("epoch-restored", recovered); err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("outbox", journalv2.AggregateID(coordinates.EffectID), journalv2.Scope{Kind: journalv2.ScopeHost})
	view, err := readModel.Get(projection.GetQueryV2{Coordinate: coordinate, AtRevision: 20})
	if err != nil {
		t.Fatal(err)
	}
	state, err := controlstore.DecodeOutboxDeliveryStateV2(view.CanonicalState)
	if err != nil || state.Stage != controlstore.OutboxAttemptStartedV2 || state.AttemptID != "attempt-restorable" || state.SourcePublicationID != source.PublicationID() {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	durableObservation, err := authority.ObserveOutboxState(t.Context(), coordinate)
	if err != nil || durableObservation.State == nil || durableObservation.RecordRevision != 20 {
		t.Fatalf("durable observation=%+v err=%v", durableObservation, err)
	}
	recoveredIntent, err := controlstore.RecoverDurableOutboxIntentV2(*durableObservation.State)
	if err != nil {
		t.Fatal(err)
	}
	unavailable, err := controlstore.NewOutboxProviderObservationV2(0, "", "", "unavailable")
	if err != nil {
		t.Fatal(err)
	}
	continuation := controlstore.OutboxCommandV2{Action: controlstore.OutboxRecordObservationV2, Intent: recoveredIntent, Prior: durableObservation.State, PriorRecordRevision: durableObservation.RecordRevision, AttemptID: state.AttemptID, RuntimeEpochID: "epoch-restored", Observation: &unavailable, OperationEventID: "event-restored-observation-operation", OutboxEventID: "event-restored-observation-outbox"}
	continuation = bindGapDBOutboxCommandV2(t, continuation, "restored-observation")
	continuationCapability, err := controlstore.BuildOutboxAuthorityPlanV2(continuation, registry)
	if err != nil {
		t.Fatal(err)
	}
	continuationPlan, _ := continuationCapability.Publication()
	if len(continuationPlan.Assertions()) != 0 {
		t.Fatal("continuation incorrectly required the source head to remain current")
	}
	continuationAck, err := authority.PublishOutbox(t.Context(), continuationCapability)
	if err != nil || continuationAck.AuthorityRevision != 21 || memory.batchCalls != 5 {
		t.Fatalf("continuation=%+v batches=%d err=%v", continuationAck, memory.batchCalls, err)
	}
}

func outboxProjectionReducersV2(t *testing.T, registry journalv2.EventRegistryV2) journalv2.ReducerSetV2 {
	t.Helper()
	owned := []journalv2.ReducerRegistration{{Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Owner: "internal/controljournal/application", ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2}}
	owned = append(owned, journalv2.ReducerRegistration{Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/mission", ImplementationDigest: mission.MissionReducerImplementationDigestV2(), Reduce: mission.ReduceMissionEventV2})
	owned = append(owned, artifact.ReducerRegistrationsV2()...)
	owned = append(owned, workgraph.ReducerRegistrationsV2()...)
	owned = append(owned, execution.ExecutionReducerRegistrationsV2()...)
	owned = append(owned, workspace.ReducerRegistrationsV2()...)
	owned = append(owned, handoff.ReducerRegistrationsV2()...)
	owned = append(owned, review.ReducerRegistrationsV2()...)
	owned = append(owned, integration.AcceptanceReducerRegistrationsV2()...)
	owned = append(owned, integration.IntegrationReducerRegistrationsV2()...)
	owned = append(owned, execution.EffectReducerRegistrationsV2()...)
	owned = append(owned, controlstore.OutboxReducerRegistrationsV2()...)
	byFamily := make(map[string]journalv2.ReducerRegistration, len(owned))
	for _, registration := range owned {
		if prior := byFamily[registration.Family]; prior.Reduce != nil {
			t.Fatalf("duplicate production reducer registration for %s", registration.Family)
		}
		byFamily[registration.Family] = registration
	}
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		registration := byFamily[tuple.Family]
		if tuple.Family == "runtime_host" { // WP13 owns the only not-yet-migrated family.
			sum := sha256.Sum256([]byte("wp11-test-reducer/" + tuple.Family))
			digest, _ := journalv2.ParseDigest("test_reducer", hex.EncodeToString(sum[:]))
			registration = journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: func(_ []byte, _ journalv2.EventEnvelopeV2) ([]byte, error) {
				return nil, fmt.Errorf("runtime_host is owned by WP13")
			}}
		}
		if registration.Family != tuple.Family || registration.EventSchemaVersion != tuple.EventSchemaVersion || registration.ReducerVersion != tuple.ReducerVersion || registration.Owner != tuple.Owner || registration.Reduce == nil || registration.ImplementationDigest.IsZero() {
			t.Fatalf("missing exact production reducer for %s", tuple.Family)
		}
		registrations = append(registrations, registration)
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return reducers
}

// TestWP11ExecutableCapabilityConformanceV2 is intentionally an executable
// matrix, not a list of test names. Each row runs an accepted path and a
// refusal/fault path through the capability's consumer-owned production port.
// This makes a deleted, renamed, or weakened capability test observable here
// and keeps the final composition proof tied to behavior rather than prose.
func TestWP11ExecutableCapabilityConformanceV2(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded capability campaign is excluded from the short PR tier")
	}
	type evidence struct {
		name     string
		families []string
		accepted func(*testing.T)
		refused  func(*testing.T)
	}
	matrix := []evidence{
		{"mission_decision_blocker", []string{"mission"}, TestMissionAuthorityAdapterOneBatchRestartReplayV2, TestMissionAuthorityAdapterBindingConflictPrecedesMissionReadV2},
		{"readiness_actions", []string{"mission"}, TestReadinessAuthorityAdapterCreateActivityPauseRestartReplayV2, TestReadinessAuthorityAdapterRejectsLegacyPhaseStateV2},
		{"artifact", []string{"artifact_catalog", "artifact_revision"}, TestArtifactAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB, TestArtifactAuthorityRefusesNil},
		{"workrevision_reconciliation", []string{"workgraph"}, TestWorkgraphAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB, TestWorkgraphAuthorityRefusesNil},
		{"run_lease_governance", []string{"execution_slot"}, TestExecutionAndEffectAuthoritiesProductionConstructorsCommitAndRecoverAmbiguityV2, TestAuthorityAdapterNeverDeliversAfterPreRegistrationRefusalV2},
		{"workspace", []string{"workspace"}, TestWorkspaceAuthorityCommitsBindingAndPermanentPathClaimThroughGapDBV2, TestWorkspacePathClaimContentionAndIndependentProgressV2},
		{"handoff", []string{"handoff_slot"}, TestHandoffGapDBPublisherCommitsOneCompleteBatchAndContendsV2, TestHandoffGapDBPartialPublicationRemainsUnresolvedV2},
		{"independent_review", []string{"review_slot"}, TestReviewGapDBPublisherCommitsOneCompleteBatchAndContendsV2, TestReviewGapDBPartialPublicationRemainsUnresolvedV2},
		{"acceptance_integration", []string{"acceptance", "integration_target", "integration_attempt"}, TestAcceptanceAndIntegrationPublishThroughRealGapDBAndReconcileLostCASV2, TestAcceptanceAndIntegrationGapDBRejectPartialAndMixedPublicationsV2},
		{"external_effect", []string{"external_effect"}, TestExternalProviderRunsThroughRealGapDBEffectAuthorityV2, TestEffectDispatchAuthorizationIsDurableOneShotThroughGapDBV2},
		{"outbox_projection", []string{"outbox"}, TestOutboxGapDBHistoryRestoresDisposableProjectionAfterDroppedAckV2, TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2},
	}
	covered := map[string]string{"operation": "global_operation"}
	for _, row := range matrix {
		row := row
		if row.accepted == nil || row.refused == nil || len(row.families) == 0 {
			t.Fatalf("incomplete executable conformance row %s", row.name)
		}
		for _, family := range row.families {
			if prior := covered[family]; prior != "" && family != "mission" {
				t.Fatalf("family %s is ambiguously evidenced by %s and %s", family, prior, row.name)
			}
			covered[family] = row.name
		}
		t.Run(row.name+"/accepted", row.accepted)
		t.Run(row.name+"/refused", row.refused)
	}
	for _, tuple := range journalv2.ExpectedEventTuples() {
		if tuple.Family == "runtime_host" { // WP13 owns runtime-host composition.
			continue
		}
		if covered[tuple.Family] == "" {
			t.Fatalf("registered migrated family %s lacks executable accepted/refused evidence", tuple.Family)
		}
	}
	// Constructing this set verifies that the projection joins the exact real
	// reducer implementation for every covered family. Synthetic pass-through
	// reducers cannot satisfy this call.
	_ = outboxProjectionReducersV2(t, journalv2.DefaultEventRegistryV2())
}

func outboxGapDBSourceFixtureV2(t *testing.T, registry journalv2.EventRegistryV2) (journalv2.SealedCommitPlanV2, journalv2.SealedCommitPlanV2, controlstore.OutboxIntentCoordinatesV2) {
	t.Helper()
	executionCapability, slot, _, _ := gapdbExecutionAdmissionPlanFixtureV2(t, "outbox-source", "work-outbox-source")
	executionPlan, err := executionCapability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	request := gapdbEffectRequestForSlotV2(t, "outbox-source", slot)
	capability := gapdbEffectPlanFixtureForSlotV2(t, "outbox-source", slot, 18)
	plan, err := capability.Publication()
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.BindingCreate()
	if binding == nil || binding.OperationID() != request.Intent.OperationID {
		t.Fatal("effect source fixture omitted its operation binding")
	}
	return executionPlan, plan, controlstore.OutboxIntentCoordinatesV2{
		EffectID: request.Intent.EffectID, SourceOperationID: request.Intent.OperationID,
		CanonicalIntent: request.Intent.CanonicalBytesV2(), IntendedMutationDigest: request.Intent.CanonicalRequestDigest,
		IntendedValue: request.Intent.IntendedValue, ExpectedProviderRevision: uint64(request.Intent.ExpectedProviderRevision),
		ExpectedValue: request.Intent.ExpectedValue, ExpectedOperationMarker: request.Intent.ExpectedOperationMarker,
		AttributionMarker: string(request.Intent.OperationID), ActorID: journalv2.ActorID(request.Intent.ActorID), Capability: binding.CommandKind(),
	}
}
