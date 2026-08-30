package gapdb

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

func TestBatchTranslatesOneProductBatchToOneAtomicCall(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		if batch.Ack != gapdbapi.AckDurable || len(batch.Mutations) != 5 {
			t.Fatalf("translated batch=%+v", batch)
		}
		conditions := map[string]gapdbapi.Condition{
			batch.Mutations[0].Key: batch.Mutations[0].Condition,
			batch.Mutations[1].Key: batch.Mutations[1].Condition,
			batch.Mutations[2].Key: batch.Mutations[2].Condition,
		}
		if conditions["operation"].Kind != gapdbapi.ConditionAbsent || conditions["event"].Kind != gapdbapi.ConditionAbsent || conditions["head"].ExpectedRevision != 41 {
			t.Fatalf("independent conditions lost: %+v", conditions)
		}
		return gapdbapi.MutationResult{Revision: 42, Ack: gapdbapi.AckDurable, DurableThroughRevision: 42, MutationCount: len(batch.Mutations)}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	checkpoint := put("checkpoint", []byte("checkpoint"), ConditionAbsent, 0)
	checkpointHead := put("checkpoint-head", []byte("head"), ConditionRevision, 40)
	batch := ProductBatch{
		OperationID: "op-1", Operation: put("operation", []byte("result"), ConditionAbsent, 0),
		Events:         []Mutation{put("event", []byte("event"), ConditionAbsent, 0)},
		AggregateHeads: []Mutation{put("head", []byte("head"), ConditionRevision, 41)},
		Checkpoint:     &checkpoint, CheckpointHead: &checkpointHead,
	}
	result, err := adapter.AtomicBatch(context.Background(), batch)
	if err != nil || result.Revision != 42 || result.MutationCount != 5 || calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestBatchCountBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ProductBatch)
		valid  bool
	}{
		{"events-min", func(batch *ProductBatch) { batch.Events = mutations("event", 1) }, true},
		{"events-max", func(batch *ProductBatch) { batch.Events = mutations("event", MaxEventsPerCommand) }, true},
		{"events-zero", func(batch *ProductBatch) { batch.Events = nil }, false},
		{"events-plus-one", func(batch *ProductBatch) { batch.Events = mutations("event", MaxEventsPerCommand+1) }, false},
		{"heads-min", func(batch *ProductBatch) { batch.AggregateHeads = mutations("head", 1) }, true},
		{"heads-max", func(batch *ProductBatch) { batch.AggregateHeads = mutations("head", MaxHeadsPerCommand) }, true},
		{"heads-zero", func(batch *ProductBatch) { batch.AggregateHeads = nil }, false},
		{"heads-plus-one", func(batch *ProductBatch) { batch.AggregateHeads = mutations("head", MaxHeadsPerCommand+1) }, false},
		{"intents-zero", func(batch *ProductBatch) { batch.EffectIntents = nil }, true},
		{"intents-max", func(batch *ProductBatch) { batch.EffectIntents = mutations("intent", MaxIntentsPerCommand) }, true},
		{"intents-plus-one", func(batch *ProductBatch) { batch.EffectIntents = mutations("intent", MaxIntentsPerCommand+1) }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := minimalBatch()
			test.mutate(&batch)
			_, _, err := encodeProductBatch(batch)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestProviderBatchAndWireCeilingsAreInclusive(t *testing.T) {
	if err := validateMutationCount(MaxMutationsPerBatch); err != nil {
		t.Fatalf("exact mutation ceiling rejected: %v", err)
	}
	if err := validateMutationCount(MaxMutationsPerBatch + 1); err == nil {
		t.Fatal("mutation ceiling plus one accepted")
	}
	if err := validateWireFrameSize(MaxWireFrameBytes); err != nil {
		t.Fatalf("exact wire ceiling rejected: %v", err)
	}
	if err := validateWireFrameSize(MaxWireFrameBytes + 1); err == nil {
		t.Fatal("wire ceiling plus one accepted")
	}
}

func TestBatchRequiresRoleSpecificConditions(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProductBatch)
	}{
		{"operation", func(batch *ProductBatch) { batch.Operation.Condition.Kind = ConditionAny }},
		{"event", func(batch *ProductBatch) {
			batch.Events[0].Condition.Kind = ConditionRevision
			batch.Events[0].Condition.ExpectedRevision = 1
		}},
		{"head", func(batch *ProductBatch) { batch.AggregateHeads[0].Condition.Kind = ConditionAny }},
		{"intent", func(batch *ProductBatch) { batch.EffectIntents = []Mutation{put("intent", nil, ConditionAny, 0)} }},
		{"checkpoint", func(batch *ProductBatch) {
			record := put("checkpoint", nil, ConditionAny, 0)
			head := put("checkpoint-head", nil, ConditionAbsent, 0)
			batch.Checkpoint, batch.CheckpointHead = &record, &head
		}},
		{"checkpoint-head", func(batch *ProductBatch) {
			record := put("checkpoint", nil, ConditionAbsent, 0)
			head := put("checkpoint-head", nil, ConditionAny, 0)
			batch.Checkpoint, batch.CheckpointHead = &record, &head
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			batch := minimalBatch()
			test.mutate(&batch)
			if _, _, err := encodeProductBatch(batch); err == nil {
				t.Fatal("unconditional or role-incompatible batch mutation accepted")
			}
		})
	}
}

func TestBatchValueAndKeyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name  string
		size  int
		apply func(*ProductBatch, []byte)
		valid bool
	}{
		{"ordinary-exact", MaxOrdinaryValueBytes, func(batch *ProductBatch, value []byte) { batch.Operation.Value = value }, true},
		{"ordinary-plus-one", MaxOrdinaryValueBytes + 1, func(batch *ProductBatch, value []byte) { batch.Operation.Value = value }, false},
		{"checkpoint-exact", MaxCheckpointValueBytes, func(batch *ProductBatch, value []byte) {
			mutation := put("checkpoint", value, ConditionAbsent, 0)
			head := put("checkpoint-head", nil, ConditionAbsent, 0)
			batch.Checkpoint, batch.CheckpointHead = &mutation, &head
		}, true},
		{"checkpoint-plus-one", MaxCheckpointValueBytes + 1, func(batch *ProductBatch, value []byte) {
			mutation := put("checkpoint", value, ConditionAbsent, 0)
			head := put("checkpoint-head", nil, ConditionAbsent, 0)
			batch.Checkpoint, batch.CheckpointHead = &mutation, &head
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			batch := minimalBatch()
			test.apply(&batch, make([]byte, test.size))
			_, _, err := encodeProductBatch(batch)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
	batch := minimalBatch()
	batch.Operation.Key = string(make([]byte, MaxKeyBytes))
	if _, _, err := encodeProductBatch(batch); err != nil {
		t.Fatalf("exact key boundary rejected: %v", err)
	}
	batch.Operation.Key += "x"
	if _, _, err := encodeProductBatch(batch); err == nil {
		t.Fatal("key boundary plus one accepted")
	}
}

func TestBatchRawAndWireBudgets(t *testing.T) {
	batch := minimalBatch()
	batch.Events = mutations("event", 22)
	all := batchMutations(&batch)
	remaining := MaxRawBatchBytes
	for index := range all {
		remaining -= len(all[index].Key)
	}
	for index := range all {
		size := min(remaining, MaxOrdinaryValueBytes)
		all[index].Value = make([]byte, size)
		remaining -= size
	}
	if remaining != 0 {
		t.Fatalf("test could not form exact raw boundary: %d", remaining)
	}
	_, raw, err := encodeProductBatch(batch)
	if err != nil || raw != MaxRawBatchBytes {
		t.Fatalf("raw=%d err=%v", raw, err)
	}
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(_ context.Context, encoded gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{Revision: 1, Ack: gapdbapi.AckDurable, DurableThroughRevision: 1, MutationCount: len(encoded.Mutations)}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.AtomicBatch(context.Background(), batch); err != nil {
		t.Fatalf("exact raw/wire budget rejected: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("exact boundary provider calls=%d", calls.Load())
	}
	all = batchMutations(&batch)
	all[len(all)-1].Value = append(all[len(all)-1].Value, 0)
	if _, err := adapter.AtomicBatch(context.Background(), batch); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("raw plus one error=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("raw plus one reached provider: calls=%d", calls.Load())
	}
	if MaxWireFrameBytes >= gapdbapi.DefaultMaxFrameBytes || MaxRawBatchBytes >= gapdbapi.DefaultMaxBatchBytes {
		t.Fatal("product wire/raw budgets no longer remain below provider defaults")
	}
}

func TestMutationAcknowledgementsMustMatchRequestedContract(t *testing.T) {
	routes := []struct {
		name     string
		mutation Mutation
	}{
		{"put", put("key", nil, ConditionAny, 0)},
		{"put-if-absent", put("key", nil, ConditionAbsent, 0)},
		{"compare-and-swap", put("key", nil, ConditionRevision, 3)},
		{"delete", Mutation{Kind: MutationDelete, Key: "key", Condition: Condition{Kind: ConditionRevision, ExpectedRevision: 3}}},
	}
	for _, route := range routes {
		for _, count := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s-count-%d", route.name, count), func(t *testing.T) {
				var calls atomic.Int32
				provider := mutationResultProvider(&calls, gapdbapi.MutationResult{Revision: 4, Ack: gapdbapi.AckDurable, DurableThroughRevision: 4, MutationCount: count})
				adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
				_, err := adapter.ConditionalMutate(context.Background(), "same-operation", route.mutation, AckDurable)
				if !IsKind(err, OutcomeMalformedResponse) || calls.Load() != 1 {
					t.Fatalf("err=%v calls=%d", err, calls.Load())
				}
				var typed *Error
				if !errors.As(err, &typed) || typed.OperationID != "same-operation" || typed.Recovery != RecoveryReconcile {
					t.Fatalf("diagnostic=%+v", typed)
				}
			})
		}
	}

	for _, test := range []struct {
		name      string
		requested AckMode
		result    gapdbapi.MutationResult
	}{
		{"durable-request-memory-result", AckDurable, gapdbapi.MutationResult{Revision: 4, Ack: gapdbapi.AckMemory, DurableThroughRevision: 3, MutationCount: 1}},
		{"memory-request-durable-result", AckMemory, gapdbapi.MutationResult{Revision: 4, Ack: gapdbapi.AckDurable, DurableThroughRevision: 4, MutationCount: 1}},
		{"durable-through-ahead", AckMemory, gapdbapi.MutationResult{Revision: 4, Ack: gapdbapi.AckMemory, DurableThroughRevision: 5, MutationCount: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			adapter, _ := newAdapter(mutationResultProvider(&calls, test.result), "/explicit/socket", time.Second)
			_, err := adapter.ConditionalMutate(context.Background(), "same-operation", put("key", nil, ConditionAny, 0), test.requested)
			if !IsKind(err, OutcomeMalformedResponse) || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestAtomicBatchRejectsNonDurableAcknowledgement(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{Revision: 9, Ack: gapdbapi.AckMemory, DurableThroughRevision: 8, MutationCount: 3}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	_, err := adapter.AtomicBatch(context.Background(), minimalBatch())
	if !IsKind(err, OutcomeMalformedResponse) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.OperationID != "operation" || typed.Recovery != RecoveryReconcile {
		t.Fatalf("diagnostic=%+v", typed)
	}
}

func TestCASCompetingUpdatesHaveExactlyOneWinner(t *testing.T) {
	var mu sync.Mutex
	revision := gapdbapi.Revision(7)
	provider := &fakeProvider{casFn: func(_ context.Context, _ string, expected gapdbapi.Revision, _ []byte, _ *time.Time, _ gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if expected != revision {
			return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeRevisionMismatch}
		}
		revision++
		return gapdbapi.MutationResult{Revision: revision, Ack: gapdbapi.AckDurable, DurableThroughRevision: revision, MutationCount: 1}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			<-start
			_, err := adapter.ConditionalMutate(context.Background(), fmt.Sprintf("cas-%d", index), put("head", []byte{byte(index)}, ConditionRevision, 7), AckDurable)
			results <- err
		}(index)
	}
	close(start)
	winners, conflicts := 0, 0
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			winners++
		case IsKind(err, OutcomeConflict):
			conflicts++
		default:
			t.Fatalf("unexpected CAS result: %v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
}

func TestScanPaginationIsOrderedWithoutSkipOrDuplicate(t *testing.T) {
	provider := &fakeProvider{scanFn: func(_ context.Context, prefix string, limit int, cursor string) (gapdbapi.ScanPage, error) {
		if prefix != "journal/" || limit != 2 {
			t.Fatalf("request prefix=%q limit=%d", prefix, limit)
		}
		switch cursor {
		case "":
			return scanPage(9, true, "next", "journal/a", "journal/b"), nil
		case "next":
			return scanPage(9, false, "", "journal/c"), nil
		default:
			return gapdbapi.ScanPage{}, errors.New("unexpected cursor")
		}
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	var keys []string
	cursor := ""
	for {
		page, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "journal/", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range page.Records {
			keys = append(keys, record.Key)
		}
		if !page.Truncated {
			break
		}
		cursor = page.Cursor
	}
	if fmt.Sprint(keys) != "[journal/a journal/b journal/c]" {
		t.Fatalf("keys=%v", keys)
	}
}

func TestScanByteBoundaryAndDishonestOrdering(t *testing.T) {
	provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		return gapdbapi.ScanPage{ObservedRevision: 1, AsOf: time.Now().UTC(), Records: []gapdbapi.Record{
			gapdbapi.NewRecord("prefix/b", make([]byte, 10), 1, nil),
			gapdbapi.NewRecord("prefix/a", make([]byte, 10), 1, nil),
		}}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: 2, MaxBytes: MaxScanBytes}); !IsKind(err, OutcomeMalformedResponse) {
		t.Fatalf("dishonest ordering error=%v", err)
	}
	provider.scanFn = func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		return gapdbapi.ScanPage{ObservedRevision: 1, AsOf: time.Now().UTC(), Records: []gapdbapi.Record{gapdbapi.NewRecord("prefix/a", make([]byte, 11), 1, nil)}}, nil
	}
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: 1, MaxBytes: len("prefix/a") + 10 + 32 - 1}); !IsKind(err, OutcomeMalformedResponse) {
		t.Fatalf("byte plus one error=%v", err)
	}
}

func TestScanAccountingIncludesRecordAndExpiryOverhead(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC()
	record := gapdbapi.NewRecord("prefix/a", []byte("value"), 1, &expires)
	cost := len(record.Key) + len(record.Value) + 32 + 8
	provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		return gapdbapi.ScanPage{ObservedRevision: 1, AsOf: time.Now().UTC(), Records: []gapdbapi.Record{record}}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: 1, MaxBytes: cost}); err != nil {
		t.Fatalf("exact accounted byte boundary rejected: %v", err)
	}
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: 1, MaxBytes: cost - 1}); !IsKind(err, OutcomeMalformedResponse) {
		t.Fatalf("accounted byte boundary plus one error=%v", err)
	}
}

func TestScanRecordAndRequestByteCeilingsRefuseBeforeProvider(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		calls.Add(1)
		return gapdbapi.ScanPage{ObservedRevision: 1, AsOf: time.Now().UTC()}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: MaxScanRecords, MaxBytes: MaxScanBytes}); err != nil || calls.Load() != 1 {
		t.Fatalf("exact ceilings err=%v calls=%d", err, calls.Load())
	}
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: MaxScanRecords + 1, MaxBytes: MaxScanBytes}); !IsKind(err, OutcomeInvalid) || calls.Load() != 1 {
		t.Fatalf("record plus one err=%v calls=%d", err, calls.Load())
	}
	if _, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: 1, MaxBytes: MaxScanBytes + 1}); !IsKind(err, OutcomeInvalid) || calls.Load() != 1 {
		t.Fatalf("byte plus one err=%v calls=%d", err, calls.Load())
	}
}

func TestScanFiveHundredRecordPageUsesExactProviderAccounting(t *testing.T) {
	records := make([]gapdbapi.Record, MaxScanRecords)
	baseBytes := 0
	expires := time.Now().Add(time.Hour).UTC()
	for index := range records {
		key := fmt.Sprintf("prefix/%03d", index)
		records[index] = gapdbapi.NewRecord(key, nil, 1, &expires)
		baseBytes += len(key) + 32 + 8
	}
	remaining := MaxScanBytes - baseBytes
	for index := range records {
		size := remaining / (len(records) - index)
		records[index] = gapdbapi.NewRecord(records[index].Key, make([]byte, size), 1, &expires)
		remaining -= size
	}
	if remaining != 0 {
		t.Fatalf("failed to form exact scan page: remaining=%d", remaining)
	}
	var calls []int
	provider := &fakeProvider{scanFn: func(_ context.Context, _ string, limit int, _ string) (gapdbapi.ScanPage, error) {
		calls = append(calls, limit)
		count := min(limit, len(records))
		truncated := count < len(records)
		cursor := ""
		if truncated {
			cursor = "after-page"
		}
		return gapdbapi.ScanPage{ObservedRevision: 1, AsOf: time.Now().UTC(), Records: records[:count], Truncated: truncated, Cursor: cursor}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	page, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: MaxScanRecords, MaxBytes: MaxScanBytes})
	if err != nil || len(page.Records) != MaxScanRecords || fmt.Sprint(calls) != "[500]" {
		t.Fatalf("exact page records=%d calls=%v err=%v", len(page.Records), calls, err)
	}

	records[len(records)-1].Value = append(records[len(records)-1].Value, 0)
	calls = nil
	page, err = adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: MaxScanRecords, MaxBytes: MaxScanBytes})
	if err != nil || len(page.Records) != MaxScanRecords/2 || page.Cursor == "" || fmt.Sprint(calls) != "[500 250]" {
		t.Fatalf("plus-one repage records=%d cursor=%q calls=%v err=%v", len(page.Records), page.Cursor, calls, err)
	}
}

func TestScanPagesByBytesWithoutLocalTruncation(t *testing.T) {
	var limits []int
	provider := &fakeProvider{scanFn: func(_ context.Context, _ string, limit int, _ string) (gapdbapi.ScanPage, error) {
		limits = append(limits, limit)
		if limit > 1 {
			return scanPage(2, true, "after-b", "prefix/a", "prefix/b"), nil
		}
		return scanPage(2, true, "after-a", "prefix/a"), nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	page, err := adapter.Scan(context.Background(), ScanRequest{Prefix: "prefix/", Limit: 2, MaxBytes: len("prefix/a")*2 + len("prefix/a") + 32})
	if err != nil || len(page.Records) != 1 || page.Cursor != "after-a" {
		t.Fatalf("byte-paged result=%+v err=%v", page, err)
	}
	if fmt.Sprint(limits) != "[2 1]" {
		t.Fatalf("record limit retries=%v", limits)
	}
}

type fakeWatch struct {
	registration gapdbapi.Revision
	nextFn       func(context.Context) (gapdbapi.ChangeEvent, error)
	closed       atomic.Bool
}

func (w *fakeWatch) Registration() gapdbapi.Revision { return w.registration }
func (w *fakeWatch) Next(ctx context.Context) (gapdbapi.ChangeEvent, error) {
	return w.nextFn(ctx)
}
func (w *fakeWatch) Close() error { w.closed.Store(true); return nil }

func TestWatchLossCannotHideCommittedOrdinaryRead(t *testing.T) {
	provider := &fakeProvider{
		watchFn: func(context.Context, string, gapdbapi.Revision) (providerWatch, error) {
			return &fakeWatch{registration: 3, nextFn: func(context.Context) (gapdbapi.ChangeEvent, error) {
				return gapdbapi.ChangeEvent{}, &gapdbapi.TransportError{Operation: "watch", Cause: errors.New("lost")}
			}}, nil
		},
		getFn: func(context.Context, string) (gapdbapi.Record, error) {
			return gapdbapi.NewRecord("journal/committed", []byte("durable"), 4, nil), nil
		},
	}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	watch, err := adapter.Watch(context.Background(), "journal/", 3)
	if err != nil || watch.RegistrationRevision() != 3 {
		t.Fatalf("watch=%v err=%v", watch, err)
	}
	if _, err := watch.Next(context.Background()); !IsKind(err, OutcomeUnavailable) {
		t.Fatalf("lost watch error=%v", err)
	}
	record, err := adapter.Get(context.Background(), "journal/committed")
	if err != nil || string(record.Value) != "durable" || record.Revision != 4 {
		t.Fatalf("ordinary reconciliation record=%+v err=%v", record, err)
	}
}

func TestAuthorityNamespacesAndStaleConditions(t *testing.T) {
	provider := &fakeProvider{casFn: func(context.Context, string, gapdbapi.Revision, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
		return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeRevisionMismatch}
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	lease := AuthorityMutation{OperationID: "lease-op", Namespace: AuthorityLease, Codec: AuthorityCodec(AuthorityLease), Mutation: put("spk/v1/p/m/heads/lease/work", []byte("lease"), ConditionRevision, 7)}
	if _, err := adapter.LeaseAuthority(context.Background(), lease, AckDurable); !IsKind(err, OutcomeConflict) {
		t.Fatalf("stale authority error=%v", err)
	}
	lease.Namespace = AuthorityReview
	if _, err := adapter.LeaseAuthority(context.Background(), lease, AckDurable); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("namespace crossover error=%v", err)
	}
	lease.Namespace, lease.Codec = AuthorityLease, AuthorityCodec(AuthorityLease)
	lease.Mutation.Key = "spk/v1/p/m/heads/review/work"
	if _, err := adapter.LeaseAuthority(context.Background(), lease, AckDurable); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("key crossover error=%v", err)
	}
}

func TestAuthorityKeysAreStructurallyNamespaced(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{absentFn: func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{Revision: 1, Ack: gapdbapi.AckDurable, DurableThroughRevision: 1, MutationCount: 1}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	namespaces := []AuthorityNamespace{AuthorityLease, AuthorityReview, AuthorityWorkflow, AuthorityIntegration}
	for _, expected := range namespaces {
		for _, actual := range namespaces {
			key := "spk/v1/project/mission/heads/" + string(actual) + "/work"
			mutation := AuthorityMutation{OperationID: "authority-" + string(expected) + "-" + string(actual), Namespace: expected, Codec: AuthorityCodec(expected), Mutation: put(key, nil, ConditionAbsent, 0)}
			_, err := invokeAuthority(adapter, context.Background(), expected, mutation)
			if actual == expected {
				if err != nil {
					t.Fatalf("expected=%s actual=%s err=%v", expected, actual, err)
				}
				continue
			}
			if !IsKind(err, OutcomeInvalid) {
				t.Fatalf("expected=%s actual=%s err=%v", expected, actual, err)
			}
		}
		before := calls.Load()
		for _, key := range []string{
			"nested/spk/v1/project/mission/heads/" + string(expected) + "/work",
			"spk/v1/project/mission/heads/" + string(expected) + "/work/nested",
			"spk/v1/project/mission/heads/review/work/heads/" + string(expected) + "/nested",
			"spk/v1//mission/heads/" + string(expected) + "/work",
		} {
			mutation := AuthorityMutation{OperationID: "invalid-key", Namespace: expected, Codec: AuthorityCodec(expected), Mutation: put(key, nil, ConditionAbsent, 0)}
			if _, err := invokeAuthority(adapter, context.Background(), expected, mutation); !IsKind(err, OutcomeInvalid) {
				t.Fatalf("expected=%s key=%q err=%v", expected, key, err)
			}
		}
		if calls.Load() != before {
			t.Fatalf("invalid %s key reached provider", expected)
		}
	}
}

func TestInspectAttributesEndpointAndRefusesSkew(t *testing.T) {
	limits := gapdbapi.DefaultOptions().Limits
	provider := inspectionProvider(limits)
	adapter, _ := newAdapter(provider, "/private/full/socket.sock", time.Second)
	inspection, err := adapter.Inspect(context.Background())
	if err != nil || inspection.EndpointID == "" || inspection.EndpointID == "/private/full/socket.sock" || inspection.DatabaseID != "db-1" || !inspection.Healthy || inspection.ReportedLimits.BatchOperations != limits.MaxBatchOperations || inspection.SafeCeilings.FrameBytes != limits.MaxFrameBytes {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	provider = inspectionProvider(limits)
	provider.statusFn = func(context.Context) (gapdbapi.StatusResult, error) {
		status := healthyStatus(limits)
		status.Limits.MaxBatchOperations = MaxMutationsPerBatch - 1
		return status, nil
	}
	provider.configFn = func(context.Context) (gapdbapi.ConfigResult, error) {
		changed := limits
		changed.MaxBatchOperations = MaxMutationsPerBatch - 1
		return gapdbapi.ConfigResult{Effective: gapdbapi.ConfigValues{Limits: changed}, Source: "flag", SafeCeilings: gapdbapi.DefaultOptions().Limits}, nil
	}
	adapter, _ = newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.Inspect(context.Background()); !IsKind(err, OutcomeUnsupportedVersion) {
		t.Fatalf("capability skew error=%v", err)
	}
}

func FuzzBatchRejectsUnboundedCounts(f *testing.F) {
	f.Add(uint8(1), uint8(1), uint8(0))
	f.Add(uint8(129), uint8(33), uint8(17))
	f.Fuzz(func(t *testing.T, events, heads, intents uint8) {
		batch := minimalBatch()
		batch.Events = mutations("event", int(events))
		batch.AggregateHeads = mutations("head", int(heads))
		batch.EffectIntents = mutations("intent", int(intents))
		_, _, err := encodeProductBatch(batch)
		valid := events >= 1 && events <= MaxEventsPerCommand && heads >= 1 && heads <= MaxHeadsPerCommand && intents <= MaxIntentsPerCommand
		if (err == nil) != valid {
			t.Fatalf("events=%d heads=%d intents=%d err=%v", events, heads, intents, err)
		}
	})
}

func mutationResultProvider(calls *atomic.Int32, result gapdbapi.MutationResult) *fakeProvider {
	respond := func() (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return result, nil
	}
	return &fakeProvider{
		putFn: func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
		absentFn: func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
		casFn: func(context.Context, string, gapdbapi.Revision, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
		deleteFn: func(context.Context, string, gapdbapi.Revision, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
	}
}

func invokeAuthority(adapter *Adapter, ctx context.Context, namespace AuthorityNamespace, mutation AuthorityMutation) (MutationResult, error) {
	switch namespace {
	case AuthorityLease:
		return adapter.LeaseAuthority(ctx, mutation, AckDurable)
	case AuthorityReview:
		return adapter.ReviewAuthority(ctx, mutation, AckDurable)
	case AuthorityWorkflow:
		return adapter.WorkflowAuthority(ctx, mutation, AckDurable)
	case AuthorityIntegration:
		return adapter.IntegrationAuthority(ctx, mutation, AckDurable)
	default:
		return MutationResult{}, errors.New("unknown authority namespace")
	}
}

func minimalBatch() ProductBatch {
	return ProductBatch{
		OperationID: "operation", Operation: put("operation", nil, ConditionAbsent, 0),
		Events: mutations("event", 1), AggregateHeads: mutations("head", 1),
	}
}

func mutations(prefix string, count int) []Mutation {
	result := make([]Mutation, count)
	for index := range result {
		result[index] = put(fmt.Sprintf("%s/%03d", prefix, index), nil, ConditionAbsent, 0)
	}
	return result
}

func batchMutations(batch *ProductBatch) []*Mutation {
	result := []*Mutation{&batch.Operation}
	for index := range batch.Events {
		result = append(result, &batch.Events[index])
	}
	for index := range batch.AggregateHeads {
		result = append(result, &batch.AggregateHeads[index])
	}
	if batch.Checkpoint != nil {
		result = append(result, batch.Checkpoint, batch.CheckpointHead)
	}
	for index := range batch.EffectIntents {
		result = append(result, &batch.EffectIntents[index])
	}
	return result
}

func scanPage(revision gapdbapi.Revision, truncated bool, cursor string, keys ...string) gapdbapi.ScanPage {
	page := gapdbapi.ScanPage{ObservedRevision: revision, AsOf: time.Now().UTC(), Truncated: truncated, Cursor: cursor}
	for _, key := range keys {
		page.Records = append(page.Records, gapdbapi.NewRecord(key, []byte(key), revision, nil))
	}
	return page
}

func healthyStatus(limits gapdbapi.Limits) gapdbapi.StatusResult {
	return gapdbapi.StatusResult{
		Lifecycle: gapdbapi.LifecycleReady, DatabaseID: "db-1", CurrentRevision: 8, DurableThroughRevision: 8,
		Limits: limits, OwnerPID: 123, OwnerStartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func inspectionProvider(limits gapdbapi.Limits) *fakeProvider {
	return &fakeProvider{
		statusFn: func(context.Context) (gapdbapi.StatusResult, error) { return healthyStatus(limits), nil },
		healthFn: func(context.Context) (gapdbapi.HealthResult, error) {
			return gapdbapi.HealthResult{Lifecycle: gapdbapi.LifecycleReady, Healthy: true, FailingSubsystems: []string{}, SafeActions: []gapdbapi.SafeAction{}}, nil
		},
		configFn: func(context.Context) (gapdbapi.ConfigResult, error) {
			return gapdbapi.ConfigResult{Effective: gapdbapi.ConfigValues{Limits: limits}, Source: "default", SafeCeilings: limits}, nil
		},
	}
}
