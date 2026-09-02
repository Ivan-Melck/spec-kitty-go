package v2

import (
	"bytes"
	"context"
	"testing"
)

func TestProjectionRefusesDigestMismatchWithoutAdvancing(t *testing.T) {
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	plan.heads[1].CanonicalStateDigest = digest("wrong", nil)
	// Resealing is intentionally impossible after this internal controlled
	// mutant, so construct a correctly attributed acknowledgement against the
	// mutant's still-original sealed identity and prove projection validation is
	// independently fail-closed.
	tracker := fixtureProjectionTracker(t)
	tracker.pending[plan.PublicationID()] = plan.Digest()
	observation, _ := NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), 1, 1, plan.MutationCount())
	seal, _ := acknowledgementSeal(plan, observation)
	ack := AcknowledgedPublicationV2{plan: plan, observation: observation, seal: seal}
	if err := tracker.applyAcknowledged(ack); !IsKind(err, ErrorInvalidDigest) {
		t.Fatalf("planned-head digest mismatch advanced projection: %v", err)
	}
	if tracker.LedgerSize() != 0 || tracker.HighestObservedRevision() != 0 || len(tracker.states) != 0 {
		t.Fatal("failed projection application left partial state")
	}
}

func TestProjectionRejectsConflictingProviderRevision(t *testing.T) {
	left := projectionPlan(t, "operation-01", "mission-01", "left")
	right := projectionPlan(t, "operation-02", "mission-02", "right")
	tracker := fixtureProjectionTracker(t)
	port := &fixedRevisionPortV2{revisions: []uint64{7, 7}}
	coordinator, _ := NewPublicationCoordinatorV2(port, tracker)
	if _, err := coordinator.Publish(context.Background(), left); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(context.Background(), right); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("conflicting provider revision accepted: %v", err)
	}
	if tracker.LedgerSize() != 1 || coordinator.UnresolvedCount() != 1 {
		t.Fatal("conflicting revision mutated acknowledged ledger or lost ambiguity")
	}
}

func TestSealedProjectionPrefixRejectsZeroPartialAndSubstitution(t *testing.T) {
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	tracker := fixtureProjectionTracker(t)
	port := &fixedRevisionPortV2{revisions: []uint64{9}}
	coordinator, _ := NewPublicationCoordinatorV2(port, tracker)
	if _, err := coordinator.Publish(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	sealed, err := coordinator.SealProjectionPrefix()
	if err != nil || sealed.Validate() != nil {
		t.Fatalf("valid projection seal rejected: %v", err)
	}
	if err := (SealedProjectionPrefixV2{}).Validate(); err == nil {
		t.Fatal("zero projection seal accepted")
	}
	partial := sealed
	partial.seal = Digest{}
	if err := partial.Validate(); err == nil {
		t.Fatal("partial projection seal accepted")
	}
	substituted := sealed
	substituted.entries[0].canonical = []byte(`{"substituted":true}`)
	if err := substituted.Validate(); err == nil {
		t.Fatal("substituted projection state accepted")
	}
	summary := sealed.OperationSummary()
	if summary.Total() != 1 || summary.StatusCount(StatusAccepted) != 1 || len(summary.LeafDigests()) != 1 || summary.Digest().IsZero() {
		t.Fatalf("operation summary is not compact and complete: %+v", summary)
	}
}

func TestOperationProjectionEqualsDurableHeadByteForByte(t *testing.T) {
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	tracker := fixtureProjectionTracker(t)
	coordinator, _ := NewPublicationCoordinatorV2(&fixedRevisionPortV2{revisions: []uint64{19}}, tracker)
	if _, err := coordinator.Publish(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	coordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	projection, ok := tracker.CurrentProjection(coordinate)
	if !ok {
		t.Fatal("operation projection is absent")
	}
	var durable []byte
	for _, record := range plan.Records() {
		if record.Role() == RecordOperationHead {
			durable = record.CanonicalValue()
		}
	}
	if len(durable) == 0 || !bytes.Equal(projection.CanonicalState(), durable) {
		t.Fatalf("operation projection differs from durable head\nprojection=%s\ndurable=%s", projection.CanonicalState(), durable)
	}
	decoded, err := DecodeOperationStateV2(projection.CanonicalState())
	if err != nil || decoded.LastPublicationID() != plan.PublicationID() {
		t.Fatalf("envelope publication identity was not reduced into operation state: %v", err)
	}
}

func TestSealedPrefixIsCompleteCompactCheckpointConsumerInput(t *testing.T) {
	secretResult := []byte(`{"secret_terminal_result":"must-not-enter-checkpoint"}`)
	plan := terminalCheckpointPlan(t, secretResult)
	tracker := fixtureProjectionTracker(t)
	coordinator, _ := NewPublicationCoordinatorV2(&fixedRevisionPortV2{revisions: []uint64{23}}, tracker)
	if _, err := coordinator.Publish(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	sealed, err := coordinator.SealProjectionPrefix()
	if err != nil || sealed.Validate() != nil {
		t.Fatalf("valid checkpoint input rejected: %v", err)
	}

	// This shape is all WP04 needs: compatibility/watermark/prefix identities,
	// a complete non-operation aggregate census, and compact operation census.
	type checkpointAggregate struct {
		coordinate  AggregateCoordinate
		version     LogicalVersion
		lastEvent   EventID
		tombstone   bool
		state       []byte
		stateDigest Digest
	}
	entries := sealed.Entries()
	census := make([]checkpointAggregate, len(entries))
	for index, entry := range entries {
		census[index] = checkpointAggregate{entry.Coordinate(), entry.LogicalVersion(), entry.LastEventID(), entry.Tombstone(), entry.CanonicalState(), entry.StateDigest()}
	}
	if sealed.Watermark() != 23 || sealed.EventRegistryDigest().IsZero() || sealed.ReducerRegistryDigest().IsZero() ||
		sealed.CodecVersion() != CanonicalCodecVersionV2 || sealed.RollingPrefixDigest().IsZero() || sealed.ProjectionDigest().IsZero() {
		t.Fatal("checkpoint compatibility or prefix coordinate is incomplete")
	}
	if len(census) != 1 || census[0].coordinate.Kind != "mission" || census[0].lastEvent == "" || !census[0].tombstone ||
		census[0].version != 1 || census[0].stateDigest.IsZero() || len(census[0].state) == 0 {
		t.Fatalf("checkpoint aggregate census is incomplete: %+v", census)
	}
	summary := sealed.OperationSummary()
	if summary.Total() != 1 || summary.StatusCount(StatusResolved) != 1 || len(summary.LeafDigests()) != 1 || summary.Digest().IsZero() {
		t.Fatalf("compact operation census is incomplete: %+v", summary)
	}
	for _, entry := range entries {
		if bytes.Contains(entry.CanonicalState(), secretResult) {
			t.Fatal("terminal operation result leaked into checkpoint aggregate state")
		}
	}
	mutatedEvent := sealed
	mutatedEvent.entries = append([]ProjectionEntryV2(nil), sealed.entries...)
	mutatedEvent.entries[0].lastEventID = "event-substituted"
	if err := mutatedEvent.Validate(); err == nil {
		t.Fatal("terminal EventID substitution escaped projection seal")
	}
	mutatedTombstone := sealed
	mutatedTombstone.entries = append([]ProjectionEntryV2(nil), sealed.entries...)
	mutatedTombstone.entries[0].tombstone = false
	if err := mutatedTombstone.Validate(); err == nil {
		t.Fatal("tombstone substitution escaped projection seal")
	}
}

func terminalCheckpointPlan(t *testing.T, result []byte) SealedCommitPlanV2 {
	t.Helper()
	operationID, missionID := OperationID("operation-terminal"), MissionID("mission-terminal")
	scope := Scope{Kind: ScopeMission, ProjectID: "project-01", MissionID: missionID}
	intent, _ := CanonicalIntentDigest(StableIntentInput{"mission.complete", 1, scope, []byte(`{"complete":true}`)})
	binding, _ := NewOperationBindingV2(OperationBindingInput{OperationID: operationID, CommandKind: "mission.complete", CommandSchemaVersion: 1,
		CanonicalIntentDigest: intent, Scope: scope, OriginatingRuntimeEpochID: "epoch-01", ObservedCreatedAt: "2026-09-02T12:00:00Z"})
	opEventID, missionEventID := EventID("event-operation-terminal"), EventID("event-mission-terminal")
	eventIDs := []EventID{missionEventID, opEventID}
	state, err := NewOperationStateV2(OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: StatusResolved,
		ResultKind: "mission.result", ResultSchemaVersion: 1, CanonicalResult: result, EventIDs: eventIDs})
	if err != nil {
		t.Fatal(err)
	}
	opCoordinate, _ := NewAggregateCoordinate("operation", AggregateID(operationID), Scope{Kind: ScopeHost})
	missionCoordinate, _ := NewAggregateCoordinate("mission", AggregateID(missionID), scope)
	missionPayload := []byte(`{"status":"completed"}`)
	events := []EventDraftV2{
		{EventID: opEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: opCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-01", Payload: operationPayload(t, state)},
		{EventID: missionEventID, EventType: "mission.completed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-01", Payload: missionPayload},
	}
	heads := []HeadTransitionV2{
		{Coordinate: opCoordinate, NextLogicalVersion: 1, EventID: opEventID, CommittedRuntimeEpochID: "epoch-01", CanonicalStateDigest: digest("operation.preimage", operationPayload(t, state)), AuthoritySummary: []byte(`{"status":"resolved"}`)},
		{Coordinate: missionCoordinate, NextLogicalVersion: 1, EventID: missionEventID, CommittedRuntimeEpochID: "epoch-01", CanonicalStateDigest: digest("spec-kitty.reducer-state.v2", missionPayload), AuthoritySummary: missionPayload, Tombstone: true},
	}
	plan, err := SealCommitPlanV2(CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads, Conditions: headConditions(t, heads)}, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
