package v2

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestIdentityScopeCodecAndKeyBudgets(t *testing.T) {
	constructors := []func(string) error{
		func(v string) error { _, err := NewHostAuthorityID(v); return err },
		func(v string) error { _, err := NewProjectID(v); return err },
		func(v string) error { _, err := NewMissionID(v); return err },
		func(v string) error { _, err := NewRepositoryID(v); return err },
		func(v string) error { _, err := NewWorkRevisionID(v); return err },
		func(v string) error { _, err := NewWorkID(v); return err },
		func(v string) error { _, err := NewOperationID(v); return err },
		func(v string) error { _, err := NewRuntimeEpochID(v); return err },
		func(v string) error { _, err := NewEventID(v); return err },
		func(v string) error { _, err := NewAggregateKind(v); return err },
		func(v string) error { _, err := NewAggregateID(v); return err },
	}
	for _, constructor := range constructors {
		if err := constructor("valid-01"); err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"", "/", "é", " leading", strings.Repeat("x", MaxIdentityBytes+1)} {
			if err := constructor(bad); !IsKind(err, ErrorInvalidIdentity) {
				t.Fatalf("identity=%q observed=%v", bad, err)
			}
		}
	}

	scopes := []Scope{
		{Kind: ScopeHost},
		{Kind: ScopeProject, ProjectID: "project"},
		{Kind: ScopeMission, ProjectID: "project", MissionID: "mission"},
		{Kind: ScopeRepository, ProjectID: "project", RepositoryID: "repo"},
		{Kind: ScopeWorkRevision, ProjectID: "project", MissionID: "mission", WorkRevisionID: "revision"},
		{Kind: ScopeWork, ProjectID: "project", MissionID: "mission", WorkRevisionID: "revision", WorkID: "work"},
	}
	seenScopes := make(map[string]bool)
	for _, scope := range scopes {
		canonical, err := scope.CanonicalComponent()
		if err != nil || seenScopes[canonical] {
			t.Fatalf("scope=%+v canonical=%q err=%v", scope, canonical, err)
		}
		seenScopes[canonical] = true
	}
	for _, ambiguous := range []Scope{
		{Kind: ScopeHost, MissionID: "mission"},
		{Kind: ScopeMission, MissionID: "mission"},
		{Kind: ScopeRepository, ProjectID: "project", MissionID: "mission", RepositoryID: "repo"},
		{Kind: "unknown"},
	} {
		if err := ambiguous.Validate(); !IsKind(err, ErrorInvalidScope) {
			t.Fatalf("ambiguous scope accepted: %+v err=%v", ambiguous, err)
		}
	}

	seen := make(map[string]string)
	for index := 1; index <= 512; index++ {
		value := "id-" + strings.Repeat("x", index%31) + string(rune('A'+index%26))
		encoded, err := EncodeKeyComponent(value)
		if err != nil || strings.Contains(encoded, "/") {
			t.Fatalf("component=%q encoded=%q err=%v", value, encoded, err)
		}
		if prior := seen[encoded]; prior != "" && prior != value {
			t.Fatalf("codec collision %q %q", prior, value)
		}
		seen[encoded] = value
		decoded, err := DecodeKeyComponent(encoded)
		if err != nil || decoded != value {
			t.Fatalf("roundtrip=%q/%q err=%v", value, decoded, err)
		}
	}
	for _, alternate := range []string{"raw", "k2F", "k2f2f", "k", "ké0"} {
		if _, err := DecodeKeyComponent(alternate); !IsKind(err, ErrorInvalidKeyComponent) {
			t.Fatalf("alternate component accepted: %q", alternate)
		}
	}
	version, _ := NewLogicalVersion(42)
	hex16, _ := version.Hex16()
	if hex16 != "000000000000002a" {
		t.Fatalf("version=%s", hex16)
	}
	if parsed, err := ParseLogicalVersion(hex16); err != nil || parsed != version {
		t.Fatalf("version roundtrip=%d err=%v", parsed, err)
	}
	for _, malformed := range []string{"", "0", "0000000000000000", "000000000000002A", "00000000000000001"} {
		if _, err := ParseLogicalVersion(malformed); !IsKind(err, ErrorInvalidVersion) {
			t.Fatalf("version mutant accepted=%q", malformed)
		}
	}
	left, _ := CanonicalObject([]byte(`{"z":1,"a":{"y":2,"x":3}}`))
	right, _ := CanonicalObject([]byte(` { "a" : { "x":3, "y":2 }, "z":1 } `))
	if !bytes.Equal(left, right) {
		t.Fatalf("canonical map order differs: %s != %s", left, right)
	}
	if _, err := CanonicalObject([]byte(`{"a":null,"a":null}`)); !IsKind(err, ErrorInvalidCanonicalData) {
		t.Fatal("duplicate null key accepted")
	}
	payload := strings.Repeat("secret", MaxCanonicalPayloadBytes)
	_, err := CanonicalObject([]byte(payload))
	if err == nil || len(err.Error()) > 128 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("diagnostic is unbounded or leaked payload: %v", err)
	}
}

func TestOperationBindingCollisionIntentAndGlobalKey(t *testing.T) {
	binding := fixtureBinding(t, "epoch-17")
	encoded, err := EncodeOperationBindingV2(binding)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOperationBindingV2(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if resolution, err := ResolveOperationBinding(decoded, encoded, true); err != nil || resolution != BindingExact {
		t.Fatalf("exact resolution=%s err=%v", resolution, err)
	}
	key, _ := OperationBindingKey(binding.OperationID())
	if !strings.HasPrefix(key, "spk/v2/operations/") || strings.Contains(key, "mission") || strings.Contains(key, "project") {
		t.Fatalf("operation key is not host-global: %s", key)
	}

	base := fixtureBindingInput(t, "epoch-17")
	mutants := []func(*OperationBindingInput){
		func(v *OperationBindingInput) { v.CanonicalIntentDigest[0]++ },
		func(v *OperationBindingInput) { v.CommandKind = "mission.other" },
		func(v *OperationBindingInput) { v.CommandSchemaVersion++ },
		func(v *OperationBindingInput) { v.Scope.MissionID = "other" },
		func(v *OperationBindingInput) { v.ActorID = "other" },
		func(v *OperationBindingInput) { v.ProcessID = "other" },
		func(v *OperationBindingInput) { v.DoctrineRevision = "other" },
		func(v *OperationBindingInput) { v.ProfileRevision = "other" },
		func(v *OperationBindingInput) { v.PolicyRevision = "other" },
		func(v *OperationBindingInput) { v.OriginatingRuntimeEpochID = "epoch-18" },
		func(v *OperationBindingInput) { v.ObservedCreatedAt = "2026-09-02T12:00:01Z" },
	}
	for index, mutate := range mutants {
		candidate := base
		mutate(&candidate)
		other, err := NewOperationBindingV2(candidate)
		if err != nil {
			t.Fatalf("mutant=%d invalid fixture: %v", index, err)
		}
		resolution, err := ResolveOperationBinding(other, encoded, true)
		if resolution != BindingConflict || !IsKind(err, ErrorIdempotencyConflict) {
			t.Fatalf("mutant=%d resolution=%s err=%v", index, resolution, err)
		}
	}
	if resolution, err := ResolveOperationBinding(binding, []byte(`{"schema_version":99}`), true); resolution != BindingMalformed || !IsKind(err, ErrorJournalCorrupt) {
		t.Fatalf("malformed resolution=%s err=%v", resolution, err)
	}
}

func TestStableIntentExcludesEpochWhileBindingRetainsIt(t *testing.T) {
	scope := fixtureScope()
	intent := StableIntentInput{"mission.advance", 1, scope, []byte(`{"mission":"mission","target":"planned"}`)}
	first, err := CanonicalIntentDigest(intent)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := CanonicalIntentDigest(intent)
	if first != second {
		t.Fatal("stable intent digest changed")
	}
	left := fixtureBinding(t, "epoch-17")
	right := fixtureBinding(t, "epoch-18")
	if left.IntentDigest() != right.IntentDigest() || left.OriginatingEpoch() == right.OriginatingEpoch() {
		t.Fatal("intent/epoch boundary collapsed")
	}
}

func TestOperationStateTransitionTerminalAndAmbiguousLaws(t *testing.T) {
	accepted := fixtureState(t, StatusAccepted, 1, nil, []EventID{"event-01"}, nil)
	inProgress, err := AdvanceOperationState(accepted, OperationTransition{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-01", "event-02"}, nil, ""})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := AdvanceOperationState(inProgress, OperationTransition{2, 3, StatusResolved, "mission.result", 1, []byte(`{"ok":true}`), []EventID{"event-01", "event-02"}, nil, ""})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeOperationStateV2(resolved)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeOperationStateV2(encoded)
	if err != nil || !bytes.Equal(decoded.Result(), resolved.Result()) {
		t.Fatalf("state roundtrip err=%v", err)
	}
	if _, err := AdvanceOperationState(resolved, OperationTransition{3, 4, StatusResolved, "mission.result", 1, []byte(`{"ok":false}`), []EventID{"event-01", "event-02"}, nil, ""}); !IsKind(err, ErrorInvalidTransition) {
		t.Fatal("terminal result mutation accepted")
	}
	for _, mutation := range []OperationTransition{
		{1, 1, StatusInProgress, "", 0, nil, []EventID{"event-01"}, nil, ""},
		{1, 3, StatusInProgress, "", 0, nil, []EventID{"event-01"}, nil, ""},
		{1, 2, StatusInProgress, "", 0, nil, nil, nil, ""},
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-01", "event-01"}, nil, ""},
	} {
		if _, err := AdvanceOperationState(accepted, mutation); err == nil {
			t.Fatalf("transition mutant accepted: %+v", mutation)
		}
	}
	ambiguous, err := AdvanceOperationState(accepted, OperationTransition{1, 2, StatusAmbiguous, "", 0, nil, []EventID{"event-01"}, nil, ""})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AdvanceOperationState(ambiguous, OperationTransition{2, 3, StatusResolved, "result", 1, []byte(`{"ok":true}`), []EventID{"event-01"}, nil, ""}); !IsKind(err, ErrorInvalidTransition) {
		t.Fatal("ambiguous state bypassed reconciliation")
	}
	if _, err := AdvanceOperationState(ambiguous, OperationTransition{2, 3, StatusReconciliationRequired, "", 0, nil, []EventID{"event-01"}, nil, ""}); err != nil {
		t.Fatal(err)
	}
	refusal := fixtureState(t, StatusRefused, 1, []byte(`{"kind":"denied"}`), []EventID{"event-refusal"}, nil)
	if !bytes.Equal(refusal.Result(), []byte(`{"kind":"denied"}`)) {
		t.Fatal("semantic refusal was not exact")
	}
}

func TestReplayBeforeEpochValidation(t *testing.T) {
	binding := fixtureBinding(t, "epoch-17")
	encoded, _ := EncodeOperationBindingV2(binding)
	state := fixtureState(t, StatusResolved, 1, []byte(`{"outcome":"done"}`), []EventID{"event-01"}, nil)
	resolution, err := ResolveRetryV2(binding, encoded, true, &state, "epoch-18")
	if err != nil || resolution.Decision != RetryReplay || !bytes.Equal(resolution.Result, state.Result()) {
		t.Fatalf("existing result did not replay after epoch rotation: %+v err=%v", resolution, err)
	}
	resolution, err = ResolveRetryV2(binding, nil, false, nil, "epoch-18")
	if resolution.Decision != RetryRefuseEpoch || !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("absent predecessor request admitted: %+v err=%v", resolution, err)
	}
	conflict := fixtureBinding(t, "epoch-18")
	resolution, err = ResolveRetryV2(conflict, encoded, true, nil, "epoch-18")
	if resolution.Decision != RetryConflict || !IsKind(err, ErrorIdempotencyConflict) {
		t.Fatalf("collision did not precede epoch admission: %+v err=%v", resolution, err)
	}
}

func TestCommitPlanPublicationSealMembershipAndImmutability(t *testing.T) {
	registry := DefaultEventRegistryV2()
	input := fixturePlanInput(t)
	first, err := SealCommitPlanV2(input, registry)
	if err != nil {
		t.Fatal(err)
	}
	reversed := fixturePlanInput(t)
	reversed.Events[0], reversed.Events[1] = reversed.Events[1], reversed.Events[0]
	reversed.Heads[0], reversed.Heads[1] = reversed.Heads[1], reversed.Heads[0]
	second, err := SealCommitPlanV2(reversed, registry)
	if err != nil || first.PublicationID() != second.PublicationID() || first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatalf("plan order changed identity: err=%v", err)
	}
	before := first.CanonicalBytes()
	input.Events[0].Payload[0] = 'x'
	input.Heads[0].EventID = "changed"
	if !bytes.Equal(before, first.CanonicalBytes()) {
		t.Fatal("sealed plan aliases caller input")
	}
	for _, mutate := range []func(*CommitPlanInput){
		func(v *CommitPlanInput) { v.Heads = v.Heads[1:] },
		func(v *CommitPlanInput) { v.Events[1].Coordinate = v.Events[0].Coordinate },
		func(v *CommitPlanInput) { v.Events[1].EventID = v.Events[0].EventID },
		func(v *CommitPlanInput) { v.OperationBinding = nil },
		func(v *CommitPlanInput) { v.Heads[0].NextLogicalVersion++ },
	} {
		candidate := fixturePlanInput(t)
		mutate(&candidate)
		if _, err := SealCommitPlanV2(candidate, registry); err == nil {
			t.Fatal("commit plan mutant accepted")
		}
	}
	for _, event := range first.Events() {
		encoded, err := EncodeEventEnvelopeV2(event)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := DecodeEventEnvelopeV2(encoded, registry)
		if err != nil || decoded.CanonicalDigest() != event.CanonicalDigest() {
			t.Fatalf("event roundtrip err=%v", err)
		}
	}
}

func TestEventRegistryReducerPlaybackAndDigest(t *testing.T) {
	registry := DefaultEventRegistryV2()
	for _, tuple := range ExpectedEventTuples() {
		if got, err := registry.Resolve(tuple.Family, tuple.EventSchemaVersion, tuple.ReducerVersion); err != nil || got != tuple {
			t.Fatalf("tuple=%+v got=%+v err=%v", tuple, got, err)
		}
	}
	for _, mutate := range []func([]EventTuple) []EventTuple{
		func(v []EventTuple) []EventTuple { return v[1:] },
		func(v []EventTuple) []EventTuple { return append(v, v[0]) },
		func(v []EventTuple) []EventTuple { v[0].Owner = "internal/other"; return v },
		func(v []EventTuple) []EventTuple { v[0].ReducerVersion++; return v },
	} {
		values := append([]EventTuple(nil), ExpectedEventTuples()...)
		if _, err := NewEventRegistryV2(mutate(values)); !IsKind(err, ErrorUnknownEventTuple) {
			t.Fatal("registry mutant accepted")
		}
	}
	reducers, err := NewReducerSetV2(registry, []ReducerRegistration{{"mission", 1, 1, func(_, event []byte) ([]byte, error) { return append([]byte(nil), event...), nil }}})
	if err != nil {
		t.Fatal(err)
	}
	result, digest, err := reducers.Reduce("mission", 1, 1, []byte(`{"prior":1}`), []byte(`{"state":"ready"}`))
	if err != nil || digest.IsZero() || !bytes.Equal(result, []byte(`{"state":"ready"}`)) {
		t.Fatalf("reducer result=%s digest=%s err=%v", result, digest, err)
	}
	if _, _, err := reducers.Reduce("mission", 2, 1, []byte(`{}`), []byte(`{}`)); !IsKind(err, ErrorUnknownEventTuple) {
		t.Fatal("unknown event tuple fell back")
	}
	toggle := false
	nondeterministic, _ := NewReducerSetV2(registry, []ReducerRegistration{{"mission", 1, 1, func(_, _ []byte) ([]byte, error) {
		toggle = !toggle
		if toggle {
			return []byte(`{"n":1}`), nil
		}
		return []byte(`{"n":2}`), nil
	}}})
	if _, _, err := nondeterministic.Reduce("mission", 1, 1, []byte(`{}`), []byte(`{}`)); !IsKind(err, ErrorNondeterministic) {
		t.Fatal("nondeterministic reducer accepted")
	}
}

func TestPureDecisionsAreRaceSafeAndIdempotent(t *testing.T) {
	binding := fixtureBinding(t, "epoch-17")
	encoded, _ := EncodeOperationBindingV2(binding)
	state := fixtureState(t, StatusResolved, 1, []byte(`{"done":true}`), []EventID{"event-01"}, nil)
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for trial := 0; trial < 100; trial++ {
				resolution, err := ResolveRetryV2(binding, encoded, true, &state, "epoch-99")
				if err != nil || resolution.Decision != RetryReplay || !bytes.Equal(resolution.Result, state.Result()) {
					t.Errorf("concurrent resolution=%+v err=%v", resolution, err)
					return
				}
			}
		}()
	}
	wait.Wait()
}

func fixtureScope() Scope {
	return Scope{Kind: ScopeMission, ProjectID: "project", MissionID: "mission"}
}

func fixtureBindingInput(t *testing.T, epoch RuntimeEpochID) OperationBindingInput {
	t.Helper()
	intent, err := CanonicalIntentDigest(StableIntentInput{"mission.advance", 1, fixtureScope(), []byte(`{"mission":"mission","target":"planned"}`)})
	if err != nil {
		t.Fatal(err)
	}
	return OperationBindingInput{"operation-01", "mission.advance", 1, intent, fixtureScope(), "actor-01", "process-01", "doctrine-01", "profile-01", "policy-01", epoch, "2026-09-02T12:00:00Z"}
}

func fixtureBinding(t *testing.T, epoch RuntimeEpochID) OperationBindingV2 {
	t.Helper()
	binding, err := NewOperationBindingV2(fixtureBindingInput(t, epoch))
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func fixtureState(t *testing.T, status OperationStatus, version LogicalVersion, result []byte, events []EventID, effects []EffectID) OperationStateV2 {
	t.Helper()
	input := OperationStateInput{OperationID: "operation-01", LogicalVersion: version, Status: status, EventIDs: events, EffectIDs: effects}
	if status == StatusResolved || status == StatusRefused {
		input.ResultKind, input.ResultSchemaVersion, input.CanonicalResult = "mission.result", 1, result
	}
	state, err := NewOperationStateV2(input)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func fixturePlanInput(t *testing.T) CommitPlanInput {
	t.Helper()
	binding := fixtureBinding(t, "epoch-17")
	state := fixtureState(t, StatusAccepted, 1, nil, []EventID{"event-mission", "event-operation"}, nil)
	missionCoordinate, _ := NewAggregateCoordinate("mission", "mission", fixtureScope())
	operationCoordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	events := []EventDraftV2{
		{EventID: "event-operation", EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", Payload: []byte(`{"status":"accepted"}`)},
		{EventID: "event-mission", EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", Payload: []byte(`{"status":"planned"}`)},
	}
	heads := []HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: "event-operation", CanonicalStateDigest: digest("state", []byte("operation"))},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: "event-mission", CanonicalStateDigest: digest("state", []byte("mission"))},
	}
	return CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads, Conditions: []ProviderCondition{{"operation-head", ConditionAbsent, 0}, {"mission-head", ConditionAbsent, 0}}}
}

func TestCanonicalJSONPropertyCorpus(t *testing.T) {
	for index := 0; index < 1000; index++ {
		left := map[string]any{"b": index, "a": []any{index % 3, "value"}}
		encoded, _ := json.Marshal(left)
		canonical, err := CanonicalObject(encoded)
		if err != nil {
			t.Fatal(err)
		}
		again, err := CanonicalObject(canonical)
		if err != nil || !bytes.Equal(canonical, again) {
			t.Fatalf("canonical instability at %d", index)
		}
	}
}
