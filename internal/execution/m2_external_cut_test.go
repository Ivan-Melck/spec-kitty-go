package execution

import (
	"context"
	"errors"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestM2ExternalEffectCutBeforeProviderCall(t *testing.T) {
	_, _, _ = effectRequestFixtureV2(t)
	dispatcher := &effectDispatcherProbeV2{}
	if _, err := dispatchAuthorizedEffectV2(t.Context(), effectDispatchPermissionV2{}, dispatcher); err == nil || dispatcher.calls != 0 || dispatcher.observeCalls != 0 {
		t.Fatalf("unacknowledged permission crossed provider boundary: calls=%d observes=%d err=%v", dispatcher.calls, dispatcher.observeCalls, err)
	}
}

func TestM2ExternalEffectCutAfterCallBeforeDurableObservation(t *testing.T) {
	request, port, service := m2PreparedEffectDispatch(t)
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	expected := effectObservationFixtureV2(request.Intent.Target, request.Intent.ExpectedProviderRevision, request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker, effectOutcomeForValueMarkerV2(request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker))
	dispatcher := &effectDispatcherProbeV2{observe: *expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: *applied}}
	publications := 0
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) {
		publications++
		if publications == 1 { // durable permission before provider call
			applySealedEffectPlanToProbeV2(t, port, sealed)
			return
		}
		// Provider success is now real, but its observation publication is
		// interrupted before durability.
		port.publishErr = context.DeadlineExceeded
	}
	command := EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-m2-after-call-operation", ObservationEffectEventID: "event-m2-after-call-effect", ObservationAt: request.ObservedAt.Add(time.Second)}
	if _, err := service.ExecuteDispatchV2(t.Context(), command, dispatcher); !IsKind(err, KindDecisionRequired) || !errors.Is(err, context.DeadlineExceeded) || dispatcher.calls != 1 || port.effect.State == nil || port.effect.State.Stage != EffectAttemptStartedV2 {
		t.Fatalf("after-call cut was not retained: calls=%d state=%+v err=%v", dispatcher.calls, port.effect.State, err)
	}
	port.publishErr = nil
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) { applySealedEffectPlanToProbeV2(t, port, sealed) }
	recoveryDispatcher := &effectDispatcherProbeV2{observe: *applied}
	recovered, err := service.ExecuteDispatchV2(t.Context(), command, recoveryDispatcher)
	if !IsKind(err, KindDecisionRequired) || recovered.State.Observation == nil || recoveryDispatcher.calls != 0 || recoveryDispatcher.observeCalls != 1 {
		t.Fatalf("recovery repeated mutation or lost evidence: result=%+v calls=%d observes=%d err=%v", recovered, recoveryDispatcher.calls, recoveryDispatcher.observeCalls, err)
	}
}

func TestM2ExternalEffectCutAfterObservationBeforeResultPublication(t *testing.T) {
	request, port, service := m2PreparedEffectDispatch(t)
	applied := effectObservationFixtureV2(request.Intent.Target, 2, request.Intent.IntendedValue, string(request.Intent.OperationID), "confirmed")
	expected := effectObservationFixtureV2(request.Intent.Target, request.Intent.ExpectedProviderRevision, request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker, effectOutcomeForValueMarkerV2(request.Intent.ExpectedValue, request.Intent.ExpectedOperationMarker))
	dispatcher := &effectDispatcherProbeV2{observe: *expected, result: EffectMutationResultV2{Kind: EffectMutationDefinitiveV2, Observation: *applied}}
	publications := 0
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) {
		publications++
		applySealedEffectPlanToProbeV2(t, port, sealed)
		if publications == 2 { // observation is durable; caller result is lost
			port.publishErr = context.DeadlineExceeded
			port.resolutionErr = context.Canceled
		}
	}
	command := EffectDispatchCommandV2{Authorization: request, ObservationOperationEventID: "event-m2-after-observation-operation", ObservationEffectEventID: "event-m2-after-observation-effect", ObservationAt: request.ObservedAt.Add(time.Second)}
	if _, err := service.ExecuteDispatchV2(t.Context(), command, dispatcher); !IsKind(err, KindDecisionRequired) || !errors.Is(err, context.DeadlineExceeded) || dispatcher.calls != 1 || port.effect.State == nil || port.effect.State.Observation == nil {
		t.Fatalf("durable observation cut was not retained: calls=%d state=%+v err=%v", dispatcher.calls, port.effect.State, err)
	}
	port.publishErr = nil
	port.resolutionErr = nil
	port.afterPublish = func(sealed journalv2.SealedCommitPlanV2) { applySealedEffectPlanToProbeV2(t, port, sealed) }
	if port.execution.State == nil {
		t.Fatal("durable observation omitted execution guard state")
	}
	command.Authorization.ExecutionSlot = *port.execution.State
	replayDispatcher := &effectDispatcherProbeV2{}
	_, err := service.ExecuteDispatchV2(t.Context(), command, replayDispatcher)
	if !IsKind(err, KindDecisionRequired) || replayDispatcher.calls != 0 || port.effect.State == nil || port.effect.State.Observation == nil || !effectAttributionExactV2(*port.effect.State.Observation, *applied) {
		t.Fatalf("durable result recovery repeated provider mutation or lost evidence: state=%+v calls=%d err=%v", port.effect.State, replayDispatcher.calls, err)
	}
}

func m2PreparedEffectDispatch(t *testing.T) (EffectAuthorityRequestV2, *effectPortProbeV2, *EffectAuthorityServiceV2) {
	t.Helper()
	request, port, service := effectRequestFixtureV2(t)
	intent, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _ := port.plans[len(port.plans)-1].Publication()
	advanceEffectProbeV2(port, sealed, intent.State, &request, intent.ExecutionSlot)
	request.Action = EffectStartAttemptV2
	request.OperationEventID = "event-m2-attempt-operation"
	request.EffectEventID = "event-m2-attempt-effect"
	return request, port, service
}
