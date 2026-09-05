package controlstore

import (
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// OutboxCommandV2 records delivery evidence in its own idempotent operation.
// The source effect operation is referenced by DurableOutboxIntentV2 and is
// never advanced, resolved, or refused by this capability.
type OutboxCommandV2 struct {
	Action              OutboxActionV2
	Intent              DurableOutboxIntentV2
	Prior               *OutboxDeliveryStateV2
	PriorRecordRevision uint64
	AttemptID           string
	RuntimeEpochID      journalv2.RuntimeEpochID
	Observation         *OutboxProviderObservationV2
	PermissionMarker    string
	Resolution          string
	OperationEventID    journalv2.EventID
	OutboxEventID       journalv2.EventID
	operationBinding    journalv2.OperationBindingV2
}

func (c OutboxCommandV2) OperationBinding() journalv2.OperationBindingV2 { return c.operationBinding }

// OutboxOperationCoordinatesV2 identifies one idempotent evidence-recording
// command. It is never a provider-generated child identity and must differ
// from the product operation that requested the effect.
type OutboxOperationCoordinatesV2 struct {
	OperationID       journalv2.OperationID
	ProcessID         journalv2.ProcessID
	DoctrineRevision  string
	ProfileRevision   string
	PolicyRevision    string
	ObservedCreatedAt string
}

// BindOutboxCommandOperationV2 seals the exact command intent to a fresh
// operation binding. A timeout/restart retries this same binding; a changed
// action, observation, prior state, or attribution becomes a conflict.
func BindOutboxCommandOperationV2(command OutboxCommandV2, coordinates OutboxOperationCoordinatesV2) (OutboxCommandV2, error) {
	if command.Intent.IsZero() || coordinates.OperationID == "" || coordinates.OperationID == command.Intent.coordinates.SourceOperationID ||
		!boundedOutboxText(string(coordinates.ProcessID), journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(coordinates.DoctrineRevision, journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(coordinates.ProfileRevision, journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(coordinates.PolicyRevision, journalv2.MaxIdentityBytes) {
		return OutboxCommandV2{}, outboxError("invalid_operation_coordinates")
	}
	canonical, err := canonicalOutboxCommandIntentV2(command)
	if err != nil {
		return OutboxCommandV2{}, err
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	digest, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "outbox." + string(command.Action), CommandSchemaVersion: 1, Scope: scope, Command: canonical})
	if err != nil {
		return OutboxCommandV2{}, err
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: coordinates.OperationID, CommandKind: "outbox." + string(command.Action), CommandSchemaVersion: 1, CanonicalIntentDigest: digest, Scope: scope, ActorID: command.Intent.coordinates.ActorID, ProcessID: coordinates.ProcessID, DoctrineRevision: coordinates.DoctrineRevision, ProfileRevision: coordinates.ProfileRevision, PolicyRevision: coordinates.PolicyRevision, OriginatingRuntimeEpochID: command.RuntimeEpochID, ObservedCreatedAt: coordinates.ObservedCreatedAt})
	if err != nil {
		return OutboxCommandV2{}, err
	}
	command.operationBinding = binding
	return command, nil
}

func BuildOutboxAuthorityPlanV2(command OutboxCommandV2, registry journalv2.EventRegistryV2) (OutboxAuthorityPlanV2, error) {
	binding := command.operationBinding
	canonicalIntent, intentErr := canonicalOutboxCommandIntentV2(command)
	wantDigest, digestErr := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "outbox." + string(command.Action), CommandSchemaVersion: 1, Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, Command: canonicalIntent})
	if command.Intent.IsZero() || intentErr != nil || digestErr != nil || binding.OperationID() == "" || binding.OperationID() == command.Intent.coordinates.SourceOperationID ||
		binding.CommandKind() != "outbox."+string(command.Action) || binding.CommandSchemaVersion() != 1 || binding.IntentDigest() != wantDigest ||
		binding.ActorID() != command.Intent.coordinates.ActorID || binding.OriginatingEpoch() != command.RuntimeEpochID || binding.Scope() != (journalv2.Scope{Kind: journalv2.ScopeHost}) ||
		!boundedOutboxText(string(command.RuntimeEpochID), journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(string(command.OperationEventID), journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(string(command.OutboxEventID), journalv2.MaxIdentityBytes) || command.OperationEventID == command.OutboxEventID {
		return OutboxAuthorityPlanV2{}, outboxError("invalid_command")
	}
	next, err := nextOutboxStateV2(command)
	if err != nil {
		return OutboxAuthorityPlanV2{}, err
	}
	priorDigest := ""
	priorVersion := journalv2.LogicalVersion(0)
	if command.Prior != nil {
		priorDigestValue, digestErr := journalv2.ReducerStateDigestV2(command.Prior.CanonicalBytes())
		if digestErr != nil {
			return OutboxAuthorityPlanV2{}, outboxError("invalid_prior")
		}
		priorDigest = priorDigestValue.String()
		priorVersion = journalv2.LogicalVersion(command.Prior.LogicalVersion)
	} else if command.PriorRecordRevision != 0 {
		return OutboxAuthorityPlanV2{}, outboxError("invalid_prior_revision")
	}
	eventPayload, err := canonicalOutboxValue(outboxEventV2{SchemaVersion: 1, Action: command.Action, PriorDigest: priorDigest, State: next})
	if err != nil {
		return OutboxAuthorityPlanV2{}, err
	}
	events := []journalv2.EventID{command.OperationEventID, command.OutboxEventID}
	sort.Slice(events, func(i, j int) bool { return events[i] < events[j] })
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{
		OperationID: binding.OperationID(), LogicalVersion: 1, Status: journalv2.StatusResolved,
		ResultKind: "outbox.delivery_evidence", ResultSchemaVersion: 1, CanonicalResult: next.CanonicalBytes(), EventIDs: events,
	})
	if err != nil {
		return OutboxAuthorityPlanV2{}, err
	}
	operationPayload, err := journalv2.EncodeOperationStateEventPayloadV2(operation)
	if err != nil {
		return OutboxAuthorityPlanV2{}, err
	}
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operation.OperationID()), journalv2.Scope{Kind: journalv2.ScopeHost})
	outboxCoordinate := outboxCoordinateV2(next)
	governance := []string{binding.DoctrineRevision(), binding.ProfileRevision()}
	sort.Strings(governance)
	drafts := []journalv2.EventDraftV2{
		{EventID: command.OperationEventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: command.RuntimeEpochID, ActorID: binding.ActorID(), ProcessID: binding.ProcessID(), PolicyRevision: binding.PolicyRevision(), GovernanceRevisions: governance, Payload: operationPayload},
		{EventID: command.OutboxEventID, EventType: "outbox." + string(command.Action), Family: "outbox", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: outboxCoordinate, AggregateLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), CommittedRuntimeEpochID: command.RuntimeEpochID, ActorID: binding.ActorID(), ProcessID: binding.ProcessID(), PolicyRevision: binding.PolicyRevision(), GovernanceRevisions: governance, Payload: eventPayload},
	}
	operationDigest, _ := journalv2.ReducerStateDigestV2(operationPayload)
	outboxDigest, _ := journalv2.ReducerStateDigestV2(next.CanonicalBytes())
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, NextLogicalVersion: 1, EventID: command.OperationEventID, CommittedRuntimeEpochID: command.RuntimeEpochID, CanonicalStateDigest: operationDigest, AuthoritySummary: operationPayload},
		{Coordinate: outboxCoordinate, ExpectedLogicalVersion: priorVersion, NextLogicalVersion: journalv2.LogicalVersion(next.LogicalVersion), EventID: command.OutboxEventID, ExpectedRecordRevision: command.PriorRecordRevision, CommittedRuntimeEpochID: command.RuntimeEpochID, CanonicalStateDigest: outboxDigest, AuthoritySummary: next.CanonicalBytes()},
	}
	conditions := make([]journalv2.ProviderCondition, 0, len(heads))
	for _, head := range heads {
		condition, conditionErr := journalv2.ConditionForHeadTransitionV2(head)
		if conditionErr != nil {
			return OutboxAuthorityPlanV2{}, conditionErr
		}
		conditions = append(conditions, condition)
	}
	var assertions []journalv2.AuthorityAssertionV2
	if command.Prior == nil {
		assertions = []journalv2.AuthorityAssertionV2{command.Intent.sourceProof}
	}
	sealed, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: drafts, Heads: heads, Conditions: conditions, Assertions: assertions}, registry)
	if err != nil {
		return OutboxAuthorityPlanV2{}, err
	}
	return OutboxAuthorityPlanV2{sealed: sealed}, nil
}

func canonicalOutboxCommandIntentV2(command OutboxCommandV2) ([]byte, error) {
	if command.Intent.IsZero() {
		return nil, outboxError("missing_durable_intent")
	}
	var prior []byte
	if command.Prior != nil {
		prior = command.Prior.CanonicalBytes()
		if len(prior) == 0 {
			return nil, outboxError("invalid_prior")
		}
	}
	return canonicalOutboxValue(struct {
		Action              OutboxActionV2               `json:"action"`
		EffectID            journalv2.EffectID           `json:"effect_id"`
		SourceOperationID   journalv2.OperationID        `json:"source_operation_id"`
		SourcePublicationID journalv2.PublicationID      `json:"source_publication_id"`
		IntentDigest        string                       `json:"intent_digest_sha256"`
		SourceProofRevision uint64                       `json:"source_proof_revision"`
		SourceProofDigest   string                       `json:"source_proof_digest_sha256"`
		PriorState          []byte                       `json:"prior_state,omitempty"`
		PriorRecordRevision uint64                       `json:"prior_record_revision"`
		AttemptID           string                       `json:"attempt_id"`
		RuntimeEpochID      journalv2.RuntimeEpochID     `json:"runtime_epoch_id"`
		Observation         *OutboxProviderObservationV2 `json:"observation,omitempty"`
		PermissionMarker    string                       `json:"permission_marker,omitempty"`
		Resolution          string                       `json:"resolution,omitempty"`
		OperationEventID    journalv2.EventID            `json:"operation_event_id"`
		OutboxEventID       journalv2.EventID            `json:"outbox_event_id"`
	}{command.Action, command.Intent.coordinates.EffectID, command.Intent.coordinates.SourceOperationID, command.Intent.publication,
		command.Intent.intentDigest.String(), command.Intent.sourceProof.ObservedRevision(), command.Intent.sourceProof.ObservedValueDigest().String(), prior, command.PriorRecordRevision, command.AttemptID, command.RuntimeEpochID,
		cloneOutboxObservation(command.Observation), command.PermissionMarker, command.Resolution, command.OperationEventID, command.OutboxEventID})
}

func nextOutboxStateV2(command OutboxCommandV2) (OutboxDeliveryStateV2, error) {
	coordinates := command.Intent.coordinates
	next := OutboxDeliveryStateV2{SchemaVersion: 1, LogicalVersion: 1, Stage: actionStageV2(command.Action), EffectID: coordinates.EffectID,
		SourceOperationID: coordinates.SourceOperationID, SourcePublicationID: command.Intent.publication,
		SourceEffectCoordinate: command.Intent.sourceProof.Target().Coordinate, SourceAuthorityRecordRevision: command.Intent.sourceProof.ObservedRevision(), SourceAuthorityLogicalVersion: command.Intent.sourceProof.ExpectedLogicalVersion(), SourceAuthorityValueDigest: command.Intent.sourceProof.ObservedValueDigest().String(),
		IntentDigestSHA256:     command.Intent.intentDigest.String(),
		IntendedMutationDigest: coordinates.IntendedMutationDigest, IntendedValue: coordinates.IntendedValue, ExpectedProviderRevision: coordinates.ExpectedProviderRevision, ExpectedValue: coordinates.ExpectedValue, ExpectedOperationMarker: coordinates.ExpectedOperationMarker,
		AttributionMarker: coordinates.AttributionMarker, ActorID: coordinates.ActorID, Capability: coordinates.Capability, AttemptID: command.AttemptID, RuntimeEpochID: command.RuntimeEpochID}
	if command.Prior == nil {
		if command.Action != OutboxStartAttemptV2 || !boundedOutboxText(command.AttemptID, journalv2.MaxIdentityBytes) || command.Observation != nil || command.PermissionMarker != "" || command.Resolution != "" {
			return OutboxDeliveryStateV2{}, outboxError("invalid_initial_transition")
		}
	} else {
		if command.Prior.Validate() != nil || !sameOutboxIntentV2(*command.Prior, next) || !legalOutboxTransitionV2(command.Prior.Stage, command.Action) {
			return OutboxDeliveryStateV2{}, outboxError("illegal_transition")
		}
		next = *command.Prior
		next.LogicalVersion++
		next.Stage = actionStageV2(command.Action)
		next.RuntimeEpochID = command.RuntimeEpochID
		next.Observation = cloneOutboxObservation(command.Observation)
		next.PermissionMarker = command.PermissionMarker
		next.Resolution = command.Resolution
		if command.Action == OutboxPermitRetryV2 && !outboxRetrySafeV2(*command.Prior.Observation, command.Observation) {
			return OutboxDeliveryStateV2{}, outboxError("unsafe_retry")
		}
		if command.Action == OutboxPermitCompensationV2 && !outboxAttributionExactV2(*command.Prior.Observation, command.Observation, coordinates) {
			return OutboxDeliveryStateV2{}, outboxError("unsafe_compensation")
		}
	}
	if err := next.Validate(); err != nil {
		return OutboxDeliveryStateV2{}, err
	}
	return next, nil
}

func sameOutboxIntentV2(prior, next OutboxDeliveryStateV2) bool {
	return prior.EffectID == next.EffectID && prior.SourceOperationID == next.SourceOperationID && prior.SourcePublicationID == next.SourcePublicationID &&
		prior.SourceEffectCoordinate == next.SourceEffectCoordinate && prior.SourceAuthorityRecordRevision == next.SourceAuthorityRecordRevision && prior.SourceAuthorityLogicalVersion == next.SourceAuthorityLogicalVersion && prior.SourceAuthorityValueDigest == next.SourceAuthorityValueDigest &&
		prior.IntentDigestSHA256 == next.IntentDigestSHA256 && prior.IntendedMutationDigest == next.IntendedMutationDigest && prior.IntendedValue == next.IntendedValue && prior.ExpectedProviderRevision == next.ExpectedProviderRevision && prior.ExpectedValue == next.ExpectedValue && prior.ExpectedOperationMarker == next.ExpectedOperationMarker && prior.AttributionMarker == next.AttributionMarker && prior.ActorID == next.ActorID && prior.Capability == next.Capability && prior.AttemptID == next.AttemptID
}

func outboxAttributionExactV2(prior OutboxProviderObservationV2, current *OutboxProviderObservationV2, intent OutboxIntentCoordinatesV2) bool {
	if current == nil || prior.Validate() != nil || current.Validate() != nil || prior != *current {
		return false
	}
	return current.Outcome == "confirmed" && current.Value == intent.IntendedValue && current.AttributionMarker == intent.AttributionMarker
}

func outboxRetrySafeV2(prior OutboxProviderObservationV2, current *OutboxProviderObservationV2) bool {
	return current != nil && prior.Validate() == nil && current.Validate() == nil && prior == *current && current.Outcome == "absent"
}

func outboxCoordinateV2(state OutboxDeliveryStateV2) journalv2.AggregateCoordinate {
	coordinate, _ := journalv2.NewAggregateCoordinate("outbox", journalv2.AggregateID(state.EffectID), journalv2.Scope{Kind: journalv2.ScopeHost})
	return coordinate
}
