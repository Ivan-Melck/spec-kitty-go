package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
)

type Response struct {
	OK          bool                       `json:"ok"`
	Capsule     *execution.RunCapsule      `json:"capsule,omitempty"`
	Work        *WorkspaceCommit           `json:"work,omitempty"`
	Snapshot    *sqlite.AuthoritySnapshot  `json:"snapshot,omitempty"`
	Judgment    *JudgmentResult            `json:"judgment,omitempty"`
	Integration *IntegrationResult         `json:"integration,omitempty"`
	Error       *execution.DiagnosticError `json:"error,omitempty"`
	Message     string                     `json:"message,omitempty"`
}

type IntegrationResult struct {
	SchemaVersion   uint16                `json:"schema_version"`
	Kind            string                `json:"kind"`
	CorrelationID   string                `json:"correlation_id"`
	OperationID     ids.OperationID       `json:"operation_id"`
	AggregateIDs    map[string]string     `json:"aggregate_ids"`
	ObservedState   map[string]string     `json:"observed_state"`
	ExpectedState   map[string]string     `json:"expected_state"`
	RetrySafety     execution.RetrySafety `json:"retry_safety"`
	RecoveryActions []string              `json:"recovery_actions"`
	OccurredAt      time.Time             `json:"occurred_at"`
	Record          integration.Record    `json:"record"`
}

type JudgmentResult struct {
	SchemaVersion   uint16                `json:"schema_version"`
	Kind            string                `json:"kind"`
	CorrelationID   string                `json:"correlation_id"`
	OperationID     ids.OperationID       `json:"operation_id"`
	AggregateIDs    map[string]string     `json:"aggregate_ids"`
	ObservedState   map[string]string     `json:"observed_state"`
	ExpectedState   map[string]string     `json:"expected_state"`
	RetrySafety     execution.RetrySafety `json:"retry_safety"`
	RecoveryActions []string              `json:"recovery_actions"`
	OccurredAt      time.Time             `json:"occurred_at"`
	Handoff         *handoff.Record       `json:"handoff,omitempty"`
	Review          *review.Record        `json:"review,omitempty"`
}

type WorkspaceCommit struct {
	Commit    string    `json:"commit"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, helperUsage)
		return 2
	}
	switch args[0] {
	case "claim":
		return runClaim(ctx, args[1:], stdout, stderr)
	case "start-run":
		return runStartRun(ctx, args[1:], stdout, stderr)
	case "recover-start":
		return runRecoverStart(ctx, args[1:], stdout, stderr)
	case "expire-authority":
		return runExpireAuthority(ctx, args[1:], stdout, stderr)
	case "zombie-write":
		return runZombieWrite(ctx, args[1:], stdout, stderr)
	case "workspace-inspect":
		return runWorkspaceInspect(ctx, args[1:], stdout, stderr)
	case "workspace-cleanup":
		return runWorkspaceCleanup(ctx, args[1:], stdout, stderr)
	case "handoff":
		return runHandoff(ctx, args[1:], stdout, stderr)
	case "review":
		return runReview(ctx, args[1:], stdout, stderr)
	case "integrate":
		return runIntegrate(ctx, args[1:], stdout, stderr)
	case "inspect":
		return runInspect(ctx, args[1:], stdout, stderr)
	case "tracker":
		return RunTracker(ctx, args[1:], stdout, stderr)
	case "workgraph":
		return RunWorkGraph(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, helperUsage)
		return 2
	}
}

const helperUsage = "usage: spk-phase1-helper claim|start-run|recover-start|expire-authority|zombie-write|workspace-inspect|workspace-cleanup|handoff|review|integrate|inspect|tracker|workgraph [options]"

func runClaim(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("claim", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dbPath, barrier, operation, project, mission, revision, work, actor, process string
	var ttl time.Duration
	flags.StringVar(&dbPath, "db", "", "control database path")
	flags.StringVar(&barrier, "barrier", "", "fault-controller directory")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&mission, "mission", "", "mission ID")
	flags.StringVar(&revision, "revision", "", "work revision ID")
	flags.StringVar(&work, "work", "", "work ID")
	flags.StringVar(&actor, "actor", "", "actor ID")
	flags.StringVar(&process, "process", "", "process ID")
	flags.DurationVar(&ttl, "ttl", time.Minute, "lease lifetime")
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
	missionID, err := ids.ParseMissionID(mission)
	if err != nil {
		return encodeInvalidID(stdout, operation, "mission_id", err)
	}
	revisionID, err := ids.ParseWorkRevisionID(revision)
	if err != nil {
		return encodeInvalidID(stdout, operation, "work_revision_id", err)
	}
	workID, err := ids.ParseWorkID(work)
	if err != nil {
		return encodeInvalidID(stdout, operation, "work_id", err)
	}
	actorID, err := ids.ParseActorID(actor)
	if err != nil {
		return encodeInvalidID(stdout, operation, "actor_id", err)
	}
	processID, err := ids.ParseProcessID(process)
	if err != nil {
		return encodeInvalidID(stdout, operation, "process_id", err)
	}
	now := time.Now().UTC()
	governanceContext, err := syntheticStartGovernanceContext(now)
	if err != nil {
		diagnostic := execution.NewDiagnostic(execution.KindInvalidCommand, "construct synthetic Phase 1 governance context", execution.RetryNever, operationID.String(), operationID, nil, nil, nil, now)
		diagnostic.Cause = err
		return encodeFailure(stdout, diagnostic)
	}
	faultController := faults.FileController{Directory: barrier, Participant: processID.String()}
	if err := faultController.Reach(ctx, "CS-00"); errors.Is(err, faults.ErrCrashRequested) {
		return 86
	} else if err != nil {
		return encodeFailure(stdout, err)
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	defer store.Close()
	service := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{})
	service.WithFaults(faultController)
	request := execution.StartRequest{
		CorrelationID: operationID.String(), OperationID: operationID, ProjectID: projectID, MissionID: missionID,
		WorkRevisionID: revisionID, WorkID: workID, ActorID: actorID, ProcessID: processID, Now: now, LeaseTTL: ttl,
		GovernanceContext: governanceContext,
	}
	capsule, err := service.Start(ctx, request)
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		return encodeFailure(stdout, err)
	}
	_ = json.NewEncoder(stdout).Encode(Response{OK: true, Capsule: &capsule})
	return 0
}

func runInspect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var dbPath, work, revision string
	flags.StringVar(&dbPath, "db", "", "control database path")
	flags.StringVar(&work, "work", "", "work ID")
	flags.StringVar(&revision, "revision", "", "work revision ID")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	workID, err := ids.ParseWorkID(work)
	if err != nil {
		return encodeInvalidID(stdout, "inspect", "work_id", err)
	}
	revisionID, err := ids.ParseWorkRevisionID(revision)
	if err != nil {
		return encodeInvalidID(stdout, "inspect", "work_revision_id", err)
	}
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	defer store.Close()
	snapshot, err := store.AuthoritySnapshot(ctx, workID, revisionID)
	if err != nil {
		return encodeFailure(stdout, err)
	}
	_ = json.NewEncoder(stdout).Encode(Response{OK: true, Snapshot: &snapshot})
	return 0
}

func encodeInvalidID(stdout io.Writer, operation, field string, cause error) int {
	operationID, _ := ids.ParseOperationID(operation)
	diagnostic := execution.NewDiagnostic(execution.KindInvalidCommand, field+" is invalid", execution.RetryNever, operation, operationID, nil, map[string]string{field: "invalid"}, map[string]string{field: "portable ID"}, time.Now().UTC())
	diagnostic.Cause = cause
	return encodeFailure(stdout, diagnostic)
}

func encodeFailure(stdout io.Writer, err error) int {
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		_ = json.NewEncoder(stdout).Encode(Response{OK: false, Error: diagnostic})
		return 0
	}
	_ = json.NewEncoder(stdout).Encode(Response{OK: false, Message: err.Error()})
	return 1
}
