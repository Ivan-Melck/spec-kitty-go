package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestGovernanceContextMigrationPreservesHistoricalRunsFromEveryShippedVersion(t *testing.T) {
	for version := 1; version < currentSchemaVersion; version++ {
		version := version
		t.Run("v"+strconv.Itoa(version), func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "control.db")
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			for index := 0; index < version; index++ {
				if _, err := raw.ExecContext(ctx, migrations[index]); err != nil {
					t.Fatalf("apply migration %d: %v", index+1, err)
				}
			}
			if _, err := raw.ExecContext(ctx, "PRAGMA user_version="+strconv.Itoa(version)); err != nil {
				t.Fatal(err)
			}
			seedHistoricalAuthority(t, ctx, raw, version)
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".token-key", make([]byte, 32), 0o600); err != nil {
				t.Fatal(err)
			}

			store, err := Open(ctx, path)
			if err != nil {
				t.Fatalf("open shipped v%d: %v", version, err)
			}
			defer store.Close()
			governance, err := store.GovernanceForLease(ctx, "lease-history")
			if err != nil {
				t.Fatal(err)
			}
			if governance.RunID != "run-history" || governance.LeaseID != "lease-history" || governance.Provenance != GovernanceUnknownPreProvenance || governance.Context.ID() != "" {
				t.Fatalf("historical provenance=%+v", governance)
			}
			var operations, runs, leases int
			if err := store.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM operations),(SELECT COUNT(*) FROM runs),(SELECT COUNT(*) FROM leases)`).Scan(&operations, &runs, &leases); err != nil {
				t.Fatal(err)
			}
			if operations != 2 || runs != 2 || leases != 2 {
				t.Fatalf("preservation counts operations=%d runs=%d leases=%d", operations, runs, leases)
			}
			preservationQueries := []string{}
			if version >= 7 {
				preservationQueries = append(preservationQueries, `SELECT COUNT(*) FROM workspaces WHERE workspace_id='workspace-history' AND run_id='run-history'`)
			}
			if version >= 8 {
				preservationQueries = append(preservationQueries, `SELECT COUNT(*) FROM start_run_requests WHERE operation_id='operation-history'`, `SELECT COUNT(*) FROM start_run_results WHERE operation_id='operation-history'`)
			}
			if version >= 9 {
				preservationQueries = append(preservationQueries, `SELECT COUNT(*) FROM handoffs WHERE handoff_id='handoff-history'`, `SELECT COUNT(*) FROM reviews WHERE review_id='review-history'`)
			}
			if version >= 10 {
				preservationQueries = append(preservationQueries, `SELECT COUNT(*) FROM integrations WHERE operation_id='integration-history'`, `SELECT COUNT(*) FROM integration_done WHERE review_id='review-history'`)
			}
			if version >= 11 {
				preservationQueries = append(preservationQueries, `SELECT COUNT(*) FROM handoff_refusals WHERE operation_id='refusal-history'`)
			}
			for _, query := range preservationQueries {
				var count int
				if err := store.db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 1 {
					t.Fatalf("preservation query count=%d err=%v: %s", count, err, query)
				}
			}
		})
	}
}

func TestGovernanceContextClaimIsAtomicImmutableInspectableAndReplayable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	seed := Seed{ProjectID: "project-governance", MissionID: "mission-governance", WorkRevisionID: "revision-governance", WorkID: "work-governance"}
	seedCanonicalAuthority(t, ctx, store, seed)
	request := execution.StartRequest{CorrelationID: "operation-governance", OperationID: "operation-governance", ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: "actor-governance", ProcessID: "process-governance", Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-governance", now)}

	first, err := store.Claim(ctx, request, "run-governance", "lease-governance")
	if err != nil {
		t.Fatal(err)
	}
	if first.GovernanceContextID != request.GovernanceContext.ID() || first.GovernanceContextAggregate != request.GovernanceContext.Aggregate() {
		t.Fatalf("capsule governance=%+v", first)
	}
	var runContextID string
	if err := store.db.QueryRowContext(ctx, `SELECT governance_context_id FROM runs WHERE run_id=?`, first.RunID).Scan(&runContextID); err != nil || runContextID != request.GovernanceContext.ID().String() {
		t.Fatalf("run context id=%q err=%v", runContextID, err)
	}
	if duplicated, err := tableHasColumn(ctx, store.db, "leases", "governance_context_id"); err != nil || duplicated {
		t.Fatalf("lease governance duplication=%t err=%v", duplicated, err)
	}
	replayed, err := store.Claim(ctx, request, "run-unused", "lease-unused")
	if err != nil || !replayed.Replayed || replayed.GovernanceContextID != first.GovernanceContextID || replayed.GovernanceContextAggregate != first.GovernanceContextAggregate {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	byRun, err := store.GovernanceForRun(ctx, first.RunID)
	if err != nil || byRun.Provenance != GovernanceVerified || byRun.Context.ID() != request.GovernanceContext.ID() {
		t.Fatalf("run governance=%+v err=%v", byRun, err)
	}
	byLease, err := store.GovernanceForLease(ctx, first.LeaseID)
	if err != nil || byLease.RunID != first.RunID || byLease.Context.Aggregate() != request.GovernanceContext.Aggregate() {
		t.Fatalf("lease governance=%+v err=%v", byLease, err)
	}
	for _, statement := range []string{
		`UPDATE governance_contexts SET aggregate_digest='changed' WHERE context_id='context-governance'`,
		`UPDATE governance_context_entries SET revision='changed' WHERE context_id='context-governance'`,
		`DELETE FROM governance_context_entries WHERE context_id='context-governance'`,
		`DELETE FROM governance_contexts WHERE context_id='context-governance'`,
	} {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("immutable context accepted %s", statement)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	afterReopen, err := reopened.GovernanceForLease(ctx, first.LeaseID)
	if err != nil || afterReopen.Context.Aggregate() != request.GovernanceContext.Aggregate() {
		t.Fatalf("reopened governance=%+v err=%v", afterReopen, err)
	}
}

func TestGovernanceContextClaimRejectsMissingRebindingAndRollsBackPartialWrite(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	revision := storeRevisionMany(t, "revision-atomic", 1, []ids.WorkID{"work-one", "work-two", "work-three", "work-four"})
	if _, err := store.FinalizeWorkRevision(ctx, FinalizeWorkRevisionRequest{SchemaVersion: 1, CorrelationID: "finalize-atomic", OperationID: "finalize-atomic", ActorID: revision.ActorID(), ProcessID: "process-finalize-atomic", OccurredAt: revision.FinalizedAt(), Revision: revision}); err != nil {
		t.Fatal(err)
	}
	base := execution.StartRequest{ProjectID: revision.ProjectID(), MissionID: revision.MissionID(), WorkRevisionID: revision.ID(), ActorID: "actor-atomic", ProcessID: "process-atomic", Now: now, LeaseTTL: time.Minute}
	missing := base
	missing.CorrelationID, missing.OperationID, missing.WorkID = "operation-missing", "operation-missing", "work-one"
	if _, err := store.Claim(ctx, missing, "run-missing", "lease-missing"); !execution.IsKind(err, execution.KindInvalidCommand) {
		t.Fatalf("missing context error=%v", err)
	}
	var missingRuns, missingOperations int
	if err := store.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM runs WHERE run_id='run-missing'),(SELECT COUNT(*) FROM operations WHERE operation_id='operation-missing')`).Scan(&missingRuns, &missingOperations); err != nil {
		t.Fatal(err)
	}
	if missingRuns != 0 || missingOperations != 0 {
		t.Fatalf("missing context produced partial authority: runs=%d operations=%d", missingRuns, missingOperations)
	}

	first := base
	first.CorrelationID, first.OperationID, first.WorkID = "operation-one", "operation-one", "work-one"
	first.GovernanceContext = testGovernanceContextOrdered(t, "context-shared", "revision-1", false, now)
	if _, err := store.Claim(ctx, first, "run-one", "lease-one"); err != nil {
		t.Fatal(err)
	}
	rebound := base
	rebound.CorrelationID, rebound.OperationID, rebound.WorkID = "operation-two", "operation-two", "work-two"
	rebound.GovernanceContext = testGovernanceContextOrdered(t, "context-shared", "revision-changed", false, now)
	if _, err := store.Claim(ctx, rebound, "run-two", "lease-two"); !execution.IsKind(err, execution.KindIdempotencyConflict) {
		t.Fatalf("rebound context error=%v", err)
	}

	reordered := base
	reordered.CorrelationID, reordered.OperationID, reordered.WorkID = "operation-three", "operation-three", "work-three"
	reordered.GovernanceContext = testGovernanceContextOrdered(t, "context-shared", "revision-1", true, now)
	if _, err := store.Claim(ctx, reordered, "run-three", "lease-three"); !execution.IsKind(err, execution.KindIdempotencyConflict) {
		t.Fatalf("reordered context error=%v", err)
	}

	partial := base
	partial.CorrelationID, partial.OperationID, partial.WorkID = "operation-four", "operation-four", "work-four"
	partial.GovernanceContext = testGovernanceContext(t, "context-partial", now)
	if _, err := store.Claim(ctx, partial, "run-one", "lease-four"); err == nil {
		t.Fatal("duplicate run identity unexpectedly succeeded")
	}
	var contexts, entries int
	if err := store.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM governance_contexts WHERE context_id='context-partial'),(SELECT COUNT(*) FROM governance_context_entries WHERE context_id='context-partial')`).Scan(&contexts, &entries); err != nil {
		t.Fatal(err)
	}
	if contexts != 0 || entries != 0 {
		t.Fatalf("partial context escaped rolled-back claim: contexts=%d entries=%d", contexts, entries)
	}
}

func TestGovernanceContextReadRejectsCorruptOrdering(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	seed := Seed{ProjectID: "project-corrupt", MissionID: "mission-corrupt", WorkRevisionID: "revision-corrupt", WorkID: "work-corrupt"}
	seedCanonicalAuthority(t, ctx, store, seed)
	request := execution.StartRequest{CorrelationID: "operation-corrupt", OperationID: "operation-corrupt", ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: "actor-corrupt", ProcessID: "process-corrupt", Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-corrupt", now)}
	if _, err := store.Claim(ctx, request, "run-corrupt", "lease-corrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER immutable_governance_context_entry_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE governance_context_entries SET position=position+10 WHERE context_id='context-corrupt'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GovernanceForRun(ctx, "run-corrupt"); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("corrupt ordering error=%v", err)
	}
}

func TestGovernanceContextReadRejectsAggregateMismatch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	seed := Seed{ProjectID: "project-mismatch", MissionID: "mission-mismatch", WorkRevisionID: "revision-mismatch", WorkID: "work-mismatch"}
	seedCanonicalAuthority(t, ctx, store, seed)
	request := execution.StartRequest{CorrelationID: "operation-mismatch", OperationID: "operation-mismatch", ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: "actor-mismatch", ProcessID: "process-mismatch", Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-mismatch", now)}
	if _, err := store.Claim(ctx, request, "run-mismatch", "lease-mismatch"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER immutable_governance_context_update`); err != nil {
		t.Fatal(err)
	}
	wrong, err := governancecontext.CommitSHA256([]byte("wrong aggregate"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE governance_contexts SET aggregate_digest=? WHERE context_id='context-mismatch'`, wrong.Digest()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GovernanceForLease(ctx, "lease-mismatch"); !execution.IsKind(err, execution.KindAuthorityConflict) {
		t.Fatalf("aggregate mismatch error=%v", err)
	}
}

func testGovernanceContext(t *testing.T, id ids.GovernanceContextID, now time.Time) governancecontext.Context {
	t.Helper()
	return testGovernanceContextRevision(t, id, "revision-1", now)
}

func testGovernanceContextRevision(t *testing.T, id ids.GovernanceContextID, revision string, now time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.CommitSHA256([]byte("profile-content-" + revision))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", revision, commitment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewContext(id, governancecontext.SchemaVersion, []governancecontext.Entry{entry}, now)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testGovernanceContextOrdered(t *testing.T, id ids.GovernanceContextID, revision string, reverse bool, now time.Time) governancecontext.Context {
	t.Helper()
	configCommitment, err := governancecontext.CommitSHA256([]byte("config-content"))
	if err != nil {
		t.Fatal(err)
	}
	profileCommitment, err := governancecontext.CommitSHA256([]byte("profile-content-" + revision))
	if err != nil {
		t.Fatal(err)
	}
	config, err := governancecontext.NewEntry(governancecontext.KindConfig, "project-config", "revision-1", configCommitment)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", revision, profileCommitment)
	if err != nil {
		t.Fatal(err)
	}
	entries := []governancecontext.Entry{config, profile}
	if reverse {
		entries[0], entries[1] = entries[1], entries[0]
	}
	value, err := governancecontext.NewContext(id, governancecontext.SchemaVersion, entries, now)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func seedHistoricalAuthority(t *testing.T, ctx context.Context, db *sql.DB, version int) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	statements := []string{
		`INSERT INTO projects(project_id,created_at) VALUES('project-history','2026-08-25T12:00:00Z')`,
		`INSERT INTO actors(actor_id,created_at) VALUES('actor-history','2026-08-25T12:00:00Z')`,
		`INSERT INTO processes(process_id,actor_id,observed_at) VALUES('process-history','actor-history','2026-08-25T12:00:00Z')`,
		`INSERT INTO work_revisions(work_revision_id,project_id,mission_id,revision_number,finalized_at) VALUES('revision-history','project-history','mission-history',1,'2026-08-25T12:00:00Z')`,
		`INSERT INTO work_items(work_id,work_revision_id,state) VALUES('work-history','revision-history','ready')`,
		`INSERT INTO work_items(work_id,work_revision_id,state) VALUES('work-expired','revision-history','ready')`,
		`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,result_run_id,result_lease_id,result_generation,result_deadline,created_at) VALUES('operation-history','start_run','operation-history','operation-history','digest-history','succeeded','run-history','lease-history',1,'2026-08-25T13:00:00Z','2026-08-25T12:00:00Z')`,
		`INSERT INTO operations(operation_id,operation_kind,idempotency_key,correlation_id,request_digest,status,result_run_id,result_lease_id,result_generation,result_deadline,created_at) VALUES('operation-expired','start_run','operation-expired','operation-expired','digest-expired','succeeded','run-expired','lease-expired',1,'2026-08-25T11:00:00Z','2026-08-25T10:00:00Z')`,
		`INSERT INTO runs(run_id,operation_id,project_id,mission_id,work_id,work_revision_id,actor_id,process_id,generation,status,created_at) VALUES('run-history','operation-history','project-history','mission-history','work-history','revision-history','actor-history','process-history',1,'active','2026-08-25T12:00:00Z')`,
		`INSERT INTO runs(run_id,operation_id,project_id,mission_id,work_id,work_revision_id,actor_id,process_id,generation,status,created_at) VALUES('run-expired','operation-expired','project-history','mission-history','work-expired','revision-history','actor-history','process-history',1,'expired','2026-08-25T10:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed shipped v%d: %v\n%s", version, err, statement)
		}
	}
	if version >= 5 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_items(work_revision_id,work_id) VALUES('revision-history','work-history'),('revision-history','work-expired')`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO work_graph_revisions(work_revision_id,graph_digest,criteria_digest,author_actor_id,encoded_revision) VALUES('revision-history','graph','criteria','actor-history',x'7b7d')`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO leases(lease_id,run_id,work_id,work_revision_id,generation,token_digest,status,heartbeat_at,deadline) VALUES('lease-history','run-history','work-history','revision-history',1,'token-digest','active','2026-08-25T12:00:00Z','2026-08-25T13:00:00Z')`); err != nil {
		t.Fatalf("seed shipped v%d lease: %v", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO leases(lease_id,run_id,work_id,work_revision_id,generation,token_digest,status,heartbeat_at,deadline) VALUES('lease-expired','run-expired','work-expired','revision-history',1,'expired-token-digest','expired','2026-08-25T10:00:00Z','2026-08-25T11:00:00Z')`); err != nil {
		t.Fatalf("seed shipped v%d expired lease: %v", version, err)
	}
	if version >= 7 {
		statements := []string{
			`INSERT INTO repositories(repository_id,project_id,common_dir,primary_path,managed_root,target_ref,primary_head,primary_index_digest,primary_tree_digest,created_at,verified_at) VALUES('repository-history','project-history','/repo/.git','/repo','/managed','refs/heads/main','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','index','tree','2026-08-25T12:00:00Z','2026-08-25T12:00:00Z')`,
			`INSERT INTO workspaces(workspace_id,repository_id,operation_id,run_id,lease_id,lease_generation,lane_id,canonical_path,owned_branch,pinned_base,dependencies_json,dependency_digest,dependency_head,stage,status,registered,observed_head,git_dir,index_path,created_at,updated_at) VALUES('workspace-history','repository-history','operation-history','run-history','lease-history',1,'lane-history','/managed/workspace-history','runs/history','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',x'5b5d','dependencies','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','ready','ready',1,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','/repo/.git/worktrees/history','/repo/.git/worktrees/history/index','2026-08-25T12:00:00Z','2026-08-25T12:00:00Z')`,
		}
		for _, statement := range statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatalf("seed shipped v%d workspace: %v\n%s", version, err, statement)
			}
		}
	}
	if version >= 8 {
		for _, statement := range []string{
			`INSERT INTO start_run_requests(operation_id,request_digest,request_json,created_at) VALUES('operation-history','request-history',x'7b7d','2026-08-25T12:00:00Z')`,
			`INSERT INTO start_run_results(operation_id,result_digest,result_json,completed_at) VALUES('operation-history','result-history',x'7b7d','2026-08-25T12:30:00Z')`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatalf("seed shipped v%d recovery: %v\n%s", version, err, statement)
			}
		}
	}
	if version >= 9 {
		for _, statement := range []string{
			`INSERT INTO handoff_commands(operation_id,request_digest,result_handoff_id,actor_id,process_id,occurred_at) VALUES('handoff-operation-history','handoff-digest','handoff-history','actor-history','process-history','2026-08-25T12:15:00Z')`,
			`INSERT INTO handoff_contributors(handoff_id,actor_id) VALUES('handoff-history','actor-history')`,
			`INSERT INTO handoffs(handoff_id,operation_id,handoff_generation,project_id,mission_id,work_revision_id,work_id,run_id,workspace_id,repository_id,lease_id,lease_generation,lane_id,candidate,pinned_base,contributor_digest,criteria_digest,evidence_digest,verification_json,submitted_at) VALUES('handoff-history','handoff-operation-history',1,'project-history','mission-history','revision-history','work-history','run-history','workspace-history','repository-history','lease-history',1,'lane-history','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','contributors','criteria','evidence',x'7b7d','2026-08-25T12:15:00Z')`,
			`INSERT INTO review_criteria(review_id,criterion_id,status,evidence) VALUES('review-history','criterion-history','passed','verified')`,
			`INSERT INTO reviews(review_id,operation_id,request_digest,handoff_id,work_revision_id,work_id,candidate,reviewer_actor_id,reviewer_process_id,verdict,evidence_digest,submitted_at) VALUES('review-history','review-operation-history','review-digest','handoff-history','revision-history','work-history','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','actor-history','process-history','approved','review-evidence','2026-08-25T12:20:00Z')`,
			`INSERT INTO review_attempts(operation_id,request_digest,handoff_id,review_id,actor_id,process_id,candidate,verdict,outcome,occurred_at) VALUES('review-operation-history','review-digest','handoff-history','review-history','actor-history','process-history','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','approved','submitted','2026-08-25T12:20:00Z')`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatalf("seed shipped v%d handoff/review: %v\n%s", version, err, statement)
			}
		}
	}
	if version >= 10 {
		for _, statement := range []string{
			`INSERT INTO integration_commands(operation_id,request_digest,review_id,actor_id,process_id,token_envelope,occurred_at) VALUES('integration-history','integration-digest','review-history','actor-history','process-history',x'00','2026-08-25T12:25:00Z')`,
			`INSERT INTO integration_mutation_leases(operation_id,repository_id,target_ref,generation,token_digest,status,deadline,acquired_at,fence_ref,fence_object) VALUES('integration-history','repository-history','refs/heads/main',1,'integration-token','released','2026-08-25T12:30:00Z','2026-08-25T12:25:00Z','refs/spec-kitty/fence','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`,
			`INSERT INTO integrations(operation_id,review_id,handoff_id,mission_id,work_revision_id,work_id,repository_id,target_ref,actor_id,process_id,candidate,preflight_target,workspace_path,result_commit,observed_target,status,evidence_json,started_at,done_at,cleanup_status,cleanup_eligible_at,cleanup_completed_at) VALUES('integration-history','review-history','handoff-history','mission-history','revision-history','work-history','repository-history','refs/heads/main','actor-history','process-history','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','/managed/workspace-history','cccccccccccccccccccccccccccccccccccccccc','cccccccccccccccccccccccccccccccccccccccc','done',x'7b7d','2026-08-25T12:25:00Z','2026-08-25T12:26:00Z','complete','2026-08-25T12:26:00Z','2026-08-25T12:27:00Z')`,
			`INSERT INTO integration_done(review_id,operation_id,repository_id,target_ref,candidate,result_commit,observed_target,done_at) VALUES('review-history','integration-history','repository-history','refs/heads/main','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','cccccccccccccccccccccccccccccccccccccccc','cccccccccccccccccccccccccccccccccccccccc','2026-08-25T12:26:00Z')`,
		} {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				t.Fatalf("seed shipped v%d integration: %v\n%s", version, err, statement)
			}
		}
	}
	if version >= 11 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO handoff_refusals(operation_id,request_digest,error_kind,message,observed_json,expected_json,recovery_actions_json,occurred_at) VALUES('refusal-history','refusal-digest','candidate_mismatch','candidate changed',x'7b7d',x'7b7d',x'5b5d','2026-08-25T12:40:00Z')`); err != nil {
			t.Fatalf("seed shipped v%d refusal: %v", version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
