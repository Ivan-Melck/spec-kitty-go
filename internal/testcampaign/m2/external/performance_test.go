package external

import (
	"slices"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestPerformanceChildEnvironmentSuppliesExactProductionAuthorityExecutable(t *testing.T) {
	config := PerformanceConfig{
		Executable:          "/admitted/gapdbd",
		RunID:               "run-1",
		Reference:           "/evidence/reference.json",
		QualifierExecutable: "/bin/qualifier",
		ProductExecutable:   "/bin/spec-kitty",
	}
	environment := performanceChildEnvironment([]string{
		"PATH=/usr/bin",
		"SPEC_KITTY_WP13_GAPDBD=/stale/wp13",
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE=/stale/wp14",
		"SPEC_KITTY_WP15_GAPDBD=/stale/wp15",
	}, config, "/private/performance.json")

	want := map[string]string{
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE": "/admitted/gapdbd",
		"SPEC_KITTY_WP15_GAPDBD":               "/admitted/gapdbd",
	}
	counts := map[string]int{}
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "SPEC_KITTY_WP13_GAPDBD" {
			t.Fatal("performance child retained a legacy inherited authority executable")
		}
		if expected, exists := want[key]; exists {
			counts[key]++
			if value != expected {
				t.Fatalf("%s=%q want %q", key, value, expected)
			}
		}
	}
	for key := range want {
		if counts[key] != 1 {
			t.Fatalf("%s occurrence count=%d want=1", key, counts[key])
		}
	}
}

func TestPerformanceChildCommandHasExplicitBoundedTimeoutAndExactSelector(t *testing.T) {
	want := []string{"test", "-json", "./internal/scenario", "-run", "^TestM2AcceptedAuthorityPerformanceCampaign$", "-count=1", "-timeout=1h0m0s"}
	if got := performanceTestArguments(60 * time.Minute); !slices.Equal(got, want) {
		t.Fatalf("performance child argv=%q want=%q", got, want)
	}
}

func TestPerformanceRecoveryAllowsOnlyStructurallyBoundCheckpointBootstrap(t *testing.T) {
	intent, err := journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{
		CommandKind: "checkpoint.write", CommandSchemaVersion: 2,
		Scope: journalv2.Scope{Kind: journalv2.ScopeHost}, Command: []byte(`{"checkpoint_id":"checkpoint-1"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingInput := journalv2.OperationBindingInput{
		OperationID: "arbitrary-unmeasured-operation", CommandKind: "checkpoint.write", CommandSchemaVersion: 2,
		CanonicalIntentDigest: intent, Scope: journalv2.Scope{Kind: journalv2.ScopeHost},
		ActorID: "runtime-checkpoint", ProcessID: "runtime-checkpoint", DoctrineRevision: "runtime-v2",
		ProfileRevision: "runtime-v2", PolicyRevision: "runtime-v2", OriginatingRuntimeEpochID: "epoch-1",
		ObservedCreatedAt: "1970-01-01T00:00:00Z",
	}
	binding, err := journalv2.NewOperationBindingV2(bindingInput)
	if err != nil {
		t.Fatal(err)
	}
	operation := RecoveredOperation{
		Families: []string{"family/operation"}, PublicationIDs: []string{strings.Repeat("a", 64)},
		EventTypes: []string{"operation.accepted"}, ActorIDs: []string{"runtime-checkpoint"},
		ProcessIDs: []string{"runtime-checkpoint"}, RuntimeEpochIDs: []string{"epoch-1"},
		PolicyRevisions: []string{"runtime-v2"},
	}
	if !isPerformanceBootstrapOperation(operation, binding) {
		t.Fatal("exact durable checkpoint provenance was not recognized")
	}

	// Substitution mutant: an arbitrary operation-only command cannot replace
	// a missing measured operation even when every envelope label is copied.
	mutantInput := bindingInput
	mutantInput.CommandKind = "mission.activity"
	mutantInput.CanonicalIntentDigest, err = journalv2.CanonicalIntentDigest(journalv2.StableIntentInput{
		CommandKind: mutantInput.CommandKind, CommandSchemaVersion: 2,
		Scope: mutantInput.Scope, Command: []byte(`{"activity":"substitution"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	mutant, err := journalv2.NewOperationBindingV2(mutantInput)
	if err != nil {
		t.Fatal(err)
	}
	if isPerformanceBootstrapOperation(operation, mutant) {
		t.Fatal("arbitrary operation-only substitution balanced the exact measured census")
	}

	wrongEpoch := operation
	wrongEpoch.RuntimeEpochIDs = []string{"epoch-2"}
	if isPerformanceBootstrapOperation(wrongEpoch, binding) {
		t.Fatal("checkpoint envelope/binding epoch mismatch was accepted")
	}

	actual := map[string]RecoveredOperation{
		"measured-1": operation,
		"substitute": operation,
	}
	bindings := map[string]journalv2.OperationBindingV2{"substitute": mutant}
	if err := validatePerformanceOperationCensus(actual, []string{"measured-1"}, func(operation string) (journalv2.OperationBindingV2, error) {
		return bindings[operation], nil
	}); err == nil {
		t.Fatal("arbitrary extra operation balanced the exact measured census")
	}
	delete(actual, "measured-1")
	bindings["substitute"] = binding
	if err := validatePerformanceOperationCensus(actual, []string{"measured-1"}, func(operation string) (journalv2.OperationBindingV2, error) {
		return bindings[operation], nil
	}); err == nil || !strings.Contains(err.Error(), "omitted measured operation") {
		t.Fatalf("missing measured operation was balanced by valid bootstrap: %v", err)
	}
}
