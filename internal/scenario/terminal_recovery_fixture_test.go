package scenario

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type terminalRecoveryPort struct {
	coordinator *journalv2.PublicationCoordinatorV2
	before      execution.ExecutionSlotStateV2
	ack         journalv2.AcknowledgedPublicationV2
	epoch       journalv2.RuntimeEpochID
}

func (p *terminalRecoveryPort) ResolveExecutionOperation(context.Context, journalv2.OperationBindingV2) (execution.ExecutionOperationResolutionV2, error) {
	return execution.ExecutionOperationResolutionV2{Kind: execution.ExecutionOperationAbsentV2}, nil
}
func (p *terminalRecoveryPort) ObserveExecutionEligibility(context.Context, ids.MissionID, journalv2.EligibilityShardID) (workgraph.ObservedEligibilityShardV2, error) {
	return workgraph.ObservedEligibilityShardV2{}, fmt.Errorf("rebind must not request eligibility")
}
func (p *terminalRecoveryPort) ObserveExecutionSlot(context.Context, journalv2.AggregateCoordinate) (execution.ExecutionSlotObservationV2, error) {
	return execution.ExecutionSlotObservationV2{State: &p.before, RecordRevision: 2}, nil
}
func (p *terminalRecoveryPort) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return p.epoch, nil
}
func (p *terminalRecoveryPort) PublishExecution(ctx context.Context, plan execution.ExecutionAuthorityPlanV2) (execution.ExecutionPublicationAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return execution.ExecutionPublicationAckV2{}, err
	}
	ack, err := p.coordinator.Publish(ctx, sealed)
	if err != nil {
		return execution.ExecutionPublicationAckV2{}, err
	}
	p.ack = ack
	return execution.NewExecutionPublicationAckV2(plan, ack)
}

// Both histories have the same common seed. Only the second issues a real
// typed execution rebind; that successful result is checked against its ACK.
func terminalRecoveryFixture(t *testing.T, rebind bool) terminalSemanticProgress {
	t.Helper()
	baseline := terminalFixture(t, false)
	h := baseline.History
	reducers, err := productionruntime.BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	durable := &terminalFixturePort{revision: 1, records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(durable, tracker)
	if err != nil {
		t.Fatal(err)
	}
	_, governanceContext := twoItemRevision(t, "project-fixture", "mission-fixture")
	governance, err := execution.ExecutionGovernanceFromContextV2(governanceContext)
	if err != nil {
		t.Fatal(err)
	}
	before := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: "project-fixture", MissionID: "mission-fixture", WorkRevisionID: "revision-fixture", WorkID: "work-a", LogicalVersion: 1, RunID: "run-a", ActorID: "actor-a", ProcessID: "process-a", LeaseID: "lease-a", Generation: 1, Fence: strings.Repeat("a", 32), HeartbeatAt: "2026-09-05T12:00:00Z", Deadline: "2026-09-05T13:00:00Z", RuntimeEpochID: "epoch-old", Status: execution.ExecutionActiveV2, Governance: governance}
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-fixture", MissionID: "mission-fixture"}
	intent, _ := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "fixture.seed", CommandSchemaVersion: 1, Scope: scope, Command: before.CanonicalBytes()})
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: "operation-seed", CommandKind: "fixture.seed", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, ActorID: "actor-a", ProcessID: "process-a", OriginatingRuntimeEpochID: "epoch-old", ObservedCreatedAt: "2026-09-05T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "execution_slot.result", ResultSchemaVersion: 1, CanonicalResult: before.CanonicalBytes(), EventIDs: []journalv2.EventID{"event-seed-operation", "event-seed-slot"}})
	if err != nil {
		t.Fatal(err)
	}
	opCoordinate, _ := journalv2.NewAggregateCoordinate("operation", "operation-seed", journalv2.Scope{Kind: journalv2.ScopeHost})
	slotCoordinate, _ := execution.ExecutionSlotCoordinateV2(before.ProjectID, before.MissionID, before.WorkRevisionID, before.WorkID)
	operationPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(state)
	slotPayload := mustTerminalJSON(execution.ExecutionSlotEventV2{SchemaVersion: 1, Kind: "run_admitted_lease_granted", ObservedAt: before.HeartbeatAt, State: before})
	revisions := []string{"config." + governance.PolicyRevision, "doctrine." + governance.DoctrineRevision, "mission_type." + governance.MissionTypeRevision, "profile." + governance.ProfileRevision}
	sort.Strings(revisions)
	events := []journalv2.EventDraftV2{{EventID: "event-seed-operation", EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: before.RuntimeEpochID, ActorID: "actor-a", ProcessID: "process-a", Payload: operationPayload}, {EventID: "event-seed-slot", EventType: "execution_slot.run_admitted_lease_granted", Family: "execution_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: slotCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: before.RuntimeEpochID, ActorID: "actor-a", ProcessID: "process-a", WorkRevisionID: journalv2.WorkRevisionID(before.WorkRevisionID), PolicyRevision: governance.PolicyRevision, GovernanceRevisions: revisions, Payload: slotPayload}}
	input := journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events}
	for _, event := range events {
		digest, _ := journalv2.ReducerStateDigestV2(event.Payload)
		summary := event.Payload
		if event.Family == "execution_slot" {
			summary = before.CanonicalBytes()
			digest, _ = journalv2.ReducerStateDigestV2(summary)
		}
		head := journalv2.HeadTransitionV2{Coordinate: event.Coordinate, NextLogicalVersion: 1, EventID: event.EventID, CommittedRuntimeEpochID: before.RuntimeEpochID, CanonicalStateDigest: digest, AuthoritySummary: summary}
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			t.Fatal(err)
		}
		input.Heads = append(input.Heads, head)
		input.Conditions = append(input.Conditions, condition)
	}
	plan, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	seed, err := coordinator.Publish(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	acks := []journalv2.AcknowledgedPublicationV2{seed}
	currentExecution := before
	if rebind {
		port := &terminalRecoveryPort{coordinator: coordinator, before: before, epoch: "epoch-new"}
		service, err := execution.NewExecutionAuthorityServiceV2(port, port, journalv2.DefaultEventRegistryV2())
		if err != nil {
			t.Fatal(err)
		}
		deadline, _ := time.Parse(time.RFC3339Nano, before.Deadline)
		q := execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionRebindV2, OperationID: "operation-extra-typed-recovery", ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, RunID: before.RunID, ActorID: before.ActorID, ProcessID: before.ProcessID, LeaseID: before.LeaseID, Generation: before.Generation, Fence: before.Fence, ObservedAt: time.Date(2026, 9, 5, 12, 20, 0, 0, time.UTC), Deadline: deadline, RuntimeEpochID: port.epoch, Governance: governanceContext, OperationEventID: "event-typed-recovery-operation", SlotEventID: "event-typed-recovery-slot"}
		result, err := service.Execute(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		currentExecution = result.State
		acks = append(acks, port.ack)
		h.Recovery[q.OperationID] = scenarioRecoveryCommand{ExecutionRequest: &q, Before: before, After: result.State}
	}
	capture, _ := journalv2.NewStoreRevisionV2("database-fixture", durable.revision, durable.revision)
	for i := range h.Publications {
		h.Publications[i].Proof.CapturedCoordinate = capture
	}
	h.Progress.ProjectionRevision = durable.revision
	for _, ack := range acks {
		b := *ack.Plan().BindingCreate()
		ba, _ := journalv2.NewOperationBindingAddressV2(b.OperationID())
		wa, _ := ack.PublicationID().WitnessAddressV2()
		s := ack.Plan().OperationState()
		encoded, _ := journalv2.EncodeOperationStateV2(s)
		item := productionruntime.ProgressItem{Family: "operation", AggregateID: string(s.OperationID()), LogicalVersion: uint64(s.LogicalVersion()), LastPublicationID: string(s.LastPublicationID()), AuthorityState: string(s.Status()), CanonicalState: encoded}
		proof := productionruntime.AcknowledgedPublicationProof{Binding: b, BindingRecord: durable.records[ba], Witness: ack.Plan().PublicationWitness(), WitnessRecord: durable.records[wa], HistoricalOperationState: s, CurrentState: s, CurrentProgressItem: item, CapturedCoordinate: capture}
		for _, event := range ack.Plan().Events() {
			address, _ := journalv2.NewJournalEventAddressV2(event.Coordinate(), event.LogicalVersion())
			record := durable.records[address]
			proof.JournalRecords = append(proof.JournalRecords, record)
			if event.Family() == "operation" {
				proof.HistoricalOperationRecord = record
			}
		}
		h.Publications = append(h.Publications, terminalPublicationReceipt{Ack: ack, Proof: proof, Source: "typed-command-success"})
		h.Progress.Operations = append(h.Progress.Operations, item)
		h.Progress.Items = append(h.Progress.Items, item)
		h.Inspections[item.AggregateID] = item
	}
	h.Progress.Items = append(h.Progress.Items, productionruntime.ProgressItem{Family: "execution_slot", AggregateID: string(slotCoordinate.ID), LogicalVersion: currentExecution.LogicalVersion, CanonicalState: currentExecution.CanonicalBytes(), AuthorityState: string(currentExecution.Status)})
	h.HistorySHA256 = terminalHistoryDigest(h)
	excluded, err := validateTerminalHistory(h)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := terminalBusinessOutcome(h, nil, repositoryTerminalEvidence{}, excluded)
	if err != nil {
		t.Fatal(err)
	}
	outcome.History = h
	return outcome
}
func TestTerminalRestartAcceptsOnlyActuallyTypedRecovery(t *testing.T) {
	a, b := terminalRecoveryFixture(t, false), terminalRecoveryFixture(t, true)
	if len(b.History.Progress.Operations) != len(a.History.Progress.Operations)+1 || len(b.Operations) != len(a.Operations) {
		t.Fatal("full census lost extra recovery or business census retained it")
	}
	if err := validateTerminalRestartEquivalence(a, b); err != nil {
		t.Fatal(err)
	}
	// Dropping the typed-success classification cannot hide an extra operation.
	b.History.Recovery = nil
	b.History.HistorySHA256 = terminalHistoryDigest(b.History)
	if validateTerminalRestartEquivalence(a, b) == nil {
		t.Fatal("unclassified operation hidden")
	}
}
