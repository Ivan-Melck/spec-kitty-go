package artifact

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestArtifactServiceCompleteFreshnessProgression(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 19, 0, 0, 0, time.UTC)

	revision1 := publishArtifact(t, ctx, service, repository, publishFixture(t, "1", KindSpecification, []byte("specification v1"), true, now))
	approval1 := approveArtifact(t, ctx, service, approveFixture("1", revision1.ID(), VerdictApproved, now.Add(time.Minute)))
	assertFreshness(t, ctx, service, revision1.ID(), FreshnessFresh, approval1.ID())

	revision2 := publishArtifact(t, ctx, service, repository, publishFixture(t, "2", KindSpecification, []byte("changed invalid specification v2"), false, now.Add(2*time.Minute)))
	assertFreshness(t, ctx, service, revision2.ID(), FreshnessStale, "")
	assertFreshness(t, ctx, service, revision1.ID(), FreshnessFresh, approval1.ID())
	_, err = service.Approve(ctx, approveFixture("invalid-approval", revision2.ID(), VerdictApproved, now.Add(3*time.Minute)))
	assertErrorKind(t, err, ErrorApprovalNotAllowed)
	rejection2 := approveArtifact(t, ctx, service, approveFixture("2", revision2.ID(), VerdictRejected, now.Add(3*time.Minute)))
	assertFreshness(t, ctx, service, revision2.ID(), FreshnessRejected, rejection2.ID())

	revision3 := publishArtifact(t, ctx, service, repository, publishFixture(t, "3", KindSpecification, []byte("corrected specification v3"), true, now.Add(4*time.Minute)))
	approval3 := approveArtifact(t, ctx, service, approveFixture("3", revision3.ID(), VerdictApproved, now.Add(5*time.Minute)))
	assertFreshness(t, ctx, service, revision3.ID(), FreshnessFresh, approval3.ID())
	assertFreshness(t, ctx, service, revision1.ID(), FreshnessFresh, approval1.ID())

	if revision1.RevisionNumber() != 1 || revision2.RevisionNumber() != 2 || revision3.RevisionNumber() != 3 {
		t.Fatalf("revision sequence=%d,%d,%d", revision1.RevisionNumber(), revision2.RevisionNumber(), revision3.RevisionNumber())
	}
	if repository.revisions[revision1.ID()].ContentDigest() == repository.revisions[revision3.ID()].ContentDigest() {
		t.Fatal("successor content inherited predecessor commitment")
	}
}

func TestPublishReplayConflictAndAmbiguousCommitRecovery(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service, _ := NewService(repository)
	now := time.Date(2026, 8, 27, 20, 0, 0, 0, time.UTC)
	command := publishFixture(t, "1", KindPlan, []byte("plan v1"), true, now)

	first, err := service.Publish(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Publish(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || first.Revision.ID() != second.Revision.ID() || first.RequestDigest != second.RequestDigest || repository.publishWrites != 1 {
		t.Fatalf("replay first=%#v second=%#v writes=%d", first, second, repository.publishWrites)
	}

	mutants := []struct {
		name   string
		mutate func(*PublishCommand)
	}{
		{"bytes", func(c *PublishCommand) { c.Content = []byte("plan changed") }},
		{"kind", func(c *PublishCommand) {
			c.Kind = KindSpecification
			policy, _ := PolicyFor(c.Kind)
			c.Validation = mustValidation(t, ValidationResultParams{ArtifactRevisionID: c.ArtifactRevisionID, Verdict: ValidationPass, Policy: policy, ValidatedAt: c.Validation.ValidatedAt()})
		}},
		{"source revision", func(c *PublishCommand) { c.SourceRevision = "commit-mutated" }},
		{"actor", func(c *PublishCommand) { c.CreatedBy = "actor-mutated" }},
		{"validation", func(c *PublishCommand) {
			policy, _ := PolicyFor(c.Kind)
			c.Validation = mustValidation(t, ValidationResultParams{ArtifactRevisionID: c.ArtifactRevisionID, Verdict: ValidationPass, Policy: policy, ValidatedAt: c.Validation.ValidatedAt().Add(time.Second)})
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			changed := command
			changed.Content = append([]byte(nil), command.Content...)
			mutant.mutate(&changed)
			_, publishErr := service.Publish(ctx, changed)
			assertErrorKind(t, publishErr, ErrorIdempotencyConflict)
			if repository.publishWrites != 1 {
				t.Fatalf("conflict mutated store: writes=%d", repository.publishWrites)
			}
		})
	}

	ambiguousRepository := newMemoryRepository()
	ambiguousRepository.ambiguousPublish = true
	ambiguousService, _ := NewService(ambiguousRepository)
	ambiguousCommand := publishFixture(t, "ambiguous", KindPlan, []byte("committed before reply"), true, now.Add(time.Hour))
	_, err = ambiguousService.Publish(ctx, ambiguousCommand)
	assertErrorKind(t, err, ErrorRepositoryFailure)
	recovered, err := ambiguousService.Publish(ctx, ambiguousCommand)
	if err != nil || !recovered.Replayed || ambiguousRepository.publishWrites != 1 {
		t.Fatalf("ambiguous replay result=%#v err=%v writes=%d", recovered, err, ambiguousRepository.publishWrites)
	}

	failingRepository := newMemoryRepository()
	failingRepository.failPublish = true
	failingService, _ := NewService(failingRepository)
	_, err = failingService.Publish(ctx, publishFixture(t, "failure", KindPlan, []byte("not committed"), true, now.Add(2*time.Hour)))
	assertErrorKind(t, err, ErrorRepositoryFailure)
	if strings.Contains(err.Error(), "secret=never-retain") {
		t.Fatal("repository cause leaked into bounded diagnostic")
	}
	if failingRepository.publishWrites != 0 || len(failingRepository.revisions) != 0 {
		t.Fatal("failed publication exposed partial state")
	}
}

func TestApprovalReplayConflictAndRequiredValidation(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service, _ := NewService(repository)
	now := time.Date(2026, 8, 27, 21, 0, 0, 0, time.UTC)
	revision := publishArtifact(t, ctx, service, repository, publishFixture(t, "1", KindPlan, []byte("plan"), true, now))
	command := approveFixture("1", revision.ID(), VerdictApproved, now.Add(time.Minute))
	first, err := service.Approve(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Approve(ctx, command)
	if err != nil || !second.Replayed || first.Approval.ID() != second.Approval.ID() || repository.approvalWrites != 1 {
		t.Fatalf("approval replay first=%#v second=%#v err=%v writes=%d", first, second, err, repository.approvalWrites)
	}

	for _, mutate := range []func(*ApproveCommand){
		func(c *ApproveCommand) { c.ActorID = "reviewer-2" },
		func(c *ApproveCommand) { c.Verdict = VerdictRejected },
		func(c *ApproveCommand) { c.EvidenceDigest = ContentCommitment([]byte("other evidence")) },
		func(c *ApproveCommand) { c.GovernanceContextID = "context-2" },
	} {
		changed := command
		mutate(&changed)
		_, err = service.Approve(ctx, changed)
		assertErrorKind(t, err, ErrorIdempotencyConflict)
	}
	if repository.approvalWrites != 1 {
		t.Fatal("approval conflict created another verdict")
	}

	delete(repository.validations, revision.ID())
	_, err = service.Approve(ctx, approveFixture("missing-validation", revision.ID(), VerdictRejected, now.Add(2*time.Minute)))
	assertErrorKind(t, err, ErrorValidationRequired)

	wrongPolicy, policyErr := PolicyFor(KindSpecification)
	if policyErr != nil {
		t.Fatal(policyErr)
	}
	repository.validations[revision.ID()] = mustValidation(t, ValidationResultParams{
		ArtifactRevisionID: revision.ID(), Verdict: ValidationPass, Policy: wrongPolicy, ValidatedAt: now,
	})
	_, err = service.Approve(ctx, approveFixture("wrong-policy", revision.ID(), VerdictRejected, now.Add(3*time.Minute)))
	assertErrorKind(t, err, ErrorPolicyUnsatisfied)
}

func TestConcurrentPublicationAllocatesUniqueRevisions(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service, _ := NewService(repository)
	now := time.Date(2026, 8, 27, 22, 0, 0, 0, time.UTC)
	const total = 32
	results := make(chan PublishResult, total)
	errorsSeen := make(chan error, total)
	var group sync.WaitGroup
	for index := 0; index < total; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			command := publishFixture(t, operationSuffix(index), KindIntent, []byte(operationSuffix(index)), true, now.Add(time.Duration(index)*time.Second))
			result, err := service.Publish(ctx, command)
			if err != nil {
				errorsSeen <- err
				return
			}
			results <- result
		}(index)
	}
	group.Wait()
	close(results)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	numbers := make([]int, 0, total)
	for result := range results {
		numbers = append(numbers, int(result.Revision.RevisionNumber()))
	}
	sort.Ints(numbers)
	if len(numbers) != total {
		t.Fatalf("results=%d", len(numbers))
	}
	for index, number := range numbers {
		if number != index+1 {
			t.Fatalf("numbers=%v", numbers)
		}
	}
}

func TestFreshnessIsPureAndExactIdentityOnly(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	service, _ := NewService(repository)
	now := time.Date(2026, 8, 27, 23, 0, 0, 0, time.UTC)
	revision1 := publishArtifact(t, ctx, service, repository, publishFixture(t, "1", KindIntent, []byte("same"), true, now))
	approveArtifact(t, ctx, service, approveFixture("1", revision1.ID(), VerdictApproved, now.Add(time.Second)))
	observation, err := service.ObserveCurrentApproval(ctx, revision1.MissionID(), revision1.Kind())
	if err != nil || observation.CurrentRevision().ID() != revision1.ID() || len(observation.Approvals()) != 1 {
		t.Fatalf("aggregate current approval observation=%+v err=%v", observation, err)
	}
	callerApprovals := observation.Approvals()
	callerApprovals[0] = ArtifactApproval{}
	if observation.Approvals()[0].ID() == "" {
		t.Fatal("current approval observation exposed caller-mutable approval storage")
	}
	revision2Command := publishFixture(t, "2", KindIntent, []byte("same"), true, now.Add(2*time.Second))
	revision2Command.SourceRef = revision1.SourceRef()
	revision2 := publishArtifact(t, ctx, service, repository, revision2Command)
	beforePublish, beforeApproval := repository.publishWrites, repository.approvalWrites
	for iteration := 0; iteration < 100; iteration++ {
		result, err := service.CurrentFreshness(ctx, revision2.MissionID(), revision2.Kind())
		if err != nil || result.Status != FreshnessStale || result.ApprovalID != "" {
			t.Fatalf("iteration=%d result=%#v err=%v", iteration, result, err)
		}
	}
	if repository.publishWrites != beforePublish || repository.approvalWrites != beforeApproval {
		t.Fatal("freshness query wrote durable state")
	}
	if repository.reads == 0 {
		t.Fatal("freshness did not use repository reads")
	}
}

func TestStaleFreshnessIsCanonicalAcrossApprovalOrder(t *testing.T) {
	now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	approvalA := mustApproval(t, ArtifactApprovalParams{
		ID: "approval-a", ArtifactRevisionID: "artifact-a", Verdict: VerdictApproved,
		ActorID: "reviewer-1", EvidenceDigest: ContentCommitment([]byte("a")),
		GovernanceContextID: "context-1", SubmittedAt: now,
	})
	approvalB := mustApproval(t, ArtifactApprovalParams{
		ID: "approval-b", ArtifactRevisionID: "artifact-b", Verdict: VerdictApproved,
		ActorID: "reviewer-2", EvidenceDigest: ContentCommitment([]byte("b")),
		GovernanceContextID: "context-2", SubmittedAt: now.Add(time.Second),
	})
	left, err := EvaluateFreshness("artifact-required", []ArtifactApproval{approvalA, approvalB})
	if err != nil {
		t.Fatal(err)
	}
	right, err := EvaluateFreshness("artifact-required", []ArtifactApproval{approvalB, approvalA})
	if err != nil {
		t.Fatal(err)
	}
	if left != right || left.Status != FreshnessStale || left.ApprovalID != "" {
		t.Fatalf("stale result depends on unrelated approval order: left=%#v right=%#v", left, right)
	}
	exactApproved, err := EvaluateFreshness("artifact-a", []ArtifactApproval{approvalB, approvalA})
	if err != nil || exactApproved.Status != FreshnessFresh || exactApproved.ApprovalID != approvalA.ID() {
		t.Fatalf("exact approval rule changed: result=%#v err=%v", exactApproved, err)
	}
	rejection := mustApproval(t, ArtifactApprovalParams{
		ID: "approval-required", ArtifactRevisionID: "artifact-required", Verdict: VerdictRejected,
		ActorID: "reviewer-3", EvidenceDigest: ContentCommitment([]byte("rejected")),
		GovernanceContextID: "context-3", SubmittedAt: now.Add(2 * time.Second),
	})
	exactRejected, err := EvaluateFreshness("artifact-required", []ArtifactApproval{approvalA, approvalB, rejection})
	if err != nil || exactRejected.Status != FreshnessRejected || exactRejected.ApprovalID != rejection.ID() {
		t.Fatalf("exact rejection rule changed: result=%#v err=%v", exactRejected, err)
	}
}

func TestRequestCommitmentKnownVectors(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 34, 56, 0, time.UTC)
	command := publishFixture(t, "vector", KindIntent, []byte("abc"), true, now)
	request, err := preparePublish(command)
	if err != nil {
		t.Fatal(err)
	}
	if PublishRequestCommitmentAlgorithm != "sha256:spec-kitty-artifact-publish-request-v1" || request.RequestDigest != "cf221af475d7678fbd5e39cf6d7c2c72851632c0c8b7cc879ebecc94d6a499ec" {
		t.Fatalf("publish request vector drifted: algorithm=%q digest=%s", PublishRequestCommitmentAlgorithm, request.RequestDigest)
	}
	approval, err := prepareApproval(ApproveCommand{
		OperationID: "approve-vector", ApprovalID: "approval-vector", ArtifactRevisionID: "artifact-vector",
		Verdict: VerdictApproved, ActorID: "reviewer-1", EvidenceDigest: ContentCommitment([]byte("abc")),
		GovernanceContextID: "context-1", SubmittedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ApprovalRequestCommitmentAlgorithm != "sha256:spec-kitty-artifact-approval-request-v1" || approval.RequestDigest != "3647a4ce4e5326688ca71329ce240358c600ee34b2803afd40f7917f79b91e1d" {
		t.Fatalf("approval request vector drifted: algorithm=%q digest=%s", ApprovalRequestCommitmentAlgorithm, approval.RequestDigest)
	}
	current, err := NewCurrentApprovalEvidence("mission-vector", KindSpecification, "artifact-vector", "approval-vector")
	if err != nil {
		t.Fatal(err)
	}
	if CurrentApprovalCommitmentAlgorithm != "sha256:spec-kitty-current-artifact-approval-v1" || current.Commitment() != "2e1b7051323e1ae805942bc62fc2457d1c46dd6fad6cbb521a82c2d1c2346d17" {
		t.Fatalf("current approval vector drifted: algorithm=%q digest=%s", CurrentApprovalCommitmentAlgorithm, current.Commitment())
	}
	for _, mutant := range []CurrentApprovalEvidence{
		mustCurrentApprovalEvidence(t, "mission-other", KindSpecification, "artifact-vector", "approval-vector"),
		mustCurrentApprovalEvidence(t, "mission-vector", KindPlan, "artifact-vector", "approval-vector"),
		mustCurrentApprovalEvidence(t, "mission-vector", KindSpecification, "artifact-other", "approval-vector"),
		mustCurrentApprovalEvidence(t, "mission-vector", KindSpecification, "artifact-vector", "approval-other"),
	} {
		if mutant.Commitment() == current.Commitment() {
			t.Fatalf("semantic current approval mutation retained commitment: %+v", mutant)
		}
	}
}

func mustCurrentApprovalEvidence(t *testing.T, missionID string, kind Kind, revisionID, approvalID string) CurrentApprovalEvidence {
	t.Helper()
	evidence, err := NewCurrentApprovalEvidence(missionID, kind, revisionID, approvalID)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

type memoryRepository struct {
	mu                 sync.Mutex
	revisions          map[string]ArtifactRevision
	validations        map[string]ValidationResult
	current            map[string]string
	approvals          map[string][]ArtifactApproval
	publishOperations  map[string]PublishResult
	approvalOperations map[string]ApprovalResult
	publishWrites      int
	approvalWrites     int
	reads              int
	failPublish        bool
	ambiguousPublish   bool
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		revisions: make(map[string]ArtifactRevision), validations: make(map[string]ValidationResult),
		current: make(map[string]string), approvals: make(map[string][]ArtifactApproval),
		publishOperations: make(map[string]PublishResult), approvalOperations: make(map[string]ApprovalResult),
	}
}

func (r *memoryRepository) PublishAtomic(_ context.Context, request AtomicPublishRequest) (PublishResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.publishOperations[request.OperationID]; ok {
		if existing.RequestDigest != request.RequestDigest {
			return PublishResult{}, diagnostic(ErrorIdempotencyConflict, "operation_id", RecoveryResolveConflict, "publication operation was reused with another request", nil)
		}
		existing.Replayed = true
		return existing, nil
	}
	if r.failPublish {
		return PublishResult{}, errors.New("injected store failure containing secret=never-retain")
	}
	if _, exists := r.revisions[request.ArtifactRevisionID]; exists {
		return PublishResult{}, diagnostic(ErrorIdempotencyConflict, "artifact_revision_id", RecoveryResolveConflict, "artifact revision identity already exists", nil)
	}
	key := artifactKey(request.MissionID, request.Kind)
	next := uint64(1)
	if currentID := r.current[key]; currentID != "" {
		next = r.revisions[currentID].RevisionNumber() + 1
	}
	revision, err := NewArtifactRevision(ArtifactRevisionParams{
		ID: request.ArtifactRevisionID, MissionID: request.MissionID, Kind: request.Kind,
		RevisionNumber: next, ContentDigest: request.ContentDigest, SourceRef: request.SourceRef,
		SourceRevision: request.SourceRevision, CreatedBy: request.CreatedBy, CreatedAt: request.CreatedAt,
	})
	if err != nil {
		return PublishResult{}, err
	}
	result := PublishResult{OperationID: request.OperationID, RequestDigest: request.RequestDigest, Revision: revision, Validation: request.Validation}
	r.revisions[revision.ID()] = revision
	r.validations[revision.ID()] = request.Validation
	r.current[key] = revision.ID()
	r.publishOperations[request.OperationID] = result
	r.publishWrites++
	if r.ambiguousPublish {
		r.ambiguousPublish = false
		return PublishResult{}, errors.New("injected ambiguous return")
	}
	return result, nil
}

func (r *memoryRepository) Revision(_ context.Context, revisionID string) (ArtifactRevision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	revision, ok := r.revisions[revisionID]
	if !ok {
		return ArtifactRevision{}, diagnostic(ErrorNotFound, "artifact_revision_id", RecoveryInspectState, "artifact revision was not found", nil)
	}
	return revision, nil
}

func (r *memoryRepository) CurrentRevision(_ context.Context, missionID string, kind Kind) (ArtifactRevision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	revisionID := r.current[artifactKey(missionID, kind)]
	if revisionID == "" {
		return ArtifactRevision{}, diagnostic(ErrorNotFound, "artifact_revision_id", RecoveryInspectState, "current artifact revision was not found", nil)
	}
	return r.revisions[revisionID], nil
}

func (r *memoryRepository) ObserveCurrentApproval(_ context.Context, missionID string, kind Kind) (CurrentApprovalObservation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	revisionID := r.current[artifactKey(missionID, kind)]
	if revisionID == "" {
		return CurrentApprovalObservation{}, diagnostic(ErrorNotFound, "artifact_revision_id", RecoveryInspectState, "current artifact revision was not found", nil)
	}
	return NewCurrentApprovalObservation(r.revisions[revisionID], r.approvals[artifactKey(missionID, kind)])
}

func (r *memoryRepository) Validation(_ context.Context, revisionID string) (ValidationResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	validation, ok := r.validations[revisionID]
	if !ok {
		return ValidationResult{}, diagnostic(ErrorNotFound, "validation", RecoveryProvideEvidence, "artifact validation was not found", nil)
	}
	return validation, nil
}

func (r *memoryRepository) ApproveAtomic(_ context.Context, request AtomicApprovalRequest) (ApprovalResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.approvalOperations[request.OperationID]; ok {
		if existing.RequestDigest != request.RequestDigest {
			return ApprovalResult{}, diagnostic(ErrorIdempotencyConflict, "operation_id", RecoveryResolveConflict, "approval operation was reused with another request", nil)
		}
		existing.Replayed = true
		return existing, nil
	}
	revision, exists := r.revisions[request.ArtifactRevisionID]
	if !exists {
		return ApprovalResult{}, diagnostic(ErrorNotFound, "artifact_revision_id", RecoveryInspectState, "artifact revision was not found", nil)
	}
	key := artifactKey(revision.MissionID(), revision.Kind())
	for _, approval := range r.approvals[key] {
		if approval.ArtifactRevisionID() == request.ArtifactRevisionID {
			return ApprovalResult{}, diagnostic(ErrorIdempotencyConflict, "artifact_revision_id", RecoveryResolveConflict, "artifact revision already has an immutable verdict", nil)
		}
	}
	approval, err := NewArtifactApproval(ArtifactApprovalParams{
		ID: request.ApprovalID, ArtifactRevisionID: request.ArtifactRevisionID,
		Verdict: request.Verdict, ActorID: request.ActorID, EvidenceDigest: request.EvidenceDigest,
		GovernanceContextID: request.GovernanceContextID, SubmittedAt: request.SubmittedAt,
	})
	if err != nil {
		return ApprovalResult{}, err
	}
	result := ApprovalResult{OperationID: request.OperationID, RequestDigest: request.RequestDigest, Approval: approval}
	r.approvals[key] = append(r.approvals[key], approval)
	r.approvalOperations[request.OperationID] = result
	r.approvalWrites++
	return result, nil
}

func (r *memoryRepository) Approvals(_ context.Context, missionID string, kind Kind) ([]ArtifactApproval, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return append([]ArtifactApproval(nil), r.approvals[artifactKey(missionID, kind)]...), nil
}

func artifactKey(missionID string, kind Kind) string { return missionID + "\x00" + string(kind) }

func publishFixture(t *testing.T, suffix string, kind Kind, content []byte, pass bool, now time.Time) PublishCommand {
	t.Helper()
	revisionID := "artifact-" + suffix
	policy, err := PolicyFor(kind)
	if err != nil {
		t.Fatal(err)
	}
	verdict := ValidationPass
	var findings []Finding
	if !pass {
		verdict = ValidationFail
		findings = []Finding{mustFinding(t, "missing_required_concern", "scope", "required structural concern is absent", RecoveryProvideEvidence)}
	}
	validation := mustValidation(t, ValidationResultParams{
		ArtifactRevisionID: revisionID, Verdict: verdict, Findings: findings, Policy: policy, ValidatedAt: now,
	})
	return PublishCommand{
		OperationID: "publish-" + suffix, ArtifactRevisionID: revisionID, MissionID: "mission-1",
		Kind: kind, SourceRef: "specs/" + suffix + ".md", SourceRevision: "commit-" + suffix,
		CreatedBy: "actor-1", CreatedAt: now, Content: append([]byte(nil), content...), Validation: validation,
	}
}

func approveFixture(suffix, revisionID string, verdict ApprovalVerdict, now time.Time) ApproveCommand {
	return ApproveCommand{
		OperationID: "approve-" + suffix, ApprovalID: "approval-" + suffix,
		ArtifactRevisionID: revisionID, Verdict: verdict, ActorID: "reviewer-1",
		EvidenceDigest:      ContentCommitment([]byte("evidence-" + suffix)),
		GovernanceContextID: "context-1", SubmittedAt: now,
	}
}

func publishArtifact(t *testing.T, ctx context.Context, service *Service, repository *memoryRepository, command PublishCommand) ArtifactRevision {
	t.Helper()
	result, err := service.Publish(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if repository.revisions[result.Revision.ID()].ID() == "" || repository.validations[result.Revision.ID()].ArtifactRevisionID() == "" {
		t.Fatal("publication did not atomically expose revision and validation")
	}
	return result.Revision
}

func approveArtifact(t *testing.T, ctx context.Context, service *Service, command ApproveCommand) ArtifactApproval {
	t.Helper()
	result, err := service.Approve(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	return result.Approval
}

func assertFreshness(t *testing.T, ctx context.Context, service *Service, revisionID string, status Freshness, approvalID string) {
	t.Helper()
	result, err := service.Freshness(ctx, revisionID)
	if err != nil || result.Status != status || result.RequiredRevisionID != revisionID || result.ApprovalID != approvalID {
		t.Fatalf("freshness=%#v err=%v want_status=%s want_approval=%s", result, err, status, approvalID)
	}
}

func operationSuffix(index int) string {
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	if index < len(alphabet) {
		return string(alphabet[index])
	}
	return "x" + string(alphabet[index%len(alphabet)])
}
