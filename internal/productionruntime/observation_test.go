package productionruntime

import (
	"context"
	"encoding/json"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

// memoryObservationPort intentionally does not embed memoryAuthorityPort: its
// method set is the compile-time proof that the observation core accepts no
// mutation or epoch-admission capability.
type memoryObservationPort struct{ port *memoryAuthorityPort }

func (p memoryObservationPort) Get(ctx context.Context, key string) (storegapdb.Record, error) {
	return p.port.Get(ctx, key)
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
	recovered, err := journalrestore.RestoreV2(t.Context(), journalrestore.InputV2{Reader: emptyRecoveryReader{port}, EventRegistry: registry, Reducers: reducers, HostAuthorityID: binding.HostAuthorityID, RecoveryAttemptID: "restore-observation-fixture", Budgets: journalrestore.DefaultBudgetsV2()})
	if err != nil {
		t.Fatal(err)
	}
	epoch := journalv2.RuntimeEpochID("epoch-observation")
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
