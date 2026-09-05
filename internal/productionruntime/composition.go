package productionruntime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	"github.com/spec-kitty/go-kitty3/internal/review"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// Reconciler runs after durable restore and fresh-epoch admission while the
// command barrier remains closed. It may use capability-owned observers but
// receives no raw database or projection mutation handle.
type Reconciler interface {
	Reconcile(context.Context, *ReconciliationCommands) error
}

type Config struct {
	ControlRoot                          string
	HostAuthorityID                      journalv2.HostAuthorityID
	AuthorityBindingOperationID          string
	M1AdoptionRecordPath                 string
	AssertionExtensionAdoptionRecordPath string
	Sidecar                              sidecargapdb.LifecycleConfig
	RestoreBudgets                       journalrestore.BudgetsV2
	Reconciler                           Reconciler
	CapabilityReadiness                  mission.CapabilityReadinessSourceV2
	DecompositionProvider                mission.DecompositionProviderV2
	CandidateVerifier                    handoff.CandidateVerifier
	IntegrationGit                       integration.IntegrationGitPortV2
	CheckpointEvery                      int
	MonitorInterval                      time.Duration
}

type productionOwner struct {
	lifecycle *sidecargapdb.Lifecycle
	adapter   *storegapdb.Adapter
	closer    io.Closer
	database  journalv2.DatabaseID
	closeOnce bool
}

func (o *productionOwner) DatabaseID() journalv2.DatabaseID { return o.database }
func (o *productionOwner) Probe(ctx context.Context) error {
	if o == nil || o.lifecycle == nil || o.adapter == nil {
		return &Error{Kind: ErrorNotReady, State: HostDegraded, Field: "owner"}
	}
	diagnostic := o.lifecycle.InspectOwnership()
	if diagnostic.State != sidecargapdb.OwnershipOwnedLive {
		return &Error{Kind: ErrorNotReady, State: HostDegraded, Field: "process_identity"}
	}
	inspection, err := o.adapter.Inspect(ctx)
	if err != nil || !inspection.Healthy || inspection.DatabaseID != string(o.database) || inspection.DurableThrough < inspection.CurrentRevision {
		return &Error{Kind: ErrorNotReady, State: HostDegraded, Field: "database_identity"}
	}
	return nil
}
func (o *productionOwner) Close(ctx context.Context) error {
	if o == nil || o.closeOnce {
		return nil
	}
	o.closeOnce = true
	var failures []error
	if o.closer != nil {
		failures = append(failures, o.closer.Close())
	}
	if o.lifecycle != nil {
		failures = append(failures, o.lifecycle.Stop(ctx))
	}
	return errors.Join(failures...)
}

// Start is the production composition root. It returns only the Host and its
// authority-free progress surface; no raw GapDB or lifecycle handle escapes.
func Start(ctx context.Context, config Config) (_ *Host, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if config.ControlRoot == "" || config.HostAuthorityID == "" || config.AuthorityBindingOperationID == "" || config.M1AdoptionRecordPath == "" ||
		!filepath.IsAbs(config.ControlRoot) || filepath.Clean(config.ControlRoot) != config.ControlRoot || config.Sidecar.DataDirectory != config.ControlRoot {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "config"}
	}
	classifier, err := controlroot.NewClassifier(controlroot.DefaultBudgets(), controlroot.TrustPolicy{OwnerUID: uint32(os.Geteuid()), OwnerGID: uint32(os.Getegid()), AllowGroupWritable: true})
	if err != nil {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "root_classifier"}
	}
	decision, err := classifier.Decide(ctx, config.ControlRoot)
	if err != nil {
		return nil, err
	}
	if _, err = sidecargapdb.VerifyAdoptionRecord(config.M1AdoptionRecordPath); err != nil {
		return nil, err
	}
	extensionAdmission, err := sidecargapdb.VerifyAssertionExtensionAdoption(config.AssertionExtensionAdoptionRecordPath, config.Sidecar.AdmissionRecordPath)
	if err != nil {
		return nil, err
	}
	config.Sidecar.Admission = extensionAdmission
	if config.CapabilityReadiness == nil || config.DecompositionProvider == nil || config.CandidateVerifier == nil || config.IntegrationGit == nil {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "capability_dependencies"}
	}
	if config.CheckpointEvery < 0 || config.CheckpointEvery > journalv2.MaxAcknowledgementsBetweenSeals {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "checkpoint_every"}
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
	binding := AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: config.HostAuthorityID, DatabaseID: databaseID, AdmittedTupleSHA: storegapdb.AdmittedTupleSHA256, SidecarSHA: storegapdb.AdmittedSidecarSHA256, WireProtocol: storegapdb.AdmittedWireVersion, StorageProtocol: storegapdb.AdmittedStorageVersion, CreatedOperationID: config.AuthorityBindingOperationID}
	if err := ensureAuthorityBinding(ctx, adapter, binding, decision.Action == controlroot.Initialize); err != nil {
		return nil, err
	}
	prior, priorRevision, err := readCurrentEpoch(ctx, adapter)
	if err != nil || prior.DatabaseID != "" && (prior.DatabaseID != databaseID || prior.HostAuthorityID != config.HostAuthorityID) {
		return nil, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "runtime_current_binding"}
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil {
		return nil, err
	}
	attemptID, err := randomIdentity[string]("restore-")
	if err != nil {
		return nil, err
	}
	budgets := config.RestoreBudgets
	if budgets == (journalrestore.BudgetsV2{}) {
		budgets = journalrestore.DefaultBudgetsV2()
	}
	restored, err := journalrestore.RestoreV2(ctx, journalrestore.InputV2{Reader: adapter, EventRegistry: registry, Reducers: reducers, HostAuthorityID: config.HostAuthorityID, PriorRuntimeEpochID: prior.RuntimeEpochID, RecoveryAttemptID: attemptID, Budgets: budgets})
	if err != nil {
		return nil, err
	}
	epoch, err := admitFreshEpoch(ctx, adapter, binding, prior, priorRevision, restored, attemptID)
	if err != nil {
		return nil, err
	}
	// Epoch admission is owner metadata outside the product journal. Capture a
	// second stable restore coordinate so the memory snapshot is attributed to
	// the exact post-admission durable revision before readiness opens.
	postAttemptID, _ := randomIdentity[string]("restore-post-epoch-")
	postRestore, err := journalrestore.RestoreV2(ctx, journalrestore.InputV2{Reader: adapter, EventRegistry: registry, Reducers: reducers, HostAuthorityID: config.HostAuthorityID, PriorRuntimeEpochID: prior.RuntimeEpochID, RecoveryAttemptID: postAttemptID, Budgets: budgets})
	if err != nil {
		return nil, err
	}
	current, currentRevision, err := readCurrentEpoch(ctx, adapter)
	if err != nil || current.RuntimeEpochID != epoch {
		return nil, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "post_epoch_identity"}
	}
	if current.RecoveredStoreRevision != restored.StoreRevision().CurrentRevision() {
		return nil, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "post_epoch_recovered_prefix"}
	}
	if currentRevision != postRestore.StoreRevision().CurrentRevision() {
		return nil, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "post_epoch_record_revision"}
	}
	if currentRevision <= current.RecoveredStoreRevision {
		return nil, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "post_epoch_revision_order"}
	}
	if postRestore.Playback().CanonicalProjectionDigest().String() != current.ProjectionSHA256 || postRestore.Playback().CanonicalProjectionDigest() != restored.Playback().CanonicalProjectionDigest() {
		return nil, &Error{Kind: ErrorStartup, State: HostRecovering, Field: "post_epoch_digest"}
	}
	authority, progress, err := restoredAuthority(databaseID, epoch, postRestore, registry, reducers)
	if err != nil {
		return nil, err
	}
	host, err := newHost(owner, epoch, authority, progress)
	if err != nil {
		return nil, err
	}
	if err := host.transition(HostReconciling); err != nil {
		_ = host.Close(context.Background())
		return nil, err
	}
	capabilities, err := composeRuntimeCapabilities(adapter, registry, reducers, budgets, config.HostAuthorityID, epoch, postRestore, config)
	if err != nil {
		_ = host.Close(context.Background())
		return nil, err
	}
	progress.actions = capabilities.actions
	if config.Reconciler != nil {
		commands := newReconciliationCommands(epoch, capabilities)
		reconcileErr := config.Reconciler.Reconcile(ctx, commands)
		commands.close()
		if reconcileErr != nil {
			host.degrade("reconciliation")
			_ = host.Close(context.Background())
			return nil, &Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation"}
		}
		// Reconciliation is not trusted to mutate query memory. Rebuild from a
		// fresh stable durable coordinate and judge the resulting facts.
		reconcileAttemptID, _ := randomIdentity[string]("restore-post-reconcile-")
		reconciled, restoreErr := journalrestore.RestoreV2(ctx, journalrestore.InputV2{Reader: adapter, EventRegistry: registry, Reducers: reducers, HostAuthorityID: config.HostAuthorityID, PriorRuntimeEpochID: epoch, RecoveryAttemptID: reconcileAttemptID, Budgets: budgets})
		if restoreErr != nil {
			host.degrade("reconciliation_restore")
			_ = host.Close(context.Background())
			return nil, &Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation_restore"}
		}
		if err = authority.RestoreCurrentEpoch(epoch, reconciled); err != nil {
			_ = host.Close(context.Background())
			return nil, err
		}
		postRestore = reconciled
		capabilities, err = composeRuntimeCapabilities(adapter, registry, reducers, budgets, config.HostAuthorityID, epoch, postRestore, config)
		if err != nil {
			_ = host.Close(context.Background())
			return nil, err
		}
		progress.actions = capabilities.actions
	}
	// A reconciler completing is not itself evidence that ambiguity was
	// resolved. Readiness depends on the resulting authority state, so a no-op,
	// partial, or dishonest reconciler cannot wave the barrier open.
	if pendingReconciliation(authority) {
		host.degrade("reconciler_required")
		_ = host.Close(context.Background())
		return nil, &Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciler_required"}
	}
	host.capabilities = capabilities
	host.refresh = func(refreshContext context.Context) error {
		attempt, identityErr := randomIdentity[string]("restore-live-")
		if identityErr != nil {
			return identityErr
		}
		result, restoreErr := journalrestore.RestoreV2(refreshContext, journalrestore.InputV2{Reader: adapter, EventRegistry: registry, Reducers: reducers, HostAuthorityID: config.HostAuthorityID, PriorRuntimeEpochID: epoch, RecoveryAttemptID: attempt, Budgets: budgets})
		if restoreErr != nil {
			return restoreErr
		}
		return authority.RestoreCurrentEpoch(epoch, result)
	}
	if err := host.transition(HostReady); err != nil {
		_ = host.Close(context.Background())
		return nil, err
	}
	host.startMonitor(context.Background(), config.MonitorInterval)
	return host, nil
}

func restoredAuthority(databaseID journalv2.DatabaseID, epoch journalv2.RuntimeEpochID, restored journalrestore.ResultV2, registry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2) (*projection.AuthorityV2, *ProgressService, error) {
	authority, err := projection.NewAuthorityV2(databaseID, registry, reducers)
	if err != nil {
		return nil, nil, err
	}
	if err := authority.Restore(epoch, restored); err != nil {
		return nil, nil, err
	}
	progress, err := NewProgressService(authority, nil)
	if err != nil {
		return nil, nil, err
	}
	return authority, progress, nil
}

func composeRuntimeCapabilities(adapter *storegapdb.Adapter, registry journalv2.EventRegistryV2, reducers journalv2.ReducerSetV2, budgets journalrestore.BudgetsV2, hostAuthorityID journalv2.HostAuthorityID, epoch journalv2.RuntimeEpochID, restored journalrestore.ResultV2, config Config) (runtimeCapabilities, error) {
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	states := restored.Playback().States()
	entries := make([]journalv2.ProjectionRestoreEntryV2, len(states))
	for index, state := range states {
		entries[index] = journalv2.ProjectionRestoreEntryV2{Coordinate: state.Coordinate(), LogicalVersion: state.LogicalVersion(), LastEventID: state.LastEventID(), CanonicalState: state.CanonicalState(), StateDigest: state.StateDigest(), Tombstone: state.Tombstone()}
	}
	seed := journalv2.ProjectionRestoreSeedV2{ThroughRecordRevision: restored.StoreRevision().CurrentRevision(), RollingPrefixDigest: restored.RollingPrefixDigest(), CanonicalProjectionDigest: restored.Playback().CanonicalProjectionDigest(), Entries: entries}
	if err := tracker.Restore(seed); err != nil {
		return runtimeCapabilities{}, err
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	epochSource := runtimeEpochSource{epoch: epoch}
	missionPort, err := storegapdb.NewMissionAuthorityPortV2(adapter, coordinator)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	readinessPort, ok := missionPort.(mission.ReadinessAuthorityPortV2)
	if !ok {
		return runtimeCapabilities{}, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "readiness_port"}
	}
	missionService, err := mission.NewMissionAuthorityServiceV2(missionPort, epochSource, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	readinessService, err := mission.NewReadinessAuthorityServiceV2(readinessPort, epochSource, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	decompositionService, err := mission.NewDecompositionAuthorityServiceV2(missionPort, epochSource, config.DecompositionProvider, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	actions, err := newAuthorityActionCensusSource(readinessPort, config.CapabilityReadiness)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	executionPort, err := adapter.ExecutionAuthorityPortV2(coordinator)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	executionService, err := execution.NewExecutionAuthorityServiceV2(executionPort, epochSource, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	workspacePort, stagePort, err := adapter.WorkspaceAuthorityPortsV2(coordinator, registry, reducers, hostAuthorityID, budgets)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	workspaceService, err := execution.NewWorkspaceAuthorityServiceV2(workspacePort, epochSource, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	stageService, err := workspace.NewStageAuthorityServiceV2(stagePort, epochSource, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	handoffService, err := adapter.HandoffAuthorityServiceV2(coordinator, registry, reducers, hostAuthorityID, budgets, epochSource, config.CandidateVerifier)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	reviewService, err := adapter.ReviewAuthorityServiceV2(coordinator, registry, reducers, hostAuthorityID, budgets, epochSource)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	acceptanceService, err := adapter.AcceptanceAuthorityServiceV2(coordinator, registry, reducers, hostAuthorityID, budgets, epochSource)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	integrationService, err := adapter.IntegrationAuthorityServiceV2(coordinator, registry, reducers, hostAuthorityID, budgets, epochSource, config.IntegrationGit)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	cleanupService, err := adapter.IntegrationCleanupServiceV2(coordinator, registry, reducers, hostAuthorityID, budgets, epochSource, config.IntegrationGit)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	effectPort, err := adapter.EffectAuthorityPortV2(coordinator)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	effectService, err := execution.NewEffectAuthorityServiceV2(effectPort, epochSource, registry)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	artifactPort, err := storegapdb.NewArtifactAuthorityPortV2(coordinator)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	workgraphPort, err := storegapdb.NewWorkgraphAuthorityPortV2(coordinator)
	if err != nil {
		return runtimeCapabilities{}, err
	}
	checkpointEvery := config.CheckpointEvery
	if checkpointEvery == 0 {
		checkpointEvery = 1000
	}
	scheduler := &checkpointScheduler{tracker: tracker, coordinator: coordinator, reader: adapter, epoch: epoch, every: checkpointEvery}
	return runtimeCapabilities{mission: missionService, readiness: readinessService, decomposition: decompositionService, execution: executionService, workspace: workspaceService, stage: stageService, handoff: handoffService, review: reviewService, acceptance: acceptanceService, integration: integrationService, cleanup: cleanupService, effect: effectService, artifact: artifactPort, workgraph: workgraphPort, actions: actions, checkpoints: scheduler}, nil
}

func pendingReconciliation(query projection.QueryPortV2) bool {
	for _, family := range []string{"operation", "external_effect"} {
		cursor := ""
		for page := 0; page < MaxProgressPages; page++ {
			result, err := query.List(projection.ListQueryV2{Family: family, Limit: projection.MaxQueryPageV2, Cursor: cursor})
			if err != nil {
				return true
			}
			for _, item := range result.Items {
				state := selectiveState(item.CanonicalState).authority
				if state == "ambiguous" || state == "reconciliation_required" {
					return true
				}
			}
			cursor = result.NextCursor
			if cursor == "" {
				break
			}
		}
	}
	return false
}

// BuildReducerSetV2 composes the exact closed registry. It is intentionally
// explicit so adding a family without its capability-owned reducer fails.
func BuildReducerSetV2(registry journalv2.EventRegistryV2) (journalv2.ReducerSetV2, error) {
	missionRegistration := journalv2.ReducerRegistration{Family: "mission", EventSchemaVersion: 1, ReducerVersion: 1, Owner: "internal/mission", ImplementationDigest: mission.MissionReducerImplementationDigestV2(), Reduce: mission.ReduceMissionEventV2}
	registrations := []journalv2.ReducerRegistration{RuntimeReducerRegistrationV2(), {Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Owner: "internal/controljournal/application", ImplementationDigest: journalv2.OperationReducerImplementationDigestV2(), Reduce: journalv2.ReduceOperationStateV2}, missionRegistration}
	registrations = append(registrations, artifact.ReducerRegistrationsV2()...)
	registrations = append(registrations, workgraph.ReducerRegistrationsV2()...)
	registrations = append(registrations, execution.ExecutionReducerRegistrationsV2()...)
	registrations = append(registrations, workspace.ReducerRegistrationsV2()...)
	registrations = append(registrations, handoff.ReducerRegistrationsV2()...)
	registrations = append(registrations, review.ReducerRegistrationsV2()...)
	registrations = append(registrations, integration.AcceptanceReducerRegistrationsV2()...)
	registrations = append(registrations, integration.IntegrationReducerRegistrationsV2()...)
	registrations = append(registrations, execution.EffectReducerRegistrationsV2()...)
	registrations = append(registrations, controlstore.OutboxReducerRegistrationsV2()...)
	return journalv2.NewReducerSetV2(registry, canonicalRegistrations(registrations))
}
