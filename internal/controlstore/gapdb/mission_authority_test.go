package gapdb

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
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
	if err != nil || observed.State().Digest() != paused.Digest() || observed.RecordRevision() != 4 || observed.Capture().DatabaseID() != "database-1" {
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
	changed.ObservationEvidence = []byte(`{"authority":"changed-after-commit"}`)
	beforeReads = memory.getCalls
	if _, err = restarted.Execute(t.Context(), changed); !mission.IsMissionAuthorityKindV2(err, mission.MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("changed readiness observation evidence did not conflict: %v", err)
	}
	if epoch.calls != 0 || memory.getCalls != beforeReads+1 || memory.batchCalls != beforeBatches {
		t.Fatalf("binding conflict crossed later authority: epoch=%d reads=%d/%d batches=%d/%d", epoch.calls, beforeReads, memory.getCalls, beforeBatches, memory.batchCalls)
	}
}

func TestReadinessAuthorityPublishesResolvedPolicyFactInSameGapDBBatchV2(t *testing.T) {
	memory, _, authority := readinessAuthorityGapDBFixtureV2(t)
	service, err := mission.NewReadinessAuthorityServiceV2(authority, missionEpochFixtureV2{"epoch-readiness"}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	created := readinessStateGapDBFixtureV2(t, 1, mission.MissionOpen, 0, "operation-readiness-fact-create", "2026-09-04T12:00:00Z")
	if _, err = service.Execute(t.Context(), readinessCommandGapDBFixtureV2(created, mission.ReadinessAuthorityStateV2{}, mission.ReadinessEventCreatedV2)); err != nil {
		t.Fatal(err)
	}
	openDigest := readinessDigestGapDBFixtureV2(t, "policy-open")
	scoped := mission.ScopedMissionFactV2{
		Kind: mission.ScopedFactDecisionV2, FactID: "policy-fact-readiness",
		Scope:            mission.ActionRef{ID: mission.ActionAcceptanceEvaluate, Scope: mission.AuthorityScope{Kind: mission.ScopeWork, Identity: "work-readiness"}},
		EvidenceIdentity: "policy-open-readiness", EvidenceDigest: openDigest.String(),
	}
	opened, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: created.ProjectID(), MissionID: created.MissionID(), DefinitionRevision: "definition-1",
		DefinitionDigest: readinessDigestGapDBFixtureV2(t, "definition"), Version: 2, Lifecycle: mission.MissionOpen,
		ActiveFacts: []mission.ScopedMissionFactV2{scoped}, LastOperationID: "operation-readiness-fact-open", ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	openFact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{
		ProjectID: opened.ProjectID(), MissionID: opened.MissionID(), FactID: scoped.FactID, Kind: mission.MissionFactDecisionV2,
		Status: mission.MissionFactOpenV2, LogicalVersion: 1, DetailDigest: openDigest,
		LastOperationID: opened.LastOperationID(), ObservedAt: opened.ObservedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	openCommand := readinessCommandGapDBFixtureV2(opened, created, mission.ReadinessEventFactOpenedV2)
	openCommand.RelatedFact = &mission.MissionFactTransitionV2{Successor: openFact}
	if _, err = service.Execute(t.Context(), openCommand); err != nil {
		t.Fatal(err)
	}
	observedOpen, err := authority.ObserveMissionFact(t.Context(), opened.ProjectID(), opened.MissionID(), mission.MissionFactDecisionV2, scoped.FactID)
	if err != nil || !observedOpen.Found || observedOpen.State.Digest() != openFact.Digest() {
		t.Fatalf("open fact was not atomically observable: %+v err=%v", observedOpen, err)
	}

	resolvedDigest := readinessDigestGapDBFixtureV2(t, "policy-resolved")
	resolved, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: opened.ProjectID(), MissionID: opened.MissionID(), DefinitionRevision: "definition-1",
		DefinitionDigest: readinessDigestGapDBFixtureV2(t, "definition"), Version: 3, Lifecycle: mission.MissionOpen,
		LastOperationID: "operation-readiness-fact-resolve", ObservedAt: "2026-09-04T12:02:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedFact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{
		ProjectID: resolved.ProjectID(), MissionID: resolved.MissionID(), FactID: scoped.FactID, Kind: mission.MissionFactDecisionV2,
		Status: mission.MissionFactResolvedV2, LogicalVersion: 2, DetailDigest: resolvedDigest,
		LastOperationID: resolved.LastOperationID(), ObservedAt: resolved.ObservedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	resolveCommand := readinessCommandGapDBFixtureV2(resolved, opened, mission.ReadinessEventFactClosedV2)
	resolveCommand.RelatedFact = &mission.MissionFactTransitionV2{Prior: openFact, Successor: resolvedFact, ExpectedRecordRevision: observedOpen.RecordRevision}
	resolveResult, err := service.Execute(t.Context(), resolveCommand)
	if err != nil {
		t.Fatal(err)
	}
	observedResolved, err := authority.ObserveMissionFact(t.Context(), resolved.ProjectID(), resolved.MissionID(), mission.MissionFactDecisionV2, scoped.FactID)
	if err != nil || !observedResolved.Found || observedResolved.State.Status() != mission.MissionFactResolvedV2 || observedResolved.State.DetailDigest() != resolvedDigest {
		t.Fatalf("resolved policy fact was not acceptance-ready: %+v err=%v", observedResolved, err)
	}
	beforeBatches := memory.batchCalls
	replay, err := service.Execute(t.Context(), resolveCommand)
	if err != nil || !replay.Replayed() || !bytes.Equal(replay.CanonicalBytes(), resolveResult.CanonicalBytes()) || memory.batchCalls != beforeBatches {
		t.Fatalf("fact-bearing operation did not replay exactly: replay=%s err=%v batches=%d/%d", replay.CanonicalBytes(), err, beforeBatches, memory.batchCalls)
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

func TestReadinessAuthorityRealGapDBCheckpointSuffixMatchesOriginAndProjectionLossV2(t *testing.T) {
	memory := newV2AtomicMemory()
	memory.revision = 1
	provider := memory.provider()
	enableWorkspaceRecoveryScansV2(memory, provider)
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
	registry := journalv2.DefaultEventRegistryV2()
	projection, _ := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(store, projection)
	portValue, _ := NewMissionAuthorityPortV2(store, coordinator)
	port := portValue.(mission.ReadinessAuthorityPortV2)
	service, _ := mission.NewReadinessAuthorityServiceV2(port, missionEpochFixtureV2{"epoch-readiness"}, registry)

	created := readinessStateGapDBFixtureV2(t, 1, mission.MissionOpen, 0, "operation-readiness-cp-create", "2026-09-04T12:00:00Z")
	if _, err = service.Execute(t.Context(), readinessCommandGapDBFixtureV2(created, mission.ReadinessAuthorityStateV2{}, mission.ReadinessEventCreatedV2)); err != nil {
		t.Fatal(err)
	}
	active := readinessStateGapDBFixtureV2(t, 2, mission.MissionOpen, 1, "operation-readiness-cp-activity", "2026-09-04T12:01:00Z")
	activity := readinessCommandGapDBFixtureV2(active, created, mission.ReadinessEventActivityRecordedV2)
	activity.Activity = &mission.MissionActivityV2{ActivityID: "activity-readiness-cp", Action: mission.ActionRef{ID: mission.ActionArtifactPublish, Scope: mission.AuthorityScope{Kind: mission.ScopeArtifact, Identity: "artifact-cp"}}, Outcome: "succeeded", ResultDigest: readinessDigestGapDBFixtureV2(t, "activity-cp").String(), EvidenceIdentities: []string{"evidence-cp"}}
	if _, err = service.Execute(t.Context(), activity); err != nil {
		t.Fatal(err)
	}
	prefix, err := coordinator.SealProjectionPrefix()
	if err != nil {
		t.Fatal(err)
	}
	checkpointValue, err := checkpoint.BuildV2("checkpoint-readiness-authority", prefix)
	if err != nil {
		t.Fatal(err)
	}
	checkpointSlot, slotBytes, err := checkpoint.NewSlotV2("slot-readiness-authority", checkpointValue)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = coordinator.Publish(t.Context(), workspaceCheckpointWriterPlanV2(t, checkpointValue, checkpointSlot, slotBytes)); err != nil {
		t.Fatal(err)
	}

	paused := readinessStateGapDBFixtureV2(t, 3, mission.MissionPaused, 1, "operation-readiness-cp-pause", "2026-09-04T12:02:00Z")
	if _, err = service.Execute(t.Context(), readinessCommandGapDBFixtureV2(paused, active, mission.ReadinessEventPausedV2)); err != nil {
		t.Fatal(err)
	}
	// Delete the only in-memory projection after the suffix publication. Both
	// recovery paths below must reconstruct exclusively from durable authority.
	projection = nil
	input := restore.InputV2{Reader: store, EventRegistry: registry, Reducers: reducers, HostAuthorityID: "host-readiness", RecoveryAttemptID: "restore-readiness-checkpoint", Budgets: restore.DefaultBudgetsV2()}
	fromCheckpoint, err := restore.RestoreV2(t.Context(), input)
	if err != nil {
		t.Fatalf("checkpoint restore: %v (cause: %v)", err, errors.Unwrap(err))
	}
	input.Reader = checkpointHidingReaderV2{ReaderV2: store}
	input.RecoveryAttemptID = "restore-readiness-origin"
	fromOrigin, err := restore.RestoreV2(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if fromCheckpoint.Playback().CanonicalProjectionDigest() != fromOrigin.Playback().CanonicalProjectionDigest() {
		t.Fatal("real checkpoint-plus-suffix playback diverged from origin playback")
	}
	checkpointProjection, err := mission.NewMissionRestoredProjectionV2(fromCheckpoint.Playback())
	if err != nil {
		t.Fatal(err)
	}
	originProjection, err := mission.NewMissionRestoredProjectionV2(fromOrigin.Playback())
	if err != nil {
		t.Fatal(err)
	}
	checkpointState, found := checkpointProjection.Readiness(paused.ProjectID(), paused.MissionID())
	if !found {
		t.Fatal("checkpoint restore omitted readiness authority")
	}
	originState, found := originProjection.Readiness(paused.ProjectID(), paused.MissionID())
	if !found || !bytes.Equal(checkpointState.CanonicalBytes(), originState.CanonicalBytes()) || !bytes.Equal(checkpointState.CanonicalBytes(), paused.CanonicalBytes()) {
		t.Fatal("restored readiness authority was not byte-identical")
	}
	checkpointCensus := readinessCensusGapDBFixtureV2(t, checkpointState, fromCheckpoint.StoreRevision())
	originCensus := readinessCensusGapDBFixtureV2(t, originState, fromOrigin.StoreRevision())
	if !bytes.Equal(checkpointCensus.CanonicalBytes(), originCensus.CanonicalBytes()) {
		t.Fatal("restored complete action census was not byte-identical")
	}
}

type checkpointHidingReaderV2 struct{ restore.ReaderV2 }

func (r checkpointHidingReaderV2) ScanV2(ctx context.Context, request journalv2.ScanRequestV2) (journalv2.RecoveryPageV2, error) {
	if request.Target().Kind() == journalv2.ScanCheckpointSlots {
		return journalv2.NewRecoveryPageV2(request.AttemptRevision().CurrentRevision(), "2026-09-04T12:03:00Z", nil, nil, false)
	}
	return r.ReaderV2.ScanV2(ctx, request)
}

func readinessCensusGapDBFixtureV2(t *testing.T, state mission.ReadinessAuthorityStateV2, capture journalv2.StoreRevisionV2) mission.ActionCensus {
	t.Helper()
	definition := softwaredev.CapabilityDefinition()
	observations := make([]mission.CapabilityAuthorityObservationV2, 0, len(mission.AllActionIDs()))
	evidenceDigest := readinessDigestGapDBFixtureV2(t, "complete-scope-census").String()
	source := gapDBCapabilityReadinessSourceFuncV2(func(_ context.Context, query mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error) {
		return mission.CapabilityReadinessEvidenceV2{
			Capture: query.Capture(), ProjectID: query.ProjectID(), MissionID: query.MissionID(), Action: query.Action(),
			ObservedAbsent: true, EvidenceIdentity: "absence-" + string(query.Action()), EvidenceDigest: evidenceDigest,
		}, nil
	})
	observer, err := mission.NewCapabilityAuthorityObserverV2(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, template := range definition.Templates() {
		observation, err := observer.Observe(t.Context(), capture, state.ProjectID(), state.MissionID(), template.Action)
		if err != nil {
			t.Fatal(err)
		}
		observations = append(observations, observation)
	}
	observationSet, err := mission.SealCapabilityObservationSetV2(capture, state.ProjectID(), state.MissionID(), observations)
	if err != nil {
		t.Fatal(err)
	}
	authorityObservation, err := mission.NewReadinessAuthorityObservationV2(state, capture, capture.CurrentRevision(), "")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := mission.SealReadinessSnapshot(authorityObservation, definition, observationSet)
	if err != nil {
		t.Fatal(err)
	}
	census, err := mission.EvaluateReadiness(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return census
}

type gapDBCapabilityReadinessSourceFuncV2 func(context.Context, mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error)

func (f gapDBCapabilityReadinessSourceFuncV2) ObserveCapabilityReadiness(ctx context.Context, query mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error) {
	return f(ctx, query)
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

func readinessCommandGapDBFixtureV2(updated, expected mission.ReadinessAuthorityStateV2, eventType mission.ReadinessEventTypeV2) mission.ReadinessAuthorityCommandV2 {
	return mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: updated.LastOperationID(), ProjectID: updated.ProjectID(), MissionID: updated.MissionID(),
		ActorID: "actor-readiness", ProcessID: "process-readiness", OriginatingRuntimeEpochID: "epoch-readiness",
		DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: updated.ObservedAt(), EventType: eventType, Expected: expected, Updated: updated,
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
