package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestShippedWorkGraphMigrationV6DigestRemainsFrozen(t *testing.T) {
	digest := sha256.Sum256([]byte(workGraphMigrationV6))
	const shippedDigest = "91e83bba3dac529ccef8fb79d4b7b45599f44d2497f07697e63d80af84b07a67"
	if got := fmt.Sprintf("%x", digest); got != shippedDigest {
		t.Fatalf("shipped v6 digest=%s", got)
	}
}

func TestWorkspaceReservationBindingAndRepositoryFactsAreImmutable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	revision := storeRevision(t, "revision-1", 1, "work-1")
	if _, err := store.FinalizeWorkRevision(ctx, FinalizeWorkRevisionRequest{SchemaVersion: 1, CorrelationID: "finalize-1", OperationID: "operation-finalize-1", ActorID: revision.ActorID(), ProcessID: "process-finalize-1", OccurredAt: revision.FinalizedAt(), Revision: revision}); err != nil {
		t.Fatal(err)
	}
	claim := execution.StartRequest{CorrelationID: "operation-1", OperationID: "operation-1", ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", ActorID: "actor-1", ProcessID: "process-1", Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-1", now)}
	capsule, err := store.Claim(ctx, claim, "run-1", "lease-1")
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.RepositoryBinding{RepositoryID: "repository-1", ProjectID: "project-1", CommonDir: "/repo/.git", PrimaryPath: "/repo", ManagedRoot: "/runtime/workspaces", TargetRef: "refs/heads/main", Primary: workspace.PrimarySnapshot{Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IndexDigest: "index", TreeDigest: "tree"}, CreatedAt: now, VerifiedAt: now}
	if err := store.BindRepository(ctx, binding); err != nil {
		t.Fatal(err)
	}
	reservation := workspace.Reservation{OperationID: claim.OperationID, ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1", RunID: capsule.RunID, WorkspaceID: "workspace-1", RepositoryID: "repository-1", LaneID: "lane-1", Authority: workspace.Authority{LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token}, OwnedBranch: "runs/work-1", PinnedBase: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Now: now}
	if _, err := store.ReserveWorkspace(ctx, reservation, "/runtime/escaped/workspace-1"); !workspace.IsKind(err, workspace.KindPathUnsafe) {
		t.Fatalf("workspace path escape error=%v", err)
	}
	if _, err := store.ReserveWorkspace(ctx, reservation, "/runtime/workspaces/workspace-1"); err != nil {
		t.Fatal(err)
	}
	changed := reservation
	changed.OwnedBranch = "runs/rebound"
	if _, err := store.ReserveWorkspace(ctx, changed, "/runtime/workspaces/workspace-1"); !workspace.IsKind(err, workspace.KindWorkspaceConflict) {
		t.Fatalf("workspace rebind error=%v", err)
	}
	for _, statement := range []string{
		`UPDATE repositories SET common_dir='/other/.git' WHERE repository_id='repository-1'`,
		`UPDATE workspaces SET workspace_id='workspace-rewritten' WHERE workspace_id='workspace-1'`,
		`UPDATE workspaces SET lease_id='other-lease' WHERE workspace_id='workspace-1'`,
		`UPDATE workspaces SET stage='ready' WHERE workspace_id='workspace-1'`,
	} {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("immutable/monotonic guard accepted %s", statement)
		}
	}
	blockedTx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blockedTx.ExecContext(ctx, `UPDATE workspaces SET status='blocked' WHERE workspace_id='workspace-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := blockedTx.ExecContext(ctx, `UPDATE workspaces SET status='preparing' WHERE workspace_id='workspace-1'`); err == nil {
		t.Fatal("blocked workspace was revived")
	}
	if err := blockedTx.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []workspace.Stage{workspace.StageWorktreeCreated, workspace.StageBaseVerified, workspace.StageDependenciesPrepared, workspace.StageOwnershipVerified} {
		if _, err := store.db.ExecContext(ctx, `UPDATE workspaces SET stage=? WHERE workspace_id='workspace-1'`, stage); err != nil {
			t.Fatalf("advance direct test fixture to %s: %v", stage, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE workspaces SET stage='ready',status='ready' WHERE workspace_id='workspace-1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE workspaces SET status='quarantined',quarantine_reason='review drift',quarantined_at=? WHERE workspace_id='workspace-1'`, formatTime(now)); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE workspaces SET status='ready',quarantine_reason='',quarantined_at='' WHERE workspace_id='workspace-1'`,
		`UPDATE workspaces SET quarantine_reason='',quarantined_at='' WHERE workspace_id='workspace-1'`,
	} {
		if _, err := store.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("quarantine revival/evidence rewrite accepted %s", statement)
		}
	}
	loaded, err := store.Workspace(ctx, ids.WorkspaceID("workspace-1"))
	if err != nil || loaded.Authority.LeaseID != "lease-1" || loaded.Stage != workspace.StageReady || loaded.Status != "quarantined" || loaded.QuarantineReason != "review drift" || loaded.OwnedBranch != "runs/work-1" {
		t.Fatalf("workspace changed despite guards: %+v err=%v", loaded, err)
	}
}

func TestPopulatedV6UpgradesThroughV8WithoutLosingTrackerOrWorkGraphEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for version := 0; version < 6; version++ {
		if _, err := raw.ExecContext(ctx, migrations[version]); err != nil {
			t.Fatalf("apply shipped migration v%d: %v", version+1, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `PRAGMA user_version=6`); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO projects(project_id,created_at) VALUES('project-1','2026-08-24T12:00:00Z')`,
		`INSERT INTO actors(actor_id,created_at) VALUES('actor-1','2026-08-24T12:00:00Z')`,
		`INSERT INTO processes(process_id,actor_id,observed_at) VALUES('process-1','actor-1','2026-08-24T12:00:00Z')`,
		`INSERT INTO work_revisions(work_revision_id,project_id,mission_id,revision_number,finalized_at) VALUES('revision-1','project-1','mission-1',1,'2026-08-24T12:00:00Z')`,
		`INSERT INTO work_items(work_id,work_revision_id,state) VALUES('work-1','revision-1','ready')`,
		`INSERT INTO work_graph_items(work_revision_id,work_id) VALUES('revision-1','work-1')`,
		`INSERT INTO work_graph_revisions(work_revision_id,graph_digest,criteria_digest,author_actor_id,encoded_revision) VALUES('revision-1','graph-digest','criteria-digest','actor-1',x'7b7d')`,
		`INSERT INTO external_effects(operation_id,schema_version,correlation_id,actor_id,process_id,provider,work_key,marker,prior_revision,prior_value,applied_revision,applied_value,response_status,compensation_status,compensation_revision,conflict_revision,conflict_value,conflict_marker,observed_at) VALUES('operation-tracker',1,'correlation-1','actor-1','process-1','fake','TK-1','operation-tracker',1,'',2,'agent-1','confirmed','pending',0,0,'','','2026-08-24T12:00:00Z')`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			t.Fatalf("populate v6: %v\n%s", err, statement)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open populated v6 through v8: %v", err)
	}
	defer store.Close()
	var version int
	if err := store.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM external_effects WHERE operation_id='operation-tracker' AND applied_value='agent-1' AND response_status='confirmed'`,
		`SELECT COUNT(*) FROM work_graph_revisions WHERE work_revision_id='revision-1' AND graph_digest='graph-digest'`,
		`SELECT COUNT(*) FROM work_graph_items WHERE work_revision_id='revision-1' AND work_id='work-1'`,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='workspaces'`,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='immutable_workspace_binding_update'`,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='start_run_requests'`,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='start_run_results'`,
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("preservation query count=%d err=%v: %s", count, err, query)
		}
	}
}
