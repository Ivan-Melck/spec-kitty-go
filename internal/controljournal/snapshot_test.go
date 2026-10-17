package controljournal

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// This content-based gate names the owned scans, not source lines or hashes.
// Each must materialize once before its loop and must not rehash via Len/At.
// It checks cost structure, not authority; the unchanged type guards own that.
func TestSnapshotConsumerScans(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(file)
	targets := []struct{ path, name string }{
		{"reducer/reducer.go", "ApplyV1"},
		{"playback/playback.go", "RunV1"},
		{"checkpoint/restore.go", "deriveAuthority"},
		{"checkpoint/checkpoint.go", "analyzeCheckpointPrefix"},
		{"checkpoint/checkpoint.go", "extendWatermark"},
		{"../testcampaign/m1/faults/runner.go", "ObserveM1"},
	}
	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, target.path), nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			var function *ast.FuncDecl
			for _, decl := range parsed.Decls {
				if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == target.name {
					if target.name == "ObserveM1" {
						if f.Recv == nil {
							continue
						}
						star, ok := f.Recv.List[0].Type.(*ast.StarExpr)
						if !ok {
							continue
						}
						id, ok := star.X.(*ast.Ident)
						if !ok || id.Name != "composedObservationPort" {
							continue
						}
					}
					if function != nil {
						t.Fatal("ambiguous scan")
					}
					function = f
				}
			}
			if function == nil || function.Body == nil {
				t.Fatal("missing scan")
			}
			if err := checkSnapshotScan(function); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkSnapshotScan(function *ast.FuncDecl) error {
	var snapshotCalls, ranges int
	var snapshotName string
	var snapshotEnd token.Pos
	var failure error
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "At" || sel.Sel.Name == "Len" {
				failure = scanError("repeated capability access")
			}
			if sel.Sel.Name == "SnapshotJournalV1" || sel.Sel.Name == "SnapshotJournalV1WithinBounds" {
				snapshotCalls++
			}
		}
		return true
	})
	// Require a top-level materialization, not a snapshot inside a hot loop.
	for _, stmt := range function.Body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) != 2 {
			continue
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "SnapshotJournalV1" && sel.Sel.Name != "SnapshotJournalV1WithinBounds") {
			continue
		}
		if function.Name.Name == "RunV1" {
			if sel.Sel.Name != "SnapshotJournalV1WithinBounds" || len(call.Args) != 3 {
				return scanError("playback requires compiled bounded snapshot")
			}
			for i, name := range []string{"maxPlaybackEvents", "maxPlaybackBytes"} {
				id, ok := call.Args[i+1].(*ast.Ident)
				if !ok || id.Name != name {
					return scanError("playback bounds must be compiled constants")
				}
			}
		}
		if id, ok := assign.Lhs[0].(*ast.Ident); ok {
			snapshotName, snapshotEnd = id.Name, assign.End()
		}
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		loop, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		if id, ok := loop.X.(*ast.Ident); ok && id.Name == snapshotName && loop.Pos() > snapshotEnd {
			ranges++
		}
		return true
	})
	if failure != nil {
		return failure
	}
	expectedRanges := 1
	if function.Name.Name == "RunV1" {
		expectedRanges = 0 // Playback only needs snapshot length and its terminal fact.
	}
	if snapshotCalls != 1 || snapshotName == "" || ranges != expectedRanges {
		return scanError("scan must range over one top-level snapshot")
	}
	return nil
}

func TestSnapshotRootReadShape(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(file), "model.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range parsed.Decls {
		if f, ok := declaration.(*ast.FuncDecl); ok && f.Name.Name == "SnapshotJournalV1" {
			function = f
		}
	}
	if function == nil || function.Recv != nil || function.Body == nil || len(function.Body.List) == 0 {
		t.Fatal("missing root read function")
	}
	guard, ok := function.Body.List[0].(*ast.IfStmt)
	if !ok {
		t.Fatal("snapshot must verify before allocating/copying")
	}
	negation, ok := guard.Cond.(*ast.UnaryExpr)
	if !ok || negation.Op != token.NOT {
		t.Fatal("missing refusal guard")
	}
	call, ok := negation.X.(*ast.CallExpr)
	if !ok {
		t.Fatal("missing seal verification")
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "isValid" {
		t.Fatal("missing seal verification")
	}
	var validations, clones, loops int
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.ForStmt); ok {
			t.Error("unexpected nested/index loop")
		}
		if _, ok := node.(*ast.RangeStmt); ok {
			loops++
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "isValid":
					validations++
				case "clone":
					clones++
				case "At", "Len", "contentDigest":
					t.Error("snapshot must not repeat seal scans")
				}
			}
		}
		return true
	})
	if validations != 1 || clones != 1 || loops != 1 {
		t.Fatalf("snapshot shape: validations=%d clones=%d loops=%d", validations, clones, loops)
	}
}

// snapshotFixture also serves the benchmark and temporary test-only counter overlay.
func snapshotFixture(t testing.TB, count, payloadBytes int) ValidatedJournalV1 {
	t.Helper()
	registry, err := NewEventRegistryV1([]EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]StoredEventV1, count)
	for i := range events {
		envelope, err := NewEventEnvelopeV1(EventEnvelopeInputV1{
			EnvelopeVersion: 1, EventID: EventID(fmt.Sprintf("event-%d", i)), EventType: "mission.created",
			EventSchemaVersion: 1, ReducerVersion: "mission-v1", ProjectID: "project-1",
			AggregateKind: "mission", AggregateID: "aggregate-1", AggregateLogicalVersion: uint64(i + 1),
			OperationID: OperationID(fmt.Sprintf("operation-%d", i)), RuntimeEpochID: "epoch-1", EventCount: 1,
			WorkRevisionID: "work-1", LeaseID: "lease-1", LeaseGeneration: 1, FenceToken: 1,
			PolicyRevision: "policy-1", DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1",
			RecordedObservations: []byte(`{"observed":true}`), Payload: []byte(`{"value":"` + strings.Repeat("a", payloadBytes) + `"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		events[count-i-1] = StoredEventV1{RecordRevision: uint64(i + 1), Envelope: envelope}
	}
	journal, err := ValidateJournalV1(registry, events)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func TestSnapshotEquivalentAndDefensive(t *testing.T) {
	for _, count := range []int{0, 1, 32} {
		journal := snapshotFixture(t, count, 128)
		records, err := SnapshotJournalV1(journal)
		if err != nil {
			t.Fatal(err)
		}
		length, err := journal.Len()
		if err != nil || len(records) != length || records == nil {
			t.Fatalf("snapshot length/empty: %v", err)
		}
		for i, record := range records {
			at, err := journal.At(i)
			if err != nil || !reflect.DeepEqual(at, record) {
				t.Fatalf("ordinal %d differs: %v", i, err)
			}
		}
		if count == 0 {
			continue
		}
		original, _ := journal.At(0)
		second, err := SnapshotJournalV1(journal)
		if err != nil {
			t.Fatal(err)
		}
		records[0].RecordRevision++
		records[0].Envelope.EventID = "changed"
		records[0].Envelope.payload[0] = '['
		records[0].Envelope.recordedObservations[0] = '['
		records[0].Envelope.digest[0] ^= 1
		current, err := journal.At(0)
		if err != nil || !reflect.DeepEqual(current, original) || !reflect.DeepEqual(second[0], original) {
			t.Fatalf("snapshot leaked journal/snapshot alias: %v", err)
		}
		journal.records[0].Envelope.payload[0] = '['
		journal.records[0].Envelope.recordedObservations[0] = '['
		if !reflect.DeepEqual(second[0], original) {
			t.Fatal("private journal mutation changed earlier snapshot")
		}
	}
	// Multi-event groups keep EventIndex order within a shared revision.
	grouped, err := ValidateJournalV1(fixtureRegistry(t), []StoredEventV1{
		{RecordRevision: 7, Envelope: fixtureEnvelope(t, "b", "op", 1, 2, "agg", 2)},
		{RecordRevision: 7, Envelope: fixtureEnvelope(t, "a", "op", 0, 2, "agg", 1)},
	})
	if err != nil {
		t.Fatal(err)
	}
	records, err := SnapshotJournalV1(grouped)
	if err != nil || len(records) != 2 {
		t.Fatalf("group: %v", err)
	}
	for i := range records {
		at, err := grouped.At(i)
		if err != nil || !reflect.DeepEqual(at, records[i]) {
			t.Fatalf("group order %d: %v", i, err)
		}
	}
}

func rejectSnapshot(t *testing.T, journal ValidatedJournalV1) {
	t.Helper()
	records, err := SnapshotJournalV1(journal)
	if records != nil || !errors.As(err, new(InvalidValidatedJournalError)) {
		t.Fatalf("snapshot accepted invalid capability: %v, %T %v", records, err, err)
	}
	for _, bounds := range [][2]int{{100, 1 << 20}, {0, 0}, {1, 1}} {
		records, err := SnapshotJournalV1WithinBounds(journal, bounds[0], bounds[1])
		if records != nil || !errors.As(err, new(InvalidValidatedJournalError)) {
			t.Fatalf("bounded snapshot lost invalid-capability priority: %v, %T %v", records, err, err)
		}
	}
}

func TestSnapshotRejectsPostSuccessEveryStoredField(t *testing.T) {
	// Enumerate every exported field by content/type, so new fields cannot silently
	// escape this test; private buffers/digest and the outer metadata follow below.
	envelopeType := reflect.TypeOf(EventEnvelopeV1{})
	for i := 0; i < envelopeType.NumField(); i++ {
		field := envelopeType.Field(i)
		if !field.IsExported() {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			journal := snapshotFixture(t, 2, 128)
			if _, err := SnapshotJournalV1(journal); err != nil {
				t.Fatal(err)
			}
			value := reflect.ValueOf(&journal.records[0].Envelope).Elem().FieldByName(field.Name)
			switch value.Kind() {
			case reflect.String:
				value.SetString(value.String() + "-mutant")
			case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				value.SetUint(value.Uint() + 1)
			default:
				t.Fatalf("uncovered stored field %s", field.Name)
			}
			rejectSnapshot(t, journal)
		})
	}
	mutants := map[string]func(*ValidatedJournalV1){
		"record revision": func(j *ValidatedJournalV1) { j.records[0].RecordRevision++ },
		"payload":         func(j *ValidatedJournalV1) { j.records[0].Envelope.payload[0] = '[' },
		"observations":    func(j *ValidatedJournalV1) { j.records[0].Envelope.recordedObservations[0] = '[' },
		"event digest":    func(j *ValidatedJournalV1) { j.records[0].Envelope.digest[0] ^= 1 },
		"seal digest":     func(j *ValidatedJournalV1) { j.seal.digest[0] ^= 1 },
		"seal version":    func(j *ValidatedJournalV1) { j.seal.version++ },
		"record order":    func(j *ValidatedJournalV1) { j.records[0], j.records[1] = j.records[1], j.records[0] },
		"record count":    func(j *ValidatedJournalV1) { j.records = j.records[:1] },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			journal := snapshotFixture(t, 2, 128)
			if _, err := SnapshotJournalV1(journal); err != nil {
				t.Fatal(err)
			}
			mutate(&journal)
			rejectSnapshot(t, journal)
		})
	}
}

func TestSnapshotRejectsForged(t *testing.T) {
	for _, forged := range []ValidatedJournalV1{
		{},
		{seal: validationSealV1{version: 1}},
		{seal: validationSealV1{version: 1, digest: sha256.Sum256([]byte("forged"))}},
		{seal: validationSealV1{version: 1, digest: sha256.Sum256([]byte("forged"))}, records: []StoredEventV1{{RecordRevision: 1, Envelope: EventEnvelopeV1{payload: []byte("{")}}}},
	} {
		rejectSnapshot(t, forged)
	}
}

func TestBoundedSnapshotBudgetsAndDefensiveFacts(t *testing.T) {
	journal := snapshotFixture(t, 3, 128)
	total, first := 0, 0
	for i, record := range journal.records {
		encoded, err := EncodeEventEnvelopeV1(record.Envelope)
		if err != nil {
			t.Fatal(err)
		}
		total += len(encoded)
		if i == 0 {
			first = len(encoded)
		}
	}
	for _, test := range []struct {
		name          string
		events, bytes int
		kind          RefusalKind
		field         string
	}{
		{"zero events", 0, total, RefusalInvalidValue, "journal_snapshot_events"},
		{"negative events", -1, total, RefusalInvalidValue, "journal_snapshot_events"},
		{"zero bytes", 3, 0, RefusalInvalidValue, "journal_snapshot_bytes"},
		{"negative bytes", 3, -1, RefusalInvalidValue, "journal_snapshot_bytes"},
		{"invalid bound priority", 0, 0, RefusalInvalidValue, "journal_snapshot_events"},
		{"events", 2, total, RefusalBudgetExceeded, "journal_snapshot_events"},
		{"bytes", 3, total - 1, RefusalBudgetExceeded, "journal_snapshot_bytes"},
		{"earlier bytes before events", 1, first - 1, RefusalBudgetExceeded, "journal_snapshot_bytes"},
		{"event before next bytes", 1, first, RefusalBudgetExceeded, "journal_snapshot_events"},
	} {
		t.Run(test.name, func(t *testing.T) {
			records, err := SnapshotJournalV1WithinBounds(journal, test.events, test.bytes)
			var refusal ValidationError
			if records != nil || !errors.As(err, &refusal) || refusal.Kind != test.kind || refusal.Field != test.field {
				t.Fatalf("records=%v error=%T %v", records, err, err)
			}
		})
	}
	records, err := SnapshotJournalV1WithinBounds(journal, 3, total)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SnapshotJournalV1WithinBounds(journal, 3, total)
	if err != nil {
		t.Fatal(err)
	}
	for i, record := range records {
		at, err := journal.At(i)
		if err != nil || !reflect.DeepEqual(at, record) {
			t.Fatalf("exact-limit ordinal %d: %v", i, err)
		}
	}
	original, _ := journal.At(0)
	records[0].RecordRevision++
	records[0].Envelope.EventID = "changed"
	records[0].Envelope.payload[0] = '['
	records[0].Envelope.recordedObservations[0] = '['
	records[0].Envelope.digest[0] ^= 1
	at, err := journal.At(0)
	if err != nil || !reflect.DeepEqual(at, original) || !reflect.DeepEqual(second[0], original) {
		t.Fatal("bounded snapshot aliases source/other snapshot")
	}
	journal.records[0].Envelope.payload[0] = '['
	if !reflect.DeepEqual(second[0], original) {
		t.Fatal("private mutation changed bounded facts")
	}
	rejectSnapshot(t, journal)
	empty, err := SnapshotJournalV1WithinBounds(snapshotFixture(t, 0, 0), 1, 1)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty snapshot: %v %v", empty, err)
	}
}

func TestBoundedSnapshotPreservesEncodeRefusal(t *testing.T) {
	// The payload is individually valid, but its envelope exceeds codec limits.
	journal := snapshotFixture(t, 1, MaxOrdinaryValueBytes-12)
	_, expected := EncodeEventEnvelopeV1(journal.records[0].Envelope)
	if expected == nil {
		t.Fatal("fixture must exceed envelope encoding limit")
	}
	for _, bytes := range []int{1, 48 << 20} {
		records, err := SnapshotJournalV1WithinBounds(journal, 1, bytes)
		if records != nil || !reflect.DeepEqual(err, expected) {
			t.Fatalf("encoding priority changed: %v, want %v", err, expected)
		}
	}
}

func TestBoundedSnapshotPreflightAllocation(t *testing.T) {
	// A small prefix fits, but copying all payloads would allocate over 16 MiB.
	journal := snapshotFixture(t, 2048, 8<<10)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	records, err := SnapshotJournalV1WithinBounds(journal, 1, 48<<20)
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(journal)
	var refusal ValidationError
	if records != nil || !errors.As(err, &refusal) || refusal.Kind != RefusalBudgetExceeded || refusal.Field != "journal_snapshot_events" {
		t.Fatalf("records=%v error=%T %v", records, err, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	const ceiling = 1 << 20 // generous seal scratch + one encoded envelope allowance
	t.Logf("bounded preflight allocated=%d ceiling=%d", allocated, ceiling)
	if allocated > ceiling {
		t.Fatalf("full history copied before event guard: allocated=%d ceiling=%d", allocated, ceiling)
	}
}

func BenchmarkSnapshotJournalScan(b *testing.B) {
	for _, count := range []int{128, 512} {
		journal := snapshotFixture(b, count, 1024)
		b.Run(fmt.Sprintf("snapshot/N%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				records, err := SnapshotJournalV1(journal)
				if err != nil || len(records) != count {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("LenAt/N%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				length, err := journal.Len()
				if err != nil {
					b.Fatal(err)
				}
				for index := 0; index < length; index++ {
					if _, err := journal.At(index); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

type scanError string

func (e scanError) Error() string { return string(e) }

func TestSnapshotScanGateNonvacuity(t *testing.T) {
	for _, source := range []string{
		`package p; func f() { n,_:=journal.Len(); for i:=0;i<n;i++ { journal.At(i) } }`,
		`package p; func f() { for i:=0;i<2;i++ { records,_:=cj.SnapshotJournalV1(journal); for range records {} } }`,
		`package p; func f() { records,_:=cj.SnapshotJournalV1(journal); _=records }`,
		`package p; func RunV1() { records,_:=cj.SnapshotJournalV1(journal); _=records }`,
		`package p; func RunV1() { records,_:=cj.SnapshotJournalV1WithinBounds(journal, 999999, maxPlaybackBytes); _=records }`,
		`package p; func RunV1() { records,_:=cj.SnapshotJournalV1WithinBounds(journal, maxPlaybackEvents, maxPlaybackBytes); for range records {} }`,
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "mutant.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		if checkSnapshotScan(file.Decls[0].(*ast.FuncDecl)) == nil {
			t.Fatal("gate admitted hot-loop/decorative mutant")
		}
	}
}
