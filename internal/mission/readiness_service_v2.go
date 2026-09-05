package mission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type ReadinessAuthorityObservationV2 struct {
	State          ReadinessAuthorityStateV2
	StoreRevision  uint64
	RecordRevision uint64
	LastEventID    journalv2.EventID
}

type ReadinessAuthorityPortV2 interface {
	ResolveOperation(context.Context, journalv2.OperationBindingV2) (OperationResolutionV2, error)
	ObserveReadinessAuthority(context.Context, journalv2.ProjectID, journalv2.MissionID) (ReadinessAuthorityObservationV2, error)
	PublishReadinessAuthority(context.Context, journalv2.SealedCommitPlanV2) (MissionPublicationV2, error)
}

type ReadinessAuthorityCommandV2 struct {
	SchemaVersion             uint32
	OperationID               journalv2.OperationID
	ProjectID                 journalv2.ProjectID
	MissionID                 journalv2.MissionID
	ActorID                   journalv2.ActorID
	ProcessID                 journalv2.ProcessID
	OriginatingRuntimeEpochID journalv2.RuntimeEpochID
	DoctrineRevision          string
	ProfileRevision           string
	PolicyRevision            string
	ObservedAt                string
	EventType                 ReadinessEventTypeV2
	Expected                  ReadinessAuthorityStateV2
	Updated                   ReadinessAuthorityStateV2
	Activity                  *MissionActivityV2
	WorkRevisionSelection     *WorkRevisionSelectionV2
	ObservationEvidence       []byte
}

type ReadinessAuthorityResultV2 struct {
	state         ReadinessAuthorityStateV2
	operationID   journalv2.OperationID
	publicationID journalv2.PublicationID
	canonical     []byte
	replayed      bool
}

func (r ReadinessAuthorityResultV2) State() ReadinessAuthorityStateV2   { return r.state }
func (r ReadinessAuthorityResultV2) OperationID() journalv2.OperationID { return r.operationID }
func (r ReadinessAuthorityResultV2) PublicationID() journalv2.PublicationID {
	return r.publicationID
}
func (r ReadinessAuthorityResultV2) CanonicalBytes() []byte { return bytes.Clone(r.canonical) }
func (r ReadinessAuthorityResultV2) Replayed() bool         { return r.replayed }

type ReadinessAuthorityServiceV2 struct {
	port     ReadinessAuthorityPortV2
	epoch    RuntimeEpochSourceV2
	registry journalv2.EventRegistryV2
}

func NewReadinessAuthorityServiceV2(port ReadinessAuthorityPortV2, epoch RuntimeEpochSourceV2, registry journalv2.EventRegistryV2) (*ReadinessAuthorityServiceV2, error) {
	if port == nil || epoch == nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "service", nil)
	}
	if _, err := registry.Resolve("mission", 1, 1); err != nil {
		return nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "registry", err)
	}
	return &ReadinessAuthorityServiceV2{port: port, epoch: epoch, registry: registry}, nil
}

func (s *ReadinessAuthorityServiceV2) Execute(ctx context.Context, command ReadinessAuthorityCommandV2) (ReadinessAuthorityResultV2, error) {
	binding, err := validateReadinessAuthorityCommandV2(command)
	if err != nil {
		return ReadinessAuthorityResultV2{}, err
	}
	resolution, err := s.port.ResolveOperation(ctx, binding)
	if err != nil {
		return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "operation_resolution", err)
	}
	switch resolution.Kind {
	case OperationExactV2:
		if resolution.State.Status() != journalv2.StatusResolved && resolution.State.Status() != journalv2.StatusRefused {
			return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "operation_in_progress", nil)
		}
		result, decodeErr := decodeReadinessAuthorityResultV2(resolution.State.Result(), resolution.State.LastPublicationID(), true)
		if decodeErr != nil || result.operationID != command.OperationID {
			return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "operation_result", decodeErr)
		}
		return result, nil
	case OperationConflictV2:
		return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityIdempotencyConflictV2, "operation_id", nil)
	case OperationAbsentV2:
	default:
		return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityCorruptV2, "operation_resolution", nil)
	}
	currentEpoch, err := s.epoch.CurrentRuntimeEpoch(ctx)
	if err != nil || currentEpoch != command.OriginatingRuntimeEpochID {
		return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityStaleEpochV2, "runtime_epoch", err)
	}
	var observed ReadinessAuthorityObservationV2
	if command.EventType != ReadinessEventCreatedV2 {
		observed, err = s.port.ObserveReadinessAuthority(ctx, command.ProjectID, command.MissionID)
		if err != nil {
			return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityNotFoundV2, "mission", err)
		}
		if observed.State.Digest() != command.Expected.Digest() {
			return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityVersionConflictV2, "expected_mission", nil)
		}
	} else if !command.Expected.IsZero() {
		return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "expected_mission", nil)
	}
	plan, resultBytes, err := buildReadinessAuthorityPlanV2(command, binding, observed, s.registry)
	if err != nil {
		return ReadinessAuthorityResultV2{}, err
	}
	publication, err := s.port.PublishReadinessAuthority(ctx, plan)
	if err != nil {
		var typed *MissionAuthorityErrorV2
		if errors.As(err, &typed) {
			return ReadinessAuthorityResultV2{}, err
		}
		return ReadinessAuthorityResultV2{}, missionAuthorityErrorV2(MissionAuthorityAmbiguousV2, "publication", err)
	}
	if err := publication.validate(plan); err != nil {
		return ReadinessAuthorityResultV2{}, err
	}
	return ReadinessAuthorityResultV2{state: command.Updated, operationID: command.OperationID, publicationID: plan.PublicationID(), canonical: resultBytes}, nil
}

func validateReadinessAuthorityCommandV2(command ReadinessAuthorityCommandV2) (journalv2.OperationBindingV2, error) {
	if command.SchemaVersion != ReadinessAuthorityStateSchemaV2 || !command.EventType.validate() || command.Updated.IsZero() || command.Updated.ProjectID() != command.ProjectID || command.Updated.MissionID() != command.MissionID || command.Updated.LastOperationID() != command.OperationID || !canonicalAuthorityTimeV2(command.ObservedAt) || command.Updated.ObservedAt() != command.ObservedAt || len(command.ObservationEvidence) > journalv2.MaxCanonicalPayloadBytes {
		return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "command", nil)
	}
	if command.EventType == ReadinessEventActivityRecordedV2 {
		if command.Activity == nil || !command.Activity.validate() {
			return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "activity", nil)
		}
	} else if command.Activity != nil {
		return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "activity", nil)
	}
	if command.EventType == ReadinessEventWorkRevisionSelectedV2 {
		if command.WorkRevisionSelection == nil || command.WorkRevisionSelection.IsZero() {
			return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_selection", nil)
		}
		if _, err := NewReadinessWorkRevisionEventV2(command.Expected, command.Updated, *command.WorkRevisionSelection); err != nil {
			return journalv2.OperationBindingV2{}, err
		}
	} else if command.WorkRevisionSelection != nil {
		return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "work_revision_selection", nil)
	}
	if len(command.ObservationEvidence) != 0 {
		canonical, canonicalErr := journalv2.CanonicalObject(command.ObservationEvidence)
		if canonicalErr != nil || !bytes.Equal(canonical, command.ObservationEvidence) {
			return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "observation_evidence", canonicalErr)
		}
	}
	if err := validateReadinessTransitionV2(command.EventType, command.Expected, command.Updated); err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	for _, identity := range []string{string(command.OperationID), string(command.ProjectID), string(command.MissionID), string(command.ActorID), string(command.ProcessID), string(command.OriginatingRuntimeEpochID), command.DoctrineRevision, command.ProfileRevision, command.PolicyRevision} {
		if !boundedAuthorityIdentityV2(identity) {
			return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "identity", nil)
		}
	}
	intent, err := encodeReadinessAuthorityIntentV2(command)
	if err != nil {
		return journalv2.OperationBindingV2{}, err
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: command.ProjectID, MissionID: command.MissionID}
	digest, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{CommandKind: "mission.readiness." + string(command.EventType), CommandSchemaVersion: command.SchemaVersion, Scope: scope, Command: intent})
	if err != nil {
		return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "intent", err)
	}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{
		OperationID: command.OperationID, CommandKind: "mission.readiness." + string(command.EventType), CommandSchemaVersion: command.SchemaVersion,
		CanonicalIntentDigest: digest, Scope: scope, ActorID: command.ActorID, ProcessID: command.ProcessID,
		DoctrineRevision: command.DoctrineRevision, ProfileRevision: command.ProfileRevision, PolicyRevision: command.PolicyRevision,
		OriginatingRuntimeEpochID: command.OriginatingRuntimeEpochID, ObservedCreatedAt: command.ObservedAt,
	})
	if err != nil {
		return journalv2.OperationBindingV2{}, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "binding", err)
	}
	return binding, nil
}

func encodeReadinessAuthorityIntentV2(command ReadinessAuthorityCommandV2) ([]byte, error) {
	encoded, err := json.Marshal(struct {
		SchemaVersion uint32               `json:"schema_version"`
		ProjectID     journalv2.ProjectID  `json:"project_id"`
		MissionID     journalv2.MissionID  `json:"mission_id"`
		EventType     ReadinessEventTypeV2 `json:"event_type"`
		Expected      json.RawMessage      `json:"expected,omitempty"`
		Updated       json.RawMessage      `json:"updated"`
		Activity      *MissionActivityV2   `json:"activity,omitempty"`
		WorkRevision  json.RawMessage      `json:"work_revision_selection,omitempty"`
		Evidence      json.RawMessage      `json:"observation_evidence,omitempty"`
	}{command.SchemaVersion, command.ProjectID, command.MissionID, command.EventType, command.Expected.CanonicalBytes(), command.Updated.CanonicalBytes(), command.Activity, readinessSelectionBytesV2(command.WorkRevisionSelection), command.ObservationEvidence})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func buildReadinessAuthorityPlanV2(command ReadinessAuthorityCommandV2, binding journalv2.OperationBindingV2, observed ReadinessAuthorityObservationV2, registry journalv2.EventRegistryV2) (journalv2.SealedCommitPlanV2, []byte, error) {
	resultBytes, err := encodeReadinessAuthorityResultV2(command.OperationID, command.Updated)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, nil, err
	}
	event, err := NewReadinessAuthorityEventV2(command.EventType, command.Expected, command.Updated)
	if command.Activity != nil {
		event, err = NewReadinessActivityEventV2(command.Expected, command.Updated, *command.Activity)
	}
	if command.WorkRevisionSelection != nil {
		event, err = NewReadinessWorkRevisionEventV2(command.Expected, command.Updated, *command.WorkRevisionSelection)
	}
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, nil, err
	}
	payload, _ := EncodeReadinessAuthorityEventV2(event)
	operationEventID := deterministicAuthorityIDV2[journalv2.EventID]("readiness-operation-event", command.OperationID, 1)
	missionEventID := deterministicAuthorityIDV2[journalv2.EventID]("readiness-mission-event", command.OperationID, 1)
	eventIDs := []journalv2.EventID{missionEventID, operationEventID}
	sort.Slice(eventIDs, func(i, j int) bool { return eventIDs[i] < eventIDs[j] })
	operation, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: command.OperationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "mission.readiness.result", ResultSchemaVersion: 2, CanonicalResult: resultBytes, EventIDs: eventIDs})
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "operation_state", err)
	}
	operationPayload, _ := journalv2.EncodeOperationStateEventPayloadV2(operation)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(command.OperationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	missionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(command.MissionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: command.ProjectID, MissionID: command.MissionID})
	opDigest, _ := journalv2.ReducerStateDigestV2(operationPayload)
	governance := []string{command.DoctrineRevision, command.ProfileRevision}
	sort.Strings(governance)
	drafts := []journalv2.EventDraftV2{
		{EventID: operationEventID, EventType: "operation_transitioned", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, ActorID: command.ActorID, ProcessID: command.ProcessID, PolicyRevision: command.PolicyRevision, GovernanceRevisions: governance, Payload: operationPayload},
		{EventID: missionEventID, EventType: string(command.EventType), Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: command.Updated.Version(), CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, ActorID: command.ActorID, ProcessID: command.ProcessID, WorkRevisionID: command.Updated.CurrentWorkRevisionID(), PolicyRevision: command.PolicyRevision, GovernanceRevisions: governance, Observations: command.ObservationEvidence, Payload: payload},
	}
	expectedVersion, recordRevision := journalv2.LogicalVersion(0), uint64(0)
	if command.EventType != ReadinessEventCreatedV2 {
		expectedVersion, recordRevision = command.Expected.Version(), observed.RecordRevision
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: operationEventID, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, CanonicalStateDigest: opDigest, AuthoritySummary: []byte(`{}`)},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: expectedVersion, NextLogicalVersion: command.Updated.Version(), EventID: missionEventID, ExpectedRecordRevision: recordRevision, CommittedRuntimeEpochID: command.OriginatingRuntimeEpochID, CanonicalStateDigest: command.Updated.Digest(), AuthoritySummary: command.Updated.CanonicalBytes(), Tombstone: command.Updated.Lifecycle().Terminal()},
	}
	conditions := make([]journalv2.ProviderCondition, len(heads))
	for index := range heads {
		conditions[index], err = journalv2.ConditionForHeadTransitionV2(heads[index])
		if err != nil {
			return journalv2.SealedCommitPlanV2{}, nil, err
		}
	}
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: operation, Events: drafts, Heads: heads, Conditions: conditions}, registry)
	if err != nil {
		return journalv2.SealedCommitPlanV2{}, nil, missionAuthorityErrorV2(MissionAuthorityInvalidRequestV2, "commit_plan", err)
	}
	return plan, resultBytes, nil
}

func readinessSelectionBytesV2(selection *WorkRevisionSelectionV2) []byte {
	if selection == nil {
		return nil
	}
	return selection.CanonicalBytes()
}

type readinessAuthorityResultWireV2 struct {
	SchemaVersion uint32                `json:"schema_version"`
	OperationID   journalv2.OperationID `json:"operation_id"`
	State         json.RawMessage       `json:"state"`
}

func encodeReadinessAuthorityResultV2(operationID journalv2.OperationID, state ReadinessAuthorityStateV2) ([]byte, error) {
	encoded, err := json.Marshal(readinessAuthorityResultWireV2{ReadinessAuthorityStateSchemaV2, operationID, state.CanonicalBytes()})
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(encoded)
}

func decodeReadinessAuthorityResultV2(encoded []byte, publicationID journalv2.PublicationID, replayed bool) (ReadinessAuthorityResultV2, error) {
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ReadinessAuthorityResultV2{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var wire readinessAuthorityResultWireV2
	if err := decoder.Decode(&wire); err != nil || wire.SchemaVersion != ReadinessAuthorityStateSchemaV2 {
		return ReadinessAuthorityResultV2{}, err
	}
	state, err := DecodeReadinessAuthorityStateV2(wire.State)
	if err != nil {
		return ReadinessAuthorityResultV2{}, err
	}
	return ReadinessAuthorityResultV2{state: state, operationID: wire.OperationID, publicationID: publicationID, canonical: bytes.Clone(encoded), replayed: replayed}, nil
}
