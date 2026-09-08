package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type reviewEpochV2 journalv2.RuntimeEpochID

func (e reviewEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return journalv2.RuntimeEpochID(e), nil
}

type reviewPortV2 struct {
	resolution     OperationResolutionV2
	review         ObservationV2
	authority      AuthorityObservationV2
	plans          []journalv2.SealedCommitPlanV2
	revision       uint64
	authorityReads int
	dropAckAt      int
	publishes      int
}

func (p *reviewPortV2) ResolveReviewOperation(context.Context, journalv2.OperationBindingV2) (OperationResolutionV2, error) {
	return p.resolution, nil
}
func (p *reviewPortV2) ObserveReview(context.Context, journalv2.AggregateCoordinate) (ObservationV2, error) {
	return p.review, nil
}
func (p *reviewPortV2) ObserveReviewAuthority(context.Context, AuthorityRequestV2) (AuthorityObservationV2, error) {
	p.authorityReads++
	return p.authority, nil
}
func (p *reviewPortV2) PublishReview(_ context.Context, plan AuthorityPlanV2) (AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return AuthorityAckV2{}, err
	}
	p.revision++
	p.publishes++
	p.plans = append(p.plans, sealed)
	p.resolution = OperationResolutionV2{Kind: OperationExactV2, State: sealed.OperationState(), RecordRevision: p.revision}
	for _, event := range sealed.Events() {
		if event.Family() == "review_slot" {
			var prior []byte
			if p.review.State != nil {
				prior = p.review.State.CanonicalBytes()
			}
			next, reduceErr := ReduceEventV2(prior, event)
			if reduceErr != nil {
				return AuthorityAckV2{}, reduceErr
			}
			state, decodeErr := DecodeStateV2(next)
			if decodeErr != nil {
				return AuthorityAckV2{}, decodeErr
			}
			p.review = ObservationV2{State: &state, RecordRevision: p.revision, LastPublicationID: sealed.PublicationID()}
		}
	}
	if p.publishes == p.dropAckAt {
		return AuthorityAckV2{}, fmt.Errorf("simulated lost acknowledgement")
	}
	return AuthorityAckV2{sealed.PublicationID(), p.revision, sealed.Digest(), sealed.MutationCount()}, nil
}
func (p *reviewPortV2) ReconcileReview(_ context.Context, plan AuthorityPlanV2) (AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || len(p.plans) == 0 || p.plans[len(p.plans)-1].PublicationID() != sealed.PublicationID() {
		return AuthorityAckV2{}, fmt.Errorf("incomplete publication")
	}
	return AuthorityAckV2{sealed.PublicationID(), p.revision, sealed.Digest(), sealed.MutationCount()}, nil
}

func TestReviewAuthorityStagesEveryCriterionThenOneTerminalVerdictAndReplaysV2(t *testing.T) {
	request, authority := reviewRequestFixtureV2(t, false)
	port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 70}
	service, err := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Submit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Stage != StageVerdictRecordedV2 || result.State.Verdict != Approved || len(result.State.Criteria) != 2 || len(port.plans) != 4 {
		t.Fatalf("result=%+v plans=%d", result, len(port.plans))
	}
	for i, plan := range port.plans {
		if len(plan.Events()) != 2 || len(plan.Heads()) != 2 || len(plan.EligibilityGuards()) != 1 || len(plan.Assertions()) != 4 {
			t.Fatalf("plan[%d] e=%d h=%d g=%d a=%d", i, len(plan.Events()), len(plan.Heads()), len(plan.EligibilityGuards()), len(plan.Assertions()))
		}
	}
	reads := port.authorityReads
	port.authority = AuthorityObservationV2{}
	replay, err := service.Submit(t.Context(), request)
	if err != nil || !replay.Replayed || port.authorityReads != reads || len(port.plans) != 4 {
		t.Fatalf("replay=%+v err=%v reads=%d/%d plans=%d", replay, err, reads, port.authorityReads, len(port.plans))
	}
}

func TestReviewerIndependenceUsesActorIDNotProcessAndNeedsExactNarrowExceptionV2(t *testing.T) {
	request, authority := reviewRequestFixtureV2(t, true)
	port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority}
	service, _ := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !IsKind(err, KindReviewerNotIndependent) || len(port.plans) != 0 {
		t.Fatalf("same actor accepted: %v", err)
	}
	exception := IndependenceExceptionV2{SchemaVersion: 1, ExceptionID: "exception-1", PolicyRevision: request.PolicyRevision, ProjectID: request.ProjectID, MissionID: request.MissionID, WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID, HandoffID: request.HandoffID, Candidate: request.Candidate, ReviewerActorID: request.ReviewerActorID, Reason: "single-maintainer emergency review", AuthorizerActorID: "actor-authorizer", EffectiveFrom: request.ObservedAt.Add(-time.Minute).Format(time.RFC3339Nano), EffectiveThrough: request.ObservedAt.Add(time.Hour).Format(time.RFC3339Nano)}
	request.Exception = &exception
	exceptionCoordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+exception.ExceptionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID)})
	fact := MissionPolicyFactObservationV2{ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), FactID: exception.ExceptionID, Kind: "decision", Status: "resolved", LogicalVersion: 2, DetailDigest: exception.Digest(), RecordRevision: 66, Found: true}
	authority.Exception = ExceptionObservationV2{Fact: fact, Assertion: reviewAssertionV2(t, exceptionCoordinate, 66, 2, exception.CanonicalBytes())}
	port.authority = authority
	result, err := service.Submit(t.Context(), request)
	if err != nil || result.State.Exception == nil || len(port.plans) != 4 {
		t.Fatalf("exception result=%+v err=%v plans=%d", result, err, len(port.plans))
	}
	for _, plan := range port.plans {
		if len(plan.Assertions()) != 5 {
			t.Fatalf("policy assertion missing: %d", len(plan.Assertions()))
		}
	}
}

func TestReviewerExceptionRequiresCurrentExactMissionDecisionHeadV2(t *testing.T) {
	request, authority := reviewRequestFixtureV2(t, true)
	exception := IndependenceExceptionV2{SchemaVersion: 1, ExceptionID: "exception-current", PolicyRevision: request.PolicyRevision, ProjectID: request.ProjectID, MissionID: request.MissionID, WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID, HandoffID: request.HandoffID, Candidate: request.Candidate, ReviewerActorID: request.ReviewerActorID, Reason: "explicit current mission policy exception", AuthorizerActorID: "actor-authorizer", EffectiveFrom: request.ObservedAt.Add(-time.Minute).Format(time.RFC3339Nano), EffectiveThrough: request.ObservedAt.Add(time.Hour).Format(time.RFC3339Nano)}
	request.Exception = &exception
	coordinate, _ := journalv2.NewAggregateCoordinate("mission", journalv2.AggregateID("decision:"+exception.ExceptionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID)})

	valid := MissionPolicyFactObservationV2{ProjectID: journalv2.ProjectID(request.ProjectID), MissionID: journalv2.MissionID(request.MissionID), FactID: exception.ExceptionID, Kind: "decision", Status: "resolved", LogicalVersion: 2, DetailDigest: exception.Digest(), RecordRevision: 66, Found: true}
	validObservation := ExceptionObservationV2{Fact: valid, Assertion: reviewAssertionV2(t, coordinate, 66, 2, exception.CanonicalBytes())}

	tests := map[string]struct {
		kind   ErrorKind
		mutate func(ExceptionObservationV2) ExceptionObservationV2
	}{
		"stale head revision": {KindStaleAuthority, func(observation ExceptionObservationV2) ExceptionObservationV2 {
			observation.Fact.RecordRevision++
			return observation
		}},
		"substituted decision detail": {KindReviewerNotIndependent, func(observation ExceptionObservationV2) ExceptionObservationV2 {
			wrongDigest, _ := journalv2.ParseDigest("detail", strings.Repeat("9", 64))
			observation.Fact.DetailDigest = wrongDigest
			observation.Assertion = reviewAssertionV2(t, coordinate, 66, 2, []byte(`{"mutant":"wrong-detail"}`))
			return observation
		}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			observed := authority
			observed.Exception = test.mutate(validObservation)
			port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: observed}
			service, _ := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
			if _, submitErr := service.Submit(t.Context(), request); !IsKind(submitErr, test.kind) || len(port.plans) != 0 {
				t.Fatalf("non-current exception accepted: err=%v plans=%d", submitErr, len(port.plans))
			}
		})
	}
}

func TestReviewRejectsPartialCriterionSetChangedVerdictAndReducerRewriteV2(t *testing.T) {
	request, authority := reviewRequestFixtureV2(t, false)
	request.Criteria = request.Criteria[:1]
	request.Verdict = Approved
	port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority}
	service, _ := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !IsKind(err, KindRevisionIneligible) || len(port.plans) != 0 {
		t.Fatalf("partial immutable criterion set accepted: %v", err)
	}
	request.Criteria = append(request.Criteria, CriterionEvidenceV2{CriterionID: "criterion-1", Status: Passed, EvidenceDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64), Candidate: request.Candidate, PolicyRevision: request.PolicyRevision})
	if _, _, err := prepareReviewRequestV2(request); !IsKind(err, KindInvalid) {
		t.Fatalf("duplicate criterion accepted: %v", err)
	}
	request, _ = reviewRequestFixtureV2(t, false)
	request.Verdict = ChangesRequested
	if _, _, err := prepareReviewRequestV2(request); !IsKind(err, KindInvalid) {
		t.Fatalf("changed verdict accepted: %v", err)
	}
	_, seed, err := prepareReviewRequestV2(func() AuthorityRequestV2 { r, _ := reviewRequestFixtureV2(t, false); return r }())
	if err != nil {
		t.Fatal(err)
	}
	next := seed
	next.LogicalVersion = 2
	next.Stage = StageCriterionRecordedV2
	next.Candidate = strings.Repeat("9", 40)
	next.Criteria = []CriterionEvidenceV2{{CriterionID: "criterion-1", Status: Passed, EvidenceDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64), Candidate: next.Candidate, PolicyRevision: next.PolicyRevision}}
	payload, _ := encodeReviewEventV2(EventV2{1, string(next.Stage), digestReviewStateV2(seed), next})
	envelope := reviewEnvelopeV2(t, next, payload, "operation-review-1")
	if _, err := ReduceEventV2(seed.CanonicalBytes(), envelope); !IsKind(err, KindConflict) {
		t.Fatalf("candidate rewrite accepted: %v", err)
	}
}

func TestReviewRejectsSubstitutedWorkRevisionAssertionV2(t *testing.T) {
	request, authority := reviewRequestFixtureV2(t, false)
	assertion := authority.WorkRevision.Assertion
	digest, _ := journalv2.ParseDigest("mutant", strings.Repeat("9", 64))
	mutated, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: assertion.Target(), Kind: assertion.Kind(), ObservedRevision: assertion.ObservedRevision(), ExpectedLogicalVersion: assertion.ExpectedLogicalVersion(), ObservedValueDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	authority.WorkRevision.Assertion = mutated
	port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority}
	service, _ := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !IsKind(err, KindStaleAuthority) || len(port.plans) != 0 {
		t.Fatalf("substituted WorkRevision assertion accepted: %v", err)
	}
}

func TestReviewDroppedAcknowledgementAtEveryStageResumesSameOperationV2(t *testing.T) {
	for drop := 1; drop <= 4; drop++ {
		t.Run(fmt.Sprintf("stage_%d", drop), func(t *testing.T) {
			request, authority := reviewRequestFixtureV2(t, false)
			port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 70, dropAckAt: drop}
			service, err := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Submit(t.Context(), request)
			if err != nil || result.State.Stage != StageVerdictRecordedV2 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if len(port.plans) != 4 || port.publishes != 4 {
				t.Fatalf("duplicate publication: plans=%d publishes=%d", len(port.plans), port.publishes)
			}
		})
	}
}

func TestReviewCanonicalCriterionReorderProducesIdenticalAuthorityV2(t *testing.T) {
	request, authority := reviewRequestFixtureV2(t, false)
	reordered := request
	reordered.Criteria = []CriterionEvidenceV2{request.Criteria[1], request.Criteria[0]}
	leftBinding, leftState, err := prepareReviewRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	rightBinding, rightState, err := prepareReviewRequestV2(reordered)
	if err != nil || leftBinding.IntentDigest() != rightBinding.IntentDigest() || !bytes.Equal(leftState.CanonicalBytes(), rightState.CanonicalBytes()) {
		t.Fatalf("canonical reorder diverged: err=%v", err)
	}
	port := &reviewPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 70}
	service, _ := NewAuthorityServiceV2(port, reviewEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	result, err := service.Submit(t.Context(), reordered)
	if err != nil || result.State.Stage != StageVerdictRecordedV2 {
		t.Fatalf("reordered review=%+v err=%v", result, err)
	}
}

func reviewRequestFixtureV2(t *testing.T, sameActor bool) (AuthorityRequestV2, AuthorityObservationV2) {
	t.Helper()
	gov := reviewGovernanceV2(t)
	eligibility, revision := reviewEligibilityV2(t)
	revisionID := revision.ID()
	now := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	claim, _ := workspace.WorkspacePathClaimDigestV2("repository-1", "/tmp/spec-kitty", "/tmp/spec-kitty/workspace-1")
	binding := workspace.BindingV2{WorkspaceID: "workspace-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: revisionID, WorkID: "work-1", RunID: "run-1", LeaseID: "lease-1", Generation: 1, Fence: "0123456789abcdef", RuntimeEpochID: "epoch-1", ActorID: "actor-implementer", ProcessID: "process-implementer", RepositoryID: "repository-1", LaneID: "lane-1", ManagedRoot: "/tmp/spec-kitty", CanonicalPath: "/tmp/spec-kitty/workspace-1", PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/work-1", PinnedBase: strings.Repeat("a", 40)}
	workspaceState := workspace.StateV2{SchemaVersion: 1, LogicalVersion: 7, Binding: binding, Stage: workspace.StageReadyV2, Observation: workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, DependencyHead: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "git-dir", IndexIdentity: "index", Clean: true, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}, OccurredAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}
	executionState := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: binding.ProjectID, MissionID: binding.MissionID, WorkRevisionID: binding.WorkRevisionID, WorkID: binding.WorkID, LogicalVersion: 4, RunID: binding.RunID, ActorID: binding.ActorID, ProcessID: binding.ProcessID, LeaseID: binding.LeaseID, Generation: binding.Generation, Fence: binding.Fence, HeartbeatAt: now.Add(-time.Minute).Format(time.RFC3339Nano), Deadline: now.Add(time.Hour).Format(time.RFC3339Nano), RuntimeEpochID: binding.RuntimeEpochID, Status: execution.ExecutionActiveV2, Governance: gov, WorkspaceID: binding.WorkspaceID, WorkspacePathClaimDigest: binding.PathClaimDigest}
	contributors := []ids.ActorID{"actor-implementer", "actor-other"}
	reviewer := ids.ActorID("actor-reviewer")
	if sameActor {
		reviewer = "actor-implementer"
	}
	criterionIDs := []string{"criterion-1", "criterion-2"}
	criteriaDigest, _ := handoff.CriterionSetDigestV2(criterionIDs)
	handoffState := handoff.StateV2{SchemaVersion: 1, LogicalVersion: 3, Stage: handoff.StageCandidateRecordedV2, HandoffID: "handoff-1", ProjectID: binding.ProjectID, MissionID: binding.MissionID, WorkRevisionID: binding.WorkRevisionID, WorkID: binding.WorkID, RunID: binding.RunID, RuntimeEpochID: binding.RuntimeEpochID, WorkspaceID: binding.WorkspaceID, LeaseID: binding.LeaseID, LeaseGeneration: binding.Generation, Fence: binding.Fence, ActorID: binding.ActorID, ProcessID: binding.ProcessID, RepositoryID: binding.RepositoryID, LaneID: binding.LaneID, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Candidate: strings.Repeat("b", 40), Contributors: contributors, ContributorDigest: reviewContributorDigestV2(contributors), CriterionIDs: criterionIDs, CriteriaDigest: criteriaDigest, WorkRevisionCriteriaDigest: revision.CriteriaDigest(), EvidenceDigest: strings.Repeat("d", 64), RepositoryEvidence: handoff.CandidateEvidence{Candidate: strings.Repeat("b", 40), PinnedBase: binding.PinnedBase, OwnedBranch: binding.OwnedBranch, ObservedBranchHead: strings.Repeat("b", 40)}, Governance: gov, ObservedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}
	request := AuthorityRequestV2{OperationID: "operation-review-1", ReviewID: "review-1", HandoffID: handoffState.HandoffID, ProjectID: binding.ProjectID, MissionID: binding.MissionID, WorkRevisionID: binding.WorkRevisionID, WorkID: binding.WorkID, RuntimeEpochID: binding.RuntimeEpochID, Workspace: binding, ReviewerActorID: reviewer, ReviewerProcessID: "process-reviewer", Candidate: handoffState.Candidate, HandoffContributorDigest: handoffState.ContributorDigest, WorkRevisionCriteriaDigest: revision.CriteriaDigest(), Criteria: []CriterionEvidenceV2{{CriterionID: "criterion-1", Status: Passed, EvidenceDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64), Candidate: handoffState.Candidate, PolicyRevision: gov.PolicyRevision}, {CriterionID: "criterion-2", Status: Passed, EvidenceDigest: strings.Repeat("1", 64), ArtifactDigest: strings.Repeat("2", 64), Candidate: handoffState.Candidate, PolicyRevision: gov.PolicyRevision}}, Verdict: Approved, PolicyRevision: gov.PolicyRevision, Governance: gov, ObservedAt: now}
	execCoordinate, _ := execution.ExecutionSlotCoordinateV2(binding.ProjectID, binding.MissionID, binding.WorkRevisionID, binding.WorkID)
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(binding)
	handoffCoordinate, _ := handoff.CoordinateV2(binding.ProjectID, binding.MissionID, binding.WorkRevisionID, binding.WorkID)
	workgraphCoordinate, _ := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(binding.MissionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(binding.ProjectID), MissionID: journalv2.MissionID(binding.MissionID)})
	revisionBytes, _ := revision.Encode()
	revisionAssertion := reviewAssertionV2(t, workgraphCoordinate, 24, 1, revisionBytes)
	assertions := []journalv2.AuthorityAssertionV2{reviewAssertionV2(t, execCoordinate, 21, journalv2.LogicalVersion(executionState.LogicalVersion), executionState.CanonicalBytes()), reviewAssertionV2(t, workspaceCoordinate, 22, journalv2.LogicalVersion(workspaceState.LogicalVersion), workspaceState.CanonicalBytes()), revisionAssertion, reviewAssertionV2(t, handoffCoordinate, 23, journalv2.LogicalVersion(handoffState.LogicalVersion), handoffState.CanonicalBytes())}
	return request, AuthorityObservationV2{Eligibility: eligibility, Execution: execution.ExecutionSlotObservationV2{State: &executionState, RecordRevision: 21}, Workspace: execution.WorkspaceAuthorityObservationV2{State: &workspaceState, RecordRevision: 22}, Handoff: handoff.ObservationV2{State: &handoffState, RecordRevision: 23}, WorkRevision: handoff.WorkRevisionObservationV2{Revision: revision, RecordRevision: 24, Assertion: revisionAssertion}, Assertions: assertions}
}

func reviewGovernanceV2(t *testing.T) execution.ExecutionGovernanceV2 {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind          governancecontext.EntryKind
		ref, revision string
	}{{governancecontext.KindConfig, "config", "policy-1"}, {governancecontext.KindDoctrine, "doctrine", "doctrine-1"}, {governancecontext.KindProfile, "profile", "profile-1"}, {governancecontext.KindMissionType, "mission", "mission-1"}} {
		c, _ := governancecontext.CommitSHA256([]byte(item.ref))
		e, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, c)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	ctx, err := governancecontext.NewContext("governance-1", 1, entries, time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	g, err := execution.ExecutionGovernanceFromContextV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func reviewEligibilityV2(t *testing.T) (workgraph.ObservedEligibilityShardV2, workgraph.RevisionV2) {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-1", WorkID: "work-1", Title: "Work", Description: "Deliver reviewed work.", DesignContext: "Use review authority.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-1", Text: "one", Outcome: "pass", Verifier: "test", Requirement: workgraph.CriterionRequired}, {ID: "criterion-2", Text: "two", Outcome: "pass", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/review", Domain: "review", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-1", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-1", "mission-1", "artifact-plan", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-1", Kind: workgraph.ScopePlanConcern, MissionID: "mission-1"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("work-1"))
	final, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-1", Provider: "fake", ExternalID: "external", Revision: "provider-1", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sum), CriterionEvidenceHints: map[string]string{"criterion-1": "test", "criterion-2": "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: final, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor", FinalizedAt: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), GovernanceContextID: "governance-1"})
	if err != nil {
		t.Fatal(err)
	}
	shards, err := workgraph.BuildInitialEligibilityGuardShards(revision, "operation-workgraph", []workgraph.EligibilityOperation{workgraph.EligibilityStart, workgraph.EligibilityHandoff, workgraph.EligibilityReview, workgraph.EligibilityAcceptance, workgraph.EligibilityIntegration})
	if err != nil {
		t.Fatal(err)
	}
	for _, shard := range shards {
		b, _ := shard.CanonicalBytes()
		o, e := workgraph.NewObservedEligibilityShardV2(b, 17)
		if e == nil {
			if _, e = o.Token("work-1", revision.ID(), workgraph.EligibilityReview); e == nil {
				return o, revision
			}
		}
	}
	t.Fatal("review eligibility absent")
	return workgraph.ObservedEligibilityShardV2{}, workgraph.RevisionV2{}
}
func reviewAssertionV2(t *testing.T, c journalv2.AggregateCoordinate, revision uint64, logical journalv2.LogicalVersion, value []byte) journalv2.AuthorityAssertionV2 {
	t.Helper()
	d, _ := journalv2.ReducerStateDigestV2(value)
	a, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: c}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: revision, ExpectedLogicalVersion: logical, ObservedValueDigest: d})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func reviewContributorDigestV2(values []ids.ActorID) string {
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty/handoff-contributors/v1\x00"))
	for _, v := range values {
		fmt.Fprintf(h, "%d:%s", len(v), v)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}
func digestReviewStateV2(s StateV2) string {
	d, _ := journalv2.ReducerStateDigestV2(s.CanonicalBytes())
	return d.String()
}
func reviewEnvelopeV2(t *testing.T, s StateV2, payload []byte, op journalv2.OperationID) journalv2.EventEnvelopeV2 {
	t.Helper()
	c, _ := CoordinateV2(s.ProjectID, s.MissionID, s.WorkRevisionID, s.WorkID, s.ReviewID)
	e, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: "event-review-mutant", EventIndex: 0, EventCount: 1, EventType: "review_slot." + string(s.Stage), Family: "review_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: c, AggregateLogicalVersion: journalv2.LogicalVersion(s.LogicalVersion), OperationID: op, OperationLogicalVersion: journalv2.LogicalVersion(s.LogicalVersion), PublicationID: journalv2.PublicationID(strings.Repeat("a", 64)), CommittedRuntimeEpochID: s.RuntimeEpochID, ActorID: journalv2.ActorID(s.ReviewerActorID), ProcessID: journalv2.ProcessID(s.ReviewerProcessID), WorkRevisionID: journalv2.WorkRevisionID(s.WorkRevisionID), PolicyRevision: s.PolicyRevision, Payload: payload}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
