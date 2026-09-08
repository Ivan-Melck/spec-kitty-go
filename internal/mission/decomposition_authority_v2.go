package mission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type DecompositionEffectKindV2 string

const (
	DecompositionEffectCreateV2 DecompositionEffectKindV2 = "create"
	DecompositionEffectUpdateV2 DecompositionEffectKindV2 = "update"
)

type DecompositionEffectPlanV2 struct {
	WorkID                   journalv2.WorkID
	Kind                     DecompositionEffectKindV2
	CanonicalDefinition      []byte
	ExpectedProviderRevision uint64
	ExpectedOperationMarker  string
}

type DecompositionProviderMutationV2 struct {
	EffectID            journalv2.EffectID
	ChildOperationID    string
	WorkID              journalv2.WorkID
	Kind                DecompositionEffectKindV2
	CanonicalDefinition []byte
	ExpectedRevision    uint64
	ExpectedMarker      string
}

type DecompositionProviderObservationV2 struct {
	EffectID         journalv2.EffectID `json:"effect_id"`
	WorkID           journalv2.WorkID   `json:"work_id"`
	ExternalRef      string             `json:"external_ref"`
	ProviderRevision uint64             `json:"provider_revision"`
	ValueDigest      journalv2.Digest   `json:"value_digest"`
	OperationMarker  string             `json:"operation_marker"`
}

// DecompositionProviderMutationOutcomeV2 classifies the authority consequence
// of a provider mutation without exposing provider-specific errors to the
// mission service. A provider must explicitly distinguish a lost/indeterminate
// response from a definitive refusal or version conflict.
type DecompositionProviderMutationOutcomeV2 string

const (
	DecompositionProviderMutationAppliedV2       DecompositionProviderMutationOutcomeV2 = "applied"
	DecompositionProviderMutationIndeterminateV2 DecompositionProviderMutationOutcomeV2 = "indeterminate"
	DecompositionProviderMutationConflictV2      DecompositionProviderMutationOutcomeV2 = "conflict"
	DecompositionProviderMutationRefusedV2       DecompositionProviderMutationOutcomeV2 = "refused"
)

func (o DecompositionProviderMutationOutcomeV2) permitsExactReobservation(err error) bool {
	switch o {
	case DecompositionProviderMutationAppliedV2:
		return err == nil
	case DecompositionProviderMutationIndeterminateV2:
		return true
	case DecompositionProviderMutationConflictV2, DecompositionProviderMutationRefusedV2:
		return false
	default:
		return false
	}
}

type DecompositionProviderV2 interface {
	RequireCapabilities(context.Context, []DecompositionEffectKindV2) error
	ObserveEffect(context.Context, journalv2.EffectID) (DecompositionProviderObservationV2, bool, error)
	MutateEffect(context.Context, DecompositionProviderMutationV2) (DecompositionProviderMutationOutcomeV2, error)
	CompensateEffect(context.Context, DecompositionProviderMutationV2, DecompositionProviderObservationV2) error
}

type DecompositionAuthorityRequestV2 struct {
	Requested       MissionAuthorityCommandV2
	ProgressedState MissionAuthorityStateV2
	CompletedState  MissionAuthorityStateV2
	Effects         []DecompositionEffectPlanV2
}

type DecompositionAuthorityResultV2 struct {
	State        MissionAuthorityStateV2
	Observations []DecompositionProviderObservationV2
	Replayed     bool
}

type DecompositionAuthorityServiceV2 struct {
	port     MissionAuthorityPortV2
	epoch    RuntimeEpochSourceV2
	provider DecompositionProviderV2
	registry journalv2.EventRegistryV2
}

func NewDecompositionAuthorityServiceV2(port MissionAuthorityPortV2, epoch RuntimeEpochSourceV2, provider DecompositionProviderV2, registry journalv2.EventRegistryV2) (*DecompositionAuthorityServiceV2, error) {
	if port == nil || epoch == nil || provider == nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_service", nil)
	}
	if _, err := registry.Resolve("mission", 1, 1); err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "registry", err)
	}
	return &DecompositionAuthorityServiceV2{port: port, epoch: epoch, provider: provider, registry: registry}, nil
}

// Compensate performs the mandatory compare-before-compensate observation at
// the last possible point. A changed revision, value, or operation marker is
// a human/provider change and is never blindly undone.
func (s *DecompositionAuthorityServiceV2) Compensate(ctx context.Context, parent journalv2.OperationID, plan DecompositionEffectPlanV2, recorded DecompositionProviderObservationV2) error {
	effectID, childID := DeriveDecompositionEffectIdentityV2(parent, plan.Kind, plan.WorkID)
	canonical, err := journalv2.CanonicalObject(plan.CanonicalDefinition)
	if err != nil || !bytes.Equal(canonical, plan.CanonicalDefinition) {
		return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "compensation_plan", err)
	}
	digest, _ := journalv2.ReducerStateDigestV2(canonical)
	prepared := preparedDecompositionEffectV2{effectID: effectID, valueDigest: digest, mutation: DecompositionProviderMutationV2{
		EffectID: effectID, ChildOperationID: childID, WorkID: plan.WorkID, Kind: plan.Kind, CanonicalDefinition: canonical,
		ExpectedRevision: plan.ExpectedProviderRevision, ExpectedMarker: plan.ExpectedOperationMarker,
	}}
	current, found, err := s.provider.ObserveEffect(ctx, effectID)
	if err != nil || !found || current != recorded || !prepared.matches(current) {
		return missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "compensation_attribution", err)
	}
	if err := s.provider.CompensateEffect(ctx, prepared.mutation, current); err != nil {
		return missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "compensation", err)
	}
	return nil
}

func (s *DecompositionAuthorityServiceV2) Execute(ctx context.Context, request DecompositionAuthorityRequestV2) (DecompositionAuthorityResultV2, error) {
	command, effects, err := prepareDecompositionRequestV2(request)
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}
	binding, _, err := validateMissionAuthorityCommandV2(command)
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveOperation(ctx, binding)
	if err != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "operation_resolution", err)
	}
	if resolution.Kind == OperationConflictV2 {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityIdempotencyConflictV2, "operation_id", nil)
	}
	if resolution.Kind == OperationExactV2 && (resolution.State.Status() == journalv2.StatusResolved || resolution.State.Status() == journalv2.StatusRefused) {
		result, decodeErr := decodeDecompositionResultV2(resolution.State.Result(), true)
		if decodeErr != nil {
			return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_result", decodeErr)
		}
		return result, nil
	}
	if resolution.Kind == OperationExactV2 && (resolution.State.Status() == journalv2.StatusAmbiguous || resolution.State.Status() == journalv2.StatusReconciliationRequired) {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "reconciliation_required", nil)
	}
	initiallyAbsent := resolution.Kind == OperationAbsentV2
	var operation journalv2.OperationStateV2
	var operationRevision uint64
	if resolution.Kind == OperationAbsentV2 {
		currentEpoch, epochErr := s.epoch.CurrentRuntimeEpoch(ctx)
		if epochErr != nil || currentEpoch != command.OriginatingRuntimeEpochID {
			return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityStaleEpochV2, "runtime_epoch", epochErr)
		}
	}
	missionObservation, err := s.port.ObserveMission(ctx, command.ProjectID, command.MissionID)
	if err != nil || resolution.Kind == OperationAbsentV2 && missionObservation.State.Digest() != command.Expected.Digest() {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "expected_mission", err)
	}
	if err := s.provider.RequireCapabilities(ctx, effectKindsV2(effects)); err != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "provider_capabilities", err)
	}
	if resolution.Kind == OperationAbsentV2 {
		requestedPlan, planErr := buildMissionAuthorityStagePlanV2(command, &binding, nil, 0, missionObservation, journalv2.StatusInProgress, "", nil, command.EffectIntents, s.registry)
		if planErr != nil {
			return DecompositionAuthorityResultV2{}, planErr
		}
		publication, publishErr := s.port.PublishMission(ctx, requestedPlan)
		if publishErr != nil || publication.validate(requestedPlan) != nil {
			return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "requested_publication", publishErr)
		}
		operation, operationRevision = requestedPlan.OperationState(), publication.ProviderRevision
		missionObservation = MissionObservationV2{State: command.Successor, RecordRevision: publication.ProviderRevision, LastEventID: missionEventIDV2(requestedPlan)}
	} else if resolution.Kind == OperationExactV2 {
		operation, operationRevision = resolution.State, resolution.StateRecordRevision
		if operation.Status() != journalv2.StatusInProgress || !equalEffectSetV2(operation.EffectIDs(), command.Successor.EffectIDs()) {
			return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_operation", nil)
		}
		if missionObservation.State.Decomposition() != DecompositionRequestedV2 && missionObservation.State.Decomposition() != DecompositionProgressV2 {
			return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_state", nil)
		}
	} else {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "operation_resolution", nil)
	}

	observations := make([]DecompositionProviderObservationV2, 0, len(effects))
	for _, effect := range effects {
		observation, found, observeErr := s.provider.ObserveEffect(ctx, effect.effectID)
		if observeErr != nil {
			return s.requireDecompositionReconciliationV2(ctx, command, operation, operationRevision, missionObservation, request, observations, observeErr)
		}
		if found && initiallyAbsent {
			return s.requireDecompositionReconciliationV2(ctx, command, operation, operationRevision, missionObservation, request, observations, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "preexisting_provider_state", nil))
		}
		if !found {
			mutationOutcome, mutateErr := s.provider.MutateEffect(ctx, effect.mutation)
			observation, found, observeErr = s.provider.ObserveEffect(ctx, effect.effectID)
			if !mutationOutcome.permitsExactReobservation(mutateErr) || observeErr != nil || !found || !effect.matches(observation) {
				cause := observeErr
				if cause == nil {
					cause = mutateErr
				}
				return s.requireDecompositionReconciliationV2(ctx, command, operation, operationRevision, missionObservation, request, observations, cause)
			}
		}
		if observeErr != nil || !found || !effect.matches(observation) {
			return s.requireDecompositionReconciliationV2(ctx, command, operation, operationRevision, missionObservation, request, observations, observeErr)
		}
		observations = append(observations, observation)
	}
	observationBytes, err := encodeDecompositionObservationsV2(observations)
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}

	progressed := command
	progressed.EventType = MissionEventDecompositionProgressedV2
	progressed.Expected = missionObservation.State
	progressed.Successor = request.ProgressedState
	progressed.EffectIntents = nil
	progressed.ObservationEvidence = observationBytes
	progressPlan, err := buildMissionAuthorityStagePlanV2(progressed, nil, &operation, operationRevision, missionObservation, journalv2.StatusInProgress, "", nil, nil, s.registry)
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}
	progressPublication, err := s.port.PublishMission(ctx, progressPlan)
	if err != nil || progressPublication.validate(progressPlan) != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "progress_publication", err)
	}
	operation = progressPlan.OperationState()
	missionObservation = MissionObservationV2{State: request.ProgressedState, RecordRevision: progressPublication.ProviderRevision, LastEventID: missionEventIDV2(progressPlan)}

	finalResultBytes, err := encodeDecompositionResultV2(DecompositionAuthorityResultV2{State: request.CompletedState, Observations: observations})
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}
	completed := command
	completed.EventType = MissionEventDecompositionCompletedV2
	completed.Expected = request.ProgressedState
	completed.Successor = request.CompletedState
	completed.EffectIntents = nil
	completed.ObservationEvidence = observationBytes
	completePlan, err := buildMissionAuthorityStagePlanV2(completed, nil, &operation, progressPublication.ProviderRevision, missionObservation, journalv2.StatusResolved, "mission.decomposition.result", finalResultBytes, nil, s.registry)
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}
	finalPublication, err := s.port.PublishMission(ctx, completePlan)
	if err != nil || finalPublication.validate(completePlan) != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "completion_publication", err)
	}
	return DecompositionAuthorityResultV2{State: request.CompletedState, Observations: append([]DecompositionProviderObservationV2(nil), observations...)}, nil
}

type preparedDecompositionEffectV2 struct {
	effectID    journalv2.EffectID
	mutation    DecompositionProviderMutationV2
	valueDigest journalv2.Digest
}

func prepareDecompositionRequestV2(request DecompositionAuthorityRequestV2) (MissionAuthorityCommandV2, []preparedDecompositionEffectV2, error) {
	command := request.Requested
	if command.EventType != MissionEventDecompositionRequestedV2 || len(request.Effects) == 0 || len(request.Effects) > journalv2.MaxEffectsPerPublication {
		return MissionAuthorityCommandV2{}, nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_request", nil)
	}
	plans := append([]DecompositionEffectPlanV2(nil), request.Effects...)
	sort.Slice(plans, func(i, j int) bool { return plans[i].WorkID < plans[j].WorkID })
	prepared := make([]preparedDecompositionEffectV2, len(plans))
	command.EffectIntents = make([]MissionEffectIntentV2, len(plans))
	for index, plan := range plans {
		if _, err := journalv2.NewWorkID(string(plan.WorkID)); err != nil || index > 0 && plan.WorkID == plans[index-1].WorkID || (plan.Kind != DecompositionEffectCreateV2 && plan.Kind != DecompositionEffectUpdateV2) || plan.ExpectedProviderRevision == ^uint64(0) || plan.ExpectedOperationMarker != "" && !boundedAuthorityIdentityV2(plan.ExpectedOperationMarker) {
			return MissionAuthorityCommandV2{}, nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_effect", err)
		}
		canonical, err := journalv2.CanonicalObject(plan.CanonicalDefinition)
		if err != nil || !bytes.Equal(canonical, plan.CanonicalDefinition) {
			return MissionAuthorityCommandV2{}, nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_definition", err)
		}
		valueDigest, _ := journalv2.ReducerStateDigestV2(canonical)
		effectID, childID := DeriveDecompositionEffectIdentityV2(command.OperationID, plan.Kind, plan.WorkID)
		mutation := DecompositionProviderMutationV2{effectID, childID, plan.WorkID, plan.Kind, canonical, plan.ExpectedProviderRevision, plan.ExpectedOperationMarker}
		intentBytes, _ := encodeDecompositionEffectIntentV2(command, mutation, valueDigest)
		prepared[index] = preparedDecompositionEffectV2{effectID, mutation, valueDigest}
		command.EffectIntents[index] = MissionEffectIntentV2{EffectID: effectID, Canonical: intentBytes}
	}
	stateEffects := command.Successor.EffectIDs()
	wantEffects := make([]journalv2.EffectID, len(prepared))
	for index := range prepared {
		wantEffects[index] = prepared[index].effectID
	}
	sort.Slice(wantEffects, func(i, j int) bool { return wantEffects[i] < wantEffects[j] })
	if !equalEffectSetV2(stateEffects, wantEffects) || request.ProgressedState.Decomposition() != DecompositionProgressV2 || request.CompletedState.Decomposition() != DecompositionCompletedV2 || !equalEffectSetV2(request.ProgressedState.EffectIDs(), wantEffects) || !equalEffectSetV2(request.CompletedState.EffectIDs(), wantEffects) {
		return MissionAuthorityCommandV2{}, nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_states", nil)
	}
	return command, prepared, nil
}

func DeriveDecompositionEffectIdentityV2(parent journalv2.OperationID, kind DecompositionEffectKindV2, workID journalv2.WorkID) (journalv2.EffectID, string) {
	digest := sha256.Sum256([]byte("spec-kitty/decomposition-effect/v2\x00" + string(parent) + "\x00" + string(kind) + "\x00" + string(workID)))
	encoded := hex.EncodeToString(digest[:])
	return journalv2.EffectID("effect-" + encoded), "child-" + encoded
}

func encodeDecompositionEffectIntentV2(command MissionAuthorityCommandV2, mutation DecompositionProviderMutationV2, digest journalv2.Digest) ([]byte, error) {
	encoded, err := json.Marshal(struct {
		SchemaVersion     uint32                          `json:"schema_version"`
		MissionID         journalv2.MissionID             `json:"mission_id"`
		ParentOperationID journalv2.OperationID           `json:"parent_operation_id"`
		Mutation          DecompositionProviderMutationV2 `json:"mutation"`
		ValueSHA256       string                          `json:"value_sha256"`
	}{1, command.MissionID, command.OperationID, mutation, digest.String()})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func (e preparedDecompositionEffectV2) matches(observation DecompositionProviderObservationV2) bool {
	return observation.EffectID == e.effectID && observation.WorkID == e.mutation.WorkID && observation.ProviderRevision > e.mutation.ExpectedRevision && observation.ValueDigest == e.valueDigest && observation.OperationMarker == e.mutation.ChildOperationID && boundedAuthorityIdentityV2(observation.ExternalRef)
}

func (s *DecompositionAuthorityServiceV2) requireDecompositionReconciliationV2(ctx context.Context, base MissionAuthorityCommandV2, operation journalv2.OperationStateV2, operationRevision uint64, observed MissionObservationV2, request DecompositionAuthorityRequestV2, observations []DecompositionProviderObservationV2, cause error) (DecompositionAuthorityResultV2, error) {
	input := request.ProgressedState.input
	input.Version = observed.State.Version() + 1
	input.Decomposition = DecompositionReconciliationRequiredV2
	input.ObservedAt = base.ObservedAt
	input.LastOperationID = base.OperationID
	input.ActiveBlockerIDs = observed.State.ActiveBlockerIDs()
	input.OpenDecisionIDs = observed.State.OpenDecisionIDs()
	input.EffectIDs = observed.State.EffectIDs()
	reconciliation, err := NewMissionAuthorityStateV2(input)
	if err != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "reconciliation_state", cause)
	}
	command := base
	command.EventType = MissionEventDecompositionReconciliationV2
	command.Expected = observed.State
	command.Successor = reconciliation
	command.EffectIntents = nil
	command.ObservationEvidence, _ = encodeDecompositionObservationsV2(observations)
	plan, planErr := buildMissionAuthorityStagePlanV2(command, nil, &operation, operationRevision, observed, journalv2.StatusAmbiguous, "", nil, nil, s.registry)
	if planErr != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "reconciliation_plan", planErr)
	}
	publication, publishErr := s.port.PublishMission(ctx, plan)
	if publishErr != nil || publication.validate(plan) != nil {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "reconciliation_publication", publishErr)
	}
	return DecompositionAuthorityResultV2{State: reconciliation, Observations: append([]DecompositionProviderObservationV2(nil), observations...)}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "provider_attribution", cause)
}

func encodeDecompositionObservationsV2(observations []DecompositionProviderObservationV2) ([]byte, error) {
	if len(observations) > journalv2.MaxEffectsPerPublication {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_observations", nil)
	}
	values := append([]DecompositionProviderObservationV2(nil), observations...)
	sort.Slice(values, func(i, j int) bool { return values[i].EffectID < values[j].EffectID })
	for index, value := range values {
		if _, err := journalv2.NewEffectID(string(value.EffectID)); err != nil {
			return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_observations", err)
		}
		if _, err := journalv2.NewWorkID(string(value.WorkID)); err != nil {
			return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_observations", err)
		}
		if value.ProviderRevision == 0 || value.ValueDigest.IsZero() || !boundedAuthorityIdentityV2(value.ExternalRef) || !boundedAuthorityIdentityV2(value.OperationMarker) || index > 0 && value.EffectID == values[index-1].EffectID {
			return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "decomposition_observations", nil)
		}
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion uint32                               `json:"schema_version"`
		Observations  []DecompositionProviderObservationV2 `json:"observations"`
	}{1, values})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}
func encodeDecompositionResultV2(result DecompositionAuthorityResultV2) ([]byte, error) {
	observations, err := encodeDecompositionObservationsV2(result.Observations)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion uint32          `json:"schema_version"`
		State         json.RawMessage `json:"state"`
		Observations  json.RawMessage `json:"observations"`
	}{1, result.State.CanonicalBytes(), observations})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}
func decodeDecompositionResultV2(encoded []byte, replayed bool) (DecompositionAuthorityResultV2, error) {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_result", nil)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return DecompositionAuthorityResultV2{}, err
	}
	var wire struct {
		SchemaVersion uint32          `json:"schema_version"`
		State         json.RawMessage `json:"state"`
		Observations  json.RawMessage `json:"observations"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 {
		return DecompositionAuthorityResultV2{}, err
	}
	state, err := DecodeMissionAuthorityStateV2(wire.State)
	if err != nil {
		return DecompositionAuthorityResultV2{}, err
	}
	var observationWire struct {
		SchemaVersion uint32                               `json:"schema_version"`
		Observations  []DecompositionProviderObservationV2 `json:"observations"`
	}
	observationDecoder := json.NewDecoder(bytes.NewReader(wire.Observations))
	observationDecoder.DisallowUnknownFields()
	if err = observationDecoder.Decode(&observationWire); err != nil || observationWire.SchemaVersion != 1 {
		return DecompositionAuthorityResultV2{}, err
	}
	observations, err := encodeDecompositionObservationsV2(observationWire.Observations)
	if err != nil || !bytes.Equal(observations, wire.Observations) {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_observations", err)
	}
	effectIDs := state.EffectIDs()
	if len(effectIDs) != len(observationWire.Observations) {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_membership", nil)
	}
	for index := range effectIDs {
		if effectIDs[index] != observationWire.Observations[index].EffectID {
			return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_membership", nil)
		}
	}
	reencoded, err := encodeDecompositionResultV2(DecompositionAuthorityResultV2{State: state, Observations: observationWire.Observations})
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return DecompositionAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "decomposition_result", err)
	}
	return DecompositionAuthorityResultV2{State: state, Observations: append([]DecompositionProviderObservationV2(nil), observationWire.Observations...), Replayed: replayed}, nil
}
func effectKindsV2(effects []preparedDecompositionEffectV2) []DecompositionEffectKindV2 {
	result := make([]DecompositionEffectKindV2, len(effects))
	for i := range effects {
		result[i] = effects[i].mutation.Kind
	}
	return result
}
func equalEffectSetV2(a, b []journalv2.EffectID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func missionEventIDV2(plan journalv2.SealedCommitPlanV2) journalv2.EventID {
	for _, event := range plan.Events() {
		if event.Family() == "mission" {
			return event.EventID()
		}
	}
	return ""
}
