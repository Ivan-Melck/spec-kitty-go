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
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	restore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/projection"
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
	provider := memory.provider()
	commit := provider.batchFn
	calls := 0
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls++
		result, commitErr := commit(ctx, batch)
		if commitErr == nil && calls == 3 {
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

	source, coordinates := outboxGapDBSourceFixtureV2(t, registry)
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
	if _, err := adapter.CommitV2(t.Context(), wrongPlan); !IsKind(err, OutcomeConflict) || memory.revision != 1 {
		t.Fatalf("unproven source intent committed: revision=%d err=%v", memory.revision, err)
	}
	durable, err := controlstore.BindDurableOutboxIntentV2(source, 1, coordinates)
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
	if err != nil || ack.ProviderRevision != 2 || coordinator.UnresolvedCount() != 0 || memory.batchCalls != 3 {
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
	view, err := readModel.Get(projection.GetQueryV2{Coordinate: coordinate, AtRevision: 2})
	if err != nil {
		t.Fatal(err)
	}
	state, err := controlstore.DecodeOutboxDeliveryStateV2(view.CanonicalState)
	if err != nil || state.Stage != controlstore.OutboxAttemptStartedV2 || state.AttemptID != "attempt-restorable" || state.SourcePublicationID != source.PublicationID() {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	durableObservation, err := authority.ObserveOutboxState(t.Context(), coordinate)
	if err != nil || durableObservation.State == nil || durableObservation.RecordRevision != 2 {
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
	if err != nil || continuationAck.ProviderRevision != 3 || memory.batchCalls != 4 {
		t.Fatalf("continuation=%+v batches=%d err=%v", continuationAck, memory.batchCalls, err)
	}
}

func outboxProjectionReducersV2(t *testing.T, registry journalv2.EventRegistryV2) journalv2.ReducerSetV2 {
	t.Helper()
	outbox := controlstore.OutboxReducerRegistrationsV2()[0]
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		registration := journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner}
		switch tuple.Family {
		case "operation":
			registration.ImplementationDigest = journalv2.OperationReducerImplementationDigestV2()
			registration.Reduce = journalv2.ReduceOperationStateV2
		case "outbox":
			registration = outbox
		default:
			sum := sha256.Sum256([]byte("wp11-test-reducer/" + tuple.Family))
			registration.ImplementationDigest, _ = journalv2.ParseDigest("test_reducer", hex.EncodeToString(sum[:]))
			registration.Reduce = func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }
		}
		registrations = append(registrations, registration)
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return reducers
}

func outboxGapDBSourceFixtureV2(t *testing.T, registry journalv2.EventRegistryV2) (journalv2.SealedCommitPlanV2, controlstore.OutboxIntentCoordinatesV2) {
	t.Helper()
	operationID := journalv2.OperationID("operation-outbox-source")
	effectID := journalv2.EffectID("effect-outbox-source")
	actorID := journalv2.ActorID("actor-outbox")
	mutationSum := sha256.Sum256([]byte("outbox mutation"))
	mutationDigest := hex.EncodeToString(mutationSum[:])
	intent, err := journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"actor_id":"%s","canonical_request_digest":"%s","effect_id":"%s","expected_operation_marker":"marker-before","expected_provider_revision":7,"expected_value":"owner-before","intended_value":"owner-after","operation_id":"%s"}`, actorID, mutationDigest, effectID, operationID)))
	if err != nil {
		t.Fatal(err)
	}
	intentSum := sha256.Sum256(intent)
	intentDigest, _ := journalv2.ParseDigest("effect_intent", hex.EncodeToString(intentSum[:]))
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	commandDigest, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "external_effect.saga", CommandSchemaVersion: 1, Scope: scope, Command: intent})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "external_effect.saga", CommandSchemaVersion: 1, CanonicalIntentDigest: commandDigest, Scope: scope, ActorID: actorID, ProcessID: "process-outbox", DoctrineRevision: "doctrine-outbox", ProfileRevision: "profile-outbox", PolicyRevision: "policy-outbox", OriginatingRuntimeEpochID: "epoch-outbox", ObservedCreatedAt: "2026-09-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	eventIDs := []journalv2.EventID{"event-outbox-source-effect", "event-outbox-source-operation"}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusAccepted, EventIDs: eventIDs, EffectIDs: []journalv2.EffectID{effectID}})
	if err != nil {
		t.Fatal(err)
	}
	operationPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	effectPayload := []byte(`{"stage":"intent_recorded"}`)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), scope)
	effectCoordinate, _ := journalv2.NewAggregateCoordinate("external_effect", journalv2.AggregateID(effectID), scope)
	drafts := []journalv2.EventDraftV2{
		{EventID: eventIDs[1], EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-outbox", ActorID: actorID, ProcessID: "process-outbox", PolicyRevision: "policy-outbox", GovernanceRevisions: []string{"doctrine-outbox", "profile-outbox"}, Payload: operationPayload},
		{EventID: eventIDs[0], EventType: "external_effect.record_intent", Family: "external_effect", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: effectCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-outbox", ActorID: actorID, ProcessID: "process-outbox", PolicyRevision: "policy-outbox", GovernanceRevisions: []string{"doctrine-outbox", "profile-outbox"}, Payload: effectPayload},
	}
	operationStateDigest, _ := journalv2.ReducerStateDigestV2(operationPayload)
	effectStateDigest, _ := journalv2.ReducerStateDigestV2(effectPayload)
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, NextLogicalVersion: 1, EventID: eventIDs[1], CommittedRuntimeEpochID: "epoch-outbox", CanonicalStateDigest: operationStateDigest, AuthoritySummary: operationPayload},
		{Coordinate: effectCoordinate, NextLogicalVersion: 1, EventID: eventIDs[0], CommittedRuntimeEpochID: "epoch-outbox", CanonicalStateDigest: effectStateDigest, AuthoritySummary: effectPayload},
	}
	conditions := make([]journalv2.ProviderCondition, 0, len(heads))
	for _, head := range heads {
		condition, conditionErr := journalv2.ConditionForHeadTransitionV2(head)
		if conditionErr != nil {
			t.Fatal(conditionErr)
		}
		conditions = append(conditions, condition)
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: drafts, Heads: heads, EffectIntents: []journalv2.EffectIntentReference{{EffectID: effectID, Digest: intentDigest, Canonical: intent}}, Conditions: conditions}, registry)
	if err != nil {
		t.Fatal(err)
	}
	return plan, controlstore.OutboxIntentCoordinatesV2{EffectID: effectID, SourceOperationID: operationID, CanonicalIntent: intent, IntendedMutationDigest: mutationDigest, IntendedValue: "owner-after", ExpectedProviderRevision: 7, ExpectedValue: "owner-before", ExpectedOperationMarker: "marker-before", AttributionMarker: string(operationID), ActorID: actorID, Capability: "external_effect.saga"}
}
