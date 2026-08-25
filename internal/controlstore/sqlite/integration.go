package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func (s *Store) LookupIntegrationOperation(ctx context.Context, request integration.Request) (integration.Record, bool, error) {
	digest, err := integrationRequestDigest(request)
	if err != nil {
		return integration.Record{}, false, err
	}
	var stored string
	err = s.db.QueryRowContext(ctx, `SELECT request_digest FROM integration_commands WHERE operation_id=?`, request.OperationID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return integration.Record{}, false, nil
	}
	if err != nil {
		return integration.Record{}, false, storeIntegrationError("inspect integration replay", err)
	}
	if stored != digest {
		return integration.Record{}, false, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "integration OperationID was reused for a different request"}
	}
	record, err := loadIntegration(ctx, s.db, request.OperationID, s.tokenKey)
	return record, true, err
}

// PrepareIntegrationAuthority validates the durable integration contract and
// constructs a deterministic repository-fence intent without publishing any
// SQLite mutation authority. The caller must hold the repository acquisition
// lock from the fence observation through ActivateIntegrationAuthority.
func (s *Store) PrepareIntegrationAuthority(ctx context.Context, request integration.Request, preflightTarget string, currentFence integration.FenceFacts) (integration.Record, error) {
	if err := request.Validate(); err != nil {
		return integration.Record{}, err
	}
	digest, err := integrationRequestDigest(request)
	if err != nil {
		return integration.Record{}, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration authority preparation", err)
	}
	defer func() { _ = tx.Rollback() }()
	var stored string
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM integration_commands WHERE operation_id=?`, request.OperationID).Scan(&stored)
	if err == nil {
		if stored != digest {
			return integration.Record{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "integration OperationID was reused for a different request"}
		}
		return loadIntegration(ctx, tx, request.OperationID, s.tokenKey)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return integration.Record{}, storeIntegrationError("inspect integration authority replay", err)
	}
	var handoffID ids.HandoffID
	var missionID ids.MissionID
	var revisionID ids.WorkRevisionID
	var workID ids.WorkID
	var repositoryID ids.RepositoryID
	var candidate, verdict, targetRef string
	err = tx.QueryRowContext(ctx, `SELECT rv.handoff_id,h.mission_id,rv.work_revision_id,rv.work_id,h.repository_id,rv.candidate,rv.verdict,repo.target_ref
FROM reviews rv JOIN handoffs h ON h.handoff_id=rv.handoff_id JOIN repositories repo ON repo.repository_id=h.repository_id
WHERE rv.review_id=?`, request.ReviewID).Scan(&handoffID, &missionID, &revisionID, &workID, &repositoryID, &candidate, &verdict, &targetRef)
	if errors.Is(err, sql.ErrNoRows) || verdict != "approved" {
		return integration.Record{}, &integration.Error{Kind: integration.KindNotApproved, Message: "integration requires one immutable approved review", Cause: err}
	}
	if err != nil {
		return integration.Record{}, storeIntegrationError("read approved candidate", err)
	}
	if repositoryID != request.RepositoryID || targetRef != request.TargetRef || candidate != request.Candidate {
		return integration.Record{}, &integration.Error{Kind: integration.KindNotApproved, Message: "integration command differs from the immutable approved handoff", Observed: map[string]string{"repository_id": request.RepositoryID.String(), "target_ref": request.TargetRef, "candidate": request.Candidate}, Expected: map[string]string{"repository_id": repositoryID.String(), "target_ref": targetRef, "candidate": candidate}}
	}
	eligibility, reconciliationID, err := loadWorkEligibility(ctx, tx, revisionID, workID, workgraph.StatusApproved)
	if err != nil {
		return integration.Record{}, storeIntegrationError("read integration eligibility", err)
	}
	if !eligibility.IntegrationEligible {
		return integration.Record{}, &integration.Error{Kind: integration.KindRevisionIneligible, Message: "WorkRevision reconciliation forbids integration", Observed: map[string]string{"reconciliation_id": reconciliationID, "reason": eligibility.Reason}, Expected: map[string]string{"integration_eligible": "true"}}
	}
	var activeOperation ids.OperationID
	var activeDeadline string
	err = tx.QueryRowContext(ctx, `SELECT operation_id,deadline FROM integration_mutation_leases WHERE repository_id=? AND target_ref=? AND status IN ('pending','active') ORDER BY generation DESC LIMIT 1`, request.RepositoryID, request.TargetRef).Scan(&activeOperation, &activeDeadline)
	if err == nil {
		deadline, parseErr := time.Parse(time.RFC3339Nano, activeDeadline)
		if parseErr != nil {
			return integration.Record{}, storeIntegrationError("decode current target authority deadline", parseErr)
		}
		if deadline.After(time.Now().UTC()) {
			return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityBusy, Message: "another integration owns target mutation authority", Observed: map[string]string{"operation_id": activeOperation.String()}, Expected: map[string]string{"target_authority": "available"}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return integration.Record{}, storeIntegrationError("inspect target mutation authority", err)
	}
	var sqliteGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0) FROM integration_mutation_leases WHERE repository_id=? AND target_ref=?`, request.RepositoryID, request.TargetRef).Scan(&sqliteGeneration); err != nil {
		return integration.Record{}, storeIntegrationError("read latest SQLite target mutation generation", err)
	}
	if currentFence.Generation < 0 {
		return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "repository fence has an invalid generation"}
	}
	if !request.StartedAt.Add(request.LeaseTTL).After(time.Now().UTC()) {
		return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "integration request deadline elapsed before repository fencing"}
	}
	for _, operationID := range currentFence.SupersededOperations {
		if operationID == request.OperationID {
			return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "a superseded repository-fence operation cannot reclaim target mutation authority", Observed: map[string]string{"operation_id": request.OperationID.String()}, Expected: map[string]string{"repository_fence_operation": "never previously superseded"}}
		}
	}
	if currentFence.Object != "" && currentFence.OperationID != request.OperationID && currentFence.Deadline.After(time.Now().UTC()) {
		var accounted int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=? AND repository_id=? AND target_ref=? AND generation=? AND fence_object=?`, currentFence.OperationID, request.RepositoryID, request.TargetRef, currentFence.Generation, currentFence.Object).Scan(&accounted); err != nil {
			return integration.Record{}, storeIntegrationError("check whether current repository fence is durable", err)
		}
		if accounted == 0 {
			return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityBusy, Message: "an unexpired repository fence is awaiting SQLite activation", Observed: map[string]string{"operation_id": currentFence.OperationID.String(), "deadline": formatTime(currentFence.Deadline)}, Expected: map[string]string{"repository_fence": "expired or activated"}}
		}
	}
	generation := sqliteGeneration
	if currentFence.Generation > generation {
		generation = currentFence.Generation
	}
	predecessor := currentFence.Object
	if currentFence.OperationID == request.OperationID && currentFence.Object != "" {
		if currentFence.RequestDigest != digest {
			return integration.Record{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "repository fence OperationID was reused for a different integration request"}
		}
		if sqliteGeneration > currentFence.Generation {
			return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "SQLite generation is newer than the same-operation repository fence"}
		}
		generation = currentFence.Generation
		predecessor = currentFence.PredecessorObject
		if !currentFence.Deadline.After(time.Now().UTC()) {
			return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "orphaned same-operation repository fence expired before SQLite activation"}
		}
	} else {
		generation++
	}
	token := s.tokenFor(request.OperationID, "integration-run", "integration-lease", generation)
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit integration authority preparation", err)
	}
	return integration.Record{
		OperationID: request.OperationID, ReviewID: request.ReviewID, HandoffID: handoffID, MissionID: missionID,
		WorkRevisionID: revisionID, WorkID: workID, RepositoryID: request.RepositoryID, TargetRef: request.TargetRef,
		ActorID: request.ActorID, ProcessID: request.ProcessID, Candidate: request.Candidate, PreflightTarget: preflightTarget,
		MutationGeneration: generation, FencePredecessor: predecessor, FenceRequestDigest: digest, Token: token,
		LeaseDeadline: request.StartedAt.Add(request.LeaseTTL), Status: integration.StatusAuthorizing,
		CleanupStatus: integration.CleanupPending, StartedAt: request.StartedAt,
	}, nil
}

func (s *Store) ActivateIntegrationAuthority(ctx context.Context, record integration.Record, fence integration.FenceFacts) (integration.Record, error) {
	if fence.Ref == "" || fence.Object == "" || fence.Generation != record.MutationGeneration || fence.OperationID != record.OperationID || fence.RepositoryID != record.RepositoryID || fence.TargetRef != record.TargetRef || fence.RequestDigest != record.FenceRequestDigest || fence.PredecessorObject != record.FencePredecessor || fence.PreflightTarget != record.PreflightTarget || !fence.Deadline.Equal(record.LeaseDeadline) || fence.TokenDigest != tokenDigest(record.Token) {
		return integration.Record{}, &integration.Error{Kind: integration.KindInvalid, Message: "Git fencing facts are required before activation"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration fence activation", err)
	}
	defer func() { _ = tx.Rollback() }()
	var storedDigest string
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM integration_commands WHERE operation_id=?`, record.OperationID).Scan(&storedDigest)
	if err == nil {
		if storedDigest != record.FenceRequestDigest {
			return integration.Record{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "integration OperationID was reused for a different request"}
		}
		current, loadErr := loadIntegration(ctx, tx, record.OperationID, s.tokenKey)
		if loadErr != nil {
			return integration.Record{}, loadErr
		}
		if current.MutationGeneration != fence.Generation || current.FenceRef != fence.Ref || current.FenceObject != fence.Object {
			return integration.Record{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "active integration names different repository fencing facts"}
		}
		return current, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return integration.Record{}, storeIntegrationError("inspect integration activation replay", err)
	}
	if !record.LeaseDeadline.After(time.Now().UTC()) {
		return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "repository-fenced target mutation authority expired before SQLite activation"}
	}
	var candidate, verdict, targetRef string
	var handoffID ids.HandoffID
	var missionID ids.MissionID
	var revisionID ids.WorkRevisionID
	var workID ids.WorkID
	var repositoryID ids.RepositoryID
	err = tx.QueryRowContext(ctx, `SELECT rv.handoff_id,h.mission_id,rv.work_revision_id,rv.work_id,h.repository_id,rv.candidate,rv.verdict,repo.target_ref
FROM reviews rv JOIN handoffs h ON h.handoff_id=rv.handoff_id JOIN repositories repo ON repo.repository_id=h.repository_id
WHERE rv.review_id=?`, record.ReviewID).Scan(&handoffID, &missionID, &revisionID, &workID, &repositoryID, &candidate, &verdict, &targetRef)
	if errors.Is(err, sql.ErrNoRows) || verdict != "approved" {
		return integration.Record{}, &integration.Error{Kind: integration.KindNotApproved, Message: "integration approval disappeared before repository-fence activation", Cause: err}
	}
	if err != nil {
		return integration.Record{}, storeIntegrationError("revalidate approved candidate for activation", err)
	}
	if handoffID != record.HandoffID || missionID != record.MissionID || revisionID != record.WorkRevisionID || workID != record.WorkID || repositoryID != record.RepositoryID || targetRef != record.TargetRef || candidate != record.Candidate {
		return integration.Record{}, &integration.Error{Kind: integration.KindNotApproved, Message: "integration binding changed before repository-fence activation"}
	}
	eligibility, reconciliationID, err := loadWorkEligibility(ctx, tx, revisionID, workID, workgraph.StatusApproved)
	if err != nil {
		return integration.Record{}, storeIntegrationError("revalidate integration eligibility for activation", err)
	}
	if !eligibility.IntegrationEligible {
		return integration.Record{}, &integration.Error{Kind: integration.KindRevisionIneligible, Message: "WorkRevision reconciliation forbids repository-fence activation", Observed: map[string]string{"reconciliation_id": reconciliationID, "reason": eligibility.Reason}, Expected: map[string]string{"integration_eligible": "true"}}
	}
	var sqliteGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0) FROM integration_mutation_leases WHERE repository_id=? AND target_ref=?`, record.RepositoryID, record.TargetRef).Scan(&sqliteGeneration); err != nil {
		return integration.Record{}, storeIntegrationError("revalidate latest SQLite generation for activation", err)
	}
	if record.MutationGeneration <= sqliteGeneration {
		return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "repository fence generation is not newer than durable SQLite history", Observed: map[string]string{"generation": fmt.Sprint(record.MutationGeneration)}, Expected: map[string]string{"generation_greater_than": fmt.Sprint(sqliteGeneration)}}
	}
	when := formatTime(record.StartedAt)
	if _, err := tx.ExecContext(ctx, `UPDATE integration_mutation_leases SET status='expired' WHERE repository_id=? AND target_ref=? AND status IN ('pending','active') AND generation<?`, record.RepositoryID, record.TargetRef, record.MutationGeneration); err != nil {
		return integration.Record{}, storeIntegrationError("expire older target authority after repository fencing", err)
	}
	var conflicting ids.OperationID
	err = tx.QueryRowContext(ctx, `SELECT operation_id FROM integration_mutation_leases WHERE repository_id=? AND target_ref=? AND status IN ('pending','active') AND generation>=?`, record.RepositoryID, record.TargetRef, record.MutationGeneration).Scan(&conflicting)
	if err == nil {
		return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityBusy, Message: "equal or newer SQLite target authority already exists", Observed: map[string]string{"operation_id": conflicting.String()}}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return integration.Record{}, storeIntegrationError("inspect activation generation conflict", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, record.ActorID, when); err != nil {
		return integration.Record{}, storeIntegrationError("persist integration actor", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO processes(process_id,actor_id,observed_at) VALUES(?,?,?) ON CONFLICT(process_id) DO UPDATE SET observed_at=excluded.observed_at WHERE processes.actor_id=excluded.actor_id`, record.ProcessID, record.ActorID, when)
	if err != nil {
		return integration.Record{}, storeIntegrationError("persist integration process", err)
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr != nil || changed != 1 {
		return integration.Record{}, storeIntegrationError("integration ProcessID belongs to another ActorID", rowsErr)
	}
	envelope, err := sealToken(s.tokenKey, record.OperationID.String(), record.Token)
	if err != nil {
		return integration.Record{}, storeIntegrationError("seal target mutation token", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO integration_commands(operation_id,request_digest,review_id,actor_id,process_id,token_envelope,occurred_at) VALUES(?,?,?,?,?,?,?)`, record.OperationID, record.FenceRequestDigest, record.ReviewID, record.ActorID, record.ProcessID, envelope, when); err != nil {
		return integration.Record{}, storeIntegrationError("persist integration command", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO integration_mutation_leases(operation_id,repository_id,target_ref,generation,token_digest,status,deadline,acquired_at,fence_ref,fence_object) VALUES(?,?,?,?,?,'active',?,?,?,?)`, record.OperationID, record.RepositoryID, record.TargetRef, record.MutationGeneration, tokenDigest(record.Token), formatTime(record.LeaseDeadline), when, fence.Ref, fence.Object); err != nil {
		if isSQLiteBusy(err) || isUniqueConstraint(err) {
			return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityBusy, Message: "target mutation authority was activated concurrently", Cause: err}
		}
		return integration.Record{}, storeIntegrationError("persist active target mutation authority", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO integrations(operation_id,review_id,handoff_id,mission_id,work_revision_id,work_id,repository_id,target_ref,actor_id,process_id,candidate,preflight_target,status,started_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?, 'authorizing',?)`, record.OperationID, record.ReviewID, record.HandoffID, record.MissionID, record.WorkRevisionID, record.WorkID, record.RepositoryID, record.TargetRef, record.ActorID, record.ProcessID, record.Candidate, record.PreflightTarget, when); err != nil {
		return integration.Record{}, storeIntegrationError("persist integration preflight", err)
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit Git-fenced target mutation authority", err)
	}
	return loadIntegration(ctx, s.db, record.OperationID, s.tokenKey)
}

func (s *Store) RecordIntegrationWorkspace(ctx context.Context, record integration.Record, facts integration.WorkspaceFacts) (integration.Record, error) {
	if !facts.Registered || !facts.Detached || facts.Path == "" || !facts.CandidateReachable {
		return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "managed integration workspace facts are incomplete"}
	}
	if record.WorkspacePath != "" && record.WorkspacePath != facts.Path {
		return integration.Record{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "integration operation is already bound to another workspace"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration workspace record", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireIntegrationAuthority(ctx, tx, record); err != nil {
		return integration.Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integrations SET workspace_path=?,status=CASE WHEN status='authorizing' THEN 'prepared' ELSE status END WHERE operation_id=?`, facts.Path, record.OperationID); err != nil {
		return integration.Record{}, storeIntegrationError("record managed integration workspace", err)
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit managed integration workspace", err)
	}
	return loadIntegration(ctx, s.db, record.OperationID, s.tokenKey)
}

func (s *Store) RevalidateIntegration(ctx context.Context, record integration.Record, currentTarget string) (integration.Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration revalidation", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadIntegration(ctx, tx, record.OperationID, s.tokenKey)
	if err != nil {
		return integration.Record{}, err
	}
	if current.Status == integration.StatusDone {
		return current, nil
	}
	if current.Status == integration.StatusRepairRequired {
		return current, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration already requires explicit repair"}
	}
	var leaseStatus, deadline, digest string
	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT status,deadline,generation,token_digest FROM integration_mutation_leases WHERE operation_id=?`, record.OperationID).Scan(&leaseStatus, &deadline, &generation, &digest); err != nil {
		return integration.Record{}, storeIntegrationError("read target mutation lease", err)
	}
	deadlineTime, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return integration.Record{}, storeIntegrationError("decode target mutation deadline", err)
	}
	if !deadlineTime.After(time.Now().UTC()) {
		_, _ = tx.ExecContext(ctx, `UPDATE integration_mutation_leases SET status='expired' WHERE operation_id=? AND status='active'`, record.OperationID)
		_ = tx.Commit()
		return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "target mutation authority expired before use", Observed: map[string]string{"status": leaseStatus, "deadline": deadline}, Expected: map[string]string{"status": "active"}}
	}
	if leaseStatus != "active" || generation != record.MutationGeneration || digest != tokenDigest(record.Token) {
		return integration.Record{}, &integration.Error{Kind: integration.KindAuthorityExpired, Message: "target mutation authority expired before use", Observed: map[string]string{"status": leaseStatus, "deadline": deadline}, Expected: map[string]string{"status": "active"}}
	}
	if currentTarget != current.PreflightTarget {
		return integration.Record{}, &integration.Error{Kind: integration.KindTargetMoved, Message: "target changed after durable preflight", Observed: map[string]string{"target": currentTarget}, Expected: map[string]string{"target": current.PreflightTarget}}
	}
	var verdict string
	if err := tx.QueryRowContext(ctx, `SELECT verdict FROM reviews WHERE review_id=? AND candidate=?`, current.ReviewID, current.Candidate).Scan(&verdict); err != nil || verdict != "approved" {
		return integration.Record{}, &integration.Error{Kind: integration.KindNotApproved, Message: "approved review is absent during immediate revalidation", Cause: err}
	}
	eligibility, reconciliationID, err := loadWorkEligibility(ctx, tx, current.WorkRevisionID, current.WorkID, workgraph.StatusApproved)
	if err != nil {
		return integration.Record{}, storeIntegrationError("revalidate WorkRevision integration eligibility", err)
	}
	if !eligibility.IntegrationEligible {
		return integration.Record{}, &integration.Error{Kind: integration.KindRevisionIneligible, Message: "WorkRevision became ineligible before integration", Observed: map[string]string{"reconciliation_id": reconciliationID, "reason": eligibility.Reason}, Expected: map[string]string{"integration_eligible": "true"}}
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit integration revalidation", err)
	}
	return current, nil
}

func (s *Store) RecordIntegrationResult(ctx context.Context, record integration.Record, resultCommit, observedTarget string) (integration.Record, error) {
	if record.ResultCommit != "" && record.ResultCommit != resultCommit {
		return integration.Record{}, &integration.Error{Kind: integration.KindIdempotencyConflict, Message: "integration operation already names a different result"}
	}
	if resultCommit == "" || observedTarget == "" {
		return integration.Record{}, &integration.Error{Kind: integration.KindInvalid, Message: "Git-fact result recording requires result and observed target commits"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration result record", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE integrations SET result_commit=?,observed_target=?,status='result_ready' WHERE operation_id=? AND status IN ('prepared','result_ready') AND (result_commit='' OR result_commit=?)`, resultCommit, observedTarget, record.OperationID, resultCommit)
	if err != nil {
		return integration.Record{}, storeIntegrationError("record integration result", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "integration result could not advance the durable attempt"}
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit integration result", err)
	}
	return loadIntegration(ctx, s.db, record.OperationID, s.tokenKey)
}

func (s *Store) RecordIntegrationRepair(ctx context.Context, record integration.Record, evidence integration.ConflictEvidence, kind integration.ErrorKind, action string) (integration.Record, error) {
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return integration.Record{}, &integration.Error{Kind: integration.KindInvalid, Message: "encode integration repair evidence", Cause: err}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration repair", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := requireIntegrationAuthority(ctx, tx, record); err != nil {
		return integration.Record{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integrations SET status='repair_required',repair_kind=?,repair_action=?,evidence_json=? WHERE operation_id=? AND status<>'done'`, kind, action, encoded, record.OperationID); err != nil {
		return integration.Record{}, storeIntegrationError("record integration repair evidence", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integration_mutation_leases SET status='released' WHERE operation_id=? AND status='active'`, record.OperationID); err != nil {
		return integration.Record{}, storeIntegrationError("release target authority after repair", err)
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit integration repair", err)
	}
	return loadIntegration(ctx, s.db, record.OperationID, s.tokenKey)
}

func (s *Store) CompleteIntegration(ctx context.Context, record integration.Record, observedTarget string) (integration.Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin integration completion", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadIntegration(ctx, tx, record.OperationID, s.tokenKey)
	if err != nil {
		return integration.Record{}, err
	}
	if current.ResultCommit == "" {
		return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "cannot record done without an intended result commit"}
	}
	when := formatTime(time.Now().UTC())
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO integration_done(review_id,operation_id,repository_id,target_ref,candidate,result_commit,observed_target,done_at) VALUES(?,?,?,?,?,?,?,?)`, current.ReviewID, current.OperationID, current.RepositoryID, current.TargetRef, current.Candidate, current.ResultCommit, observedTarget, when); err != nil {
		return integration.Record{}, storeIntegrationError("record immutable integration done fact", err)
	}
	var doneOperation ids.OperationID
	if err := tx.QueryRowContext(ctx, `SELECT operation_id FROM integration_done WHERE review_id=?`, current.ReviewID).Scan(&doneOperation); err != nil {
		return integration.Record{}, storeIntegrationError("read canonical integration done fact", err)
	}
	if doneOperation != current.OperationID {
		if err := tx.Commit(); err != nil {
			return integration.Record{}, storeIntegrationError("commit duplicate completion observation", err)
		}
		return loadIntegration(ctx, s.db, doneOperation, s.tokenKey)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integrations SET status='done',observed_target=?,done_at=? WHERE operation_id=?`, observedTarget, when, current.OperationID); err != nil {
		return integration.Record{}, storeIntegrationError("mark integration done", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE integration_mutation_leases SET status='released' WHERE operation_id=? AND status='active'`, current.OperationID); err != nil {
		return integration.Record{}, storeIntegrationError("release completed target authority", err)
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit integration completion", err)
	}
	return loadIntegration(ctx, s.db, current.OperationID, s.tokenKey)
}

func (s *Store) PublishIntegrationCleanupEligibility(ctx context.Context, record integration.Record, beforeCommit func() error) (integration.Record, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return integration.Record{}, storeIntegrationError("begin cleanup eligibility publication", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadIntegration(ctx, tx, record.OperationID, s.tokenKey)
	if err != nil {
		return integration.Record{}, err
	}
	if current.Status != integration.StatusDone || current.WorkspacePath == "" || current.ResultCommit == "" || current.ObservedTarget == "" {
		return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "cleanup eligibility requires a terminal durable integration binding"}
	}
	if current.CleanupStatus == integration.CleanupComplete || current.CleanupStatus == integration.CleanupEligible {
		return current, nil
	}
	when := formatTime(time.Now().UTC())
	if _, err := tx.ExecContext(ctx, `UPDATE integrations SET cleanup_status='eligible',cleanup_eligible_at=? WHERE operation_id=? AND cleanup_status='pending'`, when, current.OperationID); err != nil {
		return integration.Record{}, storeIntegrationError("publish integration cleanup eligibility", err)
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return integration.Record{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return integration.Record{}, storeIntegrationError("commit integration cleanup eligibility", err)
	}
	return loadIntegration(ctx, s.db, current.OperationID, s.tokenKey)
}

func (s *Store) CompleteIntegrationCleanup(ctx context.Context, record integration.Record) (integration.Record, error) {
	when := formatTime(time.Now().UTC())
	result, err := s.db.ExecContext(ctx, `UPDATE integrations SET cleanup_status='complete',cleanup_completed_at=? WHERE operation_id=? AND status='done' AND cleanup_status IN ('eligible','complete')`, when, record.OperationID)
	if err != nil {
		return integration.Record{}, storeIntegrationError("record integration cleanup completion", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return integration.Record{}, &integration.Error{Kind: integration.KindRepairRequired, Message: "cleanup completion requires durable cleanup eligibility"}
	}
	return loadIntegration(ctx, s.db, record.OperationID, s.tokenKey)
}

type integrationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func requireIntegrationAuthority(ctx context.Context, query integrationQuery, record integration.Record) error {
	var status, deadline, digest string
	var generation int64
	if err := query.QueryRowContext(ctx, `SELECT status,deadline,generation,token_digest FROM integration_mutation_leases WHERE operation_id=?`, record.OperationID).Scan(&status, &deadline, &generation, &digest); err != nil {
		return storeIntegrationError("read target mutation authority", err)
	}
	deadlineTime, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return storeIntegrationError("decode target mutation authority deadline", err)
	}
	if status != "active" || generation != record.MutationGeneration || digest != tokenDigest(record.Token) || !deadlineTime.After(time.Now().UTC()) {
		return &integration.Error{Kind: integration.KindAuthorityExpired, Message: "integration control mutation requires the current target-ref token and generation", Observed: map[string]string{"status": status, "generation": fmt.Sprint(generation)}, Expected: map[string]string{"status": "active", "generation": fmt.Sprint(record.MutationGeneration)}}
	}
	return nil
}

func loadIntegration(ctx context.Context, query integrationQuery, operationID ids.OperationID, tokenKey []byte) (integration.Record, error) {
	var record integration.Record
	var deadline, startedAt, doneAt, cleanupEligibleAt, cleanupCompletedAt string
	var evidence, tokenEnvelope []byte
	var persistedTokenDigest string
	err := query.QueryRowContext(ctx, `SELECT i.operation_id,i.review_id,i.handoff_id,i.mission_id,i.work_revision_id,i.work_id,i.repository_id,i.target_ref,i.actor_id,i.process_id,i.candidate,i.preflight_target,i.workspace_path,i.result_commit,i.observed_target,l.generation,l.fence_ref,l.fence_object,l.deadline,l.token_digest,c.token_envelope,i.status,i.repair_kind,i.repair_action,i.evidence_json,i.started_at,i.done_at,i.cleanup_status,i.cleanup_eligible_at,i.cleanup_completed_at
FROM integrations i JOIN integration_mutation_leases l ON l.operation_id=i.operation_id JOIN integration_commands c ON c.operation_id=i.operation_id WHERE i.operation_id=?`, operationID).Scan(
		&record.OperationID, &record.ReviewID, &record.HandoffID, &record.MissionID, &record.WorkRevisionID, &record.WorkID, &record.RepositoryID, &record.TargetRef,
		&record.ActorID, &record.ProcessID, &record.Candidate, &record.PreflightTarget, &record.WorkspacePath, &record.ResultCommit, &record.ObservedTarget,
		&record.MutationGeneration, &record.FenceRef, &record.FenceObject, &deadline, &persistedTokenDigest, &tokenEnvelope, &record.Status, &record.RepairKind, &record.RepairAction, &evidence, &startedAt, &doneAt, &record.CleanupStatus, &cleanupEligibleAt, &cleanupCompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return record, &integration.Error{Kind: integration.KindStoreFailure, Message: "integration operation was not found", Cause: err}
	}
	if err != nil {
		return record, storeIntegrationError("read integration operation", err)
	}
	record.Token, err = openToken(tokenKey, operationID.String(), tokenEnvelope)
	if err != nil || tokenDigest(record.Token) != persistedTokenDigest {
		return record, storeIntegrationError("open target mutation token", err)
	}
	if record.LeaseDeadline, err = time.Parse(time.RFC3339Nano, deadline); err != nil {
		return record, storeIntegrationError("decode integration lease deadline", err)
	}
	if record.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); err != nil {
		return record, storeIntegrationError("decode integration start time", err)
	}
	if doneAt != "" {
		if record.DoneAt, err = time.Parse(time.RFC3339Nano, doneAt); err != nil {
			return record, storeIntegrationError("decode integration completion time", err)
		}
	}
	if cleanupEligibleAt != "" {
		if record.CleanupEligibleAt, err = time.Parse(time.RFC3339Nano, cleanupEligibleAt); err != nil {
			return record, storeIntegrationError("decode integration cleanup eligibility time", err)
		}
	}
	if cleanupCompletedAt != "" {
		if record.CleanupCompletedAt, err = time.Parse(time.RFC3339Nano, cleanupCompletedAt); err != nil {
			return record, storeIntegrationError("decode integration cleanup completion time", err)
		}
	}
	if len(evidence) != 0 {
		if err := json.Unmarshal(evidence, &record.Evidence); err != nil {
			return record, storeIntegrationError("decode integration repair evidence", err)
		}
	}
	return record, nil
}

func integrationRequestDigest(request integration.Request) (string, error) {
	payload := struct {
		SchemaVersion uint16           `json:"schema_version"`
		CorrelationID string           `json:"correlation_id"`
		OperationID   ids.OperationID  `json:"operation_id"`
		ReviewID      ids.ReviewID     `json:"review_id"`
		RepositoryID  ids.RepositoryID `json:"repository_id"`
		TargetRef     string           `json:"target_ref"`
		ActorID       ids.ActorID      `json:"actor_id"`
		ProcessID     ids.ProcessID    `json:"process_id"`
		Candidate     string           `json:"candidate"`
		LeaseTTL      int64            `json:"lease_ttl_nanoseconds"`
		StartedAt     time.Time        `json:"started_at"`
	}{request.SchemaVersion, request.CorrelationID, request.OperationID, request.ReviewID, request.RepositoryID, request.TargetRef, request.ActorID, request.ProcessID, request.Candidate, int64(request.LeaseTTL), request.StartedAt.UTC()}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", &integration.Error{Kind: integration.KindInvalid, Message: "encode integration command", Cause: err}
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func storeIntegrationError(message string, cause error) error {
	return &integration.Error{Kind: integration.KindStoreFailure, Message: message, Cause: cause}
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") || strings.Contains(message, "constraint failed")
}
