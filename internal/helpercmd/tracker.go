package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

type TrackerResponse struct {
	OK      bool                           `json:"ok"`
	Result  *execution.TrackerEffectResult `json:"result,omitempty"`
	Error   *execution.DiagnosticError     `json:"error,omitempty"`
	Message string                         `json:"message,omitempty"`
}

func RunTracker(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || (args[0] != "assign" && args[0] != "compensate") {
		fmt.Fprintln(stderr, "usage: tracker assign|compensate [options]")
		return 2
	}
	action := args[0]
	flags := flag.NewFlagSet("tracker "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var database, trackerPath, barrier, operation, correlation, actor, process, provider, workKey, assignee string
	var expectedRevision uint64
	flags.StringVar(&database, "db", "", "control database path")
	flags.StringVar(&trackerPath, "tracker", "", "fake tracker store path")
	flags.StringVar(&barrier, "barrier", "", "fault-controller directory")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&correlation, "correlation", "", "correlation ID")
	flags.StringVar(&actor, "actor", "", "actor ID")
	flags.StringVar(&process, "process", "", "process ID")
	flags.StringVar(&provider, "provider", "fake", "provider name")
	flags.StringVar(&workKey, "work-key", "", "provider work key")
	flags.StringVar(&assignee, "assignee", "", "assignment value")
	flags.Uint64Var(&expectedRevision, "expected-revision", 0, "expected provider revision")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	operationID, err := ids.ParseOperationID(operation)
	if err != nil {
		return encodeTrackerInvalid(stdout, correlation, operation, actor, process, provider, workKey, expectedRevision, "operation_id", err)
	}
	actorID, err := ids.ParseActorID(actor)
	if err != nil {
		return encodeTrackerInvalid(stdout, correlation, operation, actor, process, provider, workKey, expectedRevision, "actor_id", err)
	}
	processID, err := ids.ParseProcessID(process)
	if err != nil {
		return encodeTrackerInvalid(stdout, correlation, operation, actor, process, provider, workKey, expectedRevision, "process_id", err)
	}
	control, err := sqlite.Open(ctx, database)
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	defer control.Close()
	providerStore, err := fake.Open(trackerPath)
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	now := time.Now().UTC()
	saga := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).
		WithFaults(faults.FileController{Directory: barrier, Participant: processID.String()})
	var result execution.TrackerEffectResult
	if action == "assign" {
		result, err = saga.Assign(ctx, execution.TrackerAssignRequest{
			SchemaVersion: execution.ResultSchemaVersion, CorrelationID: correlation, OperationID: operationID,
			ActorID: actorID, ProcessID: processID, Now: now, Provider: provider, WorkKey: workKey,
			ExpectedRevision: tracker.Revision(expectedRevision), Assignee: assignee,
		})
	} else {
		result, err = saga.Compensate(ctx, execution.TrackerCompensateRequest{
			SchemaVersion: execution.ResultSchemaVersion, CorrelationID: correlation, OperationID: operationID,
			ActorID: actorID, ProcessID: processID, Now: now, Provider: provider, WorkKey: workKey,
			ExpectedRevision: tracker.Revision(expectedRevision),
		})
	}
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		return encodeTrackerFailure(stdout, err)
	}
	_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: true, Result: &result})
	return 0
}

func encodeTrackerInvalid(stdout io.Writer, correlation, operation, actor, process, provider, workKey string, revision uint64, field string, cause error) int {
	operationID, _ := ids.ParseOperationID(operation)
	diagnostic := execution.NewDiagnostic(execution.KindInvalidCommand, field+" is invalid", execution.RetryNever, correlation, operationID,
		map[string]string{"operation_id": operation, "actor_id": actor, "process_id": process, "provider": provider, "provider_work_key": workKey, "expected_provider_revision": strconv.FormatUint(revision, 10)},
		map[string]string{field: "invalid"}, map[string]string{field: "portable command-envelope value"}, time.Now().UTC())
	diagnostic.Cause = cause
	return encodeTrackerFailure(stdout, diagnostic)
}

func encodeTrackerFailure(stdout io.Writer, err error) int {
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: false, Error: diagnostic})
		return 0
	}
	_ = json.NewEncoder(stdout).Encode(TrackerResponse{OK: false, Message: err.Error()})
	return 1
}
