package gapdb

import (
	"context"
	"errors"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

type missionEpochFixtureV2 struct{ epoch journalv2.RuntimeEpochID }

func (f missionEpochFixtureV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return f.epoch, nil
}

func TestMissionAuthorityAdapterOneBatchRestartReplayV2(t *testing.T) {
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "database-1", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	store, err := newAdapter(provider, "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reducers := missionReducerSetFixtureV2(t)
	projection, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(store, projection)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := NewMissionAuthorityPortV2(store, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-1"}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	state := missionStateFixtureV2(t)
	command := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1",
		ActorID: "actor-1", ProcessID: "process-1", OriginatingRuntimeEpochID: "epoch-1",
		DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: state,
	}
	result, err := service.Execute(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if memory.batchCalls != 1 || result.State().Digest() != state.Digest() {
		t.Fatalf("batch_calls=%d result=%+v", memory.batchCalls, result)
	}
	observed, err := authority.ObserveMission(t.Context(), "project-1", "mission-1")
	if err != nil || observed.State.Digest() != state.Digest() || observed.RecordRevision != 2 {
		t.Fatalf("observed=%+v err=%v", observed, err)
	}

	restarted, _ := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"replacement-epoch"}, journalv2.DefaultEventRegistryV2())
	replay, err := restarted.Execute(t.Context(), command)
	if err != nil || !replay.Replayed() || replay.State().Digest() != state.Digest() || memory.batchCalls != 1 {
		t.Fatalf("replay=%+v batches=%d err=%v", replay, memory.batchCalls, err)
	}
}

func TestMissionAuthorityAdapterBindingConflictPrecedesMissionReadV2(t *testing.T) {
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "database-1", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	store, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := journalv2.NewProjectionTrackerV2(missionReducerSetFixtureV2(t), journalv2.CanonicalCodecVersionV2)
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(store, projection)
	authority, _ := NewMissionAuthorityPortV2(store, coordinator)
	service, _ := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-1"}, journalv2.DefaultEventRegistryV2())
	created := missionStateFixtureV2(t)
	create := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-global", ProjectID: "project-1", MissionID: "mission-1",
		ActorID: "actor-1", ProcessID: "process-1", OriginatingRuntimeEpochID: "epoch-1",
		DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: created,
	}
	// Bind the shared operation ID globally through its first publication.
	createdInput := mission.MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1", DefinitionDigest: created.DefinitionDigest(),
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "specifying", Decomposition: mission.DecompositionNoneV2,
		LastOperationID: "operation-global", ObservedAt: "2026-09-04T12:00:00Z",
	}
	create.Successor, _ = mission.NewMissionAuthorityStateV2(createdInput)
	if _, err := service.Execute(t.Context(), create); err != nil {
		t.Fatal(err)
	}

	// A different mission reusing the OperationID must stop after the exact
	// global binding read. If resolution were mission-scoped or reordered, this
	// lifecycle command would proceed to a mission-head read.
	priorInput := createdInput
	priorInput.MissionID = "mission-other"
	priorInput.LastOperationID = "operation-prior"
	prior, _ := mission.NewMissionAuthorityStateV2(priorInput)
	nextInput := priorInput
	nextInput.Version = 2
	nextInput.Step = "planning"
	nextInput.LastOperationID = "operation-global"
	next, _ := mission.NewMissionAuthorityStateV2(nextInput)
	collision := create
	collision.MissionID = "mission-other"
	collision.EventType = mission.MissionEventLifecycleAdvancedV2
	collision.Expected = prior
	collision.Successor = next
	beforeReads, beforeBatches := memory.getCalls, memory.batchCalls
	if _, err := service.Execute(t.Context(), collision); !mission.IsMissionAuthorityKindV2(err, mission.MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("global binding collision=%v", err)
	}
	if memory.getCalls != beforeReads+1 || memory.batchCalls != beforeBatches {
		t.Fatalf("binding collision crossed mission/mutation boundary: reads=%d/%d batches=%d/%d", beforeReads, memory.getCalls, beforeBatches, memory.batchCalls)
	}
}

func TestMissionAuthorityAdapterResponseLossReconcilesExactPublicationV2(t *testing.T) {
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	originalBatch := provider.batchFn
	provider.batchFn = func(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		_, err := originalBatch(ctx, batch)
		if err != nil {
			return gapdbapi.MutationResult{}, err
		}
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Cause: errors.New("response lost")}
	}
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "database-1", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	store, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := journalv2.NewProjectionTrackerV2(missionReducerSetFixtureV2(t), journalv2.CanonicalCodecVersionV2)
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(store, projection)
	authority, _ := NewMissionAuthorityPortV2(store, coordinator)
	service, _ := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-1"}, journalv2.DefaultEventRegistryV2())
	state := missionStateFixtureV2(t)
	command := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1",
		OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: state,
	}
	result, err := service.Execute(t.Context(), command)
	if err != nil || result.State().Digest() != state.Digest() || memory.batchCalls != 1 || coordinator.UnresolvedCount() != 0 {
		t.Fatalf("result=%+v batches=%d unresolved=%d err=%v", result, memory.batchCalls, coordinator.UnresolvedCount(), err)
	}
}

func TestMissionAuthorityAdapterPartialPublicationClosesAuthorityV2(t *testing.T) {
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	provider.batchFn = func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		memory.batchCalls++
		memory.revision++
		limit := len(batch.Mutations) / 2
		for _, mutation := range batch.Mutations[:limit] {
			memory.records[mutation.Key] = gapdbapi.NewRecord(mutation.Key, mutation.Value, memory.revision, nil)
		}
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Cause: errors.New("partial response")}
	}
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "database-1", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	store, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := journalv2.NewProjectionTrackerV2(missionReducerSetFixtureV2(t), journalv2.CanonicalCodecVersionV2)
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(store, projection)
	authority, _ := NewMissionAuthorityPortV2(store, coordinator)
	service, _ := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-1"}, journalv2.DefaultEventRegistryV2())
	state := missionStateFixtureV2(t)
	command := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1",
		OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: state,
	}
	if _, err := service.Execute(t.Context(), command); !mission.IsMissionAuthorityKindV2(err, mission.MissionAuthorityAmbiguousV2) {
		t.Fatalf("partial publication fabricated outcome: %v", err)
	}
	if memory.batchCalls != 1 || coordinator.UnresolvedCount() != 1 {
		t.Fatalf("partial publication retried or resolved: batches=%d unresolved=%d", memory.batchCalls, coordinator.UnresolvedCount())
	}
}

func TestMissionAuthorityAdapterPublishesAndObservesFactInSameBatchV2(t *testing.T) {
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		return gapdbapi.StatusResult{DatabaseID: "database-1", CurrentRevision: memory.revision, DurableThroughRevision: memory.revision}, nil
	}
	store, _ := newAdapter(provider, "/explicit/socket", time.Second)
	projection, _ := journalv2.NewProjectionTrackerV2(missionReducerSetFixtureV2(t), journalv2.CanonicalCodecVersionV2)
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(store, projection)
	authority, _ := NewMissionAuthorityPortV2(store, coordinator)
	service, _ := mission.NewMissionAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-1"}, journalv2.DefaultEventRegistryV2())
	created := missionStateFixtureV2(t)
	create := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1",
		OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: created,
	}
	if _, err := service.Execute(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	blocked, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: created.DefinitionRevision(), DefinitionDigest: created.DefinitionDigest(),
		Version: 2, Status: mission.AuthorityMissionActiveV2, Step: created.Step(), ActiveBlockerIDs: []string{"blocker-1"},
		Decomposition: mission.DecompositionNoneV2, LastOperationID: "operation-blocker", ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	fact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", FactID: "blocker-1", Kind: mission.MissionFactBlockerV2,
		Status: mission.MissionFactActiveV2, LogicalVersion: 1, DetailDigest: created.DefinitionDigest(), LastOperationID: "operation-blocker", ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-blocker", ProjectID: "project-1", MissionID: "mission-1", ActorID: "actor-1", ProcessID: "process-1",
		OriginatingRuntimeEpochID: "epoch-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: "2026-09-04T12:01:00Z", EventType: mission.MissionEventBlockerRaisedV2, Expected: created, Successor: blocked,
		RelatedFact: &mission.MissionFactTransitionV2{Successor: fact},
	}
	before := memory.batchCalls
	if _, err := service.Execute(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if memory.batchCalls != before+1 {
		t.Fatalf("fact publication split across batches: %d -> %d", before, memory.batchCalls)
	}
	observed, err := authority.ObserveMissionFact(t.Context(), "project-1", "mission-1", mission.MissionFactBlockerV2, "blocker-1")
	if err != nil || !observed.Found || observed.State.Digest() != fact.Digest() || observed.RecordRevision != 3 {
		t.Fatalf("fact observation=%+v err=%v", observed, err)
	}
}

func missionStateFixtureV2(t *testing.T) mission.MissionAuthorityStateV2 {
	t.Helper()
	digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"definition":"one"}`))
	state, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1", DefinitionDigest: digest,
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "specifying", Decomposition: mission.DecompositionNoneV2,
		LastOperationID: "operation-1", ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func missionReducerSetFixtureV2(t *testing.T) journalv2.ReducerSetV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		reduce := journalv2.Reducer(func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"fixture":"` + tuple.Family + `"}`))
		if tuple.Family == "operation" {
			reduce, digest = journalv2.ReduceOperationStateV2, journalv2.OperationReducerImplementationDigestV2()
		}
		if tuple.Family == "mission" {
			reduce, digest = mission.ReduceMissionEventV2, mission.MissionReducerImplementationDigestV2()
		}
		registrations = append(registrations, journalv2.ReducerRegistration{
			Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion,
			Owner: tuple.Owner, ImplementationDigest: digest, Reduce: reduce,
		})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return reducers
}
