package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const (
	KindTrackerUnavailable          ErrorKind = "tracker_unavailable"
	KindExternalAttributionLost     ErrorKind = "external_attribution_lost"
	KindDecisionRequired            ErrorKind = "decision_required"
	KindSafeCompensationUnsupported ErrorKind = "safe_compensation_unsupported"
)

type EffectResponseStatus string

const (
	EffectPending          EffectResponseStatus = "pending"
	EffectConfirmed        EffectResponseStatus = "confirmed"
	EffectAmbiguous        EffectResponseStatus = "ambiguous"
	EffectUnavailable      EffectResponseStatus = "unavailable"
	EffectDecisionRequired EffectResponseStatus = "decision_required"
)

type CompensationStatus string

const (
	CompensationPending         CompensationStatus = "pending"
	CompensationAmbiguous       CompensationStatus = "ambiguous"
	CompensationUnavailable     CompensationStatus = "unavailable"
	CompensationDone            CompensationStatus = "compensated"
	CompensationAttributionLost CompensationStatus = "attribution_lost"
	CompensationUnsupported     CompensationStatus = "safe_compensation_unsupported"
	CompensationReconciliation  CompensationStatus = "reconciliation_required"
)

type ExternalEffect struct {
	SchemaVersion        uint16
	OperationID          ids.OperationID
	CorrelationID        string
	ActorID              ids.ActorID
	ProcessID            ids.ProcessID
	Provider             string
	WorkKey              string
	Marker               string
	PriorRevision        tracker.Revision
	PriorValue           string
	AppliedRevision      tracker.Revision
	AppliedValue         string
	ResponseStatus       EffectResponseStatus
	CompensationStatus   CompensationStatus
	CompensationRevision tracker.Revision
	ConflictRevision     tracker.Revision
	ConflictValue        string
	ConflictMarker       string
	ObservationRevision  tracker.Revision
	ObservationValue     string
	ObservationMarker    string
	ObservedAt           time.Time
}

type ExternalEffectRepository interface {
	SaveExternalEffect(context.Context, ExternalEffect) error
	ExternalEffect(context.Context, ids.OperationID) (ExternalEffect, error)
}

type TrackerAssignRequest struct {
	SchemaVersion     uint16
	CorrelationID     string
	OperationID       ids.OperationID
	ActorID           ids.ActorID
	ProcessID         ids.ProcessID
	Now               time.Time
	Provider          string
	WorkKey           string
	ExpectedRevision  tracker.Revision
	Assignee          string
	ExternalRef       tracker.ExternalWorkRef
	RequiredGuarantee tracker.AssignmentLevel
}

type TrackerCompensateRequest struct {
	SchemaVersion    uint16
	CorrelationID    string
	OperationID      ids.OperationID
	ActorID          ids.ActorID
	ProcessID        ids.ProcessID
	Now              time.Time
	Provider         string
	WorkKey          string
	ExpectedRevision tracker.Revision
	ExternalRef      tracker.ExternalWorkRef
}

type TrackerEffectResult struct {
	SchemaVersion   uint16            `json:"schema_version"`
	Kind            string            `json:"kind"`
	CorrelationID   string            `json:"correlation_id"`
	OperationID     ids.OperationID   `json:"operation_id"`
	AggregateIDs    map[string]string `json:"aggregate_ids"`
	ObservedState   map[string]string `json:"observed_state"`
	ExpectedState   map[string]string `json:"expected_state"`
	RetrySafety     RetrySafety       `json:"retry_safety"`
	RecoveryActions []string          `json:"recovery_actions"`
	AppliedRevision tracker.Revision  `json:"applied_revision"`
	Assignee        string            `json:"assignee"`
	Replayed        bool              `json:"replayed"`
	Noop            bool              `json:"noop"`
	Revision        tracker.Revision  `json:"revision"`
}

type TrackerSaga struct {
	repository ExternalEffectRepository
	provider   legacyAssignmentProvider
	projection tracker.AssignmentProjection
	faults     faults.Hook
}

type legacyAssignmentProvider interface {
	Read(context.Context, string) (tracker.Work, error)
	CompareAndSetAssignment(context.Context, tracker.AssignmentRequest) (tracker.Mutation, error)
	CompareBeforeCompensate(context.Context, tracker.CompensationRequest) (tracker.Mutation, error)
}

func NewTrackerSaga(repository ExternalEffectRepository, provider any, _ Clock) *TrackerSaga {
	legacy, _ := provider.(legacyAssignmentProvider)
	projection, _ := provider.(tracker.AssignmentProjection)
	return &TrackerSaga{repository: repository, provider: legacy, projection: projection, faults: faults.Noop{}}
}

func (s *TrackerSaga) WithFaults(hook faults.Hook) *TrackerSaga {
	if hook == nil {
		hook = faults.Noop{}
	}
	s.faults = hook
	return s
}

func (s *TrackerSaga) assignProjection(ctx context.Context, request TrackerAssignRequest) (TrackerEffectResult, error) {
	envelope := assignEnvelope(request)
	if s.projection == nil {
		return TrackerEffectResult{}, commandDiagnostic(KindInvalidCommand, "provider does not implement assignment projection", RetryNever, envelope, nil, request.Now)
	}
	effect, err := s.readEffect(ctx, envelope)
	switch {
	case err == nil:
		if effect.Provider != envelope.Provider || effect.WorkKey != envelope.WorkKey || effect.AppliedValue != request.Assignee || effect.PriorRevision != request.ExpectedRevision || effect.Marker != request.OperationID.String() {
			return TrackerEffectResult{}, commandDiagnostic(KindIdempotencyConflict, "operation ID was reused for a different tracker assignment", RetryNever, envelope, &effect, request.Now)
		}
		if effect.ResponseStatus == EffectConfirmed {
			return effectResult(effect, envelope, true, true), nil
		}
	case IsKind(err, KindNotFound):
		observed, observeErr := s.projection.ObserveAssignment(ctx, request.ExternalRef)
		if observeErr != nil {
			return TrackerEffectResult{}, s.providerFailure(ctx, envelope, nil, observeErr)
		}
		if observed.Observation.Revision != request.ExpectedRevision {
			return TrackerEffectResult{}, assignmentDriftDiagnostic(envelope, nil, observed, request.Now)
		}
		effect = ExternalEffect{
			SchemaVersion: ResultSchemaVersion, OperationID: request.OperationID, CorrelationID: request.CorrelationID,
			ActorID: request.ActorID, ProcessID: request.ProcessID, Provider: envelope.Provider, WorkKey: envelope.WorkKey,
			Marker: request.OperationID.String(), PriorRevision: observed.Observation.Revision, PriorValue: observed.Assignee,
			AppliedValue: request.Assignee, ResponseStatus: EffectPending, CompensationStatus: CompensationPending,
			ObservationRevision: observed.Observation.Revision, ObservationValue: observed.Assignee, ObservationMarker: observed.OperationMarker,
			ObservedAt: observed.Observation.ObservedAt,
		}
		if err := s.saveEffect(ctx, envelope, effect); err != nil {
			return TrackerEffectResult{}, err
		}
	case err != nil:
		return TrackerEffectResult{}, err
	}

	mutation, mutationErr := s.projection.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{
		ExternalRef: request.ExternalRef, OperationID: request.OperationID, Assignee: request.Assignee,
		ExpectedObservation: effect.PriorRevision, RequiredGuarantee: request.RequiredGuarantee,
	})
	actual := mutation.Observation
	if mutationErr == nil && actual.Observation.Revision != 0 && actual.Assignee == request.Assignee && actual.OperationMarker == effect.Marker {
		effect.ResponseStatus = EffectConfirmed
		effect.AppliedRevision = actual.Observation.Revision
		setEffectObservation(&effect, actual)
		if err := s.saveEffect(ctx, envelope, effect); err != nil {
			return TrackerEffectResult{}, err
		}
		return effectResult(effect, envelope, mutation.Replayed, mutation.Noop), nil
	}
	if tracker.IsKind(mutationErr, tracker.ErrorAmbiguous) {
		effect.ResponseStatus = EffectAmbiguous
		clearEffectObservation(&effect, request.Now)
	} else if tracker.IsKind(mutationErr, tracker.ErrorUnavailable) || tracker.IsKind(mutationErr, tracker.ErrorTimeout) {
		effect.ResponseStatus = EffectUnavailable
		clearEffectObservation(&effect, request.Now)
	} else if actual.Observation.Revision != 0 {
		effect.ResponseStatus = EffectDecisionRequired
		effect.ConflictRevision, effect.ConflictValue, effect.ConflictMarker = actual.Observation.Revision, actual.Assignee, actual.OperationMarker
		setEffectObservation(&effect, actual)
	}
	if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
		return TrackerEffectResult{}, saveErr
	}
	if mutationErr == nil {
		mutationErr = &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "assignment post-observation did not prove the complete effect"}
	}
	return TrackerEffectResult{}, s.providerFailure(ctx, envelope, &effect, mutationErr)
}

func (s *TrackerSaga) compensateProjection(ctx context.Context, request TrackerCompensateRequest) (TrackerEffectResult, error) {
	envelope := compensateEnvelope(request)
	if s.projection == nil {
		return TrackerEffectResult{}, commandDiagnostic(KindInvalidCommand, "provider does not implement assignment projection", RetryNever, envelope, nil, request.Now)
	}
	effect, err := s.readEffect(ctx, envelope)
	if err != nil {
		if IsKind(err, KindNotFound) {
			return TrackerEffectResult{}, commandDiagnostic(KindNotFound, "external effect was not found", RetryNever, envelope, nil, request.Now)
		}
		return TrackerEffectResult{}, err
	}
	if effect.Provider != envelope.Provider || effect.WorkKey != envelope.WorkKey || effect.AppliedRevision != request.ExpectedRevision {
		return TrackerEffectResult{}, commandDiagnostic(KindIdempotencyConflict, "compensation command does not match the recorded external effect", RetryNever, envelope, &effect, request.Now)
	}
	if effect.ResponseStatus != EffectConfirmed {
		return TrackerEffectResult{}, commandDiagnostic(KindDecisionRequired, "only a confirmed attributable effect can be compensated", RetryDecisionRequired, envelope, &effect, request.Now)
	}
	if effect.CompensationStatus == CompensationDone {
		return compensationResult(effect, envelope, true, true), nil
	}
	if effect.CompensationStatus == CompensationUnsupported || effect.CompensationStatus == CompensationAttributionLost || effect.CompensationStatus == CompensationReconciliation {
		return TrackerEffectResult{}, compensationDecisionDiagnostic(envelope, effect, request.Now)
	}

	mutation, mutationErr := s.projection.CompensateAssignment(ctx, tracker.CompensateAssignmentRequest{
		ExternalRef: request.ExternalRef, OperationID: request.OperationID, ExpectedObservation: effect.AppliedRevision,
		ExpectedAssignee: effect.AppliedValue, ExpectedMarker: effect.Marker, RestoreAssignee: effect.PriorValue,
	})
	actual := mutation.Observation
	switch {
	case mutationErr == nil && actual.Observation.Revision != 0:
		effect.CompensationStatus = CompensationDone
		effect.CompensationRevision = actual.Observation.Revision
		setEffectObservation(&effect, actual)
		if err := s.saveEffect(ctx, envelope, effect); err != nil {
			return TrackerEffectResult{}, err
		}
		return compensationResult(effect, envelope, mutation.Replayed, mutation.Noop), nil
	case tracker.IsKind(mutationErr, tracker.ErrorSafeCompensationUnsupported):
		effect.CompensationStatus = CompensationUnsupported
		setEffectObservation(&effect, actual)
	case tracker.IsKind(mutationErr, tracker.ErrorAttributionLost):
		effect.CompensationStatus = CompensationAttributionLost
		effect.ConflictRevision, effect.ConflictValue, effect.ConflictMarker = actual.Observation.Revision, actual.Assignee, actual.OperationMarker
		setEffectObservation(&effect, actual)
	case tracker.IsKind(mutationErr, tracker.ErrorAmbiguous):
		effect.CompensationStatus = CompensationAmbiguous
		clearEffectObservation(&effect, request.Now)
	case tracker.IsKind(mutationErr, tracker.ErrorUnavailable), tracker.IsKind(mutationErr, tracker.ErrorTimeout):
		effect.CompensationStatus = CompensationUnavailable
		clearEffectObservation(&effect, request.Now)
	default:
		effect.CompensationStatus = CompensationReconciliation
		if actual.Observation.Revision != 0 {
			setEffectObservation(&effect, actual)
		}
	}
	if err := s.saveEffect(ctx, envelope, effect); err != nil {
		return TrackerEffectResult{}, err
	}
	if effect.CompensationStatus == CompensationUnsupported || effect.CompensationStatus == CompensationAttributionLost || effect.CompensationStatus == CompensationReconciliation {
		diagnostic := compensationDecisionDiagnostic(envelope, effect, request.Now)
		diagnostic.Cause = mutationErr
		return TrackerEffectResult{}, diagnostic
	}
	return TrackerEffectResult{}, s.providerFailure(ctx, envelope, &effect, mutationErr)
}

func setEffectObservation(effect *ExternalEffect, actual tracker.AssignmentObservation) {
	effect.ObservationRevision = actual.Observation.Revision
	effect.ObservationValue = actual.Assignee
	effect.ObservationMarker = actual.OperationMarker
	effect.ObservedAt = actual.Observation.ObservedAt.UTC()
}

func clearEffectObservation(effect *ExternalEffect, now time.Time) {
	effect.ObservationRevision, effect.ObservationValue, effect.ObservationMarker = 0, "", ""
	effect.ObservedAt = now.UTC()
}

func assignmentDriftDiagnostic(envelope trackerEnvelope, effect *ExternalEffect, actual tracker.AssignmentObservation, now time.Time) *DiagnosticError {
	diagnostic := commandDiagnostic(KindDecisionRequired, "provider observation changed before assignment", RetryDecisionRequired, envelope, effect, now)
	diagnostic.ObservedState = map[string]string{"provider_revision": fmt.Sprint(actual.Observation.Revision), "assignee": actual.Assignee, "operation_marker": actual.OperationMarker}
	diagnostic.ExpectedState = map[string]string{"provider_revision": fmt.Sprint(envelope.ExpectedRevision)}
	diagnostic.RecoveryActions = []string{"preserve the current provider state", "reconcile the mapping before issuing a new operation"}
	return diagnostic
}

func compensationDecisionDiagnostic(envelope trackerEnvelope, effect ExternalEffect, now time.Time) *DiagnosticError {
	kind, message := KindDecisionRequired, "external assignment requires explicit reconciliation"
	if effect.CompensationStatus == CompensationUnsupported {
		kind, message = KindSafeCompensationUnsupported, "provider cannot enforce safe automatic compensation"
	} else if effect.CompensationStatus == CompensationAttributionLost {
		kind, message = KindExternalAttributionLost, "current tracker assignment is no longer attributable to this operation"
	}
	diagnostic := commandDiagnostic(kind, message, RetryNever, envelope, &effect, now)
	diagnostic.ObservedState = map[string]string{"provider_revision": fmt.Sprint(effect.ObservationRevision), "assignee": effect.ObservationValue, "operation_marker": effect.ObservationMarker, "compensation_status": string(effect.CompensationStatus)}
	diagnostic.ExpectedState = map[string]string{"provider_revision": fmt.Sprint(effect.AppliedRevision), "assignee": effect.AppliedValue, "operation_marker": effect.Marker}
	diagnostic.RecoveryActions = []string{"preserve the current provider assignment", "record an explicit reconciliation decision"}
	return diagnostic
}

func (s *TrackerSaga) Assign(ctx context.Context, request TrackerAssignRequest) (TrackerEffectResult, error) {
	if err := validateAssignRequest(request); err != nil {
		return TrackerEffectResult{}, err
	}
	if request.ExternalRef.ExternalID != "" {
		return s.assignProjection(ctx, request)
	}
	if s.provider == nil {
		return TrackerEffectResult{}, commandDiagnostic(KindInvalidCommand, "tracker assignment projection is unavailable", RetryNever, assignEnvelope(request), nil, request.Now)
	}
	envelope := assignEnvelope(request)
	effect, err := s.readEffect(ctx, envelope)
	switch {
	case err == nil:
		if effect.Provider != request.Provider || effect.WorkKey != request.WorkKey || effect.AppliedValue != request.Assignee || effect.PriorRevision != request.ExpectedRevision {
			return TrackerEffectResult{}, commandDiagnostic(KindIdempotencyConflict, "operation ID was reused for a different tracker assignment", RetryNever, envelope, &effect, request.Now)
		}
		if effect.ResponseStatus == EffectConfirmed {
			return effectResult(effect, envelope, true, false), nil
		}
		if effect.PriorRevision != 0 {
			return s.observeThenContinue(ctx, request, effect)
		}
	case IsKind(err, KindNotFound):
		// A new operation establishes immutable prior evidence before calling the provider.
	case err != nil:
		return TrackerEffectResult{}, err
	}

	work, err := s.provider.Read(ctx, request.WorkKey)
	if err != nil {
		return TrackerEffectResult{}, s.providerFailure(ctx, envelope, nil, err)
	}
	if work.Revision != request.ExpectedRevision {
		diagnostic := commandDiagnostic(KindDecisionRequired, "provider revision does not match the command envelope", RetryDecisionRequired, envelope, nil, request.Now)
		diagnostic.ObservedState = map[string]string{"provider_revision": fmt.Sprint(work.Revision), "assignee": work.Assignee}
		diagnostic.ExpectedState = map[string]string{"provider_revision": fmt.Sprint(request.ExpectedRevision)}
		diagnostic.RecoveryActions = []string{"inspect the current provider revision", "issue a new operation only after resolving the revision change"}
		return TrackerEffectResult{}, diagnostic
	}
	effect = ExternalEffect{
		SchemaVersion: ResultSchemaVersion, OperationID: request.OperationID, CorrelationID: request.CorrelationID,
		ActorID: request.ActorID, ProcessID: request.ProcessID, Provider: request.Provider, WorkKey: request.WorkKey,
		Marker: request.OperationID.String(), PriorRevision: work.Revision, PriorValue: work.Assignee,
		AppliedValue: request.Assignee, ResponseStatus: EffectPending, CompensationStatus: CompensationPending,
		ObservedAt: request.Now.UTC(),
	}
	if err := s.saveEffect(ctx, envelope, effect); err != nil {
		return TrackerEffectResult{}, err
	}
	return s.applyAssignment(ctx, request, effect)
}

func (s *TrackerSaga) observeThenContinue(ctx context.Context, request TrackerAssignRequest, effect ExternalEffect) (TrackerEffectResult, error) {
	envelope := assignEnvelope(request)
	work, err := s.provider.Read(ctx, request.WorkKey)
	if err != nil {
		return TrackerEffectResult{}, s.providerFailure(ctx, envelope, &effect, err)
	}
	if work.AssignmentMarker == effect.Marker && work.Assignee == effect.AppliedValue {
		effect.ResponseStatus = EffectConfirmed
		effect.AppliedRevision = work.Revision
		effect.ObservedAt = request.Now.UTC()
		if err := s.saveEffect(ctx, envelope, effect); err != nil {
			return TrackerEffectResult{}, err
		}
		return effectResult(effect, envelope, true, false), nil
	}
	if work.Revision == effect.PriorRevision && work.Assignee == effect.PriorValue {
		result, err := s.applyAssignment(ctx, request, effect)
		if err == nil {
			result.Replayed = true
		}
		return result, err
	}
	effect.ResponseStatus = EffectDecisionRequired
	effect.ConflictRevision = work.Revision
	effect.ConflictValue = work.Assignee
	effect.ConflictMarker = work.AssignmentMarker
	effect.ObservedAt = request.Now.UTC()
	if err := s.saveEffect(ctx, envelope, effect); err != nil {
		return TrackerEffectResult{}, err
	}
	diagnostic := commandDiagnostic(KindDecisionRequired, "external assignment cannot be attributed or safely retried", RetryDecisionRequired, envelope, &effect, request.Now)
	diagnostic.ObservedState = map[string]string{"provider_revision": fmt.Sprint(work.Revision), "assignee": work.Assignee, "operation_marker": work.AssignmentMarker}
	diagnostic.ExpectedState = map[string]string{"provider_revision": fmt.Sprint(effect.PriorRevision), "assignee": effect.PriorValue, "operation_marker": effect.Marker}
	diagnostic.RecoveryActions = []string{"inspect tracker history for the operation marker", "choose explicitly whether to preserve or replace the current assignment"}
	return TrackerEffectResult{}, diagnostic
}

func (s *TrackerSaga) applyAssignment(ctx context.Context, request TrackerAssignRequest, effect ExternalEffect) (TrackerEffectResult, error) {
	envelope := assignEnvelope(request)
	if err := s.faults.Reach(ctx, "SR-03"); err != nil {
		return TrackerEffectResult{}, err
	}
	mutation, err := s.provider.CompareAndSetAssignment(ctx, tracker.AssignmentRequest{
		WorkKey: request.WorkKey, ExpectedRevision: effect.PriorRevision, Assignee: effect.AppliedValue, Marker: effect.Marker,
	})
	if mutation.Revision != 0 {
		if faultErr := s.faults.Reach(ctx, "SR-04"); faultErr != nil {
			return TrackerEffectResult{}, faultErr
		}
	}
	if err == nil {
		effect.ResponseStatus = EffectConfirmed
		effect.AppliedRevision = mutation.Revision
		effect.ObservedAt = request.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
			return TrackerEffectResult{}, saveErr
		}
		return effectResult(effect, envelope, mutation.Replayed, false), nil
	}
	if tracker.IsKind(err, tracker.ErrorAmbiguous) {
		effect.ResponseStatus = EffectAmbiguous
		effect.ObservedAt = request.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
			return TrackerEffectResult{}, saveErr
		}
	}
	return TrackerEffectResult{}, s.providerFailure(ctx, envelope, &effect, err)
}

func (s *TrackerSaga) Compensate(ctx context.Context, request TrackerCompensateRequest) (TrackerEffectResult, error) {
	if err := validateCompensateRequest(request); err != nil {
		return TrackerEffectResult{}, err
	}
	if request.ExternalRef.ExternalID != "" {
		return s.compensateProjection(ctx, request)
	}
	if s.provider == nil {
		return TrackerEffectResult{}, commandDiagnostic(KindInvalidCommand, "tracker assignment projection is unavailable", RetryNever, compensateEnvelope(request), nil, request.Now)
	}
	envelope := compensateEnvelope(request)
	effect, err := s.readEffect(ctx, envelope)
	if err != nil {
		if IsKind(err, KindNotFound) {
			return TrackerEffectResult{}, commandDiagnostic(KindNotFound, "external effect was not found", RetryNever, envelope, nil, request.Now)
		}
		return TrackerEffectResult{}, err
	}
	if effect.Provider != request.Provider || effect.WorkKey != request.WorkKey || effect.AppliedRevision != request.ExpectedRevision {
		return TrackerEffectResult{}, commandDiagnostic(KindIdempotencyConflict, "compensation command does not match the recorded external effect", RetryNever, envelope, &effect, request.Now)
	}
	if effect.ResponseStatus != EffectConfirmed {
		return TrackerEffectResult{}, commandDiagnostic(KindDecisionRequired, "only a confirmed attributable effect can be compensated", RetryDecisionRequired, envelope, &effect, request.Now)
	}
	if effect.CompensationStatus == CompensationDone {
		return compensationResult(effect, envelope, true, true), nil
	}
	mutation, err := s.provider.CompareBeforeCompensate(ctx, tracker.CompensationRequest{
		WorkKey: effect.WorkKey, ExpectedRevision: effect.AppliedRevision, ExpectedAssignee: effect.AppliedValue,
		Marker: effect.Marker, RestoreAssignee: effect.PriorValue,
	})
	if err == nil {
		effect.CompensationStatus = CompensationDone
		effect.CompensationRevision = mutation.Revision
		effect.ObservationRevision = mutation.Revision
		effect.ObservationValue = mutation.Assignee
		effect.ObservationMarker = mutation.Marker
		effect.ObservedAt = request.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
			return TrackerEffectResult{}, saveErr
		}
		return compensationResult(effect, envelope, mutation.Replayed, mutation.Noop), nil
	}
	if tracker.IsKind(err, tracker.ErrorAttributionLost) {
		work, readErr := s.provider.Read(ctx, effect.WorkKey)
		if readErr != nil {
			return TrackerEffectResult{}, s.providerFailure(ctx, envelope, nil, readErr)
		}
		effect.CompensationStatus = CompensationAttributionLost
		effect.ConflictRevision = work.Revision
		effect.ConflictValue = work.Assignee
		effect.ConflictMarker = work.AssignmentMarker
		effect.ObservationRevision = work.Revision
		effect.ObservationValue = work.Assignee
		effect.ObservationMarker = work.AssignmentMarker
		effect.ObservedAt = request.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
			return TrackerEffectResult{}, saveErr
		}
		diagnostic := commandDiagnostic(KindExternalAttributionLost, "current tracker state is not attributable to this operation", RetryDecisionRequired, envelope, &effect, request.Now)
		diagnostic.ObservedState = map[string]string{"provider_revision": fmt.Sprint(work.Revision), "assignee": work.Assignee, "operation_marker": work.AssignmentMarker}
		diagnostic.ExpectedState = map[string]string{"provider_revision": fmt.Sprint(effect.AppliedRevision), "assignee": effect.AppliedValue, "operation_marker": effect.Marker}
		diagnostic.RecoveryActions = []string{"preserve the current tracker assignment", "ask a human whether a new explicit operation should change it"}
		diagnostic.Cause = err
		return TrackerEffectResult{}, diagnostic
	}
	if tracker.IsKind(err, tracker.ErrorAmbiguous) {
		effect.CompensationStatus = CompensationAmbiguous
		effect.ObservedAt = request.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
			return TrackerEffectResult{}, saveErr
		}
	}
	if tracker.IsKind(err, tracker.ErrorUnavailable) {
		effect.CompensationStatus = CompensationUnavailable
		effect.ObservedAt = request.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, effect); saveErr != nil {
			return TrackerEffectResult{}, saveErr
		}
	}
	return TrackerEffectResult{}, s.providerFailure(ctx, envelope, nil, err)
}

type trackerEnvelope struct {
	SchemaVersion    uint16
	CorrelationID    string
	OperationID      ids.OperationID
	ActorID          ids.ActorID
	ProcessID        ids.ProcessID
	Now              time.Time
	Provider         string
	WorkKey          string
	ExpectedRevision tracker.Revision
}

func assignEnvelope(request TrackerAssignRequest) trackerEnvelope {
	provider, workKey := request.Provider, request.WorkKey
	if request.ExternalRef.ExternalID != "" {
		provider, workKey = request.ExternalRef.Provider, request.ExternalRef.ExternalID
	}
	return trackerEnvelope{request.SchemaVersion, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.Now, provider, workKey, request.ExpectedRevision}
}

func compensateEnvelope(request TrackerCompensateRequest) trackerEnvelope {
	provider, workKey := request.Provider, request.WorkKey
	if request.ExternalRef.ExternalID != "" {
		provider, workKey = request.ExternalRef.Provider, request.ExternalRef.ExternalID
	}
	return trackerEnvelope{request.SchemaVersion, request.CorrelationID, request.OperationID, request.ActorID, request.ProcessID, request.Now, provider, workKey, request.ExpectedRevision}
}

func validateAssignRequest(request TrackerAssignRequest) error {
	if err := validateEnvelope(assignEnvelope(request)); err != nil {
		return err
	}
	if request.Assignee == "" {
		return commandDiagnostic(KindInvalidCommand, "assignee is required", RetryNever, assignEnvelope(request), nil, request.Now)
	}
	if request.ExternalRef.ExternalID != "" {
		if err := request.ExternalRef.Validate(); err != nil {
			return commandDiagnostic(KindInvalidCommand, "external work mapping is invalid", RetryNever, assignEnvelope(request), nil, request.Now)
		}
		if request.Provider != "" && request.Provider != request.ExternalRef.Provider || request.WorkKey != "" && request.WorkKey != request.ExternalRef.ExternalID {
			return commandDiagnostic(KindInvalidCommand, "external work mapping conflicts with provider envelope", RetryNever, assignEnvelope(request), nil, request.Now)
		}
		if request.RequiredGuarantee != tracker.AssignmentUnguarded && request.RequiredGuarantee != tracker.AssignmentAtomicClaim {
			return commandDiagnostic(KindInvalidCommand, "assignment guarantee is required", RetryNever, assignEnvelope(request), nil, request.Now)
		}
	}
	return nil
}

func validateCompensateRequest(request TrackerCompensateRequest) error {
	if err := validateEnvelope(compensateEnvelope(request)); err != nil {
		return err
	}
	if request.ExternalRef.ExternalID != "" {
		if err := request.ExternalRef.Validate(); err != nil {
			return commandDiagnostic(KindInvalidCommand, "external work mapping is invalid", RetryNever, compensateEnvelope(request), nil, request.Now)
		}
		if request.Provider != "" && request.Provider != request.ExternalRef.Provider || request.WorkKey != "" && request.WorkKey != request.ExternalRef.ExternalID {
			return commandDiagnostic(KindInvalidCommand, "external work mapping conflicts with provider envelope", RetryNever, compensateEnvelope(request), nil, request.Now)
		}
	}
	return nil
}

func validateEnvelope(envelope trackerEnvelope) error {
	invalid := ""
	_, operationErr := ids.ParseOperationID(envelope.OperationID.String())
	_, actorErr := ids.ParseActorID(envelope.ActorID.String())
	_, processErr := ids.ParseProcessID(envelope.ProcessID.String())
	switch {
	case envelope.SchemaVersion != ResultSchemaVersion:
		invalid = "schema_version"
	case envelope.CorrelationID == "":
		invalid = "correlation_id"
	case operationErr != nil:
		invalid = "operation_id"
	case actorErr != nil:
		invalid = "actor_id"
	case processErr != nil:
		invalid = "process_id"
	case envelope.Now.IsZero():
		invalid = "timestamp"
	case envelope.Provider == "":
		invalid = "provider"
	case envelope.WorkKey == "":
		invalid = "provider_work_key"
	case envelope.ExpectedRevision == 0:
		invalid = "expected_provider_revision"
	}
	if invalid == "" {
		return nil
	}
	diagnostic := commandDiagnostic(KindInvalidCommand, invalid+" is invalid", RetryNever, envelope, nil, envelope.Now)
	diagnostic.ObservedState = map[string]string{invalid: "invalid"}
	diagnostic.ExpectedState = map[string]string{invalid: "valid command-envelope value"}
	return diagnostic
}

func (s *TrackerSaga) readEffect(ctx context.Context, envelope trackerEnvelope) (ExternalEffect, error) {
	effect, err := s.repository.ExternalEffect(ctx, envelope.OperationID)
	if err == nil || IsKind(err, KindNotFound) {
		return effect, err
	}
	return ExternalEffect{}, repositoryDiagnostic("read", envelope, nil, err)
}

func (s *TrackerSaga) saveEffect(ctx context.Context, envelope trackerEnvelope, effect ExternalEffect) error {
	if err := s.repository.SaveExternalEffect(ctx, effect); err != nil {
		return repositoryDiagnostic("save", envelope, &effect, err)
	}
	return nil
}

func repositoryDiagnostic(action string, envelope trackerEnvelope, effect *ExternalEffect, cause error) *DiagnosticError {
	kind, retry := KindControlStoreBusy, RetrySafeSameOperation
	message := "control repository " + action + " failed"
	var existing *DiagnosticError
	if errors.As(cause, &existing) {
		kind = existing.Kind
		if existing.RetrySafety != "" {
			retry = existing.RetrySafety
		}
	}
	diagnostic := commandDiagnostic(kind, message, retry, envelope, effect, envelope.Now)
	diagnostic.ObservedState = map[string]string{"control_repository": action + "_failed"}
	diagnostic.ExpectedState = map[string]string{"control_repository": "durable and available"}
	diagnostic.RecoveryActions = []string{"inspect the same operation before retrying", "retry with the same operation ID after the control repository recovers"}
	diagnostic.Cause = cause
	return diagnostic
}

func (s *TrackerSaga) providerFailure(ctx context.Context, envelope trackerEnvelope, effect *ExternalEffect, err error) error {
	var providerError *tracker.Error
	if !errors.As(err, &providerError) {
		diagnostic := commandDiagnostic(KindDecisionRequired, "tracker provider returned an unknown refusal", RetryDecisionRequired, envelope, effect, envelope.Now)
		diagnostic.Cause = err
		return diagnostic
	}
	kind, retry, message := KindDecisionRequired, RetryDecisionRequired, "tracker provider outcome requires inspection"
	actions := []string{"inspect current provider state", "retry only after resolving the provider refusal"}
	switch providerError.Kind {
	case tracker.ErrorUnavailable:
		kind, retry, message = KindTrackerUnavailable, RetrySafeSameOperation, "tracker provider is unavailable"
		actions = []string{"retry the same operation ID after provider recovery", "leave execution authority unchanged while the effect is deferred"}
	case tracker.ErrorAmbiguous:
		kind, retry, message = KindDecisionRequired, RetrySafeSameOperation, "tracker provider outcome is ambiguous"
		actions = []string{"retry the same operation ID so marker and revision are inspected", "do not invent a replacement operation"}
	case tracker.ErrorRevisionConflict:
		message = "tracker provider revision changed"
	case tracker.ErrorNotFound:
		kind, retry, message = KindNotFound, RetryNever, "tracker work was not found"
		actions = []string{"correct the provider work key before starting a new operation"}
	case tracker.ErrorInvalidRequest:
		kind, retry, message = KindInvalidCommand, RetryNever, "tracker provider rejected the request"
		actions = []string{"correct the command envelope before retrying"}
	case tracker.ErrorAttributionLost:
		kind, retry, message = KindExternalAttributionLost, RetryDecisionRequired, "tracker attribution was lost"
		actions = []string{"preserve current provider state", "inspect provider history before deciding recovery"}
	}
	if effect != nil && (providerError.Kind == tracker.ErrorUnavailable || providerError.Kind == tracker.ErrorRevisionConflict) {
		if providerError.Kind == tracker.ErrorUnavailable {
			effect.ResponseStatus = EffectUnavailable
		} else {
			effect.ResponseStatus = EffectDecisionRequired
		}
		effect.ObservedAt = envelope.Now.UTC()
		if saveErr := s.saveEffect(ctx, envelope, *effect); saveErr != nil {
			return saveErr
		}
	}
	diagnostic := commandDiagnostic(kind, message, retry, envelope, effect, envelope.Now)
	diagnostic.ObservedState = map[string]string{"provider_refusal": string(providerError.Kind)}
	diagnostic.ExpectedState = map[string]string{"provider_result": "confirmed or explicitly recoverable"}
	diagnostic.RecoveryActions = actions
	diagnostic.Cause = err
	return diagnostic
}

func effectResult(effect ExternalEffect, envelope trackerEnvelope, replayed, noop bool) TrackerEffectResult {
	return TrackerEffectResult{
		SchemaVersion: ResultSchemaVersion, Kind: "tracker_effect_confirmed", CorrelationID: envelope.CorrelationID,
		OperationID: effect.OperationID, AggregateIDs: commandAggregates(envelope, &effect),
		ObservedState: map[string]string{"response_status": string(effect.ResponseStatus), "provider_revision": fmt.Sprint(effect.AppliedRevision), "assignee": effect.AppliedValue},
		ExpectedState: map[string]string{"provider_revision": fmt.Sprint(effect.PriorRevision), "operation_marker": effect.Marker},
		RetrySafety:   RetryNever, RecoveryActions: []string{"compensate only while revision, value, and marker still match"},
		AppliedRevision: effect.AppliedRevision, Assignee: effect.AppliedValue, Replayed: replayed, Noop: noop, Revision: effect.AppliedRevision,
	}
}

func compensationResult(effect ExternalEffect, envelope trackerEnvelope, replayed, noop bool) TrackerEffectResult {
	return TrackerEffectResult{
		SchemaVersion: ResultSchemaVersion, Kind: "tracker_effect_compensated", CorrelationID: envelope.CorrelationID,
		OperationID: effect.OperationID, AggregateIDs: commandAggregates(envelope, &effect),
		ObservedState: map[string]string{"compensation_status": string(effect.CompensationStatus), "provider_revision": fmt.Sprint(effect.CompensationRevision), "assignee": effect.PriorValue},
		ExpectedState: map[string]string{"provider_revision": fmt.Sprint(effect.AppliedRevision), "assignee": effect.AppliedValue, "operation_marker": effect.Marker},
		RetrySafety:   RetryNever, RecoveryActions: []string{"no further compensation is required"},
		AppliedRevision: effect.AppliedRevision, Assignee: effect.PriorValue, Replayed: replayed, Noop: noop, Revision: effect.CompensationRevision,
	}
}

func commandDiagnostic(kind ErrorKind, message string, retry RetrySafety, envelope trackerEnvelope, effect *ExternalEffect, now time.Time) *DiagnosticError {
	return NewDiagnostic(kind, message, retry, envelope.CorrelationID, envelope.OperationID, commandAggregates(envelope, effect),
		map[string]string{"external_effect": "not confirmed"}, map[string]string{"external_effect": "confirmed or explicitly recoverable"}, now)
}

func commandAggregates(envelope trackerEnvelope, effect *ExternalEffect) map[string]string {
	marker := envelope.OperationID.String()
	if effect != nil && effect.Marker != "" {
		marker = effect.Marker
	}
	return map[string]string{
		"operation_id": envelope.OperationID.String(), "actor_id": envelope.ActorID.String(), "process_id": envelope.ProcessID.String(),
		"provider": envelope.Provider, "provider_work_key": envelope.WorkKey, "operation_marker": marker,
		"expected_provider_revision": fmt.Sprint(envelope.ExpectedRevision),
	}
}
