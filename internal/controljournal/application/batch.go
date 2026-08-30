// Package application admits product commands to the durable control journal.
// It is deliberately the impure composition boundary between the pure journal
// model and the narrow GapDB port.
package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const (
	applicationSchemaV1 = uint32(1)
	batchSealVersionV1  = byte(1)
)

// CommandEnvelopeInputV1 contains every caller-controlled fact whose identity
// must remain stable across timeout, restart, and explicit retry.
type CommandEnvelopeInputV1 struct {
	SchemaVersion  uint32
	ProjectID      controljournal.ProjectID
	MissionID      controljournal.MissionID
	OperationID    controljournal.OperationID
	CommandType    string
	RuntimeEpochID controljournal.RuntimeEpochID
	Payload        []byte
}

// ValidatedCommandEnvelopeV1 is immutable after construction. Its payload and
// canonical encoding are private and copy-on-read.
type ValidatedCommandEnvelopeV1 struct {
	schemaVersion  uint32
	projectID      controljournal.ProjectID
	missionID      controljournal.MissionID
	operationID    controljournal.OperationID
	commandType    string
	runtimeEpochID controljournal.RuntimeEpochID
	payload        []byte
	canonical      []byte
	digest         [32]byte
}

type commandWireV1 struct {
	CommandType    string          `json:"command_type"`
	MissionID      string          `json:"mission_id"`
	OperationID    string          `json:"operation_id"`
	Payload        json.RawMessage `json:"payload"`
	ProjectID      string          `json:"project_id"`
	RuntimeEpochID string          `json:"runtime_epoch_id"`
	SchemaVersion  uint32          `json:"schema_version"`
}

// ValidateCommandEnvelopeV1 canonicalizes and seals one command. The command
// digest is independent of later mutation construction and slice ordering.
func ValidateCommandEnvelopeV1(input CommandEnvelopeInputV1) (ValidatedCommandEnvelopeV1, error) {
	if input.SchemaVersion != applicationSchemaV1 {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeUnsupportedVersion, "command schema version is unsupported")
	}
	if _, err := controljournal.NewProjectID(string(input.ProjectID)); err != nil {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeRefused, "project identity is invalid")
	}
	if _, err := controljournal.NewMissionID(string(input.MissionID)); err != nil {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeRefused, "mission identity is invalid")
	}
	if _, err := controljournal.NewOperationID(string(input.OperationID)); err != nil {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeRefused, "operation identity is invalid")
	}
	if _, err := controljournal.NewRuntimeEpochID(string(input.RuntimeEpochID)); err != nil {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeRefused, "runtime epoch identity is invalid")
	}
	if !validToken(input.CommandType) {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeRefused, "command type is invalid")
	}
	payload, err := canonicalObject(input.Payload)
	if err != nil {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeRefused, "command payload is not canonicalizable JSON")
	}
	wire := commandWireV1{
		CommandType: input.CommandType, MissionID: string(input.MissionID),
		OperationID: string(input.OperationID), Payload: payload,
		ProjectID: string(input.ProjectID), RuntimeEpochID: string(input.RuntimeEpochID),
		SchemaVersion: applicationSchemaV1,
	}
	canonical, err := json.Marshal(wire)
	if err != nil || len(canonical) > storegapdb.MaxOrdinaryValueBytes {
		return ValidatedCommandEnvelopeV1{}, refusal(OutcomeBudgetExceeded, "canonical command exceeds the ordinary value budget")
	}
	return ValidatedCommandEnvelopeV1{
		schemaVersion: applicationSchemaV1, projectID: input.ProjectID,
		missionID: input.MissionID, operationID: input.OperationID,
		commandType: input.CommandType, runtimeEpochID: input.RuntimeEpochID,
		payload: append([]byte(nil), payload...), canonical: append([]byte(nil), canonical...),
		digest: sha256.Sum256(canonical),
	}, nil
}

func (c ValidatedCommandEnvelopeV1) OperationID() controljournal.OperationID { return c.operationID }
func (c ValidatedCommandEnvelopeV1) RuntimeEpochID() controljournal.RuntimeEpochID {
	return c.runtimeEpochID
}
func (c ValidatedCommandEnvelopeV1) Digest() [32]byte { return c.digest }
func (c ValidatedCommandEnvelopeV1) CanonicalBytes() []byte {
	return append([]byte(nil), c.canonical...)
}

func (c ValidatedCommandEnvelopeV1) valid() bool {
	if c.schemaVersion != applicationSchemaV1 || len(c.canonical) == 0 || sha256.Sum256(c.canonical) != c.digest {
		return false
	}
	validated, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: c.schemaVersion, ProjectID: c.projectID, MissionID: c.missionID,
		OperationID: c.operationID, CommandType: c.commandType,
		RuntimeEpochID: c.runtimeEpochID, Payload: c.payload,
	})
	return err == nil && bytes.Equal(validated.canonical, c.canonical) && validated.digest == c.digest
}

// CheckpointInputV1 combines a constructor-sealed prior-prefix proof with the
// caller's exact provider observation of the mutable checkpoint selector.
// Revision zero proves selector absence; a positive revision requests CAS.
type CheckpointInputV1 struct {
	Validated            journalcheckpoint.ValidatedCheckpointV1
	ObservedHeadRevision uint64
}

// BatchInputV1 is the complete semantic result of validating and reducing one
// command. ExpectedHeads and NextHeads are position-independent and are paired
// by aggregate identity during validation.
type BatchInputV1 struct {
	Command                     ValidatedCommandEnvelopeV1
	ExpectedHeads               []controljournal.ExpectedAggregateHeadV1
	NextHeads                   []controljournal.NewAggregateHeadV1
	Events                      []controljournal.EventEnvelopeV1
	EffectIntents               []controljournal.EffectIntentV1
	Checkpoint                  *CheckpointInputV1
	ExpectedPreconditionsDigest [32]byte
	ResultType                  string
	ResultSchemaVersion         uint32
	CanonicalResult             []byte
}

// ValidatedBatchV1 is the sole publication capability. product and semantic
// observations are private deep copies bound by the seal.
type ValidatedBatchV1 struct {
	seal                 [32]byte
	command              ValidatedCommandEnvelopeV1
	product              storegapdb.ProductBatch
	expectedHeads        []controljournal.ExpectedAggregateHeadV1
	nextHeads            []controljournal.NewAggregateHeadV1
	events               []controljournal.EventEnvelopeV1
	intents              []controljournal.EffectIntentV1
	operationRecordBytes []byte
}

// NewValidatedBatchV1 validates all identities, budgets, transitions, and
// encoded values before constructing a single immutable ProductBatch.
func NewValidatedBatchV1(input BatchInputV1) (ValidatedBatchV1, error) {
	if !input.Command.valid() {
		return ValidatedBatchV1{}, refusal(OutcomeRefused, "command validation capability is invalid")
	}
	if input.ResultSchemaVersion == 0 || !validToken(input.ResultType) {
		return ValidatedBatchV1{}, refusal(OutcomeRefused, "operation result or preconditions are invalid")
	}
	if len(input.Events) < 1 || len(input.Events) > storegapdb.MaxEventsPerCommand {
		return ValidatedBatchV1{}, refusal(OutcomeBudgetExceeded, "event count is outside the product budget")
	}
	if len(input.ExpectedHeads) < 1 || len(input.ExpectedHeads) > storegapdb.MaxHeadsPerCommand || len(input.ExpectedHeads) != len(input.NextHeads) {
		return ValidatedBatchV1{}, refusal(OutcomeBudgetExceeded, "aggregate head count is outside the product budget")
	}
	if len(input.EffectIntents) > storegapdb.MaxIntentsPerCommand {
		return ValidatedBatchV1{}, refusal(OutcomeBudgetExceeded, "effect intent count is outside the product budget")
	}

	operationID := input.Command.operationID
	epoch := input.Command.runtimeEpochID
	prefix := canonicalPrefix(input.Command.projectID, input.Command.missionID)
	events := append([]controljournal.EventEnvelopeV1(nil), input.Events...)
	sort.Slice(events, func(i, j int) bool {
		if events[i].EventIndex != events[j].EventIndex {
			return events[i].EventIndex < events[j].EventIndex
		}
		return events[i].EventID < events[j].EventID
	})
	eventIDs := make([]controljournal.EventID, len(events))
	eventMutations := make([]storegapdb.Mutation, len(events))
	seenEvents := make(map[controljournal.EventID]struct{}, len(events))
	for index, event := range events {
		encoded, err := controljournal.EncodeEventEnvelopeV1(event)
		if err != nil || event.OperationID != operationID || event.RuntimeEpochID != epoch || event.ProjectID != input.Command.projectID ||
			int(event.EventIndex) != index || int(event.EventCount) != len(events) {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "event group is inconsistent with the command")
		}
		if _, duplicate := seenEvents[event.EventID]; duplicate {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "duplicate event identity")
		}
		seenEvents[event.EventID] = struct{}{}
		eventIDs[index] = event.EventID
		eventMutations[index] = absentPut(prefix+"journal/"+string(event.EventID), encoded)
	}

	type headPair struct {
		expected controljournal.ExpectedAggregateHeadV1
		next     controljournal.NewAggregateHeadV1
	}
	pairs := make([]headPair, len(input.ExpectedHeads))
	nextByKey := make(map[string]controljournal.NewAggregateHeadV1, len(input.NextHeads))
	for _, next := range input.NextHeads {
		key := aggregateCoordinate(next.Head.AggregateKind, next.Head.AggregateID)
		if _, duplicate := nextByKey[key]; duplicate {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "duplicate next aggregate head")
		}
		nextByKey[key] = next
	}
	seenHeads := make(map[string]struct{}, len(input.ExpectedHeads))
	for index, expected := range input.ExpectedHeads {
		key := aggregateCoordinate(expected.AggregateKind, expected.AggregateID)
		if _, duplicate := seenHeads[key]; duplicate {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "duplicate expected aggregate head")
		}
		seenHeads[key] = struct{}{}
		next, found := nextByKey[key]
		if !found || controljournal.ValidateHeadTransition(expected, next) != nil || next.Head.RuntimeEpochID != epoch {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "aggregate head transition is inconsistent")
		}
		pairs[index] = headPair{expected: expected, next: next}
	}
	lastEventByHead := make(map[string]controljournal.EventID, len(pairs))
	for _, event := range events {
		coordinate := aggregateCoordinate(event.AggregateKind, event.AggregateID)
		next, found := nextByKey[coordinate]
		if !found || event.AggregateLogicalVersion != next.Head.LogicalVersion {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "event does not belong to an affected aggregate transition")
		}
		lastEventByHead[coordinate] = event.EventID
	}
	for _, pair := range pairs {
		coordinate := aggregateCoordinate(pair.expected.AggregateKind, pair.expected.AggregateID)
		if lastEventByHead[coordinate] == "" || pair.next.Head.LastEventID != lastEventByHead[coordinate] {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "aggregate head does not name its terminal command event")
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return aggregateCoordinate(pairs[i].expected.AggregateKind, pairs[i].expected.AggregateID) < aggregateCoordinate(pairs[j].expected.AggregateKind, pairs[j].expected.AggregateID)
	})
	expectedHeads := make([]controljournal.ExpectedAggregateHeadV1, len(pairs))
	nextHeads := make([]controljournal.NewAggregateHeadV1, len(pairs))
	headMutations := make([]storegapdb.Mutation, len(pairs))
	for index, pair := range pairs {
		encoded, err := controljournal.EncodeAggregateHeadV1(pair.next.Head)
		if err != nil {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "aggregate head cannot be encoded")
		}
		condition := storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: pair.expected.ObservedHeadRevision}
		// Logical version zero denotes creation. A supplied observed revision is
		// still forbidden by ValidateHeadTransition, so creation must be explicit.
		if pair.expected.LogicalVersion == 0 {
			condition = storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
		}
		expectedHeads[index], nextHeads[index] = pair.expected, pair.next
		headMutations[index] = storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: prefix + "heads/" + aggregateCoordinate(pair.expected.AggregateKind, pair.expected.AggregateID), Condition: condition, Value: encoded}
	}
	preconditionsDigest, err := CanonicalPreconditionsDigestV1(expectedHeads)
	if err != nil {
		return ValidatedBatchV1{}, refusal(OutcomeRefused, "aggregate preconditions cannot be canonicalized")
	}
	if input.ExpectedPreconditionsDigest != [32]byte{} && input.ExpectedPreconditionsDigest != preconditionsDigest {
		return ValidatedBatchV1{}, refusal(OutcomeRefused, "supplied preconditions digest does not match aggregate heads")
	}

	intents := append([]controljournal.EffectIntentV1(nil), input.EffectIntents...)
	sort.Slice(intents, func(i, j int) bool { return intents[i].EffectID < intents[j].EffectID })
	effectIDs := make([]controljournal.EffectID, len(intents))
	intentMutations := make([]storegapdb.Mutation, len(intents))
	seenIntents := make(map[controljournal.IntentID]struct{}, len(intents))
	seenEffects := make(map[controljournal.EffectID]struct{}, len(intents))
	for index, intent := range intents {
		encoded, err := controljournal.EncodeEffectIntentV1(intent)
		if err != nil || intent.OperationID != operationID {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "effect intent is inconsistent with the command")
		}
		if _, duplicate := seenIntents[intent.IntentID]; duplicate {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "duplicate intent identity")
		}
		if _, duplicate := seenEffects[intent.EffectID]; duplicate {
			return ValidatedBatchV1{}, refusal(OutcomeRefused, "duplicate effect identity")
		}
		seenIntents[intent.IntentID], seenEffects[intent.EffectID] = struct{}{}, struct{}{}
		effectIDs[index] = intent.EffectID
		intentMutations[index] = absentPut(prefix+"effects/"+string(intent.EffectID)+"/intent", encoded)
	}

	record, err := controljournal.NewOperationRecordV1(controljournal.OperationRecordInputV1{
		OperationID: operationID, CommandType: input.Command.commandType,
		RequestDigest: input.Command.digest, RuntimeEpochID: epoch,
		ExpectedPreconditionsDigest: preconditionsDigest,
		ResultType:                  input.ResultType, ResultSchemaVersion: input.ResultSchemaVersion,
		CanonicalResult: input.CanonicalResult, EventIDs: eventIDs, EffectIDs: effectIDs,
	})
	if err != nil {
		return ValidatedBatchV1{}, refusal(OutcomeRefused, "operation record is invalid")
	}
	operationBytes, err := controljournal.EncodeOperationRecordV1(record)
	if err != nil {
		return ValidatedBatchV1{}, refusal(OutcomeBudgetExceeded, "operation record cannot be encoded")
	}
	product := storegapdb.ProductBatch{
		OperationID: string(operationID), Operation: absentPut(prefix+"operations/"+string(operationID), operationBytes),
		Events: eventMutations, AggregateHeads: headMutations, EffectIntents: intentMutations,
	}
	if input.Checkpoint != nil {
		if err := validateCheckpoint(prefix, operationID, input.Command.projectID, input.Command.missionID, input.Checkpoint, events, &product); err != nil {
			return ValidatedBatchV1{}, err
		}
	}
	if err := validateProductBudget(product); err != nil {
		return ValidatedBatchV1{}, err
	}
	batch := ValidatedBatchV1{
		command: cloneCommand(input.Command), product: cloneProductBatch(product),
		expectedHeads: expectedHeads, nextHeads: nextHeads, events: events,
		intents:              intents,
		operationRecordBytes: append([]byte(nil), operationBytes...),
	}
	batch.seal = batchDigest(batch.product)
	return batch, nil
}

type preconditionWireV1 struct {
	AggregateID          string `json:"aggregate_id"`
	AggregateKind        string `json:"aggregate_kind"`
	LogicalVersion       uint64 `json:"logical_version"`
	ObservedHeadRevision uint64 `json:"observed_head_revision"`
}

// CanonicalPreconditionsDigestV1 binds retry identity to the exact set of
// per-aggregate logical versions and observed provider revisions. Input order
// cannot change the digest.
func CanonicalPreconditionsDigestV1(heads []controljournal.ExpectedAggregateHeadV1) ([32]byte, error) {
	if len(heads) < 1 || len(heads) > storegapdb.MaxHeadsPerCommand {
		return [32]byte{}, refusal(OutcomeBudgetExceeded, "aggregate precondition count is outside the product budget")
	}
	values := append([]controljournal.ExpectedAggregateHeadV1(nil), heads...)
	sort.Slice(values, func(i, j int) bool {
		return aggregateCoordinate(values[i].AggregateKind, values[i].AggregateID) < aggregateCoordinate(values[j].AggregateKind, values[j].AggregateID)
	})
	wire := make([]preconditionWireV1, len(values))
	previous := ""
	for index, head := range values {
		coordinate := aggregateCoordinate(head.AggregateKind, head.AggregateID)
		if controljournal.ValidateAggregateCoordinateV1(head.AggregateKind, head.AggregateID) != nil || coordinate == previous ||
			(head.LogicalVersion == 0) != (head.ObservedHeadRevision == 0) {
			return [32]byte{}, refusal(OutcomeRefused, "aggregate precondition is inconsistent")
		}
		previous = coordinate
		wire[index] = preconditionWireV1{AggregateID: string(head.AggregateID), AggregateKind: string(head.AggregateKind), LogicalVersion: head.LogicalVersion, ObservedHeadRevision: head.ObservedHeadRevision}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return [32]byte{}, refusal(OutcomeRefused, "aggregate preconditions cannot be encoded")
	}
	return sha256.Sum256(encoded), nil
}

func validateCheckpoint(prefix string, operationID controljournal.OperationID, projectID controljournal.ProjectID, missionID controljournal.MissionID, input *CheckpointInputV1, events []controljournal.EventEnvelopeV1, product *storegapdb.ProductBatch) error {
	publication, err := input.Validated.PublicationV1(operationID)
	if err != nil {
		return refusal(OutcomeInvalidCheckpoint, "checkpoint capability is invalid for the forming operation")
	}
	record, err := input.Validated.RecordV1()
	if err != nil {
		return refusal(OutcomeInvalidCheckpoint, "checkpoint record capability is invalid")
	}
	head, err := input.Validated.HeadV1()
	if err != nil || publication.FormingBatchOperationID != operationID ||
		publication.CheckpointID != record.CheckpointID || head.CheckpointID != record.CheckpointID ||
		record.ProjectID != projectID || record.MissionID != missionID ||
		record.Watermark.ThroughRecordRevision == 0 || record.Watermark.ThroughEventID == "" ||
		head.ThroughRecordRevision != record.Watermark.ThroughRecordRevision || head.ThroughEventID != record.Watermark.ThroughEventID ||
		len(publication.RecordBytes) == 0 || len(publication.RecordBytes) > storegapdb.MaxCheckpointValueBytes ||
		len(publication.HeadBytes) == 0 || len(publication.HeadBytes) > storegapdb.MaxOrdinaryValueBytes {
		return refusal(OutcomeInvalidCheckpoint, "checkpoint does not prove a bounded prior prefix")
	}
	for _, event := range events {
		if event.EventID == record.Watermark.ThroughEventID {
			return refusal(OutcomeInvalidCheckpoint, "checkpoint includes a current-batch event")
		}
	}
	checkpointRecord := absentPut(prefix+"checkpoints/"+string(record.CheckpointID), publication.RecordBytes)
	headCondition := storegapdb.Condition{Kind: storegapdb.ConditionAbsent}
	if input.ObservedHeadRevision > 0 {
		headCondition = storegapdb.Condition{Kind: storegapdb.ConditionRevision, ExpectedRevision: input.ObservedHeadRevision}
	}
	checkpointHead := storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: prefix + "checkpoint-head", Condition: headCondition, Value: append([]byte(nil), publication.HeadBytes...)}
	product.Checkpoint, product.CheckpointHead = &checkpointRecord, &checkpointHead
	return nil
}

func validateProductBudget(product storegapdb.ProductBatch) error {
	mutations := productMutations(product)
	if err := validateMutationBudget(len(mutations)); err != nil {
		return err
	}
	raw := 0
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if _, duplicate := seen[mutation.Key]; duplicate {
			return refusal(OutcomeRefused, "duplicate mutation key")
		}
		seen[mutation.Key] = struct{}{}
		raw += len(mutation.Key) + len(mutation.Value)
	}
	if err := validateRawBudget(raw); err != nil {
		return err
	}
	// JSON base64 expansion is at most 4/3 plus a bounded per-mutation
	// envelope. This conservative estimate is checked before the adapter call;
	// the adapter independently measures the exact provider frame.
	wire := raw + (raw+2)/3 + len(mutations)*256 + 1024
	return validateWireBudget(wire)
}

func validateMutationBudget(count int) error {
	if count < 1 || count > storegapdb.MaxMutationsPerBatch {
		return refusal(OutcomeBudgetExceeded, "mutation count exceeds the product budget")
	}
	return nil
}

func validateRawBudget(size int) error {
	if size < 1 || size > storegapdb.MaxRawBatchBytes {
		return refusal(OutcomeBudgetExceeded, "raw batch exceeds the product budget")
	}
	return nil
}

func validateWireBudget(size int) error {
	if size < 1 || size > storegapdb.MaxWireFrameBytes {
		return refusal(OutcomeBudgetExceeded, "wire frame exceeds the product budget")
	}
	return nil
}

func (b ValidatedBatchV1) valid() bool {
	return b.command.valid() && b.product.OperationID == string(b.command.operationID) &&
		(b.product.Checkpoint == nil) == (b.product.CheckpointHead == nil) &&
		b.seal != [32]byte{} && b.seal == batchDigest(b.product)
}

// ProductBatch returns a deep copy. Mutating it cannot alter the validated
// publication capability retained by the service.
func (b ValidatedBatchV1) ProductBatch() (storegapdb.ProductBatch, error) {
	if !b.valid() {
		return storegapdb.ProductBatch{}, refusal(OutcomeRefused, "batch validation capability is invalid")
	}
	return cloneProductBatch(b.product), nil
}

func batchDigest(product storegapdb.ProductBatch) [32]byte {
	h := sha256.New()
	h.Write([]byte{batchSealVersionV1})
	writeDigestPart(h, []byte(product.OperationID))
	for _, mutation := range productMutations(product) {
		writeDigestPart(h, []byte(string(mutation.Kind)))
		writeDigestPart(h, []byte(mutation.Key))
		writeDigestPart(h, []byte(string(mutation.Condition.Kind)))
		writeDigestPart(h, []byte(fmt.Sprintf("%d", mutation.Condition.ExpectedRevision)))
		if mutation.Value == nil {
			writeDigestPart(h, nil)
		} else {
			writeDigestPart(h, append([]byte{1}, mutation.Value...))
		}
		if mutation.ExpiresAt == nil {
			writeDigestPart(h, nil)
		} else {
			writeDigestPart(h, []byte(mutation.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")))
		}
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func writeDigestPart(h interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}

func productMutations(product storegapdb.ProductBatch) []storegapdb.Mutation {
	result := make([]storegapdb.Mutation, 0, 1+len(product.Events)+len(product.AggregateHeads)+len(product.EffectIntents)+2)
	result = append(result, product.Operation)
	result = append(result, product.Events...)
	result = append(result, product.AggregateHeads...)
	if product.Checkpoint != nil {
		result = append(result, *product.Checkpoint)
	}
	if product.CheckpointHead != nil {
		result = append(result, *product.CheckpointHead)
	}
	return append(result, product.EffectIntents...)
}

func cloneProductBatch(product storegapdb.ProductBatch) storegapdb.ProductBatch {
	clone := storegapdb.ProductBatch{OperationID: product.OperationID, Operation: cloneMutation(product.Operation)}
	clone.Events = cloneMutations(product.Events)
	clone.AggregateHeads = cloneMutations(product.AggregateHeads)
	clone.EffectIntents = cloneMutations(product.EffectIntents)
	if product.Checkpoint != nil {
		value := cloneMutation(*product.Checkpoint)
		clone.Checkpoint = &value
	}
	if product.CheckpointHead != nil {
		value := cloneMutation(*product.CheckpointHead)
		clone.CheckpointHead = &value
	}
	return clone
}

func cloneMutations(values []storegapdb.Mutation) []storegapdb.Mutation {
	result := make([]storegapdb.Mutation, len(values))
	for index := range values {
		result[index] = cloneMutation(values[index])
	}
	return result
}

func cloneMutation(value storegapdb.Mutation) storegapdb.Mutation {
	value.Value = append([]byte(nil), value.Value...)
	if value.ExpiresAt != nil {
		copy := *value.ExpiresAt
		value.ExpiresAt = &copy
	}
	return value
}

func cloneCommand(value ValidatedCommandEnvelopeV1) ValidatedCommandEnvelopeV1 {
	value.payload = append([]byte(nil), value.payload...)
	value.canonical = append([]byte(nil), value.canonical...)
	return value
}

func absentPut(key string, value []byte) storegapdb.Mutation {
	return storegapdb.Mutation{Kind: storegapdb.MutationPut, Key: key, Condition: storegapdb.Condition{Kind: storegapdb.ConditionAbsent}, Value: append([]byte(nil), value...)}
}

func canonicalPrefix(project controljournal.ProjectID, mission controljournal.MissionID) string {
	return "spk/v1/" + string(project) + "/" + string(mission) + "/"
}

func aggregateCoordinate(kind controljournal.AggregateKind, id controljournal.AggregateID) string {
	return string(kind) + "/" + string(id)
}

func validToken(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		valid := character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
		if index > 0 {
			valid = valid || strings.ContainsRune("._:-", rune(character))
		}
		if !valid {
			return false
		}
	}
	return true
}

func canonicalObject(input []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeUniqueValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("JSON value is not an object")
	}
	if token, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON contains trailing token %v", token)
	}
	return json.Marshal(value)
}

func decodeUniqueValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key is not a string")
			}
			if _, duplicate := object[key]; duplicate {
				return nil, fmt.Errorf("duplicate object field %q", key)
			}
			value, err := decodeUniqueValue(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
			return nil, errors.New("object is not closed")
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := decodeUniqueValue(decoder)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("array is not closed")
		}
		return array, nil
	default:
		return nil, errors.New("unexpected JSON delimiter")
	}
}

// CanonicalDigestHex is useful in operator-safe evidence without exposing
// mutable command bytes.
func CanonicalDigestHex(command ValidatedCommandEnvelopeV1) (string, error) {
	if !command.valid() {
		return "", refusal(OutcomeRefused, "command validation capability is invalid")
	}
	return hex.EncodeToString(command.digest[:]), nil
}
