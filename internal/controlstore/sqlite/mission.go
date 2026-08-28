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
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

var _ mission.Store = (*Store)(nil)

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
	_, err = tx.ExecContext(ctx, `INSERT INTO phase3_missions(mission_id,project_id,definition_id,definition_version,definition_commitment,lifecycle,current_step,current_work_revision_id,state_version,created_at,updated_at,snapshot_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, commit.Instance.MissionID, commit.Instance.ProjectID, commit.Instance.DefinitionID, commit.Instance.DefinitionVersion, commit.Instance.DefinitionCommitment, commit.Instance.Lifecycle, commit.Instance.CurrentStep, nullString(commit.Instance.CurrentWorkRevisionID.String()), commit.Instance.StateVersion, formatTime(commit.Instance.CreatedAt), formatTime(commit.Instance.UpdatedAt), encoded)
	if err != nil {
		if isUnique(err) {
			return mission.CreateResult{}, missionError(mission.KindAlreadyExists, "mission", "mission already exists", nil)
		}
		return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	resultJSON, _ := json.Marshal(mission.CreateResult{Instance: commit.Instance})
	if err = insertPhase3Operation(ctx, tx, commit.OperationID.String(), "create_mission", commit.Instance.MissionID.String(), commit.RequestCommitment.String(), resultJSON, commit.OccurredAt); err != nil {
		return mission.CreateResult{}, err
	}
	if err = insertPhase3Event(ctx, tx, "mission-created:"+commit.OperationID.String(), commit.OperationID.String(), commit.Instance.MissionID.String(), "mission_created", commit.RequestCommitment.String(), commit.OccurredAt); err != nil {
		return mission.CreateResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return mission.CreateResult{}, missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	return mission.CreateResult{Instance: commit.Instance}, nil
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
	var result mission.CreateResult
	if err := strictJSON(raw, &result); err != nil || result.Instance.Validate() != nil {
		return mission.CreateResult{}, false, missionError(mission.KindInvalidStoredState, "operation", "stored mission result is invalid", err)
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
	res, err := tx.ExecContext(ctx, `UPDATE phase3_missions SET lifecycle=?,current_step=?,current_work_revision_id=?,state_version=?,updated_at=?,snapshot_json=? WHERE mission_id=? AND state_version=?`, commit.Candidate.Successor.Lifecycle, commit.Candidate.Successor.CurrentStep, nullString(commit.Candidate.Successor.CurrentWorkRevisionID.String()), commit.Candidate.Successor.StateVersion, formatTime(commit.Candidate.Successor.UpdatedAt), successorJSON, commit.Candidate.Successor.MissionID, commit.Candidate.Previous.StateVersion)
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
	_, err := tx.ExecContext(ctx, `INSERT INTO phase3_operations(operation_id,operation_kind,mission_id,request_digest,result_json,occurred_at) VALUES(?,?,?,?,?,?)`, op, kind, missionID, digest, result, formatTime(at))
	if err != nil {
		return missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	return nil
}
func insertPhase3Event(ctx context.Context, tx *sql.Tx, eventID, op, missionID, kind, digest string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO phase3_events(event_id,operation_id,mission_id,event_kind,payload_digest,occurred_at) VALUES(?,?,?,?,?,?)`, eventID, op, missionID, kind, digest, formatTime(at)); err != nil {
		return missionError(mission.KindPersistenceUnavailable, "store", "mission persistence operation failed", err)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO phase3_outbox(event_id,event_kind,payload_digest,created_at) VALUES(?,?,?,?)`, eventID, kind, digest, formatTime(at))
	return err
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
