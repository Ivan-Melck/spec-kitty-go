package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/faults"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestIntegrationMigrationV10DigestRemainsFrozen(t *testing.T) {
	digest := sha256.Sum256([]byte(integrationMigrationV10))
	const shippedDigest = "270184e5f92468f24480ac82f55916afa9128d66d22212b6b41b542ef5bd5d6d"
	if got := fmt.Sprintf("%x", digest); got != shippedDigest {
		t.Fatalf("shipped v10 digest=%s", got)
	}
}

func TestIntegrationAuthorityIsRepositoryTargetScopedAndDoneIsSingular(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	now := time.Now().UTC()
	request := integrationRequest("operation-integrate-1", "review-integration", candidate, now)
	record := prepareIntegrationForTest(t, fixture, request, commitForStore('a'), integration.FenceFacts{})
	var leases int
	var leaseStatus string
	var err error
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=?`, record.OperationID).Scan(&leases); err != nil || leases != 0 {
		t.Fatalf("repository-first preparation published %d SQLite leases err=%v", leases, err)
	}
	record = activateIntegrationForTest(t, fixture, record)
	if record.RepositoryID != "repository-1" || record.TargetRef != "refs/heads/main" || record.MutationGeneration != 1 || record.Status != integration.StatusAuthorizing {
		t.Fatalf("authority=%+v", record)
	}

	competing := integrationRequest("operation-integrate-2", "review-integration", candidate, now.Add(time.Nanosecond))
	competing.ActorID, competing.ProcessID = "actor-other", "process-other"
	if _, err := fixture.store.PrepareIntegrationAuthority(fixture.ctx, competing, commitForStore('a'), fenceFactsForStoreRecord(record)); !integration.IsKind(err, integration.KindAuthorityBusy) {
		t.Fatalf("competing target authority error=%v", err)
	}

	record, err = fixture.store.RecordIntegrationWorkspace(fixture.ctx, record, integration.WorkspaceFacts{Path: "/runtime/integration-1", Registered: true, Detached: true, Clean: true, Head: commitForStore('a'), CandidateReachable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.RevalidateIntegration(fixture.ctx, record, commitForStore('a')); err != nil {
		t.Fatal(err)
	}
	record, err = fixture.store.RecordIntegrationResult(fixture.ctx, record, commitForStore('c'), commitForStore('c'))
	if err != nil {
		t.Fatal(err)
	}
	record, err = fixture.store.CompleteIntegration(fixture.ctx, record, commitForStore('c'))
	if err != nil || record.Status != integration.StatusDone || record.DoneAt.IsZero() {
		t.Fatalf("done=%+v err=%v", record, err)
	}
	if _, err := fixture.store.PublishIntegrationCleanupEligibility(fixture.ctx, record, func() error { return faults.ErrCrashRequested }); !errors.Is(err, faults.ErrCrashRequested) {
		t.Fatalf("cleanup publication crash=%v", err)
	}
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT cleanup_status FROM integrations WHERE operation_id=?`, record.OperationID).Scan(&leaseStatus); err != nil || leaseStatus != string(integration.CleanupPending) {
		t.Fatalf("rolled-back cleanup status=%s err=%v", leaseStatus, err)
	}
	record, err = fixture.store.PublishIntegrationCleanupEligibility(fixture.ctx, record, nil)
	if err != nil || record.CleanupStatus != integration.CleanupEligible || record.CleanupEligibleAt.IsZero() {
		t.Fatalf("cleanup eligibility=%+v err=%v", record, err)
	}
	record, err = fixture.store.CompleteIntegrationCleanup(fixture.ctx, record)
	if err != nil || record.CleanupStatus != integration.CleanupComplete || record.CleanupCompletedAt.IsZero() {
		t.Fatalf("cleanup completion=%+v err=%v", record, err)
	}
	replayed, found, err := fixture.store.LookupIntegrationOperation(fixture.ctx, request)
	if err != nil || !found || replayed.Status != integration.StatusDone || replayed.ResultCommit != record.ResultCommit {
		t.Fatalf("replay=%+v found=%v err=%v", replayed, found, err)
	}
	var done int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_done WHERE review_id='review-integration'`).Scan(&done); err != nil || done != 1 {
		t.Fatalf("done records=%d err=%v", done, err)
	}
	mutant := request
	mutant.Candidate = commitForStore('d')
	if _, _, err := fixture.store.LookupIntegrationOperation(fixture.ctx, mutant); !integration.IsKind(err, integration.KindIdempotencyConflict) {
		t.Fatalf("OperationID mutant error=%v", err)
	}
}

func TestExpiredIntegrationTokenCannotWriteReplacementWorkspace(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	now := time.Now().UTC()
	firstRequest := integrationRequest("operation-stale-integration", "review-integration", candidate, now)
	stale := prepareIntegrationForTest(t, fixture, firstRequest, commitForStore('a'), integration.FenceFacts{})
	stale = activateIntegrationForTest(t, fixture, stale)
	if stale.Token == "" {
		t.Fatal("mutation fencing token was not recovered for the operation")
	}
	encoded, err := json.Marshal(stale)
	if err != nil || strings.Contains(string(encoded), stale.Token) {
		t.Fatalf("token serialization err=%v payload=%s", err, encoded)
	}
	var envelope []byte
	var digest string
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT c.token_envelope,l.token_digest FROM integration_commands c JOIN integration_mutation_leases l ON l.operation_id=c.operation_id WHERE c.operation_id=?`, stale.OperationID).Scan(&envelope, &digest); err != nil || strings.Contains(string(envelope), stale.Token) || digest == stale.Token || digest != tokenDigest(stale.Token) {
		t.Fatalf("token persistence leaked or changed: digest=%q envelope=%x err=%v", digest, envelope, err)
	}
	if _, err := fixture.store.db.ExecContext(fixture.ctx, `UPDATE integration_mutation_leases SET deadline=? WHERE operation_id=?`, formatTime(now.Add(-time.Second)), stale.OperationID); err != nil {
		t.Fatal(err)
	}
	replacementRequest := integrationRequest("operation-replacement-integration", "review-integration", candidate, now.Add(time.Second))
	replacementRequest.ActorID, replacementRequest.ProcessID = "actor-replacement", "process-replacement"
	staleFence := fenceFactsForStoreRecord(stale)
	staleFence.Deadline = now.Add(-time.Second)
	replacement := prepareIntegrationForTest(t, fixture, replacementRequest, commitForStore('a'), staleFence)
	replacement = activateIntegrationForTest(t, fixture, replacement)
	if replacement.MutationGeneration != stale.MutationGeneration+1 || replacement.Token == stale.Token {
		t.Fatalf("stale=%+v replacement=%+v", stale, replacement)
	}
	staleFacts := integration.WorkspaceFacts{Path: "/runtime/stale-workspace", Registered: true, Detached: true, Clean: true, Head: commitForStore('a'), CandidateReachable: true}
	if _, err := fixture.store.RecordIntegrationWorkspace(fixture.ctx, stale, staleFacts); !integration.IsKind(err, integration.KindAuthorityExpired) {
		t.Fatalf("stale filesystem binding error=%v", err)
	}
	replacementFacts := integration.WorkspaceFacts{Path: "/runtime/replacement-workspace", Registered: true, Detached: true, Clean: true, Head: commitForStore('a'), CandidateReachable: true}
	if got, err := fixture.store.RecordIntegrationWorkspace(fixture.ctx, replacement, replacementFacts); err != nil || got.WorkspacePath != replacementFacts.Path {
		t.Fatalf("replacement workspace=%+v err=%v", got, err)
	}
	var stalePath string
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT workspace_path FROM integrations WHERE operation_id=?`, stale.OperationID).Scan(&stalePath); err != nil || stalePath != "" {
		t.Fatalf("stale workspace path=%q err=%v", stalePath, err)
	}
}

func TestRepositoryFenceGenerationDominatesSQLiteAndActivationNeverRegresses(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	now := time.Now().UTC()
	seedRequest := integrationRequest("operation-generation-seed", "review-integration", candidate, now)
	seed := prepareIntegrationForTest(t, fixture, seedRequest, commitForStore('a'), integration.FenceFacts{})
	seed = activateIntegrationForTest(t, fixture, seed)
	if _, err := fixture.store.db.ExecContext(fixture.ctx, `UPDATE integration_mutation_leases SET generation=10,deadline=? WHERE operation_id=?`, formatTime(now.Add(-time.Second)), seed.OperationID); err != nil {
		t.Fatal(err)
	}
	orphan := integration.FenceFacts{Ref: "refs/spec-kitty/fences/repository-1", Object: commitForStore('g'), Generation: 7, OperationID: "operation-orphan-generation-7", RepositoryID: "repository-1", TargetRef: "refs/heads/main", RequestDigest: "orphan-digest", PreflightTarget: commitForStore('a'), TokenDigest: "orphan-token-digest", Deadline: now.Add(-time.Second)}
	request := integrationRequest("operation-generation-11", "review-integration", candidate, now.Add(time.Second))
	planned := prepareIntegrationForTest(t, fixture, request, commitForStore('a'), orphan)
	delayedRequest := integrationRequest("operation-generation-delayed", "review-integration", candidate, now.Add(2*time.Second))
	delayedRequest.ActorID, delayedRequest.ProcessID = "actor-delayed", "process-delayed"
	delayed := prepareIntegrationForTest(t, fixture, delayedRequest, commitForStore('a'), orphan)
	if planned.MutationGeneration != 11 || delayed.MutationGeneration != 11 {
		t.Fatalf("planned generation=%d delayed=%d want=11", planned.MutationGeneration, delayed.MutationGeneration)
	}
	var operationRows int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_commands WHERE operation_id IN (?,?)`, planned.OperationID, delayed.OperationID).Scan(&operationRows); err != nil || operationRows != 0 {
		t.Fatalf("read-only planning published %d commands err=%v", operationRows, err)
	}
	planned = activateIntegrationForTest(t, fixture, planned)
	var seedStatus string
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT status FROM integration_mutation_leases WHERE operation_id=?`, seed.OperationID).Scan(&seedStatus); err != nil || seedStatus != "expired" {
		t.Fatalf("older authority status=%s err=%v", seedStatus, err)
	}
	if _, err := fixture.store.db.ExecContext(fixture.ctx, `UPDATE integration_mutation_leases SET status='released' WHERE operation_id=?`, planned.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ActivateIntegrationAuthority(fixture.ctx, delayed, fenceFactsForStoreRecord(delayed)); !integration.IsKind(err, integration.KindAuthorityExpired) {
		t.Fatalf("delayed equal-generation activation error=%v", err)
	}
}

func TestSameOperationReconstructsGitFenceBeforeSQLiteActivation(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	request := integrationRequest("operation-git-before-db", "review-integration", candidate, time.Now().UTC())
	first := prepareIntegrationForTest(t, fixture, request, commitForStore('a'), integration.FenceFacts{})
	installed := fenceFactsForStoreRecord(first)
	reconstructed := prepareIntegrationForTest(t, fixture, request, commitForStore('a'), installed)
	if reconstructed.MutationGeneration != first.MutationGeneration || reconstructed.Token != first.Token || reconstructed.FencePredecessor != first.FencePredecessor || reconstructed.FenceRequestDigest != first.FenceRequestDigest {
		t.Fatalf("first=%+v reconstructed=%+v", first, reconstructed)
	}
	activated, err := fixture.store.ActivateIntegrationAuthority(fixture.ctx, reconstructed, installed)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.store.ActivateIntegrationAuthority(fixture.ctx, reconstructed, installed)
	if err != nil || replayed.OperationID != activated.OperationID || replayed.MutationGeneration != activated.MutationGeneration {
		t.Fatalf("activation replay=%+v err=%v", replayed, err)
	}
	var leases int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_mutation_leases WHERE operation_id=?`, request.OperationID).Scan(&leases); err != nil || leases != 1 {
		t.Fatalf("same-operation leases=%d err=%v", leases, err)
	}
}

func TestSupersededRepositoryFenceOperationCannotBePlannedAgain(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	now := time.Now().UTC()
	request := integrationRequest("operation-superseded-orphan", "review-integration", candidate, now)
	current := integration.FenceFacts{
		Ref:                  "refs/spec-kitty/fences/repository-1",
		Object:               commitForStore('b'),
		PredecessorObject:    commitForStore('a'),
		SupersededOperations: []ids.OperationID{request.OperationID},
		Generation:           2,
		OperationID:          "operation-current-successor",
		RepositoryID:         "repository-1",
		TargetRef:            "refs/heads/main",
		RequestDigest:        "successor-digest",
		PreflightTarget:      commitForStore('a'),
		TokenDigest:          "successor-token",
		Deadline:             now.Add(time.Minute),
	}
	if _, err := fixture.store.PrepareIntegrationAuthority(fixture.ctx, request, commitForStore('a'), current); !integration.IsKind(err, integration.KindAuthorityExpired) {
		t.Fatalf("superseded operation preparation error=%v", err)
	}
	expired := integrationRequest("operation-expired-before-fence", "review-integration", candidate, now.Add(-time.Minute))
	expired.LeaseTTL = time.Millisecond
	if _, err := fixture.store.PrepareIntegrationAuthority(fixture.ctx, expired, commitForStore('a'), integration.FenceFacts{}); !integration.IsKind(err, integration.KindAuthorityExpired) {
		t.Fatalf("expired operation preparation error=%v", err)
	}
	var commands int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_commands WHERE operation_id IN (?,?)`, request.OperationID, expired.OperationID).Scan(&commands); err != nil || commands != 0 {
		t.Fatalf("rejected preparation commands=%d err=%v", commands, err)
	}
}

func TestStaleIntegrationTokenCannotExpireCurrentAuthority(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	record := prepareIntegrationForTest(t, fixture, integrationRequest("operation-token-mutant", "review-integration", candidate, time.Now().UTC()), commitForStore('a'), integration.FenceFacts{})
	record = activateIntegrationForTest(t, fixture, record)
	mutant := record
	mutant.Token = "stale-token"
	if _, err := fixture.store.RevalidateIntegration(fixture.ctx, mutant, record.PreflightTarget); !integration.IsKind(err, integration.KindAuthorityExpired) {
		t.Fatalf("stale token error=%v", err)
	}
	var status string
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT status FROM integration_mutation_leases WHERE operation_id=?`, record.OperationID).Scan(&status); err != nil || status != "active" {
		t.Fatalf("stale token changed authority status=%q err=%v", status, err)
	}
}

func TestIntegrationAuthorityPreparationIsReadOnlyAcrossConcurrentMissions(t *testing.T) {
	fixture, candidate := approvedIntegrationFixture(t)
	now := time.Now().UTC()
	requests := []integration.Request{
		integrationRequest("operation-race-mission-a", "review-integration", candidate, now),
		integrationRequest("operation-race-mission-b", "review-integration", candidate, now.Add(time.Nanosecond)),
	}
	requests[1].ActorID, requests[1].ProcessID = "actor-mission-b", "process-mission-b"
	start := make(chan struct{})
	results := make(chan integration.Record, len(requests))
	errorsFound := make(chan error, len(requests))
	var wait sync.WaitGroup
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			record, err := fixture.store.PrepareIntegrationAuthority(context.Background(), request, commitForStore('a'), integration.FenceFacts{})
			results <- record
			errorsFound <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent read-only preparation error=%v", err)
		}
	}
	for record := range results {
		if record.MutationGeneration != 1 {
			t.Fatalf("prepared generation=%d want=1", record.MutationGeneration)
		}
	}
	var commands, leases int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_commands`).Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM integration_mutation_leases`).Scan(&leases); err != nil {
		t.Fatal(err)
	}
	if commands != 0 || leases != 0 {
		t.Fatalf("preparation published commands=%d leases=%d", commands, leases)
	}
}

func TestIntegrationEligibilityHonorsContinueSupersedeAndInvalidate(t *testing.T) {
	for _, disposition := range []workgraph.Disposition{workgraph.Continue, workgraph.Supersede, workgraph.Invalidate} {
		disposition := disposition
		t.Run(string(disposition), func(t *testing.T) {
			fixture, candidate := approvedIntegrationFixture(t)
			newWork := ids.WorkID("work-1")
			if disposition == workgraph.Supersede {
				newWork = "work-replacement"
			}
			oldRevision, err := fixture.store.WorkRevision(fixture.ctx, "revision-1")
			if err != nil {
				t.Fatal(err)
			}
			newRevision := storeRevision(t, "revision-2", 2, newWork)
			if err := fixture.store.finalizeWorkRevisionForTest(fixture.ctx, newRevision, 1); err != nil {
				t.Fatal(err)
			}
			reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{
				ID: "reconciliation-integration-" + string(disposition), Old: oldRevision, New: newRevision, Disposition: disposition,
				ActorID: "actor-owner", Rationale: "integration eligibility proof", OldWorkIDs: []ids.WorkID{"work-1"}, NewWorkIDs: []ids.WorkID{newWork}, OccurredAt: time.Now().UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.ReconcileWorkRevision(fixture.ctx, ReconcileWorkRevisionRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-" + reconciliation.ID(), OperationID: ids.OperationID(reconciliation.ID()), ActorID: reconciliation.ActorID(), ProcessID: "process-" + ids.ProcessID(disposition), OccurredAt: reconciliation.OccurredAt(), Reconciliation: reconciliation}); err != nil {
				t.Fatal(err)
			}
			request := integrationRequest(ids.OperationID("operation-eligibility-"+string(disposition)), "review-integration", candidate, time.Now().UTC())
			_, err = fixture.store.PrepareIntegrationAuthority(fixture.ctx, request, commitForStore('a'), integration.FenceFacts{})
			if disposition == workgraph.Continue && err != nil {
				t.Fatalf("continue refused old digest: %v", err)
			}
			if disposition != workgraph.Continue && !integration.IsKind(err, integration.KindRevisionIneligible) {
				t.Fatalf("%s integration error=%v", disposition, err)
			}
		})
	}
}

func approvedIntegrationFixture(t *testing.T) (*handoffReviewFixture, string) {
	t.Helper()
	fixture := newHandoffReviewFixture(t)
	candidate := commitForStore('b')
	h, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-integration", "operation-handoff-integration", candidate), fixture.evidence(candidate))
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.reviewRequest("review-integration", "operation-review-integration", "actor-reviewer-integration", "process-reviewer-integration", h, review.Approved)
	if _, err := fixture.store.CommitReview(fixture.ctx, request); err != nil {
		t.Fatal(err)
	}
	return fixture, candidate
}

func integrationRequest(operation ids.OperationID, reviewID ids.ReviewID, candidate string, now time.Time) integration.Request {
	return integration.Request{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: operation.String(), OperationID: operation, ReviewID: reviewID,
		RepositoryID: "repository-1", TargetRef: "refs/heads/main", ActorID: "actor-integrator", ProcessID: ids.ProcessID(fmt.Sprintf("process-%s", operation)),
		Candidate: candidate, LeaseTTL: time.Minute, StartedAt: now,
	}
}

func activateIntegrationForTest(t *testing.T, fixture *handoffReviewFixture, record integration.Record) integration.Record {
	t.Helper()
	activated, err := fixture.store.ActivateIntegrationAuthority(fixture.ctx, record, fenceFactsForStoreRecord(record))
	if err != nil {
		t.Fatal(err)
	}
	return activated
}

func prepareIntegrationForTest(t *testing.T, fixture *handoffReviewFixture, request integration.Request, target string, current integration.FenceFacts) integration.Record {
	t.Helper()
	record, err := fixture.store.PrepareIntegrationAuthority(fixture.ctx, request, target, current)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func fenceFactsForStoreRecord(record integration.Record) integration.FenceFacts {
	return integration.FenceFacts{
		Ref: "refs/spec-kitty/fences/repository-1", Object: commitForStore(byte('a' + record.MutationGeneration)),
		PredecessorObject: record.FencePredecessor, Generation: record.MutationGeneration, OperationID: record.OperationID,
		RepositoryID: record.RepositoryID, TargetRef: record.TargetRef, RequestDigest: record.FenceRequestDigest,
		PreflightTarget: record.PreflightTarget, TokenDigest: tokenDigest(record.Token), Deadline: record.LeaseDeadline,
	}
}
