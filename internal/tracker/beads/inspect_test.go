package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type inspectionRunner struct {
	workspace  string
	databaseID string
	version    string
	schema     uint16
	mode       tracker.ConcurrencyMode
	readOnly   bool
	serverID   string
	healthy    bool
	malformed  CommandKind
	calls      []CommandKind
}

func (runner *inspectionRunner) run(_ context.Context, request command) commandResult {
	runner.calls = append(runner.calls, request.kind)
	result := commandResult{kind: request.kind, started: true, mutationOutcome: mutationUnknown}
	zero := 0
	result.exitCode = &zero
	var data any
	switch request.kind {
	case CommandVersion:
		data = map[string]any{"version": runner.version, "build": "qualified", "future_optional": true}
	case CommandWhere:
		data = map[string]any{"workspace_path": runner.workspace, "database_id": runner.databaseID, "future_optional": true}
	case CommandInfo:
		data = map[string]any{"protocol_schema": runner.schema, "storage_mode": runner.mode, "read_only": runner.readOnly, "server_id": runner.serverID, "future_optional": true}
	case CommandDoctor:
		data = map[string]any{"healthy": runner.healthy, "server_id": runner.serverID, "future_optional": true}
	default:
		data = map[string]any{"ok": true}
	}
	if runner.malformed == request.kind {
		result.stdout = []byte("not-json")
		return result
	}
	payload, _ := json.Marshal(map[string]any{"schema_version": runner.schema, "data": data})
	result.stdout = payload
	return result
}

type countingLocker struct {
	calls int
	err   error
}
type noopLock struct{}

func (*noopLock) release() {}
func (locker *countingLocker) acquire(context.Context, tracker.TrackerBinding) (acquiredWriterLock, error) {
	locker.calls++
	if locker.err != nil {
		return nil, locker.err
	}
	return &noopLock{}, nil
}

func testBoundAdapter(t *testing.T, mode tracker.ConcurrencyMode, databaseID string) (*BoundAdapter, *inspectionRunner) {
	t.Helper()
	root := t.TempDir()
	executable := testExecutable(t, root)
	workspace := root + "/workspace"
	mustMkdir(t, workspace)
	binding, err := ResolveBinding(BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: databaseID, ProtocolSchema: 1, ConcurrencyMode: mode})
	if err != nil {
		t.Fatal(err)
	}
	runner := &inspectionRunner{workspace: workspace, databaseID: databaseID, version: SupportedBeadsVersion, schema: 1, mode: mode, healthy: true, serverID: "server-1"}
	bound, err := newBoundAdapter(binding, newAdapter(runner), nil)
	if err != nil {
		t.Fatal(err)
	}
	return bound, runner
}

func TestPreflightAndInspectHealthyEmbeddedCapabilities(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if readiness.Binding().WorkspaceIdentity != bound.binding.WorkspaceIdentity || readiness.ServerQualified() {
		t.Fatalf("readiness=%#v", readiness)
	}
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Health != tracker.HealthHealthy || inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersAdapterSerialized || inspection.Capabilities.CatalogUpdate != tracker.UpdateUnguarded || inspection.Capabilities.SafeCompensation != tracker.CompensationUnsupported {
		t.Fatalf("inspection=%#v", inspection)
	}
	for _, kind := range runner.calls {
		if kind == CommandCreate || kind == CommandUpdate || kind == CommandBackupInit {
			t.Fatalf("inspection started mutation %s", kind)
		}
	}
}

func TestInspectQualifiedServerCapabilities(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersProviderNative {
		t.Fatalf("capabilities=%#v", inspection.Capabilities)
	}
}

func TestPreflightRefusesVersionSchemaWorkspaceAndReadOnly(t *testing.T) {
	tests := []struct {
		name string
		edit func(*BoundAdapter, *inspectionRunner)
		kind tracker.ErrorKind
	}{
		{"old version", func(_ *BoundAdapter, r *inspectionRunner) { r.version = "1.2.1" }, tracker.ErrorVersionSkew},
		{"new version", func(_ *BoundAdapter, r *inspectionRunner) { r.version = "1.2.3" }, tracker.ErrorVersionSkew},
		{"future schema", func(_ *BoundAdapter, r *inspectionRunner) { r.schema = 2 }, tracker.ErrorSchemaSkew},
		{"missing schema", func(_ *BoundAdapter, r *inspectionRunner) { r.schema = 0 }, tracker.ErrorSchemaSkew},
		{"workspace redirect", func(_ *BoundAdapter, r *inspectionRunner) {
			r.workspace = r.workspace + "/nested"
			mustMkdir(t, r.workspace)
		}, tracker.ErrorWorkspaceMismatch},
		{"database redirect", func(_ *BoundAdapter, r *inspectionRunner) { r.databaseID = "other" }, tracker.ErrorWorkspaceMismatch},
		{"read only", func(_ *BoundAdapter, r *inspectionRunner) { r.readOnly = true }, tracker.ErrorUnavailable},
		{"malformed", func(_ *BoundAdapter, r *inspectionRunner) { r.malformed = CommandInfo }, tracker.ErrorMalformedProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
			test.edit(bound, runner)
			_, err := bound.Preflight(context.Background())
			if !tracker.IsKind(err, test.kind) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMutationRefusalPrecedesWriterLockAndSubprocess(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	locker := &countingLocker{}
	bound.locker = locker
	request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(runner.calls)
	_, err = bound.executeMutation(context.Background(), Readiness{}, request)
	if !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
		t.Fatalf("error=%v", err)
	}
	if locker.calls != 0 || len(runner.calls) != before {
		t.Fatalf("lock=%d calls=%v", locker.calls, runner.calls[before:])
	}
}

func TestPreflightChangedExecutableFailsDigest(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	if err := os.WriteFile(bound.binding.ExecutablePath, []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err := bound.Preflight(context.Background())
	if !tracker.IsKind(err, tracker.ErrorUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestInspectDegradedDiagnosticsAreStableAndSorted(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	runner.healthy = false
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Health != tracker.HealthUnavailable || inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersNone {
		t.Fatalf("inspection=%#v", inspection)
	}
	diagnostics := inspection.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].RecoveryCode != "repair_provider_server" {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	sorted := append([]tracker.Diagnostic(nil), diagnostics...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Kind < sorted[j].Kind })
	if fmt.Sprint(sorted) != fmt.Sprint(diagnostics) {
		t.Fatal("diagnostics not sorted")
	}
}

func TestPreflightLatencyCharacterization(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	samples := make([]time.Duration, 100)
	for i := range samples {
		started := time.Now()
		if _, err := bound.Preflight(context.Background()); err != nil {
			t.Fatal(err)
		}
		samples[i] = time.Since(started)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p95, max := samples[49], samples[94], samples[99]
	t.Logf("preflight samples=100 p50=%s p95=%s max=%s host=%s/%s go=%s", p50, p95, max, runtime.GOOS, runtime.GOARCH, runtime.Version())
	if p95 >= time.Second {
		t.Fatalf("healthy local fixture p95=%s", p95)
	}
}

func TestInspectionFixturesRemainByteIdentical(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	executableBefore, _ := os.ReadFile(bound.binding.ExecutablePath)
	entriesBefore, err := os.ReadDir(bound.binding.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID}); err != nil {
		t.Fatal(err)
	}
	executableAfter, _ := os.ReadFile(bound.binding.ExecutablePath)
	entriesAfter, _ := os.ReadDir(bound.binding.WorkspacePath)
	if string(executableBefore) != string(executableAfter) || len(entriesBefore) != len(entriesAfter) {
		t.Fatal("inspection mutated fixture")
	}
}

func TestReadinessDetectsExecutableReplacementAfterPreflight(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bound.binding.ExecutablePath, []byte("#!/bin/sh\nexit 9\n"), 0700); err != nil {
		t.Fatal(err)
	}
	request, _ := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	before := len(runner.calls)
	_, err = bound.executeMutation(context.Background(), readiness, request)
	if !tracker.IsKind(err, tracker.ErrorUnavailable) || len(runner.calls) != before {
		t.Fatalf("error=%v calls=%v", err, runner.calls[before:])
	}
}

func TestPortableErrorExtraction(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	_, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: "other"})
	var portable *tracker.Error
	if !errors.As(err, &portable) || portable.RecoveryAction() == "" {
		t.Fatalf("error=%v", err)
	}
}
