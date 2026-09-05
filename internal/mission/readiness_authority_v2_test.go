package mission

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestReadinessAuthorityStateHasNoPhaseCursorAndFactDigestIgnoresActivityOrder(t *testing.T) {
	first := readinessAuthorityStateFixture(t, 3, 9, "2026-09-04T12:03:00Z")
	second := readinessAuthorityStateFixture(t, 19, 41, "2026-09-04T13:00:00Z")
	if bytes.Contains(first.CanonicalBytes(), []byte("step")) || bytes.Contains(first.CanonicalBytes(), []byte("successor")) || bytes.Contains(first.CanonicalBytes(), []byte("phase")) {
		t.Fatalf("new authority encoded ceremony: %s", first.CanonicalBytes())
	}
	if first.AuthorityFactSetDigest() != second.AuthorityFactSetDigest() {
		t.Fatalf("activity/version metadata changed authority fact digest: %s != %s", first.AuthorityFactSetDigest(), second.AuthorityFactSetDigest())
	}
	decoded, err := DecodeReadinessAuthorityStateV2(first.CanonicalBytes())
	if err != nil || decoded.Digest() != first.Digest() {
		t.Fatalf("state did not round trip: digest=%s err=%v", decoded.Digest(), err)
	}
}

func TestReadinessAuthorityReducerReplaysCoarseLifecycleAndRejectsLegacyPhaseState(t *testing.T) {
	created := readinessAuthorityStateFixture(t, 0, 1, "2026-09-04T12:00:00Z")
	created.input.ActivityCount = 0
	created.input.Version = 1
	created.input.LastOperationID = "operation-readiness-create"
	created, _ = NewReadinessAuthorityStateV2(created.input)
	payload, err := NewReadinessAuthorityEventV2(ReadinessEventCreatedV2, ReadinessAuthorityStateV2{}, created)
	if err != nil {
		t.Fatal(err)
	}
	envelope := readinessEnvelope(t, payload, "event-readiness-create", 1)
	got, err := ReduceMissionEventV2([]byte(`{}`), envelope)
	if err != nil || !bytes.Equal(got, created.CanonicalBytes()) {
		t.Fatalf("created reduce=%s err=%v", got, err)
	}

	pausedInput := created.input
	pausedInput.Version = 2
	pausedInput.Lifecycle = MissionPaused
	pausedInput.LastOperationID = "operation-readiness-pause"
	pausedInput.ObservedAt = "2026-09-04T12:01:00Z"
	paused, _ := NewReadinessAuthorityStateV2(pausedInput)
	pausePayload, _ := NewReadinessAuthorityEventV2(ReadinessEventPausedV2, created, paused)
	got, err = ReduceMissionEventV2(got, readinessEnvelope(t, pausePayload, "event-readiness-pause", 2))
	if err != nil || !bytes.Equal(got, paused.CanonicalBytes()) {
		t.Fatalf("pause reduce=%s err=%v", got, err)
	}

	legacy, _ := NewMissionAuthorityStateV2(MissionAuthorityStateInputV2{
		ProjectID: "project-readiness", MissionID: "mission-readiness", DefinitionRevision: "definition-v1",
		DefinitionDigest: readinessDigest(t, strings.Repeat("a", 64)), Version: 1, Status: AuthorityMissionActiveV2,
		Step: "planning", Decomposition: DecompositionNoneV2, ObservedAt: "2026-09-04T12:00:00Z",
	})
	if _, err := DecodeReadinessAuthorityStateV2(legacy.CanonicalBytes()); !IsMissionAuthorityKindV2(err, MissionAuthorityLegacyPhaseV2) {
		t.Fatalf("legacy phase state entered readiness: %v", err)
	}
}

func TestReadinessAuthorityTransitionAllowsActivityWithoutLifecycleAdvance(t *testing.T) {
	prior := readinessAuthorityStateFixture(t, 0, 1, "2026-09-04T12:00:00Z")
	nextInput := prior.input
	nextInput.ActiveFacts = prior.ActiveFacts()
	nextInput.Version = 2
	nextInput.ActivityCount = 1
	nextInput.LastOperationID = "operation-readiness-activity"
	nextInput.ObservedAt = "2026-09-04T12:00:01Z"
	next, err := NewReadinessAuthorityStateV2(nextInput)
	if err != nil {
		t.Fatal(err)
	}
	activity := MissionActivityV2{ActivityID: "activity-1", Action: ActionRef{ID: ActionArtifactPublish, Scope: AuthorityScope{Kind: ScopeArtifact, Identity: "specification"}}, Outcome: "succeeded", ResultDigest: strings.Repeat("c", 64), EvidenceIdentities: []string{"evidence-specification"}}
	if _, err := NewReadinessActivityEventV2(prior, next, activity); err != nil {
		t.Fatalf("activity needed a phase advance: %v", err)
	}
	if _, err := NewReadinessAuthorityEventV2(ReadinessEventTypeV2("lifecycle_advanced"), prior, next); !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidRequestV2) {
		t.Fatalf("lifecycle advance re-entered new writes: %v", err)
	}
}

func TestReadinessWorkRevisionSelectionBindsGenerationLineageAndMixedReconciliation(t *testing.T) {
	command := successorPublicationFixture(t, "operation-readiness-successor")
	initialResult := FinalizationResult{Revision: command.ExpectedOld, RequestDigest: testEvidenceDigest(), Receipt: FinalizationReceipt(testEvidenceDigest())}
	initialResult.publication = newWorkRevisionPublicationV2(initialResult.Revision, initialResult.RequestDigest, string(initialResult.Receipt))
	initial, err := SealInitialWorkRevisionSelectionV2(initialResult)
	if err != nil {
		t.Fatal(err)
	}
	created, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: "project-final", MissionID: "mission-final", DefinitionRevision: "definition-v1",
		DefinitionDigest: readinessDigest(t, strings.Repeat("a", 64)), Version: 1, Lifecycle: MissionOpen,
		LastOperationID: "operation-readiness-create", ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	selectedInput := created.input
	selectedInput.Version = 2
	selectedInput.CurrentWorkRevision = initial.Head()
	selectedInput.LastOperationID = "operation-readiness-finalize"
	selectedInput.ObservedAt = "2026-09-04T12:01:00Z"
	selected, err := NewReadinessAuthorityStateV2(selectedInput)
	if err != nil {
		t.Fatal(err)
	}
	initialEvent, err := NewReadinessWorkRevisionEventV2(created, selected, initial)
	if err != nil {
		t.Fatalf("exact generation-one revision was not selectable: %v", err)
	}
	encoded, err := EncodeReadinessAuthorityEventV2(initialEvent)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeReadinessAuthorityEventV2(encoded)
	if err != nil || decoded.WorkRevisionSelection() == nil || decoded.State().CurrentWorkRevisionID() != initial.Head().ID() {
		t.Fatalf("selection evidence did not round trip: %v", err)
	}

	successorResult := SuccessorPublicationResult{Revision: command.Candidate, Reconciliation: command.Reconciliation, RequestDigest: command.Reconciliation.RequestDigest()}
	successorResult.publication = newWorkRevisionPublicationV2(successorResult.Revision, testEvidenceDigest(), testEvidenceDigest())
	reconciled, err := SealReconciledWorkRevisionSelectionV2(initial.Head(), successorResult)
	if err != nil {
		t.Fatal(err)
	}
	successorInput := selected.input
	successorInput.Version = 3
	successorInput.CurrentWorkRevision = reconciled.Head()
	successorInput.LastOperationID = "operation-readiness-reconcile"
	successorInput.ObservedAt = "2026-09-04T12:02:00Z"
	successor, err := NewReadinessAuthorityStateV2(successorInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewReadinessWorkRevisionEventV2(selected, successor, reconciled); err != nil {
		t.Fatalf("mixed reconciliation was not preserved: %v", err)
	}
	dispositions := reconciled.Dispositions()
	if len(dispositions) != 2 || dispositions[0].Disposition != workgraph.Invalidate || dispositions[1].Disposition != workgraph.Continue {
		t.Fatalf("continue/invalidate semantics were not bound: %+v", dispositions)
	}

	forged := successorInput
	forged.Version = 4
	forged.LastOperationID = "operation-readiness-forged"
	forgedState, err := NewReadinessAuthorityStateV2(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewReadinessWorkRevisionEventV2(successor, forgedState, reconciled); !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidTransitionV2) && !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidRequestV2) {
		t.Fatalf("reused selection changed current WorkRevision: %v", err)
	}
}

func TestReadinessAuthorityCheckpointSuffixMatchesOriginReplayAndCensus(t *testing.T) {
	created := readinessAuthorityStateFixture(t, 0, 1, "2026-09-04T12:00:00Z")
	created.input.LastOperationID = "operation-origin-create"
	created, _ = NewReadinessAuthorityStateV2(created.input)
	createPayload, err := NewReadinessAuthorityEventV2(ReadinessEventCreatedV2, ReadinessAuthorityStateV2{}, created)
	if err != nil {
		t.Fatal(err)
	}

	activityInput := created.input
	activityInput.Version = 2
	activityInput.ActivityCount = 1
	activityInput.LastOperationID = "operation-origin-activity"
	activityInput.ObservedAt = "2026-09-04T12:01:00Z"
	activityState, _ := NewReadinessAuthorityStateV2(activityInput)
	activityPayload, err := NewReadinessActivityEventV2(created, activityState, MissionActivityV2{
		ActivityID: "activity-origin-1", Action: ActionRef{ID: ActionArtifactPublish, Scope: AuthorityScope{Kind: ScopeArtifact, Identity: "specification"}},
		Outcome: "succeeded", ResultDigest: strings.Repeat("c", 64), EvidenceIdentities: []string{"evidence-specification"},
	})
	if err != nil {
		t.Fatal(err)
	}

	pausedInput := activityState.input
	pausedInput.Version = 3
	pausedInput.Lifecycle = MissionPaused
	pausedInput.LastOperationID = "operation-origin-pause"
	pausedInput.ObservedAt = "2026-09-04T12:02:00Z"
	paused, _ := NewReadinessAuthorityStateV2(pausedInput)
	pausePayload, err := NewReadinessAuthorityEventV2(ReadinessEventPausedV2, activityState, paused)
	if err != nil {
		t.Fatal(err)
	}

	events := []journalv2.EventEnvelopeV2{
		readinessEnvelope(t, createPayload, "event-origin-create", 1),
		readinessEnvelope(t, activityPayload, "event-origin-activity", 2),
		readinessEnvelope(t, pausePayload, "event-origin-pause", 3),
	}
	origin := []byte(`{}`)
	for _, event := range events {
		origin, err = ReduceMissionEventV2(origin, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	checkpoint, err := ReduceMissionEventV2([]byte(`{}`), events[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events[1:] {
		checkpoint, err = ReduceMissionEventV2(checkpoint, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(origin, checkpoint) || !bytes.Equal(origin, paused.CanonicalBytes()) {
		t.Fatalf("checkpoint+suffix diverged from origin replay:\n%s\n%s", origin, checkpoint)
	}

	replayed, err := DecodeReadinessAuthorityStateV2(origin)
	if err != nil {
		t.Fatal(err)
	}
	contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionMissionResume}, CapabilityContract{Action: ActionArtifactPublish})
	input := ReadinessInput{MissionID: string(replayed.MissionID()), Revision: uint64(replayed.Version()), Lifecycle: replayed.Lifecycle(), Contracts: contracts}
	fromOrigin, err := evaluateReadinessInput(t, input)
	if err != nil {
		t.Fatal(err)
	}
	fromCheckpoint, err := evaluateReadinessInput(t, input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fromOrigin.CanonicalBytes(), fromCheckpoint.CanonicalBytes()) || !slices.Equal(fromOrigin.Authorized(), []ActionRef{testRefFor(ActionMissionResume, "mission-readiness")}) {
		t.Fatalf("replay changed census: origin=%s checkpoint=%s", fromOrigin.CanonicalBytes(), fromCheckpoint.CanonicalBytes())
	}
}

func readinessAuthorityStateFixture(t *testing.T, activity, version uint64, observedAt string) ReadinessAuthorityStateV2 {
	t.Helper()
	state, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: "project-readiness", MissionID: "mission-readiness", DefinitionRevision: "definition-v1",
		DefinitionDigest: readinessDigest(t, strings.Repeat("a", 64)), Version: journalv2.LogicalVersion(version), Lifecycle: MissionOpen,
		ActivityCount: activity,
		ActiveFacts: []ScopedMissionFactV2{
			{Kind: ScopedFactBlockerV2, FactID: "blocker-1", Scope: ActionRef{ID: ActionExecutionClaim, Scope: AuthorityScope{Kind: ScopeWork, Identity: "work-a"}}, EvidenceIdentity: "evidence-blocker", EvidenceDigest: strings.Repeat("b", 64)},
		},
		LastOperationID: "operation-readiness", ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func readinessEnvelope(t *testing.T, payload ReadinessAuthorityEventV2, eventID journalv2.EventID, version journalv2.LogicalVersion) journalv2.EventEnvelopeV2 {
	t.Helper()
	encoded, err := EncodeReadinessAuthorityEventV2(payload)
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-readiness", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-readiness", MissionID: "mission-readiness"})
	envelope, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{
		EventID: eventID, EventType: string(payload.EventType()), Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1,
		Coordinate: coordinate, AggregateLogicalVersion: version, CommittedRuntimeEpochID: "epoch-readiness", ActorID: "actor-readiness", ProcessID: "process-readiness",
		PublicationID: journalv2.PublicationID(strings.Repeat("d", 64)), OperationID: journalv2.OperationID("operation-" + string(eventID)), OperationLogicalVersion: 1,
		EventIndex: 0, EventCount: 1, Payload: encoded,
	}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func readinessDigest(t *testing.T, value string) journalv2.Digest {
	t.Helper()
	digest, err := journalv2.ParseDigest("test", value)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
