package helpercmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation(t *testing.T) {
	sidecar := os.Getenv("SPEC_KITTY_WP15_GAPDBD")
	if sidecar == "" {
		t.Skip("set SPEC_KITTY_WP15_GAPDBD to the exact admitted assertion-extension sidecar")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp("", "spk-wp15-real-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "authority")
	factory := NewProductionFactory(Installation{GapDBExecutable: sidecar, EvidenceRoot: filepath.Join(repository, "docs", "programs", "evidence"), RuntimeRoot: filepath.Join(parent, "runtime")})
	session, err := factory.Start(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(t.Context()) })

	requests := realHostValidationRequests(t)
	seen := make(map[OperationKind]bool, len(requests))
	for _, request := range requests {
		t.Run(string(request.Kind), func(t *testing.T) {
			assertRecursivelySnakeCaseJSON(t, request.Payload)
			decoded, decodeErr := decodeOperation(request)
			if decodeErr != nil {
				t.Fatalf("snake-case request did not decode: %v\npayload=%s", decodeErr, request.Payload)
			}
			result, executeErr := session.Execute(t.Context(), decoded)
			seen[request.Kind] = true
			wantOutcome, wantDiagnostic, wantSuccess := realHostExpectedResult(request.Kind)
			if wantSuccess {
				if executeErr != nil {
					t.Fatalf("valid request did not produce its typed result: %v", executeErr)
				}
				if result.Kind != resultKindFor(request.Kind) || !json.Valid(result.Payload) {
					t.Fatalf("wrong typed result: %+v", result)
				}
				return
			}
			if executeErr == nil {
				t.Fatalf("request unexpectedly succeeded: %+v", result)
			}
			outcome, diagnostic := classifyError(executeErr)
			if outcome != wantOutcome || diagnostic.Kind != wantDiagnostic {
				t.Fatalf("wrong stable domain refusal: err=%v outcome=%s diagnostic=%+v want_outcome=%s want_kind=%s", executeErr, outcome, diagnostic, wantOutcome, wantDiagnostic)
			}
		})
	}
	for _, kind := range []OperationKind{OperationMission, OperationReadiness, OperationExecution, OperationWorkspaceRebind, OperationHandoff, OperationReview, OperationAcceptance, OperationIntegration, OperationIntegrationCleanup, OperationEffect} {
		if !seen[kind] {
			t.Fatalf("declared kind %s was not exercised", kind)
		}
	}
}

func assertRecursivelySnakeCaseJSON(t *testing.T, raw []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				for _, character := range key {
					if character != '_' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
						t.Fatalf("non-snake-case JSON key %q in %s", key, raw)
					}
				}
				visit(nested)
			}
		case []any:
			for _, nested := range typed {
				visit(nested)
			}
		}
	}
	visit(value)
}

func realHostExpectedResult(kind OperationKind) (Outcome, string, bool) {
	switch kind {
	case OperationMission, OperationReadiness:
		return OutcomeSuccess, "", true
	case OperationExecution, OperationWorkspaceRebind, OperationHandoff, OperationReview, OperationEffect:
		return OutcomeUnavailable, string(execution.KindStaleAuthority), false
	case OperationAcceptance, OperationIntegration, OperationIntegrationCleanup:
		return OutcomeUnavailable, string(integration.KindEvidenceUnavailable), false
	default:
		return OutcomeInvalid, "unknown_operation_kind", false
	}
}

func realHostValidationRequests(t *testing.T) []OperationRequest {
	t.Helper()
	governance, err := execution.ExecutionGovernanceFromContextV2(helperGovernanceContext(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	epoch := RuntimeEpochCurrent
	project, missionID, revisionID, workID := ids.ProjectID("project-real"), ids.MissionID("mission-real"), ids.WorkRevisionID("revision-real"), ids.WorkID("work-real")
	repositoryID := ids.RepositoryID("repository-real")
	claim, err := workspace.WorkspacePathClaimDigestV2(repositoryID, "/srv/spec-kitty", "/srv/spec-kitty/workspaces/real")
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.BindingV2{WorkspaceID: "workspace-real", ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, RunID: "run-real", LeaseID: "lease-real", Generation: 1, Fence: strings.Repeat("f", 32), RuntimeEpochID: epoch, ActorID: "actor-real", ProcessID: "process-real", RepositoryID: repositoryID, LaneID: "lane-real", ManagedRoot: "/srv/spec-kitty", CanonicalPath: "/srv/spec-kitty/workspaces/real", PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/real", PinnedBase: strings.Repeat("a", 40), Dependencies: []workspace.DependencyV2{}}
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	criteria := []string{"criterion-real"}
	criteriaDigest, err := handoff.CriterionSetDigestV2(criteria)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	sha := strings.Repeat("c", 40)

	missionRequest := missionCreateRequestFor(t, "mission-real-create", "operation-real-mission")
	setRequestEpoch(t, &missionRequest, epoch)
	readinessRequest := readinessCreateRequest(t)
	setRequestEpoch(t, &readinessRequest, epoch)
	executionRequest := executionClaimRequest(t, "work-real-execution", "operation-real-execution")
	setRequestEpoch(t, &executionRequest, epoch)

	rebind := workspaceRebindWire{ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, ActorID: "actor-real", ProcessID: "process-real", OriginatingRuntimeEpochID: epoch, OperationID: "operation-real-rebind", OperationEventID: "event-real-rebind-operation", ExecutionEventID: "event-real-rebind-execution", WorkspaceEventID: "event-real-rebind-workspace", Governance: governance, ObservedAt: now}
	handoffRequest := handoffWire{OperationID: "operation-real-handoff", HandoffID: "handoff-real", ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, RunID: binding.RunID, RuntimeEpochID: epoch, Workspace: binding, LeaseID: binding.LeaseID, LeaseGeneration: binding.Generation, Fence: binding.Fence, ActorID: binding.ActorID, ProcessID: binding.ProcessID, RepositoryID: repositoryID, RepositoryPath: "/srv/spec-kitty/repository", LaneID: binding.LaneID, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Candidate: sha, Contributors: []ids.ActorID{"actor-real"}, CriterionIDs: criteria, CriteriaDigest: criteriaDigest, WorkRevisionCriteriaDigest: digest, EvidenceDigest: digest, Governance: governance, ObservedAt: now}
	reviewRequest := reviewWire{OperationID: "operation-real-review", ReviewID: "review-real", HandoffID: "handoff-real", ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, RuntimeEpochID: epoch, Workspace: binding, ReviewerActorID: "actor-reviewer", ReviewerProcessID: "process-reviewer", Candidate: sha, HandoffContributorDigest: digest, WorkRevisionCriteriaDigest: digest, Criteria: []review.CriterionEvidenceV2{{CriterionID: criteria[0], Status: review.Passed, EvidenceDigest: digest, ArtifactDigest: digest, Candidate: sha, PolicyRevision: governance.PolicyRevision}}, Verdict: review.Approved, PolicyRevision: governance.PolicyRevision, Governance: governance, ObservedAt: now}
	acceptanceRequest := acceptanceWire{OperationID: "operation-real-acceptance", AcceptanceID: "acceptance-real", ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, HandoffID: "handoff-real", RepositoryID: repositoryID, Candidate: sha, ContributorDigest: digest, ReviewIDs: []ids.ReviewID{"review-real"}, Evidence: []integration.AcceptanceEvidenceV2{{CriterionID: criteria[0], Kind: "policy", Disposition: integration.EvidenceInapplicableV2, Producer: "policy-authority", PolicyBasis: "not applicable", EvidenceID: "evidence-real"}}, PolicyRevision: governance.PolicyRevision, PolicyFactID: "policy-fact-real", RuntimeEpochID: epoch, ActorID: "actor-acceptor", ProcessID: "process-acceptor", Governance: governance, ObservedAt: now}
	integrationRequest := integrationWire{OperationID: "operation-real-integration", AttemptID: "attempt-real", ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, AcceptanceID: "acceptance-real", RepositoryID: repositoryID, TargetRef: "refs/heads/main", Candidate: sha, RuntimeEpochID: epoch, ActorID: "actor-integrator", ProcessID: "process-integrator", Governance: governance, ObservedAt: now}
	cleanupRequest := cleanupWire{CleanupOperationID: "operation-real-cleanup", IntegrationOperationID: "operation-real-integration", ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, AttemptID: "attempt-real", RepositoryID: repositoryID, TargetRef: "refs/heads/main", RuntimeEpochID: epoch, ActorID: "actor-integrator", ProcessID: "process-integrator", Governance: governance, ObservedAt: now}
	slot := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: project, MissionID: missionID, WorkRevisionID: revisionID, WorkID: workID, LogicalVersion: 1, RunID: "run-real", ActorID: "actor-real", ProcessID: "process-real", LeaseID: "lease-real", Generation: 1, Fence: binding.Fence, HeartbeatAt: now.Format(time.RFC3339Nano), Deadline: now.Add(time.Hour).Format(time.RFC3339Nano), RuntimeEpochID: epoch, Status: execution.ExecutionActiveV2, Governance: governance, WorkspaceID: binding.WorkspaceID, WorkspacePathClaimDigest: binding.PathClaimDigest}
	target := externalWorkRefWire{ProjectID: project, WorkID: workID, Provider: "provider-real", ExternalID: "external-real", MetadataSchema: 1, CreatedByOperation: "operation-real-effect"}
	intent := execution.EffectIntentV2{EffectID: "effect-real", OperationID: "operation-real-effect", Target: target.value(), IntendedValue: "actor-real", ExpectedProviderRevision: 1, ExpectedValue: "", ExpectedOperationMarker: "", RequiredGuarantee: tracker.AssignmentAtomicClaim, ActorID: "actor-real", ProcessID: "process-real", Governance: governance, OriginRuntimeEpochID: epoch, OriginObservedCreatedAt: now.Format(time.RFC3339)}
	intent.CanonicalRequestDigest, err = execution.EffectRequestDigestV2(intent)
	if err != nil {
		t.Fatal(err)
	}
	effectRequest := effectWire{Action: execution.EffectRecordIntentV2, Intent: effectIntentWire{EffectID: intent.EffectID, OperationID: intent.OperationID, Target: target, IntendedValue: intent.IntendedValue, ExpectedProviderRevision: intent.ExpectedProviderRevision, ExpectedValue: intent.ExpectedValue, ExpectedOperationMarker: intent.ExpectedOperationMarker, RequiredGuarantee: intent.RequiredGuarantee, CanonicalRequestDigest: intent.CanonicalRequestDigest, ActorID: intent.ActorID, ProcessID: intent.ProcessID, Governance: intent.Governance, OriginRuntimeEpochID: intent.OriginRuntimeEpochID, OriginObservedCreatedAt: intent.OriginObservedCreatedAt}, RuntimeEpochID: epoch, ExecutionSlot: slot, ObservedAt: now, OperationEventID: "event-real-effect-operation", EffectEventID: "event-real-effect"}

	return []OperationRequest{
		missionRequest, readinessRequest, executionRequest,
		wireRequest(OperationWorkspaceRebind, string(mission.ActionWorkspaceAllocate), "work:"+workID.String(), rebind),
		wireRequest(OperationHandoff, string(mission.ActionCandidateHandoff), "work:"+workID.String(), handoffRequest),
		wireRequest(OperationReview, string(mission.ActionReviewRecord), "work:"+workID.String(), reviewRequest),
		wireRequest(OperationAcceptance, string(mission.ActionAcceptanceEvaluate), "mission:"+missionID.String(), acceptanceRequest),
		wireRequest(OperationIntegration, string(mission.ActionIntegrationPublish), "mission:"+missionID.String(), integrationRequest),
		wireRequest(OperationIntegrationCleanup, string(mission.ActionIntegrationPublish), "mission:"+missionID.String(), cleanupRequest),
		wireRequest(OperationEffect, string(mission.ActionEffectDispatch), "action:"+string(mission.ActionEffectDispatch), effectRequest),
	}
}

func wireRequest(kind OperationKind, action, scope string, payload any) OperationRequest {
	raw, _ := json.Marshal(payload)
	return OperationRequest{SchemaVersion: 1, Kind: kind, Action: action, Scope: scope, Payload: raw}
}

func setRequestEpoch(t *testing.T, request *OperationRequest, epoch journalv2.RuntimeEpochID) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(request.Payload, &value); err != nil {
		t.Fatal(err)
	}
	value["runtime_epoch_id"] = string(epoch)
	request.Payload, _ = json.Marshal(value)
}
