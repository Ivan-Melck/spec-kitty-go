package productionruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

// Observation is a read-only, process-scoped view of durable authority. It
// deliberately exposes neither a Host nor any command, committer, checkpoint,
// reconciliation, or epoch-admission capability.
type Observation struct {
	owner    *productionOwner
	status   HostStatus
	progress *ProgressService
}

type readOnlyObservationPort interface {
	authorityReader
	journalrestore.ReaderV2
}

func (o *Observation) Status() HostStatus {
	if o == nil {
		return HostStatus{}
	}
	return o.status
}

func (o *Observation) Progress() *ProgressService {
	if o == nil {
		return nil
	}
	return o.progress
}

func (o *Observation) Close(ctx context.Context) error {
	if o == nil || o.owner == nil {
		return nil
	}
	return o.owner.Close(ctx)
}

// Observe restores the immutable projection at the already-admitted current
// runtime epoch without rotating that epoch or publishing any durable record.
// An empty root is not observable: initialization belongs to a mutation path.
func Observe(ctx context.Context, config Config) (_ *Observation, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.ControlRoot == "" || config.HostAuthorityID == "" || config.M1AdoptionRecordPath == "" ||
		!filepath.IsAbs(config.ControlRoot) || filepath.Clean(config.ControlRoot) != config.ControlRoot || config.Sidecar.DataDirectory != config.ControlRoot {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "config"}
	}
	classifier, err := controlroot.NewClassifier(controlroot.DefaultBudgets(), controlroot.TrustPolicy{
		OwnerUID: uint32(os.Geteuid()), OwnerGID: uint32(os.Getegid()), AllowGroupWritable: true,
	})
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "root_classifier"}
	}
	decision, err := classifier.Decide(ctx, config.ControlRoot)
	if err != nil {
		return nil, err
	}
	if decision.Action != controlroot.Recover {
		if decision.Action == controlroot.Initialize {
			return nil, &Error{Kind: ErrorNotReady, State: HostRecovering, Field: "control_root_uninitialized"}
		}
		return nil, &controlroot.OfflineDispositionError{Code: controlroot.OfflineDispositionRequiredCode, Disposition: decision.Disposition}
	}
	if _, err = sidecargapdb.VerifyAdoptionRecord(config.M1AdoptionRecordPath); err != nil {
		return nil, err
	}
	extensionAdmission, err := sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, config.Sidecar.AdmissionRecordPath)
	if err != nil {
		return nil, err
	}
	config.Sidecar.Admission = extensionAdmission
	if config.CapabilityReadiness == nil {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "capability_dependencies"}
	}
	lifecycle, err := sidecargapdb.Start(ctx, config.Sidecar)
	if err != nil {
		return nil, err
	}
	owner := &productionOwner{lifecycle: lifecycle}
	defer func() {
		if resultErr != nil {
			_ = owner.Close(context.Background())
		}
	}()
	provenance := lifecycle.Provenance()
	databaseID := journalv2.DatabaseID(provenance.DatabaseID)
	adapter, closer, err := storegapdb.Dial(provenance.ConnectedSocket, storegapdb.DefaultRequestTimeout)
	if err != nil {
		return nil, err
	}
	owner.adapter, owner.closer, owner.database = adapter, closer, databaseID
	binding := AuthorityBindingV2{
		SchemaVersion: 2, HostAuthorityID: config.HostAuthorityID, DatabaseID: databaseID,
		AdmittedTupleSHA: storegapdb.AdmittedTupleSHA256, SidecarSHA: storegapdb.AdmittedSidecarSHA256,
		WireProtocol: storegapdb.AdmittedWireVersion, StorageProtocol: storegapdb.AdmittedStorageVersion,
		CreatedOperationID: config.AuthorityBindingOperationID,
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil {
		return nil, err
	}
	budgets := config.RestoreBudgets
	if budgets == (journalrestore.BudgetsV2{}) {
		budgets = productionRecoveryBudgetsV2()
	}
	status, progress, _, err := restoreReadOnlyObservation(ctx, adapter, binding, registry, reducers, budgets)
	if err != nil {
		return nil, err
	}
	readiness, err := storegapdb.NewReadinessObservationPortV2(adapter)
	if err != nil {
		return nil, err
	}
	actions, err := newAuthorityActionCensusSource(readiness, config.CapabilityReadiness)
	if err != nil {
		return nil, err
	}
	progress.actions = actions
	return &Observation{owner: owner, status: status, progress: progress}, nil
}

func restoreReadOnlyObservation(ctx context.Context, port readOnlyObservationPort, binding AuthorityBindingV2, registry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, budgets journalrestore.BudgetsV2) (HostStatus, *ProgressService, journalrestore.ResultV2, error) {
	return restoreReadOnlyObservationWith(ctx, port, binding, registry, reducers, budgets, journalrestore.RestoreV2)
}

type observationRestore func(context.Context, journalrestore.InputV2) (journalrestore.ResultV2, error)

func restoreReadOnlyObservationWith(ctx context.Context, port readOnlyObservationPort, binding AuthorityBindingV2, registry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, budgets journalrestore.BudgetsV2, restore observationRestore) (HostStatus, *ProgressService, journalrestore.ResultV2, error) {
	if err := observeAuthorityBinding(ctx, port, binding); err != nil {
		return HostStatus{}, nil, journalrestore.ResultV2{}, err
	}
	current, currentRevision, err := readCurrentEpoch(ctx, port)
	if err != nil || current.RuntimeEpochID == "" || current.DatabaseID != binding.DatabaseID || current.HostAuthorityID != binding.HostAuthorityID {
		return HostStatus{}, nil, journalrestore.ResultV2{}, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_current_binding"}
	}
	attemptID, err := randomIdentity[string]("observe-")
	if err != nil {
		return HostStatus{}, nil, journalrestore.ResultV2{}, err
	}
	restored, err := restore(ctx, journalrestore.InputV2{Reader: recoveryReaderForModeV2(port, false), EventRegistry: registry, Reducers: reducers, HostAuthorityID: binding.HostAuthorityID, PriorRuntimeEpochID: current.RuntimeEpochID, RecoveryAttemptID: attemptID, Budgets: budgets})
	if err != nil {
		return HostStatus{}, nil, journalrestore.ResultV2{}, err
	}
	if currentRevision != restored.StoreRevision().CurrentRevision() || current.RecoveredStoreRevision >= currentRevision || restored.Playback().CanonicalProjectionDigest().String() != current.ProjectionSHA256 {
		return HostStatus{}, nil, journalrestore.ResultV2{}, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "observation_coordinate"}
	}
	authority, err := projection.NewAuthorityV2(binding.DatabaseID, registry, reducers)
	if err != nil {
		return HostStatus{}, nil, journalrestore.ResultV2{}, err
	}
	if err := authority.RestoreCurrentEpoch(current.RuntimeEpochID, restored); err != nil {
		return HostStatus{}, nil, journalrestore.ResultV2{}, err
	}
	progress, err := NewProgressService(authority, nil)
	if err != nil {
		return HostStatus{}, nil, journalrestore.ResultV2{}, err
	}
	status := HostStatus{SchemaVersion: 1, State: HostReady, DatabaseID: binding.DatabaseID, RuntimeEpochID: current.RuntimeEpochID, ProjectionRevision: currentRevision, ProjectionSHA256: restored.Playback().CanonicalProjectionDigest().String()}
	return status, progress, restored, nil
}

func observeAuthorityBinding(ctx context.Context, port authorityReader, expected AuthorityBindingV2) error {
	canonical, err := expected.canonical()
	if err != nil {
		return err
	}
	record, err := port.Get(ctx, authorityBindingKey)
	if err != nil || record.Revision == 0 || !bytes.Equal(record.Value, canonical) {
		return &Error{Kind: ErrorStartup, State: HostRecovering, Field: "authority_binding_mismatch"}
	}
	return nil
}
