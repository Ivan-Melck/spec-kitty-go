package mission

import (
	"bytes"
	"encoding/json"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const ReadinessAuthorityStateSchemaV2 = uint32(2)

type ScopedMissionFactKindV2 string

const (
	ScopedFactBlockerV2  ScopedMissionFactKindV2 = "blocker"
	ScopedFactDecisionV2 ScopedMissionFactKindV2 = "decision"
	ScopedFactConflictV2 ScopedMissionFactKindV2 = "conflict"
)

func (k ScopedMissionFactKindV2) validate() bool {
	return k == ScopedFactBlockerV2 || k == ScopedFactDecisionV2 || k == ScopedFactConflictV2
}

type ScopedMissionFactV2 struct {
	Kind             ScopedMissionFactKindV2 `json:"kind"`
	FactID           string                  `json:"fact_id"`
	Scope            ActionRef               `json:"scope"`
	EvidenceIdentity string                  `json:"evidence_identity"`
	EvidenceDigest   string                  `json:"evidence_sha256"`
}

func (f ScopedMissionFactV2) validate() bool {
	return f.Kind.validate() && boundedAuthorityIdentityV2(f.FactID) && f.Scope.Validate() == nil &&
		boundedAuthorityIdentityV2(f.EvidenceIdentity) && lowerHexDigest(f.EvidenceDigest)
}

type ReadinessAuthorityStateInputV2 struct {
	ProjectID           journalv2.ProjectID
	MissionID           journalv2.MissionID
	DefinitionRevision  string
	DefinitionDigest    journalv2.Digest
	Version             journalv2.LogicalVersion
	Lifecycle           MissionLifecycle
	CurrentWorkRevision WorkRevisionAuthorityV2
	ActivityCount       uint64
	ActiveFacts         []ScopedMissionFactV2
	LastOperationID     journalv2.OperationID
	ObservedAt          string
}

type ReadinessAuthorityStateV2 struct {
	input          ReadinessAuthorityStateInputV2
	facts          []ScopedMissionFactV2
	canonical      []byte
	digest         journalv2.Digest
	authorityFacts journalv2.Digest
}

type readinessAuthorityStateWireV2 struct {
	SchemaVersion       uint32                `json:"schema_version"`
	ProjectID           journalv2.ProjectID   `json:"project_id"`
	MissionID           journalv2.MissionID   `json:"mission_id"`
	DefinitionRevision  string                `json:"definition_revision"`
	DefinitionSHA256    string                `json:"definition_sha256"`
	Version             uint64                `json:"version"`
	Lifecycle           MissionLifecycle      `json:"lifecycle"`
	CurrentWorkRevision json.RawMessage       `json:"current_work_revision,omitempty"`
	ActivityCount       uint64                `json:"activity_count"`
	ActiveFacts         []ScopedMissionFactV2 `json:"active_facts"`
	LastOperationID     journalv2.OperationID `json:"last_operation_id"`
	ObservedAt          string                `json:"observed_at"`
}

func NewReadinessAuthorityStateV2(input ReadinessAuthorityStateInputV2) (ReadinessAuthorityStateV2, error) {
	return newReadinessAuthorityStateV2(input, nil)
}

func newReadinessAuthorityStateV2(input ReadinessAuthorityStateInputV2, trustedCanonical []byte) (ReadinessAuthorityStateV2, error) {
	if _, err := journalv2.NewProjectID(string(input.ProjectID)); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "project_id", err)
	}
	if _, err := journalv2.NewMissionID(string(input.MissionID)); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_id", err)
	}
	if !boundedAuthorityIdentityV2(input.DefinitionRevision) || input.DefinitionDigest.IsZero() || input.Version == 0 || input.Lifecycle.Validate() != nil || !canonicalAuthorityTimeV2(input.ObservedAt) {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", nil)
	}
	if _, err := journalv2.NewOperationID(string(input.LastOperationID)); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "last_operation_id", err)
	}
	if len(input.ActiveFacts) > MaxMissionAuthorityMembersV2 {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "active_facts", nil)
	}
	facts := append([]ScopedMissionFactV2(nil), input.ActiveFacts...)
	sort.Slice(facts, func(i, j int) bool { return scopedMissionFactKeyV2(facts[i]) < scopedMissionFactKeyV2(facts[j]) })
	for index, fact := range facts {
		if !fact.validate() || index > 0 && scopedMissionFactKeyV2(facts[index-1]) == scopedMissionFactKeyV2(fact) {
			return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "active_facts", nil)
		}
	}
	input.ActiveFacts = nil
	state := ReadinessAuthorityStateV2{input: input, facts: facts}
	if trustedCanonical != nil {
		// The enclosing sealed EventEnvelopeV2 has already established exact
		// recursive canonical JSON. Semantic construction above remains
		// mandatory; hashes unused by playback need not be recomputed.
		state.canonical = trustedCanonical
		return state, nil
	}
	canonical, err := canonicalReadinessAuthorityStateV2(state)
	if err != nil {
		return ReadinessAuthorityStateV2{}, err
	}
	digest, err := journalv2.ReducerStateDigestV2(canonical)
	if err != nil {
		return ReadinessAuthorityStateV2{}, err
	}
	factBytes, err := canonicalReadinessFactSetV2(state)
	if err != nil {
		return ReadinessAuthorityStateV2{}, err
	}
	factDigest, err := journalv2.ReducerStateDigestV2(factBytes)
	if err != nil {
		return ReadinessAuthorityStateV2{}, err
	}
	state.canonical, state.digest, state.authorityFacts = canonical, digest, factDigest
	return state, nil
}

func (s ReadinessAuthorityStateV2) IsZero() bool                      { return len(s.canonical) == 0 }
func (s ReadinessAuthorityStateV2) ProjectID() journalv2.ProjectID    { return s.input.ProjectID }
func (s ReadinessAuthorityStateV2) MissionID() journalv2.MissionID    { return s.input.MissionID }
func (s ReadinessAuthorityStateV2) Version() journalv2.LogicalVersion { return s.input.Version }
func (s ReadinessAuthorityStateV2) Lifecycle() MissionLifecycle       { return s.input.Lifecycle }
func (s ReadinessAuthorityStateV2) CurrentWorkRevisionID() journalv2.WorkRevisionID {
	return s.input.CurrentWorkRevision.ID()
}
func (s ReadinessAuthorityStateV2) CurrentWorkRevision() WorkRevisionAuthorityV2 {
	return s.input.CurrentWorkRevision
}
func (s ReadinessAuthorityStateV2) ActivityCount() uint64 { return s.input.ActivityCount }
func (s ReadinessAuthorityStateV2) ActiveFacts() []ScopedMissionFactV2 {
	return append([]ScopedMissionFactV2(nil), s.facts...)
}
func (s ReadinessAuthorityStateV2) LastOperationID() journalv2.OperationID {
	return s.input.LastOperationID
}
func (s ReadinessAuthorityStateV2) ObservedAt() string       { return s.input.ObservedAt }
func (s ReadinessAuthorityStateV2) Digest() journalv2.Digest { return s.digest }
func (s ReadinessAuthorityStateV2) AuthorityFactSetDigest() journalv2.Digest {
	return s.authorityFacts
}
func (s ReadinessAuthorityStateV2) CanonicalBytes() []byte { return bytes.Clone(s.canonical) }

func canonicalReadinessAuthorityStateV2(state ReadinessAuthorityStateV2) ([]byte, error) {
	// Field declaration order is the canonical JSON key order. Keep the decode
	// wire independent so changing encoder layout cannot weaken strict decoding.
	wire := struct {
		ActiveFacts         []ScopedMissionFactV2 `json:"active_facts"`
		ActivityCount       uint64                `json:"activity_count"`
		CurrentWorkRevision json.RawMessage       `json:"current_work_revision,omitempty"`
		DefinitionRevision  string                `json:"definition_revision"`
		DefinitionSHA256    string                `json:"definition_sha256"`
		LastOperationID     journalv2.OperationID `json:"last_operation_id"`
		Lifecycle           MissionLifecycle      `json:"lifecycle"`
		MissionID           journalv2.MissionID   `json:"mission_id"`
		ObservedAt          string                `json:"observed_at"`
		ProjectID           journalv2.ProjectID   `json:"project_id"`
		SchemaVersion       uint32                `json:"schema_version"`
		Version             uint64                `json:"version"`
	}{
		ActiveFacts: append([]ScopedMissionFactV2(nil), state.facts...), ActivityCount: state.input.ActivityCount,
		CurrentWorkRevision: state.input.CurrentWorkRevision.CanonicalBytes(), DefinitionRevision: state.input.DefinitionRevision,
		DefinitionSHA256: state.input.DefinitionDigest.String(), LastOperationID: state.input.LastOperationID,
		Lifecycle: state.input.Lifecycle, MissionID: state.input.MissionID, ObservedAt: state.input.ObservedAt,
		ProjectID: state.input.ProjectID, SchemaVersion: ReadinessAuthorityStateSchemaV2, Version: uint64(state.input.Version),
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", err)
	}
	return journalv2.CanonicalObject(encoded)
}

func canonicalReadinessFactSetV2(state ReadinessAuthorityStateV2) ([]byte, error) {
	encoded, err := json.Marshal(struct {
		ActiveFacts         []ScopedMissionFactV2 `json:"active_facts"`
		CurrentWorkRevision json.RawMessage       `json:"current_work_revision,omitempty"`
		DefinitionRevision  string                `json:"definition_revision"`
		DefinitionSHA256    string                `json:"definition_sha256"`
		Lifecycle           MissionLifecycle      `json:"lifecycle"`
		MissionID           journalv2.MissionID   `json:"mission_id"`
		ProjectID           journalv2.ProjectID   `json:"project_id"`
	}{
		ActiveFacts: append([]ScopedMissionFactV2(nil), state.facts...), CurrentWorkRevision: state.input.CurrentWorkRevision.CanonicalBytes(),
		DefinitionRevision: state.input.DefinitionRevision, DefinitionSHA256: state.input.DefinitionDigest.String(),
		Lifecycle: state.input.Lifecycle, MissionID: state.input.MissionID, ProjectID: state.input.ProjectID,
	})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeReadinessAuthorityStateV2(encoded []byte) (ReadinessAuthorityStateV2, error) {
	return decodeReadinessAuthorityStateV2(encoded, false)
}

func decodeReadinessAuthorityStateV2(encoded []byte, trustedCanonical bool) (ReadinessAuthorityStateV2, error) {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", nil)
	}
	if !trustedCanonical {
		canonical, err := journalv2.CanonicalObject(encoded)
		if err != nil || !bytes.Equal(canonical, encoded) {
			return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
		}
	}
	if trustedCanonical && !readinessStateRequiredFieldsV2(encoded) {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state_fields", nil)
	}
	var header struct {
		SchemaVersion uint32 `json:"schema_version"`
	}
	if err := json.Unmarshal(encoded, &header); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	if header.SchemaVersion == MissionAuthoritySchemaVersionV2 {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityLegacyPhaseV2, "legacy_phase_state", nil)
	}
	if header.SchemaVersion != ReadinessAuthorityStateSchemaV2 {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", nil)
	}
	var wire readinessAuthorityStateWireV2
	var decodeErr error
	if trustedCanonical {
		decodeErr = decodeCanonicalSealedV2(encoded, &wire)
	} else {
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		decodeErr = decoder.Decode(&wire)
	}
	if decodeErr != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", decodeErr)
	}
	digest, err := journalv2.ParseDigest("definition_sha256", wire.DefinitionSHA256)
	if err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	var current WorkRevisionAuthorityV2
	if len(wire.CurrentWorkRevision) != 0 {
		var head workRevisionHeadWireV2
		if decodeErr := json.Unmarshal(wire.CurrentWorkRevision, &head); decodeErr != nil {
			return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "work_revision", decodeErr)
		}
		current, err = sealWorkRevisionHeadV2(head)
		if err != nil {
			return ReadinessAuthorityStateV2{}, err
		}
	}
	input := ReadinessAuthorityStateInputV2{
		ProjectID: wire.ProjectID, MissionID: wire.MissionID, DefinitionRevision: wire.DefinitionRevision, DefinitionDigest: digest,
		Version: journalv2.LogicalVersion(wire.Version), Lifecycle: wire.Lifecycle, CurrentWorkRevision: current,
		ActivityCount: wire.ActivityCount, ActiveFacts: wire.ActiveFacts, LastOperationID: wire.LastOperationID, ObservedAt: wire.ObservedAt,
	}
	var state ReadinessAuthorityStateV2
	if trustedCanonical {
		state, err = newReadinessAuthorityStateV2(input, encoded)
	} else {
		state, err = NewReadinessAuthorityStateV2(input)
	}
	if err != nil || !trustedCanonical && !bytes.Equal(state.CanonicalBytes(), encoded) {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	return state, nil
}

func readinessStateRequiredFieldsV2(encoded []byte) bool {
	for _, field := range [...]string{
		`"active_facts":`, `"activity_count":`, `"definition_revision":`, `"definition_sha256":`,
		`"last_operation_id":`, `"lifecycle":`, `"mission_id":`, `"observed_at":`, `"project_id":`,
		`"schema_version":`, `"version":`,
	} {
		if !bytes.Contains(encoded, []byte(field)) {
			return false
		}
	}
	return true
}

type ReadinessEventTypeV2 string

const (
	ReadinessEventCreatedV2              ReadinessEventTypeV2 = "readiness_created"
	ReadinessEventPausedV2               ReadinessEventTypeV2 = "readiness_paused"
	ReadinessEventResumedV2              ReadinessEventTypeV2 = "readiness_resumed"
	ReadinessEventFailedV2               ReadinessEventTypeV2 = "readiness_failed"
	ReadinessEventCanceledV2             ReadinessEventTypeV2 = "readiness_canceled"
	ReadinessEventCompletedV2            ReadinessEventTypeV2 = "readiness_completed"
	ReadinessEventActivityRecordedV2     ReadinessEventTypeV2 = "readiness_activity_recorded"
	ReadinessEventFactOpenedV2           ReadinessEventTypeV2 = "readiness_fact_opened"
	ReadinessEventFactClosedV2           ReadinessEventTypeV2 = "readiness_fact_closed"
	ReadinessEventWorkRevisionSelectedV2 ReadinessEventTypeV2 = "readiness_work_revision_selected"
)

func (e ReadinessEventTypeV2) validate() bool {
	switch e {
	case ReadinessEventCreatedV2, ReadinessEventPausedV2, ReadinessEventResumedV2, ReadinessEventFailedV2,
		ReadinessEventCanceledV2, ReadinessEventCompletedV2, ReadinessEventActivityRecordedV2,
		ReadinessEventFactOpenedV2, ReadinessEventFactClosedV2, ReadinessEventWorkRevisionSelectedV2:
		return true
	default:
		return false
	}
}

type ReadinessAuthorityEventV2 struct {
	eventType ReadinessEventTypeV2
	prior     ReadinessAuthorityStateV2
	next      ReadinessAuthorityStateV2
	activity  *MissionActivityV2
	selection *WorkRevisionSelectionV2
}

func (e ReadinessAuthorityEventV2) WorkRevisionSelection() *WorkRevisionSelectionV2 {
	if e.selection == nil {
		return nil
	}
	copyValue := *e.selection
	return &copyValue
}

func (e ReadinessAuthorityEventV2) EventType() ReadinessEventTypeV2  { return e.eventType }
func (e ReadinessAuthorityEventV2) Prior() ReadinessAuthorityStateV2 { return e.prior }
func (e ReadinessAuthorityEventV2) State() ReadinessAuthorityStateV2 { return e.next }
func (e ReadinessAuthorityEventV2) Activity() *MissionActivityV2 {
	if e.activity == nil {
		return nil
	}
	copyValue := *e.activity
	copyValue.EvidenceIdentities = append([]string(nil), e.activity.EvidenceIdentities...)
	return &copyValue
}

type MissionActivityV2 struct {
	ActivityID         string    `json:"activity_id"`
	Action             ActionRef `json:"action"`
	Outcome            string    `json:"outcome"`
	ResultDigest       string    `json:"result_sha256"`
	EvidenceIdentities []string  `json:"evidence_identities"`
}

func (a MissionActivityV2) validate() bool {
	if !boundedAuthorityIdentityV2(a.ActivityID) || a.Action.Validate() != nil || !boundedAuthorityIdentityV2(a.Outcome) || !lowerHexDigest(a.ResultDigest) || len(a.EvidenceIdentities) > MaxMissionAuthorityMembersV2 {
		return false
	}
	if !sort.StringsAreSorted(a.EvidenceIdentities) {
		return false
	}
	for index, identity := range a.EvidenceIdentities {
		if !boundedAuthorityIdentityV2(identity) || index > 0 && a.EvidenceIdentities[index-1] == identity {
			return false
		}
	}
	return true
}

func NewReadinessAuthorityEventV2(eventType ReadinessEventTypeV2, prior, next ReadinessAuthorityStateV2) (ReadinessAuthorityEventV2, error) {
	if !eventType.validate() || next.IsZero() {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "readiness_event", nil)
	}
	if err := validateReadinessTransitionV2(eventType, prior, next); err != nil {
		return ReadinessAuthorityEventV2{}, err
	}
	return ReadinessAuthorityEventV2{eventType: eventType, prior: prior, next: next}, nil
}

func NewReadinessActivityEventV2(prior, next ReadinessAuthorityStateV2, activity MissionActivityV2) (ReadinessAuthorityEventV2, error) {
	base, err := NewReadinessAuthorityEventV2(ReadinessEventActivityRecordedV2, prior, next)
	if err != nil || !activity.validate() {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "activity", err)
	}
	activity.EvidenceIdentities = append([]string(nil), activity.EvidenceIdentities...)
	base.activity = &activity
	return base, nil
}

func NewReadinessWorkRevisionEventV2(prior, next ReadinessAuthorityStateV2, selection WorkRevisionSelectionV2) (ReadinessAuthorityEventV2, error) {
	base, err := NewReadinessAuthorityEventV2(ReadinessEventWorkRevisionSelectedV2, prior, next)
	if err != nil || selection.IsZero() || next.CurrentWorkRevision().CanonicalBytes() == nil || !bytes.Equal(next.CurrentWorkRevision().CanonicalBytes(), selection.Head().CanonicalBytes()) {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_selection", err)
	}
	if prior.CurrentWorkRevision().IsZero() {
		if selection.Head().Generation() != 1 || selection.Head().PredecessorID() != "" || len(selection.Dispositions()) != 0 {
			return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "initial_work_revision", nil)
		}
	} else if selection.Head().Generation() != prior.CurrentWorkRevision().Generation()+1 || selection.Head().PredecessorID() != prior.CurrentWorkRevisionID() || len(selection.Dispositions()) == 0 {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "reconciled_work_revision", nil)
	}
	base.selection = &selection
	return base, nil
}

type readinessAuthorityEventWireV2 struct {
	SchemaVersion uint32               `json:"schema_version"`
	EventType     ReadinessEventTypeV2 `json:"event_type"`
	Prior         json.RawMessage      `json:"prior,omitempty"`
	Next          json.RawMessage      `json:"next"`
	Activity      *MissionActivityV2   `json:"activity,omitempty"`
	WorkRevision  json.RawMessage      `json:"work_revision_selection,omitempty"`
}

func EncodeReadinessAuthorityEventV2(event ReadinessAuthorityEventV2) ([]byte, error) {
	validated, err := NewReadinessAuthorityEventV2(event.eventType, event.prior, event.next)
	if err != nil {
		return nil, err
	}
	if event.activity != nil {
		validated, err = NewReadinessActivityEventV2(event.prior, event.next, *event.activity)
		if err != nil {
			return nil, err
		}
	} else if event.eventType == ReadinessEventActivityRecordedV2 {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "activity", nil)
	}
	if event.selection != nil {
		validated, err = NewReadinessWorkRevisionEventV2(event.prior, event.next, *event.selection)
		if err != nil {
			return nil, err
		}
	} else if event.eventType == ReadinessEventWorkRevisionSelectedV2 {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_selection", nil)
	}
	var prior json.RawMessage
	if !validated.prior.IsZero() {
		prior = validated.prior.CanonicalBytes()
	}
	var selection json.RawMessage
	if validated.selection != nil {
		selection = validated.selection.CanonicalBytes()
	}
	encoded, err := json.Marshal(readinessAuthorityEventWireV2{ReadinessAuthorityStateSchemaV2, validated.eventType, prior, validated.next.CanonicalBytes(), validated.Activity(), selection})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeReadinessAuthorityEventV2(encoded []byte) (ReadinessAuthorityEventV2, error) {
	return decodeReadinessAuthorityEventV2(encoded, false)
}

func decodeReadinessAuthorityEventV2(encoded []byte, trustedCanonical bool) (ReadinessAuthorityEventV2, error) {
	if !trustedCanonical {
		canonical, err := journalv2.CanonicalObject(encoded)
		if err != nil || !bytes.Equal(canonical, encoded) {
			return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "readiness_event", err)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire readinessAuthorityEventWireV2
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != ReadinessAuthorityStateSchemaV2 {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "readiness_event", err)
	}
	var prior ReadinessAuthorityStateV2
	var err error
	if len(wire.Prior) != 0 {
		prior, err = decodeReadinessAuthorityStateV2(wire.Prior, trustedCanonical)
		if err != nil {
			return ReadinessAuthorityEventV2{}, err
		}
	}
	next, err := decodeReadinessAuthorityStateV2(wire.Next, trustedCanonical)
	if err != nil {
		return ReadinessAuthorityEventV2{}, err
	}
	if wire.Activity != nil {
		return NewReadinessActivityEventV2(prior, next, *wire.Activity)
	}
	if len(wire.WorkRevision) != 0 {
		selection, decodeErr := decodeWorkRevisionSelectionV2(wire.WorkRevision)
		if decodeErr != nil {
			return ReadinessAuthorityEventV2{}, decodeErr
		}
		return NewReadinessWorkRevisionEventV2(prior, next, selection)
	}
	return NewReadinessAuthorityEventV2(wire.EventType, prior, next)
}

func ReduceReadinessAuthorityEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "mission" || event.EventSchemaVersion() != 1 || event.ReducerVersion() != 1 || event.Coordinate().Kind != "mission" {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_tuple", nil)
	}
	// EventEnvelopeV2 construction already establishes recursively canonical
	// payload bytes. The reducer keeps full strict semantic decoding but avoids
	// validating the same JSON bytes a second time during playback.
	payload, err := decodeReadinessAuthorityEventV2(event.Payload(), true)
	if err != nil || string(payload.eventType) != event.EventType() || payload.next.Version() != event.LogicalVersion() || payload.next.MissionID() != journalv2.MissionID(event.Coordinate().ID) {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_membership", err)
	}
	if payload.prior.IsZero() {
		if !bytes.Equal(prior, []byte(`{}`)) && len(prior) != 0 {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
		}
	} else if !bytes.Equal(prior, payload.prior.CanonicalBytes()) {
		// DecodeReadinessAuthorityEventV2 already strictly decoded and validated
		// the embedded prior state. Exact canonical-byte continuity is stronger
		// than decoding the playback state a second time and comparing its digest.
		return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
	}
	return payload.next.CanonicalBytes(), nil
}

// ReduceMissionEventSequenceV2 preserves one decoded readiness state across an
// already ordered playback chain. Each event remains strictly decoded and its
// exact embedded prior bytes, transition, membership, and next state are
// checked; only the duplicate decode of next-as-following-prior is removed.
func ReduceMissionEventSequenceV2(prior []byte, events []journalv2.EventEnvelopeV2) ([]byte, error) {
	currentBytes := prior
	var current ReadinessAuthorityStateV2
	var currentMission MissionAuthorityStateV2
	for _, envelope := range events {
		if envelope.Family() != "mission" || envelope.EventSchemaVersion() != 1 || envelope.ReducerVersion() != 1 || envelope.Coordinate().Kind != "mission" {
			return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_tuple", nil)
		}
		payloadBytes := envelope.Payload()
		readinessSchema := bytes.HasSuffix(payloadBytes, []byte(`,"schema_version":2}`))
		missionSchema := bytes.HasSuffix(payloadBytes, []byte(`,"schema_version":1}`))
		if missionSchema && !(envelope.Coordinate().Scope.Kind == journalv2.ScopeMission && envelope.Coordinate().ID != journalv2.AggregateID(envelope.Coordinate().Scope.MissionID)) {
			if currentMission.IsZero() && len(currentBytes) != 0 && !bytes.Equal(currentBytes, []byte(`{}`)) {
				var decodeErr error
				currentMission, decodeErr = DecodeMissionAuthorityStateV2(currentBytes)
				if decodeErr != nil {
					return nil, decodeErr
				}
			}
			payload, decodeErr := decodeMissionAuthoritySequenceEventV2(payloadBytes, currentBytes, currentMission)
			if decodeErr != nil || string(payload.eventType) != envelope.EventType() || payload.next.Version() != envelope.LogicalVersion() || payload.next.MissionID() != journalv2.MissionID(envelope.Coordinate().ID) {
				return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_membership", decodeErr)
			}
			currentMission, currentBytes = payload.next, payload.next.canonical
			continue
		}
		if !readinessSchema {
			output, reduceErr := ReduceMissionEventV2(currentBytes, envelope)
			if reduceErr != nil {
				return nil, reduceErr
			}
			current, currentBytes = ReadinessAuthorityStateV2{}, output
			continue
		}
		if current.IsZero() && len(currentBytes) != 0 && !bytes.Equal(currentBytes, []byte(`{}`)) {
			var decodeErr error
			current, decodeErr = DecodeReadinessAuthorityStateV2(currentBytes)
			if decodeErr != nil {
				return nil, decodeErr
			}
		}
		payload, decodeErr := decodeReadinessAuthoritySequenceEventV2(payloadBytes, currentBytes, current)
		if decodeErr != nil || string(payload.eventType) != envelope.EventType() || payload.next.Version() != envelope.LogicalVersion() || payload.next.MissionID() != journalv2.MissionID(envelope.Coordinate().ID) {
			return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_membership", decodeErr)
		}
		current, currentBytes = payload.next, payload.next.canonical
	}
	return currentBytes, nil
}

func decodeMissionAuthoritySequenceEventV2(encoded, priorBytes []byte, prior MissionAuthorityStateV2) (MissionEventPayloadV2, error) {
	var wire missionEventPayloadWireV2
	if err := decodeCanonicalSealedV2(encoded, &wire); err != nil || wire.SchemaVersion != MissionAuthoritySchemaVersionV2 {
		return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_event", err)
	}
	if prior.IsZero() {
		if len(wire.Prior) != 0 || len(priorBytes) != 0 && !bytes.Equal(priorBytes, []byte(`{}`)) {
			return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
		}
	} else if !bytes.Equal(wire.Prior, priorBytes) {
		return MissionEventPayloadV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
	}
	next, err := decodeMissionAuthorityStateV2(wire.Next, true)
	if err != nil {
		return MissionEventPayloadV2{}, err
	}
	return NewMissionEventPayloadV2(wire.EventType, prior, next)
}

func decodeReadinessAuthoritySequenceEventV2(encoded, priorBytes []byte, prior ReadinessAuthorityStateV2) (ReadinessAuthorityEventV2, error) {
	var wire readinessAuthorityEventWireV2
	if err := decodeCanonicalSealedV2(encoded, &wire); err != nil || wire.SchemaVersion != ReadinessAuthorityStateSchemaV2 {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "readiness_event", err)
	}
	if prior.IsZero() {
		if len(wire.Prior) != 0 || len(priorBytes) != 0 && !bytes.Equal(priorBytes, []byte(`{}`)) {
			return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
		}
	} else if !bytes.Equal(wire.Prior, priorBytes) {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
	}
	next, err := decodeReadinessAuthorityStateV2(wire.Next, true)
	if err != nil {
		return ReadinessAuthorityEventV2{}, err
	}
	if wire.Activity != nil {
		return NewReadinessActivityEventV2(prior, next, *wire.Activity)
	}
	if len(wire.WorkRevision) != 0 {
		selection, decodeErr := decodeWorkRevisionSelectionV2(wire.WorkRevision)
		if decodeErr != nil {
			return ReadinessAuthorityEventV2{}, decodeErr
		}
		return NewReadinessWorkRevisionEventV2(prior, next, selection)
	}
	return NewReadinessAuthorityEventV2(wire.EventType, prior, next)
}

func validateReadinessTransitionV2(event ReadinessEventTypeV2, prior, next ReadinessAuthorityStateV2) error {
	if event == ReadinessEventCreatedV2 {
		if !prior.IsZero() || next.Version() != 1 || next.Lifecycle() != MissionOpen || next.ActivityCount() != 0 || len(next.ActiveFacts()) != 0 || !next.CurrentWorkRevision().IsZero() {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "readiness_created", nil)
		}
		return nil
	}
	if prior.IsZero() || prior.Lifecycle().Terminal() || next.Version() != prior.Version()+1 || next.ProjectID() != prior.ProjectID() || next.MissionID() != prior.MissionID() || next.input.DefinitionRevision != prior.input.DefinitionRevision || next.input.DefinitionDigest != prior.input.DefinitionDigest || next.ObservedAt() < prior.ObservedAt() {
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "readiness_transition", nil)
	}
	sameAuthority := next.CurrentWorkRevisionID() == prior.CurrentWorkRevisionID() && next.ActivityCount() == prior.ActivityCount() && equalScopedFactsV2(next.facts, prior.facts)
	switch event {
	case ReadinessEventPausedV2:
		if prior.Lifecycle() != MissionOpen || next.Lifecycle() != MissionPaused || !sameAuthority {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "pause", nil)
		}
	case ReadinessEventResumedV2:
		if prior.Lifecycle() != MissionPaused || next.Lifecycle() != MissionOpen || !sameAuthority {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "resume", nil)
		}
	case ReadinessEventFailedV2, ReadinessEventCanceledV2, ReadinessEventCompletedV2:
		want := map[ReadinessEventTypeV2]MissionLifecycle{ReadinessEventFailedV2: MissionFailed, ReadinessEventCanceledV2: MissionCanceled, ReadinessEventCompletedV2: MissionCompleted}[event]
		if next.Lifecycle() != want || !sameAuthority {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "terminal", nil)
		}
	case ReadinessEventActivityRecordedV2:
		if next.Lifecycle() != prior.Lifecycle() || next.ActivityCount() != prior.ActivityCount()+1 || next.CurrentWorkRevisionID() != prior.CurrentWorkRevisionID() || !equalScopedFactsV2(next.facts, prior.facts) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "activity", nil)
		}
	case ReadinessEventFactOpenedV2:
		if next.Lifecycle() != prior.Lifecycle() || next.ActivityCount() != prior.ActivityCount() || next.CurrentWorkRevisionID() != prior.CurrentWorkRevisionID() || !oneScopedFactAddedV2(prior.facts, next.facts) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "fact", nil)
		}
	case ReadinessEventFactClosedV2:
		if next.Lifecycle() != prior.Lifecycle() || next.ActivityCount() != prior.ActivityCount() || next.CurrentWorkRevisionID() != prior.CurrentWorkRevisionID() || !oneScopedFactAddedV2(next.facts, prior.facts) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "fact", nil)
		}
	case ReadinessEventWorkRevisionSelectedV2:
		if next.Lifecycle() != prior.Lifecycle() || next.ActivityCount() != prior.ActivityCount() || next.CurrentWorkRevision().IsZero() || next.CurrentWorkRevisionID() == prior.CurrentWorkRevisionID() || !equalScopedFactsV2(next.facts, prior.facts) {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "work_revision", nil)
		}
	default:
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "event_type", nil)
	}
	return nil
}

func scopedMissionFactKeyV2(fact ScopedMissionFactV2) string {
	return string(fact.Kind) + "\x00" + fact.FactID + "\x00" + actionRefKey(fact.Scope)
}

func equalScopedFactsV2(left, right []ScopedMissionFactV2) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if scopedMissionFactKeyV2(left[index]) != scopedMissionFactKeyV2(right[index]) || left[index].EvidenceIdentity != right[index].EvidenceIdentity || left[index].EvidenceDigest != right[index].EvidenceDigest {
			return false
		}
	}
	return true
}

func oneScopedFactAddedV2(before, after []ScopedMissionFactV2) bool {
	if len(after) != len(before)+1 {
		return false
	}
	beforeIndex := 0
	for _, fact := range after {
		if beforeIndex < len(before) && equalScopedFactsV2([]ScopedMissionFactV2{fact}, []ScopedMissionFactV2{before[beforeIndex]}) {
			beforeIndex++
		}
	}
	return beforeIndex == len(before)
}
