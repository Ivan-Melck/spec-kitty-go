package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

var _ mission.SuccessorPublicationStore = (*Store)(nil)

type mixedDecisionWire struct {
	OldWorkID   ids.WorkID            `json:"old_work_id"`
	Status      workgraph.WorkStatus  `json:"status"`
	Disposition workgraph.Disposition `json:"disposition"`
	NewWorkIDs  []ids.WorkID          `json:"new_work_ids"`
	ForkEdges   []workgraph.ForkEdge  `json:"fork_edges"`
	Rationale   string                `json:"rationale"`
}
type mixedWire struct {
	ID            string              `json:"id"`
	ActorID       ids.ActorID         `json:"actor_id"`
	Rationale     string              `json:"rationale"`
	OccurredAt    string              `json:"occurred_at"`
	Decisions     []mixedDecisionWire `json:"decisions"`
	RequestDigest string              `json:"request_digest"`
}
type successorResultWire struct {
	Revision       json.RawMessage           `json:"revision"`
	Drift          json.RawMessage           `json:"drift"`
	Reconciliation mixedWire                 `json:"reconciliation"`
	Mission        mission.MissionSnapshot   `json:"mission"`
	Actions        []mission.AuthorityAction `json:"actions"`
	RequestDigest  string                    `json:"request_digest"`
}

func mixedToWire(value workgraph.MixedReconciliation) mixedWire {
	decisions := value.Decisions()
	out := mixedWire{ID: value.ID(), ActorID: value.ActorID(), Rationale: value.Rationale(), OccurredAt: formatTime(value.OccurredAt()), Decisions: make([]mixedDecisionWire, len(decisions)), RequestDigest: value.RequestDigest()}
	for i, d := range decisions {
		status, _ := value.AssignedStatus(d.OldWorkID())
		out.Decisions[i] = mixedDecisionWire{d.OldWorkID(), status, d.Disposition(), d.NewWorkIDs(), d.ForkEdges(), d.Rationale()}
	}
	return out
}
func restoreMixed(w mixedWire, old, new workgraph.RevisionV2, drift workgraph.DriftReport) (workgraph.MixedReconciliation, error) {
	at, err := parseTime(w.OccurredAt)
	if err != nil {
		return workgraph.MixedReconciliation{}, err
	}
	statuses := make([]workgraph.WorkStatusAssignment, len(w.Decisions))
	decisions := make([]workgraph.WorkDispositionDecisionParams, len(w.Decisions))
	for i, d := range w.Decisions {
		statuses[i] = workgraph.WorkStatusAssignment{WorkID: d.OldWorkID, Status: d.Status}
		decisions[i] = workgraph.WorkDispositionDecisionParams{OldWorkID: d.OldWorkID, Disposition: d.Disposition, NewWorkIDs: d.NewWorkIDs, ForkEdges: d.ForkEdges, Rationale: d.Rationale}
	}
	value, err := workgraph.ReconcileMixed(workgraph.MixedReconcileRequest{ID: w.ID, Old: old, New: new, Drift: drift, Statuses: statuses, Decisions: decisions, ActorID: w.ActorID, Rationale: w.Rationale, OccurredAt: at})
	if err != nil || value.RequestDigest() != w.RequestDigest {
		return workgraph.MixedReconciliation{}, errors.New("stored mixed reconciliation is invalid")
	}
	return value, nil
}
func parseTime(v string) (time.Time, error) { return time.Parse(time.RFC3339Nano, v) }

func (s *Store) ReplaySuccessorPublication(ctx context.Context, missionID ids.MissionID, op ids.OperationID, digest string) (mission.SuccessorPublicationResult, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return mission.SuccessorPublicationResult{}, false, missionError(mission.KindPersistenceUnavailable, "store", "successor replay is unavailable", err)
	}
	defer tx.Rollback()
	return loadSuccessorOperationTx(ctx, tx, missionID, op, digest)
}

// decodeSuccessorResultTx restores every immutable component from the same
// database snapshot. It never consults mutable current pointers.
func decodeSuccessorResultTx(ctx context.Context, q rowQuery, raw []byte) (mission.SuccessorPublicationResult, error) {
	var w successorResultWire
	if err := strictJSON(raw, &w); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "successor", "stored successor result is invalid", err)
	}
	newRevision, err := workgraph.DecodeRevisionV2(w.Revision)
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "successor", "stored successor revision is invalid", err)
	}
	var oldBytes []byte
	if err = q.QueryRowContext(ctx, `SELECT encoded_revision FROM phase3_work_revisions_v2 WHERE work_revision_id=?`, newRevision.PredecessorID()).Scan(&oldBytes); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "successor", "stored predecessor is missing", err)
	}
	oldRevision, err := workgraph.DecodeRevisionV2(oldBytes)
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "successor", "stored predecessor is invalid", err)
	}
	drift, err := workgraph.DetectDrift(oldRevision, newRevision, mustDriftTime(w.Drift))
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "drift", "stored drift is invalid", err)
	}
	actualDrift, _ := drift.Encode()
	if string(actualDrift) != string(w.Drift) {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "drift", "stored drift evidence changed", nil)
	}
	mixed, err := restoreMixed(w.Reconciliation, oldRevision, newRevision, drift)
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "reconciliation", "stored reconciliation is invalid", err)
	}
	if w.Mission.Validate() != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidStoredState, "mission", "stored successor mission is invalid", nil)
	}
	return mission.SuccessorPublicationResult{Revision: newRevision, Drift: drift, Reconciliation: mixed, Mission: w.Mission, Actions: append([]mission.AuthorityAction(nil), w.Actions...), RequestDigest: w.RequestDigest}, nil
}
func mustDriftTime(raw []byte) time.Time {
	var fields struct {
		ObservedAt string `json:"observed_at"`
	}
	_ = json.Unmarshal(raw, &fields)
	t, _ := parseTime(fields.ObservedAt)
	return t
}

func (s *Store) PublishSuccessorAtomically(ctx context.Context, c mission.SuccessorPublicationCommit) (mission.SuccessorPublicationResult, error) {
	oldBytes, err := c.ExpectedOld.Encode()
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidRequest, "old_revision", "predecessor is invalid", err)
	}
	newBytes, err := c.Candidate.Encode()
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidRequest, "candidate", "successor is invalid", err)
	}
	driftBytes, err := c.Drift.Encode()
	if err != nil || c.ExpectedMission.Validate() != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidRequest, "commit", "successor commit is invalid", err)
	}
	recomputed, err := workgraph.DetectDrift(c.ExpectedOld, c.Candidate, c.Drift.ObservedAt())
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidRequest, "drift", "drift is invalid", err)
	}
	check, _ := recomputed.Encode()
	if string(check) != string(driftBytes) {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidRequest, "drift", "drift is not canonical", nil)
	}
	mw := mixedToWire(c.Reconciliation)
	if restored, e := restoreMixed(mw, c.ExpectedOld, c.Candidate, c.Drift); e != nil || restored.RequestDigest() != c.Reconciliation.RequestDigest() {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindInvalidRequest, "reconciliation", "reconciliation is invalid", e)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindPersistenceUnavailable, "store", "successor publication is unavailable", err)
	}
	defer tx.Rollback()
	if prior, found, e := loadSuccessorOperationTx(ctx, tx, c.MissionID, c.OperationID, c.RequestDigest); e != nil {
		return mission.SuccessorPublicationResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	var missionRaw []byte
	var stateVersion uint64
	if err = tx.QueryRowContext(ctx, `SELECT state_version,snapshot_json FROM phase3_missions WHERE mission_id=?`, c.MissionID).Scan(&stateVersion, &missionRaw); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "mission", "expected mission is not current", nil)
	}
	current, e := decodeSnapshot(missionRaw)
	if e != nil {
		return mission.SuccessorPublicationResult{}, e
	}
	if stateVersion != c.ExpectedMission.StateVersion || !sameSnapshot(current, c.ExpectedMission) {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "mission", "expected mission is not current", nil)
	}
	var currentID string
	var generation uint64
	if err = tx.QueryRowContext(ctx, `SELECT work_revision_id,generation FROM phase3_current_work_revisions WHERE mission_id=?`, c.MissionID).Scan(&currentID, &generation); err != nil || currentID != c.ExpectedOld.ID().String() || generation != c.ExpectedCurrentGeneration {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "generation", "current generation changed", nil)
	}
	var persistedOld []byte
	if err = tx.QueryRowContext(ctx, `SELECT encoded_revision FROM phase3_work_revisions_v2 WHERE work_revision_id=?`, c.ExpectedOld.ID()).Scan(&persistedOld); err != nil || string(persistedOld) != string(oldBytes) {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "old_revision", "predecessor changed", nil)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_work_revisions_v2(work_revision_id,mission_id,project_id,generation,predecessor_id,graph_digest,criteria_digest,encoded_revision) VALUES(?,?,?,?,?,?,?,?)`, c.Candidate.ID(), c.MissionID, c.ProjectID, c.Candidate.Generation(), c.ExpectedOld.ID(), c.Candidate.GraphDigest(), c.Candidate.CriteriaDigest(), newBytes); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "generation", "successor publication lost compare-and-set", err)
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_drift_reports(drift_report_id,mission_id,old_work_revision_id,new_work_revision_id,semantic_status,encoded_report) VALUES(?,?,?,?,?,?)`, c.Drift.ID(), c.MissionID, c.ExpectedOld.ID(), c.Candidate.ID(), c.Drift.SemanticStatus(), driftBytes); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindPersistenceUnavailable, "store", "successor publication is unavailable", err)
	}
	mixedJSON, _ := json.Marshal(mw)
	if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_mixed_reconciliations(reconciliation_id,drift_report_id,old_work_revision_id,new_work_revision_id,request_digest,encoded_reconciliation) VALUES(?,?,?,?,?,?)`, c.Reconciliation.ID(), c.Drift.ID(), c.ExpectedOld.ID(), c.Candidate.ID(), c.Reconciliation.RequestDigest(), mixedJSON); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindPersistenceUnavailable, "store", "successor publication is unavailable", err)
	}
	if err = persistEligibilityFacts(ctx, tx, c.Reconciliation); err != nil {
		return mission.SuccessorPublicationResult{}, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE phase3_current_work_revisions SET work_revision_id=?,generation=? WHERE mission_id=? AND work_revision_id=? AND generation=?`, c.Candidate.ID(), c.Candidate.Generation(), c.MissionID, c.ExpectedOld.ID(), c.ExpectedCurrentGeneration)
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindPersistenceUnavailable, "store", "successor publication is unavailable", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "generation", "current generation changed", nil)
	}
	missionAfter := c.ExpectedMission
	missionAfter.CurrentWorkRevisionID = c.Candidate.ID()
	missionAfter.StateVersion++
	missionAfter.UpdatedAt = c.OccurredAt
	missionJSON, _ := encodeSnapshot(missionAfter)
	res, err = tx.ExecContext(ctx, `UPDATE phase3_missions SET current_work_revision_id=?,state_version=?,updated_at=?,snapshot_json=? WHERE mission_id=? AND state_version=?`, missionAfter.CurrentWorkRevisionID, missionAfter.StateVersion, formatTime(missionAfter.UpdatedAt), missionJSON, c.MissionID, c.ExpectedMission.StateVersion)
	if err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindPersistenceUnavailable, "store", "successor publication is unavailable", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindStateVersionConflict, "mission", "mission state changed", nil)
	}
	result := mission.SuccessorPublicationResult{Revision: c.Candidate, Drift: c.Drift, Reconciliation: c.Reconciliation, Mission: missionAfter, Actions: append([]mission.AuthorityAction(nil), c.Actions...), RequestDigest: c.RequestDigest}
	wire := successorResultWire{newBytes, driftBytes, mw, missionAfter, result.Actions, c.RequestDigest}
	resultJSON, _ := json.Marshal(wire)
	if err = insertPhase3Operation(ctx, tx, c.OperationID.String(), "publish_successor", c.MissionID.String(), c.RequestDigest, resultJSON, c.OccurredAt); err != nil {
		return mission.SuccessorPublicationResult{}, err
	}
	if err = insertPhase3Event(ctx, tx, "successor-published:"+c.OperationID.String(), c.OperationID.String(), c.MissionID.String(), "work_successor_published", c.RequestDigest, c.OccurredAt); err != nil {
		return mission.SuccessorPublicationResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return mission.SuccessorPublicationResult{}, missionError(mission.KindPersistenceUnavailable, "store", "successor publication is unavailable", err)
	}
	return result, nil
}

func loadSuccessorOperationTx(ctx context.Context, q rowQuery, missionID ids.MissionID, op ids.OperationID, digest string) (mission.SuccessorPublicationResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.SuccessorPublicationResult{}, false, nil
	}
	if err != nil {
		return mission.SuccessorPublicationResult{}, false, missionError(mission.KindPersistenceUnavailable, "store", "successor replay is unavailable", err)
	}
	if kind != "publish_successor" || scope != missionID.String() || stored != digest {
		return mission.SuccessorPublicationResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	r, e := decodeSuccessorResultTx(ctx, q, raw)
	return r, e == nil, e
}

func persistEligibilityFacts(ctx context.Context, tx *sql.Tx, r workgraph.MixedReconciliation) error {
	for _, d := range r.Decisions() {
		for _, pair := range []struct {
			status    workgraph.WorkStatus
			operation workgraph.EligibilityOperation
		}{{workgraph.StatusActive, workgraph.EligibilityHandoff}, {workgraph.StatusHandedOff, workgraph.EligibilityReview}, {workgraph.StatusApproved, workgraph.EligibilityIntegration}} {
			fact, err := r.EligibilityFact(d.OldWorkID(), pair.status, pair.operation)
			if err != nil || !fact.Complete() {
				return missionError(mission.KindInvalidRequest, "eligibility", "eligibility authority is incomplete", err)
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO phase3_eligibility_facts(reconciliation_id,old_work_revision_id,new_work_revision_id,work_id,work_status,disposition,operation,allowed,reason) VALUES(?,?,?,?,?,?,?,?,?)`, fact.ReconciliationID(), fact.OldRevisionID(), fact.NewRevisionID(), fact.WorkID(), fact.Status(), fact.Disposition(), fact.Operation(), fact.Permitted(), fact.Reason()); err != nil {
				return missionError(mission.KindPersistenceUnavailable, "store", "eligibility persistence is unavailable", err)
			}
		}
	}
	return nil
}

func (s *Store) WorkEligibilityFact(ctx context.Context, oldRevision ids.WorkRevisionID, workID ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	var reconciliation, newRevision, disposition string
	var allowed bool
	var reason string
	err := s.db.QueryRowContext(ctx, `SELECT reconciliation_id,new_work_revision_id,disposition,allowed,reason FROM phase3_eligibility_facts WHERE old_work_revision_id=? AND work_id=? AND work_status=? AND operation=? ORDER BY reconciliation_id DESC LIMIT 1`, oldRevision, workID, status, operation).Scan(&reconciliation, &newRevision, &disposition, &allowed, &reason)
	if err != nil {
		return workgraph.EligibilityFact{}, err
	}
	fact, err := workgraph.NewEligibilityFact(reconciliation, oldRevision, ids.WorkRevisionID(newRevision), workID, status, workgraph.Disposition(disposition), operation)
	if err != nil || !fact.Complete() || fact.Permitted() != allowed || fact.Reason() != reason {
		return workgraph.EligibilityFact{}, errors.New("stored eligibility fact is invalid")
	}
	return fact, nil
}
