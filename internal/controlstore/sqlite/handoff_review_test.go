package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
	_ "modernc.org/sqlite"
)

func TestShippedStartRunMigrationV7DigestRemainsFrozen(t *testing.T) {
	digest := sha256.Sum256([]byte(startRunMigrationV7))
	const shippedDigest = "b66037af69e58dc6d9d7ee19d28f5d3ca6a41c49c0c3ca716f6215ab4ce7ca77"
	if got := fmt.Sprintf("%x", digest); got != shippedDigest {
		t.Fatalf("shipped v7 digest=%s", got)
	}
}

func TestShippedRecoveryMigrationV8DigestRemainsFrozen(t *testing.T) {
	digest := sha256.Sum256([]byte(startRecoveryMigrationV8))
	const shippedDigest = "92bd217d39aeb540276a38eb7d651ce2611fcc2ff1d3011c7ade25c8ac88dca3"
	if got := fmt.Sprintf("%x", digest); got != shippedDigest {
		t.Fatalf("shipped v8 digest=%s", got)
	}
}

func TestPopulatedV7UpgradesThroughV8AndLaterMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 0; version < 7; version++ {
		if _, err := raw.ExecContext(ctx, migrations[version]); err != nil {
			t.Fatalf("apply v%d: %v", version+1, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `PRAGMA user_version=7`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO projects(project_id,created_at) VALUES('project-preserved','2026-08-24T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version, project, v8Table, v9Table, v10Table, v11Table int
	if err := store.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE project_id='project-preserved'`).Scan(&project); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='start_run_requests'`).Scan(&v8Table); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='handoffs'`).Scan(&v9Table); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='integrations'`).Scan(&v10Table); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='handoff_refusals'`).Scan(&v11Table); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || project != 1 || v8Table != 1 || v9Table != 1 || v10Table != 1 || v11Table != 1 {
		t.Fatalf("version=%d project=%d v8=%d v9=%d v10=%d v11=%d", version, project, v8Table, v9Table, v10Table, v11Table)
	}
}

func TestImmutableHandoffAndIndependentAtomicReview(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	handoffRecord, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-1", "operation-handoff-1", commitForStore('b')), fixture.evidence(commitForStore('b')))
	if err != nil {
		t.Fatal(err)
	}
	if handoffRecord.HandoffGeneration != 1 || handoffRecord.CriteriaDigest == "" || len(handoffRecord.Contributors) != 1 {
		t.Fatalf("handoff=%+v", handoffRecord)
	}

	sameActor := fixture.reviewRequest("review-self", "operation-review-self", "actor-implementer", "process-review-self", handoffRecord, review.Approved)
	if _, err := fixture.store.CommitReview(fixture.ctx, sameActor); !review.IsKind(err, review.KindReviewerNotIndependent) {
		t.Fatalf("same actor error=%v", err)
	}

	request := fixture.reviewRequest("review-1", "operation-review-1", "actor-reviewer", "process-reviewer", handoffRecord, review.Approved)
	record, err := fixture.store.CommitReview(fixture.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if record.Verdict != review.Approved || record.Candidate != handoffRecord.Candidate || len(record.Criteria) != 1 {
		t.Fatalf("review=%+v", record)
	}
	var reviews, criteria, attempts, done int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM reviews WHERE handoff_id='handoff-1'`:               &reviews,
		`SELECT COUNT(*) FROM review_criteria WHERE review_id='review-1'`:         &criteria,
		`SELECT COUNT(*) FROM review_attempts WHERE handoff_id='handoff-1'`:       &attempts,
		`SELECT COUNT(*) FROM work_items WHERE work_id='work-1' AND state='done'`: &done,
	} {
		if err := fixture.store.db.QueryRowContext(fixture.ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if reviews != 1 || criteria != 1 || attempts != 2 || done != 0 {
		t.Fatalf("reviews=%d criteria=%d attempts=%d done=%d", reviews, criteria, attempts, done)
	}
	for _, statement := range []string{
		`UPDATE handoffs SET candidate='cccccccccccccccccccccccccccccccccccccccc' WHERE handoff_id='handoff-1'`,
		`DELETE FROM handoff_contributors WHERE handoff_id='handoff-1'`,
		`UPDATE reviews SET verdict='changes_requested' WHERE review_id='review-1'`,
		`DELETE FROM review_criteria WHERE review_id='review-1'`,
	} {
		if _, err := fixture.store.db.ExecContext(fixture.ctx, statement); err == nil {
			t.Fatalf("immutable evidence accepted: %s", statement)
		}
	}
}

func TestHandoffOperationReplaySurvivesAuthorityMovementAndRejectsMutation(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	request := fixture.handoffRequest("handoff-replay", "operation-handoff-replay", commitForStore('b'))
	want, err := fixture.store.CommitHandoff(fixture.ctx, request, fixture.evidence(request.Candidate))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.ExecContext(fixture.ctx, `UPDATE leases SET status='expired' WHERE lease_id=?`, request.LeaseID); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	got, found, err := fixture.store.LookupHandoffOperation(fixture.ctx, request)
	gotJSON, _ := json.Marshal(got)
	if err != nil || !found || string(gotJSON) != string(wantJSON) {
		t.Fatalf("found=%v err=%v\nwant=%s\ngot=%s", found, err, wantJSON, gotJSON)
	}
	mutants := []handoff.Request{request, request, request}
	mutants[0].Candidate = commitForStore('c')
	mutants[1].ActorID = "actor-mutated"
	mutants[1].Contributors = []ids.ActorID{"actor-mutated"}
	mutants[2].ProcessID = "process-mutated"
	for _, mutant := range mutants {
		if _, _, err := fixture.store.LookupHandoffOperation(fixture.ctx, mutant); !handoff.IsKind(err, handoff.KindIdempotencyConflict) {
			t.Fatalf("mutant=%+v err=%v", mutant, err)
		}
	}
}

func TestHandoffRefusalReplaySurvivesAuthorityAndBranchMovement(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	request := fixture.handoffRequest("handoff-refused", "operation-handoff-refused", commitForStore('b'))
	mismatch := &handoff.Error{
		Kind: handoff.KindCandidateMismatch, Message: "owned branch moved",
		Observed: map[string]string{
			"candidate": request.Candidate, "branch_head": commitForStore('c'),
			"divergence_kind":   handoff.BranchMovementDivergence,
			"evidence_ref_root": "refs/spec-kitty/handoff-evidence/refused",
			"repository_id":     "repository-1",
		},
		Expected:        map[string]string{"branch_head": request.Candidate},
		RecoveryActions: []string{"inspect preserved refs", "reconcile explicitly"},
	}
	want, err := fixture.store.CommitHandoffRefusal(fixture.ctx, request, mismatch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.ExecContext(fixture.ctx, `UPDATE leases SET status='expired' WHERE lease_id=?`, request.LeaseID); err != nil {
		t.Fatal(err)
	}
	got, found, err := fixture.store.LookupHandoffRefusal(fixture.ctx, request)
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(got)
	if err != nil || !found || string(gotJSON) != string(wantJSON) {
		t.Fatalf("found=%v err=%v\nwant=%s\ngot=%s", found, err, wantJSON, gotJSON)
	}
	if _, err := fixture.store.CommitHandoff(fixture.ctx, request, fixture.evidence(request.Candidate)); !handoff.IsKind(err, handoff.KindCandidateMismatch) {
		t.Fatalf("refused operation became an accepted handoff: %v", err)
	}
	mutant := request
	mutant.Candidate = commitForStore('d')
	if _, _, err := fixture.store.LookupHandoffRefusal(fixture.ctx, mutant); !handoff.IsKind(err, handoff.KindIdempotencyConflict) {
		t.Fatalf("mutated refusal replay=%v", err)
	}
	for _, statement := range []string{
		`UPDATE handoff_refusals SET message='changed' WHERE operation_id='operation-handoff-refused'`,
		`DELETE FROM handoff_refusals WHERE operation_id='operation-handoff-refused'`,
	} {
		if _, err := fixture.store.db.ExecContext(fixture.ctx, statement); err == nil {
			t.Fatalf("immutable refusal accepted: %s", statement)
		}
	}
}

func TestReviewOperationReplaySurvivesLostAuthorityAndRejectsMutation(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	h, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-review-replay", "operation-handoff-review-replay", commitForStore('b')), fixture.evidence(commitForStore('b')))
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.reviewRequest("review-replay", "operation-review-replay", "actor-reviewer", "process-reviewer", h, review.Approved)
	want, err := fixture.store.CommitReview(fixture.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.ExecContext(fixture.ctx, `UPDATE leases SET status='expired' WHERE lease_id=?`, request.LeaseID); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	got, found, err := fixture.store.LookupReviewOperation(fixture.ctx, request)
	gotJSON, _ := json.Marshal(got)
	if err != nil || !found || string(gotJSON) != string(wantJSON) {
		t.Fatalf("found=%v err=%v\nwant=%s\ngot=%s", found, err, wantJSON, gotJSON)
	}
	mutants := []review.Request{request, request, request}
	mutants[0].Candidate = commitForStore('c')
	mutants[1].ActorID = "actor-mutated"
	mutants[2].ProcessID = "process-mutated"
	for _, mutant := range mutants {
		if _, _, err := fixture.store.LookupReviewOperation(fixture.ctx, mutant); !review.IsKind(err, review.KindIdempotencyConflict) {
			t.Fatalf("mutant=%+v err=%v", mutant, err)
		}
	}
}

func TestStaleAuthorityAndNewHandoffRejectOldCandidateReview(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	stale := fixture.handoffRequest("handoff-stale", "operation-handoff-stale", commitForStore('b'))
	stale.Token = "wrong-token"
	if _, err := fixture.store.CommitHandoff(fixture.ctx, stale, fixture.evidence(stale.Candidate)); !handoff.IsKind(err, handoff.KindStaleAuthority) {
		t.Fatalf("stale handoff error=%v", err)
	}
	first, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-first", "operation-handoff-first", commitForStore('b')), fixture.evidence(commitForStore('b')))
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-second", "operation-handoff-second", commitForStore('c')), fixture.evidence(commitForStore('c')))
	if err != nil {
		t.Fatal(err)
	}
	if second.HandoffGeneration != first.HandoffGeneration+1 {
		t.Fatalf("first=%d second=%d", first.HandoffGeneration, second.HandoffGeneration)
	}
	oldReview := fixture.reviewRequest("review-old", "operation-review-old", "actor-reviewer", "process-reviewer", first, review.Approved)
	if _, err := fixture.store.CommitReview(fixture.ctx, oldReview); !review.IsKind(err, review.KindRevisionIneligible) {
		t.Fatalf("old handoff review error=%v", err)
	}
}

func TestCompetingVerdictWritersLeaveOneTerminalReviewAndTwoAttributedAttempts(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	handoffRecord, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-race", "operation-handoff-race", commitForStore('b')), fixture.evidence(commitForStore('b')))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(fixture.ctx, fixture.path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	stores := []*Store{fixture.store, second}
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for index := range stores {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			request := fixture.reviewRequest(ids.ReviewID(fmt.Sprintf("review-race-%d", index)), ids.OperationID(fmt.Sprintf("operation-review-race-%d", index)), ids.ActorID(fmt.Sprintf("actor-reviewer-%d", index)), ids.ProcessID(fmt.Sprintf("process-reviewer-%d", index)), handoffRecord, review.Approved)
			_, errs[index] = stores[index].CommitReview(fixture.ctx, request)
		}(index)
	}
	wait.Wait()
	successes, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if review.IsKind(err, review.KindConflict) {
			conflicts++
		} else {
			t.Fatalf("race error=%v", err)
		}
	}
	var persisted, attempts int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM reviews WHERE handoff_id='handoff-race'`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM review_attempts WHERE handoff_id='handoff-race'`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || conflicts != 1 || persisted != 1 || attempts != 2 {
		t.Fatalf("success=%d conflict=%d persisted=%d attempts=%d errs=%v", successes, conflicts, persisted, attempts, errs)
	}
}

func TestDistinctActorChangesRequestedIsDurableAndReviewNeedsLiveAuthority(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	handoffRecord, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-reject", "operation-handoff-reject", commitForStore('b')), fixture.evidence(commitForStore('b')))
	if err != nil {
		t.Fatal(err)
	}
	stale := fixture.reviewRequest("review-stale", "operation-review-stale", "actor-reviewer", "process-reviewer-stale", handoffRecord, review.ChangesRequested)
	stale.Token = "stale-token"
	if _, err := fixture.store.CommitReview(fixture.ctx, stale); !review.IsKind(err, review.KindStaleAuthority) {
		t.Fatalf("stale review error=%v", err)
	}
	request := fixture.reviewRequest("review-reject", "operation-review-reject", "actor-reviewer", "process-reviewer", handoffRecord, review.ChangesRequested)
	recorded, err := fixture.store.CommitReview(fixture.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Verdict != review.ChangesRequested || len(recorded.Criteria) != 1 || recorded.Criteria[0].Status != review.Failed {
		t.Fatalf("rejection=%+v", recorded)
	}
	replayed, err := fixture.store.CommitReview(fixture.ctx, request)
	if err != nil || replayed.ReviewID != recorded.ReviewID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
}

func TestHandoffReviewMutationAttacksCannotAlterCandidateOrCriteria(t *testing.T) {
	fixture := newHandoffReviewFixture(t)
	handoffRecord, err := fixture.store.CommitHandoff(fixture.ctx, fixture.handoffRequest("handoff-mutation", "operation-handoff-mutation", commitForStore('b')), fixture.evidence(commitForStore('b')))
	if err != nil {
		t.Fatal(err)
	}
	mutatedCandidate := fixture.reviewRequest("review-mutated-candidate", "operation-review-mutated-candidate", "actor-reviewer-a", "process-reviewer-a", handoffRecord, review.Approved)
	mutatedCandidate.Candidate = commitForStore('c')
	if _, err := fixture.store.CommitReview(fixture.ctx, mutatedCandidate); !review.IsKind(err, review.KindCandidateMismatch) {
		t.Fatalf("candidate mutation error=%v", err)
	}
	mutatedCriteria := fixture.reviewRequest("review-mutated-criteria", "operation-review-mutated-criteria", "actor-reviewer-b", "process-reviewer-b", handoffRecord, review.Approved)
	mutatedCriteria.Criteria[0].CriterionID = "criterion-invented"
	if _, err := fixture.store.CommitReview(fixture.ctx, mutatedCriteria); !review.IsKind(err, review.KindInvalid) {
		t.Fatalf("criteria mutation error=%v", err)
	}
	var reviews int
	if err := fixture.store.db.QueryRowContext(fixture.ctx, `SELECT COUNT(*) FROM reviews WHERE handoff_id='handoff-mutation'`).Scan(&reviews); err != nil || reviews != 0 {
		t.Fatalf("mutations persisted reviews=%d err=%v", reviews, err)
	}
}

type handoffReviewFixture struct {
	t       *testing.T
	ctx     context.Context
	store   *Store
	path    string
	now     time.Time
	capsule execution.RunCapsule
}

func newHandoffReviewFixture(t *testing.T) *handoffReviewFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 24, 18, 0, 0, 0, time.UTC)
	revision := storeRevision(t, "revision-1", 1, "work-1")
	if _, err := store.FinalizeWorkRevision(ctx, FinalizeWorkRevisionRequest{SchemaVersion: 1, CorrelationID: "finalize-1", OperationID: "operation-finalize-1", ActorID: revision.ActorID(), ProcessID: "process-finalize", OccurredAt: revision.FinalizedAt(), Revision: revision}); err != nil {
		t.Fatal(err)
	}
	claim := execution.StartRequest{CorrelationID: "claim-1", OperationID: "operation-claim-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", ActorID: "actor-implementer", ProcessID: "process-implementer", Now: now, LeaseTTL: time.Hour}
	capsule, err := store.Claim(ctx, claim, "run-1", "lease-1")
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.RepositoryBinding{RepositoryID: "repository-1", ProjectID: "project-1", CommonDir: "/repo/.git", PrimaryPath: "/repo", ManagedRoot: "/runtime", TargetRef: "refs/heads/main", Primary: workspace.PrimarySnapshot{Head: commitForStore('a'), IndexDigest: "index", TreeDigest: "tree"}, CreatedAt: now, VerifiedAt: now}
	if err := store.BindRepository(ctx, binding); err != nil {
		t.Fatal(err)
	}
	reservation := workspace.Reservation{OperationID: claim.OperationID, ProjectID: claim.ProjectID, MissionID: claim.MissionID, WorkRevisionID: claim.WorkRevisionID, WorkID: claim.WorkID, RunID: capsule.RunID, WorkspaceID: "workspace-1", RepositoryID: binding.RepositoryID, LaneID: "lane-a", Authority: workspace.Authority{LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token}, OwnedBranch: "runs/work-1", PinnedBase: commitForStore('a'), Now: now}
	if _, err := store.ReserveWorkspace(ctx, reservation, "/runtime/workspace-1"); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []workspace.Stage{workspace.StageWorktreeCreated, workspace.StageBaseVerified, workspace.StageDependenciesPrepared, workspace.StageOwnershipVerified} {
		if _, err := store.db.ExecContext(ctx, `UPDATE workspaces SET stage=? WHERE workspace_id='workspace-1'`, stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE workspaces SET stage='ready',status='ready',registered=1,observed_head=?,dependency_head=?,git_dir='/repo/.git/worktrees/workspace-1',index_path='/repo/.git/worktrees/workspace-1/index' WHERE workspace_id='workspace-1'`, commitForStore('a'), commitForStore('a')); err != nil {
		t.Fatal(err)
	}
	return &handoffReviewFixture{t: t, ctx: ctx, store: store, path: path, now: now, capsule: capsule}
}

func (f *handoffReviewFixture) handoffRequest(handoffID ids.HandoffID, operationID ids.OperationID, candidate string) handoff.Request {
	return handoff.Request{SchemaVersion: 1, CorrelationID: operationID.String(), OperationID: operationID, HandoffID: handoffID, ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", RunID: f.capsule.RunID, WorkspaceID: "workspace-1", LeaseID: f.capsule.LeaseID, LeaseGeneration: f.capsule.Generation, Token: f.capsule.Token, ActorID: "actor-implementer", ProcessID: "process-implementer", Candidate: candidate, Contributors: []ids.ActorID{"actor-implementer"}, EvidenceDigest: digestForStore('e'), SubmittedAt: f.now.Add(time.Minute)}
}
func (f *handoffReviewFixture) evidence(candidate string) handoff.CandidateEvidence {
	return handoff.CandidateEvidence{Candidate: candidate, PinnedBase: commitForStore('a'), OwnedBranch: "runs/work-1", ObservedBranchHead: candidate}
}
func (f *handoffReviewFixture) reviewRequest(reviewID ids.ReviewID, operationID ids.OperationID, actor ids.ActorID, process ids.ProcessID, h handoff.Record, verdict review.Verdict) review.Request {
	status := review.Passed
	if verdict == review.ChangesRequested {
		status = review.Failed
	}
	return review.Request{SchemaVersion: 1, CorrelationID: operationID.String(), OperationID: operationID, ReviewID: reviewID, HandoffID: h.HandoffID, WorkRevisionID: h.WorkRevisionID, WorkID: h.WorkID, LeaseID: h.LeaseID, LeaseGeneration: h.LeaseGeneration, Token: f.capsule.Token, ActorID: actor, ProcessID: process, Candidate: h.Candidate, Verdict: verdict, Criteria: []review.CriterionResult{{CriterionID: "criterion-work-1", Status: status, Evidence: "go test passed"}}, EvidenceDigest: digestForStore('f'), SubmittedAt: f.now.Add(2 * time.Minute)}
}
func commitForStore(character byte) string { return string(repeated(character, 40)) }
func digestForStore(character byte) string { return string(repeated(character, 64)) }
func repeated(character byte, length int) []byte {
	value := make([]byte, length)
	for index := range value {
		value[index] = character
	}
	return value
}
