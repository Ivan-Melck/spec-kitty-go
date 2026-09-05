package mission

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestSuccessorPublicationAtomicallyAdvancesPointerAndReplays(t *testing.T) {
	command := successorPublicationFixture(t, "operation-publish")
	store := newSuccessorStore(command)
	service := NewSuccessorPublicationService(store)
	result, err := service.Publish(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed || result.Revision.ID() != command.Candidate.ID() || result.Mission.CurrentWorkRevisionID != command.Candidate.ID() || result.Mission.StateVersion != command.ExpectedMission.StateVersion+1 || result.Mission.Lifecycle != LifecycleOpen || result.Mission.CurrentStep != command.ExpectedMission.CurrentStep || result.Mission.TerminalOutcome != "" || len(result.RequestDigest) != 64 {
		t.Fatalf("incomplete publication result: %#v", result)
	}
	wantKinds := []AuthorityActionKind{AuthorityExpireRun, AuthorityQuarantineWorkspace}
	gotKinds := []AuthorityActionKind{result.Actions[0].Kind, result.Actions[1].Kind}
	slices.Sort(gotKinds)
	slices.Sort(wantKinds)
	if !slices.Equal(gotKinds, wantKinds) || store.publishCalls.Load() != 1 {
		t.Fatalf("durable action facts=%#v publish=%d", result.Actions, store.publishCalls.Load())
	}
	replay, err := service.Publish(context.Background(), command)
	if err != nil || !replay.Replayed || store.publishCalls.Load() != 1 || replay.RequestDigest != result.RequestDigest || replay.Revision.ID() != result.Revision.ID() {
		t.Fatalf("replay=%#v err=%v calls=%d", replay, err, store.publishCalls.Load())
	}
}

func TestSuccessorPublicationAcceptsAdditionOnlyReconciliationAndReplays(t *testing.T) {
	command := additionOnlySuccessorPublicationFixture(t, "operation-publish-addition")
	if len(command.Drift.AffectedOldWorkIDs()) != 0 || len(command.Reconciliation.Decisions()) != 0 || len(command.Drift.AffectedNewWorkIDs()) != 1 {
		t.Fatalf("fixture is not exact addition-only authority: old=%v new=%v decisions=%v", command.Drift.AffectedOldWorkIDs(), command.Drift.AffectedNewWorkIDs(), command.Reconciliation.Decisions())
	}
	store := newSuccessorStore(command)
	service := NewSuccessorPublicationService(store)
	result, err := service.Publish(context.Background(), command)
	if err != nil || result.Replayed || len(result.Actions) != 0 || result.Mission.CurrentWorkRevisionID != command.Candidate.ID() || store.publishCalls.Load() != 1 {
		t.Fatalf("addition publication=%#v err=%v calls=%d", result, err, store.publishCalls.Load())
	}
	replay, err := service.Publish(context.Background(), command)
	if err != nil || !replay.Replayed || len(replay.Actions) != 0 || replay.RequestDigest != result.RequestDigest || store.publishCalls.Load() != 1 {
		t.Fatalf("addition replay=%#v err=%v calls=%d", replay, err, store.publishCalls.Load())
	}
}

func TestSuccessorPublicationRefusesInvalidAuthorityBeforeStore(t *testing.T) {
	tests := map[string]func(*SuccessorPublicationCommand){
		"stale generation": func(command *SuccessorPublicationCommand) { command.ExpectedCurrentGeneration-- },
		"wrong current pointer": func(command *SuccessorPublicationCommand) {
			command.ExpectedMission.CurrentWorkRevisionID = "revision-other"
		},
		"clean substituted drift": func(command *SuccessorPublicationCommand) {
			command.Drift, _ = workgraph.DetectDrift(command.ExpectedOld, command.ExpectedOld, command.Drift.ObservedAt())
		},
		"observation-only substituted drift": func(command *SuccessorPublicationCommand) {
			noise := revisionWithProviderNoise(t, command.ExpectedOld)
			command.Drift, _ = workgraph.DetectDrift(command.ExpectedOld, noise, command.Drift.ObservedAt())
		},
		"incomplete plan":               func(command *SuccessorPublicationCommand) { command.Reconciliation = workgraph.MixedReconciliation{} },
		"wrong reconciliation identity": func(command *SuccessorPublicationCommand) { command.ReconciliationID = "reconciliation-other" },
		"unsealed governance":           func(command *SuccessorPublicationCommand) { command.GovernanceContextID = "" },
		"non UTC time": func(command *SuccessorPublicationCommand) {
			command.OccurredAt = command.OccurredAt.In(time.FixedZone("offset", 3600))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			command := successorPublicationFixture(t, "operation-invalid")
			store := newSuccessorStore(command)
			mutate(&command)
			_, err := NewSuccessorPublicationService(store).Publish(context.Background(), command)
			if !IsKind(err, KindInvalidRequest) || store.replayCalls.Load() != 0 || store.publishCalls.Load() != 0 {
				t.Fatalf("error=%v replay=%d publish=%d", err, store.replayCalls.Load(), store.publishCalls.Load())
			}
			if len(err.Error()) > 300 || strings.Contains(err.Error(), "seeded-secret") {
				t.Fatalf("unbounded diagnostic: %v", err)
			}
		})
	}
}

func TestSuccessorPublicationReplayConflictPrecedesMutation(t *testing.T) {
	command := successorPublicationFixture(t, "operation-conflict")
	store := newSuccessorStore(command)
	service := NewSuccessorPublicationService(store)
	if _, err := service.Publish(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	changed := command
	changed.ExpectedMission.StateVersion++
	_, err := service.Publish(context.Background(), changed)
	if !IsKind(err, KindIdempotencyConflict) || store.publishCalls.Load() != 1 {
		t.Fatalf("altered replay=%v publish=%d", err, store.publishCalls.Load())
	}
}

func TestSuccessorPublicationFaultsHaveOnlyCompleteStates(t *testing.T) {
	t.Run("before commit", func(t *testing.T) {
		command := successorPublicationFixture(t, "operation-before-fault")
		store := newSuccessorStore(command)
		store.failBefore = true
		_, err := NewSuccessorPublicationService(store).Publish(context.Background(), command)
		if !IsKind(err, KindPersistenceUnavailable) || store.currentRevision.ID() != command.ExpectedOld.ID() || len(store.operations) != 0 {
			t.Fatalf("partial pre-commit state: err=%v current=%s operations=%d", err, store.currentRevision.ID(), len(store.operations))
		}
	})
	t.Run("lost committed response", func(t *testing.T) {
		command := successorPublicationFixture(t, "operation-after-fault")
		store := newSuccessorStore(command)
		store.loseResponse = true
		service := NewSuccessorPublicationService(store)
		if _, err := service.Publish(context.Background(), command); !IsKind(err, KindPersistenceUnavailable) {
			t.Fatalf("lost response=%v", err)
		}
		result, err := service.Publish(context.Background(), command)
		if err != nil || !result.Replayed || store.currentRevision.ID() != command.Candidate.ID() || len(store.operations) != 1 {
			t.Fatalf("ambiguous replay=%#v err=%v", result, err)
		}
	})
}

func TestSuccessorPublicationTwoCallerCASHasOneCompleteWinner(t *testing.T) {
	first := successorPublicationFixture(t, "operation-race-a")
	second := first
	second.OperationID = "operation-race-b"
	store := newSuccessorStore(first)
	service := NewSuccessorPublicationService(store)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, command := range []SuccessorPublicationCommand{first, second} {
		go func(command SuccessorPublicationCommand) {
			<-start
			_, err := service.Publish(context.Background(), command)
			errs <- err
		}(command)
	}
	close(start)
	var success, conflict int
	for range 2 {
		if err := <-errs; err == nil {
			success++
		} else if IsKind(err, KindStateVersionConflict) {
			conflict++
		} else {
			t.Fatalf("race error=%v", err)
		}
	}
	if success != 1 || conflict != 1 || len(store.operations) != 1 || store.currentRevision.ID() != first.Candidate.ID() {
		t.Fatalf("race success=%d conflict=%d operations=%d", success, conflict, len(store.operations))
	}
}

func TestSuccessorPublicationConcurrentSameOperationConverges(t *testing.T) {
	command := successorPublicationFixture(t, "operation-same-race")
	store := newSuccessorStore(command)
	service := NewSuccessorPublicationService(store)
	const workers = 32
	start := make(chan struct{})
	results := make(chan error, workers)
	for range workers {
		go func() { <-start; _, err := service.Publish(context.Background(), command); results <- err }()
	}
	close(start)
	for range workers {
		if err := <-results; err != nil {
			t.Fatalf("same-operation caller failed: %v", err)
		}
	}
	if len(store.operations) != 1 || store.currentRevision.ID() != command.Candidate.ID() {
		t.Fatalf("same operation did not converge: operations=%d", len(store.operations))
	}
}

func TestSuccessorPublicationRejectsSubstitutedStoreOutcomes(t *testing.T) {
	tests := map[string]func(*SuccessorPublicationResult, *testing.T){
		"revision evidence": func(result *SuccessorPublicationResult, t *testing.T) {
			result.Revision = revisionWithProviderNoise(t, result.Revision)
		},
		"drift": func(result *SuccessorPublicationResult, _ *testing.T) { result.Drift = workgraph.DriftReport{} },
		"reconciliation": func(result *SuccessorPublicationResult, _ *testing.T) {
			result.Reconciliation = workgraph.MixedReconciliation{}
		},
		"mission":        func(result *SuccessorPublicationResult, _ *testing.T) { result.Mission.StateVersion++ },
		"actions":        func(result *SuccessorPublicationResult, _ *testing.T) { result.Actions = result.Actions[:1] },
		"request digest": func(result *SuccessorPublicationResult, _ *testing.T) { result.RequestDigest = strings.Repeat("0", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			command := successorPublicationFixture(t, "operation-mutant")
			store := newSuccessorStore(command)
			store.mutateResponse = func(result *SuccessorPublicationResult) { mutate(result, t) }
			_, err := NewSuccessorPublicationService(store).Publish(context.Background(), command)
			if !IsKind(err, KindPersistenceContract) {
				t.Fatalf("substituted outcome accepted: %v", err)
			}
		})
	}
}

func TestSuccessorPublicationRejectsSubstitutedReplayOutcomes(t *testing.T) {
	command := successorPublicationFixture(t, "operation-replay-mutant")
	store := newSuccessorStore(command)
	service := NewSuccessorPublicationService(store)
	if _, err := service.Publish(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	record := store.operations[command.OperationID]
	record.result.Revision = revisionWithProviderNoise(t, record.result.Revision)
	store.operations[command.OperationID] = record
	store.mu.Unlock()
	if _, err := service.Publish(context.Background(), command); !IsKind(err, KindPersistenceContract) {
		t.Fatalf("substituted replay accepted: %v", err)
	}
}

func TestSuccessorPublicationDefensivelyCopiesResults(t *testing.T) {
	command := successorPublicationFixture(t, "operation-copy")
	command.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-preserved"}
	command.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-preserved"}
	store := newSuccessorStore(command)
	service := NewSuccessorPublicationService(store)
	result, err := service.Publish(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	result.Actions[0].Reason = "mutated"
	result.Mission.ActiveBlockers[0] = "blocker-mutated"
	result.Mission.UnresolvedDecisions[0] = "decision-mutated"
	replay, err := service.Publish(context.Background(), command)
	if err != nil || replay.Actions[0].Reason == "mutated" || replay.Mission.ActiveBlockers[0] != "blocker-preserved" || replay.Mission.UnresolvedDecisions[0] != "decision-preserved" {
		t.Fatalf("caller mutation reached stored result: %#v err=%v", replay, err)
	}
}

func TestSuccessorPublicationDigestBindsEveryAuthorityClass(t *testing.T) {
	command := successorPublicationFixture(t, "operation-digest")
	actions, digest, err := validateSuccessorPublication(command)
	if err != nil {
		t.Fatal(err)
	}
	oldBytes, _ := command.ExpectedOld.Encode()
	newBytes, _ := command.Candidate.Encode()
	driftBytes, _ := command.Drift.Encode()
	mutants := map[string]func(*SuccessorPublicationCommand, *[]byte, *[]byte, *[]byte, *[]AuthorityAction){
		"mission": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.ExpectedMission.StateVersion++
		},
		"generation": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.ExpectedCurrentGeneration++
		},
		"reconciliation": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.ReconciliationID = "reconciliation-digest-other"
		},
		"operation": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.OperationID = "operation-digest-other"
		},
		"actor": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.ActorID = "actor-other"
		},
		"governance": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.GovernanceContextID = "governance-other"
		},
		"rationale": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.Rationale = "changed rationale"
		},
		"time": func(value *SuccessorPublicationCommand, _, _, _ *[]byte, _ *[]AuthorityAction) {
			value.OccurredAt = value.OccurredAt.Add(time.Second)
		},
		"old revision": func(_ *SuccessorPublicationCommand, value, _, _ *[]byte, _ *[]AuthorityAction) {
			*value = append(*value, 0)
		},
		"candidate revision": func(_ *SuccessorPublicationCommand, _, value, _ *[]byte, _ *[]AuthorityAction) {
			*value = append(*value, 0)
		},
		"drift evidence": func(_ *SuccessorPublicationCommand, _, _, value *[]byte, _ *[]AuthorityAction) {
			*value = append(*value, 0)
		},
		"action facts": func(_ *SuccessorPublicationCommand, _, _, _ *[]byte, value *[]AuthorityAction) {
			(*value)[0].Reason = "changed"
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			changed := command
			oldCopy, newCopy, driftCopy, actionCopy := append([]byte(nil), oldBytes...), append([]byte(nil), newBytes...), append([]byte(nil), driftBytes...), cloneAuthorityActions(actions)
			mutate(&changed, &oldCopy, &newCopy, &driftCopy, &actionCopy)
			got, buildErr := successorCommandDigest(changed, oldCopy, newCopy, driftCopy, actionCopy)
			if buildErr != nil || got == digest {
				t.Fatalf("authority mutation not bound: %s err=%v", got, buildErr)
			}
		})
	}
}

type successorRecord struct {
	digest string
	result SuccessorPublicationResult
}
type memorySuccessorStore struct {
	mu              sync.Mutex
	currentMission  MissionSnapshot
	currentRevision workgraph.RevisionV2
	operations      map[ids.OperationID]successorRecord
	replayCalls     atomic.Int64
	publishCalls    atomic.Int64
	failBefore      bool
	loseResponse    bool
	mutateResponse  func(*SuccessorPublicationResult)
}

func newSuccessorStore(command SuccessorPublicationCommand) *memorySuccessorStore {
	return &memorySuccessorStore{currentMission: command.ExpectedMission.clone(), currentRevision: command.ExpectedOld, operations: map[ids.OperationID]successorRecord{}}
}
func (store *memorySuccessorStore) ReplaySuccessorPublication(_ context.Context, _ ids.MissionID, operation ids.OperationID, digest string) (SuccessorPublicationResult, bool, error) {
	store.replayCalls.Add(1)
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.operations[operation]
	if !ok {
		return SuccessorPublicationResult{}, false, nil
	}
	if record.digest != digest {
		return SuccessorPublicationResult{}, false, refusal(KindIdempotencyConflict, RecoveryCorrectInput, "operation_id", "operation identity is bound to different content")
	}
	return cloneSuccessorResult(record.result), true, nil
}
func (store *memorySuccessorStore) PublishSuccessorAtomically(_ context.Context, commit SuccessorPublicationCommit) (SuccessorPublicationResult, error) {
	store.publishCalls.Add(1)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failBefore {
		return SuccessorPublicationResult{}, errors.New("seeded-secret-before-commit")
	}
	if record, ok := store.operations[commit.OperationID]; ok {
		if record.digest != commit.RequestDigest {
			return SuccessorPublicationResult{}, refusal(KindIdempotencyConflict, RecoveryCorrectInput, "operation_id", "operation identity is bound to different content")
		}
		result := cloneSuccessorResult(record.result)
		result.Replayed = true
		return result, nil
	}
	if store.currentRevision.ID() != commit.ExpectedOld.ID() || store.currentRevision.Generation() != commit.ExpectedCurrentGeneration || !sameMissionSnapshot(store.currentMission, commit.ExpectedMission) {
		return SuccessorPublicationResult{}, refusal(KindStateVersionConflict, RecoveryRefreshState, "current_generation", "current generation changed")
	}
	mission := commit.ExpectedMission.clone()
	mission.CurrentWorkRevisionID = commit.Candidate.ID()
	mission.StateVersion++
	mission.UpdatedAt = commit.OccurredAt
	result := SuccessorPublicationResult{Revision: commit.Candidate, Drift: commit.Drift, Reconciliation: commit.Reconciliation, Mission: mission, Actions: cloneAuthorityActions(commit.Actions), RequestDigest: commit.RequestDigest}
	store.currentMission, store.currentRevision = mission.clone(), commit.Candidate
	store.operations[commit.OperationID] = successorRecord{commit.RequestDigest, cloneSuccessorResult(result)}
	if store.loseResponse {
		store.loseResponse = false
		return SuccessorPublicationResult{}, errors.New("seeded-secret-lost-response")
	}
	response := cloneSuccessorResult(result)
	if store.mutateResponse != nil {
		store.mutateResponse(&response)
	}
	return response, nil
}

func successorPublicationFixture(t *testing.T, operation ids.OperationID) SuccessorPublicationCommand {
	t.Helper()
	oldCommand := finalizationCommandFixture("operation-fixture-generation-one")
	old, err := workgraph.BuildGenerationOneCandidate(workgraph.GenerationOneCandidateParams{ID: oldCommand.ProposedRevisionID, Observation: missionFinalizationObservation(t, "provider-old"), PlanningSourceCommit: oldCommand.PlanningSourceCommit, MissionDefinition: workgraph.MissionDefinitionIdentity{Key: oldCommand.ExpectedMission.DefinitionID.String(), Version: uint64(oldCommand.ExpectedMission.DefinitionVersion), Digest: oldCommand.ExpectedMission.DefinitionCommitment.String()}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: oldCommand.ActorID, FinalizedAt: oldCommand.OccurredAt, GovernanceContextID: oldCommand.GovernanceContextID})
	if err != nil {
		t.Fatal(err)
	}
	when := oldCommand.OccurredAt.Add(time.Hour)
	candidate, err := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: "revision-successor", ProjectID: old.ProjectID(), MissionID: old.MissionID(), Generation: 2, PredecessorID: old.ID(), PlanningSourceCommit: strings.Repeat("d", 40), MissionDefinition: old.MissionDefinition(), DependencyPolicy: old.DependencyPolicy(), WorkItems: revisionWorkParams(old), Lanes: old.Lanes(), ProviderObservations: old.ProviderObservations(), FinalizedBy: "actor-reconciler", FinalizedAt: when, GovernanceContextID: "governance-reconcile"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := workgraph.DetectDrift(old, candidate, when)
	if err != nil {
		t.Fatal(err)
	}
	rationale := "reconcile the exact finalized work generation"
	plan, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: "reconciliation-successor", Old: old, New: candidate, Drift: drift, Statuses: []workgraph.WorkStatusAssignment{{WorkID: "work-a", Status: workgraph.StatusActive}, {WorkID: "work-b", Status: workgraph.StatusApproved}}, Decisions: []workgraph.WorkDispositionDecisionParams{{OldWorkID: "work-a", Disposition: workgraph.Invalidate, Rationale: "invalidate active old authority"}, {OldWorkID: "work-b", Disposition: workgraph.Continue, NewWorkIDs: []ids.WorkID{"work-b"}, Rationale: "continue compatible completed work"}}, ActorID: "actor-reconciler", Rationale: rationale, OccurredAt: when})
	if err != nil {
		t.Fatal(err)
	}
	mission := oldCommand.ExpectedMission.clone()
	mission.CurrentWorkRevisionID = old.ID()
	mission.StateVersion++
	mission.UpdatedAt = oldCommand.OccurredAt
	return SuccessorPublicationCommand{MissionID: old.MissionID(), ProjectID: old.ProjectID(), ExpectedMission: mission, ExpectedCurrentGeneration: 1, ExpectedOld: old, Candidate: candidate, Drift: drift, ReconciliationID: "reconciliation-successor", Reconciliation: plan, OperationID: operation, ActorID: "actor-reconciler", GovernanceContextID: "governance-reconcile", Rationale: rationale, OccurredAt: when}
}

func additionOnlySuccessorPublicationFixture(t *testing.T, operation ids.OperationID) SuccessorPublicationCommand {
	t.Helper()
	command := successorPublicationFixture(t, operation)
	baseDraft := missionFinalizationObservation(t, "provider-old").Draft()
	addedObservation, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{
		ProjectID: "project-final", WorkID: "work-added", Title: "Implement independent addition", Description: "Complete the independent added work item.", DesignContext: "Use the approved design.",
		Criteria:    []workgraph.CriterionObservation{{ID: "criterion-work-added", Text: "Run the added test.", Outcome: "The added test passes.", Verifier: "test", Requirement: workgraph.CriterionRequired}},
		WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/added", Domain: "added", Access: workgraph.ScopeWriteExclusive}},
		Provenance:  []workgraph.ScopeReference{{ID: "scope-work-added", Kind: workgraph.ScopePlanConcern}},
	})
	if err != nil {
		t.Fatal(err)
	}
	approved := append(baseDraft.ApprovedScope(), workgraph.ApprovedScope{ID: "scope-work-added", Kind: workgraph.ScopePlanConcern, MissionID: command.MissionID})
	work := append(baseDraft.Work(), addedObservation)
	draft, err := workgraph.NewDraftObservation(command.ProjectID, command.MissionID, baseDraft.PlanRevisionID(), baseDraft.PlanDigest(), approved, work)
	if err != nil {
		t.Fatal(err)
	}
	lanes, err := workgraph.CollapseLanes(draft)
	if err != nil {
		t.Fatal(err)
	}
	items := append(revisionWorkParams(command.ExpectedOld), workgraph.WorkItemV2Params{
		WorkID: "work-added", Title: "Implement independent addition", Description: "Complete the independent added work item.", DesignContext: "Use the approved design.",
		Criteria:    []workgraph.CriterionV2Params{{ID: "criterion-work-added", Text: "Run the added test.", Outcome: "The added test passes.", VerifierKind: "test", Required: true}},
		WriteScopes: []workgraph.NormalizedWriteScope{{Path: "internal/added", Domain: "added", Access: workgraph.ScopeWriteExclusive}},
	})
	observations := append(command.ExpectedOld.ProviderObservations(), workgraph.ProviderObservationV2{WorkID: "work-added", Provider: "durable_fake", ExternalID: "external-added", Revision: "provider-added"})
	command.Candidate, err = workgraph.NewRevisionV2(workgraph.RevisionV2Params{
		ID: "revision-successor-addition", ProjectID: command.ProjectID, MissionID: command.MissionID, Generation: command.ExpectedOld.Generation() + 1, PredecessorID: command.ExpectedOld.ID(),
		PlanningSourceCommit: command.ExpectedOld.PlanningSourceCommit(), MissionDefinition: command.ExpectedOld.MissionDefinition(), DependencyPolicy: command.ExpectedOld.DependencyPolicy(),
		WorkItems: items, Lanes: lanes, ProviderObservations: observations, FinalizedBy: command.ActorID, FinalizedAt: command.OccurredAt, GovernanceContextID: command.GovernanceContextID,
	})
	if err != nil {
		t.Fatal(err)
	}
	command.Drift, err = workgraph.DetectDrift(command.ExpectedOld, command.Candidate, command.OccurredAt)
	if err != nil {
		t.Fatal(err)
	}
	command.Reconciliation, err = workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: command.ReconciliationID.String(), Old: command.ExpectedOld, New: command.Candidate, Drift: command.Drift, ActorID: command.ActorID, Rationale: command.Rationale, OccurredAt: command.OccurredAt})
	if err != nil {
		t.Fatal(err)
	}
	return command
}

func revisionWorkParams(revision workgraph.RevisionV2) []workgraph.WorkItemV2Params {
	result := make([]workgraph.WorkItemV2Params, 0, len(revision.WorkItems()))
	for _, item := range revision.WorkItems() {
		criteria := make([]workgraph.CriterionV2Params, 0, len(item.Criteria()))
		for _, criterion := range item.Criteria() {
			metadata := criterion.Metadata()
			outcome, _ := metadata[workgraph.CriterionMetadataOutcome].(string)
			hint, _ := metadata[workgraph.CriterionMetadataEvidenceHint].(string)
			delete(metadata, workgraph.CriterionMetadataOutcome)
			delete(metadata, workgraph.CriterionMetadataEvidenceHint)
			criteria = append(criteria, workgraph.CriterionV2Params{ID: criterion.ID(), Text: criterion.Text(), VerifierKind: criterion.VerifierKind(), Required: criterion.Required(), Outcome: outcome, EvidenceHint: hint, Metadata: metadata})
		}
		result = append(result, workgraph.WorkItemV2Params{WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(), Criteria: criteria, Dependencies: item.Dependencies(), WriteScopes: item.WriteScopes()})
	}
	return result
}

func revisionWithProviderNoise(t *testing.T, revision workgraph.RevisionV2) workgraph.RevisionV2 {
	t.Helper()
	observations := revision.ProviderObservations()
	observations[0].Revision = "substituted-provider-revision"
	changed, err := workgraph.NewRevisionV2(workgraph.RevisionV2Params{ID: revision.ID(), ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), Generation: revision.Generation(), PredecessorID: revision.PredecessorID(), PlanningSourceCommit: revision.PlanningSourceCommit(), MissionDefinition: revision.MissionDefinition(), DependencyPolicy: revision.DependencyPolicy(), WorkItems: revisionWorkParams(revision), Lanes: revision.Lanes(), ProviderObservations: observations, FinalizedBy: revision.FinalizedBy(), FinalizedAt: revision.FinalizedAt(), GovernanceContextID: revision.GovernanceContextID()})
	if err != nil {
		t.Fatal(err)
	}
	return changed
}
