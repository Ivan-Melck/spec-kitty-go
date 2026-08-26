package execution

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// RecoveryDisposition is an operator-visible conclusion, not a cached saga
// stage. A decision is returned only after the normal start path has re-probed
// tracker and Git facts.
type RecoveryDisposition string

const (
	RecoveryResume           RecoveryDisposition = "resume"
	RecoveryComplete         RecoveryDisposition = "complete"
	RecoveryDecisionRequired RecoveryDisposition = "decision_required"
	RecoveryExpired          RecoveryDisposition = "expired"
)

type StartRecoveryRequest struct {
	SchemaVersion uint16          `json:"schema_version"`
	CorrelationID string          `json:"correlation_id"`
	OperationID   ids.OperationID `json:"operation_id"`
	Now           time.Time       `json:"now"`
}

func (r StartRecoveryRequest) Validate() error {
	if r.SchemaVersion != ResultSchemaVersion {
		return NewDiagnostic(KindInvalidCommand, "unsupported schema_version", RetryNever, r.CorrelationID, r.OperationID, recoveryAggregates(r.OperationID), map[string]string{"schema_version": fmt.Sprint(r.SchemaVersion)}, map[string]string{"schema_version": fmt.Sprint(ResultSchemaVersion)}, r.Now)
	}
	if _, err := ids.ParseOperationID(r.OperationID.String()); err != nil {
		diagnostic := NewDiagnostic(KindInvalidCommand, "operation_id is invalid", RetryNever, r.CorrelationID, r.OperationID, recoveryAggregates(r.OperationID), map[string]string{"operation_id": r.OperationID.String()}, map[string]string{"operation_id": "portable ID"}, r.Now)
		diagnostic.Cause = err
		return diagnostic
	}
	if r.CorrelationID == "" {
		return NewDiagnostic(KindInvalidCommand, "correlation_id is required", RetryNever, r.CorrelationID, r.OperationID, recoveryAggregates(r.OperationID), map[string]string{"correlation_id": ""}, map[string]string{"correlation_id": "non-empty"}, r.Now)
	}
	return nil
}

type StartRecoveryRecord struct {
	Request          StartRunRequest `json:"request"`
	OperationStatus  string          `json:"operation_status"`
	RunID            ids.RunID       `json:"run_id,omitempty"`
	LeaseID          ids.LeaseID     `json:"lease_id,omitempty"`
	Generation       int64           `json:"generation,omitempty"`
	Deadline         time.Time       `json:"deadline,omitempty"`
	Token            string          `json:"-"`
	LeaseStatus      string          `json:"lease_status,omitempty"`
	WorkspaceStatus  string          `json:"workspace_status,omitempty"`
	WorkspaceStage   workspace.Stage `json:"workspace_stage,omitempty"`
	TerminalRecorded bool            `json:"terminal_recorded"`
}

type StartRecoveryDecision struct {
	SchemaVersion   uint16              `json:"schema_version"`
	Kind            string              `json:"kind"`
	CorrelationID   string              `json:"correlation_id"`
	OperationID     ids.OperationID     `json:"operation_id"`
	Disposition     RecoveryDisposition `json:"disposition"`
	StoredStage     workspace.Stage     `json:"stored_stage,omitempty"`
	ObservedState   map[string]string   `json:"observed_state"`
	ExpectedState   map[string]string   `json:"expected_state"`
	RetrySafety     RetrySafety         `json:"retry_safety"`
	RecoveryActions []string            `json:"recovery_actions"`
	Diagnostic      *DiagnosticError    `json:"diagnostic,omitempty"`
	Capsule         *RunCapsule         `json:"capsule,omitempty"`
}

type StartRecoveryRepository interface {
	PrepareStartRun(context.Context, StartRunRequest) error
	LoadStartRun(context.Context, ids.OperationID) (StartRecoveryRecord, error)
	CompleteStartRun(context.Context, RunCapsule, time.Time) error
}

func (s *Service) PrepareStartRun(ctx context.Context, request StartRunRequest) error {
	request.Claim = withGovernanceCommandDigest(request.Claim)
	if err := s.faults.Reach(ctx, "SR-00"); err != nil {
		return err
	}
	repository, ok := s.repository.(StartRecoveryRepository)
	if ok {
		if err := repository.PrepareStartRun(ctx, request); err != nil {
			return err
		}
	}
	return s.faults.Reach(ctx, "SR-01")
}

func (s *Service) LoadStartRun(ctx context.Context, operationID ids.OperationID) (StartRecoveryRecord, error) {
	repository, ok := s.repository.(StartRecoveryRepository)
	if !ok {
		return StartRecoveryRecord{}, NewDiagnostic(KindInvalidCommand, "authority repository does not support operation recovery", RetryNever, operationID.String(), operationID, recoveryAggregates(operationID), map[string]string{"recovery_store": "unavailable"}, map[string]string{"recovery_store": "operation envelope available"}, s.clock.Now())
	}
	return repository.LoadStartRun(ctx, operationID)
}

func (s *Service) CompleteStartRun(ctx context.Context, capsule RunCapsule, completedAt time.Time) error {
	repository, ok := s.repository.(StartRecoveryRepository)
	if !ok {
		return nil
	}
	return repository.CompleteStartRun(ctx, capsule, completedAt)
}

func (s *StartSaga) Recover(ctx context.Context, request StartRecoveryRequest) (StartRecoveryDecision, error) {
	if request.Now.IsZero() {
		request.Now = s.authority.clock.Now()
	}
	if request.CorrelationID == "" {
		request.CorrelationID = request.OperationID.String()
	}
	if err := request.Validate(); err != nil {
		return StartRecoveryDecision{}, err
	}
	record, err := s.authority.LoadStartRun(ctx, request.OperationID)
	if err != nil {
		return StartRecoveryDecision{}, err
	}
	decision := StartRecoveryDecision{
		SchemaVersion: ResultSchemaVersion, Kind: "start_run_recovery", CorrelationID: request.CorrelationID,
		OperationID: request.OperationID, Disposition: RecoveryResume, StoredStage: record.WorkspaceStage,
		ObservedState: recoveryState(record), ExpectedState: map[string]string{"operation": "one verified terminal run", "workspace": "ready or typed decision"},
		RetrySafety: RetrySafeSameOperation, RecoveryActions: []string{"resume only this OperationID through its durable request envelope"},
	}
	if record.LeaseStatus == "active" && !record.Deadline.IsZero() && !record.Deadline.After(request.Now.UTC()) {
		expire := ExpireRequest{
			SchemaVersion: ResultSchemaVersion, CorrelationID: request.CorrelationID, OperationID: request.OperationID,
			ActorID: record.Request.Claim.ActorID, ProcessID: record.Request.Claim.ProcessID,
			LeaseID: record.LeaseID, Generation: record.Generation, Token: record.Token, Now: request.Now.UTC(),
		}
		if err := s.authority.Expire(ctx, expire); err != nil && !IsKind(err, KindStaleAuthority) {
			return StartRecoveryDecision{}, err
		}
		decision.Disposition = RecoveryExpired
		decision.RetrySafety = RetryDecisionRequired
		decision.ObservedState["lease_status"] = "expired"
		decision.ObservedState["workspace_status"] = "quarantined"
		decision.RecoveryActions = []string{"inspect the quarantined workspace", "start a new operation with a new workspace identity if replacement is allowed"}
		decision.Diagnostic = NewDiagnostic(KindStaleAuthority, "stored StartRun authority reached its deadline and was expired during recovery", RetryDecisionRequired,
			request.CorrelationID, request.OperationID,
			map[string]string{"operation_id": request.OperationID.String(), "run_id": record.RunID.String(), "lease_id": record.LeaseID.String(), "generation": fmt.Sprint(record.Generation)},
			map[string]string{"lease_status": "expired", "workspace_status": "quarantined"}, map[string]string{"lease_status": "active before resume"}, request.Now)
		decision.Diagnostic.RecoveryActions = append([]string(nil), decision.RecoveryActions...)
		return decision, nil
	}
	capsule, err := s.Start(ctx, record.Request)
	if err == nil {
		decision.Disposition = RecoveryComplete
		decision.RetrySafety = RetryNever
		decision.RecoveryActions = []string{"use the verified run capsule while its authority remains current"}
		decision.Capsule = &capsule
		decision.ObservedState["operation"] = "terminal"
		decision.ObservedState["workspace"] = "ready"
		return decision, nil
	}
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) {
		return StartRecoveryDecision{}, err
	}
	decision.Diagnostic = diagnostic
	decision.ObservedState = diagnostic.ObservedState
	decision.ExpectedState = diagnostic.ExpectedState
	decision.RetrySafety = diagnostic.RetrySafety
	decision.RecoveryActions = append([]string(nil), diagnostic.RecoveryActions...)
	switch diagnostic.Kind {
	case KindStaleAuthority:
		decision.Disposition = RecoveryExpired
	case KindRepositoryConflict, KindTargetChanged, KindDependencyConflict, KindWorkspaceNotReady, KindPathUnsafe:
		decision.Disposition = RecoveryDecisionRequired
		decision.RetrySafety = RetryDecisionRequired
	default:
		return StartRecoveryDecision{}, err
	}
	return decision, nil
}

func recoveryState(record StartRecoveryRecord) map[string]string {
	return map[string]string{
		"operation_status":             record.OperationStatus,
		"run_id":                       record.RunID.String(),
		"lease_id":                     record.LeaseID.String(),
		"generation":                   fmt.Sprint(record.Generation),
		"lease_status":                 record.LeaseStatus,
		"workspace_status":             record.WorkspaceStatus,
		"workspace_stage":              string(record.WorkspaceStage),
		"terminal_recorded":            fmt.Sprint(record.TerminalRecorded),
		"governance_context_id":        record.Request.Claim.GovernanceContext.ID().String(),
		"governance_context_aggregate": record.Request.Claim.GovernanceContext.Aggregate().Algorithm() + ":" + record.Request.Claim.GovernanceContext.Aggregate().Digest(),
	}
}

func recoveryAggregates(operationID ids.OperationID) map[string]string {
	return map[string]string{"operation_id": operationID.String()}
}
