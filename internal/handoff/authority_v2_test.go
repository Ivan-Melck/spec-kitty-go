package handoff

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
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type handoffEpochV2 journalv2.RuntimeEpochID

func (e handoffEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return journalv2.RuntimeEpochID(e), nil
}

type handoffVerifierV2 struct{ calls int }

func (v *handoffVerifierV2) Verify(_ context.Context, scope AuthorityScope, candidate string) (CandidateEvidence, error) {
	v.calls++
	return CandidateEvidence{Candidate: candidate, PinnedBase: scope.PinnedBase, OwnedBranch: scope.OwnedBranch, ObservedBranchHead: candidate}, nil
}
func (*handoffVerifierV2) RevalidateRefusal(context.Context, Refusal) error { return nil }

type refusingHandoffVerifierV2 struct{ calls int }

func (v *refusingHandoffVerifierV2) Verify(_ context.Context, scope AuthorityScope, candidate string) (CandidateEvidence, error) {
	v.calls++
	return CandidateEvidence{}, &Error{Kind: KindCandidateMismatch, Message: strings.Repeat("secret", 4096), Observed: map[string]string{"candidate": candidate, "branch_head": strings.Repeat("9", 40), "divergence_kind": BranchMovementDivergence, "evidence_ref_root": "refs/spec-kitty/handoff-evidence/root", "evidence_candidate_ref": "refs/spec-kitty/handoff-evidence/root/candidate", "evidence_observed_ref": "refs/spec-kitty/handoff-evidence/root/observed"}, Expected: map[string]string{"branch_head": candidate}, Cause: fmt.Errorf("private repository path /secret/repo")}
}
func (*refusingHandoffVerifierV2) RevalidateRefusal(context.Context, Refusal) error { return nil }

type handoffPortV2 struct {
	resolution OperationResolutionV2
	handoff    ObservationV2
	authority  AuthorityObservationV2
	plans      []journalv2.SealedCommitPlanV2
	revision   uint64
	dropAckAt  int
	publishes  int
}

func (p *handoffPortV2) ResolveHandoffOperation(context.Context, journalv2.OperationBindingV2) (OperationResolutionV2, error) {
	return p.resolution, nil
}
func (p *handoffPortV2) ObserveHandoff(context.Context, journalv2.AggregateCoordinate) (ObservationV2, error) {
	return p.handoff, nil
}
func (p *handoffPortV2) ObserveHandoffAuthority(context.Context, AuthorityRequestV2) (AuthorityObservationV2, error) {
	return p.authority, nil
}
func (p *handoffPortV2) PublishHandoff(_ context.Context, plan AuthorityPlanV2) (AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return AuthorityAckV2{}, err
	}
	p.revision++
	p.publishes++
	p.plans = append(p.plans, sealed)
	p.resolution = OperationResolutionV2{Kind: OperationExactV2, State: sealed.OperationState(), RecordRevision: p.revision}
	for _, event := range sealed.Events() {
		if event.Family() == "handoff_slot" {
			stateBytes, reduceErr := ReduceEventV2(func() []byte {
				if p.handoff.State != nil {
					return p.handoff.State.CanonicalBytes()
				}
				return nil
			}(), event)
			if reduceErr != nil {
				return AuthorityAckV2{}, reduceErr
			}
			state, decodeErr := DecodeStateV2(stateBytes)
			if decodeErr != nil {
				return AuthorityAckV2{}, decodeErr
			}
			p.handoff = ObservationV2{State: &state, RecordRevision: p.revision, LastPublicationID: sealed.PublicationID()}
		}
	}
	if p.publishes == p.dropAckAt {
		return AuthorityAckV2{}, fmt.Errorf("simulated lost acknowledgement")
	}
	return AuthorityAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: p.revision, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
}
func (p *handoffPortV2) ReconcileHandoff(_ context.Context, plan AuthorityPlanV2) (AuthorityAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || len(p.plans) == 0 || p.plans[len(p.plans)-1].PublicationID() != sealed.PublicationID() {
		return AuthorityAckV2{}, fmt.Errorf("incomplete publication")
	}
	return AuthorityAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: p.revision, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
}

func TestHandoffAuthorityStagesImmutableCandidateAndReplaysBeforeInspectionV2(t *testing.T) {
	request, authority := handoffRequestFixtureV2(t)
	port := &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 40}
	verifier := &handoffVerifierV2{}
	service, err := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), verifier, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Submit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Stage != StageCandidateRecordedV2 || len(port.plans) != 3 || verifier.calls != 1 {
		t.Fatalf("result=%+v plans=%d verifier=%d", result, len(port.plans), verifier.calls)
	}
	for i, plan := range port.plans {
		if len(plan.Events()) != 2 || len(plan.Heads()) != 2 || len(plan.Conditions()) != 3 || len(plan.Assertions()) != 3 || len(plan.EligibilityGuards()) != 1 {
			t.Fatalf("plan[%d] membership e=%d h=%d c=%d a=%d g=%d", i, len(plan.Events()), len(plan.Heads()), len(plan.Conditions()), len(plan.Assertions()), len(plan.EligibilityGuards()))
		}
	}
	port.authority = AuthorityObservationV2{}
	replayed, err := service.Submit(t.Context(), request)
	if err != nil || !replayed.Replayed || replayed.State.Candidate != request.Candidate || len(port.plans) != 3 || verifier.calls != 1 {
		t.Fatalf("replay=%+v err=%v plans=%d verifier=%d", replayed, err, len(port.plans), verifier.calls)
	}
}

func TestHandoffReducerRejectsCandidateMutationAndContributorSubstitutionV2(t *testing.T) {
	request, _ := handoffRequestFixtureV2(t)
	_, state, err := prepareRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	mutated := state
	mutated.Candidate = strings.Repeat("d", 40)
	mutated.LogicalVersion = 2
	mutated.Stage = StageRepositoryObservedV2
	mutated.RepositoryEvidence = CandidateEvidence{Candidate: mutated.Candidate, PinnedBase: mutated.PinnedBase, OwnedBranch: mutated.OwnedBranch, ObservedBranchHead: mutated.Candidate}
	payload, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: string(mutated.Stage), PriorDigest: digestStateV2(state), State: mutated})
	if err != nil {
		t.Fatal(err)
	}
	event := handoffEnvelopeV2(t, request, mutated, payload)
	if _, err := ReduceEventV2(state.CanonicalBytes(), event); !IsKind(err, KindConflict) {
		t.Fatalf("candidate mutation error=%v", err)
	}

	bad := state
	bad.Contributors = []ids.ActorID{"actor-other"}
	bad.ContributorDigest = contributorDigestV2(bad.Contributors)
	if !IsKind(bad.Validate(), KindInvalid) {
		t.Fatal("submitting actor omission accepted")
	}
	partial := state
	partial.LogicalVersion = 2
	partial.Stage = StageRepositoryObservedV2
	partial.Contributors = []ids.ActorID{state.ActorID}
	partial.ContributorDigest = contributorDigestV2(partial.Contributors)
	partial.RepositoryEvidence = CandidateEvidence{Candidate: partial.Candidate, PinnedBase: partial.PinnedBase, OwnedBranch: partial.OwnedBranch, ObservedBranchHead: partial.Candidate}
	partialPayload, err := encodeEventV2(EventV2{SchemaVersion: 1, Kind: string(partial.Stage), PriorDigest: digestStateV2(state), State: partial})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReduceEventV2(state.CanonicalBytes(), handoffEnvelopeV2(t, request, partial, partialPayload)); !IsKind(err, KindConflict) {
		t.Fatalf("partial contributor set accepted across transition: %v", err)
	}
}

func TestHandoffAuthorityRejectsPredecessorWorkspaceAndStaleFenceBeforeGitV2(t *testing.T) {
	request, authority := handoffRequestFixtureV2(t)
	for name, mutate := range map[string]func(*AuthorityObservationV2){
		"stale_fence": func(o *AuthorityObservationV2) { o.Execution.State.Fence = "ffffffffffffffff" },
		"expired_lease": func(o *AuthorityObservationV2) {
			o.Execution.State.Deadline = request.ObservedAt.Format(time.RFC3339Nano)
		},
		"predecessor_workspace": func(o *AuthorityObservationV2) {
			o.Workspace.State.Binding.WorkspaceID = "workspace-predecessor"
		},
		"stale_eligibility": func(o *AuthorityObservationV2) { o.Eligibility = workgraph.ObservedEligibilityShardV2{} },
	} {
		t.Run(name, func(t *testing.T) {
			mutant := authority
			executionCopy, workspaceCopy := *authority.Execution.State, *authority.Workspace.State
			mutant.Execution.State, mutant.Workspace.State = &executionCopy, &workspaceCopy
			mutate(&mutant)
			port := &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: mutant, revision: 7}
			verifier := &handoffVerifierV2{}
			service, _ := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), verifier, journalv2.DefaultEventRegistryV2())
			if _, err := service.Submit(t.Context(), request); err == nil || len(port.plans) != 0 || verifier.calls != 0 {
				t.Fatalf("mutant accepted: error=%v plans=%d verifier=%d", err, len(port.plans), verifier.calls)
			}
		})
	}
}

type optimisticHandoffPortV2 struct{ handoffPortV2 }

func (p *optimisticHandoffPortV2) PublishHandoff(_ context.Context, plan AuthorityPlanV2) (AuthorityAckV2, error) {
	sealed, _ := plan.Publication()
	return AuthorityAckV2{PublicationID: sealed.PublicationID(), ProviderRevision: 0, PlanDigest: sealed.Digest(), MutationCount: sealed.MutationCount()}, nil
}

func TestHandoffRejectsOptimisticResultBeforeDurableAcknowledgementV2(t *testing.T) {
	request, authority := handoffRequestFixtureV2(t)
	port := &optimisticHandoffPortV2{handoffPortV2: handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority}}
	service, _ := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), &handoffVerifierV2{}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !IsKind(err, KindConflict) {
		t.Fatalf("optimistic zero-revision acknowledgement accepted: %v", err)
	}
}

func TestHandoffDroppedAcknowledgementAtEveryStageRecoversWithoutDuplicateGitV2(t *testing.T) {
	for drop := 1; drop <= 3; drop++ {
		t.Run(fmt.Sprintf("stage_%d", drop), func(t *testing.T) {
			request, authority := handoffRequestFixtureV2(t)
			port := &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 40, dropAckAt: drop}
			verifier := &handoffVerifierV2{}
			service, err := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), verifier, journalv2.DefaultEventRegistryV2())
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.Submit(t.Context(), request)
			if err != nil || result.State.Stage != StageCandidateRecordedV2 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			if len(port.plans) != 3 || port.publishes != 3 || verifier.calls != 1 {
				t.Fatalf("duplicate side effect: plans=%d publishes=%d git=%d", len(port.plans), port.publishes, verifier.calls)
			}
		})
	}
}

func TestHandoffCandidateRefusalIsDurableBoundedAndReplaysBeforeGitV2(t *testing.T) {
	request, authority := handoffRequestFixtureV2(t)
	port := &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 40}
	verifier := &refusingHandoffVerifierV2{}
	service, _ := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), verifier, journalv2.DefaultEventRegistryV2())
	result, err := service.Submit(t.Context(), request)
	if !IsKind(err, KindCandidateMismatch) || result.State.Stage != StageCandidateRefusedV2 || result.State.Refusal == nil || len(port.plans) != 2 || verifier.calls != 1 {
		t.Fatalf("result=%+v err=%v plans=%d calls=%d", result, err, len(port.plans), verifier.calls)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/secret/repo") || len(err.Error()) >= 4096 {
		t.Fatalf("unbounded refusal diagnostic: %q", err)
	}
	port.authority = AuthorityObservationV2{}
	replayed, replayErr := service.Submit(t.Context(), request)
	if !IsKind(replayErr, KindCandidateMismatch) || !replayed.Replayed || replayed.State.Refusal == nil || verifier.calls != 1 || len(port.plans) != 2 {
		t.Fatalf("replay=%+v err=%v plans=%d calls=%d", replayed, replayErr, len(port.plans), verifier.calls)
	}
}

func TestHandoffContributorCanonicalizationAndReducerRegistrationV2(t *testing.T) {
	if _, err := CanonicalContributors([]ids.ActorID{"actor-b", "actor-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalContributors([]ids.ActorID{"actor-a", "actor-a"}); err == nil {
		t.Fatal("duplicate contributor accepted")
	}
	registration := ReducerRegistrationsV2()
	if len(registration) != 1 || registration[0].Family != "handoff_slot" || registration[0].Reduce == nil {
		t.Fatalf("registration=%+v", registration)
	}
}

func TestHandoffCandidateReplacementPreservesExactLineageCASAndReplayV2(t *testing.T) {
	request, authority := handoffRequestFixtureV2(t)
	port := &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority, revision: 40}
	service, _ := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), &handoffVerifierV2{}, journalv2.DefaultEventRegistryV2())
	first, err := service.Submit(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	predecessorBytes := first.State.CanonicalBytes()
	predecessorDigest, _ := journalv2.ReducerStateDigestV2(predecessorBytes)
	replacement := request
	replacement.OperationID = "operation-handoff-replacement"
	replacement.HandoffID = "handoff-replacement"
	replacement.Candidate = strings.Repeat("c", 40)
	replacement.Replacement = &CandidateReplacementRequestV2{PredecessorHandoffID: first.State.HandoffID, PredecessorCandidate: first.State.Candidate, PredecessorLogicalVersion: first.State.LogicalVersion, PredecessorStateDigest: predecessorDigest.String(), Reason: "supersede the reviewed candidate after an explicit fix"}
	port.resolution = OperationResolutionV2{Kind: OperationAbsentV2}
	result, err := service.Submit(t.Context(), replacement)
	if err != nil || result.State.Stage != StageCandidateRecordedV2 || result.State.Replacement == nil || result.State.Replacement.PredecessorStateDigest != predecessorDigest.String() || !bytes.Equal(first.State.CanonicalBytes(), predecessorBytes) {
		t.Fatalf("replacement=%+v err=%v", result, err)
	}
	if len(port.plans) != 6 {
		t.Fatalf("replacement stage census=%d", len(port.plans))
	}
	replacementPlan := port.plans[3]
	if replacementPlan.BindingCreate() == nil {
		t.Fatal("replacement operation did not create its own immutable binding")
	}
	var replacementEvent EventV2
	var replacementHead journalv2.HeadTransitionV2
	for _, envelope := range replacementPlan.Events() {
		if envelope.Family() == "handoff_slot" {
			replacementEvent, err = DecodeEventV2(envelope.Payload())
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, head := range replacementPlan.Heads() {
		if head.Coordinate.Kind == "handoff_slot" {
			replacementHead = head
		}
	}
	if replacementEvent.State.Stage != StageCandidateReplacedV2 || replacementHead.ExpectedLogicalVersion != journalv2.LogicalVersion(first.State.LogicalVersion) || replacementHead.ExpectedRecordRevision != 43 {
		t.Fatalf("replacement CAS/event not bound to predecessor: event=%+v head=%+v", replacementEvent, replacementHead)
	}
	mutated := replacementEvent.State
	mutated.Replacement = &CandidateReplacementV2{}
	*mutated.Replacement = *replacementEvent.State.Replacement
	mutated.Replacement.PredecessorCandidate = strings.Repeat("9", 40)
	mutatedPayload, encodeErr := encodeEventV2(EventV2{SchemaVersion: 1, Kind: string(mutated.Stage), PriorDigest: digestStateV2(first.State), State: mutated})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if _, reduceErr := ReduceEventV2(first.State.CanonicalBytes(), handoffEnvelopeV2(t, replacement, mutated, mutatedPayload)); !IsKind(reduceErr, KindConflict) {
		t.Fatalf("substituted replacement lineage replayed: %v", reduceErr)
	}
	replayed, err := service.Submit(t.Context(), replacement)
	if err != nil || !replayed.Replayed || len(port.plans) != 6 {
		t.Fatalf("replacement replay=%+v err=%v plans=%d", replayed, err, len(port.plans))
	}
	changed := replacement
	changed.Replacement = &CandidateReplacementRequestV2{PredecessorHandoffID: first.State.HandoffID, PredecessorCandidate: first.State.Candidate, PredecessorLogicalVersion: first.State.LogicalVersion, PredecessorStateDigest: predecessorDigest.String(), Reason: "different reason"}
	if _, err := service.Submit(t.Context(), changed); !IsKind(err, KindConflict) && !IsKind(err, KindIdempotencyConflict) {
		t.Fatalf("changed replacement replay accepted: %v", err)
	}
}

func TestHandoffRejectsSelfConsistentCriterionSubsetAgainstWorkRevisionV2(t *testing.T) {
	request, authority := handoffRequestFixtureV2(t)
	request.CriterionIDs = request.CriterionIDs[:1]
	request.CriteriaDigest, _ = CriterionSetDigestV2(request.CriterionIDs)
	port := &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority}
	service, _ := NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), &handoffVerifierV2{}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !IsKind(err, KindRevisionIneligible) || len(port.plans) != 0 {
		t.Fatalf("self-consistent subset accepted: %v", err)
	}

	request, authority = handoffRequestFixtureV2(t)
	authority.WorkRevision.Assertion = substituteAssertionDigestV2(t, authority.WorkRevision.Assertion)
	port = &handoffPortV2{resolution: OperationResolutionV2{Kind: OperationAbsentV2}, authority: authority}
	service, _ = NewAuthorityServiceV2(port, handoffEpochV2(request.RuntimeEpochID), &handoffVerifierV2{}, journalv2.DefaultEventRegistryV2())
	if _, err := service.Submit(t.Context(), request); !IsKind(err, KindStaleAuthority) || len(port.plans) != 0 {
		t.Fatalf("substituted WorkRevision assertion accepted: %v", err)
	}
}

func substituteAssertionDigestV2(t *testing.T, assertion journalv2.AuthorityAssertionV2) journalv2.AuthorityAssertionV2 {
	t.Helper()
	digest, _ := journalv2.ParseDigest("mutant", strings.Repeat("9", 64))
	mutated, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: assertion.Target(), Kind: assertion.Kind(), ObservedRevision: assertion.ObservedRevision(), ExpectedLogicalVersion: assertion.ExpectedLogicalVersion(), ObservedValueDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	return mutated
}

func handoffRequestFixtureV2(t *testing.T) (AuthorityRequestV2, AuthorityObservationV2) {
	t.Helper()
	governance := handoffGovernanceV2(t)
	now := time.Date(2026, 9, 5, 14, 0, 0, 0, time.UTC)
	pathClaim, err := workspace.WorkspacePathClaimDigestV2("repository-1", "/tmp/spec-kitty", "/tmp/spec-kitty/workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.BindingV2{WorkspaceID: "workspace-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", RunID: "run-1", LeaseID: "lease-1", Generation: 1, Fence: "0123456789abcdef", RuntimeEpochID: "epoch-1", ActorID: "actor-1", ProcessID: "process-1", RepositoryID: "repository-1", LaneID: "lane-1", ManagedRoot: "/tmp/spec-kitty", CanonicalPath: "/tmp/spec-kitty/workspace-1", PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: pathClaim, OwnedBranch: "kitty/work-1", PinnedBase: strings.Repeat("a", 40)}
	workspaceState := workspace.StateV2{SchemaVersion: 1, LogicalVersion: 7, Binding: binding, Stage: workspace.StageReadyV2, Observation: workspace.ObservationV2{Registered: true, Head: binding.PinnedBase, DependencyHead: binding.PinnedBase, Branch: binding.OwnedBranch, GitDirIdentity: "git-dir-1", IndexIdentity: "index-1", Clean: true, TargetBefore: binding.PinnedBase, TargetAfter: binding.PinnedBase}, OccurredAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}
	executionState := execution.ExecutionSlotStateV2{SchemaVersion: 1, ProjectID: binding.ProjectID, MissionID: binding.MissionID, WorkRevisionID: binding.WorkRevisionID, WorkID: binding.WorkID, LogicalVersion: 4, RunID: binding.RunID, ActorID: binding.ActorID, ProcessID: binding.ProcessID, LeaseID: binding.LeaseID, Generation: binding.Generation, Fence: binding.Fence, HeartbeatAt: now.Add(-time.Minute).Format(time.RFC3339Nano), Deadline: now.Add(time.Hour).Format(time.RFC3339Nano), RuntimeEpochID: binding.RuntimeEpochID, Status: execution.ExecutionActiveV2, Governance: governance, WorkspaceID: binding.WorkspaceID, WorkspacePathClaimDigest: binding.PathClaimDigest}
	eligibility, revision := handoffEligibilityV2(t)
	revisionID := revision.ID()
	binding.WorkRevisionID = revisionID
	workspaceState.Binding.WorkRevisionID = revisionID
	executionState.WorkRevisionID = revisionID
	if err := workspaceState.Validate(); err != nil {
		t.Fatalf("workspace fixture: %v", err)
	}
	if err := executionState.Validate(); err != nil {
		t.Fatalf("execution fixture: %v", err)
	}
	execCoordinate, _ := execution.ExecutionSlotCoordinateV2(binding.ProjectID, binding.MissionID, binding.WorkRevisionID, binding.WorkID)
	workspaceCoordinate, _ := workspace.WorkspaceCoordinateV2(binding)
	workgraphCoordinate, _ := journalv2.NewAggregateCoordinate("workgraph", journalv2.AggregateID(binding.MissionID), journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: journalv2.ProjectID(binding.ProjectID), MissionID: journalv2.MissionID(binding.MissionID)})
	assertion := func(c journalv2.AggregateCoordinate, revision uint64, state []byte) journalv2.AuthorityAssertionV2 {
		d, _ := journalv2.ReducerStateDigestV2(state)
		a, e := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: c}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: revision, ExpectedLogicalVersion: 1, ObservedValueDigest: d})
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	criterionIDs := []string{"criterion-1", "criterion-2"}
	criteriaDigest, _ := CriterionSetDigestV2(criterionIDs)
	revisionBytes, _ := revision.Encode()
	request := AuthorityRequestV2{OperationID: "operation-handoff-1", HandoffID: "handoff-1", ProjectID: binding.ProjectID, MissionID: binding.MissionID, WorkRevisionID: binding.WorkRevisionID, WorkID: binding.WorkID, RunID: binding.RunID, RuntimeEpochID: binding.RuntimeEpochID, Workspace: binding, LeaseID: binding.LeaseID, LeaseGeneration: binding.Generation, Fence: binding.Fence, ActorID: binding.ActorID, ProcessID: binding.ProcessID, RepositoryID: binding.RepositoryID, RepositoryPath: "/repo", LaneID: binding.LaneID, OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Candidate: strings.Repeat("b", 40), Contributors: []ids.ActorID{"actor-1", "actor-2"}, CriterionIDs: criterionIDs, CriteriaDigest: criteriaDigest, WorkRevisionCriteriaDigest: revision.CriteriaDigest(), EvidenceDigest: strings.Repeat("d", 64), Governance: governance, ObservedAt: now}
	revisionAssertion := assertion(workgraphCoordinate, 23, revisionBytes)
	return request, AuthorityObservationV2{Eligibility: eligibility, Execution: execution.ExecutionSlotObservationV2{State: &executionState, RecordRevision: 21}, Workspace: execution.WorkspaceAuthorityObservationV2{State: &workspaceState, RecordRevision: 22}, WorkRevision: WorkRevisionObservationV2{Revision: revision, RecordRevision: 23, Assertion: revisionAssertion}, Assertions: []journalv2.AuthorityAssertionV2{assertion(execCoordinate, 21, executionState.CanonicalBytes()), assertion(workspaceCoordinate, 22, workspaceState.CanonicalBytes()), revisionAssertion}}
}

func handoffGovernanceV2(t *testing.T) execution.ExecutionGovernanceV2 {
	t.Helper()
	entries := make([]governancecontext.Entry, 0, 4)
	for _, item := range []struct {
		kind          governancecontext.EntryKind
		ref, revision string
	}{{governancecontext.KindConfig, "config", "policy-1"}, {governancecontext.KindDoctrine, "doctrine", "doctrine-1"}, {governancecontext.KindProfile, "profile", "profile-1"}, {governancecontext.KindMissionType, "mission", "mission-type-1"}} {
		c, _ := governancecontext.CommitSHA256([]byte(item.ref))
		e, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, c)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	ctx, err := governancecontext.NewContext("governance-1", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	g, err := execution.ExecutionGovernanceFromContextV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func handoffEligibilityV2(t *testing.T) (workgraph.ObservedEligibilityShardV2, workgraph.RevisionV2) {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-1", WorkID: "work-1", Title: "Work", Description: "Deliver work safely.", DesignContext: "Use authority.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-1", Text: "passes", Outcome: "green", Verifier: "test", Requirement: workgraph.CriterionRequired}, {ID: "criterion-2", Text: "also passes", Outcome: "green", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/handoff", Domain: "handoff", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-1", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-1", "mission-1", "artifact-plan", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-1", Kind: workgraph.ScopePlanConcern, MissionID: "mission-1"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("work-1"))
	final, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-1", Provider: "fake", ExternalID: "external-1", Revision: "provider-1", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sum), CriterionEvidenceHints: map[string]string{"criterion-1": "go test", "criterion-2": "go test"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: final, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-1", FinalizedAt: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), GovernanceContextID: "governance-1"})
	if err != nil {
		t.Fatal(err)
	}
	shards, err := workgraph.BuildInitialEligibilityGuardShards(revision, "operation-workgraph", []workgraph.EligibilityOperation{workgraph.EligibilityStart, workgraph.EligibilityHandoff, workgraph.EligibilityReview, workgraph.EligibilityAcceptance, workgraph.EligibilityIntegration})
	if err != nil {
		t.Fatal(err)
	}
	for _, shard := range shards {
		canonical, _ := shard.CanonicalBytes()
		observed, e := workgraph.NewObservedEligibilityShardV2(canonical, 17)
		if e == nil {
			if _, e = observed.Token("work-1", revision.ID(), workgraph.EligibilityHandoff); e == nil {
				return observed, revision
			}
		}
	}
	t.Fatal("handoff eligibility absent")
	return workgraph.ObservedEligibilityShardV2{}, workgraph.RevisionV2{}
}

func digestStateV2(s StateV2) string {
	d, _ := journalv2.ReducerStateDigestV2(s.CanonicalBytes())
	return d.String()
}
func handoffEnvelopeV2(t *testing.T, request AuthorityRequestV2, state StateV2, payload []byte) journalv2.EventEnvelopeV2 {
	t.Helper()
	coordinate, _ := CoordinateV2(state.ProjectID, state.MissionID, state.WorkRevisionID, state.WorkID)
	event, err := journalv2.NewEventEnvelopeV2(journalv2.EventEnvelopeInput{EventID: "event-mutant", EventIndex: 0, EventCount: 1, EventType: "handoff_slot." + string(state.Stage), Family: "handoff_slot", EventSchemaVersion: 1, ReducerVersion: 1, Coordinate: coordinate, AggregateLogicalVersion: journalv2.LogicalVersion(state.LogicalVersion), OperationID: request.OperationID, OperationLogicalVersion: journalv2.LogicalVersion(state.LogicalVersion), PublicationID: journalv2.PublicationID(strings.Repeat("e", 64)), CommittedRuntimeEpochID: state.RuntimeEpochID, ActorID: journalv2.ActorID(state.ActorID), ProcessID: journalv2.ProcessID(state.ProcessID), WorkRevisionID: journalv2.WorkRevisionID(state.WorkRevisionID), PolicyRevision: state.Governance.PolicyRevision, Payload: payload}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	return event
}
