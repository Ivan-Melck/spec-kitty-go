package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type WorkGraphResult struct {
	SchemaVersion   uint16                         `json:"schema_version"`
	Kind            string                         `json:"kind"`
	CorrelationID   string                         `json:"correlation_id"`
	OperationID     ids.OperationID                `json:"operation_id"`
	AggregateIDs    map[string]string              `json:"aggregate_ids"`
	ObservedState   map[string]string              `json:"observed_state"`
	ExpectedState   map[string]string              `json:"expected_state"`
	RetrySafety     execution.RetrySafety          `json:"retry_safety"`
	RecoveryActions []string                       `json:"recovery_actions"`
	Evidence        *sqlite.ReconciliationEvidence `json:"evidence,omitempty"`
	Consequence     *workgraph.Consequence         `json:"consequence,omitempty"`
}

type WorkGraphResponse struct {
	OK     bool                       `json:"ok"`
	Result *WorkGraphResult           `json:"result,omitempty"`
	Error  *execution.DiagnosticError `json:"error,omitempty"`
}

type graphCommand struct {
	database      string
	operationText string
	correlation   string
	schemaVersion uint
	actorText     string
	processText   string
	occurredText  string
	operationID   ids.OperationID
	actorID       ids.ActorID
	processID     ids.ProcessID
	occurredAt    time.Time
}

func RunWorkGraph(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return encodeGraphUsage(stdout, "", "", "workgraph requires finalize, reconcile, evidence, or consequence")
	}
	switch args[0] {
	case "finalize":
		return runFinalizeWorkRevision(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return runReconcileWorkRevision(ctx, args[1:], stdout, stderr)
	case "evidence", "consequence":
		return runInspectWorkGraph(ctx, args[0], args[1:], stdout, stderr)
	default:
		return encodeGraphUsage(stdout, "", "", "unknown workgraph command")
	}
}

func graphFlags(name string, stderr io.Writer) (*flag.FlagSet, *graphCommand) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	command := &graphCommand{}
	flags.StringVar(&command.database, "db", "", "control database path")
	flags.StringVar(&command.operationText, "operation", "", "OperationID")
	flags.StringVar(&command.correlation, "correlation", "", "correlation ID")
	return flags, command
}

func mutatingGraphFlags(flags *flag.FlagSet, command *graphCommand) {
	flags.UintVar(&command.schemaVersion, "schema-version", 0, "command schema version")
	flags.StringVar(&command.actorText, "actor", "", "ActorID")
	flags.StringVar(&command.processText, "process", "", "ProcessID")
	flags.StringVar(&command.occurredText, "at", "", "RFC3339Nano command timestamp")
}

func (c *graphCommand) validate(stdout io.Writer, mutating bool) int {
	operationID, err := ids.ParseOperationID(c.operationText)
	if err != nil {
		return encodeGraphDiagnostic(stdout, graphDiagnostic(c.operationText, c.correlation, execution.KindInvalidCommand, "operation_id is invalid", execution.RetryNever,
			map[string]string{"operation_id": c.operationText}, map[string]string{"operation_id": "invalid"}, map[string]string{"operation_id": "portable ID"}, err))
	}
	c.operationID = operationID
	if c.correlation == "" {
		c.correlation = operationID.String()
	}
	if c.database == "" {
		return encodeGraphDiagnostic(stdout, graphDiagnostic(operationID.String(), c.correlation, execution.KindInvalidCommand, "db is required", execution.RetryNever,
			map[string]string{"operation_id": operationID.String()}, map[string]string{"db": "missing"}, map[string]string{"db": "control database path"}, nil))
	}
	if !mutating {
		return -1
	}
	if c.schemaVersion != uint(execution.ResultSchemaVersion) {
		return encodeGraphDiagnostic(stdout, graphDiagnostic(operationID.String(), c.correlation, execution.KindInvalidCommand, "schema_version is unsupported", execution.RetryNever,
			map[string]string{"operation_id": operationID.String(), "schema_version": strconv.FormatUint(uint64(c.schemaVersion), 10), "actor_id": c.actorText, "process_id": c.processText}, map[string]string{"schema_version": strconv.FormatUint(uint64(c.schemaVersion), 10)}, map[string]string{"schema_version": strconv.FormatUint(uint64(execution.ResultSchemaVersion), 10)}, nil))
	}
	actorID, err := ids.ParseActorID(c.actorText)
	if err != nil {
		return encodeGraphDiagnostic(stdout, graphDiagnostic(operationID.String(), c.correlation, execution.KindInvalidCommand, "actor_id is invalid", execution.RetryNever,
			map[string]string{"operation_id": operationID.String(), "actor_id": c.actorText, "process_id": c.processText}, map[string]string{"actor_id": "invalid"}, map[string]string{"actor_id": "portable ID"}, err))
	}
	processID, err := ids.ParseProcessID(c.processText)
	if err != nil {
		return encodeGraphDiagnostic(stdout, graphDiagnostic(operationID.String(), c.correlation, execution.KindInvalidCommand, "process_id is invalid", execution.RetryNever,
			map[string]string{"operation_id": operationID.String(), "actor_id": c.actorText, "process_id": c.processText}, map[string]string{"process_id": "invalid"}, map[string]string{"process_id": "portable ID"}, err))
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, c.occurredText)
	if err != nil {
		return encodeGraphDiagnostic(stdout, graphDiagnostic(operationID.String(), c.correlation, execution.KindInvalidCommand, "occurred_at is invalid", execution.RetryNever,
			map[string]string{"operation_id": operationID.String(), "actor_id": c.actorText, "process_id": c.processText}, map[string]string{"occurred_at": c.occurredText}, map[string]string{"occurred_at": time.RFC3339Nano}, err))
	}
	c.actorID, c.processID, c.occurredAt = actorID, processID, occurredAt.UTC()
	return -1
}

func runFinalizeWorkRevision(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags, command := graphFlags("workgraph finalize", stderr)
	var revisionJSON string
	var expectedPrevious int64
	mutatingGraphFlags(flags, command)
	flags.StringVar(&revisionJSON, "revision-json", "", "encoded immutable WorkRevision")
	flags.Int64Var(&expectedPrevious, "expected-previous", -1, "optimistic previous revision number")
	if err := flags.Parse(args); err != nil {
		return encodeGraphUsage(stdout, command.operationText, command.correlation, err.Error())
	}
	if code := command.validate(stdout, true); code >= 0 {
		return code
	}
	revision, err := workgraph.DecodeRevision([]byte(revisionJSON))
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"work_revision": "invalid"}, map[string]string{"work_revision": "canonical encoded revision"})
	}
	if revision.ActorID() != command.actorID || !revision.FinalizedAt().Equal(command.occurredAt) {
		return encodeWorkGraphFailure(stdout, *command, invalidGraphInput("finalize actor or timestamp", nil),
			map[string]string{"actor_id": command.actorID.String(), "occurred_at": command.occurredAt.Format(time.RFC3339Nano)},
			map[string]string{"actor_id": revision.ActorID().String(), "occurred_at": revision.FinalizedAt().Format(time.RFC3339Nano)})
	}
	store, err := sqlite.Open(ctx, command.database)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"control_store": "unavailable"}, map[string]string{"control_store": "open"})
	}
	defer store.Close()
	result, err := store.FinalizeWorkRevision(ctx, sqlite.FinalizeWorkRevisionRequest{
		SchemaVersion: uint16(command.schemaVersion), CorrelationID: command.correlation, OperationID: command.operationID,
		ActorID: command.actorID, ProcessID: command.processID, OccurredAt: command.occurredAt,
		Revision: revision, ExpectedPreviousNumber: expectedPrevious,
	})
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err,
			map[string]string{"previous_revision_number": strconv.FormatInt(expectedPrevious, 10)},
			map[string]string{"next_revision_number": strconv.FormatInt(expectedPrevious+1, 10)})
	}
	return encodeWorkGraphSuccess(stdout, WorkGraphResult{
		Kind: "work_revision_finalized", CorrelationID: command.correlation, OperationID: command.operationID,
		AggregateIDs:  map[string]string{"operation_id": command.operationID.String(), "work_revision_id": result.WorkRevisionID.String(), "project_id": result.ProjectID.String(), "mission_id": result.MissionID.String(), "actor_id": command.actorID.String(), "process_id": command.processID.String()},
		ObservedState: map[string]string{"revision_number": strconv.FormatInt(result.RevisionNumber, 10), "graph_digest": result.GraphDigest, "criteria_digest": result.CriteriaDigest, "schema_version": strconv.FormatUint(uint64(command.schemaVersion), 10), "occurred_at": command.occurredAt.Format(time.RFC3339Nano)},
		ExpectedState: map[string]string{"previous_revision_number": strconv.FormatInt(expectedPrevious, 10)}, RecoveryActions: []string{"inspect the immutable WorkRevision by ID"},
	})
}

func runReconcileWorkRevision(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags, command := graphFlags("workgraph reconcile", stderr)
	var oldRevisionText, newRevisionText, dispositionText, rationale, oldWorkText, newWorkText, forkEdgeText string
	mutatingGraphFlags(flags, command)
	flags.StringVar(&oldRevisionText, "old", "", "old WorkRevisionID")
	flags.StringVar(&newRevisionText, "new", "", "new WorkRevisionID")
	flags.StringVar(&dispositionText, "disposition", "", "continue, supersede, invalidate, or fork")
	flags.StringVar(&rationale, "rationale", "", "owner rationale")
	flags.StringVar(&oldWorkText, "old-work", "", "comma-separated old WorkIDs")
	flags.StringVar(&newWorkText, "new-work", "", "comma-separated new WorkIDs")
	flags.StringVar(&forkEdgeText, "fork-edges", "", "comma-separated old:new fork edges")
	if err := flags.Parse(args); err != nil {
		return encodeGraphUsage(stdout, command.operationText, command.correlation, err.Error())
	}
	if code := command.validate(stdout, true); code >= 0 {
		return code
	}
	oldRevisionID, err := ids.ParseWorkRevisionID(oldRevisionText)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, invalidGraphInput("old WorkRevisionID", err), map[string]string{"old_work_revision_id": oldRevisionText}, map[string]string{"old_work_revision_id": "portable ID"})
	}
	newRevisionID, err := ids.ParseWorkRevisionID(newRevisionText)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, invalidGraphInput("new WorkRevisionID", err), map[string]string{"new_work_revision_id": newRevisionText}, map[string]string{"new_work_revision_id": "portable ID"})
	}
	oldWorkIDs, err := parseWorkIDs(oldWorkText)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"old_work_ids": oldWorkText}, map[string]string{"old_work_ids": "portable IDs"})
	}
	newWorkIDs, err := parseWorkIDs(newWorkText)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"new_work_ids": newWorkText}, map[string]string{"new_work_ids": "portable IDs"})
	}
	forkEdges, err := parseForkEdges(forkEdgeText)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"fork_edges": forkEdgeText}, map[string]string{"fork_edges": "old:new pairs"})
	}
	store, err := sqlite.Open(ctx, command.database)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"control_store": "unavailable"}, map[string]string{"control_store": "open"})
	}
	defer store.Close()
	oldRevision, err := store.WorkRevision(ctx, oldRevisionID)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"old_work_revision_id": oldRevisionText}, map[string]string{"old_work_revision": "present"})
	}
	newRevision, err := store.WorkRevision(ctx, newRevisionID)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"new_work_revision_id": newRevisionText}, map[string]string{"new_work_revision": "present"})
	}
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
		ID: command.operationID.String(), Old: oldRevision, New: newRevision, Disposition: workgraph.Disposition(dispositionText), ActorID: command.actorID,
		Rationale: rationale, OldWorkIDs: oldWorkIDs, NewWorkIDs: newWorkIDs, ForkEdges: forkEdges, OccurredAt: command.occurredAt,
	})
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"reconciliation": "invalid"}, map[string]string{"reconciliation": "valid explicit disposition"})
	}
	if err := store.ReconcileWorkRevision(ctx, sqlite.ReconcileWorkRevisionRequest{
		SchemaVersion: uint16(command.schemaVersion), CorrelationID: command.correlation, OperationID: command.operationID,
		ActorID: command.actorID, ProcessID: command.processID, OccurredAt: command.occurredAt, Reconciliation: reconciliation,
	}); err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"request_digest": reconciliation.RequestDigest()}, map[string]string{"operation_id": "unused or identical request"})
	}
	evidence, err := store.Reconciliation(ctx, reconciliation.ID())
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"reconciliation": "committed but unreadable"}, map[string]string{"reconciliation": "queryable"})
	}
	return encodeWorkGraphSuccess(stdout, WorkGraphResult{
		Kind: "work_revision_reconciled", CorrelationID: command.correlation, OperationID: command.operationID,
		AggregateIDs:  map[string]string{"operation_id": command.operationID.String(), "reconciliation_id": reconciliation.ID(), "old_work_revision_id": oldRevisionText, "new_work_revision_id": newRevisionText, "actor_id": command.actorID.String(), "process_id": command.processID.String()},
		ObservedState: map[string]string{"disposition": dispositionText, "request_digest": evidence.RequestDigest, "schema_version": strconv.FormatUint(uint64(command.schemaVersion), 10), "occurred_at": command.occurredAt.Format(time.RFC3339Nano)}, ExpectedState: map[string]string{"reconciliation": "immutable and enforceable"},
		RecoveryActions: []string{"inspect recorded consequences before advancing old work"}, Evidence: &evidence,
	})
}

func runInspectWorkGraph(ctx context.Context, kind string, args []string, stdout, stderr io.Writer) int {
	flags, command := graphFlags("workgraph "+kind, stderr)
	var reconciliation, work, status string
	flags.StringVar(&reconciliation, "reconciliation", "", "reconciliation ID")
	flags.StringVar(&work, "work", "", "old WorkID")
	flags.StringVar(&status, "status", "", "active, handed_off, approved, or integrated")
	if err := flags.Parse(args); err != nil {
		return encodeGraphUsage(stdout, command.operationText, command.correlation, err.Error())
	}
	if code := command.validate(stdout, false); code >= 0 {
		return code
	}
	store, err := sqlite.Open(ctx, command.database)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"control_store": "unavailable"}, map[string]string{"control_store": "open"})
	}
	defer store.Close()
	if kind == "evidence" {
		evidence, err := store.Reconciliation(ctx, reconciliation)
		if err != nil {
			return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"reconciliation_id": reconciliation}, map[string]string{"reconciliation": "present"})
		}
		return encodeWorkGraphSuccess(stdout, WorkGraphResult{
			Kind: "reconciliation_evidence", CorrelationID: command.correlation, OperationID: command.operationID,
			AggregateIDs:  map[string]string{"operation_id": command.operationID.String(), "reconciliation_id": reconciliation},
			ObservedState: map[string]string{"disposition": string(evidence.Disposition), "request_digest": evidence.RequestDigest}, ExpectedState: map[string]string{"evidence": "immutable"},
			RecoveryActions: []string{"query a status-specific consequence before advancing old work"}, Evidence: &evidence,
		})
	}
	workID, err := ids.ParseWorkID(work)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, invalidGraphInput("WorkID", err), map[string]string{"work_id": work}, map[string]string{"work_id": "portable ID"})
	}
	workStatus := workgraph.WorkStatus(status)
	if workStatus != workgraph.StatusActive && workStatus != workgraph.StatusHandedOff && workStatus != workgraph.StatusApproved && workStatus != workgraph.StatusIntegrated {
		return encodeWorkGraphFailure(stdout, *command, invalidGraphInput("work status", nil), map[string]string{"work_status": status}, map[string]string{"work_status": "active, handed_off, approved, or integrated"})
	}
	consequence, err := store.ReconciliationConsequence(ctx, reconciliation, workID, workStatus)
	if err != nil {
		return encodeWorkGraphFailure(stdout, *command, err, map[string]string{"reconciliation_id": reconciliation, "work_id": work, "work_status": status}, map[string]string{"consequence": "present"})
	}
	return encodeWorkGraphSuccess(stdout, WorkGraphResult{
		Kind: "reconciliation_consequence", CorrelationID: command.correlation, OperationID: command.operationID,
		AggregateIDs:  map[string]string{"operation_id": command.operationID.String(), "reconciliation_id": reconciliation, "work_id": work},
		ObservedState: map[string]string{"work_status": status, "reason": consequence.Reason}, ExpectedState: map[string]string{"eligibility": "enforced by command gates"},
		RecoveryActions: []string{"obey the recorded eligibility flags"}, Consequence: &consequence,
	})
}

func parseWorkIDs(value string) ([]ids.WorkID, error) {
	if value == "" {
		return nil, invalidGraphInput("WorkIDs", nil)
	}
	parts := strings.Split(value, ",")
	result := make([]ids.WorkID, len(parts))
	for index, part := range parts {
		workID, err := ids.ParseWorkID(part)
		if err != nil {
			return nil, invalidGraphInput("WorkID", err)
		}
		result[index] = workID
	}
	return result, nil
}

func parseForkEdges(value string) ([]workgraph.ForkEdge, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	result := make([]workgraph.ForkEdge, len(parts))
	for index, part := range parts {
		ends := strings.Split(part, ":")
		if len(ends) != 2 {
			return nil, invalidGraphInput("fork edge", nil)
		}
		from, err := ids.ParseWorkID(ends[0])
		if err != nil {
			return nil, invalidGraphInput("fork edge source", err)
		}
		to, err := ids.ParseWorkID(ends[1])
		if err != nil {
			return nil, invalidGraphInput("fork edge target", err)
		}
		result[index] = workgraph.ForkEdge{FromOldWorkID: from, ToNewWorkID: to}
	}
	return result, nil
}

func invalidGraphInput(message string, cause error) error {
	return &workgraph.Error{Kind: workgraph.KindInvalidReconciliation, Message: message + " is invalid", Cause: cause}
}

func encodeWorkGraphSuccess(stdout io.Writer, result WorkGraphResult) int {
	result.SchemaVersion = execution.ResultSchemaVersion
	result.RetrySafety = execution.RetryNever
	if result.AggregateIDs == nil {
		result.AggregateIDs = map[string]string{}
	}
	if result.ObservedState == nil {
		result.ObservedState = map[string]string{}
	}
	if result.ExpectedState == nil {
		result.ExpectedState = map[string]string{}
	}
	if result.RecoveryActions == nil {
		result.RecoveryActions = []string{}
	}
	_ = json.NewEncoder(stdout).Encode(WorkGraphResponse{OK: true, Result: &result})
	return 0
}

func encodeWorkGraphFailure(stdout io.Writer, command graphCommand, err error, observed, expected map[string]string) int {
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		if diagnostic.CorrelationID == "" {
			diagnostic.CorrelationID = command.correlation
		}
		if diagnostic.OperationID == "" {
			diagnostic.OperationID = command.operationID
		}
		return encodeGraphDiagnostic(stdout, diagnostic)
	}
	var graphError *workgraph.Error
	kind := execution.KindInvalidCommand
	message := err.Error()
	aggregates := map[string]string{"operation_id": command.operationID.String()}
	if command.actorText != "" {
		aggregates["actor_id"] = command.actorText
	}
	if command.processText != "" {
		aggregates["process_id"] = command.processText
	}
	retry := execution.RetryNever
	if errors.As(err, &graphError) {
		switch graphError.Kind {
		case workgraph.KindNotFound:
			kind = execution.KindNotFound
		case workgraph.KindRevisionConflict, workgraph.KindReconciliationConflict:
			kind = execution.ErrorKind(graphError.Kind)
		default:
			kind = execution.KindInvalidCommand
		}
		message = graphError.Message
		for key, value := range graphError.Fields {
			aggregates[key] = value
		}
		if graphError.Kind == workgraph.KindRevisionConflict || graphError.Kind == workgraph.KindReconciliationConflict {
			retry = execution.RetryDecisionRequired
		}
	}
	return encodeGraphDiagnostic(stdout, graphDiagnostic(command.operationID.String(), command.correlation, kind, message, retry, aggregates, observed, expected, err))
}

func encodeGraphUsage(stdout io.Writer, operation, correlation, message string) int {
	return encodeGraphDiagnostic(stdout, graphDiagnostic(operation, correlation, execution.KindInvalidCommand, message, execution.RetryNever,
		map[string]string{"operation_id": operation}, map[string]string{"command": "invalid"}, map[string]string{"command": "valid workgraph invocation"}, nil))
}

func graphDiagnostic(operation, correlation string, kind execution.ErrorKind, message string, retry execution.RetrySafety, aggregates, observed, expected map[string]string, cause error) *execution.DiagnosticError {
	operationID, _ := ids.ParseOperationID(operation)
	diagnostic := execution.NewDiagnostic(kind, message, retry, correlation, operationID, aggregates, observed, expected, time.Now().UTC())
	diagnostic.Cause = cause
	return diagnostic
}

func encodeGraphDiagnostic(stdout io.Writer, diagnostic *execution.DiagnosticError) int {
	_ = json.NewEncoder(stdout).Encode(WorkGraphResponse{OK: false, Error: diagnostic})
	return 0
}
