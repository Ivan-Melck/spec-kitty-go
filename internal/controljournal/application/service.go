package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
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
	ProjectID                 controljournal.ProjectID
	MissionID                 controljournal.MissionID
	OperationID               controljournal.OperationID
	EventID                   controljournal.EventID
	NewRuntimeEpochID         controljournal.RuntimeEpochID
	PreviousRuntimeEpochID    controljournal.RuntimeEpochID
	HostInstanceID            string
	StartedAtObservation      string
	SidecarPath               string
	SidecarSHA256             string
	DatabaseID                string
	OwnerPIDObservation       int
	OwnerStartedAtObservation string
	ExpectedRuntimeHead       controljournal.ExpectedAggregateHeadV1
	ObservedCurrentRevision   uint64
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
	if _, err := controljournal.NewRuntimeEpochID(string(input.NewRuntimeEpochID)); err != nil || input.NewRuntimeEpochID == input.PreviousRuntimeEpochID {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "new runtime epoch identity is invalid or reused")
	}
	if (input.PreviousRuntimeEpochID == "") != (input.ObservedCurrentRevision == 0) {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "previous epoch and current revision disagree")
	}
	if input.PreviousRuntimeEpochID != "" {
		if _, err := controljournal.NewRuntimeEpochID(string(input.PreviousRuntimeEpochID)); err != nil {
			return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "previous runtime epoch identity is invalid")
		}
	}
	if !validToken(input.HostInstanceID) || !validToken(input.DatabaseID) || input.OwnerPIDObservation <= 0 ||
		input.StartedAtObservation == "" || input.OwnerStartedAtObservation == "" || input.SidecarPath == "" || !validSHA256(input.SidecarSHA256) {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch provenance is incomplete")
	}
	if input.ExpectedRuntimeHead.AggregateKind != "runtime" || input.ExpectedRuntimeHead.AggregateID != "host-runtime" {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime aggregate coordinate is invalid")
	}
	epochWire := epochAdmissionWireV1{
		SchemaVersion: 1, RuntimeEpochID: string(input.NewRuntimeEpochID), PredecessorRuntimeEpochID: string(input.PreviousRuntimeEpochID),
		HostInstanceID: input.HostInstanceID, StartedAtObservation: input.StartedAtObservation,
		SidecarPath: input.SidecarPath, SidecarSHA256: input.SidecarSHA256, DatabaseID: input.DatabaseID,
		OwnerPIDObservation: input.OwnerPIDObservation, OwnerStartedAtObservation: input.OwnerStartedAtObservation,
		Status: "recovering",
	}
	epochValue, err := json.Marshal(epochWire)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch record cannot be encoded")
	}
	currentValue, err := json.Marshal(runtimeCurrentWireV1{
		SchemaVersion: 1, RuntimeEpochID: string(input.NewRuntimeEpochID), PredecessorRuntimeEpochID: string(input.PreviousRuntimeEpochID),
		HostInstanceID: input.HostInstanceID, CreatedOperationID: string(input.OperationID), Status: "recovering",
	})
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime current binding cannot be encoded")
	}
	eventIndex, _ := controljournal.NewEventIndexV1(0)
	eventCount, _ := controljournal.NewEventCountV1(1)
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: input.EventID, EventType: "runtime.epoch_admitted", EventSchemaVersion: 1,
		ReducerVersion: "runtime-v1", ProjectID: input.ProjectID, AggregateKind: "runtime", AggregateID: "host-runtime",
		AggregateLogicalVersion: input.ExpectedRuntimeHead.LogicalVersion + 1, OperationID: input.OperationID,
		RuntimeEpochID: input.NewRuntimeEpochID, EventIndex: eventIndex, EventCount: eventCount, Payload: epochValue,
	})
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch event is invalid")
	}
	eventValue, err := controljournal.EncodeEventEnvelopeV1(event)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime epoch event cannot be encoded")
	}
	head, err := controljournal.ConstructAggregateHeadV1("runtime", "host-runtime", input.ExpectedRuntimeHead.LogicalVersion+1, input.NewRuntimeEpochID, input.EventID, sha256.Sum256(currentValue))
	if err != nil || controljournal.ValidateHeadTransition(input.ExpectedRuntimeHead, controljournal.NewAggregateHeadV1{Head: head}) != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime aggregate transition is invalid")
	}
	headValue, err := controljournal.EncodeAggregateHeadV1(head)
	if err != nil {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeRefused, "runtime aggregate head cannot be encoded")
	}
	preconditionsDigest := sha256.Sum256([]byte(fmt.Sprintf("runtime-admission-v1\x00%d\x00%d\x00%s", input.ExpectedRuntimeHead.LogicalVersion, input.ExpectedRuntimeHead.ObservedHeadRevision, input.PreviousRuntimeEpochID)))
	requestDigest := sha256.Sum256(epochValue)
	operation, err := controljournal.NewOperationRecordV1(controljournal.OperationRecordInputV1{
		OperationID: input.OperationID, CommandType: "runtime.admit", RequestDigest: requestDigest,
		RuntimeEpochID: input.NewRuntimeEpochID, ExpectedPreconditionsDigest: preconditionsDigest,
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
	prefix := canonicalPrefix(input.ProjectID, input.MissionID)
	currentCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	if input.ObservedCurrentRevision > 0 {
		currentCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: input.ObservedCurrentRevision}
	}
	headCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	if input.ExpectedRuntimeHead.ObservedHeadRevision > 0 {
		headCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: input.ExpectedRuntimeHead.ObservedHeadRevision}
	}
	batch := storegapdb.RuntimeEpochBatch{
		OperationID:    string(input.OperationID),
		Operation:      absentPut(prefix+"operations/"+string(input.OperationID), operationValue),
		Event:          absentPut(prefix+"journal/"+string(input.EventID), eventValue),
		AggregateHead:  storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: prefix + "heads/runtime/host-runtime", Condition: headCondition, Value: headValue},
		EpochRecord:    absentPut(prefix+"runtime/epochs/"+string(input.NewRuntimeEpochID), epochValue),
		CurrentBinding: storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: prefix + "runtime/current", Condition: currentCondition, Value: currentValue},
	}
	if stored, readErr := port.Get(ctx, batch.Operation.Key); readErr == nil {
		return reconcileEpochAdmission(ctx, port, input, batch, stored)
	} else if !storegapdb.IsKind(readErr, storegapdb.OutcomeNotFound) {
		return EpochAdmissionV1{}, mapReadError(input.OperationID, readErr), nil
	}
	result, err := port.AdmitRuntimeEpoch(ctx, batch)
	if err != nil {
		mapped := mapMutationError(input.OperationID, err)
		return EpochAdmissionV1{}, mapped, nil
	}
	if result.Ack != storegapdb.AckDurable || result.Revision == 0 || result.DurableThrough != result.Revision || result.MutationCount != 5 {
		return EpochAdmissionV1{}, ResultV1{}, refusal(OutcomeIndeterminate, "runtime epoch acknowledgement is malformed")
	}
	admission := EpochAdmissionV1{projectID: input.ProjectID, missionID: input.MissionID, epochID: input.NewRuntimeEpochID, revision: result.Revision}
	admission.seal = epochAdmissionDigest(admission.projectID, admission.missionID, admission.epochID, admission.revision)
	return admission, ResultV1{Kind: OutcomeCommitted, OperationID: input.OperationID, CommittedRevision: result.Revision, Recovery: RecoveryNone}, nil
}

func reconcileEpochAdmission(ctx context.Context, port epochAdmissionPort, input EpochAdmissionInputV1, batch storegapdb.RuntimeEpochBatch, operation storegapdb.Record) (EpochAdmissionV1, ResultV1, error) {
	expected := []storegapdb.Mutation{batch.Operation, batch.Event, batch.AggregateHead, batch.EpochRecord, batch.CurrentBinding}
	for _, mutation := range expected {
		record := operation
		var err error
		if mutation.Key != batch.Operation.Key {
			record, err = port.Get(ctx, mutation.Key)
			if err != nil {
				return EpochAdmissionV1{}, ResultV1{Kind: OutcomeIndeterminate, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
			}
		}
		if record.Revision != operation.Revision || record.Key != mutation.Key || !bytes.Equal(record.Value, mutation.Value) {
			kind := OutcomeIndeterminate
			if mutation.Key == batch.Operation.Key {
				kind = OutcomeOperationIdentityCollision
			}
			return EpochAdmissionV1{}, ResultV1{Kind: kind, OperationID: input.OperationID, Recovery: RecoveryOperator}, nil
		}
	}
	admission := EpochAdmissionV1{projectID: input.ProjectID, missionID: input.MissionID, epochID: input.NewRuntimeEpochID, revision: operation.Revision}
	admission.seal = epochAdmissionDigest(admission.projectID, admission.missionID, admission.epochID, admission.revision)
	return admission, ResultV1{Kind: OutcomeAlreadyCommitted, OperationID: input.OperationID, CommittedRevision: operation.Revision, Recovery: RecoveryNone}, nil
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
	product, err := batch.ProductBatch()
	if err != nil {
		return ResultV1{}, err
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
		CommittedRevision: result.Revision, ResultType: batchResultType(batch),
		CanonicalResult: batchResult(batch), Recovery: RecoveryNone,
	}, nil
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

func batchResultType(batch ValidatedBatchV1) string {
	record, err := controljournal.DecodeOperationRecordV1(batch.operationRecordBytes)
	if err != nil {
		return ""
	}
	return record.ResultType
}

func batchResult(batch ValidatedBatchV1) []byte {
	record, err := controljournal.DecodeOperationRecordV1(batch.operationRecordBytes)
	if err != nil {
		return nil
	}
	return record.CanonicalResult()
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
