package helpercmd

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	workspacepkg "github.com/spec-kitty/go-kitty3/internal/workspace"
)

type RecoveryResponse struct {
	OK       bool                             `json:"ok"`
	Decision *execution.StartRecoveryDecision `json:"decision,omitempty"`
	Error    *execution.DiagnosticError       `json:"error,omitempty"`
	Message  string                           `json:"message,omitempty"`
}

type ZombieResult struct {
	Commit           string                 `json:"commit"`
	HeartbeatRefusal execution.ErrorKind    `json:"heartbeat_refusal"`
	ExpireRefusal    execution.ErrorKind    `json:"expire_refusal"`
	WorkspaceRefusal workspacepkg.ErrorKind `json:"workspace_refusal"`
}

type ZombieResponse struct {
	OK      bool                       `json:"ok"`
	Result  *ZombieResult              `json:"result,omitempty"`
	Error   *execution.DiagnosticError `json:"error,omitempty"`
	Message string                     `json:"message,omitempty"`
}

func runRecoverStart(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("recover-start", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dbPath, trackerPath, primary, workspaceRoot, barrier, operation, project, repository, correlation, at string
	flags.StringVar(&dbPath, "db", "", "control database path")
	flags.StringVar(&trackerPath, "tracker-store", "", "fake tracker store path")
	flags.StringVar(&primary, "repository", "", "primary Git checkout root")
	flags.StringVar(&workspaceRoot, "workspace-root", "", "managed workspace root")
	flags.StringVar(&barrier, "barrier", "", "fault-controller directory")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&project, "project", "", "project ID used to authenticate the repository adapter")
	flags.StringVar(&repository, "repository-id", "", "repository ID used to authenticate the repository adapter")
	flags.StringVar(&correlation, "correlation-id", "", "recovery correlation ID")
	flags.StringVar(&at, "at", "", "injected RFC3339 timestamp")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	operationID, err := ids.ParseOperationID(operation)
	if err != nil {
		return encodeInvalidID(stdout, operation, "operation_id", err)
	}
	projectID, err := ids.ParseProjectID(project)
	if err != nil {
		return encodeInvalidID(stdout, operation, "project_id", err)
	}
	repositoryID, err := ids.ParseRepositoryID(repository)
	if err != nil {
		return encodeInvalidID(stdout, operation, "repository_id", err)
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeFailure(stdout, execution.NewDiagnostic(execution.KindInvalidCommand, "at must be RFC3339", execution.RetryNever, correlation, operationID, map[string]string{"operation_id": operation}, map[string]string{"at": at}, map[string]string{"at": "RFC3339"}, time.Now()))
	}
	adapter, err := gitrepo.Bind(ctx, gitrepo.Runner{}, repositoryID, projectID, primary, workspaceRoot, "refs/heads/main", now)
	if err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(operationID, correlation, err, now))
	}
	for _, path := range []string{dbPath, trackerPath} {
		if err := adapter.ValidateExternalPath(path); err != nil {
			return encodeFailure(stdout, executionWorkspaceFailure(operationID, correlation, err, now))
		}
	}
	if err := adapter.PrepareManagedRoot(); err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(operationID, correlation, err, now))
	}
	control, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	defer control.Close()
	record, err := control.LoadStartRun(ctx, operationID)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	if record.Request.Claim.ProjectID != projectID || record.Request.Workspace.RepositoryID != repositoryID {
		return encodeFailure(stdout, execution.NewDiagnostic(execution.KindRepositoryConflict, "recovery adapter identity differs from the operation envelope", execution.RetryDecisionRequired, correlation, operationID,
			map[string]string{"operation_id": operation}, map[string]string{"project_id": project, "repository_id": repository}, map[string]string{"project_id": record.Request.Claim.ProjectID.String(), "repository_id": record.Request.Workspace.RepositoryID.String()}, now))
	}
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	controller := faults.FileController{Directory: barrier, Participant: "recovery-" + operation}
	workspaceService := workspacepkg.NewService(control, adapter).WithFaults(controller)
	authority := execution.NewService(control, execution.RandomIdentities{}, execution.SystemClock{}).WithFaults(controller)
	trackerSaga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).WithFaults(controller)
	decision, err := execution.NewStartSaga(authority, trackerSaga, workspaceService).Recover(ctx, execution.StartRecoveryRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: correlation, OperationID: operationID, Now: now.UTC()})
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		var diagnostic *execution.DiagnosticError
		if errors.As(err, &diagnostic) {
			return encodeJSON(stdout, RecoveryResponse{OK: false, Error: diagnostic})
		}
		return encodeJSON(stdout, RecoveryResponse{OK: false, Message: err.Error()})
	}
	return encodeJSON(stdout, RecoveryResponse{OK: true, Decision: &decision})
}

func runExpireAuthority(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("expire-authority", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dbPath, correlation, operation, actor, process, lease, token, at string
	var generation int64
	flags.StringVar(&dbPath, "db", "", "control database path")
	flags.StringVar(&correlation, "correlation-id", "", "correlation ID")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&actor, "actor", "", "actor ID")
	flags.StringVar(&process, "process", "", "process ID")
	flags.StringVar(&lease, "lease", "", "lease ID")
	flags.Int64Var(&generation, "generation", 0, "lease generation")
	flags.StringVar(&token, "token", "", "fencing token")
	flags.StringVar(&at, "at", "", "injected RFC3339 timestamp")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	request := execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: correlation, OperationID: ids.OperationID(operation), ActorID: ids.ActorID(actor), ProcessID: ids.ProcessID(process), LeaseID: ids.LeaseID(lease), Generation: generation, Token: token, Now: now.UTC()}
	if err := request.Validate(); err != nil {
		return encodeFailure(stdout, err)
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	defer store.Close()
	if err := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{}).Expire(ctx, request); err != nil {
		return encodeFailure(stdout, err)
	}
	return encodeJSON(stdout, Response{OK: true})
}

func runZombieWrite(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("zombie-write", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dbPath, barrier, operation, token, name, content, at string
	flags.StringVar(&dbPath, "db", "", "control database path")
	flags.StringVar(&barrier, "barrier", "", "fault-controller directory")
	flags.StringVar(&operation, "operation", "", "start operation ID")
	flags.StringVar(&token, "token", "", "captured fencing token")
	flags.StringVar(&name, "file", "", "safe workspace-relative file")
	flags.StringVar(&content, "content", "", "file content")
	flags.StringVar(&at, "at", "", "post-expiry RFC3339 timestamp")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	operationID, err := ids.ParseOperationID(operation)
	if err != nil {
		return encodeInvalidID(stdout, operation, "operation_id", err)
	}
	if filepath.Base(name) != name || name == "." || name == ".." {
		return encodeFailure(stdout, execution.NewDiagnostic(execution.KindPathUnsafe, "zombie file must be one safe path component", execution.RetryNever, operation, operationID, nil, map[string]string{"file": name}, map[string]string{"file": "safe path component"}, time.Now()))
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	recovery, err := store.LoadStartRun(ctx, operationID)
	if err != nil {
		_ = store.Close()
		return encodeFailure(stdout, err)
	}
	record, err := store.Workspace(ctx, recovery.Request.Workspace.WorkspaceID)
	if err != nil {
		_ = store.Close()
		return encodeFailure(stdout, err)
	}
	if err := store.Close(); err != nil {
		return encodeFailure(stdout, err)
	}
	controller := faults.FileController{Directory: barrier, Participant: recovery.Request.Claim.ProcessID.String()}
	if err := controller.Reach(ctx, "ZOMBIE"); err != nil {
		return encodeFailure(stdout, err)
	}
	if err := os.WriteFile(filepath.Join(record.CanonicalPath, name), []byte(content), 0o600); err != nil {
		return encodeJSON(stdout, ZombieResponse{Message: err.Error()})
	}
	runner := gitrepo.Runner{}
	if _, err := runner.Run(ctx, record.CanonicalPath, "add", "--", name); err != nil {
		return encodeJSON(stdout, ZombieResponse{Message: err.Error()})
	}
	if _, err := runner.Run(ctx, record.CanonicalPath, "commit", "-m", "Zombie write after authority expiry"); err != nil {
		return encodeJSON(stdout, ZombieResponse{Message: err.Error()})
	}
	commit, err := runner.Run(ctx, record.CanonicalPath, "rev-parse", "HEAD")
	if err != nil {
		return encodeJSON(stdout, ZombieResponse{Message: err.Error()})
	}
	store, err = sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	defer store.Close()
	result := ZombieResult{Commit: commit}
	heartbeat := execution.HeartbeatRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "zombie-heartbeat", OperationID: operationID, ActorID: recovery.Request.Claim.ActorID, ProcessID: recovery.Request.Claim.ProcessID, LeaseID: recovery.LeaseID, Generation: recovery.Generation, Token: token, Now: now.UTC(), LeaseTTL: time.Minute}
	_, heartbeatErr := store.Heartbeat(ctx, heartbeat)
	result.HeartbeatRefusal = diagnosticKind(heartbeatErr)
	expire := execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "zombie-expire", OperationID: operationID, ActorID: heartbeat.ActorID, ProcessID: heartbeat.ProcessID, LeaseID: heartbeat.LeaseID, Generation: heartbeat.Generation, Token: heartbeat.Token, Now: heartbeat.Now}
	result.ExpireRefusal = diagnosticKind(store.Expire(ctx, expire))
	workspaceErr := store.QuarantineWorkspace(ctx, record.WorkspaceID, workspacepkg.Authority{LeaseID: recovery.LeaseID, Generation: recovery.Generation, Token: token}, "zombie stale mutation", now.UTC())
	var typedWorkspace *workspacepkg.Error
	if errors.As(workspaceErr, &typedWorkspace) {
		result.WorkspaceRefusal = typedWorkspace.Kind
	}
	return encodeJSON(stdout, ZombieResponse{OK: true, Result: &result})
}

func diagnosticKind(err error) execution.ErrorKind {
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		return diagnostic.Kind
	}
	return ""
}
