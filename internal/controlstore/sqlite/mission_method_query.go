package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	missionreadmodel "github.com/spec-kitty/go-kitty3/internal/mission/readmodel"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	workgraphreadmodel "github.com/spec-kitty/go-kitty3/internal/workgraph/readmodel"
)

// MissionMethodObservation is one adapter-owned, transaction-consistent view
// for presentation consumers. It contains no storage DTO or projection view.
type MissionMethodObservation struct {
	Mission   missionreadmodel.Snapshot
	Workgraph workgraphreadmodel.Snapshot
}

// ObserveMissionMethod restores a mission and its exact current executable
// generation in one read transaction. Cross-generation joins fail closed.
func (s *Store) ObserveMissionMethod(ctx context.Context, missionID ids.MissionID) (MissionMethodObservation, error) {
	if _, err := ids.ParseMissionID(missionID.String()); err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: invalid mission identity")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: unavailable")
	}
	defer tx.Rollback()
	var missionBytes, revisionBytes []byte
	err = tx.QueryRowContext(ctx, `SELECT m.snapshot_json,r.encoded_revision
		FROM phase3_missions m
		JOIN phase3_current_work_revisions c ON c.mission_id=m.mission_id AND c.work_revision_id=m.current_work_revision_id
		JOIN phase3_work_revisions_v2 r ON r.mission_id=c.mission_id AND r.work_revision_id=c.work_revision_id AND r.generation=c.generation
		WHERE m.mission_id=?`, missionID).Scan(&missionBytes, &revisionBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: no finalized current generation")
	}
	if err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: unavailable")
	}
	missionValue, err := decodeSnapshot(missionBytes)
	if err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: stored mission is invalid")
	}
	revision, err := workgraph.DecodeRevisionV2(revisionBytes)
	if err != nil || revision.MissionID() != missionValue.MissionID || revision.ProjectID() != missionValue.ProjectID || revision.ID() != missionValue.CurrentWorkRevisionID {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: current generation is inconsistent")
	}

	driftStatus := workgraphreadmodel.DriftNotChecked
	var reconciliations []workgraphreadmodel.ReconciliationParams
	var mixedBytes, driftBytes, oldBytes []byte
	err = tx.QueryRowContext(ctx, `SELECT r.encoded_reconciliation,d.encoded_report,old_revision.encoded_revision
		FROM phase3_mixed_reconciliations r
		JOIN phase3_drift_reports d ON d.drift_report_id=r.drift_report_id AND d.old_work_revision_id=r.old_work_revision_id AND d.new_work_revision_id=r.new_work_revision_id
		JOIN phase3_work_revisions_v2 old_revision ON old_revision.work_revision_id=r.old_work_revision_id AND old_revision.mission_id=?
		WHERE r.new_work_revision_id=?`, missionID, revision.ID()).Scan(&mixedBytes, &driftBytes, &oldBytes)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: reconciliation is unavailable")
	default:
		oldRevision, decodeErr := workgraph.DecodeRevisionV2(oldBytes)
		if decodeErr != nil || revision.PredecessorID() != oldRevision.ID() {
			return MissionMethodObservation{}, fmt.Errorf("mission method observation: reconciliation predecessor is invalid")
		}
		drift, driftErr := workgraph.DetectDrift(oldRevision, revision, mustDriftTime(driftBytes))
		canonicalDrift, encodeErr := drift.Encode()
		var wire mixedWire
		wireErr := strictJSON(mixedBytes, &wire)
		mixed, mixedErr := restoreMixed(wire, oldRevision, revision, drift)
		if driftErr != nil || encodeErr != nil || string(canonicalDrift) != string(driftBytes) || wireErr != nil || mixedErr != nil {
			return MissionMethodObservation{}, fmt.Errorf("mission method observation: reconciliation evidence is invalid")
		}
		switch drift.SemanticStatus() {
		case workgraph.SemanticClean:
			driftStatus = workgraphreadmodel.DriftClean
		case workgraph.SemanticObservationOnly:
			driftStatus = workgraphreadmodel.DriftObservationOnly
		case workgraph.SemanticChanged:
			driftStatus = workgraphreadmodel.DriftChanged
		default:
			return MissionMethodObservation{}, fmt.Errorf("mission method observation: drift state is invalid")
		}
		for _, decision := range mixed.Decisions() {
			reconciliations = append(reconciliations, workgraphreadmodel.ReconciliationParams{
				PreviousWorkID: decision.OldWorkID(), Disposition: workgraphreadmodel.ReconciliationDisposition(decision.Disposition()),
				CurrentWorkIDs: decision.NewWorkIDs(), Rationale: decision.Rationale(),
			})
		}
	}

	missionView, err := missionreadmodel.NewSnapshot(missionreadmodel.SnapshotParams{MissionID: missionValue.MissionID, StateVersion: missionValue.StateVersion, CurrentStep: missionValue.CurrentStep.String(), CurrentWorkRevisionID: revision.ID()})
	if err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: mission projection is invalid")
	}
	workParams := workgraphreadmodel.SnapshotParams{MissionID: missionID, RevisionID: revision.ID(), Generation: revision.Generation(), GraphDigest: revision.GraphDigest(), DriftStatus: driftStatus, Reconciliations: reconciliations}
	for _, item := range revision.WorkItems() {
		params := workgraphreadmodel.WorkItemParams{WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(), Dependencies: item.Dependencies()}
		for _, criterion := range item.Criteria() {
			params.Criteria = append(params.Criteria, workgraphreadmodel.CriterionParams{ID: criterion.ID(), Text: criterion.Text(), VerifierKind: criterion.VerifierKind(), Required: criterion.Required(), Metadata: criterion.Metadata()})
		}
		for _, scope := range item.WriteScopes() {
			params.WriteScopes = append(params.WriteScopes, workgraphreadmodel.ScopeParams{Path: scope.Path, Domain: scope.Domain, Access: string(scope.Access), SerializationKey: scope.SerializationKey})
		}
		workParams.WorkItems = append(workParams.WorkItems, params)
	}
	for _, lane := range revision.Lanes() {
		workParams.Lanes = append(workParams.Lanes, workgraphreadmodel.LaneParams{LaneID: lane.ID(), WorkIDs: lane.WorkIDs(), DependencyLaneIDs: lane.DependencyLaneIDs()})
	}
	workView, err := workgraphreadmodel.NewSnapshot(workParams)
	if err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: work projection is invalid")
	}
	if err = tx.Commit(); err != nil {
		return MissionMethodObservation{}, fmt.Errorf("mission method observation: unavailable")
	}
	return MissionMethodObservation{Mission: missionView, Workgraph: workView}, nil
}
