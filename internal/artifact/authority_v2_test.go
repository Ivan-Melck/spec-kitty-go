package artifact

import (
	"bytes"
	"sort"
	"strings"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestArtifactV2PublicationAndDecisionAreExactAtomicAuthority(t *testing.T) {
	revisionA, validationA, contentA := artifactV2Fixture(t, "artifact-a", 1)
	contextA := artifactV2Context("publish-a")
	planA, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: contextA, Revision: revisionA, Validation: validationA, Content: contentA})
	if err != nil {
		t.Fatal(err)
	}
	if got := artifactFamilies(planA); got != "artifact_catalog,artifact_revision,operation" {
		t.Fatalf("publication event membership = %s", got)
	}
	if planA.BindingCreate() == nil || len(planA.Conditions()) != 3 || planA.MutationCount() != 8 {
		t.Fatalf("publication is not one complete sealed authority batch: conditions=%d mutations=%d", len(planA.Conditions()), planA.MutationCount())
	}
	catalogA := reduceArtifactEvent(t, nil, eventByFamily(t, planA, "artifact_catalog"))
	revisionStateA := reduceArtifactEvent(t, nil, eventByFamily(t, planA, "artifact_revision"))

	approval, err := NewArtifactApproval(ArtifactApprovalParams{ID: "approval-a", ArtifactRevisionID: revisionA.ID(), Verdict: VerdictApproved, ActorID: "reviewer-a", EvidenceDigest: strings.Repeat("e", 64), GovernanceContextID: "governance-a", SubmittedAt: time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	decisionContext := artifactV2Context("decide-a")
	decisionContext.CatalogEventID = ""
	decision, err := BuildArtifactDecisionPlanV2(DecisionPlanInputV2{Context: decisionContext, PriorRevisionState: revisionStateA, Approval: approval, ExpectedRevisionRecordRevision: 17})
	if err != nil {
		t.Fatal(err)
	}
	if got := artifactFamilies(decision); got != "artifact_revision,operation" || len(decision.Conditions()) != 2 || decision.MutationCount() != 6 {
		t.Fatalf("decision membership is not exact: families=%s conditions=%d mutations=%d", got, len(decision.Conditions()), decision.MutationCount())
	}
	decidedA := reduceArtifactEvent(t, revisionStateA, eventByFamily(t, decision, "artifact_revision"))
	decodedDecision, err := DecodeArtifactRevisionStateV2(decidedA)
	if err != nil {
		t.Fatal(err)
	}
	contractA, err := decodeArtifactAuthorityContractV2(decodedDecision.Contract)
	if err != nil || contractA.Approval == nil || contractA.Approval.ArtifactRevisionID() != revisionA.ID() {
		t.Fatalf("decision floated away from exact revision: %+v %v", contractA.Approval, err)
	}

	revisionB, validationB, contentB := artifactV2Fixture(t, "artifact-b", 2)
	planB, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-b"), Revision: revisionB, Validation: validationB, Content: contentB, PriorCatalogState: catalogA, ExpectedCatalogRecordRevision: 18})
	if err != nil {
		t.Fatal(err)
	}
	catalogB := reduceArtifactEvent(t, catalogA, eventByFamily(t, planB, "artifact_catalog"))
	current, err := DecodeArtifactCatalogStateV2(catalogB)
	if err != nil || current.LatestRevisionID != revisionB.ID() || current.LogicalVersion != 2 {
		t.Fatalf("catalog did not advance exactly: %+v %v", current, err)
	}
	stillA, err := DecodeArtifactRevisionStateV2(decidedA)
	if err != nil || !bytes.Equal(stillA.Contract, decodedDecision.Contract) {
		t.Fatal("later catalog publication rewrote historical revision decision")
	}
}

func TestArtifactV2RefusesStaleCatalogAndFailedValidationApproval(t *testing.T) {
	revisionA, validationA, contentA := artifactV2Fixture(t, "artifact-a", 1)
	plan, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-a"), Revision: revisionA, Validation: validationA, Content: contentA})
	if err != nil {
		t.Fatal(err)
	}
	catalog := reduceArtifactEvent(t, nil, eventByFamily(t, plan, "artifact_catalog"))
	revisionB, validationB, contentB := artifactV2Fixture(t, "artifact-b", 2)
	revisionBWrong, _ := NewArtifactRevision(ArtifactRevisionParams{ID: revisionB.ID(), MissionID: revisionB.MissionID(), Kind: revisionB.Kind(), RevisionNumber: 3, ContentDigest: revisionB.ContentDigest(), SourceRef: revisionB.SourceRef(), SourceRevision: revisionB.SourceRevision(), CreatedBy: revisionB.CreatedBy(), CreatedAt: revisionB.CreatedAt()})
	if _, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-b"), Revision: revisionBWrong, Validation: validationB, Content: contentB, PriorCatalogState: catalog, ExpectedCatalogRecordRevision: 9}); !IsKind(err, ErrorInvalidRevision) {
		t.Fatalf("stale catalog expectation admitted: %v", err)
	}

	finding, _ := NewFinding("missing_required_concern", "risk", "required structural concern is absent", RecoveryProvideEvidence)
	failedRevision, _ := NewArtifactRevision(ArtifactRevisionParams{ID: "artifact-failed", MissionID: revisionA.MissionID(), Kind: KindPlan, RevisionNumber: 1, ContentDigest: revisionA.ContentDigest(), SourceRef: revisionA.SourceRef(), SourceRevision: revisionA.SourceRevision(), CreatedBy: revisionA.CreatedBy(), CreatedAt: revisionA.CreatedAt()})
	failed := mustValidationFor(t, failedRevision.ID(), ValidationFail, []Finding{finding})
	failedPlan, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-failed"), Revision: failedRevision, Validation: failed, Content: contentA})
	if err != nil {
		t.Fatal(err)
	}
	failedState := reduceArtifactEvent(t, nil, eventByFamily(t, failedPlan, "artifact_revision"))
	restoredFailed, err := DecodeArtifactRevisionStateV2(failedState)
	if err != nil {
		t.Fatal(err)
	}
	restoredContract, err := decodeArtifactAuthorityContractV2(restoredFailed.Contract)
	if err != nil || restoredContract.Validation.Policy().Revision() != failed.Policy().Revision() || restoredContract.Validation.Findings()[0].Recovery() != RecoveryProvideEvidence {
		t.Fatalf("restore lost validation policy or recovery evidence: %+v %v", restoredContract.Validation, err)
	}
	approval, _ := NewArtifactApproval(ArtifactApprovalParams{ID: "approval-failed", ArtifactRevisionID: failedRevision.ID(), Verdict: VerdictApproved, ActorID: "reviewer-a", EvidenceDigest: strings.Repeat("f", 64), GovernanceContextID: "governance-a", SubmittedAt: time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)})
	if _, err := BuildArtifactDecisionPlanV2(DecisionPlanInputV2{Context: artifactV2Context("decide-failed"), PriorRevisionState: failedState, Approval: approval, ExpectedRevisionRecordRevision: 12}); !IsKind(err, ErrorValidationRequired) {
		t.Fatalf("failed validation authorized approval: %v", err)
	}
}

func TestArtifactV2DecisionRequiresOperationBoundGovernance(t *testing.T) {
	revision, validation, content := artifactV2Fixture(t, "artifact-governance", 1)
	publication, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-governance"), Revision: revision, Validation: validation, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	prior := reduceArtifactEvent(t, nil, eventByFamily(t, publication, "artifact_revision"))
	approval, err := NewArtifactApproval(ArtifactApprovalParams{ID: "approval-governance", ArtifactRevisionID: revision.ID(), Verdict: VerdictApproved, ActorID: "reviewer-a", EvidenceDigest: strings.Repeat("e", 64), GovernanceContextID: "governance-foreign", SubmittedAt: time.Date(2026, 9, 4, 2, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildArtifactDecisionPlanV2(DecisionPlanInputV2{Context: artifactV2Context("decide-governance"), PriorRevisionState: prior, Approval: approval, ExpectedRevisionRecordRevision: 9}); !IsKind(err, ErrorApprovalNotAllowed) {
		t.Fatalf("foreign governance approved an exact revision: %v", err)
	}
}

func TestArtifactV2RequiresExactObservedContentBytes(t *testing.T) {
	revision, validation, _ := artifactV2Fixture(t, "artifact-content", 1)
	other, err := CommitContentV2([]byte("different bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("content-mismatch"), Revision: revision, Validation: validation, Content: other}); !IsKind(err, ErrorInvalidRevision) {
		t.Fatalf("caller-supplied digest was accepted without its bytes: %v", err)
	}
}

func TestArtifactV2ContentCommitmentUsesFrozenDomainSeparatedAlgorithm(t *testing.T) {
	content := []byte("same bytes, same authority")
	commitment, err := CommitContentV2(content)
	if err != nil {
		t.Fatal(err)
	}
	if commitment.Digest() != ContentCommitment(content) {
		t.Fatalf("v2 commitment %s diverged from frozen domain-separated algorithm %s", commitment.Digest(), ContentCommitment(content))
	}
	other, err := CommitContentV2([]byte("same bytes, different authority"))
	if err != nil || other.Digest() == commitment.Digest() {
		t.Fatalf("byte change aliased commitment: %s/%s (%v)", commitment.Digest(), other.Digest(), err)
	}
}

func TestArtifactV2ExactReplayPrecedesEpochAndChangedBindingConflicts(t *testing.T) {
	revision, validation, content := artifactV2Fixture(t, "artifact-a", 1)
	plan, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-a"), Revision: revision, Validation: validation, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.BindingCreate()
	encoded, _ := journalv2.EncodeOperationBindingV2(*binding)
	state := plan.OperationState()
	resolution, err := ResolveArtifactRetryV2(*binding, encoded, true, &state, "epoch-replacement")
	if err != nil || resolution.Decision != journalv2.RetryReplay || !bytes.Equal(resolution.Result, state.Result()) {
		t.Fatalf("exact retry did not replay before stale epoch: %+v %v", resolution, err)
	}

	otherRevision, otherValidation, otherContent := artifactV2Fixture(t, "artifact-other", 1)
	other, err := BuildArtifactPublishPlanV2(PublishPlanInputV2{Context: artifactV2Context("publish-a"), Revision: otherRevision, Validation: otherValidation, Content: otherContent})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err = ResolveArtifactRetryV2(*other.BindingCreate(), encoded, true, &state, binding.OriginatingEpoch())
	if resolution.Decision != journalv2.RetryConflict || !journalv2.IsKind(err, journalv2.ErrorIdempotencyConflict) {
		t.Fatalf("changed binding was not the earliest refusal: %+v %v", resolution, err)
	}
}

func artifactV2Fixture(t *testing.T, revisionID string, number uint64) (ArtifactRevision, ValidationResult, ContentCommitmentV2) {
	t.Helper()
	content, err := CommitContentV2([]byte("artifact content for " + revisionID))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := NewArtifactRevision(ArtifactRevisionParams{ID: revisionID, MissionID: "mission-artifact", Kind: KindPlan, RevisionNumber: number, ContentDigest: content.Digest(), SourceRef: "kitty-specs/plan.md", SourceRevision: "commit-a", CreatedBy: "author-a", CreatedAt: time.Date(2026, 9, 4, 0, int(number), 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	validation := mustValidationFor(t, revisionID, ValidationPass, nil)
	return revision, validation, content
}

func mustValidationFor(t *testing.T, revisionID string, verdict ValidationVerdict, findings []Finding) ValidationResult {
	t.Helper()
	policy, _ := PolicyFor(KindPlan)
	validation, err := NewValidationResult(ValidationResultParams{ArtifactRevisionID: revisionID, Verdict: verdict, Findings: findings, Policy: policy, ValidatedAt: time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return validation
}

func artifactV2Context(label string) AuthorityContextV2 {
	return AuthorityContextV2{ProjectID: "project-artifact", OperationID: journalv2.OperationID("operation-" + label), RuntimeEpochID: "epoch-artifact", ActorID: "actor-artifact", ProcessID: "process-artifact", DoctrineRevision: "doctrine-a", ProfileRevision: "profile-a", PolicyRevision: "policy-a", Governance: []string{"governance-a"}, ObservedCreatedAt: "2026-09-04T00:00:00Z", OperationEventID: journalv2.EventID("event-operation-" + label), CatalogEventID: journalv2.EventID("event-catalog-" + label), RevisionEventID: journalv2.EventID("event-revision-" + label)}
}

func eventByFamily(t *testing.T, plan journalv2.SealedCommitPlanV2, family string) journalv2.EventEnvelopeV2 {
	t.Helper()
	for _, event := range plan.Events() {
		if event.Family() == family {
			return event
		}
	}
	t.Fatalf("event family %s is absent", family)
	return journalv2.EventEnvelopeV2{}
}

func reduceArtifactEvent(t *testing.T, prior []byte, event journalv2.EventEnvelopeV2) []byte {
	t.Helper()
	var result []byte
	var err error
	if event.Family() == "artifact_catalog" {
		result, err = ReduceArtifactCatalogEventV2(prior, event)
	} else {
		result, err = ReduceArtifactRevisionEventV2(prior, event)
	}
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func artifactFamilies(plan journalv2.SealedCommitPlanV2) string {
	families := make([]string, 0, len(plan.Events()))
	for _, event := range plan.Events() {
		families = append(families, event.Family())
	}
	sort.Strings(families)
	return strings.Join(families, ",")
}
