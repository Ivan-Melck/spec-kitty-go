package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	workspacepkg "github.com/spec-kitty/go-kitty3/internal/workspace"
)

func runStartRun(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("start-run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var dbPath, trackerPath, primary, workspaceRoot, barrier string
	var correlation, at, operation, project, repository, mission, revision, work, lane, actor, process, workspaceID string
	var branch, base, provider, trackerWork, assignee, dependencyText string
	var editFile, editContent string
	var providerRevision int64
	var schemaVersion uint
	var ttl time.Duration
	flags.StringVar(&dbPath, "db", "", "control database path")
	flags.StringVar(&trackerPath, "tracker-store", "", "fake tracker store path")
	flags.StringVar(&primary, "repository", "", "primary Git checkout root")
	flags.StringVar(&workspaceRoot, "workspace-root", "", "managed workspace root")
	flags.StringVar(&barrier, "barrier", "", "fault-controller directory")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.UintVar(&schemaVersion, "schema-version", 0, "command schema version")
	flags.StringVar(&correlation, "correlation-id", "", "correlation ID")
	flags.StringVar(&at, "at", "", "injected RFC3339 timestamp")
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&repository, "repository-id", "", "repository ID")
	flags.StringVar(&mission, "mission", "", "mission ID")
	flags.StringVar(&revision, "revision", "", "work revision ID")
	flags.StringVar(&work, "work", "", "work ID")
	flags.StringVar(&lane, "lane", "", "lane ID")
	flags.StringVar(&actor, "actor", "", "actor ID")
	flags.StringVar(&process, "process", "", "process ID")
	flags.StringVar(&workspaceID, "workspace", "", "workspace ID")
	flags.StringVar(&branch, "branch", "", "owned branch")
	flags.StringVar(&base, "base", "", "pinned base commit")
	flags.StringVar(&provider, "provider", "fake", "tracker provider name")
	flags.StringVar(&trackerWork, "tracker-work", "", "tracker work key")
	flags.Int64Var(&providerRevision, "tracker-revision", 1, "expected tracker revision")
	flags.StringVar(&assignee, "assignee", "", "tracker assignee")
	flags.StringVar(&dependencyText, "dependencies", "", "comma-separated WorkID=commit pairs")
	flags.StringVar(&editFile, "edit-file", "", "optional safe workspace-relative file to commit")
	flags.StringVar(&editContent, "edit-content", "", "optional committed file content")
	flags.DurationVar(&ttl, "ttl", time.Minute, "lease lifetime")
	if err := flags.Parse(args); err != nil {
		return encodeStartFailure(stdout, execution.KindInvalidCommand, "parse start-run flags", operation, correlation, actor, process, time.Time{}, err)
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeStartFailure(stdout, execution.KindInvalidCommand, "at must be an RFC3339 timestamp", operation, correlation, actor, process, time.Time{}, err)
	}
	now = now.UTC()
	if schemaVersion != uint(execution.ResultSchemaVersion) || correlation == "" {
		return encodeStartFailure(stdout, execution.KindInvalidCommand, "schema-version and correlation-id are required", operation, correlation, actor, process, now, nil)
	}
	parsed, err := parseStartIDs(operation, project, repository, mission, revision, work, lane, actor, process, workspaceID)
	if err != nil {
		return encodeStartFailure(stdout, execution.KindInvalidCommand, "start-run ID is invalid", operation, correlation, actor, process, now, err)
	}
	dependencies, err := parseDependencies(dependencyText)
	if err != nil {
		return encodeFailure(stdout, execution.NewDiagnostic(execution.KindInvalidCommand, err.Error(), execution.RetryNever, correlation, parsed.operation, nil, map[string]string{"dependencies": dependencyText}, map[string]string{"dependencies": "WorkID=commit pairs"}, now))
	}
	governanceContext, err := syntheticStartGovernanceContext(now)
	if err != nil {
		return encodeStartFailure(stdout, execution.KindInvalidCommand, "construct synthetic Phase 1 governance context", operation, correlation, actor, process, now, err)
	}
	repositoryAdapter, err := gitrepo.Bind(ctx, gitrepo.Runner{}, parsed.repository, parsed.project, primary, workspaceRoot, "refs/heads/main", now)
	if err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(parsed.operation, correlation, err, now))
	}
	if err := repositoryAdapter.ValidateExternalPath(dbPath); err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(parsed.operation, correlation, err, now))
	}
	if err := repositoryAdapter.ValidateExternalPath(trackerPath); err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(parsed.operation, correlation, err, now))
	}
	if err := repositoryAdapter.PrepareManagedRoot(); err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(parsed.operation, correlation, err, now))
	}
	dbPath, err = repositoryAdapter.PrepareExternalPath(dbPath)
	if err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(parsed.operation, correlation, err, now))
	}
	trackerPath, err = repositoryAdapter.PrepareExternalPath(trackerPath)
	if err != nil {
		return encodeFailure(stdout, executionWorkspaceFailure(parsed.operation, correlation, err, now))
	}
	control, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeStartFailure(stdout, execution.KindControlStoreBusy, "open control store", operation, correlation, actor, process, now, err)
	}
	defer control.Close()
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		return encodeStartFailure(stdout, execution.KindWorkspaceNotReady, "open tracker store", operation, correlation, actor, process, now, err)
	}
	faultController := faults.FileController{Directory: barrier, Participant: parsed.process.String()}
	workspaceService := workspacepkg.NewService(control, repositoryAdapter).WithFaults(faultController)
	authority := execution.NewService(control, execution.RandomIdentities{}, execution.SystemClock{}).WithFaults(faultController)
	trackerSaga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).WithFaults(faultController)
	saga := execution.NewStartSaga(authority, trackerSaga, workspaceService)
	request := execution.StartRunRequest{
		SchemaVersion: uint16(schemaVersion),
		Claim:         execution.StartRequest{CorrelationID: correlation, OperationID: parsed.operation, ProjectID: parsed.project, MissionID: parsed.mission, WorkRevisionID: parsed.revision, WorkID: parsed.work, ActorID: parsed.actor, ProcessID: parsed.process, Now: now, LeaseTTL: ttl, GovernanceContext: governanceContext},
		Assignment:    execution.TrackerAssignRequest{SchemaVersion: uint16(schemaVersion), CorrelationID: correlation, OperationID: parsed.operation, ActorID: parsed.actor, ProcessID: parsed.process, Now: now, Provider: provider, WorkKey: trackerWork, ExpectedRevision: tracker.Revision(providerRevision), Assignee: assignee},
		Workspace:     execution.WorkspaceRequest{RepositoryID: parsed.repository, LaneID: parsed.lane, WorkspaceID: parsed.workspace, OwnedBranch: branch, PinnedBase: base, Dependencies: dependencies},
	}
	capsule, err := saga.Start(ctx, request)
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		var diagnostic *execution.DiagnosticError
		if !errors.As(err, &diagnostic) {
			return encodeStartFailure(stdout, execution.KindWorkspaceNotReady, "start-run failed", operation, correlation, actor, process, now, err)
		}
		return encodeFailure(stdout, diagnostic)
	}
	response := Response{OK: true, Capsule: &capsule}
	if editFile != "" {
		work, err := commitWorkspaceEdit(ctx, faultController, capsule, editFile, editContent)
		if errors.Is(err, faults.ErrCrashRequested) {
			return 86
		}
		if err != nil {
			return encodeStartFailure(stdout, execution.KindGitFailure, "commit workspace edit", operation, correlation, actor, process, now, err)
		}
		response.Work = &work
	}
	return encodeJSON(stdout, response)
}

func syntheticStartGovernanceContext(createdAt time.Time) (governancecontext.Context, error) {
	commitment, err := governancecontext.CommitSHA256([]byte("go-kitty3/phase1-helper/synthetic-governance/v1"))
	if err != nil {
		return governancecontext.Context{}, err
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindConfig, "phase1-helper-synthetic", "v1", commitment)
	if err != nil {
		return governancecontext.Context{}, err
	}
	aggregate, err := governancecontext.ComputeAggregate(governancecontext.SchemaVersion, []governancecontext.Entry{entry})
	if err != nil {
		return governancecontext.Context{}, err
	}
	contextID, err := ids.ParseGovernanceContextID("context-synthetic-" + aggregate.Digest())
	if err != nil {
		return governancecontext.Context{}, err
	}
	return governancecontext.NewContext(contextID, governancecontext.SchemaVersion, []governancecontext.Entry{entry}, createdAt)
}

func commitWorkspaceEdit(ctx context.Context, controller faults.FileController, capsule execution.RunCapsule, name, content string) (WorkspaceCommit, error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return WorkspaceCommit{}, execution.NewDiagnostic(execution.KindPathUnsafe, "edit file must be one safe workspace-relative path component", execution.RetryNever, capsule.CorrelationID, capsule.OperationID, capsule.AggregateIDs, map[string]string{"edit_file": name}, map[string]string{"edit_file": "safe path component"}, time.Now().UTC())
	}
	result := WorkspaceCommit{StartedAt: time.Now().UTC()}
	if err := controller.Reach(ctx, "WORK"); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(capsule.WorkspacePath, name), []byte(content), 0o600); err != nil {
		return result, err
	}
	if err := controller.Reach(ctx, "EDIT"); err != nil {
		return result, err
	}
	runner := gitrepo.Runner{}
	if _, err := runner.Run(ctx, capsule.WorkspacePath, "add", "--", name); err != nil {
		return result, err
	}
	commit, err := runner.Run(ctx, capsule.WorkspacePath, "commit", "-m", "Implement "+capsule.WorkID.String())
	if err != nil {
		return result, err
	}
	_ = commit
	result.Commit, err = runner.Run(ctx, capsule.WorkspacePath, "rev-parse", "HEAD")
	result.EndedAt = time.Now().UTC()
	return result, err
}

type startIDs struct {
	operation  ids.OperationID
	project    ids.ProjectID
	repository ids.RepositoryID
	mission    ids.MissionID
	revision   ids.WorkRevisionID
	work       ids.WorkID
	lane       ids.LaneID
	actor      ids.ActorID
	process    ids.ProcessID
	workspace  ids.WorkspaceID
}

func parseStartIDs(operation, project, repository, mission, revision, work, lane, actor, process, workspaceID string) (startIDs, error) {
	var result startIDs
	var err error
	if result.operation, err = ids.ParseOperationID(operation); err != nil {
		return result, err
	}
	if result.project, err = ids.ParseProjectID(project); err != nil {
		return result, err
	}
	if result.repository, err = ids.ParseRepositoryID(repository); err != nil {
		return result, err
	}
	if result.mission, err = ids.ParseMissionID(mission); err != nil {
		return result, err
	}
	if result.revision, err = ids.ParseWorkRevisionID(revision); err != nil {
		return result, err
	}
	if result.work, err = ids.ParseWorkID(work); err != nil {
		return result, err
	}
	if result.lane, err = ids.ParseLaneID(lane); err != nil {
		return result, err
	}
	if result.actor, err = ids.ParseActorID(actor); err != nil {
		return result, err
	}
	if result.process, err = ids.ParseProcessID(process); err != nil {
		return result, err
	}
	if result.workspace, err = ids.ParseWorkspaceID(workspaceID); err != nil {
		return result, err
	}
	return result, nil
}

func parseDependencies(value string) ([]workspacepkg.Dependency, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	dependencies := make([]workspacepkg.Dependency, 0, len(parts))
	for _, part := range parts {
		pair := strings.SplitN(part, "=", 2)
		if len(pair) != 2 {
			return nil, fmt.Errorf("dependency %q is not WorkID=commit", part)
		}
		workID, err := ids.ParseWorkID(pair[0])
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, workspacepkg.Dependency{WorkID: workID, Candidate: pair[1]})
	}
	return workspacepkg.CanonicalDependencies(dependencies)
}

func executionWorkspaceFailure(operation ids.OperationID, correlation string, err error, now time.Time) error {
	var typed *workspacepkg.Error
	if !errors.As(err, &typed) {
		diagnostic := execution.NewDiagnostic(execution.KindWorkspaceNotReady, "workspace operation failed", execution.RetrySafeSameOperation, correlation, operation, nil, nil, nil, now)
		diagnostic.Cause = err
		return diagnostic
	}
	kind := execution.KindWorkspaceNotReady
	switch typed.Kind {
	case workspacepkg.KindInvalid:
		kind = execution.KindInvalidCommand
	case workspacepkg.KindBindingConflict:
		kind = execution.KindRepositoryConflict
	case workspacepkg.KindPathUnsafe:
		kind = execution.KindPathUnsafe
	case workspacepkg.KindTargetMoved:
		kind = execution.KindTargetChanged
	case workspacepkg.KindDependencyConflict:
		kind = execution.KindDependencyConflict
	case workspacepkg.KindStaleAuthority:
		kind = execution.KindStaleAuthority
	case workspacepkg.KindGitFailure:
		kind = execution.KindGitFailure
	}
	diagnostic := execution.NewDiagnostic(kind, typed.Message, execution.RetrySafeSameOperation, correlation, operation, nil, typed.Observed, typed.Expected, now)
	diagnostic.Cause = err
	return diagnostic
}

func encodeStartFailure(stdout io.Writer, kind execution.ErrorKind, message, operation, correlation, actor, process string, now time.Time, cause error) int {
	operationID, _ := ids.ParseOperationID(operation)
	diagnostic := execution.NewDiagnostic(kind, message, execution.RetryNever, correlation, operationID, map[string]string{"operation_id": operation, "correlation_id": correlation, "actor_id": actor, "process_id": process}, map[string]string{}, map[string]string{"command": "complete valid start-run envelope"}, now)
	diagnostic.Cause = cause
	return encodeFailure(stdout, diagnostic)
}

func encodeJSON(stdout io.Writer, value any) int {
	if err := json.NewEncoder(stdout).Encode(value); err != nil {
		_, _ = fmt.Fprintln(stdout, err.Error())
		return 1
	}
	return 0
}
