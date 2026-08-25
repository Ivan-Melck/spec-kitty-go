package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type WorkspaceRequest struct {
	RepositoryID ids.RepositoryID
	LaneID       ids.LaneID
	WorkspaceID  ids.WorkspaceID
	OwnedBranch  string
	PinnedBase   string
	Dependencies []workspace.Dependency
}

type StartRunRequest struct {
	SchemaVersion uint16
	Claim         StartRequest
	Assignment    TrackerAssignRequest
	Workspace     WorkspaceRequest
}

type WorkspacePreparer interface {
	Validate(context.Context, workspace.Reservation) error
	ValidateBinding(context.Context, workspace.Reservation) error
	EnsureReady(context.Context, workspace.Reservation) (workspace.Record, error)
}

type StartSaga struct {
	authority  *Service
	tracker    *TrackerSaga
	workspaces WorkspacePreparer
}

func NewStartSaga(authority *Service, tracker *TrackerSaga, workspaces WorkspacePreparer) *StartSaga {
	return &StartSaga{authority: authority, tracker: tracker, workspaces: workspaces}
}

func (s *StartSaga) Start(ctx context.Context, request StartRunRequest) (RunCapsule, error) {
	if err := validateStartRun(request); err != nil {
		return RunCapsule{}, err
	}
	digest, err := startRunDigest(request)
	if err != nil {
		return RunCapsule{}, err
	}
	claim := request.Claim
	claim.CommandDigest = digest
	request.Claim = claim
	existing, err := s.authority.LookupStartOperation(ctx, claim)
	if err != nil {
		return RunCapsule{}, err
	}
	if !existing {
		preflight := startReservation(request, "run-validation", workspace.Authority{LeaseID: "lease-validation", Generation: 1, Token: "validation"})
		if err := s.workspaces.Validate(ctx, preflight); err != nil {
			return RunCapsule{}, workspaceDiagnostic(request, RunCapsule{}, err)
		}
	} else {
		preflight := startReservation(request, "run-validation", workspace.Authority{LeaseID: "lease-validation", Generation: 1, Token: "validation"})
		if err := s.workspaces.ValidateBinding(ctx, preflight); err != nil {
			return RunCapsule{}, workspaceDiagnostic(request, RunCapsule{}, err)
		}
	}
	if err := s.authority.PrepareStartRun(ctx, request); err != nil {
		return RunCapsule{}, err
	}
	capsule, err := s.authority.StartPrepared(ctx, claim)
	if err != nil {
		return RunCapsule{}, err
	}
	if _, err := s.tracker.Assign(ctx, request.Assignment); err != nil {
		return RunCapsule{}, err
	}
	record, err := s.workspaces.EnsureReady(ctx, startReservation(request, capsule.RunID, workspace.Authority{LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token}))
	if err != nil {
		return RunCapsule{}, workspaceDiagnostic(request, capsule, err)
	}
	if !record.Ready() {
		return RunCapsule{}, workspaceDiagnostic(request, capsule, &workspace.Error{Kind: workspace.KindWorkspaceNotReady, Message: "workspace service returned an unready record"})
	}
	capsule.Kind = "run_ready"
	capsule.RepositoryID = record.RepositoryID
	capsule.LaneID = record.LaneID
	capsule.WorkspaceID = record.WorkspaceID
	capsule.WorkspacePath = record.CanonicalPath
	capsule.OwnedBranch = record.OwnedBranch
	capsule.VerifiedBase = record.DependencyHead
	capsule.GitDir = record.GitDir
	capsule.IndexPath = record.IndexPath
	capsule.WorkspaceStage = record.Stage
	capsule.ObservedState = map[string]string{"lease": "active", "tracker_assignment": "confirmed", "workspace": "ready", "head": record.DependencyHead}
	capsule.ExpectedState = map[string]string{"lease": "active", "workspace": "verified ready"}
	capsule.AggregateIDs["repository_id"] = record.RepositoryID.String()
	capsule.AggregateIDs["lane_id"] = record.LaneID.String()
	capsule.AggregateIDs["workspace_id"] = record.WorkspaceID.String()
	if err := s.authority.CompleteStartRun(ctx, capsule, request.Claim.Now); err != nil {
		return RunCapsule{}, err
	}
	return capsule, nil
}

func validateStartRun(request StartRunRequest) error {
	if request.SchemaVersion != ResultSchemaVersion {
		return NewDiagnostic(KindInvalidCommand, "unsupported schema_version", RetryNever, request.Claim.CorrelationID, request.Claim.OperationID, nil, map[string]string{"schema_version": fmt.Sprint(request.SchemaVersion)}, map[string]string{"schema_version": fmt.Sprint(ResultSchemaVersion)}, request.Claim.Now)
	}
	if err := request.Claim.Validate(); err != nil {
		return err
	}
	if err := validateAssignRequest(request.Assignment); err != nil {
		return err
	}
	if request.Assignment.OperationID != request.Claim.OperationID || request.Assignment.ActorID != request.Claim.ActorID || request.Assignment.ProcessID != request.Claim.ProcessID || request.Assignment.CorrelationID != request.Claim.CorrelationID {
		return NewDiagnostic(KindInvalidCommand, "start-run claim and tracker envelopes must identify the same operation, actor, process, and correlation", RetryNever, request.Claim.CorrelationID, request.Claim.OperationID, nil, map[string]string{"envelopes": "mismatch"}, map[string]string{"envelopes": "same authority identity"}, request.Claim.Now)
	}
	reservation := workspace.Reservation{
		OperationID: request.Claim.OperationID, RunID: "run-validation", WorkspaceID: request.Workspace.WorkspaceID,
		ProjectID: request.Claim.ProjectID, MissionID: request.Claim.MissionID, WorkRevisionID: request.Claim.WorkRevisionID, WorkID: request.Claim.WorkID,
		RepositoryID: request.Workspace.RepositoryID, LaneID: request.Workspace.LaneID,
		Authority:   workspace.Authority{LeaseID: "lease-validation", Generation: 1, Token: "validation"},
		OwnedBranch: request.Workspace.OwnedBranch, PinnedBase: request.Workspace.PinnedBase,
		Dependencies: request.Workspace.Dependencies, Now: request.Claim.Now,
	}
	if err := reservation.Validate(); err != nil {
		return workspaceDiagnostic(request, RunCapsule{}, err)
	}
	return nil
}

func startReservation(request StartRunRequest, runID ids.RunID, authority workspace.Authority) workspace.Reservation {
	return workspace.Reservation{
		OperationID: request.Claim.OperationID, ProjectID: request.Claim.ProjectID, MissionID: request.Claim.MissionID,
		WorkRevisionID: request.Claim.WorkRevisionID, WorkID: request.Claim.WorkID, RunID: runID,
		WorkspaceID: request.Workspace.WorkspaceID, RepositoryID: request.Workspace.RepositoryID, LaneID: request.Workspace.LaneID,
		Authority: authority, OwnedBranch: request.Workspace.OwnedBranch, PinnedBase: request.Workspace.PinnedBase,
		Dependencies: request.Workspace.Dependencies, Now: request.Claim.Now,
	}
}

func startRunDigest(request StartRunRequest) (string, error) {
	dependencies, err := workspace.CanonicalDependencies(request.Workspace.Dependencies)
	if err != nil {
		return "", err
	}
	identity := struct {
		SchemaVersion uint16               `json:"schema_version"`
		CorrelationID string               `json:"correlation_id"`
		Claim         StartRequest         `json:"claim"`
		Assignment    TrackerAssignRequest `json:"assignment"`
		Workspace     WorkspaceRequest     `json:"workspace"`
	}{request.SchemaVersion, request.Claim.CorrelationID, request.Claim, request.Assignment, request.Workspace}
	identity.Claim.CommandDigest = ""
	identity.Workspace.Dependencies = dependencies
	encoded, err := json.Marshal(identity)
	if err != nil {
		return "", NewDiagnostic(KindInvalidCommand, "encode start-run identity", RetryNever, request.Claim.CorrelationID, request.Claim.OperationID, nil, nil, nil, request.Claim.Now)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func workspaceDiagnostic(request StartRunRequest, capsule RunCapsule, cause error) error {
	var workspaceError *workspace.Error
	if !errors.As(cause, &workspaceError) {
		workspaceError = &workspace.Error{Kind: workspace.KindWorkspaceNotReady, Message: "workspace preparation failed", Cause: cause}
	}
	kind := map[workspace.ErrorKind]ErrorKind{
		workspace.KindInvalid: KindInvalidCommand, workspace.KindBindingConflict: KindRepositoryConflict,
		workspace.KindWorkspaceConflict: KindWorkspaceNotReady, workspace.KindWorkspaceNotReady: KindWorkspaceNotReady,
		workspace.KindPathUnsafe: KindPathUnsafe, workspace.KindTargetMoved: KindTargetChanged,
		workspace.KindDependencyConflict: KindDependencyConflict, workspace.KindStaleAuthority: KindStaleAuthority,
		workspace.KindGitFailure: KindGitFailure,
	}[workspaceError.Kind]
	if kind == "" {
		kind = KindWorkspaceNotReady
	}
	aggregates := map[string]string{
		"operation_id": request.Claim.OperationID.String(), "project_id": request.Claim.ProjectID.String(), "mission_id": request.Claim.MissionID.String(),
		"work_id": request.Claim.WorkID.String(), "work_revision_id": request.Claim.WorkRevisionID.String(), "actor_id": request.Claim.ActorID.String(), "process_id": request.Claim.ProcessID.String(),
		"repository_id": request.Workspace.RepositoryID.String(), "lane_id": request.Workspace.LaneID.String(), "workspace_id": request.Workspace.WorkspaceID.String(),
	}
	if capsule.RunID != "" {
		aggregates["run_id"] = capsule.RunID.String()
		aggregates["lease_id"] = capsule.LeaseID.String()
		aggregates["generation"] = fmt.Sprint(capsule.Generation)
	}
	retry := RetrySafeSameOperation
	actions := []string{"inspect the same operation and workspace Git facts", "retry with the same operation ID after correcting the reported blocker"}
	if workspaceError.Kind == workspace.KindBindingConflict {
		retry = RetryDecisionRequired
		actions = []string{"inspect the durable repository binding and the live canonical Git paths", "resume the exact operation only through its originally bound physical repository"}
	}
	diagnostic := NewDiagnostic(kind, workspaceError.Message, retry, request.Claim.CorrelationID, request.Claim.OperationID, aggregates, workspaceError.Observed, workspaceError.Expected, request.Claim.Now)
	diagnostic.RecoveryActions = actions
	diagnostic.Cause = cause
	return diagnostic
}
