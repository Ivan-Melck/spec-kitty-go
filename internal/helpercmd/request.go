package helpercmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

type decodedOperation struct {
	request    OperationRequest
	mission    *mission.MissionAuthorityCommandV2
	readiness  *mission.ReadinessAuthorityCommandV2
	projectID  journalv2.ProjectID
	missionID  journalv2.MissionID
	operation  journalv2.OperationID
	requestSHA string
}

type missionCommandWire struct {
	SchemaVersion       uint32                     `json:"schema_version"`
	OperationID         journalv2.OperationID      `json:"operation_id"`
	ProjectID           journalv2.ProjectID        `json:"project_id"`
	MissionID           journalv2.MissionID        `json:"mission_id"`
	ActorID             journalv2.ActorID          `json:"actor_id"`
	ProcessID           journalv2.ProcessID        `json:"process_id"`
	RuntimeEpochID      journalv2.RuntimeEpochID   `json:"runtime_epoch_id"`
	DoctrineRevision    string                     `json:"doctrine_revision"`
	ProfileRevision     string                     `json:"profile_revision"`
	PolicyRevision      string                     `json:"policy_revision"`
	ObservedAt          string                     `json:"observed_at"`
	EventType           mission.MissionEventTypeV2 `json:"event_type"`
	Expected            json.RawMessage            `json:"expected,omitempty"`
	Successor           json.RawMessage            `json:"successor"`
	ObservationEvidence json.RawMessage            `json:"observation_evidence,omitempty"`
}

type readinessCommandWire struct {
	SchemaVersion       uint32                       `json:"schema_version"`
	OperationID         journalv2.OperationID        `json:"operation_id"`
	ProjectID           journalv2.ProjectID          `json:"project_id"`
	MissionID           journalv2.MissionID          `json:"mission_id"`
	ActorID             journalv2.ActorID            `json:"actor_id"`
	ProcessID           journalv2.ProcessID          `json:"process_id"`
	RuntimeEpochID      journalv2.RuntimeEpochID     `json:"runtime_epoch_id"`
	DoctrineRevision    string                       `json:"doctrine_revision"`
	ProfileRevision     string                       `json:"profile_revision"`
	PolicyRevision      string                       `json:"policy_revision"`
	ObservedAt          string                       `json:"observed_at"`
	EventType           mission.ReadinessEventTypeV2 `json:"event_type"`
	Expected            json.RawMessage              `json:"expected,omitempty"`
	Updated             json.RawMessage              `json:"updated"`
	ObservationEvidence json.RawMessage              `json:"observation_evidence,omitempty"`
}

func readBoundedJSON(reader io.Reader, target any) error {
	if reader == nil {
		return errors.New("request reader is required")
	}
	limited := io.LimitReader(reader, MaxRequestBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(data)) > MaxRequestBytes {
		return errors.New("request exceeds the bounded input size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request contains trailing JSON")
	}
	return nil
}

func decodeOperation(request OperationRequest) (decodedOperation, error) {
	if request.SchemaVersion != RequestSchemaVersion || request.Action == "" || len(request.Action) > 128 || len(request.Payload) == 0 {
		return decodedOperation{}, errors.New("operation request header is invalid")
	}
	canonical, err := journalv2.CanonicalObject(request.Payload)
	if err != nil {
		return decodedOperation{}, errors.New("operation payload is not valid JSON")
	}
	request.Payload = canonical
	outer, err := json.Marshal(request)
	if err != nil {
		return decodedOperation{}, err
	}
	outer, err = journalv2.CanonicalObject(outer)
	if err != nil {
		return decodedOperation{}, err
	}
	digest := sha256.Sum256(outer)
	decoded := decodedOperation{request: request, requestSHA: hex.EncodeToString(digest[:])}
	switch request.Kind {
	case OperationMission:
		command, decodeErr := decodeMissionCommand(request.Action, canonical)
		if decodeErr != nil {
			return decodedOperation{}, decodeErr
		}
		decoded.mission = &command
		decoded.projectID, decoded.missionID, decoded.operation = command.ProjectID, command.MissionID, command.OperationID
	case OperationReadiness:
		command, decodeErr := decodeReadinessCommand(request.Action, canonical)
		if decodeErr != nil {
			return decodedOperation{}, decodeErr
		}
		decoded.readiness = &command
		decoded.projectID, decoded.missionID, decoded.operation = command.ProjectID, command.MissionID, command.OperationID
	default:
		return decodedOperation{}, fmt.Errorf("unsupported operation kind %q", request.Kind)
	}
	return decoded, nil
}

func decodeMissionCommand(action string, payload []byte) (mission.MissionAuthorityCommandV2, error) {
	var wire missionCommandWire
	if err := decodeStrict(payload, &wire); err != nil {
		return mission.MissionAuthorityCommandV2{}, err
	}
	if wire.SchemaVersion != mission.MissionAuthoritySchemaVersionV2 || missionAction(wire.EventType) != action {
		return mission.MissionAuthorityCommandV2{}, errors.New("mission action and event type do not match")
	}
	expected, err := decodeMissionState(wire.Expected, true)
	if err != nil {
		return mission.MissionAuthorityCommandV2{}, err
	}
	successor, err := decodeMissionState(wire.Successor, false)
	if err != nil {
		return mission.MissionAuthorityCommandV2{}, err
	}
	evidence, err := canonicalEvidence(wire.ObservationEvidence)
	if err != nil {
		return mission.MissionAuthorityCommandV2{}, err
	}
	return mission.MissionAuthorityCommandV2{
		SchemaVersion: wire.SchemaVersion, OperationID: wire.OperationID, ProjectID: wire.ProjectID, MissionID: wire.MissionID,
		ActorID: wire.ActorID, ProcessID: wire.ProcessID, OriginatingRuntimeEpochID: wire.RuntimeEpochID,
		DoctrineRevision: wire.DoctrineRevision, ProfileRevision: wire.ProfileRevision, PolicyRevision: wire.PolicyRevision,
		ObservedAt: wire.ObservedAt, EventType: wire.EventType, Expected: expected, Successor: successor, ObservationEvidence: evidence,
	}, nil
}

func decodeReadinessCommand(action string, payload []byte) (mission.ReadinessAuthorityCommandV2, error) {
	var wire readinessCommandWire
	if err := decodeStrict(payload, &wire); err != nil {
		return mission.ReadinessAuthorityCommandV2{}, err
	}
	if wire.SchemaVersion != mission.ReadinessAuthorityStateSchemaV2 || readinessAction(wire.EventType) != action {
		return mission.ReadinessAuthorityCommandV2{}, errors.New("readiness action and event type do not match")
	}
	expected, err := decodeReadinessState(wire.Expected, true)
	if err != nil {
		return mission.ReadinessAuthorityCommandV2{}, err
	}
	updated, err := decodeReadinessState(wire.Updated, false)
	if err != nil {
		return mission.ReadinessAuthorityCommandV2{}, err
	}
	evidence, err := canonicalEvidence(wire.ObservationEvidence)
	if err != nil {
		return mission.ReadinessAuthorityCommandV2{}, err
	}
	return mission.ReadinessAuthorityCommandV2{
		SchemaVersion: wire.SchemaVersion, OperationID: wire.OperationID, ProjectID: wire.ProjectID, MissionID: wire.MissionID,
		ActorID: wire.ActorID, ProcessID: wire.ProcessID, OriginatingRuntimeEpochID: wire.RuntimeEpochID,
		DoctrineRevision: wire.DoctrineRevision, ProfileRevision: wire.ProfileRevision, PolicyRevision: wire.PolicyRevision,
		ObservedAt: wire.ObservedAt, EventType: wire.EventType, Expected: expected, Updated: updated, ObservationEvidence: evidence,
	}, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("payload contains trailing JSON")
	}
	return nil
}

func decodeMissionState(raw json.RawMessage, optional bool) (mission.MissionAuthorityStateV2, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		if optional {
			return mission.MissionAuthorityStateV2{}, nil
		}
		return mission.MissionAuthorityStateV2{}, errors.New("successor state is required")
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		return mission.MissionAuthorityStateV2{}, err
	}
	return mission.DecodeMissionAuthorityStateV2(canonical)
}

func decodeReadinessState(raw json.RawMessage, optional bool) (mission.ReadinessAuthorityStateV2, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		if optional {
			return mission.ReadinessAuthorityStateV2{}, nil
		}
		return mission.ReadinessAuthorityStateV2{}, errors.New("updated state is required")
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		return mission.ReadinessAuthorityStateV2{}, err
	}
	return mission.DecodeReadinessAuthorityStateV2(canonical)
}

func canonicalEvidence(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	return journalv2.CanonicalObject(raw)
}

func missionAction(event mission.MissionEventTypeV2) string {
	switch event {
	case mission.MissionEventCreatedV2:
		return string(mission.ActionMissionCreate)
	case mission.MissionEventPausedV2:
		return string(mission.ActionMissionPause)
	case mission.MissionEventResumedV2:
		return string(mission.ActionMissionResume)
	case mission.MissionEventFailedV2:
		return string(mission.ActionMissionFail)
	case mission.MissionEventCanceledV2:
		return string(mission.ActionMissionCancel)
	case mission.MissionEventCompletedV2:
		return string(mission.ActionMissionComplete)
	default:
		return ""
	}
}

func readinessAction(event mission.ReadinessEventTypeV2) string {
	switch event {
	case mission.ReadinessEventCreatedV2:
		return string(mission.ActionMissionCreate)
	case mission.ReadinessEventPausedV2:
		return string(mission.ActionMissionPause)
	case mission.ReadinessEventResumedV2:
		return string(mission.ActionMissionResume)
	case mission.ReadinessEventFailedV2:
		return string(mission.ActionMissionFail)
	case mission.ReadinessEventCanceledV2:
		return string(mission.ActionMissionCancel)
	case mission.ReadinessEventCompletedV2:
		return string(mission.ActionMissionComplete)
	default:
		return ""
	}
}
