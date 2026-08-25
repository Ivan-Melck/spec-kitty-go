package scenario

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	controlsqlite "github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
)

func TestReviewImmutableCandidateAndActorIndependence(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "immutable")
	handoffResponse := runHandoffHelper(t, fixture, capsule, candidate, "handoff-immutable", "operation-handoff-immutable", "")
	if !handoffResponse.OK || handoffResponse.Judgment == nil || handoffResponse.Judgment.Handoff == nil {
		t.Fatalf("handoff=%+v", handoffResponse)
	}
	assertJudgmentEnvelope(t, handoffResponse, "handoff_recorded", "operation-handoff-immutable")
	originalHandoff, _ := json.Marshal(handoffResponse.Judgment.Handoff)

	if err := os.WriteFile(filepath.Join(capsule.WorkspacePath, "after-handoff.txt"), []byte("later mutation\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, capsule.WorkspacePath, "add", "after-handoff.txt")
	scenarioGit(t, capsule.WorkspacePath, "commit", "-m", "move branch after handoff")
	moved := scenarioGit(t, capsule.WorkspacePath, "rev-parse", "HEAD")
	if moved == candidate {
		t.Fatal("workspace did not move after handoff")
	}
	replayed := runHandoffHelper(t, fixture, capsule, candidate, "handoff-immutable", "operation-handoff-immutable", "")
	replayedHandoff, _ := json.Marshal(replayed.Judgment.Handoff)
	if !replayed.OK || string(replayedHandoff) != string(originalHandoff) {
		t.Fatalf("moved branch replay changed immutable handoff: original=%s replay=%s response=%+v", originalHandoff, replayedHandoff, replayed)
	}
	for name, args := range map[string][]string{
		"candidate": replaceJudgmentArg(handoffArgs(fixture, capsule, candidate, "handoff-immutable", "operation-handoff-immutable"), "--candidate", repeatedHex('c', 40)),
		"actor":     replaceJudgmentArg(replaceJudgmentArg(handoffArgs(fixture, capsule, candidate, "handoff-immutable", "operation-handoff-immutable"), "--actor", "actor-mutated"), "--contributors", "actor-mutated"),
		"process":   replaceJudgmentArg(handoffArgs(fixture, capsule, candidate, "handoff-immutable", "operation-handoff-immutable"), "--process", "process-mutated"),
	} {
		mutant := runJudgmentHelper(t, args)
		if mutant.OK || mutant.Error == nil || mutant.Error.Kind != execution.KindIdempotencyConflict {
			t.Fatalf("%s operation reuse=%+v", name, mutant)
		}
		assertDiagnosticEnvelope(t, mutant.Error, "operation-handoff-immutable")
	}

	self := runReviewHelper(t, fixture, capsule, candidate, "handoff-immutable", "review-self", "operation-review-self", "actor-immutable", "process-self-review", review.Approved, "")
	if self.OK || self.Error == nil || self.Error.Kind != execution.ErrorKind(review.KindReviewerNotIndependent) {
		t.Fatalf("same ActorID review=%+v", self)
	}
	assertDiagnosticEnvelope(t, self.Error, "operation-review-self")
	staleArgs := replaceJudgmentArg(reviewArgs(fixture, capsule, candidate, "handoff-immutable", "review-stale", "operation-review-stale", "actor-stale-reviewer", "process-stale-reviewer", review.Approved), "--token", "stale-token")
	stale := runJudgmentHelper(t, staleArgs)
	if stale.OK || stale.Error == nil || stale.Error.Kind != execution.ErrorKind(review.KindStaleAuthority) || stale.Error.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("stale review=%+v", stale)
	}
	assertDiagnosticEnvelope(t, stale.Error, "operation-review-stale")
	distinct := runReviewHelper(t, fixture, capsule, candidate, "handoff-immutable", "review-distinct", "operation-review-distinct", "actor-independent", "process-independent", review.Approved, "")
	if !distinct.OK || distinct.Judgment == nil || distinct.Judgment.Review == nil || distinct.Judgment.Review.Candidate != candidate || distinct.Judgment.Review.Candidate == moved || distinct.Judgment.Review.Verdict != review.Approved {
		t.Fatalf("distinct review=%+v moved=%s", distinct, moved)
	}
	assertJudgmentEnvelope(t, distinct, "review_recorded", "operation-review-distinct")
	originalReview, _ := json.Marshal(distinct.Judgment.Review)
	reviewReplay := runReviewHelper(t, fixture, capsule, candidate, "handoff-immutable", "review-distinct", "operation-review-distinct", "actor-independent", "process-independent", review.Approved, "")
	replayedReview, _ := json.Marshal(reviewReplay.Judgment.Review)
	if !reviewReplay.OK || string(replayedReview) != string(originalReview) {
		t.Fatalf("review replay changed immutable verdict: original=%s replay=%s response=%+v", originalReview, replayedReview, reviewReplay)
	}
	for name, args := range map[string][]string{
		"candidate": replaceJudgmentArg(reviewArgs(fixture, capsule, candidate, "handoff-immutable", "review-distinct", "operation-review-distinct", "actor-independent", "process-independent", review.Approved), "--candidate", repeatedHex('c', 40)),
		"actor":     replaceJudgmentArg(reviewArgs(fixture, capsule, candidate, "handoff-immutable", "review-distinct", "operation-review-distinct", "actor-mutated-review", "process-independent", review.Approved), "--actor", "actor-mutated-review"),
		"process":   replaceJudgmentArg(reviewArgs(fixture, capsule, candidate, "handoff-immutable", "review-distinct", "operation-review-distinct", "actor-independent", "process-mutated-review", review.Approved), "--process", "process-mutated-review"),
	} {
		mutant := runJudgmentHelper(t, args)
		if mutant.OK || mutant.Error == nil || mutant.Error.Kind != execution.KindIdempotencyConflict {
			t.Fatalf("%s review operation reuse=%+v", name, mutant)
		}
		assertDiagnosticEnvelope(t, mutant.Error, "operation-review-distinct")
	}
	fixture.assertPrimaryUnchanged(t)
}

func TestHandoffMovedOwnedBranchPreservesExactGitFactsAndRefusesReplay(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "handoff-branch-moved")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	primaryBefore := fixture.primary.Binding().Primary

	// Move the product-owned branch without creating any fixture evidence ref.
	scenarioGit(t, capsule.WorkspacePath, "reset", "--hard", fixture.base)
	observed := scenarioGit(t, fixture.repository, "rev-parse", capsule.OwnedBranch)
	if observed != fixture.base || observed == candidate {
		t.Fatalf("branch movement candidate=%s observed=%s base=%s", candidate, observed, fixture.base)
	}

	operation := "operation-handoff-branch-moved"
	first := runHandoffHelper(t, fixture, capsule, candidate, "handoff-branch-moved", operation, "")
	assertMovedBranchRefusal(t, first, capsule, candidate, observed, operation)
	root := first.Error.ObservedState["evidence_ref_root"]
	refsBefore := strings.Fields(scenarioGit(t, fixture.repository, "for-each-ref", "--format=%(refname)", root+"/"))
	if len(refsBefore) != 2 {
		t.Fatalf("evidence refs=%v response=%+v", refsBefore, first)
	}

	// Restore the branch and expire authority. The same OperationID must still
	// be a read-only inspection of the already preserved divergence, never a
	// newly successful handoff based on changed live state.
	scenarioGit(t, capsule.WorkspacePath, "reset", "--hard", candidate)
	scenarioGit(t, fixture.repository, "update-ref", "-d", root+"/candidate")
	expiryStore, err := controlsqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	expiry := execution.ExpireRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-expire-handoff-branch-moved", OperationID: capsule.OperationID,
		ActorID: ids.ActorID(capsule.AggregateIDs["actor_id"]), ProcessID: ids.ProcessID(capsule.AggregateIDs["process_id"]),
		LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token, Now: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := execution.NewService(expiryStore, execution.RandomIdentities{}, execution.SystemClock{}).Expire(ctx, expiry); err != nil {
		_ = expiryStore.Close()
		t.Fatal(err)
	}
	if err := expiryStore.Close(); err != nil {
		t.Fatal(err)
	}
	replay := runHandoffHelper(t, fixture, capsule, candidate, "handoff-branch-moved", operation, "")
	assertMovedBranchRefusal(t, replay, capsule, candidate, observed, operation)
	if !reflect.DeepEqual(first.Error.ObservedState, replay.Error.ObservedState) || !reflect.DeepEqual(first.Error.ExpectedState, replay.Error.ExpectedState) || !reflect.DeepEqual(first.Error.RecoveryActions, replay.Error.RecoveryActions) {
		t.Fatalf("mismatch inspection drifted first=%+v replay=%+v", first.Error, replay.Error)
	}
	refsAfter := strings.Fields(scenarioGit(t, fixture.repository, "for-each-ref", "--format=%(refname)", root+"/"))
	if !reflect.DeepEqual(refsBefore, refsAfter) {
		t.Fatalf("replay duplicated evidence before=%v after=%v", refsBefore, refsAfter)
	}

	db, err := sql.Open("sqlite", fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, check := range map[string]struct {
		query string
		want  int
	}{
		"handoffs":        {`SELECT COUNT(*) FROM handoffs WHERE handoff_id='handoff-branch-moved'`, 0},
		"reviews":         {`SELECT COUNT(*) FROM reviews WHERE handoff_id='handoff-branch-moved'`, 0},
		"refusal_records": {`SELECT COUNT(*) FROM handoff_refusals WHERE operation_id='operation-handoff-branch-moved'`, 1},
	} {
		var count int
		if err := db.QueryRowContext(ctx, check.query).Scan(&count); err != nil || count != check.want {
			t.Fatalf("%s count=%d want=%d error=%v", table, count, check.want, err)
		}
	}

	scenarioGit(t, fixture.repository, "reflog", "expire", "--expire=now", "--all")
	scenarioGit(t, fixture.repository, "gc", "--prune=now")
	for label, want := range map[string]string{"candidate": candidate, "observed": observed} {
		ref := root + "/" + label
		if got := scenarioGit(t, fixture.repository, "rev-parse", ref+"^{commit}"); got != want {
			t.Fatalf("%s evidence=%s want=%s", label, got, want)
		}
		if got := scenarioGit(t, fixture.repository, "cat-file", "-t", want); got != "commit" {
			t.Fatalf("%s object type=%s", label, got)
		}
	}
	scenarioGit(t, fixture.repository, "update-ref", root+"/observed", candidate)
	conflict := runHandoffHelper(t, fixture, capsule, candidate, "handoff-branch-moved", operation, "")
	if conflict.OK || conflict.Error == nil || conflict.Error.Kind != execution.ErrorKind(handoff.KindCandidateMismatch) || conflict.Error.RetrySafety != execution.RetryDecisionRequired || conflict.Error.ObservedState["preservation_kind"] != handoff.EvidenceRefMutated || conflict.Error.ObservedState["evidence_observed_ref_head"] != candidate || conflict.Error.ExpectedState["evidence_observed_ref_head"] != observed || strings.Contains(strings.ToLower(conflict.Error.Message), "preserved") || len(conflict.Error.RecoveryActions) < 2 {
		t.Fatalf("mutated evidence replay=%+v", conflict)
	}
	if got := scenarioGit(t, fixture.repository, "rev-parse", root+"/observed^{commit}"); got != candidate {
		t.Fatalf("mutated evidence was overwritten=%s want=%s", got, candidate)
	}
	if got, err := fixture.primary.SnapshotPrimary(ctx); err != nil || got != primaryBefore {
		t.Fatalf("primary changed before=%+v after=%+v error=%v", primaryBefore, got, err)
	}
}

func assertMovedBranchRefusal(t *testing.T, response helpercmd.Response, capsule *execution.RunCapsule, candidate, observed, operation string) {
	t.Helper()
	if response.OK || response.Error == nil || response.Error.Kind != execution.ErrorKind(handoff.KindCandidateMismatch) || response.Error.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("moved branch response=%+v", response)
	}
	diagnostic := response.Error
	if diagnostic.AggregateIDs["operation_id"] != operation || diagnostic.AggregateIDs["handoff_id"] != "handoff-branch-moved" || diagnostic.AggregateIDs["work_revision_id"] != capsule.WorkRevisionID.String() || diagnostic.AggregateIDs["work_id"] != capsule.WorkID.String() || diagnostic.AggregateIDs["run_id"] != capsule.RunID.String() || diagnostic.AggregateIDs["workspace_id"] != capsule.WorkspaceID.String() || diagnostic.AggregateIDs["lease_id"] != capsule.LeaseID.String() || diagnostic.AggregateIDs["actor_id"] != capsule.AggregateIDs["actor_id"] || diagnostic.AggregateIDs["process_id"] != capsule.AggregateIDs["process_id"] {
		t.Fatalf("incomplete identities=%+v", diagnostic.AggregateIDs)
	}
	if diagnostic.ObservedState["candidate"] != candidate || diagnostic.ObservedState["branch_head"] != observed || diagnostic.ObservedState["divergence_kind"] != handoff.BranchMovementDivergence || diagnostic.ExpectedState["candidate"] != candidate || diagnostic.ExpectedState["branch_head"] != candidate || diagnostic.ObservedState["evidence_candidate_ref"] == "" || diagnostic.ObservedState["evidence_observed_ref"] == "" || len(diagnostic.RecoveryActions) < 2 {
		t.Fatalf("incomplete divergence=%+v", diagnostic)
	}
}

func TestReviewMalformedCriteriaJSONEmitsCompleteTypedContext(t *testing.T) {
	const occurred = "2026-08-24T12:00:40Z"
	operation := "operation-review-malformed"
	args := []string{"review", "--db", filepath.Join(t.TempDir(), "unused.db"), "--schema-version", "1", "--correlation-id", "correlation-review-malformed", "--at", occurred,
		"--operation", operation, "--review-id", "review-malformed", "--handoff-id", "handoff-malformed", "--revision", "revision-malformed", "--work", "work-malformed",
		"--lease", "lease-malformed", "--generation", "7", "--token", "opaque-token", "--actor", "actor-reviewer-malformed", "--process", "process-reviewer-malformed",
		"--candidate", repeatedHex('a', 40), "--verdict", string(review.Approved), "--criteria-json", "not-json", "--evidence-digest", repeatedHex('f', 64)}
	response := runJudgmentHelper(t, args)
	if response.OK || response.Error == nil || response.Error.Kind != execution.KindInvalidCommand {
		t.Fatalf("malformed criteria response=%+v", response)
	}
	diagnostic := response.Error
	wantAggregates := map[string]string{
		"operation_id": operation, "review_id": "review-malformed", "handoff_id": "handoff-malformed", "work_revision_id": "revision-malformed", "work_id": "work-malformed",
		"lease_id": "lease-malformed", "lease_generation": "7", "actor_id": "actor-reviewer-malformed", "process_id": "process-reviewer-malformed", "candidate": repeatedHex('a', 40),
	}
	if diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.CorrelationID != "correlation-review-malformed" || diagnostic.OperationID.String() != operation || !diagnostic.OccurredAt.Equal(time.Date(2026, 8, 24, 12, 0, 40, 0, time.UTC)) || diagnostic.RetrySafety != execution.RetryNever || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || len(diagnostic.RecoveryActions) == 0 {
		t.Fatalf("incomplete malformed criteria envelope=%+v", diagnostic)
	}
	for key, want := range wantAggregates {
		if got := diagnostic.AggregateIDs[key]; got != want {
			t.Fatalf("aggregate %s=%q want %q; diagnostic=%+v", key, got, want, diagnostic)
		}
	}
	for dropped := range wantAggregates {
		mutant := *diagnostic
		mutant.AggregateIDs = make(map[string]string, len(diagnostic.AggregateIDs)-1)
		for key, value := range diagnostic.AggregateIDs {
			if key != dropped {
				mutant.AggregateIDs[key] = value
			}
		}
		if completeReviewDiagnostic(&mutant, wantAggregates) {
			t.Fatalf("identifier-drop mutant was not detected: %s", dropped)
		}
	}
}

func completeReviewDiagnostic(diagnostic *execution.DiagnosticError, required map[string]string) bool {
	if diagnostic == nil || diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.CorrelationID == "" || diagnostic.OperationID == "" || diagnostic.OccurredAt.IsZero() || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || diagnostic.RetrySafety == "" || len(diagnostic.RecoveryActions) == 0 {
		return false
	}
	for key, want := range required {
		if diagnostic.AggregateIDs[key] != want {
			return false
		}
	}
	return true
}

func TestHandoffFaultBoundaryReplayHR00ThroughHR07(t *testing.T) {
	for _, boundary := range []string{"HR-00", "HR-01", "HR-02", "HR-03", "HR-04", "HR-05", "HR-06", "HR-07"} {
		t.Run(boundary, func(t *testing.T) {
			fixture, capsule, candidate := startReviewCandidate(t, "fault-"+boundary)
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("crash\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if boundary <= "HR-02" {
				crashJudgmentHelper(t, fixture, append(handoffArgs(fixture, capsule, candidate, "handoff-fault", "operation-handoff-fault"), "--barrier", fixture.barrier), boundary)
			} else {
				handoffResponse := runHandoffHelper(t, fixture, capsule, candidate, "handoff-fault", "operation-handoff-fault", "")
				if !handoffResponse.OK {
					t.Fatalf("handoff=%+v", handoffResponse)
				}
				crashJudgmentHelper(t, fixture, append(reviewArgs(fixture, capsule, candidate, "handoff-fault", "review-fault", "operation-review-fault", "actor-reviewer", "process-reviewer", review.Approved), "--barrier", fixture.barrier), boundary)
			}
			if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("continue\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if boundary == "HR-02" {
				if err := os.WriteFile(filepath.Join(capsule.WorkspacePath, "after-lost-response.txt"), []byte("move after durable commit\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				scenarioGit(t, capsule.WorkspacePath, "add", "after-lost-response.txt")
				scenarioGit(t, capsule.WorkspacePath, "commit", "-m", "move after HR-02")
				expireReviewAuthority(t, fixture.database, capsule, "hr-02")
			}
			if boundary == "HR-06" || boundary == "HR-07" {
				expireReviewAuthority(t, fixture.database, capsule, strings.ToLower(boundary))
			}
			if boundary <= "HR-02" {
				replay := runHandoffHelper(t, fixture, capsule, candidate, "handoff-fault", "operation-handoff-fault", fixture.barrier)
				if !replay.OK || replay.Judgment == nil || replay.Judgment.Handoff == nil || replay.Judgment.Handoff.Candidate != candidate {
					t.Fatalf("%s handoff replay=%+v", boundary, replay)
				}
				assertJudgmentEnvelope(t, replay, "handoff_recorded", "operation-handoff-fault")
			} else {
				replay := runReviewHelper(t, fixture, capsule, candidate, "handoff-fault", "review-fault", "operation-review-fault", "actor-reviewer", "process-reviewer", review.Approved, fixture.barrier)
				if !replay.OK || replay.Judgment == nil || replay.Judgment.Review == nil || replay.Judgment.Review.Candidate != candidate {
					t.Fatalf("%s review replay=%+v", boundary, replay)
				}
				assertJudgmentEnvelope(t, replay, "review_recorded", "operation-review-fault")
			}
			fixture.assertPrimaryUnchanged(t)
		})
	}
}

func expireReviewAuthority(t *testing.T, database string, capsule *execution.RunCapsule, suffix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := controlsqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	request := execution.ExpireRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-expire-" + suffix, OperationID: capsule.OperationID,
		ActorID: ids.ActorID(capsule.AggregateIDs["actor_id"]), ProcessID: ids.ProcessID(capsule.AggregateIDs["process_id"]),
		LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token, Now: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := execution.NewService(store, execution.RandomIdentities{}, execution.SystemClock{}).Expire(ctx, request); err != nil {
		t.Fatal(err)
	}
}

func TestReviewCompetingVerdictWritersAndDistinctRejection(t *testing.T) {
	fixture, capsule, candidate := startReviewCandidate(t, "race")
	handoffResponse := runHandoffHelper(t, fixture, capsule, candidate, "handoff-race", "operation-handoff-race", "")
	if !handoffResponse.OK {
		t.Fatalf("handoff=%+v", handoffResponse)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 2)
	outputs := make([]bytes.Buffer, 2)
	for index := range commands {
		args := reviewArgs(fixture, capsule, candidate, "handoff-race", fmt.Sprintf("review-race-%d", index), fmt.Sprintf("operation-review-race-%d", index), fmt.Sprintf("actor-reviewer-%d", index), fmt.Sprintf("process-reviewer-%d", index), map[bool]review.Verdict{true: review.Approved, false: review.ChangesRequested}[index == 0])
		commands[index] = exec.CommandContext(ctx, helperBinary, args...)
		commands[index].Stdout = &outputs[index]
		if err := commands[index].Start(); err != nil {
			t.Fatal(err)
		}
	}
	responses := make([]helpercmd.Response, 2)
	for index := range commands {
		if err := commands[index].Wait(); err != nil {
			t.Fatalf("writer %d: %v output=%s", index, err, outputs[index].String())
		}
		if err := json.Unmarshal(outputs[index].Bytes(), &responses[index]); err != nil {
			t.Fatal(err)
		}
	}
	successes, conflicts := 0, 0
	for _, response := range responses {
		if response.OK && response.Judgment != nil && response.Judgment.Review != nil {
			successes++
		} else if response.Error != nil && response.Error.Kind == execution.ErrorKind(review.KindConflict) {
			conflicts++
			assertDiagnosticEnvelope(t, response.Error, response.Error.OperationID.String())
		} else {
			t.Fatalf("unexpected writer response=%+v", response)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("responses=%+v", responses)
	}
}

func replaceJudgmentArg(args []string, name, value string) []string {
	result := append([]string(nil), args...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == name {
			result[index+1] = value
			return result
		}
	}
	return append(result, name, value)
}

func assertJudgmentEnvelope(t *testing.T, response helpercmd.Response, kind, operation string) {
	t.Helper()
	result := response.Judgment
	if result == nil || result.SchemaVersion != execution.ResultSchemaVersion || result.Kind != kind || result.CorrelationID != operation || result.OperationID.String() != operation || result.AggregateIDs["operation_id"] != operation || result.AggregateIDs["actor_id"] == "" || result.AggregateIDs["process_id"] == "" || result.AggregateIDs["candidate"] == "" || result.AggregateIDs["work_revision_id"] == "" || result.AggregateIDs["work_id"] == "" || result.AggregateIDs["lease_id"] == "" || len(result.ObservedState) == 0 || len(result.ExpectedState) == 0 || result.RetrySafety == "" || len(result.RecoveryActions) == 0 || result.OccurredAt.IsZero() {
		t.Fatalf("incomplete %s judgment envelope: %+v", kind, result)
	}
	if kind == "handoff_recorded" && (result.Handoff == nil || result.AggregateIDs["handoff_id"] == "" || result.AggregateIDs["run_id"] == "") {
		t.Fatalf("incomplete handoff payload/aggregates: %+v", result)
	}
	if kind == "review_recorded" && (result.Review == nil || result.AggregateIDs["review_id"] == "" || result.AggregateIDs["handoff_id"] == "") {
		t.Fatalf("incomplete review payload/aggregates: %+v", result)
	}
}

func assertDiagnosticEnvelope(t *testing.T, diagnostic *execution.DiagnosticError, operation string) {
	t.Helper()
	if diagnostic.SchemaVersion != execution.ResultSchemaVersion || diagnostic.CorrelationID == "" || diagnostic.OperationID.String() != operation || diagnostic.AggregateIDs["operation_id"] != operation || diagnostic.AggregateIDs["actor_id"] == "" || diagnostic.AggregateIDs["process_id"] == "" || diagnostic.AggregateIDs["candidate"] == "" || diagnostic.AggregateIDs["work_revision_id"] == "" || diagnostic.AggregateIDs["work_id"] == "" || diagnostic.AggregateIDs["lease_id"] == "" || len(diagnostic.ObservedState) == 0 || len(diagnostic.ExpectedState) == 0 || diagnostic.RetrySafety == "" || len(diagnostic.RecoveryActions) == 0 || diagnostic.OccurredAt.IsZero() {
		t.Fatalf("incomplete diagnostic envelope: %+v", diagnostic)
	}
}

func startReviewCandidate(t *testing.T, suffix string) (workspaceScenario, *execution.RunCapsule, string) {
	t.Helper()
	work := "work-" + suffix
	lane := "lane-" + suffix
	trackerKey := "TK-" + suffix
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: idsWork(work), lane: idsLane(lane), tracker: trackerKey}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	response := waitWorkspaceProcess(t, fixture.start(ctx, t, workspaceStart{operation: "operation-start-" + suffix, work: work, lane: lane, actor: "actor-" + suffix, process: "process-" + suffix, workspace: "workspace-" + suffix, branch: "runs/" + work, trackerWork: trackerKey, editFile: "candidate.txt", editContent: "candidate\n"}))
	if !response.OK || response.Capsule == nil || response.Work == nil {
		t.Fatalf("start candidate=%+v", response)
	}
	return fixture, response.Capsule, response.Work.Commit
}

func idsWork(value string) ids.WorkID { return ids.WorkID(value) }
func idsLane(value string) ids.LaneID { return ids.LaneID(value) }

func runHandoffHelper(t *testing.T, fixture workspaceScenario, capsule *execution.RunCapsule, candidate, handoffID, operation, barrier string) helpercmd.Response {
	t.Helper()
	args := handoffArgs(fixture, capsule, candidate, handoffID, operation)
	if barrier != "" {
		args = append(args, "--barrier", barrier)
	}
	return runJudgmentHelper(t, args)
}

func handoffArgs(fixture workspaceScenario, capsule *execution.RunCapsule, candidate, handoffID, operation string) []string {
	return []string{"handoff", "--db", fixture.database, "--schema-version", "1", "--correlation-id", operation, "--at", "2026-08-24T12:00:30Z", "--operation", operation, "--handoff-id", handoffID, "--project", "project-1", "--mission", "mission-1", "--revision", capsule.WorkRevisionID.String(), "--work", capsule.WorkID.String(), "--run", capsule.RunID.String(), "--workspace", capsule.WorkspaceID.String(), "--lease", capsule.LeaseID.String(), "--generation", fmt.Sprint(capsule.Generation), "--token", capsule.Token, "--actor", capsule.AggregateIDs["actor_id"], "--process", capsule.AggregateIDs["process_id"], "--candidate", candidate, "--contributors", capsule.AggregateIDs["actor_id"], "--evidence-digest", repeatedHex('e', 64)}
}

func runReviewHelper(t *testing.T, fixture workspaceScenario, capsule *execution.RunCapsule, candidate, handoffID, reviewID, operation, actor, process string, verdict review.Verdict, barrier string) helpercmd.Response {
	t.Helper()
	args := reviewArgs(fixture, capsule, candidate, handoffID, reviewID, operation, actor, process, verdict)
	if barrier != "" {
		args = append(args, "--barrier", barrier)
	}
	return runJudgmentHelper(t, args)
}

func reviewArgs(fixture workspaceScenario, capsule *execution.RunCapsule, candidate, handoffID, reviewID, operation, actor, process string, verdict review.Verdict) []string {
	status := review.Passed
	if verdict == review.ChangesRequested {
		status = review.Failed
	}
	criteria, _ := json.Marshal([]review.CriterionResult{{CriterionID: "criterion-" + capsule.WorkID.String(), Status: status, Evidence: "scenario evidence"}})
	return []string{"review", "--db", fixture.database, "--schema-version", "1", "--correlation-id", operation, "--at", "2026-08-24T12:00:40Z", "--operation", operation, "--review-id", reviewID, "--handoff-id", handoffID, "--revision", capsule.WorkRevisionID.String(), "--work", capsule.WorkID.String(), "--lease", capsule.LeaseID.String(), "--generation", fmt.Sprint(capsule.Generation), "--token", capsule.Token, "--actor", actor, "--process", process, "--candidate", candidate, "--verdict", string(verdict), "--criteria-json", string(criteria), "--evidence-digest", repeatedHex('f', 64)}
}

func runJudgmentHelper(t *testing.T, args []string) helpercmd.Response {
	t.Helper()
	command := exec.Command(helperBinary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("helper %v: %v stderr=%s stdout=%s", args[0], err, stderr.String(), stdout.String())
	}
	var response helpercmd.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode %q: %v", stdout.String(), err)
	}
	return response
}

func crashJudgmentHelper(t *testing.T, fixture workspaceScenario, args []string, boundary string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, helperBinary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if err := waitForBoundaryReached(ctx, fixture.barrier, boundary, 1); err != nil {
		t.Fatal(err)
	}
	err := command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 || stdout.Len() != 0 {
		t.Fatalf("%s crash err=%v stdout=%q stderr=%q", boundary, err, stdout.String(), stderr.String())
	}
}

func repeatedHex(character byte, count int) string {
	return string(bytes.Repeat([]byte{character}, count))
}
