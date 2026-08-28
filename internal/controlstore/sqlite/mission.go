package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/controlstore"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

var _ mission.Store = (*Store)(nil)
var _ controlstore.OutboxStore = (*Store)(nil)

type transitionResultWire struct {
	Instance  mission.MissionSnapshot  `json:"instance"`
	Audit     mission.TransitionAudit  `json:"audit"`
	Authority *transitionAuthorityWire `json:"authority,omitempty"`
}
type transitionAuthorityWire struct {
	RequestCommitment mission.RequestCommitment `json:"request_commitment"`
	MissionID         string                    `json:"mission_id"`
	Kind              artifact.Kind             `json:"kind"`
	RevisionID        string                    `json:"artifact_revision_id"`
	ApprovalID        string                    `json:"approval_id"`
}
type createResultWire struct {
	Instance          mission.MissionSnapshot    `json:"instance"`
	ActorID           ids.ActorID                `json:"actor_id,omitempty"`
	ProcessID         ids.ProcessID              `json:"process_id,omitempty"`
	Governance        *governancecontext.Context `json:"governance,omitempty"`
	AuthorityRecorded bool                       `json:"authority_recorded"`
	// LegacyReplayed accepts the v13 CreateResult projection. Replay is derived
	// from the operation lookup and is never trusted as stored authority.
	LegacyReplayed bool `json:"Replayed,omitempty"`
}

func missionError(kind mission.ErrorKind, field, message string, cause error) error {
	recovery := mission.RecoveryRetryPersistence
	switch kind {
	case mission.KindNotFound, mission.KindAlreadyExists, mission.KindIdempotencyConflict:
		recovery = mission.RecoveryCorrectInput
	case mission.KindStateVersionConflict:
		recovery = mission.RecoveryRefreshState
	case mission.KindApprovalStale:
		recovery = mission.RecoveryRefreshApproval
	case mission.KindInvalidStoredState, mission.KindPersistenceContract:
		recovery = mission.RecoveryRepairStoredState
	}
	return &mission.Error{Kind: kind, Recovery: recovery, Field: field, Message: message, Cause: cause}
}

func encodeSnapshot(snapshot mission.MissionSnapshot) ([]byte, error) {
	if err := snapshot.Validate(); err != nil {
		return nil, missionError(mission.KindInvalidRequest, "mission", "mission snapshot is invalid", err)
	}
	return json.Marshal(snapshot)
}
func decodeSnapshot(encoded []byte) (mission.MissionSnapshot, error) {
	var snapshot mission.MissionSnapshot
	if err := strictJSON(encoded, &snapshot); err != nil || snapshot.Validate() != nil {
		return mission.MissionSnapshot{}, missionError(mission.KindInvalidStoredState, "mission", "stored mission snapshot is invalid", err)
	}
	return snapshot, nil
}
func strictJSON(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func (s *Store) CreateMission(ctx context.Context, commit mission.CreateCommit) (mission.CreateResult, error) {
	encoded, err := encodeSnapshot(commit.Instance)
	if err != nil {
		return mission.CreateResult{}, err
	}
	if _, err := ids.ParseOperationID(commit.OperationID.String()); err != nil || commit.RequestCommitment.Validate() != nil || commit.OccurredAt.IsZero() || commit.OccurredAt.Location() != time.UTC {
		return mission.CreateResult{}, missionError(mission.KindInvalidRequest, "create", "mission creation commit is invalid", err)
	}
	authorityRequired := commit.ProcessID != "" || commit.Governance.ID() != ""
	if authorityRequired {
		if _, err := ids.ParseActorID(commit.ActorID.String()); err != nil {
			return mission.CreateResult{}, missionError(mission.KindInvalidRequest, "actor_id", "mission creation actor is invalid", err)
		}
		if _, err := ids.ParseProcessID(commit.ProcessID.String()); err != nil {
			return mission.CreateResult{}, missionError(mission.KindInvalidRequest, "process_id", "mission creation process is invalid", err)
		}
		if err := commit.Governance.Validate(); err != nil {
			return mission.CreateResult{}, missionError(mission.KindInvalidRequest, "governance_context", "mission creation governance is invalid", err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	defer tx.Rollback()
	if prior, found, e := loadCreateOperation(ctx, tx, commit.OperationID, commit.RequestCommitment); e != nil {
		return mission.CreateResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	if authorityRequired {
		when := formatTime(commit.OccurredAt)
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO actors(actor_id,created_at) VALUES(?,?)`, commit.ActorID, when); err != nil {
			return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "actor_id", "mission creation authority could not be persisted", err)
		}
		processResult, processErr := tx.ExecContext(ctx, `INSERT INTO processes(process_id,actor_id,observed_at) VALUES(?,?,?) ON CONFLICT(process_id) DO UPDATE SET observed_at=excluded.observed_at WHERE processes.actor_id=excluded.actor_id`, commit.ProcessID, commit.ActorID, when)
		if processErr != nil {
			return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "process_id", "mission creation authority could not be persisted", processErr)
		}
		if affected, _ := processResult.RowsAffected(); affected != 1 {
			return mission.CreateResult{}, missionError(mission.KindIdempotencyConflict, "process_id", "process identity is bound to another actor", nil)
		}
		if err = saveGovernanceContextTx(ctx, tx, commit.Governance, commit.OperationID, commit.OccurredAt); err != nil {
			return mission.CreateResult{}, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO phase3_missions(mission_id,project_id,definition_id,definition_version,definition_commitment,lifecycle,current_step,state_version,created_at,updated_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, commit.Instance.MissionID, commit.Instance.ProjectID, commit.Instance.DefinitionID, commit.Instance.DefinitionVersion, commit.Instance.DefinitionCommitment, commit.Instance.Lifecycle, commit.Instance.CurrentStep, commit.Instance.StateVersion, formatTime(commit.Instance.CreatedAt), formatTime(commit.Instance.UpdatedAt), encoded)
	if err != nil {
		if isUnique(err) {
			return mission.CreateResult{}, missionError(mission.KindAlreadyExists, "mission", "mission already exists", nil)
		}
		return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	if authorityRequired {
		if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_mission_creation_authority(mission_id,actor_id,process_id,governance_context_id,governance_aggregate) VALUES(?,?,?,?,?)`, commit.Instance.MissionID, commit.ActorID, commit.ProcessID, commit.Governance.ID(), commit.Governance.Aggregate().Algorithm()+":"+commit.Governance.Aggregate().Digest()); err != nil {
			return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "creation_authority", "mission creation authority could not be persisted", err)
		}
	}
	var governance *governancecontext.Context
	if authorityRequired {
		value := commit.Governance
		governance = &value
	}
	resultJSON, _ := json.Marshal(createResultWire{Instance: commit.Instance, ActorID: commit.ActorID, ProcessID: commit.ProcessID, Governance: governance, AuthorityRecorded: authorityRequired})
	if err = insertPhase3Operation(ctx, tx, commit.OperationID.String(), "create_mission", commit.Instance.MissionID.String(), commit.RequestCommitment.String(), resultJSON, commit.OccurredAt); err != nil {
		return mission.CreateResult{}, err
	}
	if err = insertPhase3Event(ctx, tx, "mission-created:"+commit.OperationID.String(), commit.OperationID.String(), commit.Instance.MissionID.String(), "mission_created", commit.RequestCommitment.String(), commit.OccurredAt); err != nil {
		return mission.CreateResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	return mission.CreateResult{Instance: commit.Instance, ActorID: commit.ActorID, ProcessID: commit.ProcessID, Governance: commit.Governance, AuthorityRecorded: authorityRequired}, nil
}

func loadCreateOperation(ctx context.Context, q rowQuery, op ids.OperationID, digest mission.RequestCommitment) (mission.CreateResult, bool, error) {
	var kind, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.CreateResult{}, false, nil
	}
	if err != nil {
		return mission.CreateResult{}, false, missionError(mission.KindPersistenceUnavailable, "store", "mission replay is unavailable", err)
	}
	if kind != "create_mission" || stored != digest.String() {
		return mission.CreateResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	var wire createResultWire
	if err := strictJSON(raw, &wire); err != nil || wire.Instance.Validate() != nil {
		return mission.CreateResult{}, false, missionError(mission.KindInvalidStoredState, "operation", "stored mission result is invalid", err)
	}
	result := mission.CreateResult{Instance: wire.Instance, ActorID: wire.ActorID, ProcessID: wire.ProcessID, AuthorityRecorded: wire.AuthorityRecorded}
	if wire.Governance != nil {
		result.Governance = *wire.Governance
	}
	if result.AuthorityRecorded && (result.Governance.Validate() != nil || result.ActorID == "" || result.ProcessID == "") {
		return mission.CreateResult{}, false, missionError(mission.KindInvalidStoredState, "operation", "stored mission creation authority is invalid", nil)
	}
	return result, true, nil
}

func (s *Store) LoadMission(ctx context.Context, id ids.MissionID) (mission.MissionSnapshot, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT snapshot_json FROM phase3_missions WHERE mission_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.MissionSnapshot{}, missionError(mission.KindNotFound, "mission", "mission was not found", nil)
	}
	if err != nil {
		return mission.MissionSnapshot{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	return decodeSnapshot(raw)
}

func (s *Store) ReplayTransition(ctx context.Context, id ids.MissionID, op ids.OperationID, digest mission.RequestCommitment) (mission.TransitionResult, bool, error) {
	return loadTransitionOperation(ctx, s.db, id, op, digest)
}

func loadTransitionOperation(ctx context.Context, q rowQuery, id ids.MissionID, op ids.OperationID, digest mission.RequestCommitment) (mission.TransitionResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.TransitionResult{}, false, nil
	}
	if err != nil {
		return mission.TransitionResult{}, false, missionError(mission.KindPersistenceUnavailable, "store", "mission replay is unavailable", err)
	}
	if kind != "transition_mission" || scope != id.String() || stored != digest.String() {
		return mission.TransitionResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	result, err := decodeTransitionResult(raw)
	if err != nil {
		return mission.TransitionResult{}, false, err
	}
	return result, true, nil
}

func (s *Store) CommitTransition(ctx context.Context, commit mission.TransitionCommit) (mission.TransitionResult, error) {
	if commit.Candidate.Previous.Validate() != nil || commit.Candidate.Successor.Validate() != nil || commit.RequestCommitment.Validate() != nil {
		return mission.TransitionResult{}, missionError(mission.KindInvalidRequest, "transition", "mission transition commit is invalid", nil)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.TransitionResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	defer tx.Rollback()
	if prior, found, e := loadTransitionOperation(ctx, tx, commit.Candidate.Previous.MissionID, commit.OperationID, commit.RequestCommitment); e != nil {
		return mission.TransitionResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	var raw []byte
	var version uint64
	if err = tx.QueryRowContext(ctx, `SELECT state_version,snapshot_json FROM phase3_missions WHERE mission_id=?`, commit.Candidate.Previous.MissionID).Scan(&version, &raw); errors.Is(err, sql.ErrNoRows) {
		return mission.TransitionResult{}, missionError(mission.KindNotFound, "mission", "mission was not found", nil)
	} else if err != nil {
		return mission.TransitionResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	current, e := decodeSnapshot(raw)
	if e != nil {
		return mission.TransitionResult{}, e
	}
	if version != commit.Candidate.Previous.StateVersion || !sameSnapshot(current, commit.Candidate.Previous) {
		return mission.TransitionResult{}, missionError(mission.KindStateVersionConflict, "mission", "mission state version changed", nil)
	}
	if commit.Authority.Required() {
		evidence := commit.Authority.ArtifactApproval()
		var rev, approval string
		err = tx.QueryRowContext(ctx, `SELECT r.artifact_revision_id,a.approval_id FROM phase3_artifact_revisions r JOIN phase3_artifact_approvals a ON a.artifact_revision_id=r.artifact_revision_id WHERE r.mission_id=? AND r.kind=? ORDER BY r.revision_number DESC,a.submitted_at DESC LIMIT 1`, evidence.MissionID(), evidence.Kind()).Scan(&rev, &approval)
		if err != nil || rev != evidence.ArtifactRevisionID() || approval != evidence.ApprovalID() {
			return mission.TransitionResult{}, missionError(mission.KindApprovalStale, "artifact_approval", "current artifact approval changed", nil)
		}
	}
	successorJSON, _ := encodeSnapshot(commit.Candidate.Successor)
	res, err := tx.ExecContext(ctx, `UPDATE phase3_missions SET lifecycle=?,current_step=?,state_version=?,updated_at=?,snapshot_json=? WHERE mission_id=? AND state_version=?`, commit.Candidate.Successor.Lifecycle, commit.Candidate.Successor.CurrentStep, commit.Candidate.Successor.StateVersion, formatTime(commit.Candidate.Successor.UpdatedAt), successorJSON, commit.Candidate.Successor.MissionID, commit.Candidate.Previous.StateVersion)
	if err != nil {
		return mission.TransitionResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return mission.TransitionResult{}, missionError(mission.KindStateVersionConflict, "mission", "mission state version changed", nil)
	}
	result := mission.TransitionResult{Instance: commit.Candidate.Successor, Audit: commit.Candidate.Audit, Authority: commit.Authority}
	resultJSON, err := encodeTransitionResult(result)
	if err != nil {
		return mission.TransitionResult{}, err
	}
	if err = insertPhase3Operation(ctx, tx, commit.OperationID.String(), "transition_mission", commit.Candidate.Successor.MissionID.String(), commit.RequestCommitment.String(), resultJSON, commit.Candidate.Audit.OccurredAt); err != nil {
		return mission.TransitionResult{}, err
	}
	auditJSON, _ := json.Marshal(commit.Candidate.Audit)
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_mission_transitions(event_id,mission_id,operation_id,old_state_version,new_state_version,old_step,new_step,actor_id,governance_context_id,audit_json) VALUES(?,?,?,?,?,?,?,?,?,?)`, commit.Candidate.Audit.EventID, commit.Candidate.Audit.MissionID, commit.OperationID, commit.Candidate.Audit.OldStateVersion, commit.Candidate.Audit.NewStateVersion, commit.Candidate.Audit.OldStep, commit.Candidate.Audit.NewStep, commit.Candidate.Audit.ActorID, nullString(commit.Candidate.Audit.GovernanceContextID.String()), auditJSON); err != nil {
		return mission.TransitionResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	if err = insertPhase3Event(ctx, tx, commit.Candidate.Audit.EventID.String(), commit.OperationID.String(), commit.Candidate.Audit.MissionID.String(), "mission_transition", commit.RequestCommitment.String(), commit.Candidate.Audit.OccurredAt); err != nil {
		return mission.TransitionResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return mission.TransitionResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	return result, nil
}

func encodeTransitionResult(r mission.TransitionResult) ([]byte, error) {
	var authority *transitionAuthorityWire
	if r.Authority.Required() {
		e := r.Authority.ArtifactApproval()
		authority = &transitionAuthorityWire{r.Authority.RequestCommitment(), e.MissionID(), e.Kind(), e.ArtifactRevisionID(), e.ApprovalID()}
	}
	return json.Marshal(transitionResultWire{r.Instance, r.Audit, authority})
}
func decodeTransitionResult(raw []byte) (mission.TransitionResult, error) {
	var wire transitionResultWire
	if err := strictJSON(raw, &wire); err != nil || wire.Instance.Validate() != nil {
		return mission.TransitionResult{}, missionError(mission.KindInvalidStoredState, "transition", "stored transition is invalid", err)
	}
	var authority mission.TransitionAuthority
	if wire.Authority != nil {
		e, err := artifact.NewCurrentApprovalEvidence(wire.Authority.MissionID, wire.Authority.Kind, wire.Authority.RevisionID, wire.Authority.ApprovalID)
		if err != nil {
			return mission.TransitionResult{}, missionError(mission.KindInvalidStoredState, "transition", "stored transition authority is invalid", err)
		}
		authority, err = mission.NewTransitionAuthority(wire.Authority.RequestCommitment, e)
		if err != nil {
			return mission.TransitionResult{}, missionError(mission.KindInvalidStoredState, "transition", "stored transition authority is invalid", err)
		}
	}
	return mission.TransitionResult{Instance: wire.Instance, Audit: wire.Audit, Authority: authority}, nil
}

type rowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertPhase3Operation(ctx context.Context, tx *sql.Tx, op, kind, missionID, digest string, result []byte, at time.Time) error {
	conflict, err := phase3OperationConflictsWithLegacy(ctx, tx, op)
	if err != nil {
		return missionError(mission.KindPersistenceUnavailable, "store", "operation identity cannot be inspected", err)
	}
	if conflict {
		return missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity is already bound", nil)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO phase3_operations(operation_id,operation_kind,mission_id,request_digest,result_json,occurred_at) VALUES(?,?,?,?,?,?)`, op, kind, missionID, digest, result, formatTime(at))
	if err != nil {
		if contains(err.Error(), "OperationID is already bound") {
			return missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity is already bound", nil)
		}
		return missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	return nil
}

func phase3OperationConflictsWithLegacy(ctx context.Context, q rowQuery, operationID string) (bool, error) {
	var conflict bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE operation_id=?) OR EXISTS(SELECT 1 FROM work_graph_operations WHERE operation_id=?)`, operationID, operationID).Scan(&conflict)
	return conflict, err
}
func insertPhase3Event(ctx context.Context, tx *sql.Tx, eventID, op, missionID, kind, digest string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO phase3_events(event_id,operation_id,mission_id,event_kind,payload_digest,occurred_at) VALUES(?,?,?,?,?,?)`, eventID, op, missionID, kind, digest, formatTime(at)); err != nil {
		return missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO phase3_outbox(event_id,event_kind,payload_digest,created_at,available_at) VALUES(?,?,?,?,?)`, eventID, kind, digest, formatTime(at), formatTime(at))
	return err
}

func scanOutbox(row *sql.Row) (controlstore.OutboxEnvelope, error) {
	var value controlstore.OutboxEnvelope
	var created, available string
	var claimed, expires, last, delivered sql.NullString
	err := row.Scan(&value.Sequence, &value.EventID, &value.EventKind, &value.PayloadDigest, &created, &value.DeliveryStatus, &value.AttemptCount, &available, &value.ClaimID, &claimed, &expires, &last, &delivered)
	if err != nil {
		return value, err
	}
	if value.CreatedAt, err = parseTime(created); err != nil {
		return value, fmt.Errorf("stored outbox creation time is invalid")
	}
	if value.AvailableAt, err = parseTime(available); err != nil {
		return value, fmt.Errorf("stored outbox availability is invalid")
	}
	if claimed.Valid {
		if value.ClaimedAt, err = parseTime(claimed.String); err != nil {
			return value, fmt.Errorf("stored outbox claim time is invalid")
		}
	}
	if expires.Valid {
		if value.ClaimExpiresAt, err = parseTime(expires.String); err != nil {
			return value, fmt.Errorf("stored outbox claim expiry is invalid")
		}
	}
	if last.Valid {
		if value.LastAttemptAt, err = parseTime(last.String); err != nil {
			return value, fmt.Errorf("stored outbox attempt time is invalid")
		}
	}
	if delivered.Valid {
		if value.DeliveredAt, err = parseTime(delivered.String); err != nil {
			return value, fmt.Errorf("stored outbox delivery time is invalid")
		}
	}
	if value.Sequence <= 0 || !boundedStoreText(value.EventID, 256) || !boundedStoreText(value.EventKind, 128) || !boundedStoreText(value.PayloadDigest, 128) {
		return value, fmt.Errorf("stored outbox identity is invalid")
	}
	switch value.DeliveryStatus {
	case controlstore.OutboxPending:
		if value.AttemptCount != 0 || value.ClaimID != "" || !value.ClaimedAt.IsZero() || !value.ClaimExpiresAt.IsZero() || !value.LastAttemptAt.IsZero() || !value.DeliveredAt.IsZero() || value.AvailableAt.Before(value.CreatedAt) {
			return value, fmt.Errorf("stored pending outbox evidence is invalid")
		}
	case controlstore.OutboxClaimed:
		if value.AttemptCount == 0 || !boundedStoreText(value.ClaimID, 128) || value.ClaimedAt.IsZero() || !value.ClaimExpiresAt.After(value.ClaimedAt) || value.ClaimedAt.Before(value.AvailableAt) || value.AvailableAt.Before(value.CreatedAt) || (!value.LastAttemptAt.IsZero() && value.ClaimedAt.Before(value.LastAttemptAt)) || !value.DeliveredAt.IsZero() {
			return value, fmt.Errorf("stored claimed outbox evidence is invalid")
		}
	case controlstore.OutboxRetryable:
		if value.AttemptCount == 0 || value.ClaimID != "" || value.ClaimedAt.IsZero() || !value.ClaimExpiresAt.After(value.ClaimedAt) || value.LastAttemptAt.Before(value.ClaimedAt) || value.LastAttemptAt.After(value.ClaimExpiresAt) || value.AvailableAt.Before(value.LastAttemptAt) || !value.DeliveredAt.IsZero() {
			return value, fmt.Errorf("stored retryable outbox evidence is invalid")
		}
	case controlstore.OutboxDelivered:
		if value.AttemptCount == 0 || value.ClaimID != "" || value.ClaimedAt.IsZero() || !value.ClaimExpiresAt.After(value.ClaimedAt) || value.LastAttemptAt.Before(value.ClaimedAt) || value.LastAttemptAt.After(value.ClaimExpiresAt) || value.DeliveredAt.Before(value.LastAttemptAt) || value.DeliveredAt.After(value.ClaimExpiresAt) {
			return value, fmt.Errorf("stored delivered outbox evidence is invalid")
		}
	default:
		return value, fmt.Errorf("stored outbox status is invalid")
	}
	return value, nil
}

const outboxColumns = `sequence,event_id,event_kind,payload_digest,created_at,delivery_status,attempt_count,available_at,COALESCE(claim_id,''),claimed_at,claim_expires_at,last_attempt_at,delivered_at`

const maxOutboxClaimLease = 5 * time.Minute

func (s *Store) ClaimOutbox(ctx context.Context, claimID string, now, expires time.Time) (controlstore.OutboxEnvelope, bool, error) {
	if !boundedStoreText(claimID, 128) || now.IsZero() || now.Location() != time.UTC || expires.Location() != time.UTC || !expires.After(now) || expires.Sub(now) > maxOutboxClaimLease {
		return controlstore.OutboxEnvelope{}, false, fmt.Errorf("outbox claim is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlstore.OutboxEnvelope{}, false, err
	}
	defer tx.Rollback()
	var sequence int64
	var status, priorClaimID string
	var attempts uint64
	var priorExpiry sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT sequence,delivery_status,attempt_count,COALESCE(claim_id,''),claim_expires_at FROM phase3_outbox
		WHERE (delivery_status IN ('pending','retryable') AND julianday(available_at)<=julianday(?))
		   OR (delivery_status='claimed' AND julianday(claim_expires_at)<=julianday(?))
		ORDER BY sequence LIMIT 1`, formatTime(now), formatTime(now)).Scan(&sequence, &status, &attempts, &priorClaimID, &priorExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return controlstore.OutboxEnvelope{}, false, nil
	}
	if err != nil {
		return controlstore.OutboxEnvelope{}, false, err
	}
	if status == string(controlstore.OutboxClaimed) && claimID == priorClaimID {
		return controlstore.OutboxEnvelope{}, false, fmt.Errorf("replacement outbox claim identity must differ")
	}
	res, err := tx.ExecContext(ctx, `UPDATE phase3_outbox SET delivery_status='claimed',attempt_count=?,claim_id=?,claimed_at=?,claim_expires_at=?
		WHERE sequence=? AND delivery_status=? AND attempt_count=? AND (
		  (delivery_status IN ('pending','retryable') AND claim_id IS NULL AND julianday(available_at)<=julianday(?))
		  OR (delivery_status='claimed' AND claim_id=? AND claim_expires_at=? AND julianday(claim_expires_at)<=julianday(?))
		)`, attempts+1, claimID, formatTime(now), formatTime(expires), sequence, status, attempts, formatTime(now), priorClaimID, priorExpiry.String, formatTime(now))
	if err != nil {
		return controlstore.OutboxEnvelope{}, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return controlstore.OutboxEnvelope{}, false, nil
	}
	value, err := scanOutbox(tx.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM phase3_outbox WHERE sequence=?`, sequence))
	if err != nil {
		return controlstore.OutboxEnvelope{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return controlstore.OutboxEnvelope{}, false, err
	}
	return value, true, nil
}
func (s *Store) RetryOutbox(ctx context.Context, sequence int64, claimID string, available, attempted time.Time) (controlstore.OutboxEnvelope, error) {
	if sequence <= 0 || !boundedStoreText(claimID, 128) || available.IsZero() || available.Location() != time.UTC || attempted.IsZero() || attempted.Location() != time.UTC || available.Before(attempted) {
		return controlstore.OutboxEnvelope{}, fmt.Errorf("outbox retry is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlstore.OutboxEnvelope{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE phase3_outbox SET delivery_status='retryable',claim_id=NULL,last_attempt_at=?,available_at=? WHERE sequence=? AND delivery_status='claimed' AND claim_id=? AND julianday(?)>=julianday(claimed_at) AND julianday(?)<=julianday(claim_expires_at)`, formatTime(attempted), formatTime(available), sequence, claimID, formatTime(attempted), formatTime(attempted))
	if err != nil {
		return controlstore.OutboxEnvelope{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return controlstore.OutboxEnvelope{}, fmt.Errorf("outbox claim changed")
	}
	value, err := scanOutbox(tx.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM phase3_outbox WHERE sequence=?`, sequence))
	if err != nil {
		return value, err
	}
	if err = tx.Commit(); err != nil {
		return value, err
	}
	return value, nil
}
func (s *Store) DeliverOutbox(ctx context.Context, sequence int64, claimID string, at time.Time) (controlstore.OutboxEnvelope, error) {
	if sequence <= 0 || !boundedStoreText(claimID, 128) || at.IsZero() || at.Location() != time.UTC {
		return controlstore.OutboxEnvelope{}, fmt.Errorf("outbox delivery is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return controlstore.OutboxEnvelope{}, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE phase3_outbox SET delivery_status='delivered',claim_id=NULL,last_attempt_at=?,delivered_at=? WHERE sequence=? AND delivery_status='claimed' AND claim_id=? AND julianday(?)>=julianday(claimed_at) AND julianday(?)<=julianday(claim_expires_at)`, formatTime(at), formatTime(at), sequence, claimID, formatTime(at), formatTime(at))
	if err != nil {
		return controlstore.OutboxEnvelope{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return controlstore.OutboxEnvelope{}, fmt.Errorf("outbox claim changed")
	}
	value, err := scanOutbox(tx.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM phase3_outbox WHERE sequence=?`, sequence))
	if err != nil {
		return value, err
	}
	if err = tx.Commit(); err != nil {
		return value, err
	}
	return value, nil
}
func boundedStoreText(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, r := range value {
		if r < 0x21 || r == 0x7f {
			return false
		}
	}
	return true
}
func sameSnapshot(a, b mission.MissionSnapshot) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(aa) == string(bb)
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func isUnique(err error) bool {
	return err != nil && (contains(err.Error(), "UNIQUE constraint") || contains(err.Error(), "PRIMARY KEY"))
}
func contains(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
