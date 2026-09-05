package mission

import (
	"context"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestReadinessServicePublishesOneSealedOperationAndMissionBatch(t *testing.T) {
	command := readinessCreateCommand(t, "operation-readiness-create")
	port := &readinessPortProbeV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}}
	epoch := &readinessEpochProbeV2{value: command.OriginatingRuntimeEpochID}
	service, err := NewReadinessAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed() || result.State().Digest() != command.Updated.Digest() || port.publishCalls != 1 || port.observeCalls != 0 || epoch.calls != 1 {
		t.Fatalf("result=%s publish=%d observe=%d epoch=%d", result.CanonicalBytes(), port.publishCalls, port.observeCalls, epoch.calls)
	}
	if len(port.plan.Events()) != 2 || len(port.plan.Heads()) != 2 || port.plan.BindingCreate() == nil || port.plan.OperationState().Status() != journalv2.StatusResolved {
		t.Fatalf("publication was not one complete sealed batch: events=%d heads=%d mutations=%d", len(port.plan.Events()), len(port.plan.Heads()), port.plan.MutationCount())
	}
	for _, event := range port.plan.Events() {
		if event.Family() == "mission" && (strings.Contains(string(event.Payload()), "step") || strings.Contains(string(event.Payload()), "successor") || strings.Contains(string(event.Payload()), "phase")) {
			t.Fatalf("new publication wrote ceremony: %s", event.Payload())
		}
	}
}

func TestReadinessServiceExactReplayAndCollisionPrecedeEpochAndAuthorityReads(t *testing.T) {
	command := readinessCreateCommand(t, "operation-readiness-replay")
	resultBytes, _ := encodeReadinessAuthorityResultV2(command.OperationID, command.Updated)
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{
		OperationID: command.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved,
		ResultKind: "mission.readiness.result", ResultSchemaVersion: 2, CanonicalResult: resultBytes,
		EventIDs: []journalv2.EventID{"event-readiness-replay"}, LastPublicationID: journalv2.PublicationID(strings.Repeat("d", 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		resolution OperationResolutionV2
		wantKind   MissionAuthorityErrorKindV2
	}{
		{name: "exact", resolution: OperationResolutionV2{Kind: OperationExactV2, State: operation}},
		{name: "collision", resolution: OperationResolutionV2{Kind: OperationConflictV2}, wantKind: MissionAuthorityIdempotencyConflictV2},
	} {
		t.Run(test.name, func(t *testing.T) {
			port := &readinessPortProbeV2{resolution: test.resolution}
			epoch := &readinessEpochProbeV2{value: "wrong-epoch"}
			service, _ := NewReadinessAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
			result, executeErr := service.Execute(context.Background(), command)
			if test.wantKind == "" {
				if executeErr != nil || !result.Replayed() || result.State().Digest() != command.Updated.Digest() {
					t.Fatalf("exact replay failed: result=%s err=%v", result.CanonicalBytes(), executeErr)
				}
			} else if !IsMissionAuthorityKindV2(executeErr, test.wantKind) {
				t.Fatalf("collision kind=%v", executeErr)
			}
			if epoch.calls != 0 || port.observeCalls != 0 || port.publishCalls != 0 {
				t.Fatalf("operation resolution crossed later authority: epoch=%d observe=%d publish=%d", epoch.calls, port.observeCalls, port.publishCalls)
			}
		})
	}
}

func TestReadinessOperationIdentityBindsExactObservationEvidenceBeforeReads(t *testing.T) {
	first := readinessCreateCommand(t, "operation-readiness-evidence")
	first.ObservationEvidence = []byte(`{"authority":"one"}`)
	changed := first
	changed.ObservationEvidence = []byte(`{"authority":"two"}`)
	firstBinding, err := validateReadinessAuthorityCommandV2(first)
	if err != nil {
		t.Fatal(err)
	}
	changedBinding, err := validateReadinessAuthorityCommandV2(changed)
	if err != nil {
		t.Fatal(err)
	}
	if firstBinding.IntentDigest() == changedBinding.IntentDigest() {
		t.Fatal("changed committed observation evidence reused the operation binding")
	}
	port := &readinessPortProbeV2{resolution: OperationResolutionV2{Kind: OperationConflictV2}}
	epoch := &readinessEpochProbeV2{value: "wrong-epoch"}
	service, _ := NewReadinessAuthorityServiceV2(port, epoch, journalv2.DefaultEventRegistryV2())
	if _, err := service.Execute(context.Background(), changed); !IsMissionAuthorityKindV2(err, MissionAuthorityIdempotencyConflictV2) {
		t.Fatalf("changed evidence was not refused as an operation collision: %v", err)
	}
	if epoch.calls != 0 || port.observeCalls != 0 || port.publishCalls != 0 {
		t.Fatalf("evidence collision crossed later authority: epoch=%d observe=%d publish=%d", epoch.calls, port.observeCalls, port.publishCalls)
	}
}

func TestReadinessFactTransitionPublishesAtomicMissionAndAcceptanceEvidence(t *testing.T) {
	created := readinessCreateCommand(t, "operation-readiness-created").Updated
	scoped := ScopedMissionFactV2{
		Kind: ScopedFactDecisionV2, FactID: "policy-fact-1",
		Scope:            ActionRef{ID: ActionAcceptanceEvaluate, Scope: AuthorityScope{Kind: ScopeWork, Identity: "work-1"}},
		EvidenceIdentity: "acceptance-policy-v1", EvidenceDigest: strings.Repeat("b", 64),
	}
	updated, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: created.ProjectID(), MissionID: created.MissionID(), DefinitionRevision: created.input.DefinitionRevision,
		DefinitionDigest: created.input.DefinitionDigest, Version: 2, Lifecycle: MissionOpen, ActiveFacts: []ScopedMissionFactV2{scoped},
		LastOperationID: "operation-readiness-fact-open", ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := journalv2.ParseDigest("detail", scoped.EvidenceDigest)
	fact, err := NewMissionFactStateV2(MissionFactStateInputV2{
		ProjectID: updated.ProjectID(), MissionID: updated.MissionID(), FactID: scoped.FactID, Kind: MissionFactDecisionV2,
		Status: MissionFactOpenV2, LogicalVersion: 1, DetailDigest: detail,
		LastOperationID: updated.LastOperationID(), ObservedAt: updated.ObservedAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	command := readinessCommandForStateV2(updated, created, ReadinessEventFactOpenedV2)
	command.RelatedFact = &MissionFactTransitionV2{Successor: fact}
	observation, _ := NewReadinessAuthorityObservationV2(created, readinessStoreRevisionV2(t, 7), 7, "event-readiness-created")
	port := &readinessPortProbeV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, observation: observation}
	service, _ := NewReadinessAuthorityServiceV2(port, &readinessEpochProbeV2{value: command.OriginatingRuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	result, err := service.Execute(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.State().Digest() != updated.Digest() || port.factObserveCalls != 1 || len(port.plan.Events()) != 3 || len(port.plan.Heads()) != 3 {
		t.Fatalf("related fact was not one atomic publication: fact_reads=%d events=%d heads=%d", port.factObserveCalls, len(port.plan.Events()), len(port.plan.Heads()))
	}
	foundFact := false
	for _, event := range port.plan.Events() {
		if event.Coordinate().ID == "decision:policy-fact-1" {
			foundFact = event.EventType() == string(MissionEventDecisionOpenedV2) && event.OperationID() == command.OperationID
		}
	}
	if !foundFact {
		t.Fatal("atomic publication omitted the acceptance-consumable fact event")
	}
}

func TestReadinessRelatedFactIsIntentBoundAndTamperRefusedBeforeReads(t *testing.T) {
	created := readinessCreateCommand(t, "operation-readiness-created-binding").Updated
	scoped := ScopedMissionFactV2{Kind: ScopedFactDecisionV2, FactID: "policy-fact-binding", Scope: ActionRef{ID: ActionAcceptanceEvaluate, Scope: AuthorityScope{Kind: ScopeWork, Identity: "work-binding"}}, EvidenceIdentity: "policy-binding", EvidenceDigest: strings.Repeat("c", 64)}
	updated, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: created.ProjectID(), MissionID: created.MissionID(), DefinitionRevision: created.input.DefinitionRevision,
		DefinitionDigest: created.input.DefinitionDigest, Version: 2, Lifecycle: MissionOpen, ActiveFacts: []ScopedMissionFactV2{scoped},
		LastOperationID: "operation-readiness-fact-binding", ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := journalv2.ParseDigest("detail", scoped.EvidenceDigest)
	fact, _ := NewMissionFactStateV2(MissionFactStateInputV2{ProjectID: updated.ProjectID(), MissionID: updated.MissionID(), FactID: scoped.FactID, Kind: MissionFactDecisionV2, Status: MissionFactOpenV2, LogicalVersion: 1, DetailDigest: detail, LastOperationID: updated.LastOperationID(), ObservedAt: updated.ObservedAt()})
	withFact := readinessCommandForStateV2(updated, created, ReadinessEventFactOpenedV2)
	withFact.RelatedFact = &MissionFactTransitionV2{Successor: fact}
	withoutFact := withFact
	withoutFact.RelatedFact = nil
	boundWith, err := validateReadinessAuthorityCommandV2(withFact)
	if err != nil {
		t.Fatal(err)
	}
	boundWithout, err := validateReadinessAuthorityCommandV2(withoutFact)
	if err != nil {
		t.Fatal(err)
	}
	if boundWith.IntentDigest() == boundWithout.IntentDigest() {
		t.Fatal("related fact omission reused the operation intent")
	}

	tamperedFact, _ := NewMissionFactStateV2(MissionFactStateInputV2{ProjectID: updated.ProjectID(), MissionID: updated.MissionID(), FactID: "substituted-fact", Kind: MissionFactDecisionV2, Status: MissionFactOpenV2, LogicalVersion: 1, DetailDigest: detail, LastOperationID: updated.LastOperationID(), ObservedAt: updated.ObservedAt()})
	tampered := withFact
	tampered.RelatedFact = &MissionFactTransitionV2{Successor: tamperedFact}
	port := &readinessPortProbeV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}}
	service, _ := NewReadinessAuthorityServiceV2(port, &readinessEpochProbeV2{value: tampered.OriginatingRuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Execute(context.Background(), tampered); !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidTransitionV2) {
		t.Fatalf("substituted fact was not refused: %v", err)
	}
	if port.resolveCalls != 0 || port.observeCalls != 0 || port.factObserveCalls != 0 || port.publishCalls != 0 {
		t.Fatal("invalid related fact crossed the validation boundary")
	}
}

func TestReadinessRelatedFactRefusesCompetingFactRevisionBeforePublication(t *testing.T) {
	created := readinessCreateCommand(t, "operation-readiness-created-competing").Updated
	scoped := ScopedMissionFactV2{Kind: ScopedFactDecisionV2, FactID: "policy-fact-competing", Scope: ActionRef{ID: ActionAcceptanceEvaluate, Scope: AuthorityScope{Kind: ScopeWork, Identity: "work-competing"}}, EvidenceIdentity: "policy-competing", EvidenceDigest: strings.Repeat("d", 64)}
	opened, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: created.ProjectID(), MissionID: created.MissionID(), DefinitionRevision: created.input.DefinitionRevision,
		DefinitionDigest: created.input.DefinitionDigest, Version: 2, Lifecycle: MissionOpen, ActiveFacts: []ScopedMissionFactV2{scoped},
		LastOperationID: "operation-readiness-fact-open-competing", ObservedAt: "2026-09-04T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	detail, _ := journalv2.ParseDigest("detail", scoped.EvidenceDigest)
	prior, _ := NewMissionFactStateV2(MissionFactStateInputV2{ProjectID: opened.ProjectID(), MissionID: opened.MissionID(), FactID: scoped.FactID, Kind: MissionFactDecisionV2, Status: MissionFactOpenV2, LogicalVersion: 1, DetailDigest: detail, LastOperationID: opened.LastOperationID(), ObservedAt: opened.ObservedAt()})
	closed, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: opened.ProjectID(), MissionID: opened.MissionID(), DefinitionRevision: opened.input.DefinitionRevision,
		DefinitionDigest: opened.input.DefinitionDigest, Version: 3, Lifecycle: MissionOpen,
		LastOperationID: "operation-readiness-fact-close-competing", ObservedAt: "2026-09-04T12:02:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	resolvedDetail, _ := journalv2.ParseDigest("detail", strings.Repeat("e", 64))
	next, _ := NewMissionFactStateV2(MissionFactStateInputV2{ProjectID: closed.ProjectID(), MissionID: closed.MissionID(), FactID: scoped.FactID, Kind: MissionFactDecisionV2, Status: MissionFactResolvedV2, LogicalVersion: 2, DetailDigest: resolvedDetail, LastOperationID: closed.LastOperationID(), ObservedAt: closed.ObservedAt()})
	command := readinessCommandForStateV2(closed, opened, ReadinessEventFactClosedV2)
	command.RelatedFact = &MissionFactTransitionV2{Prior: prior, Successor: next, ExpectedRecordRevision: 9}
	missionObservation, _ := NewReadinessAuthorityObservationV2(opened, readinessStoreRevisionV2(t, 10), 8, "event-opened-competing")
	port := &readinessPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2}, observation: missionObservation,
		fact: MissionFactObservationV2{State: prior, RecordRevision: 10, Found: true},
	}
	service, _ := NewReadinessAuthorityServiceV2(port, &readinessEpochProbeV2{value: command.OriginatingRuntimeEpochID}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Execute(context.Background(), command); !IsMissionAuthorityKindV2(err, MissionAuthorityVersionConflictV2) {
		t.Fatalf("competing fact revision was not refused: %v", err)
	}
	if port.publishCalls != 0 {
		t.Fatal("competing fact revision reached publication")
	}
}

func readinessCommandForStateV2(updated, expected ReadinessAuthorityStateV2, event ReadinessEventTypeV2) ReadinessAuthorityCommandV2 {
	return ReadinessAuthorityCommandV2{
		SchemaVersion: ReadinessAuthorityStateSchemaV2, OperationID: updated.LastOperationID(),
		ProjectID: updated.ProjectID(), MissionID: updated.MissionID(), ActorID: "actor-readiness", ProcessID: "process-readiness",
		OriginatingRuntimeEpochID: "epoch-readiness", DoctrineRevision: "doctrine-v1", ProfileRevision: "profile-v1", PolicyRevision: "policy-v1",
		ObservedAt: updated.ObservedAt(), EventType: event, Expected: expected, Updated: updated,
	}
}

func readinessStoreRevisionV2(t *testing.T, revision uint64) journalv2.StoreRevisionV2 {
	t.Helper()
	value, err := journalv2.NewStoreRevisionV2("database-readiness", revision, revision)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type readinessPortProbeV2 struct {
	resolution       OperationResolutionV2
	observation      ReadinessAuthorityObservationV2
	fact             MissionFactObservationV2
	plan             journalv2.SealedCommitPlanV2
	resolveCalls     int
	observeCalls     int
	factObserveCalls int
	publishCalls     int
}

func (p *readinessPortProbeV2) ResolveOperation(_ context.Context, _ journalv2.OperationBindingV2) (OperationResolutionV2, error) {
	p.resolveCalls++
	return p.resolution, nil
}

func (p *readinessPortProbeV2) ObserveReadinessAuthority(_ context.Context, _ journalv2.ProjectID, _ journalv2.MissionID) (ReadinessAuthorityObservationV2, error) {
	p.observeCalls++
	return p.observation, nil
}

func (p *readinessPortProbeV2) ObserveMissionFact(_ context.Context, _ journalv2.ProjectID, _ journalv2.MissionID, _ MissionFactKindV2, _ string) (MissionFactObservationV2, error) {
	p.factObserveCalls++
	return p.fact, nil
}

func (p *readinessPortProbeV2) PublishReadinessAuthority(_ context.Context, plan journalv2.SealedCommitPlanV2) (MissionPublicationV2, error) {
	p.publishCalls++
	p.plan = plan
	return MissionPublicationV2{PublicationID: plan.PublicationID(), ProviderRevision: 1, DurableThroughRevision: 1, MutationCount: plan.MutationCount()}, nil
}

type readinessEpochProbeV2 struct {
	value journalv2.RuntimeEpochID
	calls int
}

func (p *readinessEpochProbeV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	p.calls++
	return p.value, nil
}

func readinessCreateCommand(t *testing.T, operationID journalv2.OperationID) ReadinessAuthorityCommandV2 {
	t.Helper()
	state, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: "project-readiness-service", MissionID: "mission-readiness-service", DefinitionRevision: "definition-v1",
		DefinitionDigest: readinessDigest(t, strings.Repeat("a", 64)), Version: 1, Lifecycle: MissionOpen,
		LastOperationID: operationID, ObservedAt: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ReadinessAuthorityCommandV2{
		SchemaVersion: ReadinessAuthorityStateSchemaV2, OperationID: operationID,
		ProjectID: state.ProjectID(), MissionID: state.MissionID(), ActorID: "actor-readiness", ProcessID: "process-readiness",
		OriginatingRuntimeEpochID: "epoch-readiness", DoctrineRevision: "doctrine-v1", ProfileRevision: "profile-v1", PolicyRevision: "policy-v1",
		ObservedAt: state.ObservedAt(), EventType: ReadinessEventCreatedV2, Updated: state,
	}
}
