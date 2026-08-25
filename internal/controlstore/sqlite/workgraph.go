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

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type FinalizeWorkRevisionRequest struct {
	SchemaVersion          uint16
	CorrelationID          string
	OperationID            ids.OperationID
	ActorID                ids.ActorID
	ProcessID              ids.ProcessID
	OccurredAt             time.Time
	Revision               workgraph.Revision
	ExpectedPreviousNumber int64
}

type FinalizeWorkRevisionResult struct {
	WorkRevisionID ids.WorkRevisionID
	ProjectID      ids.ProjectID
	MissionID      ids.MissionID
	RevisionNumber int64
	GraphDigest    string
	CriteriaDigest string
}

type ReconcileWorkRevisionRequest struct {
	SchemaVersion  uint16
	CorrelationID  string
	OperationID    ids.OperationID
	ActorID        ids.ActorID
	ProcessID      ids.ProcessID
	OccurredAt     time.Time
	Reconciliation workgraph.Reconciliation
}

func (s *Store) ensureWorkGraphSchema(ctx context.Context) error {
	var present int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='work_graph_revisions'`).Scan(&present); err != nil {
		return fmt.Errorf("verify work graph schema: %w", err)
	}
	if present != 1 {
		return fmt.Errorf("work graph schema migration is unavailable")
	}
	return nil
}

func (s *Store) FinalizeWorkRevision(ctx context.Context, request FinalizeWorkRevisionRequest) (FinalizeWorkRevisionResult, error) {
	revision := request.Revision
	if err := validateFinalizeWorkRevisionRequest(request); err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	encoded, err := revision.Encode()
	if err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	digest, err := finalizeWorkRevisionDigest(request, encoded)
	if err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "begin idempotent WorkRevision finalization", revision, request.ExpectedPreviousNumber, 0, err)
	}
	defer func() { _ = tx.Rollback() }()
	result, found, err := loadFinalizeWorkRevisionOperation(ctx, tx, request, digest)
	if err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	if found {
		return result, nil
	}
	when := request.OccurredAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, request.ActorID, when); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist finalization actor", revision, request.ExpectedPreviousNumber, 0, err)
	}
	processResult, err := tx.ExecContext(ctx, `INSERT INTO processes(process_id,actor_id,observed_at) VALUES(?,?,?) ON CONFLICT(process_id) DO UPDATE SET observed_at=excluded.observed_at WHERE processes.actor_id=excluded.actor_id`, request.ProcessID, request.ActorID, when)
	if err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist finalization process", revision, request.ExpectedPreviousNumber, 0, err)
	}
	if changed, rowsErr := processResult.RowsAffected(); rowsErr != nil || changed != 1 {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "ProcessID is already bound to a different ActorID", revision, request.ExpectedPreviousNumber, 0, rowsErr)
	}
	result, err = saveWorkRevisionTx(ctx, tx, revision, request.ExpectedPreviousNumber)
	if err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_operations(
operation_id,operation_kind,request_digest,schema_version,correlation_id,actor_id,process_id,occurred_at,result_work_revision_id
) VALUES(?,'finalize_work_revision',?,?,?,?,?,?,?)`, request.OperationID, digest, request.SchemaVersion, request.CorrelationID, request.ActorID, request.ProcessID, when, revision.ID()); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist finalization operation result", revision, request.ExpectedPreviousNumber, 0, err)
	}
	if err := tx.Commit(); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "commit idempotent WorkRevision finalization", revision, request.ExpectedPreviousNumber, 0, err)
	}
	return result, nil
}

func saveWorkRevisionTx(ctx context.Context, tx *sql.Tx, revision workgraph.Revision, expectedPreviousNumber int64) (FinalizeWorkRevisionResult, error) {
	encoded, err := revision.Encode()
	if err != nil {
		return FinalizeWorkRevisionResult{}, err
	}
	result := FinalizeWorkRevisionResult{
		WorkRevisionID: revision.ID(), ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), RevisionNumber: revision.Number(),
		GraphDigest: revision.GraphDigest(), CriteriaDigest: revision.CriteriaDigest(),
	}

	var observed int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision_number),0) FROM work_revisions WHERE project_id=? AND mission_id=?`, revision.ProjectID(), revision.MissionID()).Scan(&observed); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "read current WorkRevision number", revision, expectedPreviousNumber, 0, err)
	}
	if observed != expectedPreviousNumber {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "another WorkRevision generation won optimistic creation", revision, expectedPreviousNumber, observed, nil)
	}
	now := revision.FinalizedAt().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO projects(project_id, created_at) VALUES(?,?)`, revision.ProjectID(), now); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist WorkRevision project", revision, expectedPreviousNumber, observed, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id, created_at) VALUES(?,?)`, revision.ActorID(), now); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist WorkRevision author", revision, expectedPreviousNumber, observed, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_revisions(work_revision_id,project_id,mission_id,revision_number,finalized_at) VALUES(?,?,?,?,?)`, revision.ID(), revision.ProjectID(), revision.MissionID(), revision.Number(), now); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist numbered WorkRevision", revision, expectedPreviousNumber, observed, err)
	}
	for _, item := range revision.WorkItems() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_items(work_revision_id,work_id) VALUES(?,?)`, revision.ID(), item.ID); err != nil {
			return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist canonical WorkRevision membership", revision, expectedPreviousNumber, observed, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_revisions(work_revision_id,graph_digest,criteria_digest,author_actor_id,encoded_revision) VALUES(?,?,?,?,?)`, revision.ID(), revision.GraphDigest(), revision.CriteriaDigest(), revision.ActorID(), encoded); err != nil {
		return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist immutable WorkRevision graph", revision, expectedPreviousNumber, observed, err)
	}
	for _, item := range revision.WorkItems() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_items(work_id,work_revision_id,state) VALUES(?,?,'ready')`, item.ID, revision.ID()); err != nil {
			return FinalizeWorkRevisionResult{}, workGraphError(workgraph.KindRevisionConflict, "persist WorkRevision item", revision, expectedPreviousNumber, observed, err)
		}
	}
	return result, nil
}

func validateFinalizeWorkRevisionRequest(request FinalizeWorkRevisionRequest) error {
	revision := request.Revision
	if request.SchemaVersion != execution.ResultSchemaVersion {
		return workGraphError(workgraph.KindInvalidRevision, "finalization schema version is unsupported", revision, request.ExpectedPreviousNumber, 0, nil)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil || request.CorrelationID == "" {
		return workGraphError(workgraph.KindInvalidRevision, "finalization operation identity is invalid", revision, request.ExpectedPreviousNumber, 0, err)
	}
	if _, err := ids.ParseActorID(request.ActorID.String()); err != nil {
		return workGraphError(workgraph.KindInvalidRevision, "finalization ActorID is invalid", revision, request.ExpectedPreviousNumber, 0, err)
	}
	if _, err := ids.ParseProcessID(request.ProcessID.String()); err != nil {
		return workGraphError(workgraph.KindInvalidRevision, "finalization ProcessID is invalid", revision, request.ExpectedPreviousNumber, 0, err)
	}
	if request.OccurredAt.IsZero() || revision.ActorID() != request.ActorID || !revision.FinalizedAt().Equal(request.OccurredAt) {
		return workGraphError(workgraph.KindInvalidRevision, "finalization ActorID and timestamp must match the immutable revision", revision, request.ExpectedPreviousNumber, 0, nil)
	}
	if revision.ID() == "" || revision.Number() <= 0 || request.ExpectedPreviousNumber < 0 || revision.Number() != request.ExpectedPreviousNumber+1 {
		return workGraphError(workgraph.KindRevisionConflict, "revision number does not match the optimistic expectation", revision, request.ExpectedPreviousNumber, 0, nil)
	}
	return nil
}

func finalizeWorkRevisionDigest(request FinalizeWorkRevisionRequest, encoded []byte) (string, error) {
	payload := struct {
		SchemaVersion          uint16          `json:"schema_version"`
		CorrelationID          string          `json:"correlation_id"`
		ActorID                ids.ActorID     `json:"actor_id"`
		ProcessID              ids.ProcessID   `json:"process_id"`
		OccurredAt             time.Time       `json:"occurred_at"`
		ExpectedPreviousNumber int64           `json:"expected_previous_number"`
		EncodedRevision        json.RawMessage `json:"encoded_revision"`
	}{request.SchemaVersion, request.CorrelationID, request.ActorID, request.ProcessID, request.OccurredAt.UTC(), request.ExpectedPreviousNumber, json.RawMessage(encoded)}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func loadFinalizeWorkRevisionOperation(ctx context.Context, tx *sql.Tx, request FinalizeWorkRevisionRequest, digest string) (FinalizeWorkRevisionResult, bool, error) {
	var storedDigest string
	var result FinalizeWorkRevisionResult
	err := tx.QueryRowContext(ctx, `SELECT o.request_digest,o.result_work_revision_id,wr.project_id,wr.mission_id,wr.revision_number,g.graph_digest,g.criteria_digest
FROM work_graph_operations o
JOIN work_revisions wr ON wr.work_revision_id=o.result_work_revision_id
JOIN work_graph_revisions g ON g.work_revision_id=o.result_work_revision_id
WHERE o.operation_id=? AND o.operation_kind='finalize_work_revision'`, request.OperationID).Scan(
		&storedDigest, &result.WorkRevisionID, &result.ProjectID, &result.MissionID, &result.RevisionNumber, &result.GraphDigest, &result.CriteriaDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return FinalizeWorkRevisionResult{}, false, nil
	}
	if err != nil {
		return FinalizeWorkRevisionResult{}, false, workGraphError(workgraph.KindRevisionConflict, "inspect finalization operation replay", request.Revision, request.ExpectedPreviousNumber, 0, err)
	}
	if storedDigest != digest {
		return FinalizeWorkRevisionResult{}, false, workGraphError(workgraph.KindRevisionConflict, "finalization OperationID was reused for a different request", request.Revision, request.ExpectedPreviousNumber, result.RevisionNumber-1, nil)
	}
	return result, true, nil
}

func (s *Store) WorkRevision(ctx context.Context, revisionID ids.WorkRevisionID) (workgraph.Revision, error) {
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return workgraph.Revision{}, err
	}
	var encoded []byte
	err := s.db.QueryRowContext(ctx, `SELECT encoded_revision FROM work_graph_revisions WHERE work_revision_id=?`, revisionID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return workgraph.Revision{}, &workgraph.Error{Kind: workgraph.KindNotFound, Message: "WorkRevision was not found", Fields: map[string]string{"work_revision_id": revisionID.String()}}
	}
	if err != nil {
		return workgraph.Revision{}, fmt.Errorf("read WorkRevision %s: %w", revisionID, err)
	}
	return workgraph.DecodeRevision(encoded)
}

type ReconciliationEvidence struct {
	ID                string                `json:"reconciliation_id"`
	RequestDigest     string                `json:"request_digest"`
	OldRevisionID     ids.WorkRevisionID    `json:"old_work_revision_id"`
	NewRevisionID     ids.WorkRevisionID    `json:"new_work_revision_id"`
	Disposition       workgraph.Disposition `json:"disposition"`
	OldGraphDigest    string                `json:"old_graph_digest"`
	NewGraphDigest    string                `json:"new_graph_digest"`
	OldCriteriaDigest string                `json:"old_criteria_digest"`
	NewCriteriaDigest string                `json:"new_criteria_digest"`
	ActorID           ids.ActorID           `json:"actor_id"`
	Rationale         string                `json:"rationale"`
	OldWorkIDs        []ids.WorkID          `json:"old_work_ids"`
	NewWorkIDs        []ids.WorkID          `json:"new_work_ids"`
	ForkEdges         []workgraph.ForkEdge  `json:"fork_edges"`
	OccurredAt        time.Time             `json:"occurred_at"`
}

func saveReconciliationTx(ctx context.Context, tx *sql.Tx, reconciliation workgraph.Reconciliation) (bool, error) {
	var recordedDigest string
	err := tx.QueryRowContext(ctx, `SELECT request_digest FROM reconciliations WHERE reconciliation_id=?`, reconciliation.ID()).Scan(&recordedDigest)
	if err == nil {
		if recordedDigest == reconciliation.RequestDigest() {
			return false, nil
		}
		return false, &workgraph.Error{Kind: workgraph.KindReconciliationConflict, Message: "reconciliation operation ID was reused for a different request", Fields: map[string]string{"reconciliation_id": reconciliation.ID(), "expected_request_digest": recordedDigest, "observed_request_digest": reconciliation.RequestDigest()}}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, reconciliationConflict(reconciliation, "inspect reconciliation replay", err)
	}
	oldWorkIDs, err := json.Marshal(reconciliation.OldWorkIDs())
	if err != nil {
		return false, reconciliationConflict(reconciliation, "encode old WorkIDs", err)
	}
	newWorkIDs, err := json.Marshal(reconciliation.NewWorkIDs())
	if err != nil {
		return false, reconciliationConflict(reconciliation, "encode new WorkIDs", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, reconciliation.ActorID(), reconciliation.OccurredAt().Format(time.RFC3339Nano)); err != nil {
		return false, reconciliationConflict(reconciliation, "persist reconciliation actor", err)
	}
	for _, edge := range reconciliation.ForkEdges() {
		if _, err := tx.ExecContext(ctx, `INSERT INTO reconciliation_fork_edges(reconciliation_id,from_old_work_id,to_new_work_id) VALUES(?,?,?)`, reconciliation.ID(), edge.FromOldWorkID, edge.ToNewWorkID); err != nil {
			return false, reconciliationConflict(reconciliation, "persist fork edge", err)
		}
	}
	statuses := []workgraph.WorkStatus{workgraph.StatusActive, workgraph.StatusHandedOff, workgraph.StatusApproved, workgraph.StatusIntegrated}
	for _, workID := range reconciliation.OldWorkIDs() {
		for _, status := range statuses {
			consequence, ok := reconciliation.Consequence(workID, status)
			if !ok {
				return false, reconciliationConflict(reconciliation, "reconciliation omitted a status consequence", nil)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO reconciliation_consequences(
reconciliation_id,work_id,work_status,run_may_continue,handoff_eligible,integration_eligible,expire_authority,quarantine_workspace,preserves_integrated_history,reason
) VALUES(?,?,?,?,?,?,?,?,?,?)`, reconciliation.ID(), workID, status, consequence.RunMayContinue, consequence.HandoffEligible,
				consequence.IntegrationEligible, consequence.ExpireAuthority, consequence.QuarantineWorkspace, consequence.PreservesIntegratedHistory, consequence.Reason)
			if err != nil {
				return false, reconciliationConflict(reconciliation, "persist reconciliation consequence", err)
			}
		}
	}
	if reconciliation.Disposition() == workgraph.Invalidate {
		if err := expireInvalidatedAuthority(ctx, tx, reconciliation); err != nil {
			return false, reconciliationConflict(reconciliation, "expire invalidated authority", err)
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO reconciliations(
reconciliation_id,request_digest,old_work_revision_id,new_work_revision_id,disposition,old_graph_digest,new_graph_digest,old_criteria_digest,new_criteria_digest,actor_id,rationale,old_work_ids,new_work_ids,occurred_at
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		reconciliation.ID(), reconciliation.RequestDigest(), reconciliation.OldRevisionID(), reconciliation.NewRevisionID(), reconciliation.Disposition(),
		reconciliation.OldGraphDigest(), reconciliation.NewGraphDigest(), reconciliation.OldCriteriaDigest(), reconciliation.NewCriteriaDigest(),
		reconciliation.ActorID(), reconciliation.Rationale(), oldWorkIDs, newWorkIDs, reconciliation.OccurredAt().Format(time.RFC3339Nano))
	if err != nil {
		return false, reconciliationConflict(reconciliation, "persist reconciliation evidence", err)
	}
	return true, nil
}

func (s *Store) ReconcileWorkRevision(ctx context.Context, request ReconcileWorkRevisionRequest) error {
	reconciliation := request.Reconciliation
	if request.SchemaVersion != execution.ResultSchemaVersion || request.CorrelationID == "" || request.OperationID.String() != reconciliation.ID() || request.ActorID != reconciliation.ActorID() || !request.OccurredAt.Equal(reconciliation.OccurredAt()) {
		return reconciliationConflict(reconciliation, "reconciliation command metadata does not match the immutable request", nil)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil {
		return reconciliationConflict(reconciliation, "reconciliation OperationID is invalid", err)
	}
	if _, err := ids.ParseActorID(request.ActorID.String()); err != nil {
		return reconciliationConflict(reconciliation, "reconciliation ActorID is invalid", err)
	}
	if _, err := ids.ParseProcessID(request.ProcessID.String()); err != nil {
		return reconciliationConflict(reconciliation, "reconciliation ProcessID is invalid", err)
	}
	digest, err := reconcileWorkRevisionDigest(request)
	if err != nil {
		return reconciliationConflict(reconciliation, "encode reconciliation command", err)
	}
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return reconciliationConflict(reconciliation, "begin reconciliation operation persistence", err)
	}
	defer func() { _ = tx.Rollback() }()
	var recordedKind, recordedDigest string
	err = tx.QueryRowContext(ctx, `SELECT operation_kind,request_digest FROM work_graph_operations WHERE operation_id=?`, request.OperationID).Scan(&recordedKind, &recordedDigest)
	if err == nil {
		if recordedKind == "reconcile_work_revision" && recordedDigest == digest {
			return nil
		}
		return reconciliationConflict(reconciliation, "reconciliation OperationID was reused for a different command", nil)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return reconciliationConflict(reconciliation, "inspect reconciliation operation replay", err)
	}
	if _, err := saveReconciliationTx(ctx, tx, reconciliation); err != nil {
		return err
	}
	when := request.OccurredAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, request.ActorID, when); err != nil {
		return reconciliationConflict(reconciliation, "persist reconciliation command actor", err)
	}
	processResult, err := tx.ExecContext(ctx, `INSERT INTO processes(process_id,actor_id,observed_at) VALUES(?,?,?) ON CONFLICT(process_id) DO UPDATE SET observed_at=excluded.observed_at WHERE processes.actor_id=excluded.actor_id`, request.ProcessID, request.ActorID, when)
	if err != nil {
		return reconciliationConflict(reconciliation, "persist reconciliation command process", err)
	}
	if changed, rowsErr := processResult.RowsAffected(); rowsErr != nil || changed != 1 {
		return reconciliationConflict(reconciliation, "ProcessID is already bound to a different ActorID", rowsErr)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_operations(
operation_id,operation_kind,request_digest,schema_version,correlation_id,actor_id,process_id,occurred_at,result_reconciliation_id
) VALUES(?,'reconcile_work_revision',?,?,?,?,?,?,?)`, request.OperationID, digest, request.SchemaVersion, request.CorrelationID, request.ActorID, request.ProcessID, when, reconciliation.ID()); err != nil {
		return reconciliationConflict(reconciliation, "persist reconciliation operation result", err)
	}
	if err := tx.Commit(); err != nil {
		return reconciliationConflict(reconciliation, "commit reconciliation operation result", err)
	}
	return nil
}

func reconcileWorkRevisionDigest(request ReconcileWorkRevisionRequest) (string, error) {
	payload := struct {
		SchemaVersion uint16        `json:"schema_version"`
		CorrelationID string        `json:"correlation_id"`
		ActorID       ids.ActorID   `json:"actor_id"`
		ProcessID     ids.ProcessID `json:"process_id"`
		OccurredAt    time.Time     `json:"occurred_at"`
		RequestDigest string        `json:"reconciliation_request_digest"`
	}{request.SchemaVersion, request.CorrelationID, request.ActorID, request.ProcessID, request.OccurredAt.UTC(), request.Reconciliation.RequestDigest()}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

type invalidatedLease struct {
	leaseID    ids.LeaseID
	runID      ids.RunID
	workID     ids.WorkID
	generation int64
}

func expireInvalidatedAuthority(ctx context.Context, tx *sql.Tx, reconciliation workgraph.Reconciliation) error {
	for _, workID := range reconciliation.OldWorkIDs() {
		rows, err := tx.QueryContext(ctx, `SELECT l.lease_id,l.run_id,l.work_id,l.generation FROM leases l WHERE l.work_revision_id=? AND l.work_id=? AND l.status='active'`, reconciliation.OldRevisionID(), workID)
		if err != nil {
			return err
		}
		var leases []invalidatedLease
		for rows.Next() {
			var lease invalidatedLease
			if err := rows.Scan(&lease.leaseID, &lease.runID, &lease.workID, &lease.generation); err != nil {
				_ = rows.Close()
				return err
			}
			leases = append(leases, lease)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, lease := range leases {
			if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_quarantine_intents(reconciliation_id,lease_id,work_id,work_revision_id,reason,created_at) VALUES(?,?,?,?,?,?)`,
				reconciliation.ID(), lease.leaseID, lease.workID, reconciliation.OldRevisionID(), "WorkRevision invalidated; quarantine any workspace bound to this lease", reconciliation.OccurredAt().Format(time.RFC3339Nano)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE leases SET status='expired' WHERE lease_id=? AND status='active'`, lease.leaseID); err != nil {
				return err
			}
			if err := finalizeAuthorityExpiryTx(ctx, tx, lease.leaseID, lease.runID, lease.generation, "WorkRevision invalidated; workspace permanently quarantined", reconciliation.OccurredAt()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Reconciliation(ctx context.Context, reconciliationID string) (ReconciliationEvidence, error) {
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return ReconciliationEvidence{}, err
	}
	var result ReconciliationEvidence
	var oldWorkIDs, newWorkIDs []byte
	var occurredAt string
	err := s.db.QueryRowContext(ctx, `SELECT reconciliation_id,request_digest,old_work_revision_id,new_work_revision_id,disposition,old_graph_digest,new_graph_digest,old_criteria_digest,new_criteria_digest,actor_id,rationale,old_work_ids,new_work_ids,occurred_at FROM reconciliations WHERE reconciliation_id=?`, reconciliationID).Scan(
		&result.ID, &result.RequestDigest, &result.OldRevisionID, &result.NewRevisionID, &result.Disposition, &result.OldGraphDigest, &result.NewGraphDigest,
		&result.OldCriteriaDigest, &result.NewCriteriaDigest, &result.ActorID, &result.Rationale, &oldWorkIDs, &newWorkIDs, &occurredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ReconciliationEvidence{}, &workgraph.Error{Kind: workgraph.KindNotFound, Message: "reconciliation was not found", Fields: map[string]string{"reconciliation_id": reconciliationID}}
	}
	if err != nil {
		return ReconciliationEvidence{}, fmt.Errorf("read reconciliation: %w", err)
	}
	if err := json.Unmarshal(oldWorkIDs, &result.OldWorkIDs); err != nil {
		return ReconciliationEvidence{}, fmt.Errorf("decode old WorkIDs: %w", err)
	}
	if err := json.Unmarshal(newWorkIDs, &result.NewWorkIDs); err != nil {
		return ReconciliationEvidence{}, fmt.Errorf("decode new WorkIDs: %w", err)
	}
	result.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
	if err != nil {
		return ReconciliationEvidence{}, fmt.Errorf("decode reconciliation time: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT from_old_work_id,to_new_work_id FROM reconciliation_fork_edges WHERE reconciliation_id=? ORDER BY from_old_work_id,to_new_work_id`, reconciliationID)
	if err != nil {
		return ReconciliationEvidence{}, fmt.Errorf("read fork edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var edge workgraph.ForkEdge
		if err := rows.Scan(&edge.FromOldWorkID, &edge.ToNewWorkID); err != nil {
			return ReconciliationEvidence{}, err
		}
		result.ForkEdges = append(result.ForkEdges, edge)
	}
	return result, rows.Err()
}

func (s *Store) ReconciliationConsequence(ctx context.Context, reconciliationID string, workID ids.WorkID, status workgraph.WorkStatus) (workgraph.Consequence, error) {
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return workgraph.Consequence{}, err
	}
	var result workgraph.Consequence
	err := s.db.QueryRowContext(ctx, `SELECT run_may_continue,handoff_eligible,integration_eligible,expire_authority,quarantine_workspace,preserves_integrated_history,reason FROM reconciliation_consequences WHERE reconciliation_id=? AND work_id=? AND work_status=?`, reconciliationID, workID, status).Scan(
		&result.RunMayContinue, &result.HandoffEligible, &result.IntegrationEligible, &result.ExpireAuthority, &result.QuarantineWorkspace, &result.PreservesIntegratedHistory, &result.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return workgraph.Consequence{}, &workgraph.Error{Kind: workgraph.KindNotFound, Message: "reconciliation consequence was not found", Fields: map[string]string{"reconciliation_id": reconciliationID, "work_id": workID.String(), "work_status": string(status)}}
	}
	if err != nil {
		return workgraph.Consequence{}, fmt.Errorf("read reconciliation consequence: %w", err)
	}
	return result, nil
}

// WorkEligibility resolves the latest immutable reconciliation decision for a
// work generation and status. Handoff and integration packages consume this
// same query; absence of a reconciliation preserves the original contract.
func (s *Store) WorkEligibility(ctx context.Context, revisionID ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus) (workgraph.Consequence, string, error) {
	if err := s.ensureWorkGraphSchema(ctx); err != nil {
		return workgraph.Consequence{}, "", err
	}
	return loadWorkEligibility(ctx, s.db, revisionID, workID, status)
}

type workEligibilityQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadWorkEligibility(ctx context.Context, query workEligibilityQuery, revisionID ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus) (workgraph.Consequence, string, error) {
	result := workgraph.Consequence{RunMayContinue: true, HandoffEligible: true, IntegrationEligible: true, Reason: "no reconciliation restricts this WorkRevision generation"}
	var reconciliationID string
	err := query.QueryRowContext(ctx, `SELECT r.reconciliation_id,c.run_may_continue,c.handoff_eligible,c.integration_eligible,c.expire_authority,c.quarantine_workspace,c.preserves_integrated_history,c.reason
FROM reconciliations r JOIN reconciliation_consequences c ON c.reconciliation_id=r.reconciliation_id
WHERE r.old_work_revision_id=? AND c.work_id=? AND c.work_status=?
ORDER BY CASE r.disposition WHEN 'invalidate' THEN 0 WHEN 'supersede' THEN 1 ELSE 2 END,
         r.occurred_at DESC,r.reconciliation_id DESC LIMIT 1`, revisionID, workID, status).Scan(
		&reconciliationID, &result.RunMayContinue, &result.HandoffEligible, &result.IntegrationEligible, &result.ExpireAuthority, &result.QuarantineWorkspace, &result.PreservesIntegratedHistory, &result.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return result, "", nil
	}
	if err != nil {
		return workgraph.Consequence{}, "", fmt.Errorf("read work eligibility: %w", err)
	}
	return result, reconciliationID, nil
}

func workGraphError(kind workgraph.ErrorKind, message string, revision workgraph.Revision, expected, observed int64, cause error) error {
	return &workgraph.Error{Kind: kind, Message: message, Fields: map[string]string{
		"work_revision_id": revision.ID().String(), "mission_id": revision.MissionID().String(),
		"expected_previous_number": fmt.Sprint(expected), "observed_previous_number": fmt.Sprint(observed),
	}, Cause: cause}
}

func reconciliationConflict(reconciliation workgraph.Reconciliation, message string, cause error) error {
	return &workgraph.Error{Kind: workgraph.KindReconciliationConflict, Message: message, Fields: map[string]string{
		"reconciliation_id": reconciliation.ID(), "old_work_revision_id": reconciliation.OldRevisionID().String(), "new_work_revision_id": reconciliation.NewRevisionID().String(),
	}, Cause: cause}
}
