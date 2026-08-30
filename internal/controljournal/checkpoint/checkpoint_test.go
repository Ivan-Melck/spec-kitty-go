package checkpoint

import (
	"errors"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

func testSource(t *testing.T) SourceIdentityV1 {
	t.Helper()
	eventDigest, err := testEventRegistry(t).Digest()
	if err != nil {
		t.Fatal(err)
	}
	reducerDigest, err := testReducers(t).Digest()
	if err != nil {
		t.Fatal(err)
	}
	return SourceIdentityV1{ProjectID: "project-1", MissionID: "mission-1", RunID: "run-1", ReducerSetDigest: reducerDigest, EventSchemaSetDigest: eventDigest}
}

func testAuthority() AuthoritySummaryV1 {
	return AuthoritySummaryV1{RunID: "run-1", RuntimeEpochID: "epoch-1", LeaseID: "lease-1", LeaseGeneration: 2, FenceToken: 3, DurableRevision: 4}
}

func testEventRegistry(t *testing.T) controljournal.EventRegistryV1 {
	t.Helper()
	registry, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testReducers(t *testing.T) reducer.RegistryV1 {
	t.Helper()
	registry, err := reducer.NewRegistryV1([]reducer.EntryV1{{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func testStoredEvent(t *testing.T, revision uint64, eventID, operationID, aggregateID string, logical uint64, index, count uint16, payload string) controljournal.StoredEventV1 {
	t.Helper()
	eventIndex, err := controljournal.NewEventIndexV1(index)
	if err != nil {
		t.Fatal(err)
	}
	eventCount, err := controljournal.NewEventCountV1(count)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{
		EnvelopeVersion: 1, EventID: controljournal.EventID(eventID), EventType: "mission.updated", EventSchemaVersion: 1,
		ReducerVersion: "mission-v1", ProjectID: "project-1", AggregateKind: "mission", AggregateID: controljournal.AggregateID(aggregateID),
		AggregateLogicalVersion: logical, OperationID: controljournal.OperationID(operationID), RuntimeEpochID: "epoch-1",
		LeaseID: "lease-1", LeaseGeneration: 2, FenceToken: 3,
		EventIndex: eventIndex, EventCount: eventCount, Payload: []byte(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	return controljournal.StoredEventV1{RecordRevision: revision, Envelope: envelope}
}

func testJournal(t *testing.T, records ...controljournal.StoredEventV1) controljournal.ValidatedJournalV1 {
	t.Helper()
	journal, err := controljournal.ValidateJournalV1(testEventRegistry(t), records)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func snapshot(t *testing.T, id string, version uint64, eventID, state string) AggregateSnapshotV1 {
	t.Helper()
	value, err := reducer.NewStateV1([]byte(state))
	if err != nil {
		t.Fatal(err)
	}
	return AggregateSnapshotV1{Kind: "mission", ID: controljournal.AggregateID(id), LogicalVersion: version, RuntimeEpochID: "epoch-1", LastEventID: controljournal.EventID(eventID), State: value.CanonicalBytes(), StateDigest: value.Digest()}
}

type checkpointFixture struct {
	Capability  ValidatedCheckpointV1
	Record      CheckpointRecordV1
	Head        CheckpointHeadV1
	RecordBytes []byte
	HeadBytes   []byte
}

func buildFixture(t *testing.T, records []controljournal.StoredEventV1, aggregates []AggregateSnapshotV1, checkpointID string) checkpointFixture {
	t.Helper()
	journal := testJournal(t, records...)
	watermark, err := journalWatermark(journal, "")
	if err != nil {
		t.Fatal(err)
	}
	authority := testAuthority()
	authority.DurableRevision = watermark.ThroughRecordRevision
	result, err := BuildV1(BuildInputV1{CheckpointID: CheckpointID(checkpointID), Source: testSource(t), PriorPrefix: journal, EventRegistry: testEventRegistry(t), Reducers: testReducers(t), Watermark: watermark, Aggregates: aggregates, Authority: authority, FormingBatchOperation: "forming-operation"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := result.RecordV1()
	if err != nil {
		t.Fatal(err)
	}
	head, err := result.HeadV1()
	if err != nil {
		t.Fatal(err)
	}
	publication, err := result.PublicationV1("forming-operation")
	if err != nil {
		t.Fatal(err)
	}
	return checkpointFixture{Capability: result, Record: record, Head: head, RecordBytes: publication.RecordBytes, HeadBytes: publication.HeadBytes}
}

func TestCheckpointCanonicalBytesStableAcrossAggregateOrder(t *testing.T) {
	records := []controljournal.StoredEventV1{
		testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`),
		testStoredEvent(t, 8, "event-b", "operation-b", "aggregate-b", 1, 0, 1, `{"value":2}`),
	}
	a := snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)
	b := snapshot(t, "aggregate-b", 1, "event-b", `{"value":2}`)
	left := buildFixture(t, records, []AggregateSnapshotV1{a, b}, "checkpoint-1")
	right := buildFixture(t, records, []AggregateSnapshotV1{b, a}, "checkpoint-1")
	if string(left.RecordBytes) != string(right.RecordBytes) || left.Head.CheckpointRecordDigest != right.Head.CheckpointRecordDigest {
		t.Fatal("map/order-dependent checkpoint bytes")
	}
	decoded, err := DecodeRecordV1(left.RecordBytes)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeRecordV1(decoded)
	if err != nil || string(encoded) != string(left.RecordBytes) {
		t.Fatalf("checkpoint round trip drift err=%v", err)
	}
}

func TestCheckpointRejectsCurrentBatchAndNonterminalWatermark(t *testing.T) {
	records := []controljournal.StoredEventV1{testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)}
	journal := testJournal(t, records...)
	watermark, _ := journalWatermark(journal, "")
	authority := testAuthority()
	authority.DurableRevision = watermark.ThroughRecordRevision
	base := BuildInputV1{CheckpointID: "checkpoint-1", Source: testSource(t), PriorPrefix: journal, EventRegistry: testEventRegistry(t), Reducers: testReducers(t), Watermark: watermark, Aggregates: []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, Authority: authority, FormingBatchOperation: "forming-operation"}
	current := base
	current.FormingBatchOperation = "operation-a"
	if _, err := BuildV1(current); err == nil {
		t.Fatal("current-batch checkpoint mutant survived")
	}
	nonterminal := base
	nonterminal.Watermark.ThroughEventID = "event-wrong"
	if _, err := BuildV1(nonterminal); err == nil {
		t.Fatal("invalid watermark mutant survived")
	}
	zero := base
	zero.PriorPrefix = controljournal.ValidatedJournalV1{}
	if _, err := BuildV1(zero); !errors.As(err, new(controljournal.InvalidValidatedJournalError)) {
		t.Fatalf("zero/manual journal error=%v", err)
	}
}

func TestCheckpointBuildRefusesFabricatedProjectionHeadAndAuthority(t *testing.T) {
	record := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	journal := testJournal(t, record)
	watermark, err := journalWatermark(journal, "")
	if err != nil {
		t.Fatal(err)
	}
	authority := testAuthority()
	authority.DurableRevision = watermark.ThroughRecordRevision
	base := BuildInputV1{CheckpointID: "checkpoint-proof", Source: testSource(t), PriorPrefix: journal, EventRegistry: testEventRegistry(t), Reducers: testReducers(t), Watermark: watermark, Aggregates: []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, Authority: authority, FormingBatchOperation: "forming-operation"}
	mutants := map[string]func(*BuildInputV1){
		"state": func(input *BuildInputV1) {
			input.Aggregates = []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":9}`)}
		},
		"logical head":   func(input *BuildInputV1) { input.Aggregates[0].LogicalVersion++ },
		"terminal event": func(input *BuildInputV1) { input.Aggregates[0].LastEventID = "event-fabricated" },
		"terminal epoch": func(input *BuildInputV1) { input.Aggregates[0].RuntimeEpochID = "epoch-fabricated" },
		"authority":      func(input *BuildInputV1) { input.Authority.FenceToken++ },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			input := base
			input.Aggregates = cloneAggregates(base.Aggregates)
			mutate(&input)
			if _, err := BuildV1(input); err == nil {
				t.Fatal("fabricated canonical checkpoint accepted")
			}
		})
	}
}

func TestCheckpointDueExactBoundaries(t *testing.T) {
	for _, test := range []struct {
		name         string
		below, exact SuffixMeasureV1
	}{
		{"commands", SuffixMeasureV1{CommittedCommands: 127}, SuffixMeasureV1{CommittedCommands: 128}},
		{"events", SuffixMeasureV1{CommittedEvents: 511}, SuffixMeasureV1{CommittedEvents: 512}},
		{"bytes", SuffixMeasureV1{CanonicalEventBytes: (4 << 20) - 1}, SuffixMeasureV1{CanonicalEventBytes: 4 << 20}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if CheckpointDueV1(test.below) || !CheckpointDueV1(test.exact) {
				t.Fatal("checkpoint cadence boundary drift")
			}
		})
	}
}

func TestCheckpointInvalidRecordMutantsRefuse(t *testing.T) {
	result := buildFixture(t, []controljournal.StoredEventV1{testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)}, []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, "checkpoint-1")
	mutants := [][]byte{
		append(append([]byte(nil), result.RecordBytes...), ' '),
		[]byte(`{"schema_version":2}`),
		append([]byte(nil), result.RecordBytes[:len(result.RecordBytes)-1]...),
		[]byte(strings.Replace(string(result.RecordBytes), `"event_count":1,`, "", 1)),
		[]byte(strings.Replace(string(result.RecordBytes), `"event_prefix_digest":"`, `"event_prefix_digest":"A`, 1)),
	}
	for index, mutant := range mutants {
		if _, err := DecodeRecordV1(mutant); err == nil {
			t.Fatalf("invalid-checkpoint mutant %d survived", index)
		}
	}
	head := result.Head
	head.CheckpointRecordDigest[0] ^= 1
	if head.CheckpointRecordDigest == result.Head.CheckpointRecordDigest {
		t.Fatal("digest mutant setup failed")
	}
}

func TestCheckpointCapabilityIsSealedAndCopyOnAccess(t *testing.T) {
	fixture := buildFixture(t,
		[]controljournal.StoredEventV1{testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)},
		[]AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, "checkpoint-capability")
	assertInvalid := func(t *testing.T, value ValidatedCheckpointV1) {
		t.Helper()
		if _, err := value.RecordV1(); !errors.As(err, new(InvalidValidatedCheckpointErrorV1)) {
			t.Fatalf("record accessor error=%T %v", err, err)
		}
		if _, err := value.HeadV1(); !errors.As(err, new(InvalidValidatedCheckpointErrorV1)) {
			t.Fatalf("head accessor error=%T %v", err, err)
		}
		if _, err := value.PublicationV1("forming-operation"); !errors.As(err, new(InvalidValidatedCheckpointErrorV1)) {
			t.Fatalf("publication accessor error=%T %v", err, err)
		}
	}
	assertInvalid(t, ValidatedCheckpointV1{})
	manual := ValidatedCheckpointV1{seal: checkpointValidationSealV1{version: 1, digest: [32]byte{1}, formingBatchOperation: "forming-operation"}, recordBytes: []byte(`{}`), headBytes: []byte(`{}`)}
	assertInvalid(t, manual)
	for name, mutate := range map[string]func(*ValidatedCheckpointV1){
		"seal":              func(value *ValidatedCheckpointV1) { value.seal.digest[0] ^= 1 },
		"forming operation": func(value *ValidatedCheckpointV1) { value.seal.formingBatchOperation = "different-operation" },
		"record":            func(value *ValidatedCheckpointV1) { value.recordBytes[0] ^= 1 },
		"head":              func(value *ValidatedCheckpointV1) { value.headBytes[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			value := fixture.Capability
			value.recordBytes = append([]byte(nil), value.recordBytes...)
			value.headBytes = append([]byte(nil), value.headBytes...)
			mutate(&value)
			assertInvalid(t, value)
		})
	}
	publication, err := fixture.Capability.PublicationV1("forming-operation")
	if err != nil {
		t.Fatal(err)
	}
	publication.RecordBytes[0] ^= 1
	publication.HeadBytes[0] ^= 1
	second, err := fixture.Capability.PublicationV1("forming-operation")
	if err != nil || string(second.RecordBytes) != string(fixture.RecordBytes) || string(second.HeadBytes) != string(fixture.HeadBytes) {
		t.Fatalf("publication mutation reached sealed checkpoint err=%v", err)
	}
	if second.FormingBatchOperationID != "forming-operation" {
		t.Fatalf("publication lost forming operation: %+v", second)
	}
	if _, err := fixture.Capability.PublicationV1("different-operation"); err == nil {
		t.Fatal("checkpoint built for operation A published into operation B")
	}
	record, err := fixture.Capability.RecordV1()
	if err != nil {
		t.Fatal(err)
	}
	record.Aggregates[0].State[0] ^= 1
	again, err := fixture.Capability.RecordV1()
	if err != nil || string(again.Aggregates[0].State) != `{"value":1}` {
		t.Fatalf("record mutation reached sealed checkpoint state=%s err=%v", again.Aggregates[0].State, err)
	}
}

func TestCheckpointBuildBindsActualEventAndReducerRegistries(t *testing.T) {
	record := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	journal := testJournal(t, record)
	watermark, err := journalWatermark(journal, "")
	if err != nil {
		t.Fatal(err)
	}
	authority := testAuthority()
	authority.DurableRevision = watermark.ThroughRecordRevision
	base := BuildInputV1{CheckpointID: "checkpoint-bindings", Source: testSource(t), PriorPrefix: journal, EventRegistry: testEventRegistry(t), Reducers: testReducers(t), Watermark: watermark, Aggregates: []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, Authority: authority, FormingBatchOperation: "forming-operation"}
	wrongEvents, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{
		{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"},
		{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongReducers, err := reducer.NewRegistryV1([]reducer.EntryV1{
		{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "mission", EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BuildInputV1){
		"event registry":       func(input *BuildInputV1) { input.EventRegistry = wrongEvents },
		"reducer registry":     func(input *BuildInputV1) { input.Reducers = wrongReducers },
		"event digest label":   func(input *BuildInputV1) { input.Source.EventSchemaSetDigest[0] ^= 1 },
		"reducer digest label": func(input *BuildInputV1) { input.Source.ReducerSetDigest[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if _, err := BuildV1(input); err == nil {
				t.Fatal("registry/source digest mismatch accepted")
			}
		})
	}
}

func FuzzCheckpointDecodeBounded(f *testing.F) {
	f.Add([]byte(`{"schema_version":1}`))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > MaxCheckpointBytes+1 {
			encoded = encoded[:MaxCheckpointBytes+1]
		}
		record, err := DecodeRecordV1(encoded)
		if err == nil {
			canonical, encodeErr := EncodeRecordV1(record)
			if encodeErr != nil || string(canonical) != string(encoded) {
				t.Fatalf("accepted noncanonical checkpoint err=%v", encodeErr)
			}
		}
	})
}
