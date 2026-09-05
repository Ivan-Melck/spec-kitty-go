package gapdb

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

type readinessEpochCounterV2 struct {
	epoch journalv2.RuntimeEpochID
	calls int
}

func (e *readinessEpochCounterV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	e.calls++
	return e.epoch, nil
}

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

func TestReadinessAuthorityAdapterCreateActivityPauseRestartReplayV2(t *testing.T) {
	memory, _, readiness := readinessAuthorityGapDBFixtureV2(t)
	service, err := mission.NewReadinessAuthorityServiceV2(readiness, missionEpochFixtureV2{"epoch-readiness"}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	created := readinessStateGapDBFixtureV2(t, 1, mission.MissionOpen, 0, "operation-readiness-create", "2026-09-04T12:00:00Z")
	create := readinessCommandGapDBFixtureV2(created, mission.ReadinessAuthorityStateV2{}, mission.ReadinessEventCreatedV2)
	createResult, err := service.Execute(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}

	active := readinessStateGapDBFixtureV2(t, 2, mission.MissionOpen, 1, "operation-readiness-activity", "2026-09-04T12:01:00Z")
	activity := readinessCommandGapDBFixtureV2(active, created, mission.ReadinessEventActivityRecordedV2)
	activity.Activity = &mission.MissionActivityV2{
		ActivityID: "activity-readiness-1",
		Action:     mission.ActionRef{ID: mission.ActionArtifactPublish, Scope: mission.AuthorityScope{Kind: mission.ScopeArtifact, Identity: "artifact-readiness"}},
		Outcome:    "succeeded", ResultDigest: readinessDigestGapDBFixtureV2(t, "activity").String(), EvidenceIdentities: []string{"evidence-readiness"},
	}
	if _, err = service.Execute(t.Context(), activity); err != nil {
		t.Fatal(err)
	}

	paused := readinessStateGapDBFixtureV2(t, 3, mission.MissionPaused, 1, "operation-readiness-pause", "2026-09-04T12:02:00Z")
	pause := readinessCommandGapDBFixtureV2(paused, active, mission.ReadinessEventPausedV2)
	pauseResult, err := service.Execute(t.Context(), pause)
	if err != nil {
		t.Fatal(err)
	}
	if memory.batchCalls != 3 || createResult.State().Digest() != created.Digest() || pauseResult.State().Digest() != paused.Digest() {
		t.Fatalf("readiness progression batches=%d create=%s pause=%s", memory.batchCalls, createResult.State().Digest(), pauseResult.State().Digest())
	}
	observed, err := readiness.ObserveReadinessAuthority(t.Context(), paused.ProjectID(), paused.MissionID())
	if err != nil || observed.State.Digest() != paused.Digest() || observed.RecordRevision != 4 {
		t.Fatalf("readiness readback=%+v err=%v", observed, err)
	}

	epoch := &readinessEpochCounterV2{epoch: "replacement-epoch"}
	restarted, err := mission.NewReadinessAuthorityServiceV2(readiness, epoch, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	beforeReads, beforeBatches := memory.getCalls, memory.batchCalls
	replay, err := restarted.Execute(t.Context(), pause)
	if err != nil || !replay.Replayed() || !bytes.Equal(replay.CanonicalBytes(), pauseResult.CanonicalBytes()) || replay.PublicationID() != pauseResult.PublicationID() {
		t.Fatalf("canonical restart replay=%s err=%v", replay.CanonicalBytes(), err)
	}
	if epoch.calls != 0 || memory.getCalls != beforeReads+2 || memory.batchCalls != beforeBatches {
		t.Fatalf("replay crossed later authority: epoch=%d reads=%d/%d batches=%d/%d", epoch.calls, beforeReads, memory.getCalls, beforeBatches, memory.batchCalls)
	}

	changed := pause
	changed.ProcessID = "process-readiness-other"
	beforeReads = memory.getCalls
	if _, err = restarted.Execute(t.Context(), changed); !mission.IsMissionAuthorityKindV2(err, mission.MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("changed readiness binding did not conflict: %v", err)
	}
	if epoch.calls != 0 || memory.getCalls != beforeReads+1 || memory.batchCalls != beforeBatches {
		t.Fatalf("binding conflict crossed later authority: epoch=%d reads=%d/%d batches=%d/%d", epoch.calls, beforeReads, memory.getCalls, beforeBatches, memory.batchCalls)
	}
}

func TestReadinessAuthorityAdapterRejectsLegacyPhaseStateV2(t *testing.T) {
	_, _, readiness, legacy := missionAuthorityGapDBFixtureV2(t)
	service, err := mission.NewMissionAuthorityServiceV2(legacy, missionEpochFixtureV2{"epoch-legacy"}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	definitionDigest := readinessDigestGapDBFixtureV2(t, "legacy-definition")
	state, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-1", MissionID: "mission-1", DefinitionRevision: "definition-1", DefinitionDigest: definitionDigest,
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "specifying", Decomposition: mission.DecompositionNoneV2,
		LastOperationID: "operation-legacy-readiness", ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-legacy-readiness", ProjectID: state.ProjectID(), MissionID: state.MissionID(),
		ActorID: "actor-legacy", ProcessID: "process-legacy", OriginatingRuntimeEpochID: "epoch-legacy",
		DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: state.ObservedAt(), EventType: mission.MissionEventCreatedV2, Successor: state,
	}
	if _, err = service.Execute(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	if _, err = readiness.ObserveReadinessAuthority(t.Context(), state.ProjectID(), state.MissionID()); !mission.IsMissionAuthorityKindV2(err, mission.MissionAuthorityLegacyPhaseV2) {
		t.Fatalf("legacy phase state entered readiness authority: %v", err)
	}
}

func TestReadinessAuthorityAdapterRefusesOneRevisionDriftV2(t *testing.T) {
	memory, provider, readiness := readinessAuthorityGapDBFixtureV2(t)
	service, _ := mission.NewReadinessAuthorityServiceV2(readiness, missionEpochFixtureV2{"epoch-readiness"}, journalv2.DefaultEventRegistryV2())
	created := readinessStateGapDBFixtureV2(t, 1, mission.MissionOpen, 0, "operation-readiness-drift", "2026-09-04T12:00:00Z")
	if _, err := service.Execute(t.Context(), readinessCommandGapDBFixtureV2(created, mission.ReadinessAuthorityStateV2{}, mission.ReadinessEventCreatedV2)); err != nil {
		t.Fatal(err)
	}
	statusCalls := 0
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		memory.mu.Lock()
		defer memory.mu.Unlock()
		statusCalls++
		revision := memory.revision
		if statusCalls > 1 {
			revision++
		}
		return gapdbapi.StatusResult{DatabaseID: "database-1", CurrentRevision: revision, DurableThroughRevision: revision}, nil
	}
	if _, err := readiness.ObserveReadinessAuthority(t.Context(), created.ProjectID(), created.MissionID()); !journalv2.IsKind(err, journalv2.ErrorRevisionMoved) {
		t.Fatalf("one-revision drift was accepted: %v", err)
	}
	if statusCalls != 2 {
		t.Fatalf("readiness observation did not capture and verify one revision: calls=%d", statusCalls)
	}
}

func readinessAuthorityGapDBFixtureV2(t *testing.T) (*v2AtomicMemory, *fakeProvider, mission.ReadinessAuthorityPortV2) {
	t.Helper()
	memory, provider, readiness, _ := missionAuthorityGapDBFixtureV2(t)
	return memory, provider, readiness
}

func missionAuthorityGapDBFixtureV2(t *testing.T) (*v2AtomicMemory, *fakeProvider, mission.ReadinessAuthorityPortV2, mission.MissionAuthorityPortV2) {
	t.Helper()
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
	projection, err := journalv2.NewProjectionTrackerV2(missionReducerSetFixtureV2(t), journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(store, projection)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := NewMissionAuthorityPortV2(store, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	readiness, ok := legacy.(mission.ReadinessAuthorityPortV2)
	if !ok {
		t.Fatal("mission adapter does not implement readiness authority")
	}
	return memory, provider, readiness, legacy
}

func readinessStateGapDBFixtureV2(t *testing.T, version journalv2.LogicalVersion, lifecycle mission.MissionLifecycle, activityCount uint64, operationID journalv2.OperationID, observedAt string) mission.ReadinessAuthorityStateV2 {
	t.Helper()
	state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: "project-readiness", MissionID: "mission-readiness", DefinitionRevision: "definition-1",
		DefinitionDigest: readinessDigestGapDBFixtureV2(t, "definition"), Version: version, Lifecycle: lifecycle,
		ActivityCount: activityCount, LastOperationID: operationID, ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func readinessCommandGapDBFixtureV2(successor, expected mission.ReadinessAuthorityStateV2, eventType mission.ReadinessEventTypeV2) mission.ReadinessAuthorityCommandV2 {
	return mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: successor.LastOperationID(), ProjectID: successor.ProjectID(), MissionID: successor.MissionID(),
		ActorID: "actor-readiness", ProcessID: "process-readiness", OriginatingRuntimeEpochID: "epoch-readiness",
		DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: successor.ObservedAt(), EventType: eventType, Expected: expected, Successor: successor,
	}
}

func readinessDigestGapDBFixtureV2(t *testing.T, value string) journalv2.Digest {
	t.Helper()
	digest, err := journalv2.ReducerStateDigestV2([]byte(`{"value":"` + value + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
