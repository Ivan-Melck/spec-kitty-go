package scenario

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type terminalWorkFacts struct {
	execution               execution.ExecutionSlotStateV2
	workspace               workspace.StateV2
	workspaceRecordRevision uint64
	binding                 workspace.BindingV2
	handoff                 handoff.StateV2
	review                  review.StateV2
	evidence                integration.AcceptanceEvidenceV2
	accepted                integration.AcceptanceStateV2
}

func completeTwoItemMission(t *testing.T, h *portableHarness, missionState mission.ReadinessAuthorityStateV2, revision workgraph.RevisionV2, governance governancecontext.Context, admitted map[ids.WorkID]execution.ExecutionSlotStateV2, restartEveryBoundary bool) {
	t.Helper()
	repositoryID := ids.RepositoryID("repository-two-item")
	targetRef := "refs/heads/main"
	base := strings.Repeat("a", 40)
	h.git.enable(repositoryID, targetRef, base)

	workIDs := []ids.WorkID{"work-a", "work-b"}
	facts := make(map[ids.WorkID]*terminalWorkFacts, len(workIDs))
	for index, workID := range workIDs {
		active, ok := admitted[workID]
		if !ok {
			t.Fatalf("missing admission for %s", workID)
		}
		facts[workID] = runWorkspaceToReady(t, h, active, governance, repositoryID, base, index, restartEveryBoundary)
	}

	for index, workID := range workIDs {
		ensureWorkCurrent(t, h, facts[workID], governance, "before-handoff-"+workID.String())
		candidate := fmt.Sprintf("%040x", index+11)
		facts[workID].handoff = submitTerminalHandoff(t, h, facts[workID], revision, repositoryID, candidate, index)
		restartWorkAfterDurableBoundary(t, h, facts[workID], governance, "handoff-"+workID.String(), restartEveryBoundary)
		facts[workID].review = submitTerminalReview(t, h, facts[workID], revision, index)
		restartWorkAfterDurableBoundary(t, h, facts[workID], governance, "review-"+workID.String(), restartEveryBoundary)
		facts[workID].evidence = publishTerminalEvidence(t, h, facts[workID], index)
		restartWorkAfterDurableBoundary(t, h, facts[workID], governance, "artifact-"+workID.String(), restartEveryBoundary)
	}

	currentMission := missionState
	for index, workID := range workIDs {
		ensureWorkCurrent(t, h, facts[workID], governance, "before-acceptance-"+workID.String())
		policyID := "policy-accept-" + workID.String()
		request := terminalAcceptanceRequest(t, h, facts[workID], revision, repositoryID, policyID, index)
		currentMission = recordResolvedPolicyFact(t, h, currentMission, request, facts[workID], governance, restartEveryBoundary)
		request.RuntimeEpochID = h.host.RuntimeEpochID()
		accepted, err := h.host.ExecuteAcceptance(t.Context(), h.host.RuntimeEpochID(), request)
		if err != nil {
			t.Fatalf("accept %s: %v", workID, err)
		}
		if accepted.State.Stage != integration.AcceptanceGrantedV2 {
			t.Fatalf("accept %s stage=%s", workID, accepted.State.Stage)
		}
		facts[workID].accepted = accepted.State
		restartWorkAfterDurableBoundary(t, h, facts[workID], governance, "acceptance-"+workID.String(), restartEveryBoundary)

		integrated, err := h.host.ExecuteIntegration(t.Context(), h.host.RuntimeEpochID(), integration.IntegrationAuthorityRequestV2{
			OperationID: journalv2.OperationID("operation-integrate-" + workID.String()),
			AttemptID:   "attempt-" + workID.String(), ProjectID: accepted.State.ProjectID,
			MissionID: accepted.State.MissionID, WorkRevisionID: accepted.State.WorkRevisionID,
			WorkID: workID, AcceptanceID: accepted.State.AcceptanceID,
			RepositoryID: repositoryID, TargetRef: targetRef, Candidate: accepted.State.Candidate,
			RuntimeEpochID: h.host.RuntimeEpochID(), ActorID: "actor-integrator",
			ProcessID: ids.ProcessID("process-integrator-" + workID.String()), Governance: accepted.State.Governance,
			ObservedAt: terminalTime(index, 40),
		})
		if err != nil {
			t.Fatalf("integrate %s: %v", workID, err)
		}
		if integrated.Stage != integration.IntegrationAttemptCompletedV2 {
			t.Fatalf("integrate %s stage=%s", workID, integrated.Stage)
		}
		restartWorkAfterDurableBoundary(t, h, facts[workID], governance, "integration-"+workID.String(), restartEveryBoundary)
		markExecutionCompleted(t, h, facts[workID].execution, governance, index)
		restartAfterDurableBoundary(t, h, accepted.State.ProjectID.String(), accepted.State.MissionID.String(), "execution-outcome-"+workID.String(), restartEveryBoundary)
	}

	completeMissionAuthority(t, h, currentMission, revision.ID())
	restartAfterDurableBoundary(t, h, string(currentMission.ProjectID()), string(currentMission.MissionID()), "mission-completed", restartEveryBoundary)
}

func runWorkspaceToReady(t *testing.T, h *portableHarness, active execution.ExecutionSlotStateV2, governance governancecontext.Context, repositoryID ids.RepositoryID, pinnedBase string, index int, restartEveryBoundary bool) *terminalWorkFacts {
	t.Helper()
	if active.RuntimeEpochID != h.host.RuntimeEpochID() {
		active = rebindUnboundExecutionToCurrent(t, h, active, governance, "before-workspace-"+active.WorkID.String())
	}
	managedRoot := filepath.Join(h.root, "workspaces")
	canonicalPath := filepath.Join(managedRoot, active.WorkID.String())
	claim, err := workspace.WorkspacePathClaimDigestV2(repositoryID, managedRoot, canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.BindingV2{
		WorkspaceID: ids.WorkspaceID("workspace-" + active.WorkID.String()), ProjectID: active.ProjectID,
		MissionID: active.MissionID, WorkRevisionID: active.WorkRevisionID, WorkID: active.WorkID,
		RunID: active.RunID, LeaseID: active.LeaseID, Generation: active.Generation, Fence: active.Fence,
		RuntimeEpochID: active.RuntimeEpochID, ActorID: active.ActorID, ProcessID: active.ProcessID,
		RepositoryID: repositoryID, LaneID: ids.LaneID("lane-" + active.WorkID.String()),
		ManagedRoot: managedRoot, CanonicalPath: canonicalPath,
		PathIdentity:    workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true},
		PathClaimDigest: claim, OwnedBranch: "kitty/" + active.WorkID.String(), PinnedBase: pinnedBase,
	}
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{
		Binding: binding, EffectID: journalv2.EffectID("effect-create-" + active.WorkID.String()),
		EventID: journalv2.EventID("event-workspace-bind-" + active.WorkID.String()), OccurredAt: terminalTime(index, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := h.host.ExecuteWorkspace(t.Context(), h.host.RuntimeEpochID(), execution.WorkspaceAuthorityRequestV2{
		Kind: execution.WorkspaceBindV2, OperationID: journalv2.OperationID("operation-workspace-bind-" + active.WorkID.String()),
		OperationEventID: journalv2.EventID("event-operation-workspace-bind-" + active.WorkID.String()),
		ExecutionEventID: journalv2.EventID("event-execution-workspace-bind-" + active.WorkID.String()),
		Governance:       active.Governance, Contribution: contribution, ObservedAt: terminalTime(index, 1),
	})
	if err != nil {
		t.Fatalf("bind workspace %s: %v", active.WorkID, err)
	}

	current := bound.Workspace
	currentExecution := bound.Execution
	recordRevision := bound.RecordRevision
	if recordRevision == 0 {
		t.Fatalf("bind workspace %s omitted reusable authority revision", active.WorkID)
	}
	currentExecution, current, recordRevision = restartBoundWorkspaceAfterDurableBoundary(t, h, currentExecution, current, recordRevision, "workspace-bound-"+active.WorkID.String(), restartEveryBoundary)
	ready := workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "gitdir-" + active.WorkID.String(), IndexIdentity: "index-" + active.WorkID.String(), Clean: true, DependencyHead: binding.PinnedBase, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}
	steps := []struct {
		stage       workspace.StageV2
		observation workspace.ObservationV2
		effectID    journalv2.EffectID
	}{
		{workspace.StageWorktreeObservedV2, workspace.ObservationV2{Registered: true}, ""},
		{workspace.StageBaseVerifiedV2, workspace.ObservationV2{Registered: true, Head: binding.PinnedBase}, ""},
		{workspace.StageDependenciesRequestedV2, workspace.ObservationV2{Registered: true, Head: binding.PinnedBase}, journalv2.EffectID("effect-dependencies-" + active.WorkID.String())},
		{workspace.StageDependenciesObservedV2, ready, ""},
		{workspace.StageOwnershipVerifiedV2, ready, ""},
		{workspace.StageReadyV2, ready, ""},
	}
	for stepIndex, step := range steps {
		occurredAt := terminalTime(index, stepIndex+2)
		transition, transitionErr := workspace.NewTransitionContributionV2(workspace.TransitionContributionInputV2{
			Prior: current, RecordRevision: recordRevision,
			EventID:   journalv2.EventID(fmt.Sprintf("event-workspace-stage-%s-%d", active.WorkID, stepIndex)),
			NextStage: step.stage, Observation: step.observation, EffectID: step.effectID, OccurredAt: occurredAt,
		})
		if transitionErr != nil {
			t.Fatalf("workspace contribution %s %s: %v", active.WorkID, step.stage, transitionErr)
		}
		governanceRevisions := []string{active.Governance.DoctrineRevision, active.Governance.ProfileRevision}
		sort.Strings(governanceRevisions)
		request := workspace.StageAuthorityRequestV2{
			OperationID:      journalv2.OperationID(fmt.Sprintf("operation-workspace-stage-%s-%d", active.WorkID, stepIndex)),
			OperationEventID: journalv2.EventID(fmt.Sprintf("event-operation-workspace-stage-%s-%d", active.WorkID, stepIndex)),
			Contribution:     transition, PolicyRevision: active.Governance.PolicyRevision,
			DoctrineRevision: active.Governance.DoctrineRevision, ProfileRevision: active.Governance.ProfileRevision,
			GovernanceRevisions: governanceRevisions, ObservedAt: occurredAt,
		}
		staged, stageErr := h.host.ExecuteWorkspaceStage(t.Context(), h.host.RuntimeEpochID(), request)
		if stageErr != nil {
			t.Fatalf("stage workspace %s %s: %v host=%+v", active.WorkID, step.stage, stageErr, h.host.Status())
		}
		current = staged.State
		recordRevision = staged.RecordRevision
		if recordRevision == 0 {
			t.Fatalf("stage workspace %s %s omitted reusable authority revision", active.WorkID, step.stage)
		}
		currentExecution, current, recordRevision = restartBoundWorkspaceAfterDurableBoundary(t, h, currentExecution, current, recordRevision, fmt.Sprintf("workspace-%s-%s", active.WorkID, step.stage), restartEveryBoundary)
	}
	if current.Stage != workspace.StageReadyV2 {
		t.Fatalf("workspace %s not ready: %s", active.WorkID, current.Stage)
	}
	return &terminalWorkFacts{execution: currentExecution, workspace: current, workspaceRecordRevision: recordRevision, binding: current.Binding}
}

func submitTerminalHandoff(t *testing.T, h *portableHarness, facts *terminalWorkFacts, revision workgraph.RevisionV2, repositoryID ids.RepositoryID, candidate string, index int) handoff.StateV2 {
	t.Helper()
	criterionID := "criterion-" + strings.TrimPrefix(facts.execution.WorkID.String(), "work-")
	criteriaDigest, err := handoff.CriterionSetDigestV2([]string{criterionID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.host.SubmitHandoff(t.Context(), h.host.RuntimeEpochID(), handoff.AuthorityRequestV2{
		OperationID: journalv2.OperationID("operation-handoff-" + facts.execution.WorkID.String()),
		HandoffID:   ids.HandoffID("handoff-" + facts.execution.WorkID.String()),
		ProjectID:   facts.execution.ProjectID, MissionID: facts.execution.MissionID,
		WorkRevisionID: facts.execution.WorkRevisionID, WorkID: facts.execution.WorkID,
		RunID: facts.execution.RunID, RuntimeEpochID: h.host.RuntimeEpochID(), Workspace: facts.binding,
		LeaseID: facts.execution.LeaseID, LeaseGeneration: facts.execution.Generation, Fence: facts.execution.Fence,
		ActorID: facts.execution.ActorID, ProcessID: facts.execution.ProcessID,
		RepositoryID: repositoryID, RepositoryPath: h.root, LaneID: facts.binding.LaneID,
		OwnedBranch: facts.binding.OwnedBranch, PinnedBase: facts.binding.PinnedBase, Candidate: candidate,
		Contributors: []ids.ActorID{facts.execution.ActorID}, CriterionIDs: []string{criterionID},
		CriteriaDigest: criteriaDigest, WorkRevisionCriteriaDigest: revision.CriteriaDigest(),
		EvidenceDigest: strings.Repeat(fmt.Sprintf("%x", index+1), 64), Governance: facts.execution.Governance,
		ObservedAt: terminalTime(index, 20),
	})
	if err != nil {
		t.Fatalf("handoff %s: %v", facts.execution.WorkID, err)
	}
	if result.State.Stage != handoff.StageCandidateRecordedV2 {
		t.Fatalf("handoff %s stage=%s", facts.execution.WorkID, result.State.Stage)
	}
	return result.State
}

func submitTerminalReview(t *testing.T, h *portableHarness, facts *terminalWorkFacts, revision workgraph.RevisionV2, index int) review.StateV2 {
	t.Helper()
	criterionID := "criterion-" + strings.TrimPrefix(facts.execution.WorkID.String(), "work-")
	result, err := h.host.SubmitReview(t.Context(), h.host.RuntimeEpochID(), review.AuthorityRequestV2{
		OperationID: journalv2.OperationID("operation-review-" + facts.execution.WorkID.String()),
		ReviewID:    ids.ReviewID("review-" + facts.execution.WorkID.String()), HandoffID: facts.handoff.HandoffID,
		ProjectID: facts.execution.ProjectID, MissionID: facts.execution.MissionID,
		WorkRevisionID: facts.execution.WorkRevisionID, WorkID: facts.execution.WorkID,
		RuntimeEpochID: h.host.RuntimeEpochID(), Workspace: facts.binding,
		ReviewerActorID: "actor-independent-reviewer", ReviewerProcessID: ids.ProcessID("process-reviewer-" + facts.execution.WorkID.String()),
		Candidate: facts.handoff.Candidate, HandoffContributorDigest: facts.handoff.ContributorDigest,
		WorkRevisionCriteriaDigest: revision.CriteriaDigest(), Criteria: []review.CriterionEvidenceV2{{
			CriterionID: criterionID, Status: review.Passed, EvidenceDigest: strings.Repeat("e", 64),
			ArtifactDigest: strings.Repeat("f", 64), Candidate: facts.handoff.Candidate,
			PolicyRevision: facts.execution.Governance.PolicyRevision,
		}}, Verdict: review.Approved, PolicyRevision: facts.execution.Governance.PolicyRevision,
		Governance: facts.execution.Governance, ObservedAt: terminalTime(index, 25),
	})
	if err != nil {
		t.Fatalf("review %s: %v", facts.execution.WorkID, err)
	}
	if result.State.Stage != review.StageVerdictRecordedV2 || result.State.Verdict != review.Approved {
		t.Fatalf("review %s incomplete: %+v", facts.execution.WorkID, result.State)
	}
	if result.RecordRevision == 0 {
		t.Fatalf("review %s omitted reusable authority revision", facts.execution.WorkID)
	}
	return result.State
}

func publishTerminalEvidence(t *testing.T, h *portableHarness, facts *terminalWorkFacts, index int) integration.AcceptanceEvidenceV2 {
	t.Helper()
	contentBytes := []byte("scenario verification evidence for " + facts.execution.WorkID.String())
	content, err := artifact.CommitContentV2(contentBytes)
	if err != nil {
		t.Fatal(err)
	}
	kind := artifact.KindIntent
	if index%2 == 1 {
		kind = artifact.KindPlan
	}
	revisionID := "evidence-revision-" + facts.execution.WorkID.String()
	revision, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{
		ID: revisionID, MissionID: facts.execution.MissionID.String(), Kind: kind, RevisionNumber: 1,
		ContentDigest: content.Digest(), SourceRef: "scenario/evidence/" + facts.execution.WorkID.String(),
		SourceRevision: facts.handoff.Candidate, CreatedBy: "actor-independent-reviewer", CreatedAt: terminalTime(index, 26),
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := artifact.PolicyFor(kind)
	if err != nil {
		t.Fatal(err)
	}
	validation, err := artifact.NewValidationResult(artifact.ValidationResultParams{
		ArtifactRevisionID: revisionID, Verdict: artifact.ValidationPass, Policy: policy, ValidatedAt: terminalTime(index, 27),
	})
	if err != nil {
		t.Fatal(err)
	}
	governanceRevisions := []string{
		"config." + facts.execution.Governance.PolicyRevision,
		"doctrine." + facts.execution.Governance.DoctrineRevision,
		"mission_type." + facts.execution.Governance.MissionTypeRevision,
		"profile." + facts.execution.Governance.ProfileRevision,
	}
	sort.Strings(governanceRevisions)
	plan, err := artifact.BuildArtifactPublishAuthorityV2(artifact.PublishPlanInputV2{
		Context: artifact.AuthorityContextV2{
			ProjectID: journalv2.ProjectID(facts.execution.ProjectID), OperationID: journalv2.OperationID("operation-evidence-" + facts.execution.WorkID.String()),
			RuntimeEpochID: h.host.RuntimeEpochID(), ActorID: "actor-independent-reviewer", ProcessID: journalv2.ProcessID("process-evidence-" + facts.execution.WorkID.String()),
			DoctrineRevision: facts.execution.Governance.DoctrineRevision, ProfileRevision: facts.execution.Governance.ProfileRevision,
			PolicyRevision: facts.execution.Governance.PolicyRevision, Governance: governanceRevisions, ObservedCreatedAt: terminalTime(index, 27).Format(time.RFC3339Nano),
			OperationEventID: journalv2.EventID("event-operation-evidence-" + facts.execution.WorkID.String()),
			CatalogEventID:   journalv2.EventID("event-catalog-evidence-" + facts.execution.WorkID.String()), RevisionEventID: journalv2.EventID("event-revision-evidence-" + facts.execution.WorkID.String()),
		},
		Revision: revision, Validation: validation, Content: content,
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	ack, err := h.host.PublishArtifact(t.Context(), h.host.RuntimeEpochID(), plan)
	if err != nil {
		t.Fatalf("publish evidence %s: %v", facts.execution.WorkID, err)
	}
	var evidenceHead journalv2.HeadTransitionV2
	for _, head := range sealed.Heads() {
		if head.Coordinate.Kind == "artifact_revision" {
			evidenceHead = head
			break
		}
	}
	if ack.ProviderRevision() == 0 || evidenceHead.Coordinate.Kind == "" || evidenceHead.CanonicalStateDigest.IsZero() {
		t.Fatalf("evidence publication %s omitted exact head authority", facts.execution.WorkID)
	}
	criterionID := "criterion-" + strings.TrimPrefix(facts.execution.WorkID.String(), "work-")
	return integration.AcceptanceEvidenceV2{
		CriterionID: criterionID, Kind: "automated_test", Disposition: integration.EvidenceSatisfiedV2,
		Producer: "scenario.verify", PolicyBasis: "typed production-host scenario", EvidenceID: revisionID,
		Coordinate: evidenceHead.Coordinate, LogicalVersion: evidenceHead.NextLogicalVersion,
		RecordRevision: ack.ProviderRevision(), StateDigestSHA256: evidenceHead.CanonicalStateDigest.String(),
	}
}

func terminalAcceptanceRequest(t *testing.T, h *portableHarness, facts *terminalWorkFacts, revision workgraph.RevisionV2, repositoryID ids.RepositoryID, policyID string, index int) integration.AcceptanceAuthorityRequestV2 {
	t.Helper()
	return integration.AcceptanceAuthorityRequestV2{
		OperationID:  journalv2.OperationID("operation-accept-" + facts.execution.WorkID.String()),
		AcceptanceID: "acceptance-" + facts.execution.WorkID.String(), ProjectID: facts.execution.ProjectID,
		MissionID: facts.execution.MissionID, WorkRevisionID: revision.ID(), WorkID: facts.execution.WorkID,
		HandoffID: facts.handoff.HandoffID, RepositoryID: repositoryID, Candidate: facts.handoff.Candidate,
		ContributorDigest: facts.handoff.ContributorDigest, ReviewIDs: []ids.ReviewID{facts.review.ReviewID},
		Evidence:       []integration.AcceptanceEvidenceV2{facts.evidence},
		PolicyRevision: facts.execution.Governance.PolicyRevision, PolicyFactID: policyID,
		RuntimeEpochID: h.host.RuntimeEpochID(), ActorID: "actor-acceptor",
		ProcessID: ids.ProcessID("process-acceptor-" + facts.execution.WorkID.String()), Governance: facts.execution.Governance,
		ObservedAt: terminalTime(index, 30),
	}
}

func recordResolvedPolicyFact(t *testing.T, h *portableHarness, current mission.ReadinessAuthorityStateV2, request integration.AcceptanceAuthorityRequestV2, facts *terminalWorkFacts, governance governancecontext.Context, restartEveryBoundary bool) mission.ReadinessAuthorityStateV2 {
	t.Helper()
	openAt := request.ObservedAt.Add(-2 * time.Second)
	openOperation := journalv2.OperationID("operation-open-" + request.PolicyFactID)
	openDigest, _ := journalv2.ReducerStateDigestV2([]byte(`{"status":"open"}`))
	scopedFact := mission.ScopedMissionFactV2{
		Kind: mission.ScopedFactDecisionV2, FactID: request.PolicyFactID,
		Scope:            mission.ActionRef{ID: mission.ActionAcceptanceEvaluate, Scope: mission.AuthorityScope{Kind: mission.ScopeMission, Identity: request.MissionID.String()}},
		EvidenceIdentity: "policy-open-" + request.PolicyFactID, EvidenceDigest: openDigest.String(),
	}
	openState := cloneReadinessState(t, current, current.Version()+1, current.Lifecycle(), openOperation, openAt, []mission.ScopedMissionFactV2{scopedFact})
	openFact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), FactID: request.PolicyFactID, Kind: mission.MissionFactDecisionV2, Status: mission.MissionFactOpenV2, LogicalVersion: 1, DetailDigest: openDigest, LastOperationID: openOperation, ObservedAt: openAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	openResult, err := h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: openOperation, ProjectID: current.ProjectID(), MissionID: current.MissionID(), ActorID: "actor-policy", ProcessID: "process-policy", OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: request.Governance.DoctrineRevision, ProfileRevision: request.Governance.ProfileRevision, PolicyRevision: request.PolicyRevision, ObservedAt: openAt.Format(time.RFC3339Nano), EventType: mission.ReadinessEventFactOpenedV2, Expected: current, Updated: openState, RelatedFact: &mission.MissionFactTransitionV2{Successor: openFact}})
	if err != nil {
		t.Fatalf("open policy fact %s: %v", request.PolicyFactID, err)
	}
	restartWorkAfterDurableBoundary(t, h, facts, governance, "policy-open-"+request.WorkID.String(), restartEveryBoundary)

	resolvedAt := request.ObservedAt.Add(-time.Second)
	resolveOperation := journalv2.OperationID("operation-resolve-" + request.PolicyFactID)
	resolvedState := cloneReadinessState(t, openResult.State(), openResult.State().Version()+1, openResult.State().Lifecycle(), resolveOperation, resolvedAt, nil)
	resolvedFact, err := mission.NewMissionFactStateV2(mission.MissionFactStateInputV2{ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), FactID: request.PolicyFactID, Kind: mission.MissionFactDecisionV2, Status: mission.MissionFactResolvedV2, LogicalVersion: 2, DetailDigest: integration.AcceptancePolicyRevisionDigestV2(request), LastOperationID: resolveOperation, ObservedAt: resolvedAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: resolveOperation, ProjectID: current.ProjectID(), MissionID: current.MissionID(), ActorID: "actor-policy", ProcessID: "process-policy", OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: request.Governance.DoctrineRevision, ProfileRevision: request.Governance.ProfileRevision, PolicyRevision: request.PolicyRevision, ObservedAt: resolvedAt.Format(time.RFC3339Nano), EventType: mission.ReadinessEventFactClosedV2, Expected: openResult.State(), Updated: resolvedState, RelatedFact: &mission.MissionFactTransitionV2{Prior: openFact, Successor: resolvedFact, ExpectedRecordRevision: openResult.RecordRevision()}})
	if err != nil {
		t.Fatalf("resolve policy fact %s: %v", request.PolicyFactID, err)
	}
	restartWorkAfterDurableBoundary(t, h, facts, governance, "policy-resolved-"+request.WorkID.String(), restartEveryBoundary)
	return resolved.State()
}

func restartAfterDurableBoundary(t *testing.T, h *portableHarness, projectID, missionID, boundary string, enabled bool) {
	t.Helper()
	if !enabled {
		return
	}
	t.Logf("restart boundary=%s phase=before", boundary)
	before := semanticProgressDigest(t, h, projectID, missionID)
	predecessor, _ := h.restart()
	t.Logf("restart boundary=%s phase=restored", boundary)
	after := semanticProgressDigest(t, h, projectID, missionID)
	if after != before {
		t.Fatalf("boundary %s changed recovered projection: before=%s after=%s", boundary, before, after)
	}
	assertStaleEpochRefusesBeforeRepository(t, h, predecessor)
}

func restartUnboundExecutionAfterDurableBoundary(t *testing.T, h *portableHarness, state execution.ExecutionSlotStateV2, governance governancecontext.Context, boundary string, enabled bool) execution.ExecutionSlotStateV2 {
	t.Helper()
	if !enabled {
		return state
	}
	restartAfterDurableBoundary(t, h, state.ProjectID.String(), state.MissionID.String(), boundary, true)
	return rebindUnboundExecutionToCurrent(t, h, state, governance, boundary)
}

func rebindUnboundExecutionToCurrent(t *testing.T, h *portableHarness, state execution.ExecutionSlotStateV2, governance governancecontext.Context, boundary string) execution.ExecutionSlotStateV2 {
	t.Helper()
	deadline, err := time.Parse(time.RFC3339Nano, state.Deadline)
	if err != nil {
		t.Fatal(err)
	}
	observedAt := time.Date(2026, 9, 5, 12, 20, 0, 0, time.UTC)
	result, err := h.host.ExecuteWork(t.Context(), h.host.RuntimeEpochID(), execution.ExecutionAuthorityRequestV2{
		Kind: execution.ExecutionRebindV2, OperationID: journalv2.OperationID("operation-rebind-" + boundary),
		ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID,
		RunID: state.RunID, ActorID: state.ActorID, ProcessID: state.ProcessID, LeaseID: state.LeaseID,
		Generation: state.Generation, Fence: state.Fence, ObservedAt: observedAt, Deadline: deadline,
		RuntimeEpochID: h.host.RuntimeEpochID(), Governance: governance,
		OperationEventID: journalv2.EventID("event-operation-rebind-" + boundary), SlotEventID: journalv2.EventID("event-slot-rebind-" + boundary),
	})
	if err != nil {
		t.Fatalf("rebind execution after %s: %v", boundary, err)
	}
	if result.State.RuntimeEpochID != h.host.RuntimeEpochID() || result.State.LeaseID != state.LeaseID || result.State.Generation != state.Generation || result.State.Fence != state.Fence || result.State.WorkspaceID != "" {
		t.Fatalf("execution rebind after %s changed authority: before=%+v after=%+v", boundary, state, result.State)
	}
	assertUniversalInvariants(t, h, state.ProjectID.String(), state.MissionID.String(), "rebound-"+boundary)
	return result.State
}

func restartBoundWorkspaceAfterDurableBoundary(t *testing.T, h *portableHarness, state execution.ExecutionSlotStateV2, workspaceState workspace.StateV2, recordRevision uint64, boundary string, enabled bool) (execution.ExecutionSlotStateV2, workspace.StateV2, uint64) {
	t.Helper()
	if !enabled {
		return state, workspaceState, recordRevision
	}
	restartAfterDurableBoundary(t, h, state.ProjectID.String(), state.MissionID.String(), boundary, true)
	return rebindBoundWorkspaceToCurrent(t, h, state, workspaceState, recordRevision, boundary)
}

func rebindBoundWorkspaceToCurrent(t *testing.T, h *portableHarness, state execution.ExecutionSlotStateV2, workspaceState workspace.StateV2, recordRevision uint64, boundary string) (execution.ExecutionSlotStateV2, workspace.StateV2, uint64) {
	t.Helper()
	observedAt := time.Date(2026, 9, 5, 12, 20, 0, 0, time.UTC)
	result, err := h.host.RebindWorkspaceRuntimeEpoch(t.Context(), h.host.RuntimeEpochID(), execution.WorkspaceEpochRebindRequestV2{
		ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID,
		ActorID: state.ActorID, ProcessID: state.ProcessID, OriginatingRuntimeEpochID: h.host.RuntimeEpochID(),
		OperationID:      journalv2.OperationID("operation-workspace-rebind-" + boundary),
		OperationEventID: journalv2.EventID("event-operation-workspace-rebind-" + boundary), ExecutionEventID: journalv2.EventID("event-execution-workspace-rebind-" + boundary),
		WorkspaceEventID: journalv2.EventID("event-workspace-rebind-" + boundary), Governance: state.Governance, ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatalf("rebind workspace after %s: %v", boundary, err)
	}
	if result.Execution.RuntimeEpochID != h.host.RuntimeEpochID() || result.Workspace.Binding.RuntimeEpochID != h.host.RuntimeEpochID() || result.Execution.LeaseID != state.LeaseID || result.Execution.Generation != state.Generation || result.Execution.Fence != state.Fence || result.Workspace.Binding.WorkspaceID != workspaceState.Binding.WorkspaceID {
		t.Fatalf("workspace rebind after %s changed authority: before_execution=%+v after=%+v", boundary, state, result)
	}
	assertUniversalInvariants(t, h, state.ProjectID.String(), state.MissionID.String(), "rebound-"+boundary)
	return result.Execution, result.Workspace, result.RecordRevision
}

func restartWorkAfterDurableBoundary(t *testing.T, h *portableHarness, facts *terminalWorkFacts, governance governancecontext.Context, boundary string, enabled bool) {
	t.Helper()
	if facts.workspace.Binding.WorkspaceID == "" {
		facts.execution = restartUnboundExecutionAfterDurableBoundary(t, h, facts.execution, governance, boundary, enabled)
		return
	}
	facts.execution, facts.workspace, facts.workspaceRecordRevision = restartBoundWorkspaceAfterDurableBoundary(t, h, facts.execution, facts.workspace, facts.workspaceRecordRevision, boundary, enabled)
	facts.binding = facts.workspace.Binding
}

func ensureWorkCurrent(t *testing.T, h *portableHarness, facts *terminalWorkFacts, governance governancecontext.Context, boundary string) {
	t.Helper()
	if facts.execution.RuntimeEpochID == h.host.RuntimeEpochID() {
		return
	}
	if facts.workspace.Binding.WorkspaceID == "" {
		facts.execution = rebindUnboundExecutionToCurrent(t, h, facts.execution, governance, boundary)
		return
	}
	facts.execution, facts.workspace, facts.workspaceRecordRevision = rebindBoundWorkspaceToCurrent(t, h, facts.execution, facts.workspace, facts.workspaceRecordRevision, boundary)
	facts.binding = facts.workspace.Binding
}

func cloneReadinessState(t *testing.T, prior mission.ReadinessAuthorityStateV2, version journalv2.LogicalVersion, lifecycle mission.MissionLifecycle, operation journalv2.OperationID, observedAt time.Time, facts []mission.ScopedMissionFactV2) mission.ReadinessAuthorityStateV2 {
	t.Helper()
	definitionDigest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"portable-scenario"}`))
	if err != nil {
		t.Fatal(err)
	}
	state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{ProjectID: prior.ProjectID(), MissionID: prior.MissionID(), DefinitionRevision: "definition-1", DefinitionDigest: definitionDigest, Version: version, Lifecycle: lifecycle, CurrentWorkRevision: prior.CurrentWorkRevision(), ActivityCount: prior.ActivityCount(), ActiveFacts: facts, LastOperationID: operation, ObservedAt: observedAt.Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func markExecutionCompleted(t *testing.T, h *portableHarness, state execution.ExecutionSlotStateV2, governance governancecontext.Context, index int) {
	t.Helper()
	observedAt := terminalTime(index, 50)
	result, err := h.host.ExecuteWork(t.Context(), h.host.RuntimeEpochID(), execution.ExecutionAuthorityRequestV2{Kind: execution.ExecutionOutcomeV2, OperationID: journalv2.OperationID("operation-outcome-" + state.WorkID.String()), ProjectID: state.ProjectID, MissionID: state.MissionID, WorkRevisionID: state.WorkRevisionID, WorkID: state.WorkID, RunID: state.RunID, ActorID: state.ActorID, ProcessID: state.ProcessID, LeaseID: state.LeaseID, Generation: state.Generation, Fence: state.Fence, ObservedAt: observedAt, Deadline: observedAt.Add(time.Minute), RuntimeEpochID: h.host.RuntimeEpochID(), Outcome: "completed", Governance: governance, OperationEventID: journalv2.EventID("event-operation-outcome-" + state.WorkID.String()), SlotEventID: journalv2.EventID("event-slot-outcome-" + state.WorkID.String())})
	if err != nil {
		t.Fatalf("complete execution %s: %v", state.WorkID, err)
	}
	if result.State.Status != execution.ExecutionCompletedV2 {
		t.Fatalf("execution %s status=%s", state.WorkID, result.State.Status)
	}
}

func completeMissionAuthority(t *testing.T, h *portableHarness, current mission.ReadinessAuthorityStateV2, revisionID ids.WorkRevisionID) {
	t.Helper()
	observedAt := terminalTime(2, 55)
	operation := journalv2.OperationID("operation-mission-complete")
	next := cloneReadinessState(t, current, current.Version()+1, mission.MissionCompleted, operation, observedAt, nil)
	result, err := h.host.ExecuteReadiness(t.Context(), h.host.RuntimeEpochID(), mission.ReadinessAuthorityCommandV2{SchemaVersion: mission.ReadinessAuthorityStateSchemaV2, OperationID: operation, ProjectID: current.ProjectID(), MissionID: current.MissionID(), ActorID: "actor-orchestrator", ProcessID: "process-orchestrator", OriginatingRuntimeEpochID: h.host.RuntimeEpochID(), DoctrineRevision: "doctrine-1", ProfileRevision: "profile-1", PolicyRevision: "policy-1", ObservedAt: observedAt.Format(time.RFC3339Nano), EventType: mission.ReadinessEventCompletedV2, Expected: current, Updated: next})
	if err != nil {
		t.Fatalf("complete mission revision %s: %v", revisionID, err)
	}
	if result.State().Lifecycle() != mission.MissionCompleted {
		t.Fatalf("mission did not complete: %s", result.State().Lifecycle())
	}
}

func terminalTime(index, second int) time.Time {
	return time.Date(2026, 9, 5, 12, 10+index, second, 0, time.UTC)
}
