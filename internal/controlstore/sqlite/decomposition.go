package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

const decompositionOperationKind = "decomposition_saga"

var _ mission.DecompositionOperationAuthority = (*Store)(nil)

func (s *Store) ReserveDecompositionOperation(ctx context.Context, request mission.DecompositionOperationRequest) (mission.DecompositionOperationReservation, error) {
	if _, err := ids.ParseMissionID(request.MissionID.String()); err != nil {
		return mission.DecompositionOperationReservation{}, decompositionStoreError(mission.DecompositionInvalidRequest, "mission_id", "mission identity is invalid", err)
	}
	if _, err := ids.ParseOperationID(request.OperationID.String()); err != nil || len(request.RequestDigest) != 64 || !lowerHex(request.RequestDigest) || request.OccurredAt.IsZero() || request.OccurredAt.Location() != time.UTC {
		return mission.DecompositionOperationReservation{}, decompositionStoreError(mission.DecompositionInvalidRequest, "operation", "decomposition authority is invalid", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return mission.DecompositionOperationReservation{}, decompositionStoreError(mission.DecompositionProviderFailure, "store", "decomposition authority is unavailable", err)
	}
	defer tx.Rollback()
	if replay, found, err := loadDecompositionReservation(ctx, tx, request); err != nil {
		return mission.DecompositionOperationReservation{}, err
	} else if found {
		return replay, nil
	}
	if err := insertPhase3Operation(ctx, tx, request.OperationID.String(), decompositionOperationKind, request.MissionID.String(), request.RequestDigest, []byte(`{"schema_version":1,"reserved":true}`), request.OccurredAt); err != nil {
		return mission.DecompositionOperationReservation{}, decompositionStoreError(mission.DecompositionOperationConflict, "operation_id", "operation identity is already reserved", err)
	}
	if err := tx.Commit(); err != nil {
		return mission.DecompositionOperationReservation{}, decompositionStoreError(mission.DecompositionProviderFailure, "store", "decomposition authority could not be committed", err)
	}
	return mission.DecompositionOperationReservation{}, nil
}

func loadDecompositionReservation(ctx context.Context, q rowQuery, request mission.DecompositionOperationRequest) (mission.DecompositionOperationReservation, bool, error) {
	var kind, missionID, digest string
	err := q.QueryRowContext(ctx, `SELECT operation_kind,mission_id,request_digest FROM phase3_operations WHERE operation_id=?`, request.OperationID).Scan(&kind, &missionID, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return mission.DecompositionOperationReservation{}, false, nil
	}
	if err != nil {
		return mission.DecompositionOperationReservation{}, false, decompositionStoreError(mission.DecompositionProviderFailure, "store", "decomposition authority is unavailable", err)
	}
	if kind != decompositionOperationKind || missionID != request.MissionID.String() || digest != request.RequestDigest {
		return mission.DecompositionOperationReservation{}, false, decompositionStoreError(mission.DecompositionOperationConflict, "operation_id", "operation identity was reused", nil)
	}
	return mission.DecompositionOperationReservation{Replayed: true}, true, nil
}

func decompositionStoreError(kind mission.DecompositionErrorKind, field, message string, cause error) error {
	recovery := mission.DecompositionRetrySameOperation
	if kind == mission.DecompositionInvalidRequest || kind == mission.DecompositionOperationConflict {
		recovery = mission.DecompositionCorrectInput
	}
	return &mission.DecompositionError{Kind: kind, Recovery: recovery, Field: field, Message: message, Cause: cause}
}

func lowerHex(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) }) == -1
}
