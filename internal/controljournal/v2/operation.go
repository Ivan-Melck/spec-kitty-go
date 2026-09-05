package v2

import (
	"bytes"
	"encoding/json"
)

type StableIntentInput struct {
	CommandKind          string
	CommandSchemaVersion uint32
	Scope                Scope
	Command              []byte
}

func CanonicalIntentDigest(input StableIntentInput) (Digest, error) {
	if err := validateCommand(input.CommandKind, input.CommandSchemaVersion); err != nil {
		return Digest{}, err
	}
	if err := input.Scope.Validate(); err != nil {
		return Digest{}, err
	}
	command, err := CanonicalObject(input.Command)
	if err != nil {
		return Digest{}, err
	}
	encoded, err := marshalCanonical(struct {
		CommandKind          string          `json:"command_kind"`
		CommandSchemaVersion uint32          `json:"command_schema_version"`
		Scope                Scope           `json:"scope"`
		Command              json.RawMessage `json:"command"`
	}{input.CommandKind, input.CommandSchemaVersion, input.Scope, command}, MaxCanonicalPayloadBytes)
	if err != nil {
		return Digest{}, err
	}
	return digest(canonicalIntentDomain, encoded), nil
}

type OperationBindingInput struct {
	OperationID               OperationID
	CommandKind               string
	CommandSchemaVersion      uint32
	CanonicalIntentDigest     Digest
	Scope                     Scope
	ActorID                   ActorID
	ProcessID                 ProcessID
	DoctrineRevision          string
	ProfileRevision           string
	PolicyRevision            string
	OriginatingRuntimeEpochID RuntimeEpochID
	ObservedCreatedAt         string
}

type OperationBindingV2 struct {
	operationID               OperationID
	commandKind               string
	commandSchemaVersion      uint32
	canonicalIntentDigest     Digest
	scope                     Scope
	actorID                   ActorID
	processID                 ProcessID
	doctrineRevision          string
	profileRevision           string
	policyRevision            string
	originatingRuntimeEpochID RuntimeEpochID
	observedCreatedAt         string
}

func NewOperationBindingV2(input OperationBindingInput) (OperationBindingV2, error) {
	if err := validateIdentity("operation_id", string(input.OperationID)); err != nil {
		return OperationBindingV2{}, err
	}
	if err := validateCommand(input.CommandKind, input.CommandSchemaVersion); err != nil {
		return OperationBindingV2{}, err
	}
	if input.CanonicalIntentDigest.IsZero() {
		return OperationBindingV2{}, invalid(ErrorInvalidDigest, "canonical_intent_sha256")
	}
	if err := input.Scope.Validate(); err != nil {
		return OperationBindingV2{}, err
	}
	for _, optional := range []struct{ field, value string }{
		{"actor_id", string(input.ActorID)}, {"process_id", string(input.ProcessID)},
		{"doctrine_revision", input.DoctrineRevision}, {"profile_revision", input.ProfileRevision}, {"policy_revision", input.PolicyRevision},
	} {
		if optional.value != "" && validateIdentity(optional.field, optional.value) != nil {
			return OperationBindingV2{}, invalid(ErrorInvalidIdentity, optional.field)
		}
	}
	if err := validateIdentity("originating_runtime_epoch_id", string(input.OriginatingRuntimeEpochID)); err != nil {
		return OperationBindingV2{}, err
	}
	if !canonicalUTCSecond(input.ObservedCreatedAt) {
		return OperationBindingV2{}, invalid(ErrorInvalidCanonicalData, "observed_created_at")
	}
	return OperationBindingV2{
		operationID: input.OperationID, commandKind: input.CommandKind, commandSchemaVersion: input.CommandSchemaVersion,
		canonicalIntentDigest: input.CanonicalIntentDigest, scope: input.Scope, actorID: input.ActorID, processID: input.ProcessID,
		doctrineRevision: input.DoctrineRevision, profileRevision: input.ProfileRevision, policyRevision: input.PolicyRevision,
		originatingRuntimeEpochID: input.OriginatingRuntimeEpochID, observedCreatedAt: input.ObservedCreatedAt,
	}, nil
}

func validateCommand(kind string, version uint32) error {
	if version == 0 || version > 65535 || len(kind) == 0 || len(kind) > MaxIdentityBytes || kind[0] < 'a' || kind[0] > 'z' {
		return invalid(ErrorInvalidVersion, "command")
	}
	for _, char := range []byte(kind) {
		if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' || char == '.' || char == '-') {
			return invalid(ErrorInvalidIdentity, "command_kind")
		}
	}
	return nil
}

func canonicalUTCSecond(value string) bool {
	if len(value) != 20 || value[4] != '-' || value[7] != '-' || value[10] != 'T' || value[13] != ':' || value[16] != ':' || value[19] != 'Z' {
		return false
	}
	for _, index := range [...]int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	digit := func(index int) int {
		return int(value[index] - '0')
	}
	number := func(a, b int) int { return digit(a)*10 + digit(b) }
	year := digit(0)*1000 + digit(1)*100 + digit(2)*10 + digit(3)
	month, day, hour, minute, second := number(5, 6), number(8, 9), number(11, 12), number(14, 15), number(17, 18)
	if year < 1 || month < 1 || month > 12 || hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return false
	}
	days := [...]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month == 2 && (year%400 == 0 || year%4 == 0 && year%100 != 0) {
		days[2] = 29
	}
	return day >= 1 && day <= days[month]
}

func (b OperationBindingV2) OperationID() OperationID         { return b.operationID }
func (b OperationBindingV2) SchemaVersion() uint32            { return SchemaVersion }
func (b OperationBindingV2) IntentDigest() Digest             { return b.canonicalIntentDigest }
func (b OperationBindingV2) OriginatingEpoch() RuntimeEpochID { return b.originatingRuntimeEpochID }
func (b OperationBindingV2) Scope() Scope                     { return b.scope }
func (b OperationBindingV2) CommandKind() string              { return b.commandKind }
func (b OperationBindingV2) CommandSchemaVersion() uint32     { return b.commandSchemaVersion }
func (b OperationBindingV2) ActorID() ActorID                 { return b.actorID }
func (b OperationBindingV2) ProcessID() ProcessID             { return b.processID }
func (b OperationBindingV2) DoctrineRevision() string         { return b.doctrineRevision }
func (b OperationBindingV2) ProfileRevision() string          { return b.profileRevision }
func (b OperationBindingV2) PolicyRevision() string           { return b.policyRevision }
func (b OperationBindingV2) ObservedCreatedAt() string        { return b.observedCreatedAt }

type operationBindingWire struct {
	SchemaVersion             uint32 `json:"schema_version"`
	OperationID               string `json:"operation_id"`
	CommandKind               string `json:"command_kind"`
	CommandSchemaVersion      uint32 `json:"command_schema_version"`
	CanonicalIntentSHA256     string `json:"canonical_intent_sha256"`
	Scope                     Scope  `json:"scope"`
	ActorID                   string `json:"actor_id,omitempty"`
	ProcessID                 string `json:"process_id,omitempty"`
	DoctrineRevision          string `json:"doctrine_revision,omitempty"`
	ProfileRevision           string `json:"profile_revision,omitempty"`
	PolicyRevision            string `json:"policy_revision,omitempty"`
	OriginatingRuntimeEpochID string `json:"originating_runtime_epoch_id"`
	ObservedCreatedAt         string `json:"observed_created_at"`
}

func (b OperationBindingV2) wire() operationBindingWire {
	return operationBindingWire{SchemaVersion, string(b.operationID), b.commandKind, b.commandSchemaVersion, b.canonicalIntentDigest.String(), b.scope,
		string(b.actorID), string(b.processID), b.doctrineRevision, b.profileRevision, b.policyRevision, string(b.originatingRuntimeEpochID), b.observedCreatedAt}
}

func EncodeOperationBindingV2(binding OperationBindingV2) ([]byte, error) {
	validated, err := NewOperationBindingV2(OperationBindingInput{
		OperationID: binding.operationID, CommandKind: binding.commandKind, CommandSchemaVersion: binding.commandSchemaVersion,
		CanonicalIntentDigest: binding.canonicalIntentDigest, Scope: binding.scope, ActorID: binding.actorID, ProcessID: binding.processID,
		DoctrineRevision: binding.doctrineRevision, ProfileRevision: binding.profileRevision, PolicyRevision: binding.policyRevision,
		OriginatingRuntimeEpochID: binding.originatingRuntimeEpochID, ObservedCreatedAt: binding.observedCreatedAt,
	})
	if err != nil {
		return nil, err
	}
	return marshalCanonicalObject(validated.wire(), MaxCanonicalPayloadBytes)
}

func DecodeOperationBindingV2(encoded []byte) (OperationBindingV2, error) {
	var wire operationBindingWire
	if err := decodeStrict(encoded, MaxCanonicalPayloadBytes, &wire); err != nil || wire.SchemaVersion != SchemaVersion {
		return OperationBindingV2{}, invalid(ErrorJournalCorrupt, "operation_binding")
	}
	digest, err := ParseDigest("canonical_intent_sha256", wire.CanonicalIntentSHA256)
	if err != nil {
		return OperationBindingV2{}, invalid(ErrorJournalCorrupt, "operation_binding")
	}
	result, err := NewOperationBindingV2(OperationBindingInput{OperationID(wire.OperationID), wire.CommandKind, wire.CommandSchemaVersion, digest, wire.Scope,
		ActorID(wire.ActorID), ProcessID(wire.ProcessID), wire.DoctrineRevision, wire.ProfileRevision, wire.PolicyRevision,
		RuntimeEpochID(wire.OriginatingRuntimeEpochID), wire.ObservedCreatedAt})
	if err != nil {
		return OperationBindingV2{}, invalid(ErrorJournalCorrupt, "operation_binding")
	}
	canonical, _ := EncodeOperationBindingV2(result)
	if !bytes.Equal(canonical, encoded) {
		return OperationBindingV2{}, invalid(ErrorJournalCorrupt, "operation_binding")
	}
	return result, nil
}

func OperationBindingKey(operationID OperationID) (string, error) {
	component, err := EncodeKeyComponent(string(operationID))
	if err != nil {
		return "", err
	}
	return "spk/v2/operations/" + component + "/binding", nil
}

type BindingResolution string

const (
	BindingAbsent    BindingResolution = "absent"
	BindingExact     BindingResolution = "exact"
	BindingConflict  BindingResolution = "conflict"
	BindingMalformed BindingResolution = "malformed_existing"
)

func ResolveOperationBinding(candidate OperationBindingV2, existing []byte, present bool) (BindingResolution, error) {
	want, err := EncodeOperationBindingV2(candidate)
	if err != nil {
		return "", err
	}
	if !present {
		return BindingAbsent, nil
	}
	stored, err := DecodeOperationBindingV2(existing)
	if err != nil {
		return BindingMalformed, err
	}
	got, _ := EncodeOperationBindingV2(stored)
	if bytes.Equal(got, want) {
		return BindingExact, nil
	}
	return BindingConflict, invalid(ErrorIdempotencyConflict, "operation_id")
}

type OperationStatus string

const (
	StatusAccepted               OperationStatus = "accepted"
	StatusInProgress             OperationStatus = "in_progress"
	StatusResolved               OperationStatus = "resolved"
	StatusRefused                OperationStatus = "refused"
	StatusAmbiguous              OperationStatus = "ambiguous"
	StatusReconciliationRequired OperationStatus = "reconciliation_required"
)

type OperationStateInput struct {
	OperationID         OperationID
	LogicalVersion      LogicalVersion
	Status              OperationStatus
	ResultKind          string
	ResultSchemaVersion uint32
	CanonicalResult     []byte
	EventIDs            []EventID
	EffectIDs           []EffectID
	LastPublicationID   PublicationID
}

type OperationStateV2 struct {
	operationID         OperationID
	logicalVersion      LogicalVersion
	status              OperationStatus
	resultKind          string
	resultSchemaVersion uint32
	canonicalResult     []byte
	resultDigest        Digest
	eventIDs            []EventID
	effectIDs           []EffectID
	lastPublicationID   PublicationID
}

func NewOperationStateV2(input OperationStateInput) (OperationStateV2, error) {
	if validateIdentity("operation_id", string(input.OperationID)) != nil || input.LogicalVersion == 0 || !validOperationStatus(input.Status) {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "operation_state")
	}
	events, err := canonicalIDs("event_ids", input.EventIDs, MaxMembers)
	if err != nil {
		return OperationStateV2{}, err
	}
	effects, err := canonicalIDs("effect_ids", input.EffectIDs, MaxMembers)
	if err != nil {
		return OperationStateV2{}, err
	}
	var result []byte
	var resultDigest Digest
	terminal := input.Status == StatusResolved || input.Status == StatusRefused
	if terminal {
		if validateIdentity("result_kind", input.ResultKind) != nil || input.ResultSchemaVersion == 0 {
			return OperationStateV2{}, invalid(ErrorInvalidTransition, "operation_result")
		}
		result, err = CanonicalObject(input.CanonicalResult)
		if err != nil {
			return OperationStateV2{}, err
		}
		resultDigest = digest("spec-kitty.operation-result.v2", result)
	} else if input.ResultKind != "" || input.ResultSchemaVersion != 0 || len(input.CanonicalResult) != 0 {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "operation_result")
	}
	if input.LastPublicationID != "" {
		if _, err := DecodePublicationID(string(input.LastPublicationID)); err != nil {
			return OperationStateV2{}, err
		}
	}
	return OperationStateV2{input.OperationID, input.LogicalVersion, input.Status, input.ResultKind, input.ResultSchemaVersion,
		append([]byte(nil), result...), resultDigest, events, effects, input.LastPublicationID}, nil
}

func validOperationStatus(value OperationStatus) bool {
	switch value {
	case StatusAccepted, StatusInProgress, StatusResolved, StatusRefused, StatusAmbiguous, StatusReconciliationRequired:
		return true
	default:
		return false
	}
}

func (s OperationStateV2) OperationID() OperationID         { return s.operationID }
func (s OperationStateV2) SchemaVersion() uint32            { return SchemaVersion }
func (s OperationStateV2) LogicalVersion() LogicalVersion   { return s.logicalVersion }
func (s OperationStateV2) Status() OperationStatus          { return s.status }
func (s OperationStateV2) ResultKind() string               { return s.resultKind }
func (s OperationStateV2) ResultSchemaVersion() uint32      { return s.resultSchemaVersion }
func (s OperationStateV2) Result() []byte                   { return append([]byte(nil), s.canonicalResult...) }
func (s OperationStateV2) ResultDigest() Digest             { return s.resultDigest }
func (s OperationStateV2) EventIDs() []EventID              { return append([]EventID(nil), s.eventIDs...) }
func (s OperationStateV2) EffectIDs() []EffectID            { return append([]EffectID(nil), s.effectIDs...) }
func (s OperationStateV2) LastPublicationID() PublicationID { return s.lastPublicationID }

type operationStateWire struct {
	CanonicalResult     []byte          `json:"canonical_result,omitempty"`
	EffectIDs           []string        `json:"effect_ids"`
	EventIDs            []string        `json:"event_ids"`
	LastPublicationID   string          `json:"last_publication_id,omitempty"`
	LogicalVersion      uint64          `json:"logical_version"`
	OperationID         string          `json:"operation_id"`
	ResultKind          string          `json:"result_kind,omitempty"`
	ResultSchemaVersion uint32          `json:"result_schema_version,omitempty"`
	ResultSHA256        string          `json:"result_sha256,omitempty"`
	SchemaVersion       uint32          `json:"schema_version"`
	Status              OperationStatus `json:"status"`
}

func EncodeOperationStateV2(state OperationStateV2) ([]byte, error) {
	if state.lastPublicationID == "" {
		return nil, invalid(ErrorInvalidDigest, "last_publication_id")
	}
	rebuilt, err := NewOperationStateV2(OperationStateInput{state.operationID, state.logicalVersion, state.status, state.resultKind,
		state.resultSchemaVersion, state.canonicalResult, state.eventIDs, state.effectIDs, state.lastPublicationID})
	if err != nil || rebuilt.resultDigest != state.resultDigest {
		return nil, invalid(ErrorJournalCorrupt, "operation_state")
	}
	events := make([]string, len(state.eventIDs))
	for index, value := range state.eventIDs {
		events[index] = string(value)
	}
	effects := make([]string, len(state.effectIDs))
	for index, value := range state.effectIDs {
		effects[index] = string(value)
	}
	resultDigest := ""
	if !state.resultDigest.IsZero() {
		resultDigest = state.resultDigest.String()
	}
	encoded, err := marshalCanonical(operationStateWire{
		CanonicalResult: append([]byte(nil), state.canonicalResult...), EffectIDs: effects, EventIDs: events,
		LastPublicationID: string(state.lastPublicationID), LogicalVersion: uint64(state.logicalVersion), OperationID: string(state.operationID),
		ResultKind: state.resultKind, ResultSchemaVersion: state.resultSchemaVersion, ResultSHA256: resultDigest,
		SchemaVersion: SchemaVersion, Status: state.status,
	}, MaxCanonicalPayloadBytes)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func DecodeOperationStateV2(encoded []byte) (OperationStateV2, error) {
	var wire operationStateWire
	if err := decodeStrict(encoded, MaxCanonicalPayloadBytes, &wire); err != nil || wire.SchemaVersion != SchemaVersion {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_state")
	}
	events := make([]EventID, len(wire.EventIDs))
	for index, value := range wire.EventIDs {
		events[index] = EventID(value)
	}
	effects := make([]EffectID, len(wire.EffectIDs))
	for index, value := range wire.EffectIDs {
		effects[index] = EffectID(value)
	}
	if _, err := DecodePublicationID(wire.LastPublicationID); err != nil {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_state")
	}
	state, err := NewOperationStateV2(OperationStateInput{OperationID(wire.OperationID), LogicalVersion(wire.LogicalVersion), wire.Status,
		wire.ResultKind, wire.ResultSchemaVersion, wire.CanonicalResult, events, effects, PublicationID(wire.LastPublicationID)})
	if err != nil || state.resultDigest.String() != wire.ResultSHA256 && !(state.resultDigest.IsZero() && wire.ResultSHA256 == "") {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_state")
	}
	canonical, _ := EncodeOperationStateV2(state)
	if !bytes.Equal(canonical, encoded) {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_state")
	}
	return state, nil
}

// EncodeOperationStateEventPayloadV2 returns the exact immutable operation
// event payload. Publication identity is excluded because the sealer derives
// it from the plan containing this payload.
func EncodeOperationStateEventPayloadV2(state OperationStateV2) ([]byte, error) {
	if state.logicalVersion == 0 || state.lastPublicationID != "" {
		return nil, invalid(ErrorInvalidTransition, "operation_event_payload")
	}
	encoded, err := marshalCanonical(operationStatePreimage(state, false), MaxCanonicalPayloadBytes)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), encoded...), nil
}

// ReconstructOperationStateV2 combines the operation transition preimage with
// the publication identity supplied by its validated final event envelope.
func ReconstructOperationStateV2(payload []byte, publicationID PublicationID) (OperationStateV2, error) {
	if _, err := DecodePublicationID(string(publicationID)); err != nil {
		return OperationStateV2{}, err
	}
	var wire operationStatePreimageWire
	if err := decodeStrict(payload, MaxCanonicalPayloadBytes, &wire); err != nil || wire.LastPublicationID != "" {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_event_payload")
	}
	resultDigest := Digest{}
	if wire.ResultSHA256 != "" {
		parsed, err := ParseDigest("result_sha256", wire.ResultSHA256)
		if err != nil {
			return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_event_payload")
		}
		resultDigest = parsed
	}
	state, err := NewOperationStateV2(OperationStateInput{
		OperationID: OperationID(wire.OperationID), LogicalVersion: LogicalVersion(wire.LogicalVersion), Status: wire.Status,
		ResultKind: wire.ResultKind, ResultSchemaVersion: wire.ResultSchemaVersion, CanonicalResult: wire.CanonicalResult,
		EventIDs: wire.EventIDs, EffectIDs: wire.EffectIDs, LastPublicationID: publicationID,
	})
	if err != nil || state.resultDigest != resultDigest {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_event_payload")
	}
	withoutPublication := state
	withoutPublication.lastPublicationID = ""
	canonical, _ := EncodeOperationStateEventPayloadV2(withoutPublication)
	if !bytes.Equal(canonical, payload) {
		return OperationStateV2{}, invalid(ErrorJournalCorrupt, "operation_event_payload")
	}
	return state, nil
}

type OperationTransition struct {
	ExpectedVersion     LogicalVersion
	NextVersion         LogicalVersion
	Status              OperationStatus
	ResultKind          string
	ResultSchemaVersion uint32
	CanonicalResult     []byte
	EventIDs            []EventID
	EffectIDs           []EffectID
	LastPublicationID   PublicationID
}

// EffectPermissionCoordinateV2 identifies the newly published permission
// event and the effect whose pre-existing operation membership authorizes a
// reconciliation resume. The journal deliberately does not interpret the
// capability-owned permission payload.
type EffectPermissionCoordinateV2 struct {
	eventID    EventID
	effectID   EffectID
	commitment Digest
}

func (p EffectPermissionCoordinateV2) EventID() EventID   { return p.eventID }
func (p EffectPermissionCoordinateV2) EffectID() EffectID { return p.effectID }
func (p EffectPermissionCoordinateV2) Commitment() Digest { return p.commitment }

func AdvanceOperationState(prior OperationStateV2, transition OperationTransition) (OperationStateV2, error) {
	if !allowedOperationTransition(prior.status, transition.Status) {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "operation_version_or_status")
	}
	return advanceOperationStateMembership(prior, transition)
}

// ResumeOperationStateWithEffectPermissionV2 admits only the otherwise
// forbidden reconciliation_required -> in_progress edge. The permission must
// be a fresh event in the next cumulative membership for an effect already
// bound to the operation before this transition.
func ResumeOperationStateWithEffectPermissionV2(prior OperationStateV2, transition OperationTransition, permission EffectPermissionCoordinateV2) (OperationStateV2, error) {
	if prior.status != StatusReconciliationRequired || transition.Status != StatusInProgress ||
		validateIdentity("permission_event_id", string(permission.eventID)) != nil || validateIdentity("permission_effect_id", string(permission.effectID)) != nil || permission.commitment.IsZero() ||
		containsID(prior.eventIDs, permission.eventID) || !containsID(transition.EventIDs, permission.eventID) || !containsID(prior.effectIDs, permission.effectID) {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "effect_permission_resume")
	}
	return advanceOperationStateMembership(prior, transition)
}

func advanceOperationStateMembership(prior OperationStateV2, transition OperationTransition) (OperationStateV2, error) {
	if prior.logicalVersion == 0 || transition.ExpectedVersion != prior.logicalVersion || transition.NextVersion != prior.logicalVersion+1 {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "operation_version_or_status")
	}
	if prior.status == StatusResolved || prior.status == StatusRefused {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "terminal_state")
	}
	if !containsAllIDs(prior.eventIDs, transition.EventIDs) || !containsAllIDs(prior.effectIDs, transition.EffectIDs) {
		return OperationStateV2{}, invalid(ErrorInvalidTransition, "operation_membership")
	}
	return NewOperationStateV2(OperationStateInput{prior.operationID, transition.NextVersion, transition.Status, transition.ResultKind,
		transition.ResultSchemaVersion, transition.CanonicalResult, transition.EventIDs, transition.EffectIDs, transition.LastPublicationID})
}

func containsID[T ~string](values []T, wanted T) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func allowedOperationTransition(from, to OperationStatus) bool {
	switch from {
	case StatusAccepted:
		return to == StatusInProgress || to == StatusResolved || to == StatusRefused || to == StatusAmbiguous
	case StatusInProgress:
		return to == StatusInProgress || to == StatusResolved || to == StatusRefused || to == StatusAmbiguous
	case StatusAmbiguous:
		return to == StatusReconciliationRequired
	case StatusReconciliationRequired:
		return to == StatusResolved || to == StatusRefused || to == StatusAmbiguous
	default:
		return false
	}
}

func containsAllIDs[T ~string](prior, next []T) bool {
	priorIndex, nextIndex := 0, 0
	for priorIndex < len(prior) && nextIndex < len(next) {
		switch {
		case next[nextIndex] < prior[priorIndex]:
			nextIndex++
		case next[nextIndex] == prior[priorIndex]:
			priorIndex++
			nextIndex++
		default:
			return false
		}
	}
	return priorIndex == len(prior)
}

type RetryDecision string

const (
	RetryReplay      RetryDecision = "replay"
	RetryResume      RetryDecision = "resume"
	RetryAdmitNew    RetryDecision = "admit_new"
	RetryRefuseEpoch RetryDecision = "refuse_epoch"
	RetryConflict    RetryDecision = "conflict"
	RetryCorrupt     RetryDecision = "corrupt"
)

type RetryResolution struct {
	Decision RetryDecision
	Result   []byte
}

// ResolveRetryV2 orders existing-operation resolution before current-epoch
// admission. It is pure, so an absent stale request cannot trigger capability
// reads or effects through this seam.
func ResolveRetryV2(candidate OperationBindingV2, existingBinding []byte, bindingPresent bool, existingState *OperationStateV2, currentEpoch RuntimeEpochID) (RetryResolution, error) {
	resolution, err := ResolveOperationBinding(candidate, existingBinding, bindingPresent)
	if resolution == BindingMalformed {
		return RetryResolution{Decision: RetryCorrupt}, err
	}
	if resolution == BindingConflict {
		return RetryResolution{Decision: RetryConflict}, err
	}
	if resolution == BindingExact {
		if existingState == nil || existingState.operationID != candidate.operationID {
			return RetryResolution{Decision: RetryCorrupt}, invalid(ErrorJournalCorrupt, "operation_state")
		}
		if existingState.status == StatusResolved || existingState.status == StatusRefused {
			return RetryResolution{Decision: RetryReplay, Result: existingState.Result()}, nil
		}
		return RetryResolution{Decision: RetryResume}, nil
	}
	if validateIdentity("current_runtime_epoch_id", string(currentEpoch)) != nil || candidate.originatingRuntimeEpochID != currentEpoch {
		return RetryResolution{Decision: RetryRefuseEpoch}, invalid(ErrorInvalidTransition, "runtime_epoch")
	}
	return RetryResolution{Decision: RetryAdmitNew}, nil
}
