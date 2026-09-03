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

	keys := []v2Key{v2AuthorityBindingKey(), v2RuntimeCurrentKey()}
	appendKey := func(key v2Key, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	appendKey(v2RuntimeEpochKey("epoch-1"))
	appendKey(v2OperationBindingKey("operation-1"))
	appendKey(v2OperationHeadKey("operation-1"))
	appendKey(v2EffectIntentKey("effect-1"))
	appendKey(v2CheckpointSlotKey("slot-1"))
	appendKey(v2CheckpointRecordKey("checkpoint-1"))
	appendKey(v2EligibilityGuardKey("mission-1", "shard-1"))
	for index, scope := range []journalv2.Scope{host, project, mission, repository, workRevision, work} {
		coordinate, err := journalv2.NewAggregateCoordinate("aggregate", "aggregate-1", scope)
		if err != nil {
			t.Fatal(err)
		}
		appendKey(v2AggregateHeadKey(coordinate))
		appendKey(v2JournalEventKey(coordinate, journalv2.LogicalVersion(index+1)))
	}

	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key.String() == "" || seen[key.String()] {
			t.Fatalf("empty or colliding key %q", key.String())
		}
		seen[key.String()] = true
		decoded, err := parseV2Key(key.String())
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
		key, err := v2JournalEventKey(coordinate, version)
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
	left, err := v2OperationBindingKey("operation-1")
	if err != nil {
		t.Fatal(err)
	}
	right, err := v2OperationBindingKey("operation-1")
	if err != nil || left.String() != right.String() || strings.Contains(left.String(), "mission") || strings.Contains(left.String(), "project") {
		t.Fatalf("left=%q right=%q err=%v", left.String(), right.String(), err)
	}
	if left.String() != "spk/v2/operations/k6f7065726174696f6e2d31/binding" {
		t.Fatalf("unexpected canonical global key %q", left.String())
	}
}

func TestV2KeyOperationAggregatePlacementHasNoScopedAliases(t *testing.T) {
	t.Parallel()
	kind, _ := journalv2.EncodeKeyComponent("operation")
	id, _ := journalv2.EncodeKeyComponent("operation-1")
	version, _ := journalv2.LogicalVersion(1).Hex16()

	canonicalHead, err := v2OperationHeadKey("operation-1")
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := parseV2Key(canonicalHead.String()); err != nil || parsed.String() != canonicalHead.String() {
		t.Fatalf("canonical operation head rejected: parsed=%q err=%v", parsed.String(), err)
	}

	for _, test := range []struct {
		name  string
		scope journalv2.Scope
		host  bool
	}{
		{name: "host", scope: journalv2.Scope{Kind: journalv2.ScopeHost}, host: true},
		{name: "project", scope: journalv2.Scope{Kind: journalv2.ScopeProject, ProjectID: "project-1"}},
		{name: "mission", scope: journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			coordinate, err := journalv2.NewAggregateCoordinate("operation", "operation-1", test.scope)
			if err != nil {
				t.Fatal(err)
			}
			scope, _ := test.scope.CanonicalComponent()
			headAlias := strings.Join([]string{"spk", "v2", "scopes", scope, "heads", kind, id}, "/")
			if _, err := v2AggregateHeadKey(coordinate); !IsKind(err, OutcomeInvalid) {
				t.Fatalf("generic operation head constructor accepted %s scope: %v", test.name, err)
			}
			if _, err := parseV2Key(headAlias); !IsKind(err, OutcomeInvalid) {
				t.Fatalf("generic operation head parser accepted %s alias %q: %v", test.name, headAlias, err)
			}

			eventAlias := strings.Join([]string{"spk", "v2", "scopes", scope, "journal", kind, id, version}, "/")
			event, constructErr := v2JournalEventKey(coordinate, 1)
			parsed, parseErr := parseV2Key(eventAlias)
			if test.host {
				if constructErr != nil || parseErr != nil || event.String() != eventAlias || parsed.String() != eventAlias {
					t.Fatalf("canonical host operation event rejected: event=%q parsed=%q construct=%v parse=%v", event.String(), parsed.String(), constructErr, parseErr)
				}
				return
			}
			if !IsKind(constructErr, OutcomeInvalid) || !IsKind(parseErr, OutcomeInvalid) {
				t.Fatalf("scoped operation event alias accepted: construct=%v parse=%v key=%q", constructErr, parseErr, eventAlias)
			}
		})
	}
}

func TestV2KeyRejectsAliasesAndMalformedVersions(t *testing.T) {
	t.Parallel()
	valid, err := v2OperationBindingKey("operation-1")
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
		if _, err := parseV2Key(mutant); !IsKind(err, OutcomeInvalid) {
			t.Fatalf("alias accepted %q err=%v", mutant, err)
		}
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", journalv2.Scope{Kind: journalv2.ScopeHost})
	event, err := v2JournalEventKey(coordinate, 1)
	if err != nil {
		t.Fatal(err)
	}
	version := event.String()[len(event.String())-16:]
	for _, mutant := range []string{"0000000000000000", "000000000000001", "00000000000000001", "000000000000000A", "+000000000000001", "fffffffffffffffff", "gggggggggggggggg"} {
		encoded := strings.TrimSuffix(event.String(), version) + mutant
		if _, err := parseV2Key(encoded); !IsKind(err, OutcomeInvalid) {
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
	exact, err := v2JournalEventKey(coordinate, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.String()) != MaxKeyBytes {
		t.Fatal("could not construct exact canonical key boundary")
	}
	if decoded, err := parseV2Key(exact.String()); err != nil || decoded.String() != exact.String() {
		t.Fatalf("exact key rejected: %v", err)
	}
	if _, err := parseV2Key(exact.String() + "x"); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("key maximum-plus-one accepted: %v", err)
	}
}

func TestV2CanonicalPrefixesRoundTrip(t *testing.T) {
	t.Parallel()
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", scope)
	prefixes := []v2Prefix{v2RuntimeEpochsPrefix(), v2OperationsPrefix(), v2EffectsPrefix(), v2CheckpointSlotsPrefix(), v2CheckpointRecordsPrefix()}
	appendPrefix := func(prefix v2Prefix, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		prefixes = append(prefixes, prefix)
	}
	prefixes = append(prefixes, v2AggregateHeadsPrefix())
	appendPrefix(v2JournalPrefix(scope))
	appendPrefix(v2AggregateJournalPrefix(coordinate))
	appendPrefix(v2EligibilityPrefix("mission-1"))
	for _, prefix := range prefixes {
		parsed, err := parseV2Prefix(prefix.String())
		if err != nil || parsed.String() != prefix.String() || parsed.Kind() != prefix.Kind() {
			t.Fatalf("prefix=%q parsed=%+v err=%v", prefix.String(), parsed, err)
		}
	}
	for _, malformed := range []string{"", "spk/v2/scopes/host/heads", "spk/v2/scopes/host//heads/", "spk/v2/operations//", strings.Repeat("x", MaxKeyBytes+1)} {
		if _, err := parseV2Prefix(malformed); !IsKind(err, OutcomeInvalid) {
			t.Fatalf("malformed prefix accepted %q: %v", malformed, err)
		}
	}
}

func TestV2CaptureAndVerifyStableRevisionCoordinate(t *testing.T) {
	statuses := []gapdbapi.StatusResult{
		{DatabaseID: "db-1", CurrentRevision: 17, DurableThroughRevision: 19},
		{DatabaseID: "db-1", CurrentRevision: 17, DurableThroughRevision: 23},
	}
	var calls atomic.Int32
	provider := &fakeProvider{statusFn: func(context.Context) (gapdbapi.StatusResult, error) {
		index := int(calls.Add(1)) - 1
		return statuses[index], nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	captured, err := adapter.CaptureRevisionV2(t.Context())
	if err != nil || captured.DatabaseID() != "db-1" || captured.CurrentRevision() != 17 || captured.DurableThroughRevision() != 19 {
		t.Fatalf("capture=%+v err=%v", captured, err)
	}
	if err := adapter.VerifyRevisionV2(t.Context(), captured); err != nil {
		t.Fatalf("stable final coordinate rejected: %v", err)
	}

	for name, status := range map[string]gapdbapi.StatusResult{
		"database":   {DatabaseID: "db-2", CurrentRevision: 17, DurableThroughRevision: 17},
		"revision":   {DatabaseID: "db-1", CurrentRevision: 18, DurableThroughRevision: 18},
		"durability": {DatabaseID: "db-1", CurrentRevision: 17, DurableThroughRevision: 16},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{statusFn: func(context.Context) (gapdbapi.StatusResult, error) { return status, nil }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if err := adapter.VerifyRevisionV2(t.Context(), captured); !journalv2.IsKind(err, journalv2.ErrorRevisionMoved) {
				t.Fatalf("movement accepted: %v", err)
			}
		})
	}
}

func TestV2CaptureRejectsNonDurableOrUnidentifiedStatus(t *testing.T) {
	for name, status := range map[string]gapdbapi.StatusResult{
		"database":    {CurrentRevision: 1, DurableThroughRevision: 1},
		"zero":        {DatabaseID: "db-1"},
		"not-durable": {DatabaseID: "db-1", CurrentRevision: 2, DurableThroughRevision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{statusFn: func(context.Context) (gapdbapi.StatusResult, error) { return status, nil }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.CaptureRevisionV2(t.Context()); !journalv2.IsKind(err, journalv2.ErrorMalformed) {
				t.Fatalf("malformed capture accepted: %v", err)
			}
		})
	}
}

func TestV2ReaderPreservesMetadataAndCopiesBuffers(t *testing.T) {
	key, _ := v2OperationHeadKey("operation-1")
	address, _ := journalv2.NewOperationHeadAddressV2("operation-1")
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 17, 19)
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	expires := time.Date(2030, 1, 2, 3, 4, 5, 123456789, time.FixedZone("offset", 3600))
	providerValue := []byte(`{"status":"accepted"}`)
	provider := &fakeProvider{getFn: func(_ context.Context, got string) (gapdbapi.Record, error) {
		if got != key.String() {
			t.Fatalf("got key %q", got)
		}
		return gapdbapi.Record{Key: got, Value: providerValue, Revision: 17, ExpiresAt: &expires}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	record, err := adapter.ReadV2(t.Context(), request)
	wantExpiry := expires.UTC().Format(time.RFC3339Nano)
	if err != nil || record.Address() != address || record.RecordRevision() != 17 || record.ExpiresAt() != wantExpiry {
		t.Fatalf("record=%+v expiry=%q want=%q err=%v", record, record.ExpiresAt(), wantExpiry, err)
	}
	providerValue[0] = 'x'
	value := record.CanonicalValue()
	value[0] = 'y'
	if string(record.CanonicalValue()) != `{"status":"accepted"}` {
		t.Fatal("provider or caller buffer mutation escaped reader copies")
	}
}

func TestV2ReaderGlobalHeadScanDiscoversPreviouslyUnknownScope(t *testing.T) {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-new", MissionID: "mission-new"}
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-new", scope)
	key, _ := v2AggregateHeadKey(coordinate)
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 11, 11)
	request, _ := journalv2.NewScanRequestV2(journalv2.AggregateHeadScanTargetV2(), attempt, nil, 1, MaxScanBytes)
	provider := &fakeProvider{scanFn: func(_ context.Context, prefix string, _ int, _ string) (gapdbapi.ScanPage, error) {
		if prefix != "spk/v2/heads/" {
			t.Fatalf("scope-first head prefix returned: %q", prefix)
		}
		return gapdbapi.ScanPage{ObservedRevision: 11, AsOf: time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC), Records: []gapdbapi.Record{gapdbapi.NewRecord(key.String(), []byte(`{"status":"new"}`), 10, nil)}}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	page, err := adapter.ScanV2(t.Context(), request)
	if err != nil || len(page.Records()) != 1 || page.Records()[0].Address().Coordinate() != coordinate {
		t.Fatalf("unknown-scope head not discovered: page=%+v err=%v", page, err)
	}
}

func TestV2ReaderRejectsDishonestExactResponses(t *testing.T) {
	key, _ := v2OperationHeadKey("operation-1")
	foreign, _ := v2OperationHeadKey("operation-2")
	address, _ := journalv2.NewOperationHeadAddressV2("operation-1")
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 2, 2)
	request, _ := journalv2.NewReadRequestV2(address, attempt)
	zeroTime := time.Time{}
	mutants := map[string]gapdbapi.Record{
		"foreign":     gapdbapi.NewRecord(foreign.String(), []byte(`{}`), 1, nil),
		"zero-rev":    gapdbapi.NewRecord(key.String(), []byte(`{}`), 0, nil),
		"future-rev":  gapdbapi.NewRecord(key.String(), []byte(`{}`), 3, nil),
		"oversized":   gapdbapi.NewRecord(key.String(), make([]byte, MaxOrdinaryValueBytes+1), 1, nil),
		"zero-expiry": gapdbapi.NewRecord(key.String(), []byte(`{}`), 1, &zeroTime),
	}
	for name, response := range mutants {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) { return response, nil }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.ReadV2(t.Context(), request); !journalv2.IsKind(err, journalv2.ErrorMalformed) {
				t.Fatalf("dishonest response accepted: %v", err)
			}
		})
	}
}

func TestV2ReaderScanExactBudgetsAndSafeRepage(t *testing.T) {
	records := make([]gapdbapi.Record, MaxScanRecords)
	base := 0
	for index := range records {
		coordinate, err := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(fmt.Sprintf("mission-%03d", index)), journalv2.Scope{Kind: journalv2.ScopeHost})
		if err != nil {
			t.Fatal(err)
		}
		key, err := v2AggregateHeadKey(coordinate)
		if err != nil {
			t.Fatal(err)
		}
		records[index] = gapdbapi.NewRecord(key.String(), []byte(`{}`), 7, nil)
		base += len(key.String()) + 32
	}
	remaining := MaxScanBytes - base
	for index := range records {
		size := remaining / (len(records) - index)
		records[index] = gapdbapi.NewRecord(records[index].Key, v2CanonicalAt(size), 7, nil)
		remaining -= size
	}
	if remaining != 0 {
		t.Fatalf("scan fixture remaining=%d", remaining)
	}
	prefix := v2AggregateHeadsPrefix()
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 7, 7)
	request, _ := journalv2.NewScanRequestV2(journalv2.AggregateHeadScanTargetV2(), attempt, nil, MaxScanRecords, MaxScanBytes)
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
		return gapdbapi.ScanPage{ObservedRevision: 7, AsOf: time.Date(2026, 9, 2, 1, 2, 3, 456789123, time.UTC), Records: records[:count], Cursor: next, Truncated: truncated}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	page, err := adapter.ScanV2(t.Context(), request)
	if err != nil || len(page.Records()) != MaxScanRecords || page.ObservedRevision() != 7 || page.AsOf() != "2026-09-02T01:02:03.456789123Z" || fmt.Sprint(calls) != "[500]" {
		t.Fatalf("page records=%d revision=%d calls=%v err=%v", len(page.Records()), page.ObservedRevision(), calls, err)
	}
	records[len(records)-1].Value = v2CanonicalAt(len(records[len(records)-1].Value) + 1)
	calls = nil
	page, err = adapter.ScanV2(t.Context(), request)
	if err != nil || len(page.Records()) != MaxScanRecords/2 || !page.Truncated() || fmt.Sprint(calls) != "[500 250]" {
		t.Fatalf("repage records=%d calls=%v err=%v", len(page.Records()), calls, err)
	}
}

func TestV2ReaderScanRejectsMetadataMutantsAndUnstableRepage(t *testing.T) {
	coordinateA, _ := journalv2.NewAggregateCoordinate("mission", "mission-a", journalv2.Scope{Kind: journalv2.ScopeHost})
	coordinateB, _ := journalv2.NewAggregateCoordinate("mission", "mission-b", journalv2.Scope{Kind: journalv2.ScopeHost})
	keyA, _ := v2AggregateHeadKey(coordinateA)
	keyB, _ := v2AggregateHeadKey(coordinateB)
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 2, 2)
	request, _ := journalv2.NewScanRequestV2(journalv2.AggregateHeadScanTargetV2(), attempt, nil, 2, MaxScanBytes)
	now := time.Date(2026, 9, 2, 1, 2, 3, 0, time.UTC)
	mutants := map[string]gapdbapi.ScanPage{
		"zero-observed":   {AsOf: now},
		"zero-asof":       {ObservedRevision: 2},
		"cursor-lie":      {ObservedRevision: 2, AsOf: now, Cursor: "cursor"},
		"truncated-empty": {ObservedRevision: 2, AsOf: now, Cursor: "cursor", Truncated: true},
		"wrong-observed":  {ObservedRevision: 1, AsOf: now},
		"unsorted":        {ObservedRevision: 2, AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord(keyB.String(), []byte(`{}`), 1, nil), gapdbapi.NewRecord(keyA.String(), []byte(`{}`), 1, nil)}},
		"future-record":   {ObservedRevision: 2, AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord(keyA.String(), []byte(`{}`), 3, nil)}},
		"foreign":         {ObservedRevision: 2, AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord("spk/v2/runtime/current", []byte(`{}`), 1, nil)}},
	}
	for name, response := range mutants {
		t.Run(name, func(t *testing.T) {
			provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) { return response, nil }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.ScanV2(t.Context(), request); !journalv2.IsKind(err, journalv2.ErrorMalformed) {
				t.Fatalf("metadata mutant accepted: %v", err)
			}
		})
	}
	provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		return gapdbapi.ScanPage{ObservedRevision: 2, AsOf: now}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	terminal, err := adapter.ScanV2(t.Context(), request)
	if err != nil || len(terminal.Records()) != 0 || terminal.Truncated() || len(terminal.Cursor()) != 0 {
		t.Fatalf("valid empty terminal page rejected: page=%+v err=%v", terminal, err)
	}

	var calls atomic.Int32
	provider = &fakeProvider{scanFn: func(_ context.Context, _ string, limit int, _ string) (gapdbapi.ScanPage, error) {
		call := calls.Add(1)
		value := v2CanonicalAt(100)
		page := gapdbapi.ScanPage{ObservedRevision: gapdbapi.Revision(call + 1), AsOf: now, Records: []gapdbapi.Record{gapdbapi.NewRecord(keyA.String(), value, 1, nil), gapdbapi.NewRecord(keyB.String(), value, 1, nil)}, Cursor: "next", Truncated: true}
		if limit == 1 {
			page.Records = page.Records[:1]
		}
		return page, nil
	}}
	adapter, _ = newAdapter(provider, "/explicit/socket", time.Second)
	smallRequest, _ := journalv2.NewScanRequestV2(journalv2.AggregateHeadScanTargetV2(), attempt, nil, 2, len(keyA.String())+100+32)
	if _, err := adapter.ScanV2(t.Context(), smallRequest); !journalv2.IsKind(err, journalv2.ErrorMalformed) || calls.Load() != 2 {
		t.Fatalf("unstable repage err=%v calls=%d", err, calls.Load())
	}
}

func TestV2ReaderContinuationCursorAndStaleProviderCursor(t *testing.T) {
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 9, 9)
	cursor := []byte("opaque-revision-nine")
	request, _ := journalv2.NewScanRequestV2(journalv2.OperationRecordScanTargetV2(), attempt, cursor, 10, MaxScanBytes)
	provider := &fakeProvider{scanFn: func(_ context.Context, prefix string, limit int, got string) (gapdbapi.ScanPage, error) {
		if prefix != v2OperationsPrefix().String() || limit != 10 || got != string(cursor) {
			t.Fatalf("prefix=%q limit=%d cursor=%q", prefix, limit, got)
		}
		return gapdbapi.ScanPage{ObservedRevision: 9, AsOf: time.Date(2026, 9, 2, 1, 2, 3, 987654321, time.UTC)}, nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	page, err := adapter.ScanV2(t.Context(), request)
	if err != nil || page.ObservedRevision() != 9 || page.AsOf() != "2026-09-02T01:02:03.987654321Z" {
		t.Fatalf("page=%+v err=%v", page, err)
	}

	provider.scanFn = func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		return gapdbapi.ScanPage{}, &gapdbapi.Error{Code: gapdbapi.CodeScanStale}
	}
	if _, err := adapter.ScanV2(t.Context(), request); !journalv2.IsKind(err, journalv2.ErrorStaleCursor) {
		t.Fatalf("stale cursor was not preserved: %v", err)
	}
}

func TestV2ReaderRequestBudgetsRefuseBeforeProvider(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{scanFn: func(context.Context, string, int, string) (gapdbapi.ScanPage, error) {
		calls.Add(1)
		return gapdbapi.ScanPage{}, errors.New("unexpected")
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.ScanV2(t.Context(), journalv2.ScanRequestV2{}); !journalv2.IsKind(err, journalv2.ErrorInvalidCanonicalData) {
		t.Fatalf("zero request accepted: %v", err)
	}
	attempt, _ := journalv2.NewStoreRevisionV2("db-1", 1, 1)
	for _, input := range []struct {
		cursor []byte
		limit  int
		bytes  int
	}{
		{nil, MaxScanRecords + 1, 1},
		{nil, 1, MaxScanBytes + 1},
		{[]byte(strings.Repeat("x", MaxCursorBytes+1)), 1, 1},
	} {
		if _, err := journalv2.NewScanRequestV2(journalv2.AggregateHeadScanTargetV2(), attempt, input.cursor, input.limit, input.bytes); err == nil {
			t.Fatal("foundation accepted an over-budget request")
		}
	}
	invalidUTF8, _ := journalv2.NewScanRequestV2(journalv2.AggregateHeadScanTargetV2(), attempt, []byte{0xff}, 1, 1)
	if _, err := adapter.ScanV2(t.Context(), invalidUTF8); !journalv2.IsKind(err, journalv2.ErrorInvalidCanonicalData) {
		t.Fatalf("provider-inexpressible cursor accepted: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached provider %d times", calls.Load())
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
	if err != nil || calls.Load() != 1 || result.Revision() != 51 || result.DurableThroughRevision() != 52 || result.MutationCount() != len(records) ||
		result.PublicationID() != plan.PublicationID() || result.PlanDigest() != plan.Digest() || result.RecordSetDigest() != plan.RecordSetDigest() {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestV2CommitPreservesIndependentRevisionConditions(t *testing.T) {
	plan := v2LaterTestPlan(t)
	want := map[v2KeyKind]uint64{v2KeyOperationHead: 41, v2KeyAggregateHead: 42, v2KeyEligibilityGuard: 43, v2KeyCheckpointSlot: 44}
	provider := &fakeProvider{batchFn: func(_ context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		for _, mutation := range batch.Mutations {
			key, err := parseV2Key(mutation.Key)
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

func TestV2CommitAmbiguousTransportTakesPrecedenceOverJoinedCancellation(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{}, errors.Join(
			context.Canceled,
			&gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: true, Cause: io.EOF},
		)
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.CommitV2(t.Context(), v2TestPlan(t)); !IsKind(err, OutcomeAmbiguous) || calls.Load() != 1 {
		t.Fatalf("joined may-have-applied outcome lost ambiguity: err=%v calls=%d", err, calls.Load())
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
				statusFn: func(context.Context) (gapdbapi.StatusResult, error) {
					return gapdbapi.StatusResult{DatabaseID: "db-1", CurrentRevision: 82, DurableThroughRevision: 82}, nil
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
	attempt, err := harness.adapter.CaptureRevisionV2(t.Context())
	if err != nil || attempt.CurrentRevision() < result.Revision() {
		t.Fatalf("capture=%+v err=%v", attempt, err)
	}
	for _, sealed := range plan.Records() {
		key, err := v2KeyForSealedRecord(sealed)
		if err != nil {
			t.Fatal(err)
		}
		address, err := v2RecoveryAddressForKey(key)
		if err != nil {
			continue // effects and eligibility guards are not restore exact-read families
		}
		request, _ := journalv2.NewReadRequestV2(address, attempt)
		record, err := harness.adapter.ReadV2(t.Context(), request)
		if err != nil || record.RecordRevision() != result.Revision() || string(record.CanonicalValue()) != string(sealed.CanonicalValue()) {
			t.Fatalf("key=%s revision=%d err=%v", key.String(), record.RecordRevision(), err)
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
	measurement, err := adapter.preflightCommitV2(v2TestPlan(t))
	if err != nil || measurement.MutationCount != len(v2TestPlan(t).Records()) || measurement.RawBytes <= 0 || measurement.WireBytes <= measurement.RawBytes || calls.Load() != 0 {
		t.Fatalf("measurement=%+v calls=%d err=%v", measurement, calls.Load(), err)
	}
	adapter.wireSize = func([]byte) int { return MaxWireFrameBytes + 1 }
	if _, err := adapter.preflightCommitV2(v2TestPlan(t)); !IsKind(err, OutcomeInvalid) || calls.Load() != 0 {
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
		statusFn: func(context.Context) (gapdbapi.StatusResult, error) {
			return gapdbapi.StatusResult{DatabaseID: "db-1", CurrentRevision: 80, DurableThroughRevision: 79}, nil
		},
	}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	result, err := adapter.reconcileV2(t.Context(), plan)
	if err != nil || result.disposition != v2ReconcileCommitted || result.observation.PublicationID() != plan.PublicationID() || result.observation.Revision() != 77 ||
		result.observation.DurableThroughRevision() != 79 || result.operationState.LastPublicationID() != plan.PublicationID() ||
		result.observation.MutationCount() != len(encoded.keys) || int(reads.Load()) != len(encoded.keys) || writes.Load() != 0 {
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
		values := make(map[string][]byte, len(encoded.keys))
		for index, key := range encoded.keys {
			conditions[key.String()] = encoded.batch.Mutations[index].Condition
			values[key.String()] = encoded.values[index]
		}
		provider := &fakeProvider{getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
			condition := conditions[key]
			if condition.Kind == gapdbapi.ConditionAbsent {
				return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
			}
			return gapdbapi.NewRecord(key, values[key], condition.ExpectedRevision, nil), nil
		}}
		adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
		result, err := adapter.reconcileV2(t.Context(), plan)
		if err != nil || result.disposition != v2ReconcileRetrySamePublication || result.observation.Revision() != 0 {
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
			if _, err := adapter.reconcileV2(t.Context(), plan); !IsKind(err, OutcomeMalformedResponse) {
				t.Fatalf("authority remained open: %v", err)
			}
		})
	}
}

func TestV2AmbiguityEverySingletonExactMutableMemberClosesAuthority(t *testing.T) {
	plan := v2LaterTestPlan(t)
	probe, _ := newAdapter(&fakeProvider{}, "/explicit/socket", time.Second)
	encoded, err := probe.encodeV2Plan(plan)
	if err != nil {
		t.Fatal(err)
	}

	covered := make(map[journalv2.SealedRecordRole]int)
	for target, mutation := range encoded.batch.Mutations {
		if mutation.Condition.Kind != gapdbapi.ConditionRevision {
			continue
		}
		role := encoded.roles[target]
		covered[role]++
		t.Run(fmt.Sprintf("%s-%d", role, target), func(t *testing.T) {
			indexByKey := make(map[string]int, len(encoded.keys))
			for index, key := range encoded.keys {
				indexByKey[key.String()] = index
			}
			provider := &fakeProvider{getFn: func(_ context.Context, key string) (gapdbapi.Record, error) {
				index := indexByKey[key]
				condition := encoded.batch.Mutations[index].Condition
				switch {
				case index == target:
					return gapdbapi.NewRecord(key, encoded.values[index], condition.ExpectedRevision+100, nil), nil
				case condition.Kind == gapdbapi.ConditionAbsent:
					return gapdbapi.Record{}, &gapdbapi.Error{Code: gapdbapi.CodeNotFound}
				default:
					return gapdbapi.NewRecord(key, []byte(`{"unchanged":true}`), condition.ExpectedRevision, nil), nil
				}
			}}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			if _, err := adapter.reconcileV2(t.Context(), plan); !IsKind(err, OutcomeMalformedResponse) {
				t.Fatalf("singleton exact %s member did not close authority: %v", role, err)
			}
		})
	}
	for _, role := range []journalv2.SealedRecordRole{
		journalv2.RecordOperationHead,
		journalv2.RecordAggregateHead,
		journalv2.RecordEligibilityGuard,
		journalv2.RecordCheckpointSlot,
	} {
		if covered[role] == 0 {
			t.Fatalf("fixture omitted mutable role %s", role)
		}
	}
}

func TestV2AmbiguityReadUnavailabilityDoesNotBecomeAbsence(t *testing.T) {
	provider := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) {
		return gapdbapi.Record{}, &gapdbapi.TransportError{Operation: "get", Cause: errors.New("offline")}
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	if _, err := adapter.reconcileV2(t.Context(), v2TestPlan(t)); !IsKind(err, OutcomeUnavailable) {
		t.Fatalf("unavailability became absence: %v", err)
	}
}

func v2TestDigest(label string) journalv2.Digest {
	value := sha256.Sum256([]byte(label))
	return journalv2.Digest(value)
}

func v2CanonicalAt(size int) []byte {
	const overhead = len(`{"x":""}`)
	if size < overhead {
		return []byte(`{}`)
	}
	return []byte(`{"x":"` + strings.Repeat("x", size-overhead) + `"}`)
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
	operationPayload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		t.Fatal(err)
	}
	missionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID(missionID), scope)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), journalv2.Scope{Kind: journalv2.ScopeHost})
	events := []journalv2.EventDraftV2{
		{EventID: operationEventID, EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", Payload: operationPayload},
		{EventID: missionEventID, EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-1", Payload: []byte(`{"status":"planned"}`)},
	}
	heads := []journalv2.HeadTransitionV2{
		{Coordinate: operationCoordinate, NextLogicalVersion: 1, EventID: operationEventID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: v2TestDigest("operation-state-" + operationLabel), AuthoritySummary: []byte(`{"status":"accepted"}`)},
		{Coordinate: missionCoordinate, NextLogicalVersion: 1, EventID: missionEventID, CommittedRuntimeEpochID: "epoch-1", CanonicalStateDigest: v2TestDigest("mission-state-" + operationLabel), AuthoritySummary: []byte(`{"status":"planned"}`)},
	}
	guards := []journalv2.EligibilityGuardTransition{{MissionID: missionID, ShardID: journalv2.EligibilityShardID("shard-" + missionLabel), Canonical: []byte(fmt.Sprintf(`{"eligible":true,"operation":%q}`, operationID))}}
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
	operationPayload, err := journalv2.EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		t.Fatal(err)
	}
	missionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", "mission-1", scope)
	operationCoordinate, _ := journalv2.NewAggregateCoordinate("operation", "operation-1", journalv2.Scope{Kind: journalv2.ScopeHost})
	events := []journalv2.EventDraftV2{
		{EventID: "event-operation-2", EventType: "operation.progressed", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 2, CommittedRuntimeEpochID: "epoch-1", Payload: operationPayload},
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
