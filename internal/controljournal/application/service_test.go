package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

type memoryPort struct {
	mu               sync.Mutex
	revision         uint64
	records          map[string]storegapdb.Record
	batchCalls       int
	preflightCalls   int
	getCalls         int
	afterGet         func(string, *memoryPort)
	beforeBatch      func(storegapdb.ProductBatch) error
	beforeEpochBatch func(storegapdb.RuntimeEpochBatch) error
	ambiguousAfter   bool
	malformedResult  bool
	preflightResult  *storegapdb.BatchMeasurement
	preflightErr     error
}

func (p *memoryPort) PreflightAtomicBatch(batch storegapdb.ProductBatch) (storegapdb.BatchMeasurement, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.preflightCalls++
	if p.preflightErr != nil {
		return storegapdb.BatchMeasurement{}, p.preflightErr
	}
	if p.preflightResult != nil {
		return *p.preflightResult, nil
	}
	raw := 0
	for _, mutation := range productMutations(batch) {
		raw += len(mutation.Key) + len(mutation.Value)
	}
	return storegapdb.BatchMeasurement{MutationCount: len(productMutations(batch)), RawBytes: raw, WireBytes: raw + 1}, nil
}

func newMemoryPort() *memoryPort {
	return &memoryPort{records: make(map[string]storegapdb.Record)}
}

func (p *memoryPort) Get(_ context.Context, key string) (storegapdb.Record, error) {
	p.mu.Lock()
	p.getCalls++
	record, found := p.records[key]
	hook := p.afterGet
	p.mu.Unlock()
	if hook != nil {
		hook(key, p)
	}
	if !found {
		return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeNotFound, Operation: "get", Recovery: storegapdb.RecoveryRead}
	}
	record.Value = append([]byte(nil), record.Value...)
	return record, nil
}

func (p *memoryPort) AtomicBatch(_ context.Context, batch storegapdb.ProductBatch) (storegapdb.MutationResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.batchCalls++
	if p.beforeBatch != nil {
		if err := p.beforeBatch(cloneProductBatch(batch)); err != nil {
			return storegapdb.MutationResult{}, err
		}
	}
	mutations := productMutations(batch)
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if _, duplicate := seen[mutation.Key]; duplicate {
			return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "atomic_batch"}
		}
		seen[mutation.Key] = struct{}{}
		record, exists := p.records[mutation.Key]
		switch mutation.Condition.Kind {
		case storegapdb.ConditionAbsent:
			if exists {
				return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "atomic_batch"}
			}
		case storegapdb.ConditionRevision:
			if !exists || record.Revision != mutation.Condition.ExpectedRevision {
				return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "atomic_batch"}
			}
		default:
			return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeInvalid, Operation: "atomic_batch"}
		}
	}
	p.revision++
	commitRevision := p.revision
	for _, mutation := range mutations {
		p.records[mutation.Key] = storegapdb.Record{Key: mutation.Key, Value: append([]byte(nil), mutation.Value...), Revision: commitRevision}
	}
	if p.ambiguousAfter {
		p.ambiguousAfter = false
		return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeAmbiguous, Operation: "atomic_batch", OperationID: batch.OperationID, Recovery: storegapdb.RecoveryReconcile}
	}
	result := storegapdb.MutationResult{Revision: commitRevision, Ack: storegapdb.AckDurable, DurableThrough: commitRevision, MutationCount: len(mutations)}
	if p.malformedResult {
		result.MutationCount++
	}
	return result, nil
}

func (p *memoryPort) AdmitRuntimeEpoch(_ context.Context, batch storegapdb.RuntimeEpochBatch) (storegapdb.MutationResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.batchCalls++
	if p.beforeEpochBatch != nil {
		if err := p.beforeEpochBatch(batch); err != nil {
			return storegapdb.MutationResult{}, err
		}
	}
	mutations := []storegapdb.Mutation{batch.Operation, batch.Event, batch.AggregateHead, batch.EpochRecord, batch.CurrentBinding}
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if _, duplicate := seen[mutation.Key]; duplicate {
			return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "admit_runtime_epoch"}
		}
		seen[mutation.Key] = struct{}{}
		record, exists := p.records[mutation.Key]
		switch mutation.Condition.Kind {
		case storegapdb.ConditionAbsent:
			if exists {
				return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "admit_runtime_epoch"}
			}
		case storegapdb.ConditionRevision:
			if !exists || record.Revision != mutation.Condition.ExpectedRevision {
				return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "admit_runtime_epoch"}
			}
		default:
			return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeInvalid, Operation: "admit_runtime_epoch"}
		}
	}
	p.revision++
	for _, mutation := range mutations {
		p.records[mutation.Key] = storegapdb.Record{Key: mutation.Key, Value: append([]byte(nil), mutation.Value...), Revision: p.revision}
	}
	count := len(mutations)
	if p.malformedResult {
		count++
	}
	return storegapdb.MutationResult{Revision: p.revision, Ack: storegapdb.AckDurable, DurableThrough: p.revision, MutationCount: count}, nil
}

func (p *memoryPort) snapshot() map[string]storegapdb.Record {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make(map[string]storegapdb.Record, len(p.records))
	for key, value := range p.records {
		value.Value = append([]byte(nil), value.Value...)
		result[key] = value
	}
	return result
}

func admittedService(t *testing.T, port *memoryPort, suffix string) (*Service, EpochAdmissionV1) {
	t.Helper()
	admission, result, err := admitRuntimeEpochFactsV1(t.Context(), port, epochAdmissionFixture(suffix))
	if err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("admission result=%+v err=%v", result, err)
	}
	service, err := NewService(port, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, admission
}

func TestServiceOneBatchStableIdentityAndNoSilentRetry(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "service")
	batch := batchFixture(t, "service", 2, 1, 1)
	before := port.batchCalls
	result, err := service.Apply(t.Context(), batch)
	if err != nil || result.Kind != OutcomeCommitted || result.CommittedRevision == 0 || port.batchCalls != before+1 {
		t.Fatalf("first apply result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
	}
	firstRevision := result.CommittedRevision
	result, err = service.Apply(t.Context(), batch)
	if err != nil || result.Kind != OutcomeAlreadyCommitted || result.CommittedRevision != firstRevision || port.batchCalls != before+1 {
		t.Fatalf("same-ID retry result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
	}

	collisionInput := batchInputFixture(t, "service", 2, 1, 1)
	collisionInput.Command, err = ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation-service",
		CommandType: "mission.advance", RuntimeEpochID: "epoch-service", Payload: []byte(`{"changed":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range collisionInput.Events {
		collisionInput.Events[index] = eventFixture(t, controljournal.EventID("collision-event-"+string(rune('a'+index))), index, len(collisionInput.Events), collisionInput.Command.operationID, "aggregate-0", collisionInput.Command.runtimeEpochID)
	}
	for index := range collisionInput.NextHeads {
		digest := collisionInput.NextHeads[index].Head.StateDigest
		head, makeErr := controljournal.ConstructAggregateHeadV1("mission", collisionInput.NextHeads[index].Head.AggregateID, 1, collisionInput.Command.runtimeEpochID, collisionInput.Events[len(collisionInput.Events)-1].EventID, digest)
		if makeErr != nil {
			t.Fatal(makeErr)
		}
		collisionInput.NextHeads[index] = controljournal.NewAggregateHeadV1{Head: head}
	}
	collision, err := NewValidatedBatchV1(collisionInput)
	if err != nil {
		t.Fatal(err)
	}
	result, err = service.Apply(t.Context(), collision)
	if err != nil || result.Kind != OutcomeOperationIdentityCollision || port.batchCalls != before+1 {
		t.Fatalf("identity collision result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
	}
}

func TestServiceAmbiguityVisibleThenSameIDReadReconciles(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "ambiguous")
	batch := batchFixture(t, "ambiguous", 2, 1, 0)
	port.ambiguousAfter = true
	before := port.batchCalls
	result, err := service.Apply(t.Context(), batch)
	if err != nil || result.Kind != OutcomeAmbiguousResponse || result.CommittedRevision != 0 || port.batchCalls != before+1 {
		t.Fatalf("ambiguous apply result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
	}
	result, err = service.Reconcile(t.Context(), batch, ReconcileConfigV1{Attempts: 3, Deadline: time.Second})
	if err != nil || result.Kind != OutcomeAlreadyCommitted || result.CommittedRevision == 0 || port.batchCalls != before+1 {
		t.Fatalf("reconcile result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
	}
}

func TestServiceAggregateConcurrencyIsPerHeadNotGlobal(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "concurrent")
	makeBatch := func(operation, aggregate string) ValidatedBatchV1 {
		input := batchInputFixture(t, operation, 1, 1, 0)
		input.Command, _ = ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: controljournal.OperationID("operation-" + operation), CommandType: "mission.advance", RuntimeEpochID: "epoch-concurrent", Payload: []byte(`{"go":true}`)})
		input.Events[0] = eventFixture(t, controljournal.EventID("event-"+operation), 0, 1, input.Command.operationID, aggregate, input.Command.runtimeEpochID)
		input.ExpectedHeads[0] = controljournal.ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: controljournal.AggregateID(aggregate)}
		input.ExpectedPreconditionsDigest = [32]byte{}
		digest := sha256.Sum256([]byte(aggregate))
		head, _ := controljournal.ConstructAggregateHeadV1("mission", controljournal.AggregateID(aggregate), 1, input.Command.runtimeEpochID, input.Events[0].EventID, digest)
		input.NextHeads[0] = controljournal.NewAggregateHeadV1{Head: head}
		batch, err := NewValidatedBatchV1(input)
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	left, right := makeBatch("left", "aggregate-left"), makeBatch("right", "aggregate-right")
	results := make(chan ResultV1, 2)
	for _, batch := range []ValidatedBatchV1{left, right} {
		go func(batch ValidatedBatchV1) {
			result, _ := service.Apply(context.Background(), batch)
			results <- result
		}(batch)
	}
	for range 2 {
		if result := <-results; result.Kind != OutcomeCommitted {
			t.Fatalf("disjoint aggregate globally serialized: %+v", result)
		}
	}

	first, second := makeBatch("first", "aggregate-shared"), makeBatch("second", "aggregate-shared")
	for _, batch := range []ValidatedBatchV1{first, second} {
		go func(batch ValidatedBatchV1) {
			result, _ := service.Apply(context.Background(), batch)
			results <- result
		}(batch)
	}
	counts := map[OutcomeKind]int{}
	for range 2 {
		counts[(<-results).Kind]++
	}
	if counts[OutcomeCommitted] != 1 || counts[OutcomeLogicalVersionConflict] != 1 {
		t.Fatalf("same-head CAS outcomes=%v", counts)
	}
	for key := range port.snapshot() {
		if key == "spk/v1/project/mission/projection-head" || key == "spk/v1/project/mission/runtime/current-cas" {
			t.Fatalf("global CAS coordinate appeared: %s", key)
		}
	}
}

func TestServiceHeadPreflightProvesStoredSemanticPredecessor(t *testing.T) {
	for name, seed := range map[string]func(*testing.T, *memoryPort, ValidatedBatchV1){
		"dishonest logical at exact revision": func(t *testing.T, port *memoryPort, batch ValidatedBatchV1) {
			seedApplicationHead(t, port, batch, 5, 40, "mission", "aggregate-0")
		},
		"wrong coordinate": func(t *testing.T, port *memoryPort, batch ValidatedBatchV1) {
			seedApplicationHead(t, port, batch, 4, 40, "mission", "other-aggregate")
		},
		"corrupt bytes": func(_ *testing.T, port *memoryPort, batch ValidatedBatchV1) {
			port.mu.Lock()
			port.records[batch.product.AggregateHeads[0].Key] = storegapdb.Record{Key: batch.product.AggregateHeads[0].Key, Revision: 40, Value: []byte(`{"corrupt":true}`)}
			port.revision = 40
			port.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			suffix := "preflight-" + strings.ReplaceAll(name, " ", "-")
			port := newMemoryPort()
			service, _ := admittedService(t, port, suffix)
			batch := existingBatchFixture(t, suffix, 4, 40)
			seed(t, port, batch)
			before := port.batchCalls
			result, err := service.Apply(t.Context(), batch)
			if err != nil || port.batchCalls != before {
				t.Fatalf("preflight result=%+v mutation_calls=%d err=%v", result, port.batchCalls-before, err)
			}
			if name == "dishonest logical at exact revision" && result.Kind != OutcomeLogicalVersionConflict {
				t.Fatalf("dishonest logical result=%+v", result)
			}
			if name != "dishonest logical at exact revision" && result.Kind != OutcomeRefused {
				t.Fatalf("malformed predecessor result=%+v", result)
			}
			if _, found := port.snapshot()[batch.product.Operation.Key]; found {
				t.Fatal("preflight refusal published an operation")
			}
		})
	}

	t.Run("matching existing head", func(t *testing.T) {
		port := newMemoryPort()
		service, _ := admittedService(t, port, "preflight-match")
		batch := existingBatchFixture(t, "preflight-match", 4, 40)
		seedApplicationHead(t, port, batch, 4, 40, "mission", "aggregate-0")
		result, err := service.Apply(t.Context(), batch)
		if err != nil || result.Kind != OutcomeCommitted {
			t.Fatalf("matching predecessor result=%+v err=%v", result, err)
		}
	})
}

func TestServiceHeadPreflightRetainsCASRaceProtection(t *testing.T) {
	t.Run("appearance after absence read", func(t *testing.T) {
		port := newMemoryPort()
		service, _ := admittedService(t, port, "appearance-race")
		batch := batchFixture(t, "appearance-race", 1, 1, 0)
		target := batch.product.AggregateHeads[0].Key
		port.afterGet = func(key string, port *memoryPort) {
			if key != target {
				return
			}
			port.mu.Lock()
			port.afterGet = nil
			port.mu.Unlock()
			seedApplicationHead(t, port, batch, 1, 20, "mission", "aggregate-0")
		}
		before := port.batchCalls
		result, err := service.Apply(t.Context(), batch)
		if err != nil || result.Kind != OutcomeLogicalVersionConflict || port.batchCalls != before+1 {
			t.Fatalf("appearance race result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
		}
		assertNoOperationPublication(t, port, batch)
	})

	t.Run("change after existing read", func(t *testing.T) {
		port := newMemoryPort()
		service, _ := admittedService(t, port, "change-race")
		batch := existingBatchFixture(t, "change-race", 4, 40)
		seedApplicationHead(t, port, batch, 4, 40, "mission", "aggregate-0")
		target := batch.product.AggregateHeads[0].Key
		port.afterGet = func(key string, port *memoryPort) {
			if key != target {
				return
			}
			port.mu.Lock()
			port.afterGet = nil
			port.mu.Unlock()
			seedApplicationHead(t, port, batch, 5, 41, "mission", "aggregate-0")
		}
		before := port.batchCalls
		result, err := service.Apply(t.Context(), batch)
		if err != nil || result.Kind != OutcomeLogicalVersionConflict || port.batchCalls != before+1 {
			t.Fatalf("change race result=%+v calls=%d err=%v", result, port.batchCalls-before, err)
		}
		assertNoOperationPublication(t, port, batch)
	})
}

func existingBatchFixture(t *testing.T, suffix string, logicalVersion, revision uint64) ValidatedBatchV1 {
	t.Helper()
	input := batchInputFixture(t, suffix, 1, 1, 0)
	input.ExpectedHeads[0].LogicalVersion = logicalVersion
	input.ExpectedHeads[0].ObservedHeadRevision = revision
	input.ExpectedPreconditionsDigest = [32]byte{}
	input.Events[0] = eventFixtureVersion(t, input.Events[0].EventID, 0, 1, input.Command.operationID, "aggregate-0", input.Command.runtimeEpochID, logicalVersion+1)
	digest := sha256.Sum256([]byte("next-" + suffix))
	head, err := controljournal.ConstructAggregateHeadV1("mission", "aggregate-0", logicalVersion+1, input.Command.runtimeEpochID, input.Events[0].EventID, digest)
	if err != nil {
		t.Fatal(err)
	}
	input.NextHeads[0] = controljournal.NewAggregateHeadV1{Head: head}
	batch, err := NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func seedApplicationHead(t *testing.T, port *memoryPort, batch ValidatedBatchV1, logicalVersion, revision uint64, kind controljournal.AggregateKind, id controljournal.AggregateID) {
	t.Helper()
	digest := sha256.Sum256([]byte("stored-head"))
	head, err := controljournal.ConstructAggregateHeadV1(kind, id, logicalVersion, batch.command.runtimeEpochID, "prior-event", digest)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := controljournal.EncodeAggregateHeadV1(head)
	if err != nil {
		t.Fatal(err)
	}
	port.mu.Lock()
	port.records[batch.product.AggregateHeads[0].Key] = storegapdb.Record{Key: batch.product.AggregateHeads[0].Key, Revision: revision, Value: encoded}
	if port.revision < revision {
		port.revision = revision
	}
	port.mu.Unlock()
}

func assertNoOperationPublication(t *testing.T, port *memoryPort, batch ValidatedBatchV1) {
	t.Helper()
	if _, found := port.snapshot()[batch.product.Operation.Key]; found {
		t.Fatal("racing CAS published a partial operation")
	}
}

func TestEpochAdmissionOnlyAtOwnerRestart(t *testing.T) {
	port := newMemoryPort()
	service, first := admittedService(t, port, "owner-1")
	firstEpoch, _ := first.RuntimeEpochID()
	batch := batchFixture(t, "owner-1", 1, 1, 0)
	if result, err := service.Apply(t.Context(), batch); err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("ordinary command failed: %+v %v", result, err)
	}
	currentKey := "spk/v1/project/mission/runtime/current"
	current := port.snapshot()[currentKey]
	if current.Revision == 0 {
		t.Fatal("runtime binding absent")
	}
	// Lease expiry has no epoch API and therefore leaves the service pin and
	// durable current binding unchanged.
	if epoch, _ := first.RuntimeEpochID(); epoch != firstEpoch || port.snapshot()[currentKey].Revision != current.Revision {
		t.Fatal("ordinary lifetime activity advanced the epoch")
	}
	beforeRestart := port.snapshot()
	secondInput := epochAdmissionFixture("owner-2")
	secondInput.Startup.DatabaseID = "database-owner-1"
	secondInput.Startup.HostInstanceID = "host-2"
	secondInput.Startup.Daemon.PID = 43
	secondInput.Startup.Daemon.StartTicks = 43
	secondInput.Startup.ExecutableSHA256 = strings.Repeat("b", 64)
	secondInput.Startup.AdmittedAt = "2026-08-30T12:01:00Z"
	secondInput.Startup.DaemonOwnerStartedAt = "2026-08-30T12:01:00Z"
	secondInput.Restore.Binding.DatabaseID = secondInput.Startup.DatabaseID
	secondInput.Restore.Binding.HostInstanceID = secondInput.Startup.HostInstanceID
	second, result, err := admitRuntimeEpochFactsV1(t.Context(), port, secondInput)
	if err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("restart admission=%+v err=%v", result, err)
	}
	secondEpoch, _ := second.RuntimeEpochID()
	if secondEpoch == firstEpoch {
		t.Fatal("owner restart reused runtime epoch")
	}
	afterRestart := port.snapshot()
	for key, before := range beforeRestart {
		if key == currentKey || key == "spk/v1/project/mission/heads/runtime/host-runtime" {
			continue
		}
		if after, found := afterRestart[key]; !found || !reflect.DeepEqual(after, before) {
			t.Fatalf("epoch transition changed preexisting durable record %q: before=%+v after=%+v", key, before, after)
		}
	}
	newService, err := NewService(port, second)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := newService.Apply(t.Context(), batch); err != nil || result.Kind != OutcomeRuntimeEpochConflict {
		t.Fatalf("predecessor epoch command result=%+v err=%v", result, err)
	}
	retained := batchForEpoch(t, "owner-retained", firstEpoch, "aggregate-retained")
	copiedService := *service
	beforeRetained := port.batchCalls
	if result, err := service.Apply(t.Context(), retained); err != nil || result.Kind != OutcomeRuntimeEpochConflict || port.batchCalls != beforeRetained {
		t.Fatalf("retained predecessor service result=%+v calls=%d err=%v", result, port.batchCalls-beforeRetained, err)
	}
	if _, found := port.snapshot()[retained.product.Operation.Key]; found {
		t.Fatal("revoked predecessor service published untouched aggregate operation")
	}
	if result, err := copiedService.Apply(t.Context(), batchForEpoch(t, "owner-copied", firstEpoch, "aggregate-copied")); err != nil || result.Kind != OutcomeRuntimeEpochConflict {
		t.Fatalf("copied predecessor service result=%+v err=%v", result, err)
	}
}

func TestEpochReplacementLinearizesAgainstInFlightPredecessor(t *testing.T) {
	for trial := 0; trial < 25; trial++ {
		t.Run(fmt.Sprintf("replacement-first-%02d", trial), func(t *testing.T) {
			suffix := fmt.Sprintf("race-replacement-%02d", trial)
			port := newMemoryPort()
			oldService, oldAdmission := admittedService(t, port, suffix)
			oldEpoch, _ := oldAdmission.RuntimeEpochID()
			next := replacementAdmissionFixture(suffix, port)
			entered, release := make(chan struct{}), make(chan struct{})
			port.beforeEpochBatch = func(storegapdb.RuntimeEpochBatch) error {
				close(entered)
				<-release
				return nil
			}
			type admissionOutcome struct {
				result ResultV1
				err    error
			}
			admitted := make(chan admissionOutcome, 1)
			go func() {
				_, result, err := admitRuntimeEpochFactsV1(context.Background(), port, next)
				admitted <- admissionOutcome{result: result, err: err}
			}()
			<-entered
			oldBatch := batchForEpoch(t, suffix+"-old", oldEpoch, "aggregate-race-old")
			oldResult := make(chan ResultV1, 1)
			oldStarted := make(chan struct{})
			go func() {
				close(oldStarted)
				result, _ := oldService.Apply(context.Background(), oldBatch)
				oldResult <- result
			}()
			<-oldStarted
			select {
			case result := <-oldResult:
				t.Fatalf("old operation crossed exclusive transition guard: %+v", result)
			case <-time.After(10 * time.Millisecond):
			}
			close(release)
			if outcome := <-admitted; outcome.err != nil || outcome.result.Kind != OutcomeCommitted {
				t.Fatalf("replacement outcome=%+v err=%v", outcome.result, outcome.err)
			}
			if result := <-oldResult; result.Kind != OutcomeRuntimeEpochConflict {
				t.Fatalf("post-replacement old result=%+v", result)
			}
		})

		t.Run(fmt.Sprintf("operation-first-%02d", trial), func(t *testing.T) {
			suffix := fmt.Sprintf("race-operation-%02d", trial)
			port := newMemoryPort()
			oldService, oldAdmission := admittedService(t, port, suffix)
			oldEpoch, _ := oldAdmission.RuntimeEpochID()
			oldBatch := batchForEpoch(t, suffix+"-old", oldEpoch, "aggregate-operation-first")
			next := replacementAdmissionFixture(suffix, port)
			entered, release := make(chan struct{}), make(chan struct{})
			port.beforeBatch = func(storegapdb.ProductBatch) error {
				close(entered)
				<-release
				return nil
			}
			oldResult := make(chan ResultV1, 1)
			go func() {
				result, _ := oldService.Apply(context.Background(), oldBatch)
				oldResult <- result
			}()
			<-entered
			type admissionOutcome struct {
				result ResultV1
				err    error
			}
			admitted := make(chan admissionOutcome, 1)
			go func() {
				_, result, err := admitRuntimeEpochFactsV1(context.Background(), port, next)
				admitted <- admissionOutcome{result: result, err: err}
			}()
			close(release)
			committed := <-oldResult
			if committed.Kind != OutcomeCommitted {
				t.Fatalf("pre-transition operation result=%+v", committed)
			}
			outcome := <-admitted
			if outcome.err != nil || outcome.result.Kind != OutcomeCommitted || outcome.result.CommittedRevision <= committed.CommittedRevision {
				t.Fatalf("replacement outcome=%+v old=%+v err=%v", outcome.result, committed, outcome.err)
			}
		})
	}
}

func replacementAdmissionFixture(suffix string, port *memoryPort) epochAdmissionRequestV1 {
	next := epochAdmissionFixture(suffix + "-replacement")
	next.Startup.DatabaseID = "database-" + suffix
	next.Startup.HostInstanceID = "host-replacement-" + suffix
	next.Startup.Daemon.PID++
	next.Startup.Daemon.StartTicks++
	next.Startup.AdmittedAt = "2026-08-30T12:01:00Z"
	next.Startup.DaemonOwnerStartedAt = "2026-08-30T12:01:00Z"
	next.Restore.Binding.DatabaseID = next.Startup.DatabaseID
	next.Restore.Binding.HostInstanceID = next.Startup.HostInstanceID
	port.mu.Lock()
	next.Restore.ObservedStoreRevision = max(uint64(1), port.revision)
	port.mu.Unlock()
	return next
}

func batchForEpoch(t *testing.T, suffix string, epoch controljournal.RuntimeEpochID, aggregate controljournal.AggregateID) ValidatedBatchV1 {
	t.Helper()
	input := batchInputFixture(t, suffix, 1, 1, 0)
	var err error
	input.Command, err = ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{
		SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: controljournal.OperationID("operation-" + suffix),
		CommandType: "mission.advance", RuntimeEpochID: epoch, Payload: []byte(`{"retained":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Events[0] = eventFixture(t, controljournal.EventID("event-"+suffix), 0, 1, input.Command.operationID, string(aggregate), epoch)
	input.ExpectedHeads[0] = controljournal.ExpectedAggregateHeadV1{AggregateKind: "mission", AggregateID: aggregate}
	digest := sha256.Sum256([]byte("state-" + suffix))
	head, err := controljournal.ConstructAggregateHeadV1("mission", aggregate, 1, epoch, input.Events[0].EventID, digest)
	if err != nil {
		t.Fatal(err)
	}
	input.NextHeads[0] = controljournal.NewAggregateHeadV1{Head: head}
	input.ExpectedPreconditionsDigest = [32]byte{}
	batch, err := NewValidatedBatchV1(input)
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestEpochAdmissionValidatesCompleteAckAndThreadsReconcileContext(t *testing.T) {
	malformed := newMemoryPort()
	malformed.malformedResult = true
	_, _, err := admitRuntimeEpochFactsV1(t.Context(), malformed, epochAdmissionFixture("bad-ack"))
	if outcomeOf(err) != OutcomeIndeterminate {
		t.Fatalf("malformed five-record acknowledgement survived: %v", err)
	}

	base := newMemoryPort()
	input := epochAdmissionFixture("context")
	if _, result, err := admitRuntimeEpochFactsV1(t.Context(), base, input); err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("initial admission result=%+v err=%v", result, err)
	}
	wrapped := &contextAuditEpochPort{memoryPort: base}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, result, err := admitRuntimeEpochFactsV1(canceled, wrapped, input)
	if err != nil || result.Kind != OutcomeIndeterminate || !wrapped.observedCancellation {
		t.Fatalf("reconcile context result=%+v observed=%t err=%v", result, wrapped.observedCancellation, err)
	}
}

func TestEpochAdmissionRequiresSealedOwnerStartupProof(t *testing.T) {
	port := newMemoryPort()
	_, result, err := AdmitRuntimeEpochV1(t.Context(), port, EpochAdmissionInputV1{
		ProjectID: "project", MissionID: "mission", OperationID: "admit-no-proof", EventID: "event-no-proof",
	})
	if outcomeOf(err) != OutcomeRefused || result.Kind != "" || port.batchCalls != 0 || port.getCalls != 0 {
		t.Fatalf("unsealed admission result=%+v reads=%d mutations=%d err=%v", result, port.getCalls, port.batchCalls, err)
	}
}

func TestEpochAdmissionRequiresMatchingMissionRestoreFactsBeforeReads(t *testing.T) {
	for name, mutate := range map[string]func(*epochAdmissionRequestV1){
		"project":  func(input *epochAdmissionRequestV1) { input.Restore.Source.ProjectID = "other-project" },
		"mission":  func(input *epochAdmissionRequestV1) { input.Restore.Source.MissionID = "other-mission" },
		"database": func(input *epochAdmissionRequestV1) { input.Restore.Binding.DatabaseID = "other-database" },
		"host":     func(input *epochAdmissionRequestV1) { input.Restore.Binding.HostInstanceID = "other-host" },
		"revision": func(input *epochAdmissionRequestV1) {
			input.Restore.ObservedStoreRevision = 0
			input.Restore.Pristine = false
		},
		"projection": func(input *epochAdmissionRequestV1) { input.Restore.CanonicalProjectionDigest = [32]byte{} },
	} {
		t.Run(name, func(t *testing.T) {
			port := newMemoryPort()
			input := epochAdmissionFixture("restore-mismatch-" + name)
			mutate(&input)
			_, result, err := admitRuntimeEpochFactsV1(t.Context(), port, input)
			if outcomeOf(err) != OutcomeRefused || result.Kind != "" || port.getCalls != 0 || port.batchCalls != 0 {
				t.Fatalf("restore mismatch result=%+v reads=%d mutations=%d err=%v", result, port.getCalls, port.batchCalls, err)
			}
		})
	}
}

func TestEpochAdmissionDerivesAndProvesDurablePredecessor(t *testing.T) {
	for name, corrupt := range map[string]func(*testing.T, *memoryPort){
		"false predecessor at exact revisions": func(t *testing.T, port *memoryPort) {
			key := "spk/v1/project/mission/runtime/current"
			port.mu.Lock()
			record := port.records[key]
			current, err := decodeRuntimeCurrentV1(record.Value)
			if err != nil {
				port.mu.Unlock()
				t.Fatal(err)
			}
			current.RuntimeEpochID = "epoch-false-predecessor"
			record.Value, err = json.Marshal(current)
			if err != nil {
				port.mu.Unlock()
				t.Fatal(err)
			}
			port.records[key] = record
			port.mu.Unlock()
		},
		"corrupt current": func(_ *testing.T, port *memoryPort) {
			port.mu.Lock()
			record := port.records["spk/v1/project/mission/runtime/current"]
			record.Value = []byte(`{"corrupt":true}`)
			port.records[record.Key] = record
			port.mu.Unlock()
		},
		"wrong runtime coordinate": func(t *testing.T, port *memoryPort) {
			key := "spk/v1/project/mission/heads/runtime/host-runtime"
			port.mu.Lock()
			record := port.records[key]
			head, err := controljournal.DecodeAggregateHeadV1(record.Value)
			if err != nil {
				port.mu.Unlock()
				t.Fatal(err)
			}
			head.AggregateID = "other-runtime"
			record.Value, err = controljournal.EncodeAggregateHeadV1(head)
			if err != nil {
				port.mu.Unlock()
				t.Fatal(err)
			}
			port.records[key] = record
			port.mu.Unlock()
		},
	} {
		t.Run(name, func(t *testing.T) {
			port := newMemoryPort()
			if _, result, err := admitRuntimeEpochFactsV1(t.Context(), port, epochAdmissionFixture("lineage-first")); err != nil || result.Kind != OutcomeCommitted {
				t.Fatalf("first admission result=%+v err=%v", result, err)
			}
			corrupt(t, port)
			before := port.batchCalls
			next := epochAdmissionFixture("lineage-next")
			next.Startup.DatabaseID = "database-lineage-first"
			next.Startup.HostInstanceID = "host-lineage-next"
			next.Restore.Binding.DatabaseID = next.Startup.DatabaseID
			next.Restore.Binding.HostInstanceID = next.Startup.HostInstanceID
			_, result, err := admitRuntimeEpochFactsV1(t.Context(), port, next)
			if err != nil || result.Kind != OutcomeRefused || port.batchCalls != before {
				t.Fatalf("lineage refusal result=%+v mutation_calls=%d err=%v", result, port.batchCalls-before, err)
			}
			if _, found := port.snapshot()["spk/v1/project/mission/operations/admit-lineage-next"]; found {
				t.Fatal("lineage refusal published a new operation")
			}
		})
	}
}

func TestEpochAdmissionSameHostEpochIsReadOnlyReconciliation(t *testing.T) {
	port := newMemoryPort()
	first := epochAdmissionFixture("same-host")
	_, result, err := admitRuntimeEpochFactsV1(t.Context(), port, first)
	if err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("first admission result=%+v err=%v", result, err)
	}
	beforeRecords, beforeMutations := port.snapshot(), port.batchCalls
	repeated := first
	repeated.OperationID = "admit-same-host-second"
	repeated.EventID = "epoch-event-same-host-second"
	admission, result, err := admitRuntimeEpochFactsV1(t.Context(), port, repeated)
	if err != nil || result.Kind != OutcomeAlreadyCommitted || port.batchCalls != beforeMutations || !admission.valid() {
		t.Fatalf("same-host admission result=%+v mutation_calls=%d err=%v", result, port.batchCalls-beforeMutations, err)
	}
	if !reflect.DeepEqual(beforeRecords, port.snapshot()) {
		t.Fatal("same-host admission produced a second event or changed current")
	}
	port.mu.Lock()
	delete(port.records, "spk/v1/project/mission/journal/"+string(first.EventID))
	port.mu.Unlock()
	repeated.OperationID = "admit-same-host-third"
	repeated.EventID = "epoch-event-same-host-third"
	beforeMutations = port.batchCalls
	_, result, err = admitRuntimeEpochFactsV1(t.Context(), port, repeated)
	if err != nil || result.Kind != OutcomeIndeterminate || port.batchCalls != beforeMutations {
		t.Fatalf("incomplete same-host proof result=%+v mutation_calls=%d err=%v", result, port.batchCalls-beforeMutations, err)
	}
}

type contextAuditEpochPort struct {
	*memoryPort
	reads                int
	observedCancellation bool
}

func (p *contextAuditEpochPort) Get(ctx context.Context, key string) (storegapdb.Record, error) {
	p.reads++
	if p.reads > 1 && ctx.Err() != nil {
		p.observedCancellation = true
		return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeCanceled, Operation: "get", Recovery: storegapdb.RecoveryRead, Cause: ctx.Err()}
	}
	return p.memoryPort.Get(context.Background(), key)
}

func epochAdmissionFixture(suffix string) epochAdmissionRequestV1 {
	request := epochAdmissionRequestV1{
		ProjectID: "project", MissionID: "mission", OperationID: controljournal.OperationID("admit-" + suffix), EventID: controljournal.EventID("epoch-event-" + suffix),
		Startup: sidecargapdb.OwnerStartupFactsV1{
			RuntimeEpochID: "epoch-" + controljournal.RuntimeEpochID(suffix), HostInstanceID: "host-1", SupervisorRunID: "run-1",
			Supervisor:     sidecargapdb.ProcessIdentity{PID: 41, StartTicks: 41, Device: 1, Inode: 1, Size: 1, SHA256: strings.Repeat("c", 64)},
			Daemon:         sidecargapdb.ProcessIdentity{PID: 42, StartTicks: 42, Device: 2, Inode: 2, Size: 2, SHA256: strings.Repeat("a", 64)},
			ExecutablePath: "/opt/spec-kitty/gapdbd", ExecutableSHA256: strings.Repeat("a", 64), DatabaseID: "database-" + suffix,
			DaemonOwnerStartedAt: "2026-08-30T12:00:00Z", AdmittedAt: "2026-08-30T12:00:00Z",
		},
	}
	request.Restore = journalcheckpoint.RestoreAdmissionFactsV1{
		Source: journalcheckpoint.SourceIdentityV1{
			ProjectID: request.ProjectID, MissionID: request.MissionID, RunID: "run-restore-" + controljournal.RunID(suffix),
			ReducerSetDigest: sha256.Sum256([]byte("reducers-" + suffix)), EventSchemaSetDigest: sha256.Sum256([]byte("events-" + suffix)),
		},
		Binding:               journalcheckpoint.RestoreBindingV1{DatabaseID: request.Startup.DatabaseID, HostInstanceID: request.Startup.HostInstanceID},
		ObservedStoreRevision: 100, Pristine: true, CanonicalProjectionDigest: sha256.Sum256([]byte("projection-" + suffix)),
	}
	return request
}

func TestGJ01PreservesNonemptyCensusExactly(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "gj01")
	port.mu.Lock()
	port.records["preexisting/a"] = storegapdb.Record{Key: "preexisting/a", Value: []byte("alpha"), Revision: 41}
	port.records["preexisting/b"] = storegapdb.Record{Key: "preexisting/b", Value: []byte("beta"), Revision: 42}
	port.mu.Unlock()
	before := port.snapshot()
	port.beforeBatch = func(storegapdb.ProductBatch) error {
		return &storegapdb.Error{Kind: storegapdb.OutcomeUnavailable, Operation: "atomic_batch", Recovery: storegapdb.RecoveryRetrySameOperation}
	}
	result, err := service.Apply(t.Context(), batchFixture(t, "gj01", 1, 1, 0))
	if err != nil || result.Kind != OutcomeRefused {
		t.Fatalf("GJ-01 result=%+v err=%v", result, err)
	}
	after := port.snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("GJ-01 changed preexisting census\nbefore=%v\nafter=%v", before, after)
	}
	assertUniversalApplicationInvariants(t, port, batchFixture(t, "gj01", 1, 1, 0), false, 0)
}

func TestServiceMalformedAcknowledgementIsIndeterminate(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "malformed")
	port.malformedResult = true
	result, err := service.Apply(t.Context(), batchFixture(t, "malformed", 1, 1, 0))
	if err != nil || result.Kind != OutcomeIndeterminate || result.CommittedRevision != 0 {
		t.Fatalf("malformed acknowledgement result=%+v err=%v", result, err)
	}
}

func TestServiceRefusesUndecodableBoundOperationWithoutEmptyFallback(t *testing.T) {
	port := newMemoryPort()
	service, _ := admittedService(t, port, "operation-decode")
	batch := batchFixture(t, "operation-decode", 1, 1, 0)
	batch.operationRecordBytes = []byte(`{"corrupt":true}`)
	before := port.batchCalls
	result, err := service.Apply(t.Context(), batch)
	if err != nil || result.Kind != OutcomeIndeterminate || result.ResultType != "" || len(result.CanonicalResult) != 0 || port.batchCalls != before {
		t.Fatalf("operation decode result=%+v mutation_calls=%d err=%v", result, port.batchCalls-before, err)
	}
}

func TestServiceConsumesExactNonmutatingBatchPreflight(t *testing.T) {
	for name, measurement := range map[string]storegapdb.BatchMeasurement{
		"wire exact":    {MutationCount: 3, RawBytes: 1, WireBytes: storegapdb.MaxWireFrameBytes},
		"wire plus one": {MutationCount: 3, RawBytes: 1, WireBytes: storegapdb.MaxWireFrameBytes + 1},
		"wrong count":   {MutationCount: 2, RawBytes: 1, WireBytes: 1},
	} {
		t.Run(name, func(t *testing.T) {
			port := newMemoryPort()
			service, _ := admittedService(t, port, "preflight-"+strings.ReplaceAll(name, " ", "-"))
			batch := batchFixture(t, "preflight-"+strings.ReplaceAll(name, " ", "-"), 1, 1, 0)
			port.preflightResult = &measurement
			beforeMutations, beforePreflights := port.batchCalls, port.preflightCalls
			result, err := service.Apply(t.Context(), batch)
			if err != nil || port.preflightCalls != beforePreflights+1 {
				t.Fatalf("preflight result=%+v calls=%d err=%v", result, port.preflightCalls-beforePreflights, err)
			}
			if name == "wire exact" {
				if result.Kind != OutcomeCommitted || port.batchCalls != beforeMutations+1 {
					t.Fatalf("exact wire result=%+v mutation_calls=%d", result, port.batchCalls-beforeMutations)
				}
				return
			}
			if port.batchCalls != beforeMutations || result.Kind == OutcomeCommitted {
				t.Fatalf("invalid preflight result=%+v mutation_calls=%d", result, port.batchCalls-beforeMutations)
			}
			assertNoOperationPublication(t, port, batch)
		})
	}

	t.Run("adapter refusal", func(t *testing.T) {
		port := newMemoryPort()
		service, _ := admittedService(t, port, "preflight-refusal")
		batch := batchFixture(t, "preflight-refusal", 1, 1, 0)
		port.preflightErr = &storegapdb.Error{Kind: storegapdb.OutcomeInvalid, Operation: "preflight_atomic_batch"}
		before := port.batchCalls
		result, err := service.Apply(t.Context(), batch)
		if err != nil || result.Kind != OutcomeBudgetExceeded || port.batchCalls != before {
			t.Fatalf("refused preflight result=%+v mutation_calls=%d err=%v", result, port.batchCalls-before, err)
		}
		assertNoOperationPublication(t, port, batch)
	})
}

func TestServiceErrorsContainNoPayload(t *testing.T) {
	secret := "super-secret-payload"
	_, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation", CommandType: "mission.advance", RuntimeEpochID: "epoch", Payload: []byte(`{"` + secret + `":`)})
	if err == nil || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
}

type ordinaryReader interface {
	Get(context.Context, string) (storegapdb.Record, error)
}

func assertUniversalApplicationInvariants(t *testing.T, reader ordinaryReader, batch ValidatedBatchV1, applied bool, revision uint64) {
	t.Helper()
	mutations := productMutations(batch.product)
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if _, duplicate := seen[mutation.Key]; duplicate {
			t.Fatalf("universal invariant duplicate key=%q", mutation.Key)
		}
		seen[mutation.Key] = struct{}{}
		for _, forbidden := range []string{"/runtime/current", "/heads/lease/", "/heads/review/", "/heads/workflow/", "/heads/integration/"} {
			if strings.Contains(mutation.Key, forbidden) {
				t.Fatalf("universal invariant ordinary command crossed authority coordinate key=%q", mutation.Key)
			}
		}
		record, err := reader.Get(t.Context(), mutation.Key)
		if !applied {
			if !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				t.Fatalf("universal invariant refused batch left key=%q record=%+v err=%v", mutation.Key, record, err)
			}
			continue
		}
		if err != nil || record.Key != mutation.Key || record.Revision != revision || !bytes.Equal(record.Value, mutation.Value) {
			t.Fatalf("universal invariant committed member key=%q record=%+v revision=%d err=%v", mutation.Key, record, revision, err)
		}
	}
	if !applied {
		return
	}
	if _, err := controljournal.DecodeOperationRecordV1(batch.product.Operation.Value); err != nil {
		t.Fatalf("universal invariant operation codec: %v", err)
	}
	for _, mutation := range batch.product.Events {
		if _, err := controljournal.DecodeEventEnvelopeV1(mutation.Value); err != nil {
			t.Fatalf("universal invariant event codec: %v", err)
		}
	}
	for _, mutation := range batch.product.AggregateHeads {
		if _, err := controljournal.DecodeAggregateHeadV1(mutation.Value); err != nil {
			t.Fatalf("universal invariant head codec: %v", err)
		}
	}
	for _, mutation := range batch.product.EffectIntents {
		if _, err := controljournal.DecodeEffectIntentV1(mutation.Value); err != nil {
			t.Fatalf("universal invariant intent codec: %v", err)
		}
	}
}
