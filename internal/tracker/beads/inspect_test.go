package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type inspectionRunner struct {
	workspace      string
	databasePath   string
	databaseID     string
	showDataDir    string
	statusDataDir  string
	showDatabase   string
	statusDatabase string
	version        string
	schema         uint16
	schemaByKind   map[CommandKind]uint16
	mode           tracker.ConcurrencyMode
	serverEndpoint string
	showEndpoint   string
	statusEndpoint string
	healthy        bool
	showEmbedded   *bool
	malformed      CommandKind
	calls          []CommandKind
	argvs          [][]string
}

func (runner *inspectionRunner) run(_ context.Context, request command) commandResult {
	runner.calls = append(runner.calls, request.kind)
	runner.argvs = append(runner.argvs, request.renderedArgv())
	result := commandResult{kind: request.kind, started: true, mutationOutcome: mutationUnknown}
	zero := 0
	result.exitCode = &zero
	var data any
	switch request.kind {
	case CommandVersion:
		data = map[string]any{"version": runner.version, "build": "qualified", "future_optional": true}
	case CommandWhere:
		data = map[string]any{"path": runner.workspace, "prefix": runner.databaseID, "database_path": runner.databasePath, "future_optional": true}
	case CommandDoltShow:
		dataDir := firstNonEmpty(runner.showDataDir, runner.databasePath)
		database := firstNonEmpty(runner.showDatabase, runner.databaseID)
		embedded := runner.mode == tracker.ConcurrencyEmbeddedSerialized
		if runner.showEmbedded != nil {
			embedded = *runner.showEmbedded
		}
		data = map[string]any{"backend": "dolt", "data_dir": dataDir, "database": database, "embedded": embedded, "future_optional": true}
		if !embedded {
			host, port := splitTestEndpoint(firstNonEmpty(runner.showEndpoint, runner.serverEndpoint))
			data = map[string]any{"backend": "dolt", "data_dir": dataDir, "database": database, "embedded": false, "host": host, "port": port, "user": "dolt", "shared_server": true, "connection_ok": runner.healthy, "future_optional": true}
		}
	case CommandDoltStatus:
		dataDir := firstNonEmpty(runner.statusDataDir, runner.databasePath)
		database := firstNonEmpty(runner.statusDatabase, runner.databaseID)
		if runner.mode == tracker.ConcurrencyEmbeddedSerialized {
			data = map[string]any{"data_dir": dataDir, "data_dir_exists": true, "mode": "embedded", "server_running": false, "future_optional": true}
		} else {
			host, port := splitTestEndpoint(firstNonEmpty(runner.statusEndpoint, runner.serverEndpoint))
			data = map[string]any{"mode": "external", "host": host, "port": port, "user": "dolt", "database": database, "tls": false, "running": runner.healthy, "version": "1.70.0", "future_optional": true}
			if !runner.healthy {
				data.(map[string]any)["error"] = "unavailable"
			}
		}
	default:
		data = map[string]any{"ok": true}
	}
	if runner.malformed == request.kind {
		result.stdout = []byte("not-json")
		return result
	}
	schema := runner.schema
	if override, ok := runner.schemaByKind[request.kind]; ok {
		schema = override
	}
	payload, _ := json.Marshal(map[string]any{"schema_version": schema, "data": data})
	result.stdout = payload
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func splitTestEndpoint(endpoint string) (string, int) {
	parts := strings.Split(endpoint, ":")
	if len(parts) != 2 {
		return endpoint, 0
	}
	port, _ := strconv.Atoi(parts[1])
	return parts[0], port
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
	databasePath := filepath.Join(workspace, "embeddeddolt")
	mustMkdir(t, databasePath)
	config := BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: databasePath, ProtocolSchema: 1, ConcurrencyMode: mode}
	connection := providerConnection{}
	if mode == tracker.ConcurrencyServerMultiwriter {
		config.ServerIdentity, config.ServerEndpoint = "server-1", "127.0.0.1:3306"
		config.Credentials = map[string]string{"DOLT_SERVER_HOST": "127.0.0.1", "DOLT_SERVER_PORT": "3306"}
		connection = providerConnection{serverIdentity: config.ServerIdentity, serverEndpoint: config.ServerEndpoint, credentials: cloneCredentials(config.Credentials)}
	}
	binding, err := ResolveBinding(config)
	if err != nil {
		t.Fatal(err)
	}
	runner := &inspectionRunner{workspace: workspace, databasePath: databasePath, databaseID: databaseID, version: SupportedBeadsVersion, schema: 1, mode: mode, healthy: true, serverEndpoint: "127.0.0.1:3306"}
	bound, err := newConfiguredBoundAdapter(binding, newAdapter(runner), nil, connection)
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
	if readiness.binding.WorkspaceIdentity != bound.binding.WorkspaceIdentity || readiness.serverQualified {
		t.Fatalf("readiness=%#v", readiness)
	}
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Health != tracker.HealthHealthy || inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersAdapterSerialized || inspection.Capabilities.CatalogUpdate != tracker.UpdateUnsupported || inspection.Capabilities.SafeCompensation != tracker.CompensationUnsupported {
		t.Fatalf("inspection=%#v", inspection)
	}
	assertOnlyWriterCapability(t, inspection.Capabilities)
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
	assertOnlyWriterCapability(t, inspection.Capabilities)
}

func assertOnlyWriterCapability(t *testing.T, capabilities tracker.ProviderCapabilities) {
	t.Helper()
	if capabilities.CatalogRead != tracker.SupportUnsupported || capabilities.CatalogCreate != tracker.SupportUnsupported ||
		capabilities.CatalogUpdate != tracker.UpdateUnsupported || capabilities.BlockingGraph != tracker.SupportUnsupported ||
		capabilities.Discussion != tracker.DiscussionUnsupported || capabilities.Assignment != tracker.AssignmentUnsupported ||
		capabilities.ObservationRevision != tracker.ObservationRevisionUnsupported || capabilities.SafeCompensation != tracker.CompensationUnsupported ||
		capabilities.PortableExport != tracker.SupportUnsupported || capabilities.RestorableBackup != tracker.SupportUnsupported {
		t.Fatalf("unimplemented port advertised: %#v", capabilities)
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
		{"database redirect", func(_ *BoundAdapter, r *inspectionRunner) {
			r.databasePath = filepath.Join(filepath.Dir(r.databasePath), "other-database")
			mustMkdir(t, r.databasePath)
		}, tracker.ErrorWorkspaceMismatch},
		{"read only", func(b *BoundAdapter, _ *inspectionRunner) { b.binding.ReadOnly = true }, tracker.ErrorUnavailable},
		{"malformed", func(_ *BoundAdapter, r *inspectionRunner) { r.malformed = CommandDoltShow }, tracker.ErrorMalformedProtocol},
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

func TestPreflightChecksSchemaOnEveryV122Observation(t *testing.T) {
	for _, kind := range []CommandKind{CommandVersion, CommandWhere, CommandDoltShow, CommandDoltStatus} {
		t.Run(string(kind), func(t *testing.T) {
			bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
			runner.schemaByKind = map[CommandKind]uint16{kind: 2}
			if _, err := bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorSchemaSkew) {
				t.Fatalf("error=%v calls=%v", err, runner.calls)
			}
		})
	}
}

func TestPreflightCrossChecksIndependentDatabaseAndServerObservations(t *testing.T) {
	t.Run("embedded show directory", func(t *testing.T) {
		bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
		runner.showDataDir = filepath.Join(runner.workspace, "other-show")
		mustMkdir(t, runner.showDataDir)
		if _, err := bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorWorkspaceMismatch) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("embedded status directory", func(t *testing.T) {
		bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
		runner.statusDataDir = filepath.Join(runner.workspace, "other-status")
		mustMkdir(t, runner.statusDataDir)
		if _, err := bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorWorkspaceMismatch) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("server observation splice", func(t *testing.T) {
		bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
		runner.statusEndpoint = "attacker:3306"
		if _, err := bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorUnavailable) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("server database splice", func(t *testing.T) {
		bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
		runner.statusDatabase = "other-db"
		if _, err := bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorUnavailable) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestV122InspectionFixturesUseDocumentedSchemaOneEnvelopes(t *testing.T) {
	fixtures := []struct {
		name string
		body string
	}{
		{"version", "{\n  \"data\": {\n    \"build\": \"dev\",\n    \"version\": \"1.2.2\"\n  },\n  \"schema_version\": 1\n}\n"},
		{"where", "{\n  \"data\": {\n    \"path\": \"/tmp/provider/.beads\",\n    \"prefix\": \"pf\",\n    \"database_path\": \"/tmp/provider/.beads/embeddeddolt\"\n  },\n  \"schema_version\": 1\n}\n"},
		{"dolt-show", "{\n  \"data\": {\n    \"backend\": \"dolt\",\n    \"data_dir\": \"/tmp/provider/.beads/embeddeddolt\",\n    \"database\": \"pf\",\n    \"embedded\": true\n  },\n  \"schema_version\": 1\n}\n"},
		{"dolt-status", "{\n  \"data\": {\n    \"data_dir\": \"/tmp/provider/.beads/embeddeddolt\",\n    \"data_dir_exists\": true,\n    \"mode\": \"embedded\",\n    \"server_running\": false\n  },\n  \"schema_version\": 1\n}\n"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			payload, metadata, err := decodeEnvelope([]byte(fixture.body), expectedObject)
			if err != nil || metadata.schemaVersion != SupportedProtocolSchema || len(payload) == 0 {
				t.Fatalf("metadata=%#v payload=%s err=%v", metadata, payload, err)
			}
		})
	}
}

func TestIncompatibleInspectionFailsAllCapabilitiesClosed(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	runner.version = "1.2.3"
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyWriterCapability(t, inspection.Capabilities)
	if inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersNone {
		t.Fatalf("incompatible provider advertised writer capability: %#v", inspection.Capabilities)
	}
}

func TestReadOnlyInspectionIsDegradedAndFailsCapabilitiesClosed(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	bound.binding.ReadOnly = true
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Health != tracker.HealthDegraded || inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersNone {
		t.Fatalf("inspection=%#v", inspection)
	}
	assertOnlyWriterCapability(t, inspection.Capabilities)
}

func TestUnavailableInspectionFailsCapabilitiesClosed(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
	if err := os.Remove(bound.binding.ExecutablePath); err != nil {
		t.Fatal(err)
	}
	inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Health != tracker.HealthUnavailable || inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersNone {
		t.Fatalf("inspection=%#v", inspection)
	}
	assertOnlyWriterCapability(t, inspection.Capabilities)
}

func TestServerIdentitySpoofAndStaleReadinessRefuseBeforeMutation(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	runner.serverEndpoint = "attacker:3306"
	if _, err := bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorUnavailable) {
		t.Fatalf("spoofed server error=%v", err)
	}
	runner.serverEndpoint = bound.connection.serverEndpoint
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	locker := &countingLocker{}
	bound.locker = locker
	before := len(runner.calls)
	bound.now = func() time.Time { return readiness.expiresAt.Add(time.Nanosecond) }
	_, err = bound.executeMutation(context.Background(), readiness, request)
	if !tracker.IsKind(err, tracker.ErrorUnavailable) || locker.calls != 0 || len(runner.calls) != before {
		t.Fatalf("stale readiness error=%v lock=%d calls=%v", err, locker.calls, runner.calls[before:])
	}
}

func TestUnqualifiedServerPreflightAndReadinessRefuseBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name string
		edit func(*inspectionRunner)
	}{{"mode", func(r *inspectionRunner) { value := true; r.showEmbedded = &value }}, {"endpoint", func(r *inspectionRunner) { r.serverEndpoint = "attacker:3306" }}, {"unhealthy", func(r *inspectionRunner) { r.healthy = false }}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
			test.edit(runner)
			locker := &countingLocker{}
			bound.locker = locker
			request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = bound.Preflight(context.Background()); !tracker.IsKind(err, tracker.ErrorUnavailable) {
				t.Fatalf("preflight error=%v", err)
			}
			_, err = bound.executeMutation(context.Background(), Readiness{}, request)
			if !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
				t.Fatalf("readiness error=%v", err)
			}
			for _, kind := range runner.calls {
				if kind == CommandCreate {
					t.Fatalf("unqualified server mutated: %v", runner.calls)
				}
			}
			if locker.calls != 0 {
				t.Fatalf("unqualified server acquired local lock %d times", locker.calls)
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

func TestIncompatiblePreflightAndReadinessRefuseBeforeLockAndMutatingSideEffect(t *testing.T) {
	tests := []struct {
		name string
		edit func(*inspectionRunner)
	}{
		{"version", func(r *inspectionRunner) { r.version = "1.2.3" }},
		{"schema", func(r *inspectionRunner) { r.schema = 2 }},
		{"workspace", func(r *inspectionRunner) {
			other := filepath.Join(filepath.Dir(r.workspace), "redirected")
			mustMkdir(t, other)
			r.workspace = other
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "db-1")
			test.edit(runner)
			locker := &countingLocker{}
			bound.locker = locker
			request, err := newCreateCommand(bound.commandBinding(), createValues{title: "work"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = bound.Preflight(context.Background()); err == nil {
				t.Fatal("incompatible preflight succeeded")
			}
			_, err = bound.executeMutation(context.Background(), Readiness{}, request)
			if !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
				t.Fatalf("readiness error=%v", err)
			}
			for _, kind := range runner.calls {
				if kind == CommandCreate {
					t.Fatalf("mutation side effect started: %v", runner.calls)
				}
			}
			if locker.calls != 0 {
				t.Fatalf("writer lock acquired %d times", locker.calls)
			}
		})
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
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	mustMkdir(t, workspace)
	databasePath := filepath.Join(workspace, "embeddeddolt")
	mustMkdir(t, databasePath)
	executable := testProtocolExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	helperSource, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	for _, remote := range []string{"curl", "wget", "http://", "https://", "go install", "git clone"} {
		if strings.Contains(strings.ToLower(string(helperSource)), remote) {
			t.Fatalf("latency helper attempts remote operation %q", remote)
		}
	}
	bound, err := Open(BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: databasePath, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized})
	if err != nil {
		t.Fatal(err)
	}
	startup := make([]time.Duration, 100)
	for i := range startup {
		request, buildErr := newVersionCommand(bound.commandBinding())
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		started := time.Now()
		if _, executeErr := bound.adapter.execute(context.Background(), request); executeErr != nil {
			t.Fatal(executeErr)
		}
		startup[i] = time.Since(started)
	}
	samples := make([]time.Duration, 100)
	for i := range samples {
		started := time.Now()
		if _, preflightErr := bound.Preflight(context.Background()); preflightErr != nil {
			t.Fatal(preflightErr)
		}
		samples[i] = time.Since(started)
	}
	sort.Slice(startup, func(i, j int) bool { return startup[i] < startup[j] })
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p95, max := samples[49], samples[94], samples[99]
	t.Logf("process-start samples=100 p50=%s p95=%s max=%s", startup[49], startup[94], startup[99])
	t.Logf("preflight samples=100 p50=%s p95=%s max=%s host=%s/%s cpus=%d go=%s fixture=local-offline-shell", p50, p95, max, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	if p95 >= time.Second {
		t.Fatalf("healthy local fixture p95=%s", p95)
	}
}

func TestInspectionFixturesRemainByteIdentical(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "provider")
	mustMkdir(t, workspace)
	databasePath := filepath.Join(workspace, "embeddeddolt")
	mustMkdir(t, databasePath)
	laneA, laneB := filepath.Join(root, "lane-a"), filepath.Join(root, "lane-b")
	for _, lane := range []string{laneA, laneB} {
		mustMkdir(t, filepath.Join(lane, ".git", "hooks"))
		if err := os.WriteFile(filepath.Join(lane, "AGENTS.md"), []byte("instructions\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, ".git", "config"), []byte("[core]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lane, ".git", "hooks", "pre-commit"), []byte("hook\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	executable := testProtocolExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	bound, err := Open(BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: databasePath, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized})
	if err != nil {
		t.Fatal(err)
	}
	before := censusTree(t, root)
	if _, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID}); err != nil {
		t.Fatal(err)
	}
	after := censusTree(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("inspection changed repository/provider census\nbefore=%v\nafter=%v", before, after)
	}
}

func TestInspectionArgvHasNoInitializationRepairOrRepositorySetup(t *testing.T) {
	bound, runner := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	if _, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: bound.binding.ProjectID}); err != nil {
		t.Fatal(err)
	}
	for _, argv := range runner.argvs {
		joined := strings.ToLower(strings.Join(argv, " "))
		if len(argv) > 0 && (argv[0] == "info" || argv[0] == "doctor") {
			t.Fatalf("obsolete non-machine inspection command invoked: %q", argv)
		}
		for _, forbidden := range []string{"init", "fix", "hook", "agent setup", "git config", "install"} {
			if strings.Contains(joined, forbidden) {
				t.Fatalf("forbidden inspection argv %q", joined)
			}
		}
	}
	want := []CommandKind{CommandVersion, CommandWhere, CommandDoltShow, CommandDoltStatus}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("inspection calls=%v want=%v", runner.calls, want)
	}
}

func TestServerIdentityIsLocalSealedPolicy(t *testing.T) {
	bound, _ := testBoundAdapter(t, tracker.ConcurrencyServerMultiwriter, "db-1")
	readiness, err := bound.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bound.connection.serverIdentity = "different-local-policy"
	if err := bound.validateReadiness(readiness); !tracker.IsKind(err, tracker.ErrorUnavailable) {
		t.Fatalf("changed local server identity accepted: %v", err)
	}
}

func TestWorktreeConvergenceAndDivergenceThroughProductionComposition(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "provider")
	other := filepath.Join(root, "other")
	mustMkdir(t, workspace)
	mustMkdir(t, other)
	mustMkdir(t, filepath.Join(workspace, "embeddeddolt"))
	mustMkdir(t, filepath.Join(other, "embeddeddolt"))
	laneA, laneB := filepath.Join(root, "lane-a"), filepath.Join(root, "lane-b")
	mustMkdir(t, laneA)
	mustMkdir(t, laneB)
	executable := testProtocolExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	config := BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: filepath.Join(workspace, "embeddeddolt"), ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized}
	first, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	if err := os.Chdir(laneA); err != nil {
		t.Fatal(err)
	}
	one, err := first.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(laneB); err != nil {
		t.Fatal(err)
	}
	two, err := second.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if one.binding.WorkspaceIdentity != two.binding.WorkspaceIdentity {
		t.Fatal("lane CWD changed canonical identity")
	}
	divergent := config
	divergent.WorkspacePath = other
	divergent.ProviderDatabaseIdentity = filepath.Join(other, "embeddeddolt")
	third, err := Open(divergent)
	if err != nil {
		t.Fatal(err)
	}
	// The executable reports its explicit BEADS_DIR, so this separately bound
	// lane is observably distinct rather than silently adopted.
	three, err := third.Preflight(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if three.binding.WorkspaceIdentity == one.binding.WorkspaceIdentity {
		t.Fatal("divergent lane workspace compared equal")
	}
	redirectedRunner := &inspectionRunner{workspace: other, databasePath: filepath.Join(other, "embeddeddolt"), databaseID: "db-1", version: SupportedBeadsVersion, schema: 1, mode: tracker.ConcurrencyEmbeddedSerialized, healthy: true}
	locker := &countingLocker{}
	redirected, err := newConfiguredBoundAdapter(first.binding, newAdapter(redirectedRunner), locker, providerConnection{})
	if err != nil {
		t.Fatal(err)
	}
	request, err := newCreateCommand(redirected.commandBinding(), createValues{title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = redirected.Preflight(context.Background())
	if !tracker.IsKind(err, tracker.ErrorWorkspaceMismatch) {
		t.Fatalf("redirect preflight error=%v", err)
	}
	_, err = redirected.executeMutation(context.Background(), Readiness{}, request)
	if !tracker.IsKind(err, tracker.ErrorInvalidRequest) {
		t.Fatalf("redirect readiness error=%v", err)
	}
	for _, kind := range redirectedRunner.calls {
		if kind == CommandCreate {
			t.Fatalf("redirected lane mutated provider: %v", redirectedRunner.calls)
		}
	}
	if locker.calls != 0 {
		t.Fatalf("redirected lane acquired lock %d times", locker.calls)
	}
}

func TestInspectProviderProductionEntry(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	mustMkdir(t, workspace)
	databasePath := filepath.Join(workspace, "embeddeddolt")
	mustMkdir(t, databasePath)
	executable := testProtocolExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	inspection, err := InspectProvider(context.Background(), BindingConfig{ProjectID: "project-1", ExecutablePath: executable, WorkspacePath: workspace, ProviderDatabaseIdentity: databasePath, ProtocolSchema: 1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized})
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Health != tracker.HealthHealthy || inspection.Binding.WorkspacePath != workspace {
		t.Fatalf("inspection=%#v", inspection)
	}
}

func censusTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if info.IsDir() {
			result[relative] = "dir:" + info.Mode().Perm().String()
			return nil
		}
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		result[relative] = fmt.Sprintf("file:%s:%x", info.Mode().Perm(), payload)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func testProtocolExecutable(t *testing.T, root string, mode tracker.ConcurrencyMode) string {
	t.Helper()
	path := filepath.Join(root, "bd-protocol")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
  version) printf '%%s\n' '{"schema_version":1,"data":{"version":"1.2.2","build":"test-helper"}}' ;;
  where) printf '{"schema_version":1,"data":{"path":"%%s","prefix":"db-1","database_path":"%%s/embeddeddolt"}}\n' "$BEADS_DIR" "$BEADS_DIR" ;;
  dolt)
    case "$2" in
      show)
        if [ "%s" = "server_multiwriter" ]; then
          printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%%s/embeddeddolt","database":"db-1","embedded":false,"host":"127.0.0.1","port":3306,"user":"dolt","shared_server":true,"connection_ok":true}}\n' "$BEADS_DIR"
        else
          printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%%s/embeddeddolt","database":"db-1","embedded":true}}\n' "$BEADS_DIR"
        fi ;;
      status)
        if [ "%s" = "server_multiwriter" ]; then
          printf '%%s\n' '{"schema_version":1,"data":{"mode":"external","host":"127.0.0.1","port":3306,"user":"dolt","database":"db-1","tls":false,"running":true,"version":"1.70.0"}}'
        else
          printf '{"schema_version":1,"data":{"data_dir":"%%s/embeddeddolt","data_dir_exists":true,"mode":"embedded","server_running":false}}\n' "$BEADS_DIR"
        fi ;;
      *) exit 64 ;;
    esac ;;
  create)
    if [ -n "${DOLT_CREDENTIALS_PATH:-}" ]; then
      printf 'ready\n' > "${DOLT_CREDENTIALS_PATH}.$$.ready"
      while [ ! -f "${DOLT_CREDENTIALS_PATH}.go" ]; do sleep 0.01; done
      printf 'start %%s %%s\n' "$$" "$(date +%%s%%N)" >> "$DOLT_CREDENTIALS_PATH"
      printf 'started\n' > "${DOLT_CREDENTIALS_PATH}.$$.started"
      while [ ! -f "${DOLT_CREDENTIALS_PATH}.release" ]; do sleep 0.01; done
      sleep 0.10
      printf 'end %%s %%s\n' "$$" "$(date +%%s%%N)" >> "$DOLT_CREDENTIALS_PATH"
    fi
    printf '%%s\n' '{"schema_version":1,"data":{"id":"work-1"}}' ;;
  *) exit 64 ;;
esac
`, mode, mode)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
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
