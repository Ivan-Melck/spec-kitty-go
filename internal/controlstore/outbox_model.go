package controlstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const (
	OutboxEventSchemaV2 = uint32(1)
	OutboxReducerV2     = uint32(1)
	maxOutboxTextBytes  = 1024
)

// OutboxActionV2 describes delivery evidence, not product authority. Product
// state remains owned by the capability which requested the external effect.
type OutboxActionV2 string

const (
	OutboxStartAttemptV2       OutboxActionV2 = "start_attempt"
	OutboxRecordObservationV2  OutboxActionV2 = "record_observation"
	OutboxRequireReconcileV2   OutboxActionV2 = "require_reconciliation"
	OutboxPermitRetryV2        OutboxActionV2 = "permit_retry"
	OutboxPermitCompensationV2 OutboxActionV2 = "permit_compensation"
	OutboxResolveV2            OutboxActionV2 = "resolve"
	OutboxRefuseV2             OutboxActionV2 = "refuse"
)

type OutboxStageV2 string

const (
	OutboxAttemptStartedV2         OutboxStageV2 = "attempt_started"
	OutboxObservationRecordedV2    OutboxStageV2 = "observation_recorded"
	OutboxReconciliationRequiredV2 OutboxStageV2 = "reconciliation_required"
	OutboxRetryPermittedV2         OutboxStageV2 = "retry_permitted"
	OutboxCompensationPermittedV2  OutboxStageV2 = "compensation_permitted"
	OutboxResolvedV2               OutboxStageV2 = "resolved"
	OutboxRefusedV2                OutboxStageV2 = "refused"
)

// OutboxIntentCoordinatesV2 is the provider-neutral attribution needed for
// compare-before-compensate. CanonicalIntent must be the exact capability-owned
// intent already present in SourcePublication; callers cannot substitute a
// digest string for observed bytes.
type OutboxIntentCoordinatesV2 struct {
	EffectID                 journalv2.EffectID
	SourceOperationID        journalv2.OperationID
	CanonicalIntent          []byte
	IntendedMutationDigest   string
	IntendedValue            string
	ExpectedProviderRevision uint64
	ExpectedValue            string
	ExpectedOperationMarker  string
	AttributionMarker        string
	ActorID                  journalv2.ActorID
	Capability               string
}

// DurableOutboxIntentV2 is an opaque proof that delivery coordinates were
// bound to an effect intent in one sealed authority publication.
type DurableOutboxIntentV2 struct {
	coordinates  OutboxIntentCoordinatesV2
	publication  journalv2.PublicationID
	intentDigest journalv2.Digest
	sourceProof  journalv2.AuthorityAssertionV2
}

func BindDurableOutboxIntentV2(source journalv2.SealedCommitPlanV2, sourceRecordRevision uint64, input OutboxIntentCoordinatesV2) (DurableOutboxIntentV2, error) {
	if input.SourceOperationID == "" || source.OperationState().OperationID() != input.SourceOperationID ||
		!boundedOutboxText(input.IntendedMutationDigest, 64) || !lowerHexOutboxDigest(input.IntendedMutationDigest) ||
		input.ExpectedProviderRevision == 0 || !boundedOutboxOptionalText(input.IntendedValue, maxOutboxTextBytes) || !boundedOutboxOptionalText(input.ExpectedValue, maxOutboxTextBytes) ||
		!boundedOutboxOptionalText(input.ExpectedOperationMarker, journalv2.MaxIdentityBytes) ||
		!boundedOutboxOptionalText(input.AttributionMarker, journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(string(input.ActorID), journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(input.Capability, journalv2.MaxIdentityBytes) || sourceRecordRevision == 0 {
		return DurableOutboxIntentV2{}, outboxError("invalid_intent_coordinates")
	}
	canonical, err := journalv2.CanonicalObject(input.CanonicalIntent)
	if err != nil || !bytes.Equal(canonical, input.CanonicalIntent) {
		return DurableOutboxIntentV2{}, outboxError("invalid_intent_bytes")
	}
	if err := validateOutboxCoordinatesAgainstIntentV2(input, canonical); err != nil {
		return DurableOutboxIntentV2{}, err
	}
	digestBytes := sha256.Sum256(canonical)
	digest, err := journalv2.ParseDigest("effect_intent", hex.EncodeToString(digestBytes[:]))
	if err != nil {
		return DurableOutboxIntentV2{}, outboxError("invalid_intent_digest")
	}
	found := false
	for _, candidate := range source.EffectIntents() {
		if candidate.EffectID != input.EffectID {
			continue
		}
		if found || candidate.Digest != digest || !bytes.Equal(candidate.Canonical, canonical) {
			return DurableOutboxIntentV2{}, outboxError("intent_binding_mismatch")
		}
		found = true
	}
	binding := source.BindingCreate()
	if !found || source.PublicationID() == "" || binding == nil || binding.OperationID() != input.SourceOperationID ||
		binding.ActorID() != input.ActorID || binding.CommandKind() != input.Capability {
		return DurableOutboxIntentV2{}, outboxError("durable_intent_missing")
	}
	var sourceHead *journalv2.HeadTransitionV2
	for _, head := range source.Heads() {
		if head.Coordinate.Kind == "external_effect" && head.Coordinate.ID == journalv2.AggregateID(input.EffectID) {
			if sourceHead != nil {
				return DurableOutboxIntentV2{}, outboxError("source_authority_ambiguous")
			}
			copyHead := head
			sourceHead = &copyHead
		}
	}
	var sourceRecord []byte
	for _, record := range source.Records() {
		if sourceHead != nil && record.Role() == journalv2.RecordAggregateHead && record.Target().Coordinate() == sourceHead.Coordinate {
			if sourceRecord != nil {
				return DurableOutboxIntentV2{}, outboxError("source_authority_ambiguous")
			}
			sourceRecord = record.CanonicalValue()
		}
	}
	if sourceHead == nil || len(sourceRecord) == 0 {
		return DurableOutboxIntentV2{}, outboxError("source_authority_missing")
	}
	valueDigest, err := journalv2.ReducerStateDigestV2(sourceRecord)
	if err != nil {
		return DurableOutboxIntentV2{}, outboxError("source_authority_digest")
	}
	proof, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: sourceHead.Coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: sourceRecordRevision, ExpectedLogicalVersion: sourceHead.NextLogicalVersion, ObservedValueDigest: valueDigest})
	if err != nil {
		return DurableOutboxIntentV2{}, err
	}
	input.CanonicalIntent = bytes.Clone(canonical)
	return DurableOutboxIntentV2{coordinates: input, publication: source.PublicationID(), intentDigest: digest, sourceProof: proof}, nil
}

func (i DurableOutboxIntentV2) IsZero() bool {
	return i.publication == "" || i.intentDigest.IsZero() || i.sourceProof.ObservedRevision() == 0
}

// RecoverDurableOutboxIntentV2 reconstructs the opaque continuation
// capability from an exact durable outbox state. The next publication CASes
// the outbox head that carries the original source proof; it does not freeze
// or reassert a source effect head that may have legitimately advanced.
func RecoverDurableOutboxIntentV2(state OutboxDeliveryStateV2) (DurableOutboxIntentV2, error) {
	if state.Validate() != nil {
		return DurableOutboxIntentV2{}, outboxError("invalid_recovery_state")
	}
	intentDigest, err := journalv2.ParseDigest("effect_intent", state.IntentDigestSHA256)
	if err != nil {
		return DurableOutboxIntentV2{}, err
	}
	valueDigest, err := journalv2.ParseDigest("source_authority", state.SourceAuthorityValueDigest)
	if err != nil {
		return DurableOutboxIntentV2{}, err
	}
	proof, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: state.SourceEffectCoordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: state.SourceAuthorityRecordRevision, ExpectedLogicalVersion: state.SourceAuthorityLogicalVersion, ObservedValueDigest: valueDigest})
	if err != nil {
		return DurableOutboxIntentV2{}, err
	}
	coordinates := OutboxIntentCoordinatesV2{EffectID: state.EffectID, SourceOperationID: state.SourceOperationID, IntendedMutationDigest: state.IntendedMutationDigest, IntendedValue: state.IntendedValue, ExpectedProviderRevision: state.ExpectedProviderRevision, ExpectedValue: state.ExpectedValue, ExpectedOperationMarker: state.ExpectedOperationMarker, AttributionMarker: state.AttributionMarker, ActorID: state.ActorID, Capability: state.Capability}
	return DurableOutboxIntentV2{coordinates: coordinates, publication: state.SourcePublicationID, intentDigest: intentDigest, sourceProof: proof}, nil
}

func validateOutboxCoordinatesAgainstIntentV2(input OutboxIntentCoordinatesV2, canonical []byte) error {
	// External-effect intent is capability-owned and may add fields. Outbox
	// consumes only the stable attribution subset and refuses if any coordinate
	// presented by the caller differs from those exact observed bytes.
	var wire struct {
		EffectID                 journalv2.EffectID    `json:"effect_id"`
		OperationID              journalv2.OperationID `json:"operation_id"`
		ExpectedProviderRevision uint64                `json:"expected_provider_revision"`
		IntendedValue            string                `json:"intended_value"`
		ExpectedValue            string                `json:"expected_value"`
		ExpectedOperationMarker  string                `json:"expected_operation_marker"`
		CanonicalRequestDigest   string                `json:"canonical_request_digest"`
		ActorID                  string                `json:"actor_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if err := decoder.Decode(&wire); err != nil || wire.EffectID != input.EffectID || wire.OperationID != input.SourceOperationID ||
		wire.ExpectedProviderRevision != input.ExpectedProviderRevision || wire.IntendedValue != input.IntendedValue || wire.ExpectedValue != input.ExpectedValue ||
		wire.ExpectedOperationMarker != input.ExpectedOperationMarker || input.AttributionMarker != string(input.SourceOperationID) || wire.CanonicalRequestDigest != input.IntendedMutationDigest ||
		wire.ActorID != string(input.ActorID) {
		return outboxError("intent_attribution_mismatch")
	}
	return nil
}

type OutboxProviderObservationV2 struct {
	ProviderRevision  uint64 `json:"provider_revision"`
	Value             string `json:"value,omitempty"`
	AttributionMarker string `json:"attribution_marker,omitempty"`
	Outcome           string `json:"outcome"`
	DigestSHA256      string `json:"digest_sha256,omitempty"`
}

func NewOutboxProviderObservationV2(revision uint64, value, marker, outcome string) (OutboxProviderObservationV2, error) {
	observation := OutboxProviderObservationV2{ProviderRevision: revision, Value: value, AttributionMarker: marker, Outcome: outcome}
	if !observation.validShape() {
		return OutboxProviderObservationV2{}, outboxError("invalid_observation")
	}
	if revision != 0 {
		observation.DigestSHA256 = observation.expectedDigest()
	}
	return observation, nil
}

func (o OutboxProviderObservationV2) validShape() bool {
	if !boundedOutboxOptionalText(o.Value, maxOutboxTextBytes) || !boundedOutboxOptionalText(o.AttributionMarker, journalv2.MaxIdentityBytes) || !boundedOutboxText(o.Outcome, 64) {
		return false
	}
	switch o.Outcome {
	case "confirmed", "stale", "conflicting", "absent":
		return o.ProviderRevision > 0 && (o.Outcome != "absent" || o.Value == "" && o.AttributionMarker == "")
	case "unavailable", "malformed", "unknown":
		return o.ProviderRevision == 0 && o.Value == "" && o.AttributionMarker == "" && o.DigestSHA256 == ""
	default:
		return false
	}
}

func (o OutboxProviderObservationV2) Validate() error {
	if !o.validShape() || o.ProviderRevision != 0 && (!lowerHexOutboxDigest(o.DigestSHA256) || o.DigestSHA256 != o.expectedDigest()) {
		return outboxError("invalid_observation")
	}
	return nil
}

func (o OutboxProviderObservationV2) expectedDigest() string {
	raw, _ := json.Marshal(struct {
		Revision uint64 `json:"revision"`
		Value    string `json:"value"`
		Marker   string `json:"marker"`
		Outcome  string `json:"outcome"`
	}{o.ProviderRevision, o.Value, o.AttributionMarker, o.Outcome})
	canonical, _ := journalv2.CanonicalObject(raw)
	sum := sha256.Sum256(append([]byte("spec-kitty/outbox-observation/v2\x00"), canonical...))
	return hex.EncodeToString(sum[:])
}

// OutboxDeliveryStateV2 is durable evidence about delivery. It deliberately
// contains no mission/work/review/acceptance/integration status field.
type OutboxDeliveryStateV2 struct {
	SchemaVersion                 uint16                        `json:"schema_version"`
	LogicalVersion                uint64                        `json:"logical_version"`
	Stage                         OutboxStageV2                 `json:"stage"`
	EffectID                      journalv2.EffectID            `json:"effect_id"`
	SourceOperationID             journalv2.OperationID         `json:"source_operation_id"`
	SourcePublicationID           journalv2.PublicationID       `json:"source_publication_id"`
	SourceEffectCoordinate        journalv2.AggregateCoordinate `json:"source_effect_coordinate"`
	SourceAuthorityRecordRevision uint64                        `json:"source_authority_record_revision"`
	SourceAuthorityLogicalVersion journalv2.LogicalVersion      `json:"source_authority_logical_version"`
	SourceAuthorityValueDigest    string                        `json:"source_authority_value_digest_sha256"`
	IntentDigestSHA256            string                        `json:"intent_digest_sha256"`
	IntendedMutationDigest        string                        `json:"intended_mutation_digest_sha256"`
	IntendedValue                 string                        `json:"intended_value"`
	ExpectedProviderRevision      uint64                        `json:"expected_provider_revision"`
	ExpectedValue                 string                        `json:"expected_value"`
	ExpectedOperationMarker       string                        `json:"expected_operation_marker"`
	AttributionMarker             string                        `json:"attribution_marker"`
	ActorID                       journalv2.ActorID             `json:"actor_id"`
	Capability                    string                        `json:"capability"`
	AttemptID                     string                        `json:"attempt_id"`
	RuntimeEpochID                journalv2.RuntimeEpochID      `json:"runtime_epoch_id"`
	Observation                   *OutboxProviderObservationV2  `json:"observation,omitempty"`
	PermissionMarker              string                        `json:"permission_marker,omitempty"`
	Resolution                    string                        `json:"resolution,omitempty"`
}

func (s OutboxDeliveryStateV2) Validate() error {
	if s.SchemaVersion != 1 || s.LogicalVersion == 0 ||
		!boundedOutboxText(string(s.EffectID), journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(string(s.SourceOperationID), journalv2.MaxIdentityBytes) ||
		len(s.SourcePublicationID) != 64 || !lowerHexOutboxDigest(s.IntentDigestSHA256) ||
		s.SourceEffectCoordinate.Kind != "external_effect" || s.SourceEffectCoordinate.ID != journalv2.AggregateID(s.EffectID) || s.SourceEffectCoordinate.Scope.Validate() != nil ||
		s.SourceAuthorityRecordRevision == 0 || s.SourceAuthorityLogicalVersion == 0 || !lowerHexOutboxDigest(s.SourceAuthorityValueDigest) ||
		!lowerHexOutboxDigest(s.IntendedMutationDigest) || s.ExpectedProviderRevision == 0 ||
		!boundedOutboxOptionalText(s.IntendedValue, maxOutboxTextBytes) || !boundedOutboxOptionalText(s.ExpectedValue, maxOutboxTextBytes) || !boundedOutboxOptionalText(s.ExpectedOperationMarker, journalv2.MaxIdentityBytes) ||
		!boundedOutboxOptionalText(s.AttributionMarker, journalv2.MaxIdentityBytes) || s.AttributionMarker != string(s.SourceOperationID) ||
		!boundedOutboxText(string(s.ActorID), journalv2.MaxIdentityBytes) || !boundedOutboxText(s.Capability, journalv2.MaxIdentityBytes) ||
		!boundedOutboxText(s.AttemptID, journalv2.MaxIdentityBytes) || !boundedOutboxText(string(s.RuntimeEpochID), journalv2.MaxIdentityBytes) {
		return outboxError("invalid_state")
	}
	if _, err := journalv2.DecodePublicationID(string(s.SourcePublicationID)); err != nil {
		return outboxError("invalid_source_publication")
	}
	if s.Observation != nil && s.Observation.Validate() != nil {
		return outboxError("invalid_observation")
	}
	switch s.Stage {
	case OutboxAttemptStartedV2:
		if s.LogicalVersion != 1 || s.Observation != nil || s.PermissionMarker != "" || s.Resolution != "" {
			return outboxError("invalid_stage")
		}
	case OutboxObservationRecordedV2, OutboxReconciliationRequiredV2:
		if s.Observation == nil || s.PermissionMarker != "" || s.Resolution != "" {
			return outboxError("invalid_stage")
		}
	case OutboxRetryPermittedV2, OutboxCompensationPermittedV2:
		if s.Observation == nil || s.PermissionMarker != string(s.SourceOperationID) || s.Resolution != "" {
			return outboxError("invalid_stage")
		}
	case OutboxResolvedV2, OutboxRefusedV2:
		if s.Observation == nil || !boundedOutboxText(s.Resolution, 64) || s.PermissionMarker != "" {
			return outboxError("invalid_stage")
		}
	default:
		return outboxError("invalid_stage")
	}
	return nil
}

func (s OutboxDeliveryStateV2) CanonicalBytes() []byte {
	if s.Validate() != nil {
		return nil
	}
	raw, _ := json.Marshal(s)
	canonical, _ := journalv2.CanonicalObject(raw)
	return canonical
}

func DecodeOutboxDeliveryStateV2(encoded []byte) (OutboxDeliveryStateV2, error) {
	var state OutboxDeliveryStateV2
	if strictOutboxJSON(encoded, &state) != nil || state.Validate() != nil || !bytes.Equal(encoded, state.CanonicalBytes()) {
		return OutboxDeliveryStateV2{}, outboxError("invalid_state")
	}
	if state.Observation != nil {
		copyObservation := *state.Observation
		state.Observation = &copyObservation
	}
	return state, nil
}

func cloneOutboxObservation(value *OutboxProviderObservationV2) *OutboxProviderObservationV2 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func canonicalOutboxValue(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, outboxError("canonical_value")
	}
	return journalv2.CanonicalObject(raw)
}

func strictOutboxJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing outbox JSON")
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return fmt.Errorf("non-canonical outbox JSON")
	}
	return nil
}

func boundedOutboxText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value)
}
func boundedOutboxOptionalText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value)
}
func lowerHexOutboxDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

type OutboxErrorV2 struct{ Kind string }

func (e *OutboxErrorV2) Error() string { return "OUTBOX_REFUSED: " + e.Kind }
func outboxError(kind string) error    { return &OutboxErrorV2{Kind: kind} }

// CanonicalOutboxStatesV2 returns defensive, stable ordering for query and
// checkpoint composition without exposing shared mutable maps.
func CanonicalOutboxStatesV2(values []OutboxDeliveryStateV2) ([]OutboxDeliveryStateV2, error) {
	result := append([]OutboxDeliveryStateV2(nil), values...)
	for _, value := range result {
		if value.Validate() != nil {
			return nil, outboxError("invalid_state_set")
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EffectID < result[j].EffectID })
	for i := 1; i < len(result); i++ {
		if result[i-1].EffectID == result[i].EffectID {
			return nil, outboxError("duplicate_effect")
		}
	}
	return result, nil
}
