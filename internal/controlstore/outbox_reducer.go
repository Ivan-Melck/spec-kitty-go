package controlstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type outboxEventV2 struct {
	SchemaVersion uint16                `json:"schema_version"`
	Action        OutboxActionV2        `json:"action"`
	PriorDigest   string                `json:"prior_digest,omitempty"`
	State         OutboxDeliveryStateV2 `json:"state"`
}

func ReduceOutboxEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "outbox" || event.EventSchemaVersion() != OutboxEventSchemaV2 || event.ReducerVersion() != OutboxReducerV2 || event.Coordinate().Kind != "outbox" {
		return nil, outboxError("unsupported_event_tuple")
	}
	var change outboxEventV2
	if strictOutboxJSON(event.Payload(), &change) != nil || change.SchemaVersion != 1 || change.State.Validate() != nil || actionStageV2(change.Action) != change.State.Stage {
		return nil, outboxError("invalid_event")
	}
	next := change.State
	coordinate := outboxCoordinateV2(next)
	if coordinate != event.Coordinate() || journalv2.LogicalVersion(next.LogicalVersion) != event.LogicalVersion() ||
		event.EventType() != "outbox."+string(change.Action) || event.ActorID() != next.ActorID || event.CommittedRuntimeEpochID() != next.RuntimeEpochID {
		return nil, outboxError("event_authority_mismatch")
	}
	initial := len(prior) == 0 || bytes.Equal(prior, []byte(`{}`))
	if initial {
		if change.Action != OutboxStartAttemptV2 || next.LogicalVersion != 1 || change.PriorDigest != "" {
			return nil, outboxError("invalid_initial_transition")
		}
		return next.CanonicalBytes(), nil
	}
	old, err := DecodeOutboxDeliveryStateV2(prior)
	if err != nil || old.LogicalVersion+1 != next.LogicalVersion || !sameOutboxIdentityV2(old, next) || !legalOutboxTransitionV2(old.Stage, change.Action) {
		return nil, outboxError("illegal_transition")
	}
	digest, _ := journalv2.ReducerStateDigestV2(old.CanonicalBytes())
	if change.PriorDigest != digest.String() {
		return nil, outboxError("prior_digest_mismatch")
	}
	if change.Action == OutboxPermitRetryV2 && !outboxRetrySafeV2(*old.Observation, next.Observation) {
		return nil, outboxError("unsafe_retry")
	}
	if change.Action == OutboxPermitCompensationV2 && !outboxAttributionExactV2(*old.Observation, next.Observation, outboxIntentCoordinatesFromStateV2(next)) {
		return nil, outboxError("unsafe_compensation")
	}
	return next.CanonicalBytes(), nil
}

func outboxIntentCoordinatesFromStateV2(state OutboxDeliveryStateV2) OutboxIntentCoordinatesV2 {
	return OutboxIntentCoordinatesV2{
		EffectID: state.EffectID, SourceOperationID: state.SourceOperationID,
		IntendedMutationDigest: state.IntendedMutationDigest, IntendedValue: state.IntendedValue,
		ExpectedProviderRevision: state.ExpectedProviderRevision, ExpectedValue: state.ExpectedValue,
		ExpectedOperationMarker: state.ExpectedOperationMarker, AttributionMarker: state.AttributionMarker,
		ActorID: state.ActorID, Capability: state.Capability,
	}
}

func OutboxReducerRegistrationsV2() []journalv2.ReducerRegistration {
	sum := sha256.Sum256([]byte("spec-kitty/outbox-delivery-evidence-reducer/1/attribution-safe"))
	digest, _ := journalv2.ParseDigest("outbox_reducer", hex.EncodeToString(sum[:]))
	return []journalv2.ReducerRegistration{{Family: "outbox", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/controlstore", ImplementationDigest: digest, Reduce: ReduceOutboxEventV2}}
}

func actionStageV2(action OutboxActionV2) OutboxStageV2 {
	switch action {
	case OutboxStartAttemptV2:
		return OutboxAttemptStartedV2
	case OutboxRecordObservationV2:
		return OutboxObservationRecordedV2
	case OutboxRequireReconcileV2:
		return OutboxReconciliationRequiredV2
	case OutboxPermitRetryV2:
		return OutboxRetryPermittedV2
	case OutboxPermitCompensationV2:
		return OutboxCompensationPermittedV2
	case OutboxResolveV2:
		return OutboxResolvedV2
	case OutboxRefuseV2:
		return OutboxRefusedV2
	default:
		return ""
	}
}

func legalOutboxTransitionV2(prior OutboxStageV2, action OutboxActionV2) bool {
	switch action {
	case OutboxRecordObservationV2:
		return prior == OutboxAttemptStartedV2 || prior == OutboxRetryPermittedV2 || prior == OutboxCompensationPermittedV2
	case OutboxRequireReconcileV2:
		return prior == OutboxObservationRecordedV2
	case OutboxPermitRetryV2, OutboxPermitCompensationV2, OutboxResolveV2, OutboxRefuseV2:
		return prior == OutboxReconciliationRequiredV2
	default:
		return false
	}
}

func sameOutboxIdentityV2(a, b OutboxDeliveryStateV2) bool {
	return a.EffectID == b.EffectID && a.SourceOperationID == b.SourceOperationID && a.SourcePublicationID == b.SourcePublicationID &&
		a.SourceEffectCoordinate == b.SourceEffectCoordinate && a.SourceAuthorityRecordRevision == b.SourceAuthorityRecordRevision && a.SourceAuthorityLogicalVersion == b.SourceAuthorityLogicalVersion && a.SourceAuthorityValueDigest == b.SourceAuthorityValueDigest &&
		a.IntentDigestSHA256 == b.IntentDigestSHA256 && a.IntendedMutationDigest == b.IntendedMutationDigest && a.IntendedValue == b.IntendedValue &&
		a.ExpectedProviderRevision == b.ExpectedProviderRevision && a.ExpectedValue == b.ExpectedValue && a.ExpectedOperationMarker == b.ExpectedOperationMarker && a.AttributionMarker == b.AttributionMarker &&
		a.ActorID == b.ActorID && a.Capability == b.Capability && a.AttemptID == b.AttemptID
}
