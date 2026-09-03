package v2

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type observingPortV2 struct {
	tracker              *ProjectionTrackerV2
	revision             atomic.Uint64
	err                  error
	registeredBeforeCall atomic.Bool
}

func (p *observingPortV2) Commit(_ context.Context, plan SealedCommitPlanV2) (CommitObservationV2, error) {
	p.tracker.mu.Lock()
	_, registered := p.tracker.pending[plan.PublicationID()]
	p.tracker.mu.Unlock()
	p.registeredBeforeCall.Store(registered)
	if p.err != nil {
		return CommitObservationV2{}, p.err
	}
	revision := p.revision.Add(1)
	return NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), revision, revision, plan.MutationCount())
}

func TestCoordinatorRegistersBeforeIOAndKeepsAmbiguityUnresolved(t *testing.T) {
	tracker := fixtureProjectionTracker(t)
	port := &observingPortV2{tracker: tracker, err: errors.New("delivery uncertain")}
	coordinator, err := NewPublicationCoordinatorV2(port, tracker)
	if err != nil {
		t.Fatal(err)
	}
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	if _, err := coordinator.Publish(context.Background(), plan); err == nil {
		t.Fatal("ambiguous delivery reported success")
	}
	if !port.registeredBeforeCall.Load() || coordinator.UnresolvedCount() != 1 || tracker.LedgerSize() != 0 {
		t.Fatal("coordinator did not register before I/O or advanced projection optimistically")
	}
	if _, err := coordinator.SealProjectionPrefix(); !IsKind(err, ErrorAmbiguous) {
		t.Fatalf("unresolved publication did not block barrier: %v", err)
	}
}

type reversePortV2 struct {
	entered   chan PublicationID
	releases  map[PublicationID]chan struct{}
	revisions map[PublicationID]uint64
}

func (p *reversePortV2) Commit(ctx context.Context, plan SealedCommitPlanV2) (CommitObservationV2, error) {
	p.entered <- plan.PublicationID()
	select {
	case <-ctx.Done():
		return CommitObservationV2{}, ctx.Err()
	case <-p.releases[plan.PublicationID()]:
	}
	revision := p.revisions[plan.PublicationID()]
	return NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), revision, revision, plan.MutationCount())
}

func TestConcurrentReverseCompletionFoldsProviderRevisionOrder(t *testing.T) {
	left := projectionPlan(t, "operation-11", "mission-11", "left")
	right := projectionPlan(t, "operation-12", "mission-12", "right")
	tracker := fixtureProjectionTracker(t)
	port := &reversePortV2{entered: make(chan PublicationID, 2), releases: map[PublicationID]chan struct{}{
		left.PublicationID(): make(chan struct{}), right.PublicationID(): make(chan struct{}),
	}, revisions: map[PublicationID]uint64{left.PublicationID(): 11, right.PublicationID(): 12}}
	coordinator, _ := NewPublicationCoordinatorV2(port, tracker)
	type result struct {
		ack AcknowledgedPublicationV2
		err error
	}
	results := make(chan result, 2)
	go func() { ack, err := coordinator.Publish(context.Background(), left); results <- result{ack, err} }()
	go func() { ack, err := coordinator.Publish(context.Background(), right); results <- result{ack, err} }()
	for index := 0; index < 2; index++ {
		select {
		case <-port.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("coordinator held its mutex across durable I/O")
		}
	}
	close(port.releases[right.PublicationID()])
	first := <-results
	if first.err != nil || first.ack.ProviderRevision() != 12 {
		t.Fatalf("revision 12 did not complete first: %+v", first)
	}
	if tracker.HighestObservedRevision() != 12 || tracker.SealedWatermark() != 0 {
		t.Fatal("highest observed revision was confused with sealed watermark")
	}
	close(port.releases[left.PublicationID()])
	second := <-results
	if second.err != nil || second.ack.ProviderRevision() != 11 {
		t.Fatalf("revision 11 did not complete second: %+v", second)
	}
	reverseSeal, err := coordinator.SealProjectionPrefix()
	if err != nil || reverseSeal.Validate() != nil || reverseSeal.Watermark() != 12 {
		t.Fatalf("reverse seal invalid: %+v %v", reverseSeal, err)
	}

	sequentialTracker := fixtureProjectionTracker(t)
	sequentialPort := &fixedRevisionPortV2{revisions: []uint64{11, 12}}
	sequential, _ := NewPublicationCoordinatorV2(sequentialPort, sequentialTracker)
	if _, err := sequential.Publish(context.Background(), left); err != nil {
		t.Fatal(err)
	}
	if _, err := sequential.Publish(context.Background(), right); err != nil {
		t.Fatal(err)
	}
	orderedSeal, err := sequential.SealProjectionPrefix()
	if err != nil {
		t.Fatal(err)
	}
	if reverseSeal.RollingPrefixDigest() != orderedSeal.RollingPrefixDigest() {
		t.Fatal("rolling prefix followed response completion rather than provider revision")
	}
}

type fixedRevisionPortV2 struct {
	mu        sync.Mutex
	revisions []uint64
	next      int
}

func (p *fixedRevisionPortV2) Commit(_ context.Context, plan SealedCommitPlanV2) (CommitObservationV2, error) {
	p.mu.Lock()
	revision := p.revisions[p.next]
	p.next++
	p.mu.Unlock()
	return NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), revision, revision, plan.MutationCount())
}

func TestAcknowledgementAttributionAndOpaqueSealMutants(t *testing.T) {
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	valid, _ := NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), 7, 7, plan.MutationCount())
	mutants := map[string]func(*CommitObservationV2){
		"publication": func(v *CommitObservationV2) { v.publicationID = testPublicationID("other") },
		"plan":        func(v *CommitObservationV2) { v.planDigest = digest("other", nil) },
		"record_set":  func(v *CommitObservationV2) { v.recordSetDigest = digest("other", nil) },
		"count":       func(v *CommitObservationV2) { v.mutationCount++ },
		"revision":    func(v *CommitObservationV2) { v.revision = 0 },
		"durability":  func(v *CommitObservationV2) { v.durableThrough = v.revision - 1 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			tracker := fixtureProjectionTracker(t)
			coordinator, _ := NewPublicationCoordinatorV2(&observingPortV2{tracker: tracker}, tracker)
			if err := coordinator.register(plan); err != nil {
				t.Fatal(err)
			}
			candidate := valid
			mutate(&candidate)
			if _, err := coordinator.ReconcileComplete(plan, candidate); err == nil || coordinator.UnresolvedCount() != 1 || tracker.LedgerSize() != 0 {
				t.Fatalf("misattributed acknowledgement advanced projection: %v", err)
			}
		})
	}
	tracker := fixtureProjectionTracker(t)
	if err := tracker.applyAcknowledged(AcknowledgedPublicationV2{}); err == nil {
		t.Fatal("zero acknowledgement seal accepted")
	}
	partial := AcknowledgedPublicationV2{plan: plan, observation: valid}
	if err := tracker.applyAcknowledged(partial); err == nil {
		t.Fatal("partial acknowledgement seal accepted")
	}
}

func TestProjectionLedgerAdmitsTenThousandAndRefusesOverflow(t *testing.T) {
	tracker := fixtureProjectionTracker(t)
	revisions := make([]uint64, MaxAcknowledgementsBetweenSeals+1)
	for index := range revisions {
		revisions[index] = uint64(index + 1)
	}
	port := &fixedRevisionPortV2{revisions: revisions}
	coordinator, _ := NewPublicationCoordinatorV2(port, tracker)
	for index := 0; index < MaxAcknowledgementsBetweenSeals; index++ {
		plan := operationOnlyPlan(t, index)
		if _, err := coordinator.Publish(context.Background(), plan); err != nil {
			t.Fatalf("publication %d within ledger budget rejected: %v", index, err)
		}
	}
	if tracker.LedgerSize() != MaxAcknowledgementsBetweenSeals {
		t.Fatal("ledger did not retain exact admitted capacity")
	}
	sealed, err := coordinator.SealProjectionPrefix()
	if err != nil || sealed.Validate() != nil || sealed.Watermark() != MaxAcknowledgementsBetweenSeals {
		t.Fatalf("ten-thousand-publication seal failed: %v", err)
	}

	overflowTracker := fixtureProjectionTracker(t)
	for index := 0; index < MaxAcknowledgementsBetweenSeals; index++ {
		overflowTracker.ledger[uint64(index+1)] = acknowledgedLedgerEntryV2{revision: uint64(index + 1), publicationID: testPublicationID(fmt.Sprintf("p-%d", index)), planDigest: digest("plan", []byte(fmt.Sprint(index))), recordSetDigest: digest("records", []byte(fmt.Sprint(index)))}
	}
	plan := operationOnlyPlan(t, MaxAcknowledgementsBetweenSeals+1)
	if err := overflowTracker.registerPublication(plan); err != nil {
		t.Fatal(err)
	}
	observation, _ := NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), uint64(MaxAcknowledgementsBetweenSeals+1), uint64(MaxAcknowledgementsBetweenSeals+1), plan.MutationCount())
	seal, _ := acknowledgementSeal(plan, observation)
	ack := AcknowledgedPublicationV2{plan: plan, observation: observation, seal: seal}
	if err := overflowTracker.applyAcknowledged(ack); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("first over-budget acknowledgement accepted: %v", err)
	}
}

func fixtureProjectionTracker(t *testing.T) *ProjectionTrackerV2 {
	t.Helper()
	reducers, err := NewReducerSetV2(DefaultEventRegistryV2(), fixtureReducerRegistrations(nil))
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := NewProjectionTrackerV2(reducers, CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

func projectionPlan(t *testing.T, operationID OperationID, missionID MissionID, stateValue string) SealedCommitPlanV2 {
	t.Helper()
	scope := Scope{Kind: ScopeMission, ProjectID: "project-01", MissionID: missionID}
	intent, _ := CanonicalIntentDigest(StableIntentInput{"mission.advance", 1, scope, []byte(fmt.Sprintf(`{"state":%q}`, stateValue))})
	binding, err := NewOperationBindingV2(OperationBindingInput{OperationID: operationID, CommandKind: "mission.advance", CommandSchemaVersion: 1,
		CanonicalIntentDigest: intent, Scope: scope, OriginatingRuntimeEpochID: "epoch-01", ObservedCreatedAt: "2026-09-02T12:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	opEventID, missionEventID := EventID("event-operation-"+string(operationID)), EventID("event-mission-"+string(operationID))
	eventIDs := []EventID{missionEventID, opEventID}
	if eventIDs[1] < eventIDs[0] {
		eventIDs[0], eventIDs[1] = eventIDs[1], eventIDs[0]
	}
	operationState, err := NewOperationStateV2(OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: StatusAccepted, EventIDs: eventIDs})
	if err != nil {
		t.Fatal(err)
	}
	operationCoordinate, _ := NewAggregateCoordinate("operation", AggregateID(operationID), Scope{Kind: ScopeHost})
	missionCoordinate, _ := NewAggregateCoordinate("mission", AggregateID(missionID), scope)
	missionPayload := []byte(fmt.Sprintf(`{"state":%q}`, stateValue))
	events := []EventDraftV2{
		{EventID: opEventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-01", Payload: operationPayload(t, operationState)},
		{EventID: missionEventID, EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-01", Payload: missionPayload},
	}
	heads := []HeadTransitionV2{
		{Coordinate: operationCoordinate, NextLogicalVersion: 1, EventID: opEventID, CommittedRuntimeEpochID: "epoch-01", CanonicalStateDigest: digest("spec-kitty.reducer-state.v2", operationPayload(t, operationState)), AuthoritySummary: []byte(`{"status":"accepted"}`)},
		{Coordinate: missionCoordinate, NextLogicalVersion: 1, EventID: missionEventID, CommittedRuntimeEpochID: "epoch-01", CanonicalStateDigest: digest("spec-kitty.reducer-state.v2", missionPayload), AuthoritySummary: missionPayload},
	}
	input := CommitPlanInput{OperationBinding: &binding, OperationState: operationState, Events: events, Heads: heads, Conditions: headConditions(t, heads)}
	plan, err := SealCommitPlanV2(input, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func operationOnlyPlan(t *testing.T, index int) SealedCommitPlanV2 {
	t.Helper()
	operationID := OperationID(fmt.Sprintf("operation-%05d", index))
	scope := Scope{Kind: ScopeHost}
	intent, _ := CanonicalIntentDigest(StableIntentInput{"runtime.noop", 1, scope, []byte(fmt.Sprintf(`{"index":%d}`, index))})
	binding, _ := NewOperationBindingV2(OperationBindingInput{OperationID: operationID, CommandKind: "runtime.noop", CommandSchemaVersion: 1,
		CanonicalIntentDigest: intent, Scope: scope, OriginatingRuntimeEpochID: "epoch-01", ObservedCreatedAt: "2026-09-02T12:00:00Z"})
	eventID := EventID(fmt.Sprintf("event-%05d", index))
	state, _ := NewOperationStateV2(OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: StatusAccepted, EventIDs: []EventID{eventID}})
	coordinate, _ := NewAggregateCoordinate("operation", AggregateID(operationID), scope)
	head := HeadTransitionV2{Coordinate: coordinate, NextLogicalVersion: 1, EventID: eventID, CommittedRuntimeEpochID: "epoch-01",
		CanonicalStateDigest: digest("spec-kitty.reducer-state.v2", operationPayload(t, state)), AuthoritySummary: []byte(`{"status":"accepted"}`)}
	input := CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: []EventDraftV2{{EventID: eventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-01", Payload: operationPayload(t, state)}}, Heads: []HeadTransitionV2{head}, Conditions: []ProviderCondition{mustHeadCondition(t, head)}}
	plan, err := SealCommitPlanV2(input, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
