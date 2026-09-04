package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

type effectPortProbeV2 struct {
	resolution    EffectOperationResolutionV2
	effect        EffectStateObservationV2
	execution     ExecutionSlotObservationV2
	trace         []string
	plans         []EffectAuthorityPlanV2
	resolutionErr error
	effectErr     error
	executionErr  error
	publishErr    error
	afterPublish  func(journalv2.SealedCommitPlanV2)
	timeline      *[]string
}

type effectDispatcherProbeV2 struct {
	calls        int
	compensation bool
	result       EffectMutationResultV2
	observeErr   error
	mutationErr  error
	observe      EffectProviderObservationV2
	timeline     *[]string
}

type assignmentProjectionProbeV2 struct {
	observation     tracker.AssignmentObservation
	mutation        tracker.AssignmentMutation
	observeErr      error
	mutationErr     error
	observeCalls    int
	applyCalls      int
	compensateCalls int
}

func (p *assignmentProjectionProbeV2) ObserveAssignment(context.Context, tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	p.observeCalls++
	return p.observation, p.observeErr
}
func (p *assignmentProjectionProbeV2) ProjectAssignment(context.Context, tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.applyCalls++
	return p.mutation, p.mutationErr
}
func (p *assignmentProjectionProbeV2) CompensateAssignment(context.Context, tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.compensateCalls++
	return p.mutation, p.mutationErr
}

func (p *effectDispatcherProbeV2) ObserveEffect(context.Context, tracker.ExternalWorkRef) (EffectProviderObservationV2, error) {
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "provider_observe")
	}
	if p.observe.Outcome != "" {
		return p.observe, p.observeErr
	}
	return p.result.Observation, p.observeErr
}
func (p *effectDispatcherProbeV2) ApplyEffect(context.Context, EffectConditionalMutationV2) (EffectMutationResultV2, error) {
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "provider_apply")
	}
	p.calls++
	return p.result, p.mutationErr
}
func (p *effectDispatcherProbeV2) CompensateEffect(context.Context, EffectConditionalMutationV2) (EffectMutationResultV2, error) {
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "provider_compensate")
	}
	p.calls++
	p.compensation = true
	return p.result, p.mutationErr
}

func (p *effectPortProbeV2) ResolveEffectOperation(context.Context, journalv2.OperationBindingV2) (EffectOperationResolutionV2, error) {
	p.trace = append(p.trace, "resolve")
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "resolve")
	}
	return p.resolution, p.resolutionErr
}
func (p *effectPortProbeV2) ObserveEffectState(context.Context, journalv2.AggregateCoordinate) (EffectStateObservationV2, error) {
	p.trace = append(p.trace, "effect")
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "effect")
	}
	return p.effect, p.effectErr
}
func (p *effectPortProbeV2) ObserveEffectExecution(context.Context, journalv2.AggregateCoordinate) (ExecutionSlotObservationV2, error) {
	p.trace = append(p.trace, "execution")
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "execution")
	}
	return p.execution, p.executionErr
}
func (p *effectPortProbeV2) PublishEffect(_ context.Context, plan EffectAuthorityPlanV2) (EffectPublicationAckV2, error) {
	p.trace = append(p.trace, "publish")
	if p.timeline != nil {
		*p.timeline = append(*p.timeline, "publish")
	}
	p.plans = append(p.plans, plan)
	sealed, _ := plan.Publication()
	if p.afterPublish != nil {
		p.afterPublish(sealed)
	}
	if p.publishErr != nil {
		return EffectPublicationAckV2{}, p.publishErr
	}
	return EffectPublicationAckV2{sealed.PublicationID(), sealed.Digest(), sealed.MutationCount(), 17}, nil
}

func TestExternalEffectSagaPublishesIntentBeforePermissionAndProgressesMonotonicallyV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	result, err := service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectIntentRecordedV2 || result.Permission != nil {
		t.Fatalf("intent result=%+v err=%v", result, err)
	}
	if strings.Join(port.trace, ",") != "resolve,execution,effect,publish" {
		t.Fatalf("ordering=%v", port.trace)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	if sealed.BindingCreate() == nil || len(sealed.EffectIntents()) != 1 || len(sealed.Events()) != 3 || len(sealed.Heads()) != 3 || len(sealed.Conditions()) != 3 {
		t.Fatalf("intent membership binding=%v intents=%d events=%d heads=%d conditions=%d", sealed.BindingCreate() != nil, len(sealed.EffectIntents()), len(sealed.Events()), len(sealed.Heads()), len(sealed.Conditions()))
	}
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)

	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-effect-operation-attempt"
	request.EffectEventID = "event-effect-attempt"
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectAttemptStartedV2 || result.Permission == nil {
		t.Fatalf("attempt result=%+v err=%v", result, err)
	}
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	if sealed.BindingCreate() != nil || len(sealed.EffectIntents()) != 0 {
		t.Fatal("attempt rewrote immutable intent")
	}
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)

	request.Action = EffectRecordObservationV2
	request.OperationEventID = "event-effect-operation-observe"
	request.EffectEventID = "event-effect-observe"
	request.Observation = effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	request.DispatchEvidence = &EffectDispatchEvidenceV2{observation: *request.Observation, publicationID: sealed.PublicationID(), planDigest: sealed.Digest(), providerRevision: 17}
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectObservationRecordedV2 {
		t.Fatalf("ambiguous result=%+v err=%v", result, err)
	}
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)

	request.Action = EffectRequireReconcileV2
	request.OperationEventID = "event-effect-operation-reconcile"
	request.EffectEventID = "event-effect-reconcile"
	request.Observation = nil
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectReconciliationRequiredV2 {
		t.Fatalf("reconcile result=%+v err=%v", result, err)
	}
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)

	request.Action = EffectAllowCompensationV2
	request.OperationEventID = "event-effect-operation-compensate"
	request.EffectEventID = "event-effect-compensate"
	request.PermissionMarker = string(request.Intent.OperationID)
	request.Observation = effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.Permission == nil || result.State.Stage != EffectCompensationAllowedV2 {
		t.Fatalf("permission result=%+v err=%v", result, err)
	}
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	compensated := effectObservationFixtureV2(request.Intent.Target, 3, request.Intent.ExpectedValue, "", "absent")
	dispatcher := &effectDispatcherProbeV2{observe: *request.Observation, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: *compensated}}
	evidence, err := DispatchAuthorizedEffectV2(t.Context(), *result.Permission, dispatcher)
	if err != nil || !dispatcher.compensation {
		t.Fatalf("compensation dispatch err=%v calls=%d", err, dispatcher.calls)
	}
	request.Action = EffectRecordObservationV2
	request.OperationEventID = "event-effect-operation-compensation-observed"
	request.EffectEventID = "event-effect-compensation-observed"
	request.Observation = compensated
	request.DispatchEvidence = &evidence
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectReconciliationRequiredV2 {
		t.Fatalf("compensation observation result=%+v err=%v", result, err)
	}
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	request.Action = EffectResolveV2
	request.OperationEventID = "event-effect-operation-compensated"
	request.EffectEventID = "event-effect-compensated"
	request.Observation = nil
	request.DispatchEvidence = nil
	request.Outcome = "compensated"
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectResolvedV2 || result.State.Outcome != "compensated" {
		t.Fatalf("terminal result=%+v err=%v", result, err)
	}
}

func TestExternalEffectAttributionRequiresRevisionValueMarkerAndMappingV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	result, _ := service.Execute(t.Context(), request)
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-op-a"
	request.EffectEventID = "event-a"
	result, _ = service.Execute(t.Context(), request)
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	request.Action = EffectRecordObservationV2
	request.OperationEventID = "event-op-b"
	request.EffectEventID = "event-b"
	request.Observation = effectObservationFixtureV2(request.Intent.Target, 3, "actor-1", string(request.Intent.OperationID), "confirmed")
	request.DispatchEvidence = &EffectDispatchEvidenceV2{observation: *request.Observation, publicationID: sealed.PublicationID(), planDigest: sealed.Digest(), providerRevision: 17}
	result, _ = service.Execute(t.Context(), request)
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	request.Action = EffectRequireReconcileV2
	request.OperationEventID = "event-op-c"
	request.EffectEventID = "event-c"
	request.Observation = nil
	result, _ = service.Execute(t.Context(), request)
	sealed, _ = port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	base := *effectObservationFixtureV2(request.Intent.Target, 3, "actor-1", string(request.Intent.OperationID), "confirmed")
	for _, tc := range []struct {
		name   string
		mutate func(*EffectProviderObservationV2)
	}{{"revision", func(v *EffectProviderObservationV2) {
		v.ProviderRevision = 4
		v.Target.LastObservation = 4
		v.CanonicalDigest = effectObservationDigestV2(*v)
	}}, {"value", func(v *EffectProviderObservationV2) {
		v.Value = "actor-human"
		v.CanonicalDigest = effectObservationDigestV2(*v)
	}}, {"marker", func(v *EffectProviderObservationV2) {
		v.OperationMarker = "human-marker"
		v.CanonicalDigest = effectObservationDigestV2(*v)
	}}, {"mapping", func(v *EffectProviderObservationV2) { v.Target.ExternalID = "other-work" }}} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := base
			tc.mutate(&candidate)
			r := request
			r.Action = EffectAllowCompensationV2
			r.PermissionMarker = string(request.Intent.OperationID)
			r.Observation = &candidate
			r.OperationEventID = journalv2.EventID("event-op-refuse-" + tc.name)
			r.EffectEventID = journalv2.EventID("event-refuse-" + tc.name)
			if _, err := service.Execute(t.Context(), r); !IsKind(err, KindExternalAttributionLost) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestExternalEffectAbsentEvidenceCannotCarryValueOrMarkerV2(t *testing.T) {
	request, _, _ := effectRequestFixtureV2(t)
	for _, test := range []struct {
		name   string
		value  string
		marker string
	}{{name: "absent-with-value", value: "human-owner"}, {name: "absent-with-marker", marker: "forged-operation"}} {
		t.Run(test.name, func(t *testing.T) {
			observation := effectObservationFixtureV2(request.Intent.Target, 2, test.value, test.marker, "absent")
			if err := observation.validateFor(request.Intent.Target); err == nil {
				t.Fatal("accepted contradictory absence evidence")
			}
		})
	}
}

func TestWP07ExecutableEffectMutantsV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	intent, err := service.Execute(t.Context(), request)
	if err != nil || intent.Permission != nil {
		t.Fatalf("M-NAKED-INTENT survived: result=%+v err=%v", intent, err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, intent.State, &request, intent.ExecutionSlot)
	request.Action = EffectResolveV2
	request.OperationEventID, request.EffectEventID = "event-mutant-optimistic-operation", "event-mutant-optimistic-effect"
	request.Outcome = "optimistic"
	if _, err := service.Execute(t.Context(), request); !IsKind(err, KindDecisionRequired) {
		t.Fatalf("M-OPTIMISTIC-EFFECT-COMPLETION survived: %v", err)
	}

	request.Action = EffectStartAttemptV2
	request.OperationEventID, request.EffectEventID = "event-mutant-attempt-operation", "event-mutant-attempt-effect"
	request.Outcome = ""
	attempt, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	applied := *effectObservationFixtureV2(request.Intent.Target, attempt.Permission.expected.ProviderRevision+1, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	dispatcher := &effectDispatcherProbeV2{observe: attempt.Permission.expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: applied}, mutationErr: &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "warning-like conflict"}}
	evidence, err := DispatchAuthorizedEffectV2(t.Context(), *attempt.Permission, dispatcher)
	if !IsKind(err, KindDecisionRequired) || !effectAttributionExactV2(evidence.Observation(), applied) {
		t.Fatalf("M-WARNING-AS-SUCCESS survived: evidence=%+v err=%v", evidence.Observation(), err)
	}
	dishonest := applied
	dishonest.Value = "wrong-value"
	dishonest.CanonicalDigest = effectObservationDigestV2(dishonest)
	dispatcher = &effectDispatcherProbeV2{observe: attempt.Permission.expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: dishonest}}
	evidence, err = DispatchAuthorizedEffectV2(t.Context(), *attempt.Permission, dispatcher)
	if !IsKind(err, KindDecisionRequired) || evidence.Observation().Value != dishonest.Value {
		t.Fatalf("M-WRONG-VALUE-AS-SUCCESS survived: evidence=%+v err=%v", evidence.Observation(), err)
	}
}

func TestWP07ExactEffectMutantCampaignV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	intent, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, intent.State, &request, intent.ExecutionSlot)
	request.Action = EffectStartAttemptV2
	request.OperationEventID, request.EffectEventID = "event-census-attempt-operation", "event-census-attempt-effect"
	attempt, err := service.Execute(t.Context(), request)
	if err != nil || attempt.Permission == nil {
		t.Fatalf("permission=%+v err=%v", attempt.Permission, err)
	}
	attemptSealed, _ := port.plans[len(port.plans)-1].Publication()
	permission := *attempt.Permission
	applied := *effectObservationFixtureV2(request.Intent.Target, permission.expected.ProviderRevision+1, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")

	type executableMutant struct {
		id   string
		kill func(*testing.T)
	}
	mutants := []executableMutant{
		{"M-ABSENT-WITH-MARKER", func(t *testing.T) {
			bad := *effectObservationFixtureV2(request.Intent.Target, 2, "", "forged", "absent")
			if bad.validateFor(request.Intent.Target) == nil {
				t.Fatal("contradictory absence survived")
			}
		}},
		{"M-ABSENT-WITH-VALUE", func(t *testing.T) {
			bad := *effectObservationFixtureV2(request.Intent.Target, 2, "owner", "", "absent")
			if bad.validateFor(request.Intent.Target) == nil {
				t.Fatal("contradictory absence survived")
			}
		}},
		{"M-BLIND-RETRY", func(t *testing.T) {
			changed := permission.expected
			changed.ProviderRevision++
			changed.Target.LastObservation = changed.ProviderRevision
			changed.CanonicalDigest = effectObservationDigestV2(changed)
			provider := &effectDispatcherProbeV2{observe: changed, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: applied}}
			if _, dispatchErr := DispatchAuthorizedEffectV2(t.Context(), permission, provider); !IsKind(dispatchErr, KindDecisionRequired) || provider.calls != 0 {
				t.Fatalf("blind retry survived calls=%d err=%v", provider.calls, dispatchErr)
			}
		}},
		{"M-NAKED-INTENT", func(t *testing.T) {
			if intent.Permission != nil {
				t.Fatal("intent reserved provider authority")
			}
		}},
		{"M-OPTIMISTIC-EFFECT-COMPLETION", func(t *testing.T) {
			candidate := request
			candidate.Action = EffectResolveV2
			candidate.OperationEventID, candidate.EffectEventID = "event-census-optimistic-operation", "event-census-optimistic-effect"
			candidate.Outcome = EffectOutcomeAppliedV2
			if _, _, _, transitionErr := decideEffectTransitionV2(candidate, &attempt.State, attemptSealed.OperationState()); !IsKind(transitionErr, KindDecisionRequired) {
				t.Fatalf("optimistic completion survived err=%v", transitionErr)
			}
		}},
		{"M-RESULT-BEFORE-DURABLE-ACK", func(t *testing.T) {
			provider := &effectDispatcherProbeV2{observe: permission.expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: applied}}
			if _, dispatchErr := DispatchAuthorizedEffectV2(t.Context(), EffectDispatchPermissionV2{}, provider); dispatchErr == nil || provider.calls != 0 {
				t.Fatalf("unacknowledged dispatch survived calls=%d err=%v", provider.calls, dispatchErr)
			}
		}},
		{"M-UNSAFE-COMPENSATION", func(t *testing.T) {
			candidate := permission
			candidate.action = EffectAllowCompensationV2
			candidate.guarantee = tracker.AssignmentUnguarded
			provider := &effectDispatcherProbeV2{observe: candidate.expected}
			if _, dispatchErr := DispatchAuthorizedEffectV2(t.Context(), candidate, provider); !IsKind(dispatchErr, KindSafeCompensationUnsupported) || provider.calls != 0 {
				t.Fatalf("unsafe compensation survived calls=%d err=%v", provider.calls, dispatchErr)
			}
		}},
		{"M-WARNING-AS-SUCCESS", func(t *testing.T) {
			provider := &effectDispatcherProbeV2{observe: permission.expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: applied}, mutationErr: &tracker.Error{Kind: tracker.ErrorRevisionConflict, Message: "warning"}}
			if _, dispatchErr := DispatchAuthorizedEffectV2(t.Context(), permission, provider); !IsKind(dispatchErr, KindDecisionRequired) {
				t.Fatalf("provider warning survived err=%v", dispatchErr)
			}
		}},
		{"M-WRONG-MARKER-AS-SUCCESS", func(t *testing.T) {
			bad := applied
			bad.OperationMarker = "other-operation"
			bad.CanonicalDigest = effectObservationDigestV2(bad)
			provider := &effectDispatcherProbeV2{observe: permission.expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: bad}}
			if _, dispatchErr := DispatchAuthorizedEffectV2(t.Context(), permission, provider); !IsKind(dispatchErr, KindDecisionRequired) {
				t.Fatalf("wrong marker survived err=%v", dispatchErr)
			}
		}},
		{"M-WRONG-VALUE-AS-SUCCESS", func(t *testing.T) {
			bad := applied
			bad.Value = "other-value"
			bad.CanonicalDigest = effectObservationDigestV2(bad)
			provider := &effectDispatcherProbeV2{observe: permission.expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: bad}}
			if _, dispatchErr := DispatchAuthorizedEffectV2(t.Context(), permission, provider); !IsKind(dispatchErr, KindDecisionRequired) {
				t.Fatalf("wrong value survived err=%v", dispatchErr)
			}
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.id, mutant.kill)
	}
	ids := make([]string, len(mutants))
	for i := range mutants {
		ids[i] = mutants[i].id
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("mutant census is not canonical: %v", ids)
	}
	sum := sha256.Sum256([]byte(strings.Join(ids, "\n")))
	if got := hex.EncodeToString(sum[:]); got != "6760c84e44fcec3c237fa86f95a5ba8d15b792c4f56f4930484028a89ec85ebb" {
		t.Fatalf("mutant census count=%d digest=%s", len(mutants), got)
	}
}

func TestExternalEffectReducerRejectsEnvelopeAndNestedObservationDriftV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[0].Publication()
	var event journalv2.EventEnvelopeV2
	for _, candidate := range sealed.Events() {
		if candidate.Family() == "external_effect" {
			event = candidate
		}
	}
	if _, err := ReduceExternalEffectEventV2(nil, event); err != nil {
		t.Fatal(err)
	}
	state := result.State
	state.Intent.ProcessID = "process-other"
	payload, encodeErr := encodeExternalEffectStateV2(state)
	if encodeErr != nil {
		return
	}
	mutated, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: event.EventID(), EventIndex: event.EventIndex(), EventCount: event.EventCount(), EventType: event.EventType(), Family: event.Family(), EventSchemaVersion: event.EventSchemaVersion(), ReducerVersion: event.ReducerVersion(), Coordinate: event.Coordinate(), AggregateLogicalVersion: event.LogicalVersion(), OperationID: event.OperationID(), OperationLogicalVersion: event.OperationVersion(), CommittedRuntimeEpochID: event.CommittedRuntimeEpochID(), ActorID: event.ActorID(), ProcessID: event.ProcessID(), WorkRevisionID: event.WorkRevisionID(), PolicyRevision: event.PolicyRevision(), GovernanceRevisions: event.GovernanceRevisions(), Payload: payload, PublicationID: event.PublicationID()}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReduceExternalEffectEventV2(nil, mutated); err == nil {
		t.Fatal("accepted envelope/payload process drift")
	}
}

func TestExternalEffectReducerRejectsInitialAppliedObservationAndOperationSubstitutionV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[0].Publication()
	var event journalv2.EventEnvelopeV2
	for _, candidate := range sealed.Events() {
		if candidate.Family() == "external_effect" {
			event = candidate
		}
	}
	newEvent := func(operationID journalv2.OperationID, payload []byte) journalv2.EventEnvelopeV2 {
		t.Helper()
		mutated, eventErr := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: event.EventID(), EventIndex: event.EventIndex(), EventCount: event.EventCount(), EventType: event.EventType(), Family: event.Family(), EventSchemaVersion: event.EventSchemaVersion(), ReducerVersion: event.ReducerVersion(), Coordinate: event.Coordinate(), AggregateLogicalVersion: event.LogicalVersion(), OperationID: operationID, OperationLogicalVersion: event.OperationVersion(), CommittedRuntimeEpochID: event.CommittedRuntimeEpochID(), ActorID: event.ActorID(), ProcessID: event.ProcessID(), WorkRevisionID: event.WorkRevisionID(), PolicyRevision: event.PolicyRevision(), GovernanceRevisions: event.GovernanceRevisions(), Payload: payload, PublicationID: event.PublicationID()}, journalv2.DefaultEventRegistryV2())
		if eventErr != nil {
			t.Fatal(eventErr)
		}
		return mutated
	}
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	forged := result.State
	forged.AppliedObservation = applied
	forgedPayload, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReduceExternalEffectEventV2(nil, newEvent(event.OperationID(), forgedPayload)); err == nil {
		t.Fatal("accepted forged initial applied observation")
	}
	if _, err := ReduceExternalEffectEventV2(nil, newEvent("operation-substituted", event.Payload())); !IsKind(err, KindAuthorityConflict) {
		t.Fatalf("accepted substituted event operation ID: %v", err)
	}
}

func TestExternalEffectDispatchRequiresAcknowledgedOpaquePermissionAndClassifiesResponseLossV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	dispatcher := &effectDispatcherProbeV2{}
	if _, err := DispatchAuthorizedEffectV2(t.Context(), EffectDispatchPermissionV2{}, dispatcher); err == nil || dispatcher.calls != 0 {
		t.Fatalf("forgeable permission calls=%d err=%v", dispatcher.calls, err)
	}
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)
	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-dispatch-op"
	request.EffectEventID = "event-dispatch"
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.Permission == nil {
		t.Fatalf("permission=%+v err=%v", result.Permission, err)
	}
	dispatcher.result = EffectMutationResultV2{Kind: EffectMutationIndeterminateV2, Observation: result.Permission.expected}
	if evidence, err := DispatchAuthorizedEffectV2(t.Context(), *result.Permission, dispatcher); !IsKind(err, KindDecisionRequired) || evidence.Observation().Outcome != "unknown" || dispatcher.calls != 1 {
		t.Fatalf("indeterminate evidence=%+v calls=%d err=%v", evidence, dispatcher.calls, err)
	}
}

func TestExternalEffectRecoveryAdoptsReplacementWithoutRewritingOriginV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	origin := result.State.Intent
	replacement := result.ExecutionSlot
	replacement.RunID = "run-recovery"
	replacement.ProcessID = "process-recovery"
	replacement.LeaseID = "lease-recovery"
	replacement.Generation++
	replacement.Fence = "recovery-fence-0123456789"
	replacement.LogicalVersion++
	replacement.HeartbeatAt = request.ObservedAt.Add(time.Second).Format(time.RFC3339Nano)
	replacement.Deadline = request.ObservedAt.Add(time.Minute).Format(time.RFC3339Nano)
	port.resolution = EffectOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState(), RecordRevision: 21}
	port.effect = EffectStateObservationV2{State: &result.State, RecordRevision: 31}
	port.execution = ExecutionSlotObservationV2{State: &replacement, RecordRevision: 41}
	port.trace = nil
	request.Action = EffectAdoptRecoveryV2
	request.RuntimeEpochID = replacement.RuntimeEpochID
	request.ExecutionSlot = replacement
	request.OperationEventID = "event-recovery-op"
	request.EffectEventID = "event-recovery"
	epochs := &executionEpochProbeV2{epoch: replacement.RuntimeEpochID}
	service, _ = NewEffectAuthorityServiceV2(port, epochs, journalv2.DefaultEventRegistryV2())
	adopted, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !effectIntentEqualV2(adopted.State.Intent, origin) || adopted.State.RuntimeEpochID != replacement.RuntimeEpochID || adopted.State.Execution.ProcessID != replacement.ProcessID {
		t.Fatalf("bad adoption %+v", adopted.State)
	}
}

func TestExternalEffectLostAcknowledgementRecoversExactNonterminalPermissionV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, result.State, &request, result.ExecutionSlot)

	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-lost-ack-operation"
	request.EffectEventID = "event-lost-ack-effect"
	port.publishErr = context.DeadlineExceeded
	port.afterPublish = func(committed journalv2.SealedCommitPlanV2) {
		var effectEvent journalv2.EventEnvelopeV2
		var executionHead journalv2.HeadTransitionV2
		for _, event := range committed.Events() {
			if event.Family() == "external_effect" {
				effectEvent = event
			}
		}
		for _, head := range committed.Heads() {
			if head.Coordinate.Kind == "execution_slot" {
				executionHead = head
			}
		}
		state, decodeErr := DecodeExternalEffectStateV2(effectEvent.Payload())
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		executionState, decodeErr := DecodeExecutionSlotStateV2(executionHead.AuthoritySummary)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		port.resolution = EffectOperationResolutionV2{Kind: ExecutionOperationExactV2, State: committed.OperationState(), RecordRevision: 52}
		port.effect = EffectStateObservationV2{State: &state, RecordRevision: 52, LastPublicationID: committed.PublicationID()}
		port.execution = ExecutionSlotObservationV2{State: &executionState, RecordRevision: 52, LastPublicationID: committed.PublicationID()}
	}
	expected := effectObservationFixtureV2(request.Intent.Target, request.Intent.ExpectedProviderRevision, request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker, effectOutcomeForValueMarkerV2(request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker))
	dispatcher := &effectDispatcherProbeV2{observe: *expected, result: EffectMutationResultV2{Kind: EffectMutationIndeterminateV2}}
	recovered, err := service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-lost-ack-observation-operation", ObservationEffectEventID: "event-lost-ack-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if !IsKind(err, KindDecisionRequired) || !recovered.Replayed || recovered.State.Stage != EffectObservationRecordedV2 || recovered.State.Observation == nil || recovered.State.Observation.Outcome != "unknown" || dispatcher.calls != 1 {
		t.Fatalf("recovered=%+v calls=%d err=%v", recovered, dispatcher.calls, err)
	}
}

func TestExternalEffectRestartCutsIntentAttemptAndObservationV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) { applySealedEffectPlanToProbeV2(t, port, sealed) }
	restart := func() *EffectAuthorityServiceV2 {
		t.Helper()
		durable := &effectPortProbeV2{resolution: port.resolution, effect: port.effect, execution: port.execution}
		value, err := NewEffectAuthorityServiceV2(durable, &executionEpochProbeV2{epoch: request.RuntimeEpochID}, journalv2.DefaultEventRegistryV2())
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	assertReplay := func(action string, expected ExternalEffectStateV2) EffectAuthorityResultV2 {
		t.Helper()
		result, err := restart().Execute(t.Context(), request)
		gotBytes, _ := encodeExternalEffectStateV2(result.State)
		wantBytes, _ := encodeExternalEffectStateV2(expected)
		if err != nil || !result.Replayed || !bytes.Equal(gotBytes, wantBytes) {
			t.Fatalf("%s restart result=%+v err=%v", action, result, err)
		}
		return result
	}
	intent, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_ = assertReplay("intent", intent.State)
	request.ExecutionSlot = intent.ExecutionSlot
	request.Action = EffectStartAttemptV2
	request.OperationEventID, request.EffectEventID = "event-restart-attempt-operation", "event-restart-attempt-effect"
	attempt, err := service.Execute(t.Context(), request)
	if err != nil || attempt.Permission == nil {
		t.Fatalf("attempt=%+v err=%v", attempt, err)
	}
	replayedAttempt := assertReplay("attempt", attempt.State)
	if replayedAttempt.Permission == nil {
		t.Fatal("attempt restart lost acknowledged dispatch permission")
	}
	request.ExecutionSlot = attempt.ExecutionSlot
	request.Action = EffectRecordObservationV2
	request.OperationEventID, request.EffectEventID = "event-restart-observation-operation", "event-restart-observation-effect"
	request.Observation = effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	request.DispatchEvidence = &EffectDispatchEvidenceV2{observation: *request.Observation, publicationID: attempt.Permission.publicationID, planDigest: attempt.Permission.planDigest, providerRevision: attempt.Permission.providerRevision}
	observed, err := service.Execute(t.Context(), request)
	if err != nil || observed.State.Observation == nil {
		t.Fatalf("observation=%+v err=%v", observed, err)
	}
	replayedObservation := assertReplay("observation", observed.State)
	if replayedObservation.State.Observation == nil || !effectAttributionExactV2(*replayedObservation.State.Observation, *request.Observation) {
		t.Fatal("observation restart lost provider evidence")
	}
}

func TestExternalEffectTrustedCoordinatorPersistsOutcomeAfterAcknowledgementV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) {
		applySealedEffectPlanToProbeV2(t, port, sealed)
	}
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-coordinator-attempt-operation"
	request.EffectEventID = "event-coordinator-attempt-effect"
	timeline := []string{}
	port.timeline = &timeline
	expected := effectObservationFixtureV2(request.Intent.Target, request.Intent.ExpectedProviderRevision, request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker, effectOutcomeForValueMarkerV2(request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker))
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	dispatcher := &effectDispatcherProbeV2{observe: *expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: *applied}, timeline: &timeline}
	recorded, err := service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-coordinator-observation-operation", ObservationEffectEventID: "event-coordinator-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if err != nil || recorded.State.Stage != EffectObservationRecordedV2 || recorded.State.AppliedObservation == nil || !effectAttributionExactV2(*recorded.State.AppliedObservation, *applied) {
		t.Fatalf("recorded=%+v err=%v", recorded, err)
	}
	want := "resolve,execution,effect,publish,provider_observe,provider_apply,resolve,execution,effect,publish"
	if got := strings.Join(timeline, ","); got != want {
		t.Fatalf("timeline=%s want=%s", got, want)
	}
}

func TestExternalEffectServiceDiagnosticRedactsProviderCauseAndBindsRequestV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) {
		applySealedEffectPlanToProbeV2(t, port, sealed)
	}
	intent, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = intent.ExecutionSlot
	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-diagnostic-attempt-operation"
	request.EffectEventID = "event-diagnostic-attempt-effect"
	expected := effectObservationFixtureV2(request.Intent.Target, request.Intent.ExpectedProviderRevision, request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker, effectOutcomeForValueMarkerV2(request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker))
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	secretText := "provider-secret-" + strings.Repeat("x", 32*1024)
	providerCause := errors.New(secretText)
	dispatcher := &effectDispatcherProbeV2{observe: *expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: *applied}, mutationErr: providerCause}
	recorded, err := service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-diagnostic-observation-operation", ObservationEffectEventID: "event-diagnostic-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if !IsKind(err, KindDecisionRequired) || recorded.State.Observation == nil || !errors.Is(err, providerCause) {
		t.Fatalf("recorded=%+v err=%v", recorded, err)
	}
	var diagnostic *AuthorityRequestErrorV2
	if !errors.As(err, &diagnostic) {
		t.Fatalf("missing request diagnostic: %T", err)
	}
	want := request.ExecutionSlot
	got := diagnostic.Diagnostic
	if got.OperationID != request.Intent.OperationID || got.RuntimeEpochID != request.RuntimeEpochID || got.WorkRevisionID != want.WorkRevisionID || got.WorkID != want.WorkID || got.RunID != want.RunID || got.LeaseID != want.LeaseID || got.ActorID != want.ActorID || got.ProcessID != want.ProcessID || got.CauseClass != "provider_failure" {
		t.Fatalf("diagnostic coordinates=%+v", got)
	}
	encoded, marshalErr := json.Marshal(diagnostic)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	for _, rendered := range []string{diagnostic.Error(), string(encoded)} {
		if strings.Contains(rendered, "provider-secret") || strings.Contains(rendered, secretText) || len(rendered) > 4096 {
			t.Fatalf("unbounded or secret-bearing diagnostic: length=%d", len(rendered))
		}
	}
}

func TestExternalEffectAuthorityExternalFailuresUseBoundedRequestDiagnosticsV2(t *testing.T) {
	for _, test := range []struct {
		name      string
		subject   string
		kind      ErrorKind
		configure func(*effectPortProbeV2, *executionEpochProbeV2, error)
	}{
		{"operation_resolution", "operation_resolution", KindControlStoreBusy, func(p *effectPortProbeV2, _ *executionEpochProbeV2, cause error) { p.resolutionErr = cause }},
		{"runtime_epoch", "runtime_epoch", KindStaleAuthority, func(_ *effectPortProbeV2, e *executionEpochProbeV2, cause error) { e.err = cause }},
		{"execution_slot", "execution_slot", KindStaleAuthority, func(p *effectPortProbeV2, _ *executionEpochProbeV2, cause error) { p.executionErr = cause }},
		{"effect_state", "effect_state", KindControlStoreBusy, func(p *effectPortProbeV2, _ *executionEpochProbeV2, cause error) { p.effectErr = cause }},
		{"effect_publication", "effect_publication", KindControlStoreBusy, func(p *effectPortProbeV2, _ *executionEpochProbeV2, cause error) { p.publishErr = cause }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, port, service := effectRequestFixtureV2(t)
			epoch := service.epochs.(*executionEpochProbeV2)
			secret := errors.New("provider-secret-" + strings.Repeat("q", 32*1024))
			test.configure(port, epoch, secret)
			_, err := service.Execute(t.Context(), request)
			executionRequest := ExecutionAuthorityRequestV2{OperationID: request.Intent.OperationID, WorkRevisionID: request.ExecutionSlot.WorkRevisionID, WorkID: request.ExecutionSlot.WorkID, RunID: request.ExecutionSlot.RunID, LeaseID: request.ExecutionSlot.LeaseID, ActorID: request.ExecutionSlot.ActorID, ProcessID: request.ExecutionSlot.ProcessID, RuntimeEpochID: request.RuntimeEpochID}
			assertAuthorityRequestDiagnosticV2(t, err, secret, test.kind, test.subject, executionRequest)
		})
	}
}

func TestExternalEffectCrashAfterProviderCallRecoversByObserveWithoutSecondMutationV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) {
		applySealedEffectPlanToProbeV2(t, port, sealed)
	}
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-crash-attempt-operation"
	request.EffectEventID = "event-crash-attempt-effect"
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	dispatcher := &effectDispatcherProbeV2{observe: *applied}
	recorded, err := service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-crash-observation-operation", ObservationEffectEventID: "event-crash-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if !IsKind(err, KindDecisionRequired) || dispatcher.calls != 0 || recorded.State.AppliedObservation == nil || !effectAttributionExactV2(*recorded.State.AppliedObservation, *applied) {
		t.Fatalf("recorded=%+v mutation_calls=%d err=%v", recorded, dispatcher.calls, err)
	}
}

func TestExternalEffectRetryProgressionReplaysEveryPublicationV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) { applySealedEffectPlanToProbeV2(t, port, sealed) }
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectStartAttemptV2
	request.OperationEventID, request.EffectEventID = "event-retry-start-operation", "event-retry-start-effect"
	absent := effectObservationFixtureV2(request.Intent.Target, 1, "", "", "absent")
	unknownDispatcher := &effectDispatcherProbeV2{observe: *absent, result: EffectMutationResultV2{Kind: EffectMutationIndeterminateV2}}
	result, err = service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-retry-unknown-operation", ObservationEffectEventID: "event-retry-unknown-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, unknownDispatcher)
	if !IsKind(err, KindDecisionRequired) || result.State.Stage != EffectObservationRecordedV2 {
		t.Fatalf("unknown result=%+v err=%v", result, err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectRequireReconcileV2
	request.OperationEventID, request.EffectEventID = "event-retry-reconcile-1-operation", "event-retry-reconcile-1-effect"
	request.Observation, request.DispatchEvidence = nil, nil
	result, err = service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectRecordObservationV2
	request.OperationEventID, request.EffectEventID = "event-retry-absence-operation", "event-retry-absence-effect"
	request.Observation = absent
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectObservationRecordedV2 {
		t.Fatalf("absence result=%+v err=%v", result, err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectRequireReconcileV2
	request.OperationEventID, request.EffectEventID = "event-retry-reconcile-2-operation", "event-retry-reconcile-2-effect"
	request.Observation = nil
	result, err = service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectAllowRetryV2
	request.OperationEventID, request.EffectEventID = "event-retry-permission-operation", "event-retry-permission-effect"
	request.Observation = absent
	request.PermissionMarker = string(request.Intent.OperationID)
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	retryDispatcher := &effectDispatcherProbeV2{observe: *absent, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: *applied}}
	result, err = service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-retry-applied-operation", ObservationEffectEventID: "event-retry-applied-effect", ObservationAt: request.ObservedAt.Add(2 * time.Second)}, retryDispatcher)
	if err != nil || result.State.Stage != EffectReconciliationRequiredV2 || result.State.AppliedObservation == nil || retryDispatcher.calls != 1 {
		t.Fatalf("retry result=%+v calls=%d err=%v", result, retryDispatcher.calls, err)
	}
	request.ExecutionSlot = result.ExecutionSlot
	request.Action = EffectResolveV2
	request.OperationEventID, request.EffectEventID = "event-retry-resolve-operation", "event-retry-resolve-effect"
	request.Observation, request.DispatchEvidence, request.PermissionMarker = nil, nil, ""
	request.Outcome = "applied"
	result, err = service.Execute(t.Context(), request)
	if err != nil || result.State.Stage != EffectResolvedV2 {
		t.Fatalf("resolve result=%+v err=%v", result, err)
	}
}

func TestAssignmentEffectDispatcherClassifiesDishonestAndIndeterminateProvidersV2(t *testing.T) {
	request, _, _ := effectRequestFixtureV2(t)
	assignment := assignmentObservationFixtureV2(t, request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID))
	probe := &assignmentProjectionProbeV2{observation: assignment, mutation: tracker.AssignmentMutation{Observation: assignment, Guarantee: tracker.AssignmentAtomicClaim}}
	dispatcher, err := NewAssignmentEffectDispatcherV2(probe)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := dispatcher.ObserveEffect(t.Context(), request.Intent.Target)
	if err != nil || observed.Value != request.Intent.IntendedValue || observed.OperationMarker != string(request.Intent.OperationID) {
		t.Fatalf("observed=%+v err=%v", observed, err)
	}
	conditional := EffectConditionalMutationV2{EffectID: request.Intent.EffectID, Target: request.Intent.Target, ExpectedRevision: 1, ExpectedValue: "", ExpectedMarker: "none", OperationMarker: string(request.Intent.OperationID), IntendedValue: request.Intent.IntendedValue, RequiredGuarantee: tracker.AssignmentAtomicClaim}
	mutation, err := dispatcher.ApplyEffect(t.Context(), conditional)
	if err != nil || mutation.Kind != EffectMutationDefinitiveV2 || mutation.Observation.ProviderRevision != 2 {
		t.Fatalf("mutation=%+v err=%v", mutation, err)
	}
	probe.mutation.Guarantee = tracker.AssignmentUnguarded
	if downgraded, err := dispatcher.ApplyEffect(t.Context(), conditional); !IsKind(err, KindDecisionRequired) || downgraded.Kind != EffectMutationIndeterminateV2 {
		t.Fatalf("apply guarantee downgrade accepted: %+v err=%v", downgraded, err)
	}
	if downgraded, err := dispatcher.CompensateEffect(t.Context(), conditional); !IsKind(err, KindDecisionRequired) || downgraded.Kind != EffectMutationIndeterminateV2 {
		t.Fatalf("compensation guarantee downgrade accepted: %+v err=%v", downgraded, err)
	}
	probe.mutation.Guarantee = tracker.AssignmentAtomicClaim
	conditional.RequiredGuarantee = tracker.AssignmentUnguarded
	if stronger, err := dispatcher.ApplyEffect(t.Context(), conditional); err != nil || stronger.Kind != EffectMutationDefinitiveV2 {
		t.Fatalf("stronger atomic guarantee did not satisfy unguarded request: %+v err=%v", stronger, err)
	}
	conditional.RequiredGuarantee = tracker.AssignmentAtomicClaim

	probe.mutationErr = &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "response lost"}
	probe.mutation = tracker.AssignmentMutation{}
	mutation, err = dispatcher.ApplyEffect(t.Context(), conditional)
	if mutation.Kind != EffectMutationIndeterminateV2 || err == nil || mutation.Observation.ProviderRevision != 0 {
		t.Fatalf("ambiguous mutation fabricated facts: %+v err=%v", mutation, err)
	}

	probe.mutationErr = nil
	dishonest := assignment
	dishonest.ExternalRef.ExternalID = "different-work"
	probe.observation = dishonest
	if _, err := dispatcher.ObserveEffect(t.Context(), request.Intent.Target); err == nil {
		t.Fatal("dishonest mapping was accepted")
	}
}

func TestAssignmentEffectDispatcherUsesRealFakeTrackerAtomicContractV2(t *testing.T) {
	request, _, _ := effectRequestFixtureV2(t)
	store, err := trackerfake.Open(filepath.Join(t.TempDir(), "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: request.Intent.Target.ExternalID, Title: "effect target"}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := NewAssignmentEffectDispatcherV2(trackerfake.NewProvider(store))
	if err != nil {
		t.Fatal(err)
	}
	before, err := dispatcher.ObserveEffect(t.Context(), request.Intent.Target)
	if err != nil || before.Outcome != "absent" || before.ProviderRevision != 1 {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	applied, err := dispatcher.ApplyEffect(t.Context(), EffectConditionalMutationV2{EffectID: request.Intent.EffectID, Target: request.Intent.Target, ExpectedRevision: before.ProviderRevision, ExpectedValue: before.Value, ExpectedMarker: before.OperationMarker, OperationMarker: string(request.Intent.OperationID), IntendedValue: request.Intent.IntendedValue, RequiredGuarantee: tracker.AssignmentAtomicClaim})
	if err != nil || applied.Kind != EffectMutationDefinitiveV2 || applied.Observation.Value != request.Intent.IntendedValue || applied.Observation.OperationMarker != string(request.Intent.OperationID) {
		t.Fatalf("applied=%+v err=%v", applied, err)
	}
	compensated, err := dispatcher.CompensateEffect(t.Context(), EffectConditionalMutationV2{EffectID: request.Intent.EffectID, Target: request.Intent.Target, ExpectedRevision: applied.Observation.ProviderRevision, ExpectedValue: applied.Observation.Value, ExpectedMarker: applied.Observation.OperationMarker, OperationMarker: string(request.Intent.OperationID), RestoreValue: "", RequiredGuarantee: tracker.AssignmentAtomicClaim})
	if err != nil || compensated.Kind != EffectMutationDefinitiveV2 || compensated.Observation.Outcome != "absent" || compensated.Observation.OperationMarker != "" {
		t.Fatalf("compensated=%+v err=%v", compensated, err)
	}
}

func TestAssignmentEffectDispatcherPreservesBeadsSafeCompensationRefusalEvidenceV2(t *testing.T) {
	request, _, _ := effectRequestFixtureV2(t)
	appliedEffect := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	appliedAssignment := assignmentObservationFixtureV2(t, request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID))
	probe := &assignmentProjectionProbeV2{observation: appliedAssignment, mutation: tracker.AssignmentMutation{Observation: appliedAssignment, Guarantee: tracker.AssignmentUnguarded, Noop: true}, mutationErr: &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported, Message: "provider lacks atomic restore"}}
	dispatcher, _ := NewAssignmentEffectDispatcherV2(probe)
	digest, _ := journalv2.ParseDigest("effect_test", strings.Repeat("a", 64))
	permission := EffectDispatchPermissionV2{effectID: request.Intent.EffectID, action: EffectAllowCompensationV2, marker: string(request.Intent.OperationID), expected: *appliedEffect, publicationID: testPublicationIDV2(t, "beads-refusal"), planDigest: digest, providerRevision: 17, guarantee: tracker.AssignmentAtomicClaim, intendedValue: request.Intent.IntendedValue, restoreValue: request.Intent.ExpectedValue}
	evidence, err := DispatchAuthorizedEffectV2(t.Context(), permission, dispatcher)
	if !IsKind(err, KindDecisionRequired) || probe.compensateCalls != 1 || evidence.Observation().ProviderRevision != 2 || evidence.Observation().Value != request.Intent.IntendedValue || evidence.Observation().OperationMarker != string(request.Intent.OperationID) {
		t.Fatalf("evidence=%+v calls=%d err=%v", evidence.Observation(), probe.compensateCalls, err)
	}
}

func TestExternalEffectTerminalPlaybackCallsNoProviderV2(t *testing.T) {
	request, port, service := effectRequestFixtureV2(t)
	governance, _ := ExecutionGovernanceFromContextV2(request.Intent.Governance.Context)
	state := ExternalEffectStateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: EffectResolvedV2, Intent: request.Intent, ExecutionSlot: mustExecutionSlotCoordinateV2(request.ExecutionSlot), Execution: executionCoordinatesV2(request.ExecutionSlot), CurrentGovernance: governance, RuntimeEpochID: request.RuntimeEpochID, Outcome: "applied", LastOperationEventID: "event-terminal-operation", LastEffectEventID: "event-terminal-effect"}
	encoded, err := encodeExternalEffectStateV2(state)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: request.Intent.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "external_effect.result", ResultSchemaVersion: effectResultSchemaV2, CanonicalResult: encoded, EventIDs: []journalv2.EventID{"event-terminal"}, LastPublicationID: testPublicationIDV2(t, "effect-terminal")})
	if err != nil {
		t.Fatal(err)
	}
	port.resolution = EffectOperationResolutionV2{Kind: ExecutionOperationExactV2, State: operation, RecordRevision: 77}
	dispatcher := &effectDispatcherProbeV2{}
	_, err = service.ExecuteDispatchV2(t.Context(), EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-terminal-observation-operation", ObservationEffectEventID: "event-terminal-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}, dispatcher)
	if !IsKind(err, KindDecisionRequired) || dispatcher.calls != 0 {
		t.Fatalf("terminal playback dispatched provider calls=%d err=%v", dispatcher.calls, err)
	}
}

func effectRequestFixtureV2(t *testing.T) (EffectAuthorityRequestV2, *effectPortProbeV2, *EffectAuthorityServiceV2) {
	t.Helper()
	executionRequest, _ := executionRequestFixtureV2(t, ExecutionAdmitV2)
	governance, _ := ExecutionGovernanceFromContextV2(executionRequest.Governance)
	slot, err := decideExecutionTransitionV2(executionRequest, governance, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := tracker.ExternalWorkRef{ProjectID: slot.ProjectID, WorkID: slot.WorkID, Provider: "fake", ExternalID: "work-external", MetadataSchema: 1, CreatedByOperation: "operation-create", LastObservation: 1}
	intent := EffectIntentV2{EffectID: "effect-1", OperationID: "operation-effect-1", Target: target, IntendedValue: "actor-1", ExpectedProviderRevision: 1, ExpectedValue: "", ExpectedOperationMarker: "", RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: slot.ActorID, ProcessID: slot.ProcessID, Governance: slot.Governance, OriginRuntimeEpochID: slot.RuntimeEpochID, OriginObservedCreatedAt: executionRequest.ObservedAt.Add(10).UTC().Truncate(time.Second).Format(time.RFC3339)}
	intent.CanonicalRequestDigest, _ = EffectRequestDigestV2(intent)
	request := EffectAuthorityRequestV2{Action: EffectRecordIntentV2, Intent: intent, RuntimeEpochID: slot.RuntimeEpochID, ExecutionSlot: slot, ObservedAt: executionRequest.ObservedAt.Add(10), OperationEventID: "event-effect-operation-intent", EffectEventID: "event-effect-intent"}
	port := &effectPortProbeV2{resolution: EffectOperationResolutionV2{Kind: ExecutionOperationAbsentV2}, execution: ExecutionSlotObservationV2{State: &slot, RecordRevision: 11}}
	epochs := &executionEpochProbeV2{epoch: slot.RuntimeEpochID}
	service, err := NewEffectAuthorityServiceV2(port, epochs, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return request, port, service
}

func advanceEffectProbeV2(port *effectPortProbeV2, sealed journalv2.SealedCommitPlanV2, state ExternalEffectStateV2, request *EffectAuthorityRequestV2, executionSlot ExecutionSlotStateV2) {
	port.resolution = EffectOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState(), RecordRevision: uint64(state.LogicalVersion + 20)}
	port.effect = EffectStateObservationV2{State: &state, RecordRevision: uint64(state.LogicalVersion + 30)}
	request.ExecutionSlot = executionSlot
	port.execution = ExecutionSlotObservationV2{State: &request.ExecutionSlot, RecordRevision: uint64(executionSlot.LogicalVersion + 40)}
	port.trace = nil
}

func effectObservationFixtureV2(target tracker.ExternalWorkRef, revision tracker.Revision, value, marker, outcome string) *EffectProviderObservationV2 {
	target.LastObservation = revision
	observation := &EffectProviderObservationV2{Target: target, ProviderRevision: revision, Value: value, OperationMarker: marker, Outcome: outcome}
	observation.CanonicalDigest = effectObservationDigestV2(*observation)
	return observation
}

func assignmentObservationFixtureV2(t *testing.T, target tracker.ExternalWorkRef, revision tracker.Revision, value, marker string) tracker.AssignmentObservation {
	t.Helper()
	provider, err := tracker.NewProviderObservation(target.ExternalID, []byte("canonical-assignment"), revision, "", "provider-token", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	provider.Assignee = value
	provider.OperationMarker = marker
	target.LastObservation = revision
	return tracker.AssignmentObservation{ExternalRef: target, Assignee: value, OperationMarker: marker, Observation: provider}
}

func testPublicationIDV2(t *testing.T, preimage string) journalv2.PublicationID {
	t.Helper()
	sum := sha256.Sum256([]byte(preimage))
	id, err := journalv2.DecodePublicationID(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func applySealedEffectPlanToProbeV2(t *testing.T, port *effectPortProbeV2, sealed journalv2.SealedCommitPlanV2) {
	t.Helper()
	var effectState ExternalEffectStateV2
	var executionState ExecutionSlotStateV2
	for _, event := range sealed.Events() {
		if event.Family() == "external_effect" {
			var err error
			effectState, err = DecodeExternalEffectStateV2(event.Payload())
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind == "execution_slot" {
			var err error
			executionState, err = DecodeExecutionSlotStateV2(head.AuthoritySummary)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	revision := uint64(effectState.LogicalVersion + 60)
	port.resolution = EffectOperationResolutionV2{Kind: ExecutionOperationExactV2, State: sealed.OperationState(), RecordRevision: revision}
	port.effect = EffectStateObservationV2{State: &effectState, RecordRevision: revision, LastPublicationID: sealed.PublicationID()}
	port.execution = ExecutionSlotObservationV2{State: &executionState, RecordRevision: revision, LastPublicationID: sealed.PublicationID()}
}
