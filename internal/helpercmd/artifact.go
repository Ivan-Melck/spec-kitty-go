package helpercmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

type artifactPublishRequest struct {
	Database                 string             `json:"database"`
	CorrelationID            string             `json:"correlation_id"`
	OperationID              ids.OperationID    `json:"operation_id"`
	ArtifactRevisionID       string             `json:"artifact_revision_id"`
	MissionID                ids.MissionID      `json:"mission_id"`
	Kind                     artifact.Kind      `json:"kind"`
	SourceRef                string             `json:"source_ref"`
	SourceRevision           string             `json:"source_revision"`
	CreatedBy                ids.ActorID        `json:"created_by"`
	CreatedAt                time.Time          `json:"created_at"`
	Content                  []byte             `json:"content"`
	PresentConcerns          []artifact.Concern `json:"present_concerns"`
	ValidatedAt              time.Time          `json:"validated_at"`
	ExpectedLatestRevisionID string             `json:"expected_latest_revision_id"`
}

type artifactApproveRequest struct {
	Database            string                   `json:"database"`
	CorrelationID       string                   `json:"correlation_id"`
	OperationID         ids.OperationID          `json:"operation_id"`
	ApprovalID          string                   `json:"approval_id"`
	ArtifactRevisionID  string                   `json:"artifact_revision_id"`
	Verdict             artifact.ApprovalVerdict `json:"verdict"`
	ActorID             ids.ActorID              `json:"actor_id"`
	EvidenceDigest      string                   `json:"evidence_digest"`
	GovernanceContextID ids.GovernanceContextID  `json:"governance_context_id"`
	SubmittedAt         time.Time                `json:"submitted_at"`
}

type artifactRevisionResult struct {
	ID             string        `json:"artifact_revision_id"`
	MissionID      string        `json:"mission_id"`
	Kind           artifact.Kind `json:"kind"`
	RevisionNumber uint64        `json:"revision_number"`
	ContentDigest  string        `json:"content_digest"`
	SourceRef      string        `json:"source_ref"`
	SourceRevision string        `json:"source_revision"`
	CreatedBy      string        `json:"created_by"`
	CreatedAt      time.Time     `json:"created_at"`
}

func RunArtifact(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: artifact publish|approve --request-json <json>")
		return 2
	}
	switch args[0] {
	case "publish":
		var request artifactPublishRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runArtifactPublish(ctx, request, stdout)
	case "approve":
		var request artifactApproveRequest
		if code := decodeMethodRequest(args[1:], &request, stdout, stderr); code >= 0 {
			return code
		}
		return runArtifactApprove(ctx, request, stdout)
	default:
		fmt.Fprintln(stderr, "usage: artifact publish|approve --request-json <json>")
		return 2
	}
}

func runArtifactPublish(ctx context.Context, request artifactPublishRequest, stdout io.Writer) int {
	policy, err := artifact.PolicyFor(request.Kind)
	if err != nil {
		return encodeMethodFailure(stdout, "artifact_publish_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	validation, err := artifact.ValidateConcerns(request.ArtifactRevisionID, policy, request.PresentConcerns, request.ValidatedAt.UTC())
	if err != nil {
		return encodeMethodFailure(stdout, "artifact_publish_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "artifact_publish_refused", request.CorrelationID, request.OperationID, nil, controlStoreError())
	}
	defer store.Close()
	service, _ := artifact.NewService(store)
	result, err := service.Publish(ctx, artifact.PublishCommand{OperationID: request.OperationID.String(), ArtifactRevisionID: request.ArtifactRevisionID, MissionID: request.MissionID.String(), Kind: request.Kind, SourceRef: request.SourceRef, SourceRevision: request.SourceRevision, CreatedBy: request.CreatedBy.String(), CreatedAt: request.CreatedAt.UTC(), Content: append([]byte(nil), request.Content...), Validation: validation, ExpectedLatestRevisionID: request.ExpectedLatestRevisionID})
	if err != nil {
		return encodeMethodFailure(stdout, "artifact_publish_refused", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String()}, err)
	}
	revision := result.Revision
	return encodeMethodSuccess(stdout, "artifact_published", request.CorrelationID, request.OperationID, map[string]string{"mission_id": request.MissionID.String(), "artifact_revision_id": revision.ID()}, struct {
		Revision          artifactRevisionResult     `json:"revision"`
		ValidationVerdict artifact.ValidationVerdict `json:"validation_verdict"`
		RequestDigest     string                     `json:"request_digest"`
		Replayed          bool                       `json:"replayed"`
	}{artifactRevisionResult{revision.ID(), revision.MissionID(), revision.Kind(), revision.RevisionNumber(), revision.ContentDigest(), revision.SourceRef(), revision.SourceRevision(), revision.CreatedBy(), revision.CreatedAt()}, result.Validation.Verdict(), result.RequestDigest, result.Replayed})
}

func runArtifactApprove(ctx context.Context, request artifactApproveRequest, stdout io.Writer) int {
	if _, err := artifact.NewArtifactApproval(artifact.ArtifactApprovalParams{ID: request.ApprovalID, ArtifactRevisionID: request.ArtifactRevisionID, Verdict: request.Verdict, ActorID: request.ActorID.String(), EvidenceDigest: request.EvidenceDigest, GovernanceContextID: request.GovernanceContextID.String(), SubmittedAt: request.SubmittedAt}); err != nil {
		return encodeMethodFailure(stdout, "artifact_approval_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return encodeMethodFailure(stdout, "artifact_approval_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	store, err := sqlite.Open(ctx, request.Database)
	if err != nil {
		return encodeMethodFailure(stdout, "artifact_approval_refused", request.CorrelationID, request.OperationID, nil, controlStoreError())
	}
	defer store.Close()
	service, _ := artifact.NewService(store)
	result, err := service.Approve(ctx, artifact.ApproveCommand{OperationID: request.OperationID.String(), ApprovalID: request.ApprovalID, ArtifactRevisionID: request.ArtifactRevisionID, Verdict: request.Verdict, ActorID: request.ActorID.String(), EvidenceDigest: request.EvidenceDigest, GovernanceContextID: request.GovernanceContextID.String(), SubmittedAt: request.SubmittedAt.UTC()})
	if err != nil {
		return encodeMethodFailure(stdout, "artifact_approval_refused", request.CorrelationID, request.OperationID, nil, err)
	}
	approval := result.Approval
	return encodeMethodSuccess(stdout, "artifact_approved", request.CorrelationID, request.OperationID, map[string]string{"artifact_revision_id": request.ArtifactRevisionID, "approval_id": approval.ID()}, struct {
		ApprovalID          string                   `json:"approval_id"`
		ArtifactRevisionID  string                   `json:"artifact_revision_id"`
		Verdict             artifact.ApprovalVerdict `json:"verdict"`
		ActorID             string                   `json:"actor_id"`
		EvidenceDigest      string                   `json:"evidence_digest"`
		GovernanceContextID string                   `json:"governance_context_id"`
		SubmittedAt         time.Time                `json:"submitted_at"`
		RequestDigest       string                   `json:"request_digest"`
		Replayed            bool                     `json:"replayed"`
	}{approval.ID(), approval.ArtifactRevisionID(), approval.Verdict(), approval.ActorID(), approval.EvidenceDigest(), approval.GovernanceContextID(), approval.SubmittedAt(), result.RequestDigest, result.Replayed})
}
