package sqlite

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type Seed struct {
	ProjectID      ids.ProjectID
	MissionID      ids.MissionID
	WorkRevisionID ids.WorkRevisionID
	WorkID         ids.WorkID
}

func (s *Store) SeedAuthority(ctx context.Context, seed Seed) error {
	if _, err := ids.ParseProjectID(seed.ProjectID.String()); err != nil {
		return execution.NewError(execution.KindInvalidCommand, "project ID is invalid", execution.RetryNever, nil)
	}
	if _, err := ids.ParseMissionID(seed.MissionID.String()); err != nil {
		return execution.NewError(execution.KindInvalidCommand, "mission ID is invalid", execution.RetryNever, nil)
	}
	if _, err := ids.ParseWorkRevisionID(seed.WorkRevisionID.String()); err != nil {
		return execution.NewError(execution.KindInvalidCommand, "work revision ID is invalid", execution.RetryNever, nil)
	}
	if _, err := ids.ParseWorkID(seed.WorkID.String()); err != nil {
		return execution.NewError(execution.KindInvalidCommand, "work ID is invalid", execution.RetryNever, nil)
	}
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return err
	}
	var ownerProject, ownerMission string
	err := s.db.QueryRowContext(ctx, `SELECT project_id, mission_id FROM work_revisions WHERE work_revision_id=?`, seed.WorkRevisionID).Scan(&ownerProject, &ownerMission)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.NewDiagnostic(execution.KindInvalidCommand, "SeedAuthority requires an existing finalized canonical WorkRevision", execution.RetryNever, seed.WorkRevisionID.String(), "",
			map[string]string{"work_revision_id": seed.WorkRevisionID.String(), "work_id": seed.WorkID.String()}, map[string]string{"work_revision": "missing"}, map[string]string{"work_revision": "finalized canonical generation"}, time.Now().UTC())
	}
	if err != nil {
		return translate(err, map[string]string{"work_revision_id": seed.WorkRevisionID.String()})
	}
	if ownerProject != seed.ProjectID.String() || ownerMission != seed.MissionID.String() {
		return execution.NewDiagnostic(execution.KindIdempotencyConflict, "work revision identity is immutable", execution.RetryNever, "", "",
			map[string]string{"work_revision_id": seed.WorkRevisionID.String(), "work_id": seed.WorkID.String()},
			map[string]string{"project_id": ownerProject, "mission_id": ownerMission},
			map[string]string{"project_id": seed.ProjectID.String(), "mission_id": seed.MissionID.String()}, time.Now().UTC())
	}
	var finalizedGraph, canonicalMember int
	if err := s.db.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=?),
EXISTS(SELECT 1 FROM work_graph_items WHERE work_revision_id=? AND work_id=?)`, seed.WorkRevisionID, seed.WorkRevisionID, seed.WorkID).Scan(&finalizedGraph, &canonicalMember); err != nil {
		return translate(err, map[string]string{"work_revision_id": seed.WorkRevisionID.String(), "work_id": seed.WorkID.String()})
	}
	if finalizedGraph != 1 || canonicalMember != 1 {
		return execution.NewDiagnostic(execution.KindInvalidCommand, "SeedAuthority requires finalized canonical WorkRevision membership", execution.RetryNever, seed.WorkRevisionID.String(), "",
			map[string]string{"work_revision_id": seed.WorkRevisionID.String(), "work_id": seed.WorkID.String()},
			map[string]string{"canonical_membership": map[bool]string{true: "present", false: "absent"}[canonicalMember == 1], "work_revision": map[bool]string{true: "finalized", false: "bare"}[finalizedGraph == 1]},
			map[string]string{"canonical_membership": "present", "work_revision": "finalized"}, time.Now().UTC())
	}
	return nil
}

func (s *Store) LookupStartOperation(ctx context.Context, request execution.StartRequest) (bool, error) {
	var kind, storedDigest string
	err := s.db.QueryRowContext(ctx, `SELECT operation_kind,request_digest FROM operations WHERE operation_id=?`, request.OperationID).Scan(&kind, &storedDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, translateClaim(err, request, map[string]string{"operation_id": request.OperationID.String()})
	}
	observedDigest := requestDigest(request)
	if kind != "start_run" || storedDigest != observedDigest {
		return false, execution.NewDiagnostic(execution.KindIdempotencyConflict, "operation ID was reused for a different request", execution.RetryNever, request.CorrelationID, request.OperationID,
			map[string]string{"operation_id": request.OperationID.String()},
			map[string]string{"operation_kind": kind, "request_digest": observedDigest},
			map[string]string{"operation_kind": "start_run", "request_digest": storedDigest}, request.Now)
	}
	return true, nil
}

func (s *Store) Claim(ctx context.Context, request execution.StartRequest, runID ids.RunID, leaseID ids.LeaseID) (execution.RunCapsule, error) {
	aggregates := map[string]string{
		"operation_id": request.OperationID.String(), "project_id": request.ProjectID.String(),
		"mission_id": request.MissionID.String(), "work_id": request.WorkID.String(),
		"work_revision_id": request.WorkRevisionID.String(), "actor_id": request.ActorID.String(),
		"process_id": request.ProcessID.String(),
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	defer func() { _ = tx.Rollback() }()

	digest := requestDigest(request)
	if replay, found, err := s.loadOperation(ctx, tx, request, digest); err != nil {
		return execution.RunCapsule{}, err
	} else if found {
		replay.Replayed = true
		return replay, nil
	}
	if err := request.GovernanceContext.Validate(); err != nil {
		diagnostic := claimDiagnostic(request, execution.KindInvalidCommand, "governance_context is invalid", execution.RetryNever, aggregates,
			map[string]string{"governance_context": "invalid"}, map[string]string{"governance_context": "validated immutable context"})
		diagnostic.Cause = err
		return execution.RunCapsule{}, diagnostic
	}
	aggregates["governance_context_id"] = request.GovernanceContext.ID().String()

	var state, ownerProject, ownerMission string
	err = tx.QueryRowContext(ctx, `SELECT wi.state, wr.project_id, wr.mission_id
FROM work_items wi JOIN work_revisions wr ON wr.work_revision_id=wi.work_revision_id
WHERE wi.work_id=? AND wi.work_revision_id=?`, request.WorkID, request.WorkRevisionID).Scan(&state, &ownerProject, &ownerMission)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.RunCapsule{}, claimDiagnostic(request, execution.KindNotFound, "work generation was not seeded", execution.RetryNever, aggregates, map[string]string{"work_item": "missing"}, map[string]string{"work_item": "ready"})
	}
	if err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	if ownerProject != request.ProjectID.String() || ownerMission != request.MissionID.String() {
		return execution.RunCapsule{}, claimDiagnostic(request, execution.KindInvalidCommand, "work revision belongs to a different project or mission", execution.RetryNever, aggregates,
			map[string]string{"project_id": ownerProject, "mission_id": ownerMission},
			map[string]string{"project_id": request.ProjectID.String(), "mission_id": request.MissionID.String()})
	}
	if state != "ready" {
		return execution.RunCapsule{}, claimDiagnostic(request, execution.KindAuthorityConflict, "work is not ready", execution.RetryDecisionRequired, aggregates, map[string]string{"work_state": state}, map[string]string{"work_state": "ready"})
	}
	var finalizedGraph, canonicalMember int
	if err := tx.QueryRowContext(ctx, `SELECT
EXISTS(SELECT 1 FROM work_graph_revisions WHERE work_revision_id=?),
EXISTS(SELECT 1 FROM work_graph_items WHERE work_revision_id=? AND work_id=?)`, request.WorkRevisionID, request.WorkRevisionID, request.WorkID).Scan(&finalizedGraph, &canonicalMember); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	if finalizedGraph != 1 || canonicalMember != 1 {
		return execution.RunCapsule{}, claimDiagnostic(request, execution.KindNotFound, "authority requires finalized canonical WorkRevision membership", execution.RetryNever, aggregates,
			map[string]string{"canonical_membership": map[bool]string{true: "present", false: "absent"}[canonicalMember == 1], "work_revision": map[bool]string{true: "finalized", false: "bare"}[finalizedGraph == 1]},
			map[string]string{"canonical_membership": "present", "work_revision": "finalized"})
	}
	eligibility, reconciliationID, err := loadWorkEligibility(ctx, tx, request.WorkRevisionID, request.WorkID, workgraph.StatusActive)
	if err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	if reconciliationID != "" && !eligibility.HandoffEligible {
		aggregates["reconciliation_id"] = reconciliationID
		return execution.RunCapsule{}, claimDiagnostic(request, execution.KindAuthorityConflict, "WorkRevision reconciliation forbids new authority", execution.RetryDecisionRequired, aggregates,
			map[string]string{"handoff_eligible": "false", "new_authority": "ineligible", "reason": eligibility.Reason},
			map[string]string{"handoff_eligible": "true", "new_authority": "eligible"})
	}

	now := request.Now.UTC()
	correlationID := request.CorrelationID
	if correlationID == "" {
		correlationID = request.OperationID.String()
	}
	var prepared int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE operation_id=?)`, request.OperationID).Scan(&prepared); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	if prepared == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO operations(operation_id, operation_kind, idempotency_key, correlation_id, request_digest, status, created_at) VALUES(?, 'start_run', ?, ?, ?, 'started', ?)`, request.OperationID, request.OperationID, correlationID, digest, now.Format(time.RFC3339Nano)); err != nil {
			return execution.RunCapsule{}, translateClaim(err, request, aggregates)
		}
	}

	var existingLease string
	err = tx.QueryRowContext(ctx, `SELECT lease_id FROM leases WHERE work_id=? AND work_revision_id=? AND status='active'`, request.WorkID, request.WorkRevisionID).Scan(&existingLease)
	if err == nil {
		aggregates["lease_id"] = existingLease
		return execution.RunCapsule{}, commitRefusal(ctx, tx, request, execution.KindAuthorityConflict, "an active implementation lease already exists", aggregates, now)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}

	var generation int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation), 0) + 1 FROM leases WHERE work_id=? AND work_revision_id=?`, request.WorkID, request.WorkRevisionID).Scan(&generation); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	deadline := now.Add(request.LeaseTTL)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id, created_at) VALUES(?, ?)`, request.ActorID, now.Format(time.RFC3339Nano)); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	processResult, err := tx.ExecContext(ctx, `INSERT INTO processes(process_id, actor_id, observed_at) VALUES(?, ?, ?) ON CONFLICT(process_id) DO UPDATE SET observed_at=excluded.observed_at WHERE processes.actor_id=excluded.actor_id`, request.ProcessID, request.ActorID, now.Format(time.RFC3339Nano))
	if err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	if changed, err := processResult.RowsAffected(); err != nil {
		return execution.RunCapsule{}, fmt.Errorf("process identity affected rows: %w", err)
	} else if changed != 1 {
		return execution.RunCapsule{}, claimDiagnostic(request, execution.KindInvalidCommand, "ProcessID is already bound to a different ActorID", execution.RetryNever, aggregates,
			map[string]string{"actor_id": "different durable actor"}, map[string]string{"actor_id": request.ActorID.String()})
	}
	if err := saveGovernanceContextTx(ctx, tx, request.GovernanceContext, request.OperationID, now); err != nil {
		return execution.RunCapsule{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runs(run_id, operation_id, project_id, mission_id, work_id, work_revision_id, actor_id, process_id, generation, status, created_at, governance_context_id) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?)`, runID, request.OperationID, request.ProjectID, request.MissionID, request.WorkID, request.WorkRevisionID, request.ActorID, request.ProcessID, generation, now.Format(time.RFC3339Nano), request.GovernanceContext.ID()); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	token := s.tokenFor(request.OperationID, runID, leaseID, generation)
	if _, err := tx.ExecContext(ctx, `INSERT INTO leases(lease_id, run_id, work_id, work_revision_id, generation, token_digest, status, heartbeat_at, deadline) VALUES(?, ?, ?, ?, ?, ?, 'active', ?, ?)`, leaseID, runID, request.WorkID, request.WorkRevisionID, generation, tokenDigest(token), now.Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano)); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return execution.RunCapsule{}, claimDiagnostic(request, execution.KindAuthorityConflict, "another process won the lease", execution.RetrySafeSameOperation, aggregates, map[string]string{"lease": "already active"}, map[string]string{"lease": "unclaimed"})
		}
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	result := execution.RunCapsule{
		SchemaVersion: execution.ResultSchemaVersion, Kind: "run_started", CorrelationID: correlationID,
		OperationID: request.OperationID, AggregateIDs: aggregates,
		ObservedState: map[string]string{"lease": "active", "work_state": state}, ExpectedState: map[string]string{"work_state": "ready"},
		RetrySafety: execution.RetryNever, RecoveryActions: []string{"heartbeat before the lease deadline"},
		WorkID: request.WorkID, WorkRevisionID: request.WorkRevisionID,
		RunID: runID, GovernanceContextID: request.GovernanceContext.ID(), GovernanceContextAggregate: request.GovernanceContext.Aggregate(),
		LeaseID: leaseID, Generation: generation, Token: token, Deadline: deadline,
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='succeeded', result_run_id=?, result_lease_id=?, result_generation=?, result_deadline=? WHERE operation_id=?`, runID, leaseID, generation, deadline.Format(time.RFC3339Nano), request.OperationID); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	event := result
	event.Kind = "lease_granted"
	event.Token = ""
	payload, _ := json.Marshal(event)
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(operation_id, kind, payload, occurred_at) VALUES(?, 'lease_granted', ?, ?)`, request.OperationID, string(payload), now.Format(time.RFC3339Nano)); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	if err := tx.Commit(); err != nil {
		return execution.RunCapsule{}, translateClaim(err, request, aggregates)
	}
	return result, nil
}

func (s *Store) loadOperation(ctx context.Context, tx *sql.Tx, request execution.StartRequest, digest string) (execution.RunCapsule, bool, error) {
	operationID := request.OperationID
	var storedDigest, correlationID, status, runID, leaseID, deadlineText, errorKind, errorPayload string
	var tokenEnvelope []byte
	var generation int64
	err := tx.QueryRowContext(ctx, `SELECT request_digest, correlation_id, status, COALESCE(result_run_id,''), COALESCE(result_lease_id,''), COALESCE(result_generation,0), COALESCE(result_deadline,''), COALESCE(result_error_kind,''), COALESCE(result_error_payload,''), COALESCE(result_token_envelope,x'') FROM operations WHERE operation_id=?`, operationID).Scan(&storedDigest, &correlationID, &status, &runID, &leaseID, &generation, &deadlineText, &errorKind, &errorPayload, &tokenEnvelope)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.RunCapsule{}, false, nil
	}
	if err != nil {
		return execution.RunCapsule{}, false, err
	}
	if storedDigest != digest {
		return execution.RunCapsule{}, false, execution.NewDiagnostic(execution.KindIdempotencyConflict, "operation ID was reused for a different request", execution.RetryNever, operationID.String(), operationID, map[string]string{"operation_id": operationID.String()}, map[string]string{"request_digest": digest}, map[string]string{"request_digest": storedDigest}, time.Now().UTC())
	}
	if status == "failed" {
		var diagnostic execution.DiagnosticError
		if errorPayload != "" && json.Unmarshal([]byte(errorPayload), &diagnostic) == nil {
			normalizeReplayedDiagnostic(&diagnostic, request, status)
			return execution.RunCapsule{}, false, &diagnostic
		}
		return execution.RunCapsule{}, false, execution.NewDiagnostic(execution.ErrorKind(errorKind), "recorded claim refusal", execution.RetrySafeSameOperation, operationID.String(), operationID, map[string]string{"operation_id": operationID.String()}, map[string]string{"operation_status": status}, map[string]string{"operation_status": "terminal"}, time.Now().UTC())
	}
	if status == "started" {
		return execution.RunCapsule{}, false, nil
	}
	if status != "succeeded" {
		return execution.RunCapsule{}, false, execution.NewDiagnostic(execution.KindAuthorityConflict, "operation is not terminal", execution.RetrySafeSameOperation, operationID.String(), operationID, map[string]string{"operation_id": operationID.String()}, map[string]string{"operation_status": status}, map[string]string{"operation_status": "terminal"}, time.Now().UTC())
	}
	var leaseStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM leases WHERE lease_id=? AND run_id=? AND generation=?`, leaseID, runID, generation).Scan(&leaseStatus); err != nil {
		return execution.RunCapsule{}, false, translateClaim(err, request, map[string]string{"operation_id": operationID.String(), "lease_id": leaseID, "run_id": runID})
	}
	if leaseStatus != "active" {
		return execution.RunCapsule{}, false, execution.NewDiagnostic(execution.KindStaleAuthority, "recorded StartRun authority is no longer active", execution.RetryNever,
			correlationID, operationID,
			map[string]string{"operation_id": operationID.String(), "lease_id": leaseID, "run_id": runID, "work_id": request.WorkID.String(), "work_revision_id": request.WorkRevisionID.String()},
			map[string]string{"lease_status": leaseStatus}, map[string]string{"lease_status": "active"}, request.Now.UTC())
	}
	deadline, err := time.Parse(time.RFC3339Nano, deadlineText)
	if err != nil {
		return execution.RunCapsule{}, false, fmt.Errorf("parse stored deadline: %w", err)
	}
	token := s.tokenFor(operationID, ids.RunID(runID), ids.LeaseID(leaseID), generation)
	if len(tokenEnvelope) != 0 {
		token, err = openToken(s.tokenKey, operationID.String(), tokenEnvelope)
		if err != nil {
			return execution.RunCapsule{}, false, fmt.Errorf("recover migrated operation token: %w", err)
		}
	}
	governance, err := loadRunGovernance(ctx, tx, ids.RunID(runID), ids.LeaseID(leaseID))
	if err != nil {
		return execution.RunCapsule{}, false, err
	}
	aggregateIDs := map[string]string{
		"operation_id": operationID.String(), "project_id": request.ProjectID.String(), "mission_id": request.MissionID.String(),
		"work_id": request.WorkID.String(), "work_revision_id": request.WorkRevisionID.String(), "actor_id": request.ActorID.String(), "process_id": request.ProcessID.String(),
	}
	var governanceID ids.GovernanceContextID
	var governanceAggregate governancecontext.Commitment
	if governance.Provenance == GovernanceVerified {
		governanceID = governance.Context.ID()
		governanceAggregate = governance.Context.Aggregate()
		aggregateIDs["governance_context_id"] = governanceID.String()
	}
	return execution.RunCapsule{
		SchemaVersion: execution.ResultSchemaVersion, Kind: "run_started", CorrelationID: correlationID,
		OperationID: operationID, AggregateIDs: aggregateIDs,
		ObservedState: map[string]string{"lease": "active", "work_state": "ready"}, ExpectedState: map[string]string{"work_state": "ready"},
		RetrySafety: execution.RetryNever, RecoveryActions: []string{"heartbeat before the lease deadline"},
		WorkID: request.WorkID, WorkRevisionID: request.WorkRevisionID,
		RunID: ids.RunID(runID), GovernanceContextID: governanceID, GovernanceContextAggregate: governanceAggregate,
		LeaseID: ids.LeaseID(leaseID), Generation: generation, Token: token, Deadline: deadline,
	}, true, nil
}

func normalizeReplayedDiagnostic(diagnostic *execution.DiagnosticError, request execution.StartRequest, status string) {
	if diagnostic.SchemaVersion == 0 {
		diagnostic.SchemaVersion = execution.ResultSchemaVersion
	}
	if diagnostic.CorrelationID == "" {
		diagnostic.CorrelationID = request.OperationID.String()
	}
	if diagnostic.OperationID == "" {
		diagnostic.OperationID = request.OperationID
	}
	if diagnostic.AggregateIDs == nil {
		diagnostic.AggregateIDs = map[string]string{"operation_id": request.OperationID.String(), "work_id": request.WorkID.String(), "work_revision_id": request.WorkRevisionID.String(), "actor_id": request.ActorID.String(), "process_id": request.ProcessID.String()}
	}
	if diagnostic.ObservedState == nil {
		diagnostic.ObservedState = map[string]string{"operation_status": status}
	}
	if diagnostic.ExpectedState == nil {
		diagnostic.ExpectedState = map[string]string{"operation_status": "terminal result"}
	}
	if diagnostic.RetrySafety == "" {
		diagnostic.RetrySafety = execution.RetrySafeSameOperation
	}
	if len(diagnostic.RecoveryActions) == 0 {
		diagnostic.RecoveryActions = []string{"inspect the recorded operation result before retrying"}
	}
	if diagnostic.OccurredAt.IsZero() {
		diagnostic.OccurredAt = request.Now.UTC()
	}
}

func commitRefusal(ctx context.Context, tx *sql.Tx, request execution.StartRequest, kind execution.ErrorKind, message string, aggregates map[string]string, now time.Time) error {
	diagnostic := claimDiagnostic(request, kind, message, execution.RetrySafeSameOperation, aggregates, map[string]string{"lease": "active"}, map[string]string{"lease": "unclaimed"})
	diagnostic.RecoveryActions = []string{"retry the same operation ID to inspect this result", "wait for lease expiry or select other ready work"}
	payload, _ := json.Marshal(diagnostic)
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='failed', result_error_kind=?, result_error_payload=? WHERE operation_id=?`, kind, string(payload), request.OperationID); err != nil {
		return translateClaim(err, request, aggregates)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(operation_id, kind, payload, occurred_at) VALUES(?, 'lease_refused', ?, ?)`, request.OperationID, string(payload), now.Format(time.RFC3339Nano)); err != nil {
		return translateClaim(err, request, aggregates)
	}
	if err := tx.Commit(); err != nil {
		return translateClaim(err, request, aggregates)
	}
	return diagnostic
}

type AuthoritySnapshot struct {
	ActiveLeases int
	Runs         int
	Operations   int
	Events       int
}

func (s *Store) AuthoritySnapshot(ctx context.Context, workID ids.WorkID, revisionID ids.WorkRevisionID) (AuthoritySnapshot, error) {
	var snapshot AuthoritySnapshot
	queries := []struct {
		query string
		args  []any
		dest  *int
	}{
		{`SELECT COUNT(*) FROM leases WHERE work_id=? AND work_revision_id=? AND status='active'`, []any{workID, revisionID}, &snapshot.ActiveLeases},
		{`SELECT COUNT(*) FROM runs WHERE work_id=? AND work_revision_id=?`, []any{workID, revisionID}, &snapshot.Runs},
		{`SELECT COUNT(*) FROM operations`, nil, &snapshot.Operations},
		{`SELECT COUNT(*) FROM events`, nil, &snapshot.Events},
	}
	for _, item := range queries {
		if err := s.db.QueryRowContext(ctx, item.query, item.args...).Scan(item.dest); err != nil {
			return snapshot, fmt.Errorf("read authority snapshot: %w", err)
		}
	}
	return snapshot, nil
}

func (s *Store) Heartbeat(ctx context.Context, request execution.HeartbeatRequest) (time.Time, error) {
	deadline := request.Now.UTC().Add(request.LeaseTTL)
	result, err := s.db.ExecContext(ctx, `UPDATE leases SET heartbeat_at=?, deadline=? WHERE lease_id=? AND generation=? AND token_digest=? AND status='active' AND deadline>?`,
		request.Now.UTC().Format(time.RFC3339Nano), deadline.Format(time.RFC3339Nano), request.LeaseID, request.Generation, tokenDigest(request.Token), request.Now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return time.Time{}, translateLease(err, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return time.Time{}, fmt.Errorf("heartbeat affected rows: %w", err)
	}
	if changed != 1 {
		return time.Time{}, staleLeaseError(request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	return deadline, nil
}

func (s *Store) Expire(ctx context.Context, request execution.ExpireRequest) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return translateLease(err, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	defer func() { _ = tx.Rollback() }()
	nowText := request.Now.UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `UPDATE leases SET status='expired' WHERE lease_id=? AND generation=? AND token_digest=? AND status='active' AND deadline<=?`,
		request.LeaseID, request.Generation, tokenDigest(request.Token), nowText)
	if err != nil {
		return translateLease(err, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("expire affected rows: %w", err)
	}
	if changed != 1 {
		var deadlineText string
		err := tx.QueryRowContext(ctx, `SELECT deadline FROM leases WHERE lease_id=? AND generation=? AND token_digest=? AND status='active'`, request.LeaseID, request.Generation, tokenDigest(request.Token)).Scan(&deadlineText)
		if err == nil {
			diagnostic := execution.NewDiagnostic(execution.KindAuthorityConflict, "live lease cannot expire before its deadline", execution.RetrySafeSameOperation, request.CorrelationID, request.OperationID,
				leaseCommandAggregates(request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation),
				map[string]string{"deadline": deadlineText, "now": nowText}, map[string]string{"deadline": "at or before now"}, request.Now)
			diagnostic.RecoveryActions = []string{"wait until the recorded deadline before expiring the lease"}
			return diagnostic
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return translateLease(err, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
		}
		return staleLeaseError(request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	var runID ids.RunID
	if err := tx.QueryRowContext(ctx, `SELECT run_id FROM leases WHERE lease_id=? AND generation=?`, request.LeaseID, request.Generation).Scan(&runID); err != nil {
		return translateLease(err, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	if err := finalizeAuthorityExpiryTx(ctx, tx, request.LeaseID, runID, request.Generation, "lease generation expired", request.Now); err != nil {
		return translateLease(err, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.LeaseID, request.Generation, request.Now)
	}
	return tx.Commit()
}

func staleLeaseError(correlationID string, operationID ids.OperationID, actorID ids.ActorID, processID ids.ProcessID, leaseID ids.LeaseID, generation int64, now time.Time) error {
	diagnostic := execution.NewDiagnostic(execution.KindStaleAuthority, "lease token or generation is no longer current", execution.RetryDecisionRequired, correlationID, operationID,
		leaseCommandAggregates(operationID, actorID, processID, leaseID, generation),
		map[string]string{"authority": "not current"}, map[string]string{"authority": "active current generation and token"}, now)
	diagnostic.RecoveryActions = []string{"inspect current run authority", "start a replacement operation if policy permits"}
	return diagnostic
}

func leaseCommandAggregates(operationID ids.OperationID, actorID ids.ActorID, processID ids.ProcessID, leaseID ids.LeaseID, generation int64) map[string]string {
	return map[string]string{"operation_id": operationID.String(), "actor_id": actorID.String(), "process_id": processID.String(), "lease_id": leaseID.String(), "generation": strconv.FormatInt(generation, 10)}
}

func translateLease(err error, correlationID string, operationID ids.OperationID, actorID ids.ActorID, processID ids.ProcessID, leaseID ids.LeaseID, generation int64, now time.Time) error {
	translated := translate(err, leaseCommandAggregates(operationID, actorID, processID, leaseID, generation))
	var diagnostic *execution.DiagnosticError
	if errors.As(translated, &diagnostic) {
		diagnostic.CorrelationID = correlationID
		diagnostic.OperationID = operationID
		if diagnostic.OccurredAt.IsZero() {
			diagnostic.OccurredAt = now.UTC()
		}
	}
	return translated
}

func (s *Store) tokenFor(operationID ids.OperationID, runID ids.RunID, leaseID ids.LeaseID, generation int64) string {
	mac := hmac.New(sha256.New, s.tokenKey)
	_, _ = fmt.Fprintf(mac, "%s\x00%s\x00%s\x00%d", operationID, runID, leaseID, generation)
	return "fence-" + hex.EncodeToString(mac.Sum(nil))
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func requestDigest(request execution.StartRequest) string {
	value := strings.Join([]string{
		request.CorrelationID, request.ProjectID.String(), request.MissionID.String(), request.WorkRevisionID.String(),
		request.WorkID.String(), request.ActorID.String(), request.ProcessID.String(), request.LeaseTTL.String(), request.CommandDigest,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func claimDiagnostic(request execution.StartRequest, kind execution.ErrorKind, message string, retry execution.RetrySafety, aggregates, observed, expected map[string]string) *execution.DiagnosticError {
	return execution.NewDiagnostic(kind, message, retry, request.CorrelationID, request.OperationID, aggregates, observed, expected, request.Now)
}

func translateClaim(err error, request execution.StartRequest, aggregates map[string]string) error {
	translated := translate(err, aggregates)
	var diagnostic *execution.DiagnosticError
	if errors.As(translated, &diagnostic) {
		diagnostic.SchemaVersion = execution.ResultSchemaVersion
		diagnostic.CorrelationID = request.CorrelationID
		if diagnostic.CorrelationID == "" {
			diagnostic.CorrelationID = request.OperationID.String()
		}
		diagnostic.OperationID = request.OperationID
		if diagnostic.OccurredAt.IsZero() {
			diagnostic.OccurredAt = request.Now.UTC()
		}
	}
	return translated
}

func translate(err error, aggregates map[string]string) error {
	if err == nil {
		return nil
	}
	if execution.IsKind(err, execution.KindAuthorityConflict) || execution.IsKind(err, execution.KindIdempotencyConflict) || execution.IsKind(err, execution.KindNotFound) || execution.IsKind(err, execution.KindInvalidCommand) {
		return err
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") {
		diagnostic := execution.NewDiagnostic(execution.KindControlStoreBusy, "control-store writer remained busy after bounded wait", execution.RetrySafeSameOperation, "", "", aggregates, map[string]string{"control_store": "busy"}, map[string]string{"control_store": "writable"}, time.Now().UTC())
		diagnostic.RecoveryActions = []string{"retry the same operation ID"}
		diagnostic.Cause = err
		return diagnostic
	}
	return fmt.Errorf("control store: %w", err)
}
