package productionruntime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// memoryObservationPort intentionally does not embed memoryAuthorityPort: its
// method set is the compile-time proof that the observation core accepts no
// mutation or epoch-admission capability.
type memoryObservationPort struct{ port *memoryAuthorityPort }

func (p memoryObservationPort) Get(ctx context.Context, key string) (storegapdb.Record, error) {
	return p.port.Get(ctx, key)
}

func realObservationConfig(t *testing.T) Config {
	t.Helper()
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_WP13_GAPDBD to the exact assertion-extension sidecar")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("", "spk-observe-")
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
	return Config{
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
}

func TestReadOnlyObservationRealSidecarKeepsCurrentCoordinate(t *testing.T) {
	assertRealObservationAfterPublications(t, 0, 0)
}

func TestReadOnlyObservationRealSidecarAfterPublications(t *testing.T) {
	t.Run("one_publication", func(t *testing.T) { assertRealObservationAfterPublications(t, 1, 0) })
	t.Run("multiple_publications", func(t *testing.T) { assertRealObservationAfterPublications(t, 2, 0) })
	t.Run("checkpoint_publications", func(t *testing.T) { assertRealObservationAfterPublications(t, 2, 1) })
}

func assertRealObservationAfterPublications(t *testing.T, count, checkpointEvery int) {
	t.Helper()
	config := realObservationConfig(t)
	config.CheckpointEvery = checkpointEvery
	host, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = host.Close(t.Context()) })
	initial := host.Status()
	for index := 0; index < count; index++ {
		missionID := journalv2.MissionID("mission-observe-left")
		operationID := journalv2.OperationID("operation-observe-left")
		if index != 0 {
			missionID, operationID = "mission-observe-right", "operation-observe-right"
		}
		digest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"observation-regression"}`))
		if err != nil {
			t.Fatal(err)
		}
		state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
			ProjectID: "project-observe", MissionID: missionID, DefinitionRevision: "definition-observe", DefinitionDigest: digest,
			Version: 1, Lifecycle: mission.MissionOpen, LastOperationID: operationID, ObservedAt: "2026-09-05T12:00:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := host.ExecuteReadiness(t.Context(), host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{
			SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: operationID, ProjectID: "project-observe", MissionID: missionID,
			ActorID: "actor-observe", ProcessID: "process-observe", OriginatingRuntimeEpochID: host.RuntimeEpochID(),
			DoctrineRevision: "doctrine-observe", ProfileRevision: "profile-observe", PolicyRevision: "policy-observe",
			ObservedAt: "2026-09-05T12:00:00Z", EventType: mission.ReadinessEventCreatedV2, Updated: state,
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := host.Status()
	assertObservedMissionOperations(t, host.Progress(), count)
	if count != 0 {
		item, err := host.InspectOperation(t.Context(), "project-observe", "mission-observe-left", "operation-observe-left")
		if err != nil || item.Family != "operation" || item.AggregateID != "operation-observe-left" {
			t.Fatalf("live host could not inspect its bound operation: item=%+v err=%v", item, err)
		}
	}
	if count != 0 && (want.ProjectionRevision <= initial.ProjectionRevision || want.ProjectionSHA256 == initial.ProjectionSHA256) {
		t.Fatal("fixture did not publish a new product projection")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.Sidecar.SupervisorRunID = "supervisor-observe-read"
	observed, err := Observe(t.Context(), config)
	if err != nil {
		t.Fatalf("read-only observation: %T %v", err, err)
	}
	t.Cleanup(func() { _ = observed.Close(t.Context()) })
	got := observed.Status()
	assertObservedMissionOperations(t, observed.Progress(), count)
	if got.DatabaseID != want.DatabaseID || got.RuntimeEpochID != want.RuntimeEpochID || got.ProjectionRevision != want.ProjectionRevision || got.ProjectionSHA256 != want.ProjectionSHA256 {
		t.Fatalf("observation changed coordinate: want=%+v got=%+v", want, got)
	}
	if count != 0 {
		progress, err := observed.Progress().Mission("project-observe", "mission-observe-left")
		if err != nil || !progress.Complete || progress.Lifecycle != "open" || progress.ProjectionRevision != want.ProjectionRevision {
			t.Fatalf("durable mission publication was not observable: progress=%+v err=%v", progress, err)
		}
	}
}

func assertObservedMissionOperations(t *testing.T, service *ProgressService, count int) {
	t.Helper()
	for index := 0; index < count; index++ {
		missionID, operationID := journalv2.MissionID("mission-observe-left"), "operation-observe-left"
		if index != 0 {
			missionID, operationID = "mission-observe-right", "operation-observe-right"
		}
		progress, err := service.Mission("project-observe", missionID)
		if err != nil || len(progress.Operations) != 1 || progress.Operations[0].AggregateID != operationID {
			t.Fatalf("mission operations omitted or crossed immutable membership: mission=%s operations=%+v err=%v", missionID, progress.Operations, err)
		}
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
	fixture := newObservationFixture(t)
	port, epoch := fixture.port, fixture.epoch
	before := port.revision
	status, progress, _, err := restoreReadOnlyObservation(t.Context(), memoryObservationPort{port: port}, fixture.binding, fixture.registry, fixture.reducers, journalrestore.DefaultBudgetsV2())
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
