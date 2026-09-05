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
	Reconcile(context.Context, projection.QueryPortV2, journalv2.RuntimeEpochID) error
}

type Config struct {
	ControlRoot                 string
	HostAuthorityID             journalv2.HostAuthorityID
	AuthorityBindingOperationID string
	M1AdoptionRecordPath        string
	Sidecar                     sidecargapdb.LifecycleConfig
	RestoreBudgets              journalrestore.BudgetsV2
	Reconciler                  Reconciler
	Actions                     ActionCensusSource
	MonitorInterval             time.Duration
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
	authority, err := projection.NewAuthorityV2(databaseID, registry, reducers)
	if err != nil {
		return nil, err
	}
	if err := authority.Restore(epoch, postRestore); err != nil {
		return nil, err
	}
	progress, err := NewProgressService(authority, config.Actions)
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
	if config.Reconciler != nil {
		if err := config.Reconciler.Reconcile(ctx, authority, epoch); err != nil {
			host.degrade("reconciliation")
			_ = host.Close(context.Background())
			return nil, &Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation"}
		}
	}
	// A reconciler completing is not itself evidence that ambiguity was
	// resolved. Readiness depends on the resulting authority state, so a no-op,
	// partial, or dishonest reconciler cannot wave the barrier open.
	if pendingReconciliation(authority) {
		host.degrade("reconciler_required")
		_ = host.Close(context.Background())
		return nil, &Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciler_required"}
	}
	if err := host.transition(HostReady); err != nil {
		_ = host.Close(context.Background())
		return nil, err
	}
	host.startMonitor(context.Background(), config.MonitorInterval)
	return host, nil
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
