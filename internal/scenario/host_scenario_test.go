package scenario

import (
	"crypto/sha256"
	"fmt"
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
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name(), CandidateVerifier: sourceBoundCandidateVerifier{}})
	projectID, missionID := "project-two-item", "mission-two-item"
	revision, governance := twoItemRevision(t, projectID, missionID)
	missionResult := h.createMission(projectID, missionID, "operation-mission-create")
	assertUniversalInvariants(t, h, projectID, missionID, "mission-publication")
	prior, _ := h.restart()
	assertStaleEpochRefusesBeforeRepository(t, h, prior)

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
	acknowledgedWorkgraph, err := h.host.PublishWorkgraph(t.Context(), h.host.RuntimeEpochID(), plan)
	if err != nil {
		t.Fatalf("publish two-item graph: %v", err)
	}
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
	missionResult, err = h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{
		SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: selectedState.LastOperationID(),
		ProjectID: selectedState.ProjectID(), MissionID: selectedState.MissionID(), ActorID: "actor-planner", ProcessID: "process-planner",
		OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1",
		ObservedAt: selectedState.ObservedAt(), EventType: mission.ReadinessEventWorkRevisionSelectedV2,
		Expected: missionResult.State(), Updated: selectedState, WorkRevisionSelection: &selection,
	})
	if err != nil {
		t.Fatalf("select acknowledged two-item graph: %v", err)
	}
	digestBeforeGraphRestart := semanticProgressDigest(t, h, projectID, missionID)
	prior, _ = h.restart()
	if got := semanticProgressDigest(t, h, projectID, missionID); got != digestBeforeGraphRestart {
		t.Fatalf("graph restart changed canonical projection: before=%s after=%s", digestBeforeGraphRestart, got)
	}
	assertStaleEpochRefusesBeforeRepository(t, h, prior)

	start := make(chan struct{})
	results := make(chan execution.ExecutionAuthorityResultV2, 2)
	errs := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	for index, workID := range []ids.WorkID{"work-a", "work-b"} {
		index, workID := index, workID
		go func() {
			ready.Done()
			<-start
			now := time.Date(2026, 9, 5, 12, 2, index, 0, time.UTC)
			result, executeErr := h.host.ExecuteWork(t.Context(), h.host.RuntimeEpochID(), execution.ExecutionAuthorityRequestV2{
				Kind:        execution.ExecutionAdmitV2,
				OperationID: journalv2.OperationID("operation-admit-" + workID.String()),
				ProjectID:   ids.ProjectID(projectID), MissionID: ids.MissionID(missionID),
				WorkRevisionID: revision.ID(), WorkID: workID,
				RunID: ids.RunID("run-" + workID.String()), ActorID: ids.ActorID("actor-" + workID.String()),
				ProcessID: ids.ProcessID("process-" + workID.String()), LeaseID: ids.LeaseID("lease-" + workID.String()),
				Generation: 1, ObservedAt: now, Deadline: now.Add(time.Hour), RuntimeEpochID: h.host.RuntimeEpochID(),
				Governance:       governance,
				OperationEventID: journalv2.EventID("event-operation-" + workID.String()),
				SlotEventID:      journalv2.EventID("event-slot-" + workID.String()),
			})
			results <- result
			errs <- executeErr
		}()
	}
	ready.Wait()
	close(start)
	admitted := make(map[ids.WorkID]execution.ExecutionSlotStateV2, 2)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent work admission: %v", err)
		}
		if result := <-results; result.State.LogicalVersion == 0 || result.State.Fence == "" {
			t.Fatal("concurrent work admission omitted current authority")
		} else {
			admitted[result.State.WorkID] = result.State
		}
	}
	progress, err := h.host.Progress().Mission(journalv2.ProjectID(projectID), journalv2.MissionID(missionID))
	if err != nil {
		t.Fatal(err)
	}
	if progress.TotalWork != 2 || len(progress.Running) != 2 {
		t.Fatalf("two-item progress lost parallel authority: total=%d running=%d progress=%+v", progress.TotalWork, len(progress.Running), progress)
	}
	completeTwoItemMission(t, h, missionResult.State(), revision, governance, admitted)
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
	terminalDigest := semanticProgressDigest(t, h, projectID, missionID)
	assertUniversalInvariants(t, h, projectID, missionID, "two-items-terminal")
	prior, _ = h.restart()
	assertStaleEpochRefusesBeforeRepository(t, h, prior)
	if got := semanticProgressDigest(t, h, projectID, missionID); got != terminalDigest {
		t.Fatalf("two-item restart changed terminal authority: before=%s after=%s", terminalDigest, got)
	}
	assertUniversalInvariants(t, h, projectID, missionID, "two-items-terminal-restored")
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
