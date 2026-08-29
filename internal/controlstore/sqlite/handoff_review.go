package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type handoffQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) InspectHandoffAuthority(ctx context.Context, request handoff.Request) (handoff.AuthorityScope, error) {
	if err := request.Validate(); err != nil {
		return handoff.AuthorityScope{}, err
	}
	return inspectHandoffAuthority(ctx, s.db, request)
}

func (s *Store) LookupHandoffOperation(ctx context.Context, request handoff.Request) (handoff.Record, bool, error) {
	digest, err := handoffCommandDigest(request)
	if err != nil {
		return handoff.Record{}, false, err
	}
	return loadHandoffOperation(ctx, s.db, request.OperationID, digest)
}

func handoffCommandDigest(request handoff.Request) (string, error) {
	contributors, err := handoff.CanonicalContributors(request.Contributors)
	if err != nil {
		return "", &handoff.Error{Kind: handoff.KindInvalid, Message: "canonicalize handoff contributors", Cause: err}
	}
	encodedContributors, _ := json.Marshal(contributors)
	digest, err := handoffRequestDigest(request, digestHandoffBytes(encodedContributors))
	if err != nil {
		return "", &handoff.Error{Kind: handoff.KindInvalid, Message: "encode handoff command", Cause: err}
	}
	return digest, nil
}

func (s *Store) LookupHandoffRefusal(ctx context.Context, request handoff.Request) (handoff.Refusal, bool, error) {
	digest, err := handoffCommandDigest(request)
	if err != nil {
		return handoff.Refusal{}, false, err
	}
	return loadHandoffRefusal(ctx, s.db, request.OperationID, digest)
}

func loadHandoffRefusal(ctx context.Context, query handoffQuery, operationID ids.OperationID, digest string) (handoff.Refusal, bool, error) {
	var refusal handoff.Refusal
	var storedDigest, kind, observedJSON, expectedJSON, actionsJSON, occurredAt string
	err := query.QueryRowContext(ctx, `SELECT request_digest,error_kind,message,observed_json,expected_json,recovery_actions_json,occurred_at FROM handoff_refusals WHERE operation_id=?`, operationID).Scan(
		&storedDigest, &kind, &refusal.Message, &observedJSON, &expectedJSON, &actionsJSON, &occurredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return handoff.Refusal{}, false, nil
	}
	if err != nil {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "inspect handoff refusal replay", Cause: err}
	}
	if storedDigest != digest {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "handoff OperationID was reused for a different refused request"}
	}
	refusal.OperationID, refusal.Kind = operationID, handoff.ErrorKind(kind)
	if err := json.Unmarshal([]byte(observedJSON), &refusal.Observed); err != nil {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "decode handoff refusal observed facts", Cause: err}
	}
	if err := json.Unmarshal([]byte(expectedJSON), &refusal.Expected); err != nil {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "decode handoff refusal expected facts", Cause: err}
	}
	if err := json.Unmarshal([]byte(actionsJSON), &refusal.RecoveryActions); err != nil {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "decode handoff refusal recovery actions", Cause: err}
	}
	repositoryID := refusal.Observed["repository_id"]
	if repositoryID == "" {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "handoff refusal is missing its repository identity"}
	}
	if err := query.QueryRowContext(ctx, `SELECT common_dir FROM repositories WHERE repository_id=?`, repositoryID).Scan(&refusal.RepositoryPath); err != nil {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "resolve handoff refusal repository", Cause: err}
	}
	refusal.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return handoff.Refusal{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "decode handoff refusal timestamp", Cause: err}
	}
	return refusal, true, nil
}

func (s *Store) CommitHandoffRefusal(ctx context.Context, request handoff.Request, refusalError *handoff.Error) (handoff.Refusal, error) {
	if err := request.Validate(); err != nil {
		return handoff.Refusal{}, err
	}
	if refusalError == nil || refusalError.Cause != nil || refusalError.Kind != handoff.KindCandidateMismatch || refusalError.Observed["divergence_kind"] != handoff.BranchMovementDivergence || refusalError.Observed["evidence_ref_root"] == "" || len(refusalError.RecoveryActions) == 0 {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindInvalid, Message: "only a completely preserved branch-movement refusal can be recorded"}
	}
	digest, err := handoffCommandDigest(request)
	if err != nil {
		return handoff.Refusal{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindConflict, Message: "begin handoff refusal transaction", Cause: err}
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := loadHandoffRefusal(ctx, tx, request.OperationID, digest); err != nil {
		return handoff.Refusal{}, err
	} else if found {
		return replay, nil
	}
	if _, found, err := loadHandoffOperation(ctx, tx, request.OperationID, digest); err != nil {
		return handoff.Refusal{}, err
	} else if found {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindConflict, Message: "handoff operation already has an immutable accepted result"}
	}
	observedJSON, _ := json.Marshal(refusalError.Observed)
	expectedJSON, _ := json.Marshal(refusalError.Expected)
	actionsJSON, _ := json.Marshal(refusalError.RecoveryActions)
	when := formatTime(request.SubmittedAt)
	_, err = tx.ExecContext(ctx, `INSERT INTO handoff_refusals(operation_id,request_digest,error_kind,message,observed_json,expected_json,recovery_actions_json,occurred_at) VALUES(?,?,?,?,?,?,?,?)`,
		request.OperationID, digest, refusalError.Kind, refusalError.Message, observedJSON, expectedJSON, actionsJSON, when)
	if err != nil {
		// A concurrent same-operation writer may have committed the exact
		// refusal. Resolve that race through the immutable replay path.
		_ = tx.Rollback()
		if replay, found, replayErr := loadHandoffRefusal(ctx, s.db, request.OperationID, digest); replayErr != nil {
			return handoff.Refusal{}, replayErr
		} else if found {
			return replay, nil
		}
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindConflict, Message: "persist handoff refusal", Cause: err}
	}
	if err := tx.Commit(); err != nil {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindConflict, Message: "commit immutable handoff refusal", Cause: err}
	}
	refusal, found, err := loadHandoffRefusal(ctx, s.db, request.OperationID, digest)
	if err != nil || !found {
		return handoff.Refusal{}, &handoff.Error{Kind: handoff.KindConflict, Message: "read committed handoff refusal", Cause: err}
	}
	return refusal, nil
}

func inspectHandoffAuthority(ctx context.Context, query handoffQuery, request handoff.Request) (handoff.AuthorityScope, error) {
	scope := handoff.AuthorityScope{
		OperationID: request.OperationID, HandoffID: request.HandoffID,
		WorkRevisionID: request.WorkRevisionID, WorkID: request.WorkID,
	}
	var projectID ids.ProjectID
	var missionID ids.MissionID
	var revisionID ids.WorkRevisionID
	var workID ids.WorkID
	var runID ids.RunID
	var workspaceID ids.WorkspaceID
	var leaseID ids.LeaseID
	var generation int64
	var actorID ids.ActorID
	var processID ids.ProcessID
	var runStatus, tokenDigestValue, leaseStatus, deadline string
	var workspaceStage, workspaceStatus string
	var registered int
	var observedHead, dependencyHead, gitDir, indexPath string
	err := query.QueryRowContext(ctx, `SELECT r.project_id,r.mission_id,r.work_revision_id,r.work_id,r.run_id,w.workspace_id,l.lease_id,l.generation,
r.actor_id,r.process_id,r.status,l.token_digest,l.status,l.deadline,w.stage,w.status,w.registered,w.observed_head,w.dependency_head,w.git_dir,w.index_path,w.repository_id,repo.common_dir,w.owned_branch,w.pinned_base,w.lane_id,g.criteria_digest
FROM runs r JOIN leases l ON l.run_id=r.run_id JOIN workspaces w ON w.run_id=r.run_id AND w.lease_id=l.lease_id
JOIN repositories repo ON repo.repository_id=w.repository_id JOIN work_graph_revisions g ON g.work_revision_id=r.work_revision_id
WHERE r.run_id=? AND w.workspace_id=? AND l.lease_id=?`, request.RunID, request.WorkspaceID, request.LeaseID).Scan(
		&projectID, &missionID, &revisionID, &workID, &runID, &workspaceID, &leaseID, &generation, &actorID, &processID,
		&runStatus, &tokenDigestValue, &leaseStatus, &deadline, &workspaceStage, &workspaceStatus, &registered, &observedHead, &dependencyHead, &gitDir, &indexPath,
		&scope.RepositoryID, &scope.RepositoryPath, &scope.OwnedBranch, &scope.PinnedBase, &scope.LaneID, &scope.CriteriaDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return scope, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "handoff run, lease, and workspace binding was not found", Cause: err}
	}
	if err != nil {
		return scope, &handoff.Error{Kind: handoff.KindConflict, Message: "read handoff authority", Cause: err}
	}
	if projectID != request.ProjectID || missionID != request.MissionID || revisionID != request.WorkRevisionID || workID != request.WorkID || runID != request.RunID || workspaceID != request.WorkspaceID || leaseID != request.LeaseID || generation != request.LeaseGeneration || actorID != request.ActorID || processID != request.ProcessID {
		return scope, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "handoff identity does not match the immutable run and workspace binding"}
	}
	deadlineTime, parseErr := time.Parse(time.RFC3339Nano, deadline)
	if parseErr != nil {
		return scope, &handoff.Error{Kind: handoff.KindConflict, Message: "lease deadline is corrupt", Cause: parseErr}
	}
	if tokenDigestValue != tokenDigest(request.Token) || runStatus != "active" || leaseStatus != "active" || !deadlineTime.After(request.SubmittedAt.UTC()) {
		return scope, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "handoff requires the current live fencing token", Observed: map[string]string{"lease_status": leaseStatus}, Expected: map[string]string{"lease_status": "active"}}
	}
	if workspaceStage != "ready" || workspaceStatus != "ready" || registered != 1 || observedHead == "" || observedHead != dependencyHead || gitDir == "" || indexPath == "" {
		return scope, &handoff.Error{Kind: handoff.KindStaleAuthority, Message: "handoff requires the ready workspace permanently bound to the lease"}
	}
	eligibility, reconciliationID, err := loadWorkEligibility(ctx, query, request.WorkRevisionID, request.WorkID, workgraph.StatusActive)
	if err != nil {
		return scope, &handoff.Error{Kind: handoff.KindConflict, Message: "read WorkRevision handoff eligibility", Cause: err}
	}
	if !eligibility.HandoffEligible {
		return scope, &handoff.Error{Kind: handoff.KindRevisionIneligible, Message: "WorkRevision reconciliation forbids handoff", Observed: map[string]string{"reconciliation_id": reconciliationID, "reason": eligibility.Reason}, Expected: map[string]string{"handoff_eligible": "true"}}
	}
	return scope, nil
}

func (s *Store) CommitHandoff(ctx context.Context, request handoff.Request, evidence handoff.CandidateEvidence) (handoff.Record, error) {
	if err := request.Validate(); err != nil {
		return handoff.Record{}, err
	}
	contributors, err := handoff.CanonicalContributors(request.Contributors)
	if err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindInvalid, Message: "canonicalize handoff contributors", Cause: err}
	}
	encodedContributors, _ := json.Marshal(contributors)
	contributorDigest := digestHandoffBytes(encodedContributors)
	digest, err := handoffRequestDigest(request, contributorDigest)
	if err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindInvalid, Message: "encode handoff command", Cause: err}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "begin handoff transaction", Cause: err}
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := loadHandoffOperation(ctx, tx, request.OperationID, digest); err != nil {
		return handoff.Record{}, err
	} else if found {
		return replay, nil
	}
	if refusal, found, err := loadHandoffRefusal(ctx, tx, request.OperationID, digest); err != nil {
		return handoff.Record{}, err
	} else if found {
		return handoff.Record{}, refusal.Error()
	}
	scope, err := inspectHandoffAuthority(ctx, tx, request)
	if err != nil {
		return handoff.Record{}, err
	}
	if evidence.Candidate != request.Candidate || evidence.PinnedBase != scope.PinnedBase || evidence.OwnedBranch != scope.OwnedBranch || evidence.ObservedBranchHead != request.Candidate {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindCandidateMismatch, Message: "candidate verification evidence does not match durable workspace facts"}
	}
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(handoff_generation),0)+1 FROM handoffs WHERE work_revision_id=? AND work_id=?`, request.WorkRevisionID, request.WorkID).Scan(&generation); err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "allocate handoff generation", Cause: err}
	}
	when := formatTime(request.SubmittedAt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO handoff_commands(operation_id,request_digest,result_handoff_id,actor_id,process_id,occurred_at) VALUES(?,?,?,?,?,?)`, request.OperationID, digest, request.HandoffID, request.ActorID, request.ProcessID, when); err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "persist handoff command", Cause: err}
	}
	for _, contributor := range contributors {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, contributor, when); err != nil {
			return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "persist contributor actor", Cause: err}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO handoff_contributors(handoff_id,actor_id) VALUES(?,?)`, request.HandoffID, contributor); err != nil {
			return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "persist immutable contributor set", Cause: err}
		}
	}
	verification, _ := json.Marshal(evidence)
	_, err = tx.ExecContext(ctx, `INSERT INTO handoffs(handoff_id,operation_id,handoff_generation,project_id,mission_id,work_revision_id,work_id,run_id,workspace_id,repository_id,lease_id,lease_generation,lane_id,candidate,pinned_base,contributor_digest,criteria_digest,evidence_digest,verification_json,submitted_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, request.HandoffID, request.OperationID, generation, request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.RunID, request.WorkspaceID, scope.RepositoryID, request.LeaseID, request.LeaseGeneration, scope.LaneID, request.Candidate, scope.PinnedBase, contributorDigest, scope.CriteriaDigest, request.EvidenceDigest, verification, when)
	if err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "handoff ID, candidate, and generation must be unique", Cause: err}
	}
	if err := tx.Commit(); err != nil {
		return handoff.Record{}, &handoff.Error{Kind: handoff.KindConflict, Message: "commit immutable handoff", Cause: err}
	}
	return s.Handoff(ctx, request.HandoffID)
}

func loadHandoffOperation(ctx context.Context, query handoffQuery, operationID ids.OperationID, digest string) (handoff.Record, bool, error) {
	var storedDigest string
	var handoffID ids.HandoffID
	err := query.QueryRowContext(ctx, `SELECT request_digest,result_handoff_id FROM handoff_commands WHERE operation_id=?`, operationID).Scan(&storedDigest, &handoffID)
	if errors.Is(err, sql.ErrNoRows) {
		return handoff.Record{}, false, nil
	}
	if err != nil {
		return handoff.Record{}, false, &handoff.Error{Kind: handoff.KindConflict, Message: "inspect handoff replay", Cause: err}
	}
	if storedDigest != digest {
		return handoff.Record{}, false, &handoff.Error{Kind: handoff.KindIdempotencyConflict, Message: "handoff OperationID was reused for a different request"}
	}
	record, err := loadHandoff(ctx, query, handoffID)
	return record, true, err
}

func (s *Store) Handoff(ctx context.Context, handoffID ids.HandoffID) (handoff.Record, error) {
	return loadHandoff(ctx, s.db, handoffID)
}

func loadHandoff(ctx context.Context, query handoffQuery, handoffID ids.HandoffID) (handoff.Record, error) {
	var record handoff.Record
	var verification []byte
	var contributorDigest string
	var submittedAt string
	err := query.QueryRowContext(ctx, `SELECT handoff_id,handoff_generation,project_id,mission_id,work_revision_id,work_id,run_id,workspace_id,repository_id,lease_id,lease_generation,lane_id,candidate,pinned_base,contributor_digest,criteria_digest,evidence_digest,verification_json,submitted_at FROM handoffs WHERE handoff_id=?`, handoffID).Scan(
		&record.HandoffID, &record.HandoffGeneration, &record.ProjectID, &record.MissionID, &record.WorkRevisionID, &record.WorkID, &record.RunID, &record.WorkspaceID, &record.RepositoryID, &record.LeaseID, &record.LeaseGeneration, &record.LaneID, &record.Candidate, &record.PinnedBase, &contributorDigest, &record.CriteriaDigest, &record.EvidenceDigest, &verification, &submittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return record, &handoff.Error{Kind: handoff.KindNotFound, Message: "handoff was not found", Cause: err}
	}
	if err != nil {
		return record, &handoff.Error{Kind: handoff.KindConflict, Message: "read handoff", Cause: err}
	}
	if err := json.Unmarshal(verification, &record.Verification); err != nil {
		return record, &handoff.Error{Kind: handoff.KindConflict, Message: "decode candidate verification", Cause: err}
	}
	if record.SubmittedAt, err = time.Parse(time.RFC3339Nano, submittedAt); err != nil {
		return record, &handoff.Error{Kind: handoff.KindConflict, Message: "decode handoff timestamp", Cause: err}
	}
	rows, err := queryContributors(ctx, query, handoffID)
	if err != nil {
		return record, err
	}
	record.Contributors = rows
	encodedContributors, _ := json.Marshal(rows)
	if digestHandoffBytes(encodedContributors) != contributorDigest {
		return record, &handoff.Error{Kind: handoff.KindConflict, Message: "handoff contributor digest does not match immutable membership"}
	}
	record.ContributorDigest = contributorDigest
	return record, nil
}

func queryContributors(ctx context.Context, query handoffQuery, handoffID ids.HandoffID) ([]ids.ActorID, error) {
	// QueryRow-only interfaces keep transaction/read paths identical. The
	// immutable contributor digest gives us a bounded indexed lookup count.
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM handoff_contributors WHERE handoff_id=?`, handoffID).Scan(&count); err != nil {
		return nil, &handoff.Error{Kind: handoff.KindConflict, Message: "count handoff contributors", Cause: err}
	}
	result := make([]ids.ActorID, 0, count)
	for offset := 0; offset < count; offset++ {
		var actorID ids.ActorID
		if err := query.QueryRowContext(ctx, `SELECT actor_id FROM handoff_contributors WHERE handoff_id=? ORDER BY actor_id LIMIT 1 OFFSET ?`, handoffID, offset).Scan(&actorID); err != nil {
			return nil, &handoff.Error{Kind: handoff.KindConflict, Message: "read handoff contributor", Cause: err}
		}
		result = append(result, actorID)
	}
	return result, nil
}

func (s *Store) InspectReview(ctx context.Context, request review.Request) (review.Scope, error) {
	if err := request.Validate(); err != nil {
		return review.Scope{}, err
	}
	if err := s.recordReviewAttempt(ctx, request); err != nil {
		return review.Scope{}, err
	}
	return inspectReviewScope(ctx, s.db, request)
}

// InspectReviewAuthority performs the read-only authority portion of review
// inspection. Composition adapters use it after immutable operation replay to
// preserve a stale-fence diagnosis without recording a review attempt or
// treating the observation as permission to commit. CommitReview repeats the
// same authority checks transactionally before it writes a verdict.
func (s *Store) InspectReviewAuthority(ctx context.Context, request review.Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	_, err := inspectReviewAuthority(ctx, s.db, request)
	return err
}

func inspectReviewAuthority(ctx context.Context, query handoffQuery, request review.Request) (handoff.Record, error) {
	h, err := loadHandoff(ctx, query, request.HandoffID)
	if err != nil {
		return handoff.Record{}, &review.Error{Kind: review.KindNotFound, Message: "immutable handoff was not found", Cause: err}
	}
	if h.WorkRevisionID != request.WorkRevisionID || h.WorkID != request.WorkID || h.LeaseID != request.LeaseID || h.LeaseGeneration != request.LeaseGeneration {
		return handoff.Record{}, &review.Error{Kind: review.KindCandidateMismatch, Message: "review identity differs from immutable handoff"}
	}
	if h.Candidate != request.Candidate {
		return handoff.Record{}, &review.Error{Kind: review.KindCandidateMismatch, Message: "review candidate differs from immutable handoff"}
	}
	var status, deadline, digestValue string
	var generation int64
	if err := query.QueryRowContext(ctx, `SELECT generation,token_digest,status,deadline FROM leases WHERE lease_id=?`, request.LeaseID).Scan(&generation, &digestValue, &status, &deadline); err != nil {
		return handoff.Record{}, &review.Error{Kind: review.KindStaleAuthority, Message: "handoff lease was not found", Cause: err}
	}
	deadlineTime, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return handoff.Record{}, &review.Error{Kind: review.KindConflict, Message: "lease deadline is corrupt", Cause: err}
	}
	if generation != request.LeaseGeneration || digestValue != tokenDigest(request.Token) || status != "active" || !deadlineTime.After(request.SubmittedAt.UTC()) {
		return handoff.Record{}, &review.Error{Kind: review.KindStaleAuthority, Message: "review requires the handoff's current live fencing authority"}
	}
	return h, nil
}

func (s *Store) LookupReviewOperation(ctx context.Context, request review.Request) (review.Record, bool, error) {
	digest, err := reviewRequestDigest(request)
	if err != nil {
		return review.Record{}, false, &review.Error{Kind: review.KindInvalid, Message: "encode review command", Cause: err}
	}
	return loadReviewOperation(ctx, s.db, request.OperationID, digest)
}

func inspectReviewScope(ctx context.Context, query handoffQuery, request review.Request) (review.Scope, error) {
	record, err := loadHandoff(ctx, query, request.HandoffID)
	if err != nil {
		return review.Scope{}, &review.Error{Kind: review.KindNotFound, Message: "immutable handoff was not found", Cause: err}
	}
	var encoded []byte
	if err := query.QueryRowContext(ctx, `SELECT encoded_revision FROM work_graph_revisions WHERE work_revision_id=?`, record.WorkRevisionID).Scan(&encoded); err != nil {
		return review.Scope{}, &review.Error{Kind: review.KindRevisionIneligible, Message: "immutable WorkRevision was not found", Cause: err}
	}
	revision, err := workgraph.DecodeRevision(encoded)
	if err != nil {
		return review.Scope{}, &review.Error{Kind: review.KindRevisionIneligible, Message: "decode immutable WorkRevision", Cause: err}
	}
	if record.CriteriaDigest != revision.CriteriaDigest() {
		return review.Scope{}, &review.Error{Kind: review.KindRevisionIneligible, Message: "handoff criteria digest differs from its immutable WorkRevision"}
	}
	for _, item := range revision.WorkItems() {
		if item.ID == record.WorkID {
			ids := make([]string, len(item.Criteria))
			for index := range item.Criteria {
				ids[index] = item.Criteria[index].ID
			}
			return review.Scope{Handoff: record, CriterionIDs: ids}, nil
		}
	}
	return review.Scope{}, &review.Error{Kind: review.KindRevisionIneligible, Message: "handoff WorkID is absent from its WorkRevision"}
}

func (s *Store) recordReviewAttempt(ctx context.Context, request review.Request) error {
	digest, err := reviewRequestDigest(request)
	if err != nil {
		return &review.Error{Kind: review.KindInvalid, Message: "encode review command", Cause: err}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &review.Error{Kind: review.KindConflict, Message: "begin review attempt", Cause: err}
	}
	defer func() { _ = tx.Rollback() }()
	var storedDigest string
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM review_attempts WHERE operation_id=?`, request.OperationID).Scan(&storedDigest)
	if err == nil {
		if storedDigest != digest {
			return &review.Error{Kind: review.KindIdempotencyConflict, Message: "review OperationID was reused for a different request"}
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return &review.Error{Kind: review.KindConflict, Message: "inspect review attempt", Cause: err}
	}
	when := formatTime(request.SubmittedAt)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, request.ActorID, when); err != nil {
		return &review.Error{Kind: review.KindConflict, Message: "persist reviewer actor", Cause: err}
	}
	processResult, err := tx.ExecContext(ctx, `INSERT INTO processes(process_id,actor_id,observed_at) VALUES(?,?,?) ON CONFLICT(process_id) DO UPDATE SET observed_at=excluded.observed_at WHERE processes.actor_id=excluded.actor_id`, request.ProcessID, request.ActorID, when)
	if err != nil {
		return &review.Error{Kind: review.KindConflict, Message: "persist reviewer process", Cause: err}
	}
	if changed, rowsErr := processResult.RowsAffected(); rowsErr != nil || changed != 1 {
		return &review.Error{Kind: review.KindConflict, Message: "review ProcessID is already bound to a different ActorID", Cause: rowsErr}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO review_attempts(operation_id,request_digest,handoff_id,review_id,actor_id,process_id,candidate,verdict,outcome,occurred_at) VALUES(?,?,?,?,?,?,?,?, 'submitted',?)`, request.OperationID, digest, request.HandoffID, request.ReviewID, request.ActorID, request.ProcessID, request.Candidate, request.Verdict, when); err != nil {
		return &review.Error{Kind: review.KindConflict, Message: "persist attributed review attempt", Cause: err}
	}
	if err := tx.Commit(); err != nil {
		return &review.Error{Kind: review.KindConflict, Message: "commit attributed review attempt", Cause: err}
	}
	return nil
}

func (s *Store) CommitReview(ctx context.Context, request review.Request) (review.Record, error) {
	if err := request.Validate(); err != nil {
		return review.Record{}, err
	}
	if err := s.recordReviewAttempt(ctx, request); err != nil {
		return review.Record{}, err
	}
	digest, _ := reviewRequestDigest(request)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "begin review judgment transaction", Cause: err}
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := loadReviewOperation(ctx, tx, request.OperationID, digest); err != nil {
		return review.Record{}, err
	} else if found {
		return replay, nil
	}
	h, err := inspectReviewAuthority(ctx, tx, request)
	if err != nil {
		return review.Record{}, err
	}
	scope, err := inspectReviewScope(ctx, tx, request)
	if err != nil {
		return review.Record{}, err
	}
	if err := review.ValidateCriterionSet(request.Criteria, scope.CriterionIDs); err != nil {
		return review.Record{}, err
	}
	for _, contributor := range h.Contributors {
		if contributor == request.ActorID {
			return review.Record{}, &review.Error{Kind: review.KindReviewerNotIndependent, Message: "reviewer ActorID contributed to the candidate", Observed: map[string]string{"actor_id": request.ActorID.String(), "process_id": request.ProcessID.String()}, Expected: map[string]string{"actor_id": "not in handoff contributor set"}}
		}
	}
	var latest int64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(handoff_generation) FROM handoffs WHERE work_revision_id=? AND work_id=?`, request.WorkRevisionID, request.WorkID).Scan(&latest); err != nil {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "read current handoff generation", Cause: err}
	}
	if h.HandoffGeneration != latest {
		return review.Record{}, &review.Error{Kind: review.KindRevisionIneligible, Message: "a newer immutable handoff supersedes this candidate", Observed: map[string]string{"handoff_generation": fmt.Sprint(h.HandoffGeneration)}, Expected: map[string]string{"handoff_generation": fmt.Sprint(latest)}}
	}
	eligibility, reconciliationID, err := loadWorkEligibility(ctx, tx, request.WorkRevisionID, request.WorkID, workgraph.StatusHandedOff)
	if err != nil {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "read review eligibility", Cause: err}
	}
	if !eligibility.HandoffEligible {
		return review.Record{}, &review.Error{Kind: review.KindRevisionIneligible, Message: "WorkRevision reconciliation forbids review", Observed: map[string]string{"reconciliation_id": reconciliationID, "reason": eligibility.Reason}}
	}
	var existingReview ids.ReviewID
	err = tx.QueryRowContext(ctx, `SELECT review_id FROM reviews WHERE handoff_id=?`, request.HandoffID).Scan(&existingReview)
	if err == nil {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "terminal review slot is already occupied", Observed: map[string]string{"review_id": existingReview.String()}}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "inspect terminal review slot", Cause: err}
	}
	for _, criterion := range request.Criteria {
		if _, err := tx.ExecContext(ctx, `INSERT INTO review_criteria(review_id,criterion_id,status,evidence) VALUES(?,?,?,?)`, request.ReviewID, criterion.CriterionID, criterion.Status, criterion.Evidence); err != nil {
			return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "persist criterion judgment", Cause: err}
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO reviews(review_id,operation_id,request_digest,handoff_id,work_revision_id,work_id,candidate,reviewer_actor_id,reviewer_process_id,verdict,evidence_digest,submitted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, request.ReviewID, request.OperationID, digest, request.HandoffID, request.WorkRevisionID, request.WorkID, request.Candidate, request.ActorID, request.ProcessID, request.Verdict, request.EvidenceDigest, formatTime(request.SubmittedAt))
	if err != nil {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "persist terminal candidate-bound judgment", Cause: err}
	}
	if err := tx.Commit(); err != nil {
		return review.Record{}, &review.Error{Kind: review.KindConflict, Message: "commit verdict and criterion judgments atomically", Cause: err}
	}
	return s.Review(ctx, request.ReviewID)
}

func loadReviewOperation(ctx context.Context, query handoffQuery, operationID ids.OperationID, digest string) (review.Record, bool, error) {
	var storedDigest string
	var reviewID ids.ReviewID
	err := query.QueryRowContext(ctx, `SELECT request_digest,review_id FROM reviews WHERE operation_id=?`, operationID).Scan(&storedDigest, &reviewID)
	if errors.Is(err, sql.ErrNoRows) {
		return review.Record{}, false, nil
	}
	if err != nil {
		return review.Record{}, false, &review.Error{Kind: review.KindConflict, Message: "inspect review replay", Cause: err}
	}
	if storedDigest != digest {
		return review.Record{}, false, &review.Error{Kind: review.KindIdempotencyConflict, Message: "review OperationID was reused for a different judgment"}
	}
	record, err := loadReview(ctx, query, reviewID)
	return record, true, err
}

func (s *Store) Review(ctx context.Context, reviewID ids.ReviewID) (review.Record, error) {
	return loadReview(ctx, s.db, reviewID)
}
func loadReview(ctx context.Context, query handoffQuery, reviewID ids.ReviewID) (review.Record, error) {
	var record review.Record
	var submittedAt string
	err := query.QueryRowContext(ctx, `SELECT review_id,handoff_id,work_revision_id,work_id,candidate,reviewer_actor_id,reviewer_process_id,verdict,evidence_digest,submitted_at FROM reviews WHERE review_id=?`, reviewID).Scan(&record.ReviewID, &record.HandoffID, &record.WorkRevisionID, &record.WorkID, &record.Candidate, &record.ReviewerActorID, &record.ReviewerProcessID, &record.Verdict, &record.EvidenceDigest, &submittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return record, &review.Error{Kind: review.KindNotFound, Message: "review was not found", Cause: err}
	}
	if err != nil {
		return record, &review.Error{Kind: review.KindConflict, Message: "read review", Cause: err}
	}
	if record.SubmittedAt, err = time.Parse(time.RFC3339Nano, submittedAt); err != nil {
		return record, &review.Error{Kind: review.KindConflict, Message: "decode review timestamp", Cause: err}
	}
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM review_criteria WHERE review_id=?`, reviewID).Scan(&count); err != nil {
		return record, &review.Error{Kind: review.KindConflict, Message: "count review criteria", Cause: err}
	}
	for offset := 0; offset < count; offset++ {
		var criterion review.CriterionResult
		if err := query.QueryRowContext(ctx, `SELECT criterion_id,status,evidence FROM review_criteria WHERE review_id=? ORDER BY criterion_id LIMIT 1 OFFSET ?`, reviewID, offset).Scan(&criterion.CriterionID, &criterion.Status, &criterion.Evidence); err != nil {
			return record, &review.Error{Kind: review.KindConflict, Message: "read review criterion", Cause: err}
		}
		record.Criteria = append(record.Criteria, criterion)
	}
	return record, nil
}

func handoffRequestDigest(request handoff.Request, contributorDigest string) (string, error) {
	payload := struct {
		SchemaVersion     uint16             `json:"schema_version"`
		CorrelationID     string             `json:"correlation_id"`
		HandoffID         ids.HandoffID      `json:"handoff_id"`
		ProjectID         ids.ProjectID      `json:"project_id"`
		MissionID         ids.MissionID      `json:"mission_id"`
		WorkRevisionID    ids.WorkRevisionID `json:"work_revision_id"`
		WorkID            ids.WorkID         `json:"work_id"`
		RunID             ids.RunID          `json:"run_id"`
		WorkspaceID       ids.WorkspaceID    `json:"workspace_id"`
		LeaseID           ids.LeaseID        `json:"lease_id"`
		LeaseGeneration   int64              `json:"lease_generation"`
		TokenDigest       string             `json:"token_digest"`
		ActorID           ids.ActorID        `json:"actor_id"`
		ProcessID         ids.ProcessID      `json:"process_id"`
		Candidate         string             `json:"candidate"`
		ContributorDigest string             `json:"contributor_digest"`
		EvidenceDigest    string             `json:"evidence_digest"`
		SubmittedAt       time.Time          `json:"submitted_at"`
	}{request.SchemaVersion, request.CorrelationID, request.HandoffID, request.ProjectID, request.MissionID, request.WorkRevisionID, request.WorkID, request.RunID, request.WorkspaceID, request.LeaseID, request.LeaseGeneration, tokenDigest(request.Token), request.ActorID, request.ProcessID, request.Candidate, contributorDigest, request.EvidenceDigest, request.SubmittedAt.UTC()}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return digestHandoffBytes(encoded), nil
}

func reviewRequestDigest(request review.Request) (string, error) {
	payload := struct {
		SchemaVersion   uint16                   `json:"schema_version"`
		CorrelationID   string                   `json:"correlation_id"`
		ReviewID        ids.ReviewID             `json:"review_id"`
		HandoffID       ids.HandoffID            `json:"handoff_id"`
		WorkRevisionID  ids.WorkRevisionID       `json:"work_revision_id"`
		WorkID          ids.WorkID               `json:"work_id"`
		LeaseID         ids.LeaseID              `json:"lease_id"`
		LeaseGeneration int64                    `json:"lease_generation"`
		TokenDigest     string                   `json:"token_digest"`
		ActorID         ids.ActorID              `json:"actor_id"`
		ProcessID       ids.ProcessID            `json:"process_id"`
		Candidate       string                   `json:"candidate"`
		Verdict         review.Verdict           `json:"verdict"`
		Criteria        []review.CriterionResult `json:"criteria"`
		EvidenceDigest  string                   `json:"evidence_digest"`
		SubmittedAt     time.Time                `json:"submitted_at"`
	}{request.SchemaVersion, request.CorrelationID, request.ReviewID, request.HandoffID, request.WorkRevisionID, request.WorkID, request.LeaseID, request.LeaseGeneration, tokenDigest(request.Token), request.ActorID, request.ProcessID, request.Candidate, request.Verdict, request.Criteria, request.EvidenceDigest, request.SubmittedAt.UTC()}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return digestHandoffBytes(encoded), nil
}
func digestHandoffBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
