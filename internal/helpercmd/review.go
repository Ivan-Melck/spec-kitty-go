package helpercmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
)

func runHandoff(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("handoff", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var database, barrier, correlation, at, operation, handoffID, project, mission, revision, work, run, workspaceID, lease, actor, process, token, candidate, contributors, evidence string
	var generation int64
	var schema uint
	flags.StringVar(&database, "db", "", "control database")
	flags.StringVar(&barrier, "barrier", "", "fault boundary directory")
	flags.UintVar(&schema, "schema-version", 0, "command schema version")
	flags.StringVar(&correlation, "correlation-id", "", "correlation ID")
	flags.StringVar(&at, "at", "", "RFC3339 timestamp")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&handoffID, "handoff-id", "", "handoff ID")
	flags.StringVar(&project, "project", "", "project ID")
	flags.StringVar(&mission, "mission", "", "mission ID")
	flags.StringVar(&revision, "revision", "", "work revision ID")
	flags.StringVar(&work, "work", "", "work ID")
	flags.StringVar(&run, "run", "", "run ID")
	flags.StringVar(&workspaceID, "workspace", "", "workspace ID")
	flags.StringVar(&lease, "lease", "", "lease ID")
	flags.Int64Var(&generation, "generation", 0, "lease generation")
	flags.StringVar(&token, "token", "", "opaque lease token")
	flags.StringVar(&actor, "actor", "", "actor ID")
	flags.StringVar(&process, "process", "", "process ID")
	flags.StringVar(&candidate, "candidate", "", "exact candidate commit")
	flags.StringVar(&contributors, "contributors", "", "comma-separated contributor ActorIDs")
	flags.StringVar(&evidence, "evidence-digest", "", "evidence digest")
	if err := flags.Parse(args); err != nil {
		return encodeJudgmentInvalid(stdout, operation, correlation, actor, process, time.Time{}, err)
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeJudgmentInvalid(stdout, operation, correlation, actor, process, time.Time{}, err)
	}
	request := handoff.Request{SchemaVersion: uint16(schema), CorrelationID: correlation, OperationID: ids.OperationID(operation), HandoffID: ids.HandoffID(handoffID), ProjectID: ids.ProjectID(project), MissionID: ids.MissionID(mission), WorkRevisionID: ids.WorkRevisionID(revision), WorkID: ids.WorkID(work), RunID: ids.RunID(run), WorkspaceID: ids.WorkspaceID(workspaceID), LeaseID: ids.LeaseID(lease), LeaseGeneration: generation, Token: token, ActorID: ids.ActorID(actor), ProcessID: ids.ProcessID(process), Candidate: candidate, EvidenceDigest: evidence, SubmittedAt: now.UTC()}
	for _, value := range strings.Split(contributors, ",") {
		if value != "" {
			request.Contributors = append(request.Contributors, ids.ActorID(value))
		}
	}
	if err := request.Validate(); err != nil {
		return encodeJudgmentFailure(stdout, handoffJudgmentContext(request), err)
	}
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		return encodeJudgmentFailure(stdout, handoffJudgmentContext(request), err)
	}
	defer store.Close()
	controller := faults.FileController{Directory: barrier, Participant: process}
	record, err := handoff.NewService(store, gitrepo.NewHandoffVerifier()).WithFaults(controller).Submit(ctx, request)
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		return encodeJudgmentFailure(stdout, handoffJudgmentContext(request), err)
	}
	result := handoffSuccess(request, record)
	return encodeJSON(stdout, Response{OK: true, Judgment: &result})
}

func runReview(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var database, barrier, correlation, at, operation, reviewID, handoffID, revision, work, lease, actor, process, token, candidate, verdict, criteriaJSON, evidence string
	var generation int64
	var schema uint
	flags.StringVar(&database, "db", "", "control database")
	flags.StringVar(&barrier, "barrier", "", "fault boundary directory")
	flags.UintVar(&schema, "schema-version", 0, "command schema version")
	flags.StringVar(&correlation, "correlation-id", "", "correlation ID")
	flags.StringVar(&at, "at", "", "RFC3339 timestamp")
	flags.StringVar(&operation, "operation", "", "operation ID")
	flags.StringVar(&reviewID, "review-id", "", "review ID")
	flags.StringVar(&handoffID, "handoff-id", "", "handoff ID")
	flags.StringVar(&revision, "revision", "", "work revision ID")
	flags.StringVar(&work, "work", "", "work ID")
	flags.StringVar(&lease, "lease", "", "lease ID")
	flags.Int64Var(&generation, "generation", 0, "lease generation")
	flags.StringVar(&token, "token", "", "opaque lease token")
	flags.StringVar(&actor, "actor", "", "reviewer ActorID")
	flags.StringVar(&process, "process", "", "reviewer ProcessID")
	flags.StringVar(&candidate, "candidate", "", "exact handoff candidate")
	flags.StringVar(&verdict, "verdict", "", "approved or changes_requested")
	flags.StringVar(&criteriaJSON, "criteria-json", "", "JSON criterion results")
	flags.StringVar(&evidence, "evidence-digest", "", "review evidence digest")
	if err := flags.Parse(args); err != nil {
		return encodeJudgmentInvalid(stdout, operation, correlation, actor, process, time.Time{}, err)
	}
	now, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return encodeJudgmentInvalid(stdout, operation, correlation, actor, process, time.Time{}, err)
	}
	request := review.Request{SchemaVersion: uint16(schema), CorrelationID: correlation, OperationID: ids.OperationID(operation), ReviewID: ids.ReviewID(reviewID), HandoffID: ids.HandoffID(handoffID), WorkRevisionID: ids.WorkRevisionID(revision), WorkID: ids.WorkID(work), LeaseID: ids.LeaseID(lease), LeaseGeneration: generation, Token: token, ActorID: ids.ActorID(actor), ProcessID: ids.ProcessID(process), Candidate: candidate, Verdict: review.Verdict(verdict), EvidenceDigest: evidence, SubmittedAt: now.UTC()}
	request, err = parseReviewIdentifiers(request)
	if err != nil {
		return encodeJudgmentFailure(stdout, reviewJudgmentContext(request), &review.Error{Kind: review.KindInvalid, Message: "review identifier is invalid", Cause: err})
	}
	if err := json.Unmarshal([]byte(criteriaJSON), &request.Criteria); err != nil {
		return encodeJudgmentFailure(stdout, reviewJudgmentContext(request), &review.Error{Kind: review.KindInvalid, Message: "criteria_json must encode criterion results", Cause: err})
	}
	if err := request.Validate(); err != nil {
		return encodeJudgmentFailure(stdout, reviewJudgmentContext(request), err)
	}
	store, err := sqlite.Open(ctx, database)
	if err != nil {
		return encodeJudgmentFailure(stdout, reviewJudgmentContext(request), err)
	}
	defer store.Close()
	controller := faults.FileController{Directory: barrier, Participant: process}
	record, err := review.NewService(store).WithFaults(controller).Submit(ctx, request)
	if errors.Is(err, faults.ErrCrashRequested) {
		return 86
	}
	if err != nil {
		return encodeJudgmentFailure(stdout, reviewJudgmentContext(request), err)
	}
	result := reviewSuccess(request, record)
	return encodeJSON(stdout, Response{OK: true, Judgment: &result})
}

func parseReviewIdentifiers(request review.Request) (review.Request, error) {
	operation, err := ids.ParseOperationID(request.OperationID.String())
	if err != nil {
		return request, fmt.Errorf("operation_id: %w", err)
	}
	reviewID, err := ids.ParseReviewID(request.ReviewID.String())
	if err != nil {
		return request, fmt.Errorf("review_id: %w", err)
	}
	handoffID, err := ids.ParseHandoffID(request.HandoffID.String())
	if err != nil {
		return request, fmt.Errorf("handoff_id: %w", err)
	}
	revisionID, err := ids.ParseWorkRevisionID(request.WorkRevisionID.String())
	if err != nil {
		return request, fmt.Errorf("work_revision_id: %w", err)
	}
	workID, err := ids.ParseWorkID(request.WorkID.String())
	if err != nil {
		return request, fmt.Errorf("work_id: %w", err)
	}
	leaseID, err := ids.ParseLeaseID(request.LeaseID.String())
	if err != nil {
		return request, fmt.Errorf("lease_id: %w", err)
	}
	actorID, err := ids.ParseActorID(request.ActorID.String())
	if err != nil {
		return request, fmt.Errorf("actor_id: %w", err)
	}
	processID, err := ids.ParseProcessID(request.ProcessID.String())
	if err != nil {
		return request, fmt.Errorf("process_id: %w", err)
	}
	request.OperationID, request.ReviewID, request.HandoffID = operation, reviewID, handoffID
	request.WorkRevisionID, request.WorkID, request.LeaseID = revisionID, workID, leaseID
	request.ActorID, request.ProcessID = actorID, processID
	return request, nil
}

func encodeJudgmentInvalid(stdout io.Writer, operation, correlation, actor, process string, now time.Time, cause error) int {
	return encodeJudgmentFailure(stdout, judgmentContext{operation: ids.OperationID(operation), correlation: correlation, occurredAt: now, aggregates: map[string]string{"operation_id": operation, "actor_id": actor, "process_id": process}}, &handoff.Error{Kind: handoff.KindInvalid, Message: "invalid judgment command", Cause: cause})
}

type judgmentContext struct {
	operation   ids.OperationID
	correlation string
	occurredAt  time.Time
	aggregates  map[string]string
}

func encodeJudgmentFailure(stdout io.Writer, command judgmentContext, cause error) int {
	kind := execution.KindInvalidCommand
	retry := execution.RetryNever
	var handoffError *handoff.Error
	var reviewError *review.Error
	observed, expected := map[string]string{}, map[string]string{}
	message := cause.Error()
	switch {
	case errors.As(cause, &handoffError):
		kind = execution.ErrorKind(handoffError.Kind)
		observed, expected = handoffError.Observed, handoffError.Expected
		message = handoffError.Message
	case errors.As(cause, &reviewError):
		kind = execution.ErrorKind(reviewError.Kind)
		observed, expected = reviewError.Observed, reviewError.Expected
		message = reviewError.Message
	default:
		kind = execution.KindControlStoreBusy
		retry = execution.RetrySafeSameOperation
	}
	if kind == execution.ErrorKind(handoff.KindStaleAuthority) || kind == execution.ErrorKind(review.KindStaleAuthority) || kind == execution.ErrorKind(handoff.KindCandidateMismatch) || kind == execution.ErrorKind(review.KindCandidateMismatch) || kind == execution.ErrorKind(handoff.KindRevisionIneligible) || kind == execution.ErrorKind(review.KindRevisionIneligible) {
		retry = execution.RetryDecisionRequired
	}
	if len(observed) == 0 {
		observed = map[string]string{"command": "rejected", "error_kind": string(kind)}
	}
	if len(expected) == 0 {
		expected = map[string]string{"command": "accepted under current immutable authority"}
	}
	diagnostic := execution.NewDiagnostic(kind, message, retry, command.correlation, command.operation, command.aggregates, observed, expected, command.occurredAt)
	if handoffError != nil && len(handoffError.RecoveryActions) > 0 {
		diagnostic.RecoveryActions = append([]string(nil), handoffError.RecoveryActions...)
	}
	diagnostic.Cause = cause
	return encodeFailure(stdout, diagnostic)
}

func handoffJudgmentContext(request handoff.Request) judgmentContext {
	return judgmentContext{operation: request.OperationID, correlation: request.CorrelationID, occurredAt: request.SubmittedAt, aggregates: map[string]string{
		"operation_id": request.OperationID.String(), "handoff_id": request.HandoffID.String(), "project_id": request.ProjectID.String(), "mission_id": request.MissionID.String(),
		"work_revision_id": request.WorkRevisionID.String(), "work_id": request.WorkID.String(), "run_id": request.RunID.String(), "workspace_id": request.WorkspaceID.String(),
		"lease_id": request.LeaseID.String(), "lease_generation": fmt.Sprint(request.LeaseGeneration), "actor_id": request.ActorID.String(), "process_id": request.ProcessID.String(), "candidate": request.Candidate,
	}}
}

func reviewJudgmentContext(request review.Request) judgmentContext {
	return judgmentContext{operation: request.OperationID, correlation: request.CorrelationID, occurredAt: request.SubmittedAt, aggregates: map[string]string{
		"operation_id": request.OperationID.String(), "review_id": request.ReviewID.String(), "handoff_id": request.HandoffID.String(), "work_revision_id": request.WorkRevisionID.String(),
		"work_id": request.WorkID.String(), "lease_id": request.LeaseID.String(), "lease_generation": fmt.Sprint(request.LeaseGeneration), "actor_id": request.ActorID.String(), "process_id": request.ProcessID.String(), "candidate": request.Candidate,
	}}
}

func handoffSuccess(request handoff.Request, record handoff.Record) JudgmentResult {
	context := handoffJudgmentContext(request)
	return JudgmentResult{SchemaVersion: execution.ResultSchemaVersion, Kind: "handoff_recorded", CorrelationID: request.CorrelationID, OperationID: request.OperationID,
		AggregateIDs: context.aggregates, ObservedState: map[string]string{"candidate": record.Candidate, "handoff_generation": fmt.Sprint(record.HandoffGeneration), "durability": "immutable"},
		ExpectedState: map[string]string{"candidate": request.Candidate, "authority": "verified before first commit"}, RetrySafety: execution.RetrySafeSameOperation,
		RecoveryActions: []string{"retry the same operation ID to retrieve the immutable handoff"}, OccurredAt: record.SubmittedAt, Handoff: &record}
}

func reviewSuccess(request review.Request, record review.Record) JudgmentResult {
	context := reviewJudgmentContext(request)
	return JudgmentResult{SchemaVersion: execution.ResultSchemaVersion, Kind: "review_recorded", CorrelationID: request.CorrelationID, OperationID: request.OperationID,
		AggregateIDs: context.aggregates, ObservedState: map[string]string{"candidate": record.Candidate, "verdict": string(record.Verdict), "durability": "immutable"},
		ExpectedState: map[string]string{"candidate": request.Candidate, "reviewer_actor": "independent of contributor ActorIDs"}, RetrySafety: execution.RetrySafeSameOperation,
		RecoveryActions: []string{"retry the same operation ID to retrieve the immutable review"}, OccurredAt: record.SubmittedAt, Review: &record}
}
