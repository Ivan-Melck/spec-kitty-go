package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type acceptanceEpochV2 journalv2.RuntimeEpochID

func (e acceptanceEpochV2) CurrentRuntimeEpoch(context.Context) (journalv2.RuntimeEpochID, error) {
	return journalv2.RuntimeEpochID(e), nil
}

type acceptancePortV2 struct {
	resolution     AcceptanceOperationResolutionV2
	acceptance     AcceptanceObservationV2
	authority      AcceptanceAuthorityObservationV2
	plans          []journalv2.SealedCommitPlanV2
	revision       uint64
	authorityReads int
	dropAckAt      int
}

func (p *acceptancePortV2) ResolveAcceptanceOperation(context.Context, journalv2.OperationBindingV2) (AcceptanceOperationResolutionV2, error) {
	return p.resolution, nil
}
func (p *acceptancePortV2) ObserveAcceptance(context.Context, journalv2.AggregateCoordinate) (AcceptanceObservationV2, error) {
	return p.acceptance, nil
}
func (p *acceptancePortV2) ObserveAcceptanceAuthority(context.Context, AcceptanceAuthorityRequestV2) (AcceptanceAuthorityObservationV2, error) {
	p.authorityReads++
	return p.authority, nil
}
func (p *acceptancePortV2) PublishAcceptance(_ context.Context, plan AcceptancePlanV2) (AcceptanceAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil {
		return AcceptanceAckV2{}, err
	}
	p.revision++
	p.plans = append(p.plans, sealed)
	p.resolution = AcceptanceOperationResolutionV2{Kind: AcceptanceOperationExactV2, State: sealed.OperationState(), RecordRevision: p.revision}
	for _, event := range sealed.Events() {
		if event.Family() != "acceptance" {
			continue
		}
		var prior []byte
		if p.acceptance.State != nil {
			prior = p.acceptance.State.CanonicalBytes()
		}
		next, reduceErr := ReduceAcceptanceEventV2(prior, event)
		if reduceErr != nil {
			return AcceptanceAckV2{}, reduceErr
		}
		state, decodeErr := DecodeAcceptanceStateV2(next)
		if decodeErr != nil {
			return AcceptanceAckV2{}, decodeErr
		}
		p.acceptance = AcceptanceObservationV2{State: &state, RecordRevision: p.revision, LastPublicationID: sealed.PublicationID()}
	}
	if len(p.plans) == p.dropAckAt {
		return AcceptanceAckV2{}, fmt.Errorf("simulated lost acknowledgement")
	}
	return AcceptanceAckV2{sealed.PublicationID(), p.revision, sealed.Digest(), sealed.MutationCount()}, nil
}
func (p *acceptancePortV2) ReconcileAcceptance(_ context.Context, plan AcceptancePlanV2) (AcceptanceAckV2, error) {
	sealed, err := plan.Publication()
	if err != nil || len(p.plans) == 0 || p.plans[len(p.plans)-1].PublicationID() != sealed.PublicationID() {
		return AcceptanceAckV2{}, fmt.Errorf("publication absent")
	}
	return AcceptanceAckV2{sealed.PublicationID(), p.revision, sealed.Digest(), sealed.MutationCount()}, nil
}

func TestAcceptanceIsExactEvidenceReadinessNotPhaseCeremonyV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority, revision: 70, dropAckAt: 1}
	service, err := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Accept(t.Context(), request)
	if err != nil || result.State.Stage != AcceptanceGrantedV2 || len(port.plans) != 2 || len(result.State.Evidence) != 3 {
		t.Fatalf("result=%+v error=%v plans=%d", result, err, len(port.plans))
	}
	for _, plan := range port.plans {
		if len(plan.Events()) != 2 || len(plan.Heads()) != 2 || len(plan.Conditions()) != 3 || len(plan.EligibilityGuards()) != 1 || len(plan.Assertions()) != 3 {
			t.Fatalf("unexpected readiness plan: events=%d heads=%d conditions=%d guards=%d assertions=%d", len(plan.Events()), len(plan.Heads()), len(plan.Conditions()), len(plan.EligibilityGuards()), len(plan.Assertions()))
		}
	}
	reads := port.authorityReads
	port.authority = AcceptanceAuthorityObservationV2{}
	replay, err := service.Accept(t.Context(), request)
	if err != nil || !replay.Replayed || port.authorityReads != reads || len(port.plans) != 2 {
		t.Fatalf("replay=%+v error=%v reads=%d/%d plans=%d", replay, err, reads, port.authorityReads, len(port.plans))
	}
}

func TestAcceptanceLostAuthorityResponsesReconcileOneHundredTrialsV2(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		request, authority := acceptanceFixtureV2(t)
		request.OperationID = journalv2.OperationID(fmt.Sprintf("operation-acceptance-lost-%03d", trial))
		request.AcceptanceID = fmt.Sprintf("acceptance-lost-%03d", trial)
		port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority, revision: 70, dropAckAt: trial%2 + 1}
		service, _ := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
		result, err := service.Accept(t.Context(), request)
		if err != nil || result.State.Stage != AcceptanceGrantedV2 || len(port.plans) != 2 {
			t.Fatalf("trial=%d stage=%d result=%+v error=%v plans=%d", trial, port.dropAckAt, result, err, len(port.plans))
		}
	}
}

func TestAcceptanceRefusesStaleEvidenceAndSameActorBeforePublicationV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	for name, mutate := range map[string]func(*AcceptanceAuthorityObservationV2){
		"stale evidence revision": func(observed *AcceptanceAuthorityObservationV2) {
			item := observed.Evidence[0]
			item.Evidence.RecordRevision++
			observed.Evidence[0] = item
		},
		"substituted evidence logical version": func(observed *AcceptanceAuthorityObservationV2) {
			item := observed.Evidence[0]
			item.Evidence.LogicalVersion++
			observed.Evidence[0] = item
		},
		"same actor review": func(observed *AcceptanceAuthorityObservationV2) {
			state := *observed.Reviews[0].State
			state.ReviewerActorID = "actor-implementer"
			observed.Reviews[0].State = &state
		},
	} {
		t.Run(name, func(t *testing.T) {
			observed := authority
			observed.Reviews = append([]review.ObservationV2(nil), authority.Reviews...)
			observed.Evidence = append([]AcceptanceEvidenceObservationV2(nil), authority.Evidence...)
			mutate(&observed)
			port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: observed}
			service, _ := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
			if _, err := service.Accept(t.Context(), request); err == nil || len(port.plans) != 0 {
				t.Fatalf("mutant accepted: %v plans=%d", err, len(port.plans))
			}
		})
	}
}

func TestApplicableMissingEvidenceNamesProducerAndRecoveryV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	authority.Evidence = nil
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority}
	service, _ := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	_, err := service.Accept(t.Context(), request)
	var typed *Error
	if !errors.As(err, &typed) || typed.Kind != KindEvidenceUnavailable || typed.Observed["criterion_id"] != "criterion-test" || typed.Observed["producer"] != "project.verify" || typed.Expected["recovery"] == "" || len(port.plans) != 0 {
		t.Fatalf("error=%+v plans=%d", typed, len(port.plans))
	}
}

func TestAcceptanceWaiversAreNarrowCurrentAndContentAddressedV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	waiver, err := SealAcceptanceWaiverV2(AcceptanceWaiverV2{SchemaVersion: 1, WaiverID: "waiver-docs-1", CriterionID: "criterion-docs", ProjectID: request.ProjectID, MissionID: request.MissionID, WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID, PolicyRevision: request.PolicyRevision, PolicyBasis: "project policy permits a documentation-only waiver", AccountableActor: "actor-owner", Rationale: "documentation is supplied by the generated API reference", EffectiveFrom: request.ObservedAt.Add(-time.Minute).Format(time.RFC3339Nano), EffectiveThrough: request.ObservedAt.Add(time.Hour).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	request.Waivers = []AcceptanceWaiverV2{waiver}
	request.Evidence[1] = AcceptanceEvidenceV2{CriterionID: "criterion-docs", Kind: "documentation", Disposition: EvidenceWaivedV2, Producer: "project.policy", PolicyBasis: "project policy permits a documentation-only waiver", WaiverID: waiver.WaiverID}
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority}
	service, _ := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if _, err := service.Accept(t.Context(), request); err != nil {
		t.Fatalf("current exact waiver refused: %v", err)
	}
	request.OperationID = "operation-acceptance-expired"
	request.ObservedAt = request.ObservedAt.Add(2 * time.Hour)
	port = &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority}
	service, _ = NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if _, err := service.Accept(t.Context(), request); !IsKind(err, KindPolicyRefused) || len(port.plans) != 0 {
		t.Fatalf("expired waiver accepted: %v", err)
	}
}

func acceptanceFixtureV2(t *testing.T) (AcceptanceAuthorityRequestV2, AcceptanceAuthorityObservationV2) {
	t.Helper()
	now := time.Date(2026, 9, 5, 16, 0, 0, 0, time.UTC)
	governance := acceptanceGovernanceV2(t)
	eligibility, revision := acceptanceEligibilityV2(t)
	contributors := []ids.ActorID{"actor-implementer", "actor-other"}
	contributorDigest := acceptanceContributorDigestV2(contributors)
	criteria := []string{"criterion-docs", "criterion-network", "criterion-test"}
	criteriaDigest, _ := handoff.CriterionSetDigestV2(criteria)
	handoffState := handoff.StateV2{SchemaVersion: 1, LogicalVersion: 3, Stage: handoff.StageCandidateRecordedV2, HandoffID: "handoff-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: revision.ID(), WorkID: "work-1", RunID: "run-1", RuntimeEpochID: "epoch-1", WorkspaceID: "workspace-1", LeaseID: "lease-1", LeaseGeneration: 1, Fence: "0123456789abcdef", ActorID: "actor-implementer", ProcessID: "process-implementer", RepositoryID: "repository-1", LaneID: "lane-1", OwnedBranch: "kitty/work-1", PinnedBase: strings.Repeat("a", 40), Candidate: strings.Repeat("b", 40), Contributors: contributors, ContributorDigest: contributorDigest, CriterionIDs: criteria, CriteriaDigest: criteriaDigest, WorkRevisionCriteriaDigest: revision.CriteriaDigest(), EvidenceDigest: strings.Repeat("d", 64), RepositoryEvidence: handoff.CandidateEvidence{Candidate: strings.Repeat("b", 40), PinnedBase: strings.Repeat("a", 40), OwnedBranch: "kitty/work-1", ObservedBranchHead: strings.Repeat("b", 40)}, Governance: governance, ObservedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}
	reviewState := review.StateV2{SchemaVersion: 1, LogicalVersion: 3, Stage: review.StageVerdictRecordedV2, ReviewID: "review-1", HandoffID: "handoff-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: revision.ID(), WorkID: "work-1", RuntimeEpochID: "epoch-1", Candidate: strings.Repeat("b", 40), HandoffContributorDigest: contributorDigest, ReviewerActorID: "actor-reviewer", ReviewerProcessID: "process-reviewer", PolicyRevision: governance.PolicyRevision, CriterionSetDigest: acceptanceReviewCriteriaDigestV2(criteria), WorkRevisionCriteriaDigest: revision.CriteriaDigest(), CriterionIDs: criteria, Criteria: []review.CriterionEvidenceV2{{CriterionID: "criterion-docs", Status: review.Passed, EvidenceDigest: strings.Repeat("1", 64), ArtifactDigest: strings.Repeat("2", 64), Candidate: strings.Repeat("b", 40), PolicyRevision: governance.PolicyRevision}, {CriterionID: "criterion-network", Status: review.Passed, EvidenceDigest: strings.Repeat("3", 64), ArtifactDigest: strings.Repeat("4", 64), Candidate: strings.Repeat("b", 40), PolicyRevision: governance.PolicyRevision}, {CriterionID: "criterion-test", Status: review.Passed, EvidenceDigest: strings.Repeat("e", 64), ArtifactDigest: strings.Repeat("f", 64), Candidate: strings.Repeat("b", 40), PolicyRevision: governance.PolicyRevision}}, Verdict: review.Approved, Governance: governance, ObservedAt: now.Format(time.RFC3339Nano)}
	if handoffState.Validate() != nil || reviewState.Validate() != nil {
		t.Fatal("invalid acceptance fixture authority")
	}
	evidenceCoordinate, _ := journalv2.NewAggregateCoordinate("mission", "evidence:test", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"})
	evidenceDigest, _ := journalv2.ReducerStateDigestV2([]byte(`{"status":"pass"}`))
	evidence := AcceptanceEvidenceV2{CriterionID: "criterion-test", Kind: "automated_test", Disposition: EvidenceSatisfiedV2, Producer: "project.verify", PolicyBasis: "project verification contract", EvidenceID: "evidence-test-1", Coordinate: evidenceCoordinate, LogicalVersion: 2, RecordRevision: 22, StateDigestSHA256: evidenceDigest.String()}
	request := AcceptanceAuthorityRequestV2{OperationID: "operation-acceptance-1", AcceptanceID: "acceptance-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: revision.ID(), WorkID: "work-1", HandoffID: "handoff-1", RepositoryID: "repository-1", Candidate: strings.Repeat("b", 40), ContributorDigest: contributorDigest, ReviewIDs: []ids.ReviewID{"review-1"}, Evidence: []AcceptanceEvidenceV2{evidence, {CriterionID: "criterion-docs", Kind: "documentation", Disposition: EvidenceInapplicableV2, Producer: "project.policy", PolicyBasis: "no documentation deliverable is applicable"}, {CriterionID: "criterion-network", Kind: "contract", Disposition: EvidenceInapplicableV2, Producer: "project.policy", PolicyBasis: "the product has no network surface"}}, PolicyRevision: governance.PolicyRevision, RuntimeEpochID: "epoch-1", ActorID: "actor-acceptor", ProcessID: "process-acceptor", Governance: governance, ObservedAt: now}
	handoffCoordinate, _ := handoff.CoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID)
	reviewCoordinate, _ := review.CoordinateV2(request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, "review-1")
	return request, AcceptanceAuthorityObservationV2{Eligibility: eligibility, Handoff: handoff.ObservationV2{State: &handoffState, RecordRevision: 20}, Reviews: []review.ObservationV2{{State: &reviewState, RecordRevision: 21}}, Evidence: []AcceptanceEvidenceObservationV2{{Evidence: evidence, Assertion: acceptanceAssertionV2(t, evidenceCoordinate, 22, 2, []byte(`{"head":"evidence"}`))}}, Assertions: []journalv2.AuthorityAssertionV2{acceptanceAssertionV2(t, handoffCoordinate, 20, 3, handoffState.CanonicalBytes()), acceptanceAssertionV2(t, reviewCoordinate, 21, 3, reviewState.CanonicalBytes()), acceptanceAssertionV2(t, evidenceCoordinate, 22, 2, []byte(`{"head":"evidence"}`))}}
}

func acceptanceGovernanceV2(t *testing.T) execution.ExecutionGovernanceV2 {
	t.Helper()
	var entries []governancecontext.Entry
	for _, item := range []struct {
		kind          governancecontext.EntryKind
		ref, revision string
	}{{governancecontext.KindConfig, "config", "policy-1"}, {governancecontext.KindDoctrine, "doctrine", "doctrine-1"}, {governancecontext.KindProfile, "profile", "profile-1"}, {governancecontext.KindMissionType, "mission", "mission-1"}} {
		commit, _ := governancecontext.CommitSHA256([]byte(item.ref))
		entry, err := governancecontext.NewEntry(item.kind, item.ref, item.revision, commit)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	ctx, err := governancecontext.NewContext("governance-1", governancecontext.SchemaVersion, entries, time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	governance, err := execution.ExecutionGovernanceFromContextV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return governance
}

func acceptanceEligibilityV2(t *testing.T) (workgraph.ObservedEligibilityShardV2, workgraph.RevisionV2) {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-1", WorkID: "work-1", Title: "Work", Description: "Deliver accepted work.", DesignContext: "Use readiness authority.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-docs", Text: "documentation policy applies", Outcome: "pass", Verifier: "test", Requirement: workgraph.CriterionRequired}, {ID: "criterion-network", Text: "network contract policy applies", Outcome: "pass", Verifier: "test", Requirement: workgraph.CriterionRequired}, {ID: "criterion-test", Text: "tests pass", Outcome: "pass", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/integration", Domain: "integration", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "scope-1", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-1", "mission-1", "artifact-plan", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "scope-1", Kind: workgraph.ScopePlanConcern, MissionID: "mission-1"}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("work-1"))
	final, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: "work-1", Provider: "fake", ExternalID: "external", Revision: "provider-1", MetadataSchema: 1, DefinitionDigest: hex.EncodeToString(sum[:]), CriterionEvidenceHints: map[string]string{"criterion-docs": "project.policy", "criterion-network": "project.policy", "criterion-test": "project.verify"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: final, PlanningSourceCommit: strings.Repeat("b", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("c", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-owner", FinalizedAt: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), GovernanceContextID: "governance-1"})
	if err != nil {
		t.Fatal(err)
	}
	shards, err := workgraph.BuildInitialEligibilityGuardShards(revision, "operation-workgraph", []workgraph.EligibilityOperation{workgraph.EligibilityStart, workgraph.EligibilityHandoff, workgraph.EligibilityReview, workgraph.EligibilityAcceptance, workgraph.EligibilityIntegration})
	if err != nil {
		t.Fatal(err)
	}
	for _, shard := range shards {
		canonical, _ := shard.CanonicalBytes()
		observed, observeErr := workgraph.NewObservedEligibilityShardV2(canonical, 17)
		if observeErr == nil {
			if _, tokenErr := observed.Token("work-1", revision.ID(), workgraph.EligibilityAcceptance); tokenErr == nil {
				return observed, revision
			}
		}
	}
	t.Fatal("acceptance eligibility absent")
	return workgraph.ObservedEligibilityShardV2{}, workgraph.RevisionV2{}
}

func acceptanceAssertionV2(t *testing.T, coordinate journalv2.AggregateCoordinate, revision uint64, logical journalv2.LogicalVersion, value []byte) journalv2.AuthorityAssertionV2 {
	t.Helper()
	digest, _ := journalv2.ReducerStateDigestV2(value)
	assertion, err := journalv2.NewAuthorityAssertionV2(journalv2.AuthorityAssertionInputV2{Target: journalv2.ConditionTarget{Kind: journalv2.ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: journalv2.ConditionRecordRevision, ObservedRevision: revision, ExpectedLogicalVersion: logical, ObservedValueDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	return assertion
}

func acceptanceContributorDigestV2(values []ids.ActorID) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("spec-kitty/handoff-contributors/v1\x00"))
	for _, value := range values {
		fmt.Fprintf(hash, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func acceptanceReviewCriteriaDigestV2(values []string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("spec-kitty/review-criteria/v1\x00"))
	for _, value := range values {
		fmt.Fprintf(hash, "%d:%s", len(value), value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
