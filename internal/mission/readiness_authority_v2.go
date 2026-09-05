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
	ProjectID             journalv2.ProjectID
	MissionID             journalv2.MissionID
	DefinitionRevision    string
	DefinitionDigest      journalv2.Digest
	Version               journalv2.LogicalVersion
	Lifecycle             MissionLifecycle
	CurrentWorkRevisionID journalv2.WorkRevisionID
	ActivityCount         uint64
	ActiveFacts           []ScopedMissionFactV2
	LastOperationID       journalv2.OperationID
	ObservedAt            string
}

type ReadinessAuthorityStateV2 struct {
	input          ReadinessAuthorityStateInputV2
	facts          []ScopedMissionFactV2
	canonical      []byte
	digest         journalv2.Digest
	authorityFacts journalv2.Digest
}

type readinessAuthorityStateWireV2 struct {
	SchemaVersion         uint32                   `json:"schema_version"`
	ProjectID             journalv2.ProjectID      `json:"project_id"`
	MissionID             journalv2.MissionID      `json:"mission_id"`
	DefinitionRevision    string                   `json:"definition_revision"`
	DefinitionSHA256      string                   `json:"definition_sha256"`
	Version               uint64                   `json:"version"`
	Lifecycle             MissionLifecycle         `json:"lifecycle"`
	CurrentWorkRevisionID journalv2.WorkRevisionID `json:"current_work_revision_id,omitempty"`
	ActivityCount         uint64                   `json:"activity_count"`
	ActiveFacts           []ScopedMissionFactV2    `json:"active_facts"`
	LastOperationID       journalv2.OperationID    `json:"last_operation_id"`
	ObservedAt            string                   `json:"observed_at"`
}

func NewReadinessAuthorityStateV2(input ReadinessAuthorityStateInputV2) (ReadinessAuthorityStateV2, error) {
	if _, err := journalv2.NewProjectID(string(input.ProjectID)); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "project_id", err)
	}
	if _, err := journalv2.NewMissionID(string(input.MissionID)); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_id", err)
	}
	if !boundedAuthorityIdentityV2(input.DefinitionRevision) || input.DefinitionDigest.IsZero() || input.Version == 0 || input.Lifecycle.Validate() != nil || !canonicalAuthorityTimeV2(input.ObservedAt) {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", nil)
	}
	if input.CurrentWorkRevisionID != "" {
		if _, err := journalv2.NewWorkRevisionID(string(input.CurrentWorkRevisionID)); err != nil {
			return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_id", err)
		}
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
	return s.input.CurrentWorkRevisionID
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
	wire := readinessAuthorityStateWireV2{
		SchemaVersion: ReadinessAuthorityStateSchemaV2, ProjectID: state.input.ProjectID, MissionID: state.input.MissionID,
		DefinitionRevision: state.input.DefinitionRevision, DefinitionSHA256: state.input.DefinitionDigest.String(), Version: uint64(state.input.Version),
		Lifecycle: state.input.Lifecycle, CurrentWorkRevisionID: state.input.CurrentWorkRevisionID, ActivityCount: state.input.ActivityCount,
		ActiveFacts: append([]ScopedMissionFactV2(nil), state.facts...), LastOperationID: state.input.LastOperationID, ObservedAt: state.input.ObservedAt,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "mission_state", err)
	}
	return journalv2.CanonicalObject(encoded)
}

func canonicalReadinessFactSetV2(state ReadinessAuthorityStateV2) ([]byte, error) {
	encoded, err := json.Marshal(struct {
		ProjectID             journalv2.ProjectID      `json:"project_id"`
		MissionID             journalv2.MissionID      `json:"mission_id"`
		DefinitionRevision    string                   `json:"definition_revision"`
		DefinitionSHA256      string                   `json:"definition_sha256"`
		Lifecycle             MissionLifecycle         `json:"lifecycle"`
		CurrentWorkRevisionID journalv2.WorkRevisionID `json:"current_work_revision_id,omitempty"`
		ActiveFacts           []ScopedMissionFactV2    `json:"active_facts"`
	}{state.input.ProjectID, state.input.MissionID, state.input.DefinitionRevision, state.input.DefinitionDigest.String(), state.input.Lifecycle, state.input.CurrentWorkRevisionID, state.facts})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeReadinessAuthorityStateV2(encoded []byte) (ReadinessAuthorityStateV2, error) {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", nil)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
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
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire readinessAuthorityStateWireV2
	if err := decoder.Decode(&wire); err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	digest, err := journalv2.ParseDigest("definition_sha256", wire.DefinitionSHA256)
	if err != nil {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	state, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: wire.ProjectID, MissionID: wire.MissionID, DefinitionRevision: wire.DefinitionRevision, DefinitionDigest: digest,
		Version: journalv2.LogicalVersion(wire.Version), Lifecycle: wire.Lifecycle, CurrentWorkRevisionID: wire.CurrentWorkRevisionID,
		ActivityCount: wire.ActivityCount, ActiveFacts: wire.ActiveFacts, LastOperationID: wire.LastOperationID, ObservedAt: wire.ObservedAt,
	})
	if err != nil || !bytes.Equal(state.CanonicalBytes(), encoded) {
		return ReadinessAuthorityStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "mission_state", err)
	}
	return state, nil
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

type readinessAuthorityEventWireV2 struct {
	SchemaVersion uint32               `json:"schema_version"`
	EventType     ReadinessEventTypeV2 `json:"event_type"`
	Prior         json.RawMessage      `json:"prior,omitempty"`
	Next          json.RawMessage      `json:"next"`
	Activity      *MissionActivityV2   `json:"activity,omitempty"`
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
	var prior json.RawMessage
	if !validated.prior.IsZero() {
		prior = validated.prior.CanonicalBytes()
	}
	encoded, err := json.Marshal(readinessAuthorityEventWireV2{ReadinessAuthorityStateSchemaV2, validated.eventType, prior, validated.next.CanonicalBytes(), validated.Activity()})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func DecodeReadinessAuthorityEventV2(encoded []byte) (ReadinessAuthorityEventV2, error) {
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "readiness_event", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire readinessAuthorityEventWireV2
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != ReadinessAuthorityStateSchemaV2 {
		return ReadinessAuthorityEventV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "readiness_event", err)
	}
	var prior ReadinessAuthorityStateV2
	if len(wire.Prior) != 0 {
		prior, err = DecodeReadinessAuthorityStateV2(wire.Prior)
		if err != nil {
			return ReadinessAuthorityEventV2{}, err
		}
	}
	next, err := DecodeReadinessAuthorityStateV2(wire.Next)
	if err != nil {
		return ReadinessAuthorityEventV2{}, err
	}
	if wire.Activity != nil {
		return NewReadinessActivityEventV2(prior, next, *wire.Activity)
	}
	return NewReadinessAuthorityEventV2(wire.EventType, prior, next)
}

func ReduceReadinessAuthorityEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	if event.Family() != "mission" || event.EventSchemaVersion() != 1 || event.ReducerVersion() != 1 || event.Coordinate().Kind != "mission" {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_tuple", nil)
	}
	payload, err := DecodeReadinessAuthorityEventV2(event.Payload())
	if err != nil || string(payload.eventType) != event.EventType() || payload.next.Version() != event.LogicalVersion() || payload.next.MissionID() != journalv2.MissionID(event.Coordinate().ID) {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "event_membership", err)
	}
	if payload.prior.IsZero() {
		if !bytes.Equal(prior, []byte(`{}`)) && len(prior) != 0 {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", nil)
		}
	} else {
		decoded, decodeErr := DecodeReadinessAuthorityStateV2(prior)
		if decodeErr != nil || decoded.Digest() != payload.prior.Digest() {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "prior_state", decodeErr)
		}
	}
	return payload.next.CanonicalBytes(), nil
}

func validateReadinessTransitionV2(event ReadinessEventTypeV2, prior, next ReadinessAuthorityStateV2) error {
	if event == ReadinessEventCreatedV2 {
		if !prior.IsZero() || next.Version() != 1 || next.Lifecycle() != MissionOpen || next.ActivityCount() != 0 || len(next.ActiveFacts()) != 0 {
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
		if next.Lifecycle() != prior.Lifecycle() || next.ActivityCount() != prior.ActivityCount() || next.CurrentWorkRevisionID() == prior.CurrentWorkRevisionID() || !equalScopedFactsV2(next.facts, prior.facts) {
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
