package external

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
)

func TestPerformanceChildEnvironmentSuppliesExactProductionAuthorityExecutable(t *testing.T) {
	config := PerformanceConfig{
		Executable:          "/admitted/gapdbd",
		RunID:               "run-1",
		Reference:           "/evidence/reference.json",
		QualifierExecutable: "/bin/qualifier",
		ProductExecutable:   "/bin/spec-kitty",
		Source:              m2.Source{GoVersion: "go1.26.7"},
	}
	environment := performanceChildEnvironment([]string{
		"PATH=/usr/bin",
		"CGO_ENABLED=1",
		"GOTOOLCHAIN=local",
		"SPEC_KITTY_WP13_GAPDBD=/stale/wp13",
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE=/stale/wp14",
		"SPEC_KITTY_WP15_GAPDBD=/stale/wp15",
	}, config, "/private/performance.json")

	want := map[string]string{
		"CGO_ENABLED":                          "0",
		"GOTOOLCHAIN":                          "go1.26.7",
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

func TestPerformanceQuiescenceRetriesOnlyLoadDriftThenPasses(t *testing.T) {
	reference, valid := performanceEnvironmentFixture(t)
	observations := []m2.PerformanceEnvironment{valid, valid}
	observations[0].Load1 = 4.25
	observations[1].Load1 = 3.75
	calls := 0
	err := awaitPerformanceEnvironment(context.Background(), reference, time.Second, time.Millisecond, func() (m2.PerformanceEnvironment, error) {
		observed := observations[calls]
		calls++
		return observed, nil
	})
	if err != nil {
		t.Fatalf("load quiescence refused: %v", err)
	}
	if calls != 2 {
		t.Fatalf("environment observations=%d want=2", calls)
	}
}

func TestPerformanceQuiescenceRefusesNonLoadDriftWithoutRetry(t *testing.T) {
	reference, observed := performanceEnvironmentFixture(t)
	observed.GoVersion = "go1.26.4"
	calls := 0
	err := awaitPerformanceEnvironment(context.Background(), reference, time.Second, time.Millisecond, func() (m2.PerformanceEnvironment, error) {
		calls++
		return observed, nil
	})
	if err == nil || !strings.Contains(err.Error(), "non-load drift") || !strings.Contains(err.Error(), "go_version=go1.26.4") {
		t.Fatalf("non-load refusal=%v", err)
	}
	if calls != 1 {
		t.Fatalf("non-load drift was retried: observations=%d", calls)
	}
}

func TestPerformanceQuiescenceCancellationAndTimeoutRetainLastLoadDiagnostic(t *testing.T) {
	reference, observed := performanceEnvironmentFixture(t)
	observed.Load1 = 6.68
	observe := func() (m2.PerformanceEnvironment, error) { return observed, nil }

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := awaitPerformanceEnvironment(cancelled, reference, time.Second, time.Second, observe)
	if err == nil || !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "load1=6.68") {
		t.Fatalf("cancellation diagnostic=%v", err)
	}

	err = awaitPerformanceEnvironment(context.Background(), reference, 10*time.Millisecond, 2*time.Millisecond, observe)
	if err == nil || !strings.Contains(err.Error(), "timed out") || !strings.Contains(err.Error(), "load1=6.68") {
		t.Fatalf("timeout diagnostic=%v", err)
	}
}

func TestPerformanceQuiescenceRejectsInvalidBounds(t *testing.T) {
	for _, bounds := range [][2]time.Duration{{0, time.Second}, {time.Second, 0}, {time.Second, 2 * time.Second}} {
		if err := AwaitPerformanceEnvironment(context.Background(), PerformanceConfig{}, bounds[0], bounds[1]); err == nil {
			t.Fatalf("invalid bounds accepted: wait=%s poll=%s", bounds[0], bounds[1])
		}
	}
}

func performanceEnvironmentFixture(t *testing.T) ([]byte, m2.PerformanceEnvironment) {
	t.Helper()
	referenceBytes, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "programs", "evidence", "m2-performance-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference m2.PerformanceReference
	if err := m2.DecodeStrict(referenceBytes, &reference); err != nil {
		t.Fatal(err)
	}
	return referenceBytes, m2.PerformanceEnvironment{
		CPUModel: reference.CPUModel, LogicalCPUs: reference.LogicalCPUs, PhysicalCores: reference.PhysicalCores,
		MemoryBytes: reference.MemoryMinBytes, KernelSystem: reference.KernelSystem, KernelRelease: reference.KernelRelease,
		Architecture: reference.Architecture, Filesystem: reference.Filesystem, StorageDevice: reference.StorageDevicePrefix + "0n1p1",
		PowerGovernor: reference.PowerGovernor, BoostEnabled: reference.BoostEnabled, Load1: 0.1,
		GoVersion: reference.GoVersion, CGOEnabled: reference.CGOEnabled, SidecarSHA256: m2.QualificationCandidateSidecarSHA256,
		QualifierSHA256: strings.Repeat("a", 64), ProductSHA256: strings.Repeat("b", 64),
	}
}

func TestPerformanceRecoveryReceiptRejectsMalformedSubstitutedAndStaleEvidence(t *testing.T) {
	request := performanceRecoveryRequestV1{
		SchemaVersion: 1, RunID: "run-1", RunIndex: 3,
		Source:             m2.Source{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)},
		CheckpointRevision: 9, ExpectedOperationIDs: []string{"operation-1"}, DatasetSHA256: strings.Repeat("c", 64),
		IncludeOperations: true, ChildExecutable: "/exact/scenario.test", ChildSHA256: strings.Repeat("d", 64),
	}
	operations := map[string]RecoveredOperation{"operation-1": {Families: []string{"family/operation"}}}
	operationBytes, err := m2.CanonicalJSON(operations)
	if err != nil {
		t.Fatal(err)
	}
	requestSHA := strings.Repeat("e", 64)
	valid := performanceRecoveryReceiptV1{
		SchemaVersion: 1, RunID: request.RunID, RunIndex: request.RunIndex,
		SourceCommit: request.Source.Commit, SourceTree: request.Source.Tree,
		DatasetSHA256: request.DatasetSHA256, RequestSHA256: requestSHA,
		ProcessID: 17, ChildExecutable: request.ChildExecutable, ChildSHA256: request.ChildSHA256,
		StoreRevision: 10, ProjectionSHA256: strings.Repeat("f", 64), ObservedOperations: 1,
		PostCheckpointStates: 1, OperationsSHA256: m2.SHA256(operationBytes), DurationNS: 1,
		RuntimeOperations: []string{}, Operations: operations,
	}
	encoded, err := m2.CanonicalJSON(valid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePerformanceRecoveryReceipt(request, requestSHA, encoded); err != nil {
		t.Fatalf("valid recovery receipt rejected: %v", err)
	}
	summaryRequest := request
	summaryRequest.IncludeOperations = false
	emptyOperations := map[string]RecoveredOperation{}
	emptyBytes, err := m2.CanonicalJSON(emptyOperations)
	if err != nil {
		t.Fatal(err)
	}
	summary := valid
	summary.Operations = emptyOperations
	summary.OperationsSHA256 = m2.SHA256(emptyBytes)
	summaryBytes, err := m2.CanonicalJSON(summary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePerformanceRecoveryReceipt(summaryRequest, requestSHA, summaryBytes); err != nil {
		t.Fatalf("valid summary-only recovery receipt rejected: %v", err)
	}
	if _, err := decodePerformanceRecoveryReceipt(summaryRequest, requestSHA, encoded); err == nil {
		t.Fatal("later recovery child was allowed to repeat the full operation census")
	}
	if _, err := decodePerformanceRecoveryReceiptBounded(request, requestSHA, encoded, len(encoded)-1); err == nil {
		t.Fatal("oversized recovery receipt accepted")
	}
	for name, mutant := range map[string][]byte{
		"truncated": encoded[:len(encoded)/2],
		"malformed": []byte(`{"schema_version":1,}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodePerformanceRecoveryReceipt(request, requestSHA, mutant); err == nil {
				t.Fatal("invalid recovery receipt accepted")
			}
		})
	}
	mutants := map[string]func(*performanceRecoveryReceiptV1){
		"run":                  func(v *performanceRecoveryReceiptV1) { v.RunIndex++ },
		"source":               func(v *performanceRecoveryReceiptV1) { v.SourceTree = strings.Repeat("0", 40) },
		"dataset":              func(v *performanceRecoveryReceiptV1) { v.DatasetSHA256 = strings.Repeat("0", 64) },
		"request":              func(v *performanceRecoveryReceiptV1) { v.RequestSHA256 = strings.Repeat("0", 64) },
		"child_path":           func(v *performanceRecoveryReceiptV1) { v.ChildExecutable = "/substituted/scenario.test" },
		"child_digest":         func(v *performanceRecoveryReceiptV1) { v.ChildSHA256 = strings.Repeat("0", 64) },
		"process":              func(v *performanceRecoveryReceiptV1) { v.ProcessID = 0 },
		"stale_revision":       func(v *performanceRecoveryReceiptV1) { v.StoreRevision = 8 },
		"projection":           func(v *performanceRecoveryReceiptV1) { v.ProjectionSHA256 = "" },
		"missing_operation":    func(v *performanceRecoveryReceiptV1) { v.ObservedOperations = 0 },
		"state_census":         func(v *performanceRecoveryReceiptV1) { v.PostCheckpointStates = 3 },
		"operation_digest":     func(v *performanceRecoveryReceiptV1) { v.OperationsSHA256 = strings.Repeat("0", 64) },
		"runtime_operation":    func(v *performanceRecoveryReceiptV1) { v.RuntimeOperations = []string{"forged"} },
		"nonpositive_duration": func(v *performanceRecoveryReceiptV1) { v.DurationNS = 0 },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			candidateBytes, encodeErr := m2.CanonicalJSON(candidate)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if _, err := decodePerformanceRecoveryReceipt(request, requestSHA, candidateBytes); err == nil {
				t.Fatal("substituted recovery receipt accepted")
			}
		})
	}
}

func TestPerformanceRecoveryArtifactsAreCreateOnceBoundedRegularFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "receipt.json")
	if err := writePerformanceRecoveryCreateOnce(path, []byte(`{"first":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := writePerformanceRecoveryCreateOnce(path, []byte(`{"second":true}`)); err == nil {
		t.Fatal("create-once recovery artifact was overwritten")
	}
	linked := filepath.Join(root, "linked.json")
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := readPerformanceRecoveryArtifact(linked); err == nil {
		t.Fatal("symlink recovery artifact was read")
	}
}

func TestPerformanceRecoveryChildFailureCannotProduceObservation(t *testing.T) {
	root := t.TempDir()
	executable, err := filepath.EvalSymlinks("/bin/false")
	if err != nil {
		t.Fatal(err)
	}
	childSHA, err := digestPerformanceRecoveryExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	config := PerformanceConfig{
		RunID: "run-child-failure", Repository: root, Executable: executable,
		RecoveryExecutable: executable, RuntimeRoot: filepath.Join(root, "runtime"), DataRoot: filepath.Join(root, "data"),
		Source:             m2.Source{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40)},
		CheckpointRevision: 1, ExpectedOperationIDs: []string{"operation-1"}, StartTimeout: time.Second, StopTimeout: time.Second,
	}
	if _, _, err := runPerformanceRecoveryChild(context.Background(), config, "host-1", 0, strings.Repeat("c", 64), childSHA); err == nil || !strings.Contains(err.Error(), "recovery child") {
		t.Fatalf("failed child produced an observation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.RuntimeRoot, "recovery-00", "receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("failed child produced a receipt: %v", err)
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
