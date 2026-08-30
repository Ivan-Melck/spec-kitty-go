package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

type memoryPort struct {
	mu              sync.Mutex
	revision        uint64
	records         map[string]storegapdb.Record
	batchCalls      int
	getCalls        int
	beforeBatch     func(storegapdb.ProductBatch) error
	ambiguousAfter  bool
	malformedResult bool
}

func newMemoryPort() *memoryPort {
	return &memoryPort{records: make(map[string]storegapdb.Record)}
}

func (p *memoryPort) Get(_ context.Context, key string) (storegapdb.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.getCalls++
	record, found := p.records[key]
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
	admission, result, err := AdmitRuntimeEpochV1(t.Context(), port, EpochAdmissionInputV1{
		ProjectID: "project", MissionID: "mission", OperationID: controljournal.OperationID("admit-" + suffix), EventID: controljournal.EventID("epoch-event-" + suffix),
		NewRuntimeEpochID: controljournal.RuntimeEpochID("epoch-" + suffix), HostInstanceID: "host-1",
		StartedAtObservation: "2026-08-30T12:00:00Z", SidecarPath: "/opt/spec-kitty/gapdbd",
		SidecarSHA256: strings.Repeat("a", 64), DatabaseID: "database-1", OwnerPIDObservation: 42,
		OwnerStartedAtObservation: "2026-08-30T12:00:00Z",
		ExpectedRuntimeHead:       controljournal.ExpectedAggregateHeadV1{AggregateKind: "runtime", AggregateID: "host-runtime"},
	})
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
	second, result, err := AdmitRuntimeEpochV1(t.Context(), port, EpochAdmissionInputV1{
		ProjectID: "project", MissionID: "mission", OperationID: "admit-owner-2", EventID: "epoch-event-owner-2", NewRuntimeEpochID: "epoch-owner-2",
		PreviousRuntimeEpochID: firstEpoch, HostInstanceID: "host-1", StartedAtObservation: "2026-08-30T12:01:00Z",
		SidecarPath: "/opt/spec-kitty/gapdbd", SidecarSHA256: strings.Repeat("b", 64), DatabaseID: "database-1",
		OwnerPIDObservation: 43, OwnerStartedAtObservation: "2026-08-30T12:01:00Z",
		ExpectedRuntimeHead:     controljournal.ExpectedAggregateHeadV1{AggregateKind: "runtime", AggregateID: "host-runtime", LogicalVersion: 1, ObservedHeadRevision: current.Revision},
		ObservedCurrentRevision: current.Revision,
	})
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
}

func TestEpochAdmissionValidatesCompleteAckAndThreadsReconcileContext(t *testing.T) {
	malformed := newMemoryPort()
	malformed.malformedResult = true
	_, _, err := AdmitRuntimeEpochV1(t.Context(), malformed, epochAdmissionFixture("bad-ack"))
	if outcomeOf(err) != OutcomeIndeterminate {
		t.Fatalf("malformed five-record acknowledgement survived: %v", err)
	}

	base := newMemoryPort()
	input := epochAdmissionFixture("context")
	if _, result, err := AdmitRuntimeEpochV1(t.Context(), base, input); err != nil || result.Kind != OutcomeCommitted {
		t.Fatalf("initial admission result=%+v err=%v", result, err)
	}
	wrapped := &contextAuditEpochPort{memoryPort: base}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, result, err := AdmitRuntimeEpochV1(canceled, wrapped, input)
	if err != nil || result.Kind != OutcomeIndeterminate || !wrapped.observedCancellation {
		t.Fatalf("reconcile context result=%+v observed=%t err=%v", result, wrapped.observedCancellation, err)
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

func epochAdmissionFixture(suffix string) EpochAdmissionInputV1 {
	return EpochAdmissionInputV1{
		ProjectID: "project", MissionID: "mission", OperationID: controljournal.OperationID("admit-" + suffix), EventID: controljournal.EventID("epoch-event-" + suffix),
		NewRuntimeEpochID: controljournal.RuntimeEpochID("epoch-" + suffix), HostInstanceID: "host-1",
		StartedAtObservation: "2026-08-30T12:00:00Z", SidecarPath: "/opt/spec-kitty/gapdbd",
		SidecarSHA256: strings.Repeat("a", 64), DatabaseID: "database-1", OwnerPIDObservation: 42,
		OwnerStartedAtObservation: "2026-08-30T12:00:00Z",
		ExpectedRuntimeHead:       controljournal.ExpectedAggregateHeadV1{AggregateKind: "runtime", AggregateID: "host-runtime"},
	}
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

func TestServiceErrorsContainNoPayload(t *testing.T) {
	secret := "super-secret-payload"
	_, err := ValidateCommandEnvelopeV1(CommandEnvelopeInputV1{SchemaVersion: 1, ProjectID: "project", MissionID: "mission", OperationID: "operation", CommandType: "mission.advance", RuntimeEpochID: "epoch", Payload: []byte(`{"` + secret + `":`)})
	if err == nil || errors.Is(err, context.Canceled) || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
}
