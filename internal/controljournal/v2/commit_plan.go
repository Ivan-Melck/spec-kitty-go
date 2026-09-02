package v2

import (
	"bytes"
	"encoding/hex"
	"sort"
)

type EventDraftV2 struct {
	EventID                 EventID
	EventType               string
	Family                  string
	EventSchemaVersion      uint32
	ReducerVersion          uint32
	Coordinate              AggregateCoordinate
	AggregateLogicalVersion LogicalVersion
	CommittedRuntimeEpochID RuntimeEpochID
	ActorID                 ActorID
	ProcessID               ProcessID
	WorkRevisionID          WorkRevisionID
	PolicyRevision          string
	GovernanceRevisions     []string
	Observations            []byte
	Payload                 []byte
}

type HeadTransitionV2 struct {
	Coordinate             AggregateCoordinate `json:"coordinate"`
	ExpectedLogicalVersion LogicalVersion      `json:"expected_logical_version"`
	NextLogicalVersion     LogicalVersion      `json:"next_logical_version"`
	EventID                EventID             `json:"event_id"`
	ExpectedRecordRevision uint64              `json:"expected_record_revision,omitempty"`
	CanonicalStateDigest   Digest              `json:"canonical_state_digest"`
	Tombstone              bool                `json:"tombstone"`
}

type EffectIntentReference struct {
	EffectID EffectID `json:"effect_id"`
	Digest   Digest   `json:"digest"`
}

type EligibilityGuardTransition struct {
	GuardID string `json:"guard_id"`
	Digest  Digest `json:"digest"`
}

type CheckpointRecord struct {
	CheckpointID string
	Canonical    []byte
}

type ConditionKind string

const (
	ConditionAbsent         ConditionKind = "absent"
	ConditionRecordRevision ConditionKind = "record_revision"
)

type ProviderCondition struct {
	ConditionID      string        `json:"condition_id"`
	Kind             ConditionKind `json:"kind"`
	ObservedRevision uint64        `json:"observed_revision,omitempty"`
}

type CommitPlanInput struct {
	OperationBinding    *OperationBindingV2
	PriorOperationState *OperationStateV2
	OperationState      OperationStateV2
	Events              []EventDraftV2
	Heads               []HeadTransitionV2
	EffectIntents       []EffectIntentReference
	EligibilityGuards   []EligibilityGuardTransition
	Checkpoints         []CheckpointRecord
	Conditions          []ProviderCondition
}

type SealedCommitPlanV2 struct {
	publicationID  PublicationID
	digest         Digest
	binding        *OperationBindingV2
	operationState OperationStateV2
	events         []EventEnvelopeV2
	heads          []HeadTransitionV2
	effects        []EffectIntentReference
	eligibility    []EligibilityGuardTransition
	checkpoints    []CheckpointRecord
	conditions     []ProviderCondition
	canonical      []byte
}

func SealCommitPlanV2(input CommitPlanInput, registry EventRegistryV2) (SealedCommitPlanV2, error) {
	if err := preflightCommitPlan(input); err != nil {
		return SealedCommitPlanV2{}, err
	}
	if input.OperationState.logicalVersion == 0 || input.OperationState.lastPublicationID != "" {
		return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_transition")
	}
	if input.OperationState.logicalVersion == 1 && (input.OperationBinding == nil || input.PriorOperationState != nil) ||
		input.OperationState.logicalVersion > 1 && (input.OperationBinding != nil || input.PriorOperationState == nil) {
		return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_binding_create")
	}
	if input.OperationBinding != nil && input.OperationBinding.operationID != input.OperationState.operationID {
		return SealedCommitPlanV2{}, invalid(ErrorInvalidTransition, "operation_binding_membership")
	}
	for _, event := range input.Events {
		if _, err := registry.Resolve(event.Family, event.EventSchemaVersion, event.ReducerVersion); err != nil {
			return SealedCommitPlanV2{}, err
		}
		if err := validateEventAggregateAuthority(event.Family, event.Coordinate, input.OperationState.operationID, input.OperationState.logicalVersion, event.AggregateLogicalVersion); err != nil {
			return SealedCommitPlanV2{}, err
		}
	}

	drafts := cloneEventDrafts(input.Events)
	heads := append([]HeadTransitionV2(nil), input.Heads...)
	effects := append([]EffectIntentReference(nil), input.EffectIntents...)
	eligibility := append([]EligibilityGuardTransition(nil), input.EligibilityGuards...)
	checkpoints := cloneCheckpoints(input.Checkpoints)
	conditions := append([]ProviderCondition(nil), input.Conditions...)
	for index := range drafts {
		payload, err := CanonicalObject(drafts[index].Payload)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
		drafts[index].Payload = payload
		if len(drafts[index].Observations) == 0 {
			drafts[index].Observations = []byte("{}")
		} else {
			drafts[index].Observations, err = CanonicalObject(drafts[index].Observations)
			if err != nil {
				return SealedCommitPlanV2{}, err
			}
		}
		sort.Strings(drafts[index].GovernanceRevisions)
	}
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].Coordinate.canonical()+"/"+string(drafts[i].EventID) < drafts[j].Coordinate.canonical()+"/"+string(drafts[j].EventID)
	})
	sort.Slice(heads, func(i, j int) bool { return heads[i].Coordinate.canonical() < heads[j].Coordinate.canonical() })
	sort.Slice(effects, func(i, j int) bool { return effects[i].EffectID < effects[j].EffectID })
	sort.Slice(eligibility, func(i, j int) bool { return eligibility[i].GuardID < eligibility[j].GuardID })
	sort.Slice(checkpoints, func(i, j int) bool { return checkpoints[i].CheckpointID < checkpoints[j].CheckpointID })
	sort.Slice(conditions, func(i, j int) bool { return conditions[i].ConditionID < conditions[j].ConditionID })
	if err := validatePlanMembers(input.PriorOperationState, input.OperationState, drafts, heads, effects, eligibility, checkpoints, conditions); err != nil {
		return SealedCommitPlanV2{}, err
	}
	preimage, err := planPreimage(input.OperationBinding, input.PriorOperationState, input.OperationState, drafts, heads, effects, eligibility, checkpoints, conditions)
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	planDigest := digest("spec-kitty.commit-plan.v2", preimage)
	version, _ := input.OperationState.logicalVersion.Hex16()
	publicationDigest := digest(publicationDomain, []byte(string(input.OperationState.operationID)+"\x00"+version+"\x00"+planDigest.String()))
	publicationID := PublicationID(publicationDigest.String())

	state, err := NewOperationStateV2(OperationStateInput{input.OperationState.operationID, input.OperationState.logicalVersion, input.OperationState.status,
		input.OperationState.resultKind, input.OperationState.resultSchemaVersion, input.OperationState.canonicalResult,
		input.OperationState.eventIDs, input.OperationState.effectIDs, publicationID})
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	events := make([]EventEnvelopeV2, len(drafts))
	for index, draft := range drafts {
		events[index], err = NewEventEnvelopeV2(EventEnvelopeInput{
			EventID: draft.EventID, EventType: draft.EventType, Family: draft.Family, EventSchemaVersion: draft.EventSchemaVersion,
			ReducerVersion: draft.ReducerVersion, Coordinate: draft.Coordinate, AggregateLogicalVersion: draft.AggregateLogicalVersion,
			OperationID: state.operationID, OperationLogicalVersion: state.logicalVersion, PublicationID: publicationID,
			CommittedRuntimeEpochID: draft.CommittedRuntimeEpochID, EventIndex: uint16(index), EventCount: uint16(len(drafts)),
			ActorID: draft.ActorID, ProcessID: draft.ProcessID, WorkRevisionID: draft.WorkRevisionID, PolicyRevision: draft.PolicyRevision,
			GovernanceRevisions: draft.GovernanceRevisions, Observations: draft.Observations, Payload: draft.Payload,
		}, registry)
		if err != nil {
			return SealedCommitPlanV2{}, err
		}
	}
	if err := ValidatePublicationEventsV2(events); err != nil {
		return SealedCommitPlanV2{}, err
	}
	plan := SealedCommitPlanV2{publicationID, planDigest, cloneBinding(input.OperationBinding), state, events, heads, effects, eligibility, checkpoints, conditions, nil}
	plan.canonical, err = plan.finalBytes()
	if err != nil {
		return SealedCommitPlanV2{}, err
	}
	return plan, nil
}

func validatePlanMembers(prior *OperationStateV2, state OperationStateV2, events []EventDraftV2, heads []HeadTransitionV2, effects []EffectIntentReference, eligibility []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition) error {
	eventCoordinates := make(map[string]EventDraftV2, len(events))
	eventIDs := make(map[EventID]bool, len(events))
	operationEvents := 0
	for _, event := range events {
		if _, err := NewAggregateCoordinate(event.Coordinate.Kind, event.Coordinate.ID, event.Coordinate.Scope); err != nil || event.AggregateLogicalVersion == 0 || validateIdentity("event_id", string(event.EventID)) != nil {
			return invalid(ErrorInvalidTransition, "event")
		}
		coordinate := event.Coordinate.canonical()
		if eventCoordinates[coordinate].EventID != "" || eventIDs[event.EventID] {
			return invalid(ErrorInvalidTransition, "duplicate_event")
		}
		eventCoordinates[coordinate], eventIDs[event.EventID] = event, true
		if event.Coordinate.Kind == AggregateKind("operation") {
			operationEvents++
		}
		if err := validateEventAggregateAuthority(event.Family, event.Coordinate, state.operationID, state.logicalVersion, event.AggregateLogicalVersion); err != nil {
			return err
		}
	}
	if operationEvents != 1 {
		return invalid(ErrorInvalidTransition, "operation_event")
	}
	headCoordinates := make(map[string]bool, len(heads))
	for _, head := range heads {
		coordinate := head.Coordinate.canonical()
		event, ok := eventCoordinates[coordinate]
		if !ok || headCoordinates[coordinate] || head.EventID != event.EventID || head.NextLogicalVersion != event.AggregateLogicalVersion ||
			head.NextLogicalVersion == 0 || head.ExpectedLogicalVersion+1 != head.NextLogicalVersion || head.CanonicalStateDigest.IsZero() {
			return invalid(ErrorInvalidTransition, "event_head_membership")
		}
		headCoordinates[coordinate] = true
	}
	currentEventIDs := make([]EventID, 0, len(eventIDs))
	for eventID := range eventIDs {
		currentEventIDs = append(currentEventIDs, eventID)
	}
	sort.Slice(currentEventIDs, func(i, j int) bool { return currentEventIDs[i] < currentEventIDs[j] })
	currentEffectIDs := make([]EffectID, len(effects))
	for index, effect := range effects {
		currentEffectIDs[index] = effect.EffectID
	}
	if err := exactPublicationMembership(prior, state, currentEventIDs, currentEffectIDs); err != nil {
		return err
	}
	if err := uniqueEffects(effects); err != nil {
		return err
	}
	if err := uniqueNamedDigests(eligibility, checkpoints, conditions); err != nil {
		return err
	}
	return nil
}

func preflightCommitPlan(input CommitPlanInput) error {
	if len(input.Events) == 0 || len(input.Events) > MaxEventsPerPublication || len(input.Heads) != len(input.Events) ||
		len(input.EffectIntents) > MaxEffectsPerPublication || len(input.EligibilityGuards) > MaxMembers ||
		len(input.Checkpoints) > MaxCheckpointsPerPlan || len(input.Conditions) > MaxConditionsPerPlan {
		return invalid(ErrorBudgetExceeded, "commit_plan_members")
	}
	nestedBytes := 0
	add := func(field string, size, maximum int) error {
		if size > maximum {
			return invalid(ErrorBudgetExceeded, field)
		}
		if size > MaxCommitPlanBytes-nestedBytes {
			return invalid(ErrorBudgetExceeded, "commit_plan_bytes")
		}
		nestedBytes += size
		return nil
	}
	for _, event := range input.Events {
		if err := preflightGovernanceRevisions(event.GovernanceRevisions); err != nil {
			return err
		}
		for _, revision := range event.GovernanceRevisions {
			if err := add("governance_revisions", len(revision), MaxIdentityBytes); err != nil {
				return err
			}
		}
		if err := add("event_payload", len(event.Payload), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
		if err := add("event_observations", len(event.Observations), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	for _, checkpoint := range input.Checkpoints {
		if err := add("checkpoint_payload", len(checkpoint.Canonical), MaxCanonicalPayloadBytes); err != nil {
			return err
		}
	}
	return nil
}

func uniqueEffects(effects []EffectIntentReference) error {
	seen := make(map[EffectID]bool, len(effects))
	for _, effect := range effects {
		if validateIdentity("effect_id", string(effect.EffectID)) != nil || effect.Digest.IsZero() || seen[effect.EffectID] {
			return invalid(ErrorInvalidTransition, "effect_membership")
		}
		seen[effect.EffectID] = true
	}
	return nil
}

func exactPublicationMembership(prior *OperationStateV2, state OperationStateV2, events []EventID, effects []EffectID) error {
	if prior == nil {
		if !equalIDs(state.eventIDs, events) || !equalIDs(state.effectIDs, effects) {
			return invalid(ErrorInvalidTransition, "first_publication_membership")
		}
		return nil
	}
	if prior.operationID != state.operationID || prior.logicalVersion+1 != state.logicalVersion || !containsAllIDs(prior.eventIDs, state.eventIDs) || !containsAllIDs(prior.effectIDs, state.effectIDs) {
		return invalid(ErrorInvalidTransition, "prior_operation_membership")
	}
	rebuilt, err := AdvanceOperationState(*prior, OperationTransition{prior.logicalVersion, state.logicalVersion, state.status, state.resultKind,
		state.resultSchemaVersion, state.canonicalResult, state.eventIDs, state.effectIDs, state.lastPublicationID})
	rebuiltBytes, _ := marshalCanonical(operationStatePreimage(rebuilt, false), MaxCanonicalPayloadBytes)
	stateBytes, _ := marshalCanonical(operationStatePreimage(state, false), MaxCanonicalPayloadBytes)
	if err != nil || !bytes.Equal(rebuiltBytes, stateBytes) {
		return invalid(ErrorInvalidTransition, "prior_operation_transition")
	}
	if !equalIDs(addedIDs(prior.eventIDs, state.eventIDs), events) || !equalIDs(addedIDs(prior.effectIDs, state.effectIDs), effects) {
		return invalid(ErrorInvalidTransition, "later_publication_membership")
	}
	return nil
}

func addedIDs[T ~string](prior, current []T) []T {
	result := make([]T, 0, len(current))
	priorIndex := 0
	for _, value := range current {
		for priorIndex < len(prior) && prior[priorIndex] < value {
			priorIndex++
		}
		if priorIndex == len(prior) || prior[priorIndex] != value {
			result = append(result, value)
		}
	}
	return result
}

func equalIDs[T ~string](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func uniqueNamedDigests(guards []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition) error {
	seen := make(map[string]bool, len(guards)+len(checkpoints)+len(conditions))
	for _, guard := range guards {
		if validateIdentity("guard_id", guard.GuardID) != nil || guard.Digest.IsZero() || seen["guard:"+guard.GuardID] {
			return invalid(ErrorInvalidTransition, "eligibility_guards")
		}
		seen["guard:"+guard.GuardID] = true
	}
	for _, checkpoint := range checkpoints {
		if validateIdentity("checkpoint_id", checkpoint.CheckpointID) != nil || seen["checkpoint:"+checkpoint.CheckpointID] {
			return invalid(ErrorInvalidTransition, "checkpoints")
		}
		if canonical, err := CanonicalObject(checkpoint.Canonical); err != nil || !bytes.Equal(canonical, checkpoint.Canonical) {
			return invalid(ErrorInvalidCanonicalData, "checkpoint")
		}
		seen["checkpoint:"+checkpoint.CheckpointID] = true
	}
	for _, condition := range conditions {
		if validateIdentity("condition_id", condition.ConditionID) != nil || seen["condition:"+condition.ConditionID] ||
			condition.Kind != ConditionAbsent && condition.Kind != ConditionRecordRevision || condition.Kind == ConditionAbsent && condition.ObservedRevision != 0 || condition.Kind == ConditionRecordRevision && condition.ObservedRevision == 0 {
			return invalid(ErrorInvalidTransition, "conditions")
		}
		seen["condition:"+condition.ConditionID] = true
	}
	return nil
}

func cloneEventDrafts(values []EventDraftV2) []EventDraftV2 {
	result := append([]EventDraftV2(nil), values...)
	for index := range result {
		result[index].GovernanceRevisions = append([]string(nil), result[index].GovernanceRevisions...)
		result[index].Observations = append([]byte(nil), result[index].Observations...)
		result[index].Payload = append([]byte(nil), result[index].Payload...)
	}
	return result
}

func cloneCheckpoints(values []CheckpointRecord) []CheckpointRecord {
	result := append([]CheckpointRecord(nil), values...)
	for index := range result {
		result[index].Canonical = append([]byte(nil), result[index].Canonical...)
	}
	return result
}

func cloneBinding(value *OperationBindingV2) *OperationBindingV2 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func planPreimage(binding *OperationBindingV2, prior *OperationStateV2, state OperationStateV2, events []EventDraftV2, heads []HeadTransitionV2, effects []EffectIntentReference, eligibility []EligibilityGuardTransition, checkpoints []CheckpointRecord, conditions []ProviderCondition) ([]byte, error) {
	var bindingWire *operationBindingWire
	if binding != nil {
		value := binding.wire()
		bindingWire = &value
	}
	var priorWire *operationStatePreimageWire
	if prior != nil {
		value := operationStatePreimage(*prior, true)
		priorWire = &value
	}
	return marshalCanonical(struct {
		Binding     *operationBindingWire        `json:"operation_binding_create,omitempty"`
		PriorState  *operationStatePreimageWire  `json:"prior_operation_state,omitempty"`
		State       operationStatePreimageWire   `json:"operation_state"`
		Events      []EventDraftV2               `json:"events"`
		Heads       []HeadTransitionV2           `json:"heads"`
		Effects     []EffectIntentReference      `json:"effect_intents"`
		Eligibility []EligibilityGuardTransition `json:"eligibility_guards"`
		Checkpoints []CheckpointRecord           `json:"checkpoints"`
		Conditions  []ProviderCondition          `json:"conditions"`
	}{bindingWire, priorWire, operationStatePreimage(state, false), events, heads, effects, eligibility, checkpoints, conditions}, MaxCommitPlanBytes)
}

type operationStatePreimageWire struct {
	OperationID         string          `json:"operation_id"`
	LogicalVersion      uint64          `json:"logical_version"`
	Status              OperationStatus `json:"status"`
	ResultKind          string          `json:"result_kind,omitempty"`
	ResultSchemaVersion uint32          `json:"result_schema_version,omitempty"`
	CanonicalResult     []byte          `json:"canonical_result,omitempty"`
	ResultSHA256        string          `json:"result_sha256,omitempty"`
	EventIDs            []EventID       `json:"event_ids"`
	EffectIDs           []EffectID      `json:"effect_ids"`
	LastPublicationID   string          `json:"last_publication_id,omitempty"`
}

func operationStatePreimage(state OperationStateV2, includePublication bool) operationStatePreimageWire {
	resultDigest := ""
	if !state.resultDigest.IsZero() {
		resultDigest = state.resultDigest.String()
	}
	publicationID := ""
	if includePublication {
		publicationID = string(state.lastPublicationID)
	}
	return operationStatePreimageWire{string(state.operationID), uint64(state.logicalVersion), state.status, state.resultKind, state.resultSchemaVersion,
		append([]byte(nil), state.canonicalResult...), resultDigest, append([]EventID(nil), state.eventIDs...), append([]EffectID(nil), state.effectIDs...), publicationID}
}

func (p SealedCommitPlanV2) finalBytes() ([]byte, error) {
	var binding *operationBindingWire
	if p.binding != nil {
		value := p.binding.wire()
		binding = &value
	}
	state, err := EncodeOperationStateV2(p.operationState)
	if err != nil {
		return nil, err
	}
	events := make([][]byte, len(p.events))
	for index, event := range p.events {
		encoded, err := EncodeEventEnvelopeV2(event)
		if err != nil {
			return nil, err
		}
		events[index] = encoded
	}
	return marshalCanonical(struct {
		PublicationID  string                       `json:"publication_id"`
		PlanSHA256     string                       `json:"canonical_plan_sha256"`
		Binding        *operationBindingWire        `json:"operation_binding_create,omitempty"`
		OperationState []byte                       `json:"operation_state"`
		Events         [][]byte                     `json:"events"`
		Heads          []HeadTransitionV2           `json:"heads"`
		Effects        []EffectIntentReference      `json:"effect_intents"`
		Eligibility    []EligibilityGuardTransition `json:"eligibility_guards"`
		Checkpoints    []CheckpointRecord           `json:"checkpoints"`
		Conditions     []ProviderCondition          `json:"conditions"`
	}{string(p.publicationID), p.digest.String(), binding, state, events, p.heads, p.effects, p.eligibility, p.checkpoints, p.conditions}, MaxCommitPlanBytes)
}

func (p SealedCommitPlanV2) PublicationID() PublicationID     { return p.publicationID }
func (p SealedCommitPlanV2) Digest() Digest                   { return p.digest }
func (p SealedCommitPlanV2) CanonicalBytes() []byte           { return append([]byte(nil), p.canonical...) }
func (p SealedCommitPlanV2) OperationState() OperationStateV2 { return p.operationState }
func (p SealedCommitPlanV2) Events() []EventEnvelopeV2 {
	return append([]EventEnvelopeV2(nil), p.events...)
}
func (p SealedCommitPlanV2) Heads() []HeadTransitionV2 {
	return append([]HeadTransitionV2(nil), p.heads...)
}
func (p SealedCommitPlanV2) Conditions() []ProviderCondition {
	return append([]ProviderCondition(nil), p.conditions...)
}
func (p SealedCommitPlanV2) BindingCreate() *OperationBindingV2 { return cloneBinding(p.binding) }
func (p SealedCommitPlanV2) EffectIntents() []EffectIntentReference {
	return append([]EffectIntentReference(nil), p.effects...)
}
func (p SealedCommitPlanV2) EligibilityGuards() []EligibilityGuardTransition {
	return append([]EligibilityGuardTransition(nil), p.eligibility...)
}
func (p SealedCommitPlanV2) Checkpoints() []CheckpointRecord { return cloneCheckpoints(p.checkpoints) }

func DecodePublicationID(value string) (PublicationID, error) {
	if _, err := hex.DecodeString(value); err != nil || len(value) != 64 {
		return "", invalid(ErrorInvalidDigest, "publication_id")
	}
	for _, char := range []byte(value) {
		if char >= 'A' && char <= 'F' {
			return "", invalid(ErrorInvalidDigest, "publication_id")
		}
	}
	return PublicationID(value), nil
}
