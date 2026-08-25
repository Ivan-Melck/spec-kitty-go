package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func (s *Store) SaveExternalEffect(ctx context.Context, effect execution.ExternalEffect) error {
	if effect.ObservedAt.IsZero() {
		effect.ObservedAt = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin external-effect observation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO external_effects(
operation_id,schema_version,correlation_id,actor_id,process_id,provider,work_key,marker,prior_revision,prior_value,applied_revision,applied_value,response_status,compensation_status,compensation_revision,conflict_revision,conflict_value,conflict_marker,observed_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(operation_id) DO UPDATE SET
applied_revision=excluded.applied_revision,
response_status=excluded.response_status,compensation_status=excluded.compensation_status,compensation_revision=excluded.compensation_revision,
conflict_revision=excluded.conflict_revision,conflict_value=excluded.conflict_value,conflict_marker=excluded.conflict_marker,observed_at=excluded.observed_at
WHERE external_effects.schema_version=excluded.schema_version AND external_effects.correlation_id=excluded.correlation_id
  AND external_effects.actor_id=excluded.actor_id AND external_effects.process_id=excluded.process_id
  AND external_effects.provider=excluded.provider AND external_effects.work_key=excluded.work_key
  AND external_effects.marker=excluded.marker AND external_effects.prior_revision=excluded.prior_revision
  AND external_effects.prior_value=excluded.prior_value AND external_effects.applied_value=excluded.applied_value`,
		effect.OperationID, effect.SchemaVersion, effect.CorrelationID, effect.ActorID, effect.ProcessID, effect.Provider, effect.WorkKey, effect.Marker, effect.PriorRevision, effect.PriorValue,
		effect.AppliedRevision, effect.AppliedValue, effect.ResponseStatus, effect.CompensationStatus, effect.CompensationRevision,
		effect.ConflictRevision, effect.ConflictValue, effect.ConflictMarker, effect.ObservedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save external effect: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("external-effect affected rows: %w", err)
	}
	if changed != 1 {
		return execution.NewDiagnostic(execution.KindIdempotencyConflict, "operation ID was reused for a different external effect", execution.RetryNever,
			effect.CorrelationID, effect.OperationID, map[string]string{"operation_id": effect.OperationID.String(), "actor_id": effect.ActorID.String(), "process_id": effect.ProcessID.String(), "provider": effect.Provider, "provider_work_key": effect.WorkKey, "expected_provider_revision": fmt.Sprint(effect.PriorRevision)},
			map[string]string{"external_effect": "different request"}, map[string]string{"external_effect": "same immutable request"}, effect.ObservedAt)
	}
	providerRevision, providerValue, providerMarker := effect.AppliedRevision, effect.AppliedValue, effect.Marker
	switch {
	case effect.CompensationStatus == execution.CompensationPending && (effect.ResponseStatus == execution.EffectAmbiguous || effect.ResponseStatus == execution.EffectUnavailable):
		providerRevision, providerValue, providerMarker = 0, "", ""
	case effect.CompensationStatus == execution.CompensationDone:
		providerRevision, providerValue, providerMarker = effect.CompensationRevision, effect.PriorValue, ""
		if effect.ObservationRevision != 0 {
			providerRevision, providerValue, providerMarker = effect.ObservationRevision, effect.ObservationValue, effect.ObservationMarker
		}
	case effect.CompensationStatus == execution.CompensationAmbiguous || effect.CompensationStatus == execution.CompensationUnavailable:
		providerRevision, providerValue, providerMarker = 0, "", ""
	case effect.ConflictRevision != 0:
		providerRevision, providerValue, providerMarker = effect.ConflictRevision, effect.ConflictValue, effect.ConflictMarker
	case effect.AppliedRevision == 0:
		providerRevision, providerValue, providerMarker = effect.PriorRevision, effect.PriorValue, ""
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO external_effect_observations(operation_id,response_status,compensation_status,provider_revision,provider_value,provider_marker,observed_at) VALUES(?,?,?,?,?,?,?)`,
		effect.OperationID, effect.ResponseStatus, effect.CompensationStatus, providerRevision, providerValue, providerMarker, effect.ObservedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("append external-effect observation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit external-effect observation: %w", err)
	}
	return nil
}

func (s *Store) ExternalEffect(ctx context.Context, operationID ids.OperationID) (execution.ExternalEffect, error) {
	var effect execution.ExternalEffect
	var observedAt string
	var priorRevision, appliedRevision, compensationRevision, conflictRevision uint64
	err := s.db.QueryRowContext(ctx, `SELECT operation_id,schema_version,correlation_id,actor_id,process_id,provider,work_key,marker,prior_revision,prior_value,applied_revision,applied_value,response_status,compensation_status,compensation_revision,conflict_revision,conflict_value,conflict_marker,observed_at FROM external_effects WHERE operation_id=?`, operationID).Scan(
		&effect.OperationID, &effect.SchemaVersion, &effect.CorrelationID, &effect.ActorID, &effect.ProcessID, &effect.Provider, &effect.WorkKey, &effect.Marker, &priorRevision, &effect.PriorValue,
		&appliedRevision, &effect.AppliedValue, &effect.ResponseStatus, &effect.CompensationStatus, &compensationRevision,
		&conflictRevision, &effect.ConflictValue, &effect.ConflictMarker, &observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return execution.ExternalEffect{}, execution.NewDiagnostic(execution.KindNotFound, "external effect was not found", execution.RetryNever, operationID.String(), operationID,
			map[string]string{"operation_id": operationID.String()}, map[string]string{"external_effect": "missing"}, map[string]string{"external_effect": "recorded"}, time.Now().UTC())
	}
	if err != nil {
		return execution.ExternalEffect{}, fmt.Errorf("read external effect: %w", err)
	}
	effect.PriorRevision = tracker.Revision(priorRevision)
	effect.AppliedRevision = tracker.Revision(appliedRevision)
	effect.CompensationRevision = tracker.Revision(compensationRevision)
	effect.ConflictRevision = tracker.Revision(conflictRevision)
	effect.ObservedAt, err = time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return execution.ExternalEffect{}, fmt.Errorf("parse external-effect observed time: %w", err)
	}
	return effect, nil
}
