package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

var _ mission.FinalizationStore = (*Store)(nil)

type finalizationResultWire struct {
	Revision      json.RawMessage             `json:"revision"`
	Mission       mission.MissionSnapshot     `json:"mission"`
	RequestDigest string                      `json:"request_digest"`
	Receipt       mission.FinalizationReceipt `json:"receipt"`
}

func finalizationStoreError(kind mission.FinalizationErrorKind, field, message string, cause error) error {
	recovery := mission.FinalizationRetrySameOperation
	if kind == mission.FinalizationOperationConflict {
		recovery = mission.FinalizationCorrectInput
	}
	if kind == mission.FinalizationCurrentConflict {
		recovery = mission.FinalizationReconcileCurrent
	}
	if kind == mission.FinalizationContractFailure {
		recovery = mission.FinalizationRepairPersistence
	}
	return &mission.FinalizationError{Kind: kind, Recovery: recovery, Field: field, Message: message, Cause: cause}
}

func (s *Store) ReplayFinalization(ctx context.Context, missionID ids.MissionID, operationID ids.OperationID, digest string) (mission.FinalizationResult, bool, error) {
	return loadFinalizationOperation(ctx, s.db, missionID, operationID, digest)
}
func loadFinalizationOperation(ctx context.Context, q rowQuery, missionID ids.MissionID, operationID ids.OperationID, digest string) (mission.FinalizationResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, operationID).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.FinalizationResult{}, false, nil
	}
	if err != nil {
		return mission.FinalizationResult{}, false, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "finalization replay is unavailable", err)
	}
	if kind != "finalize_generation_one" || scope != missionID.String() || stored != digest {
		return mission.FinalizationResult{}, false, finalizationStoreError(mission.FinalizationOperationConflict, "operation_id", "operation identity was reused", nil)
	}
	result, err := decodeFinalizationResult(raw)
	if err != nil {
		return mission.FinalizationResult{}, false, err
	}
	return result, true, nil
}

func decodeFinalizationResult(raw []byte) (mission.FinalizationResult, error) {
	var wire finalizationResultWire
	if err := strictJSON(raw, &wire); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationContractFailure, "result", "stored finalization result is invalid", err)
	}
	revision, err := workgraph.DecodeRevisionV2(wire.Revision)
	if err != nil || wire.Mission.Validate() != nil || wire.Receipt.Validate() != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationContractFailure, "result", "stored finalization result is invalid", err)
	}
	return mission.FinalizationResult{Revision: revision, Mission: wire.Mission, RequestDigest: wire.RequestDigest, Receipt: wire.Receipt}, nil
}
func encodeFinalizationResult(r mission.FinalizationResult) ([]byte, error) {
	encoded, err := r.Revision.Encode()
	if err != nil {
		return nil, err
	}
	return json.Marshal(finalizationResultWire{encoded, r.Mission, r.RequestDigest, r.Receipt})
}

func (s *Store) PublishGenerationOne(ctx context.Context, c mission.FinalizationCommit) (mission.FinalizationResult, error) {
	encoded, err := c.Candidate.Encode()
	if err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationInvalidRequest, "candidate", "work revision is invalid", err)
	}
	restored, err := workgraph.DecodeRevisionV2(encoded)
	if err != nil || c.ExpectedMission.Validate() != nil || c.ExpectedReceipt.Validate() != nil || c.Candidate.Generation() != 1 || c.ExpectedCurrentGeneration != 0 || c.Candidate.PredecessorID() != "" {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationInvalidRequest, "commit", "generation-one commit is invalid", err)
	}
	if !sameRevisionV2(c.Candidate, restored) {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationInvalidRequest, "candidate", "work revision is not canonical", nil)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "finalization persistence is unavailable", err)
	}
	defer tx.Rollback()
	if prior, found, e := loadFinalizationOperation(ctx, tx, c.ExpectedMission.MissionID, c.OperationID, c.RequestDigest); e != nil {
		return mission.FinalizationResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	var currentRaw []byte
	var currentVersion uint64
	if err = tx.QueryRowContext(ctx, `SELECT state_version,snapshot_json FROM phase3_missions WHERE mission_id=?`, c.ExpectedMission.MissionID).Scan(&currentVersion, &currentRaw); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationCurrentConflict, "mission", "expected mission is not current", nil)
	}
	current, e := decodeSnapshot(currentRaw)
	if e != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationContractFailure, "mission", "stored mission is invalid", e)
	}
	if currentVersion != c.ExpectedMission.StateVersion || !sameSnapshot(current, c.ExpectedMission) {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationCurrentConflict, "mission", "expected mission is not current", nil)
	}
	var generation uint64
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(generation),0) FROM phase3_work_revisions_v2 WHERE mission_id=?`, c.ExpectedMission.MissionID).Scan(&generation)
	if generation != c.ExpectedCurrentGeneration {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationCurrentConflict, "generation", "current generation changed", nil)
	}
	missionAfter := c.ExpectedMission
	missionAfter.CurrentStep = mission.StepWorkFinalized
	missionAfter.CurrentWorkRevisionID = c.Candidate.ID()
	missionAfter.StateVersion++
	missionAfter.Lifecycle = mission.LifecycleTerminal
	missionAfter.TerminalOutcome = mission.OutcomeWorkFinalized
	missionAfter.UpdatedAt = c.OccurredAt
	if missionAfter.Validate() != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationInvalidRequest, "mission", "finalized mission would be invalid", nil)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_work_revisions_v2(work_revision_id,mission_id,project_id,generation,predecessor_id,graph_digest,criteria_digest,encoded_revision) VALUES(?,?,?,?,?,?,?,?)`, c.Candidate.ID(), c.Candidate.MissionID(), c.Candidate.ProjectID(), c.Candidate.Generation(), nullString(c.Candidate.PredecessorID().String()), c.Candidate.GraphDigest(), c.Candidate.CriteriaDigest(), encoded); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationCurrentConflict, "generation", "generation publication lost its compare-and-set", err)
	}
	missionJSON, _ := encodeSnapshot(missionAfter)
	res, err := tx.ExecContext(ctx, `UPDATE phase3_missions SET lifecycle=?,current_step=?,state_version=?,updated_at=?,snapshot_json=? WHERE mission_id=? AND state_version=?`, missionAfter.Lifecycle, missionAfter.CurrentStep, missionAfter.StateVersion, formatTime(missionAfter.UpdatedAt), missionJSON, missionAfter.MissionID, c.ExpectedMission.StateVersion)
	if err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "finalization persistence is unavailable", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationCurrentConflict, "mission", "mission state changed", nil)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_current_work_revisions(mission_id,work_revision_id,generation) VALUES(?,?,?)`, c.ExpectedMission.MissionID, c.Candidate.ID(), 1); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationCurrentConflict, "generation", "generation publication lost its compare-and-set", err)
	}
	result := mission.FinalizationResult{Revision: c.Candidate, Mission: missionAfter, RequestDigest: c.RequestDigest, Receipt: c.ExpectedReceipt}
	resultJSON, err := encodeFinalizationResult(result)
	if err != nil {
		return mission.FinalizationResult{}, err
	}
	if err = insertPhase3Operation(ctx, tx, c.OperationID.String(), "finalize_generation_one", c.ExpectedMission.MissionID.String(), c.RequestDigest, resultJSON, c.OccurredAt); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "finalization persistence is unavailable", err)
	}
	if err = insertPhase3Event(ctx, tx, "work-finalized:"+c.OperationID.String(), c.OperationID.String(), c.ExpectedMission.MissionID.String(), "work_generation_finalized", c.RequestDigest, c.OccurredAt); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "finalization persistence is unavailable", err)
	}
	if err = tx.Commit(); err != nil {
		return mission.FinalizationResult{}, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "finalization persistence is unavailable", err)
	}
	return result, nil
}

func sameRevisionV2(a, b workgraph.RevisionV2) bool {
	aa, _ := a.Encode()
	bb, _ := b.Encode()
	return string(aa) == string(bb)
}

func (s *Store) WorkRevisionV2(ctx context.Context, id ids.WorkRevisionID) (workgraph.RevisionV2, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT encoded_revision FROM phase3_work_revisions_v2 WHERE work_revision_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return workgraph.RevisionV2{}, finalizationStoreError(mission.FinalizationCurrentConflict, "work_revision_id", "work revision was not found", nil)
	}
	if err != nil {
		return workgraph.RevisionV2{}, finalizationStoreError(mission.FinalizationPersistenceFailure, "store", "work revision read is unavailable", err)
	}
	r, e := workgraph.DecodeRevisionV2(raw)
	if e != nil {
		return workgraph.RevisionV2{}, finalizationStoreError(mission.FinalizationContractFailure, "work_revision", "stored work revision is invalid", e)
	}
	return r, nil
}
