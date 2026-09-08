package playback

import (
	"crypto/sha256"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func fixtureDigest(label string) journal.Digest {
	value := sha256.Sum256([]byte(label))
	return journal.Digest(value)
}
func fixtureReducers(t *testing.T) (journal.EventRegistryV2, journal.ReducerSetV2) {
	t.Helper()
	registry := journal.DefaultEventRegistryV2()
	registrations := make([]journal.ReducerRegistration, 0, len(journal.ExpectedEventTuples()))
	for _, tuple := range journal.ExpectedEventTuples() {
		reduce := journal.Reducer(func(_ []byte, event journal.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "operation" {
			reduce = journal.ReduceOperationStateV2
		}
		registrations = append(registrations, journal.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: fixtureDigest(tuple.Family), Reduce: reduce})
	}
	reducers, err := journal.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return registry, reducers
}
func publication(label string) journal.PublicationID {
	return journal.PublicationID(fixtureDigest("publication-" + label).String())
}
func playbackEvents(t *testing.T, count int) []MaterializedEventV2 {
	t.Helper()
	registry := journal.DefaultEventRegistryV2()
	coordinate, _ := journal.NewAggregateCoordinate("mission", "mission-1", journal.Scope{Kind: journal.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
	events := make([]MaterializedEventV2, count)
	for i := range events {
		payload := []byte(`{"value":` + strconv.Itoa(i+1) + `}`)
		envelope, err := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: journal.EventID("event-" + strconv.Itoa(i+1)), EventType: "mission.updated", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: journal.LogicalVersion(i + 1), OperationID: journal.OperationID("operation-" + strconv.Itoa(i+1)), OperationLogicalVersion: 1, PublicationID: publication(strconv.Itoa(i + 1)), CommittedRuntimeEpochID: "epoch-1", EventIndex: 0, EventCount: 1, Payload: payload, Observations: []byte(`{}`)}, registry)
		if err != nil {
			t.Fatal(err)
		}
		events[i], err = NewMaterializedEventV2(uint64(i+1), envelope, false)
		if err != nil {
			t.Fatal(err)
		}
	}
	return events
}

func TestPlaybackDeterministicPermutationAndMoreThan256ReducerInputs(t *testing.T) {
	registry, reducers := fixtureReducers(t)
	eventDescriptor, _ := registry.Descriptor()
	reducerDescriptor, _ := reducers.Descriptor()
	events := playbackEvents(t, 300)
	input := InputV2{Events: events, EventRegistry: registry, Reducers: reducers, ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest()}
	left, err := RunV2(input)
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	input.Events = events
	right, err := RunV2(input)
	if err != nil {
		t.Fatal(err)
	}
	if left.CanonicalProjectionDigest() != right.CanonicalProjectionDigest() || left.EventCount() != 300 || left.Validate() != nil || right.Validate() != nil {
		t.Fatal("playback was order- or session-bound")
	}
}

func TestPlaybackRejectsRegistryDriftAndForgedResult(t *testing.T) {
	registry, reducers := fixtureReducers(t)
	eventDescriptor, _ := registry.Descriptor()
	reducerDescriptor, _ := reducers.Descriptor()
	input := InputV2{Events: playbackEvents(t, 1), EventRegistry: registry, Reducers: reducers, ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest()}
	input.ExpectedReducerRegistryDigest = fixtureDigest("wrong")
	if _, err := RunV2(input); err == nil {
		t.Fatal("registry drift accepted")
	}
	if (ResultV2{}).Validate() == nil {
		t.Fatal("zero result accepted")
	}
}

func TestPlaybackProductionSurfaceHasStructurallyNoIO(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(file), "playback.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{`"bytes"`: true, `"crypto/sha256"`: true, `"encoding/json"`: true, `"sort"`: true, `"github.com/spec-kitty/go-kitty3/internal/controljournal/v2"`: true}
	for _, item := range parsed.Imports {
		if !allowed[item.Path.Value] {
			t.Fatalf("I/O-capable import %s", item.Path.Value)
		}
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Type.Params == nil {
			continue
		}
		for _, field := range function.Type.Params.List {
			if _, ok := field.Type.(*ast.InterfaceType); ok {
				t.Fatalf("function %s exposes interface capability", function.Name.Name)
			}
		}
	}
}

func TestQualificationFixture10000Operations30Runs(t *testing.T) {
	registry, reducers := fixtureReducers(t)
	eventDescriptor, _ := registry.Descriptor()
	reducerDescriptor, _ := reducers.Descriptor()
	base := make([]StateV2, 10_000)
	for i := range base {
		label := strconv.Itoa(i + 1)
		coordinate, _ := journal.NewAggregateCoordinate("operation", journal.AggregateID("operation-"+label), journal.Scope{Kind: journal.ScopeHost})
		operation, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: journal.OperationID("operation-" + label), LogicalVersion: 1, Status: journal.StatusAccepted, EventIDs: []journal.EventID{journal.EventID("event-op-" + label)}, LastPublicationID: publication("base-" + label)})
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := journal.EncodeOperationStateV2(operation)
		if err != nil {
			t.Fatal(err)
		}
		stateDigest, _ := journal.ReducerStateDigestV2(canonical)
		state, err := NewStateV2(coordinate, 1, journal.EventID("event-op-"+label), canonical, stateDigest, false)
		if err != nil {
			t.Fatal(err)
		}
		base[i] = state
	}
	// One operation crosses the checkpoint boundary and 300 distinct reducer
	// inputs create a mission scope that did not exist in the checkpoint.
	stagePublication := publication("stage-2")
	stageState, err := journal.NewOperationStateV2(journal.OperationStateInput{OperationID: "operation-1", LogicalVersion: 2, Status: journal.StatusInProgress, EventIDs: []journal.EventID{"event-op-1", "event-stage-2"}})
	if err != nil {
		t.Fatal(err)
	}
	stagePayload, err := journal.EncodeOperationStateEventPayloadV2(stageState)
	if err != nil {
		t.Fatal(err)
	}
	stageCoordinate, _ := journal.NewAggregateCoordinate("operation", "operation-1", journal.Scope{Kind: journal.ScopeHost})
	stageEnvelope, err := journal.NewEventEnvelopeV2(journal.EventEnvelopeInput{EventID: "event-stage-2", EventType: "operation.in_progress", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: stageCoordinate, AggregateLogicalVersion: 2, OperationID: "operation-1", OperationLogicalVersion: 2, PublicationID: stagePublication, CommittedRuntimeEpochID: "epoch-2", EventIndex: 0, EventCount: 1, Payload: stagePayload, Observations: []byte(`{}`)}, registry)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := NewMaterializedEventV2(10_001, stageEnvelope, false)
	if err != nil {
		t.Fatal(err)
	}
	events := append([]MaterializedEventV2{stage}, playbackEvents(t, 300)...)
	for i := 1; i < len(events); i++ {
		events[i].revision += 10_001
	}
	input := InputV2{Base: base, Events: events, EventRegistry: registry, Reducers: reducers, ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest()}
	var want journal.Digest
	durations := make([]time.Duration, 30)
	for run := 0; run < 30; run++ {
		started := time.Now()
		result, err := RunV2(input)
		durations[run] = time.Since(started)
		if err != nil {
			t.Fatal(err)
		}
		if run == 0 {
			want = result.CanonicalProjectionDigest()
		} else if result.CanonicalProjectionDigest() != want {
			t.Fatalf("run %d diverged", run)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[28]
	if p95 > 5*time.Second {
		t.Fatalf("gross CI p95 bound exceeded: %s", p95)
	}
	t.Logf("qualification fixture: operations=10000 reducer_inputs=301 runs=30 p95=%s (reference adoption threshold measured separately)", p95)
}
