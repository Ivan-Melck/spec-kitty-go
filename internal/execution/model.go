package execution

import (
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type ErrorKind string

type RetrySafety string

const (
	KindInvalidCommand      ErrorKind = "invalid_command"
	KindAuthorityConflict   ErrorKind = "authority_conflict"
	KindIdempotencyConflict ErrorKind = "idempotency_conflict"
	KindControlStoreBusy    ErrorKind = "control_store_busy"
	KindStaleAuthority      ErrorKind = "stale_authority"
	KindNotFound            ErrorKind = "not_found"
	KindWorkspaceNotReady   ErrorKind = "workspace_not_ready"
	KindRepositoryConflict  ErrorKind = "repository_binding_conflict"
	KindPathUnsafe          ErrorKind = "path_unsafe"
	KindTargetChanged       ErrorKind = "target_changed"
	KindDependencyConflict  ErrorKind = "dependency_conflict"
	KindGitFailure          ErrorKind = "git_failure"
	KindCleanupRefused      ErrorKind = "cleanup_refused"

	RetrySafeSameOperation RetrySafety = "safe_same_operation"
	RetrySafeNewOperation  RetrySafety = "safe_new_operation"
	RetryDecisionRequired  RetrySafety = "decision_required"
	RetryNever             RetrySafety = "never"

	ResultSchemaVersion uint16 = 1
)

type DiagnosticError struct {
	SchemaVersion   uint16            `json:"schema_version"`
	Kind            ErrorKind         `json:"kind"`
	Message         string            `json:"message"`
	CorrelationID   string            `json:"correlation_id"`
	OperationID     ids.OperationID   `json:"operation_id"`
	AggregateIDs    map[string]string `json:"aggregate_ids"`
	ObservedState   map[string]string `json:"observed_state"`
	ExpectedState   map[string]string `json:"expected_state"`
	RetrySafety     RetrySafety       `json:"retry_safety"`
	RecoveryActions []string          `json:"recovery_actions"`
	OccurredAt      time.Time         `json:"occurred_at"`
	Cause           error             `json:"-"`
}

func (e *DiagnosticError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Kind, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *DiagnosticError) Unwrap() error { return e.Cause }

func NewError(kind ErrorKind, message string, retrySafety RetrySafety, aggregateIDs map[string]string) error {
	return NewDiagnostic(kind, message, retrySafety, "", "", aggregateIDs, nil, nil, time.Now().UTC())
}

func NewDiagnostic(kind ErrorKind, message string, retrySafety RetrySafety, correlationID string, operationID ids.OperationID, aggregateIDs, observedState, expectedState map[string]string, occurredAt time.Time) *DiagnosticError {
	if correlationID == "" {
		correlationID = operationID.String()
	}
	if aggregateIDs == nil {
		aggregateIDs = map[string]string{}
	}
	if observedState == nil {
		observedState = map[string]string{}
	}
	if expectedState == nil {
		expectedState = map[string]string{}
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return &DiagnosticError{
		SchemaVersion: ResultSchemaVersion, Kind: kind, Message: message,
		CorrelationID: correlationID, OperationID: operationID, AggregateIDs: aggregateIDs,
		ObservedState: observedState, ExpectedState: expectedState, RetrySafety: retrySafety,
		RecoveryActions: defaultRecoveryActions(retrySafety), OccurredAt: occurredAt.UTC(),
	}
}

func defaultRecoveryActions(retrySafety RetrySafety) []string {
	switch retrySafety {
	case RetrySafeSameOperation:
		return []string{"retry the same operation ID after inspecting current state"}
	case RetrySafeNewOperation:
		return []string{"inspect the failed operation before starting a new operation"}
	case RetryDecisionRequired:
		return []string{"inspect current authority and choose an explicit recovery"}
	default:
		return []string{"correct the command or state before retrying"}
	}
}

func IsKind(err error, kind ErrorKind) bool {
	var diagnostic *DiagnosticError
	return errors.As(err, &diagnostic) && diagnostic.Kind == kind
}

type StartRequest struct {
	CorrelationID     string                    `json:"correlation_id"`
	OperationID       ids.OperationID           `json:"operation_id"`
	ProjectID         ids.ProjectID             `json:"project_id"`
	MissionID         ids.MissionID             `json:"mission_id"`
	WorkRevisionID    ids.WorkRevisionID        `json:"work_revision_id"`
	WorkID            ids.WorkID                `json:"work_id"`
	ActorID           ids.ActorID               `json:"actor_id"`
	ProcessID         ids.ProcessID             `json:"process_id"`
	Now               time.Time                 `json:"now"`
	LeaseTTL          time.Duration             `json:"lease_ttl"`
	CommandDigest     string                    `json:"command_digest,omitempty"`
	GovernanceContext governancecontext.Context `json:"governance_context"`
}

func (r StartRequest) Validate() error {
	values := []struct {
		name  string
		value string
		parse func(string) error
	}{
		{"operation_id", r.OperationID.String(), func(v string) error { _, err := ids.ParseOperationID(v); return err }},
		{"project_id", r.ProjectID.String(), func(v string) error { _, err := ids.ParseProjectID(v); return err }},
		{"mission_id", r.MissionID.String(), func(v string) error { _, err := ids.ParseMissionID(v); return err }},
		{"work_revision_id", r.WorkRevisionID.String(), func(v string) error { _, err := ids.ParseWorkRevisionID(v); return err }},
		{"work_id", r.WorkID.String(), func(v string) error { _, err := ids.ParseWorkID(v); return err }},
		{"actor_id", r.ActorID.String(), func(v string) error { _, err := ids.ParseActorID(v); return err }},
		{"process_id", r.ProcessID.String(), func(v string) error { _, err := ids.ParseProcessID(v); return err }},
	}
	for _, value := range values {
		if err := value.parse(value.value); err != nil {
			diagnostic := NewDiagnostic(KindInvalidCommand, value.name+" is invalid", RetryNever, r.CorrelationID, r.OperationID, nil, map[string]string{value.name: value.value}, map[string]string{value.name: "portable ID"}, r.Now)
			diagnostic.Cause = err
			return diagnostic
		}
	}
	if r.LeaseTTL <= 0 {
		return NewDiagnostic(KindInvalidCommand, "lease_ttl must be positive", RetryNever, r.CorrelationID, r.OperationID, nil, map[string]string{"lease_ttl": r.LeaseTTL.String()}, map[string]string{"lease_ttl": "> 0"}, r.Now)
	}
	if err := r.GovernanceContext.Validate(); err != nil {
		diagnostic := NewDiagnostic(KindInvalidCommand, "governance_context is invalid", RetryNever, r.CorrelationID, r.OperationID, nil,
			map[string]string{"governance_context_id": r.GovernanceContext.ID().String()}, map[string]string{"governance_context": "validated immutable context"}, r.Now)
		diagnostic.Cause = err
		return diagnostic
	}
	return nil
}

type RunCapsule struct {
	SchemaVersion              uint16                       `json:"schema_version"`
	Kind                       string                       `json:"kind"`
	CorrelationID              string                       `json:"correlation_id"`
	OperationID                ids.OperationID              `json:"operation_id"`
	AggregateIDs               map[string]string            `json:"aggregate_ids"`
	ObservedState              map[string]string            `json:"observed_state"`
	ExpectedState              map[string]string            `json:"expected_state"`
	RetrySafety                RetrySafety                  `json:"retry_safety"`
	RecoveryActions            []string                     `json:"recovery_actions"`
	WorkID                     ids.WorkID                   `json:"work_id"`
	WorkRevisionID             ids.WorkRevisionID           `json:"work_revision_id"`
	RunID                      ids.RunID                    `json:"run_id"`
	GovernanceContextID        ids.GovernanceContextID      `json:"governance_context_id"`
	GovernanceContextAggregate governancecontext.Commitment `json:"governance_context_aggregate"`
	LeaseID                    ids.LeaseID                  `json:"lease_id"`
	Generation                 int64                        `json:"generation"`
	Token                      string                       `json:"token,omitempty"`
	Deadline                   time.Time                    `json:"deadline"`
	Replayed                   bool                         `json:"replayed"`
	RepositoryID               ids.RepositoryID             `json:"repository_id,omitempty"`
	LaneID                     ids.LaneID                   `json:"lane_id,omitempty"`
	WorkspaceID                ids.WorkspaceID              `json:"workspace_id,omitempty"`
	WorkspacePath              string                       `json:"workspace_path,omitempty"`
	OwnedBranch                string                       `json:"owned_branch,omitempty"`
	VerifiedBase               string                       `json:"verified_base,omitempty"`
	GitDir                     string                       `json:"git_dir,omitempty"`
	IndexPath                  string                       `json:"index_path,omitempty"`
	WorkspaceStage             workspace.Stage              `json:"workspace_stage,omitempty"`
}

type HeartbeatRequest struct {
	SchemaVersion uint16
	CorrelationID string
	OperationID   ids.OperationID
	ActorID       ids.ActorID
	ProcessID     ids.ProcessID
	LeaseID       ids.LeaseID
	Generation    int64
	Token         string
	Now           time.Time
	LeaseTTL      time.Duration
}

func (r HeartbeatRequest) Validate() error {
	if r.SchemaVersion != ResultSchemaVersion {
		return NewDiagnostic(KindInvalidCommand, "unsupported schema_version", RetryNever, r.CorrelationID, r.OperationID, leaseAggregates(r.OperationID, r.ActorID, r.ProcessID, r.LeaseID, r.Generation), map[string]string{"schema_version": fmt.Sprint(r.SchemaVersion)}, map[string]string{"schema_version": fmt.Sprint(ResultSchemaVersion)}, r.Now)
	}
	if _, err := ids.ParseOperationID(r.OperationID.String()); err != nil {
		return leaseValidationError("operation_id", r.OperationID.String(), r)
	}
	if _, err := ids.ParseActorID(r.ActorID.String()); err != nil {
		return leaseValidationError("actor_id", r.ActorID.String(), r)
	}
	if _, err := ids.ParseProcessID(r.ProcessID.String()); err != nil {
		return leaseValidationError("process_id", r.ProcessID.String(), r)
	}
	if _, err := ids.ParseLeaseID(r.LeaseID.String()); err != nil || r.Generation <= 0 || r.Token == "" || r.LeaseTTL <= 0 {
		return NewDiagnostic(KindInvalidCommand, "lease_id, positive generation, token, and lease_ttl are required", RetryNever, r.CorrelationID, r.OperationID, leaseAggregates(r.OperationID, r.ActorID, r.ProcessID, r.LeaseID, r.Generation), map[string]string{"lease_command": "incomplete"}, map[string]string{"lease_command": "current authority and positive ttl"}, r.Now)
	}
	return nil
}

type ExpireRequest struct {
	SchemaVersion uint16
	CorrelationID string
	OperationID   ids.OperationID
	ActorID       ids.ActorID
	ProcessID     ids.ProcessID
	LeaseID       ids.LeaseID
	Generation    int64
	Token         string
	Now           time.Time
}

func (r ExpireRequest) Validate() error {
	if r.SchemaVersion != ResultSchemaVersion {
		return NewDiagnostic(KindInvalidCommand, "unsupported schema_version", RetryNever, r.CorrelationID, r.OperationID, leaseAggregates(r.OperationID, r.ActorID, r.ProcessID, r.LeaseID, r.Generation), map[string]string{"schema_version": fmt.Sprint(r.SchemaVersion)}, map[string]string{"schema_version": fmt.Sprint(ResultSchemaVersion)}, r.Now)
	}
	if _, err := ids.ParseOperationID(r.OperationID.String()); err != nil {
		return expireValidationError("operation_id", r.OperationID.String(), r)
	}
	if _, err := ids.ParseActorID(r.ActorID.String()); err != nil {
		return expireValidationError("actor_id", r.ActorID.String(), r)
	}
	if _, err := ids.ParseProcessID(r.ProcessID.String()); err != nil {
		return expireValidationError("process_id", r.ProcessID.String(), r)
	}
	if _, err := ids.ParseLeaseID(r.LeaseID.String()); err != nil || r.Generation <= 0 || r.Token == "" {
		return NewDiagnostic(KindInvalidCommand, "lease_id, positive generation, and token are required", RetryNever, r.CorrelationID, r.OperationID, leaseAggregates(r.OperationID, r.ActorID, r.ProcessID, r.LeaseID, r.Generation), map[string]string{"lease_command": "incomplete"}, map[string]string{"lease_command": "current authority"}, r.Now)
	}
	return nil
}

func leaseAggregates(operationID ids.OperationID, actorID ids.ActorID, processID ids.ProcessID, leaseID ids.LeaseID, generation int64) map[string]string {
	return map[string]string{"operation_id": operationID.String(), "actor_id": actorID.String(), "process_id": processID.String(), "lease_id": leaseID.String(), "generation": fmt.Sprint(generation)}
}

func leaseValidationError(field, value string, r HeartbeatRequest) error {
	return NewDiagnostic(KindInvalidCommand, field+" is invalid", RetryNever, r.CorrelationID, r.OperationID, leaseAggregates(r.OperationID, r.ActorID, r.ProcessID, r.LeaseID, r.Generation), map[string]string{field: value}, map[string]string{field: "portable ID"}, r.Now)
}

func expireValidationError(field, value string, r ExpireRequest) error {
	return NewDiagnostic(KindInvalidCommand, field+" is invalid", RetryNever, r.CorrelationID, r.OperationID, leaseAggregates(r.OperationID, r.ActorID, r.ProcessID, r.LeaseID, r.Generation), map[string]string{field: value}, map[string]string{field: "portable ID"}, r.Now)
}
