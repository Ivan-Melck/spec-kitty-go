package mission

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type decompositionPortProbeV2 struct {
	authorityPortProbeV2
	trace *[]string
}

func (p *decompositionPortProbeV2) PublishMission(ctx context.Context, plan journalv2.SealedCommitPlanV2) (MissionPublicationV2, error) {
	*p.trace = append(*p.trace, "publish:"+string(plan.OperationState().Status()))
	return p.authorityPortProbeV2.PublishMission(ctx, plan)
}

type decompositionProviderProbeV2 struct {
	trace             *[]string
	observations      map[journalv2.EffectID]DecompositionProviderObservationV2
	mutation          DecompositionProviderMutationV2
	dishonest         bool
	observeErr        error
	mutateErr         error
	mutateResponseErr error
	mutateObserved    func(DecompositionProviderObservationV2) DecompositionProviderObservationV2
	compensations     int
}

func (p *decompositionProviderProbeV2) CompensateEffect(context.Context, DecompositionProviderMutationV2, DecompositionProviderObservationV2) error {
	p.compensations++
	*p.trace = append(*p.trace, "compensate")
	return nil
}

func (p *decompositionProviderProbeV2) RequireCapabilities(context.Context, []DecompositionEffectKindV2) error {
	*p.trace = append(*p.trace, "capability")
	return nil
}
func (p *decompositionProviderProbeV2) ObserveEffect(_ context.Context, effectID journalv2.EffectID) (DecompositionProviderObservationV2, bool, error) {
	*p.trace = append(*p.trace, "observe")
	if p.observeErr != nil {
		return DecompositionProviderObservationV2{}, false, p.observeErr
	}
	value, found := p.observations[effectID]
	return value, found, nil
}
func (p *decompositionProviderProbeV2) MutateEffect(_ context.Context, mutation DecompositionProviderMutationV2) error {
	*p.trace = append(*p.trace, "mutate")
	p.mutation = mutation
	if p.mutateErr != nil {
		return p.mutateErr
	}
	digest, _ := journalv2.ReducerStateDigestV2(mutation.CanonicalDefinition)
	marker := mutation.ChildOperationID
	if p.dishonest {
		marker = "foreign-marker"
	}
	observation := DecompositionProviderObservationV2{
		EffectID: mutation.EffectID, WorkID: mutation.WorkID, ExternalRef: "external-1",
		ProviderRevision: mutation.ExpectedRevision + 1, ValueDigest: digest, OperationMarker: marker,
	}
	if p.mutateObserved != nil {
		observation = p.mutateObserved(observation)
	}
	p.observations[mutation.EffectID] = observation
	return p.mutateResponseErr
}

func TestDecompositionIntentPrecedesProviderAndCompletionV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	trace := []string{}
	port := &decompositionPortProbeV2{authorityPortProbeV2: authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2},
		mission:    MissionObservationV2{State: request.Requested.Expected, RecordRevision: 8, LastEventID: "prior-event"},
	}, trace: &trace}
	provider := &decompositionProviderProbeV2{trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2)}
	service, err := NewDecompositionAuthorityServiceV2(port, &epochProbeV2{epoch: request.Requested.OriginatingRuntimeEpochID}, provider, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Digest() != request.CompletedState.Digest() || len(result.Observations) != 1 || len(port.publishes) != 3 {
		t.Fatalf("state=%s observations=%d publications=%d", result.State.Decomposition(), len(result.Observations), len(port.publishes))
	}
	wantTrace := []string{"capability", "publish:in_progress", "observe", "mutate", "observe", "publish:in_progress", "publish:resolved"}
	if !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("trace=%v want=%v", trace, wantTrace)
	}
	first := port.publishes[0]
	if len(first.EffectIntents()) != 1 || first.OperationState().Status() != journalv2.StatusInProgress {
		t.Fatalf("first publication omitted durable request/effect: effects=%d status=%s", len(first.EffectIntents()), first.OperationState().Status())
	}
	effectID, childID := DeriveDecompositionEffectIdentityV2(request.Requested.OperationID, DecompositionEffectCreateV2, "work-1")
	if provider.mutation.EffectID != effectID || provider.mutation.ChildOperationID != childID {
		t.Fatalf("unstable identities: mutation=%+v", provider.mutation)
	}
}

func TestDecompositionDishonestObservationRequiresReconciliationV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	trace := []string{}
	port := &decompositionPortProbeV2{authorityPortProbeV2: authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2},
		mission:    MissionObservationV2{State: request.Requested.Expected, RecordRevision: 8, LastEventID: "prior-event"},
	}, trace: &trace}
	provider := &decompositionProviderProbeV2{trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2), dishonest: true}
	service, _ := NewDecompositionAuthorityServiceV2(port, &epochProbeV2{epoch: request.Requested.OriginatingRuntimeEpochID}, provider, journalv2.DefaultEventRegistryV2())
	result, err := service.Execute(t.Context(), request)
	if !IsMissionAuthorityKindV2(err, MissionAuthorityAmbiguousV2) || result.State.Decomposition() != DecompositionReconciliationRequiredV2 {
		t.Fatalf("result=%+v err=%v cause=%v", result, err, errors.Unwrap(err))
	}
	if len(port.publishes) != 2 || port.publishes[1].OperationState().Status() != journalv2.StatusAmbiguous {
		t.Fatalf("reconciliation publication missing: count=%d", len(port.publishes))
	}
}

func TestDecompositionDishonestProviderMatrixNeverCompletesV2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		configure func(*decompositionProviderProbeV2, DecompositionAuthorityRequestV2)
	}{
		{"stale-revision", func(provider *decompositionProviderProbeV2, _ DecompositionAuthorityRequestV2) {
			provider.mutateObserved = func(value DecompositionProviderObservationV2) DecompositionProviderObservationV2 {
				value.ProviderRevision = 0
				return value
			}
		}},
		{"changed-value", func(provider *decompositionProviderProbeV2, _ DecompositionAuthorityRequestV2) {
			provider.mutateObserved = func(value DecompositionProviderObservationV2) DecompositionProviderObservationV2 {
				value.ValueDigest = testDigestV2("foreign-value")
				return value
			}
		}},
		{"missing-marker", func(provider *decompositionProviderProbeV2, _ DecompositionAuthorityRequestV2) {
			provider.mutateObserved = func(value DecompositionProviderObservationV2) DecompositionProviderObservationV2 {
				value.OperationMarker = ""
				return value
			}
		}},
		{"foreign-marker", func(provider *decompositionProviderProbeV2, _ DecompositionAuthorityRequestV2) {
			provider.dishonest = true
		}},
		{"provider-outage", func(provider *decompositionProviderProbeV2, _ DecompositionAuthorityRequestV2) {
			provider.observeErr = errors.New("unavailable")
		}},
		{"mutation-without-attributable-observation", func(provider *decompositionProviderProbeV2, _ DecompositionAuthorityRequestV2) {
			provider.mutateObserved = func(value DecompositionProviderObservationV2) DecompositionProviderObservationV2 {
				value.ExternalRef = ""
				return value
			}
		}},
		{"preexisting-foreign-result", func(provider *decompositionProviderProbeV2, request DecompositionAuthorityRequestV2) {
			plan := request.Effects[0]
			effectID, _ := DeriveDecompositionEffectIdentityV2(request.Requested.OperationID, plan.Kind, plan.WorkID)
			digest, _ := journalv2.ReducerStateDigestV2(plan.CanonicalDefinition)
			provider.observations[effectID] = DecompositionProviderObservationV2{EffectID: effectID, WorkID: plan.WorkID, ExternalRef: "preexisting", ProviderRevision: 1, ValueDigest: digest, OperationMarker: "foreign-operation"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := fixtureDecompositionRequestV2(t)
			trace := []string{}
			port := &decompositionPortProbeV2{authorityPortProbeV2: authorityPortProbeV2{
				resolution: OperationResolutionV2{Kind: OperationAbsentV2},
				mission:    MissionObservationV2{State: request.Requested.Expected, RecordRevision: 8, LastEventID: "prior-event"},
			}, trace: &trace}
			provider := &decompositionProviderProbeV2{trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2)}
			test.configure(provider, request)
			service, err := NewDecompositionAuthorityServiceV2(port, &epochProbeV2{epoch: request.Requested.OriginatingRuntimeEpochID}, provider, journalv2.DefaultEventRegistryV2())
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Execute(t.Context(), request)
			if !IsMissionAuthorityKindV2(err, MissionAuthorityAmbiguousV2) || result.State.Decomposition() != DecompositionReconciliationRequiredV2 {
				t.Fatalf("dishonest provider completed: state=%s err=%v", result.State.Decomposition(), err)
			}
			if len(port.publishes) != 2 || port.publishes[0].OperationState().Status() != journalv2.StatusInProgress || port.publishes[1].OperationState().Status() != journalv2.StatusAmbiguous {
				t.Fatalf("reconciliation evidence missing: publications=%d", len(port.publishes))
			}
		})
	}
}

func TestDecompositionCrashBeforeIntentMakesNoProviderCallV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	trace := []string{}
	port := &decompositionPortProbeV2{authorityPortProbeV2: authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2}, mission: MissionObservationV2{State: request.Requested.Expected, RecordRevision: 8},
		publishErr: errors.New("crash before durable acknowledgement"),
	}, trace: &trace}
	provider := &decompositionProviderProbeV2{trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2)}
	service, _ := NewDecompositionAuthorityServiceV2(port, &epochProbeV2{epoch: request.Requested.OriginatingRuntimeEpochID}, provider, journalv2.DefaultEventRegistryV2())
	if _, err := service.Execute(t.Context(), request); !IsMissionAuthorityKindV2(err, MissionAuthorityAmbiguousV2) {
		t.Fatalf("err=%v", err)
	}
	for _, call := range trace {
		if call == "mutate" {
			t.Fatalf("provider mutated before durable intent: %v", trace)
		}
	}
}

func TestDecompositionRestartAfterRequestedPublicationReusesIdentitiesV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	command, effects, err := prepareDecompositionRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := validateMissionAuthorityCommandV2(command)
	if err != nil {
		t.Fatal(err)
	}
	requestedPlan, err := buildMissionAuthorityStagePlanV2(command, &binding, nil, 0, MissionObservationV2{State: command.Expected, RecordRevision: 8}, journalv2.StatusInProgress, "", nil, command.EffectIntents, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	trace := []string{}
	port := &decompositionPortProbeV2{authorityPortProbeV2: authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationExactV2, State: requestedPlan.OperationState(), StateRecordRevision: 19},
		mission:    MissionObservationV2{State: command.Successor, RecordRevision: 19, LastEventID: missionEventIDV2(requestedPlan)},
	}, trace: &trace}
	provider := &decompositionProviderProbeV2{trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2)}
	epoch := &epochProbeV2{epoch: "different-after-restart"}
	service, _ := NewDecompositionAuthorityServiceV2(port, epoch, provider, journalv2.DefaultEventRegistryV2())
	result, err := service.Execute(t.Context(), request)
	if err != nil || result.State.Digest() != request.CompletedState.Digest() {
		t.Fatalf("restart result=%+v err=%v", result, err)
	}
	if epoch.calls.Load() != 0 || len(port.publishes) != 2 {
		t.Fatalf("restart re-admitted epoch or request: epoch=%d publications=%d", epoch.calls.Load(), len(port.publishes))
	}
	if provider.mutation.EffectID != effects[0].effectID || provider.mutation.ChildOperationID != effects[0].mutation.ChildOperationID {
		t.Fatalf("restart identity drift: got=%+v want=%+v", provider.mutation, effects[0].mutation)
	}
}

func TestDecompositionLostProviderResponseReobservesWithoutDuplicateMutationV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	trace := []string{}
	port := &decompositionPortProbeV2{authorityPortProbeV2: authorityPortProbeV2{
		resolution: OperationResolutionV2{Kind: OperationAbsentV2},
		mission:    MissionObservationV2{State: request.Requested.Expected, RecordRevision: 8, LastEventID: "prior-event"},
	}, trace: &trace}
	provider := &decompositionProviderProbeV2{
		trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2),
		mutateResponseErr: errors.New("provider response lost"),
	}
	service, _ := NewDecompositionAuthorityServiceV2(port, &epochProbeV2{epoch: request.Requested.OriginatingRuntimeEpochID}, provider, journalv2.DefaultEventRegistryV2())
	result, err := service.Execute(t.Context(), request)
	if err != nil || result.State.Digest() != request.CompletedState.Digest() {
		t.Fatalf("lost-response recovery=%+v err=%v", result, err)
	}
	mutations := 0
	observations := 0
	for _, call := range trace {
		if call == "mutate" {
			mutations++
		}
		if call == "observe" {
			observations++
		}
	}
	if mutations != 1 || observations != 2 {
		t.Fatalf("response loss duplicated or skipped re-observation: trace=%v", trace)
	}
}

func TestDecompositionCompensationRequiresUnchangedRevisionValueAndMarkerV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	trace := []string{}
	provider := &decompositionProviderProbeV2{trace: &trace, observations: make(map[journalv2.EffectID]DecompositionProviderObservationV2)}
	service, _ := NewDecompositionAuthorityServiceV2(&decompositionPortProbeV2{trace: &trace}, &epochProbeV2{}, provider, journalv2.DefaultEventRegistryV2())
	plan := request.Effects[0]
	effectID, childID := DeriveDecompositionEffectIdentityV2(request.Requested.OperationID, plan.Kind, plan.WorkID)
	digest, _ := journalv2.ReducerStateDigestV2(plan.CanonicalDefinition)
	recorded := DecompositionProviderObservationV2{EffectID: effectID, WorkID: plan.WorkID, ExternalRef: "external-1", ProviderRevision: 1, ValueDigest: digest, OperationMarker: childID}
	provider.observations[effectID] = recorded
	if err := service.Compensate(t.Context(), request.Requested.OperationID, plan, recorded); err != nil || provider.compensations != 1 {
		t.Fatalf("safe compensation err=%v calls=%d", err, provider.compensations)
	}
	changed := recorded
	changed.ProviderRevision++
	provider.observations[effectID] = changed
	if err := service.Compensate(t.Context(), request.Requested.OperationID, plan, recorded); !IsMissionAuthorityKindV2(err, MissionAuthorityAmbiguousV2) || provider.compensations != 1 {
		t.Fatalf("human change was compensated: err=%v calls=%d", err, provider.compensations)
	}
}

func TestDecompositionProposalPermutationPreservesBindingAndChildIdentitiesV2(t *testing.T) {
	t.Parallel()
	request := fixtureDecompositionRequestV2(t)
	secondID, _ := DeriveDecompositionEffectIdentityV2(request.Requested.OperationID, DecompositionEffectUpdateV2, "work-2")
	input := request.Requested.Successor.input
	input.EffectIDs = []journalv2.EffectID{request.Requested.Successor.EffectIDs()[0], secondID}
	sort.Slice(input.EffectIDs, func(i, j int) bool { return input.EffectIDs[i] < input.EffectIDs[j] })
	request.Requested.Successor, _ = NewMissionAuthorityStateV2(input)
	progressInput := request.ProgressedState.input
	progressInput.EffectIDs = append([]journalv2.EffectID(nil), input.EffectIDs...)
	request.ProgressedState, _ = NewMissionAuthorityStateV2(progressInput)
	completeInput := request.CompletedState.input
	completeInput.EffectIDs = append([]journalv2.EffectID(nil), input.EffectIDs...)
	request.CompletedState, _ = NewMissionAuthorityStateV2(completeInput)
	request.Effects = append(request.Effects, DecompositionEffectPlanV2{WorkID: "work-2", Kind: DecompositionEffectUpdateV2, CanonicalDefinition: []byte(`{"title":"work two"}`), ExpectedProviderRevision: 4})

	left, leftEffects, err := prepareDecompositionRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Effects[0], request.Effects[1] = request.Effects[1], request.Effects[0]
	right, rightEffects, err := prepareDecompositionRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	leftBinding, leftDigest, _ := validateMissionAuthorityCommandV2(left)
	rightBinding, rightDigest, _ := validateMissionAuthorityCommandV2(right)
	if leftDigest != rightDigest || leftBinding.IntentDigest() != rightBinding.IntentDigest() || len(leftEffects) != len(rightEffects) {
		t.Fatalf("permutation changed request identity")
	}
	for index := range leftEffects {
		if leftEffects[index].effectID != rightEffects[index].effectID || leftEffects[index].mutation.ChildOperationID != rightEffects[index].mutation.ChildOperationID {
			t.Fatalf("permutation changed child identity at %d", index)
		}
	}
}

func fixtureDecompositionRequestV2(t *testing.T) DecompositionAuthorityRequestV2 {
	t.Helper()
	prior := fixtureAuthorityStateV2(t, 1, AuthorityMissionActiveV2, "plan_approved")
	effectID, _ := DeriveDecompositionEffectIdentityV2("operation-1", DecompositionEffectCreateV2, "work-1")
	requested := authorityStateFromV2(t, prior, 2, DecompositionRequestedV2, []journalv2.EffectID{effectID})
	progressed := authorityStateFromV2(t, requested, 3, DecompositionProgressV2, []journalv2.EffectID{effectID})
	completed := authorityStateFromV2(t, progressed, 4, DecompositionCompletedV2, []journalv2.EffectID{effectID})
	command := fixtureAuthorityCommandV2(t, MissionEventDecompositionRequestedV2, prior, requested)
	return DecompositionAuthorityRequestV2{Requested: command, ProgressedState: progressed, CompletedState: completed, Effects: []DecompositionEffectPlanV2{{WorkID: "work-1", Kind: DecompositionEffectCreateV2, CanonicalDefinition: []byte(`{"title":"work one"}`)}}}
}

func authorityStateFromV2(t *testing.T, prior MissionAuthorityStateV2, version uint64, phase DecompositionPhaseV2, effects []journalv2.EffectID) MissionAuthorityStateV2 {
	t.Helper()
	input := prior.input
	input.Version = journalv2.LogicalVersion(version)
	input.Decomposition = phase
	input.ActiveBlockerIDs = prior.ActiveBlockerIDs()
	input.OpenDecisionIDs = prior.OpenDecisionIDs()
	input.EffectIDs = append([]journalv2.EffectID(nil), effects...)
	state, err := NewMissionAuthorityStateV2(input)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
