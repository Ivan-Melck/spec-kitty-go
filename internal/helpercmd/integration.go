package helpercmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func runIntegrate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("integrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var database, primary, managedRoot, barrier, correlation, at string
	var operation, reviewID, project, repository, targetRef, actor, process, candidate string
	var schema uint
	var ttl time.Duration
	flags.StringVar(&database, "db", "", "control database")
	flags.StringVar(&primary, "repository", "", "primary Git checkout root")
	flags.StringVar(&managedRoot, "workspace-root", "", "managed workspace root")
	flags.StringVar(&barrier, "barrier", "", "fault boundary directory")
	flags.UintVar(&schema, "schema-version", 0, "command schema version")
	flags.StringVar(&correlation, "correlation-id", "", "correlation ID")
	flags.StringVar(&at, "at", "", "RFC3339 timestamp")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&reviewID, "review-id", "", "approved review ID")
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&repository, "repository-id", "", "repository ID")
	flags.StringVar(&targetRef, "target-ref", "refs/heads/main", "explicit target ref")
	flags.StringVar(&actor, "actor", "", "integration actor ID")
	flags.StringVar(&process, "process", "", "integration process ID")
	flags.StringVar(&candidate, "candidate", "", "exact approved candidate commit")
	flags.DurationVar(&ttl, "ttl", time.Minute, "target mutation lease lifetime")
	if err := flags.Parse(args); err != nil {
		return encodeIntegrationFailure(stdout, integrationContext(operation, correlation, reviewID, repository, targetRef, actor, process, candidate, time.Time{}), &integration.Error{Kind: integration.KindInvalid, Message: "parse integration flags", Cause: err})
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeIntegrationFailure(stdout, integrationContext(operation, correlation, reviewID, repository, targetRef, actor, process, candidate, time.Time{}), &integration.Error{Kind: integration.KindInvalid, Message: "at must be an RFC3339 timestamp", Cause: err})
	}
	request := integration.Request{SchemaVersion: uint16(schema), CorrelationID: correlation, OperationID: ids.OperationID(operation), ReviewID: ids.ReviewID(reviewID), RepositoryID: ids.RepositoryID(repository), TargetRef: targetRef, ActorID: ids.ActorID(actor), ProcessID: ids.ProcessID(process), Candidate: candidate, LeaseTTL: ttl, StartedAt: now.UTC()}
	if err := request.Validate(); err != nil {
		return encodeIntegrationFailure(stdout, integrationRequestContext(request), err)
	}
	projectID, err := ids.ParseProjectID(project)
	if err != nil {
		return encodeIntegrationFailure(stdout, integrationRequestContext(request), &integration.Error{Kind: integration.KindInvalid, Message: "project ID is invalid", Cause: err})
	}
	commandContext := integrationRequestContext(request)
	commandContext.aggregates["project_id"] = projectID.String()
	repositoryAdapter, err := gitrepo.Bind(ctx, gitrepo.Runner{}, request.RepositoryID, projectID, primary, managedRoot, request.TargetRef, now)
	if err != nil {
		return encodeIntegrationFailure(stdout, commandContext, err)
	}
	if err := repositoryAdapter.ValidateExternalPath(database); err != nil {
		return encodeIntegrationFailure(stdout, commandContext, err)
	}
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		return encodeIntegrationFailure(stdout, commandContext, &integration.Error{Kind: integration.KindStoreFailure, Message: "open control store", Cause: err})
	}
	defer store.Close()
	commandContext = enrichIntegrationReviewContext(ctx, store, request, commandContext)
	controller := faults.FileController{Directory: barrier, Participant: process}
	record, err := integration.NewService(store, repositoryAdapter).WithFaults(controller).Integrate(ctx, request)
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		if durable, found, lookupErr := store.LookupIntegrationOperation(ctx, request); lookupErr == nil && found {
			commandContext = enrichIntegrationRecordContext(commandContext, durable)
		}
		return encodeIntegrationFailure(stdout, commandContext, err)
	}
	commandContext = enrichIntegrationRecordContext(commandContext, record)
	result := IntegrationResult{
		SchemaVersion: execution.ResultSchemaVersion, Kind: "integration_done", CorrelationID: request.CorrelationID, OperationID: request.OperationID,
		AggregateIDs:  commandContext.aggregates,
		ObservedState: map[string]string{"target": record.ObservedTarget, "result": record.ResultCommit, "status": string(record.Status)},
		ExpectedState: map[string]string{"candidate": request.Candidate, "target_update": "expected-old compare"}, RetrySafety: execution.RetrySafeSameOperation,
		RecoveryActions: []string{"retry the same operation ID; recovery probes Git facts before durable phase"}, OccurredAt: record.DoneAt, Record: record,
	}
	return encodeJSON(stdout, Response{OK: true, Integration: &result})
}

type integrationCommandContext struct {
	operation   ids.OperationID
	correlation string
	occurredAt  time.Time
	aggregates  map[string]string
}

func integrationContext(operation, correlation, reviewID, repository, targetRef, actor, process, candidate string, at time.Time) integrationCommandContext {
	return integrationCommandContext{operation: ids.OperationID(operation), correlation: correlation, occurredAt: at, aggregates: map[string]string{
		"operation_id": operation, "review_id": reviewID, "repository_id": repository, "target_ref": targetRef,
		"actor_id": actor, "process_id": process, "candidate": candidate,
	}}
}

func integrationRequestContext(request integration.Request) integrationCommandContext {
	return integrationContext(request.OperationID.String(), request.CorrelationID, request.ReviewID.String(), request.RepositoryID.String(), request.TargetRef, request.ActorID.String(), request.ProcessID.String(), request.Candidate, request.StartedAt)
}

func enrichIntegrationReviewContext(ctx context.Context, store *sqlite.Store, request integration.Request, command integrationCommandContext) integrationCommandContext {
	reviewRecord, err := store.Review(ctx, request.ReviewID)
	if err != nil {
		return command
	}
	command.aggregates["handoff_id"] = reviewRecord.HandoffID.String()
	command.aggregates["work_revision_id"] = reviewRecord.WorkRevisionID.String()
	command.aggregates["work_id"] = reviewRecord.WorkID.String()
	if handoffRecord, err := store.Handoff(ctx, reviewRecord.HandoffID); err == nil {
		command.aggregates["mission_id"] = handoffRecord.MissionID.String()
		command.aggregates["lane_id"] = handoffRecord.LaneID.String()
	}
	return command
}

func enrichIntegrationRecordContext(command integrationCommandContext, record integration.Record) integrationCommandContext {
	command.aggregates["handoff_id"] = record.HandoffID.String()
	command.aggregates["mission_id"] = record.MissionID.String()
	command.aggregates["work_revision_id"] = record.WorkRevisionID.String()
	command.aggregates["work_id"] = record.WorkID.String()
	command.aggregates["mutation_generation"] = fmt.Sprint(record.MutationGeneration)
	if record.WorkspacePath != "" {
		command.aggregates["workspace_path"] = record.WorkspacePath
	}
	if record.ResultCommit != "" {
		command.aggregates["result_commit"] = record.ResultCommit
	}
	return command
}

func encodeIntegrationFailure(stdout io.Writer, command integrationCommandContext, cause error) int {
	kind := execution.KindInvalidCommand
	retry := execution.RetryNever
	observed := map[string]string{"command": "rejected"}
	expected := map[string]string{"command": "accepted under current approved integration authority"}
	message := cause.Error()
	var typed *integration.Error
	if errors.As(cause, &typed) {
		kind = execution.ErrorKind(typed.Kind)
		message = typed.Message
		if len(typed.Observed) != 0 {
			observed = typed.Observed
		}
		if len(typed.Expected) != 0 {
			expected = typed.Expected
		}
		switch typed.Kind {
		case integration.KindAuthorityBusy:
			retry = execution.RetrySafeSameOperation
		case integration.KindAuthorityExpired, integration.KindTargetMoved, integration.KindConflict, integration.KindRepairRequired, integration.KindRevisionIneligible:
			retry = execution.RetryDecisionRequired
		}
	} else {
		var workspaceError *workspace.Error
		if errors.As(cause, &workspaceError) {
			kind = map[workspace.ErrorKind]execution.ErrorKind{
				workspace.KindInvalid: execution.KindInvalidCommand, workspace.KindBindingConflict: execution.KindRepositoryConflict,
				workspace.KindWorkspaceConflict: execution.KindWorkspaceNotReady, workspace.KindWorkspaceNotReady: execution.KindWorkspaceNotReady,
				workspace.KindPathUnsafe: execution.KindPathUnsafe, workspace.KindTargetMoved: execution.KindTargetChanged,
				workspace.KindDependencyConflict: execution.KindDependencyConflict, workspace.KindStaleAuthority: execution.KindStaleAuthority,
				workspace.KindGitFailure: execution.KindGitFailure,
			}[workspaceError.Kind]
			if kind == "" {
				kind = execution.KindWorkspaceNotReady
			}
			message = workspaceError.Message
			if len(workspaceError.Observed) != 0 {
				observed = workspaceError.Observed
			}
			if len(workspaceError.Expected) != 0 {
				expected = workspaceError.Expected
			}
			retry = execution.RetryDecisionRequired
		}
	}
	diagnostic := execution.NewDiagnostic(kind, message, retry, command.correlation, command.operation, command.aggregates, observed, expected, command.occurredAt)
	diagnostic.Cause = cause
	diagnostic.RecoveryActions = integrationRecoveryActions(typed)
	if typed == nil && kind != execution.KindInvalidCommand {
		diagnostic.RecoveryActions = []string{"inspect the reported repository and managed-workspace facts", "retry only after the typed Git or path condition is resolved"}
	}
	return encodeFailure(stdout, diagnostic)
}

func integrationRecoveryActions(typed *integration.Error) []string {
	if typed == nil {
		return []string{"correct the command and retry with a new OperationID"}
	}
	switch typed.Kind {
	case integration.KindAuthorityBusy:
		return []string{"wait for the bounded target-ref mutation lease, then retry"}
	case integration.KindTargetMoved, integration.KindConflict, integration.KindRepairRequired:
		return []string{"inspect preserved Git evidence", "revalidate against the observed target before a new operation"}
	case integration.KindAuthorityExpired:
		return []string{"start a replacement operation with a fresh lease-scoped integration workspace"}
	default:
		return []string{fmt.Sprintf("resolve %s and retry safely", typed.Kind)}
	}
}
