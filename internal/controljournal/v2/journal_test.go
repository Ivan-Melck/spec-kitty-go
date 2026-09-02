package v2

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestOperationStateCompleteTransitionMatrixAndSetExtension(t *testing.T) {
	statuses := []OperationStatus{StatusAccepted, StatusInProgress, StatusResolved, StatusRefused, StatusAmbiguous, StatusReconciliationRequired}
	for _, from := range statuses {
		for _, to := range statuses {
			priorResult := []byte(nil)
			if from == StatusResolved || from == StatusRefused {
				priorResult = []byte(`{"terminal":true}`)
			}
			prior := fixtureState(t, from, 1, priorResult, []EventID{"event-z"}, []EffectID{"effect-z"})
			transition := OperationTransition{ExpectedVersion: 1, NextVersion: 2, Status: to, EventIDs: []EventID{"event-a", "event-z"}, EffectIDs: []EffectID{"effect-a", "effect-z"}}
			if to == StatusResolved || to == StatusRefused {
				transition.ResultKind, transition.ResultSchemaVersion, transition.CanonicalResult = "result", 1, []byte(`{"terminal":true}`)
			}
			_, err := AdvanceOperationState(prior, transition)
			if got, want := err == nil, allowedOperationTransition(from, to); got != want {
				t.Fatalf("transition %s->%s accepted=%t expected=%t err=%v", from, to, got, want, err)
			}
		}
	}
	prior := fixtureState(t, StatusInProgress, 1, nil, []EventID{"event-m", "event-z"}, []EffectID{"effect-m", "effect-z"})
	valid := []OperationTransition{
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-a", "event-m", "event-z"}, []EffectID{"effect-a", "effect-m", "effect-z"}, ""},
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-m", "event-n", "event-z"}, []EffectID{"effect-m", "effect-n", "effect-z"}, ""},
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-m", "event-z", "event-zz"}, []EffectID{"effect-m", "effect-z", "effect-zz"}, ""},
	}
	for _, transition := range valid {
		if _, err := AdvanceOperationState(prior, transition); err != nil {
			t.Fatalf("valid canonical set extension rejected: %+v err=%v", transition, err)
		}
	}
	invalid := []OperationTransition{
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-m"}, []EffectID{"effect-m", "effect-z"}, ""},
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-m", "event-z"}, []EffectID{"effect-m"}, ""},
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-m", "event-m", "event-z"}, []EffectID{"effect-m", "effect-z"}, ""},
		{1, 2, StatusInProgress, "", 0, nil, []EventID{"event-n", "event-z"}, []EffectID{"effect-m", "effect-z"}, ""},
	}
	for _, transition := range invalid {
		if _, err := AdvanceOperationState(prior, transition); err == nil {
			t.Fatalf("invalid membership accepted: %+v", transition)
		}
	}
}

func TestOperationBindingRFC3339CalendarBoundaries(t *testing.T) {
	for _, valid := range []string{"2024-02-29T23:59:59Z", "2026-04-30T00:00:00Z", "2000-02-29T12:00:00Z"} {
		input := fixtureBindingInput(t, "epoch-17")
		input.ObservedCreatedAt = valid
		if _, err := NewOperationBindingV2(input); err != nil {
			t.Fatalf("valid date rejected=%s err=%v", valid, err)
		}
	}
	for _, invalidDate := range []string{
		"2026-02-29T12:00:00Z", "2026-02-31T12:00:00Z", "2026-04-31T12:00:00Z", "1900-02-29T12:00:00Z",
		"2026-00-01T12:00:00Z", "2026-01-00T12:00:00Z", "2026-01-01T24:00:00Z",
		"2026-01-01T12:00:00+00:00", "2026-01-01T12:00:00.0Z", "2026-01-01T12:00:00z",
	} {
		input := fixtureBindingInput(t, "epoch-17")
		input.ObservedCreatedAt = invalidDate
		if _, err := NewOperationBindingV2(input); !IsKind(err, ErrorInvalidCanonicalData) {
			t.Fatalf("invalid date accepted=%s err=%v", invalidDate, err)
		}
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
	input.Events[0].Observations[0] = 'x'
	input.Events[0].GovernanceRevisions[0] = "changed"
	input.Heads[0].EventID = "changed"
	input.EffectIntents[0].EffectID = "changed"
	input.EligibilityGuards[0].GuardID = "changed"
	input.Checkpoints[0].Canonical[0] = 'x'
	input.Conditions[0].ConditionID = "changed"
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

func TestCommitPlanBindsCompleteOperationStateSemantics(t *testing.T) {
	registry := DefaultEventRegistryV2()
	base := fixturePlanInput(t)
	base.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved,
		ResultKind: "result.one", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
	baseline, err := SealCommitPlanV2(base, registry)
	if err != nil {
		t.Fatal(err)
	}
	mutants := []func(*CommitPlanInput){
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusInProgress, EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
		},
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved, ResultKind: "result.two", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
		},
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved, ResultKind: "result.one", ResultSchemaVersion: 2, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
		},
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved, ResultKind: "result.one", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":false}`), EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
		},
		func(v *CommitPlanInput) {
			v.Events[1].EventID, v.Heads[1].EventID = "event-mission-2", "event-mission-2"
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved, ResultKind: "result.one", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []EventID{"event-mission-2", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
		},
		func(v *CommitPlanInput) {
			v.EffectIntents[0].EffectID = "effect-02"
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved, ResultKind: "result.one", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-02"}})
		},
	}
	for index, mutate := range mutants {
		candidate := fixturePlanInput(t)
		candidate.OperationState = base.OperationState
		mutate(&candidate)
		sealed, err := SealCommitPlanV2(candidate, registry)
		if err != nil {
			t.Fatalf("semantic mutant=%d rejected rather than bound: %v", index, err)
		}
		if sealed.Digest() == baseline.Digest() || sealed.PublicationID() == baseline.PublicationID() {
			t.Fatalf("semantic mutant=%d did not change publication identity", index)
		}
	}
}

func TestCommitPlanExactFirstAndCumulativeLaterMembership(t *testing.T) {
	registry := DefaultEventRegistryV2()
	for _, mutate := range []func(*CommitPlanInput){
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted, EventIDs: []EventID{"event-ghost", "event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
		},
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted, EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01", "effect-ghost"}})
		},
		func(v *CommitPlanInput) {
			v.Events[0].Coordinate = AggregateCoordinate{Kind: "operation", ID: "operation-01", Scope: fixtureScope()}
			v.Heads[0].Coordinate = v.Events[0].Coordinate
		},
	} {
		candidate := fixturePlanInput(t)
		mutate(&candidate)
		if _, err := SealCommitPlanV2(candidate, registry); err == nil {
			t.Fatal("ghost or mission-scoped operation membership accepted")
		}
	}

	prior := mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted,
		EventIDs: []EventID{"event-z"}, EffectIDs: []EffectID{"effect-z"}, LastPublicationID: "publication-old"})
	next, err := AdvanceOperationState(prior, OperationTransition{1, 2, StatusInProgress, "", 0, nil,
		[]EventID{"event-a", "event-z"}, []EffectID{"effect-a", "effect-z"}, ""})
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	later := CommitPlanInput{PriorOperationState: &prior, OperationState: next,
		Events:        []EventDraftV2{{EventID: "event-a", EventType: "operation.progressed", Family: "operation", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: 2, CommittedRuntimeEpochID: "epoch-18", Payload: []byte(`{"status":"in_progress"}`)}},
		Heads:         []HeadTransitionV2{{Coordinate: coordinate, ExpectedLogicalVersion: 1, NextLogicalVersion: 2, EventID: "event-a", CanonicalStateDigest: digest("state", []byte("next"))}},
		EffectIntents: []EffectIntentReference{{EffectID: "effect-a", Digest: digest("effect", []byte("a"))}},
		Conditions:    []ProviderCondition{{ConditionID: "operation-head", Kind: ConditionRecordRevision, ObservedRevision: 7}},
	}
	if _, err := SealCommitPlanV2(later, registry); err != nil {
		t.Fatalf("valid cumulative later publication rejected: %v", err)
	}
	for _, mutate := range []func(*CommitPlanInput){
		func(v *CommitPlanInput) { v.PriorOperationState = nil },
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 2, Status: StatusInProgress, EventIDs: []EventID{"event-a", "event-ghost", "event-z"}, EffectIDs: []EffectID{"effect-a", "effect-z"}})
		},
		func(v *CommitPlanInput) {
			v.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 2, Status: StatusInProgress, EventIDs: []EventID{"event-a", "event-z"}, EffectIDs: []EffectID{"effect-a", "effect-ghost", "effect-z"}})
		},
	} {
		candidate := later
		mutate(&candidate)
		if _, err := SealCommitPlanV2(candidate, registry); err == nil {
			t.Fatal("unproven later membership accepted")
		}
	}
}

func TestCollectionBudgetBoundaries(t *testing.T) {
	events := make([]EventID, MaxMembers)
	for index := range events {
		events[index] = EventID(fmt.Sprintf("event-%03d", index))
	}
	if _, err := NewOperationStateV2(OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted, EventIDs: events}); err != nil {
		t.Fatalf("maximum membership rejected: %v", err)
	}
	events = append(events, "event-over")
	if _, err := NewOperationStateV2(OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted, EventIDs: events}); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("maximum-plus-one membership accepted: %v", err)
	}
	candidate := fixturePlanInput(t)
	for _, overBudget := range []func(*CommitPlanInput){
		func(v *CommitPlanInput) { v.Events = make([]EventDraftV2, MaxEventsPerPublication+1) },
		func(v *CommitPlanInput) { v.EffectIntents = make([]EffectIntentReference, MaxEffectsPerPublication+1) },
		func(v *CommitPlanInput) { v.EligibilityGuards = make([]EligibilityGuardTransition, MaxMembers+1) },
		func(v *CommitPlanInput) { v.Checkpoints = make([]CheckpointRecord, MaxCheckpointsPerPlan+1) },
		func(v *CommitPlanInput) { v.Conditions = make([]ProviderCondition, MaxConditionsPerPlan+1) },
	} {
		value := candidate
		overBudget(&value)
		if _, err := SealCommitPlanV2(value, DefaultEventRegistryV2()); !IsKind(err, ErrorBudgetExceeded) {
			t.Fatalf("maximum-plus-one collection accepted: %v", err)
		}
	}

	maximumEffects := fixturePlanInput(t)
	maximumEffects.EffectIntents = make([]EffectIntentReference, MaxEffectsPerPublication)
	effectIDs := make([]EffectID, MaxEffectsPerPublication)
	for index := range maximumEffects.EffectIntents {
		id := EffectID(fmt.Sprintf("effect-%03d", index))
		maximumEffects.EffectIntents[index] = EffectIntentReference{id, digest("effect", []byte(id))}
		effectIDs[index] = id
	}
	maximumEffects.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted,
		EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: effectIDs})
	if _, err := SealCommitPlanV2(maximumEffects, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("maximum effects rejected: %v", err)
	}

	maximumMetadata := fixturePlanInput(t)
	maximumMetadata.EligibilityGuards = make([]EligibilityGuardTransition, MaxMembers)
	for index := range maximumMetadata.EligibilityGuards {
		id := fmt.Sprintf("guard-%03d", index)
		maximumMetadata.EligibilityGuards[index] = EligibilityGuardTransition{id, digest("guard", []byte(id))}
	}
	maximumMetadata.Checkpoints = make([]CheckpointRecord, MaxCheckpointsPerPlan)
	for index := range maximumMetadata.Checkpoints {
		maximumMetadata.Checkpoints[index] = CheckpointRecord{fmt.Sprintf("checkpoint-%03d", index), []byte(fmt.Sprintf(`{"index":%d}`, index))}
	}
	maximumMetadata.Conditions = make([]ProviderCondition, MaxConditionsPerPlan)
	for index := range maximumMetadata.Conditions {
		maximumMetadata.Conditions[index] = ProviderCondition{fmt.Sprintf("condition-%03d", index), ConditionAbsent, 0}
	}
	if _, err := SealCommitPlanV2(maximumMetadata, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("maximum metadata collections rejected: %v", err)
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

func mustState(t *testing.T, input OperationStateInput) OperationStateV2 {
	t.Helper()
	state, err := NewOperationStateV2(input)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func fixturePlanInput(t *testing.T) CommitPlanInput {
	t.Helper()
	binding := fixtureBinding(t, "epoch-17")
	state := fixtureState(t, StatusAccepted, 1, nil, []EventID{"event-mission", "event-operation"}, []EffectID{"effect-01"})
	missionCoordinate, _ := NewAggregateCoordinate("mission", "mission", fixtureScope())
	operationCoordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	events := []EventDraftV2{
		{EventID: "event-operation", EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", GovernanceRevisions: []string{"gov-01"}, Observations: []byte(`{"source":"test"}`), Payload: []byte(`{"status":"accepted"}`)},
		{EventID: "event-mission", EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", Payload: []byte(`{"status":"planned"}`)},
	}
	heads := []HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: "event-operation", CanonicalStateDigest: digest("state", []byte("operation"))},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: "event-mission", CanonicalStateDigest: digest("state", []byte("mission"))},
	}
	return CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads,
		EffectIntents:     []EffectIntentReference{{EffectID: "effect-01", Digest: digest("effect", []byte("one"))}},
		EligibilityGuards: []EligibilityGuardTransition{{GuardID: "guard-01", Digest: digest("guard", []byte("one"))}},
		Checkpoints:       []CheckpointRecord{{CheckpointID: "checkpoint-01", Canonical: []byte(`{"head":1}`)}},
		Conditions:        []ProviderCondition{{"operation-head", ConditionAbsent, 0}, {"mission-head", ConditionAbsent, 0}}}
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
