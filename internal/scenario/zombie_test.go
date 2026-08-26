package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestZombieWriterCommitsAfterExpiryButCannotCrossControlPlane(t *testing.T) {
	t.Parallel()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-zombie", lane: "lane-zombie", tracker: "TK-ZOMBIE"}})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	seedExpiredGenerations(t, ctx, fixture.database, 16)

	aInput := workspaceStart{operation: "operation-zombie-a", work: "work-zombie", lane: "lane-zombie", actor: "actor-zombie-a", process: "process-zombie-a", workspace: "workspace-zombie-w1", branch: "runs/zombie-w1", trackerWork: "TK-ZOMBIE", at: "2026-08-24T12:00:00Z"}
	aResponse := waitWorkspaceProcess(t, fixture.start(ctx, t, aInput))
	if !aResponse.OK || aResponse.Capsule == nil || aResponse.Capsule.Generation != 17 {
		t.Fatalf("generation-17 start=%+v", aResponse)
	}
	a := aResponse.Capsule
	if err := os.WriteFile(filepath.Join(fixture.barrier, "ZOMBIE.action"), []byte("block\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zombie := startZombieProcess(t, ctx, fixture, *a)
	t.Cleanup(func() {
		if zombie.command.ProcessState == nil {
			_ = zombie.command.Process.Signal(syscall.SIGCONT)
			_ = zombie.command.Process.Kill()
		}
	})
	if err := waitForBoundaryReached(ctx, fixture.barrier, "ZOMBIE", 1); err != nil {
		t.Fatal(err)
	}
	if err := zombie.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}

	expireAuthorityProcess(t, ctx, fixture.database, *a, aInput, a.Deadline)
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	w1BeforeResume, err := control.Workspace(ctx, a.WorkspaceID)
	if err != nil || !workspace.IsQuarantined(w1BeforeResume) {
		t.Fatalf("W1 not quarantined: %+v err=%v", w1BeforeResume, err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	bInput := workspaceStart{operation: "operation-zombie-b", work: "work-zombie", lane: "lane-zombie", actor: "actor-zombie-b", process: "process-zombie-b", workspace: "workspace-zombie-w2", branch: "runs/zombie-w2", trackerWork: "TK-ZOMBIE", trackerRevision: 2, at: "2026-08-24T12:01:00Z"}
	bResponse := waitWorkspaceProcess(t, fixture.start(ctx, t, bInput))
	if !bResponse.OK || bResponse.Capsule == nil || bResponse.Capsule.Generation != 18 {
		t.Fatalf("generation-18 replacement=%+v", bResponse)
	}
	b := bResponse.Capsule
	if a.WorkspaceID == b.WorkspaceID || a.WorkspacePath == b.WorkspacePath || a.OwnedBranch == b.OwnedBranch || a.IndexPath == b.IndexPath {
		t.Fatalf("replacement reused W1: A=%+v B=%+v", a, b)
	}
	w2HeadBefore := scenarioGit(t, b.WorkspacePath, "rev-parse", "HEAD")
	fixture.assertPrimaryUnchanged(t)

	if err := zombie.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, "ZOMBIE.release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	zombieResponse := waitZombieProcess(t, zombie)
	if !zombieResponse.OK || zombieResponse.Result == nil || zombieResponse.Result.Commit == "" {
		t.Fatalf("zombie did not commit after resume: %+v", zombieResponse)
	}
	if zombieResponse.Result.HeartbeatRefusal != execution.KindStaleAuthority || zombieResponse.Result.ExpireRefusal != execution.KindStaleAuthority || zombieResponse.Result.WorkspaceRefusal != workspace.KindStaleAuthority {
		t.Fatalf("stale seams were not fenced: %+v", zombieResponse.Result)
	}
	if got := scenarioGit(t, a.WorkspacePath, "rev-parse", "HEAD"); got != zombieResponse.Result.Commit {
		t.Fatalf("W1 head=%s zombie commit=%s", got, zombieResponse.Result.Commit)
	}
	if got := scenarioGit(t, b.WorkspacePath, "rev-parse", "HEAD"); got != w2HeadBefore {
		t.Fatalf("zombie changed W2: %s -> %s", w2HeadBefore, got)
	}
	fixture.assertPrimaryUnchanged(t)

	oldRecovery := recoverWorkspaceOperation(t, ctx, fixture, aInput.operation)
	if !oldRecovery.OK || oldRecovery.Decision == nil || oldRecovery.Decision.Disposition != execution.RecoveryExpired || oldRecovery.Decision.Diagnostic == nil || oldRecovery.Decision.Diagnostic.Kind != execution.KindStaleAuthority {
		t.Fatalf("old operation recovery=%+v", oldRecovery)
	}
	adapter, err := gitrepo.Bind(ctx, gitrepo.Runner{}, "repository-1", "project-1", fixture.repository, fixture.managedRoot, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := adapter.Inspect(ctx, w1BeforeResume)
	if err != nil || inspection.ObservedHead != zombieResponse.Result.Commit || len(inspection.Discrepancies) == 0 || !containsCommit(inspection.PreservedCommits, w1BeforeResume.DependencyHead) || !containsCommit(inspection.PreservedCommits, zombieResponse.Result.Commit) {
		t.Fatalf("zombie inspection=%+v err=%v", inspection, err)
	}
	if err := adapter.Cleanup(ctx, w1BeforeResume, "expired"); !workspace.IsKind(err, workspace.KindCleanupRefused) {
		t.Fatalf("quarantine cleanup error=%v", err)
	}
	if got := scenarioGit(t, fixture.repository, "rev-parse", a.OwnedBranch); got != zombieResponse.Result.Commit {
		t.Fatalf("quarantined evidence lost: branch=%s commit=%s", got, zombieResponse.Result.Commit)
	}
}

func TestZombieRevisionInvalidationUsesSameExpiryQuarantineTransition(t *testing.T) {
	t.Parallel()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-invalidate", lane: "lane-invalidate", tracker: "TK-INVALIDATE"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := workspaceStart{operation: "operation-invalidate-run", work: "work-invalidate", lane: "lane-invalidate", actor: "actor-invalidate", process: "process-invalidate", workspace: "workspace-invalidate", branch: "runs/invalidate", trackerWork: "TK-INVALIDATE"}
	response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
	if !response.OK || response.Capsule == nil {
		t.Fatalf("start=%+v", response)
	}
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	oldRevision, err := control.WorkRevision(ctx, "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	newRevision := scenarioRevision(t, "revision-invalidate-2", 2, []ids.WorkID{"work-invalidate"})
	if err := finalizeScenarioRevision(ctx, control, newRevision, 1); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 8, 24, 12, 2, 0, 0, time.UTC)
	reconciliation, err := workgraph.Reconcile(workgraph.ReconcileRequest{ID: "reconciliation-invalidate-zombie", Old: oldRevision, New: newRevision, Disposition: workgraph.Invalidate, ActorID: "actor-owner", Rationale: "revision invalidates the live contract", OldWorkIDs: []ids.WorkID{"work-invalidate"}, NewWorkIDs: []ids.WorkID{"work-invalidate"}, OccurredAt: when})
	if err != nil {
		t.Fatal(err)
	}
	if err := reconcileScenarioRevision(ctx, control, reconciliation); err != nil {
		t.Fatal(err)
	}
	for _, status := range []workgraph.WorkStatus{workgraph.StatusActive, workgraph.StatusHandedOff, workgraph.StatusApproved} {
		eligibility, reconciliationID, err := control.WorkEligibility(ctx, "revision-1", "work-invalidate", status)
		if err != nil || reconciliationID != reconciliation.ID() || eligibility.RunMayContinue || eligibility.HandoffEligible || eligibility.IntegrationEligible {
			t.Fatalf("stale %s seam remained eligible: reconciliation=%s eligibility=%+v err=%v", status, reconciliationID, eligibility, err)
		}
	}
	record, err := control.Workspace(ctx, response.Capsule.WorkspaceID)
	if err != nil || !workspace.IsQuarantined(record) || record.QuarantineReason != workspace.QuarantineRevisionInvalidated {
		t.Fatalf("invalidated workspace=%+v err=%v", record, err)
	}
	heartbeat := execution.HeartbeatRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "invalidate-stale", OperationID: response.Capsule.OperationID, ActorID: "actor-invalidate", ProcessID: "process-invalidate", LeaseID: response.Capsule.LeaseID, Generation: response.Capsule.Generation, Token: response.Capsule.Token, Now: when, LeaseTTL: time.Minute}
	if _, err := control.Heartbeat(ctx, heartbeat); !execution.IsKind(err, execution.KindStaleAuthority) {
		t.Fatalf("invalidated token remained live: %v", err)
	}
	fixture.assertPrimaryUnchanged(t)
}

func seedExpiredGenerations(t *testing.T, ctx context.Context, database string, count int) {
	t.Helper()
	control, err := sqlite.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	for generation := 1; generation <= count; generation++ {
		operation := ids.OperationID(fmt.Sprintf("operation-prior-%02d", generation))
		request := execution.StartRequest{CorrelationID: operation.String(), OperationID: operation, ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-zombie", ActorID: "actor-prior", ProcessID: "process-prior", Now: base, LeaseTTL: time.Minute, GovernanceContext: scenarioGovernanceContext(t, ids.GovernanceContextID("context-"+operation.String()), base)}
		capsule, err := control.Claim(ctx, request, ids.RunID(fmt.Sprintf("run-prior-%02d", generation)), ids.LeaseID(fmt.Sprintf("lease-prior-%02d", generation)))
		if err != nil || capsule.Generation != int64(generation) {
			t.Fatalf("seed generation %d capsule=%+v err=%v", generation, capsule, err)
		}
		if err := control.Expire(ctx, execution.ExpireRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "expire-" + operation.String(), OperationID: operation, ActorID: request.ActorID, ProcessID: request.ProcessID, LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token, Now: capsule.Deadline}); err != nil {
			t.Fatalf("expire generation %d: %v", generation, err)
		}
	}
}

func startZombieProcess(t *testing.T, ctx context.Context, fixture workspaceScenario, capsule execution.RunCapsule) *claimProcess {
	t.Helper()
	command := exec.CommandContext(ctx, helperBinary, "zombie-write", "--db", fixture.database, "--barrier", fixture.barrier, "--operation", capsule.OperationID.String(), "--token", capsule.Token, "--file", "zombie.txt", "--content", "dangerous but contained\n", "--at", "2026-08-24T12:02:00Z")
	process := &claimProcess{command: command}
	command.Stdout, command.Stderr = &process.stdout, &process.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return process
}

func waitZombieProcess(t *testing.T, process *claimProcess) helpercmd.ZombieResponse {
	t.Helper()
	if err := process.command.Wait(); err != nil {
		t.Fatalf("zombie helper: %v stderr=%s stdout=%s", err, process.stderr.String(), process.stdout.String())
	}
	var response helpercmd.ZombieResponse
	if err := json.Unmarshal(process.stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode zombie %q: %v", process.stdout.String(), err)
	}
	return response
}

func expireAuthorityProcess(t *testing.T, ctx context.Context, database string, capsule execution.RunCapsule, input workspaceStart, at time.Time) {
	t.Helper()
	command := exec.CommandContext(ctx, helperBinary, "expire-authority", "--db", database, "--correlation-id", "expire-"+input.operation, "--operation", input.operation, "--actor", input.actor, "--process", input.process, "--lease", capsule.LeaseID.String(), "--generation", fmt.Sprint(capsule.Generation), "--token", capsule.Token, "--at", at.UTC().Format(time.RFC3339Nano))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("expire helper: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	var response helpercmd.Response
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || !response.OK {
		t.Fatalf("expire response=%+v decode=%v raw=%q", response, err, stdout.String())
	}
}

func containsCommit(commits []string, target string) bool {
	for _, commit := range commits {
		if commit == target {
			return true
		}
	}
	return false
}
