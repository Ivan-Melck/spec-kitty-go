package productionruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/projection"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type hostOwnerProbe struct {
	database journalv2.DatabaseID
	probes   atomic.Int64
	closed   atomic.Int64
	fail     atomic.Bool
}

func (o *hostOwnerProbe) DatabaseID() journalv2.DatabaseID { return o.database }
func (o *hostOwnerProbe) Probe(context.Context) error {
	o.probes.Add(1)
	if o.fail.Load() {
		return fmt.Errorf("injected owner loss")
	}
	return nil
}
func (o *hostOwnerProbe) Close(context.Context) error { o.closed.Add(1); return nil }

type fixtureCommitPort struct{ revision atomic.Uint64 }

type checkpointProbe struct {
	calls atomic.Int64
}

func (p *checkpointProbe) MaybeCheckpoint(context.Context) error {
	p.calls.Add(1)
	return nil
}

type recordingCheckpointPublisher struct {
	delegate checkpointPublisher
	calls    atomic.Int64
}

func (p *recordingCheckpointPublisher) MaybeCheckpoint(ctx context.Context) error {
	p.calls.Add(1)
	return p.delegate.MaybeCheckpoint(ctx)
}

type refusingCandidateVerifier struct{}

func (refusingCandidateVerifier) Verify(context.Context, handoff.AuthorityScope, string) (handoff.CandidateEvidence, error) {
	return handoff.CandidateEvidence{}, fmt.Errorf("test verifier is fail-closed")
}
func (refusingCandidateVerifier) RevalidateRefusal(context.Context, handoff.Refusal) error {
	return nil
}

type refusingIntegrationGit struct{}

func (refusingIntegrationGit) TargetHead(context.Context, ids.RepositoryID, string) (string, error) {
	return "", fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) InstallFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, error) {
	return integration.IntegrationFenceObservationV2{}, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) ObserveFence(context.Context, integration.IntegrationFenceIntentV2) (integration.IntegrationFenceObservationV2, bool, error) {
	return integration.IntegrationFenceObservationV2{}, false, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) EnsureWorkspace(context.Context, integration.IntegrationWorkspaceIntentV2) (integration.WorkspaceFacts, error) {
	return integration.WorkspaceFacts{}, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) MergeCandidate(context.Context, integration.IntegrationMergeIntentV2, integration.WorkspaceFacts) (string, *integration.ConflictEvidence, error) {
	return "", nil, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) CompareAndSwapTarget(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, error) {
	return integration.IntegrationPublishObservationV2{}, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) ObserveTargetPublication(context.Context, integration.IntegrationPublishIntentV2) (integration.IntegrationPublishObservationV2, bool, error) {
	return integration.IntegrationPublishObservationV2{}, false, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) CleanupWorkspace(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	return integration.IntegrationCleanupObservationV2{}, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) ObserveCleanup(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, bool, error) {
	return integration.IntegrationCleanupObservationV2{}, false, fmt.Errorf("test git is fail-closed")
}
func (refusingIntegrationGit) IsAncestor(context.Context, ids.RepositoryID, string, string) (bool, error) {
	return false, fmt.Errorf("test git is fail-closed")
}

type cancellingEffectDispatcher struct {
	cancel   context.CancelFunc
	expected execution.EffectProviderObservationV2
	applies  atomic.Int64
}

type cancellingCleanupGit struct {
	refusingIntegrationGit
	cancel context.CancelFunc
}

func (g cancellingCleanupGit) CleanupWorkspace(context.Context, integration.IntegrationCleanupIntentV2) (integration.IntegrationCleanupObservationV2, error) {
	g.cancel()
	return integration.IntegrationCleanupObservationV2{}, context.Canceled
}

type cleanupAuthorityProbe struct {
	attempt          integration.IntegrationAttemptStateV2
	attemptRevision  uint64
	source           journalv2.OperationStateV2
	sourceRevision   uint64
	resolution       integration.IntegrationOperationResolutionV2
	providerRevision uint64
}

func (p *cleanupAuthorityProbe) ResolveIntegrationCleanupOperation(context.Context, journalv2.OperationBindingV2) (integration.IntegrationOperationResolutionV2, error) {
	if p.resolution.Kind == 0 {
		return integration.IntegrationOperationResolutionV2{Kind: integration.AcceptanceOperationAbsentV2}, nil
	}
	return p.resolution, nil
}
func (p *cleanupAuthorityProbe) ObserveIntegrationCleanupAuthority(context.Context, integration.IntegrationCleanupRequestV2) (integration.IntegrationCleanupAuthorityObservationV2, error) {
	source, err := journalv2.EncodeOperationStateV2(p.source)
	if err != nil {
		return integration.IntegrationCleanupAuthorityObservationV2{}, err
	}
	digest, err := journalv2.ReducerStateDigestV2(source)
	if err != nil {
		return integration.IntegrationCleanupAuthorityObservationV2{}, err
	}
	return integration.IntegrationCleanupAuthorityObservationV2{
		Attempt: p.attempt, AttemptRecordRevision: p.attemptRevision,
		SourceOperation: p.source, SourceOperationRecordRevision: p.sourceRevision, SourceOperationValueDigest: digest,
	}, nil
}
func (p *cleanupAuthorityProbe) PublishIntegrationCleanup(_ context.Context, plan integration.IntegrationCleanupPlanV2) (integration.IntegrationAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return integration.IntegrationAckV2{}, err
	}
	for _, event := range sealed.Events() {
		if event.Family() != "integration_attempt" {
			continue
		}
		next, reduceErr := integration.ReduceIntegrationAttemptEventV2(p.attempt.CanonicalBytes(), event)
		if reduceErr != nil {
			return integration.IntegrationAckV2{}, reduceErr
		}
		p.attempt, err = integration.DecodeIntegrationAttemptStateV2(next)
		if err != nil {
			return integration.IntegrationAckV2{}, err
		}
	}
	p.providerRevision++
	p.attemptRevision = p.providerRevision
	p.resolution = integration.IntegrationOperationResolutionV2{Kind: integration.AcceptanceOperationExactV2, State: sealed.OperationState(), RecordRevision: p.providerRevision}
	return integration.IntegrationAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: p.providerRevision, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
}
func (p *cleanupAuthorityProbe) ReconcileIntegrationCleanup(context.Context, integration.IntegrationCleanupPlanV2) (integration.IntegrationAckV2, error) {
	return integration.IntegrationAckV2{}, errors.New("unexpected cleanup reconciliation")
}

func (d *cancellingEffectDispatcher) ObserveEffect(context.Context, tracker.ExternalWorkRef) (execution.EffectProviderObservationV2, error) {
	return d.expected, nil
}
func (d *cancellingEffectDispatcher) ApplyEffect(context.Context, execution.EffectConditionalMutationV2) (execution.EffectMutationResultV2, error) {
	d.applies.Add(1)
	d.cancel()
	return execution.EffectMutationResultV2{Kind: execution.EffectMutationIndeterminateV2}, context.Canceled
}
func (d *cancellingEffectDispatcher) CompensateEffect(context.Context, execution.EffectConditionalMutationV2) (execution.EffectMutationResultV2, error) {
	return execution.EffectMutationResultV2{}, errors.New("unexpected compensation")
}

type missionCreatingReconciler struct{ retained *ReconciliationCommands }

func (r *missionCreatingReconciler) Reconcile(ctx context.Context, commands *ReconciliationCommands) error {
	r.retained = commands
	epoch := commands.RuntimeEpochID()
	digest, _ := journalv2.ReducerStateDigestV2([]byte(`{"definition":"reconciled"}`))
	state, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-runtime", MissionID: "mission-reconciled", DefinitionRevision: "definition-runtime", DefinitionDigest: digest,
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "active", LastOperationID: "operation-reconciled-create", ObservedAt: "2026-09-05T11:59:00Z",
	})
	if err != nil {
		return err
	}
	_, err = commands.ExecuteMission(ctx, mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-reconciled-create", ProjectID: "project-runtime", MissionID: "mission-reconciled",
		ActorID: "actor-runtime", ProcessID: "process-reconciler", OriginatingRuntimeEpochID: epoch,
		DoctrineRevision: "doctrine-runtime", ProfileRevision: "profile-runtime", PolicyRevision: "policy-runtime",
		ObservedAt: "2026-09-05T11:59:00Z", EventType: mission.MissionEventCreatedV2, Successor: state,
	})
	return err
}

func (p *fixtureCommitPort) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	revision := p.revision.Add(1)
	return journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), revision, revision, plan.MutationCount(), plan.AssertionCount())
}

func TestHostBarrierRejectsEveryNonReadyStateAndStaleEpochBeforeCommand(t *testing.T) {
	host, calls := hostFixture(t, "database-barrier")
	defer host.Close(t.Context())
	command := func(CommandContext) error { calls.Add(1); return nil }
	for _, state := range []HostState{HostRecovering, HostReconciling} {
		if state == HostReconciling {
			if err := host.transition(state); err != nil {
				t.Fatal(err)
			}
		}
		if err := host.executeBarrierProbe("epoch-current", command); !IsKind(err, ErrorNotReady) {
			t.Fatalf("state=%s err=%v", state, err)
		}
	}
	if err := host.transition(HostReady); err != nil {
		t.Fatal(err)
	}
	if err := host.executeBarrierProbe("epoch-predecessor", command); !IsKind(err, ErrorStaleEpoch) {
		t.Fatalf("stale epoch err=%v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("command called before authority: %d", calls.Load())
	}
	if err := host.executeBarrierProbe("epoch-current", command); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ready calls=%d", calls.Load())
	}
	host.degrade("test")
	if err := host.executeBarrierProbe("epoch-current", command); !IsKind(err, ErrorNotReady) {
		t.Fatalf("degraded err=%v", err)
	}
}

func TestHostIndependentCommandsRunConcurrentlyWithoutGlobalCommandMutex(t *testing.T) {
	host, _ := hostFixture(t, "database-concurrency")
	defer host.Close(t.Context())
	_ = host.transition(HostReconciling)
	_ = host.transition(HostReady)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var failures atomic.Int64
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := host.executeBarrierProbe("epoch-current", func(CommandContext) error {
				entered <- struct{}{}
				<-release
				return nil
			}); err != nil {
				failures.Add(1)
			}
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("commands were serialized")
		}
	}
	close(release)
	wait.Wait()
	if failures.Load() != 0 {
		t.Fatalf("command failures=%d", failures.Load())
	}
}

func TestHostProjectionRefreshRetriesConcurrentStablePrefixMovement(t *testing.T) {
	host, _ := hostFixture(t, "database-refresh-retry")
	defer host.Close(t.Context())
	if err := host.transition(HostReconciling); err != nil {
		t.Fatal(err)
	}
	if err := host.transition(HostReady); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	host.refresh = func(context.Context) error {
		if calls.Add(1) < 4 {
			return errors.New("stable prefix moved")
		}
		return nil
	}
	if err := host.refreshProjection(t.Context()); err != nil {
		t.Fatalf("transient concurrent refresh degraded host: %v", err)
	}
	if calls.Load() != 4 || host.State() != HostReady {
		t.Fatalf("calls=%d state=%s", calls.Load(), host.State())
	}
}

func TestHostProjectionRefreshRetryIsBoundedAndCancellationAware(t *testing.T) {
	host, _ := hostFixture(t, "database-refresh-bound")
	defer host.Close(t.Context())
	if err := host.transition(HostReconciling); err != nil {
		t.Fatal(err)
	}
	if err := host.transition(HostReady); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	host.refresh = func(context.Context) error {
		calls.Add(1)
		return errors.New("permanent refresh failure")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := host.refreshProjection(ctx); !IsKind(err, ErrorProjection) {
		t.Fatalf("cancelled refresh error=%v", err)
	}
	if calls.Load() != 1 || host.State() != HostDegraded {
		t.Fatalf("calls=%d state=%s", calls.Load(), host.State())
	}
}

func TestHostSecondOwnerForSameDatabaseRefusesAndDifferentDatabasesProgress(t *testing.T) {
	first, _ := hostFixture(t, "database-owner")
	defer first.Close(t.Context())
	authority := initializedProjection(t, "database-owner")
	progress, _ := NewProgressService(authority, nil)
	secondOwner := &hostOwnerProbe{database: "database-owner"}
	if _, err := newHost(secondOwner, "epoch-second", authority, progress); !IsKind(err, ErrorOwnerConflict) {
		t.Fatalf("second owner err=%v", err)
	}
	different, _ := hostFixture(t, "database-independent")
	if err := different.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHostMonitorClosesBarrierOnOwnerLossAndShutdownIsIdempotent(t *testing.T) {
	host, _ := hostFixture(t, "database-monitor")
	owner := host.owner.(*hostOwnerProbe)
	_ = host.transition(HostReconciling)
	_ = host.transition(HostReady)
	host.startMonitor(context.Background(), time.Millisecond)
	owner.fail.Store(true)
	deadline := time.Now().Add(time.Second)
	for host.State() != HostDegraded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if host.State() != HostDegraded {
		t.Fatal("owner loss did not close command admission")
	}
	if err := host.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := host.Close(t.Context()); err != nil || host.State() != HostClosed || owner.closed.Load() != 1 {
		t.Fatalf("idempotent close state=%s calls=%d err=%v", host.State(), owner.closed.Load(), err)
	}
}

func TestHostLifecycleRejectsBackwardAndCeremonyTransitions(t *testing.T) {
	host, _ := hostFixture(t, "database-lifecycle")
	defer host.Close(t.Context())
	if err := host.transition(HostReady); err == nil {
		t.Fatal("recovery skipped reconciliation")
	}
	if err := host.transition(HostReconciling); err != nil {
		t.Fatal(err)
	}
	if err := host.transition(HostReady); err != nil {
		t.Fatal(err)
	}
	if err := host.transition(HostReconciling); err == nil {
		t.Fatal("ready host moved backward")
	}
	if HostState("specify") == HostReady || HostState("plan") == HostReady || HostState("review") == HostReady {
		t.Fatal("workflow ceremony became host authority")
	}
}

func TestPossibleMutationRefreshesDurableTruthDespiteTypedErrorAndCancellation(t *testing.T) {
	host, _ := hostFixture(t, "database-possible-mutation")
	defer host.Close(t.Context())
	if err := host.transition(HostReconciling); err != nil {
		t.Fatal(err)
	}
	if err := host.transition(HostReady); err != nil {
		t.Fatal(err)
	}
	checkpoints := &checkpointProbe{}
	host.capabilities.checkpoints = checkpoints
	var refreshes atomic.Int64
	host.refresh = func(ctx context.Context) error {
		if ctx == nil || ctx.Err() != nil {
			t.Fatal("refresh inherited cancelled request authority")
		}
		if got := ctx.Value(possibleMutationContextKey{}); got != "retained" {
			t.Fatalf("refresh lost request values: %v", got)
		}
		refreshes.Add(1)
		return nil
	}
	cancelled, cancel := context.WithCancel(context.WithValue(t.Context(), possibleMutationContextKey{}, "retained"))
	cancel()
	primary := errors.New("typed decision required after durable progress")
	if err := host.afterPossibleMutation(cancelled, primary); !errors.Is(err, primary) {
		t.Fatalf("typed capability error was not preserved: %v", err)
	}
	if refreshes.Load() != 1 || host.State() != HostReady {
		t.Fatalf("possible mutation refreshes=%d state=%s", refreshes.Load(), host.State())
	}
	if checkpoints.calls.Load() != 0 {
		t.Fatalf("typed refusal published %d checkpoints", checkpoints.calls.Load())
	}
}

type possibleMutationContextKey struct{}

func TestHostIntegrationCleanupPublishesPendingProgressDespiteFailureAndCancellation(t *testing.T) {
	host, _ := hostFixture(t, "database-cleanup-route")
	defer host.Close(t.Context())
	if err := host.transition(HostReconciling); err != nil {
		t.Fatal(err)
	}
	if err := host.transition(HostReady); err != nil {
		t.Fatal(err)
	}
	_, governanceContext := productionRuntimeWorkRevision(t, "project-1", "mission-1")
	governance, err := execution.ExecutionGovernanceFromContextV2(governanceContext)
	if err != nil {
		t.Fatal(err)
	}
	attempt := integration.IntegrationAttemptStateV2{
		SchemaVersion: 1, LogicalVersion: 5, Stage: integration.IntegrationAttemptCompletedV2,
		AttemptID: "attempt-cleanup", OperationID: "operation-integration-cleanup-source",
		ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-cleanup", WorkID: "work-cleanup",
		AcceptanceID: "acceptance-cleanup", AcceptanceDigest: repeatByte('a', 64),
		RepositoryID: "repository-cleanup", TargetRef: "refs/heads/main", Candidate: repeatByte('b', 40), ExpectedOld: repeatByte('c', 40),
		TargetGeneration: 1, FenceTokenHash: repeatByte('d', 64), Result: repeatByte('e', 40), ObservedTarget: repeatByte('e', 40),
		Workspace:     integration.WorkspaceFacts{Path: "/tmp/spec-kitty-cleanup", Registered: true, Detached: true, Clean: true, Head: repeatByte('e', 40), CandidateReachable: true},
		FenceEffectID: "effect-fence-cleanup", WorkspaceEffectID: "effect-workspace-cleanup", MergeEffectID: "effect-merge-cleanup",
		PublishEffectID: "effect-publish-cleanup", CleanupEffectID: "effect-cleanup", CleanupStatus: integration.CleanupEligible,
		RuntimeEpochID: "epoch-current", ActorID: "actor-integrator", ProcessID: "process-integrator", Governance: governance,
		ObservedAt: "2026-09-05T12:10:00Z",
	}
	if err := attempt.Validate(); err != nil {
		t.Fatalf("cleanup fixture attempt: %v", err)
	}
	source, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{
		OperationID: attempt.OperationID, LogicalVersion: 5, Status: journalv2.StatusResolved,
		ResultKind: "integration.result", ResultSchemaVersion: 1, CanonicalResult: attempt.CanonicalBytes(),
		EventIDs: []journalv2.EventID{"event-integration-cleanup-source"}, EffectIDs: []journalv2.EffectID{attempt.CleanupEffectID},
		LastPublicationID: journalv2.PublicationID(repeatByte('3', 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	port := &cleanupAuthorityProbe{attempt: attempt, attemptRevision: 40, source: source, sourceRevision: 40, providerRevision: 40}
	requestContext, cancel := context.WithCancel(context.WithValue(t.Context(), possibleMutationContextKey{}, "retained"))
	service, err := integration.NewIntegrationCleanupServiceV2(port, runtimeEpochSource{epoch: "epoch-current"}, cancellingCleanupGit{cancel: cancel}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	host.capabilities.cleanup = service
	query := progressFixture()
	progress, err := NewProgressService(query, nil)
	if err != nil {
		t.Fatal(err)
	}
	host.progress = progress
	host.refresh = func(ctx context.Context) error {
		if ctx.Err() != nil || ctx.Value(possibleMutationContextKey{}) != "retained" {
			return errors.New("cleanup refresh inherited cancellation or lost values")
		}
		query.base.Revision++
		for index := range query.items {
			query.items[index].ProjectionRevision = query.base.Revision
		}
		coordinate, coordinateErr := integration.IntegrationAttemptCoordinateV2(port.attempt.ProjectID, port.attempt.MissionID, port.attempt.WorkRevisionID, port.attempt.WorkID, port.attempt.AttemptID)
		if coordinateErr != nil {
			return coordinateErr
		}
		query.items = append(query.items, projection.AggregateViewV2{
			DatabaseID: query.base.DatabaseID, ProjectionRevision: query.base.Revision, RuntimeEpochID: query.base.RuntimeEpochID,
			ProjectionDigest: repeatByte('b', 64), Coordinate: coordinate, LogicalVersion: journalv2.LogicalVersion(port.attempt.LogicalVersion),
			LastEventID: "event-cleanup-pending", LastPublicationID: journalv2.PublicationID(repeatByte('1', 64)),
			StateDigest: repeatByte('2', 64), CanonicalState: port.attempt.CanonicalBytes(),
		})
		return nil
	}
	checkpoints := &checkpointProbe{}
	host.capabilities.checkpoints = checkpoints
	request := integration.IntegrationCleanupRequestV2{
		CleanupOperationID: "operation-cleanup-route", IntegrationOperationID: attempt.OperationID,
		ProjectID: attempt.ProjectID, MissionID: attempt.MissionID, WorkRevisionID: attempt.WorkRevisionID, WorkID: attempt.WorkID,
		AttemptID: attempt.AttemptID, RepositoryID: attempt.RepositoryID, TargetRef: attempt.TargetRef, RuntimeEpochID: attempt.RuntimeEpochID,
		ActorID: "actor-cleanup", ProcessID: "process-cleanup", Governance: governance, ObservedAt: time.Date(2026, 9, 5, 12, 11, 0, 0, time.UTC),
	}
	if _, observeErr := port.ObserveIntegrationCleanupAuthority(t.Context(), request); observeErr != nil {
		t.Fatalf("cleanup authority fixture: %v", observeErr)
	}
	if _, err = host.ExecuteIntegrationCleanup(requestContext, "epoch-current", request); !integration.IsKind(err, integration.KindGitFailure) || requestContext.Err() != context.Canceled {
		t.Fatalf("cleanup failure=%v cancellation=%v", err, requestContext.Err())
	}
	item, err := progress.Get("project-1", "mission-1", ProgressIntegration, "attempt-cleanup")
	if err != nil {
		t.Fatalf("pending cleanup was not visible through retained progress: %v", err)
	}
	pending, err := integration.DecodeIntegrationAttemptStateV2(item.CanonicalState)
	if err != nil || pending.CleanupStatus != integration.CleanupPending {
		t.Fatalf("cleanup progress status=%s err=%v", pending.CleanupStatus, err)
	}
	if checkpoints.calls.Load() != 0 {
		t.Fatalf("failed cleanup published checkpoint calls=%d", checkpoints.calls.Load())
	}
}

func TestProductionReducerCompositionIsCompleteAndRuntimePlaybackPure(t *testing.T) {
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := reducers.Descriptor()
	if err != nil || descriptor.Digest().IsZero() {
		t.Fatalf("descriptor err=%v", err)
	}
	coordinate, _ := journalv2.NewAggregateCoordinate("runtime_host", "host-runtime", journalv2.Scope{Kind: journalv2.ScopeHost})
	payload := []byte(`{"database_id":"database-runtime","host_authority_id":"host-runtime","lifecycle":"ready","runtime_epoch_id":"epoch-runtime","schema_version":2}`)
	event, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: "event-runtime", EventType: "runtime_host.epoch_admitted", Family: "runtime_host", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: 1, OperationID: "operation-runtime", OperationLogicalVersion: 1, PublicationID: journalv2.PublicationID(repeatByte('a', 64)), CommittedRuntimeEpochID: "epoch-runtime", EventIndex: 0, EventCount: 1, Payload: payload}, registry)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := reducers.Apply([]byte(`{}`), event)
	if err != nil || string(state) != string(payload) {
		t.Fatalf("runtime reduction state=%s err=%v", state, err)
	}
}

type memoryAuthorityPort struct {
	mu       sync.Mutex
	revision uint64
	records  map[string]storegapdb.Record
}

func newMemoryAuthorityPort() *memoryAuthorityPort {
	return &memoryAuthorityPort{records: make(map[string]storegapdb.Record)}
}
func (p *memoryAuthorityPort) Get(_ context.Context, key string) (storegapdb.Record, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	record, ok := p.records[key]
	if !ok {
		return storegapdb.Record{}, &storegapdb.Error{Kind: storegapdb.OutcomeNotFound, Operation: "get", Recovery: storegapdb.RecoveryRead}
	}
	record.Value = append([]byte(nil), record.Value...)
	return record, nil
}
func (p *memoryAuthorityPort) ConditionalMutate(_ context.Context, _ string, mutation storegapdb.Mutation, _ storegapdb.AckMode) (storegapdb.MutationResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.records[mutation.Key]; mutation.Condition.Kind == storegapdb.ConditionAbsent && exists {
		return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "put", Recovery: storegapdb.RecoveryRead}
	}
	p.revision++
	p.records[mutation.Key] = storegapdb.Record{Key: mutation.Key, Value: append([]byte(nil), mutation.Value...), Revision: p.revision}
	return storegapdb.MutationResult{Revision: p.revision, DurableThrough: p.revision, Ack: storegapdb.AckDurable, MutationCount: 1}, nil
}
func (p *memoryAuthorityPort) AdmitRuntimeEpoch(_ context.Context, batch storegapdb.RuntimeEpochBatch) (storegapdb.MutationResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	mutations := []storegapdb.Mutation{batch.Operation, batch.Event, batch.AggregateHead, batch.EpochRecord, batch.CurrentBinding}
	for _, mutation := range mutations {
		current, exists := p.records[mutation.Key]
		switch mutation.Condition.Kind {
		case storegapdb.ConditionAbsent:
			if exists {
				return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "epoch", Recovery: storegapdb.RecoveryRead}
			}
		case storegapdb.ConditionRevision:
			if !exists || current.Revision != mutation.Condition.ExpectedRevision {
				return storegapdb.MutationResult{}, &storegapdb.Error{Kind: storegapdb.OutcomeConflict, Operation: "epoch", Recovery: storegapdb.RecoveryRead}
			}
		}
	}
	p.revision++
	for _, mutation := range mutations {
		p.records[mutation.Key] = storegapdb.Record{Key: mutation.Key, Value: append([]byte(nil), mutation.Value...), Revision: p.revision}
	}
	return storegapdb.MutationResult{Revision: p.revision, DurableThrough: p.revision, Ack: storegapdb.AckDurable, MutationCount: 5}, nil
}
func (p *memoryAuthorityPort) Inspect(context.Context) (storegapdb.Inspection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return storegapdb.Inspection{DatabaseID: "database-binding", Healthy: true, CurrentRevision: p.revision, DurableThrough: p.revision}, nil
}

func TestAuthorityBindingIsImmutableAndWrongHealthyDatabaseRefuses(t *testing.T) {
	port := newMemoryAuthorityPort()
	binding := AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: "host-binding", DatabaseID: "database-binding", AdmittedTupleSHA: storegapdb.AdmittedTupleSHA256, SidecarSHA: storegapdb.AdmittedSidecarSHA256, WireProtocol: storegapdb.AdmittedWireVersion, StorageProtocol: storegapdb.AdmittedStorageVersion, CreatedOperationID: "operation-bind"}
	if err := ensureAuthorityBinding(t.Context(), port, binding, true); err != nil {
		t.Fatal(err)
	}
	if err := ensureAuthorityBinding(t.Context(), port, binding, false); err != nil {
		t.Fatal(err)
	}
	wrong := binding
	wrong.DatabaseID = "database-healthy-but-wrong"
	if err := ensureAuthorityBinding(t.Context(), port, wrong, false); !IsKind(err, ErrorStartup) {
		t.Fatalf("wrong database admitted: %v", err)
	}
	if port.revision != 1 {
		t.Fatalf("mismatch mutated authority at revision %d", port.revision)
	}
}

type emptyRecoveryReader struct {
	port *memoryAuthorityPort
}

func (r emptyRecoveryReader) CaptureRevisionV2(context.Context) (journalv2.StoreRevisionV2, error) {
	r.port.mu.Lock()
	defer r.port.mu.Unlock()
	return journalv2.NewStoreRevisionV2("database-binding", r.port.revision, r.port.revision)
}
func (r emptyRecoveryReader) VerifyRevisionV2(_ context.Context, expected journalv2.StoreRevisionV2) error {
	r.port.mu.Lock()
	defer r.port.mu.Unlock()
	if expected.CurrentRevision() != r.port.revision {
		return fmt.Errorf("revision moved")
	}
	return nil
}
func (r emptyRecoveryReader) ReadV2(context.Context, journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	return journalv2.RecoveryRecordV2{}, &journalv2.Error{Kind: journalv2.ErrorNotFound, Field: "record"}
}
func (r emptyRecoveryReader) ScanV2(_ context.Context, request journalv2.ScanRequestV2) (journalv2.RecoveryPageV2, error) {
	r.port.mu.Lock()
	revision := r.port.revision
	r.port.mu.Unlock()
	return journalv2.NewRecoveryPageV2(revision, "2026-09-05T00:00:00Z", nil, nil, false)
}

func TestFreshEpochRequiresSealedRestoreAndRotatesPredecessorDurably(t *testing.T) {
	port := newMemoryAuthorityPort()
	binding := AuthorityBindingV2{SchemaVersion: 2, HostAuthorityID: "host-binding", DatabaseID: "database-binding", AdmittedTupleSHA: storegapdb.AdmittedTupleSHA256, SidecarSHA: storegapdb.AdmittedSidecarSHA256, WireProtocol: 1, StorageProtocol: 1, CreatedOperationID: "operation-bind"}
	if err := ensureAuthorityBinding(t.Context(), port, binding, true); err != nil {
		t.Fatal(err)
	}
	registry := journalv2.DefaultEventRegistryV2()
	reducers := fixtureReducers(t, registry)
	recovered, err := journalrestore.RestoreV2(t.Context(), journalrestore.InputV2{Reader: emptyRecoveryReader{port}, EventRegistry: registry, Reducers: reducers, HostAuthorityID: binding.HostAuthorityID, RecoveryAttemptID: "restore-epoch-test", Budgets: journalrestore.DefaultBudgetsV2()})
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := admitFreshEpoch(t.Context(), port, binding, runtimeCurrentV2{}, 0, recovered, "restore-epoch-test")
	if err != nil || epoch == "" {
		t.Fatalf("epoch=%q err=%v", epoch, err)
	}
	current, revision, err := readCurrentEpoch(t.Context(), port)
	if err != nil || current.RuntimeEpochID != epoch || current.PredecessorEpochID != "" || revision != 2 || current.RecoveredStoreRevision != 1 {
		t.Fatalf("current=%+v revision=%d err=%v", current, revision, err)
	}
	if _, err := admitFreshEpoch(t.Context(), port, binding, current, revision, journalrestore.ResultV2{}, "forged"); !IsKind(err, ErrorStartup) {
		t.Fatalf("forged restore admitted: %v", err)
	}
}

func productionRuntimeWorkRevision(t *testing.T, projectID ids.ProjectID, missionID ids.MissionID) (workgraph.RevisionV2, governancecontext.Context) {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{
		ProjectID: projectID, WorkID: "work-runtime", Title: "Exercise production dispatch", Description: "Verify durable progress after cancellation.",
		DesignContext: "Use the single GapDB authority host.",
		Criteria:      []workgraph.CriterionObservation{{ID: "criterion-runtime", Text: "Durable progress remains visible.", Outcome: "green", Verifier: "test", Requirement: workgraph.CriterionRequired}},
		WriteScopes:   []workgraph.WriteScopeObservation{{Path: "internal/productionruntime", Domain: "runtime", Access: workgraph.ScopeWriteExclusive}},
		Provenance:    []workgraph.ScopeReference{{ID: "scope-runtime", Kind: workgraph.ScopePlanConcern}},
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation(projectID, missionID, "artifact-runtime-plan", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-runtime", Kind: workgraph.ScopePlanConcern, MissionID: missionID}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	definition := sha256.Sum256([]byte("work-runtime"))
	finalized, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-runtime", Provider: "fake", ExternalID: "external-runtime", Revision: "revision-runtime", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", definition), CriterionEvidenceHints: map[string]string{"criterion-runtime": "go test"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{
		Observation: finalized, PlanningSourceCommit: strings.Repeat("b", 40),
		MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)},
		DependencyPolicy:  workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-runtime",
		FinalizedAt: time.Date(2026, 9, 5, 12, 2, 0, 0, time.UTC), GovernanceContextID: "governance-runtime",
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind                 governancecontext.EntryKind
		ref, revision, value string
	}{
		{governancecontext.KindConfig, "config.runtime", "policy-runtime", "config"},
		{governancecontext.KindDoctrine, "doctrine.runtime", "doctrine-runtime", "doctrine"},
		{governancecontext.KindProfile, "profile.runtime", "profile-runtime", "profile"},
		{governancecontext.KindMissionType, "mission-type.runtime", "mission-type-runtime", "mission-type"},
	} {
		commitment, commitErr := governancecontext.CommitSHA256([]byte(item.value))
		if commitErr != nil {
			t.Fatal(commitErr)
		}
		entry, entryErr := governancecontext.NewEntry(item.kind, item.ref, item.revision, commitment)
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		entries = append(entries, entry)
	}
	governance, err := governancecontext.NewContext("governance-runtime", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return revision, governance
}

func TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch(t *testing.T) {
	binary := os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	if binary == "" {
		t.Skip("set SPEC_KITTY_WP13_GAPDBD to the exact assertion-extension sidecar")
	}
	binary, _ = filepath.Abs(binary)
	repository, _ := filepath.Abs(filepath.Join("..", ".."))
	root, err := os.MkdirTemp("/tmp", "spk-h-")
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
	reconciler := &missionCreatingReconciler{}
	config := Config{
		ControlRoot:                          data,
		HostAuthorityID:                      "host-real-sidecar",
		AuthorityBindingOperationID:          "operation-bind-real-sidecar",
		M1AdoptionRecordPath:                 filepath.Join(repository, "docs", "programs", "evidence", "m1-adoption.json"),
		AssertionExtensionAdoptionRecordPath: filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-adoption.json"),
		Sidecar: sidecargapdb.LifecycleConfig{
			ExecutablePath: binary, AdmissionRecordPath: filepath.Join(repository, "docs", "programs", "evidence", "m1-assertion-extension-admission.json"),
			RuntimeRoot: runtimeRoot, DataDirectory: data, SupervisorRunID: "supervisor-real-1", ProductPolicySHA256: repeatByte('e', 64),
		},
		MonitorInterval:   250 * time.Millisecond,
		CandidateVerifier: refusingCandidateVerifier{},
		IntegrationGit:    refusingIntegrationGit{},
		Reconciler:        reconciler,
		CheckpointEvery:   1,
	}
	first, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	firstEpoch := first.RuntimeEpochID()
	if first.State() != HostReady || firstEpoch == "" {
		t.Fatalf("first state=%s epoch=%q", first.State(), firstEpoch)
	}
	if first.capabilities.effect == nil || first.capabilities.cleanup == nil {
		t.Fatal("production host omitted effect dispatch or integration cleanup capability")
	}
	progressHandle := first.Progress()
	if _, err := progressHandle.Mission("project-runtime", "mission-reconciled"); err != nil {
		t.Fatalf("reconciliation publication absent after rescan: %v", err)
	}
	watchBase, err := progressHandle.Mission("project-runtime", "mission-reconciled")
	if err != nil {
		t.Fatal(err)
	}
	watch, err := progressHandle.Watch("project-runtime", "mission-reconciled", watchBase.DatabaseID, watchBase.ProjectionRevision)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Cancel()
	if reconciler.retained == nil || reconciler.retained.RuntimeEpochID() != "" {
		t.Fatal("reconciliation capability remained live after startup")
	}
	if _, err := reconciler.retained.ExecuteMission(t.Context(), mission.MissionAuthorityCommandV2{}); !IsKind(err, ErrorNotReady) {
		t.Fatalf("retained reconciliation capability admitted command: %v", err)
	}
	beforeRevision := first.Status().ProjectionRevision
	definitionDigest, _ := journalv2.ReducerStateDigestV2([]byte(`{"definition":"runtime-test"}`))
	missionState, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-runtime", MissionID: "mission-runtime", DefinitionRevision: "definition-runtime", DefinitionDigest: definitionDigest,
		Version: 1, Status: mission.AuthorityMissionActiveV2, Step: "active", LastOperationID: "operation-runtime-create", ObservedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.ExecuteMission(t.Context(), firstEpoch, mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-runtime-create", ProjectID: "project-runtime", MissionID: "mission-runtime",
		ActorID: "actor-runtime", ProcessID: "process-runtime", OriginatingRuntimeEpochID: firstEpoch,
		DoctrineRevision: "doctrine-runtime", ProfileRevision: "profile-runtime", PolicyRevision: "policy-runtime",
		ObservedAt: "2026-09-05T12:00:00Z", EventType: mission.MissionEventCreatedV2, Successor: missionState,
	})
	if err != nil || created.PublicationID() == "" {
		t.Fatalf("typed mission create: result=%+v err=%v", created, err)
	}
	if first.Progress() != progressHandle {
		t.Fatal("successful command replaced the retained progress facade")
	}
	firstProgress, err := progressHandle.Mission("project-runtime", "mission-runtime")
	if err != nil || firstProgress.ProjectionRevision <= beforeRevision || firstProgress.Lifecycle != "active" {
		t.Fatalf("live memory was not refreshed: progress=%+v err=%v", firstProgress, err)
	}
	select {
	case watched := <-watch.C:
		if watched.MissionID != "mission-reconciled" || watched.ProjectionRevision != firstProgress.ProjectionRevision {
			t.Fatalf("existing watch observed wrong acknowledged revision: %+v", watched)
		}
	case watchErr := <-watch.Errors:
		t.Fatalf("existing watch was orphaned by projection refresh: %v", watchErr)
	case <-time.After(2 * time.Second):
		t.Fatal("existing watch was orphaned by projection refresh")
	}

	// Exercise the real public dispatch route after creating its exact durable
	// workgraph and execution authority through the host. The provider cancels
	// the request only after StartAttempt has been acknowledged. The host must
	// still expose that durable progress through the original retained handle,
	// and a failed dispatch must not create a checkpoint side effect.
	revision, governance := productionRuntimeWorkRevision(t, "project-runtime", "mission-runtime")
	workgraphPlan, err := workgraph.BuildFinalizationAuthorityV2(workgraph.FinalizationPlanInputV2{
		Context: workgraph.AuthorityContextV2{
			OperationID: "operation-runtime-workgraph", RuntimeEpochID: firstEpoch,
			ActorID: "actor-runtime", ProcessID: "process-runtime",
			DoctrineRevision: "doctrine-runtime", ProfileRevision: "profile-runtime", PolicyRevision: "policy-runtime",
			Governance: []string{"governance-runtime"}, ObservedCreatedAt: "2026-09-05T12:02:00Z",
			OperationEventID: "event-runtime-workgraph-operation", DomainEventID: "event-runtime-workgraph",
		},
		Revision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = first.PublishWorkgraph(t.Context(), firstEpoch, workgraphPlan); err != nil {
		t.Fatalf("publish workgraph: %v", err)
	}
	now := time.Date(2026, 9, 5, 12, 3, 0, 0, time.UTC)
	admitted, err := first.ExecuteWork(t.Context(), firstEpoch, execution.ExecutionAuthorityRequestV2{
		Kind: execution.ExecutionAdmitV2, OperationID: "operation-runtime-admit", ProjectID: "project-runtime", MissionID: "mission-runtime",
		WorkRevisionID: revision.ID(), WorkID: "work-runtime", RunID: "run-runtime", ActorID: "actor-runtime", ProcessID: "process-runtime",
		LeaseID: "lease-runtime", Generation: 1, ObservedAt: now, Deadline: now.Add(time.Minute), RuntimeEpochID: firstEpoch,
		Governance: governance, OperationEventID: "event-runtime-admit-operation", SlotEventID: "event-runtime-admit-slot",
	})
	if err != nil {
		t.Fatalf("admit execution: %v", err)
	}
	executionGovernance, err := execution.ExecutionGovernanceFromContextV2(governance)
	if err != nil {
		t.Fatal(err)
	}
	target := tracker.ExternalWorkRef{ProjectID: "project-runtime", WorkID: "work-runtime", Provider: "fake", ExternalID: "external-runtime", MetadataSchema: 1, CreatedByOperation: "operation-runtime-workgraph", LastObservation: 1}
	intent := execution.EffectIntentV2{
		EffectID: "effect-runtime", OperationID: "operation-runtime-effect", Target: target, IntendedValue: "actor-runtime",
		ExpectedProviderRevision: 1, RequiredGuarantee: tracker.AssignmentAtomicClaim,
		ActorID: "actor-runtime", ProcessID: "process-runtime", Governance: executionGovernance,
		OriginRuntimeEpochID: firstEpoch, OriginObservedCreatedAt: now.Add(time.Second).Format(time.RFC3339),
	}
	intent.CanonicalRequestDigest, err = execution.EffectRequestDigestV2(intent)
	if err != nil {
		t.Fatal(err)
	}
	effectRequest := execution.EffectAuthorityRequestV2{
		Action: execution.EffectRecordIntentV2, Intent: intent, RuntimeEpochID: firstEpoch, ExecutionSlot: admitted.State,
		ObservedAt: now.Add(time.Second), OperationEventID: "event-runtime-effect-intent-operation", EffectEventID: "event-runtime-effect-intent",
	}
	recorded, err := first.ExecuteEffect(t.Context(), firstEpoch, effectRequest)
	if err != nil {
		t.Fatalf("record effect intent: %v", err)
	}
	operationsBefore, err := progressHandle.List("project-runtime", "mission-runtime", ProgressOperations)
	if err != nil {
		t.Fatal(err)
	}
	checkpointCalls := &recordingCheckpointPublisher{delegate: first.capabilities.checkpoints}
	first.capabilities.checkpoints = checkpointCalls
	expectedObservation, err := execution.NewEffectProviderObservationV2(target, 1, "", "", "absent")
	if err != nil {
		t.Fatal(err)
	}
	dispatchContext, cancelDispatch := context.WithCancel(context.WithValue(t.Context(), possibleMutationContextKey{}, "retained"))
	dispatcher := &cancellingEffectDispatcher{cancel: cancelDispatch, expected: expectedObservation}
	effectRequest.Action = execution.EffectStartAttemptV2
	effectRequest.ExecutionSlot = recorded.ExecutionSlot
	effectRequest.ObservedAt = now.Add(2 * time.Second)
	effectRequest.OperationEventID = "event-runtime-effect-attempt-operation"
	effectRequest.EffectEventID = "event-runtime-effect-attempt"
	_, dispatchErr := first.ExecuteEffectDispatch(dispatchContext, firstEpoch, execution.EffectDispatchCommandV2{
		Authorization: effectRequest, ObservationOperationEventID: "event-runtime-effect-observation-operation",
		ObservationEffectEventID: "event-runtime-effect-observation", ObservationAt: now.Add(3 * time.Second),
	}, dispatcher)
	if !execution.IsKind(dispatchErr, execution.KindDecisionRequired) || dispatcher.applies.Load() != 1 || dispatchContext.Err() != context.Canceled {
		t.Fatalf("dispatch result calls=%d err=%v", dispatcher.applies.Load(), dispatchErr)
	}
	effectProgress, err := progressHandle.Get("project-runtime", "mission-runtime", ProgressEffects, "effect-runtime")
	if err != nil {
		t.Fatalf("durable failed-dispatch progress absent: %v", err)
	}
	effectState, err := execution.DecodeExternalEffectStateV2(effectProgress.CanonicalState)
	if err != nil || (effectState.Stage != execution.EffectAttemptStartedV2 && effectState.Stage != execution.EffectObservationRecordedV2) {
		t.Fatalf("durable failed-dispatch state=%s err=%v", effectState.Stage, err)
	}
	operationsAfter, err := progressHandle.List("project-runtime", "mission-runtime", ProgressOperations)
	if err != nil {
		t.Fatal(err)
	}
	if len(operationsAfter) != len(operationsBefore) {
		t.Fatalf("typed dispatch failure published a checkpoint: operations before=%d after=%d", len(operationsBefore), len(operationsAfter))
	}
	if checkpointCalls.calls.Load() != 0 {
		t.Fatalf("typed dispatch failure invoked checkpoint publication: calls=%d", checkpointCalls.calls.Load())
	}
	firstProgress, err = progressHandle.Mission("project-runtime", "mission-runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	config.Sidecar.SupervisorRunID = "supervisor-real-2"
	config.Reconciler = nil
	second, err := Start(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(t.Context())
	if second.State() != HostReady || second.RuntimeEpochID() == firstEpoch || second.DatabaseID() != first.DatabaseID() {
		t.Fatalf("restart state=%s first=%q second=%q firstdb=%q seconddb=%q", second.State(), firstEpoch, second.RuntimeEpochID(), first.DatabaseID(), second.DatabaseID())
	}
	restartedProgress, err := second.Progress().Mission("project-runtime", "mission-runtime")
	if err != nil || restartedProgress.Lifecycle != firstProgress.Lifecycle || restartedProgress.ProjectionSHA256 != firstProgress.ProjectionSHA256 {
		t.Fatalf("restart did not reproduce mission memory: first=%+v second=%+v err=%v", firstProgress, restartedProgress, err)
	}
	var restoredMissionState mission.MissionAuthorityStateV2
	for _, item := range restartedProgress.Items {
		if item.Family == "mission" {
			restoredMissionState, err = mission.DecodeMissionAuthorityStateV2(item.CanonicalState)
			break
		}
	}
	if err != nil || restoredMissionState.IsZero() {
		t.Fatalf("restored mission state: %v", err)
	}
	pausedState, err := mission.NewMissionAuthorityStateV2(mission.MissionAuthorityStateInputV2{
		ProjectID: "project-runtime", MissionID: "mission-runtime", DefinitionRevision: "definition-runtime", DefinitionDigest: definitionDigest,
		Version: 2, Status: mission.AuthorityMissionPausedV2, Step: "active", LastOperationID: "operation-runtime-pause", ObservedAt: "2026-09-05T12:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.ExecuteMission(t.Context(), second.RuntimeEpochID(), mission.MissionAuthorityCommandV2{
		SchemaVersion: 1, OperationID: "operation-runtime-pause", ProjectID: "project-runtime", MissionID: "mission-runtime",
		ActorID: "actor-runtime", ProcessID: "process-runtime-2", OriginatingRuntimeEpochID: second.RuntimeEpochID(),
		DoctrineRevision: "doctrine-runtime", ProfileRevision: "profile-runtime", PolicyRevision: "policy-runtime",
		ObservedAt: "2026-09-05T12:01:00Z", EventType: mission.MissionEventPausedV2, Expected: restoredMissionState, Successor: pausedState,
	}); err != nil {
		t.Fatalf("post-restart typed command: %v", err)
	}
	pausedProgress, err := second.Progress().Mission("project-runtime", "mission-runtime")
	if err != nil || pausedProgress.Lifecycle != "paused" || pausedProgress.ProjectionRevision <= restartedProgress.ProjectionRevision {
		t.Fatalf("post-restart memory did not advance: progress=%+v err=%v", pausedProgress, err)
	}
	if err := second.executeBarrierProbe(firstEpoch, func(CommandContext) error { return nil }); !IsKind(err, ErrorStaleEpoch) {
		t.Fatalf("predecessor epoch admitted: %v", err)
	}
}

func TestStartupRefusesMismatchedAndHistoricalRootsBeforeSidecarAcquisition(t *testing.T) {
	t.Run("mismatched-classification-and-data-root", func(t *testing.T) {
		control := t.TempDir()
		data := filepath.Join(t.TempDir(), "other-authority")
		runtimeRoot := filepath.Join(t.TempDir(), "runtime-not-created")
		_, err := Start(t.Context(), Config{
			ControlRoot:                 control,
			HostAuthorityID:             "host-root-mismatch",
			AuthorityBindingOperationID: "operation-root-mismatch",
			M1AdoptionRecordPath:        filepath.Join(t.TempDir(), "missing-adoption"),
			Sidecar: sidecargapdb.LifecycleConfig{
				ExecutablePath: filepath.Join(t.TempDir(), "missing-sidecar"), RuntimeRoot: runtimeRoot, DataDirectory: data,
			},
		})
		if !IsKind(err, ErrorInvalidConfiguration) {
			t.Fatalf("mismatched roots err=%v", err)
		}
		if _, statErr := os.Lstat(data); !os.IsNotExist(statErr) {
			t.Fatalf("mismatched data root was touched: %v", statErr)
		}
		if _, statErr := os.Lstat(runtimeRoot); !os.IsNotExist(statErr) {
			t.Fatalf("runtime root was touched: %v", statErr)
		}
	})

	t.Run("historical-root", func(t *testing.T) {
		root := t.TempDir()
		historical := []byte{'S', 'Q', 'L', 'i', 't', 'e', ' ', 'f', 'o', 'r', 'm', 'a', 't', ' ', '3', 0}
		if err := os.WriteFile(filepath.Join(root, "old-authority.bin"), historical, 0o600); err != nil {
			t.Fatal(err)
		}
		runtimeRoot := filepath.Join(t.TempDir(), "runtime-not-created")
		_, err := Start(t.Context(), Config{
			ControlRoot:                 root,
			HostAuthorityID:             "host-historical-refusal",
			AuthorityBindingOperationID: "operation-historical-refusal",
			M1AdoptionRecordPath:        filepath.Join(t.TempDir(), "missing-adoption"),
			Sidecar: sidecargapdb.LifecycleConfig{
				ExecutablePath: filepath.Join(t.TempDir(), "missing-sidecar"), RuntimeRoot: runtimeRoot, DataDirectory: root,
			},
		})
		if !controlroot.IsOfflineDispositionRequired(err) {
			t.Fatalf("historical root err=%v", err)
		}
		if _, statErr := os.Lstat(runtimeRoot); !os.IsNotExist(statErr) {
			t.Fatalf("sidecar runtime was acquired before refusal: %v", statErr)
		}
	})
}

type fakeProgressQuery struct {
	base       projection.ProgressBaseV2
	items      []projection.AggregateViewV2
	watch      chan projection.NotificationV2
	queryCalls atomic.Int64
}

func (f *fakeProgressQuery) Get(projection.GetQueryV2) (projection.AggregateViewV2, error) {
	f.queryCalls.Add(1)
	return projection.AggregateViewV2{}, &projection.ErrorV2{Kind: projection.ErrorNotFoundV2, Field: "aggregate"}
}
func (f *fakeProgressQuery) List(query projection.ListQueryV2) (projection.ListResultV2, error) {
	f.queryCalls.Add(1)
	result := projection.ListResultV2{DatabaseID: f.base.DatabaseID, Revision: f.base.Revision, RuntimeEpochID: f.base.RuntimeEpochID}
	for _, item := range f.items {
		if string(item.Coordinate.Kind) == query.Family && (query.ProjectID == "" || item.Coordinate.Scope.ProjectID == query.ProjectID) && (query.MissionID == "" || item.Coordinate.Scope.MissionID == query.MissionID) {
			item.CanonicalState = append([]byte(nil), item.CanonicalState...)
			result.Items = append(result.Items, item)
		}
	}
	return result, nil
}
func (f *fakeProgressQuery) Watch(database journalv2.DatabaseID, revision uint64) (*projection.WatchV2, error) {
	f.queryCalls.Add(1)
	if database != f.base.DatabaseID || revision != f.base.Revision {
		return nil, &projection.ErrorV2{Kind: projection.ErrorResyncRequiredV2, Field: "watch"}
	}
	return &projection.WatchV2{C: f.watch}, nil
}
func (f *fakeProgressQuery) ProgressBase(mission string) (projection.ProgressBaseV2, error) {
	f.queryCalls.Add(1)
	if mission != f.base.MissionID {
		return projection.ProgressBaseV2{}, &projection.ErrorV2{Kind: projection.ErrorNotFoundV2, Field: "mission"}
	}
	return f.base, nil
}

type fixedActionCensus struct{}

type absentReadinessSource struct{}

func (absentReadinessSource) ObserveCapabilityReadiness(_ context.Context, query mission.CapabilityReadinessQueryV2) (mission.CapabilityReadinessEvidenceV2, error) {
	return mission.CapabilityReadinessEvidenceV2{Capture: query.Capture(), ProjectID: query.ProjectID(), MissionID: query.MissionID(), Action: query.Action(), ObservedAbsent: true, EvidenceIdentity: "absence-" + string(query.Action()), EvidenceDigest: repeatByte('a', 64)}, nil
}

func (fixedActionCensus) Census(projectID journalv2.ProjectID, missionID journalv2.MissionID, revision uint64) (mission.ActionCensus, error) {
	capture, err := journalv2.NewStoreRevisionV2("database-progress", revision, revision)
	if err != nil {
		return mission.ActionCensus{}, err
	}
	definition := softwaredev.CapabilityDefinition()
	observer, err := mission.NewCapabilityAuthorityObserverV2(absentReadinessSource{})
	if err != nil {
		return mission.ActionCensus{}, err
	}
	observations := make([]mission.CapabilityAuthorityObservationV2, 0, len(definition.Templates()))
	for _, template := range definition.Templates() {
		observation, observeErr := observer.Observe(context.Background(), capture, projectID, missionID, template.Action)
		if observeErr != nil {
			return mission.ActionCensus{}, observeErr
		}
		observations = append(observations, observation)
	}
	set, err := mission.SealCapabilityObservationSetV2(capture, projectID, missionID, observations)
	if err != nil {
		return mission.ActionCensus{}, err
	}
	snapshot, err := mission.SealPrecreationReadinessSnapshot(projectID, missionID, capture, definition, set)
	if err != nil {
		return mission.ActionCensus{}, err
	}
	return mission.EvaluateReadiness(snapshot)
}

func TestProgressReportsCompleteMissionWithoutExclusiveCurrentStep(t *testing.T) {
	query := progressFixture()
	service, err := NewProgressService(query, fixedActionCensus{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Mission("project-1", "mission-1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || got.Lifecycle != "open" || got.Generation != 4 || got.WorkRevisionID != "revision-4" || got.TotalWork != 1 || len(got.Running) != 1 || len(got.Effects) != 1 || len(got.Reviews) != 1 || len(got.Integration) != 1 || len(got.Operations) != 1 {
		t.Fatalf("incomplete progress: %+v", got)
	}
	if len(got.Actions) != 11 || got.Actions[0].Action != "acceptance.evaluate" || got.Actions[4].Action != "mission.create" || got.Actions[4].Status != "authorized" {
		t.Fatalf("action census=%+v", got.Actions)
	}
	got.Items[0].CanonicalState[0] ^= 0xff
	again, err := service.Mission("project-1", "mission-1")
	if err != nil || again.Items[0].CanonicalState[0] == got.Items[0].CanonicalState[0] {
		t.Fatal("caller mutated immutable progress")
	}
	before := query.queryCalls.Load()
	if _, err := service.Mission("project-1", "mission-1"); err != nil {
		t.Fatal(err)
	}
	if delta := query.queryCalls.Load() - before; delta != 1 {
		t.Fatalf("immutable revision cache performed %d projection calls, want one base check", delta)
	}
}

func TestProgressLifecycleAndAuthorityStateMatrix(t *testing.T) {
	tests := []struct {
		name, lifecycle string
		mutate          func(*fakeProgressQuery)
		check           func(MissionProgress) bool
	}{
		{"active", "active", nil, func(p MissionProgress) bool { return p.Lifecycle == "active" && len(p.Running) == 1 }},
		{"blocked", "active", func(q *fakeProgressQuery) {
			q.items = append(q.items, progressView("mission", "blocker-1", `{"kind":"blocker","status":"active"}`))
		}, func(p MissionProgress) bool { return len(p.Blockers) == 1 }},
		{"decision-required", "active", func(q *fakeProgressQuery) {
			q.items = append(q.items, progressView("mission", "decision-1", `{"kind":"decision","status":"open"}`))
		}, func(p MissionProgress) bool { return len(p.Decisions) == 1 }},
		{"reconciling", "active", nil, func(p MissionProgress) bool { return len(p.PendingReconciliation) == 1 }},
		{"reviewing", "active", nil, func(p MissionProgress) bool { return len(p.Reviews) == 1 && p.Reviews[0].AuthorityState == "approved" }},
		{"integrating", "active", nil, func(p MissionProgress) bool {
			return len(p.Integration) == 1 && p.Integration[0].AuthorityState == "in_progress"
		}},
		{"failed", "failed", nil, func(p MissionProgress) bool { return p.Lifecycle == "failed" }},
		{"canceled", "canceled", nil, func(p MissionProgress) bool { return p.Lifecycle == "canceled" }},
		{"completed", "completed", nil, func(p MissionProgress) bool { return p.Lifecycle == "completed" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := progressFixture()
			query.items[0].CanonicalState = []byte(`{"current_work_revision_id":"revision-4","lifecycle":"` + test.lifecycle + `"}`)
			if test.mutate != nil {
				test.mutate(query)
			}
			service, err := NewProgressService(query, fixedActionCensus{})
			if err != nil {
				t.Fatal(err)
			}
			progress, err := service.Mission("project-1", "mission-1")
			if err != nil || !test.check(progress) {
				t.Fatalf("progress=%+v err=%v", progress, err)
			}
		})
	}
}

func TestProgressTypedFamilyGetAndListAreCopyOnly(t *testing.T) {
	service, err := NewProgressService(progressFixture(), fixedActionCensus{})
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range []ProgressFamily{ProgressWork, ProgressEffects, ProgressReviews, ProgressAcceptance, ProgressIntegration, ProgressOperations} {
		items, listErr := service.List("project-1", "mission-1", family)
		if listErr != nil || len(items) == 0 {
			t.Fatalf("family=%s items=%v err=%v", family, items, listErr)
		}
		got, getErr := service.Get("project-1", "mission-1", family, items[0].AggregateID)
		if getErr != nil || got.AggregateID != items[0].AggregateID {
			t.Fatalf("family=%s got=%+v err=%v", family, got, getErr)
		}
		got.CanonicalState[0] ^= 0xff
		again, _ := service.Get("project-1", "mission-1", family, items[0].AggregateID)
		if bytes.Equal(got.CanonicalState, again.CanonicalState) {
			t.Fatalf("family=%s returned mutable storage", family)
		}
	}
	if _, err := service.List("project-1", "mission-1", "provider-private"); !IsProgressKind(err, ProgressInvalidQuery) {
		t.Fatalf("unsupported family err=%v", err)
	}
}

func TestReconciliationReadinessDependsOnRecordedAuthorityNotCallbackSuccess(t *testing.T) {
	query := progressFixture()
	if !pendingReconciliation(query) {
		t.Fatal("recorded reconciliation requirement was ignored")
	}
	for index := range query.items {
		if query.items[index].Coordinate.Kind == "external_effect" {
			query.items[index].CanonicalState = []byte(`{"stage":"confirmed"}`)
		}
	}
	if pendingReconciliation(query) {
		t.Fatal("resolved recorded authority remained pending")
	}
}

func TestProgressWatchUsesRevisionHintAndSlowConsumerMustResnapshot(t *testing.T) {
	query := progressFixture()
	service, _ := NewProgressService(query, fixedActionCensus{})
	watch, err := service.Watch("project-1", "mission-1", query.base.DatabaseID, query.base.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Cancel()
	query.watch <- projection.NotificationV2{DatabaseID: query.base.DatabaseID, Revision: query.base.Revision, RuntimeEpochID: query.base.RuntimeEpochID}
	select {
	case got := <-watch.C:
		if got.MissionID != "mission-1" || !got.Complete {
			t.Fatalf("watch=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("watch did not publish snapshot")
	}
	resnapshot, err := service.Watch("project-1", "mission-1", "foreign", query.base.Revision-1)
	if err != nil {
		t.Fatalf("foreign resume err=%v", err)
	}
	defer resnapshot.Cancel()
	select {
	case got := <-resnapshot.C:
		if got.DatabaseID != query.base.DatabaseID || got.ProjectionRevision != query.base.Revision {
			t.Fatalf("resnapshot=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("foreign resume did not return current snapshot")
	}
}

func TestProgressSlowSubscriberCannotBlockAndMustResnapshot(t *testing.T) {
	query := progressFixture()
	service, _ := NewProgressService(query, fixedActionCensus{})
	watch, err := service.Watch("project-1", "mission-1", query.base.DatabaseID, query.base.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Cancel()
	notification := projection.NotificationV2{DatabaseID: query.base.DatabaseID, Revision: query.base.Revision, RuntimeEpochID: query.base.RuntimeEpochID}
	query.watch <- notification
	query.watch <- notification
	select {
	case err := <-watch.Errors:
		if !IsProgressKind(err, ProgressResyncRequired) {
			t.Fatalf("slow subscriber err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscriber blocked publication instead of requiring resnapshot")
	}
}

func TestProgressWatchRejectsUnbackedNotification(t *testing.T) {
	query := progressFixture()
	service, _ := NewProgressService(query, fixedActionCensus{})
	watch, err := service.Watch("project-1", "mission-1", query.base.DatabaseID, query.base.Revision)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Cancel()
	query.watch <- projection.NotificationV2{DatabaseID: query.base.DatabaseID, Revision: query.base.Revision + 1, RuntimeEpochID: query.base.RuntimeEpochID}
	select {
	case err := <-watch.Errors:
		if !IsProgressKind(err, ProgressResyncRequired) {
			t.Fatalf("unbacked watch notification err=%v", err)
		}
	case value := <-watch.C:
		t.Fatalf("watch notification became durable progress: %+v", value)
	case <-time.After(time.Second):
		t.Fatal("unbacked watch notification was not refused")
	}
}

func TestProductionHostSurfaceHasNoRawAuthorityOrBackendSelector(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(Host{}), reflect.TypeOf(Config{})} {
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if !field.IsExported() {
				continue
			}
			text := strings.ToLower(field.Name + " " + field.Type.String())
			for _, forbidden := range []string{"rawowner", "rawadapter", "databasehandle", "sqlitefallback", "backendselector", "controlstore/gapdb.adapter"} {
				if strings.Contains(strings.ReplaceAll(text, " ", ""), forbidden) {
					t.Fatalf("production surface leaked %s", field.Name)
				}
			}
		}
	}
	hostType := reflect.TypeOf((*Host)(nil))
	for index := 0; index < hostType.NumMethod(); index++ {
		method := hostType.Method(index)
		text := strings.ToLower(method.Type.String())
		if strings.Contains(text, "controlstore/gapdb") || strings.Contains(text, "sidecar/gapdb") {
			t.Fatalf("production method leaked raw authority: %s", method.Name)
		}
	}
}

func TestProgressTenThousandQueriesRemainMemoryOnlyAndBelowP95Budget(t *testing.T) {
	query := progressFixture()
	service, _ := NewProgressService(query, nil)
	samples := make([]time.Duration, 10_000)
	for i := range samples {
		started := time.Now()
		if _, err := service.Mission("project-1", "mission-1"); err != nil {
			t.Fatal(err)
		}
		samples[i] = time.Since(started)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if p95 := samples[9499]; p95 >= 2*time.Millisecond {
		t.Fatalf("query p95=%s", p95)
	}
}

func hostFixture(t *testing.T, database journalv2.DatabaseID) (*Host, *atomic.Int64) {
	t.Helper()
	authority := initializedProjection(t, database)
	progress, err := NewProgressService(authority, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := &hostOwnerProbe{database: database}
	host, err := newHost(owner, "epoch-current", authority, progress)
	if err != nil {
		t.Fatal(err)
	}
	return host, &atomic.Int64{}
}

func initializedProjection(t *testing.T, database journalv2.DatabaseID) *projection.AuthorityV2 {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers := fixtureReducers(t, registry)
	authority, err := projection.NewAuthorityV2(database, registry, reducers)
	if err != nil {
		t.Fatal(err)
	}
	tracker, _ := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	coordinator, _ := journalv2.NewPublicationCoordinatorV2(&fixtureCommitPort{}, tracker)
	plan := operationPlan(t, registry)
	ack, err := coordinator.Publish(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.PublishAcknowledged(ack); err != nil {
		t.Fatal(err)
	}
	return authority
}

func fixtureReducers(t *testing.T, registry journalv2.EventRegistryV2) journalv2.ReducerSetV2 {
	t.Helper()
	registrations := make([]journalv2.ReducerRegistration, 0, len(journalv2.ExpectedEventTuples()))
	for _, tuple := range journalv2.ExpectedEventTuples() {
		digest, _ := journalv2.ReducerStateDigestV2([]byte(fmt.Sprintf(`{"family":%q}`, tuple.Family)))
		reduce := journalv2.Reducer(func(_ []byte, event journalv2.EventEnvelopeV2) ([]byte, error) { return event.Payload(), nil })
		if tuple.Family == "operation" {
			digest, reduce = journalv2.OperationReducerImplementationDigestV2(), journalv2.ReduceOperationStateV2
		}
		registrations = append(registrations, journalv2.ReducerRegistration{Family: tuple.Family, EventSchemaVersion: tuple.EventSchemaVersion, ReducerVersion: tuple.ReducerVersion, Owner: tuple.Owner, ImplementationDigest: digest, Reduce: reduce})
	}
	reducers, err := journalv2.NewReducerSetV2(registry, registrations)
	if err != nil {
		t.Fatal(err)
	}
	return reducers
}

func operationPlan(t *testing.T, registry journalv2.EventRegistryV2) journalv2.SealedCommitPlanV2 {
	t.Helper()
	operationID := journalv2.OperationID("operation-host-fixture")
	eventID := journalv2.EventID("event-operation-host-fixture")
	state, err := journalv2.NewOperationStateV2(journalv2.OperationStateInput{OperationID: operationID, LogicalVersion: 1, Status: journalv2.StatusResolved, ResultKind: "host.fixture", ResultSchemaVersion: 1, CanonicalResult: []byte(`{"ok":true}`), EventIDs: []journalv2.EventID{eventID}})
	if err != nil {
		t.Fatal(err)
	}
	scope := journalv2.Scope{Kind: journalv2.ScopeHost}
	intent, _ := journalv2.ReducerStateDigestV2([]byte(`{"intent":"host"}`))
	binding, err := journalv2.NewOperationBindingV2(journalv2.OperationBindingInput{OperationID: operationID, CommandKind: "host.fixture", CommandSchemaVersion: 1, CanonicalIntentDigest: intent, Scope: scope, OriginatingRuntimeEpochID: "epoch-current", ObservedCreatedAt: "2026-09-05T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := journalv2.EncodeOperationStateEventPayloadV2(state)
	coordinate, _ := journalv2.NewAggregateCoordinate("operation", journalv2.AggregateID(operationID), scope)
	digest, _ := journalv2.ReducerStateDigestV2(payload)
	head := journalv2.HeadTransitionV2{Coordinate: coordinate, NextLogicalVersion: 1, EventID: eventID, CommittedRuntimeEpochID: "epoch-current", CanonicalStateDigest: digest, AuthoritySummary: payload}
	condition, _ := journalv2.ConditionForHeadTransitionV2(head)
	plan, err := journalv2.SealCommitPlanV2(journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: state, Events: []journalv2.EventDraftV2{{EventID: eventID, EventType: "operation.resolved", Family: "operation", EventSchemaVersion: 2, ReducerVersion: 2, Coordinate: coordinate, AggregateLogicalVersion: 1, CommittedRuntimeEpochID: "epoch-current", Payload: payload}}, Heads: []journalv2.HeadTransitionV2{head}, Conditions: []journalv2.ProviderCondition{condition}}, registry)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func progressFixture() *fakeProgressQuery {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	makeItem := func(family, id, state string) projection.AggregateViewV2 {
		coordinate, _ := journalv2.NewAggregateCoordinate(journalv2.AggregateKind(family), journalv2.AggregateID(id), scope)
		return projection.AggregateViewV2{DatabaseID: "database-progress", ProjectionRevision: 42, RuntimeEpochID: "epoch-progress", ProjectionDigest: repeatByte('b', 64), Coordinate: coordinate, LogicalVersion: 1, LastEventID: journalv2.EventID("event-" + id), LastPublicationID: journalv2.PublicationID(repeatByte('c', 64)), StateDigest: repeatByte('d', 64), CanonicalState: []byte(state)}
	}
	items := []projection.AggregateViewV2{
		makeItem("mission", "mission-1", `{"current_work_revision_id":"revision-4","lifecycle":"open"}`),
		makeItem("workgraph", "revision-4", `{"generation":4,"status":"current"}`),
		makeItem("execution_slot", "work-1", `{"actor_id":"agent-a","lease_id":"lease-1","process_id":"process-a","run_id":"run-1","stage":"active","work_id":"work-1"}`),
		makeItem("external_effect", "effect-1", `{"stage":"reconciliation_required"}`),
		makeItem("review_slot", "review-1", `{"stage":"approved"}`),
		makeItem("acceptance", "accept-1", `{"stage":"accepted"}`),
		makeItem("integration_attempt", "integration-1", `{"stage":"in_progress"}`),
		makeItem("operation", "operation-1", `{"status":"in_progress"}`),
	}
	return &fakeProgressQuery{base: projection.ProgressBaseV2{DatabaseID: "database-progress", Revision: 42, RuntimeEpochID: "epoch-progress", MissionID: "mission-1"}, items: items, watch: make(chan projection.NotificationV2, 2)}
}

func progressView(family, id, state string) projection.AggregateViewV2 {
	scope := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	coordinate, _ := journalv2.NewAggregateCoordinate(journalv2.AggregateKind(family), journalv2.AggregateID(id), scope)
	return projection.AggregateViewV2{DatabaseID: "database-progress", ProjectionRevision: 42, RuntimeEpochID: "epoch-progress", ProjectionDigest: repeatByte('b', 64), Coordinate: coordinate, LogicalVersion: 1, LastEventID: journalv2.EventID("event-" + id), LastPublicationID: journalv2.PublicationID(repeatByte('c', 64)), StateDigest: repeatByte('d', 64), CanonicalState: []byte(state)}
}

func repeatByte(value byte, count int) string {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return string(result)
}

type wp13SourceReplacement struct{ old, new string }
type wp13ProductionMutant struct {
	id, source, pkg, killer, want string
	replacements                  []wp13SourceReplacement
}

func TestWP13MandatoryProductionMutantCampaign(t *testing.T) {
	if testing.Short() {
		t.Skip("WP13 production overlay campaign is excluded from the short tier")
	}
	if os.Getenv("SPEC_KITTY_WP13_GAPDBD") == "" {
		t.Skip("exact admitted sidecar is required for the lost-memory mutant control")
	}
	root := wp13RepositoryRoot(t)
	mutants := []wp13ProductionMutant{
		{"arbitrary_executable", "internal/sidecar/gapdb/provenance.go", "./internal/sidecar/gapdb", "TestProductionExecutableRequiresAbsolutePath", "relative executable did not fail", []wp13SourceReplacement{{`if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {`, `if path == "" || strings.ContainsRune(path, 0) {`}}},
		{"unadopted_record", "internal/sidecar/gapdb/adoption.go", "./internal/sidecar/gapdb", "TestProductionAdoptionRecordRejectsEveryAuthorityCoordinate", "signature_value", []wp13SourceReplacement{{`record.Signature.Kind != AdoptedSignatureKind || record.Signature.Value != AdoptedSignatureValue`, `record.Signature.Kind != AdoptedSignatureKind || record.Signature.Value == ""`}}},
		{"classifier_after_mutation", "internal/productionruntime/composition.go", "./internal/productionruntime", "TestStartupRefusesMismatchedAndHistoricalRootsBeforeSidecarAcquisition", "historical root err", []wp13SourceReplacement{{"decision, err := classifier.Decide(ctx, config.ControlRoot)\n\tif err != nil {\n\t\treturn nil, err\n\t}", "decision, err := classifier.Decide(ctx, config.ControlRoot)\n\tif err != nil {\n\t\tdecision = controlroot.Decision{}\n\t}"}}},
		{"second_host", "internal/productionruntime/host.go", "./internal/productionruntime", "TestHostSecondOwnerForSameDatabaseRefusesAndDifferentDatabasesProgress", "second owner err=<nil>", []wp13SourceReplacement{{`if current := liveOwners.byDatabase[h.ownerKey]; current != nil && current != h {`, `if current := liveOwners.byDatabase[h.ownerKey]; current != nil && current != h && false {`}}},
		{"ready_before_recovery", "internal/productionruntime/host.go", "./internal/productionruntime", "TestHostBarrierRejectsEveryNonReadyStateAndStaleEpochBeforeCommand", "state=recovering", []wp13SourceReplacement{{"func (h *Host) beginCommand(expectedEpoch journalv2.RuntimeEpochID) (func(), error) {\n\th.mu.Lock()\n\tif h.state != HostReady {", "func (h *Host) beginCommand(expectedEpoch journalv2.RuntimeEpochID) (func(), error) {\n\th.mu.Lock()\n\tif false {"}}},
		{"epoch_before_restore", "internal/productionruntime/recovery.go", "./internal/productionruntime", "TestFreshEpochRequiresSealedRestoreAndRotatesPredecessorDurably", "forged restore admitted", []wp13SourceReplacement{{"request, err := recovered.FreshEpochAdmissionRequestV2()\n\tif err != nil || request.DatabaseID() != binding.DatabaseID", "request, err := recovered.FreshEpochAdmissionRequestV2()\n\tif err != nil { return journalv2.RuntimeEpochID(\"epoch-mutant\"), nil }\n\tif request.DatabaseID() != binding.DatabaseID"}}},
		{"predecessor_client", "internal/productionruntime/host.go", "./internal/productionruntime", "TestHostBarrierRejectsEveryNonReadyStateAndStaleEpochBeforeCommand", "stale epoch err=<nil>", []wp13SourceReplacement{{`if expectedEpoch != h.epoch {`, `if expectedEpoch != h.epoch && false {`}}},
		{"projection_before_ack", "internal/projection/authority.go", "./internal/projection", "TestProjectionPublishesCompleteAcknowledgedRevisionsAndDefensiveQueries", "duplicate acknowledgement err=<nil>", []wp13SourceReplacement{{`return &ErrorV2{ErrorIncompleteRevisionV2, "acknowledgement"}`, `return nil`}}},
		{"watch_as_durable", "internal/productionruntime/progress.go", "./internal/productionruntime", "TestProgressWatchRejectsUnbackedNotification", "watch notification became durable progress", []wp13SourceReplacement{{`if queryErr != nil || value.ProjectionRevision != notification.Revision {`, `if queryErr != nil {`}}},
		{"lost_memory_loses_state", "internal/productionruntime/composition.go", "./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch", "live memory was not refreshed", []wp13SourceReplacement{{"result, restoreErr := journalrestore.RestoreV2", "_, restoreErr := journalrestore.RestoreV2"}, {"return authority.RestoreCurrentEpoch(epoch, result)", "return nil // mutant: discard restored state"}}},
		{"possible_mutation_refresh_omitted", "internal/productionruntime/capabilities.go", "./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch", "durable failed-dispatch state=intent_recorded", []wp13SourceReplacement{{"refreshErr = h.refreshProjection(refreshContext)", "refreshErr = nil // mutant: durable progress is left invisible"}}},
		{"possible_mutation_inherits_cancellation", "internal/productionruntime/capabilities.go", "./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch", "durable failed-dispatch state=intent_recorded", []wp13SourceReplacement{{"context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)", "context.WithTimeout(ctx, 30*time.Second)"}}},
		{"refusal_publishes_checkpoint", "internal/productionruntime/capabilities.go", "./internal/productionruntime", "TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch", "typed dispatch failure invoked checkpoint publication", []wp13SourceReplacement{{"refreshErr = h.refreshProjection(refreshContext)", "refreshErr = h.afterMutation(refreshContext) // mutant: refusal writes a checkpoint"}}},
		{"query_mints_authority", "internal/projection/query.go", "./internal/projection", "TestQueryPortCannotMutateAuthorityOrCollapseProgressIntoCeremony", "query method count", []wp13SourceReplacement{{"\tProgressBase(string) (ProgressBaseV2, error)\n}", "\tProgressBase(string) (ProgressBaseV2, error)\n\tPublishAcknowledged(journalv2.AcknowledgedPublicationV2) error\n}"}}},
		{"raw_handle_escape", "internal/productionruntime/host.go", "./internal/productionruntime", "TestProductionHostSurfaceHasNoRawAuthorityOrBackendSelector", "production surface leaked RawOwner", []wp13SourceReplacement{{"type Host struct {\n\tmu", "type Host struct {\n\tRawOwner ownerLifecycle\n\tmu"}}},
		{"sqlite_backend_selector", "internal/productionruntime/composition.go", "./internal/productionruntime", "TestProductionHostSurfaceHasNoRawAuthorityOrBackendSelector", "production surface leaked SQLiteFallback", []wp13SourceReplacement{{"type Config struct {\n", "type Config struct {\n\tSQLiteFallback any\n"}}},
		{"playback_external_io", "internal/productionruntime/recovery.go", "./internal/productionruntime", "TestProductionReducerCompositionIsCompleteAndRuntimePlaybackPure", "mutant external call", []wp13SourceReplacement{{"\t\"sort\"\n\t\"sync\"", "\t\"sort\"\n\t\"sync\"\n\t\"os\""}, {"func ReduceRuntimeHostEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {\n", "func ReduceRuntimeHostEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {\n\tif _, err := os.Stat(\".\"); err == nil { return nil, fmt.Errorf(\"mutant external call\") }\n"}}},
	}
	if len(mutants) != 17 {
		t.Fatalf("WP13 mutant census=%d want=17", len(mutants))
	}
	seen := make(map[string]bool, len(mutants))
	for _, mutant := range mutants {
		mutant := mutant
		t.Run(mutant.id, func(t *testing.T) {
			if mutant.id == "" || mutant.source == "" || mutant.pkg == "" || mutant.killer == "" || mutant.want == "" || len(mutant.replacements) == 0 || seen[mutant.id] {
				t.Fatalf("unsealed mutant row: %+v", mutant)
			}
			seen[mutant.id] = true
			if output, err := wp13RunKiller(t, root, mutant, ""); err != nil {
				t.Fatalf("positive control failed: %v\n%s", err, output)
			}
			mutated := wp13MutatedSource(t, filepath.Join(root, mutant.source), mutant.replacements)
			overlay := wp13Overlay(t, filepath.Join(root, mutant.source), mutated)
			output, err := wp13RunKiller(t, root, mutant, overlay)
			if err == nil {
				t.Fatalf("production mutant survived\n%s", output)
			}
			if !strings.Contains(output, mutant.want) {
				t.Fatalf("mutant died for wrong reason; want %q\n%s", mutant.want, output)
			}
		})
	}
}

func wp13RepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate WP13 mutant campaign")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func wp13MutatedSource(t *testing.T, source string, replacements []wp13SourceReplacement) string {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	value := string(raw)
	for _, replacement := range replacements {
		if count := strings.Count(value, replacement.old); count != 1 {
			t.Fatalf("mutant anchor count=%d source=%s anchor=%q", count, source, replacement.old)
		}
		value = strings.Replace(value, replacement.old, replacement.new, 1)
	}
	path := filepath.Join(t.TempDir(), filepath.Base(source))
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wp13Overlay(t *testing.T, source, replacement string) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{map[string]string{source: replacement}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wp13RunKiller(t *testing.T, root string, mutant wp13ProductionMutant, overlay string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	args := []string{"test"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	args = append(args, mutant.pkg, "-run", "^"+mutant.killer+"$", "-count=1")
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), fmt.Errorf("killer timed out: %w", ctx.Err())
	}
	return string(output), err
}
