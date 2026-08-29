package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	PublishRequestCommitmentAlgorithm  = "sha256:spec-kitty-artifact-publish-request-v2"
	ApprovalRequestCommitmentAlgorithm = "sha256:spec-kitty-artifact-approval-request-v1"
	CurrentApprovalCommitmentAlgorithm = "sha256:spec-kitty-current-artifact-approval-v1"
	publishRequestDomain               = "spec-kitty/artifact-publish-request/v2\x00"
	approvalRequestDomain              = "spec-kitty/artifact-approval-request/v1\x00"
	currentApprovalDomain              = "spec-kitty/current-artifact-approval/v1\x00"
)

// Repository is a consumer-owned aggregate port. PublishAtomic must allocate
// the next positive per-mission/per-kind revision and commit the revision plus
// validation as one operation. ApproveAtomic must append one exact-revision
// verdict. Both operations must implement: same OperationID + same
// RequestDigest replays the complete original result; same OperationID + a
// different digest returns ErrorIdempotencyConflict without mutation.
//
// Adapter implementations must also prevent two concurrent publishers from
// receiving the same revision number and prevent contradictory verdicts for a
// revision. Durable implementation and crash injection belong to WP08, not
// this portable package.
type Repository interface {
	PublishAtomic(context.Context, AtomicPublishRequest) (PublishResult, error)
	Revision(context.Context, string) (ArtifactRevision, error)
	CurrentRevision(context.Context, string, Kind) (ArtifactRevision, error)
	// ObserveCurrentApproval returns the current revision and its approval
	// history from one atomic repository observation.
	ObserveCurrentApproval(context.Context, string, Kind) (CurrentApprovalObservation, error)
	Validation(context.Context, string) (ValidationResult, error)
	ApproveAtomic(context.Context, AtomicApprovalRequest) (ApprovalResult, error)
	Approvals(context.Context, string, Kind) ([]ArtifactApproval, error)
}

// CurrentApprovalObservation is one immutable aggregate read of the current
// revision and the approval history in the same repository snapshot.
type CurrentApprovalObservation struct {
	revision  ArtifactRevision
	approvals []ArtifactApproval
}

func NewCurrentApprovalObservation(revision ArtifactRevision, approvals []ArtifactApproval) (CurrentApprovalObservation, error) {
	if revision.ID() == "" || revision.MissionID() == "" || revision.Kind().Validate() != nil {
		return CurrentApprovalObservation{}, diagnostic(ErrorRepositoryFailure, "current_approval", RecoveryInspectState, "current approval observation contains an invalid revision", nil)
	}
	for _, approval := range approvals {
		if approval.ID() == "" || approval.ArtifactRevisionID() == "" || approval.Verdict().Validate() != nil {
			return CurrentApprovalObservation{}, diagnostic(ErrorRepositoryFailure, "current_approval", RecoveryInspectState, "current approval observation contains an invalid approval", nil)
		}
	}
	return CurrentApprovalObservation{revision: revision, approvals: append([]ArtifactApproval(nil), approvals...)}, nil
}

func (o CurrentApprovalObservation) CurrentRevision() ArtifactRevision {
	return o.revision
}

func (o CurrentApprovalObservation) Approvals() []ArtifactApproval {
	return append([]ArtifactApproval(nil), o.approvals...)
}

// CurrentApprovalEvidence is the immutable current-revision/approval binding
// carried into a mission transition's atomic commit precondition.
type CurrentApprovalEvidence struct {
	missionID          string
	kind               Kind
	artifactRevisionID string
	approvalID         string
	commitment         string
}

func NewCurrentApprovalEvidence(missionID string, kind Kind, artifactRevisionID, approvalID string) (CurrentApprovalEvidence, error) {
	if err := validateIdentity("mission_id", missionID); err != nil {
		return CurrentApprovalEvidence{}, err
	}
	if err := kind.Validate(); err != nil {
		return CurrentApprovalEvidence{}, err
	}
	if err := validateIdentity("artifact_revision_id", artifactRevisionID); err != nil {
		return CurrentApprovalEvidence{}, err
	}
	if err := validateIdentity("approval_id", approvalID); err != nil {
		return CurrentApprovalEvidence{}, err
	}
	commitment, err := canonicalDigest(currentApprovalDomain, struct {
		MissionID          string `json:"mission_id"`
		Kind               Kind   `json:"kind"`
		ArtifactRevisionID string `json:"artifact_revision_id"`
		ApprovalID         string `json:"approval_id"`
	}{missionID, kind, artifactRevisionID, approvalID})
	if err != nil {
		return CurrentApprovalEvidence{}, err
	}
	return CurrentApprovalEvidence{
		missionID: missionID, kind: kind, artifactRevisionID: artifactRevisionID,
		approvalID: approvalID, commitment: commitment,
	}, nil
}

func (e CurrentApprovalEvidence) MissionID() string          { return e.missionID }
func (e CurrentApprovalEvidence) Kind() Kind                 { return e.kind }
func (e CurrentApprovalEvidence) ArtifactRevisionID() string { return e.artifactRevisionID }
func (e CurrentApprovalEvidence) ApprovalID() string         { return e.approvalID }
func (e CurrentApprovalEvidence) Commitment() string         { return e.commitment }

func (e CurrentApprovalEvidence) Validate() error {
	restored, err := NewCurrentApprovalEvidence(e.missionID, e.kind, e.artifactRevisionID, e.approvalID)
	if err != nil || restored.commitment != e.commitment {
		return diagnostic(ErrorInvalidValidation, "current_approval", RecoveryInspectState, "current approval evidence commitment is invalid", err)
	}
	return nil
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, diagnostic(ErrorInvalidCommand, "repository", RecoveryCorrectInput, "artifact repository is required", nil)
	}
	return &Service{repository: repository}, nil
}

// PublishCommand carries raw content only to the service boundary. The service
// replaces it with a commitment before invoking persistence.
type PublishCommand struct {
	OperationID        string
	ArtifactRevisionID string
	MissionID          string
	Kind               Kind
	SourceRef          string
	SourceRevision     string
	CreatedBy          string
	CreatedAt          time.Time
	Content            []byte
	Validation         ValidationResult
	// ExpectedLatestRevisionID is the exact current revision observed by the
	// caller. Empty means the caller observed no prior revision.
	ExpectedLatestRevisionID string
}

// AtomicPublishRequest is the complete persistence envelope and contains no
// artifact bytes. The repository assigns RevisionNumber atomically.
type AtomicPublishRequest struct {
	OperationID              string
	RequestDigest            string
	ArtifactRevisionID       string
	MissionID                string
	Kind                     Kind
	ContentDigest            string
	SourceRef                string
	SourceRevision           string
	CreatedBy                string
	CreatedAt                time.Time
	Validation               ValidationResult
	ExpectedLatestRevisionID string
}

type PublishResult struct {
	OperationID   string
	RequestDigest string
	Revision      ArtifactRevision
	Validation    ValidationResult
	Replayed      bool
}

func (s *Service) Publish(ctx context.Context, command PublishCommand) (PublishResult, error) {
	request, err := preparePublish(command)
	if err != nil {
		return PublishResult{}, err
	}
	result, err := s.repository.PublishAtomic(ctx, request)
	if err != nil {
		return PublishResult{}, repositoryError("publish", err)
	}
	if err := validatePublishResult(request, result); err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

func preparePublish(command PublishCommand) (AtomicPublishRequest, error) {
	if err := validateIdentity("operation_id", command.OperationID); err != nil {
		return AtomicPublishRequest{}, err
	}
	if err := validateIdentity("artifact_revision_id", command.ArtifactRevisionID); err != nil {
		return AtomicPublishRequest{}, err
	}
	if err := validateIdentity("mission_id", command.MissionID); err != nil {
		return AtomicPublishRequest{}, err
	}
	if command.ExpectedLatestRevisionID != "" {
		if err := validateIdentity("expected_latest_revision_id", command.ExpectedLatestRevisionID); err != nil {
			return AtomicPublishRequest{}, err
		}
	}
	if err := command.Kind.Validate(); err != nil {
		return AtomicPublishRequest{}, err
	}
	if err := validateSourceRef(command.SourceRef); err != nil {
		return AtomicPublishRequest{}, err
	}
	if err := validateIdentity("source_revision", command.SourceRevision); err != nil {
		return AtomicPublishRequest{}, diagnostic(ErrorInvalidProvenance, "source_revision", RecoveryCorrectInput, "source revision must be a stable identity", err)
	}
	if err := validateIdentity("created_by", command.CreatedBy); err != nil {
		return AtomicPublishRequest{}, diagnostic(ErrorInvalidProvenance, "created_by", RecoveryCorrectInput, "creator must be a stable identity", err)
	}
	createdAt, err := normalizeTime("created_at", command.CreatedAt)
	if err != nil {
		return AtomicPublishRequest{}, err
	}
	if command.Validation.ArtifactRevisionID() != command.ArtifactRevisionID {
		return AtomicPublishRequest{}, diagnostic(ErrorInvalidValidation, "validation.artifact_revision_id", RecoveryCorrectInput, "validation must name the revision being published", nil)
	}
	expectedPolicy, err := PolicyFor(command.Kind)
	if err != nil {
		return AtomicPublishRequest{}, err
	}
	if !samePolicy(command.Validation.Policy(), expectedPolicy) {
		return AtomicPublishRequest{}, diagnostic(ErrorPolicyUnsatisfied, "validation.policy", RecoveryProvideEvidence, "validation policy does not match artifact kind", nil)
	}
	contentDigest := ContentCommitment(command.Content)
	request := AtomicPublishRequest{
		OperationID: command.OperationID, ArtifactRevisionID: command.ArtifactRevisionID,
		MissionID: command.MissionID, Kind: command.Kind, ContentDigest: contentDigest,
		SourceRef: command.SourceRef, SourceRevision: command.SourceRevision,
		CreatedBy: command.CreatedBy, CreatedAt: createdAt, Validation: command.Validation,
		ExpectedLatestRevisionID: command.ExpectedLatestRevisionID,
	}
	request.RequestDigest, err = publishRequestDigest(request)
	if err != nil {
		return AtomicPublishRequest{}, err
	}
	return request, nil
}

func publishRequestDigest(request AtomicPublishRequest) (string, error) {
	validationDigest, err := request.Validation.Commitment()
	if err != nil {
		return "", err
	}
	identity := struct {
		ArtifactRevisionID       string    `json:"artifact_revision_id"`
		MissionID                string    `json:"mission_id"`
		Kind                     Kind      `json:"kind"`
		ContentDigest            string    `json:"content_digest"`
		SourceRef                string    `json:"source_ref"`
		SourceRevision           string    `json:"source_revision"`
		CreatedBy                string    `json:"created_by"`
		CreatedAt                time.Time `json:"created_at"`
		ValidationDigest         string    `json:"validation_digest"`
		ExpectedLatestRevisionID string    `json:"expected_latest_revision_id"`
	}{
		ArtifactRevisionID: request.ArtifactRevisionID, MissionID: request.MissionID,
		Kind: request.Kind, ContentDigest: request.ContentDigest, SourceRef: request.SourceRef,
		SourceRevision: request.SourceRevision, CreatedBy: request.CreatedBy,
		CreatedAt: request.CreatedAt.UTC(), ValidationDigest: validationDigest,
		ExpectedLatestRevisionID: request.ExpectedLatestRevisionID,
	}
	return canonicalDigest(publishRequestDomain, identity)
}

func validatePublishResult(request AtomicPublishRequest, result PublishResult) error {
	if result.OperationID != request.OperationID || result.RequestDigest != request.RequestDigest {
		return diagnostic(ErrorIdempotencyConflict, "operation_id", RecoveryResolveConflict, "repository returned a result for another publication request", nil)
	}
	revision := result.Revision
	if revision.ID() != request.ArtifactRevisionID || revision.MissionID() != request.MissionID ||
		revision.Kind() != request.Kind || revision.ContentDigest() != request.ContentDigest ||
		revision.SourceRef() != request.SourceRef || revision.SourceRevision() != request.SourceRevision ||
		revision.CreatedBy() != request.CreatedBy || !revision.CreatedAt().Equal(request.CreatedAt) ||
		revision.RevisionNumber() == 0 {
		return diagnostic(ErrorRepositoryFailure, "publication", RecoveryInspectState, "repository returned publication evidence that does not match the command", nil)
	}
	expectedValidation, err := request.Validation.Commitment()
	if err != nil {
		return err
	}
	actualValidation, err := result.Validation.Commitment()
	if err != nil || actualValidation != expectedValidation || result.Validation.ArtifactRevisionID() != revision.ID() {
		return diagnostic(ErrorRepositoryFailure, "validation", RecoveryInspectState, "repository returned incomplete or mismatched validation evidence", nil)
	}
	return nil
}

// ApproveCommand appends one immutable verdict to one exact revision.
type ApproveCommand struct {
	OperationID         string
	ApprovalID          string
	ArtifactRevisionID  string
	Verdict             ApprovalVerdict
	ActorID             string
	EvidenceDigest      string
	GovernanceContextID string
	SubmittedAt         time.Time
}

type AtomicApprovalRequest struct {
	OperationID         string
	RequestDigest       string
	ApprovalID          string
	ArtifactRevisionID  string
	Verdict             ApprovalVerdict
	ActorID             string
	EvidenceDigest      string
	GovernanceContextID string
	SubmittedAt         time.Time
}

type ApprovalResult struct {
	OperationID   string
	RequestDigest string
	Approval      ArtifactApproval
	Replayed      bool
}

func (s *Service) Approve(ctx context.Context, command ApproveCommand) (ApprovalResult, error) {
	request, err := prepareApproval(command)
	if err != nil {
		return ApprovalResult{}, err
	}
	revision, err := s.repository.Revision(ctx, request.ArtifactRevisionID)
	if err != nil {
		return ApprovalResult{}, repositoryError("load revision", err)
	}
	validation, err := s.repository.Validation(ctx, request.ArtifactRevisionID)
	if err != nil {
		if IsKind(err, ErrorNotFound) {
			return ApprovalResult{}, diagnostic(ErrorValidationRequired, "validation", RecoveryProvideEvidence, "artifact validation is required before a verdict", nil)
		}
		return ApprovalResult{}, repositoryError("load validation", err)
	}
	if validation.ArtifactRevisionID() != revision.ID() {
		return ApprovalResult{}, diagnostic(ErrorValidationRequired, "validation.artifact_revision_id", RecoveryProvideEvidence, "validation does not name the target revision", nil)
	}
	expectedPolicy, policyErr := PolicyFor(revision.Kind())
	if policyErr != nil || !samePolicy(validation.Policy(), expectedPolicy) {
		return ApprovalResult{}, diagnostic(ErrorPolicyUnsatisfied, "validation.policy", RecoveryProvideEvidence, "artifact validation does not satisfy the applicable policy", nil)
	}
	if request.Verdict == VerdictApproved && validation.Verdict() != ValidationPass {
		return ApprovalResult{}, diagnostic(ErrorApprovalNotAllowed, "verdict", RecoveryProvideEvidence, "failed validation cannot be approved", nil)
	}
	result, err := s.repository.ApproveAtomic(ctx, request)
	if err != nil {
		return ApprovalResult{}, repositoryError("approve", err)
	}
	if err := validateApprovalResult(request, result); err != nil {
		return ApprovalResult{}, err
	}
	return result, nil
}

func prepareApproval(command ApproveCommand) (AtomicApprovalRequest, error) {
	if err := validateIdentity("operation_id", command.OperationID); err != nil {
		return AtomicApprovalRequest{}, err
	}
	if err := validateIdentity("approval_id", command.ApprovalID); err != nil {
		return AtomicApprovalRequest{}, err
	}
	if err := validateIdentity("artifact_revision_id", command.ArtifactRevisionID); err != nil {
		return AtomicApprovalRequest{}, err
	}
	if err := command.Verdict.Validate(); err != nil {
		return AtomicApprovalRequest{}, err
	}
	if err := validateIdentity("actor_id", command.ActorID); err != nil {
		return AtomicApprovalRequest{}, diagnostic(ErrorInvalidProvenance, "actor_id", RecoveryCorrectInput, "approval actor must be a stable identity", err)
	}
	if err := validateDigest("evidence_digest", command.EvidenceDigest); err != nil {
		return AtomicApprovalRequest{}, err
	}
	if err := validateIdentity("governance_context_id", command.GovernanceContextID); err != nil {
		return AtomicApprovalRequest{}, diagnostic(ErrorInvalidProvenance, "governance_context_id", RecoveryCorrectInput, "governance context must be sealed by stable identity", err)
	}
	submittedAt, err := normalizeTime("submitted_at", command.SubmittedAt)
	if err != nil {
		return AtomicApprovalRequest{}, err
	}
	request := AtomicApprovalRequest{
		OperationID: command.OperationID, ApprovalID: command.ApprovalID,
		ArtifactRevisionID: command.ArtifactRevisionID, Verdict: command.Verdict,
		ActorID: command.ActorID, EvidenceDigest: command.EvidenceDigest,
		GovernanceContextID: command.GovernanceContextID, SubmittedAt: submittedAt,
	}
	request.RequestDigest, err = approvalRequestDigest(request)
	if err != nil {
		return AtomicApprovalRequest{}, err
	}
	return request, nil
}

func approvalRequestDigest(request AtomicApprovalRequest) (string, error) {
	identity := struct {
		ApprovalID          string          `json:"approval_id"`
		ArtifactRevisionID  string          `json:"artifact_revision_id"`
		Verdict             ApprovalVerdict `json:"verdict"`
		ActorID             string          `json:"actor_id"`
		EvidenceDigest      string          `json:"evidence_digest"`
		GovernanceContextID string          `json:"governance_context_id"`
		SubmittedAt         time.Time       `json:"submitted_at"`
	}{
		ApprovalID: request.ApprovalID, ArtifactRevisionID: request.ArtifactRevisionID,
		Verdict: request.Verdict, ActorID: request.ActorID, EvidenceDigest: request.EvidenceDigest,
		GovernanceContextID: request.GovernanceContextID, SubmittedAt: request.SubmittedAt.UTC(),
	}
	return canonicalDigest(approvalRequestDomain, identity)
}

func validateApprovalResult(request AtomicApprovalRequest, result ApprovalResult) error {
	if result.OperationID != request.OperationID || result.RequestDigest != request.RequestDigest {
		return diagnostic(ErrorIdempotencyConflict, "operation_id", RecoveryResolveConflict, "repository returned a result for another approval request", nil)
	}
	approval := result.Approval
	if approval.ID() != request.ApprovalID || approval.ArtifactRevisionID() != request.ArtifactRevisionID ||
		approval.Verdict() != request.Verdict || approval.ActorID() != request.ActorID ||
		approval.EvidenceDigest() != request.EvidenceDigest || approval.GovernanceContextID() != request.GovernanceContextID ||
		!approval.SubmittedAt().Equal(request.SubmittedAt) {
		return diagnostic(ErrorRepositoryFailure, "approval", RecoveryInspectState, "repository returned approval evidence that does not match the command", nil)
	}
	return nil
}

// Freshness is a pure classification for the exact required revision.
type Freshness string

const (
	FreshnessFresh    Freshness = "fresh"
	FreshnessMissing  Freshness = "missing"
	FreshnessRejected Freshness = "rejected"
	FreshnessStale    Freshness = "stale"
)

type FreshnessResult struct {
	Status             Freshness
	RequiredRevisionID string
	ApprovalID         string
}

// Freshness performs reads only. Same kind, path, revision number, or digest
// never substitutes for exact artifact revision identity.
func (s *Service) Freshness(ctx context.Context, requiredRevisionID string) (FreshnessResult, error) {
	if err := validateIdentity("artifact_revision_id", requiredRevisionID); err != nil {
		return FreshnessResult{}, err
	}
	revision, err := s.repository.Revision(ctx, requiredRevisionID)
	if err != nil {
		return FreshnessResult{}, repositoryError("load required revision", err)
	}
	approvals, err := s.repository.Approvals(ctx, revision.MissionID(), revision.Kind())
	if err != nil {
		return FreshnessResult{}, repositoryError("load approvals", err)
	}
	return EvaluateFreshness(requiredRevisionID, approvals)
}

// ObserveCurrentApproval returns one repository-atomic current-revision and
// approval observation suitable for a later commit precondition.
func (s *Service) ObserveCurrentApproval(ctx context.Context, missionID string, kind Kind) (CurrentApprovalObservation, error) {
	if err := validateIdentity("mission_id", missionID); err != nil {
		return CurrentApprovalObservation{}, err
	}
	if err := kind.Validate(); err != nil {
		return CurrentApprovalObservation{}, err
	}
	observation, err := s.repository.ObserveCurrentApproval(ctx, missionID, kind)
	if err != nil {
		return CurrentApprovalObservation{}, repositoryError("observe current approval", err)
	}
	revision := observation.CurrentRevision()
	if revision.MissionID() != missionID || revision.Kind() != kind {
		return CurrentApprovalObservation{}, diagnostic(ErrorRepositoryFailure, "current_approval", RecoveryInspectState, "current approval observation is outside the requested scope", nil)
	}
	if _, err := NewCurrentApprovalObservation(revision, observation.Approvals()); err != nil {
		return CurrentApprovalObservation{}, err
	}
	return observation, nil
}

// CurrentFreshness applies exact freshness to one aggregate observation. It
// remains read-only and cannot mix a revision with approvals from another
// repository snapshot.
func (s *Service) CurrentFreshness(ctx context.Context, missionID string, kind Kind) (FreshnessResult, error) {
	observation, err := s.ObserveCurrentApproval(ctx, missionID, kind)
	if err != nil {
		return FreshnessResult{}, err
	}
	revision := observation.CurrentRevision()
	return EvaluateFreshness(revision.ID(), observation.Approvals())
}

func EvaluateFreshness(requiredRevisionID string, approvals []ArtifactApproval) (FreshnessResult, error) {
	if err := validateIdentity("artifact_revision_id", requiredRevisionID); err != nil {
		return FreshnessResult{}, err
	}
	result := FreshnessResult{Status: FreshnessMissing, RequiredRevisionID: requiredRevisionID}
	exactVerdicts := 0
	for _, approval := range approvals {
		if approval.ID() == "" {
			return FreshnessResult{}, diagnostic(ErrorRepositoryFailure, "approvals", RecoveryInspectState, "repository returned an invalid approval", nil)
		}
		if approval.ArtifactRevisionID() == requiredRevisionID {
			exactVerdicts++
			if exactVerdicts > 1 {
				return FreshnessResult{}, diagnostic(ErrorRepositoryFailure, "approvals", RecoveryInspectState, "repository returned multiple verdicts for one revision", nil)
			}
			switch approval.Verdict() {
			case VerdictApproved:
				if result.Status == FreshnessRejected {
					return FreshnessResult{}, diagnostic(ErrorRepositoryFailure, "approvals", RecoveryInspectState, "repository returned contradictory verdicts", nil)
				}
				result.Status, result.ApprovalID = FreshnessFresh, approval.ID()
			case VerdictRejected:
				if result.Status == FreshnessFresh {
					return FreshnessResult{}, diagnostic(ErrorRepositoryFailure, "approvals", RecoveryInspectState, "repository returned contradictory verdicts", nil)
				}
				result.Status, result.ApprovalID = FreshnessRejected, approval.ID()
			default:
				return FreshnessResult{}, diagnostic(ErrorRepositoryFailure, "approvals", RecoveryInspectState, "repository returned an unsupported verdict", nil)
			}
			continue
		}
		if approval.Verdict() == VerdictApproved && result.Status == FreshnessMissing {
			result.Status = FreshnessStale
		}
	}
	return result, nil
}

func canonicalDigest(domain string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", diagnostic(ErrorInvalidCommand, "request", RecoveryCorrectInput, "request commitment could not be encoded", nil)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(domain))
	_, _ = hash.Write(encoded)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func repositoryError(operation string, err error) error {
	var typed *Error
	if errors.As(err, &typed) {
		if typed.Kind.Validate() == nil && typed.Recovery.Validate() == nil &&
			len(typed.Message) <= maxErrorMessageLength && !containsSensitiveEvidence(typed.Message) {
			return err
		}
	}
	return diagnostic(ErrorRepositoryFailure, "repository", RecoveryRetrySameOperation, fmt.Sprintf("artifact repository could not %s", operation), err)
}
