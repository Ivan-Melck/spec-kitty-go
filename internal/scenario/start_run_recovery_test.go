package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestStartRunRecoveryCrashMatrix(t *testing.T) {
	t.Parallel()
	for index, boundary := range []string{"SR-00", "SR-01", "SR-02", "SR-03", "SR-04", "SR-05", "SR-06", "SR-07", "SR-08", "SR-09"} {
		t.Run(boundary, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-recovery", lane: "lane-recovery", tracker: "TK-RECOVERY"}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: fmt.Sprintf("operation-recovery-%02d", index), work: "work-recovery", lane: "lane-recovery", actor: "actor-recovery", process: fmt.Sprintf("process-recovery-%02d", index), workspace: fmt.Sprintf("workspace-recovery-%02d", index), branch: fmt.Sprintf("runs/recovery-%02d", index), trackerWork: "TK-RECOVERY"}
			crashWorkspaceAt(t, ctx, fixture, boundary, input)
			assertHonestStartCrashState(t, ctx, fixture, input, boundary)

			var capsule *execution.RunCapsule
			if boundary == "SR-00" {
				response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
				if !response.OK || response.Capsule == nil {
					t.Fatalf("full-envelope retry=%+v", response)
				}
				capsule = response.Capsule
			} else {
				response := recoverWorkspaceOperation(t, ctx, fixture, input.operation)
				if !response.OK || response.Decision == nil || response.Decision.Disposition != execution.RecoveryComplete || response.Decision.Capsule == nil {
					t.Fatalf("operation-only recovery=%+v", response)
				}
				capsule = response.Decision.Capsule
			}
			if capsule.OperationID != ids.OperationID(input.operation) || capsule.WorkspaceID != ids.WorkspaceID(input.workspace) || capsule.WorkspaceStage != workspace.StageReady || capsule.Token == "" {
				t.Fatalf("recovered capsule=%+v", capsule)
			}
			assertOneRecoveredStart(t, ctx, fixture, input, capsule)
		})
	}
}

func assertHonestStartCrashState(t *testing.T, ctx context.Context, fixture workspaceScenario, input workspaceStart, boundary string) {
	t.Helper()
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	snapshot, err := control.AuthoritySnapshot(ctx, "work-recovery", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	if boundary == "SR-00" {
		if snapshot.Operations != 0 || snapshot.Runs != 0 || snapshot.ActiveLeases != 0 {
			t.Fatalf("SR-00 published state: %+v", snapshot)
		}
		return
	}
	recovery, err := control.LoadStartRun(ctx, ids.OperationID(input.operation))
	if err != nil || recovery.TerminalRecorded {
		t.Fatalf("%s recovery state=%+v err=%v", boundary, recovery, err)
	}
	if boundary == "SR-01" {
		if recovery.OperationStatus != "started" || recovery.LeaseStatus != "" || snapshot.Runs != 0 || snapshot.ActiveLeases != 0 {
			t.Fatalf("SR-01 published lease/run: record=%+v snapshot=%+v", recovery, snapshot)
		}
		return
	}
	if recovery.OperationStatus != "succeeded" || recovery.LeaseStatus != "active" || snapshot.Runs != 1 || snapshot.ActiveLeases != 1 {
		t.Fatalf("%s authority facts: record=%+v snapshot=%+v", boundary, recovery, snapshot)
	}
	if boundary < "SR-05" {
		if recovery.WorkspaceStatus != "" || recovery.WorkspaceStage != "" {
			t.Fatalf("%s published phantom workspace: %+v", boundary, recovery)
		}
		return
	}
	wantStage := workspace.StageReserved
	if boundary == "SR-07" || boundary == "SR-08" {
		wantStage = workspace.StageBaseVerified
	} else if boundary == "SR-09" {
		wantStage = workspace.StageReady
	}
	if recovery.WorkspaceStage != wantStage || (boundary != "SR-09" && recovery.WorkspaceStatus == "ready") {
		t.Fatalf("%s false readiness: %+v want_stage=%s", boundary, recovery, wantStage)
	}
	path := filepath.Join(fixture.managedRoot, input.workspace)
	_, statErr := os.Stat(path)
	if boundary == "SR-05" && !os.IsNotExist(statErr) {
		t.Fatalf("SR-05 reservation unexpectedly created path: %v", statErr)
	}
	if boundary >= "SR-06" && statErr != nil {
		t.Fatalf("%s lost created worktree: %v", boundary, statErr)
	}
}

func TestStartRunRecoveryRefusesPartialDependencyHeadAndConflictNeverReady(t *testing.T) {
	t.Parallel()
	for _, variant := range []string{"partial", "conflict"} {
		t.Run(variant, func(t *testing.T) {
			t.Parallel()
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-dependencies", lane: "lane-dependencies", tracker: "TK-DEPENDENCIES"}})
			left := scenarioCommitOnBranch(t, fixture.repository, "accepted/left", "same.txt", "left\n")
			rightFile, rightContent := "right.txt", "right\n"
			if variant == "conflict" {
				rightFile, rightContent = "same.txt", "right\n"
			}
			right := scenarioCommitOnBranch(t, fixture.repository, "accepted/right", rightFile, rightContent)
			scenarioGit(t, fixture.repository, "checkout", "main")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-dependencies-" + variant, work: "work-dependencies", lane: "lane-dependencies", actor: "actor-dependencies", process: "process-dependencies-" + variant, workspace: "workspace-dependencies-" + variant, branch: "runs/dependencies-" + variant, trackerWork: "TK-DEPENDENCIES", dependencies: fmt.Sprintf("work-left=%s,work-right=%s", left, right)}
			if variant == "partial" {
				crashWorkspaceAt(t, ctx, fixture, "SR-07", input)
				path := filepath.Join(fixture.managedRoot, input.workspace)
				scenarioGit(t, path, "cherry-pick", left)
				response := recoverWorkspaceOperation(t, ctx, fixture, input.operation)
				if !response.OK || response.Decision == nil || response.Decision.Disposition != execution.RecoveryDecisionRequired || response.Decision.Diagnostic == nil || response.Decision.Diagnostic.Kind != execution.KindTargetChanged {
					t.Fatalf("partial recovery=%+v", response)
				}
			} else {
				response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
				if response.OK || response.Error == nil || response.Error.Kind != execution.KindDependencyConflict {
					t.Fatalf("conflict response=%+v", response)
				}
			}
			control, err := sqlite.Open(ctx, fixture.database)
			if err != nil {
				t.Fatal(err)
			}
			record, err := control.Workspace(ctx, ids.WorkspaceID(input.workspace))
			_ = control.Close()
			if err != nil || record.Ready() || record.Status == "ready" || record.Stage == workspace.StageReady {
				t.Fatalf("partial/conflicting workspace reported ready: %+v err=%v", record, err)
			}
			fixture.assertPrimaryUnchanged(t)
		})
	}
}

func recoverWorkspaceOperation(t *testing.T, ctx context.Context, fixture workspaceScenario, operation string) helpercmd.RecoveryResponse {
	t.Helper()
	command := exec.CommandContext(ctx, helperBinary, "recover-start",
		"--db", fixture.database, "--tracker-store", fixture.tracker, "--repository", fixture.repository,
		"--workspace-root", fixture.managedRoot, "--barrier", fixture.barrier,
		"--operation", operation, "--project", "project-1", "--repository-id", "repository-1",
		"--correlation-id", "recover-"+operation, "--at", "2026-08-24T12:00:00Z")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("recover helper: %v stderr=%s stdout=%s", err, stderr.String(), stdout.String())
	}
	var response helpercmd.RecoveryResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode recovery %q: %v", stdout.String(), err)
	}
	return response
}

func assertOneRecoveredStart(t *testing.T, ctx context.Context, fixture workspaceScenario, input workspaceStart, capsule *execution.RunCapsule) {
	t.Helper()
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	snapshot, err := control.AuthoritySnapshot(ctx, "work-recovery", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ActiveLeases != 1 || snapshot.Runs != 1 || snapshot.Operations != 1 || snapshot.Events != 1 {
		t.Fatalf("duplicate recovery state=%+v", snapshot)
	}
	record, err := control.LoadStartRun(ctx, ids.OperationID(input.operation))
	if err != nil || !record.TerminalRecorded || record.OperationStatus != "succeeded" || record.LeaseStatus != "active" || record.WorkspaceStatus != "ready" || record.WorkspaceStage != workspace.StageReady || record.RunID != capsule.RunID || record.LeaseID != capsule.LeaseID {
		t.Fatalf("recovery record=%+v err=%v capsule=%+v", record, err, capsule)
	}
	providerStore, err := fake.Open(fixture.tracker)
	if err != nil {
		t.Fatal(err)
	}
	work, err := fake.NewProvider(providerStore).Read(ctx, input.trackerWork)
	if err != nil || work.Revision != 2 || work.Assignee != input.actor || work.AssignmentMarker != input.operation {
		t.Fatalf("tracker assignment=%+v err=%v", work, err)
	}
	fixture.assertPrimaryUnchanged(t)
}

func scenarioCommitOnBranch(t *testing.T, repository, branch, name, content string) string {
	t.Helper()
	scenarioGit(t, repository, "checkout", "-B", branch, "main")
	if err := os.WriteFile(filepath.Join(repository, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, repository, "add", "--", name)
	scenarioGit(t, repository, "commit", "-m", branch)
	return scenarioGit(t, repository, "rev-parse", "HEAD")
}
