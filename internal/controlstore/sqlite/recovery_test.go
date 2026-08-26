package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestShippedStartRunMigrationV7RemainsFrozenAndV8IsNamed(t *testing.T) {
	v7 := sha256.Sum256([]byte(startRunMigrationV7))
	const shippedV7 = "b66037af69e58dc6d9d7ee19d28f5d3ca6a41c49c0c3ca716f6215ab4ce7ca77"
	if got := fmt.Sprintf("%x", v7); got != shippedV7 {
		t.Fatalf("shipped v7 digest=%s", got)
	}
	v8 := sha256.Sum256([]byte(startRecoveryMigrationV8))
	const namedV8 = "92bd217d39aeb540276a38eb7d651ce2611fcc2ff1d3011c7ade25c8ac88dca3"
	if got := fmt.Sprintf("%x", v8); got != namedV8 {
		t.Fatalf("startRecoveryMigrationV8 digest=%s", got)
	}
}

func TestExpiryCASQuarantinesWorkspaceAndReplacementNeverRebindsIt(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seed := Seed{ProjectID: "project-recovery", MissionID: "mission-recovery", WorkRevisionID: "revision-recovery", WorkID: "work-recovery"}
	seedCanonicalAuthority(t, ctx, store, seed)
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	firstRequest := execution.StartRequest{CorrelationID: "operation-generation-17", OperationID: "operation-generation-17", ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, ActorID: "actor-a", ProcessID: "process-a", Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-generation-17", now)}
	first, err := store.Claim(ctx, firstRequest, "run-generation-17", "lease-generation-17")
	if err != nil {
		t.Fatal(err)
	}
	binding := workspace.RepositoryBinding{RepositoryID: "repository-recovery", ProjectID: seed.ProjectID, CommonDir: "/repo/.git", PrimaryPath: "/repo", ManagedRoot: "/managed", TargetRef: "refs/heads/main", Primary: workspace.PrimarySnapshot{Head: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IndexDigest: "index", TreeDigest: "tree"}, CreatedAt: now, VerifiedAt: now}
	if err := store.BindRepository(ctx, binding); err != nil {
		t.Fatal(err)
	}
	reservation := workspace.Reservation{OperationID: first.OperationID, ProjectID: seed.ProjectID, MissionID: seed.MissionID, WorkRevisionID: seed.WorkRevisionID, WorkID: seed.WorkID, RunID: first.RunID, WorkspaceID: "workspace-generation-17", RepositoryID: binding.RepositoryID, LaneID: "lane-1", Authority: workspace.Authority{LeaseID: first.LeaseID, Generation: first.Generation, Token: first.Token}, OwnedBranch: "runs/generation-17", PinnedBase: binding.Primary.Head, Now: now}
	if _, err := store.ReserveWorkspace(ctx, reservation, "/managed/workspace-generation-17"); err != nil {
		t.Fatal(err)
	}
	expire := execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "expire-generation-17", OperationID: first.OperationID, ActorID: firstRequest.ActorID, ProcessID: firstRequest.ProcessID, LeaseID: first.LeaseID, Generation: first.Generation, Token: first.Token, Now: first.Deadline}
	if err := store.Expire(ctx, expire); err != nil {
		t.Fatal(err)
	}
	quarantined, err := store.Workspace(ctx, reservation.WorkspaceID)
	if err != nil || !workspace.IsQuarantined(quarantined) || quarantined.Authority.LeaseID != first.LeaseID || quarantined.Authority.Generation != first.Generation {
		t.Fatalf("quarantined=%+v err=%v", quarantined, err)
	}
	if _, err := store.Heartbeat(ctx, execution.HeartbeatRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "stale-heartbeat", OperationID: first.OperationID, ActorID: firstRequest.ActorID, ProcessID: firstRequest.ProcessID, LeaseID: first.LeaseID, Generation: first.Generation, Token: first.Token, Now: first.Deadline, LeaseTTL: time.Minute}); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("stale heartbeat error=%v", err)
	}

	secondRequest := firstRequest
	secondRequest.CorrelationID, secondRequest.OperationID = "operation-generation-18", "operation-generation-18"
	secondRequest.ActorID, secondRequest.ProcessID, secondRequest.Now = "actor-b", "process-b", first.Deadline
	secondRequest.GovernanceContext = testGovernanceContext(t, "context-generation-18", first.Deadline)
	second, err := store.Claim(ctx, secondRequest, "run-generation-18", "lease-generation-18")
	if err != nil {
		t.Fatal(err)
	}
	if second.Generation != first.Generation+1 {
		t.Fatalf("replacement generation=%d want=%d", second.Generation, first.Generation+1)
	}
	replacement := reservation
	replacement.OperationID, replacement.RunID, replacement.WorkspaceID = second.OperationID, second.RunID, "workspace-generation-18"
	replacement.Authority = workspace.Authority{LeaseID: second.LeaseID, Generation: second.Generation, Token: second.Token}
	replacement.OwnedBranch, replacement.Now = "runs/generation-18", secondRequest.Now
	if _, err := store.ReserveWorkspace(ctx, replacement, "/managed/workspace-generation-18"); err != nil {
		t.Fatal(err)
	}
	rebind := replacement
	rebind.WorkspaceID, rebind.OwnedBranch = reservation.WorkspaceID, reservation.OwnedBranch
	if _, err := store.ReserveWorkspace(ctx, rebind, "/managed/workspace-generation-17"); !workspace.IsKind(err, workspace.KindWorkspaceConflict) {
		t.Fatalf("quarantined workspace rebind error=%v", err)
	}
	loaded, err := store.Workspace(ctx, reservation.WorkspaceID)
	if err != nil || loaded.Authority.LeaseID != first.LeaseID || loaded.Status != "quarantined" {
		t.Fatalf("old binding changed: %+v err=%v", loaded, err)
	}
}

func TestRecoveryEnvelopeAndTerminalResultAreImmutableAndTokenFree(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	request := execution.StartRunRequest{SchemaVersion: execution.ResultSchemaVersion, Claim: execution.StartRequest{CorrelationID: "operation-envelope", OperationID: "operation-envelope", ProjectID: "project-envelope", MissionID: "mission-envelope", WorkRevisionID: "revision-envelope", WorkID: "work-envelope", ActorID: "actor-envelope", ProcessID: "process-envelope", Now: now, LeaseTTL: time.Minute, CommandDigest: "full-command-digest", GovernanceContext: testGovernanceContext(t, "context-envelope", now)}, Assignment: execution.TrackerAssignRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "operation-envelope", OperationID: "operation-envelope", ActorID: "actor-envelope", ProcessID: "process-envelope", Now: now, Provider: "fake", WorkKey: "TK-ENVELOPE", ExpectedRevision: 1, Assignee: "actor-envelope"}, Workspace: execution.WorkspaceRequest{RepositoryID: "repository-envelope", LaneID: "lane-envelope", WorkspaceID: "workspace-envelope", OwnedBranch: "runs/envelope", PinnedBase: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	if err := store.PrepareStartRun(ctx, request); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadStartRun(ctx, request.Claim.OperationID)
	if err != nil || loaded.Request.Claim.ActorID != request.Claim.ActorID || loaded.OperationStatus != "started" ||
		loaded.Request.Claim.GovernanceContext.ID() != request.Claim.GovernanceContext.ID() || loaded.Request.Claim.GovernanceContext.Aggregate() != request.Claim.GovernanceContext.Aggregate() {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	seedCanonicalAuthority(t, ctx, store, Seed{ProjectID: request.Claim.ProjectID, MissionID: request.Claim.MissionID, WorkRevisionID: request.Claim.WorkRevisionID, WorkID: request.Claim.WorkID})
	claimed, err := store.Claim(ctx, request.Claim, "run-envelope", "lease-envelope")
	if err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadStartRun(ctx, request.Claim.OperationID)
	if err != nil || loaded.RunID != claimed.RunID || loaded.LeaseID != claimed.LeaseID ||
		loaded.Request.Claim.GovernanceContext.ID() != request.Claim.GovernanceContext.ID() ||
		loaded.Request.Claim.GovernanceContext.Aggregate() != request.Claim.GovernanceContext.Aggregate() {
		t.Fatalf("claimed recovery governance=%+v err=%v", loaded, err)
	}
	capsule := execution.RunCapsule{SchemaVersion: execution.ResultSchemaVersion, Kind: "run_ready", CorrelationID: request.Claim.CorrelationID, OperationID: request.Claim.OperationID, AggregateIDs: map[string]string{"operation_id": request.Claim.OperationID.String(), "governance_context_id": request.Claim.GovernanceContext.ID().String()}, GovernanceContextID: request.Claim.GovernanceContext.ID(), GovernanceContextAggregate: request.Claim.GovernanceContext.Aggregate(), Token: "must-not-persist", Replayed: true}
	mismatched := capsule
	mismatched.GovernanceContextID = "context-mismatched"
	if err := store.CompleteStartRun(ctx, mismatched, now); !execution.IsKind(err, execution.KindIdempotencyConflict) {
		t.Fatalf("mismatched terminal governance error=%v", err)
	}
	var premature int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM start_run_results WHERE operation_id=?`, request.Claim.OperationID).Scan(&premature); err != nil || premature != 0 {
		t.Fatalf("mismatched terminal persisted count=%d err=%v", premature, err)
	}
	if err := store.CompleteStartRun(ctx, capsule, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteStartRun(ctx, capsule, now); err != nil {
		t.Fatalf("terminal replay: %v", err)
	}
	var encoded string
	if err := store.db.QueryRowContext(ctx, `SELECT CAST(result_json AS TEXT) FROM start_run_results WHERE operation_id=?`, request.Claim.OperationID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || strings.Contains(encoded, "must-not-persist") {
		t.Fatalf("terminal result leaked token or was empty: %q", encoded)
	}
	var terminal execution.RunCapsule
	if err := json.Unmarshal([]byte(encoded), &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.GovernanceContextID != request.Claim.GovernanceContext.ID() || terminal.GovernanceContextAggregate != request.Claim.GovernanceContext.Aggregate() {
		t.Fatalf("terminal governance=%+v", terminal)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE start_run_requests SET request_digest='changed' WHERE operation_id=?`, request.Claim.OperationID); err == nil {
		t.Fatal("immutable recovery request accepted update")
	}
}

func TestRecoveryGovernanceConsumerRejectsEnvelopeRunDisagreement(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	request := execution.StartRunRequest{
		SchemaVersion: execution.ResultSchemaVersion,
		Claim: execution.StartRequest{
			CorrelationID: "operation-governance-recovery", OperationID: "operation-governance-recovery",
			ProjectID: "project-governance-recovery", MissionID: "mission-governance-recovery",
			WorkRevisionID: "revision-governance-recovery", WorkID: "work-governance-recovery",
			ActorID: "actor-governance-recovery", ProcessID: "process-governance-recovery",
			Now: now, LeaseTTL: time.Minute, GovernanceContext: testGovernanceContext(t, "context-governance-recovery", now),
		},
	}
	seedCanonicalAuthority(t, ctx, store, Seed{ProjectID: request.Claim.ProjectID, MissionID: request.Claim.MissionID, WorkRevisionID: request.Claim.WorkRevisionID, WorkID: request.Claim.WorkID})
	if err := store.PrepareStartRun(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(ctx, request.Claim, "run-governance-recovery", "lease-governance-recovery"); err != nil {
		t.Fatal(err)
	}
	tampered := request
	tampered.Claim.GovernanceContext = testGovernanceContext(t, "context-tampered-recovery", now)
	encoded, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TRIGGER immutable_start_run_request_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE start_run_requests SET request_json=? WHERE operation_id=?`, encoded, request.Claim.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadStartRun(ctx, request.Claim.OperationID); !execution.IsKind(err, execution.KindIdempotencyConflict) {
		t.Fatalf("recovery governance disagreement error=%v", err)
	}
}
