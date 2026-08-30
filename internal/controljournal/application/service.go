package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// OutcomeKind is the stable application-level result vocabulary.
type OutcomeKind string

const (
	OutcomeCommitted                  OutcomeKind = "committed"
	OutcomeAlreadyCommitted           OutcomeKind = "already_committed"
	OutcomeLogicalVersionConflict     OutcomeKind = "logical_version_conflict"
	OutcomeHeadRevisionConflict       OutcomeKind = "head_record_revision_conflict"
	OutcomeRuntimeEpochConflict       OutcomeKind = "runtime_epoch_conflict"
	OutcomeOperationIdentityCollision OutcomeKind = "operation_identity_collision"
	OutcomeAmbiguousResponse          OutcomeKind = "ambiguous_response"
	OutcomeProvenAbsent               OutcomeKind = "proven_absent"
	OutcomeIndeterminate              OutcomeKind = "indeterminate"
	OutcomeUnsupportedVersion         OutcomeKind = "unsupported_version"
	OutcomeBudgetExceeded             OutcomeKind = "budget_exceeded"
	OutcomeInvalidCheckpoint          OutcomeKind = "invalid_checkpoint"
	OutcomeRefused                    OutcomeKind = "refused"
)

type RecoveryAction string

const (
	RecoveryNone                   RecoveryAction = "none"
	RecoveryRetrySameOperation     RecoveryAction = "retry_same_operation"
	RecoveryReconcileSameOperation RecoveryAction = "reconcile_same_operation"
	RecoveryOperator               RecoveryAction = "operator"
)

// Error contains bounded, payload-free diagnostics.
type Error struct {
	Kind     OutcomeKind
	Recovery RecoveryAction
	Detail   string
	Cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "control journal application: " + string(e.Kind)
	if e.Recovery != "" && e.Recovery != RecoveryNone {
		message += " recovery=" + string(e.Recovery)
	}
	if e.Detail != "" {
		message += " detail=" + bounded(e.Detail, 512)
	}
	return bounded(message, 1024)
}

func (e *Error) Unwrap() error { return e.Cause }

func refusal(kind OutcomeKind, detail string) *Error {
	return &Error{Kind: kind, Recovery: RecoveryOperator, Detail: detail}
}

// ResultV1 represents applied, conflicting, refused, and ambiguous outcomes.
// CommittedRevision is populated only after acknowledgement or complete
// read-side proof.
type ResultV1 struct {
	Kind              OutcomeKind
	OperationID       controljournal.OperationID
	CommittedRevision uint64
	ResultType        string
	CanonicalResult   []byte
	Recovery          RecoveryAction
}

// publicationPort is intentionally narrower than the full adapter surface.
// A command can make exactly one mutation call and perform ordinary reads.
type publicationPort interface {
	Get(context.Context, string) (storegapdb.Record, error)
	PreflightAtomicBatch(storegapdb.ProductBatch) (storegapdb.BatchMeasurement, error)
	AtomicBatch(context.Context, storegapdb.ProductBatch) (storegapdb.MutationResult, error)
}

type epochAdmissionPort interface {
	Get(context.Context, string) (storegapdb.Record, error)
	AdmitRuntimeEpoch(context.Context, storegapdb.RuntimeEpochBatch) (storegapdb.MutationResult, error)
}

// EpochAdmissionV1 proves that startup durably installed one explicit epoch.
type EpochAdmissionV1 struct {
	seal      [32]byte
	projectID controljournal.ProjectID
	missionID controljournal.MissionID
	epochID   controljournal.RuntimeEpochID
	revision  uint64
}

func (a EpochAdmissionV1) valid() bool {
	if a.revision == 0 || a.projectID == "" || a.missionID == "" || a.epochID == "" {
		return false
	}
	return a.seal == epochAdmissionDigest(a.projectID, a.missionID, a.epochID, a.revision)
}

func (a EpochAdmissionV1) RuntimeEpochID() (controljournal.RuntimeEpochID, error) {
	if !a.valid() {
		return "", refusal(OutcomeRefused, "runtime epoch admission capability is invalid")
	}
	return a.epochID, nil
}

type EpochAdmissionInputV1 struct {
	ProjectID    controljournal.ProjectID
	MissionID    controljournal.MissionID
	OperationID  controljournal.OperationID
	EventID      controljournal.EventID
	StartupProof sidecargapdb.OwnerStartupProofV1
}

// epochAdmissionRequestV1 is an internal test seam. Production can obtain the
// Startup facts only from the sealed sidecar proof in EpochAdmissionInputV1.
type epochAdmissionRequestV1 struct {
	ProjectID   controljournal.ProjectID
	MissionID   controljournal.MissionID
	OperationID controljournal.OperationID
	EventID     controljournal.EventID
	Startup     sidecargapdb.OwnerStartupFactsV1
}

type epochAdmissionWireV1 struct {
	DatabaseID                string `json:"database_id"`
	HostInstanceID            string `json:"host_instance_id"`
	OwnerPIDObservation       int    `json:"owner_pid_observation"`
	OwnerStartedAtObservation string `json:"owner_started_at_observation"`
	PredecessorRuntimeEpochID string `json:"predecessor_epoch_id,omitempty"`
	RuntimeEpochID            string `json:"runtime_epoch_id"`
	SchemaVersion             uint32 `json:"schema_version"`
	SidecarPath               string `json:"sidecar_path"`
	SidecarSHA256             string `json:"sidecar_sha256"`
	StartedAtObservation      string `json:"started_at_observation"`
	Status                    string `json:"status"`
}

type runtimeCurrentWireV1 struct {
	CreatedOperationID        string `json:"created_operation_id"`
	HostInstanceID            string `json:"host_instance_id"`
	PredecessorRuntimeEpochID string `json:"predecessor_runtime_epoch_id,omitempty"`
	RuntimeEpochID            string `json:"runtime_epoch_id"`
	SchemaVersion             uint32 `json:"schema_version"`
	Status                    string `json:"status"`
}

// AdmitRuntimeEpochV1 is the owner-startup-only mutation. It atomically inserts
// a fresh immutable epoch and replaces (or creates) runtime/current. Ordinary
// Service.Apply never includes either key.
func AdmitRuntimeEpochV1(ctx context.Context, port epochAdmissionPort, input EpochAdmissionInputV1) (EpochAdmissionV1, ResultV1, error) {
	facts, err := input.StartupProof.Facts()
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "owner startup proof is absent or invalid")
	}
	return admitRuntimeEpochFactsV1(ctx, port, epochAdmissionRequestV1{
		ProjectID: input.ProjectID, MissionID: input.MissionID, OperationID: input.OperationID,
		EventID: input.EventID, Startup: facts,
	})
}

func admitRuntimeEpochFactsV1(ctx context.Context, port epochAdmissionPort, input epochAdmissionRequestV1) (EpochAdmissionV1, ResultV1, error) {
	if port == nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "publication port is absent")
	}
	if _, err := controljournal.NewProjectID(string(input.ProjectID)); err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "project identity is invalid")
	}
	if _, err := controljournal.NewMissionID(string(input.MissionID)); err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "mission identity is invalid")
	}
	if _, err := controljournal.NewOperationID(string(input.OperationID)); err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "operation identity is invalid")
	}
	if _, err := controljournal.NewEventID(string(input.EventID)); err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "event identity is invalid")
	}
	facts := input.Startup
	if _, err := controljournal.NewRuntimeEpochID(string(facts.RuntimeEpochID)); err != nil ||
		!validToken(facts.HostInstanceID) || !validToken(facts.DatabaseID) || facts.Daemon.PID <= 0 || facts.Daemon.StartTicks == 0 ||
		facts.AdmittedAt == "" || facts.DaemonOwnerStartedAt == "" || facts.ExecutablePath == "" || !validSHA256(facts.ExecutableSHA256) {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch provenance is incomplete")
	}
	prefix := canonicalPrefix(input.ProjectID, input.MissionID)
	operationKey := prefix + "operations/" + string(input.OperationID)
	if stored, readErr := port.Get(ctx, operationKey); readErr == nil {
		return reconcileExistingEpochAdmission(ctx, port, input, stored)
	} else if !storegapdb.IsKind(readErr, storegapdb.OutcomeNotFound) {
		return EpochAdmissionV1{}, mapReadError(input.OperationID, readErr), nil
	}
	expectedHead, currentRevision, predecessor, predecessorResult := readRuntimePredecessor(ctx, port, input, prefix)
	if predecessorResult.Kind != "" {
		if predecessorResult.Kind == OutcomeAlreadyCommitted {
			return reconcileCurrentEpochAdmission(ctx, port, input, predecessorResult.OperationID)
		}
		return EpochAdmissionV1{}, predecessorResult, nil
	}
	epochWire := epochAdmissionWireV1{
		SchemaVersion: 1, RuntimeEpochID: string(facts.RuntimeEpochID), PredecessorRuntimeEpochID: string(predecessor),
		HostInstanceID: facts.HostInstanceID, StartedAtObservation: facts.AdmittedAt,
		SidecarPath: facts.ExecutablePath, SidecarSHA256: facts.ExecutableSHA256, DatabaseID: facts.DatabaseID,
		OwnerPIDObservation: facts.Daemon.PID, OwnerStartedAtObservation: facts.DaemonOwnerStartedAt,
		Status: "recovering",
	}
	epochValue, err := json.Marshal(epochWire)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch record cannot be encoded")
	}
	currentValue, err := json.Marshal(runtimeCurrentWireV1{
		SchemaVersion: 1, RuntimeEpochID: string(facts.RuntimeEpochID), PredecessorRuntimeEpochID: string(predecessor),
		HostInstanceID: facts.HostInstanceID, CreatedOperationID: string(input.OperationID), Status: "recovering",
	})
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime current binding cannot be encoded")
	}
	eventIndex, _ := controljournal.NewEventIndexV1(0)
	eventCount, _ := controljournal.NewEventCountV1(1)
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: input.EventID, EventType: "runtime.epoch_admitted", EventSchemaVersion: 1,
		ReducerVersion: "runtime-v1", ProjectID: input.ProjectID, AggregateKind: "runtime", AggregateID: "host-runtime",
		AggregateLogicalVersion: expectedHead.LogicalVersion + 1, OperationID: input.OperationID,
		RuntimeEpochID: facts.RuntimeEpochID, EventIndex: eventIndex, EventCount: eventCount, Payload: epochValue,
	})
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch event is invalid")
	}
	eventValue, err := controljournal.EncodeEventEnvelopeV1(event)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch event cannot be encoded")
	}
	head, err := controljournal.ConstructAggregateHeadV1("runtime", "host-runtime", expectedHead.LogicalVersion+1, facts.RuntimeEpochID, input.EventID, sha256.Sum256(currentValue))
	if err != nil || controljournal.ValidateHeadTransition(expectedHead, controljournal.NewAggregateHeadV1{Head: head}) != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime aggregate transition is invalid")
	}
	headValue, err := controljournal.EncodeAggregateHeadV1(head)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime aggregate head cannot be encoded")
	}
	preconditionsDigest := sha256.Sum256([]byte(fmt.Sprintf("runtime-admission-v1\x00%d\x00%d\x00%d\x00%s", expectedHead.LogicalVersion, expectedHead.ObservedHeadRevision, currentRevision, predecessor)))
	requestDigest := sha256.Sum256(epochValue)
	operation, err := controljournal.NewOperationRecordV1(controljournal.OperationRecordInputV1{
		OperationID: input.OperationID, CommandType: "runtime.admit", RequestDigest: requestDigest,
		RuntimeEpochID: facts.RuntimeEpochID, ExpectedPreconditionsDigest: preconditionsDigest,
		ResultType: "runtime.epoch_admitted", ResultSchemaVersion: 1, CanonicalResult: currentValue,
		EventIDs: []controljournal.EventID{input.EventID},
	})
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime operation record is invalid")
	}
	operationValue, err := controljournal.EncodeOperationRecordV1(operation)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime operation cannot be encoded")
	}
	currentCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	if currentRevision > 0 {
		currentCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: currentRevision}
	}
	headCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	if expectedHead.ObservedHeadRevision > 0 {
		headCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: expectedHead.ObservedHeadRevision}
	}
	batch := storegapdb.RuntimeEpochBatch{
		OperationID:    string(input.OperationID),
		Operation:      absentPut(prefix+"operations/"+string(input.OperationID), operationValue),
		Event:          absentPut(prefix+"journal/"+string(input.EventID), eventValue),
		AggregateHead:  storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: prefix + "heads/runtime/host-runtime", Condition: headCondition, Value: headValue},
		EpochRecord:    absentPut(prefix+"runtime/epochs/"+string(facts.RuntimeEpochID), epochValue),
		CurrentBinding: storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: prefix + "runtime/current", Condition: currentCondition, Value: currentValue},
	}
	result, err := port.AdmitRuntimeEpoch(ctx, batch)
	if err != nil {
		mapped := mapMutationError(input.OperationID, err)
		return EpochAdmissionV1{}, mapped, nil
	}
	if result.Ack != storegapdb.AckDurable || result.Revision == 0 || result.DurableThrough != result.Revision || result.MutationCount != 5 {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeIndeterminate, "runtime epoch acknowledgement is malformed")
	}
	admission := newEpochAdmission(input.ProjectID, input.MissionID, facts.RuntimeEpochID, result.Revision)
	return admission, ResultV1{Kind: OutcomeCommitted, OperationID: input.OperationID, CommittedRevision: result.Revision, Recovery: RecoveryNone}, nil
}

func reconcileCurrentEpochAdmission(ctx context.Context, port epochAdmissionPort, input epochAdmissionRequestV1, operationID controljournal.OperationID) (EpochAdmissionV1, ResultV1, error) {
	prefix := canonicalPrefix(input.ProjectID, input.MissionID)
	operationRecord, err := port.Get(ctx, prefix+"operations/"+string(operationID))
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: operationID, Recovery: RecoveryOperator}, nil
	}
	operation, err := controljournal.DecodeOperationRecordV1(operationRecord.Value)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: operationID, Recovery: RecoveryOperator}, nil
	}
	eventIDs := operation.EventIDs()
	if len(eventIDs) != 1 {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: operationID, Recovery: RecoveryOperator}, nil
	}
	observed := input
	observed.OperationID = operationID
	observed.EventID = eventIDs[0]
	return reconcileExistingEpochAdmission(ctx, port, observed, operationRecord)
}

func readRuntimePredecessor(ctx context.Context, port epochAdmissionPort, input epochAdmissionRequestV1, prefix string) (controljournal.ExpectedAggregateHeadV1, uint64, controljournal.RuntimeEpochID, ResultV1) {
	currentKey, headKey := prefix+"runtime/current", prefix+"heads/runtime/host-runtime"
	currentRecord, currentErr := port.Get(ctx, currentKey)
	headRecord, headErr := port.Get(ctx, headKey)
	currentAbsent := storegapdb.IsKind(currentErr, storegapdb.OutcomeNotFound)
	headAbsent := storegapdb.IsKind(headErr, storegapdb.OutcomeNotFound)
	if currentAbsent && headAbsent {
		return controljournal.ExpectedAggregateHeadV1{AggregateKind: "runtime", AggregateID: "host-runtime"}, 0, "", ResultV1{}
	}
	if currentErr != nil || headErr != nil || currentRecord.Key != currentKey || headRecord.Key != headKey || currentRecord.Revision == 0 || headRecord.Revision == 0 || currentRecord.Revision != headRecord.Revision {
		return controljournal.ExpectedAggregateHeadV1{}, 0, "", ResultV1{Kind: OutcomeRefused, OperationID: input.OperationID, Recovery: RecoveryOperator}
	}
	current, err := decodeRuntimeCurrentV1(currentRecord.Value)
	if err != nil {
		return controljournal.ExpectedAggregateHeadV1{}, 0, "", ResultV1{Kind: OutcomeRefused, OperationID: input.OperationID, Recovery: RecoveryOperator}
	}
	head, err := controljournal.DecodeAggregateHeadV1(headRecord.Value)
	if err != nil || head.AggregateKind != "runtime" || head.AggregateID != "host-runtime" || head.LogicalVersion == 0 ||
		head.RuntimeEpochID != controljournal.RuntimeEpochID(current.RuntimeEpochID) || head.StateDigest != sha256.Sum256(currentRecord.Value) {
		return controljournal.ExpectedAggregateHeadV1{}, 0, "", ResultV1{Kind: OutcomeRefused, OperationID: input.OperationID, Recovery: RecoveryOperator}
	}
	if controljournal.RuntimeEpochID(current.RuntimeEpochID) == input.Startup.RuntimeEpochID {
		return controljournal.ExpectedAggregateHeadV1{}, 0, "", ResultV1{Kind: OutcomeAlreadyCommitted, OperationID: controljournal.OperationID(current.CreatedOperationID), CommittedRevision: currentRecord.Revision, Recovery: RecoveryNone}
	}
	expected := controljournal.ExpectedAggregateHeadV1{AggregateKind: "runtime", AggregateID: "host-runtime", LogicalVersion: head.LogicalVersion, ObservedHeadRevision: headRecord.Revision}
	return expected, currentRecord.Revision, controljournal.RuntimeEpochID(current.RuntimeEpochID), ResultV1{}
}

func reconcileExistingEpochAdmission(ctx context.Context, port epochAdmissionPort, input epochAdmissionRequestV1, operationRecord storegapdb.Record) (EpochAdmissionV1, ResultV1, error) {
	prefix := canonicalPrefix(input.ProjectID, input.MissionID)
	operation, err := controljournal.DecodeOperationRecordV1(operationRecord.Value)
	if err != nil || operationRecord.Key != prefix+"operations/"+string(input.OperationID) || operationRecord.Revision == 0 || operation.OperationID != input.OperationID || operation.CommandType != "runtime.admit" ||
		operation.RuntimeEpochID != input.Startup.RuntimeEpochID || operation.ResultType != "runtime.epoch_admitted" || len(operation.EventIDs()) != 1 || operation.EventIDs()[0] != input.EventID || len(operation.EffectIDs()) != 0 {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeOperationIdentityCollision, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
	}
	current, err := decodeRuntimeCurrentV1(operation.CanonicalResult())
	if err != nil || current.RuntimeEpochID != string(input.Startup.RuntimeEpochID) || current.HostInstanceID != input.Startup.HostInstanceID || current.CreatedOperationID != string(input.OperationID) {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeOperationIdentityCollision, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
	}
	eventKey := prefix + "journal/" + string(input.EventID)
	headKey := prefix + "heads/runtime/host-runtime"
	epochKey := prefix + "runtime/epochs/" + string(input.Startup.RuntimeEpochID)
	currentKey := prefix + "runtime/current"
	eventRecord, eventErr := port.Get(ctx, eventKey)
	headRecord, headErr := port.Get(ctx, headKey)
	epochRecord, epochErr := port.Get(ctx, epochKey)
	currentRecord, currentErr := port.Get(ctx, currentKey)
	for _, observed := range []struct {
		record storegapdb.Record
		key    string
		err    error
	}{{eventRecord, eventKey, eventErr}, {headRecord, headKey, headErr}, {epochRecord, epochKey, epochErr}, {currentRecord, currentKey, currentErr}} {
		if observed.err != nil || observed.record.Key != observed.key || observed.record.Revision != operationRecord.Revision {
			return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
		}
	}
	if !bytes.Equal(currentRecord.Value, operation.CanonicalResult()) {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
	}
	epoch, err := decodeRuntimeEpochV1(epochRecord.Value)
	if err != nil || epoch.RuntimeEpochID != string(input.Startup.RuntimeEpochID) || epoch.PredecessorRuntimeEpochID != current.PredecessorRuntimeEpochID ||
		epoch.HostInstanceID != input.Startup.HostInstanceID || epoch.DatabaseID != input.Startup.DatabaseID || epoch.OwnerPIDObservation != input.Startup.Daemon.PID ||
		epoch.OwnerStartedAtObservation != input.Startup.DaemonOwnerStartedAt || epoch.SidecarPath != input.Startup.ExecutablePath || epoch.SidecarSHA256 != input.Startup.ExecutableSHA256 ||
		epoch.StartedAtObservation != input.Startup.AdmittedAt || operation.RequestDigest != sha256.Sum256(epochRecord.Value) {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeOperationIdentityCollision, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
	}
	event, err := controljournal.DecodeEventEnvelopeV1(eventRecord.Value)
	if err != nil || event.EventID != input.EventID || event.OperationID != input.OperationID || event.RuntimeEpochID != input.Startup.RuntimeEpochID ||
		event.AggregateKind != "runtime" || event.AggregateID != "host-runtime" || !bytes.Equal(event.Payload(), epochRecord.Value) {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
	}
	head, err := controljournal.DecodeAggregateHeadV1(headRecord.Value)
	if err != nil || head.AggregateKind != "runtime" || head.AggregateID != "host-runtime" || head.RuntimeEpochID != input.Startup.RuntimeEpochID ||
		head.LastEventID != input.EventID || head.LogicalVersion != event.AggregateLogicalVersion || head.StateDigest != sha256.Sum256(currentRecord.Value) {
		return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
	}
	admission := newEpochAdmission(input.ProjectID, input.MissionID, input.Startup.RuntimeEpochID, operationRecord.Revision)
	return admission, ResultV1{Kind: OutcomeAlreadyCommitted, OperationID: input.OperationID, CommittedRevision: operationRecord.Revision, Recovery: RecoveryNone}, nil
}

func decodeRuntimeCurrentV1(encoded []byte) (runtimeCurrentWireV1, error) {
	var value runtimeCurrentWireV1
	if err := decodeCanonicalWireV1(encoded, &value); err != nil || value.SchemaVersion != 1 || !validToken(value.RuntimeEpochID) || !validToken(value.HostInstanceID) || !validToken(value.CreatedOperationID) || !validRuntimeStatus(value.Status) || value.PredecessorRuntimeEpochID != "" && !validToken(value.PredecessorRuntimeEpochID) {
		return runtimeCurrentWireV1{}, errors.New("runtime current binding is invalid")
	}
	return value, nil
}

func decodeRuntimeEpochV1(encoded []byte) (epochAdmissionWireV1, error) {
	var value epochAdmissionWireV1
	if err := decodeCanonicalWireV1(encoded, &value); err != nil || value.SchemaVersion != 1 || !validToken(value.RuntimeEpochID) || !validToken(value.HostInstanceID) || !validToken(value.DatabaseID) || value.OwnerPIDObservation <= 0 || value.StartedAtObservation == "" || value.OwnerStartedAtObservation == "" || value.SidecarPath == "" || !validSHA256(value.SidecarSHA256) || !validRuntimeStatus(value.Status) || value.PredecessorRuntimeEpochID != "" && !validToken(value.PredecessorRuntimeEpochID) {
		return epochAdmissionWireV1{}, errors.New("runtime epoch record is invalid")
	}
	return value, nil
}

func decodeCanonicalWireV1(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON contains trailing data")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return errors.New("JSON is not canonical")
	}
	return nil
}

func validRuntimeStatus(value string) bool {
	switch value {
	case "recovering", "reconciling", "ready", "degraded", "closed":
		return true
	default:
		return false
	}
}

func newEpochAdmission(project controljournal.ProjectID, mission controljournal.MissionID, epoch controljournal.RuntimeEpochID, revision uint64) EpochAdmissionV1 {
	admission := EpochAdmissionV1{projectID: project, missionID: mission, epochID: epoch, revision: revision}
	admission.seal = epochAdmissionDigest(project, mission, epoch, revision)
	return admission
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func epochAdmissionDigest(project controljournal.ProjectID, mission controljournal.MissionID, epoch controljournal.RuntimeEpochID, revision uint64) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("epoch-admission-v1\x00%s\x00%s\x00%s\x00%d", project, mission, epoch, revision)))
}

// Service is pinned to one admitted owner lifetime. It has no method that can
// advance the epoch; restart constructs a new service from a new admission.
type Service struct {
	port      publicationPort
	admission EpochAdmissionV1
}

func NewService(port publicationPort, admission EpochAdmissionV1) (*Service, error) {
	if port == nil || !admission.valid() {
		return nil, refusal(OutcomeRefused, "service requires a valid startup epoch admission")
	}
	return &Service{port: port, admission: admission}, nil
}

// Apply publishes a validated batch at most once. It performs no automatic
// mutation retry and never substitutes a new OperationID.
func (s *Service) Apply(ctx context.Context, batch ValidatedBatchV1) (ResultV1, error) {
	if s == nil || s.port == nil || !s.admission.valid() {
		return ResultV1{}, refusal(OutcomeRefused, "service is not admitted")
	}
	if !batch.valid() {
		return ResultV1{}, refusal(OutcomeRefused, "batch validation capability is invalid")
	}
	if batch.command.projectID != s.admission.projectID || batch.command.missionID != s.admission.missionID || batch.command.runtimeEpochID != s.admission.epochID {
		return ResultV1{Kind: OutcomeRuntimeEpochConflict, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	operationKey := batch.product.Operation.Key
	record, err := s.port.Get(ctx, operationKey)
	switch {
	case err == nil:
		return resultFromStoredOperation(batch, record)
	case !storegapdb.IsKind(err, storegapdb.OutcomeNotFound):
		return mapReadError(batch.command.operationID, err), nil
	}
	if preflight := s.preflightAggregateHeads(ctx, batch); preflight.Kind != "" {
		return preflight, nil
	}
	product, err := batch.ProductBatch()
	if err != nil {
		return ResultV1{}, err
	}
	operationRecord, err := controljournal.DecodeOperationRecordV1(batch.operationRecordBytes)
	if err != nil || !operationMatchesBatch(operationRecord, batch) {
		return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	measurement, err := s.port.PreflightAtomicBatch(product)
	if err != nil {
		kind := OutcomeRefused
		if storegapdb.IsKind(err, storegapdb.OutcomeInvalid) {
			kind = OutcomeBudgetExceeded
		}
		return ResultV1{Kind: kind, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	mutationCount := len(productMutations(product))
	if measurement.MutationCount != mutationCount || measurement.RawBytes <= 0 || measurement.WireBytes <= 0 {
		return ResultV1{Kind: OutcomeRefused, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	if measurement.RawBytes > storegapdb.MaxRawBatchBytes || measurement.WireBytes > storegapdb.MaxWireFrameBytes {
		return ResultV1{Kind: OutcomeBudgetExceeded, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	result, err := s.port.AtomicBatch(ctx, product)
	if err != nil {
		if storegapdb.IsKind(err, storegapdb.OutcomeConflict) {
			// A concurrent same-operation caller may have won. One ordinary read
			// distinguishes that idempotent case from a touched-head conflict.
			stored, readErr := s.port.Get(ctx, operationKey)
			if readErr == nil {
				return resultFromStoredOperation(batch, stored)
			}
			if readErr != nil && !storegapdb.IsKind(readErr, storegapdb.OutcomeNotFound) {
				return mapReadError(batch.command.operationID, readErr), nil
			}
			return s.classifyAggregateConflict(ctx, batch), nil
		}
		return mapMutationError(batch.command.operationID, err), nil
	}
	if result.Revision == 0 || result.Ack != storegapdb.AckDurable || result.DurableThrough != result.Revision || result.MutationCount != len(productMutations(product)) {
		return ResultV1{Kind: OutcomeIndeterminate, OperationID: batch.command.operationID, Recovery: RecoveryReconcileSameOperation}, nil
	}
	return ResultV1{
		Kind: OutcomeCommitted, OperationID: batch.command.operationID,
		CommittedRevision: result.Revision, ResultType: operationRecord.ResultType,
		CanonicalResult: operationRecord.CanonicalResult(), Recovery: RecoveryNone,
	}, nil
}

func (s *Service) preflightAggregateHeads(ctx context.Context, batch ValidatedBatchV1) ResultV1 {
	for index, expected := range batch.expectedHeads {
		mutation := batch.product.AggregateHeads[index]
		record, err := s.port.Get(ctx, mutation.Key)
		if expected.LogicalVersion == 0 {
			if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				continue
			}
			if err != nil {
				return mapReadError(batch.command.operationID, err)
			}
			if _, decodeErr := controljournal.DecodeAggregateHeadV1(record.Value); decodeErr != nil {
				return ResultV1{Kind: OutcomeRefused, OperationID: batch.command.operationID, Recovery: RecoveryOperator}
			}
			return ResultV1{Kind: OutcomeLogicalVersionConflict, OperationID: batch.command.operationID, Recovery: RecoveryRetrySameOperation}
		}
		if err != nil {
			if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				return ResultV1{Kind: OutcomeHeadRevisionConflict, OperationID: batch.command.operationID, Recovery: RecoveryRetrySameOperation}
			}
			return mapReadError(batch.command.operationID, err)
		}
		if record.Key != mutation.Key || record.Revision != expected.ObservedHeadRevision {
			return ResultV1{Kind: OutcomeHeadRevisionConflict, OperationID: batch.command.operationID, Recovery: RecoveryRetrySameOperation}
		}
		head, decodeErr := controljournal.DecodeAggregateHeadV1(record.Value)
		if decodeErr != nil || head.AggregateKind != expected.AggregateKind || head.AggregateID != expected.AggregateID {
			return ResultV1{Kind: OutcomeRefused, OperationID: batch.command.operationID, Recovery: RecoveryOperator}
		}
		if head.LogicalVersion != expected.LogicalVersion {
			return ResultV1{Kind: OutcomeLogicalVersionConflict, OperationID: batch.command.operationID, Recovery: RecoveryRetrySameOperation}
		}
	}
	return ResultV1{}
}

func (s *Service) classifyAggregateConflict(ctx context.Context, batch ValidatedBatchV1) ResultV1 {
	result := ResultV1{Kind: OutcomeRefused, OperationID: batch.command.operationID, Recovery: RecoveryOperator}
	for index, expected := range batch.expectedHeads {
		record, err := s.port.Get(ctx, batch.product.AggregateHeads[index].Key)
		if err != nil {
			if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				if expected.LogicalVersion != 0 {
					result.Kind = OutcomeHeadRevisionConflict
					result.Recovery = RecoveryRetrySameOperation
					return result
				}
				continue
			}
			result.Kind = OutcomeIndeterminate
			result.Recovery = RecoveryReconcileSameOperation
			return result
		}
		head, err := controljournal.DecodeAggregateHeadV1(record.Value)
		if err != nil {
			return result
		}
		if head.LogicalVersion != expected.LogicalVersion {
			result.Kind = OutcomeLogicalVersionConflict
			result.Recovery = RecoveryRetrySameOperation
			return result
		}
		if record.Revision != expected.ObservedHeadRevision {
			result.Kind = OutcomeHeadRevisionConflict
			result.Recovery = RecoveryRetrySameOperation
			return result
		}
	}
	return result
}

func resultFromStoredOperation(batch ValidatedBatchV1, stored storegapdb.Record) (ResultV1, error) {
	if stored.Key != batch.product.Operation.Key || stored.Revision == 0 {
		return ResultV1{}, refusal(OutcomeIndeterminate, "stored operation metadata is malformed")
	}
	record, err := controljournal.DecodeOperationRecordV1(stored.Value)
	if err != nil {
		return ResultV1{}, refusal(OutcomeIndeterminate, "stored operation record is malformed")
	}
	if !operationMatchesBatch(record, batch) {
		return ResultV1{Kind: OutcomeOperationIdentityCollision, OperationID: batch.command.operationID, Recovery: RecoveryOperator}, nil
	}
	return ResultV1{Kind: OutcomeAlreadyCommitted, OperationID: record.OperationID, CommittedRevision: stored.Revision, ResultType: record.ResultType, CanonicalResult: record.CanonicalResult(), Recovery: RecoveryNone}, nil
}

func operationMatchesBatch(record controljournal.OperationRecordV1, batch ValidatedBatchV1) bool {
	if record.OperationID != batch.command.operationID || record.CommandType != batch.command.commandType ||
		record.RequestDigest != batch.command.digest || record.RuntimeEpochID != batch.command.runtimeEpochID ||
		!bytes.Equal(mustOperationBytes(record), batch.operationRecordBytes) {
		return false
	}
	return true
}

func mustOperationBytes(record controljournal.OperationRecordV1) []byte {
	value, _ := controljournal.EncodeOperationRecordV1(record)
	return value
}

func mapMutationError(operationID controljournal.OperationID, err error) ResultV1 {
	switch {
	case storegapdb.IsKind(err, storegapdb.OutcomeAmbiguous), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ResultV1{Kind: OutcomeAmbiguousResponse, OperationID: operationID, Recovery: RecoveryReconcileSameOperation}
	case storegapdb.IsKind(err, storegapdb.OutcomeConflict):
		return ResultV1{Kind: OutcomeHeadRevisionConflict, OperationID: operationID, Recovery: RecoveryRetrySameOperation}
	default:
		return ResultV1{Kind: OutcomeRefused, OperationID: operationID, Recovery: RecoveryOperator}
	}
}

func mapReadError(operationID controljournal.OperationID, err error) ResultV1 {
	if storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
		return ResultV1{Kind: OutcomeProvenAbsent, OperationID: operationID, Recovery: RecoveryRetrySameOperation}
	}
	return ResultV1{Kind: OutcomeIndeterminate, OperationID: operationID, Recovery: RecoveryReconcileSameOperation}
}

func bounded(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
