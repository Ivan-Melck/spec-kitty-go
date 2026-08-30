package gapdb

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
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
		provenance.Process.Device != provenance.ExecutedImage.Device || provenance.Process.Inode != provenance.ExecutedImage.Inode ||
		provenance.Process.SHA256 != provenance.ExecutedImage.SHA256 || provenance.ExecutedImage.Seals&requiredExecutionSeals != requiredExecutionSeals {
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

func TestLifecycleCleanupRefusesRenamedRuntimeSymlinkReplacement(t *testing.T) {
	lifecycle := startTestLifecycle(t, "cleanup-symlink")
	proof := lifecycle.Provenance()
	moved := proof.RuntimeDirectory + ".moved"
	if err := os.Rename(proof.RuntimeDirectory, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, proof.RuntimeDirectory); err != nil {
		t.Fatal(err)
	}
	err := lifecycle.Stop(t.Context())
	if !IsFailure(err, FailureCleanup) {
		t.Fatalf("symlink replacement cleanup err=%v", err)
	}
	info, statErr := os.Lstat(proof.RuntimeDirectory)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("foreign runtime replacement was changed: %v %+v", statErr, info)
	}
	if _, statErr := os.Stat(moved); statErr != nil {
		t.Fatalf("renamed runtime was removed: %v", statErr)
	}
	_ = os.Remove(proof.RuntimeDirectory)
	_ = os.Remove(filepath.Join(moved, "gapdb.sock"))
	_ = os.Remove(moved)
}

func TestLifecycleObservationRefusesSocketReplacementBetweenEvidenceCalls(t *testing.T) {
	runtimeRoot := privateShortRoot(t)
	config := testLifecycleConfig(t, runtimeRoot, "replace-between-calls")
	config.StartTimeout = 250 * time.Millisecond
	var replacement *net.UnixListener
	replaced := false
	config.observationHook = func(operation string) {
		if replaced || operation != "status" {
			return
		}
		replaced = true
		entries, err := os.ReadDir(runtimeRoot)
		if err != nil || len(entries) != 1 {
			t.Fatalf("runtime discovery: entries=%d err=%v", len(entries), err)
		}
		socket := filepath.Join(runtimeRoot, entries[0].Name(), "gapdb.sock")
		if err := os.Rename(socket, socket+".owned"); err != nil {
			t.Fatal(err)
		}
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		listener.SetUnlinkOnClose(false)
		replacement = listener
	}
	_, err := Start(t.Context(), config)
	if err == nil || !replaced {
		t.Fatalf("socket replacement was admitted: replaced=%v err=%v", replaced, err)
	}
	if replacement != nil {
		_ = replacement.Close()
	}
	entries, _ := os.ReadDir(runtimeRoot)
	for _, entry := range entries {
		directory := filepath.Join(runtimeRoot, entry.Name())
		_ = os.Remove(filepath.Join(directory, "gapdb.sock"))
		_ = os.Remove(filepath.Join(directory, "gapdb.sock.owned"))
		_ = os.Remove(directory)
	}
}

func TestLifecycleStorageTopologyRefusesGitSharedUnknownAndSplitAuthority(t *testing.T) {
	t.Run("git-worktree", func(t *testing.T) {
		repository := privateShortRoot(t)
		if err := os.Mkdir(filepath.Join(repository, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		runtimeRoot := filepath.Join(repository, "runtime")
		if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		config := testLifecycleConfig(t, runtimeRoot, "git")
		config.DataDirectory = filepath.Join(repository, "database")
		if err := normalizeLifecycleConfig(&config); !IsFailure(err, FailureUnsafePath) {
			t.Fatalf("Git storage topology err=%v", err)
		}
	})
	for _, test := range []struct {
		name string
		kind int64
	}{
		{"shared", unix.NFS_SUPER_MAGIC}, {"unknown", 0x7fff1234},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := testLifecycleConfig(t, privateShortRoot(t), test.name)
			config.filesystem = func(string) (filesystemIdentity, error) { return filesystemIdentity{Type: test.kind}, nil }
			if err := normalizeLifecycleConfig(&config); !IsFailure(err, FailureUnsafePath) {
				t.Fatalf("unsupported filesystem err=%v", err)
			}
		})
	}
	t.Run("split-authority", func(t *testing.T) {
		config := testLifecycleConfig(t, privateShortRoot(t), "split")
		calls := 0
		config.filesystem = func(string) (filesystemIdentity, error) {
			calls++
			identity := filesystemIdentity{Type: unix.EXT4_SUPER_MAGIC}
			identity.FSID.Val[0] = int32(calls)
			return identity, nil
		}
		if err := normalizeLifecycleConfig(&config); !IsFailure(err, FailureUnsafePath) {
			t.Fatalf("split filesystem authority err=%v", err)
		}
	})
}

func TestLifecyclePidfdObservationAndSignalingSeams(t *testing.T) {
	executed := ExecutedImageProvenance{SHA256: strings.Repeat("a", 64), Size: 17, Device: 3, Inode: 5}
	identity := fileIdentity{device: 3, inode: 5, size: 17}
	base := systemProcessAPI
	base.startTicks = func(int) (uint64, error) { return 11, nil }
	base.executable = func(int) (fileIdentity, string, error) { return identity, executed.SHA256, nil }
	base.closeFD = func(int) error { return nil }
	t.Run("pidfd-success", func(t *testing.T) {
		api := base
		api.pidfdOpen = func(int, int) (int, error) { return 41, nil }
		process, fd, err := observeStartedProcess(api, 7, executed)
		if err != nil || fd != 41 || process.StartTicks != 11 {
			t.Fatalf("process=%+v fd=%d err=%v", process, fd, err)
		}
	})
	for _, errno := range []error{unix.ENOSYS, unix.EINVAL} {
		t.Run("observation-fallback-"+errno.Error(), func(t *testing.T) {
			api := base
			api.pidfdOpen = func(int, int) (int, error) { return -1, errno }
			_, fd, err := observeStartedProcess(api, 7, executed)
			if err != nil || fd != -1 {
				t.Fatalf("fd=%d err=%v", fd, err)
			}
		})
	}
	t.Run("pidfd-hard-error", func(t *testing.T) {
		api := base
		api.pidfdOpen = func(int, int) (int, error) { return -1, unix.EPERM }
		if _, _, err := observeStartedProcess(api, 7, executed); !IsFailure(err, FailureIdentityUnprovable) {
			t.Fatalf("hard pidfd error=%v", err)
		}
	})
	t.Run("identity-change-around-pidfd", func(t *testing.T) {
		api := base
		calls := 0
		api.startTicks = func(int) (uint64, error) {
			calls++
			return uint64(10 + calls), nil
		}
		api.pidfdOpen = func(int, int) (int, error) { return 42, nil }
		if _, _, err := observeStartedProcess(api, 7, executed); !IsFailure(err, FailureIdentityUnprovable) {
			t.Fatalf("identity drift err=%v", err)
		}
	})
	t.Run("signal-fails-closed-without-pidfd", func(t *testing.T) {
		called := false
		api := base
		api.pidfdSignal = func(int, unix.Signal, *unix.Siginfo, int) error { called = true; return nil }
		lifecycle := &Lifecycle{
			pidfd: -1, processes: api,
			process: ProcessIdentity{PID: 7, StartTicks: 11, Device: 3, Inode: 5, Size: 17, SHA256: executed.SHA256},
		}
		if err := lifecycle.signalLocked(unix.SIGTERM); err == nil || called {
			t.Fatalf("fallback signal err=%v called=%v", err, called)
		}
		if diagnostic := lifecycle.InspectOwnership(); diagnostic.State != OwnershipOwnedLive || len(diagnostic.Recovery) == 0 {
			t.Fatalf("no-pidfd diagnostic=%+v", diagnostic)
		}
	})
	t.Run("pidfd-signals-exact-capability", func(t *testing.T) {
		called := false
		api := base
		api.pidfdSignal = func(fd int, signal unix.Signal, _ *unix.Siginfo, _ int) error {
			called = fd == 43 && signal == unix.SIGTERM
			return nil
		}
		lifecycle := &Lifecycle{pidfd: 43, processes: api}
		if err := lifecycle.signalLocked(unix.SIGTERM); err != nil || !called {
			t.Fatalf("pidfd signal err=%v called=%v", err, called)
		}
	})
}

func TestLifecyclePostStartObservationFailureTerminatesAndReapsChild(t *testing.T) {
	config := testLifecycleConfig(t, privateShortRoot(t), "observe-fail")
	config.processes = systemProcessAPI
	config.processes.startTicks = func(int) (uint64, error) { return 0, errors.New("forced observation failure") }
	pid := 0
	config.commandStarted = func(process *os.Process) { pid = process.Pid }
	_, err := Start(t.Context(), config)
	if !IsFailure(err, FailureIdentityUnprovable) || pid <= 0 {
		t.Fatalf("post-start observation err=%v pid=%d", err, pid)
	}
	if _, err := processStartTicks(pid); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unobserved child remained or was unreaped: %v", err)
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
