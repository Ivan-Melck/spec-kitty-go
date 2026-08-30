package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
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
	watermark := fixtureWatermark(t, records)
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

func fixtureWatermark(t *testing.T, records []controljournal.StoredEventV1) WatermarkV1 {
	t.Helper()
	ordered := append([]controljournal.StoredEventV1(nil), records...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].RecordRevision != ordered[right].RecordRevision {
			return ordered[left].RecordRevision < ordered[right].RecordRevision
		}
		if ordered[left].Envelope.EventIndex != ordered[right].Envelope.EventIndex {
			return ordered[left].Envelope.EventIndex < ordered[right].Envelope.EventIndex
		}
		return ordered[left].Envelope.EventID < ordered[right].Envelope.EventID
	})
	digest := [32]byte{}
	for _, record := range ordered {
		encoded, err := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		if err != nil {
			t.Fatal(err)
		}
		var folded bytes.Buffer
		folded.Write(digest[:])
		var revision [8]byte
		binary.BigEndian.PutUint64(revision[:], record.RecordRevision)
		folded.Write(revision[:])
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(encoded)))
		folded.Write(size[:])
		folded.Write(encoded)
		digest = sha256.Sum256(folded.Bytes())
	}
	if len(ordered) == 0 {
		return WatermarkV1{}
	}
	last := ordered[len(ordered)-1]
	return WatermarkV1{ThroughRecordRevision: last.RecordRevision, ThroughEventID: last.Envelope.EventID, EventCount: uint64(len(ordered)), EventPrefixDigest: digest}
}

func TestCheckpointCanonicalBytesStableAcrossAggregateOrder(t *testing.T) {
	records := []controljournal.StoredEventV1{
		testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"zeta":2,"alpha":1}`),
		testStoredEvent(t, 8, "event-b", "operation-b", "aggregate-b", 1, 0, 1, `{"value":2}`),
	}
	a := snapshot(t, "aggregate-a", 1, "event-a", `{"zeta":2,"alpha":1}`)
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
	for _, wire := range []reflect.Type{
		reflect.TypeOf(checkpointWireV1{}), reflect.TypeOf(checkpointProjectionV1{}),
		reflect.TypeOf(aggregateWireV1{}), reflect.TypeOf(authorityWireV1{}),
		reflect.TypeOf(checkpointHeadWireV1{}),
	} {
		assertJSONFieldOrder(t, wire)
	}
	keys := checkpointObjectKeys(t, left.RecordBytes)
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(keys, sorted) {
		t.Fatalf("checkpoint encoding keys are not lexicographic: %v", keys)
	}
}

func TestCheckpointDecodeRejectsEveryNoncanonicalObjectSpelling(t *testing.T) {
	record := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"zeta":2,"alpha":1}`)
	fixture := buildFixture(t, []controljournal.StoredEventV1{record}, []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"zeta":2,"alpha":1}`)}, "checkpoint-canonical")
	var top map[string]json.RawMessage
	if err := json.Unmarshal(fixture.RecordBytes, &top); err != nil {
		t.Fatal(err)
	}
	topKeys := checkpointObjectKeys(t, fixture.RecordBytes)
	reversedTop := reverseStrings(topKeys)

	var projection map[string]json.RawMessage
	if err := json.Unmarshal(top["projection"], &projection); err != nil {
		t.Fatal(err)
	}
	projectionKeys := checkpointObjectKeys(t, top["projection"])
	projectionReordered := cloneRawMap(projection)
	topProjectionReordered := cloneRawMap(top)
	topProjectionReordered["projection"] = marshalCheckpointRawObject(t, projectionReordered, reverseStrings(projectionKeys))

	var aggregates []map[string]json.RawMessage
	if err := json.Unmarshal(projection["aggregates"], &aggregates); err != nil || len(aggregates) != 1 {
		t.Fatalf("aggregates=%d err=%v", len(aggregates), err)
	}
	stateKeys := checkpointObjectKeys(t, aggregates[0]["state"])
	stateReordered := cloneRawMap(aggregates[0])
	var state map[string]json.RawMessage
	if err := json.Unmarshal(stateReordered["state"], &state); err != nil {
		t.Fatal(err)
	}
	stateReordered["state"] = marshalCheckpointRawObject(t, state, reverseStrings(stateKeys))

	withAggregate := func(aggregate map[string]json.RawMessage) []byte {
		aggregateKeys := checkpointObjectKeys(t, mustMarshalJSON(t, aggregates[0]))
		projectionCopy := cloneRawMap(projection)
		projectionCopy["aggregates"] = mustMarshalJSON(t, []json.RawMessage{marshalCheckpointRawObject(t, aggregate, aggregateKeys)})
		topCopy := cloneRawMap(top)
		topCopy["projection"] = marshalCheckpointRawObject(t, projectionCopy, projectionKeys)
		return marshalCheckpointRawObject(t, topCopy, topKeys)
	}

	mutants := map[string][]byte{
		"top-level reordered":  marshalCheckpointRawObject(t, top, reversedTop),
		"top-level whitespace": append([]byte(" "), fixture.RecordBytes...),
		"top-level duplicate":  append([]byte(`{"canonical_projection_digest":"00",`), fixture.RecordBytes[1:]...),
		"trailing whitespace":  append(append([]byte(nil), fixture.RecordBytes...), ' '),
		"nested projection":    marshalCheckpointRawObject(t, topProjectionReordered, topKeys),
		"nested state order":   withAggregate(stateReordered),
		"nested state spacing": bytes.Replace(fixture.RecordBytes, []byte(`"alpha":1,"zeta":2`), []byte(`"alpha":1, "zeta":2`), 1),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(mutant, fixture.RecordBytes) {
				t.Fatal("canonical mutant setup did not change bytes")
			}
			if _, err := DecodeRecordV1(mutant); err == nil {
				t.Fatal("accepted noncanonical checkpoint bytes")
			}
		})
	}
}

func assertJSONFieldOrder(t *testing.T, wire reflect.Type) {
	t.Helper()
	fields := make([]string, wire.NumField())
	for index := range fields {
		fields[index] = wire.Field(index).Tag.Get("json")
	}
	sorted := append([]string(nil), fields...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(fields, sorted) {
		t.Fatalf("%s JSON fields are not lexicographic: %v", wire.Name(), fields)
	}
}

func checkpointObjectKeys(t *testing.T, encoded []byte) []string {
	t.Helper()
	var values map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(values))
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key.(string))
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func marshalCheckpointRawObject(t *testing.T, values map[string]json.RawMessage, order []string) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoded.WriteByte('{')
	for index, key := range order {
		if index > 0 {
			encoded.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(key)
		encoded.Write(keyBytes)
		encoded.WriteByte(':')
		encoded.Write(values[key])
	}
	encoded.WriteByte('}')
	return encoded.Bytes()
}

func cloneRawMap(value map[string]json.RawMessage) map[string]json.RawMessage {
	cloned := make(map[string]json.RawMessage, len(value))
	for key, raw := range value {
		cloned[key] = append([]byte(nil), raw...)
	}
	return cloned
}

func reverseStrings(value []string) []string {
	reversed := append([]string(nil), value...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
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
