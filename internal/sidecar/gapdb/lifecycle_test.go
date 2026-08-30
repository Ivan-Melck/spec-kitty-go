package gapdb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"golang.org/x/sys/unix"
)

const testPolicyDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestLifecycleExactBinaryStatusHealthAndConfig(t *testing.T) {
	lifecycle := startTestLifecycle(t, "exact")
	defer stopTestLifecycle(t, lifecycle)
	provenance := lifecycle.Provenance()
	if provenance.Executable.SHA256 == "" || provenance.Process.PID <= 0 || provenance.Process.StartTicks == 0 ||
		provenance.Process.Device != provenance.Executable.Device || provenance.Process.Inode != provenance.Executable.Inode {
		t.Fatalf("host provenance incomplete: %+v", provenance)
	}
	if provenance.WireVersion != 1 || provenance.StorageVersion != 1 || provenance.WireReferenceSHA256 == "" ||
		provenance.StorageReferenceSHA256 == "" || provenance.StdoutSHA256 == "" || provenance.StderrSHA256 == "" {
		t.Fatalf("pinned or bounded evidence missing: %+v", provenance)
	}
	if provenance.ConfiguredSocket != provenance.ConnectedSocket || provenance.DatabaseID == "" ||
		provenance.Observation.Status.DatabaseID != provenance.DatabaseID ||
		provenance.Observation.Status.OwnerPID != provenance.Process.PID ||
		!provenance.Observation.Health.Healthy || provenance.Observation.DescribeConfig.Source == "" {
		t.Fatalf("composite provenance incomplete: %+v", provenance)
	}
	if !provenance.Readiness.ByteRecovered || !provenance.Readiness.Healthy || !provenance.Readiness.Configured ||
		!provenance.Readiness.Admitted || provenance.Readiness.ProductRecovered || provenance.Readiness.ReadyForProductRecovery {
		t.Fatalf("byte recovery improperly became product readiness: %+v", provenance.Readiness)
	}
	if err := lifecycle.MarkProductRecovered(controljournal.ValidatedJournalV1{}); !IsFailure(err, FailureReadiness) {
		t.Fatalf("zero journal promoted readiness: %v", err)
	}
	registry, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := controljournal.ValidateJournalV1(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.MarkProductRecovered(journal); err != nil {
		t.Fatal(err)
	}
	ready := lifecycle.Provenance().Readiness
	if !ready.ProductRecovered || !ready.ReadyForProductRecovery {
		t.Fatalf("product recovery was not recorded: %+v", ready)
	}
}

func TestLifecycleProvenanceEvidenceConformsToFrozenSchema(t *testing.T) {
	lifecycle := startTestLifecycle(t, "evidence")
	defer stopTestLifecycle(t, lifecycle)
	evidence, err := lifecycle.Evidence()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Clean(filepath.Join(filepath.Dir(admissionRecordPath(t)), "..", "..", "..", "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", "sidecar-provenance-v1.schema.json"))
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	id := schemaDocument.(map[string]any)["$id"].(string)
	if err := compiler.AddResource(id, schemaDocument); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(document); err != nil {
		t.Fatalf("evidence=%s: %v", encoded, err)
	}
}

func TestLifecycleConcurrentOwnersReceiveDistinctPrivateEndpoints(t *testing.T) {
	runtimeRoot := privateShortRoot(t)
	configA := testLifecycleConfig(t, runtimeRoot, "a")
	configB := testLifecycleConfig(t, runtimeRoot, "b")
	var first, second *Lifecycle
	var firstErr, secondErr error
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); first, firstErr = Start(t.Context(), configA) }()
	go func() { defer group.Done(); second, secondErr = Start(t.Context(), configB) }()
	group.Wait()
	if firstErr != nil || secondErr != nil {
		if first != nil {
			_ = first.Stop(context.Background())
		}
		if second != nil {
			_ = second.Stop(context.Background())
		}
		t.Fatalf("first=%v second=%v", firstErr, secondErr)
	}
	defer stopTestLifecycle(t, first)
	defer stopTestLifecycle(t, second)
	firstProof, secondProof := first.Provenance(), second.Provenance()
	if firstProof.ConfiguredSocket == secondProof.ConfiguredSocket || firstProof.RuntimeDirectory == secondProof.RuntimeDirectory || firstProof.DatabaseID == secondProof.DatabaseID {
		t.Fatalf("owners collided: first=%+v second=%+v", firstProof, secondProof)
	}
}

func TestLifecyclePIDReuseAndForeignProcessAreRefused(t *testing.T) {
	start, err := processStartTicks(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := &Lifecycle{pidfd: -1, process: ProcessIdentity{PID: os.Getpid(), StartTicks: start + 1}, provenance: SidecarProvenance{ConfiguredSocket: "/not-owned"}}
	diagnostic := lifecycle.InspectOwnership()
	if diagnostic.State != OwnershipForeignLive || len(diagnostic.Recovery) == 0 {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	if err := lifecycle.Stop(t.Context()); !IsFailure(err, FailureOwnership) {
		t.Fatalf("PID-only stop err=%v", err)
	}
	// The test process is necessarily still alive; a PID-only implementation
	// would have signalled it and terminated the test binary.
	if _, err := processStartTicks(os.Getpid()); err != nil {
		t.Fatalf("unrelated process was affected: %v", err)
	}
}

func TestLifecycleStaleArtifactReconciliationAndCleanup(t *testing.T) {
	lifecycle := startTestLifecycle(t, "stale")
	provenance := lifecycle.Provenance()
	lifecycle.mu.Lock()
	if err := lifecycle.signalLocked(unix.SIGKILL); err != nil {
		lifecycle.mu.Unlock()
		t.Fatal(err)
	}
	exit := lifecycle.exit
	lifecycle.mu.Unlock()
	select {
	case <-exit:
	case <-time.After(5 * time.Second):
		t.Fatal("owned process did not exit")
	}
	diagnostic := lifecycle.InspectOwnership()
	if diagnostic.State != OwnershipStaleArtifact {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	if err := lifecycle.CleanupStale(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(provenance.RuntimeDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale runtime remained: %v", err)
	}
	if _, err := os.Stat(provenance.DataDirectory); err != nil {
		t.Fatalf("durable data was removed: %v", err)
	}
}

func TestLifecycleRestartUsesFreshEndpointAndPreservesDatabase(t *testing.T) {
	runtimeRoot := privateShortRoot(t)
	config := testLifecycleConfig(t, runtimeRoot, "restart")
	first, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	firstProof := first.Provenance()
	if err := first.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.SupervisorRunID = "run-restart-2"
	second, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer stopTestLifecycle(t, second)
	secondProof := second.Provenance()
	if firstProof.ConfiguredSocket == secondProof.ConfiguredSocket || firstProof.RuntimeDirectory == secondProof.RuntimeDirectory {
		t.Fatal("restart reused a predecessor endpoint")
	}
	if firstProof.DatabaseID != secondProof.DatabaseID {
		t.Fatalf("database identity changed: %q -> %q", firstProof.DatabaseID, secondProof.DatabaseID)
	}
}

func TestLifecycleAttachReconcilesExactLiveOwnerAfterSupervisorCrash(t *testing.T) {
	runtimeRoot := privateShortRoot(t)
	config := testLifecycleConfig(t, runtimeRoot, "attach")
	original, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	recorded := original.Provenance()
	// Simulate loss of the supervisor capability without signalling or cleaning
	// the child. The command waiter intentionally remains only as a test reaper.
	original.mu.Lock()
	original.closePidfd()
	original.stopped = true
	original.mu.Unlock()
	config.SupervisorRunID = "run-attach-replacement"
	attached, err := Attach(t.Context(), config, recorded)
	if err != nil {
		_ = original.command.Process.Kill()
		t.Fatal(err)
	}
	proof := attached.Provenance()
	if proof.Process != recorded.Process || proof.DatabaseID != recorded.DatabaseID || proof.SupervisorRunID != config.SupervisorRunID ||
		proof.Readiness.ProductRecovered || proof.Readiness.ReadyForProductRecovery {
		t.Fatalf("attached proof=%+v", proof)
	}
	if err := attached.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleAttachRefusesStaleOrLookalikeRecord(t *testing.T) {
	lifecycle := startTestLifecycle(t, "attach-refuse")
	defer stopTestLifecycle(t, lifecycle)
	recorded := lifecycle.Provenance()
	recorded.Process.StartTicks++
	config := lifecycle.config
	config.SupervisorRunID = "run-attach-refused"
	if _, err := Attach(t.Context(), config, recorded); !IsFailure(err, FailureOwnership) {
		t.Fatalf("lookalike attachment err=%v", err)
	}
}

func TestLifecycleCleanupPreservesForeignArtifacts(t *testing.T) {
	lifecycle := startTestLifecycle(t, "cleanup")
	provenance := lifecycle.Provenance()
	foreign := filepath.Join(provenance.RuntimeDirectory, "operator-note")
	if err := os.WriteFile(foreign, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := lifecycle.Stop(t.Context())
	if !IsFailure(err, FailureCleanup) {
		t.Fatalf("unsafe cleanup err=%v", err)
	}
	if encoded, readErr := os.ReadFile(foreign); readErr != nil || string(encoded) != "preserve" {
		t.Fatalf("foreign artifact was changed: %q %v", encoded, readErr)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.cleanupRuntime(true); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleStopIsIdempotentAndPreservesData(t *testing.T) {
	lifecycle := startTestLifecycle(t, "stop")
	provenance := lifecycle.Provenance()
	if err := lifecycle.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(provenance.RuntimeDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime not removed: %v", err)
	}
	if info, err := os.Stat(provenance.DataDirectory); err != nil || !info.IsDir() {
		t.Fatalf("data directory not preserved: %v", err)
	}
}

func TestLifecycleStatusHealthConfigMisattributionMutantsFail(t *testing.T) {
	now := time.Now().UTC()
	process := ProcessIdentity{PID: 41, StartTicks: 7}
	limits := gapdbapi.DefaultOptions().Limits
	status := gapdbapi.StatusResult{
		Lifecycle: gapdbapi.LifecycleReady, DatabaseID: "database", OwnerPID: 41,
		OwnerStartedAt: now.Format(time.RFC3339Nano), CurrentRevision: 2,
		DurableThroughRevision: 2, ReservedRevisionEnd: 100, Limits: limits,
	}
	health := gapdbapi.HealthResult{Lifecycle: gapdbapi.LifecycleReady, Healthy: true, FailingSubsystems: []string{}, SafeActions: []gapdbapi.SafeAction{}}
	configuration := gapdbapi.ConfigResult{Effective: gapdbapi.ConfigValues{Limits: limits}, Source: "defaults", SafeCeilings: limits}
	if err := validateComposite(status, status, health, configuration, process, now.Add(-time.Second), now.Add(time.Second)); err != nil {
		t.Fatalf("valid composite failed: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*gapdbapi.StatusResult, *gapdbapi.StatusResult, *gapdbapi.HealthResult, *gapdbapi.ConfigResult)
	}{
		{"lookalike-pid", func(_, after *gapdbapi.StatusResult, _ *gapdbapi.HealthResult, _ *gapdbapi.ConfigResult) {
			after.OwnerPID++
		}},
		{"stale-status", func(before, after *gapdbapi.StatusResult, _ *gapdbapi.HealthResult, _ *gapdbapi.ConfigResult) {
			before.CurrentRevision = after.CurrentRevision + 1
		}},
		{"health-conflict", func(_ *gapdbapi.StatusResult, _ *gapdbapi.StatusResult, health *gapdbapi.HealthResult, _ *gapdbapi.ConfigResult) {
			health.Healthy = false
		}},
		{"config-conflict", func(_ *gapdbapi.StatusResult, _ *gapdbapi.StatusResult, _ *gapdbapi.HealthResult, config *gapdbapi.ConfigResult) {
			config.Effective.Limits.MaxKeyBytes--
		}},
		{"reported-limit-low", func(before, after *gapdbapi.StatusResult, _ *gapdbapi.HealthResult, config *gapdbapi.ConfigResult) {
			before.Limits.MaxFrameBytes, after.Limits.MaxFrameBytes, config.Effective.Limits.MaxFrameBytes = 1, 1, 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			beforeCopy, afterCopy, healthCopy, configCopy := status, status, health, configuration
			test.mutate(&beforeCopy, &afterCopy, &healthCopy, &configCopy)
			if err := validateComposite(beforeCopy, afterCopy, healthCopy, configCopy, process, now.Add(-time.Second), now.Add(time.Second)); err == nil {
				t.Fatal("misattributed composite accepted")
			}
		})
	}
}

func TestLifecycleTimeoutCleansOnlyOwnedRuntime(t *testing.T) {
	runtimeRoot := privateShortRoot(t)
	foreign := filepath.Join(runtimeRoot, "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := testLifecycleConfig(t, runtimeRoot, "timeout")
	config.StartTimeout = time.Nanosecond
	_, err := Start(t.Context(), config)
	if err == nil {
		t.Fatal("nanosecond readiness unexpectedly succeeded")
	}
	if encoded, readErr := os.ReadFile(foreign); readErr != nil || string(encoded) != "keep" {
		t.Fatalf("foreign file changed: %q %v", encoded, readErr)
	}
}

func startTestLifecycle(t *testing.T, suffix string) *Lifecycle {
	t.Helper()
	lifecycle, err := Start(t.Context(), testLifecycleConfig(t, privateShortRoot(t), suffix))
	if err != nil {
		t.Fatal(err)
	}
	return lifecycle
}

func testLifecycleConfig(t *testing.T, runtimeRoot, suffix string) LifecycleConfig {
	t.Helper()
	dataRoot := privateRoot(t)
	return LifecycleConfig{
		ExecutablePath: safeAdmittedCopy(t), AdmissionRecordPath: admissionRecordPath(t),
		RuntimeRoot: runtimeRoot, DataDirectory: filepath.Join(dataRoot, "database-"+suffix),
		SupervisorRunID: "run-" + suffix, ProductPolicySHA256: testPolicyDigest,
		StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second,
	}
}

func privateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}

func privateShortRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "spk-m1-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func stopTestLifecycle(t *testing.T, lifecycle *Lifecycle) {
	t.Helper()
	if lifecycle != nil {
		if err := lifecycle.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	}
}
