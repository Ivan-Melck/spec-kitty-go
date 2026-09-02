package gapdb

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestV2KeyEveryFamilyRoundTripsCanonically(t *testing.T) {
	t.Parallel()
	host := journalv2.Scope{Kind: journalv2.ScopeHost}
	project := journalv2.Scope{Kind: journalv2.ScopeProject, ProjectID: "project-1"}
	mission := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	repository := journalv2.Scope{Kind: journalv2.ScopeRepository, ProjectID: "project-1", RepositoryID: "repository-1"}
	workRevision := journalv2.Scope{Kind: journalv2.ScopeWorkRevision, ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1"}
	work := journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1"}

	keys := []V2Key{V2AuthorityBindingKey(), V2RuntimeCurrentKey()}
	appendKey := func(key V2Key, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	appendKey(V2RuntimeEpochKey("epoch-1"))
	appendKey(V2OperationBindingKey("operation-1"))
	appendKey(V2OperationHeadKey("operation-1"))
	appendKey(V2EffectIntentKey("effect-1"))
	appendKey(V2CheckpointSlotKey("slot-1"))
	appendKey(V2CheckpointRecordKey("checkpoint-1"))
	appendKey(V2EligibilityGuardKey("mission-1", "shard-1"))
	for index, scope := range []journalv2.Scope{host, project, mission, repository, workRevision, work} {
		coordinate, err := journalv2.NewAggregateCoordinate("aggregate", "aggregate-1", scope)
		if err != nil {
			t.Fatal(err)
		}
		appendKey(V2AggregateHeadKey(coordinate))
		appendKey(V2JournalEventKey(coordinate, journalv2.LogicalVersion(index+1)))
	}

	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key.String() == "" || seen[key.String()] {
			t.Fatalf("empty or colliding key %q", key.String())
		}
		seen[key.String()] = true
		decoded, err := ParseV2Key(key.String())
		if err != nil || decoded.String() != key.String() || decoded.Kind() != key.Kind() {
			t.Fatalf("key=%q decoded=%+v err=%v", key.String(), decoded, err)
		}
	}
}

func TestV2JournalKeyLexicalVersionOrderMatchesNumericOrder(t *testing.T) {
	t.Parallel()
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", journalv2.Scope{Kind: journalv2.ScopeHost})
	var keys []string
	for _, version := range []journalv2.LogicalVersion{1, 2, 15, 16, 255, 256, ^journalv2.LogicalVersion(0)} {
		key, err := V2JournalEventKey(coordinate, version)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.String())
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("lexical version order differs: %v", keys)
	}
}

func TestV2KeyOperationIdentityIsHostGlobal(t *testing.T) {
	t.Parallel()
	left, err := V2OperationBindingKey("operation-1")
	if err != nil {
		t.Fatal(err)
	}
	right, err := V2OperationBindingKey("operation-1")
	if err != nil || left.String() != right.String() || strings.Contains(left.String(), "mission") || strings.Contains(left.String(), "project") {
		t.Fatalf("left=%q right=%q err=%v", left.String(), right.String(), err)
	}
	if left.String() != "spk/v2/operations/k6f7065726174696f6e2d31/binding" {
		t.Fatalf("unexpected canonical global key %q", left.String())
	}
}

func TestV2KeyRejectsAliasesAndMalformedVersions(t *testing.T) {
	t.Parallel()
	valid, err := V2OperationBindingKey("operation-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutant := range []string{
		strings.Replace(valid.String(), "6f", "6F", 1),
		strings.Replace(valid.String(), "k6f", "6f", 1),
		strings.Replace(valid.String(), "k6f", "k%6f", 1),
		"spk/v2/operations/./binding",
		"spk/v2/operations/../binding",
		valid.String() + "/",
		"spk/v2/unknown/k61",
		"spk//v2/operations/k61/binding",
	} {
		if _, err := ParseV2Key(mutant); !IsKind(err, OutcomeInvalid) {
			t.Fatalf("alias accepted %q err=%v", mutant, err)
		}
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", journalv2.Scope{Kind: journalv2.ScopeHost})
	event, err := V2JournalEventKey(coordinate, 1)
	if err != nil {
		t.Fatal(err)
	}
	version := event.String()[len(event.String())-16:]
	for _, mutant := range []string{"0000000000000000", "000000000000001", "00000000000000001", "000000000000000A", "+000000000000001", "fffffffffffffffff", "gggggggggggggggg"} {
		encoded := strings.TrimSuffix(event.String(), version) + mutant
		if _, err := ParseV2Key(encoded); !IsKind(err, OutcomeInvalid) {
			t.Fatalf("version mutant accepted %q err=%v", mutant, err)
		}
	}
}

func TestV2KeyExactBudgetAndPlusOne(t *testing.T) {
	t.Parallel()
	component := func(size int) string { return "a" + strings.Repeat("x", size-1) }
	scope := journalv2.Scope{Kind: journalv2.ScopeWorkRevision, ProjectID: journalv2.ProjectID(component(128)), MissionID: journalv2.MissionID(component(128)), WorkRevisionID: journalv2.WorkRevisionID(component(128))}
	coordinate, err := journalv2.NewAggregateCoordinate(journalv2.AggregateKind(component(1)), journalv2.AggregateID(component(96)), scope)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := V2JournalEventKey(coordinate, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.String()) != MaxKeyBytes {
		t.Fatal("could not construct exact canonical key boundary")
	}
	if decoded, err := ParseV2Key(exact.String()); err != nil || decoded.String() != exact.String() {
		t.Fatalf("exact key rejected: %v", err)
	}
	if _, err := ParseV2Key(exact.String() + "x"); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("key maximum-plus-one accepted: %v", err)
	}
}

func TestV2CanonicalPrefixesRoundTrip(t *testing.T) {
	t.Parallel()
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", scope)
	prefixes := []V2Prefix{V2RuntimeEpochsPrefix(), V2OperationsPrefix(), V2EffectsPrefix(), V2CheckpointSlotsPrefix(), V2CheckpointRecordsPrefix()}
	appendPrefix := func(prefix V2Prefix, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		prefixes = append(prefixes, prefix)
	}
	appendPrefix(V2AggregateHeadsPrefix(scope))
	appendPrefix(V2JournalPrefix(scope))
	appendPrefix(V2AggregateJournalPrefix(coordinate))
	appendPrefix(V2EligibilityPrefix("mission-1"))
	for _, prefix := range prefixes {
		parsed, err := ParseV2Prefix(prefix.String())
		if err != nil || parsed.String() != prefix.String() || parsed.Kind() != prefix.Kind() {
			t.Fatalf("prefix=%q parsed=%+v err=%v", prefix.String(), parsed, err)
		}
	}
	for _, malformed := range []string{"", "spk/v2/scopes/host/heads", "spk/v2/scopes/host//heads/", "spk/v2/operations//", strings.Repeat("x", MaxKeyBytes+1)} {
		if _, err := ParseV2Prefix(malformed); !IsKind(err, OutcomeInvalid) {
			t.Fatalf("malformed prefix accepted %q: %v", malformed, err)
		}
	}
}

func TestV2ReaderPreservesMetadataAndCopiesBuffers(t *testing.T) {
	key, _ := V2OperationHeadKey("operation-1")
	expires := time.Date(2030, 1, 2, 3, 4, 5, 6, time.FixedZone("offset", 3600))
	providerValue := []byte(`{"status":"accepted"}`)
	provider := &fakeProvider{getFn: func(_ context.Context, got string) (gapdbapi.Record, error) {
		if got != key.String() {
			t.Fatalf("got key %q", got)
		}
		return gapdbapi.Record{Key: got, Value: providerValue, Revision: 17, ExpiresAt: &expires}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	record, err := adapter.ReadV2(t.Context(), key)
	if err != nil || record.Key().String() != key.String() || record.Revision() != 17 || !record.ExpiresAt().Equal(expires) || record.ExpiresAt().Location() != time.UTC {
		t.Fatalf("record=%+v expiry=%v err=%v", record, record.ExpiresAt(), err)
	}
	providerValue[0] = 'x'
	value := record.CanonicalValue()
	value[0] = 'y'
	if string(record.CanonicalValue()) != `{"status":"accepted"}` {
		t.Fatal("provider or caller buffer mutation escaped reader copies")
	}
}

func TestV2ReaderRejectsDishonestExactResponses(t *testing.T) {
	key, _ := V2OperationHeadKey("operation-1")
	foreign, _ := V2OperationHeadKey("operation-2")
	zeroTime := time.Time{}
	mutants := map[string]gapdbapi.Record{
		"foreign":     gapdbapi.NewRecord(foreign.String(), []byte(`{}`), 1, nil),
		"zero-rev":    gapdbapi.NewRecord(key.String(), []byte(`{}`), 0, nil),
		"oversized":   gapdbapi.NewRecord(key.String(), make([]byte, MaxOrdinaryValueBytes+1), 1, nil),
		"zero-expiry": gapdbapi.NewRecord(key.String(), []byte(`{}`), 1, &zeroTime),
	}
	for name, response := range mutants {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) { return response, nil }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.ReadV2(t.Context(), key); !IsKind(err, OutcomeMalformedResponse) {
				t.Fatalf("dishonest response accepted: %v", err)
			}
		})
	}
}

func TestV2ReaderScanExactBudgetsAndSafeRepage(t *testing.T) {
	records := make([]gapdbapi.Record, MaxScanRecords)
	base := 0
	for index := range records {
		key, err := V2CheckpointRecordKey(journalv2.CheckpointID(fmt.Sprintf("checkpoint-%03d", index)))
		if err != nil {
			t.Fatal(err)
		}
		records[index] = gapdbapi.NewRecord(key.String(), nil, 7, nil)
		base += len(key.String()) + 32
	}
	remaining := MaxScanBytes - base
	for index := range records {
		size := remaining / (len(records) - index)
		records[index] = gapdbapi.NewRecord(records[index].Key, make([]byte, size), 7, nil)
		remaining -= size
	}
	if remaining != 0 {
		t.Fatalf("scan fixture remaining=%d", remaining)
	}
	prefix := V2CheckpointRecordsPrefix()
	var calls []int
	provider := &fakeProvider{scanFn: func(_ context.Context, gotPrefix string, limit int, cursor string) (gapdbapi.ScanPage, error) {
		calls = append(calls, limit)
		if gotPrefix != prefix.String() || cursor != "" {
			t.Fatalf("prefix=%q cursor=%q", gotPrefix, cursor)
		}
		count := min(limit, len(records))
		truncated := count < len(records)
		next := ""
		if truncated {
			next = "same-snapshot-next"
		}
		return gapdbapi.ScanPage{ObservedRevision: 7, AsOf: time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC), Records: records[:count], Cursor: next, Truncated: truncated}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	page, err := adapter.ScanV2(t.Context(), V2ScanRequest{Prefix: prefix, Limit: MaxScanRecords, MaxBytes: MaxScanBytes})
	if err != nil || len(page.Records()) != MaxScanRecords || page.ObservedRevision() != 7 || fmt.Sprint(calls) != "[500]" {
		t.Fatalf("page records=%d revision=%d calls=%v err=%v", len(page.Records()), page.ObservedRevision(), calls, err)
	}
	records[len(records)-1].Value = append(records[len(records)-1].Value, 0)
	calls = nil
	page, err = adapter.ScanV2(t.Context(), V2ScanRequest{Prefix: prefix, Limit: MaxScanRecords, MaxBytes: MaxScanBytes})
	if err != nil || len(page.Records()) != MaxScanRecords/2 || !page.Truncated() || fmt.Sprint(calls) != "[500 250]" {
		t.Fatalf("repage records=%d calls=%v err=%v", len(page.Records()), calls, err)
	}
}

func TestV2ReaderScanRejectsMetadataMutantsAndUnstableRepage(t *testing.T) {
	prefix := V2CheckpointRecordsPrefix()
	keyA, _ := V2CheckpointRecordKey("checkpoint-a")
	keyB, _ := V2CheckpointRecordKey("checkpoint-b")
	now := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
	mutants := map[string]gapdbapi.ScanPage{
		"zero-observed": {AsOf: now},
		"zero-asof":     {ObservedRevision: 2},
		"cursor-lie":    {ObservedRevision: 2, AsOf: now, Cursor: "cursor"},
		"unsorted":      {ObservedRevision: 2, AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord(keyB.String(), nil, 1, nil), gapdbapi.NewRecord(keyA.String(), nil, 1, nil)}},
		"future-record": {ObservedRevision: 2, AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord(keyA.String(), nil, 3, nil)}},
		"foreign":       {ObservedRevision: 2, AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord("spk/v2/runtime/current", nil, 1, nil)}},
	}
	for name, response := range mutants {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) { return response, nil }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.ScanV2(t.Context(), V2ScanRequest{Prefix: prefix, Limit: 2}); !IsKind(err, OutcomeMalformedResponse) {
				t.Fatalf("metadata mutant accepted: %v", err)
			}
		})
	}

	var calls atomic.Int32
	provider := &fakeProvider{scanFn: func(_ context.Context, _ string, limit int, _ string) (gapdbapi.ScanPage, error) {
		call := calls.Add(1)
		value := make([]byte, 100)
		page := gapdbapi.ScanPage{ObservedRevision: gapdbapi.Revision(call), AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord(keyA.String(), value, 1, nil), gapdbapi.NewRecord(keyB.String(), value, 1, nil)}, Cursor: "next", Truncated: true}
		if limit == 1 {
			page.Records = page.Records[:1]
		}
		return page, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.ScanV2(t.Context(), V2ScanRequest{Prefix: prefix, Limit: 2, MaxBytes: len(keyA.String()) + 100 + 32}); !IsKind(err, OutcomeMalformedResponse) || calls.Load() != 2 {
		t.Fatalf("unstable repage err=%v calls=%d", err, calls.Load())
	}
}

func TestV2ReaderRequestBudgetsRefuseBeforeProvider(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		calls.Add(1)
		return gapdbapi.ScanPage{}, errors.New("unexpected")
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	prefix := V2CheckpointRecordsPrefix()
	for _, request := range []V2ScanRequest{
		{Prefix: prefix, Limit: MaxScanRecords + 1},
		{Prefix: prefix, Limit: 1, MaxBytes: MaxScanBytes + 1},
		{Prefix: prefix, Limit: 1, Cursor: strings.Repeat("x", MaxCursorBytes+1)},
		{Prefix: V2Prefix{}, Limit: 1},
	} {
		if _, err := adapter.ScanV2(t.Context(), request); !IsKind(err, OutcomeInvalid) {
			t.Fatalf("invalid request accepted: %v", err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached provider %d times", calls.Load())
	}
}

func TestV2WatchIsNotificationOnly(t *testing.T) {
	prefix := V2OperationsPrefix()
	key, _ := V2OperationHeadKey("operation-1")
	fake := &fakeWatch{registration: 4, nextFn: func(context.Context) (gapdbapi.ChangeEvent, error) {
		record := gapdbapi.NewRecord(key.String(), []byte("secret-value-not-authority"), 5, nil)
		return gapdbapi.ChangeEvent{Revision: 5, Order: 1, Kind: gapdbapi.ChangePut, Key: key.String(), Record: &record}, nil
	}}
	provider := &fakeProvider{watchFn: func(_ context.Context, got string, after gapdbapi.Revision) (providerWatch, error) {
		if got != prefix.String() || after != 4 {
			t.Fatalf("prefix=%q after=%d", got, after)
		}
		return fake, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	watch, err := adapter.WatchV2(t.Context(), prefix, 4)
	if err != nil {
		t.Fatal(err)
	}
	notification, err := watch.Next(t.Context())
	if err != nil || notification.Revision != 5 || notification.Key.String() != key.String() {
		t.Fatalf("notification=%+v err=%v", notification, err)
	}
	if err := watch.Close(); err != nil || !fake.closed.Load() {
		t.Fatalf("close err=%v closed=%v", err, fake.closed.Load())
	}
}

func TestV2CommitTranslatesClosedProjectionToOneDurableBatch(t *testing.T) {
	plan := v2TestPlan(t)
	records := plan.Records()
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		if batch.Ack != gapdbapi.AckDurable || len(batch.Mutations) != len(records) {
			t.Fatalf("batch ack=%s mutations=%d records=%d", batch.Ack, len(batch.Mutations), len(records))
		}
		seen := make(map[string]bool, len(records))
		for index, record := range records {
			key, err := v2KeyForSealedRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			mutation := batch.Mutations[index]
			if mutation.Kind != gapdbapi.MutationPut || mutation.Key != key.String() || string(mutation.Value) != string(record.CanonicalValue()) || mutation.ExpiresAt != nil || seen[mutation.Key] {
				t.Fatalf("record=%s mutation=%+v", record.Role(), mutation)
			}
			seen[mutation.Key] = true
			if mutation.Condition.Kind == gapdbapi.ConditionAny {
				t.Fatalf("unconditional mutation for %s", record.Role())
			}
		}
		return gapdbapi.MutationResult{Revision: 51, Ack: gapdbapi.AckDurable, DurableThroughRevision: 52, MutationCount: len(batch.Mutations)}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	result, err := adapter.CommitV2(t.Context(), plan)
	if err != nil || calls.Load() != 1 || result.Revision() != 51 || result.DurableThrough() != 52 || result.MutationCount() != len(records) || result.PublicationID() != plan.PublicationID() {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestV2CommitPreservesIndependentRevisionConditions(t *testing.T) {
	plan := v2LaterTestPlan(t)
	want := map[V2KeyKind]uint64{V2KeyOperationHead: 41, V2KeyAggregateHead: 42, V2KeyEligibilityGuard: 43, V2KeyCheckpointSlot: 44}
	provider := &fakeProvider{batchFn: func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		for _, mutation := range batch.Mutations {
			key, err := ParseV2Key(mutation.Key)
			if err != nil {
				t.Fatal(err)
			}
			if revision, mutable := want[key.Kind()]; mutable {
				if mutation.Condition.Kind != gapdbapi.ConditionRevision || uint64(mutation.Condition.ExpectedRevision) != revision {
					t.Fatalf("kind=%s condition=%+v", key.Kind(), mutation.Condition)
				}
			} else if mutation.Condition.Kind != gapdbapi.ConditionAbsent {
				t.Fatalf("immutable kind=%s condition=%+v", key.Kind(), mutation.Condition)
			}
		}
		return gapdbapi.MutationResult{Revision: 60, Ack: gapdbapi.AckDurable, DurableThroughRevision: 60, MutationCount: len(batch.Mutations)}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.CommitV2(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
}

func TestV2CommitRefusesWireBudgetBeforeProviderAndValidatesAck(t *testing.T) {
	plan := v2TestPlan(t)
	for _, test := range []struct {
		name      string
		wireBytes int
		result    gapdbapi.MutationResult
		wantCalls int32
		wantKind  OutcomeKind
	}{
		{"wire-exact", MaxWireFrameBytes, gapdbapi.MutationResult{Revision: 1, Ack: gapdbapi.AckDurable, DurableThroughRevision: 1, MutationCount: len(plan.Records())}, 1, ""},
		{"wire-plus-one", MaxWireFrameBytes + 1, gapdbapi.MutationResult{}, 0, OutcomeInvalid},
		{"zero-revision", 1, gapdbapi.MutationResult{Ack: gapdbapi.AckDurable, MutationCount: len(plan.Records())}, 1, OutcomeAmbiguous},
		{"memory-ack", 1, gapdbapi.MutationResult{Revision: 1, Ack: gapdbapi.AckMemory, DurableThroughRevision: 1, MutationCount: len(plan.Records())}, 1, OutcomeAmbiguous},
		{"durable-behind", 1, gapdbapi.MutationResult{Revision: 2, Ack: gapdbapi.AckDurable, DurableThroughRevision: 1, MutationCount: len(plan.Records())}, 1, OutcomeAmbiguous},
		{"wrong-count", 1, gapdbapi.MutationResult{Revision: 1, Ack: gapdbapi.AckDurable, DurableThroughRevision: 1, MutationCount: len(plan.Records()) - 1}, 1, OutcomeAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
				calls.Add(1)
				return test.result, nil
			}}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			adapter.wireSize = func([]byte) int { return test.wireBytes }
			_, err := adapter.CommitV2(t.Context(), plan)
			if test.wantKind == "" && err != nil || test.wantKind != "" && !IsKind(err, test.wantKind) || calls.Load() != test.wantCalls {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestV2CommitFailureClassificationNeverRetries(t *testing.T) {
	plan := v2TestPlan(t)
	for _, test := range []struct {
		name string
		err  error
		kind OutcomeKind
	}{
		{"before-delivery", &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: false, Cause: errors.New("dial")}, OutcomeUnavailable},
		{"after-delivery", &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: errors.New("eof")}, OutcomeAmbiguous},
		{"bare-eof", io.EOF, OutcomeAmbiguous},
		{"timeout-after-delivery", context.DeadlineExceeded, OutcomeAmbiguous},
		{"cancel-after-delivery", context.Canceled, OutcomeAmbiguous},
		{"applied-audit", &gapdbapi.Error{Code: gapdbapi.CodeAuditFailedAfterApply, OperationApplied: true}, OutcomeAmbiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
				calls.Add(1)
				return gapdbapi.MutationResult{}, test.err
			}}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.CommitV2(t.Context(), plan); !IsKind(err, test.kind) || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestV2CommitCanceledBeforeDeliveryMakesNoProviderCall(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.CommitV2(ctx, v2TestPlan(t)); !IsKind(err, OutcomeCanceled) || calls.Load() != 0 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestV2CommitSlowProviderTimesOutAmbiguouslyWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(ctx context.Context, _ gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		<-ctx.Done()
		return gapdbapi.MutationResult{}, ctx.Err()
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Millisecond)
	if _, err := adapter.CommitV2(context.Background(), v2TestPlan(t)); !IsKind(err, OutcomeAmbiguous) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestV2CommitConflictPerformsSamePublicationResolution(t *testing.T) {
	plan := v2TestPlan(t)
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed-%v", committed), func(t *testing.T) {
			probe, _ := newAdapter(&fakeProvider{}, "/explicit/socket", time.Second)
			encoded, err := probe.encodeV2Plan(plan)
			if err != nil {
				t.Fatal(err)
			}
			values := make(map[string][]byte, len(encoded.keys))
			for index, key := range encoded.keys {
				values[key.String()] = encoded.values[index]
			}
			var reads, writes atomic.Int32
			provider := &fakeProvider{
				batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
					writes.Add(1)
					return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeRevisionMismatch}
				},
				getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
					reads.Add(1)
					if !committed {
						return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
					}
					return gapdbapi.NewRecord(key, values[key], 81, nil), nil
				},
			}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			result, err := adapter.CommitV2(t.Context(), plan)
			if writes.Load() != 1 || int(reads.Load()) != len(encoded.keys) {
				t.Fatalf("writes=%d reads=%d", writes.Load(), reads.Load())
			}
			if committed {
				if err != nil || result.Revision() != 81 || result.PublicationID() != plan.PublicationID() {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if !IsKind(err, OutcomeConflict) {
				t.Fatalf("definite loser error=%v", err)
			}
		})
	}
}

func TestV2AdapterContentionIndependentProgressAndSameHeadWinner(t *testing.T) {
	t.Run("independent", func(t *testing.T) {
		memory := newV2AtomicMemory()
		adapter, _ := newAdapter(memory.provider(), "/explicit/socket", time.Second)
		plans := []journalv2.SealedCommitPlanV2{v2NamedTestPlan(t, "a", "a"), v2NamedTestPlan(t, "b", "b")}
		start := make(chan struct{})
		results := make(chan error, len(plans))
		for _, plan := range plans {
			plan := plan
			go func() {
				<-start
				_, err := adapter.CommitV2(context.Background(), plan)
				results <- err
			}()
		}
		close(start)
		for range plans {
			if err := <-results; err != nil {
				t.Fatalf("independent publication failed: %v", err)
			}
		}
		if memory.batchCalls != 2 {
			t.Fatalf("batch calls=%d", memory.batchCalls)
		}
	})

	t.Run("same-head", func(t *testing.T) {
		memory := newV2AtomicMemory()
		adapter, _ := newAdapter(memory.provider(), "/explicit/socket", time.Second)
		plans := []journalv2.SealedCommitPlanV2{v2NamedTestPlan(t, "a", "shared"), v2NamedTestPlan(t, "b", "shared")}
		start := make(chan struct{})
		results := make(chan error, len(plans))
		for _, plan := range plans {
			plan := plan
			go func() {
				<-start
				_, err := adapter.CommitV2(context.Background(), plan)
				results <- err
			}()
		}
		close(start)
		wins, conflicts := 0, 0
		for range plans {
			switch err := <-results; {
			case err == nil:
				wins++
			case IsKind(err, OutcomeConflict):
				conflicts++
			default:
				t.Fatalf("unexpected contention result: %v", err)
			}
		}
		if wins != 1 || conflicts != 1 || memory.batchCalls != 2 || memory.getCalls == 0 {
			t.Fatalf("wins=%d conflicts=%d batches=%d reads=%d", wins, conflicts, memory.batchCalls, memory.getCalls)
		}
	})
}

func TestV2AdapterPublicSidecarAtomicMembership(t *testing.T) {
	configuration, err := externalTestConfiguration()
	if err != nil {
		if qualificationRequested() {
			t.Fatalf("qualification incomplete: %v", err)
		}
		t.Skipf("external precondition unmet: %v", err)
	}
	harness := startExternalHarness(t, configuration)
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Errorf("external owner did not terminate cleanly: %v", err)
		}
	})
	plan := v2NamedTestPlan(t, "external", "external")
	result, err := harness.adapter.CommitV2(t.Context(), plan)
	if err != nil || result.Revision() == 0 || result.MutationCount() != len(plan.Records()) {
		t.Fatalf("commit result=%+v err=%v", result, err)
	}
	for _, sealed := range plan.Records() {
		key, err := v2KeyForSealedRecord(sealed)
		if err != nil {
			t.Fatal(err)
		}
		record, err := harness.adapter.ReadV2(t.Context(), key)
		if err != nil || record.Revision() != result.Revision() || string(record.CanonicalValue()) != string(sealed.CanonicalValue()) {
			t.Fatalf("key=%s revision=%d err=%v", key.String(), record.Revision(), err)
		}
	}
}

type v2AtomicMemory struct {
	mu         sync.Mutex
	revision   gapdbapi.Revision
	records    map[string]gapdbapi.Record
	batchCalls int
	getCalls   int
}

func newV2AtomicMemory() *v2AtomicMemory {
	return &v2AtomicMemory{records: make(map[string]gapdbapi.Record)}
}

func (m *v2AtomicMemory) provider() *fakeProvider {
	return &fakeProvider{
		batchFn: func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.batchCalls++
			for _, mutation := range batch.Mutations {
				record, exists := m.records[mutation.Key]
				switch mutation.Condition.Kind {
				case gapdbapi.ConditionAbsent:
					if exists {
						return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeAlreadyExists}
					}
				case gapdbapi.ConditionRevision:
					if !exists || record.Revision != mutation.Condition.ExpectedRevision {
						return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeRevisionMismatch}
					}
				default:
					return gapdbapi.MutationResult{}, &gapdbapi.Error{Code: gapdbapi.CodeInvalidRequest}
				}
			}
			m.revision++
			for _, mutation := range batch.Mutations {
				m.records[mutation.Key] = gapdbapi.NewRecord(mutation.Key, mutation.Value, m.revision, nil)
			}
			return gapdbapi.MutationResult{Revision: m.revision, Ack: gapdbapi.AckDurable, DurableThroughRevision: m.revision, MutationCount: len(batch.Mutations)}, nil
		},
		getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.getCalls++
			record, exists := m.records[key]
			if !exists {
				return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
			}
			return record.Clone(), nil
		},
	}
}

func TestV2CommitPreflightMakesNoProviderCall(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	measurement, err := adapter.PreflightCommitV2(v2TestPlan(t))
	if err != nil || measurement.MutationCount != len(v2TestPlan(t).Records()) || measurement.RawBytes <= 0 || measurement.WireBytes <= measurement.RawBytes || calls.Load() != 0 {
		t.Fatalf("measurement=%+v calls=%d err=%v", measurement, calls.Load(), err)
	}
	adapter.wireSize = func([]byte) int { return MaxWireFrameBytes + 1 }
	if _, err := adapter.PreflightCommitV2(v2TestPlan(t)); !IsKind(err, OutcomeInvalid) || calls.Load() != 0 {
		t.Fatalf("invalid preflight err=%v calls=%d", err, calls.Load())
	}
}

func TestV2AdapterValueBoundariesRefuseBeforePublication(t *testing.T) {
	canonicalAt := func(size int) []byte {
		return []byte(`{"x":"` + strings.Repeat("x", size-len(`{"x":""}`)) + `"}`)
	}
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{Revision: gapdbapi.Revision(calls.Load()), Ack: gapdbapi.AckDurable, DurableThroughRevision: gapdbapi.Revision(calls.Load()), MutationCount: len(batch.Mutations)}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	ordinary, err := v2TryNamedTestPlan(t, "ordinary-exact", "ordinary-exact", canonicalAt(MaxOrdinaryValueBytes), []byte(`{"through":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CommitV2(t.Context(), ordinary); err != nil {
		t.Fatalf("exact ordinary value rejected: %v", err)
	}
	if _, err := v2TryNamedTestPlan(t, "ordinary-over", "ordinary-over", canonicalAt(MaxOrdinaryValueBytes+1), []byte(`{"through":0}`)); !journalv2.IsKind(err, journalv2.ErrorBudgetExceeded) {
		t.Fatalf("ordinary maximum-plus-one reached publication: %v", err)
	}
	checkpoint, err := v2TryNamedTestPlan(t, "checkpoint-exact", "checkpoint-exact", []byte(`{"intent":"one"}`), canonicalAt(MaxCheckpointValueBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.CommitV2(t.Context(), checkpoint); err != nil {
		t.Fatalf("exact checkpoint value rejected: %v", err)
	}
	if _, err := v2TryNamedTestPlan(t, "checkpoint-over", "checkpoint-over", []byte(`{"intent":"one"}`), canonicalAt(MaxCheckpointValueBytes+1)); !journalv2.IsKind(err, journalv2.ErrorBudgetExceeded) {
		t.Fatalf("checkpoint maximum-plus-one reached publication: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("over-budget plans reached provider: calls=%d", calls.Load())
	}
}

func TestV2AmbiguityCompleteReadbackReturnsRecordedProgress(t *testing.T) {
	plan := v2TestPlan(t)
	probe, _ := newAdapter(&fakeProvider{}, "/explicit/socket", time.Second)
	encoded, err := probe.encodeV2Plan(plan)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string][]byte, len(encoded.keys))
	for index, key := range encoded.keys {
		values[key.String()] = encoded.values[index]
	}
	var reads atomic.Int32
	var writes atomic.Int32
	provider := &fakeProvider{
		getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
			reads.Add(1)
			return gapdbapi.NewRecord(key, values[key], 77, nil), nil
		},
		batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
			writes.Add(1)
			return gapdbapi.MutationResult{}, errors.New("forbidden write")
		},
	}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	result, err := adapter.ReconcileV2(t.Context(), plan)
	if err != nil || result.Disposition() != V2ReconcileCommitted || result.PublicationID() != plan.PublicationID() || result.Revision() != 77 ||
		result.OperationState().LastPublicationID() != plan.PublicationID() || result.MutationCount() != len(encoded.keys) || int(reads.Load()) != len(encoded.keys) || writes.Load() != 0 {
		t.Fatalf("result=%+v reads=%d writes=%d err=%v", result, reads.Load(), writes.Load(), err)
	}
}

func TestV2AmbiguityAbsenceAndExactPreconditionsPermitOnlySamePublication(t *testing.T) {
	for _, plan := range []journalv2.SealedCommitPlanV2{v2TestPlan(t), v2LaterTestPlan(t)} {
		probe, _ := newAdapter(&fakeProvider{}, "/explicit/socket", time.Second)
		encoded, err := probe.encodeV2Plan(plan)
		if err != nil {
			t.Fatal(err)
		}
		conditions := make(map[string]gapdbapi.Condition, len(encoded.keys))
		for index, key := range encoded.keys {
			conditions[key.String()] = encoded.batch.Mutations[index].Condition
		}
		provider := &fakeProvider{getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
			condition := conditions[key]
			if condition.Kind == gapdbapi.ConditionAbsent {
				return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
			}
			return gapdbapi.NewRecord(key, []byte(`{"prior":true}`), condition.ExpectedRevision, nil), nil
		}}
		adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
		result, err := adapter.ReconcileV2(t.Context(), plan)
		if err != nil || result.Disposition() != V2ReconcileRetrySamePublication || result.PublicationID() != plan.PublicationID() || result.Revision() != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestV2AmbiguityPartialMixedAndContradictoryMembershipCloseAuthority(t *testing.T) {
	plan := v2TestPlan(t)
	probe, _ := newAdapter(&fakeProvider{}, "/explicit/socket", time.Second)
	encoded, err := probe.encodeV2Plan(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		get  func(index int, key string) (gapdbapi.Record, error)
	}{
		{"partial", func(index int, key string) (gapdbapi.Record, error) {
			if index == 0 {
				return gapdbapi.NewRecord(key, encoded.values[index], 50, nil), nil
			}
			return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
		}},
		{"mixed-revision", func(index int, key string) (gapdbapi.Record, error) {
			return gapdbapi.NewRecord(key, encoded.values[index], gapdbapi.Revision(50+index%2), nil), nil
		}},
		{"contradictory", func(index int, key string) (gapdbapi.Record, error) {
			return gapdbapi.NewRecord(key, []byte(`{"foreign":true}`), 50, nil), nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			indexByKey := make(map[string]int, len(encoded.keys))
			for index, key := range encoded.keys {
				indexByKey[key.String()] = index
			}
			provider := &fakeProvider{getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
				return test.get(indexByKey[key], key)
			}}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.ReconcileV2(t.Context(), plan); !IsKind(err, OutcomeMalformedResponse) {
				t.Fatalf("authority remained open: %v", err)
			}
		})
	}
}

func TestV2AmbiguityReadUnavailabilityDoesNotBecomeAbsence(t *testing.T) {
	provider := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) {
		return gapdbapi.Record{}, &gapdbapi.TransportError{Operation: "get", Cause: errors.New("offline")}
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.ReconcileV2(t.Context(), v2TestPlan(t)); !IsKind(err, OutcomeUnavailable) {
		t.Fatalf("unavailability became absence: %v", err)
	}
}

func v2TestDigest(label string) journalv2.Digest {
	value := sha256.Sum256([]byte(label))
	return journalv2.Digest(value)
}

func v2TestPlan(t *testing.T) journalv2.SealedCommitPlanV2 {
	return v2NamedTestPlan(t, "1", "1")
}

func v2NamedTestPlan(t *testing.T, operationLabel, missionLabel string) journalv2.SealedCommitPlanV2 {
	t.Helper()
	plan, err := v2TryNamedTestPlan(t, operationLabel, missionLabel, []byte(fmt.Sprintf(`{"intent":%q}`, operationLabel)), []byte(`{"through":0}`))
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func v2TryNamedTestPlan(t *testing.T, operationLabel, missionLabel string, effectCanonical, checkpointCanonical []byte) (journalv2.SealedCommitPlanV2, error) {
	t.Helper()
	operationID := journalv2.OperationID("operation-" + operationLabel)
	missionID := journalv2.MissionID("mission-" + missionLabel)
	effectID := journalv2.EffectID("effect-" + operationLabel)
	operationEventID := journalv2.EventID("event-operation-" + operationLabel)
	missionEventID := journalv2.EventID("event-mission-" + operationLabel)
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: missionID}
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{
		OperationID: operationID, CommandKind: "mission.advance", CommandSchemaVersion: 1, CanonicalIntentDigest: v2TestDigest("intent-" + operationLabel), Scope: scope,
		ActorID: "actor-1", ProcessID: "process-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		OriginatingRuntimeEpochID: "epoch-1", ObservedCreatedAt: "2026-09-02T01:02:03Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusAccepted,
		EventIDs: []journalv2.EventID{missionEventID, operationEventID}, EffectIDs: []journalv2.EffectID{effectID}})
	if err != nil {
		t.Fatal(err)
	}
	missionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(missionID), scope)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	events := []journalv2.EventDraftV2{
		{EventID: operationEventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", Payload: []byte(`{"status":"accepted"}`)},
		{EventID: missionEventID, EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", Payload: []byte(`{"status":"planned"}`)},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, NextLogicalVersion: 1, EventID: operationEventID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: v2TestDigest("operation-state-" + operationLabel), AuthoritySummary: []byte(`{"status":"accepted"}`)},
		{Coordinate: missionCoordinate, NextLogicalVersion: 1, EventID: missionEventID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: v2TestDigest("mission-state-" + operationLabel), AuthoritySummary: []byte(`{"status":"planned"}`)},
	}
	guards := []journalv2.EligibilityGuardTransition{{MissionID: missionID, ShardID: journalv2.EligibilityShardID("shard-" + missionLabel), Canonical: []byte(`{"eligible":true}`)}}
	checkpointID := journalv2.CheckpointID("checkpoint-" + operationLabel)
	checkpoints := []journalv2.CheckpointRecord{{CheckpointID: checkpointID, Canonical: checkpointCanonical, SlotID: journalv2.CheckpointSlotID("slot-" + operationLabel), SlotCanonical: []byte(fmt.Sprintf(`{"checkpoint_id":%q}`, checkpointID))}}
	conditions := v2TestConditions(t, heads, guards, checkpoints)
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads,
		EffectIntents:     []journalv2.EffectIntentReference{{EffectID: effectID, Digest: v2TestDigest("effect-" + operationLabel), Canonical: effectCanonical}},
		EligibilityGuards: guards, Checkpoints: checkpoints, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
	return plan, err
}

func v2LaterTestPlan(t *testing.T) journalv2.SealedCommitPlanV2 {
	t.Helper()
	prior := v2TestPlan(t).OperationState()
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: "operation-1", LogicalVersion: 2, Status: journalv2.StatusInProgress,
		EventIDs: []journalv2.EventID{"event-mission-1", "event-mission-2", "event-operation-1", "event-operation-2"}, EffectIDs: []journalv2.EffectID{"effect-1"}})
	if err != nil {
		t.Fatal(err)
	}
	missionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", scope)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", "operation-1", journalv2.Scope{Kind: journalv2.ScopeHost})
	events := []journalv2.EventDraftV2{
		{EventID: "event-operation-2", EventType: "operation.progressed", Family: "operation", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: operationCoordinate, AggregateLogicalVersion: 2, CommittedRuntimeEpochID: "epoch-1", Payload: []byte(`{"status":"in_progress"}`)},
		{EventID: "event-mission-2", EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 2, CommittedRuntimeEpochID: "epoch-1", Payload: []byte(`{"status":"running"}`)},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: 1, NextLogicalVersion: 2, EventID: "event-operation-2", ExpectedRecordRevision: 41, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: v2TestDigest("operation-state-2"), AuthoritySummary: []byte(`{"status":"in_progress"}`)},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: 1, NextLogicalVersion: 2, EventID: "event-mission-2", ExpectedRecordRevision: 42, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: v2TestDigest("mission-state-2"), AuthoritySummary: []byte(`{"status":"running"}`)},
	}
	guards := []journalv2.EligibilityGuardTransition{{MissionID: "mission-1", ShardID: "shard-1", Canonical: []byte(`{"eligible":false}`), ExpectedRecordRevision: 43}}
	checkpoints := []journalv2.CheckpointRecord{{CheckpointID: "checkpoint-2", Canonical: []byte(`{"through":1}`), SlotID: "slot-1", SlotCanonical: []byte(`{"checkpoint_id":"checkpoint-2"}`), ExpectedSlotRecordRevision: 44}}
	conditions := v2TestConditions(t, heads, guards, checkpoints)
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{PriorOperationState: &prior, OperationState: state, Events: events, Heads: heads,
		EligibilityGuards: guards, Checkpoints: checkpoints, Conditions: conditions}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func v2TestConditions(t *testing.T, heads []journalv2.HeadTransitionV2, guards []journalv2.EligibilityGuardTransition, checkpoints []journalv2.CheckpointRecord) []journalv2.ProviderCondition {
	t.Helper()
	conditions := make([]journalv2.ProviderCondition, 0, len(heads)+len(guards)+len(checkpoints))
	for _, head := range heads {
		condition, err := journalv2.ConditionForHeadTransitionV2(head)
		if err != nil {
			t.Fatal(err)
		}
		conditions = append(conditions, condition)
	}
	for _, guard := range guards {
		condition, err := journalv2.ConditionForEligibilityGuardTransitionV2(guard)
		if err != nil {
			t.Fatal(err)
		}
		conditions = append(conditions, condition)
	}
	for _, checkpoint := range checkpoints {
		condition, err := journalv2.ConditionForCheckpointRecordV2(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		conditions = append(conditions, condition)
	}
	return conditions
}
