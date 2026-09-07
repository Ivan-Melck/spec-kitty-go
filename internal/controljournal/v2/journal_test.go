package v2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
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
		func(v string) error { _, err := NewEffectID(v); return err },
		func(v string) error { _, err := NewEligibilityShardID(v); return err },
		func(v string) error { _, err := NewCheckpointID(v); return err },
		func(v string) error { _, err := NewCheckpointSlotID(v); return err },
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

func TestIdentityASCIIAllowlistSubsumesUTF8Validation(t *testing.T) {
	reference := func(value string) bool {
		if len(value) == 0 || len(value) > MaxIdentityBytes || !utf8.ValidString(value) {
			return false
		}
		for index := range len(value) {
			char := value[index]
			ok := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
			if index > 0 {
				ok = ok || char == '.' || char == '_' || char == ':' || char == '-'
			}
			if !ok {
				return false
			}
		}
		return true
	}
	assertEquivalent := func(value string) {
		t.Helper()
		got := validateIdentity("identity", value) == nil
		if got != reference(value) {
			t.Fatalf("identity byte-class equivalence differs for %x: got=%t", []byte(value), got)
		}
	}
	assertEquivalent("")
	for first := 0; first <= 0xff; first++ {
		assertEquivalent(string([]byte{byte(first)}))
		for second := 0; second <= 0xff; second++ {
			assertEquivalent(string([]byte{byte(first), byte(second)}))
		}
	}
	for _, value := range []string{
		strings.Repeat("a", MaxIdentityBytes),
		strings.Repeat("a", MaxIdentityBytes+1),
		string([]byte{0xc0, 0x80}),
		string([]byte{0xed, 0xa0, 0x80}),
		string([]byte{0xf4, 0x90, 0x80, 0x80}),
		"a" + string([]byte{0xff}) + "b",
	} {
		assertEquivalent(value)
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
	bindingAddress, _ := NewOperationBindingAddressV2(binding.OperationID())
	bindingRecord, _ := NewRecoveryRecordV2(bindingAddress, encoded, 1, "")
	trustedBinding, err := DecodeOperationBindingRecordV2(bindingRecord)
	if err != nil || trustedBinding.OperationID() != binding.OperationID() || trustedBinding.IntentDigest() != binding.IntentDigest() {
		t.Fatalf("trusted binding decode=%+v err=%v", trustedBinding, err)
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
	resolved, err := AdvanceOperationState(inProgress, OperationTransition{2, 3, StatusResolved, "mission.result", 1, []byte(`{"ok":true}`), []EventID{"event-01", "event-02"}, nil, testPublicationID("resolved")})
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
	stateAddress, _ := NewOperationHeadAddressV2(resolved.OperationID())
	stateRecord, _ := NewRecoveryRecordV2(stateAddress, encoded, 1, "")
	trustedState, err := DecodeOperationStateRecordV2(stateRecord)
	if err != nil || !EqualOperationStatesV2(trustedState, resolved) {
		t.Fatalf("trusted state decode=%+v err=%v", trustedState, err)
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

func TestOperationReconciliationResumeRequiresFreshBoundEffectPermission(t *testing.T) {
	prior := fixtureState(t, StatusReconciliationRequired, 7, nil,
		[]EventID{"event-observation", "event-reconcile"}, []EffectID{"effect-bound"})
	transition := OperationTransition{
		ExpectedVersion: 7,
		NextVersion:     8,
		Status:          StatusInProgress,
		EventIDs:        []EventID{"event-observation", "event-permission", "event-reconcile"},
		EffectIDs:       []EffectID{"effect-bound"},
	}
	permission := fixtureEffectPermission(t, "event-permission", "effect-bound")

	if _, err := AdvanceOperationState(prior, transition); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("generic transition admitted reconciliation resume: %v", err)
	}
	resumed, err := ResumeOperationStateWithEffectPermissionV2(prior, transition, permission)
	if err != nil {
		t.Fatalf("permission-bound reconciliation resume: %v", err)
	}
	if resumed.Status() != StatusInProgress || resumed.LogicalVersion() != 8 || !equalIDs(resumed.EventIDs(), transition.EventIDs) || !equalIDs(resumed.EffectIDs(), transition.EffectIDs) {
		t.Fatalf("resumed state = status %s version %d events %v effects %v", resumed.Status(), resumed.LogicalVersion(), resumed.EventIDs(), resumed.EffectIDs())
	}

	mutants := map[string]struct {
		prior      OperationStateV2
		transition OperationTransition
		permission EffectPermissionCoordinateV2
	}{
		"missing_permission_event": {prior, OperationTransition{7, 8, StatusInProgress, "", 0, nil, []EventID{"event-observation", "event-reconcile"}, []EffectID{"effect-bound"}, ""}, permission},
		"reused_permission_event": {
			fixtureState(t, StatusReconciliationRequired, 7, nil, []EventID{"event-observation", "event-permission", "event-reconcile"}, []EffectID{"effect-bound"}),
			transition,
			permission,
		},
		"unbound_effect":                  {prior, transition, fixtureEffectPermission(t, "event-permission", "effect-unbound")},
		"newly_added_not_prebound_effect": {prior, OperationTransition{7, 8, StatusInProgress, "", 0, nil, transition.EventIDs, []EffectID{"effect-bound", "effect-new"}, ""}, fixtureEffectPermission(t, "event-permission", "effect-new")},
		"empty_event":                     {prior, transition, EffectPermissionCoordinateV2{}},
		"empty_effect":                    {prior, transition, EffectPermissionCoordinateV2{}},
		"version_skip":                    {prior, OperationTransition{7, 9, StatusInProgress, "", 0, nil, transition.EventIDs, transition.EffectIDs, ""}, permission},
		"event_regression":                {prior, OperationTransition{7, 8, StatusInProgress, "", 0, nil, []EventID{"event-permission", "event-reconcile"}, transition.EffectIDs, ""}, permission},
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := ResumeOperationStateWithEffectPermissionV2(mutant.prior, mutant.transition, mutant.permission); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("permission mutant admitted: %v", err)
			}
		})
	}
}

func TestOperationReconciliationResumeDoesNotWidenOtherStatusPairs(t *testing.T) {
	permission := fixtureEffectPermission(t, "event-permission", "effect-bound")
	for _, test := range []struct {
		name string
		from OperationStatus
		to   OperationStatus
	}{
		{"accepted_to_in_progress", StatusAccepted, StatusInProgress},
		{"in_progress_to_in_progress", StatusInProgress, StatusInProgress},
		{"ambiguous_to_in_progress", StatusAmbiguous, StatusInProgress},
		{"reconciliation_to_accepted", StatusReconciliationRequired, StatusAccepted},
		{"reconciliation_to_ambiguous", StatusReconciliationRequired, StatusAmbiguous},
		{"reconciliation_to_reconciliation", StatusReconciliationRequired, StatusReconciliationRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			prior := fixtureState(t, test.from, 4, nil, []EventID{"event-old"}, []EffectID{"effect-bound"})
			transition := OperationTransition{4, 5, test.to, "", 0, nil, []EventID{"event-old", "event-permission"}, []EffectID{"effect-bound"}, ""}
			if _, err := ResumeOperationStateWithEffectPermissionV2(prior, transition, permission); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("widened permission transition %s -> %s: %v", test.from, test.to, err)
			}
		})
	}
}

func TestCommitPlanUsesPermissionBoundResumeWithoutWideningGenericTransition(t *testing.T) {
	prior := fixtureState(t, StatusReconciliationRequired, 3, nil,
		[]EventID{"event-old"}, []EffectID{"effect-bound"})
	next := mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 4, Status: StatusInProgress,
		EventIDs: []EventID{"event-old", "event-permission"}, EffectIDs: []EffectID{"effect-bound"}})
	permission := fixtureEffectPermission(t, "event-permission", "effect-bound")
	if err := exactPublicationMembership(&prior, next, []EventID{"event-permission"}, nil, nil); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("generic publication path admitted permission-only edge: %v", err)
	}
	if err := exactPublicationMembership(&prior, next, []EventID{"event-permission"}, nil, &permission); err != nil {
		t.Fatalf("sealed permission path rejected exact resume: %v", err)
	}
	for name, mutant := range map[string]EffectPermissionCoordinateV2{
		"reused_event":   fixtureEffectPermission(t, "event-old", "effect-bound"),
		"unbound_effect": fixtureEffectPermission(t, "event-permission", "effect-other"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := exactPublicationMembership(&prior, next, []EventID{"event-permission"}, nil, &mutant); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("permission coordinate mutant admitted: %v", err)
			}
		})
	}
}

func TestSealCommitPlanBindsExactCanonicalEffectPermissionEvent(t *testing.T) {
	prior := fixtureState(t, StatusReconciliationRequired, 3, nil, []EventID{"event-old"}, []EffectID{"effect-bound"})
	next := mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 4, Status: StatusInProgress,
		EventIDs: []EventID{"event-old", "event-operation", "event-permission"}, EffectIDs: []EffectID{"effect-bound"}})
	operationCoordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	effectCoordinate, _ := NewAggregateCoordinate("external_effect", "effect-bound", fixtureScope())
	permissionEvent := EventDraftV2{EventID: "event-permission", EventType: "external_effect.allow_retry", Family: "external_effect", EventSchemaVersion: 1, ReducerVersion: 1,
		Coordinate: effectCoordinate, AggregateLogicalVersion: 2, CommittedRuntimeEpochID: "epoch-18", Observations: []byte(`{"source":"test"}`), Payload: []byte(`{"permission":"retry"}`)}
	permission, err := NewEffectPermissionCoordinateV2(permissionEvent, "effect-bound")
	if err != nil {
		t.Fatal(err)
	}
	events := []EventDraftV2{
		{EventID: "event-operation", EventType: "operation.progressed", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 4, CommittedRuntimeEpochID: "epoch-18", Payload: operationPayload(t, next)},
		permissionEvent,
	}
	heads := []HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: 3, NextLogicalVersion: 4, EventID: "event-operation", ExpectedRecordRevision: 30, CommittedRuntimeEpochID: "epoch-18", CanonicalStateDigest: digest("state", []byte("operation-4")), AuthoritySummary: []byte(`{"status":"in_progress"}`)},
		{Coordinate: effectCoordinate, ExpectedLogicalVersion: 1, NextLogicalVersion: 2, EventID: "event-permission", ExpectedRecordRevision: 31, CommittedRuntimeEpochID: "epoch-18", CanonicalStateDigest: digest("state", []byte("effect-2")), AuthoritySummary: []byte(`{"stage":"retry_allowed"}`)},
	}
	base := CommitPlanInput{PriorOperationState: &prior, OperationState: next, Events: events, Heads: heads,
		Conditions: []ProviderCondition{mustHeadCondition(t, heads[0]), mustHeadCondition(t, heads[1])}, EffectPermissionResume: &permission}
	if _, err := SealCommitPlanV2(base, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("exact permission event failed public sealing: %v", err)
	}
	for name, mutate := range map[string]func(*CommitPlanInput){
		"operation_event_substitution": func(v *CommitPlanInput) {
			v.EffectPermissionResume = ptrEffectPermission(fixtureEffectPermission(t, "event-operation", "effect-bound"))
		},
		"unrelated_registered_event": func(v *CommitPlanInput) { v.Events[1].EventType = "external_effect.observation_recorded" },
		"wrong_effect_aggregate": func(v *CommitPlanInput) {
			v.Events[1].Coordinate.ID = "effect-other"
			v.Heads[1].Coordinate.ID = "effect-other"
		},
		"wrong_tuple":          func(v *CommitPlanInput) { v.Events[1].Family = "workspace" },
		"altered_payload":      func(v *CommitPlanInput) { v.Events[1].Payload = []byte(`{"permission":"compensation"}`) },
		"altered_observations": func(v *CommitPlanInput) { v.Events[1].Observations = []byte(`{"source":"other"}`) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.Events = cloneEventDrafts(base.Events)
			candidate.Heads = cloneHeadTransitions(base.Heads)
			mutate(&candidate)
			if _, err := SealCommitPlanV2(candidate, DefaultEventRegistryV2()); err == nil {
				t.Fatal("permission semantic mutant admitted")
			}
		})
	}
}

func ptrEffectPermission(value EffectPermissionCoordinateV2) *EffectPermissionCoordinateV2 {
	return &value
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
	const canonical = "2026-09-02T12:34:56Z"
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		mutant := []byte(canonical)
		mutant[index] = 'a'
		input := fixtureBindingInput(t, "epoch-17")
		input.ObservedCreatedAt = string(mutant)
		if _, err := NewOperationBindingV2(input); !IsKind(err, ErrorInvalidCanonicalData) {
			t.Fatalf("non-digit accepted at numeric position=%d value=%q err=%v", index, mutant, err)
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
	firstRecords, secondRecords := first.Records(), second.Records()
	if len(firstRecords) != len(secondRecords) {
		t.Fatal("plan order changed projected membership")
	}
	for index := range firstRecords {
		left, right := firstRecords[index], secondRecords[index]
		if left.Role() != right.Role() || left.Target() != right.Target() || left.ValueClass() != right.ValueClass() ||
			left.Condition() != right.Condition() || !bytes.Equal(left.CanonicalValue(), right.CanonicalValue()) {
			t.Fatalf("plan order changed projected record at %d", index)
		}
		if index > 0 && sealedRecordOrderKey(firstRecords[index-1]) >= sealedRecordOrderKey(left) {
			t.Fatalf("projected membership is not canonically ordered at %d", index)
		}
	}
	before := first.CanonicalBytes()
	input.Events[0].Payload[0] = 'x'
	input.Events[0].Observations[0] = 'x'
	input.Events[0].GovernanceRevisions[0] = "changed"
	input.Heads[0].EventID = "changed"
	input.Heads[0].AuthoritySummary[0] = 'x'
	input.EffectIntents[0].EffectID = "changed"
	input.EffectIntents[0].Canonical[0] = 'x'
	input.EligibilityGuards[0].ShardID = "changed"
	input.EligibilityGuards[0].Canonical[0] = 'x'
	input.Checkpoints[0].Canonical[0] = 'x'
	input.Checkpoints[0].SlotCanonical[0] = 'x'
	input.Conditions[0].Target.Coordinate.ID = "changed"
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
		address, _ := NewJournalEventAddressV2(event.Coordinate(), event.LogicalVersion())
		record, _ := NewRecoveryRecordV2(address, encoded, 1, "")
		trusted, trustedErr := DecodeEventEnvelopeRecordV2(record, registry)
		if trustedErr != nil || trusted.CanonicalDigest() != event.CanonicalDigest() || trusted.CanonicalSizeV2() != len(encoded) {
			t.Fatalf("trusted event roundtrip err=%v", trustedErr)
		}
		reencoded, reencodeErr := EncodeEventEnvelopeV2(trusted)
		if reencodeErr != nil || !bytes.Equal(reencoded, encoded) {
			t.Fatalf("trusted typed decoder changed event semantics: err=%v", reencodeErr)
		}
		if event.Family() == "operation" {
			state, stateErr := ReconstructOperationStateFromEventV2(trusted)
			if stateErr != nil || state.OperationID() != event.OperationID() {
				t.Fatalf("trusted operation event state=%+v err=%v", state, stateErr)
			}
		}
	}

	encoded, err := EncodeEventEnvelopeV2(first.Events()[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	fields["zz_unknown"] = json.RawMessage(`true`)
	unknown, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	address, _ := NewJournalEventAddressV2(first.Events()[0].Coordinate(), first.Events()[0].LogicalVersion())
	record, err := NewRecoveryRecordV2(address, unknown, 1, "")
	if err != nil {
		t.Fatalf("canonical unknown-field mutant was not admitted to the typed boundary: %v", err)
	}
	if _, err := DecodeEventEnvelopeRecordV2(record, registry); err == nil {
		t.Fatal("sealed internal decoder accepted an unknown event field")
	}
	if _, err := NewRecoveryRecordV2(address, []byte(`{"a":1,"a":2}`), 1, ""); err == nil {
		t.Fatal("duplicate-field event bytes reached the sealed typed decoder")
	}
}

func TestAuthorityAssertionsAndWorkspacePathClaimAreSealedWithDistinctSemantics(t *testing.T) {
	input := fixturePlanInput(t)
	readHead, _ := NewAggregateCoordinate("execution_slot", "slot-read", fixtureScope())
	observedDigest, err := ReducerStateDigestV2([]byte(`{"slot":"current"}`))
	if err != nil {
		t.Fatal(err)
	}
	present, err := NewAuthorityAssertionV2(AuthorityAssertionInputV2{
		Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: readHead}, Kind: ConditionRecordRevision,
		ObservedRevision: 41, ExpectedLogicalVersion: 7, ObservedValueDigest: observedDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	absent, err := NewAuthorityAssertionV2(AuthorityAssertionInputV2{
		Target: ConditionTarget{Kind: ConditionTargetEligibilityGuard, MissionID: "mission", ShardID: "guard-read"}, Kind: ConditionAbsent,
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := NewWorkspacePathClaimV2(digest("workspace.path.claim", []byte("/managed/workspace")), []byte(`{"workspace_id":"workspace-01"}`))
	if err != nil {
		t.Fatal(err)
	}
	input.Assertions = []AuthorityAssertionV2{present, absent}
	input.WorkspacePathClaim = &claim
	sealed, err := SealCommitPlanV2(input, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if sealed.AssertionCount() != 2 || sealed.MutationCount() != 11 || len(sealed.Events()) != len(input.Events) {
		t.Fatalf("assertions leaked into mutations/events: assertions=%d mutations=%d events=%d", sealed.AssertionCount(), sealed.MutationCount(), len(sealed.Events()))
	}
	claims := 0
	for _, record := range sealed.Records() {
		if record.Role() != RecordWorkspacePathClaim {
			continue
		}
		claims++
		if record.Target().WorkspacePathClaimDigest() != claim.ClaimDigest() || record.Condition().Kind() != ConditionAbsent ||
			record.Condition().ObservedRevision() != 0 || !bytes.Equal(record.CanonicalValue(), claim.CanonicalValue()) {
			t.Fatalf("workspace claim record drifted: %+v", record)
		}
	}
	if claims != 1 || int(sealed.PublicationWitness().AuthorityRecordCount()) != sealed.MutationCount()-1 {
		t.Fatalf("workspace claim/witness accounting: claims=%d record_count=%d mutations=%d", claims, sealed.PublicationWitness().AuthorityRecordCount(), sealed.MutationCount())
	}
	assertions := sealed.Assertions()
	assertions[0] = AuthorityAssertionV2{}
	claimCopy := sealed.WorkspacePathClaim()
	claimBytes := claimCopy.CanonicalValue()
	claimBytes[0] = 'x'
	if sealed.Assertions()[0].ObservedValueDigest().IsZero() || sealed.WorkspacePathClaim().CanonicalValue()[0] == 'x' {
		t.Fatal("sealed assertion or workspace claim aliases returned state")
	}

	reversed := fixturePlanInput(t)
	reversed.Assertions = []AuthorityAssertionV2{absent, present}
	reversed.WorkspacePathClaim = &claim
	second, err := SealCommitPlanV2(reversed, DefaultEventRegistryV2())
	if err != nil || second.Digest() != sealed.Digest() || !bytes.Equal(second.CanonicalBytes(), sealed.CanonicalBytes()) {
		t.Fatalf("assertion input order changed sealed identity: %v", err)
	}
	withoutAssertion := fixturePlanInput(t)
	withoutAssertion.WorkspacePathClaim = &claim
	third, _ := SealCommitPlanV2(withoutAssertion, DefaultEventRegistryV2())
	withoutClaim := fixturePlanInput(t)
	withoutClaim.Assertions = []AuthorityAssertionV2{present, absent}
	fourth, _ := SealCommitPlanV2(withoutClaim, DefaultEventRegistryV2())
	if third.Digest() == sealed.Digest() || fourth.Digest() == sealed.Digest() || third.RecordSetDigest() == sealed.RecordSetDigest() || fourth.RecordSetDigest() == sealed.RecordSetDigest() {
		t.Fatal("assertion or workspace claim failed to influence its sealed commitments")
	}
}

func TestAuthorityAssertionAndWorkspaceClaimMutantsRefuse(t *testing.T) {
	readHead, _ := NewAggregateCoordinate("execution_slot", "slot-read", fixtureScope())
	presentInput := AuthorityAssertionInputV2{Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: readHead}, Kind: ConditionRecordRevision,
		ObservedRevision: 41, ExpectedLogicalVersion: 7, ObservedValueDigest: digest("observed", []byte("head"))}
	present, _ := NewAuthorityAssertionV2(presentInput)
	for name, mutate := range map[string]func(*AuthorityAssertionInputV2){
		"checkpoint_target": func(v *AuthorityAssertionInputV2) {
			v.Target = ConditionTarget{Kind: ConditionTargetCheckpointSlot, CheckpointSlotID: "slot"}
		},
		"present_zero_revision": func(v *AuthorityAssertionInputV2) { v.ObservedRevision = 0 },
		"present_zero_version":  func(v *AuthorityAssertionInputV2) { v.ExpectedLogicalVersion = 0 },
		"present_zero_digest":   func(v *AuthorityAssertionInputV2) { v.ObservedValueDigest = Digest{} },
		"absent_with_revision":  func(v *AuthorityAssertionInputV2) { v.Kind = ConditionAbsent },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := presentInput
			mutate(&candidate)
			if _, err := NewAuthorityAssertionV2(candidate); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("malformed assertion admitted: %v", err)
			}
		})
	}
	input := fixturePlanInput(t)
	input.Assertions = []AuthorityAssertionV2{present, present}
	if _, err := SealCommitPlanV2(input, DefaultEventRegistryV2()); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("duplicate assertion admitted: %v", err)
	}
	input = fixturePlanInput(t)
	mutatedHead := input.Heads[1]
	overlap, _ := NewAuthorityAssertionV2(AuthorityAssertionInputV2{Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: mutatedHead.Coordinate}, Kind: ConditionAbsent})
	input.Assertions = []AuthorityAssertionV2{overlap}
	if _, err := SealCommitPlanV2(input, DefaultEventRegistryV2()); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("same-value-write assertion substitute admitted: %v", err)
	}
	if _, err := NewWorkspacePathClaimV2(Digest{}, []byte(`{"workspace_id":"workspace-01"}`)); !IsKind(err, ErrorInvalidDigest) {
		t.Fatalf("zero workspace claim digest admitted: %v", err)
	}
	if _, err := NewWorkspacePathClaimV2(digest("claim", nil), []byte(`{"z":1,"a":2}`)); !IsKind(err, ErrorInvalidCanonicalData) {
		t.Fatalf("noncanonical workspace claim admitted: %v", err)
	}
}

func TestSealedRecordProjectionIsPersistenceCompleteAndImmutable(t *testing.T) {
	sealed, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	records := sealed.Records()
	conservative, err := conservativeProductBatchSize(fixturePlanInput(t))
	if err != nil {
		t.Fatal(err)
	}
	exact, err := validateSealedRecordBudget(records)
	if err != nil || conservative < exact {
		t.Fatalf("preflight bound=%d exact=%d err=%v", conservative, exact, err)
	}
	roles := make(map[SealedRecordRole]int)
	for _, record := range records {
		roles[record.Role()]++
		if len(record.CanonicalValue()) == 0 {
			t.Fatalf("empty projected value for role %q", record.Role())
		}
		condition := record.Condition()
		if condition.Kind() != ConditionAbsent && (condition.Kind() != ConditionRecordRevision || condition.ObservedRevision() == 0) {
			t.Fatalf("invalid projected condition for role %q: %+v", record.Role(), condition)
		}
	}
	wantRoles := map[SealedRecordRole]int{
		RecordOperationBinding: 1, RecordJournalEvent: 2, RecordOperationHead: 1, RecordAggregateHead: 1,
		RecordEffectIntent: 1, RecordEligibilityGuard: 1, RecordCheckpointRecord: 1, RecordCheckpointSlot: 1,
		RecordPublicationWitness: 1,
	}
	if len(records) != 10 || fmt.Sprint(roles) != fmt.Sprint(wantRoles) {
		t.Fatalf("projected membership=%v roles=%v", len(records), roles)
	}

	var operationHead, aggregateHead, effect, eligibility, checkpointRecord, checkpointSlot SealedRecordV2
	for _, record := range records {
		switch record.Role() {
		case RecordOperationHead:
			operationHead = record
		case RecordAggregateHead:
			aggregateHead = record
		case RecordEffectIntent:
			effect = record
		case RecordEligibilityGuard:
			eligibility = record
		case RecordCheckpointRecord:
			checkpointRecord = record
		case RecordCheckpointSlot:
			checkpointSlot = record
		}
	}
	state, err := DecodeOperationStateV2(operationHead.CanonicalValue())
	if err != nil || state.LastPublicationID() != sealed.PublicationID() || operationHead.Target().OperationID() != "operation-01" {
		t.Fatalf("operation head is not canonical OperationState: state=%+v err=%v", state, err)
	}
	head, err := DecodeAggregateHeadV2(aggregateHead.CanonicalValue())
	if err != nil || head.input.LastPublicationID != sealed.PublicationID() || head.input.CommittedRuntimeEpochID != "epoch-17" {
		t.Fatalf("aggregate head is not publication-complete: head=%+v err=%v", head, err)
	}
	if effect.Target().EffectID() != "effect-01" || string(effect.CanonicalValue()) != `{"intent":"one"}` || effect.Condition().Kind() != ConditionAbsent {
		t.Fatalf("effect projection incomplete: %+v", effect)
	}
	if eligibility.Target().MissionID() != "mission" || eligibility.Target().ShardID() != "guard-01" || string(eligibility.CanonicalValue()) != `{"eligible":true}` {
		t.Fatalf("eligibility projection incomplete: %+v", eligibility)
	}
	if checkpointRecord.Target().CheckpointID() != "checkpoint-01" || checkpointRecord.ValueClass() != RecordValueCheckpoint || checkpointRecord.Condition().Kind() != ConditionAbsent {
		t.Fatalf("checkpoint record projection incomplete: %+v", checkpointRecord)
	}
	if checkpointSlot.Target().CheckpointSlotID() != "slot-01" || string(checkpointSlot.CanonicalValue()) != `{"checkpoint_id":"checkpoint-01"}` {
		t.Fatalf("checkpoint slot projection incomplete: %+v", checkpointSlot)
	}

	firstValue := records[0].CanonicalValue()
	firstValue[0] = 'x'
	records[0] = SealedRecordV2{}
	again := sealed.Records()
	if len(again) != 10 || again[0].CanonicalValue()[0] == 'x' {
		t.Fatal("sealed record projection aliases returned buffers")
	}
}

func TestPublicationWitnessRoundTripAndSealedProjectionSemantics(t *testing.T) {
	input := fixturePlanInput(t)
	preimage, err := planPreimage(input.OperationBinding, input.PriorOperationState, input.OperationState, input.Events, input.Heads,
		input.EffectIntents, input.EligibilityGuards, input.Checkpoints, input.Conditions, input.Assertions, input.WorkspacePathClaim, input.EffectPermissionResume)
	if err != nil || !bytes.Contains(preimage, []byte(`"publication_witness_schema_version":1`)) || bytes.Contains(preimage, []byte(`"witness_sha256"`)) {
		t.Fatalf("plan preimage does not bind only the witness contract version: %s %v", preimage, err)
	}
	plan, err := SealCommitPlanV2(input, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	witness := plan.PublicationWitness()
	encoded, err := EncodePublicationWitnessV2(witness)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePublicationWitnessV2(encoded)
	if err != nil || decoded != witness {
		t.Fatalf("witness round trip failed: %+v %v", decoded, err)
	}
	address, err := witness.PublicationID().WitnessAddressV2()
	if err != nil {
		t.Fatal(err)
	}
	recoveryRecord, err := NewRecoveryRecordV2(address, encoded, 17, "")
	if err != nil {
		t.Fatalf("canonical witness did not cross the public recovery record boundary: %v", err)
	}
	recovered, err := DecodePublicationWitnessRecordV2(recoveryRecord)
	if err != nil || recovered != witness || recovered.WitnessDigest() != witness.WitnessDigest() {
		t.Fatalf("recovery record changed witness semantics or digest: %+v %v", recovered, err)
	}
	if witness.SchemaVersion() != 1 || witness.PublicationID() != plan.PublicationID() ||
		witness.OperationID() != plan.OperationState().OperationID() || witness.OperationLogicalVersion() != plan.OperationState().LogicalVersion() ||
		witness.PlanDigest() != plan.Digest() || witness.AuthorityRecordSetDigest() != plan.RecordSetDigest() ||
		int(witness.AuthorityRecordCount())+1 != plan.MutationCount() || witness.WitnessDigest().IsZero() {
		t.Fatalf("witness lost publication proof coordinates: %+v", witness)
	}
	if !bytes.Contains(plan.CanonicalBytes(), []byte(`"publication_witness":{"schema_version":1`)) {
		t.Fatal("sealed plan does not carry the structured schema-bound witness")
	}
	if _, err := DecodePublicationWitnessV2(append(append([]byte(nil), encoded...), ' ')); err == nil {
		t.Fatal("non-canonical witness encoding accepted")
	}
	declarationOrder, err := marshalCanonical(publicationWitnessWire(witness), MaxCanonicalPayloadBytes)
	if err != nil || bytes.Equal(declarationOrder, encoded) {
		t.Fatalf("test did not construct an alternate valid JSON field order: %v", err)
	}
	if _, err := DecodePublicationWitnessV2(declarationOrder); err == nil {
		t.Fatal("alternate witness field order accepted as canonical")
	}

	records := plan.Records()
	witnessRecords := 0
	for _, record := range records {
		if record.Role() != RecordPublicationWitness {
			continue
		}
		witnessRecords++
		condition := record.Condition()
		if record.Target().PublicationID() != plan.PublicationID() || record.ValueClass() != RecordValueOrdinary ||
			condition.Kind() != ConditionAbsent || condition.ObservedRevision() != 0 || !bytes.Equal(record.CanonicalValue(), encoded) {
			t.Fatalf("witness persistence projection is mutable or misaddressed: %+v", record)
		}
	}
	if witnessRecords != 1 {
		t.Fatalf("witness projection count=%d", witnessRecords)
	}
	nonWitnessDigest, err := sealedRecordSetDigest(records)
	if err != nil || nonWitnessDigest != plan.RecordSetDigest() {
		t.Fatalf("reported record set is not the finalized non-witness set: %s %v", nonWitnessDigest, err)
	}
	mutatedWitnessValue := append([]byte(nil), encoded...)
	mutatedWitnessValue[len(mutatedWitnessValue)-2] ^= 1
	for index := range records {
		if records[index].role == RecordPublicationWitness {
			records[index].canonical = mutatedWitnessValue
		}
	}
	mutatedSetDigest, err := sealedRecordSetDigest(records)
	if err != nil || mutatedSetDigest != plan.RecordSetDigest() {
		t.Fatalf("witness entered its reported record-set digest: %s %v", mutatedSetDigest, err)
	}

	base := publicationWitnessWire(witness)
	mutants := map[string]func(*publicationWitnessWireV2){
		"schema":      func(v *publicationWitnessWireV2) { v.SchemaVersion++ },
		"publication": func(v *publicationWitnessWireV2) { v.PublicationID = string(testPublicationID("other")) },
		"operation":   func(v *publicationWitnessWireV2) { v.OperationID = "operation-other" },
		"version":     func(v *publicationWitnessWireV2) { v.LogicalVersion++ },
		"plan":        func(v *publicationWitnessWireV2) { v.PlanSHA256 = digest("other", nil).String() },
		"record_set":  func(v *publicationWitnessWireV2) { v.RecordSetSHA256 = digest("other", nil).String() },
		"count":       func(v *publicationWitnessWireV2) { v.RecordCount-- },
		"self_digest": func(v *publicationWitnessWireV2) { v.WitnessSHA256 = digest("other", nil).String() },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			canonical, marshalErr := marshalCanonical(candidate, MaxCanonicalPayloadBytes)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			canonical, marshalErr = CanonicalObject(canonical)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, decodeErr := DecodePublicationWitnessV2(canonical); decodeErr == nil {
				t.Fatal("corrupted witness field was accepted")
			}
		})
	}
}

func TestPublicationWitnessPlanMutantsFailClosed(t *testing.T) {
	plan, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	witnessIndex := -1
	for index := range plan.records {
		if plan.records[index].role == RecordPublicationWitness {
			witnessIndex = index
		}
	}
	if witnessIndex < 0 {
		t.Fatal("fixture has no witness")
	}

	mutants := map[string]func(*SealedCommitPlanV2){
		"omitted": func(v *SealedCommitPlanV2) {
			v.records = append(v.records[:witnessIndex:witnessIndex], v.records[witnessIndex+1:]...)
		},
		"duplicated": func(v *SealedCommitPlanV2) {
			v.records = append(v.records, v.records[witnessIndex])
		},
		"mutable_condition": func(v *SealedCommitPlanV2) {
			v.records[witnessIndex].condition = SealedRecordConditionV2{kind: ConditionRecordRevision, observedRevision: 1}
		},
		"wrong_target": func(v *SealedCommitPlanV2) {
			v.records[witnessIndex].target.publicationID = testPublicationID("other")
		},
		"wrong_value_class": func(v *SealedCommitPlanV2) {
			v.records[witnessIndex].valueClass = RecordValueCheckpoint
		},
		"foreign_record_set": func(v *SealedCommitPlanV2) {
			v.witness.recordSetDigest = digest("foreign-record-set", nil)
			v.witness.witnessDigest, _ = publicationWitnessDigest(v.witness)
			v.records[witnessIndex].canonical, _ = EncodePublicationWitnessV2(v.witness)
		},
		"substituted_operation": func(v *SealedCommitPlanV2) {
			v.witness.operationID = "operation-other"
			v.witness.witnessDigest, _ = publicationWitnessDigest(v.witness)
			v.records[witnessIndex].canonical, _ = EncodePublicationWitnessV2(v.witness)
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := plan
			candidate.records = append([]SealedRecordV2(nil), plan.records...)
			mutate(&candidate)
			if err := validateSealedPlanIdentity(candidate); err == nil {
				t.Fatal("witness plan mutant was accepted")
			}
		})
	}
}

func TestPersistenceCompleteMemberMutantsAndDigestBinding(t *testing.T) {
	registry := DefaultEventRegistryV2()
	for name, mutate := range map[string]func(*CommitPlanInput){
		"head_missing_epoch":            func(v *CommitPlanInput) { v.Heads[1].CommittedRuntimeEpochID = "" },
		"head_wrong_epoch":              func(v *CommitPlanInput) { v.Heads[1].CommittedRuntimeEpochID = "epoch-other" },
		"head_noncanonical_summary":     func(v *CommitPlanInput) { v.Heads[1].AuthoritySummary = []byte(`{ "status":"planned"}`) },
		"effect_missing_value":          func(v *CommitPlanInput) { v.EffectIntents[0].Canonical = nil },
		"effect_noncanonical_value":     func(v *CommitPlanInput) { v.EffectIntents[0].Canonical = []byte(`{ "intent":"one"}`) },
		"eligibility_missing_mission":   func(v *CommitPlanInput) { v.EligibilityGuards[0].MissionID = "" },
		"eligibility_missing_shard":     func(v *CommitPlanInput) { v.EligibilityGuards[0].ShardID = "" },
		"eligibility_missing_value":     func(v *CommitPlanInput) { v.EligibilityGuards[0].Canonical = nil },
		"checkpoint_missing_id":         func(v *CommitPlanInput) { v.Checkpoints[0].CheckpointID = "" },
		"checkpoint_missing_slot":       func(v *CommitPlanInput) { v.Checkpoints[0].SlotID = "" },
		"checkpoint_missing_record":     func(v *CommitPlanInput) { v.Checkpoints[0].Canonical = nil },
		"checkpoint_missing_slot_value": func(v *CommitPlanInput) { v.Checkpoints[0].SlotCanonical = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := fixturePlanInput(t)
			mutate(&candidate)
			if _, err := SealCommitPlanV2(candidate, registry); err == nil {
				t.Fatal("persistence-incomplete member accepted")
			}
		})
	}

	duplicateEligibility := fixturePlanInput(t)
	duplicateEligibility.EligibilityGuards = append(duplicateEligibility.EligibilityGuards, duplicateEligibility.EligibilityGuards[0])
	duplicateEligibility.Conditions = persistenceConditions(t, duplicateEligibility.Heads, duplicateEligibility.EligibilityGuards, duplicateEligibility.Checkpoints)
	if _, err := SealCommitPlanV2(duplicateEligibility, registry); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("duplicate eligibility target accepted: %v", err)
	}
	duplicateSlot := fixturePlanInput(t)
	secondCheckpoint := duplicateSlot.Checkpoints[0]
	secondCheckpoint.CheckpointID = "checkpoint-02"
	duplicateSlot.Checkpoints = append(duplicateSlot.Checkpoints, secondCheckpoint)
	duplicateSlot.Conditions = persistenceConditions(t, duplicateSlot.Heads, duplicateSlot.EligibilityGuards, duplicateSlot.Checkpoints)
	if _, err := SealCommitPlanV2(duplicateSlot, registry); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("duplicate checkpoint slot accepted: %v", err)
	}

	baseline, err := SealCommitPlanV2(fixturePlanInput(t), registry)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CommitPlanInput){
		"head_epoch": func(v *CommitPlanInput) {
			v.Events[1].CommittedRuntimeEpochID = "epoch-18"
			v.Heads[1].CommittedRuntimeEpochID = "epoch-18"
		},
		"head_summary": func(v *CommitPlanInput) { v.Heads[1].AuthoritySummary = []byte(`{"status":"active"}`) },
		"effect_value": func(v *CommitPlanInput) { v.EffectIntents[0].Canonical = []byte(`{"intent":"two"}`) },
		"eligibility_target": func(v *CommitPlanInput) {
			v.EligibilityGuards[0].ShardID = "guard-02"
			v.Conditions = persistenceConditions(t, v.Heads, v.EligibilityGuards, v.Checkpoints)
		},
		"eligibility_value":        func(v *CommitPlanInput) { v.EligibilityGuards[0].Canonical = []byte(`{"eligible":false}`) },
		"checkpoint_record_target": func(v *CommitPlanInput) { v.Checkpoints[0].CheckpointID = "checkpoint-02" },
		"checkpoint_record_value":  func(v *CommitPlanInput) { v.Checkpoints[0].Canonical = []byte(`{"head":2}`) },
		"checkpoint_slot_target": func(v *CommitPlanInput) {
			v.Checkpoints[0].SlotID = "slot-02"
			v.Conditions = persistenceConditions(t, v.Heads, v.EligibilityGuards, v.Checkpoints)
		},
		"checkpoint_slot_value": func(v *CommitPlanInput) { v.Checkpoints[0].SlotCanonical = []byte(`{"checkpoint_id":"checkpoint-02"}`) },
	} {
		t.Run("digest_"+name, func(t *testing.T) {
			candidate := fixturePlanInput(t)
			mutate(&candidate)
			sealed, err := SealCommitPlanV2(candidate, registry)
			if err != nil {
				t.Fatal(err)
			}
			if sealed.Digest() == baseline.Digest() || sealed.PublicationID() == baseline.PublicationID() {
				t.Fatal("persistence member was not bound into publication identity")
			}
		})
	}
}

func TestMutableProjectedRecordConditionBijection(t *testing.T) {
	valid := fixturePlanInput(t)
	valid.EligibilityGuards[0].ExpectedRecordRevision = 17
	valid.Checkpoints[0].ExpectedSlotRecordRevision = 23
	valid.Conditions = persistenceConditions(t, valid.Heads, valid.EligibilityGuards, valid.Checkpoints)
	sealed, err := SealCommitPlanV2(valid, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range sealed.Records() {
		switch record.Role() {
		case RecordEligibilityGuard:
			if record.Condition().Kind() != ConditionRecordRevision || record.Condition().ObservedRevision() != 17 {
				t.Fatalf("eligibility condition lost: %+v", record.Condition())
			}
		case RecordCheckpointSlot:
			if record.Condition().Kind() != ConditionRecordRevision || record.Condition().ObservedRevision() != 23 {
				t.Fatalf("checkpoint slot condition lost: %+v", record.Condition())
			}
		}
	}
	for name, mutate := range map[string]func(*CommitPlanInput){
		"omission":             func(v *CommitPlanInput) { v.Conditions = v.Conditions[:len(v.Conditions)-1] },
		"substitution":         func(v *CommitPlanInput) { v.Conditions[2].Target.ShardID = "guard-other" },
		"duplicate":            func(v *CommitPlanInput) { v.Conditions[3] = v.Conditions[2] },
		"extra":                func(v *CommitPlanInput) { v.Conditions = append(v.Conditions, v.Conditions[2]) },
		"eligibility_revision": func(v *CommitPlanInput) { v.Conditions[2].ObservedRevision++ },
		"checkpoint_revision":  func(v *CommitPlanInput) { v.Conditions[3].ObservedRevision++ },
		"eligibility_wrong_kind": func(v *CommitPlanInput) {
			v.Conditions[2].Kind, v.Conditions[2].ObservedRevision = ConditionAbsent, 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.Conditions = append([]ProviderCondition(nil), valid.Conditions...)
			mutate(&candidate)
			if _, err := SealCommitPlanV2(candidate, DefaultEventRegistryV2()); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("condition bijection mutant accepted: %v", err)
			}
		})
	}
}

func TestCommitPlanBindsCompleteOperationStateSemantics(t *testing.T) {
	registry := DefaultEventRegistryV2()
	base := fixturePlanInput(t)
	base.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusResolved,
		ResultKind: "result.one", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: []EffectID{"effect-01"}})
	base.Events[0].Payload = operationPayload(t, base.OperationState)
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
		candidate.Events[0].Payload = operationPayload(t, candidate.OperationState)
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
		EventIDs: []EventID{"event-z"}, EffectIDs: []EffectID{"effect-z"}, LastPublicationID: testPublicationID("old")})
	next, err := AdvanceOperationState(prior, OperationTransition{1, 2, StatusInProgress, "", 0, nil,
		[]EventID{"event-a", "event-z"}, []EffectID{"effect-a", "effect-z"}, ""})
	if err != nil {
		t.Fatal(err)
	}
	coordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	laterHead := HeadTransitionV2{Coordinate: coordinate, ExpectedLogicalVersion: 1, NextLogicalVersion: 2, EventID: "event-a", ExpectedRecordRevision: 7,
		CommittedRuntimeEpochID: "epoch-18", CanonicalStateDigest: digest("state", []byte("next")), AuthoritySummary: []byte(`{"status":"in_progress"}`)}
	later := CommitPlanInput{PriorOperationState: &prior, OperationState: next,
		Events:        []EventDraftV2{{EventID: "event-a", EventType: "operation.progressed", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 2, CommittedRuntimeEpochID: "epoch-18", Payload: operationPayload(t, next)}},
		Heads:         []HeadTransitionV2{laterHead},
		EffectIntents: []EffectIntentReference{{EffectID: "effect-a", Digest: digest("effect", []byte("a")), Canonical: []byte(`{"intent":"a"}`)}},
		Conditions:    []ProviderCondition{mustHeadCondition(t, laterHead)},
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

func TestCommitPlanRejectsFamilyAggregateAuthorityAliases(t *testing.T) {
	registry := DefaultEventRegistryV2()
	for name, mutate := range map[string]func(*CommitPlanInput){
		"family_kind_mismatch": func(v *CommitPlanInput) {
			v.Events[1].Coordinate.Kind = "artifact_catalog"
			v.Heads[1].Coordinate = v.Events[1].Coordinate
		},
		"disguised_mission_scoped_operation": func(v *CommitPlanInput) {
			v.Events[1].Coordinate = AggregateCoordinate{Kind: "operation", ID: "operation-shadow", Scope: fixtureScope()}
			v.Heads[1].Coordinate = v.Events[1].Coordinate
		},
		"disguised_wrong_operation_id": func(v *CommitPlanInput) {
			v.Events[1].Family = "operation"
			v.Events[1].EventSchemaVersion, v.Events[1].ReducerVersion = 2, 2
			v.Events[1].EventType = "operation.changed"
			v.Events[1].Coordinate = AggregateCoordinate{Kind: "operation", ID: "operation-shadow", Scope: Scope{Kind: ScopeHost}}
			v.Heads[1].Coordinate = v.Events[1].Coordinate
		},
		"disguised_wrong_operation_version": func(v *CommitPlanInput) {
			v.Events[1].Family = "operation"
			v.Events[1].EventSchemaVersion, v.Events[1].ReducerVersion = 2, 2
			v.Events[1].EventType = "operation.changed"
			v.Events[1].Coordinate = AggregateCoordinate{Kind: "operation", ID: "operation-01", Scope: Scope{Kind: ScopeHost}}
			v.Events[1].AggregateLogicalVersion = 2
			v.Heads[1].Coordinate = v.Events[1].Coordinate
			v.Heads[1].NextLogicalVersion = 2
			v.Heads[1].ExpectedLogicalVersion = 1
		},
		"operation_head_scope": func(v *CommitPlanInput) { v.Heads[0].Coordinate.Scope = fixtureScope() },
		"operation_head_id":    func(v *CommitPlanInput) { v.Heads[0].Coordinate.ID = "operation-other" },
		"operation_head_version": func(v *CommitPlanInput) {
			v.Heads[0].NextLogicalVersion++
			v.Heads[0].ExpectedLogicalVersion++
		},
		"operation_head_event": func(v *CommitPlanInput) { v.Heads[0].EventID = "event-other" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := fixturePlanInput(t)
			mutate(&candidate)
			if _, err := SealCommitPlanV2(candidate, registry); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("aggregate authority alias accepted: %v", err)
			}
		})
	}
	sealed, err := SealCommitPlanV2(fixturePlanInput(t), registry)
	if err != nil {
		t.Fatal(err)
	}
	disguisedEnvelope := sealed.events[0].input
	disguisedEnvelope.Family = "mission"
	disguisedEnvelope.EventSchemaVersion, disguisedEnvelope.ReducerVersion = 1, 1
	disguisedEnvelope.Payload = []byte(`{"status":"accepted"}`)
	disguisedEnvelope.Observations = []byte(`{"source":"test"}`)
	if _, err := NewEventEnvelopeV2(disguisedEnvelope, registry); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("direct event envelope family/aggregate alias accepted: %v", err)
	}
}

func TestProviderConditionsExactlyMatchHeadMutations(t *testing.T) {
	registry := DefaultEventRegistryV2()
	base := fixturePlanInput(t)
	if _, err := SealCommitPlanV2(base, registry); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CommitPlanInput){
		"omitted": func(v *CommitPlanInput) { v.Conditions = v.Conditions[:1] },
		"substituted_target": func(v *CommitPlanInput) {
			v.Conditions[1].Target.Coordinate.ID = "unrelated"
		},
		"duplicate_target": func(v *CommitPlanInput) { v.Conditions[1] = v.Conditions[0] },
		"extra_target": func(v *CommitPlanInput) {
			extra := v.Conditions[1]
			extra.Target.Coordinate.ID = "extra"
			v.Conditions = append(v.Conditions, extra)
		},
		"create_with_revision": func(v *CommitPlanInput) {
			v.Conditions[0].Kind = ConditionRecordRevision
			v.Conditions[0].ObservedRevision = 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := fixturePlanInput(t)
			mutate(&candidate)
			if _, err := SealCommitPlanV2(candidate, registry); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("condition membership mutant accepted: %v", err)
			}
		})
	}

	updated := fixturePlanInput(t)
	updated.Events[1].AggregateLogicalVersion = 2
	updated.Heads[1].ExpectedLogicalVersion = 1
	updated.Heads[1].NextLogicalVersion = 2
	updated.Heads[1].ExpectedRecordRevision = 7
	updated.Conditions = persistenceConditions(t, updated.Heads, updated.EligibilityGuards, updated.Checkpoints)
	if _, err := SealCommitPlanV2(updated, registry); err != nil {
		t.Fatalf("valid observed-revision update rejected: %v", err)
	}
	for name, mutate := range map[string]func(*CommitPlanInput){
		"update_as_absent": func(v *CommitPlanInput) {
			v.Conditions[1].Kind = ConditionAbsent
			v.Conditions[1].ObservedRevision = 0
		},
		"wrong_revision": func(v *CommitPlanInput) { v.Conditions[1].ObservedRevision++ },
		"update_without_head_revision": func(v *CommitPlanInput) {
			v.Heads[1].ExpectedRecordRevision = 0
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := updated
			candidate.Conditions = append([]ProviderCondition(nil), updated.Conditions...)
			candidate.Heads = append([]HeadTransitionV2(nil), updated.Heads...)
			mutate(&candidate)
			if _, err := SealCommitPlanV2(candidate, registry); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("update condition mutant accepted: %v", err)
			}
		})
	}
}

func TestPublicationIDIngressAndOperationHeadContext(t *testing.T) {
	valid := testPublicationID("valid")
	for _, value := range []string{"", "abc", strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		if _, err := DecodePublicationID(value); !IsKind(err, ErrorInvalidDigest) {
			t.Fatalf("invalid publication ID accepted=%q err=%v", value, err)
		}
	}
	if parsed, err := DecodePublicationID(string(valid)); err != nil || parsed != valid {
		t.Fatalf("valid publication ID rejected: %v", err)
	}

	preseal := fixtureState(t, StatusAccepted, 1, nil, []EventID{"event-01"}, nil)
	if _, err := EncodeOperationStateV2(preseal); !IsKind(err, ErrorInvalidDigest) {
		t.Fatalf("empty persisted publication ID accepted: %v", err)
	}
	for _, invalidID := range []PublicationID{"short", PublicationID(strings.Repeat("A", 64)), PublicationID(strings.Repeat("g", 64))} {
		if _, err := NewOperationStateV2(OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted, EventIDs: []EventID{"event-01"}, LastPublicationID: invalidID}); !IsKind(err, ErrorInvalidDigest) {
			t.Fatalf("operation state publication ID accepted=%q err=%v", invalidID, err)
		}
	}

	sealed, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	state := sealed.OperationState()
	var operationEvent EventEnvelopeV2
	var operationTransition HeadTransitionV2
	for _, event := range sealed.events {
		if event.input.Family == "operation" {
			operationEvent = event
		}
	}
	for _, head := range sealed.heads {
		if head.Coordinate.Kind == AggregateKind("operation") {
			operationTransition = head
		}
	}
	headInput := AggregateHeadInput{Coordinate: operationTransition.Coordinate, LogicalVersion: operationTransition.NextLogicalVersion,
		LastEventID: operationTransition.EventID, LastPublicationID: sealed.PublicationID(), CommittedRuntimeEpochID: "epoch-17",
		CanonicalStateDigest: operationTransition.CanonicalStateDigest, AuthoritySummary: []byte(`{"status":"accepted"}`)}
	head, err := NewAggregateHeadV2(headInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationPublicationHeadV2(state, operationEvent, head); err != nil {
		t.Fatalf("valid operation publication head rejected: %v", err)
	}
	badHeadPublication := headInput
	badHeadPublication.LastPublicationID = "short"
	if _, err := NewAggregateHeadV2(badHeadPublication); !IsKind(err, ErrorInvalidDigest) {
		t.Fatalf("head accepted malformed publication ID: %v", err)
	}
	badEventPublication := operationEvent.input
	badEventPublication.PublicationID = "short"
	badEventPublication.Payload = operationEvent.Payload()
	badEventPublication.Observations = operationEvent.Observations()
	if _, err := NewEventEnvelopeV2(badEventPublication, DefaultEventRegistryV2()); !IsKind(err, ErrorInvalidDigest) {
		t.Fatalf("event accepted malformed publication ID: %v", err)
	}
	encoded, err := EncodeAggregateHeadV2(head)
	if err != nil {
		t.Fatal(err)
	}
	if decoded, err := DecodeAggregateHeadV2(encoded); err != nil || ValidateOperationPublicationHeadV2(state, operationEvent, decoded) != nil {
		t.Fatalf("operation head round trip rejected: %v", err)
	}

	wrongScope := headInput
	wrongScope.Coordinate.Scope = fixtureScope()
	if _, err := NewAggregateHeadV2(wrongScope); !IsKind(err, ErrorInvalidScope) {
		t.Fatalf("mission-scoped operation head accepted: %v", err)
	}
	var wire aggregateHeadWire
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	wire.Coordinate.Scope = fixtureScope()
	malicious, _ := json.Marshal(wire)
	if _, err := DecodeAggregateHeadV2(malicious); !IsKind(err, ErrorJournalCorrupt) {
		t.Fatalf("mission-scoped decoded operation head accepted: %v", err)
	}

	for name, mutate := range map[string]func(*OperationStateV2, *EventEnvelopeV2, *AggregateHeadV2){
		"state_id":      func(v *OperationStateV2, _ *EventEnvelopeV2, _ *AggregateHeadV2) { v.operationID = "operation-other" },
		"state_version": func(v *OperationStateV2, _ *EventEnvelopeV2, _ *AggregateHeadV2) { v.logicalVersion++ },
		"event_id":      func(_ *OperationStateV2, v *EventEnvelopeV2, _ *AggregateHeadV2) { v.input.EventID = "event-other" },
		"head_event_id": func(_ *OperationStateV2, _ *EventEnvelopeV2, v *AggregateHeadV2) { v.input.LastEventID = "event-other" },
		"head_id": func(_ *OperationStateV2, _ *EventEnvelopeV2, v *AggregateHeadV2) {
			v.input.Coordinate.ID = "operation-other"
		},
		"head_version": func(_ *OperationStateV2, _ *EventEnvelopeV2, v *AggregateHeadV2) { v.input.LogicalVersion++ },
		"event_publication": func(_ *OperationStateV2, v *EventEnvelopeV2, _ *AggregateHeadV2) {
			v.input.PublicationID = testPublicationID("other")
		},
		"head_publication": func(_ *OperationStateV2, _ *EventEnvelopeV2, v *AggregateHeadV2) {
			v.input.LastPublicationID = testPublicationID("other")
		},
	} {
		t.Run(name, func(t *testing.T) {
			stateMutant, eventMutant, headMutant := state, operationEvent, head
			mutate(&stateMutant, &eventMutant, &headMutant)
			if err := ValidateOperationPublicationHeadV2(stateMutant, eventMutant, headMutant); !IsKind(err, ErrorInvalidTransition) {
				t.Fatalf("context mutant accepted: %v", err)
			}
		})
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
		maximumEffects.EffectIntents[index] = EffectIntentReference{EffectID: id, Digest: digest("effect", []byte(id)), Canonical: []byte(fmt.Sprintf(`{"intent":"%s"}`, id))}
		effectIDs[index] = id
	}
	maximumEffects.OperationState = mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted,
		EventIDs: []EventID{"event-mission", "event-operation"}, EffectIDs: effectIDs})
	maximumEffects.Events[0].Payload = operationPayload(t, maximumEffects.OperationState)
	if _, err := SealCommitPlanV2(maximumEffects, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("maximum effects rejected: %v", err)
	}

	maximumMetadata := fixturePlanInput(t)
	fixedProjectedRecords := 1 + len(maximumMetadata.Events) + len(maximumMetadata.Heads) + len(maximumMetadata.EffectIntents) + 2*MaxCheckpointsPerPlan + 1 // witness
	maximumMetadata.EligibilityGuards = make([]EligibilityGuardTransition, MaxMembers-fixedProjectedRecords)
	for index := range maximumMetadata.EligibilityGuards {
		id := fmt.Sprintf("guard-%03d", index)
		maximumMetadata.EligibilityGuards[index] = EligibilityGuardTransition{MissionID: "mission", ShardID: EligibilityShardID(id), Canonical: []byte(fmt.Sprintf(`{"guard":"%s"}`, id))}
	}
	maximumMetadata.Checkpoints = make([]CheckpointRecord, MaxCheckpointsPerPlan)
	for index := range maximumMetadata.Checkpoints {
		maximumMetadata.Checkpoints[index] = CheckpointRecord{CheckpointID: CheckpointID(fmt.Sprintf("checkpoint-%03d", index)), Canonical: []byte(fmt.Sprintf(`{"index":%d}`, index)),
			SlotID: CheckpointSlotID(fmt.Sprintf("slot-%03d", index)), SlotCanonical: []byte(fmt.Sprintf(`{"index":%d}`, index))}
	}
	maximumMetadata.Conditions = persistenceConditions(t, maximumMetadata.Heads, maximumMetadata.EligibilityGuards, maximumMetadata.Checkpoints)
	sealedMetadata, err := SealCommitPlanV2(maximumMetadata, DefaultEventRegistryV2())
	if err != nil {
		t.Fatalf("maximum metadata collections rejected: %v", err)
	}
	if len(sealedMetadata.Records()) != MaxMembers {
		t.Fatalf("sealed projection exact budget=%d", len(sealedMetadata.Records()))
	}
	if sealedMetadata.PublicationWitness().AuthorityRecordCount() != MaxMembers-1 || sealedMetadata.MutationCount() != MaxMembers {
		t.Fatalf("255 non-witness plus one witness accounting drifted: reported=%d mutations=%d",
			sealedMetadata.PublicationWitness().AuthorityRecordCount(), sealedMetadata.MutationCount())
	}
	readHead, _ := NewAggregateCoordinate("execution_slot", "slot-read", fixtureScope())
	readAssertion, _ := NewAuthorityAssertionV2(AuthorityAssertionInputV2{Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: readHead},
		Kind: ConditionAbsent})
	assertionOverflow := maximumMetadata
	assertionOverflow.Assertions = []AuthorityAssertionV2{readAssertion}
	if _, err := SealCommitPlanV2(assertionOverflow, DefaultEventRegistryV2()); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("combined assertion plus mutation maximum-plus-one admitted: %v", err)
	}
	overProjection := maximumMetadata
	overProjection.EligibilityGuards = append([]EligibilityGuardTransition(nil), maximumMetadata.EligibilityGuards...)
	overProjection.EligibilityGuards = append(overProjection.EligibilityGuards, EligibilityGuardTransition{MissionID: "mission", ShardID: "guard-extra", Canonical: []byte(`{"guard":"extra"}`)})
	overProjection.Conditions = persistenceConditions(t, overProjection.Heads, overProjection.EligibilityGuards, overProjection.Checkpoints)
	if _, err := SealCommitPlanV2(overProjection, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("sealed projection maximum-plus-one reached a later seam: %v", err)
	}
}

func TestNestedCollectionAndByteBudgetBoundaries(t *testing.T) {
	canonicalObjectAt := func(size int) []byte {
		t.Helper()
		if size < len(`{"x":""}`) {
			t.Fatalf("invalid canonical object fixture size=%d", size)
		}
		return []byte(`{"x":"` + strings.Repeat("x", size-len(`{"x":""}`)) + `"}`)
	}
	governanceAt := func(count int) []string {
		result := make([]string, count)
		for index := range result {
			result[index] = fmt.Sprintf("revision-%03d", index)
		}
		return result
	}

	maximumGovernance := fixturePlanInput(t)
	maximumGovernance.Events[0].GovernanceRevisions = governanceAt(MaxMembers)
	if _, err := SealCommitPlanV2(maximumGovernance, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("maximum nested governance membership rejected: %v", err)
	}
	overGovernance := fixturePlanInput(t)
	overGovernance.Events[0].GovernanceRevisions = governanceAt(MaxMembers + 1)
	if _, err := SealCommitPlanV2(overGovernance, DefaultEventRegistryV2()); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("maximum-plus-one nested governance membership returned %v", err)
	}

	for name, setBytes := range map[string]func(*CommitPlanInput, []byte){
		"event_payload":      func(v *CommitPlanInput, payload []byte) { v.Events[0].Payload = payload },
		"event_observations": func(v *CommitPlanInput, payload []byte) { v.Events[0].Observations = payload },
	} {
		t.Run(name, func(t *testing.T) {
			derivedOversize := fixturePlanInput(t)
			setBytes(&derivedOversize, canonicalObjectAt(MaxCanonicalPayloadBytes))
			if _, err := SealCommitPlanV2(derivedOversize, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "ordinary_record_value" {
				t.Fatalf("source-sized but derived-oversize event reached a later seam: %v", err)
			}
			over := fixturePlanInput(t)
			setBytes(&over, canonicalObjectAt(MaxCanonicalPayloadBytes+1))
			if _, err := SealCommitPlanV2(over, DefaultEventRegistryV2()); !IsKind(err, ErrorBudgetExceeded) {
				t.Fatalf("maximum-plus-one nested bytes returned %v", err)
			}
		})
	}

	sealed, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	prototype := sealed.events[0].input
	prototype.Payload = []byte(`{"status":"accepted"}`)
	prototype.Observations = []byte(`{"source":"test"}`)
	for name, setInput := range map[string]func(*EventEnvelopeInput, int){
		"envelope_governance": func(v *EventEnvelopeInput, amount int) { v.GovernanceRevisions = governanceAt(amount) },
		"envelope_payload":    func(v *EventEnvelopeInput, amount int) { v.Payload = canonicalObjectAt(amount) },
		"envelope_observation": func(v *EventEnvelopeInput, amount int) {
			v.Observations = canonicalObjectAt(amount)
		},
	} {
		t.Run(name, func(t *testing.T) {
			maximumAmount, overAmount := MaxCanonicalPayloadBytes, MaxCanonicalPayloadBytes+1
			if name == "envelope_governance" {
				maximumAmount, overAmount = MaxMembers, MaxMembers+1
			}
			maximum := prototype
			setInput(&maximum, maximumAmount)
			if _, err := NewEventEnvelopeV2(maximum, DefaultEventRegistryV2()); err != nil {
				t.Fatalf("maximum direct envelope input rejected: %v", err)
			}
			over := prototype
			setInput(&over, overAmount)
			if _, err := NewEventEnvelopeV2(over, DefaultEventRegistryV2()); !IsKind(err, ErrorBudgetExceeded) {
				t.Fatalf("maximum-plus-one direct envelope input returned %v", err)
			}
		})
	}
}

func TestCheckpointValueClassExactAndPlusOneBudget(t *testing.T) {
	canonicalAt := func(size int) []byte {
		return []byte(`{"x":"` + strings.Repeat("x", size-len(`{"x":""}`)) + `"}`)
	}
	exact := fixturePlanInput(t)
	exact.Checkpoints[0].Canonical = canonicalAt(MaxCheckpointValueBytes)
	if _, err := SealCommitPlanV2(exact, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("exact checkpoint budget rejected: %v", err)
	}
	over := fixturePlanInput(t)
	over.Checkpoints[0].Canonical = canonicalAt(MaxCheckpointValueBytes + 1)
	if _, err := SealCommitPlanV2(over, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("checkpoint maximum-plus-one reached a later seam: %v", err)
	}
	ordinaryOver := fixturePlanInput(t)
	ordinaryOver.Checkpoints[0].SlotCanonical = canonicalAt(MaxCanonicalPayloadBytes + 1)
	if _, err := SealCommitPlanV2(ordinaryOver, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("ordinary slot maximum-plus-one reached a later seam: %v", err)
	}
}

func TestSealedRecordFinalBudgetExactAndPlusOne(t *testing.T) {
	canonicalAt := func(size int) []byte {
		return []byte(`{"x":"` + strings.Repeat("x", size-len(`{"x":""}`)) + `"}`)
	}
	ordinary := []SealedRecordV2{{valueClass: RecordValueOrdinary, canonical: canonicalAt(MaxCanonicalPayloadBytes)}}
	if _, err := validateSealedRecordBudget(ordinary); err != nil {
		t.Fatalf("exact ordinary record rejected: %v", err)
	}
	ordinary[0].canonical = canonicalAt(MaxCanonicalPayloadBytes + 1)
	if _, err := validateSealedRecordBudget(ordinary); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "ordinary_record_value" {
		t.Fatalf("ordinary record maximum-plus-one accepted: %v", err)
	}
	checkpoint := []SealedRecordV2{{valueClass: RecordValueCheckpoint, canonical: canonicalAt(MaxCheckpointValueBytes)}}
	if _, err := validateSealedRecordBudget(checkpoint); err != nil {
		t.Fatalf("exact checkpoint record rejected: %v", err)
	}
	checkpoint[0].canonical = canonicalAt(MaxCheckpointValueBytes + 1)
	if _, err := validateSealedRecordBudget(checkpoint); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "checkpoint_record_value" {
		t.Fatalf("checkpoint record maximum-plus-one accepted: %v", err)
	}

	const ordinaryRecords = 8
	records := make([]SealedRecordV2, ordinaryRecords+1)
	records[0] = SealedRecordV2{valueClass: RecordValueCheckpoint, canonical: canonicalAt(MaxCheckpointValueBytes)}
	remaining := MaxProductRawBatchBytes - MaxCheckpointValueBytes - len(records)*MaxProductKeyBytes
	for index := 1; index < len(records); index++ {
		size := remaining / ordinaryRecords
		if index <= remaining%ordinaryRecords {
			size++
		}
		records[index] = SealedRecordV2{valueClass: RecordValueOrdinary, canonical: canonicalAt(size)}
	}
	if size, err := validateSealedRecordBudget(records); err != nil || size != MaxProductRawBatchBytes {
		t.Fatalf("exact product raw batch rejected: size=%d err=%v", size, err)
	}
	last := len(records) - 1
	records[last].canonical = canonicalAt(len(records[last].canonical) + 1)
	if _, err := validateSealedRecordBudget(records); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "product_raw_batch_bytes" {
		t.Fatalf("product raw batch maximum-plus-one accepted: %v", err)
	}

	const reproducedProjectionBytes = 9341911
	reproduction := make([]SealedRecordV2, 22)
	reproduction[0] = SealedRecordV2{valueClass: RecordValueCheckpoint, canonical: canonicalAt(MaxCheckpointValueBytes)}
	reproductionValues := reproducedProjectionBytes - len(reproduction)*MaxProductKeyBytes - MaxCheckpointValueBytes
	for index := 1; index < len(reproduction); index++ {
		size := reproductionValues / (len(reproduction) - 1)
		if index <= reproductionValues%(len(reproduction)-1) {
			size++
		}
		reproduction[index] = SealedRecordV2{valueClass: RecordValueOrdinary, canonical: canonicalAt(size)}
	}
	if _, err := validateSealedRecordBudget(reproduction); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "product_raw_batch_bytes" {
		t.Fatalf("9,341,911-byte projection reproduction escaped: %v", err)
	}
}

func TestConservativeProductBatchBoundaryAndDerivedEventReproduction(t *testing.T) {
	canonicalAt := func(size int) []byte {
		return []byte(`{"x":"` + strings.Repeat("x", size-len(`{"x":""}`)) + `"}`)
	}
	oversizedEvent := fixturePlanInput(t)
	oversizedEvent.Events[0].Payload = canonicalAt(MaxCanonicalPayloadBytes)
	if _, err := conservativeProductBatchSize(oversizedEvent); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "ordinary_record_value" {
		t.Fatalf("derived ordinary-event overflow escaped preflight: %v", err)
	}
	if _, err := SealCommitPlanV2(oversizedEvent, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "ordinary_record_value" {
		t.Fatalf("derived ordinary-event overflow reached registry: %v", err)
	}
	prototype, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	eventInput := prototype.events[0].input
	eventInput.Payload = canonicalAt(MaxCanonicalPayloadBytes)
	eventInput.Observations = prototype.events[0].Observations()
	derived, err := NewEventEnvelopeV2(eventInput, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeEventEnvelopeV2(derived)
	if err != nil || len(encoded) <= MaxCanonicalPayloadBytes {
		t.Fatalf("350165-byte reproduction not established: size=%d err=%v", len(encoded), err)
	}

	exact := fixturePlanInput(t)
	exact.Checkpoints = []CheckpointRecord{
		{CheckpointID: "checkpoint-a", Canonical: []byte(`{}`), SlotID: "slot-a", SlotCanonical: []byte(`{}`)},
		{CheckpointID: "checkpoint-b", Canonical: []byte(`{}`), SlotID: "slot-b", SlotCanonical: []byte(`{}`)},
	}
	exact.Conditions = persistenceConditions(t, exact.Heads, exact.EligibilityGuards, exact.Checkpoints)
	base, err := conservativeProductBatchSize(exact)
	if err != nil || base >= MaxProductRawBatchBytes {
		t.Fatalf("invalid conservative-boundary base=%d err=%v", base, err)
	}
	increase := MaxProductRawBatchBytes - int(base)
	firstIncrease := increase / 2
	secondIncrease := increase - firstIncrease
	exact.Checkpoints[0].Canonical = canonicalAt(len(exact.Checkpoints[0].Canonical) + firstIncrease)
	exact.Checkpoints[1].Canonical = canonicalAt(len(exact.Checkpoints[1].Canonical) + secondIncrease)
	if len(exact.Checkpoints[0].Canonical) > MaxCheckpointValueBytes || len(exact.Checkpoints[1].Canonical) > MaxCheckpointValueBytes {
		t.Fatal("conservative boundary fixture exceeded checkpoint value class")
	}
	if size, err := conservativeProductBatchSize(exact); err != nil || size != MaxProductRawBatchBytes {
		t.Fatalf("exact conservative product batch rejected: size=%d err=%v", size, err)
	}
	if _, err := SealCommitPlanV2(exact, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("exact conservative product batch did not seal: %v", err)
	}
	over := exact
	over.Checkpoints = cloneCheckpoints(exact.Checkpoints)
	over.Checkpoints[1].Canonical = canonicalAt(len(over.Checkpoints[1].Canonical) + 1)
	if _, err := SealCommitPlanV2(over, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "product_raw_batch_bytes" {
		t.Fatalf("conservative product batch maximum-plus-one reached registry: %v", err)
	}
}

func TestHeadCardinalityExactAndPlusOne(t *testing.T) {
	exact := fixtureSizedPlan(t, MaxHeadsPerPublication, len(`{"x":""}`))
	if _, err := SealCommitPlanV2(exact, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("exact head cardinality rejected: %v", err)
	}
	over := fixtureSizedPlan(t, MaxHeadsPerPublication+1, len(`{"x":""}`))
	if _, err := SealCommitPlanV2(over, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("head cardinality maximum-plus-one reached registry: %v", err)
	}
}

func TestWholePlanProspectiveEncodingBudgetPrecedesLaterSeams(t *testing.T) {
	const eventCount = 32
	low, high := len(`{"x":""}`), MaxCanonicalPayloadBytes
	for low < high {
		middle := low + (high-low+1)/2
		candidate := fixtureSizedPlan(t, eventCount, middle)
		if _, err := prospectiveEncodedPlanSize(candidate); err == nil {
			low = middle
		} else {
			high = middle - 1
		}
	}
	exact := fixtureSizedPlan(t, eventCount, low)
	if low >= MaxCanonicalPayloadBytes {
		t.Fatal("fixture did not reach the whole-plan boundary before a member boundary")
	}
	size, err := prospectiveEncodedPlanSize(exact)
	if err != nil || size > MaxCommitPlanBytes {
		t.Fatalf("largest admitted prospective plan rejected: size=%d err=%v", size, err)
	}
	if _, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2()); err != nil {
		t.Fatalf("ordinary plan did not seal: %v", err)
	}
	if _, err := SealCommitPlanV2(exact, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("consumer budget did not precede the evidence ceiling seam: size=%d payload=%d err=%v", size, low, err)
	}
	over := fixtureSizedPlan(t, eventCount, low+1)
	if _, err := prospectiveEncodedPlanSize(over); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "commit_plan_bytes" {
		t.Fatalf("prospective maximum-plus-one accepted: payload=%d err=%v", low+1, err)
	}
	// An empty registry would fail immediately if budget validation moved after
	// registry resolution. The stricter consumer-record refusal must win at the
	// public seal seam; prospectiveEncodedPlanSize above independently proves
	// the 12 MiB evidence-envelope boundary.
	if _, err := SealCommitPlanV2(over, EventRegistryV2{}); !IsKind(err, ErrorBudgetExceeded) || err.(*Error).Field != "ordinary_record_value" {
		t.Fatalf("maximum-plus-one reached a later seam: %v", err)
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
	eventDescriptor, err := registry.Descriptor()
	if err != nil || eventDescriptor.Digest().IsZero() || len(eventDescriptor.CanonicalBytes()) == 0 {
		t.Fatalf("event descriptor invalid: %v", err)
	}
	reducers, err := NewReducerSetV2(registry, fixtureReducerRegistrations(nil))
	if err != nil {
		t.Fatal(err)
	}
	event := fixtureMissionEnvelope(t, []byte(`{"state":"ready"}`))
	result, digest, err := reducers.Reduce([]byte(`{"prior":1}`), event)
	if err != nil || digest.IsZero() || !bytes.Equal(result, []byte(`{"state":"ready"}`)) {
		t.Fatalf("reducer result=%s digest=%s err=%v", result, digest, err)
	}
	if _, err := registry.Resolve("mission", 2, 1); !IsKind(err, ErrorUnknownEventTuple) {
		t.Fatal("unknown event tuple fell back")
	}
	reducerDescriptor, err := reducers.Descriptor()
	if err != nil || reducerDescriptor.Digest().IsZero() || len(reducerDescriptor.CanonicalBytes()) == 0 {
		t.Fatalf("reducer descriptor invalid: %v", err)
	}
	toggle := false
	nondeterministic, _ := NewReducerSetV2(registry, fixtureReducerRegistrations(func(_ []byte, _ EventEnvelopeV2) ([]byte, error) {
		toggle = !toggle
		if toggle {
			return []byte(`{"n":1}`), nil
		}
		return []byte(`{"n":2}`), nil
	}))
	if _, _, err := nondeterministic.Reduce([]byte(`{}`), event); err != nil {
		t.Fatalf("stateless runtime application rejected: %v", err)
	}
	if err := nondeterministic.QualifyDeterminismV2([]byte(`{}`), event, 4); !IsKind(err, ErrorNondeterministic) {
		t.Fatalf("qualification accepted alternating reducer: %v", err)
	}
}

func TestOperationEventTupleReconstructsExactStateAndAccessors(t *testing.T) {
	state := mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 3, Status: StatusResolved,
		ResultKind: "result.done", ResultSchemaVersion: 2, CanonicalResult: []byte(`{"done":true}`),
		EventIDs: []EventID{"event-01", "event-02"}, EffectIDs: []EffectID{"effect-01"}})
	payload := operationPayload(t, state)
	publicationID := testPublicationID("operation-event")
	reconstructed, err := ReconstructOperationStateV2(payload, publicationID)
	if err != nil {
		t.Fatal(err)
	}
	if reconstructed.SchemaVersion() != SchemaVersion || reconstructed.OperationID() != state.OperationID() ||
		reconstructed.LogicalVersion() != state.LogicalVersion() || reconstructed.Status() != state.Status() ||
		reconstructed.ResultKind() != state.ResultKind() || reconstructed.ResultSchemaVersion() != state.ResultSchemaVersion() ||
		!bytes.Equal(reconstructed.Result(), state.Result()) || reconstructed.ResultDigest() != state.ResultDigest() ||
		!equalIDs(reconstructed.EventIDs(), state.EventIDs()) || !equalIDs(reconstructed.EffectIDs(), state.EffectIDs()) ||
		reconstructed.LastPublicationID() != publicationID {
		t.Fatalf("operation event did not reconstruct exact state: %+v", reconstructed)
	}
	mutated := append([]byte(nil), payload...)
	mutated[len(mutated)-2] ^= 1
	if _, err := ReconstructOperationStateV2(mutated, publicationID); err == nil {
		t.Fatal("mutated operation payload accepted")
	}
	if _, err := DefaultEventRegistryV2().Resolve("operation", 1, 1); !IsKind(err, ErrorUnknownEventTuple) {
		t.Fatalf("obsolete operation tuple accepted: %v", err)
	}

	plan := projectionPlan(t, "operation-02", "mission-02", "planned")
	var envelope EventEnvelopeV2
	for _, candidate := range plan.Events() {
		if candidate.Family() == "operation" {
			envelope = candidate
		}
	}
	if envelope.EnvelopeVersion() != EnvelopeVersion || envelope.EventType() == "" || envelope.EventSchemaVersion() != 2 ||
		envelope.ReducerVersion() != 2 || envelope.CommittedRuntimeEpochID() == "" || envelope.CanonicalDigest().IsZero() {
		t.Fatal("event envelope semantic accessors are incomplete")
	}
	binding := plan.BindingCreate()
	if binding == nil || binding.SchemaVersion() != SchemaVersion || binding.CommandKind() == "" || binding.CommandSchemaVersion() == 0 ||
		binding.IntentDigest().IsZero() || binding.OriginatingEpoch() == "" || binding.ObservedCreatedAt() == "" {
		t.Fatal("operation binding semantic accessors are incomplete")
	}
}

func TestReducerRegistryIsCompleteAndImplementationDigestIsDeclared(t *testing.T) {
	registry := DefaultEventRegistryV2()
	registrations := fixtureReducerRegistrations(nil)
	base, err := NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	baseDescriptor, _ := base.Descriptor()
	missing := append([]ReducerRegistration(nil), registrations[1:]...)
	if _, err := NewReducerSetV2(registry, missing); !IsKind(err, ErrorUnknownEventTuple) {
		t.Fatalf("missing reducer accepted: %v", err)
	}
	owner := append([]ReducerRegistration(nil), registrations...)
	owner[0].Owner = "internal/other"
	if _, err := NewReducerSetV2(registry, owner); !IsKind(err, ErrorUnknownEventTuple) {
		t.Fatalf("wrong reducer owner accepted: %v", err)
	}
	implementation := append([]ReducerRegistration(nil), registrations...)
	implementation[0].ImplementationDigest = digest("alternate.declared.implementation", nil)
	alternate, err := NewReducerSetV2(registry, implementation)
	if err != nil {
		t.Fatal(err)
	}
	alternateDescriptor, _ := alternate.Descriptor()
	if baseDescriptor.Digest() == alternateDescriptor.Digest() {
		t.Fatal("declared implementation digest did not affect compatibility identity")
	}
	functionOnly := append([]ReducerRegistration(nil), registrations...)
	functionOnly[0].Reduce = func(_ []byte, _ EventEnvelopeV2) ([]byte, error) { return []byte(`{"different":"function"}`), nil }
	sameIdentity, err := NewReducerSetV2(registry, functionOnly)
	if err != nil {
		t.Fatal(err)
	}
	sameDescriptor, _ := sameIdentity.Descriptor()
	if baseDescriptor.Digest() != sameDescriptor.Digest() {
		t.Fatal("reducer compatibility identity was derived from Go function identity")
	}
}

func TestStatelessReductionIsolationConcurrencyAndQualification(t *testing.T) {
	registry := DefaultEventRegistryV2()
	prior := []byte(`{"prior":1}`)
	event := fixtureMissionEnvelope(t, []byte(`{"event":1}`))
	isolation, _ := NewReducerSetV2(registry, fixtureReducerRegistrations(func(priorCopy []byte, envelope EventEnvelopeV2) ([]byte, error) {
		priorCopy[0] = 'x'
		eventCopy := envelope.Payload()
		eventCopy[0] = 'x'
		return []byte(`{"stable":true}`), nil
	}))
	output, _, err := isolation.Reduce(prior, event)
	if err != nil || !bytes.Equal(prior, []byte(`{"prior":1}`)) || !bytes.Equal(event.Payload(), []byte(`{"event":1}`)) {
		t.Fatalf("reducer aliased caller input: prior=%s event=%s err=%v", prior, event.Payload(), err)
	}
	output[0] = 'x'
	second, _, err := isolation.Reduce(prior, event)
	if err != nil || !bytes.Equal(second, []byte(`{"stable":true}`)) {
		t.Fatalf("returned buffer aliased subsequent application: output=%s err=%v", second, err)
	}

	freshA, _ := NewReducerSetV2(registry, fixtureReducerRegistrations(nil))
	freshB, _ := NewReducerSetV2(registry, fixtureReducerRegistrations(nil))
	freshEvent := fixtureMissionEnvelope(t, []byte(`{"fresh":true}`))
	left, leftDigest, leftErr := freshA.Reduce([]byte(`{}`), freshEvent)
	right, rightDigest, rightErr := freshB.Reduce([]byte(`{}`), freshEvent)
	if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) || leftDigest != rightDigest {
		t.Fatalf("fresh sessions disagree: left=%s right=%s errors=%v/%v", left, right, leftErr, rightErr)
	}

	concurrent, _ := NewReducerSetV2(registry, fixtureReducerRegistrations(nil))
	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, _, err := concurrent.Reduce([]byte(`{"same":true}`), freshEvent); err != nil {
				t.Errorf("concurrent identical reduction failed: %v", err)
			}
		}()
	}
	wait.Wait()
	bounded, _ := NewReducerSetV2(registry, fixtureReducerRegistrations(nil))
	for index := 0; index < 10_001; index++ {
		priorInput := []byte(fmt.Sprintf(`{"index":%d}`, index))
		if _, _, err := bounded.Reduce(priorInput, freshEvent); err != nil {
			t.Fatalf("stateless application index=%d rejected: %v", index, err)
		}
	}
	if err := bounded.QualifyDeterminismV2([]byte(`{}`), freshEvent, MaxQualificationRuns+1); !IsKind(err, ErrorBudgetExceeded) {
		t.Fatalf("qualification run budget not enforced: %v", err)
	}
}

func fixtureReducerRegistrations(mission Reducer) []ReducerRegistration {
	if mission == nil {
		mission = func(_ []byte, event EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil }
	}
	registrations := make([]ReducerRegistration, 0, len(ExpectedEventTuples()))
	for _, tuple := range ExpectedEventTuples() {
		reducer := Reducer(func(_ []byte, event EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "mission" {
			reducer = mission
		}
		if tuple.Family == "operation" {
			reducer = ReduceOperationStateV2
		}
		implementationDigest := digest("test.reducer.implementation", []byte(tuple.Family))
		if tuple.Family == "operation" {
			implementationDigest = OperationReducerImplementationDigestV2()
		}
		registrations = append(registrations, ReducerRegistration{
			Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion,
			Owner: tuple.Owner, ImplementationDigest: implementationDigest, Reduce: reducer,
		})
	}
	return registrations
}

func fixtureMissionEnvelope(t *testing.T, payload []byte) EventEnvelopeV2 {
	t.Helper()
	coordinate, _ := NewAggregateCoordinate("mission", "mission-01", fixtureScope())
	envelope, err := NewEventEnvelopeV2(EventEnvelopeInput{EventID: "event-reducer", EventType: "mission.changed", Family: "mission",
		EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: 1,
		OperationID: "operation-reducer", OperationLogicalVersion: 1, PublicationID: testPublicationID("reducer"),
		CommittedRuntimeEpochID: "epoch-01", EventIndex: 0, EventCount: 1, Payload: payload}, DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return envelope
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

func TestDurableCodecConsumerPathsPreserveExactCanonicalBytes(t *testing.T) {
	plan, err := SealCommitPlanV2(fixturePlanInput(t), DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.BindingCreate()
	if binding == nil {
		t.Fatal("fixture did not create an operation binding")
	}
	events := plan.Events()
	if len(events) == 0 {
		t.Fatal("fixture did not create events")
	}
	event := events[0]
	headEvent := events[0]
	for _, candidate := range events {
		if len(candidate.input.GovernanceRevisions) != 0 && len(candidate.Observations()) != 0 {
			event = candidate
		}
		if candidate.input.Family == "mission" {
			headEvent = candidate
		}
	}
	state := plan.OperationState()
	witness := plan.PublicationWitness()
	head, err := NewAggregateHeadV2(AggregateHeadInput{
		Coordinate:              headEvent.input.Coordinate,
		LogicalVersion:          headEvent.input.AggregateLogicalVersion,
		LastEventID:             headEvent.input.EventID,
		LastPublicationID:       plan.PublicationID(),
		CommittedRuntimeEpochID: headEvent.input.CommittedRuntimeEpochID,
		CanonicalStateDigest:    digest("durable-codec-head-state", []byte(headEvent.input.EventID)),
		AuthoritySummary:        []byte(`{"nested":{"a":1,"b":2},"status":"accepted"}`),
		Tombstone:               false,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingAddress, _ := NewOperationBindingAddressV2(binding.OperationID())
	eventAddress, _ := NewJournalEventAddressV2(event.input.Coordinate, event.input.AggregateLogicalVersion)
	headAddress, _ := NewAggregateHeadAddressV2(head.Coordinate())
	stateAddress, _ := NewOperationHeadAddressV2(state.OperationID())
	witnessAddress, _ := witness.PublicationID().WitnessAddressV2()
	tests := []struct {
		name    string
		address RecordAddressV2
		encode  func() ([]byte, error)
		decode  func([]byte) ([]byte, error)
	}{
		{"OperationBindingV2", bindingAddress, func() ([]byte, error) { return EncodeOperationBindingV2(*binding) }, func(value []byte) ([]byte, error) {
			decoded, err := DecodeOperationBindingV2(value)
			if err != nil {
				return nil, err
			}
			return EncodeOperationBindingV2(decoded)
		}},
		{"EventEnvelopeV2", eventAddress, func() ([]byte, error) { return EncodeEventEnvelopeV2(event) }, func(value []byte) ([]byte, error) {
			decoded, err := DecodeEventEnvelopeV2(value, DefaultEventRegistryV2())
			if err != nil {
				return nil, err
			}
			return EncodeEventEnvelopeV2(decoded)
		}},
		{"AggregateHeadV2", headAddress, func() ([]byte, error) { return EncodeAggregateHeadV2(head) }, func(value []byte) ([]byte, error) {
			decoded, err := DecodeAggregateHeadV2(value)
			if err != nil {
				return nil, err
			}
			return EncodeAggregateHeadV2(decoded)
		}},
		{"OperationStateV2", stateAddress, func() ([]byte, error) { return EncodeOperationStateV2(state) }, func(value []byte) ([]byte, error) {
			decoded, err := DecodeOperationStateV2(value)
			if err != nil {
				return nil, err
			}
			return EncodeOperationStateV2(decoded)
		}},
		{"PublicationWitnessV2", witnessAddress, func() ([]byte, error) { return EncodePublicationWitnessV2(witness) }, func(value []byte) ([]byte, error) {
			decoded, err := DecodePublicationWitnessV2(value)
			if err != nil {
				return nil, err
			}
			return EncodePublicationWitnessV2(decoded)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := test.encode()
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := CanonicalObject(encoded)
			if err != nil || !bytes.Equal(canonical, encoded) {
				t.Fatalf("final encoder bytes are not recursively canonical: %v", err)
			}
			record, err := NewRecoveryRecordV2(test.address, encoded, 17, "")
			if err != nil {
				t.Fatalf("encoded value did not cross recovery boundary: %v", err)
			}
			if !bytes.Equal(record.CanonicalValue(), encoded) {
				t.Fatal("recovery boundary rewrote encoded bytes")
			}
			reencoded, err := test.decode(record.CanonicalValue())
			if err != nil || !bytes.Equal(reencoded, encoded) {
				t.Fatalf("strict semantic round trip changed bytes: %v", err)
			}
			noncanonical := reverseTopLevelObjectForTest(t, encoded)
			if _, err := NewRecoveryRecordV2(test.address, noncanonical, 17, ""); !IsKind(err, ErrorMalformed) {
				t.Fatalf("noncanonical provider evidence was not refused: %v", err)
			}
		})
	}

	summary, err := BuildOperationSummaryV2([]OperationStateV2{state})
	if err != nil {
		t.Fatal(err)
	}
	summaryBytes, err := EncodeOperationSummaryV2(summary)
	if err != nil {
		t.Fatal(err)
	}
	checkpointBytes, err := json.Marshal(struct {
		SchemaVersion    uint32          `json:"schema_version"`
		OperationSummary json.RawMessage `json:"operation_summary"`
	}{2, summaryBytes})
	if err != nil {
		t.Fatal(err)
	}
	checkpointBytes, err = CanonicalObject(checkpointBytes)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint struct {
		OperationSummary json.RawMessage `json:"operation_summary"`
	}
	if err := json.Unmarshal(checkpointBytes, &checkpoint); err != nil {
		t.Fatal(err)
	}
	restoredSummary, err := DecodeOperationSummaryV2(checkpoint.OperationSummary)
	if err != nil || restoredSummary.Digest() != summary.Digest() || ValidateOperationSummaryV2(restoredSummary, []OperationStateV2{state}) != nil {
		t.Fatalf("nested checkpoint summary changed semantics: %v", err)
	}
	if _, err := DecodeOperationSummaryV2(reverseTopLevelObjectForTest(t, summaryBytes)); err == nil {
		t.Fatal("operation summary decoder admitted alternate field order")
	}
}

func TestDurableEventFinalCanonicalizationPreservesDigestPreimage(t *testing.T) {
	event := fixtureMissionEnvelope(t, []byte(`{"nested":{"a":1,"b":2},"value":true}`))
	const expectedDigest = "e5790e93c956897a2b38eb2da3596ce69a6f9ff332510f5346a032c1b51dff5f"
	if got := event.CanonicalDigest().String(); got != expectedDigest {
		t.Fatalf("event digest preimage changed: got=%s expected=%s", got, expectedDigest)
	}
	unsigned, err := event.canonical(false)
	if err != nil || digest(canonicalEventDomain, unsigned) != event.CanonicalDigest() {
		t.Fatalf("event digest no longer uses the established unsigned preimage: %v", err)
	}
	encoded, err := EncodeEventEnvelopeV2(event)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalObject(encoded)
	if err != nil || !bytes.Equal(canonical, encoded) {
		t.Fatalf("final signed event bytes are not recursively canonical: %v", err)
	}
}

func TestRecoveryEventCanonicalScannerRejectsStructuralAndDigestMutants(t *testing.T) {
	registry := DefaultEventRegistryV2()
	event := fixtureMissionEnvelope(t, []byte(`{"canonical_event_sha256":"payload-only","nested":{"value":true}}`))
	encoded, err := EncodeEventEnvelopeV2(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, digest, ok := inspectRecoveryEventCanonicalV2(encoded); !ok || digest != event.CanonicalDigest() {
		t.Fatalf("valid recovery event inspection differs: ok=%t digest=%s want=%s", ok, digest, event.CanonicalDigest())
	}
	if decoded, err := decodeEventEnvelopeV2(encoded, registry, true); err != nil || decoded.CanonicalDigest() != event.CanonicalDigest() {
		t.Fatalf("payload-contained digest key confused recovery decoder: event=%+v err=%v", decoded, err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	canonicalDigestMember := append([]byte(`"canonical_event_sha256":`), fields["canonical_event_sha256"]...)
	duplicateDigest := bytes.Replace(encoded, canonicalDigestMember, append(append(append([]byte(nil), canonicalDigestMember...), ','), canonicalDigestMember...), 1)
	escapedKey := bytes.Replace(encoded, []byte(`"event_id"`), []byte(`"\u0065vent_id"`), 1)
	malformedBoundary := bytes.Replace(encoded, append([]byte{','}, canonicalDigestMember...), canonicalDigestMember, 1)
	reordered := reverseTopLevelObjectForTest(t, encoded)

	unknownFields := maps.Clone(fields)
	unknownFields["zz_unknown"] = json.RawMessage(`true`)
	unknown, _ := json.Marshal(unknownFields)
	nestedFields := maps.Clone(fields)
	nestedFields["zz_nested"] = json.RawMessage(`{"canonical_event_sha256":"lookalike"}`)
	nestedLookalike, _ := json.Marshal(nestedFields)
	wrongDigestFields := maps.Clone(fields)
	wrongDigestFields["canonical_event_sha256"] = json.RawMessage(`"0000000000000000000000000000000000000000000000000000000000000000"`)
	wrongDigest, _ := json.Marshal(wrongDigestFields)

	for name, mutant := range map[string][]byte{
		"unknown_top_level": unknown, "duplicate_digest": duplicateDigest, "nested_lookalike": nestedLookalike,
		"escaped_key": escapedKey, "malformed_digest_boundary": malformedBoundary, "reordered": reordered, "wrong_digest": wrongDigest,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeEventEnvelopeV2(mutant, registry, true); err == nil {
				t.Fatal("recovery event mutant was accepted")
			}
		})
	}

	optionalInput := event.input
	optionalInput.ActorID = "actor-01"
	optionalInput.ProcessID = "process-01"
	optionalInput.WorkRevisionID = "revision-01"
	optionalInput.PolicyRevision = "policy-01"
	optionalInput.GovernanceRevisions = []string{"governance-01"}
	optionalInput.Payload = event.Payload()
	optionalInput.Observations = event.Observations()
	optionalEvent, err := NewEventEnvelopeV2(optionalInput, registry)
	if err != nil {
		t.Fatal(err)
	}
	for name, scope := range map[string]Scope{
		"host":          {Kind: ScopeHost},
		"project":       {Kind: ScopeProject, ProjectID: "project"},
		"mission":       {Kind: ScopeMission, ProjectID: "project", MissionID: "mission"},
		"repository":    {Kind: ScopeRepository, ProjectID: "project", RepositoryID: "repository"},
		"work_revision": {Kind: ScopeWorkRevision, ProjectID: "project", MissionID: "mission", WorkRevisionID: "revision"},
		"work":          {Kind: ScopeWork, ProjectID: "project", MissionID: "mission", WorkRevisionID: "revision", WorkID: "work"},
	} {
		t.Run("valid_optional_"+name, func(t *testing.T) {
			input := optionalInput
			input.Coordinate.Scope = scope
			candidate, err := NewEventEnvelopeV2(input, registry)
			if err != nil {
				t.Fatal(err)
			}
			encodedCandidate, err := EncodeEventEnvelopeV2(candidate)
			if err != nil {
				t.Fatal(err)
			}
			_, inspected, ok := inspectRecoveryEventCanonicalV2(encodedCandidate)
			decoded, decodeErr := decodeEventEnvelopeV2(encodedCandidate, registry, true)
			if !ok || inspected != candidate.CanonicalDigest() || decodeErr != nil || decoded.CanonicalDigest() != candidate.CanonicalDigest() {
				t.Fatalf("valid optional event changed: inspected=%s ok=%t decoded=%+v err=%v", inspected, ok, decoded, decodeErr)
			}
		})
	}
	for name, replacement := range map[string][2][]byte{
		"empty_actor":      {[]byte(`"actor_id":"actor-01"`), []byte(`"actor_id":""`)},
		"empty_process":    {[]byte(`"process_id":"process-01"`), []byte(`"process_id":""`)},
		"empty_work":       {[]byte(`"work_revision_id":"revision-01"`), []byte(`"work_revision_id":""`)},
		"empty_policy":     {[]byte(`"policy_revision":"policy-01"`), []byte(`"policy_revision":""`)},
		"empty_governance": {[]byte(`"governance_revisions":["governance-01"]`), []byte(`"governance_revisions":[]`)},
	} {
		t.Run(name, func(t *testing.T) {
			mutant := eventMutantWithRecomputedDigestForTest(t, optionalEvent, replacement[0], replacement[1])
			if _, err := decodeEventEnvelopeV2(mutant, registry, true); err == nil {
				t.Fatal("recovery event admitted an optional top-level member that its encoder omits")
			}
		})
	}
	for name, replacement := range map[string][2][]byte{
		"wrong_type_actor":      {[]byte(`"actor_id":"actor-01"`), []byte(`"actor_id":1`)},
		"null_governance":       {[]byte(`"governance_revisions":["governance-01"]`), []byte(`"governance_revisions":null`)},
		"wrong_type_governance": {[]byte(`"governance_revisions":["governance-01"]`), []byte(`"governance_revisions":true`)},
	} {
		t.Run(name, func(t *testing.T) {
			mutant := eventMutantWithRecomputedDigestForTest(t, optionalEvent, replacement[0], replacement[1])
			if _, err := decodeEventEnvelopeV2(mutant, registry, true); err == nil {
				t.Fatal("recovery event admitted a wrong-type optional member")
			}
		})
	}

	hostInput := event.input
	hostInput.Coordinate.Scope = Scope{Kind: ScopeHost}
	hostInput.Payload = event.Payload()
	hostInput.Observations = event.Observations()
	hostEvent, err := NewEventEnvelopeV2(hostInput, registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"project_id", "mission_id", "repository_id", "work_revision_id", "work_id"} {
		t.Run("empty_scope_"+name, func(t *testing.T) {
			mutant := eventMutantWithRecomputedDigestForTest(t, hostEvent,
				[]byte(`"scope":{"kind":"host"}`), []byte(`"scope":{"kind":"host","`+name+`":""}`))
			if _, err := decodeEventEnvelopeV2(mutant, registry, true); err == nil {
				t.Fatal("recovery event admitted an optional scope member that its encoder omits")
			}
		})
	}
	t.Run("wrong_type_scope_optional", func(t *testing.T) {
		mutant := eventMutantWithRecomputedDigestForTest(t, hostEvent,
			[]byte(`"scope":{"kind":"host"}`), []byte(`"scope":{"kind":"host","project_id":1}`))
		if _, err := decodeEventEnvelopeV2(mutant, registry, true); err == nil {
			t.Fatal("recovery event admitted a wrong-type scope optional")
		}
	})
}

func eventMutantWithRecomputedDigestForTest(t *testing.T, event EventEnvelopeV2, before, after []byte) []byte {
	t.Helper()
	unsigned, err := event.canonical(false)
	if err != nil || bytes.Count(unsigned, before) != 1 {
		t.Fatalf("cannot mutate unsigned event preimage count=%d err=%v", bytes.Count(unsigned, before), err)
	}
	unsigned = bytes.Replace(unsigned, before, after, 1)
	mutantDigest := digest(canonicalEventDomain, unsigned)
	signed, err := EncodeEventEnvelopeV2(event)
	if err != nil || bytes.Count(signed, before) != 1 {
		t.Fatalf("cannot mutate signed event count=%d err=%v", bytes.Count(signed, before), err)
	}
	signed = bytes.Replace(signed, before, after, 1)
	want := []byte(`"canonical_event_sha256":"` + event.CanonicalDigest().String() + `"`)
	replacement := []byte(`"canonical_event_sha256":"` + mutantDigest.String() + `"`)
	if bytes.Count(signed, want) != 1 {
		t.Fatal("cannot replace signed event digest")
	}
	return bytes.Replace(signed, want, replacement, 1)
}

func TestRecoveryOperationPreimageScannerRejectsStructuralAndOmissionMutants(t *testing.T) {
	publicationID := testPublicationID("recovery-operation-preimage")
	accepted := fixtureState(t, StatusAccepted, 1, nil, []EventID{"event-01"}, []EffectID{"effect-01"})
	payload := operationPayload(t, accepted)
	if state, err := reconstructOperationStateV2(payload, publicationID, true); err != nil || state.OperationID() != accepted.OperationID() {
		t.Fatalf("valid sealed operation preimage changed: state=%+v err=%v", state, err)
	}
	resolved := fixtureState(t, StatusResolved, 2, []byte(`{"done":true}`), []EventID{"event-01"}, []EffectID{"effect-01"})
	resolvedPayload := operationPayload(t, resolved)
	if state, err := reconstructOperationStateV2(resolvedPayload, publicationID, true); err != nil || state.ResultDigest() != resolved.ResultDigest() {
		t.Fatalf("valid sealed operation optional members changed: state=%+v err=%v", state, err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	unknownFields := maps.Clone(fields)
	unknownFields["zz_unknown"] = json.RawMessage(`true`)
	unknown, _ := json.Marshal(unknownFields)
	operationMember := append([]byte(`"operation_id":`), fields["operation_id"]...)
	duplicate := bytes.Replace(payload, operationMember, append(append(append([]byte(nil), operationMember...), ','), operationMember...), 1)

	mutants := map[string][]byte{
		"unknown": unknown, "duplicate": duplicate, "reordered": reverseTopLevelObjectForTest(t, payload),
	}
	for name, value := range map[string]json.RawMessage{
		"canonical_result":      json.RawMessage(`""`),
		"last_publication_id":   json.RawMessage(`""`),
		"result_kind":           json.RawMessage(`""`),
		"result_schema_version": json.RawMessage(`0`),
		"result_sha256":         json.RawMessage(`""`),
	} {
		candidate := maps.Clone(fields)
		candidate[name] = value
		mutants["present_empty_"+name], _ = json.Marshal(candidate)
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := reconstructOperationStateV2(mutant, publicationID, true); err == nil {
				t.Fatal("sealed operation preimage mutant was accepted")
			}
		})
	}
}

func reverseTopLevelObjectForTest(t *testing.T, encoded []byte) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil || len(fields) < 2 {
		t.Fatalf("cannot build semantically equivalent field-order mutant: %v", err)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	var result bytes.Buffer
	result.WriteByte('{')
	for index, key := range keys {
		if index > 0 {
			result.WriteByte(',')
		}
		keyBytes, _ := json.Marshal(key)
		result.Write(keyBytes)
		result.WriteByte(':')
		result.Write(fields[key])
	}
	result.WriteByte('}')
	if bytes.Equal(result.Bytes(), encoded) {
		t.Fatal("field-order mutant unexpectedly equals canonical input")
	}
	return result.Bytes()
}

func fixtureScope() Scope {
	return Scope{Kind: ScopeMission, ProjectID: "project", MissionID: "mission"}
}

func fixtureEffectPermission(t *testing.T, eventID EventID, effectID EffectID) EffectPermissionCoordinateV2 {
	t.Helper()
	coordinate, err := NewAggregateCoordinate("external_effect", AggregateID(effectID), fixtureScope())
	if err != nil {
		t.Fatal(err)
	}
	permission, err := NewEffectPermissionCoordinateV2(EventDraftV2{EventID: eventID, EventType: "external_effect.allow_retry", Family: "external_effect",
		EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, Payload: []byte(`{"permission":"retry"}`), Observations: []byte(`{"source":"test"}`)}, effectID)
	if err != nil {
		t.Fatal(err)
	}
	return permission
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

func testPublicationID(label string) PublicationID {
	return PublicationID(digest("test.publication", []byte(label)).String())
}

func fixturePlanInput(t *testing.T) CommitPlanInput {
	t.Helper()
	binding := fixtureBinding(t, "epoch-17")
	state := fixtureState(t, StatusAccepted, 1, nil, []EventID{"event-mission", "event-operation"}, []EffectID{"effect-01"})
	missionCoordinate, _ := NewAggregateCoordinate("mission", "mission", fixtureScope())
	operationCoordinate, _ := NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
	events := []EventDraftV2{
		{EventID: "event-operation", EventType: "operation.accepted", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: operationCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", GovernanceRevisions: []string{"gov-01"}, Observations: []byte(`{"source":"test"}`), Payload: operationPayload(t, state)},
		{EventID: "event-mission", EventType: "mission.changed", Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: missionCoordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", Payload: []byte(`{"status":"planned"}`)},
	}
	heads := []HeadTransitionV2{
		{Coordinate: operationCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: "event-operation", CommittedRuntimeEpochID: "epoch-17", CanonicalStateDigest: digest("state", []byte("operation")), AuthoritySummary: []byte(`{"status":"accepted"}`)},
		{Coordinate: missionCoordinate, ExpectedLogicalVersion: 0, NextLogicalVersion: 1, EventID: "event-mission", CommittedRuntimeEpochID: "epoch-17", CanonicalStateDigest: digest("state", []byte("mission")), AuthoritySummary: []byte(`{"status":"planned"}`)},
	}
	input := CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads,
		EffectIntents:     []EffectIntentReference{{EffectID: "effect-01", Digest: digest("effect", []byte("one")), Canonical: []byte(`{"intent":"one"}`)}},
		EligibilityGuards: []EligibilityGuardTransition{{MissionID: "mission", ShardID: "guard-01", Canonical: []byte(`{"eligible":true}`)}},
		Checkpoints:       []CheckpointRecord{{CheckpointID: "checkpoint-01", Canonical: []byte(`{"head":1}`), SlotID: "slot-01", SlotCanonical: []byte(`{"checkpoint_id":"checkpoint-01"}`)}},
	}
	input.Conditions = persistenceConditions(t, input.Heads, input.EligibilityGuards, input.Checkpoints)
	return input
}

func fixtureSizedPlan(t *testing.T, eventCount, payloadSize int) CommitPlanInput {
	t.Helper()
	canonical := []byte(`{"x":"` + strings.Repeat("x", payloadSize-len(`{"x":""}`)) + `"}`)
	binding := fixtureBinding(t, "epoch-17")
	events := make([]EventDraftV2, eventCount)
	heads := make([]HeadTransitionV2, eventCount)
	eventIDs := make([]EventID, eventCount)
	for index := 0; index < eventCount; index++ {
		eventID := EventID(fmt.Sprintf("event-%03d", index))
		family := "mission"
		coordinate, _ := NewAggregateCoordinate("mission", AggregateID(fmt.Sprintf("mission-%03d", index)), fixtureScope())
		eventType := "mission.changed"
		if index == 0 {
			family = "operation"
			coordinate, _ = NewAggregateCoordinate("operation", "operation-01", Scope{Kind: ScopeHost})
			eventType = "operation.accepted"
		}
		eventSchema, reducerVersion := uint32(1), uint32(1)
		if family == "operation" {
			eventSchema, reducerVersion = 2, 2
		}
		events[index] = EventDraftV2{EventID: eventID, EventType: eventType, Family: family, EventSchemaVersion: eventSchema, ReducerVersion: reducerVersion,
			Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-17", Observations: canonical, Payload: canonical}
		heads[index] = HeadTransitionV2{Coordinate: coordinate, NextLogicalVersion: 1, EventID: eventID, CommittedRuntimeEpochID: "epoch-17",
			CanonicalStateDigest: digest("state", []byte(eventID)), AuthoritySummary: []byte(`{"state":"current"}`)}
		eventIDs[index] = eventID
	}
	state := mustState(t, OperationStateInput{OperationID: "operation-01", LogicalVersion: 1, Status: StatusAccepted, EventIDs: eventIDs})
	events[0].Payload = operationPayload(t, state)
	return CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: events, Heads: heads, Conditions: headConditions(t, heads)}
}

func operationPayload(t *testing.T, state OperationStateV2) []byte {
	t.Helper()
	payload, err := EncodeOperationStateEventPayloadV2(state)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func mustHeadCondition(t *testing.T, head HeadTransitionV2) ProviderCondition {
	t.Helper()
	condition, err := ConditionForHeadTransitionV2(head)
	if err != nil {
		t.Fatal(err)
	}
	return condition
}

func headConditions(t *testing.T, heads []HeadTransitionV2) []ProviderCondition {
	t.Helper()
	conditions := make([]ProviderCondition, len(heads))
	for index, head := range heads {
		conditions[index] = mustHeadCondition(t, head)
	}
	return conditions
}

func persistenceConditions(t *testing.T, heads []HeadTransitionV2, guards []EligibilityGuardTransition, checkpoints []CheckpointRecord) []ProviderCondition {
	t.Helper()
	conditions := headConditions(t, heads)
	for _, guard := range guards {
		condition, err := ConditionForEligibilityGuardTransitionV2(guard)
		if err != nil {
			t.Fatal(err)
		}
		conditions = append(conditions, condition)
	}
	for _, checkpoint := range checkpoints {
		condition, err := ConditionForCheckpointRecordV2(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		conditions = append(conditions, condition)
	}
	return conditions
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

func TestCanonicalObjectFastPathMatchesReferenceBoundary(t *testing.T) {
	sawFast := false
	inputs := [][]byte{
		[]byte(`{"a":1,"b":{"c":true,"d":[null,"plain",-0,1.0,1e2,1E+2]}}`),
		[]byte(`{"a":-0,"b":0,"c":0.0,"d":1e-9,"e":9E+9}`),
		[]byte(`{"é":1,"界":2}`),
		[]byte(`{"b":1,"a":2}`),
		[]byte(`{"a":1,"a":2}`),
		[]byte(`{"a":{"b":1,"b":2}}`),
		[]byte(`{"a":"line\\n"}`),
		[]byte(`{"a":"\\u0061"}`),
		[]byte(`{"a":"<html>"}`),
		[]byte("{\"a\":\"\u2028\"}"),
		[]byte(` {"a":1}`),
		[]byte("{\n\"a\":1}"),
		[]byte(`{"a" :1}`),
		[]byte(`{"a":01}`),
		[]byte(`{"a":+1}`),
		[]byte(`{"a":1.}`),
		[]byte(`{"a":NaN}`),
		[]byte(`[]`),
		{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'},
	}
	for _, encoded := range inputs {
		valid := utf8.Valid(encoded) && json.Valid(encoded)
		fast := valid && canonicalObjectFastPath(encoded)
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		value, err := decodeUniqueValue(decoder)
		if err != nil || !valid {
			if fast {
				t.Fatalf("fast path admitted invalid input %q", encoded)
			}
			continue
		}
		object, objectOK := value.(map[string]any)
		canonical, marshalErr := json.Marshal(object)
		wantFast := objectOK && marshalErr == nil && bytes.Equal(canonical, encoded)
		if fast && !wantFast {
			t.Fatalf("fast path admitted noncanonical input=%q canonical=%q", encoded, canonical)
		}
		got, gotErr := CanonicalObject(encoded)
		if objectOK && marshalErr == nil {
			if gotErr != nil || !bytes.Equal(got, canonical) {
				t.Fatalf("canonical equivalence input=%q got=%q err=%v want=%q", encoded, got, gotErr, canonical)
			}
		} else if gotErr == nil {
			t.Fatalf("non-object canonical input accepted: %q", encoded)
		}
		sawFast = sawFast || fast
	}
	if !sawFast {
		t.Fatal("fast path did not admit the canonical no-escape control")
	}
}

func TestTakeRecoveryRecordValidatesBeforeSealingAndKeepsPublicCopiesDefensive(t *testing.T) {
	address, err := NewOperationBindingAddressV2("operation-owned-record")
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"reordered":   []byte(`{"z":1,"a":2}`),
		"duplicate":   []byte(`{"a":1,"a":1}`),
		"escaped_key": []byte(`{"\u0061":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			invalidBytes := append([]byte(nil), input...)
			if _, err := TakeRecoveryRecordV2(address, &invalidBytes, 1, ""); err == nil || invalidBytes == nil {
				t.Fatal("unchecked or consumed invalid provider bytes")
			}
		})
	}
	callerBytes := []byte(`{"a":1}`)
	defensive, err := NewRecoveryRecordV2(address, callerBytes, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	callerBytes[2] = 'x'
	if string(defensive.CanonicalValue()) != `{"a":1}` {
		t.Fatal("public constructor retained caller-owned canonical bytes")
	}
	owned := []byte(`{"a":1}`)
	record, err := TakeRecoveryRecordV2(address, &owned, 1, "")
	if err != nil || owned != nil {
		t.Fatalf("owned seal err=%v retained_input=%t", err, owned != nil)
	}
	public := record.CanonicalValue()
	public[2] = 'x'
	if string(record.CanonicalValue()) != `{"a":1}` {
		t.Fatal("public canonical bytes mutated the sealed record")
	}
}
