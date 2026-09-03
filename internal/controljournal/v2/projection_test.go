package v2

import (
	"bytes"
	"context"
	"fmt"
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

func TestOperationSummaryBoundariesAreAttributableDefensiveAndStateAware(t *testing.T) {
	publicationID := testPublicationID("operation-summary")
	inputs := []OperationStateInput{
		{OperationID: "operation-accepted", LogicalVersion: 3, Status: StatusAccepted, EventIDs: []EventID{"event-a"}, LastPublicationID: publicationID},
		{OperationID: "operation-reconcile", LogicalVersion: 7, Status: StatusReconciliationRequired, EffectIDs: []EffectID{"effect-r"}, LastPublicationID: publicationID},
		{OperationID: "operation-resolved", LogicalVersion: 11, Status: StatusResolved, ResultKind: "mission.result", ResultSchemaVersion: 1,
			CanonicalResult: []byte(`{"secret":"must-not-enter-boundary"}`), EventIDs: []EventID{"event-z"}, LastPublicationID: publicationID},
	}
	states := make([]OperationStateV2, len(inputs))
	for index, input := range inputs {
		states[index] = mustState(t, input)
	}
	summary, err := BuildOperationSummaryV2(states)
	if err != nil || ValidateOperationSummaryV2(summary, states) != nil {
		t.Fatalf("valid attributable summary rejected: %v", err)
	}
	if summary.Total() != uint64(len(states)) || summary.StatusCount(StatusAccepted) != 1 ||
		summary.StatusCount(StatusReconciliationRequired) != 1 || summary.StatusCount(StatusResolved) != 1 {
		t.Fatalf("summary counts do not derive from states: %+v", summary)
	}
	boundaries := summary.Boundaries()
	for _, state := range states {
		matches := 0
		for _, boundary := range boundaries {
			if boundary.MatchesOperationID(state.OperationID()) {
				matches++
				if boundary.LogicalVersion() != state.LogicalVersion() || boundary.LeafDigest().IsZero() || boundary.OperationIDLookupDigest().IsZero() {
					t.Fatal("matched boundary lost fixed proof fields")
				}
			}
		}
		if matches != 1 {
			t.Fatalf("operation %q matched %d boundaries", state.OperationID(), matches)
		}
	}
	boundaries[0].logicalVersion++
	if summary.Boundaries()[0] == boundaries[0] {
		t.Fatal("boundary accessor aliases summary storage")
	}
	canonical, err := operationSummaryCanonicalV2(summary)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(canonical, []byte("operation-resolved")) || bytes.Contains(canonical, []byte("must-not-enter-boundary")) {
		t.Fatal("variable operation identity or terminal result leaked into summary")
	}

	structuralMutants := map[string]func(*OperationSummaryV2){
		"reordered": func(v *OperationSummaryV2) {
			v.boundaries[0], v.boundaries[1] = v.boundaries[1], v.boundaries[0]
		},
		"duplicate": func(v *OperationSummaryV2) {
			v.boundaries[1] = v.boundaries[0]
		},
		"omitted": func(v *OperationSummaryV2) {
			v.boundaries = v.boundaries[1:]
		},
		"regressed": func(v *OperationSummaryV2) {
			v.boundaries[0].logicalVersion = 0
		},
	}
	for name, mutate := range structuralMutants {
		t.Run(name, func(t *testing.T) {
			candidate := cloneOperationSummary(summary)
			mutate(&candidate)
			if err := validateOperationSummaryStructure(candidate); err == nil {
				t.Fatal("structurally invalid operation summary accepted")
			}
		})
	}

	leafMutant := cloneOperationSummary(summary)
	leafMutant.boundaries[0].leafDigest = digest("substituted-leaf", nil)
	leafMutant.digest, err = digestOperationSummary(leafMutant)
	if err != nil || validateOperationSummaryStructure(leafMutant) != nil {
		t.Fatalf("controlled structurally valid leaf mutant is invalid: %v", err)
	}
	if err := ValidateOperationSummaryV2(leafMutant, states); err == nil {
		t.Fatal("state-aware validation accepted a substituted leaf")
	}
	mismatched := append([]OperationStateV2(nil), states...)
	mismatched[0].lastPublicationID = testPublicationID("other-publication")
	if err := ValidateOperationSummaryV2(summary, mismatched); err == nil {
		t.Fatal("state-aware validation accepted a different publication boundary")
	}
}

func TestOperationSummaryStructuredTenThousandBoundaryBudget(t *testing.T) {
	states := make([]OperationStateV2, maxOperationBoundariesV2)
	publicationID := testPublicationID("maximum-operation-summary")
	for index := range states {
		states[index] = mustState(t, OperationStateInput{
			OperationID:       OperationID(fmt.Sprintf("operation-max-%05d", index)),
			LogicalVersion:    LogicalVersion(^uint64(0)),
			Status:            StatusAccepted,
			LastPublicationID: publicationID,
		})
	}
	summary, err := BuildOperationSummaryV2(states)
	if err != nil {
		t.Fatalf("10,000 maximum-version boundaries rejected: %v", err)
	}
	canonical, err := operationSummaryCanonicalV2(summary)
	if err != nil {
		t.Fatalf("final structured summary exceeded %d bytes: %v", MaxOperationSummaryBytesV2, err)
	}
	if len(canonical) > MaxOperationSummaryBytesV2 || !bytes.Contains(canonical, []byte(`"boundaries":[{"operation_id_sha256"`)) ||
		!bytes.Contains(canonical, []byte(`"summary_sha256"`)) || bytes.Contains(canonical, []byte("operation-max-00000")) {
		t.Fatalf("summary wire is not bounded structured proof: bytes=%d", len(canonical))
	}
	if err := ValidateOperationSummaryV2(summary, states); err != nil {
		t.Fatalf("state-aware validation rejected maximum census: %v", err)
	}
	if _, err := BuildOperationSummaryV2(make([]OperationStateV2, maxOperationBoundariesV2+1)); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("plus-one census reached state inspection or allocation: %v", err)
	}
}

func TestProjectionPrefixAndReducerDigestHelpersMatchLiveSemantics(t *testing.T) {
	canonical := []byte(`{"state":"planned"}`)
	stateDigest, err := ReducerStateDigestV2(canonical)
	if err != nil || stateDigest != digest("spec-kitty.reducer-state.v2", canonical) {
		t.Fatalf("shared reducer-state digest drifted: %s %v", stateDigest, err)
	}
	if _, err := ReducerStateDigestV2([]byte(`{ "state": "planned" }`)); !IsKind(err, ErrorInvalidCanonicalData) {
		t.Fatalf("non-canonical reducer state accepted: %v", err)
	}

	plan := projectionPlan(t, "operation-prefix", "mission-prefix", "planned")
	tracker := fixtureProjectionTracker(t)
	coordinator, _ := NewPublicationCoordinatorV2(&fixedRevisionPortV2{revisions: []uint64{37}}, tracker)
	if _, err := coordinator.Publish(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	sealed, err := coordinator.SealProjectionPrefix()
	if err != nil {
		t.Fatal(err)
	}
	want, err := AdvanceProjectionPrefixV2(ProjectionPrefixGenesisV2(), 37, plan.PublicationID(), plan.Digest(), plan.RecordSetDigest())
	if err != nil || want != sealed.RollingPrefixDigest() {
		t.Fatalf("live prefix differs from shared restore fold: %s %s %v", want, sealed.RollingPrefixDigest(), err)
	}
	for name, run := range map[string]func() error{
		"zero_prior": func() error {
			_, err := AdvanceProjectionPrefixV2(Digest{}, 37, plan.PublicationID(), plan.Digest(), plan.RecordSetDigest())
			return err
		},
		"zero_revision": func() error {
			_, err := AdvanceProjectionPrefixV2(ProjectionPrefixGenesisV2(), 0, plan.PublicationID(), plan.Digest(), plan.RecordSetDigest())
			return err
		},
		"bad_publication": func() error {
			_, err := AdvanceProjectionPrefixV2(ProjectionPrefixGenesisV2(), 37, "bad", plan.Digest(), plan.RecordSetDigest())
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(); err == nil {
				t.Fatal("invalid prefix coordinate accepted")
			}
		})
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
