package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/artifact"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

var _ mission.ActionStore = (*Store)(nil)
var _ mission.MissionFactStore = (*Store)(nil)
var _ mission.NextSnapshotSource = (*Store)(nil)

type decisionWire struct {
	Params     mission.DecisionParams     `json:"params"`
	Status     mission.DecisionStatus     `json:"status"`
	Version    uint64                     `json:"version"`
	Resolution mission.DecisionResolution `json:"resolution"`
}
type blockerWire struct {
	Params    mission.BlockerParams    `json:"params"`
	Status    mission.BlockerStatus    `json:"status"`
	Version   uint64                   `json:"version"`
	Clearance mission.BlockerClearance `json:"clearance"`
}
type actionOutcomeWire struct {
	Mission     mission.MissionSnapshot `json:"mission"`
	Decision    *decisionWire           `json:"decision"`
	Blocker     *blockerWire            `json:"blocker"`
	AffectedIDs []string                `json:"affected_ids"`
}
type actionResultWire struct {
	OperationID   ids.OperationID       `json:"operation_id"`
	MissionID     ids.MissionID         `json:"mission_id"`
	ActionKey     string                `json:"action_key"`
	RequestDigest string                `json:"request_digest"`
	Outcome       actionOutcomeWire     `json:"outcome"`
	Receipt       mission.ActionReceipt `json:"receipt"`
}
type missionFactResultWire struct {
	Mission       mission.MissionSnapshot `json:"mission"`
	Decision      *decisionWire           `json:"decision"`
	Blocker       *blockerWire            `json:"blocker"`
	OperationID   ids.OperationID         `json:"operation_id"`
	RequestDigest string                  `json:"request_digest"`
}

func decisionToWire(d mission.Decision) *decisionWire {
	return &decisionWire{mission.DecisionParams{ID: d.ID(), MissionID: d.MissionID(), Step: d.Step(), Question: d.Question(), Mode: d.Mode(), Options: d.Options(), CreatedBy: d.CreatedBy(), GovernanceContextID: d.GovernanceContextID(), CreatedAt: d.CreatedAt()}, d.Status(), d.Version(), d.Resolution()}
}
func restoreDecision(w *decisionWire) (*mission.Decision, error) {
	if w == nil {
		return nil, nil
	}
	d, err := mission.RestoreDecision(w.Params, w.Status, w.Version, w.Resolution)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
func blockerToWire(b mission.Blocker) *blockerWire {
	return &blockerWire{mission.BlockerParams{ID: b.ID(), MissionID: b.MissionID(), WorkID: b.WorkID(), Step: b.Step(), Kind: b.Kind(), Summary: b.Summary(), RecoveryActions: b.RecoveryActions(), ObservedAt: b.ObservedAt()}, b.Status(), b.Version(), b.Clearance()}
}
func restoreBlocker(w *blockerWire) (*mission.Blocker, error) {
	if w == nil {
		return nil, nil
	}
	b, err := mission.RestoreBlocker(w.Params, w.Status, w.Version, w.Clearance)
	if err != nil {
		return nil, err
	}
	return &b, nil
}
func outcomeToWire(o mission.ActionOutcome) actionOutcomeWire {
	var d *decisionWire
	var b *blockerWire
	if o.Decision != nil {
		d = decisionToWire(*o.Decision)
	}
	if o.Blocker != nil {
		b = blockerToWire(*o.Blocker)
	}
	return actionOutcomeWire{o.Mission, d, b, append([]string(nil), o.AffectedIDs...)}
}
func restoreOutcome(w actionOutcomeWire) (mission.ActionOutcome, error) {
	if w.Mission.Validate() != nil {
		return mission.ActionOutcome{}, errors.New("stored action mission is invalid")
	}
	d, err := restoreDecision(w.Decision)
	if err != nil {
		return mission.ActionOutcome{}, err
	}
	b, err := restoreBlocker(w.Blocker)
	if err != nil {
		return mission.ActionOutcome{}, err
	}
	return mission.ActionOutcome{Mission: w.Mission, Decision: d, Blocker: b, AffectedIDs: append([]string(nil), w.AffectedIDs...)}, nil
}

func (s *Store) ReplayAction(ctx context.Context, missionID ids.MissionID, op ids.OperationID, digest string) (mission.ActionResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.ActionResult{}, false, nil
	}
	if err != nil {
		return mission.ActionResult{}, false, missionError(mission.KindPersistenceUnavailable, "action.store", "action replay is unavailable", err)
	}
	if kind != "mission_action" || scope != missionID.String() || stored != digest {
		return mission.ActionResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	result, err := decodeActionResult(raw)
	if err != nil {
		return mission.ActionResult{}, false, err
	}
	return result, true, nil
}
func decodeActionResult(raw []byte) (mission.ActionResult, error) {
	var w actionResultWire
	if err := strictJSON(raw, &w); err != nil {
		return mission.ActionResult{}, missionError(mission.KindInvalidStoredState, "action.result", "stored action result is invalid", err)
	}
	outcome, err := restoreOutcome(w.Outcome)
	if err != nil || w.Receipt.Validate() != nil {
		return mission.ActionResult{}, missionError(mission.KindInvalidStoredState, "action.result", "stored action result is invalid", err)
	}
	return mission.ActionResult{OperationID: w.OperationID, MissionID: w.MissionID, ActionKey: w.ActionKey, RequestDigest: w.RequestDigest, Outcome: outcome, Receipt: w.Receipt}, nil
}

func (s *Store) CommitAction(ctx context.Context, c mission.ActionCommit) (mission.ActionResult, error) {
	if c.Expected.Validate() != nil || c.Outcome.Mission.Validate() != nil || c.Governance.Validate() != nil || c.Receipt.Validate() != nil {
		return mission.ActionResult{}, missionError(mission.KindInvalidRequest, "action.commit", "action commit is invalid", nil)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.ActionResult{}, missionError(mission.KindPersistenceUnavailable, "action.store", "action persistence is unavailable", err)
	}
	defer tx.Rollback()
	if prior, found, e := loadActionTx(ctx, tx, c.MissionID, c.OperationID, c.RequestDigest); e != nil {
		return mission.ActionResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	var raw []byte
	var version uint64
	if err = tx.QueryRowContext(ctx, `SELECT state_version,snapshot_json FROM phase3_missions WHERE mission_id=?`, c.MissionID).Scan(&version, &raw); err != nil {
		return mission.ActionResult{}, missionError(mission.KindStateVersionConflict, "expected_mission", "mission state changed", nil)
	}
	current, e := decodeSnapshot(raw)
	if e != nil {
		return mission.ActionResult{}, e
	}
	if version != c.Expected.StateVersion || !sameSnapshot(current, c.Expected) {
		return mission.ActionResult{}, missionError(mission.KindStateVersionConflict, "expected_mission", "mission state changed", nil)
	}
	storedGovernance, found, e := loadGovernanceContext(ctx, tx, c.Governance.ContextID())
	if e != nil || !found || storedGovernance.ID() != c.Governance.ContextID() || storedGovernance.Aggregate() != c.Governance.Aggregate() {
		return mission.ActionResult{}, missionError(mission.KindInvalidRequest, "governance", "sealed governance context is unavailable", e)
	}
	if err = persistActionFact(ctx, tx, c.Outcome.Decision, c.Outcome.Blocker); err != nil {
		return mission.ActionResult{}, err
	}
	missionJSON, _ := encodeSnapshot(c.Outcome.Mission)
	res, err := tx.ExecContext(ctx, `UPDATE phase3_missions SET lifecycle=?,current_step=?,current_work_revision_id=?,state_version=?,updated_at=?,snapshot_json=? WHERE mission_id=? AND state_version=?`, c.Outcome.Mission.Lifecycle, c.Outcome.Mission.CurrentStep, nullString(c.Outcome.Mission.CurrentWorkRevisionID.String()), c.Outcome.Mission.StateVersion, formatTime(c.Outcome.Mission.UpdatedAt), missionJSON, c.MissionID, c.Expected.StateVersion)
	if err != nil {
		return mission.ActionResult{}, missionError(mission.KindPersistenceUnavailable, "action.store", "action persistence is unavailable", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return mission.ActionResult{}, missionError(mission.KindStateVersionConflict, "expected_mission", "mission state changed", nil)
	}
	result := mission.ActionResult{OperationID: c.OperationID, MissionID: c.MissionID, ActionKey: c.ActionKey, RequestDigest: c.RequestDigest, Outcome: c.Outcome, Receipt: c.Receipt}
	wire := actionResultWire{c.OperationID, c.MissionID, c.ActionKey, c.RequestDigest, outcomeToWire(c.Outcome), c.Receipt}
	resultJSON, _ := json.Marshal(wire)
	if err = insertPhase3Operation(ctx, tx, c.OperationID.String(), "mission_action", c.MissionID.String(), c.RequestDigest, resultJSON, c.OccurredAt); err != nil {
		return mission.ActionResult{}, err
	}
	if err = insertPhase3Event(ctx, tx, "mission-action:"+c.OperationID.String(), c.OperationID.String(), c.MissionID.String(), "mission_action_committed", c.RequestDigest, c.OccurredAt); err != nil {
		return mission.ActionResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return mission.ActionResult{}, missionError(mission.KindPersistenceUnavailable, "action.store", "action persistence is unavailable", err)
	}
	return result, nil
}
func loadActionTx(ctx context.Context, q rowQuery, missionID ids.MissionID, op ids.OperationID, digest string) (mission.ActionResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.ActionResult{}, false, nil
	}
	if err != nil {
		return mission.ActionResult{}, false, missionError(mission.KindPersistenceUnavailable, "action.store", "action replay is unavailable", err)
	}
	if kind != "mission_action" || scope != missionID.String() || stored != digest {
		return mission.ActionResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	r, e := decodeActionResult(raw)
	return r, e == nil, e
}

func persistActionFact(ctx context.Context, tx *sql.Tx, d *mission.Decision, b *mission.Blocker) error {
	if d != nil {
		w := decisionToWire(*d)
		raw, _ := json.Marshal(w)
		res, err := tx.ExecContext(ctx, `INSERT INTO phase3_decisions(decision_id,mission_id,status,version,decision_json) VALUES(?,?,?,?,?) ON CONFLICT(decision_id) DO UPDATE SET status=excluded.status,version=excluded.version,decision_json=excluded.decision_json WHERE phase3_decisions.mission_id=excluded.mission_id AND phase3_decisions.version+1=excluded.version`, d.ID(), d.MissionID(), d.Status(), d.Version(), raw)
		if err != nil {
			return missionError(mission.KindPersistenceUnavailable, "decision", "decision persistence is unavailable", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return missionError(mission.KindStateVersionConflict, "decision", "decision version changed", nil)
		}
	}
	if b != nil {
		w := blockerToWire(*b)
		raw, _ := json.Marshal(w)
		res, err := tx.ExecContext(ctx, `INSERT INTO phase3_blockers(blocker_id,mission_id,status,version,blocker_json) VALUES(?,?,?,?,?) ON CONFLICT(blocker_id) DO UPDATE SET status=excluded.status,version=excluded.version,blocker_json=excluded.blocker_json WHERE phase3_blockers.mission_id=excluded.mission_id AND phase3_blockers.version+1=excluded.version`, b.ID(), b.MissionID(), b.Status(), b.Version(), raw)
		if err != nil {
			return missionError(mission.KindPersistenceUnavailable, "blocker", "blocker persistence is unavailable", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return missionError(mission.KindStateVersionConflict, "blocker", "blocker version changed", nil)
		}
	}
	return nil
}

func (s *Store) ObserveAction(ctx context.Context, id ids.MissionID) (mission.NextSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return mission.NextSnapshot{}, missionError(mission.KindPersistenceUnavailable, "next", "next snapshot is unavailable", err)
	}
	defer tx.Rollback()
	return observeNextTx(ctx, tx, id)
}
func (s *Store) ObserveNext(ctx context.Context, id ids.MissionID) (mission.NextSnapshot, error) {
	return s.ObserveAction(ctx, id)
}
func observeNextTx(ctx context.Context, tx *sql.Tx, id ids.MissionID) (mission.NextSnapshot, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_json FROM phase3_missions WHERE mission_id=?`, id).Scan(&raw); err != nil {
		return mission.NextSnapshot{}, missionError(mission.KindNotFound, "mission", "mission was not found", nil)
	}
	snapshot, err := decodeSnapshot(raw)
	if err != nil {
		return mission.NextSnapshot{}, err
	}
	definition, err := softwaredev.Definition()
	if err != nil || definition.ID() != snapshot.DefinitionID || definition.Version() != snapshot.DefinitionVersion || definition.Commitment() != snapshot.DefinitionCommitment {
		return mission.NextSnapshot{}, missionError(mission.KindDefinitionMismatch, "definition", "stored definition does not match", err)
	}
	result := mission.NextSnapshot{Mission: snapshot, Definition: definition}
	result.Decisions, err = loadDecisionsTx(ctx, tx, id)
	if err != nil {
		return mission.NextSnapshot{}, err
	}
	result.Blockers, err = loadBlockersTx(ctx, tx, id)
	if err != nil {
		return mission.NextSnapshot{}, err
	}
	if snapshot.CurrentWorkRevisionID != "" {
		var revisionBytes []byte
		if err = tx.QueryRowContext(ctx, `SELECT encoded_revision FROM phase3_work_revisions_v2 WHERE work_revision_id=?`, snapshot.CurrentWorkRevisionID).Scan(&revisionBytes); err != nil {
			return mission.NextSnapshot{}, missionError(mission.KindInvalidStoredState, "work_revision", "current work revision is missing", err)
		}
		revision, e := workgraph.DecodeRevisionV2(revisionBytes)
		if e != nil {
			return mission.NextSnapshot{}, missionError(mission.KindInvalidStoredState, "work_revision", "current work revision is invalid", e)
		}
		result.CurrentWorkRevision = &revision
	}
	result.ArtifactObservations, err = loadArtifactObservationsTx(ctx, tx, id.String())
	if err != nil {
		return mission.NextSnapshot{}, err
	}
	return result, nil
}
func loadDecisionsTx(ctx context.Context, tx *sql.Tx, id ids.MissionID) ([]mission.Decision, error) {
	rows, err := tx.QueryContext(ctx, `SELECT decision_json FROM phase3_decisions WHERE mission_id=? ORDER BY decision_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mission.Decision
	for rows.Next() {
		var raw []byte
		_ = rows.Scan(&raw)
		var w decisionWire
		if strictJSON(raw, &w) != nil {
			return nil, missionError(mission.KindInvalidStoredState, "decision", "stored decision is invalid", nil)
		}
		d, e := restoreDecision(&w)
		if e != nil {
			return nil, e
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}
func loadBlockersTx(ctx context.Context, tx *sql.Tx, id ids.MissionID) ([]mission.Blocker, error) {
	rows, err := tx.QueryContext(ctx, `SELECT blocker_json FROM phase3_blockers WHERE mission_id=? ORDER BY blocker_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mission.Blocker
	for rows.Next() {
		var raw []byte
		_ = rows.Scan(&raw)
		var w blockerWire
		if strictJSON(raw, &w) != nil {
			return nil, missionError(mission.KindInvalidStoredState, "blocker", "stored blocker is invalid", nil)
		}
		b, e := restoreBlocker(&w)
		if e != nil {
			return nil, e
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}
func loadArtifactObservationsTx(ctx context.Context, tx *sql.Tx, missionID string) ([]artifact.CurrentApprovalObservation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT revision_json FROM phase3_artifact_revisions r WHERE mission_id=? AND revision_number=(SELECT MAX(revision_number) FROM phase3_artifact_revisions x WHERE x.mission_id=r.mission_id AND x.kind=r.kind) ORDER BY kind`, missionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []artifact.CurrentApprovalObservation
	for rows.Next() {
		var raw []byte
		_ = rows.Scan(&raw)
		var rw artifactRevisionWire
		if strictJSON(raw, &rw) != nil {
			return nil, errors.New("stored artifact revision is invalid")
		}
		rev, e := restoreRevision(rw)
		if e != nil {
			return nil, e
		}
		approvalRows, e := tx.QueryContext(ctx, `SELECT approval_json FROM phase3_artifact_approvals WHERE artifact_revision_id=? ORDER BY submitted_at`, rev.ID())
		if e != nil {
			return nil, e
		}
		var approvals []artifact.ArtifactApproval
		for approvalRows.Next() {
			var b []byte
			_ = approvalRows.Scan(&b)
			var aw approvalWire
			if strictJSON(b, &aw) != nil {
				approvalRows.Close()
				return nil, errors.New("stored approval is invalid")
			}
			a, e := restoreApproval(aw)
			if e != nil {
				approvalRows.Close()
				return nil, e
			}
			approvals = append(approvals, a)
		}
		approvalRows.Close()
		observation, e := artifact.NewCurrentApprovalObservation(rev, approvals)
		if e != nil {
			return nil, e
		}
		out = append(out, observation)
	}
	return out, rows.Err()
}

func (s *Store) ReplayMissionFact(ctx context.Context, id ids.MissionID, op ids.OperationID, digest string) (mission.MissionFactResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.MissionFactResult{}, false, nil
	}
	if err != nil {
		return mission.MissionFactResult{}, false, err
	}
	if kind != "mission_fact" || scope != id.String() || stored != digest {
		return mission.MissionFactResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	r, e := decodeMissionFactResult(raw)
	return r, e == nil, e
}
func (s *Store) CommitMissionFact(ctx context.Context, c mission.MissionFactCommit) (mission.MissionFactResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.MissionFactResult{}, err
	}
	defer tx.Rollback()
	if prior, found, e := loadMissionFactTx(ctx, tx, c.Expected.MissionID, c.OperationID, c.RequestDigest); e != nil {
		return mission.MissionFactResult{}, e
	} else if found {
		prior.Replayed = true
		return prior, nil
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT snapshot_json FROM phase3_missions WHERE mission_id=? AND state_version=?`, c.Expected.MissionID, c.Expected.StateVersion).Scan(&raw); err != nil {
		return mission.MissionFactResult{}, missionError(mission.KindStateVersionConflict, "mission", "mission state changed", nil)
	}
	current, e := decodeSnapshot(raw)
	if e != nil || !sameSnapshot(current, c.Expected) {
		return mission.MissionFactResult{}, missionError(mission.KindStateVersionConflict, "mission", "mission state changed", e)
	}
	if err = persistActionFact(ctx, tx, c.Decision, c.Blocker); err != nil {
		return mission.MissionFactResult{}, err
	}
	nextJSON, _ := encodeSnapshot(c.Successor)
	res, err := tx.ExecContext(ctx, `UPDATE phase3_missions SET lifecycle=?,current_step=?,current_work_revision_id=?,state_version=?,updated_at=?,snapshot_json=? WHERE mission_id=? AND state_version=?`, c.Successor.Lifecycle, c.Successor.CurrentStep, nullString(c.Successor.CurrentWorkRevisionID.String()), c.Successor.StateVersion, formatTime(c.Successor.UpdatedAt), nextJSON, c.Expected.MissionID, c.Expected.StateVersion)
	if err != nil {
		return mission.MissionFactResult{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return mission.MissionFactResult{}, missionError(mission.KindStateVersionConflict, "mission", "mission state changed", nil)
	}
	result := mission.MissionFactResult{Mission: c.Successor, Decision: c.Decision, Blocker: c.Blocker, OperationID: c.OperationID, RequestDigest: c.RequestDigest}
	wire := missionFactResultWire{c.Successor, nil, nil, c.OperationID, c.RequestDigest}
	if c.Decision != nil {
		wire.Decision = decisionToWire(*c.Decision)
	}
	if c.Blocker != nil {
		wire.Blocker = blockerToWire(*c.Blocker)
	}
	resultJSON, _ := json.Marshal(wire)
	if err = insertPhase3Operation(ctx, tx, c.OperationID.String(), "mission_fact", c.Expected.MissionID.String(), c.RequestDigest, resultJSON, c.Successor.UpdatedAt); err != nil {
		return mission.MissionFactResult{}, err
	}
	if err = insertPhase3Event(ctx, tx, "mission-fact:"+c.OperationID.String(), c.OperationID.String(), c.Expected.MissionID.String(), "mission_fact_committed", c.RequestDigest, c.Successor.UpdatedAt); err != nil {
		return mission.MissionFactResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return mission.MissionFactResult{}, err
	}
	return result, nil
}
func loadMissionFactTx(ctx context.Context, q rowQuery, id ids.MissionID, op ids.OperationID, digest string) (mission.MissionFactResult, bool, error) {
	var kind, scope, stored string
	var raw []byte
	err := q.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest,result_json FROM phase3_operations WHERE operation_id=?`, op).Scan(&kind, &scope, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.MissionFactResult{}, false, nil
	}
	if err != nil {
		return mission.MissionFactResult{}, false, err
	}
	if kind != "mission_fact" || scope != id.String() || stored != digest {
		return mission.MissionFactResult{}, false, missionError(mission.KindIdempotencyConflict, "operation_id", "operation identity was reused", nil)
	}
	r, e := decodeMissionFactResult(raw)
	return r, e == nil, e
}
func decodeMissionFactResult(raw []byte) (mission.MissionFactResult, error) {
	var w missionFactResultWire
	if strictJSON(raw, &w) != nil || w.Mission.Validate() != nil {
		return mission.MissionFactResult{}, missionError(mission.KindInvalidStoredState, "mission_fact", "stored mission fact result is invalid", nil)
	}
	d, e := restoreDecision(w.Decision)
	if e != nil {
		return mission.MissionFactResult{}, e
	}
	b, e := restoreBlocker(w.Blocker)
	if e != nil {
		return mission.MissionFactResult{}, e
	}
	return mission.MissionFactResult{Mission: w.Mission, Decision: d, Blocker: b, OperationID: w.OperationID, RequestDigest: w.RequestDigest}, nil
}

// Mission action stores bind the original sealed context; this explicit use
// prevents adapters from accepting a caller-invented aggregate commitment.
var _ = governancecontext.Context{}
var _ = time.Time{}
