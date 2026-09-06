package helpercmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
)

type decodedOperation struct {
	request     OperationRequest
	mission     *mission.MissionAuthorityCommandV2
	readiness   *mission.ReadinessAuthorityCommandV2
	execution   *execution.ExecutionAuthorityRequestV2
	rebind      *execution.WorkspaceEpochRebindRequestV2
	handoff     *handoff.AuthorityRequestV2
	review      *review.AuthorityRequestV2
	acceptance  *integration.AcceptanceAuthorityRequestV2
	integration *integration.IntegrationAuthorityRequestV2
	cleanup     *integration.IntegrationCleanupRequestV2
	effect      *execution.EffectAuthorityRequestV2
	projectID   journalv2.ProjectID
	missionID   journalv2.MissionID
	operation   journalv2.OperationID
	scope       string
	requestSHA  string
}

type executionCommandWire struct {
	Kind             execution.ExecutionCommandKindV2 `json:"kind"`
	OperationID      journalv2.OperationID            `json:"operation_id"`
	ProjectID        ids.ProjectID                    `json:"project_id"`
	MissionID        ids.MissionID                    `json:"mission_id"`
	WorkRevisionID   ids.WorkRevisionID               `json:"work_revision_id"`
	WorkID           ids.WorkID                       `json:"work_id"`
	RunID            ids.RunID                        `json:"run_id"`
	ActorID          ids.ActorID                      `json:"actor_id"`
	ProcessID        ids.ProcessID                    `json:"process_id"`
	LeaseID          ids.LeaseID                      `json:"lease_id"`
	Generation       uint64                           `json:"generation"`
	Fence            string                           `json:"fence"`
	ObservedAt       time.Time                        `json:"observed_at"`
	Deadline         time.Time                        `json:"deadline"`
	RuntimeEpochID   journalv2.RuntimeEpochID         `json:"runtime_epoch_id"`
	Outcome          string                           `json:"outcome,omitempty"`
	Governance       governancecontext.Context        `json:"governance"`
	OperationEventID journalv2.EventID                `json:"operation_event_id"`
	SlotEventID      journalv2.EventID                `json:"slot_event_id"`
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
	Activity            *mission.MissionActivityV2   `json:"activity,omitempty"`
	WorkRevision        json.RawMessage              `json:"work_revision_selection,omitempty"`
	RelatedFact         *readinessFactTransitionWire `json:"related_fact,omitempty"`
	ObservationEvidence json.RawMessage              `json:"observation_evidence,omitempty"`
}

type readinessFactTransitionWire struct {
	Prior                  json.RawMessage `json:"prior,omitempty"`
	Successor              json.RawMessage `json:"successor"`
	ExpectedRecordRevision uint64          `json:"expected_record_revision"`
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
	if request.SchemaVersion != RequestSchemaVersion || request.Action == "" || len(request.Action) > 128 || request.Scope == "" || len(request.Scope) > 256 || len(request.Payload) == 0 {
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
	decoded := decodedOperation{request: request, scope: request.Scope, requestSHA: hex.EncodeToString(digest[:])}
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
	case OperationExecution:
		command, decodeErr := decodeExecutionCommand(request.Action, canonical)
		if decodeErr != nil {
			return decodedOperation{}, decodeErr
		}
		decoded.execution = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.OperationID
	case OperationWorkspaceRebind:
		var command execution.WorkspaceEpochRebindRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil || request.Action != string(mission.ActionWorkspaceAllocate) {
			return decodedOperation{}, errors.New("workspace rebind request is invalid")
		}
		decoded.rebind = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.OperationID
	case OperationHandoff:
		var command handoff.AuthorityRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil || request.Action != string(mission.ActionCandidateHandoff) {
			return decodedOperation{}, errors.New("handoff request is invalid")
		}
		decoded.handoff = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.OperationID
	case OperationReview:
		var command review.AuthorityRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil || request.Action != string(mission.ActionReviewRecord) {
			return decodedOperation{}, errors.New("review request is invalid")
		}
		decoded.review = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.OperationID
	case OperationAcceptance:
		var command integration.AcceptanceAuthorityRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil || request.Action != string(mission.ActionAcceptanceEvaluate) {
			return decodedOperation{}, errors.New("acceptance request is invalid")
		}
		decoded.acceptance = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.OperationID
	case OperationIntegration:
		var command integration.IntegrationAuthorityRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil || request.Action != string(mission.ActionIntegrationPublish) {
			return decodedOperation{}, errors.New("integration request is invalid")
		}
		decoded.integration = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.OperationID
	case OperationIntegrationCleanup:
		var command integration.IntegrationCleanupRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil || request.Action != string(mission.ActionIntegrationPublish) {
			return decodedOperation{}, errors.New("integration cleanup request is invalid")
		}
		decoded.cleanup = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.ProjectID), journalv2.MissionID(command.MissionID), command.CleanupOperationID
	case OperationEffect:
		var command execution.EffectAuthorityRequestV2
		if decodeErr := decodeStructWire(canonical, &command); decodeErr != nil {
			return decodedOperation{}, fmt.Errorf("effect request is invalid: %w", decodeErr)
		}
		if request.Action != string(mission.ActionEffectDispatch) && request.Action != string(mission.ActionEffectReconcile) {
			return decodedOperation{}, errors.New("effect request is invalid")
		}
		decoded.effect = &command
		decoded.projectID, decoded.missionID, decoded.operation = journalv2.ProjectID(command.Intent.Target.ProjectID), journalv2.MissionID(command.ExecutionSlot.MissionID), command.Intent.OperationID
	default:
		return decodedOperation{}, fmt.Errorf("unsupported operation kind %q", request.Kind)
	}
	if expected := decodedScope(decoded); expected == "" || expected != request.Scope {
		return decodedOperation{}, errors.New("operation scope does not match typed payload")
	}
	return decoded, nil
}

func decodedScope(decoded decodedOperation) string {
	if decoded.mission != nil {
		return "mission:" + string(decoded.mission.MissionID)
	}
	if decoded.readiness == nil {
		if decoded.execution != nil {
			return "work:" + decoded.execution.WorkID.String()
		}
		switch {
		case decoded.rebind != nil:
			return "work:" + decoded.rebind.WorkID.String()
		case decoded.handoff != nil:
			return "work:" + decoded.handoff.WorkID.String()
		case decoded.review != nil:
			return "work:" + decoded.review.WorkID.String()
		case decoded.acceptance != nil:
			return "mission:" + decoded.acceptance.MissionID.String()
		case decoded.integration != nil:
			return "mission:" + decoded.integration.MissionID.String()
		case decoded.cleanup != nil:
			return "mission:" + decoded.cleanup.MissionID.String()
		case decoded.effect != nil:
			return "action:" + decoded.request.Action
		}
		return ""
	}
	command := decoded.readiness
	if command.Activity != nil {
		return string(command.Activity.Action.Scope.Kind) + ":" + command.Activity.Action.Scope.Identity
	}
	if command.RelatedFact != nil {
		for _, state := range []mission.ReadinessAuthorityStateV2{command.Expected, command.Updated} {
			for _, fact := range state.ActiveFacts() {
				if fact.FactID == command.RelatedFact.Successor.FactID() {
					return string(fact.Scope.Scope.Kind) + ":" + fact.Scope.Scope.Identity
				}
			}
		}
	}
	return "mission:" + string(command.MissionID)
}

func decodeStructWire(data []byte, target any) error {
	typeOf := reflect.TypeOf(target)
	if typeOf.Kind() != reflect.Pointer || typeOf.Elem().Kind() != reflect.Struct {
		return errors.New("typed request target must be a struct pointer")
	}
	var source map[string]json.RawMessage
	if err := decodeStrict(data, &source); err != nil {
		return err
	}
	fields := make(map[string]string, typeOf.Elem().NumField())
	for index := 0; index < typeOf.Elem().NumField(); index++ {
		field := typeOf.Elem().Field(index)
		if !field.IsExported() {
			continue
		}
		fields[toSnakeCase(field.Name)] = field.Name
	}
	converted := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		name, ok := fields[key]
		if !ok {
			return fmt.Errorf("unknown typed request field %q", key)
		}
		converted[name] = value
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return err
	}
	return decodeStrict(encoded, target)
}

func toSnakeCase(value string) string {
	var result strings.Builder
	runes := []rune(value)
	for index, current := range runes {
		if current >= 'A' && current <= 'Z' {
			priorLowerOrDigit := index > 0 && (runes[index-1] >= 'a' && runes[index-1] <= 'z' || runes[index-1] >= '0' && runes[index-1] <= '9')
			nextLower := index+1 < len(runes) && runes[index+1] >= 'a' && runes[index+1] <= 'z'
			if index > 0 && (priorLowerOrDigit || nextLower) {
				result.WriteByte('_')
			}
			result.WriteByte(byte(current - 'A' + 'a'))
		} else {
			result.WriteRune(current)
		}
	}
	return result.String()
}

func decodeExecutionCommand(action string, payload []byte) (execution.ExecutionAuthorityRequestV2, error) {
	var wire executionCommandWire
	if err := decodeStrict(payload, &wire); err != nil {
		return execution.ExecutionAuthorityRequestV2{}, err
	}
	command := execution.ExecutionAuthorityRequestV2{
		Kind: wire.Kind, OperationID: wire.OperationID, ProjectID: wire.ProjectID, MissionID: wire.MissionID,
		WorkRevisionID: wire.WorkRevisionID, WorkID: wire.WorkID, RunID: wire.RunID, ActorID: wire.ActorID,
		ProcessID: wire.ProcessID, LeaseID: wire.LeaseID, Generation: wire.Generation, Fence: wire.Fence,
		ObservedAt: wire.ObservedAt, Deadline: wire.Deadline, RuntimeEpochID: wire.RuntimeEpochID, Outcome: wire.Outcome,
		Governance: wire.Governance, OperationEventID: wire.OperationEventID, SlotEventID: wire.SlotEventID,
	}
	if executionAction(command.Kind) != action {
		return execution.ExecutionAuthorityRequestV2{}, errors.New("execution action and command kind do not match")
	}
	return command, nil
}

func executionAction(kind execution.ExecutionCommandKindV2) string {
	switch kind {
	case execution.ExecutionAdmitV2, execution.ExecutionReplaceV2, execution.ExecutionRebindV2:
		return string(mission.ActionExecutionClaim)
	case execution.ExecutionHeartbeatV2:
		return string(mission.ActionExecutionHeartbeat)
	case execution.ExecutionExpireV2:
		return string(mission.ActionWorkspaceQuarantine)
	case execution.ExecutionOutcomeV2:
		return string(mission.ActionExecutionFinish)
	default:
		return ""
	}
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
	if wire.SchemaVersion != mission.ReadinessAuthorityStateSchemaV2 {
		return mission.ReadinessAuthorityCommandV2{}, errors.New("readiness schema version is invalid")
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
	var selection *mission.WorkRevisionSelectionV2
	if len(wire.WorkRevision) != 0 {
		eventBytes, encodeErr := journalv2.CanonicalObject(mustJSON(map[string]any{
			"schema_version": mission.ReadinessAuthorityStateSchemaV2, "event_type": wire.EventType,
			"prior": expected.CanonicalBytes(), "next": updated.CanonicalBytes(), "work_revision_selection": json.RawMessage(wire.WorkRevision),
		}))
		if encodeErr != nil {
			return mission.ReadinessAuthorityCommandV2{}, encodeErr
		}
		decodedEvent, decodeErr := mission.DecodeReadinessAuthorityEventV2(eventBytes)
		if decodeErr != nil {
			return mission.ReadinessAuthorityCommandV2{}, decodeErr
		}
		selection = decodedEvent.WorkRevisionSelection()
	}
	var relatedFact *mission.MissionFactTransitionV2
	if wire.RelatedFact != nil {
		var prior mission.MissionFactStateV2
		if len(wire.RelatedFact.Prior) != 0 {
			prior, err = mission.DecodeMissionFactStateV2(wire.RelatedFact.Prior)
			if err != nil {
				return mission.ReadinessAuthorityCommandV2{}, err
			}
		}
		successor, decodeErr := mission.DecodeMissionFactStateV2(wire.RelatedFact.Successor)
		if decodeErr != nil {
			return mission.ReadinessAuthorityCommandV2{}, decodeErr
		}
		relatedFact = &mission.MissionFactTransitionV2{Prior: prior, Successor: successor, ExpectedRecordRevision: wire.RelatedFact.ExpectedRecordRevision}
	}
	command := mission.ReadinessAuthorityCommandV2{
		SchemaVersion: wire.SchemaVersion, OperationID: wire.OperationID, ProjectID: wire.ProjectID, MissionID: wire.MissionID,
		ActorID: wire.ActorID, ProcessID: wire.ProcessID, OriginatingRuntimeEpochID: wire.RuntimeEpochID,
		DoctrineRevision: wire.DoctrineRevision, ProfileRevision: wire.ProfileRevision, PolicyRevision: wire.PolicyRevision,
		ObservedAt: wire.ObservedAt, EventType: wire.EventType, Expected: expected, Updated: updated,
		Activity: wire.Activity, WorkRevisionSelection: selection, RelatedFact: relatedFact, ObservationEvidence: evidence,
	}
	if readinessCommandAction(command) != action {
		return mission.ReadinessAuthorityCommandV2{}, errors.New("readiness action and event type do not match")
	}
	return command, nil
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
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

func readinessCommandAction(command mission.ReadinessAuthorityCommandV2) string {
	if action := readinessAction(command.EventType); action != "" {
		return action
	}
	switch command.EventType {
	case mission.ReadinessEventActivityRecordedV2:
		if command.Activity != nil {
			return string(command.Activity.Action.ID)
		}
	case mission.ReadinessEventWorkRevisionSelectedV2:
		if command.WorkRevisionSelection != nil && command.WorkRevisionSelection.Head().Generation() == 1 {
			return string(mission.ActionWorkFinalize)
		}
		return string(mission.ActionWorkReconcile)
	case mission.ReadinessEventFactOpenedV2, mission.ReadinessEventFactClosedV2:
		if command.RelatedFact != nil {
			switch command.RelatedFact.Successor.Kind() {
			case mission.MissionFactBlockerV2:
				return string(mission.ActionBlockerClear)
			case mission.MissionFactDecisionV2:
				return string(mission.ActionDecisionResolve)
			}
		}
	}
	return ""
}
