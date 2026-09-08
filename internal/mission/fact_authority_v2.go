package mission

import (
	"bytes"
	"encoding/json"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type MissionFactKindV2 string
type MissionFactStatusV2 string

const (
	MissionFactBlockerV2  MissionFactKindV2 = "blocker"
	MissionFactDecisionV2 MissionFactKindV2 = "decision"

	MissionFactActiveV2   MissionFactStatusV2 = "active"
	MissionFactClearedV2  MissionFactStatusV2 = "cleared"
	MissionFactOpenV2     MissionFactStatusV2 = "open"
	MissionFactResolvedV2 MissionFactStatusV2 = "resolved"
	MissionFactDeferredV2 MissionFactStatusV2 = "deferred"
	MissionFactCanceledV2 MissionFactStatusV2 = "canceled"
)

type MissionFactStateInputV2 struct {
	ProjectID       journalv2.ProjectID
	MissionID       journalv2.MissionID
	FactID          string
	Kind            MissionFactKindV2
	Status          MissionFactStatusV2
	LogicalVersion  journalv2.LogicalVersion
	DetailDigest    journalv2.Digest
	LastOperationID journalv2.OperationID
	ObservedAt      string
}

type MissionFactStateV2 struct {
	input     MissionFactStateInputV2
	canonical []byte
	digest    journalv2.Digest
}

type missionFactWireV2 struct {
	SchemaVersion   uint32                `json:"schema_version"`
	ProjectID       journalv2.ProjectID   `json:"project_id"`
	MissionID       journalv2.MissionID   `json:"mission_id"`
	FactID          string                `json:"fact_id"`
	Kind            MissionFactKindV2     `json:"kind"`
	Status          MissionFactStatusV2   `json:"status"`
	LogicalVersion  uint64                `json:"logical_version"`
	DetailSHA256    string                `json:"detail_sha256"`
	LastOperationID journalv2.OperationID `json:"last_operation_id"`
	ObservedAt      string                `json:"observed_at"`
}

func NewMissionFactStateV2(input MissionFactStateInputV2) (MissionFactStateV2, error) {
	if _, err := journalv2.NewProjectID(string(input.ProjectID)); err != nil {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_project", err)
	}
	if _, err := journalv2.NewMissionID(string(input.MissionID)); err != nil {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_mission", err)
	}
	if !boundedAuthorityIdentityV2(input.FactID) || input.LogicalVersion == 0 || input.DetailDigest.IsZero() || !canonicalAuthorityTimeV2(input.ObservedAt) {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact", nil)
	}
	if _, err := journalv2.NewOperationID(string(input.LastOperationID)); err != nil {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_operation", err)
	}
	if input.Kind == MissionFactBlockerV2 {
		if input.Status != MissionFactActiveV2 && input.Status != MissionFactClearedV2 {
			return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_status", nil)
		}
	} else if input.Kind == MissionFactDecisionV2 {
		if input.Status != MissionFactOpenV2 && input.Status != MissionFactResolvedV2 && input.Status != MissionFactDeferredV2 && input.Status != MissionFactCanceledV2 {
			return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_status", nil)
		}
	} else {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_kind", nil)
	}
	wire := missionFactWireV2{1, input.ProjectID, input.MissionID, input.FactID, input.Kind, input.Status, uint64(input.LogicalVersion), input.DetailDigest.String(), input.LastOperationID, input.ObservedAt}
	encoded, _ := json.Marshal(wire)
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil {
		return MissionFactStateV2{}, err
	}
	digest, _ := journalv2.ReducerStateDigestV2(canonical)
	return MissionFactStateV2{input: input, canonical: canonical, digest: digest}, nil
}

func (s MissionFactStateV2) IsZero() bool                   { return len(s.canonical) == 0 }
func (s MissionFactStateV2) ProjectID() journalv2.ProjectID { return s.input.ProjectID }
func (s MissionFactStateV2) MissionID() journalv2.MissionID { return s.input.MissionID }
func (s MissionFactStateV2) Kind() MissionFactKindV2        { return s.input.Kind }
func (s MissionFactStateV2) Status() MissionFactStatusV2    { return s.input.Status }
func (s MissionFactStateV2) FactID() string                 { return s.input.FactID }
func (s MissionFactStateV2) LogicalVersion() journalv2.LogicalVersion {
	return s.input.LogicalVersion
}
func (s MissionFactStateV2) LastOperationID() journalv2.OperationID { return s.input.LastOperationID }
func (s MissionFactStateV2) DetailDigest() journalv2.Digest         { return s.input.DetailDigest }
func (s MissionFactStateV2) Digest() journalv2.Digest               { return s.digest }
func (s MissionFactStateV2) CanonicalBytes() []byte                 { return append([]byte(nil), s.canonical...) }

func DecodeMissionFactStateV2(encoded []byte) (MissionFactStateV2, error) {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact", nil)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact", err)
	}
	var wire missionFactWireV2
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&wire); err != nil || wire.SchemaVersion != 1 {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact", err)
	}
	digest, err := journalv2.ParseDigest("detail_sha256", wire.DetailSHA256)
	if err != nil {
		return MissionFactStateV2{}, err
	}
	state, err := NewMissionFactStateV2(MissionFactStateInputV2{wire.ProjectID, wire.MissionID, wire.FactID, wire.Kind, wire.Status, journalv2.LogicalVersion(wire.LogicalVersion), digest, wire.LastOperationID, wire.ObservedAt})
	if err != nil || !bytes.Equal(state.canonical, encoded) {
		return MissionFactStateV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact", err)
	}
	return state, nil
}

func decodeMissionFactStateV2(encoded []byte) (MissionFactStateV2, error) {
	return DecodeMissionFactStateV2(encoded)
}

type MissionFactTransitionV2 struct {
	Prior                  MissionFactStateV2
	Successor              MissionFactStateV2
	ExpectedRecordRevision uint64
}

type missionFactEventWireV2 struct {
	SchemaVersion uint32          `json:"schema_version"`
	Prior         json.RawMessage `json:"prior,omitempty"`
	Successor     json.RawMessage `json:"successor"`
}

func encodeMissionFactEventV2(transition MissionFactTransitionV2) ([]byte, error) {
	if err := validateMissionFactTransitionV2(transition); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(missionFactEventWireV2{1, transition.Prior.CanonicalBytes(), transition.Successor.CanonicalBytes()})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}
func decodeMissionFactEventV2(encoded []byte) (MissionFactTransitionV2, error) {
	if len(encoded) == 0 || len(encoded) > journalv2.MaxCanonicalPayloadBytes {
		return MissionFactTransitionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact_event", nil)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return MissionFactTransitionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact_event", err)
	}
	var wire missionFactEventWireV2
	if err = json.Unmarshal(encoded, &wire); err != nil || wire.SchemaVersion != 1 {
		return MissionFactTransitionV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact_event", err)
	}
	var prior MissionFactStateV2
	if len(wire.Prior) > 0 {
		prior, err = decodeMissionFactStateV2(wire.Prior)
		if err != nil {
			return MissionFactTransitionV2{}, err
		}
	}
	next, err := decodeMissionFactStateV2(wire.Successor)
	if err != nil {
		return MissionFactTransitionV2{}, err
	}
	result := MissionFactTransitionV2{Prior: prior, Successor: next}
	if err = validateMissionFactTransitionV2(result); err != nil {
		return MissionFactTransitionV2{}, err
	}
	return result, nil
}

func validateMissionFactTransitionV2(value MissionFactTransitionV2) error {
	next := value.Successor
	if next.IsZero() {
		return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "fact_transition", nil)
	}
	if value.Prior.IsZero() {
		if next.LogicalVersion() != 1 {
			return missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "fact_version", nil)
		}
		return nil
	}
	prior := value.Prior
	if next.input.ProjectID != prior.input.ProjectID || next.input.MissionID != prior.input.MissionID || next.input.FactID != prior.input.FactID || next.input.Kind != prior.input.Kind || next.LogicalVersion() != prior.LogicalVersion()+1 {
		return missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "fact_version", nil)
	}
	if prior.Kind() == MissionFactBlockerV2 && (prior.Status() != MissionFactActiveV2 || next.Status() != MissionFactClearedV2) {
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "blocker_fact", nil)
	}
	if prior.Kind() == MissionFactDecisionV2 && (prior.Status() != MissionFactOpenV2 || next.Status() == MissionFactOpenV2) {
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision_fact", nil)
	}
	return nil
}

func missionFactAggregateIDV2(state MissionFactStateV2) journalv2.AggregateID {
	return journalv2.AggregateID(string(state.Kind()) + ":" + state.FactID())
}

func reduceMissionFactEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {
	transition, err := decodeMissionFactEventV2(event.Payload())
	if err != nil {
		return nil, err
	}
	next := transition.Successor
	coordinate := event.Coordinate()
	if err := validateMissionFactSemanticForEventV2(MissionEventTypeV2(event.EventType()), transition); err != nil {
		return nil, err
	}
	if coordinate.ID != missionFactAggregateIDV2(next) || coordinate.Scope.Kind != journalv2.ScopeMission || coordinate.Scope.ProjectID != next.input.ProjectID || coordinate.Scope.MissionID != next.input.MissionID || event.LogicalVersion() != next.LogicalVersion() || event.OperationID() != next.input.LastOperationID {
		return nil, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "fact_membership", nil)
	}
	if transition.Prior.IsZero() {
		if len(prior) != 0 && !bytes.Equal(prior, []byte(`{}`)) {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "fact_prior", nil)
		}
	} else {
		decoded, decodeErr := decodeMissionFactStateV2(prior)
		if decodeErr != nil || decoded.Digest() != transition.Prior.Digest() {
			return nil, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "fact_prior", decodeErr)
		}
	}
	return next.CanonicalBytes(), nil
}

func validateMissionFactForEventV2(event MissionEventTypeV2, fact *MissionFactTransitionV2) error {
	needs := event == MissionEventBlockerRaisedV2 || event == MissionEventBlockerClearedV2 || event == MissionEventDecisionOpenedV2 || event == MissionEventDecisionResolvedV2 || event == MissionEventDecisionDeferredV2 || event == MissionEventDecisionCanceledV2
	if !needs {
		if fact != nil {
			return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "unexpected_fact", nil)
		}
		return nil
	}
	if fact == nil {
		return missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "missing_fact", nil)
	}
	if err := validateMissionFactTransitionV2(*fact); err != nil {
		return err
	}
	if fact.Prior.IsZero() {
		if fact.ExpectedRecordRevision != 0 {
			return missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "fact_revision", nil)
		}
	} else if fact.ExpectedRecordRevision == 0 {
		return missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "fact_revision", nil)
	}
	return validateMissionFactSemanticForEventV2(event, *fact)
}

func validateMissionFactSemanticForEventV2(event MissionEventTypeV2, fact MissionFactTransitionV2) error {
	next := fact.Successor
	switch event {
	case MissionEventBlockerRaisedV2:
		if next.Kind() != MissionFactBlockerV2 || next.Status() != MissionFactActiveV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "blocker_fact", nil)
		}
	case MissionEventBlockerClearedV2:
		if next.Kind() != MissionFactBlockerV2 || next.Status() != MissionFactClearedV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "blocker_fact", nil)
		}
	case MissionEventDecisionOpenedV2:
		if next.Kind() != MissionFactDecisionV2 || next.Status() != MissionFactOpenV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision_fact", nil)
		}
	case MissionEventDecisionResolvedV2:
		if next.Kind() != MissionFactDecisionV2 || next.Status() != MissionFactResolvedV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision_fact", nil)
		}
	case MissionEventDecisionDeferredV2:
		if next.Kind() != MissionFactDecisionV2 || next.Status() != MissionFactDeferredV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision_fact", nil)
		}
	case MissionEventDecisionCanceledV2:
		if next.Kind() != MissionFactDecisionV2 || next.Status() != MissionFactCanceledV2 {
			return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "decision_fact", nil)
		}
	default:
		return missionAuthorityErrorV2(MissionAuthorityInvalidTransitionV2, "fact_event", nil)
	}
	return nil
}

func canonicalMissionFactIntentV2(fact *MissionFactTransitionV2) []byte {
	if fact == nil {
		return nil
	}
	encoded, _ := encodeMissionFactEventV2(*fact)
	return encoded
}
