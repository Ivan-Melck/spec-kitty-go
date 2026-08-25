package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type WorkspaceMaintenanceResponse struct {
	OK          bool                       `json:"ok"`
	WorkspaceID ids.WorkspaceID            `json:"workspace_id,omitempty"`
	LeaseStatus string                     `json:"lease_status,omitempty"`
	Inspection  *gitrepo.Inspection        `json:"inspection,omitempty"`
	Cleaned     bool                       `json:"cleaned,omitempty"`
	Error       *execution.DiagnosticError `json:"error,omitempty"`
}

type workspaceMaintenanceCommand struct {
	SchemaVersion uint16
	CorrelationID string
	OperationID   ids.OperationID
	WorkspaceID   ids.WorkspaceID
	ActorID       ids.ActorID
	ProcessID     ids.ProcessID
	Now           time.Time
	Database      string
	Primary       string
	WorkspaceRoot string
}

func runWorkspaceInspect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWorkspaceMaintenance(ctx, args, stdout, stderr, false)
}

func runWorkspaceCleanup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runWorkspaceMaintenance(ctx, args, stdout, stderr, true)
}

func runWorkspaceMaintenance(ctx context.Context, args []string, stdout, stderr io.Writer, cleanup bool) int {
	name := "workspace-inspect"
	if cleanup {
		name = "workspace-cleanup"
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var command workspaceMaintenanceCommand
	var schema uint
	var operation, workspaceID, actor, process, at string
	flags.UintVar(&schema, "schema-version", uint(execution.ResultSchemaVersion), "command schema version")
	flags.StringVar(&command.Database, "db", "", "control database path")
	flags.StringVar(&command.Primary, "repository", "", "canonical primary Git checkout root")
	flags.StringVar(&command.WorkspaceRoot, "workspace-root", "", "canonical managed workspace root")
	flags.StringVar(&operation, "operation", "", "workspace StartRun operation ID")
	flags.StringVar(&workspaceID, "workspace", "", "workspace ID; omit to resolve from operation")
	flags.StringVar(&actor, "actor", "", "operator actor ID")
	flags.StringVar(&process, "process", "", "operator process ID")
	flags.StringVar(&command.CorrelationID, "correlation-id", "", "diagnostic correlation ID")
	flags.StringVar(&at, "at", "", "injected RFC3339 timestamp")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	command.SchemaVersion = uint16(schema)
	var err error
	command.OperationID, err = ids.ParseOperationID(operation)
	if err != nil {
		return encodeInvalidID(stdout, operation, "operation_id", err)
	}
	if workspaceID != "" {
		command.WorkspaceID, err = ids.ParseWorkspaceID(workspaceID)
		if err != nil {
			return encodeWorkspaceMaintenanceFailure(stdout, maintenanceValidationDiagnostic(command, "workspace_id", workspaceID, "portable ID", err))
		}
	}
	command.ActorID, err = ids.ParseActorID(actor)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceValidationDiagnostic(command, "actor_id", actor, "portable ID", err))
	}
	command.ProcessID, err = ids.ParseProcessID(process)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceValidationDiagnostic(command, "process_id", process, "portable ID", err))
	}
	command.Now, err = time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceValidationDiagnostic(command, "at", at, "RFC3339 timestamp", err))
	}
	command.Now = command.Now.UTC()
	if command.CorrelationID == "" {
		command.CorrelationID = command.OperationID.String()
	}
	if command.SchemaVersion != execution.ResultSchemaVersion {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceValidationDiagnostic(command, "schema_version", strconv.FormatUint(uint64(command.SchemaVersion), 10), strconv.FormatUint(uint64(execution.ResultSchemaVersion), 10), nil))
	}

	store, err := sqlite.Open(ctx, command.Database)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceDiagnostic(command, workspace.Record{}, "", nil, err))
	}
	defer store.Close()
	record, err := resolveMaintenanceWorkspace(ctx, store, command)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, err)
	}
	command.WorkspaceID = record.WorkspaceID
	leaseStatus, err := store.WorkspaceLeaseStatus(ctx, record.WorkspaceID)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceDiagnostic(command, record, "", nil, err))
	}
	binding, err := store.Repository(ctx, record.RepositoryID)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceDiagnostic(command, record, leaseStatus, nil, err))
	}
	adapter, err := gitrepo.Bind(ctx, gitrepo.Runner{}, binding.RepositoryID, binding.ProjectID, command.Primary, command.WorkspaceRoot, binding.TargetRef, command.Now)
	if err != nil {
		return encodeWorkspaceMaintenanceFailure(stdout, maintenanceDiagnostic(command, record, leaseStatus, nil, err))
	}
	inspection, err := adapter.Inspect(ctx, record)
	if err != nil {
		return encodeWorkspaceMaintenanceResponse(stdout, WorkspaceMaintenanceResponse{
			WorkspaceID: record.WorkspaceID, LeaseStatus: leaseStatus, Inspection: &inspection,
			Error: maintenanceDiagnostic(command, record, leaseStatus, &inspection, err),
		})
	}
	response := WorkspaceMaintenanceResponse{WorkspaceID: record.WorkspaceID, LeaseStatus: leaseStatus, Inspection: &inspection}
	service := workspace.NewService(store, adapter)
	if err := service.ValidateRepositoryBinding(ctx, record.RepositoryID); err != nil {
		response.Error = maintenanceDiagnostic(command, record, leaseStatus, &inspection, err)
		return encodeWorkspaceMaintenanceResponse(stdout, response)
	}
	if !cleanup {
		if err := gitrepo.ValidateCleanup(record, leaseStatus, inspection); err != nil {
			response.Error = maintenanceDiagnostic(command, record, leaseStatus, &inspection, err)
			return encodeWorkspaceMaintenanceResponse(stdout, response)
		}
		response.OK = true
		return encodeWorkspaceMaintenanceResponse(stdout, response)
	}
	if err := adapter.Cleanup(ctx, record, leaseStatus); err != nil {
		response.Error = maintenanceDiagnostic(command, record, leaseStatus, &inspection, err)
		return encodeWorkspaceMaintenanceResponse(stdout, response)
	}
	response.OK, response.Cleaned = true, true
	return encodeWorkspaceMaintenanceResponse(stdout, response)
}

func resolveMaintenanceWorkspace(ctx context.Context, store *sqlite.Store, command workspaceMaintenanceCommand) (workspace.Record, error) {
	workspaceID := command.WorkspaceID
	if workspaceID == "" {
		recovery, err := store.LoadStartRun(ctx, command.OperationID)
		if err != nil {
			return workspace.Record{}, maintenanceDiagnostic(command, workspace.Record{}, "", nil, err)
		}
		workspaceID = recovery.Request.Workspace.WorkspaceID
	}
	record, err := store.Workspace(ctx, workspaceID)
	if err != nil {
		if workspace.IsKind(err, workspace.KindInvalid) {
			command.WorkspaceID = workspaceID
			return workspace.Record{}, maintenanceNotFoundDiagnostic(command)
		}
		return workspace.Record{}, maintenanceDiagnostic(command, workspace.Record{}, "", nil, err)
	}
	if record.OperationID != command.OperationID {
		diagnostic := execution.NewDiagnostic(execution.KindRepositoryConflict, "workspace belongs to a different StartRun operation", execution.RetryDecisionRequired,
			command.CorrelationID, command.OperationID, maintenanceAggregates(command, record),
			map[string]string{"workspace_operation_id": record.OperationID.String()}, map[string]string{"workspace_operation_id": command.OperationID.String()}, command.Now)
		diagnostic.RecoveryActions = maintenanceRecoveryActions()
		return workspace.Record{}, diagnostic
	}
	return record, nil
}

func maintenanceNotFoundDiagnostic(command workspaceMaintenanceCommand) *execution.DiagnosticError {
	diagnostic := execution.NewDiagnostic(execution.KindNotFound, "workspace is not present in the durable control store", execution.RetryDecisionRequired,
		command.CorrelationID, command.OperationID, maintenanceAggregates(command, workspace.Record{}),
		map[string]string{"workspace": "absent"}, map[string]string{"workspace": "durable record bound to the requested operation"}, command.Now)
	diagnostic.RecoveryActions = maintenanceRecoveryActions()
	return diagnostic
}

func maintenanceValidationDiagnostic(command workspaceMaintenanceCommand, field, observed, expected string, cause error) *execution.DiagnosticError {
	diagnostic := execution.NewDiagnostic(execution.KindInvalidCommand, field+" is invalid", execution.RetryNever,
		command.CorrelationID, command.OperationID, maintenanceAggregates(command, workspace.Record{}),
		map[string]string{field: observed}, map[string]string{field: expected}, command.Now)
	diagnostic.Cause = cause
	return diagnostic
}

func maintenanceDiagnostic(command workspaceMaintenanceCommand, record workspace.Record, leaseStatus string, inspection *gitrepo.Inspection, cause error) *execution.DiagnosticError {
	kind := execution.KindGitFailure
	message := "workspace inspection or cleanup could not verify durable Git facts"
	retry := execution.RetryDecisionRequired
	observed := map[string]string{
		"lease_status": leaseStatus, "workspace_status": record.Status, "quarantined": strconv.FormatBool(workspace.IsQuarantined(record)),
	}
	expected := map[string]string{"workspace": "known clean inactive non-quarantined worktree with matching durable Git facts"}
	var existing *execution.DiagnosticError
	if errors.As(cause, &existing) {
		kind, message, retry = existing.Kind, existing.Message, existing.RetrySafety
		for key, value := range existing.ObservedState {
			observed[key] = value
		}
		for key, value := range existing.ExpectedState {
			expected[key] = value
		}
	}
	var workspaceError *workspace.Error
	if errors.As(cause, &workspaceError) {
		message = workspaceError.Message
		for key, value := range workspaceError.Observed {
			observed[key] = value
		}
		for key, value := range workspaceError.Expected {
			expected[key] = value
		}
		kind = map[workspace.ErrorKind]execution.ErrorKind{
			workspace.KindInvalid: execution.KindInvalidCommand, workspace.KindBindingConflict: execution.KindRepositoryConflict,
			workspace.KindWorkspaceConflict: execution.KindWorkspaceNotReady, workspace.KindWorkspaceNotReady: execution.KindWorkspaceNotReady,
			workspace.KindPathUnsafe: execution.KindPathUnsafe, workspace.KindTargetMoved: execution.KindTargetChanged,
			workspace.KindDependencyConflict: execution.KindDependencyConflict, workspace.KindStaleAuthority: execution.KindStaleAuthority,
			workspace.KindGitFailure: execution.KindGitFailure, workspace.KindCleanupRefused: execution.KindCleanupRefused,
		}[workspaceError.Kind]
		if kind == "" {
			kind = execution.KindWorkspaceNotReady
		}
	}
	if inspection != nil {
		observed["registered"] = strconv.FormatBool(inspection.Registered)
		observed["clean"] = strconv.FormatBool(inspection.Clean)
		observed["path"] = inspection.CanonicalPath
		observed["branch"] = inspection.ObservedBranch
		observed["head"] = inspection.ObservedHead
		expected["branch"] = inspection.RecordedBranch
		expected["head"] = inspection.RecordedHead
		encoded, _ := json.Marshal(inspection.Discrepancies)
		observed["discrepancies"] = string(encoded)
		commits, _ := json.Marshal(inspection.PreservedCommits)
		observed["preserved_commits"] = string(commits)
	}
	diagnostic := execution.NewDiagnostic(kind, message, retry, command.CorrelationID, command.OperationID, maintenanceAggregates(command, record), observed, expected, command.Now)
	diagnostic.RecoveryActions = maintenanceRecoveryActions()
	diagnostic.Cause = cause
	return diagnostic
}

func maintenanceAggregates(command workspaceMaintenanceCommand, record workspace.Record) map[string]string {
	workspaceID := command.WorkspaceID
	if record.WorkspaceID != "" {
		workspaceID = record.WorkspaceID
	}
	return map[string]string{
		"operation_id": command.OperationID.String(), "actor_id": command.ActorID.String(), "process_id": command.ProcessID.String(),
		"workspace_id": workspaceID.String(), "repository_id": record.RepositoryID.String(), "run_id": record.RunID.String(),
		"lease_id": record.Authority.LeaseID.String(), "generation": strconv.FormatInt(record.Authority.Generation, 10),
	}
}

func maintenanceRecoveryActions() []string {
	return []string{
		"leave the workspace path and owned branch unchanged",
		"inspect the reported preserved commit IDs and discrepancies",
		"reconcile authority or Git facts explicitly before requesting cleanup again",
	}
}

func encodeWorkspaceMaintenanceFailure(stdout io.Writer, err error) int {
	var diagnostic *execution.DiagnosticError
	if !errors.As(err, &diagnostic) {
		diagnostic = execution.NewDiagnostic(execution.KindGitFailure, "workspace maintenance failed", execution.RetryDecisionRequired, "", "", nil, nil, nil, time.Now().UTC())
		diagnostic.Cause = err
	}
	return encodeWorkspaceMaintenanceResponse(stdout, WorkspaceMaintenanceResponse{Error: diagnostic})
}

func encodeWorkspaceMaintenanceResponse(stdout io.Writer, response WorkspaceMaintenanceResponse) int {
	if err := json.NewEncoder(stdout).Encode(response); err != nil {
		return 1
	}
	return 0
}
