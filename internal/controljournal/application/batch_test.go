package application

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

func TestBatchMinimumMaximumAndPermanentBoundaries(t *testing.T) {
	minimum := batchFixture(t, "minimum", 1, 1, 0)
	if product, err := minimum.ProductBatch(); err != nil || len(productMutations(product)) != 3 {
		t.Fatalf("minimum product mutations=%d err=%v", len(productMutations(product)), err)
	}
	maximum := batchFixture(t, "maximum", storegapdb.MaxEventsPerCommand, storegapdb.MaxHeadsPerCommand, storegapdb.MaxIntentsPerCommand)
	if product, err := maximum.ProductBatch(); err != nil || len(productMutations(product)) != 1+128+32+16 {
		t.Fatalf("maximum product mutations=%d err=%v", len(productMutations(product)), err)
	}

	base := batchInputFixture(t, "events-plus-one", 1, 1, 0)
	base.Events = append(make([]controljournal.EventEnvelopeV1, storegapdb.MaxEventsPerCommand+1), base.Events[0])
	if _, err := NewValidatedBatchV1(base); outcomeOf(err) != OutcomeBudgetExceeded {
		t.Fatalf("event boundary+1 err=%v", err)
	}
	base = batchInputFixture(t, "heads-plus-one", 1, 1, 0)
	base.ExpectedHeads = append(make([]controljournal.ExpectedAggregateHeadV1, storegapdb.MaxHeadsPerCommand+1), base.ExpectedHeads[0])
	base.NextHeads = append(make([]controljournal.NewAggregateHeadV1, storegapdb.MaxHeadsPerCommand+1), base.NextHeads[0])
	if _, err := NewValidatedBatchV1(base); outcomeOf(err) != OutcomeBudgetExceeded {
		t.Fatalf("head boundary+1 err=%v", err)
	}
	base = batchInputFixture(t, "intents-plus-one", 1, 1, 0)
	base.EffectIntents = append(make([]controljournal.EffectIntentV1, storegapdb.MaxIntentsPerCommand+1), base.EffectIntents...)
	if _, err := NewValidatedBatchV1(base); outcomeOf(err) != OutcomeBudgetExceeded {
		t.Fatalf("intent boundary+1 err=%v", err)
	}
	for name, test := range map[string]struct {
		at  func(int) error
		max int
	}{
		"mutation": {validateMutationBudget, storegapdb.MaxMutationsPerBatch},
		"raw":      {validateRawBudget, storegapdb.MaxRawBatchBytes},
		"wire":     {validateWireBudget, storegapdb.MaxWireFrameBytes},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test.at(test.max); err != nil {
				t.Fatalf("exact boundary refused: %v", err)
			}
			if err := test.at(test.max + 1); outcomeOf(err) != OutcomeBudgetExceeded {
				t.Fatalf("boundary+1 survived: %v", err)
			}
		})
	}
}

func TestBatchRawBudgetExactAndPlusOneThroughProductionPath(t *testing.T) {
	input, exact := exactRawBudgetInput(t, "raw-production")
	if got := productRawBytes(exact.product); got != storegapdb.MaxRawBatchBytes {
		t.Fatalf("exact production raw bytes=%d want=%d", got, storegapdb.MaxRawBatchBytes)
	}
	port := newMemoryPort()
	service, _ := admittedService(t, port, "raw-production")
	before := port.batchCalls
	result, err := service.Apply(t.Context(), exact)
	if err != nil || result.Kind != OutcomeCommitted || port.batchCalls != before+1 {
		t.Fatalf("exact raw apply result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
	}

	last := len(input.Events) - 1
	original := input.Events[last]
	payload := original.Payload()
	marker := bytes.LastIndex(payload, []byte{'"'})
	if marker <= 0 {
		t.Fatal("large payload shape changed")
	}
	payload = append(payload[:marker], append([]byte{'x'}, payload[marker:]...)...)
	input.Events[last] = eventFixturePayload(t, original.EventID, last, len(input.Events), input.Command.operationID, "aggregate-0", input.Command.runtimeEpochID, 1, payload)
	before = port.batchCalls
	if _, err := NewValidatedBatchV1(input); outcomeOf(err) != OutcomeBudgetExceeded {
		t.Fatalf("raw production boundary+1 survived: %v", err)
	}
	if port.batchCalls != before {
		t.Fatalf("raw pre-call refusal mutated port calls=%d", port.batchCalls-before)
	}
}

func exactRawBudgetInput(t *testing.T, suffix string) (BatchInputV1, ValidatedBatchV1) {
	t.Helper()
	const eventCount = 26
	input := batchInputFixture(t, suffix, eventCount, 1, 0)
	paddings := make([]int, eventCount)
	for index := range paddings {
		paddings[index] = 230_000
		input.Events[index] = eventFixturePayload(t, input.Events[index].EventID, index, eventCount, input.Command.operationID, "aggregate-0", input.Command.runtimeEpochID, 1, paddedObject(paddings[index]))
	}
	batch, err := NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	remaining := storegapdb.MaxRawBatchBytes - productRawBytes(batch.product)
	if remaining <= 0 {
		t.Fatalf("large fixture did not leave tunable capacity: remaining=%d", remaining)
	}
	for index := range paddings {
		capacity := 255_000 - paddings[index]
		if capacity > remaining {
			capacity = remaining
		}
		paddings[index] += capacity
		remaining -= capacity
		if remaining == 0 {
			break
		}
	}
	if remaining != 0 {
		t.Fatalf("large fixture cannot reach exact raw bound: remaining=%d", remaining)
	}
	for index := range paddings {
		input.Events[index] = eventFixturePayload(t, input.Events[index].EventID, index, eventCount, input.Command.operationID, "aggregate-0", input.Command.runtimeEpochID, 1, paddedObject(paddings[index]))
	}
	batch, err = NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return input, batch
}

func paddedObject(size int) []byte {
	return []byte(`{"padding":"` + strings.Repeat("x", size) + `"}`)
}

func productRawBytes(product storegapdb.ProductBatch) int {
	total := 0
	for _, mutation := range productMutations(product) {
		total += len(mutation.Key) + len(mutation.Value)
	}
	return total
}

func TestBatchCommandDigestCanonicalAndOrderIndependent(t *testing.T) {
	first, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation",
		CommandType: "mission.advance", RuntimeEpochID: "epoch", Payload: []byte(`{"z":2,"a":{"y":1,"x":0}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation",
		CommandType: "mission.advance", RuntimeEpochID: "epoch", Payload: []byte(` { "a" : { "x":0,"y":1 }, "z" : 2 } `),
	})
	if err != nil || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatalf("canonical command mismatch err=%v", err)
	}
	input := batchInputFixture(t, "order", 2, 2, 2)
	original, err := NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedHeads[0], input.ExpectedHeads[1] = input.ExpectedHeads[1], input.ExpectedHeads[0]
	input.NextHeads[0], input.NextHeads[1] = input.NextHeads[1], input.NextHeads[0]
	input.EffectIntents[0], input.EffectIntents[1] = input.EffectIntents[1], input.EffectIntents[0]
	shuffled, err := NewValidatedBatchV1(input)
	if err != nil || original.command.digest != shuffled.command.digest || original.seal != shuffled.seal {
		t.Fatalf("mutation order changed identity err=%v", err)
	}
	if _, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: "p", MissionID: "m", OperationID: "o", CommandType: "c", RuntimeEpochID: "e", Payload: []byte(`{"x":1,"x":2}`)}); err == nil {
		t.Fatal("duplicate JSON command field survived")
	}
}

func TestBatchImmutableUnforgeableAndNoCurrentRevision(t *testing.T) {
	input := batchInputFixture(t, "immutable", 2, 1, 1)
	batch, err := NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	product, err := batch.ProductBatch()
	if err != nil {
		t.Fatal(err)
	}
	want := cloneProductBatch(product)
	product.Operation.Value[0] ^= 0xff
	product.Events[0].Value[0] ^= 0xff
	product.Events = nil
	again, err := batch.ProductBatch()
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("validated batch was mutable err=%v", err)
	}
	if _, err := (ValidatedBatchV1{}).ProductBatch(); outcomeOf(err) != OutcomeRefused {
		t.Fatalf("zero batch capability survived: %v", err)
	}
	for _, mutation := range productMutations(again) {
		if strings.Contains(string(mutation.Value), "committed_revision") || strings.Contains(mutation.Key, "/revisions/") {
			t.Fatalf("current-batch revision leaked into %q", mutation.Key)
		}
	}
}

func TestBatchRefusesDuplicatesInconsistencyAndSplitMutant(t *testing.T) {
	for name, mutate := range map[string]func(*BatchInputV1){
		"duplicate event": func(input *BatchInputV1) { input.Events[1] = input.Events[0] },
		"duplicate head": func(input *BatchInputV1) {
			input.ExpectedHeads[1] = input.ExpectedHeads[0]
			input.NextHeads[1] = input.NextHeads[0]
		},
		"duplicate effect": func(input *BatchInputV1) { input.EffectIntents[1] = input.EffectIntents[0] },
		"wrong operation": func(input *BatchInputV1) {
			input.Events[0] = eventFixture(t, "foreign-event", 0, len(input.Events), "foreign", "aggregate-0", input.Command.runtimeEpochID)
		},
		"wrong epoch": func(input *BatchInputV1) {
			input.Events[0] = eventFixture(t, "wrong-epoch-event", 0, len(input.Events), input.Command.operationID, "aggregate-0", "other-epoch")
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := batchInputFixture(t, "mutant-"+strings.ReplaceAll(name, " ", "-"), 2, 2, 2)
			mutate(&input)
			if _, err := NewValidatedBatchV1(input); err == nil {
				t.Fatal("mutant survived")
			}
		})
	}
	batch := batchFixture(t, "split", 2, 1, 0)
	product, _ := batch.ProductBatch()
	product.Events = product.Events[:1]
	// Mutating a copy cannot create another usable publication capability.
	if batchDigest(product) == batch.seal {
		t.Fatal("split-batch mutant retained the validation seal")
	}
}

func TestBatchCheckpointMustBePriorPrefix(t *testing.T) {
	input := batchInputFixture(t, "checkpoint", 1, 1, 0)
	capability := checkpointCapabilityFixture(t, "project", "mission", "prior-operation", input.Command.operationID)
	input.Checkpoint = &CheckpointInputV1{Validated: capability}
	batch, err := NewValidatedBatchV1(input)
	if err != nil || batch.product.Checkpoint == nil || batch.product.CheckpointHead == nil {
		t.Fatalf("prior checkpoint refused: %v", err)
	}
	publication, err := capability.PublicationV1(input.Command.operationID)
	if err != nil || !bytes.Equal(batch.product.Checkpoint.Value, publication.RecordBytes) || !bytes.Equal(batch.product.CheckpointHead.Value, publication.HeadBytes) {
		t.Fatalf("checkpoint publication mismatch: %v", err)
	}
	publication.RecordBytes[0] ^= 0xff
	publication.HeadBytes[0] ^= 0xff
	if bytes.Equal(batch.product.Checkpoint.Value, publication.RecordBytes) || bytes.Equal(batch.product.CheckpointHead.Value, publication.HeadBytes) {
		t.Fatal("checkpoint publication retained mutable accessor bytes")
	}
	if batch.product.CheckpointHead.Condition.Kind != storegapdb.ConditionAbsent {
		t.Fatalf("new selector condition=%+v", batch.product.CheckpointHead.Condition)
	}
	input.Checkpoint.ObservedHeadRevision = 99
	replacement, err := NewValidatedBatchV1(input)
	if err != nil || replacement.product.CheckpointHead.Condition.Kind != storegapdb.ConditionRevision || replacement.product.CheckpointHead.Condition.ExpectedRevision != 99 {
		t.Fatalf("existing selector condition=%+v err=%v", replacement.product.CheckpointHead.Condition, err)
	}
}

func TestBatchCheckpointCapabilityRefusesZeroCrossMissionAndCurrentPrefixReuse(t *testing.T) {
	base := batchInputFixture(t, "checkpoint-refusal", 1, 1, 0)
	for name, capability := range map[string]journalcheckpoint.ValidatedCheckpointV1{
		"zero or manually unrepresentable": {},
		"cross project":                    checkpointCapabilityFixture(t, "other-project", "mission", "prior-operation", base.Command.operationID),
		"cross mission":                    checkpointCapabilityFixture(t, "project", "other-mission", "prior-operation", base.Command.operationID),
		"wrong forming operation":          checkpointCapabilityFixture(t, "project", "mission", "prior-operation", "different-forming-operation"),
		"current prefix reuse":             checkpointCapabilityFixture(t, "project", "mission", base.Command.operationID, "different-forming-operation"),
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Checkpoint = &CheckpointInputV1{Validated: capability}
			if _, err := NewValidatedBatchV1(input); outcomeOf(err) != OutcomeInvalidCheckpoint {
				t.Fatalf("checkpoint mutant survived: %v", err)
			}
		})
	}
}

func TestBatchExistingHeadRequiresExactPriorRevision(t *testing.T) {
	input := batchInputFixture(t, "existing", 1, 1, 0)
	input.ExpectedHeads[0].LogicalVersion = 7
	input.ExpectedHeads[0].ObservedHeadRevision = 44
	input.ExpectedPreconditionsDigest, _ = CanonicalPreconditionsDigestV1(input.ExpectedHeads)
	input.Events[0] = eventFixtureVersion(t, input.Events[0].EventID, 0, 1, input.Command.operationID, "aggregate-0", input.Command.runtimeEpochID, 8)
	digest := sha256.Sum256([]byte("existing-state"))
	head, err := controljournal.ConstructAggregateHeadV1("mission", "aggregate-0", 8, input.Command.runtimeEpochID, input.Events[0].EventID, digest)
	if err != nil {
		t.Fatal(err)
	}
	input.NextHeads[0] = controljournal.NewAggregateHeadV1{Head: head}
	batch, err := NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	if condition := batch.product.AggregateHeads[0].Condition; condition.Kind != storegapdb.ConditionRevision || condition.ExpectedRevision != 44 {
		t.Fatalf("existing head condition=%+v", condition)
	}
	input.ExpectedHeads[0].ObservedHeadRevision = 0
	input.ExpectedPreconditionsDigest = [32]byte{}
	if _, err := NewValidatedBatchV1(input); err == nil {
		t.Fatal("existing logical version without provider revision survived")
	}
}

func TestBatchPreconditionDigestAndEventMembershipMutants(t *testing.T) {
	input := batchInputFixture(t, "preconditions", 1, 1, 0)
	input.ExpectedPreconditionsDigest[0] ^= 0xff
	if _, err := NewValidatedBatchV1(input); outcomeOf(err) != OutcomeRefused {
		t.Fatalf("dishonest preconditions digest survived: %v", err)
	}
	input = batchInputFixture(t, "foreign-aggregate", 1, 1, 0)
	input.Events[0] = eventFixture(t, "event-foreign", 0, 1, input.Command.operationID, "untouched", input.Command.runtimeEpochID)
	if _, err := NewValidatedBatchV1(input); outcomeOf(err) != OutcomeRefused {
		t.Fatalf("untouched aggregate event survived: %v", err)
	}
}

func FuzzBatchCommandCanonicalization(f *testing.F) {
	for _, seed := range [][]byte{[]byte(`{}`), []byte(`{"a":1,"b":[true,false]}`), []byte(`{"duplicate":1,"duplicate":2}`), nil} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload []byte) {
		if len(payload) > storegapdb.MaxOrdinaryValueBytes+1 {
			t.Skip()
		}
		command, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
			SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation-fuzz",
			CommandType: "mission.advance", RuntimeEpochID: "epoch-fuzz", Payload: payload,
		})
		if err != nil {
			return
		}
		revalidated, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
			SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation-fuzz",
			CommandType: "mission.advance", RuntimeEpochID: "epoch-fuzz", Payload: command.payload,
		})
		if err != nil || command.digest != revalidated.digest || !bytes.Equal(command.canonical, revalidated.canonical) {
			t.Fatalf("canonical command was not stable: %v", err)
		}
	})
}

func batchFixture(t *testing.T, suffix string, events, heads, intents int) ValidatedBatchV1 {
	t.Helper()
	batch, err := NewValidatedBatchV1(batchInputFixture(t, suffix, events, heads, intents))
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func batchInputFixture(t *testing.T, suffix string, eventCount, headCount, intentCount int) BatchInputV1 {
	t.Helper()
	operation := controljournal.OperationID("operation-" + suffix)
	epoch := controljournal.RuntimeEpochID("epoch-" + suffix)
	command, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: operation,
		CommandType: "mission.advance", RuntimeEpochID: epoch, Payload: []byte(fmt.Sprintf(`{"suffix":%q}`, suffix)),
	})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]controljournal.EventEnvelopeV1, eventCount)
	for index := range events {
		aggregate := fmt.Sprintf("aggregate-%d", index%headCount)
		events[index] = eventFixture(t, controljournal.EventID(fmt.Sprintf("event-%s-%03d", suffix, index)), index, eventCount, operation, aggregate, epoch)
	}
	expected := make([]controljournal.ExpectedAggregateHeadV1, headCount)
	next := make([]controljournal.NewAggregateHeadV1, headCount)
	for index := range expected {
		id := controljournal.AggregateID(fmt.Sprintf("aggregate-%d", index))
		last := controljournal.EventID("")
		for _, event := range events {
			if event.AggregateID == id {
				last = event.EventID
			}
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("state-%s-%d", suffix, index)))
		head, err := controljournal.ConstructAggregateHeadV1("mission", id, 1, epoch, last, digest)
		if err != nil {
			t.Fatal(err)
		}
		expected[index] = controljournal.ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: id}
		next[index] = controljournal.NewAggregateHeadV1{Head: head}
	}
	effects := make([]controljournal.EffectIntentV1, intentCount)
	for index := range effects {
		effect, err := controljournal.NewEffectIntentV1(
			controljournal.IntentID(fmt.Sprintf("intent-%s-%02d", suffix, index)),
			controljournal.EffectID(fmt.Sprintf("effect-%s-%02d", suffix, index)), operation,
			"github", "revision-1", fmt.Sprintf("marker-%d", index), []byte(`{"old":true}`), []byte(`{"new":true}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		effects[index] = effect
	}
	preconditions, err := CanonicalPreconditionsDigestV1(expected)
	if err != nil {
		t.Fatal(err)
	}
	return BatchInputV1{
		Command: command, ExpectedHeads: expected, NextHeads: next, Events: events, EffectIntents: effects,
		ExpectedPreconditionsDigest: preconditions,
		ResultType:                  "mission.advanced", ResultSchemaVersion: 1,
		CanonicalResult: []byte(fmt.Sprintf(`{"suffix":%q}`, suffix)),
	}
}

func eventFixture(t *testing.T, id controljournal.EventID, index, count int, operation controljournal.OperationID, aggregate string, epoch controljournal.RuntimeEpochID) controljournal.EventEnvelopeV1 {
	return eventFixtureVersion(t, id, index, count, operation, aggregate, epoch, 1)
}

func eventFixtureVersion(t *testing.T, id controljournal.EventID, index, count int, operation controljournal.OperationID, aggregate string, epoch controljournal.RuntimeEpochID, logicalVersion uint64) controljournal.EventEnvelopeV1 {
	return eventFixturePayload(t, id, index, count, operation, aggregate, epoch, logicalVersion, []byte(`{"advanced":true}`))
}

func eventFixturePayload(t *testing.T, id controljournal.EventID, index, count int, operation controljournal.OperationID, aggregate string, epoch controljournal.RuntimeEpochID, logicalVersion uint64, payload []byte) controljournal.EventEnvelopeV1 {
	t.Helper()
	eventIndex, err := controljournal.NewEventIndexV1(uint16(index))
	if err != nil {
		t.Fatal(err)
	}
	eventCount, err := controljournal.NewEventCountV1(uint16(count))
	if err != nil {
		t.Fatal(err)
	}
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: id, EventType: "mission.advanced", EventSchemaVersion: 1,
		ReducerVersion: "mission-v1", ProjectID: "project", AggregateKind: "mission", AggregateID: controljournal.AggregateID(aggregate),
		AggregateLogicalVersion: logicalVersion, OperationID: operation, RuntimeEpochID: epoch,
		EventIndex: eventIndex, EventCount: eventCount, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func checkpointCapabilityFixture(t *testing.T, project, mission string, priorOperation, formingOperation controljournal.OperationID) journalcheckpoint.ValidatedCheckpointV1 {
	t.Helper()
	eventRegistry, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{
		EventType: "mission.advanced", EventSchemaVersion: 1, ReducerVersion: "mission-v1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	reducers, err := reducer.NewRegistryV1([]reducer.EntryV1{{
		AggregateKind: "mission", EventType: "mission.advanced", EventSchemaVersion: 1,
		ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	eventIndex, _ := controljournal.NewEventIndexV1(0)
	eventCount, _ := controljournal.NewEventCountV1(1)
	payload := []byte(`{"advanced":true}`)
	event, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: "checkpoint-prior-event", EventType: "mission.advanced", EventSchemaVersion: 1,
		ReducerVersion: "mission-v1", ProjectID: controljournal.ProjectID(project), AggregateKind: "mission", AggregateID: "checkpoint-aggregate",
		AggregateLogicalVersion: 1, OperationID: priorOperation, RuntimeEpochID: "checkpoint-epoch",
		LeaseID: "checkpoint-lease", LeaseGeneration: 1, FenceToken: 1,
		EventIndex: eventIndex, EventCount: eventCount, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored := controljournal.StoredEventV1{RecordRevision: 7, Envelope: event}
	journal, err := controljournal.ValidateJournalV1(eventRegistry, []controljournal.StoredEventV1{stored})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := controljournal.EncodeEventEnvelopeV1(event)
	if err != nil {
		t.Fatal(err)
	}
	var folded bytes.Buffer
	folded.Write(make([]byte, sha256.Size))
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], stored.RecordRevision)
	folded.Write(number[:])
	binary.BigEndian.PutUint64(number[:], uint64(len(encoded)))
	folded.Write(number[:])
	folded.Write(encoded)
	watermark := journalcheckpoint.WatermarkV1{
		ThroughRecordRevision: stored.RecordRevision, ThroughEventID: event.EventID,
		EventCount: 1, EventPrefixDigest: sha256.Sum256(folded.Bytes()),
	}
	state, err := reducer.NewStateV1(payload)
	if err != nil {
		t.Fatal(err)
	}
	eventDigest, err := eventRegistry.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reducerDigest, err := reducers.Digest()
	if err != nil {
		t.Fatal(err)
	}
	capability, err := journalcheckpoint.BuildV1(journalcheckpoint.BuildInputV1{
		CheckpointID: "checkpoint-prior", PriorPrefix: journal, EventRegistry: eventRegistry, Reducers: reducers,
		Source: journalcheckpoint.SourceIdentityV1{
			ProjectID: controljournal.ProjectID(project), MissionID: controljournal.MissionID(mission), RunID: "checkpoint-run",
			ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest,
		},
		Watermark: watermark,
		Aggregates: []journalcheckpoint.AggregateSnapshotV1{{
			Kind: "mission", ID: "checkpoint-aggregate", LogicalVersion: 1, RuntimeEpochID: "checkpoint-epoch",
			LastEventID: event.EventID, State: state.CanonicalBytes(), StateDigest: state.Digest(),
		}},
		Authority: journalcheckpoint.AuthoritySummaryV1{
			RunID: "checkpoint-run", RuntimeEpochID: "checkpoint-epoch", LeaseID: "checkpoint-lease",
			LeaseGeneration: 1, FenceToken: 1, DurableRevision: stored.RecordRevision,
		},
		FormingBatchOperation: formingOperation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return capability
}

func outcomeOf(err error) OutcomeKind {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return ""
}
