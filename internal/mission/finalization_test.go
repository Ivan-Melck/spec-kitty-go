package mission

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestFinalizationSagaObservesTwiceThenPublishesGenerationOne(t *testing.T) {
	source := &finalizationSource{values: []workgraph.FinalizationObservation{missionFinalizationObservation(t, "revision-1"), missionFinalizationObservation(t, "revision-1")}}
	store := newFinalizationStore()
	service := NewFinalizationService(source, store)
	command := finalizationCommandFixture("operation-finalize")
	result, err := service.Finalize(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() != 2 || store.publishCalls.Load() != 1 || result.Replayed || result.Revision.Generation() != 1 || result.Mission.CurrentWorkRevisionID != command.ProposedRevisionID {
		t.Fatalf("unexpected saga result: calls=%d publish=%d result=%#v", source.calls.Load(), store.publishCalls.Load(), result)
	}
	replay, err := service.Finalize(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || source.calls.Load() != 2 || store.publishCalls.Load() != 1 {
		t.Fatalf("replay crossed service boundaries: %#v", replay)
	}
}

func TestFinalizationRefusesChangedRecheckBeforePublication(t *testing.T) {
	source := &finalizationSource{values: []workgraph.FinalizationObservation{missionFinalizationObservation(t, "revision-1"), missionFinalizationObservation(t, "revision-2")}}
	store := newFinalizationStore()
	_, err := NewFinalizationService(source, store).Finalize(context.Background(), finalizationCommandFixture("operation-changed"))
	typed, ok := err.(*FinalizationError)
	if !ok || typed.Kind != FinalizationProviderChanged || typed.Recovery != FinalizationObserveAgain || store.publishCalls.Load() != 0 {
		t.Fatalf("error=%v publish=%d", err, store.publishCalls.Load())
	}
}

func TestFinalizationProviderFaultsNeverPublishOrLeakProviderOutput(t *testing.T) {
	for _, failAt := range []int64{1, 2} {
		t.Run(fmt.Sprintf("observation-%d", failAt), func(t *testing.T) {
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1"), failAt: failAt}
			store := newFinalizationStore()
			_, err := NewFinalizationService(source, store).Finalize(context.Background(), finalizationCommandFixture(ids.OperationID(fmt.Sprintf("operation-provider-fault-%d", failAt))))
			typed, ok := err.(*FinalizationError)
			if !ok || typed.Kind != FinalizationProviderFailure || typed.Recovery != FinalizationObserveAgain || store.publishCalls.Load() != 0 || strings.Contains(err.Error(), "seeded-provider-secret") || errors.Unwrap(err) != nil {
				t.Fatalf("fault was not bounded: err=%v publish=%d", err, store.publishCalls.Load())
			}
		})
	}
}

func TestFinalizationPrePublishFaultLeavesPriorStateAndRetryIsSafe(t *testing.T) {
	store := newFinalizationStore()
	store.failBeforeCommit = true
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	service := NewFinalizationService(source, store)
	command := finalizationCommandFixture("operation-pre-publish")
	if _, err := service.Finalize(context.Background(), command); err == nil || store.current != nil {
		t.Fatalf("pre-publish fault changed current state: err=%v", err)
	}
	store.failBeforeCommit = false
	if _, err := service.Finalize(context.Background(), command); err != nil || store.current == nil {
		t.Fatalf("same-operation retry did not recover: %v", err)
	}
}

func TestFinalizationDetectsStorePostconditionViolation(t *testing.T) {
	store := newFinalizationStore()
	store.violatePostcondition = true
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	_, err := NewFinalizationService(source, store).Finalize(context.Background(), finalizationCommandFixture("operation-bad-store"))
	typed, ok := err.(*FinalizationError)
	if !ok || typed.Kind != FinalizationContractFailure || typed.Recovery != FinalizationRepairPersistence || strings.Contains(err.Error(), "seeded-store-secret") {
		t.Fatalf("postcondition violation was not bounded: %v", err)
	}
}

func TestFinalizationRejectsGenerationAndOperationMutants(t *testing.T) {
	tests := map[string]func(*FinalizationCommand){"successor generation": func(c *FinalizationCommand) { c.RequestedGeneration = 2; c.PredecessorID = "revision-old" }, "expected current": func(c *FinalizationCommand) { c.ExpectedCurrentGeneration = 1 }, "wrong step": func(c *FinalizationCommand) { c.ExpectedMission.CurrentStep = StepReconciliationRequired }, "invalid planning source": func(c *FinalizationCommand) { c.PlanningSourceCommit = "not-a-git-object" }}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			command := finalizationCommandFixture(ids.OperationID("operation-mutant-" + strings.ReplaceAll(name, " ", "-")))
			mutate(&command)
			source := &finalizationSource{values: []workgraph.FinalizationObservation{missionFinalizationObservation(t, "revision-1")}}
			store := newFinalizationStore()
			if _, err := NewFinalizationService(source, store).Finalize(context.Background(), command); err == nil || source.calls.Load() != 0 || store.publishCalls.Load() != 0 {
				t.Fatalf("guard did not fire: %v", err)
			}
		})
	}
	command := finalizationCommandFixture("operation-conflict")
	store := newFinalizationStore()
	source := &finalizationSource{values: []workgraph.FinalizationObservation{missionFinalizationObservation(t, "revision-1"), missionFinalizationObservation(t, "revision-1")}}
	service := NewFinalizationService(source, store)
	if _, err := service.Finalize(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	changed := command
	changed.ProposedRevisionID = "revision-other"
	if _, err := service.Finalize(context.Background(), changed); err == nil {
		t.Fatal("operation reuse with different digest was accepted")
	}
}

func TestFinalizationConcurrentDistinctOperationsHaveOneCASWinner(t *testing.T) {
	store := newFinalizationStore()
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	service := NewFinalizationService(source, store)
	const workers = 100
	var successes atomic.Int64
	var conflicts atomic.Int64
	var wait sync.WaitGroup
	wait.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer wait.Done()
			command := finalizationCommandFixture(ids.OperationID(fmt.Sprintf("operation-race-%03d", index)))
			command.ProposedRevisionID = ids.WorkRevisionID(fmt.Sprintf("revision-race-%03d", index))
			_, err := service.Finalize(context.Background(), command)
			if err == nil {
				successes.Add(1)
				return
			}
			if typed, ok := err.(*FinalizationError); ok && typed.Kind == FinalizationCurrentConflict {
				conflicts.Add(1)
			}
		}(i)
	}
	wait.Wait()
	if successes.Load() != 1 || conflicts.Load() != workers-1 || store.publishCalls.Load() != workers {
		t.Fatalf("success=%d conflicts=%d publish=%d", successes.Load(), conflicts.Load(), store.publishCalls.Load())
	}
}

func TestFinalizationConcurrentSameOperationConvergesOnOneGeneration(t *testing.T) {
	store := newFinalizationStore()
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	service := NewFinalizationService(source, store)
	command := finalizationCommandFixture("operation-same-race")
	const workers = 100
	var successes atomic.Int64
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			result, err := service.Finalize(context.Background(), command)
			if err == nil && result.Revision.ID() == command.ProposedRevisionID {
				successes.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != workers || store.current == nil || len(store.operations) != 1 {
		t.Fatalf("same-operation convergence: successes=%d current=%v operations=%d", successes.Load(), store.current != nil, len(store.operations))
	}
}

func TestFinalizationAmbiguousCommittedResponseReplaysWithoutProvider(t *testing.T) {
	store := newFinalizationStore()
	store.loseFirstResponse = true
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	service := NewFinalizationService(source, store)
	command := finalizationCommandFixture("operation-lost-response")
	if _, err := service.Finalize(context.Background(), command); err == nil {
		t.Fatal("lost response was not surfaced")
	}
	calls := source.calls.Load()
	result, err := service.Finalize(context.Background(), command)
	if err != nil || !result.Replayed || source.calls.Load() != calls {
		t.Fatalf("ambiguous replay=%#v err=%v calls=%d/%d", result, err, calls, source.calls.Load())
	}
}

type finalizationSource struct {
	mu       sync.Mutex
	values   []workgraph.FinalizationObservation
	fallback workgraph.FinalizationObservation
	calls    atomic.Int64
	failAt   int64
}

func (source *finalizationSource) ObserveFinalization(context.Context, ids.MissionID) (workgraph.FinalizationObservation, error) {
	call := source.calls.Add(1)
	if source.failAt == call {
		return workgraph.FinalizationObservation{}, fmt.Errorf("seeded-provider-secret")
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.values) > 0 {
		value := source.values[0]
		source.values = source.values[1:]
		return value, nil
	}
	return source.fallback, nil
}

type memoryFinalizationStore struct {
	mu                   sync.Mutex
	operations           map[ids.OperationID]storedFinalization
	current              *FinalizationResult
	publishCalls         atomic.Int64
	loseFirstResponse    bool
	failBeforeCommit     bool
	violatePostcondition bool
}
type storedFinalization struct {
	digest string
	result FinalizationResult
}

func newFinalizationStore() *memoryFinalizationStore {
	return &memoryFinalizationStore{operations: map[ids.OperationID]storedFinalization{}}
}
func (store *memoryFinalizationStore) ReplayFinalization(_ context.Context, _ ids.MissionID, operation ids.OperationID, digest string) (FinalizationResult, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.operations[operation]
	if !ok {
		return FinalizationResult{}, false, nil
	}
	if record.digest != digest {
		return FinalizationResult{}, false, &FinalizationError{Kind: FinalizationOperationConflict, Recovery: FinalizationCorrectInput, Field: "operation_id", Message: "operation identity is bound to different content"}
	}
	return record.result, true, nil
}
func (store *memoryFinalizationStore) PublishGenerationOne(_ context.Context, commit FinalizationCommit) (FinalizationResult, error) {
	store.publishCalls.Add(1)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.failBeforeCommit {
		return FinalizationResult{}, fmt.Errorf("seeded-store-secret")
	}
	if record, ok := store.operations[commit.OperationID]; ok {
		if record.digest != commit.RequestDigest {
			return FinalizationResult{}, &FinalizationError{Kind: FinalizationOperationConflict, Recovery: FinalizationCorrectInput, Field: "operation_id", Message: "operation identity is bound to different content"}
		}
		result := record.result
		result.Replayed = true
		return result, nil
	}
	if store.current != nil {
		return FinalizationResult{}, &FinalizationError{Kind: FinalizationCurrentConflict, Recovery: FinalizationReconcileCurrent, Field: "current_generation", Message: "a current generation already exists"}
	}
	snapshot := commit.ExpectedMission.clone()
	snapshot.CurrentStep = StepWorkFinalized
	snapshot.CurrentWorkRevisionID = commit.Candidate.ID()
	snapshot.StateVersion++
	snapshot.Lifecycle = LifecycleTerminal
	snapshot.TerminalOutcome = OutcomeWorkFinalized
	snapshot.UpdatedAt = commit.OccurredAt
	result := FinalizationResult{Revision: commit.Candidate, Mission: snapshot, RequestDigest: commit.RequestDigest}
	if store.violatePostcondition {
		result.Mission.StateVersion++
	}
	store.current = &result
	store.operations[commit.OperationID] = storedFinalization{commit.RequestDigest, result}
	if store.loseFirstResponse {
		store.loseFirstResponse = false
		return FinalizationResult{}, fmt.Errorf("response lost")
	}
	return result, nil
}

func finalizationCommandFixture(operation ids.OperationID) FinalizationCommand {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	return FinalizationCommand{MissionID: "mission-final", ProjectID: "project-final", ExpectedMission: MissionSnapshot{MissionID: "mission-final", ProjectID: "project-final", DefinitionID: "software-development", DefinitionVersion: 1, DefinitionCommitment: DefinitionCommitment(strings.Repeat("b", 64)), Lifecycle: LifecycleOpen, CurrentStep: StepWorkDraftReady, StateVersion: 7, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}, ExpectedCurrentGeneration: 0, ProposedRevisionID: "revision-final", RequestedGeneration: 1, PlanningSourceCommit: strings.Repeat("a", 40), ActorID: "actor-finalizer", OperationID: operation, GovernanceContextID: "governance-final", OccurredAt: now}
}

func missionFinalizationObservation(t *testing.T, revision string) workgraph.FinalizationObservation {
	t.Helper()
	work := make([]workgraph.WorkObservation, 0, 2)
	approved := make([]workgraph.ApprovedScope, 0, 2)
	for index, id := range []ids.WorkID{"work-a", "work-b"} {
		scopeID := "scope-" + id.String()
		dependencies := []ids.WorkID{}
		if index == 1 {
			dependencies = []ids.WorkID{"work-a"}
		}
		item, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-final", WorkID: id, Title: "Implement " + id.String(), Description: "Complete the bounded work item.", DesignContext: "Use the approved design.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-" + id.String(), Text: "Run the test.", Outcome: "The test passes.", Verifier: "test", Requirement: workgraph.CriterionRequired}}, Dependencies: dependencies, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/" + string('a'+rune(index)), Domain: "domain", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: scopeID, Kind: workgraph.ScopePlanConcern}}})
		if err != nil {
			t.Fatal(err)
		}
		work = append(work, item)
		approved = append(approved, workgraph.ApprovedScope{ID: scopeID, Kind: workgraph.ScopePlanConcern, MissionID: "mission-final"})
	}
	draft, err := workgraph.NewDraftObservation("project-final", "mission-final", "plan-revision", strings.Repeat("c", 64), approved, work)
	if err != nil {
		t.Fatal(err)
	}
	sumA := sha256.Sum256([]byte("work-a"))
	sumB := sha256.Sum256([]byte("work-b"))
	observation, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-a", Provider: "durable_fake", ExternalID: "external-a", Revision: revision, MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sumA), CriterionEvidenceHints: map[string]string{"criterion-work-a": "go test output"}}, {WorkID: "work-b", Provider: "durable_fake", ExternalID: "external-b", Revision: "revision-b", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sumB), CriterionEvidenceHints: map[string]string{"criterion-work-b": "go test output"}}})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
