package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// startRecoveryMigrationV8 is intentionally a new forward migration. The v7
// workspace schema remains unchanged so existing databases retain every
// reservation, Git fact, and quarantine record.
const startRecoveryMigrationV8 = `
CREATE TABLE start_run_requests (
  operation_id TEXT PRIMARY KEY REFERENCES operations(operation_id) ON DELETE RESTRICT,
  request_digest TEXT NOT NULL,
  request_json BLOB NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE start_run_results (
  operation_id TEXT PRIMARY KEY REFERENCES operations(operation_id) ON DELETE RESTRICT,
  result_digest TEXT NOT NULL,
  result_json BLOB NOT NULL,
  completed_at TEXT NOT NULL
);
CREATE TRIGGER immutable_start_run_request_update
BEFORE UPDATE ON start_run_requests BEGIN
  SELECT RAISE(ABORT, 'start-run recovery request is immutable');
END;
CREATE TRIGGER immutable_start_run_request_delete
BEFORE DELETE ON start_run_requests BEGIN
  SELECT RAISE(ABORT, 'start-run recovery request is immutable');
END;
CREATE TRIGGER immutable_start_run_result_update
BEFORE UPDATE ON start_run_results BEGIN
  SELECT RAISE(ABORT, 'start-run terminal result is immutable');
END;
CREATE TRIGGER immutable_start_run_result_delete
BEFORE DELETE ON start_run_results BEGIN
  SELECT RAISE(ABORT, 'start-run terminal result is immutable');
END;
DROP TRIGGER terminal_workspace_status_update;
CREATE TRIGGER terminal_workspace_status_update
BEFORE UPDATE OF status ON workspaces
WHEN (OLD.status='quarantined' AND NEW.status<>OLD.status)
  OR (OLD.status='blocked' AND NEW.status NOT IN ('blocked','quarantined'))
BEGIN SELECT RAISE(ABORT, 'terminal workspace status is immutable'); END;`

func (s *Store) PrepareStartRun(ctx context.Context, request execution.StartRunRequest) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return recoveryStoreError(request.Claim.OperationID, request.Claim.Now, "encode start-run recovery request", err)
	}
	digest := digestRecoveryPayload(encoded)
	claimDigest := requestDigest(request.Claim)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return translateClaim(err, request.Claim, recoveryClaimAggregates(request.Claim))
	}
	defer func() { _ = tx.Rollback() }()

	var kind, storedClaimDigest string
	err = tx.QueryRowContext(ctx, `SELECT operation_kind,request_digest FROM operations WHERE operation_id=?`, request.Claim.OperationID).Scan(&kind, &storedClaimDigest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		correlation := request.Claim.CorrelationID
		if correlation == "" {
			correlation = request.Claim.OperationID.String()
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,created_at) VALUES(?,'start_run',?,?,?,'started',?)`,
			request.Claim.OperationID, request.Claim.OperationID, correlation, claimDigest, formatTime(request.Claim.Now))
		if err != nil {
			return translateClaim(err, request.Claim, recoveryClaimAggregates(request.Claim))
		}
	case err != nil:
		return translateClaim(err, request.Claim, recoveryClaimAggregates(request.Claim))
	case kind != "start_run" || storedClaimDigest != claimDigest:
		return execution.NewDiagnostic(execution.KindIdempotencyConflict, "operation ID was reused for a different start-run request", execution.RetryNever,
			request.Claim.CorrelationID, request.Claim.OperationID, recoveryClaimAggregates(request.Claim),
			map[string]string{"operation_kind": kind, "request_digest": storedClaimDigest},
			map[string]string{"operation_kind": "start_run", "request_digest": claimDigest}, request.Claim.Now)
	}

	var storedDigest string
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM start_run_requests WHERE operation_id=?`, request.Claim.OperationID).Scan(&storedDigest)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `INSERT INTO start_run_requests(operation_id,request_digest,request_json,created_at) VALUES(?,?,?,?)`, request.Claim.OperationID, digest, encoded, formatTime(request.Claim.Now)); err != nil {
			return translateClaim(err, request.Claim, recoveryClaimAggregates(request.Claim))
		}
	case err != nil:
		return translateClaim(err, request.Claim, recoveryClaimAggregates(request.Claim))
	case storedDigest != digest:
		return execution.NewDiagnostic(execution.KindIdempotencyConflict, "operation recovery envelope differs from the durable request", execution.RetryNever,
			request.Claim.CorrelationID, request.Claim.OperationID, recoveryClaimAggregates(request.Claim),
			map[string]string{"request_digest": digest}, map[string]string{"request_digest": storedDigest}, request.Claim.Now)
	}
	if err := tx.Commit(); err != nil {
		return translateClaim(err, request.Claim, recoveryClaimAggregates(request.Claim))
	}
	return nil
}

func (s *Store) LoadStartRun(ctx context.Context, operationID ids.OperationID) (execution.StartRecoveryRecord, error) {
	var result execution.StartRecoveryRecord
	var encoded []byte
	var stage, deadline string
	var terminal int
	err := s.db.QueryRowContext(ctx, `SELECT r.request_json,o.status,
COALESCE(o.result_run_id,''),COALESCE(o.result_lease_id,''),COALESCE(o.result_generation,0),COALESCE(o.result_deadline,''),
COALESCE((SELECT l.status FROM leases l JOIN runs n ON n.run_id=l.run_id WHERE n.operation_id=o.operation_id),''),
COALESCE((SELECT w.status FROM workspaces w WHERE w.operation_id=o.operation_id),''),
COALESCE((SELECT w.stage FROM workspaces w WHERE w.operation_id=o.operation_id),''),
EXISTS(SELECT 1 FROM start_run_results z WHERE z.operation_id=o.operation_id)
	FROM start_run_requests r JOIN operations o ON o.operation_id=r.operation_id WHERE r.operation_id=?`, operationID).Scan(
		&encoded, &result.OperationStatus, &result.RunID, &result.LeaseID, &result.Generation, &deadline, &result.LeaseStatus, &result.WorkspaceStatus, &stage, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		return result, execution.NewDiagnostic(execution.KindNotFound, "start-run operation has no durable recovery envelope", execution.RetryDecisionRequired,
			operationID.String(), operationID, map[string]string{"operation_id": operationID.String()},
			map[string]string{"recovery_request": "missing"}, map[string]string{"recovery_request": "durable"}, time.Now().UTC())
	}
	if err != nil {
		return result, recoveryStoreError(operationID, time.Now(), "load start-run recovery request", err)
	}
	if err := json.Unmarshal(encoded, &result.Request); err != nil {
		return result, recoveryStoreError(operationID, time.Now(), "decode start-run recovery request", err)
	}
	result.WorkspaceStage = workspaceStage(stage)
	result.TerminalRecorded = terminal == 1
	if deadline != "" {
		result.Deadline, err = time.Parse(time.RFC3339Nano, deadline)
		if err != nil {
			return result, recoveryStoreError(operationID, time.Now(), "decode start-run lease deadline", err)
		}
	}
	if result.LeaseID != "" && result.Generation > 0 {
		result.Token = s.tokenFor(operationID, result.RunID, result.LeaseID, result.Generation)
	}
	return result, nil
}

func (s *Store) CompleteStartRun(ctx context.Context, capsule execution.RunCapsule, completedAt time.Time) error {
	terminal := capsule
	terminal.Token = ""
	terminal.Replayed = false
	encoded, err := json.Marshal(terminal)
	if err != nil {
		return recoveryStoreError(capsule.OperationID, time.Now(), "encode terminal start-run result", err)
	}
	digest := digestRecoveryPayload(encoded)
	_, err = s.db.ExecContext(ctx, `INSERT OR IGNORE INTO start_run_results(operation_id,result_digest,result_json,completed_at) VALUES(?,?,?,?)`, capsule.OperationID, digest, encoded, formatTime(completedAt))
	if err != nil {
		return recoveryStoreError(capsule.OperationID, time.Now(), "persist terminal start-run result", err)
	}
	var storedDigest string
	if err := s.db.QueryRowContext(ctx, `SELECT result_digest FROM start_run_results WHERE operation_id=?`, capsule.OperationID).Scan(&storedDigest); err != nil {
		return recoveryStoreError(capsule.OperationID, time.Now(), "inspect terminal start-run result", err)
	}
	if storedDigest != digest {
		return execution.NewDiagnostic(execution.KindIdempotencyConflict, "terminal start-run result changed across replay", execution.RetryNever,
			capsule.CorrelationID, capsule.OperationID, capsule.AggregateIDs,
			map[string]string{"result_digest": digest}, map[string]string{"result_digest": storedDigest}, time.Now().UTC())
	}
	return nil
}

// WorkspaceLeaseStatus provides the live maintenance surface with the status
// of the exact lease generation immutably bound to a durable workspace.
func (s *Store) WorkspaceLeaseStatus(ctx context.Context, workspaceID ids.WorkspaceID) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `SELECT l.status FROM workspaces w JOIN leases l ON l.lease_id=w.lease_id AND l.generation=w.lease_generation WHERE w.workspace_id=?`, workspaceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace lease was not found"}
	}
	if err != nil {
		return "", storeWorkspaceError("read workspace lease status", err)
	}
	return status, nil
}

// finalizeAuthorityExpiryTx is the one workspace-lifetime transition used by
// deadline expiry and WorkRevision invalidation after their respective lease
// compare-and-swap succeeds.
func finalizeAuthorityExpiryTx(ctx context.Context, tx *sql.Tx, leaseID ids.LeaseID, runID ids.RunID, generation int64, reason string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE runs SET status='expired' WHERE run_id=? AND status IN ('active','starting')`, runID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE workspaces SET status='quarantined',quarantine_reason=?,quarantined_at=?,updated_at=?
WHERE lease_id=? AND lease_generation=? AND status<>'quarantined'`, reason, formatTime(now), formatTime(now), leaseID, generation)
	return err
}

func digestRecoveryPayload(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func recoveryClaimAggregates(request execution.StartRequest) map[string]string {
	return map[string]string{
		"operation_id": request.OperationID.String(), "project_id": request.ProjectID.String(), "mission_id": request.MissionID.String(),
		"work_id": request.WorkID.String(), "work_revision_id": request.WorkRevisionID.String(), "actor_id": request.ActorID.String(), "process_id": request.ProcessID.String(),
	}
}

func recoveryStoreError(operationID ids.OperationID, now time.Time, message string, cause error) error {
	diagnostic := execution.NewDiagnostic(execution.KindControlStoreBusy, message, execution.RetrySafeSameOperation, operationID.String(), operationID,
		map[string]string{"operation_id": operationID.String()}, map[string]string{"control_store": "unavailable"}, map[string]string{"control_store": "recovery facts readable"}, now)
	diagnostic.Cause = cause
	return diagnostic
}

func workspaceStage(value string) workspace.Stage { return workspace.Stage(value) }
