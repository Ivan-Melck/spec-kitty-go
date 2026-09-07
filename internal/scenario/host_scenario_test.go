package scenario

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	trackerfake "github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestHarnessProductionLifecycleAndTypedQueries(t *testing.T) {
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
	if h.host.Status().State != productionruntime.HostReady {
		t.Fatal("production host did not become ready")
	}
	h.createMission("project-scenario", "mission-scenario", "operation-create")
	before := assertUniversalInvariants(t, h, "project-scenario", "mission-scenario", "mission-created")
	prior, _ := h.restart()
	after := assertUniversalInvariants(t, h, "project-scenario", "mission-scenario", "mission-restored")
	if len(before) == 0 || len(after) == 0 {
		t.Fatal("portable invariant evidence was empty")
	}
	assertStaleEpochRefusesBeforeRepository(t, h, prior)
}

func TestTwoItemMissionRestartAfterEachDurableBoundary(t *testing.T) {
	var uninterrupted terminalSemanticProgress
	t.Run("uninterrupted", func(t *testing.T) {
		uninterrupted = runTwoItemMissionScenario(t, false)
	})
	t.Run("restart-every-boundary", func(t *testing.T) {
		restarted := runTwoItemMissionScenario(t, true)
		if err := validateTerminalSemanticEquivalence(uninterrupted, restarted); err != nil {
			t.Fatalf("restart-every-boundary terminal semantics differ: %v\nuninterrupted=%+v\nrestarted=%+v", err, uninterrupted, restarted)
		}
	})
}

func runTwoItemMissionScenario(t *testing.T, restartEveryBoundary bool) terminalSemanticProgress {
	t.Helper()
	effectReconciler := &scenarioEffectReconciler{}
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name(), CandidateVerifier: sourceBoundCandidateVerifier{}, Reconciler: effectReconciler})
	projectID, missionID := "project-two-item", "mission-two-item"
	revision, governance := twoItemRevision(t, projectID, missionID)
	missionResult := h.createMission(projectID, missionID, "operation-mission-create")
	assertUniversalInvariants(t, h, projectID, missionID, "mission-publication")
	restartAfterDurableBoundary(t, h, projectID, missionID, "mission-created", restartEveryBoundary)
	return continueTwoItemMissionScenario(t, h, missionResult, revision, governance, effectReconciler, restartEveryBoundary)
}

func continueTwoItemMissionScenario(t *testing.T, h *portableHarness, missionResult mission.ReadinessAuthorityResultV2, revision workgraph.RevisionV2, governance governancecontext.Context, effectReconciler *scenarioEffectReconciler, restartEveryBoundary bool) terminalSemanticProgress {
	t.Helper()
	projectID, missionID := string(missionResult.State().ProjectID()), string(missionResult.State().MissionID())
	plan, err := workgraph.BuildFinalizationAuthorityV2(workgraph.FinalizationPlanInputV2{
		Context: workgraph.AuthorityContextV2{
			OperationID: "operation-workgraph", RuntimeEpochID: h.host.RuntimeEpochID(),
			ActorID: "actor-planner", ProcessID: "process-planner",
			DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
			Governance: []string{"governance-two-item"}, ObservedCreatedAt: "2026-09-05T12:01:00Z",
			OperationEventID: "event-workgraph-operation", DomainEventID: "event-workgraph-domain",
		},
		Revision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	acknowledgedWorkgraph, err := h.host.PublishWorkgraph(t.Context(), h.host.RuntimeEpochID(), plan)
	h.observeMutation([]string{"workgraph", "operation"}, "operation-workgraph", started, err)
	if err != nil {
		t.Fatalf("publish two-item graph: %v", err)
	}
	workgraphPublication, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	operationEvidence := []terminalOperationEvidence{operationEvidenceFromPublication(t, workgraphPublication)}
	assertUniversalInvariants(t, h, projectID, missionID, "workgraph-published")
	restartAfterDurableBoundary(t, h, projectID, missionID, "workgraph-published", restartEveryBoundary)
	selection, err := mission.SealAcknowledgedInitialWorkRevisionSelectionV2(revision, acknowledgedWorkgraph)
	if err != nil {
		t.Fatalf("seal acknowledged two-item graph: %v", err)
	}
	definitionDigest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"portable-scenario"}`))
	if err != nil {
		t.Fatal(err)
	}
	selectedState, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: journalv2.ProjectID(projectID), MissionID: journalv2.MissionID(missionID),
		DefinitionRevision: "definition-1", DefinitionDigest: definitionDigest,
		Version: 2, Lifecycle: mission.MissionOpen, CurrentWorkRevision: selection.Head(),
		LastOperationID: "operation-mission-select-workgraph", ObservedAt: "2026-09-05T12:01:30Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	started = time.Now()
	missionResult, err = h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: selectedState.LastOperationID(),
		ProjectID: selectedState.ProjectID(), MissionID: selectedState.MissionID(), ActorID: "actor-planner", ProcessID: "process-planner",
		OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: selectedState.ObservedAt(), EventType: mission.ReadinessEventWorkRevisionSelectedV2,
		Expected: missionResult.State(), Updated: selectedState, WorkRevisionSelection: &selection,
	})
	h.observeMutation([]string{"mission", "operation"}, "operation-mission-select-workgraph", started, err)
	if err != nil {
		t.Fatalf("select acknowledged two-item graph: %v", err)
	}
	assertUniversalInvariants(t, h, projectID, missionID, "workgraph-selected")
	restartAfterDurableBoundary(t, h, projectID, missionID, "workgraph-selected", restartEveryBoundary)

	admitted := admitTwoItemWork(t, h, projectID, missionID, revision, governance, restartEveryBoundary)
	trackerEvidence := assignTwoItemTrackerEffects(t, h, governance, admitted, effectReconciler, restartEveryBoundary)
	var quarantinedWorkspace string
	if h.config.ExerciseLeaseReplacement {
		admitted["work-a"], quarantinedWorkspace = exerciseLeaseScopedReplacement(t, h, admitted["work-a"], governance, restartEveryBoundary)
	}
	progress, err := h.host.Progress().Mission(journalv2.ProjectID(projectID), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	if progress.TotalWork != 2 || len(progress.Running) != 2 {
		t.Fatalf("two-item progress lost parallel authority: total=%d running=%d progress=%+v", progress.TotalWork, len(progress.Running), progress)
	}
	operationEvidence = append(operationEvidence, completeTwoItemMission(t, h, missionResult.State(), revision, governance, admitted, restartEveryBoundary)...)
	if quarantinedWorkspace != "" {
		assertQuarantinedZombieSurvived(t, quarantinedWorkspace)
	}
	terminal, err := h.host.Progress().Mission(journalv2.ProjectID(projectID), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	// Integration targets are host-scoped serialization authority shared by
	// missions. Mission progress reports the two durable mission-scoped
	// attempts, not the mutable host lock as duplicated historical evidence.
	if terminal.Lifecycle != "completed" || len(terminal.Running) != 0 || len(terminal.Reviews) != 2 || len(terminal.Acceptance) != 2 || len(terminal.Integration) != 2 {
		t.Fatalf("incomplete terminal mission: lifecycle=%s revision=%s generation=%d total=%d running=%d reviews=%d acceptance=%d integration=%d projection=%d", terminal.Lifecycle, terminal.WorkRevisionID, terminal.Generation, terminal.TotalWork, len(terminal.Running), len(terminal.Reviews), len(terminal.Acceptance), len(terminal.Integration), terminal.ProjectionRevision)
	}
	semantic := semanticTerminalProgress(t, terminal, trackerEvidence, h.git.terminalEvidence(), operationEvidence)
	terminalDigest := semanticProgressDigest(t, h, projectID, missionID)
	assertUniversalInvariants(t, h, projectID, missionID, "two-items-terminal")
	if !restartEveryBoundary {
		assertUniversalTerminalMembershipMutants(t, h.host.Status(), terminal, projectID, missionID, h.git.count())
	}
	prior, _ := h.restart()
	assertStaleEpochRefusesBeforeRepository(t, h, prior)
	if got := semanticProgressDigest(t, h, projectID, missionID); got != terminalDigest {
		t.Fatalf("two-item restart changed terminal authority: before=%s after=%s", terminalDigest, got)
	}
	assertUniversalInvariants(t, h, projectID, missionID, "two-items-terminal-restored")
	return semantic
}

type terminalTrackerEvidence struct {
	WorkID   string
	Revision uint64
	Assignee string
	Marker   string
}

func assignTwoItemTrackerEffects(t *testing.T, h *portableHarness, governance governancecontext.Context, admitted map[ids.WorkID]execution.ExecutionSlotStateV2, reconciler *scenarioEffectReconciler, restartEveryBoundary bool) []terminalTrackerEvidence {
	t.Helper()
	evidence := make([]terminalTrackerEvidence, 0, len(admitted))
	store, err := trackerfake.Open(filepath.Join(h.root, "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	provider := trackerfake.NewProvider(store)
	dispatcher, err := execution.NewAssignmentEffectDispatcherV2(provider)
	if err != nil {
		t.Fatal(err)
	}
	for index, workID := range []ids.WorkID{"work-a", "work-b"} {
		ticketID := "ticket-" + workID.String()
		if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: ticketID, Title: "Scenario " + workID.String()}); err != nil {
			t.Fatal(err)
		}
		slot := admitted[workID]
		if err := slot.Validate(); err != nil {
			t.Fatalf("tracker effect %s received invalid execution authority: %v", workID, err)
		}
		if slot.RuntimeEpochID != h.host.RuntimeEpochID() {
			slot = rebindUnboundExecutionToCurrent(t, h, slot, governance, "before-effect-"+workID.String())
			admitted[workID] = slot
		}
		at := time.Date(2026, 9, 5, 12, 30, index, 0, time.UTC)
		target := tracker.ExternalWorkRef{
			ProjectID: slot.ProjectID, WorkID: workID, Provider: "fake", ExternalID: ticketID,
			MetadataSchema: 1, CreatedByOperation: "operation-workgraph", LastObservation: 1,
		}
		intent := execution.EffectIntentV2{
			EffectID: journalv2.EffectID("effect-assign-" + workID.String()), OperationID: journalv2.OperationID("operation-assign-" + workID.String()),
			Target: target, IntendedValue: "actor-" + workID.String(), ExpectedProviderRevision: 1,
			RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: slot.ActorID, ProcessID: slot.ProcessID,
			Governance: slot.Governance, OriginRuntimeEpochID: slot.RuntimeEpochID, OriginObservedCreatedAt: at.Format(time.RFC3339Nano),
		}
		intent.CanonicalRequestDigest, err = execution.EffectRequestDigestV2(intent)
		if err != nil {
			t.Fatal(err)
		}
		if len(intent.CanonicalBytesV2()) == 0 {
			t.Fatalf("tracker effect %s produced an invalid intent", workID)
		}
		request := execution.EffectAuthorityRequestV2{
			Action: execution.EffectRecordIntentV2, Intent: intent, RuntimeEpochID: h.host.RuntimeEpochID(), ExecutionSlot: slot, ObservedAt: at,
			OperationEventID: journalv2.EventID("event-effect-intent-operation-" + workID.String()), EffectEventID: journalv2.EventID("event-effect-intent-" + workID.String()),
		}
		started := time.Now()
		recorded, err := h.host.ExecuteEffect(t.Context(), h.host.RuntimeEpochID(), request)
		h.observeMutation([]string{"external_effect", "operation"}, string(request.Intent.OperationID), started, err)
		if err != nil {
			t.Fatalf("record tracker effect %s: %v", workID, err)
		}
		assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "effect-intent-"+workID.String())
		restartAfterIntent := restartEveryBoundary
		externalAuthorityChanged := false
		if h.config.AfterEffectIntent != nil {
			changed, hookErr := h.config.AfterEffectIntent(t.Context(), workID)
			if hookErr != nil {
				t.Fatalf("after durable tracker effect %s: %v", workID, hookErr)
			}
			restartAfterIntent = restartAfterIntent || changed
			externalAuthorityChanged = changed
		}
		if restartAfterIntent {
			if externalAuthorityChanged {
				// The direct provider capability deliberately appended outbox
				// authority outside the live Host projection. Restart restores
				// that new durable state; semantic equality with the pre-append
				// projection would therefore be the wrong invariant.
				predecessor, _ := h.restart()
				assertStaleEpochRefusesBeforeRepository(t, h, predecessor)
				assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "effect-intent-external-authority-restored-"+workID.String())
				slot = rebindUnboundExecutionToCurrent(t, h, recorded.ExecutionSlot, governance, "effect-intent-"+workID.String())
			} else {
				slot = restartUnboundExecutionAfterDurableBoundary(t, h, recorded.ExecutionSlot, governance, "effect-intent-"+workID.String(), true)
			}
			request.Action = execution.EffectAdoptRecoveryV2
			request.RuntimeEpochID = h.host.RuntimeEpochID()
			request.ExecutionSlot = slot
			request.ObservedAt = at.Add(500 * time.Millisecond)
			request.OperationEventID = journalv2.EventID("event-effect-adopt-operation-" + workID.String())
			request.EffectEventID = journalv2.EventID("event-effect-adopt-" + workID.String())
			adopted, adoptErr := h.host.ExecuteEffect(t.Context(), h.host.RuntimeEpochID(), request)
			if adoptErr != nil {
				t.Fatalf("adopt tracker effect %s after restart: %v", workID, adoptErr)
			}
			slot = adopted.ExecutionSlot
		} else {
			slot = recorded.ExecutionSlot
		}
		request.Action = execution.EffectStartAttemptV2
		request.RuntimeEpochID = h.host.RuntimeEpochID()
		request.ExecutionSlot = slot
		request.ObservedAt = at.Add(time.Second)
		request.OperationEventID = journalv2.EventID("event-effect-attempt-operation-" + workID.String())
		request.EffectEventID = journalv2.EventID("event-effect-attempt-" + workID.String())
		started = time.Now()
		applied, err := h.host.ExecuteEffectDispatch(t.Context(), h.host.RuntimeEpochID(), execution.EffectDispatchCommandV2{
			Authorization: request, ObservationOperationEventID: journalv2.EventID("event-effect-observation-operation-" + workID.String()),
			ObservationEffectEventID: journalv2.EventID("event-effect-observation-" + workID.String()), ObservationAt: at.Add(2 * time.Second),
		}, dispatcher)
		h.observeMutation([]string{"external_effect", "operation"}, string(request.Intent.OperationID), started, err)
		if err != nil || applied.State.AppliedObservation == nil || applied.State.AppliedObservation.Value != intent.IntendedValue || applied.State.AppliedObservation.OperationMarker != string(intent.OperationID) {
			t.Fatalf("apply tracker effect %s: result=%+v err=%v", workID, applied, err)
		}
		assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "effect-applied-"+workID.String())
		if restartEveryBoundary {
			reconciler.arm(scenarioEffectRecovery{
				workID: workID, intent: intent, execution: applied.ExecutionSlot, governance: governance,
				observedAt: at.Add(3 * time.Second),
			})
			t.Logf("restart boundary=effect-observation-%s phase=before", workID)
			predecessor, _ := h.restart()
			t.Logf("restart boundary=effect-observation-%s phase=reconciled", workID)
			assertStaleEpochRefusesBeforeRepository(t, h, predecessor)
			resolved, recoveryErr := reconciler.take()
			if recoveryErr != nil || resolved.State.Stage != execution.EffectResolvedV2 || resolved.State.Outcome != execution.EffectOutcomeAppliedV2 {
				t.Fatalf("recover tracker effect %s: result=%+v err=%v", workID, resolved, recoveryErr)
			}
			admitted[workID] = resolved.ExecutionSlot
			assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "effect-resolved-"+workID.String())
			observed, err := provider.ObserveAssignment(t.Context(), target)
			if err != nil || observed.Assignee != intent.IntendedValue || observed.OperationMarker != string(intent.OperationID) {
				t.Fatalf("tracker attribution %s: observation=%+v err=%v", workID, observed, err)
			}
			evidence = append(evidence, terminalTrackerEvidence{WorkID: workID.String(), Revision: uint64(observed.Observation.Revision), Assignee: observed.Assignee, Marker: observed.OperationMarker})
			continue
		}
		request.Action = execution.EffectRequireReconcileV2
		request.ExecutionSlot = applied.ExecutionSlot
		request.ObservedAt = at.Add(3 * time.Second)
		request.OperationEventID = journalv2.EventID("event-effect-reconcile-operation-" + workID.String())
		request.EffectEventID = journalv2.EventID("event-effect-reconcile-" + workID.String())
		started = time.Now()
		reconciled, err := h.host.ExecuteEffect(t.Context(), h.host.RuntimeEpochID(), request)
		h.observeMutation([]string{"external_effect", "operation"}, string(request.Intent.OperationID), started, err)
		if err != nil {
			t.Fatalf("require tracker reconciliation %s: %v", workID, err)
		}
		request.Action = execution.EffectResolveV2
		request.ExecutionSlot = reconciled.ExecutionSlot
		request.ObservedAt = at.Add(4 * time.Second)
		request.Outcome = execution.EffectOutcomeAppliedV2
		request.OperationEventID = journalv2.EventID("event-effect-resolve-operation-" + workID.String())
		request.EffectEventID = journalv2.EventID("event-effect-resolve-" + workID.String())
		started = time.Now()
		resolved, err := h.host.ExecuteEffect(t.Context(), h.host.RuntimeEpochID(), request)
		h.observeMutation([]string{"external_effect", "operation"}, string(request.Intent.OperationID), started, err)
		if err != nil || resolved.State.Stage != execution.EffectResolvedV2 || resolved.State.Outcome != execution.EffectOutcomeAppliedV2 {
			t.Fatalf("resolve tracker effect %s: result=%+v err=%v", workID, resolved, err)
		}
		admitted[workID] = resolved.ExecutionSlot
		assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "effect-resolved-"+workID.String())
		observed, err := provider.ObserveAssignment(t.Context(), target)
		if err != nil || observed.Assignee != intent.IntendedValue || observed.OperationMarker != string(intent.OperationID) {
			t.Fatalf("tracker attribution %s: observation=%+v err=%v", workID, observed, err)
		}
		evidence = append(evidence, terminalTrackerEvidence{WorkID: workID.String(), Revision: uint64(observed.Observation.Revision), Assignee: observed.Assignee, Marker: observed.OperationMarker})
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].WorkID < evidence[j].WorkID })
	return evidence
}

type scenarioEffectRecovery struct {
	workID     ids.WorkID
	intent     execution.EffectIntentV2
	execution  execution.ExecutionSlotStateV2
	governance governancecontext.Context
	observedAt time.Time
}

// scenarioEffectReconciler models the production startup contract rather than
// bypassing it: a durable applied observation is adopted into the fresh epoch,
// attributed, and resolved while the ordinary command barrier remains closed.
type scenarioEffectReconciler struct {
	mu      sync.Mutex
	pending *scenarioEffectRecovery
	result  execution.EffectAuthorityResultV2
	err     error
}

func (r *scenarioEffectReconciler) arm(recovery scenarioEffectRecovery) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = &recovery
	r.result = execution.EffectAuthorityResultV2{}
	r.err = nil
}

func (r *scenarioEffectReconciler) take() (execution.EffectAuthorityResultV2, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err
}

func (r *scenarioEffectReconciler) Reconcile(ctx context.Context, commands *productionruntime.ReconciliationCommands) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		return nil
	}
	recovery := *r.pending
	deadline, err := time.Parse(time.RFC3339Nano, recovery.execution.Deadline)
	if err != nil {
		r.err = err
		return err
	}
	rebound, err := commands.ExecuteWork(ctx, execution.ExecutionAuthorityRequestV2{
		Kind: execution.ExecutionRebindV2, OperationID: journalv2.OperationID("operation-rebind-effect-observation-" + recovery.workID.String()),
		ProjectID: recovery.execution.ProjectID, MissionID: recovery.execution.MissionID, WorkRevisionID: recovery.execution.WorkRevisionID, WorkID: recovery.execution.WorkID,
		RunID: recovery.execution.RunID, ActorID: recovery.execution.ActorID, ProcessID: recovery.execution.ProcessID, LeaseID: recovery.execution.LeaseID,
		Generation: recovery.execution.Generation, Fence: recovery.execution.Fence, ObservedAt: recovery.observedAt, Deadline: deadline,
		RuntimeEpochID: commands.RuntimeEpochID(), Governance: recovery.governance,
		OperationEventID: journalv2.EventID("event-operation-rebind-effect-observation-" + recovery.workID.String()), SlotEventID: journalv2.EventID("event-slot-rebind-effect-observation-" + recovery.workID.String()),
	})
	if err != nil {
		r.err = err
		return err
	}
	request := execution.EffectAuthorityRequestV2{
		Action: execution.EffectAdoptRecoveryV2, Intent: recovery.intent, RuntimeEpochID: commands.RuntimeEpochID(), ExecutionSlot: rebound.State,
		ObservedAt: recovery.observedAt.Add(time.Second), OperationEventID: journalv2.EventID("event-effect-startup-adopt-operation-" + recovery.workID.String()), EffectEventID: journalv2.EventID("event-effect-startup-adopt-" + recovery.workID.String()),
	}
	adopted, err := commands.ExecuteEffect(ctx, request)
	if err != nil {
		r.err = err
		return err
	}
	request.Action = execution.EffectResolveV2
	request.ExecutionSlot = adopted.ExecutionSlot
	request.ObservedAt = recovery.observedAt.Add(2 * time.Second)
	request.Outcome = execution.EffectOutcomeAppliedV2
	request.OperationEventID = journalv2.EventID("event-effect-startup-resolve-operation-" + recovery.workID.String())
	request.EffectEventID = journalv2.EventID("event-effect-startup-resolve-" + recovery.workID.String())
	r.result, r.err = commands.ExecuteEffect(ctx, request)
	if r.err == nil {
		r.pending = nil
	}
	return r.err
}

func admitTwoItemWork(t *testing.T, h *portableHarness, projectID, missionID string, revision workgraph.RevisionV2, governance governancecontext.Context, restartEveryBoundary bool) map[ids.WorkID]execution.ExecutionSlotStateV2 {
	t.Helper()
	workIDs := []ids.WorkID{"work-a", "work-b"}
	admit := func(index int, workID ids.WorkID) (execution.ExecutionAuthorityResultV2, error) {
		now := time.Date(2026, 9, 5, 12, 2, index, 0, time.UTC)
		deadline := now.Add(time.Hour)
		if h.config.ExerciseLeaseReplacement && workID == "work-b" {
			deadline = now.Add(6 * time.Hour)
		}
		started := time.Now()
		result, err := h.host.ExecuteWork(t.Context(), h.host.RuntimeEpochID(), execution.ExecutionAuthorityRequestV2{
			Kind: execution.ExecutionAdmitV2, OperationID: journalv2.OperationID("operation-admit-" + workID.String()),
			ProjectID: ids.ProjectID(projectID), MissionID: ids.MissionID(missionID), WorkRevisionID: revision.ID(), WorkID: workID,
			RunID: ids.RunID("run-" + workID.String()), ActorID: ids.ActorID("actor-" + workID.String()), ProcessID: ids.ProcessID("process-" + workID.String()),
			LeaseID: ids.LeaseID("lease-" + workID.String()), Generation: 1, ObservedAt: now, Deadline: deadline, RuntimeEpochID: h.host.RuntimeEpochID(),
			Governance: governance, OperationEventID: journalv2.EventID("event-operation-" + workID.String()), SlotEventID: journalv2.EventID("event-slot-" + workID.String()),
		})
		h.observeMutation([]string{"execution_slot", "operation"}, "operation-admit-"+workID.String(), started, err)
		return result, err
	}
	admitted := make(map[ids.WorkID]execution.ExecutionSlotStateV2, len(workIDs))
	if restartEveryBoundary {
		for index, workID := range workIDs {
			result, err := admit(index, workID)
			if err != nil {
				t.Fatalf("work admission %s: %v", workID, err)
			}
			if result.State.LogicalVersion == 0 || result.State.Fence == "" {
				t.Fatalf("work admission %s omitted current authority", workID)
			}
			admitted[workID] = restartUnboundExecutionAfterDurableBoundary(t, h, result.State, governance, "execution-admitted-"+workID.String(), true)
			assertUniversalInvariants(t, h, projectID, missionID, "execution-admitted-"+workID.String())
		}
		return admitted
	}

	start := make(chan struct{})
	results := make(chan execution.ExecutionAuthorityResultV2, len(workIDs))
	errs := make(chan error, len(workIDs))
	var ready sync.WaitGroup
	ready.Add(len(workIDs))
	for index, workID := range workIDs {
		index, workID := index, workID
		go func() {
			ready.Done()
			<-start
			result, err := admit(index, workID)
			results <- result
			errs <- err
		}()
	}
	ready.Wait()
	close(start)
	for range workIDs {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent work admission: %v", err)
		}
		result := <-results
		if result.State.LogicalVersion == 0 || result.State.Fence == "" {
			t.Fatal("concurrent work admission omitted current authority")
		}
		admitted[result.State.WorkID] = result.State
	}
	// Both concurrent commands must cross their complete acknowledgement and
	// projection boundaries before a universal point-in-time assertion. The
	// first buffered result does not prove its sibling has stopped mutating.
	for _, workID := range workIDs {
		result, ok := admitted[workID]
		if !ok {
			t.Fatalf("concurrent work admission omitted %s", workID)
		}
		assertUniversalInvariants(t, h, projectID, missionID, "execution-admitted-"+result.WorkID.String())
	}
	return admitted
}

func validateTerminalSemanticEquivalence(want, got terminalSemanticProgress) error {
	if want.Lifecycle != got.Lifecycle || want.WorkRevisionID != got.WorkRevisionID || want.Generation != got.Generation || want.Complete != got.Complete || want.TotalWork != got.TotalWork {
		return fmt.Errorf("mission summary differs")
	}
	if !equalStateCounts(want.WorkCounts, got.WorkCounts) {
		return fmt.Errorf("work counts differ")
	}
	if !equalTerminalItems(want.Items, got.Items) {
		return fmt.Errorf("canonical terminal item facts differ")
	}
	if !equalStrings(want.Families, got.Families) {
		return fmt.Errorf("event-family coverage differs")
	}
	if !equalTrackerEvidence(want.Tracker, got.Tracker) {
		return fmt.Errorf("attributed tracker observations differ")
	}
	if !equalStrings(want.Repository.Targets, got.Repository.Targets) || !equalStrings(want.Repository.Publications, got.Repository.Publications) {
		return fmt.Errorf("repository targets or attributed publications differ")
	}
	if !equalTerminalOperations(want.Operations, got.Operations) {
		return fmt.Errorf("operation result bodies or digests differ")
	}
	if want.SemanticSHA256 == "" || want.SemanticSHA256 != got.SemanticSHA256 {
		return fmt.Errorf("semantic projection digest differs")
	}
	return nil
}

func equalStateCounts(a, b []productionruntime.StateCount) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
func equalTerminalItems(a, b []terminalSemanticItem) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
func equalTrackerEvidence(a, b []terminalTrackerEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
func equalTerminalOperations(a, b []terminalOperationEvidence) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] || a[index].OperationID == "" || a[index].ResultKind == "" || a[index].ResultBody == "" || a[index].ResultSHA256 == "" {
			return false
		}
	}
	return true
}

type terminalSemanticProgress struct {
	Lifecycle      string
	WorkRevisionID string
	Generation     uint64
	Complete       bool
	TotalWork      int
	WorkCounts     []productionruntime.StateCount
	Items          []terminalSemanticItem
	Families       []string
	Tracker        []terminalTrackerEvidence
	Repository     repositoryTerminalEvidence
	SemanticSHA256 string
	Operations     []terminalOperationEvidence
}

type terminalOperationEvidence struct {
	OperationID  string
	ResultKind   string
	ResultBody   string
	ResultSHA256 string
}

type terminalSemanticItem struct {
	Family         string
	AggregateID    string
	AuthorityState string
	ObservedState  string
	SemanticSHA256 string
	ResultKind     string
	ResultBody     string
	ResultSHA256   string
	Fenced         bool
}

func semanticTerminalProgress(t *testing.T, progress productionruntime.MissionProgress, trackerEvidence []terminalTrackerEvidence, repository repositoryTerminalEvidence, operationEvidence []terminalOperationEvidence) terminalSemanticProgress {
	t.Helper()
	items := make([]terminalSemanticItem, 0, len(progress.Items))
	familySet := make(map[string]struct{})
	for _, item := range progress.Items {
		semanticState := semanticCanonicalJSON(t, item.CanonicalState)
		entry := terminalSemanticItem{
			Family: item.Family, AggregateID: item.AggregateID,
			AuthorityState: item.AuthorityState, ObservedState: item.ObservedState,
			SemanticSHA256: digestBytes(semanticState),
		}
		var decoded map[string]any
		if err := json.Unmarshal(item.CanonicalState, &decoded); err != nil {
			t.Fatal(err)
		}
		fence, _ := nestedTextField(decoded, "fence")
		entry.Fenced = fence != ""
		if item.Family == "operation" {
			operation, err := journalv2.DecodeOperationStateV2(item.CanonicalState)
			if err != nil {
				t.Fatal(err)
			}
			result := semanticCanonicalJSON(t, operation.Result())
			entry.ResultKind = operation.ResultKind()
			entry.ResultBody = string(result)
			entry.ResultSHA256 = digestBytes(result)
		}
		items = append(items, entry)
		familySet[item.Family] = struct{}{}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Family != items[j].Family {
			return items[i].Family < items[j].Family
		}
		return items[i].AggregateID < items[j].AggregateID
	})
	counts := append([]productionruntime.StateCount(nil), progress.WorkCounts...)
	sort.Slice(counts, func(i, j int) bool { return counts[i].State < counts[j].State })
	families := make([]string, 0, len(familySet))
	for family := range familySet {
		families = append(families, family)
	}
	sort.Strings(families)
	result := terminalSemanticProgress{
		Lifecycle: progress.Lifecycle, WorkRevisionID: string(progress.WorkRevisionID), Generation: progress.Generation,
		Complete: progress.Complete, TotalWork: progress.TotalWork, WorkCounts: counts, Items: items, Families: families,
		Tracker: append([]terminalTrackerEvidence(nil), trackerEvidence...), Repository: repository,
		Operations: append([]terminalOperationEvidence(nil), operationEvidence...),
	}
	sort.Slice(result.Operations, func(i, j int) bool { return result.Operations[i].OperationID < result.Operations[j].OperationID })
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	result.SemanticSHA256 = digestBytes(canonical)
	return result
}

func operationEvidenceFromPublication(t *testing.T, publication journalv2.SealedCommitPlanV2) terminalOperationEvidence {
	t.Helper()
	operation := publication.OperationState()
	if operation.OperationID() == "" || operation.ResultKind() == "" || len(operation.Result()) == 0 || operation.ResultDigest().IsZero() {
		t.Fatal("sealed publication omitted exact operation result")
	}
	return terminalOperationEvidence{OperationID: string(operation.OperationID()), ResultKind: operation.ResultKind(), ResultBody: string(operation.Result()), ResultSHA256: operation.ResultDigest().String()}
}

func semanticCanonicalJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value = projectTerminalSemantics(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := journalv2.CanonicalObject(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func projectTerminalSemantics(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		projected := make(map[string]any)
		for key, child := range typed {
			if terminalSemanticField(key) {
				projected[key] = projectTerminalSemantics(child)
				continue
			}
			if nested, ok := child.(map[string]any); ok {
				candidate := projectTerminalSemantics(nested).(map[string]any)
				if len(candidate) != 0 {
					projected[key] = candidate
				}
			}
		}
		return projected
	case []any:
		projected := make([]any, 0, len(typed))
		for _, child := range typed {
			candidate := projectTerminalSemantics(child)
			if object, ok := candidate.(map[string]any); ok && len(object) == 0 {
				continue
			}
			projected = append(projected, candidate)
		}
		return projected
	default:
		return typed
	}
}

func terminalSemanticField(key string) bool {
	switch key {
	case "schema_version", "project_id", "mission_id", "work_revision_id", "work_id", "run_id", "lease_id", "generation",
		"stage", "outcome", "status", "lifecycle", "complete", "candidate", "contributor_digest", "criteria_digest", "criterion_ids",
		"review_id", "reviewer_actor_id", "handoff_id", "verdict", "acceptance_id", "criterion_id", "kind", "disposition", "producer",
		"policy_basis", "evidence_id", "state_digest_sha256", "repository_id", "target_ref", "expected_old", "result", "observed_target",
		"provider_marker", "effect_id", "intended_value", "expected_value", "expected_operation_marker", "assignee", "operation_marker",
		"provider_revision", "artifact_revision_id", "approval_id", "content_digest", "source_ref", "source_revision", "created_by", "actor_id",
		"governance_context_id", "policy_revision", "doctrine_revision", "profile_revision", "mission_type_revision", "fact_id", "detail_digest",
		"definition_revision", "definition_digest", "title", "description", "design_context", "required_guarantee", "active_facts", "evidence",
		"criteria", "review_ids", "governance", "binding", "workspace", "execution", "intent", "observation", "applied_observation", "contract",
		"artifact_revision", "validation", "approval":
		return true
	default:
		return false
	}
}

func digestBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}

func twoItemRevision(t *testing.T, projectID, missionID string) (workgraph.RevisionV2, governancecontext.Context) {
	t.Helper()
	works := make([]workgraph.WorkObservation, 0, 2)
	evidence := make([]workgraph.ObservationEvidence, 0, 2)
	scopes := make([]workgraph.ApprovedScope, 0, 2)
	for _, suffix := range []string{"a", "b"} {
		workID := ids.WorkID("work-" + suffix)
		scopeID := "scope-" + suffix
		work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{
			ProjectID: ids.ProjectID(projectID), WorkID: workID,
			Title: "Independent item " + suffix, Description: "Exercise portable concurrent authority.",
			DesignContext: "Use only the typed production host.",
			Criteria:      []workgraph.CriterionObservation{{ID: "criterion-" + suffix, Text: "Verify durable authority.", Outcome: "The authority remains durable.", Verifier: "test", Requirement: workgraph.CriterionRequired}},
			WriteScopes:   []workgraph.WriteScopeObservation{{Path: "component/" + suffix, Domain: "scenario", Access: workgraph.ScopeWriteExclusive}},
			Provenance:    []workgraph.ScopeReference{{ID: scopeID, Kind: workgraph.ScopePlanConcern}},
		})
		if err != nil {
			t.Fatal(err)
		}
		works = append(works, work)
		scopes = append(scopes, workgraph.ApprovedScope{ID: scopeID, Kind: workgraph.ScopePlanConcern, MissionID: ids.MissionID(missionID)})
		definition := sha256.Sum256([]byte("definition-" + suffix))
		evidence = append(evidence, workgraph.ObservationEvidence{
			WorkID: workID, Provider: "fake", ExternalID: "external-" + suffix,
			Revision: "provider-revision-" + suffix, MetadataSchema: 1,
			DefinitionDigest:       fmt.Sprintf("%x", definition),
			CriterionEvidenceHints: map[string]string{"criterion-" + suffix: "go test"},
		})
	}
	draft, err := workgraph.NewDraftObservation(ids.ProjectID(projectID), ids.MissionID(missionID), "artifact-plan", strings.Repeat("a", 64), scopes, works)
	if err != nil {
		t.Fatal(err)
	}
	finalized, err := workgraph.NewFinalizationObservation(draft, evidence)
	if err != nil {
		t.Fatal(err)
	}
	definition := sha256.Sum256([]byte("two-item-mission"))
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{
		Observation: finalized, PlanningSourceCommit: strings.Repeat("b", 40),
		MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: fmt.Sprintf("%x", definition)},
		DependencyPolicy:  workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-planner",
		FinalizedAt: time.Date(2026, 9, 5, 12, 1, 0, 0, time.UTC), GovernanceContextID: "governance-two-item",
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]governancecontext.Entry, 0, 4)
	for _, input := range []struct {
		kind                 governancecontext.EntryKind
		ref, revision, value string
	}{
		{governancecontext.KindConfig, "config.scenario", "policy-1", "config"},
		{governancecontext.KindDoctrine, "doctrine.scenario", "doctrine-1", "doctrine"},
		{governancecontext.KindProfile, "profile.scenario", "profile-1", "profile"},
		{governancecontext.KindMissionType, "mission-type.scenario", "mission-type-1", "mission-type"},
	} {
		commitment, err := governancecontext.CommitSHA256([]byte(input.value))
		if err != nil {
			t.Fatal(err)
		}
		entry, err := governancecontext.NewEntry(input.kind, input.ref, input.revision, commitment)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	governance, err := governancecontext.NewContext("governance-two-item", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return revision, governance
}
