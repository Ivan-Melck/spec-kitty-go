package mission

import (
	"bytes"
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
	if _, err := SealInitialWorkRevisionSelectionV2(result); err != nil {
		t.Fatalf("validated publication did not mint work-revision authority: %v", err)
	}
	if _, err := SealInitialWorkRevisionSelectionV2(FinalizationResult{Revision: result.Revision, Mission: result.Mission, RequestDigest: result.RequestDigest, Receipt: result.Receipt}); !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidRequestV2) {
		t.Fatalf("caller-authored finalization result minted work-revision authority: %v", err)
	}
	expectedReceipt, err := finalizationOutcomeReceipt(result.Revision, result.Mission, command.MissionID, command.OperationID, result.RequestDigest)
	if err != nil || result.Receipt.Validate() != nil || result.Receipt != expectedReceipt {
		t.Fatalf("initial publication receipt is not bound to its complete outcome: receipt=%q expected=%q err=%v", result.Receipt, expectedReceipt, err)
	}
	replay, err := service.Finalize(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !replay.Replayed || source.calls.Load() != 2 || store.publishCalls.Load() != 1 {
		t.Fatalf("replay crossed service boundaries: %#v", replay)
	}
	if _, err := SealInitialWorkRevisionSelectionV2(replay); err != nil {
		t.Fatalf("validated replay lost work-revision authority: %v", err)
	}
}

func TestFinalizationIsCapabilityReadyWithoutWorkDraftPhaseAndDoesNotCompleteMission(t *testing.T) {
	command := finalizationCommandFixture("operation-capability-ready")
	command.ExpectedMission.CurrentStep = StepPlanApproved
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	result, err := NewFinalizationService(source, newFinalizationStore()).Finalize(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Mission.CurrentStep != StepPlanApproved || result.Mission.Lifecycle != LifecycleOpen || result.Mission.TerminalOutcome != "" || result.Mission.CurrentWorkRevisionID != command.ProposedRevisionID {
		t.Fatalf("finalization recreated phase ceremony or terminalized mission: %+v", result.Mission)
	}
}

func TestFinalizationReplayBindsEveryValidExpectedMissionField(t *testing.T) {
	mutants := map[string]func(*FinalizationCommand){
		"mission identity": func(command *FinalizationCommand) {
			command.MissionID = "mission-other"
			command.ExpectedMission.MissionID = command.MissionID
		},
		"project identity": func(command *FinalizationCommand) {
			command.ProjectID = "project-other"
			command.ExpectedMission.ProjectID = command.ProjectID
		},
		"definition commitment": func(command *FinalizationCommand) {
			command.ExpectedMission.DefinitionCommitment = DefinitionCommitment(strings.Repeat("d", 64))
		},
		"state version": func(command *FinalizationCommand) { command.ExpectedMission.StateVersion++ },
		"created at": func(command *FinalizationCommand) {
			command.ExpectedMission.CreatedAt = command.ExpectedMission.CreatedAt.Add(time.Second)
		},
		"updated at plus one second": func(command *FinalizationCommand) {
			command.ExpectedMission.UpdatedAt = command.ExpectedMission.UpdatedAt.Add(time.Second)
		},
		"active blocker membership": func(command *FinalizationCommand) {
			command.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-new"}
		},
		"unresolved decision membership": func(command *FinalizationCommand) {
			command.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-new"}
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			store := newFinalizationStore()
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
			service := NewFinalizationService(source, store)
			command := finalizationCommandFixture("operation-mission-identity")
			if _, err := service.Finalize(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			providerCalls := source.calls.Load()
			publishCalls := store.publishCalls.Load()
			mutate(&command)
			_, err := service.Finalize(context.Background(), command)
			typed, ok := err.(*FinalizationError)
			if !ok || typed.Kind != FinalizationOperationConflict || typed.Recovery != FinalizationCorrectInput || source.calls.Load() != providerCalls || store.publishCalls.Load() != publishCalls {
				t.Fatalf("mission identity mutation crossed replay boundary: err=%v provider=%d/%d publish=%d/%d", err, source.calls.Load(), providerCalls, store.publishCalls.Load(), publishCalls)
			}
		})
	}
}

func TestFinalizationCommandDigestBindsCompleteExpectedMissionSnapshot(t *testing.T) {
	mutants := map[string]func(*MissionSnapshot){
		"mission ID":         func(snapshot *MissionSnapshot) { snapshot.MissionID = "mission-other" },
		"project ID":         func(snapshot *MissionSnapshot) { snapshot.ProjectID = "project-other" },
		"definition ID":      func(snapshot *MissionSnapshot) { snapshot.DefinitionID = "other-definition" },
		"definition version": func(snapshot *MissionSnapshot) { snapshot.DefinitionVersion++ },
		"definition commitment": func(snapshot *MissionSnapshot) {
			snapshot.DefinitionCommitment = DefinitionCommitment(strings.Repeat("d", 64))
		},
		"lifecycle":            func(snapshot *MissionSnapshot) { snapshot.Lifecycle = LifecycleTerminal },
		"current step":         func(snapshot *MissionSnapshot) { snapshot.CurrentStep = StepReconciliationRequired },
		"current revision":     func(snapshot *MissionSnapshot) { snapshot.CurrentWorkRevisionID = "revision-other" },
		"state version":        func(snapshot *MissionSnapshot) { snapshot.StateVersion++ },
		"created at":           func(snapshot *MissionSnapshot) { snapshot.CreatedAt = snapshot.CreatedAt.Add(time.Second) },
		"updated at":           func(snapshot *MissionSnapshot) { snapshot.UpdatedAt = snapshot.UpdatedAt.Add(time.Second) },
		"terminal outcome":     func(snapshot *MissionSnapshot) { snapshot.TerminalOutcome = OutcomeWorkFinalized },
		"active blockers":      func(snapshot *MissionSnapshot) { snapshot.ActiveBlockers = []ids.BlockerID{"blocker-new"} },
		"unresolved decisions": func(snapshot *MissionSnapshot) { snapshot.UnresolvedDecisions = []ids.DecisionID{"decision-new"} },
	}
	command := finalizationCommandFixture("operation-complete-mission-digest")
	baseline, err := finalizationCommandDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			changed := command
			changed.ExpectedMission = command.ExpectedMission.clone()
			mutate(&changed.ExpectedMission)
			digest, err := finalizationCommandDigest(changed)
			if err != nil || digest == baseline {
				t.Fatalf("expected mission field is absent from request identity: digest=%q baseline=%q err=%v", digest, baseline, err)
			}
		})
	}
}

func TestFinalizationExpectedMissionSetIdentityIsCanonical(t *testing.T) {
	tests := map[string]struct {
		initial func(*FinalizationCommand)
		replay  func(*FinalizationCommand)
	}{
		"reordering unique sets": {
			initial: func(command *FinalizationCommand) {
				command.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-b", "blocker-a"}
				command.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-b", "decision-a"}
			},
			replay: func(command *FinalizationCommand) {
				command.ExpectedMission.ActiveBlockers = []ids.BlockerID{"blocker-a", "blocker-b"}
				command.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{"decision-a", "decision-b"}
			},
		},
		"nil and empty sets": {
			replay: func(command *FinalizationCommand) {
				command.ExpectedMission.ActiveBlockers = []ids.BlockerID{}
				command.ExpectedMission.UnresolvedDecisions = []ids.DecisionID{}
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			store := newFinalizationStore()
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
			service := NewFinalizationService(source, store)
			command := finalizationCommandFixture("operation-set-identity")
			if test.initial != nil {
				test.initial(&command)
			}
			originalBlockers := append([]ids.BlockerID(nil), command.ExpectedMission.ActiveBlockers...)
			originalDecisions := append([]ids.DecisionID(nil), command.ExpectedMission.UnresolvedDecisions...)
			if _, err := service.Finalize(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			if !sameBlockerOrder(originalBlockers, command.ExpectedMission.ActiveBlockers) || !sameDecisionOrder(originalDecisions, command.ExpectedMission.UnresolvedDecisions) {
				t.Fatal("canonical identity mutated the caller snapshot")
			}
			providerCalls := source.calls.Load()
			publishCalls := store.publishCalls.Load()
			test.replay(&command)
			replayBlockers := append([]ids.BlockerID(nil), command.ExpectedMission.ActiveBlockers...)
			replayDecisions := append([]ids.DecisionID(nil), command.ExpectedMission.UnresolvedDecisions...)
			result, err := service.Finalize(context.Background(), command)
			if err != nil || !result.Replayed || source.calls.Load() != providerCalls || store.publishCalls.Load() != publishCalls {
				t.Fatalf("equivalent set identity did not replay: result=%#v err=%v provider=%d/%d publish=%d/%d", result, err, source.calls.Load(), providerCalls, store.publishCalls.Load(), publishCalls)
			}
			if !sameBlockerOrder(replayBlockers, command.ExpectedMission.ActiveBlockers) || !sameDecisionOrder(replayDecisions, command.ExpectedMission.UnresolvedDecisions) {
				t.Fatal("canonical replay identity mutated the caller snapshot")
			}
		})
	}
}

func TestFinalizationRejectsInvalidExpectedMissionIdentityBeforePorts(t *testing.T) {
	mutants := map[string]func(*FinalizationCommand){
		"definition ID":      func(command *FinalizationCommand) { command.ExpectedMission.DefinitionID = "other-definition" },
		"definition version": func(command *FinalizationCommand) { command.ExpectedMission.DefinitionVersion = 2 },
		"lifecycle": func(command *FinalizationCommand) {
			command.ExpectedMission.Lifecycle = LifecycleTerminal
			command.ExpectedMission.TerminalOutcome = OutcomeWorkFinalized
		},
		"current pointer": func(command *FinalizationCommand) {
			command.ExpectedMission.CurrentWorkRevisionID = "revision-other"
		},
		"terminal outcome": func(command *FinalizationCommand) { command.ExpectedMission.TerminalOutcome = OutcomeWorkFinalized },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			store := newFinalizationStore()
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
			service := NewFinalizationService(source, store)
			command := finalizationCommandFixture("operation-invalid-mission-identity")
			if _, err := service.Finalize(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			providerCalls := source.calls.Load()
			publishCalls := store.publishCalls.Load()
			replayCalls := store.replayCalls.Load()
			mutate(&command)
			_, err := service.Finalize(context.Background(), command)
			typed, ok := err.(*FinalizationError)
			if !ok || typed.Kind != FinalizationInvalidRequest || source.calls.Load() != providerCalls || store.publishCalls.Load() != publishCalls || store.replayCalls.Load() != replayCalls {
				t.Fatalf("invalid mission identity reached a port: err=%v replay=%d/%d provider=%d/%d publish=%d/%d", err, store.replayCalls.Load(), replayCalls, source.calls.Load(), providerCalls, store.publishCalls.Load(), publishCalls)
			}
		})
	}
}

func sameBlockerOrder(left, right []ids.BlockerID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameDecisionOrder(left, right []ids.DecisionID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestFinalizationReplayValidatesCompletePersistencePostconditionsBeforeSuccess(t *testing.T) {
	mutants := map[string]func(t *testing.T, result *FinalizationResult){
		"zero revision": func(_ *testing.T, result *FinalizationResult) { result.Revision = workgraph.RevisionV2{} },
		"wrong revision": func(t *testing.T, result *FinalizationResult) {
			encoded, err := result.Revision.Encode()
			if err != nil {
				t.Fatal(err)
			}
			encoded = bytes.Replace(encoded, []byte(`"work_revision_id":"revision-final"`), []byte(`"work_revision_id":"revision-other"`), 1)
			revision, err := workgraph.DecodeRevisionV2(encoded)
			if err != nil {
				t.Fatal(err)
			}
			result.Revision = revision
		},
		"wrong mission": func(_ *testing.T, result *FinalizationResult) { result.Mission.MissionID = "mission-other" },
		"wrong current pointer": func(_ *testing.T, result *FinalizationResult) {
			result.Mission.CurrentWorkRevisionID = "revision-other"
		},
		"wrong state version": func(_ *testing.T, result *FinalizationResult) { result.Mission.StateVersion++ },
		"wrong definition": func(_ *testing.T, result *FinalizationResult) {
			result.Mission.DefinitionCommitment = DefinitionCommitment(strings.Repeat("d", 64))
		},
		"wrong request digest": func(_ *testing.T, result *FinalizationResult) { result.RequestDigest = strings.Repeat("0", 64) },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			store := newFinalizationStore()
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
			service := NewFinalizationService(source, store)
			command := finalizationCommandFixture("operation-corrupt-replay")
			if _, err := service.Finalize(context.Background(), command); err != nil {
				t.Fatal(err)
			}
			calls := source.calls.Load()
			record := store.operations[command.OperationID]
			mutate(t, &record.result)
			store.operations[command.OperationID] = record
			_, err := service.Finalize(context.Background(), command)
			typed, ok := err.(*FinalizationError)
			if !ok || typed.Kind != FinalizationContractFailure || typed.Recovery != FinalizationRepairPersistence || source.calls.Load() != calls || store.publishCalls.Load() != 1 {
				t.Fatalf("corrupt replay crossed boundary: err=%v provider=%d/%d publish=%d", err, source.calls.Load(), calls, store.publishCalls.Load())
			}
		})
	}
}

func TestFinalizationReceiptRejectsSameIDCompleteOutcomeSubstitution(t *testing.T) {
	mutants := map[string]func(t *testing.T, command FinalizationCommand, result *FinalizationResult){
		"pinned provider revision": func(t *testing.T, _ FinalizationCommand, result *FinalizationResult) {
			encoded, err := result.Revision.Encode()
			if err != nil {
				t.Fatal(err)
			}
			encoded = bytes.Replace(encoded, []byte(`"revision":"revision-1"`), []byte(`"revision":"revision-substituted"`), 1)
			changed, err := workgraph.DecodeRevisionV2(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if changed.ID() != result.Revision.ID() || changed.GraphDigest() != result.Revision.GraphDigest() || changed.CriteriaDigest() != result.Revision.CriteriaDigest() {
				t.Fatal("opaque substitution changed semantic identity")
			}
			result.Revision = changed
		},
		"same ID semantic revision": func(t *testing.T, command FinalizationCommand, result *FinalizationResult) {
			alternateStore := newFinalizationStore()
			alternateSource := &finalizationSource{fallback: missionFinalizationObservationWithTitle(t, "revision-1", "Changed bounded title")}
			alternate, err := NewFinalizationService(alternateSource, alternateStore).Finalize(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if alternate.Revision.ID() != result.Revision.ID() || alternate.Revision.GraphDigest() == result.Revision.GraphDigest() {
				t.Fatal("semantic alternate fixture is invalid")
			}
			result.Revision = alternate.Revision
		},
		"mission outcome": func(_ *testing.T, _ FinalizationCommand, result *FinalizationResult) {
			result.Mission.UpdatedAt = result.Mission.UpdatedAt.Add(time.Second)
		},
		"missing receipt": func(_ *testing.T, _ FinalizationCommand, result *FinalizationResult) { result.Receipt = "" },
		"malformed receipt": func(_ *testing.T, _ FinalizationCommand, result *FinalizationResult) {
			result.Receipt = "not-a-receipt"
		},
		"altered receipt": func(_ *testing.T, _ FinalizationCommand, result *FinalizationResult) {
			result.Receipt = FinalizationReceipt(strings.Repeat("0", 64))
		},
		"request binding": func(t *testing.T, command FinalizationCommand, result *FinalizationResult) {
			receipt, err := finalizationOutcomeReceipt(result.Revision, result.Mission, command.MissionID, command.OperationID, strings.Repeat("f", 64))
			if err != nil {
				t.Fatal(err)
			}
			result.Receipt = receipt
		},
		"operation binding": func(t *testing.T, command FinalizationCommand, result *FinalizationResult) {
			receipt, err := finalizationOutcomeReceipt(result.Revision, result.Mission, command.MissionID, "operation-other", result.RequestDigest)
			if err != nil {
				t.Fatal(err)
			}
			result.Receipt = receipt
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			store := newFinalizationStore()
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
			service := NewFinalizationService(source, store)
			command := finalizationCommandFixture("operation-receipt-mutant")
			initial, err := service.Finalize(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if initial.Receipt == "" || store.operations[command.OperationID].result.Receipt != initial.Receipt {
				t.Fatal("initial publication did not return atomically bound receipt")
			}
			calls := source.calls.Load()
			record := store.operations[command.OperationID]
			mutate(t, command, &record.result)
			store.operations[command.OperationID] = record
			_, err = service.Finalize(context.Background(), command)
			typed, ok := err.(*FinalizationError)
			if !ok || typed.Kind != FinalizationContractFailure || typed.Recovery != FinalizationRepairPersistence || source.calls.Load() != calls || store.publishCalls.Load() != 1 {
				t.Fatalf("receipt mutant crossed boundary: err=%v provider=%d/%d publish=%d", err, source.calls.Load(), calls, store.publishCalls.Load())
			}
		})
	}
}

func TestFinalizationInitialPublicationRejectsReceiptContractViolation(t *testing.T) {
	mutants := map[string]func(*FinalizationResult){
		"missing receipt":   func(result *FinalizationResult) { result.Receipt = "" },
		"malformed receipt": func(result *FinalizationResult) { result.Receipt = "not-a-receipt" },
		"wrong receipt":     func(result *FinalizationResult) { result.Receipt = FinalizationReceipt(strings.Repeat("0", 64)) },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			store := newFinalizationStore()
			store.mutateResponse = mutate
			source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
			_, err := NewFinalizationService(source, store).Finalize(context.Background(), finalizationCommandFixture("operation-initial-receipt-mutant"))
			typed, ok := err.(*FinalizationError)
			if !ok || typed.Kind != FinalizationContractFailure || typed.Recovery != FinalizationRepairPersistence || source.calls.Load() != 2 || store.publishCalls.Load() != 1 {
				t.Fatalf("initial receipt mutant was accepted: err=%v provider=%d publish=%d", err, source.calls.Load(), store.publishCalls.Load())
			}
		})
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
	tests := map[string]func(*FinalizationCommand){"successor generation": func(c *FinalizationCommand) { c.RequestedGeneration = 2; c.PredecessorID = "revision-old" }, "expected current": func(c *FinalizationCommand) { c.ExpectedCurrentGeneration = 1 }, "invalid planning source": func(c *FinalizationCommand) { c.PlanningSourceCommit = "not-a-git-object" }, "unsupported definition version": func(c *FinalizationCommand) { c.ExpectedMission.DefinitionVersion = 2 }}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			command := finalizationCommandFixture(ids.OperationID("operation-mutant-" + strings.ReplaceAll(name, " ", "-")))
			mutate(&command)
			source := &finalizationSource{values: []workgraph.FinalizationObservation{missionFinalizationObservation(t, "revision-1")}}
			store := newFinalizationStore()
			if _, err := NewFinalizationService(source, store).Finalize(context.Background(), command); err == nil || source.calls.Load() != 0 || store.replayCalls.Load() != 0 || store.publishCalls.Load() != 0 {
				t.Fatalf("guard did not fire before ports: %v replay=%d provider=%d publish=%d", err, store.replayCalls.Load(), source.calls.Load(), store.publishCalls.Load())
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
	var receiptMu sync.Mutex
	var winnerReceipt FinalizationReceipt
	var wait sync.WaitGroup
	wait.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer wait.Done()
			command := finalizationCommandFixture(ids.OperationID(fmt.Sprintf("operation-race-%03d", index)))
			command.ProposedRevisionID = ids.WorkRevisionID(fmt.Sprintf("revision-race-%03d", index))
			result, err := service.Finalize(context.Background(), command)
			if err == nil {
				successes.Add(1)
				receiptMu.Lock()
				winnerReceipt = result.Receipt
				receiptMu.Unlock()
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
	if store.current == nil || winnerReceipt == "" || winnerReceipt != store.current.Receipt || winnerReceipt.Validate() != nil {
		t.Fatalf("winning publication did not preserve its exact receipt: winner=%q current=%v", winnerReceipt, store.current != nil)
	}
}

func TestFinalizationConcurrentSameOperationConvergesOnOneGeneration(t *testing.T) {
	store := newFinalizationStore()
	source := &finalizationSource{fallback: missionFinalizationObservation(t, "revision-1")}
	service := NewFinalizationService(source, store)
	command := finalizationCommandFixture("operation-same-race")
	const workers = 100
	var successes atomic.Int64
	var receiptMu sync.Mutex
	receipts := map[FinalizationReceipt]bool{}
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			result, err := service.Finalize(context.Background(), command)
			if err == nil && result.Revision.ID() == command.ProposedRevisionID {
				successes.Add(1)
				receiptMu.Lock()
				receipts[result.Receipt] = true
				receiptMu.Unlock()
			}
		}()
	}
	wait.Wait()
	if successes.Load() != workers || store.current == nil || len(store.operations) != 1 || len(receipts) != 1 || receipts[""] {
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
	persistedReceipt := store.operations[command.OperationID].result.Receipt
	result, err := service.Finalize(context.Background(), command)
	if err != nil || !result.Replayed || source.calls.Load() != calls {
		t.Fatalf("ambiguous replay=%#v err=%v calls=%d/%d", result, err, calls, source.calls.Load())
	}
	if result.Receipt == "" || result.Receipt != persistedReceipt {
		t.Fatal("lost-response replay did not return original receipt")
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
	replayCalls          atomic.Int64
	loseFirstResponse    bool
	failBeforeCommit     bool
	violatePostcondition bool
	mutateResponse       func(*FinalizationResult)
}
type storedFinalization struct {
	digest string
	result FinalizationResult
}

func newFinalizationStore() *memoryFinalizationStore {
	return &memoryFinalizationStore{operations: map[ids.OperationID]storedFinalization{}}
}
func (store *memoryFinalizationStore) ReplayFinalization(_ context.Context, _ ids.MissionID, operation ids.OperationID, digest string) (FinalizationResult, bool, error) {
	store.replayCalls.Add(1)
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
	snapshot.CurrentWorkRevisionID = commit.Candidate.ID()
	snapshot.StateVersion++
	snapshot.UpdatedAt = commit.OccurredAt
	result := FinalizationResult{Revision: commit.Candidate, Mission: snapshot, RequestDigest: commit.RequestDigest, Receipt: commit.ExpectedReceipt}
	if store.violatePostcondition {
		result.Mission.StateVersion++
	}
	store.current = &result
	store.operations[commit.OperationID] = storedFinalization{commit.RequestDigest, result}
	if store.loseFirstResponse {
		store.loseFirstResponse = false
		return FinalizationResult{}, fmt.Errorf("response lost")
	}
	response := result
	if store.mutateResponse != nil {
		store.mutateResponse(&response)
	}
	return response, nil
}

func finalizationCommandFixture(operation ids.OperationID) FinalizationCommand {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	return FinalizationCommand{MissionID: "mission-final", ProjectID: "project-final", ExpectedMission: MissionSnapshot{MissionID: "mission-final", ProjectID: "project-final", DefinitionID: "software-development", DefinitionVersion: 1, DefinitionCommitment: DefinitionCommitment(strings.Repeat("b", 64)), Lifecycle: LifecycleOpen, CurrentStep: StepWorkDraftReady, StateVersion: 7, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute)}, ExpectedCurrentGeneration: 0, ProposedRevisionID: "revision-final", RequestedGeneration: 1, PlanningSourceCommit: strings.Repeat("a", 40), ActorID: "actor-finalizer", OperationID: operation, GovernanceContextID: "governance-final", OccurredAt: now}
}

func missionFinalizationObservation(t *testing.T, revision string) workgraph.FinalizationObservation {
	return missionFinalizationObservationWithTitle(t, revision, "")
}

func missionFinalizationObservationWithTitle(t *testing.T, revision, title string) workgraph.FinalizationObservation {
	t.Helper()
	work := make([]workgraph.WorkObservation, 0, 2)
	approved := make([]workgraph.ApprovedScope, 0, 2)
	for index, id := range []ids.WorkID{"work-a", "work-b"} {
		scopeID := "scope-" + id.String()
		dependencies := []ids.WorkID{}
		if index == 1 {
			dependencies = []ids.WorkID{"work-a"}
		}
		workTitle := "Implement " + id.String()
		if title != "" && index == 0 {
			workTitle = title
		}
		item, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-final", WorkID: id, Title: workTitle, Description: "Complete the bounded work item.", DesignContext: "Use the approved design.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-" + id.String(), Text: "Run the test.", Outcome: "The test passes.", Verifier: "test", Requirement: workgraph.CriterionRequired}}, Dependencies: dependencies, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/" + string('a'+rune(index)), Domain: "domain", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: scopeID, Kind: workgraph.ScopePlanConcern}}})
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
