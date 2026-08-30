package playback

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"runtime"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

func playbackRegistry(t *testing.T, mutate bool) reducer.RegistryV1 {
	t.Helper()
	registry, err := reducer.NewRegistryV1([]reducer.EntryV1{{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Reduce: func(_ reducer.StateV1, event controljournal.EventEnvelopeV1) (reducer.StateV1, error) {
		if mutate {
			return reducer.NewStateV1([]byte(`{"mutated":true}`))
		}
		return reducer.NewStateV1(event.Payload())
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func playbackJournal(t *testing.T, count int) controljournal.ValidatedJournalV1 {
	t.Helper()
	versions, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	records := make([]controljournal.StoredEventV1, count)
	for index := range records {
		eventIndex, _ := controljournal.NewEventIndexV1(0)
		eventCount, _ := controljournal.NewEventCountV1(1)
		envelope, envelopeErr := controljournal.NewEventEnvelopeV1(controljournal.EventEnvelopeInputV1{EnvelopeVersion: 1, EventID: controljournal.EventID("event-" + decimal(index+1)), EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", ProjectID: "project-1", AggregateKind: "mission", AggregateID: "aggregate-1", AggregateLogicalVersion: uint64(index + 1), OperationID: controljournal.OperationID("operation-" + decimal(index+1)), RuntimeEpochID: "epoch-1", EventIndex: eventIndex, EventCount: eventCount, Payload: []byte(`{"value":` + decimal(index+1) + `}`)})
		if envelopeErr != nil {
			t.Fatal(envelopeErr)
		}
		records[index] = controljournal.StoredEventV1{RecordRevision: uint64(index + 1), Envelope: envelope}
	}
	journal, err := controljournal.ValidateJournalV1(versions, records)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func decimal(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	position := len(digits)
	for value > 0 {
		position--
		digits[position] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[position:])
}

func TestPlaybackFullHistoryIsDeterministicAndZeroIO(t *testing.T) {
	for count := 1; count <= 100; count++ {
		journal := playbackJournal(t, count)
		left, err := RunV1(InputV1{Journal: journal, Reducers: playbackRegistry(t, false)})
		if err != nil {
			t.Fatal(err)
		}
		right, err := RunV1(InputV1{Journal: journal, Reducers: playbackRegistry(t, false)})
		if err != nil {
			t.Fatal(err)
		}
		if left.CanonicalProjectionDigest != right.CanonicalProjectionDigest || left.EventCount != uint64(count) || left.Observation.AdapterCalls != 0 || left.Observation.DurableWrites != 0 {
			t.Fatalf("count=%d left=%+v right=%+v", count, left, right)
		}
		if err := CompareDigestV1(left.CanonicalProjectionDigest, right); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaybackReducerMutationCausesDigestMismatch(t *testing.T) {
	journal := playbackJournal(t, 3)
	restored, err := RunV1(InputV1{Journal: journal, Reducers: playbackRegistry(t, false)})
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := RunV1(InputV1{Journal: journal, Reducers: playbackRegistry(t, true)})
	if err != nil {
		t.Fatal(err)
	}
	if err := CompareDigestV1(restored.CanonicalProjectionDigest, mutated); err == nil {
		t.Fatal("playback reducer/order mutant escaped digest comparison")
	}
}

func TestPlaybackRejectsZeroManualJournalBeforeReduction(t *testing.T) {
	registry := playbackRegistry(t, false)
	for _, journal := range []controljournal.ValidatedJournalV1{{}, *new(controljournal.ValidatedJournalV1)} {
		if _, err := RunV1(InputV1{Journal: journal, Reducers: registry}); !reflect.TypeOf(err).AssignableTo(reflect.TypeOf(controljournal.InvalidValidatedJournalError{})) {
			t.Fatalf("zero/manual journal error=%T %v", err, err)
		}
	}
	valid := playbackJournal(t, 2)
	copyOfCapability := valid
	if _, err := RunV1(InputV1{Journal: copyOfCapability, Reducers: registry}); err != nil {
		t.Fatalf("ordinary immutable capability copy refused: %v", err)
	}
}

func TestPlaybackProductionImportsAndInputExposeNoSideEffectCapability(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller unavailable")
	}
	production := current[:len(current)-len("playback_test.go")] + "playback.go"
	parsed, err := parser.ParseFile(token.NewFileSet(), production, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"\"github.com/spec-kitty/go-kitty3/internal/controljournal\"": true, "\"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer\"": true}
	for _, spec := range parsed.Imports {
		if !allowed[spec.Path.Value] {
			t.Fatalf("playback side-effect import escaped guard: %s", spec.Path.Value)
		}
	}
	inputType := reflect.TypeOf(InputV1{})
	if inputType.NumField() != 2 || inputType.Field(0).Type != reflect.TypeOf(controljournal.ValidatedJournalV1{}) || inputType.Field(1).Type != reflect.TypeOf(reducer.RegistryV1{}) {
		t.Fatalf("playback input gained a capability: %+v", inputType)
	}
	for _, declaration := range parsed.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Recv == nil && function.Type.Params != nil {
			for _, field := range function.Type.Params.List {
				if _, ok := field.Type.(*ast.InterfaceType); ok {
					t.Fatalf("playback function %s gained an interface capability", function.Name.Name)
				}
			}
		}
	}
}
