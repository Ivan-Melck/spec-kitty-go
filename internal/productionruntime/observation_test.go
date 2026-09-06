package productionruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// memoryObservationPort intentionally does not embed memoryAuthorityPort: its
// method set is the compile-time proof that the observation core accepts no
// mutation or epoch-admission capability.
type memoryObservationPort struct{ port *memoryAuthorityPort }

func (p memoryObservationPort) Get(ctx context.Context, key string) (storegapdb.Record, error) {
	return p.port.Get(ctx, key)
}

func TestReadOnlyObservationRealSidecarKeepsCurrentCoordinate(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_WP13_GAPDBD to the exact assertion-extension sidecar")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "spk-observe-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	if err := os.Mkdir(runtimeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "authority")
	config := Config{
		ControlRoot: data, HostAuthorityID: "host-observe-real", AuthorityBindingOperationID: "operation-bind-observe-real",
		M1AdoptionRecordPath:                 filepath.Join(repository, "docs", "programs", "evidence", "m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json"),
		Sidecar: sidecargapdb.LifecycleConfig{
			ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-admission.json"),
			RuntimeRoot: runtimeRoot, DataDirectory: data, SupervisorRunID: "supervisor-observe-mutate", ProductPolicySHA256: repeatByte('e', 64),
		},
		MonitorInterval: 250 * time.Millisecond, CapabilityReadiness: absentReadinessSource{},
		DecompositionProvider: refusingDecompositionProvider{}, CandidateVerifier: refusingCandidateVerifier{}, IntegrationGit: refusingIntegrationGit{},
	}
	host, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	want := host.Status()
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.Sidecar.SupervisorRunID = "supervisor-observe-read"
	observed, err := Observe(t.Context(), config)
	if err != nil {
		t.Fatalf("read-only observation: %T %v", err, err)
	}
	defer observed.Close(t.Context())
	got := observed.Status()
	if got.DatabaseID != want.DatabaseID || got.RuntimeEpochID != want.RuntimeEpochID || got.ProjectionRevision != want.ProjectionRevision || got.ProjectionSHA256 != want.ProjectionSHA256 {
		t.Fatalf("observation changed coordinate: want=%+v got=%+v", want, got)
	}
}

func (p memoryObservationPort) CaptureRevisionV2(ctx context.Context) (journalv2.StoreRevisionV2, error) {
	return emptyRecoveryReader(p).CaptureRevisionV2(ctx)
}
func (p memoryObservationPort) VerifyRevisionV2(ctx context.Context, expected journalv2.StoreRevisionV2) error {
	return emptyRecoveryReader(p).VerifyRevisionV2(ctx, expected)
}
func (p memoryObservationPort) ReadV2(ctx context.Context, request journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	return emptyRecoveryReader(p).ReadV2(ctx, request)
}
func (p memoryObservationPort) ScanV2(ctx context.Context, request journalv2.ScanRequestV2) (journalv2.RecoveryPageV2, error) {
	return emptyRecoveryReader(p).ScanV2(ctx, request)
}

func TestReadOnlyObservationDoesNotAdvanceProviderRevisionOrEpoch(t *testing.T) {
	port := newMemoryAuthorityPort()
	binding := AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: "host-binding", DatabaseID: "database-binding", AdmittedTupleSHA: storegapdb.AdmittedTupleSHA256, SidecarSHA: storegapdb.AdmittedSidecarSHA256, WireProtocol: storegapdb.AdmittedWireVersion, StorageProtocol: storegapdb.AdmittedStorageVersion, CreatedOperationID: "operation-bind"}
	if err := ensureAuthorityBinding(t.Context(), port, binding, true); err != nil {
		t.Fatal(err)
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers := fixtureReducers(t, registry)
	epoch := journalv2.RuntimeEpochID("epoch-observation")
	recovered, err := journalrestore.RestoreV2(t.Context(), journalrestore.InputV2{Reader: emptyRecoveryReader{port}, EventRegistry: registry, Reducers: reducers, HostAuthorityID: binding.HostAuthorityID, PriorRuntimeEpochID: epoch, RecoveryAttemptID: "restore-observation-fixture", Budgets: journalrestore.DefaultBudgetsV2()})
	if err != nil {
		t.Fatal(err)
	}
	current := runtimeCurrentV2{SchemaVersion: 2, RuntimeEpochID: epoch, HostAuthorityID: binding.HostAuthorityID, DatabaseID: binding.DatabaseID, RecoveredStoreRevision: 0, ProjectionSHA256: recovered.Playback().CanonicalProjectionDigest().String(), CreatedOperationID: "runtime-observation", RecoveryAttemptID: "restore-observation-fixture", Status: "ready"}
	encoded, _ := json.Marshal(current)
	canonical, _ := journalv2.CanonicalObject(encoded)
	port.records[runtimeCurrentKey] = storegapdb.Record{Key: runtimeCurrentKey, Value: canonical, Revision: port.revision}
	before := port.revision
	status, progress, _, err := restoreReadOnlyObservationWith(t.Context(), memoryObservationPort{port: port}, binding, registry, reducers, journalrestore.DefaultBudgetsV2(), func(context.Context, journalrestore.InputV2) (journalrestore.ResultV2, error) { return recovered, nil })
	if err != nil {
		t.Fatal(err)
	}
	if port.revision != before || status.ProjectionRevision != before || status.RuntimeEpochID != epoch || progress == nil {
		t.Fatalf("observation mutated authority: before=%d after=%d status=%+v progress_nil=%v", before, port.revision, status, progress == nil)
	}
	current, revision, err := readCurrentEpoch(t.Context(), port)
	if err != nil || revision != before || current.RuntimeEpochID != epoch || current.PredecessorEpochID != "" {
		t.Fatalf("observation rotated epoch: current=%+v revision=%d err=%v", current, revision, err)
	}
}
