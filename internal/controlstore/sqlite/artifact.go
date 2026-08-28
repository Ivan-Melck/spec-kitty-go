package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
)

var _ artifact.Repository = (*Store)(nil)

type artifactRevisionWire struct {
	ID             string        `json:"id"`
	MissionID      string        `json:"mission_id"`
	Kind           artifact.Kind `json:"kind"`
	RevisionNumber uint64        `json:"revision_number"`
	ContentDigest  string        `json:"content_digest"`
	SourceRef      string        `json:"source_ref"`
	SourceRevision string        `json:"source_revision"`
	CreatedBy      string        `json:"created_by"`
	CreatedAt      time.Time     `json:"created_at"`
}
type findingWire struct {
	Kind     string                    `json:"kind"`
	Field    string                    `json:"field"`
	Message  string                    `json:"message"`
	Recovery artifact.RecoveryCategory `json:"recovery"`
}
type validationWire struct {
	ArtifactRevisionID string                     `json:"artifact_revision_id"`
	Verdict            artifact.ValidationVerdict `json:"verdict"`
	PolicyKey          string                     `json:"policy_key"`
	PolicyRevision     string                     `json:"policy_revision"`
	PolicyKind         artifact.Kind              `json:"policy_kind"`
	Required           []artifact.Concern         `json:"required"`
	Findings           []findingWire              `json:"findings"`
	ValidatedAt        time.Time                  `json:"validated_at"`
}
type approvalWire struct {
	ID                  string                   `json:"id"`
	ArtifactRevisionID  string                   `json:"artifact_revision_id"`
	Verdict             artifact.ApprovalVerdict `json:"verdict"`
	ActorID             string                   `json:"actor_id"`
	EvidenceDigest      string                   `json:"evidence_digest"`
	GovernanceContextID string                   `json:"governance_context_id"`
	SubmittedAt         time.Time                `json:"submitted_at"`
}
type publishResultWire struct {
	OperationID   string               `json:"operation_id"`
	RequestDigest string               `json:"request_digest"`
	Revision      artifactRevisionWire `json:"revision"`
	Validation    validationWire       `json:"validation"`
}
type approvalResultWire struct {
	OperationID   string       `json:"operation_id"`
	RequestDigest string       `json:"request_digest"`
	Approval      approvalWire `json:"approval"`
}

func artifactError(kind artifact.ErrorKind, field, message string) error {
	recovery := artifact.RecoveryRetrySameOperation
	if kind == artifact.ErrorIdempotencyConflict {
		recovery = artifact.RecoveryResolveConflict
	} else if kind == artifact.ErrorNotFound {
		recovery = artifact.RecoveryInspectState
	}
	return &artifact.Error{Kind: kind, Field: field, Recovery: recovery, Message: message}
}
func revisionWire(r artifact.ArtifactRevision) artifactRevisionWire {
	return artifactRevisionWire{r.ID(), r.MissionID(), r.Kind(), r.RevisionNumber(), r.ContentDigest(), r.SourceRef(), r.SourceRevision(), r.CreatedBy(), r.CreatedAt()}
}
func restoreRevision(w artifactRevisionWire) (artifact.ArtifactRevision, error) {
	return artifact.RestoreArtifactRevision(artifact.ArtifactRevisionParams{ID: w.ID, MissionID: w.MissionID, Kind: w.Kind, RevisionNumber: w.RevisionNumber, ContentDigest: w.ContentDigest, SourceRef: w.SourceRef, SourceRevision: w.SourceRevision, CreatedBy: w.CreatedBy, CreatedAt: w.CreatedAt})
}
func validationToWire(v artifact.ValidationResult) validationWire {
	p := v.Policy()
	fs := v.Findings()
	out := validationWire{v.ArtifactRevisionID(), v.Verdict(), p.Key(), p.Revision(), p.Kind(), p.RequiredConcerns(), make([]findingWire, len(fs)), v.ValidatedAt()}
	for i, f := range fs {
		out.Findings[i] = findingWire{f.Kind(), f.Field(), f.Message(), f.Recovery()}
	}
	return out
}
func restoreValidation(w validationWire) (artifact.ValidationResult, error) {
	p, err := artifact.NewValidationPolicy(w.PolicyKey, w.PolicyRevision, w.PolicyKind, w.Required)
	if err != nil {
		return artifact.ValidationResult{}, err
	}
	fs := make([]artifact.Finding, len(w.Findings))
	for i, f := range w.Findings {
		fs[i], err = artifact.NewFinding(f.Kind, f.Field, f.Message, f.Recovery)
		if err != nil {
			return artifact.ValidationResult{}, err
		}
	}
	return artifact.NewValidationResult(artifact.ValidationResultParams{ArtifactRevisionID: w.ArtifactRevisionID, Verdict: w.Verdict, Findings: fs, Policy: p, ValidatedAt: w.ValidatedAt})
}
func approvalToWire(a artifact.ArtifactApproval) approvalWire {
	return approvalWire{a.ID(), a.ArtifactRevisionID(), a.Verdict(), a.ActorID(), a.EvidenceDigest(), a.GovernanceContextID(), a.SubmittedAt()}
}
func restoreApproval(w approvalWire) (artifact.ArtifactApproval, error) {
	return artifact.RestoreArtifactApproval(artifact.ArtifactApprovalParams{ID: w.ID, ArtifactRevisionID: w.ArtifactRevisionID, Verdict: w.Verdict, ActorID: w.ActorID, EvidenceDigest: w.EvidenceDigest, GovernanceContextID: w.GovernanceContextID, SubmittedAt: w.SubmittedAt})
}

func validateAtomicPublish(r artifact.AtomicPublishRequest) error {
	if r.OperationID == "" || r.RequestDigest == "" {
		return artifactError(artifact.ErrorInvalidCommand, "publication", "publication request is invalid")
	}
	_, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{ID: r.ArtifactRevisionID, MissionID: r.MissionID, Kind: r.Kind, RevisionNumber: 1, ContentDigest: r.ContentDigest, SourceRef: r.SourceRef, SourceRevision: r.SourceRevision, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt})
	if err != nil {
		return err
	}
	if r.ExpectedLatestRevisionID != "" {
		if _, err = artifact.RestoreArtifactRevision(artifact.ArtifactRevisionParams{ID: r.ExpectedLatestRevisionID, MissionID: r.MissionID, Kind: r.Kind, RevisionNumber: 1, ContentDigest: r.ContentDigest, SourceRef: r.SourceRef, SourceRevision: r.SourceRevision, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt}); err != nil {
			return artifactError(artifact.ErrorInvalidCommand, "expected_latest_revision_id", "expected latest revision identity is invalid")
		}
	}
	if r.Validation.ArtifactRevisionID() != r.ArtifactRevisionID {
		return artifactError(artifact.ErrorInvalidValidation, "validation", "validation is outside publication scope")
	}
	_, err = r.Validation.Commitment()
	return err
}

func (s *Store) PublishAtomic(ctx context.Context, r artifact.AtomicPublishRequest) (artifact.PublishResult, error) {
	if err := validateAtomicPublish(r); err != nil {
		return artifact.PublishResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	defer tx.Rollback()
	if prior, found, e := loadPublish(ctx, tx, r.OperationID, r.RequestDigest); e != nil {
		return artifact.PublishResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	if conflict, e := phase3OperationConflictsWithLegacy(ctx, tx, r.OperationID); e != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "operation identity cannot be inspected")
	} else if conflict {
		return artifact.PublishResult{}, artifactError(artifact.ErrorIdempotencyConflict, "operation_id", "operation identity is already bound")
	}
	var currentID string
	var currentNumber uint64
	err = tx.QueryRowContext(ctx, `SELECT artifact_revision_id,revision_number FROM phase3_artifact_revisions WHERE mission_id=? AND kind=? ORDER BY revision_number DESC LIMIT 1`, r.MissionID, r.Kind).Scan(&currentID, &currentNumber)
	if errors.Is(err, sql.ErrNoRows) {
		currentID = ""
		currentNumber = 0
	} else if err != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	if currentID != r.ExpectedLatestRevisionID {
		return artifact.PublishResult{}, artifactError(artifact.ErrorIdempotencyConflict, "expected_latest_revision_id", "artifact latest revision changed")
	}
	next := currentNumber + 1
	revision, err := artifact.NewArtifactRevision(artifact.ArtifactRevisionParams{ID: r.ArtifactRevisionID, MissionID: r.MissionID, Kind: r.Kind, RevisionNumber: next, ContentDigest: r.ContentDigest, SourceRef: r.SourceRef, SourceRevision: r.SourceRevision, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt})
	if err != nil {
		return artifact.PublishResult{}, err
	}
	rw := revisionWire(revision)
	vw := validationToWire(r.Validation)
	rj, _ := json.Marshal(rw)
	vj, _ := json.Marshal(vw)
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_artifact_revisions(artifact_revision_id,mission_id,kind,revision_number,content_digest,source_ref,source_revision,created_by,created_at,revision_json) VALUES(?,?,?,?,?,?,?,?,?,?)`, rw.ID, rw.MissionID, rw.Kind, rw.RevisionNumber, rw.ContentDigest, rw.SourceRef, rw.SourceRevision, rw.CreatedBy, formatTime(rw.CreatedAt), rj); err != nil {
		if isUnique(err) {
			return artifact.PublishResult{}, artifactError(artifact.ErrorIdempotencyConflict, "artifact_revision_id", "artifact revision identity already exists")
		}
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_artifact_validations(artifact_revision_id,verdict,validation_json) VALUES(?,?,?)`, rw.ID, vw.Verdict, vj); err != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	wire := publishResultWire{r.OperationID, r.RequestDigest, rw, vw}
	resultJSON, _ := json.Marshal(wire)
	if err = insertPhase3Operation(ctx, tx, r.OperationID, "artifact_publish", r.MissionID, r.RequestDigest, resultJSON, r.CreatedAt); err != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	if err = insertPhase3Event(ctx, tx, "artifact-published:"+r.OperationID, r.OperationID, r.MissionID, "artifact_published", r.RequestDigest, r.CreatedAt); err != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	if err = tx.Commit(); err != nil {
		return artifact.PublishResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	return artifact.PublishResult{OperationID: r.OperationID, RequestDigest: r.RequestDigest, Revision: revision, Validation: r.Validation}, nil
}

func loadPublish(ctx context.Context, q rowQuery, op, digest string) (artifact.PublishResult, bool, error) {
	var kind, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.PublishResult{}, false, nil
	}
	if err != nil {
		return artifact.PublishResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact replay is unavailable")
	}
	if kind != "artifact_publish" || stored != digest {
		return artifact.PublishResult{}, false, artifactError(artifact.ErrorIdempotencyConflict, "operation_id", "publication operation was reused")
	}
	var w publishResultWire
	if err = strictJSON(raw, &w); err != nil {
		return artifact.PublishResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "stored publication is invalid")
	}
	rev, e := restoreRevision(w.Revision)
	if e != nil {
		return artifact.PublishResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "stored publication is invalid")
	}
	v, e := restoreValidation(w.Validation)
	if e != nil {
		return artifact.PublishResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "stored publication is invalid")
	}
	return artifact.PublishResult{OperationID: w.OperationID, RequestDigest: w.RequestDigest, Revision: rev, Validation: v}, true, nil
}

func (s *Store) Revision(ctx context.Context, id string) (artifact.ArtifactRevision, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision_json FROM phase3_artifact_revisions WHERE artifact_revision_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorNotFound, "artifact_revision_id", "artifact revision was not found")
	}
	if err != nil {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	var w artifactRevisionWire
	if strictJSON(raw, &w) != nil {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored artifact revision is invalid")
	}
	r, e := restoreRevision(w)
	if e != nil {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored artifact revision is invalid")
	}
	return r, nil
}
func (s *Store) CurrentRevision(ctx context.Context, missionID string, kind artifact.Kind) (artifact.ArtifactRevision, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT revision_json FROM phase3_artifact_revisions WHERE mission_id=? AND kind=? ORDER BY revision_number DESC LIMIT 1`, missionID, kind).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorNotFound, "artifact_revision", "current artifact revision was not found")
	}
	if err != nil {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	var w artifactRevisionWire
	if strictJSON(raw, &w) != nil {
		return artifact.ArtifactRevision{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored artifact revision is invalid")
	}
	return restoreRevision(w)
}
func (s *Store) Validation(ctx context.Context, id string) (artifact.ValidationResult, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT validation_json FROM phase3_artifact_validations WHERE artifact_revision_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ValidationResult{}, artifactError(artifact.ErrorNotFound, "validation", "artifact validation was not found")
	}
	if err != nil {
		return artifact.ValidationResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	var w validationWire
	if strictJSON(raw, &w) != nil {
		return artifact.ValidationResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored validation is invalid")
	}
	return restoreValidation(w)
}

func (s *Store) ApproveAtomic(ctx context.Context, r artifact.AtomicApprovalRequest) (artifact.ApprovalResult, error) {
	approval, err := artifact.NewArtifactApproval(artifact.ArtifactApprovalParams{ID: r.ApprovalID, ArtifactRevisionID: r.ArtifactRevisionID, Verdict: r.Verdict, ActorID: r.ActorID, EvidenceDigest: r.EvidenceDigest, GovernanceContextID: r.GovernanceContextID, SubmittedAt: r.SubmittedAt})
	if err != nil || r.OperationID == "" || r.RequestDigest == "" {
		if err != nil {
			return artifact.ApprovalResult{}, err
		}
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorInvalidCommand, "approval", "approval request is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	defer tx.Rollback()
	if prior, found, e := loadApproval(ctx, tx, r.OperationID, r.RequestDigest); e != nil {
		return artifact.ApprovalResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	if conflict, e := phase3OperationConflictsWithLegacy(ctx, tx, r.OperationID); e != nil {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "operation identity cannot be inspected")
	} else if conflict {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorIdempotencyConflict, "operation_id", "operation identity is already bound")
	}
	w := approvalToWire(approval)
	raw, _ := json.Marshal(w)
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_artifact_approvals(approval_id,artifact_revision_id,verdict,actor_id,evidence_digest,governance_context_id,submitted_at,approval_json) VALUES(?,?,?,?,?,?,?,?)`, w.ID, w.ArtifactRevisionID, w.Verdict, w.ActorID, w.EvidenceDigest, w.GovernanceContextID, formatTime(w.SubmittedAt), raw); err != nil {
		if isUnique(err) {
			return artifact.ApprovalResult{}, artifactError(artifact.ErrorIdempotencyConflict, "artifact_revision_id", "artifact revision already has an immutable verdict")
		}
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	wire := approvalResultWire{r.OperationID, r.RequestDigest, w}
	resultJSON, _ := json.Marshal(wire)
	var missionID string
	if err = tx.QueryRowContext(ctx, `SELECT mission_id FROM phase3_artifact_revisions WHERE artifact_revision_id=?`, r.ArtifactRevisionID).Scan(&missionID); err != nil {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorNotFound, "artifact_revision_id", "artifact revision was not found")
	}
	if err = insertPhase3Operation(ctx, tx, r.OperationID, "artifact_approve", missionID, r.RequestDigest, resultJSON, r.SubmittedAt); err != nil {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	if err = insertPhase3Event(ctx, tx, "artifact-approved:"+r.OperationID, r.OperationID, missionID, "artifact_approved", r.RequestDigest, r.SubmittedAt); err != nil {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	if err = tx.Commit(); err != nil {
		return artifact.ApprovalResult{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	return artifact.ApprovalResult{OperationID: r.OperationID, RequestDigest: r.RequestDigest, Approval: approval}, nil
}
func loadApproval(ctx context.Context, q rowQuery, op, digest string) (artifact.ApprovalResult, bool, error) {
	var kind, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return artifact.ApprovalResult{}, false, nil
	}
	if err != nil {
		return artifact.ApprovalResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact replay is unavailable")
	}
	if kind != "artifact_approve" || stored != digest {
		return artifact.ApprovalResult{}, false, artifactError(artifact.ErrorIdempotencyConflict, "operation_id", "approval operation was reused")
	}
	var w approvalResultWire
	if strictJSON(raw, &w) != nil {
		return artifact.ApprovalResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "stored approval is invalid")
	}
	a, e := restoreApproval(w.Approval)
	if e != nil {
		return artifact.ApprovalResult{}, false, artifactError(artifact.ErrorRepositoryFailure, "store", "stored approval is invalid")
	}
	return artifact.ApprovalResult{OperationID: w.OperationID, RequestDigest: w.RequestDigest, Approval: a}, true, nil
}
func (s *Store) Approvals(ctx context.Context, missionID string, kind artifact.Kind) ([]artifact.ArtifactApproval, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.approval_json FROM phase3_artifact_approvals a JOIN phase3_artifact_revisions r ON r.artifact_revision_id=a.artifact_revision_id WHERE r.mission_id=? AND r.kind=? ORDER BY r.revision_number,a.submitted_at`, missionID, kind)
	if err != nil {
		return nil, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	defer rows.Close()
	var out []artifact.ArtifactApproval
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			return nil, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
		}
		var w approvalWire
		if strictJSON(raw, &w) != nil {
			return nil, artifactError(artifact.ErrorRepositoryFailure, "store", "stored approval is invalid")
		}
		a, e := restoreApproval(w)
		if e != nil {
			return nil, artifactError(artifact.ErrorRepositoryFailure, "store", "stored approval is invalid")
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) ObserveCurrentApproval(ctx context.Context, missionID string, kind artifact.Kind) (artifact.CurrentApprovalObservation, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT revision_json FROM phase3_artifact_revisions WHERE mission_id=? AND kind=? ORDER BY revision_number DESC LIMIT 1`, missionID, kind).Scan(&raw); err != nil {
		return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorNotFound, "artifact_revision", "current artifact revision was not found")
	}
	var rw artifactRevisionWire
	if strictJSON(raw, &rw) != nil {
		return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored artifact revision is invalid")
	}
	rev, e := restoreRevision(rw)
	if e != nil {
		return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored artifact revision is invalid")
	}
	rows, err := tx.QueryContext(ctx, `SELECT approval_json FROM phase3_artifact_approvals WHERE artifact_revision_id=? ORDER BY submitted_at`, rev.ID())
	if err != nil {
		return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorRepositoryFailure, "store", "artifact repository is unavailable")
	}
	defer rows.Close()
	var approvals []artifact.ArtifactApproval
	for rows.Next() {
		var b []byte
		_ = rows.Scan(&b)
		var aw approvalWire
		if strictJSON(b, &aw) != nil {
			return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored approval is invalid")
		}
		a, e := restoreApproval(aw)
		if e != nil {
			return artifact.CurrentApprovalObservation{}, artifactError(artifact.ErrorRepositoryFailure, "store", "stored approval is invalid")
		}
		approvals = append(approvals, a)
	}
	return artifact.NewCurrentApprovalObservation(rev, approvals)
}
