package scenario

import (
	"context"
	"path/filepath"
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

type scriptedAssignmentProjection struct {
	base          tracker.AssignmentProjection
	observation   tracker.AssignmentObservation
	observeErr    error
	projectResult tracker.AssignmentMutation
	projectErr    error
	projectScript bool
	observes      int
	projects      int
	compensates   int
}

func (p *scriptedAssignmentProjection) ObserveAssignment(context.Context, tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	p.observes++
	return p.observation, p.observeErr
}

func (p *scriptedAssignmentProjection) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.projects++
	if p.projectScript {
		return p.projectResult, p.projectErr
	}
	return p.base.ProjectAssignment(ctx, request)
}

func (p *scriptedAssignmentProjection) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	p.compensates++
	return p.base.CompensateAssignment(ctx, request)
}

func TestDishonestFakeTrackerMatrixPreservesAuthorityV2(t *testing.T) {
	tests := []struct {
		name             string
		prepare          func(*testing.T, *trackerfake.Store, *trackerfake.Provider, tracker.ExternalWorkRef, *scriptedAssignmentProjection)
		wantProjectCalls int
		wantOutcome      string
	}{
		{
			name: "unavailable",
			prepare: func(_ *testing.T, _ *trackerfake.Store, _ *trackerfake.Provider, _ tracker.ExternalWorkRef, scripted *scriptedAssignmentProjection) {
				scripted.observeErr = &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "scripted unavailable"}
			},
			wantOutcome: "unknown",
		},
		{
			name: "stale_revision",
			prepare: func(t *testing.T, store *trackerfake.Store, provider *trackerfake.Provider, target tracker.ExternalWorkRef, scripted *scriptedAssignmentProjection) {
				if _, err := store.HumanSetStatus(t.Context(), target.ExternalID, "in_progress"); err != nil {
					t.Fatal(err)
				}
				scripted.observation = observeAssignment(t, provider, target)
			},
			wantOutcome: "absent",
		},
		{
			name: "contradictory_value",
			prepare: func(_ *testing.T, _ *trackerfake.Store, _ *trackerfake.Provider, _ tracker.ExternalWorkRef, scripted *scriptedAssignmentProjection) {
				// The projection contradicts its own canonical provider observation.
				scripted.observation.Assignee = "actor-contradiction"
			},
			wantOutcome: "unknown",
		},
		{
			name: "unattributed_marker",
			prepare: func(t *testing.T, _ *trackerfake.Store, provider *trackerfake.Provider, target tracker.ExternalWorkRef, scripted *scriptedAssignmentProjection) {
				mutation, err := provider.ProjectAssignment(t.Context(), tracker.ProjectAssignmentRequest{
					ExternalRef: target, OperationID: "operation-someone-else", Assignee: "actor-intended",
					ExpectedObservation: 1, RequiredGuarantee: tracker.AssignmentAtomicClaim,
				})
				if err != nil {
					t.Fatal(err)
				}
				scripted.observation = mutation.Observation
			},
			wantOutcome: "confirmed",
		},
		{
			name: "malicious_human_change",
			prepare: func(t *testing.T, store *trackerfake.Store, provider *trackerfake.Provider, target tracker.ExternalWorkRef, scripted *scriptedAssignmentProjection) {
				if err := store.HumanAssign(t.Context(), target.ExternalID, "human-owner"); err != nil {
					t.Fatal(err)
				}
				scripted.observation = observeAssignment(t, provider, target)
			},
			wantOutcome: "confirmed",
		},
		{
			name: "permanently_ambiguous",
			prepare: func(_ *testing.T, _ *trackerfake.Store, _ *trackerfake.Provider, _ tracker.ExternalWorkRef, scripted *scriptedAssignmentProjection) {
				scripted.projectScript = true
				scripted.projectResult = tracker.AssignmentMutation{Observation: scripted.observation, Guarantee: tracker.AssignmentAtomicClaim}
				scripted.projectErr = &tracker.Error{Kind: tracker.ErrorAmbiguous, Message: "provider cannot say whether it applied"}
			},
			wantProjectCalls: 1,
			wantOutcome:      "unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
			slot, _ := prepareDishonestTrackerAuthority(t, h)

			store, err := trackerfake.Open(filepath.Join(h.root, "dishonest-tracker.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Seed(t.Context(), trackerfake.WorkSeed{Key: "ticket-dishonest", Title: "Dishonest provider matrix"}); err != nil {
				t.Fatal(err)
			}
			provider := trackerfake.NewProvider(store)
			target := tracker.ExternalWorkRef{
				ProjectID: slot.ProjectID, WorkID: slot.WorkID, Provider: "fake", ExternalID: "ticket-dishonest",
				MetadataSchema: 1, CreatedByOperation: "operation-workgraph", LastObservation: 1,
			}
			initial := observeAssignment(t, provider, target)
			scripted := &scriptedAssignmentProjection{base: provider, observation: initial}

			at := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
			intent := execution.EffectIntentV2{
				EffectID: "effect-dishonest", OperationID: "operation-dishonest", Target: target,
				IntendedValue: "actor-intended", ExpectedProviderRevision: 1,
				RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: slot.ActorID, ProcessID: slot.ProcessID,
				Governance: slot.Governance, OriginRuntimeEpochID: slot.RuntimeEpochID,
				OriginObservedCreatedAt: at.Format(time.RFC3339Nano),
			}
			intent.CanonicalRequestDigest, err = execution.EffectRequestDigestV2(intent)
			if err != nil {
				t.Fatal(err)
			}
			request := execution.EffectAuthorityRequestV2{
				Action: execution.EffectRecordIntentV2, Intent: intent, RuntimeEpochID: h.host.RuntimeEpochID(),
				ExecutionSlot: slot, ObservedAt: at,
				OperationEventID: "event-dishonest-intent-operation", EffectEventID: "event-dishonest-intent",
			}
			recorded, err := h.host.ExecuteEffect(t.Context(), h.host.RuntimeEpochID(), request)
			if err != nil {
				t.Fatalf("record dishonest provider intent: %v", err)
			}
			assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "dishonest-intent-"+test.name)

			test.prepare(t, store, provider, target, scripted)
			providerBefore := observeAssignment(t, provider, target)
			historyBefore, err := store.History(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			dispatcher, err := execution.NewAssignmentEffectDispatcherV2(scripted)
			if err != nil {
				t.Fatal(err)
			}
			request.Action = execution.EffectStartAttemptV2
			request.ExecutionSlot = recorded.ExecutionSlot
			request.ObservedAt = at.Add(time.Second)
			request.OperationEventID = "event-dishonest-attempt-operation"
			request.EffectEventID = "event-dishonest-attempt"
			result, dispatchErr := h.host.ExecuteEffectDispatch(t.Context(), h.host.RuntimeEpochID(), execution.EffectDispatchCommandV2{
				Authorization: request, ObservationOperationEventID: "event-dishonest-observation-operation",
				ObservationEffectEventID: "event-dishonest-observation", ObservationAt: at.Add(2 * time.Second),
			}, dispatcher)
			if !execution.IsKind(dispatchErr, execution.KindDecisionRequired) {
				t.Fatalf("dishonest provider did not produce a typed decision boundary: result=%+v err=%v", result, dispatchErr)
			}
			if result.State.Stage != execution.EffectObservationRecordedV2 || result.State.Observation == nil || result.State.Observation.Outcome != test.wantOutcome || result.State.PermissionMarker != "" || result.State.Outcome != "" {
				t.Fatalf("dishonest provider state is not explicit unresolved evidence: %+v", result.State)
			}
			if scripted.projects != test.wantProjectCalls || scripted.compensates != 0 {
				t.Fatalf("dishonest provider crossed mutation boundary: projects=%d want=%d compensates=%d", scripted.projects, test.wantProjectCalls, scripted.compensates)
			}
			if test.name == "permanently_ambiguous" {
				// Replaying the admitted attempt may observe, but it may never
				// silently repeat the indeterminate provider mutation.
				_, replayErr := h.host.ExecuteEffectDispatch(t.Context(), h.host.RuntimeEpochID(), execution.EffectDispatchCommandV2{
					Authorization: request, ObservationOperationEventID: "event-dishonest-observation-operation",
					ObservationEffectEventID: "event-dishonest-observation", ObservationAt: at.Add(2 * time.Second),
				}, dispatcher)
				if (!execution.IsKind(replayErr, execution.KindDecisionRequired) && !execution.IsKind(replayErr, execution.KindStaleAuthority)) || scripted.projects != 1 || scripted.compensates != 0 {
					t.Fatalf("ambiguous replay repeated an external effect: projects=%d compensates=%d err=%v", scripted.projects, scripted.compensates, replayErr)
				}
			}

			publicItem, err := h.host.Progress().Get(journalv2.ProjectID(slot.ProjectID), journalv2.MissionID(slot.MissionID), productionruntime.ProgressEffects, string(intent.EffectID))
			if err != nil {
				t.Fatalf("public progress omitted unresolved effect: %v", err)
			}
			publicState, err := execution.DecodeExternalEffectStateV2(publicItem.CanonicalState)
			if err != nil || publicState.Stage != result.State.Stage || publicState.LogicalVersion != result.State.LogicalVersion || publicState.Observation == nil || publicState.Observation.Outcome != test.wantOutcome {
				t.Fatalf("public progress disagrees with durable dishonest-provider result: public=%+v result=%+v err=%v", publicState, result.State, err)
			}
			providerAfter := observeAssignment(t, provider, target)
			historyAfter, err := store.History(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !sameAssignmentEvidence(providerBefore, providerAfter) || len(historyAfter) != len(historyBefore) {
				t.Fatalf("unauthorized provider mutation occurred: before=%+v after=%+v history=%d->%d", providerBefore, providerAfter, len(historyBefore), len(historyAfter))
			}
			assertUniversalInvariants(t, h, slot.ProjectID.String(), slot.MissionID.String(), "dishonest-result-"+test.name)
		})
	}
}

func prepareDishonestTrackerAuthority(t *testing.T, h *portableHarness) (execution.ExecutionSlotStateV2, governancecontext.Context) {
	t.Helper()
	const projectID, missionID = "project-dishonest", "mission-dishonest"
	revision, governance := twoItemRevision(t, projectID, missionID)
	missionResult := h.createMission(projectID, missionID, "operation-mission-create")
	plan, err := workgraph.BuildFinalizationAuthorityV2(workgraph.FinalizationPlanInputV2{
		Context: workgraph.AuthorityContextV2{
			OperationID: "operation-workgraph", RuntimeEpochID: h.host.RuntimeEpochID(), ActorID: "actor-planner", ProcessID: "process-planner",
			DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", Governance: []string{"governance-two-item"},
			ObservedCreatedAt: "2026-09-05T14:00:00Z", OperationEventID: "event-workgraph-operation", DomainEventID: "event-workgraph-domain",
		},
		Revision: revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := h.host.PublishWorkgraph(t.Context(), h.host.RuntimeEpochID(), plan)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := mission.SealAcknowledgedInitialWorkRevisionSelectionV2(revision, ack)
	if err != nil {
		t.Fatal(err)
	}
	definitionDigest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"portable-scenario"}`))
	if err != nil {
		t.Fatal(err)
	}
	selectedState, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: projectID, MissionID: missionID, DefinitionRevision: "definition-1", DefinitionDigest: definitionDigest,
		Version: 2, Lifecycle: mission.MissionOpen, CurrentWorkRevision: selection.Head(), LastOperationID: "operation-select-workgraph", ObservedAt: "2026-09-05T14:01:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: selectedState.LastOperationID(), ProjectID: projectID, MissionID: missionID,
		ActorID: "actor-planner", ProcessID: "process-planner", OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: "doctrine-1",
		ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: selectedState.ObservedAt(), EventType: mission.ReadinessEventWorkRevisionSelectedV2,
		Expected: missionResult.State(), Updated: selectedState, WorkRevisionSelection: &selection,
	}); err != nil {
		t.Fatal(err)
	}
	assertUniversalInvariants(t, h, projectID, missionID, "dishonest-workgraph-selected")
	now := time.Date(2026, 9, 5, 14, 2, 0, 0, time.UTC)
	admitted, err := h.host.ExecuteWork(t.Context(), h.host.RuntimeEpochID(), execution.ExecutionAuthorityRequestV2{
		Kind: execution.ExecutionAdmitV2, OperationID: "operation-admit-work-a", ProjectID: projectID, MissionID: missionID,
		WorkRevisionID: revision.ID(), WorkID: ids.WorkID("work-a"), RunID: "run-work-a", ActorID: "actor-work-a",
		ProcessID: "process-work-a", LeaseID: "lease-work-a", Generation: 1, ObservedAt: now, Deadline: now.Add(time.Hour),
		RuntimeEpochID: h.host.RuntimeEpochID(), Governance: governance,
		OperationEventID: "event-operation-work-a", SlotEventID: "event-slot-work-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertUniversalInvariants(t, h, projectID, missionID, "dishonest-execution-admitted")
	return admitted.State, governance
}

func observeAssignment(t *testing.T, provider tracker.AssignmentProjection, target tracker.ExternalWorkRef) tracker.AssignmentObservation {
	t.Helper()
	observation, err := provider.ObserveAssignment(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func sameAssignmentEvidence(a, b tracker.AssignmentObservation) bool {
	return a.ExternalRef.ProjectID == b.ExternalRef.ProjectID && a.ExternalRef.WorkID == b.ExternalRef.WorkID &&
		a.ExternalRef.Provider == b.ExternalRef.Provider && a.ExternalRef.ExternalID == b.ExternalRef.ExternalID &&
		a.ExternalRef.LastObservation == b.ExternalRef.LastObservation && a.Assignee == b.Assignee &&
		a.OperationMarker == b.OperationMarker && a.Observation.Revision == b.Observation.Revision
}

var _ tracker.AssignmentProjection = (*scriptedAssignmentProjection)(nil)
