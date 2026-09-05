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

type readinessPortProbeV2 struct {
	resolution   OperationResolutionV2
	observation  ReadinessAuthorityObservationV2
	plan         journalv2.SealedCommitPlanV2
	resolveCalls int
	observeCalls int
	publishCalls int
}

func (p *readinessPortProbeV2) ResolveOperation(_ context.Context, _ journalv2.OperationBindingV2) (OperationResolutionV2, error) {
	p.resolveCalls++
	return p.resolution, nil
}

func (p *readinessPortProbeV2) ObserveReadinessAuthority(_ context.Context, _ journalv2.ProjectID, _ journalv2.MissionID) (ReadinessAuthorityObservationV2, error) {
	p.observeCalls++
	return p.observation, nil
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
