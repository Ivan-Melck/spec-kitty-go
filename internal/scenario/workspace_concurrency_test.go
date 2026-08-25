package scenario

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestWorkspaceConcurrency(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-left", lane: "lane-left", tracker: "TK-LEFT"}, {work: "work-right", lane: "lane-right", tracker: "TK-RIGHT"}})
	for _, boundary := range []string{"SR-00", "WORK", "EDIT"} {
		if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("block\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	left := fixture.start(ctx, t, workspaceStart{operation: "operation-left", work: "work-left", lane: "lane-left", actor: "actor-shared", process: "process-left", workspace: "workspace-left", branch: "runs/work-left", trackerWork: "TK-LEFT", editFile: "left.txt", editContent: "left\n"})
	right := fixture.start(ctx, t, workspaceStart{operation: "operation-right", work: "work-right", lane: "lane-right", actor: "actor-shared", process: "process-right", workspace: "workspace-right", branch: "runs/work-right", trackerWork: "TK-RIGHT", editFile: "right.txt", editContent: "right\n"})

	waitAndRelease(t, ctx, fixture.barrier, "SR-00", 2)
	waitAndRelease(t, ctx, fixture.barrier, "WORK", 2)
	waitAndRelease(t, ctx, fixture.barrier, "EDIT", 2)
	responses := []helpercmd.Response{waitWorkspaceProcess(t, left), waitWorkspaceProcess(t, right)}
	for index, response := range responses {
		if !response.OK || response.Capsule == nil || response.Work == nil || response.Capsule.Kind != "run_ready" || response.Capsule.WorkspaceStage != "ready" {
			t.Fatalf("response %d = %+v", index, response)
		}
		if response.Capsule.SchemaVersion != execution.ResultSchemaVersion || response.Capsule.CorrelationID == "" || response.Capsule.AggregateIDs["actor_id"] == "" || response.Capsule.AggregateIDs["process_id"] == "" {
			t.Fatalf("response %d omitted command authority envelope: %+v", index, response.Capsule)
		}
		if response.Work.StartedAt.IsZero() || response.Work.EndedAt.IsZero() || !response.Work.StartedAt.Before(response.Work.EndedAt) {
			t.Fatalf("response %d invalid active interval: %+v", index, response.Work)
		}
	}
	leftCapsule, rightCapsule := responses[0].Capsule, responses[1].Capsule
	if leftCapsule.RunID == rightCapsule.RunID || leftCapsule.LeaseID == rightCapsule.LeaseID || leftCapsule.Token == rightCapsule.Token || leftCapsule.WorkspaceID == rightCapsule.WorkspaceID || leftCapsule.WorkspacePath == rightCapsule.WorkspacePath || leftCapsule.OwnedBranch == rightCapsule.OwnedBranch || leftCapsule.GitDir == rightCapsule.GitDir || leftCapsule.IndexPath == rightCapsule.IndexPath || responses[0].Work.Commit == responses[1].Work.Commit {
		t.Fatalf("mutable identities overlapped: left=%+v/%+v right=%+v/%+v", leftCapsule, responses[0].Work, rightCapsule, responses[1].Work)
	}
	if !responses[0].Work.StartedAt.Before(responses[1].Work.EndedAt) || !responses[1].Work.StartedAt.Before(responses[0].Work.EndedAt) {
		t.Fatalf("active intervals did not overlap: left=%+v right=%+v", responses[0].Work, responses[1].Work)
	}
	for _, response := range responses {
		if head := scenarioGit(t, response.Capsule.WorkspacePath, "rev-parse", "HEAD"); head != response.Work.Commit {
			t.Fatalf("workspace HEAD=%s commit=%s", head, response.Work.Commit)
		}
		if branchHead := scenarioGit(t, fixture.repository, "rev-parse", response.Capsule.OwnedBranch); branchHead != response.Work.Commit {
			t.Fatalf("owned branch head=%s commit=%s", branchHead, response.Work.Commit)
		}
	}
	fixture.assertPrimaryUnchanged(t)

	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	for _, workspaceID := range []ids.WorkspaceID{"workspace-left", "workspace-right"} {
		record, err := control.Workspace(ctx, workspaceID)
		if err != nil || !record.Ready() {
			t.Fatalf("persisted workspace %s=%+v err=%v", workspaceID, record, err)
		}
	}
}

func TestWorkspaceCrashReplayMatrixSR05ThroughSR09(t *testing.T) {
	for _, boundary := range []string{"SR-05", "SR-06", "SR-07", "SR-08", "SR-09"} {
		t.Run(boundary, func(t *testing.T) {
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-crash", lane: "lane-crash", tracker: "TK-CRASH"}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-crash", work: "work-crash", lane: "lane-crash", actor: "actor-crash", process: "process-crash", workspace: "workspace-crash", branch: "runs/work-crash", trackerWork: "TK-CRASH"}
			crashWorkspaceAt(t, ctx, fixture, boundary, input)
			replay := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
			if !replay.OK || replay.Capsule == nil || !replay.Capsule.Replayed || replay.Capsule.Token == "" || replay.Capsule.WorkspaceStage != "ready" || replay.Capsule.WorkspaceID != "workspace-crash" {
				t.Fatalf("%s crash replay=%+v", boundary, replay)
			}
			control, err := sqlite.Open(ctx, fixture.database)
			if err != nil {
				t.Fatal(err)
			}
			record, err := control.Workspace(ctx, "workspace-crash")
			_ = control.Close()
			if err != nil || !record.Ready() || record.RunID != replay.Capsule.RunID || record.Authority.LeaseID != replay.Capsule.LeaseID {
				t.Fatalf("recovered workspace=%+v err=%v capsule=%+v", record, err, replay.Capsule)
			}
			fixture.assertPrimaryUnchanged(t)
		})
	}
}

func TestStartRunChangedReplayIdentityNeverRevealsPriorToken(t *testing.T) {
	for _, attack := range []struct {
		name     string
		boundary string
		mutate   func(*workspaceStart)
	}{
		{"process", "SR-02", func(input *workspaceStart) { input.process = "process-changed" }},
		{"lane", "SR-02", func(input *workspaceStart) { input.lane = "lane-changed" }},
		{"base", "SR-02", func(input *workspaceStart) { input.base = "9999999999999999999999999999999999999999" }},
		{"repository", "SR-02", func(input *workspaceStart) { input.repositoryID = "repository-changed" }},
		{"work", "SR-02", func(input *workspaceStart) { input.work = "work-changed" }},
		{"revision", "SR-02", func(input *workspaceStart) { input.revision = "revision-changed" }},
		{"dependencies", "SR-02", func(input *workspaceStart) {
			input.dependencies = "work-dependency=1111111111111111111111111111111111111111"
		}},
		{"workspace-branch", "SR-04", func(input *workspaceStart) { input.workspace = "workspace-changed"; input.branch = "runs/changed" }},
		{"tracker", "SR-04", func(input *workspaceStart) { input.trackerWork = "TK-CHANGED" }},
	} {
		t.Run(attack.boundary+"-"+attack.name, func(t *testing.T) {
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-attack", lane: "lane-attack", tracker: "TK-ATTACK"}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-attack", work: "work-attack", lane: "lane-attack", actor: "actor-attack", process: "process-attack", workspace: "workspace-attack", branch: "runs/attack", trackerWork: "TK-ATTACK"}
			crashWorkspaceAt(t, ctx, fixture, attack.boundary, input)
			attack.mutate(&input)
			response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
			if response.OK || response.Capsule != nil || response.Error == nil || response.Error.Kind != execution.KindIdempotencyConflict {
				t.Fatalf("changed replay exposed prior result: %+v", response)
			}
		})
	}
}

func TestStartRunExactReplayRejectsDifferentPhysicalRepositoryBinding(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-binding", lane: "lane-binding", tracker: "TK-BINDING"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := workspaceStart{operation: "operation-binding", work: "work-binding", lane: "lane-binding", actor: "actor-binding", process: "process-binding", workspace: "workspace-binding", branch: "runs/binding", trackerWork: "TK-BINDING"}
	crashWorkspaceAt(t, ctx, fixture, "SR-04", input)

	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	before, err := control.AuthoritySnapshot(ctx, "work-binding", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	durableBefore, err := control.Repository(ctx, "repository-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}

	clone := filepath.Join(t.TempDir(), "repository-clone")
	scenarioGitClone(t, fixture.repository, clone)
	cloneReplay := input
	cloneReplay.physicalRepository = clone
	rejected := waitWorkspaceProcess(t, fixture.start(ctx, t, cloneReplay))
	if rejected.OK || rejected.Capsule != nil || rejected.Error == nil || rejected.Error.Kind != execution.KindRepositoryConflict || rejected.Error.RetrySafety != execution.RetryDecisionRequired {
		t.Fatalf("different physical repository replay=%+v", rejected)
	}
	if rejected.Error.SchemaVersion != execution.ResultSchemaVersion || rejected.Error.CorrelationID != input.operation || rejected.Error.OperationID != ids.OperationID(input.operation) || len(rejected.Error.RecoveryActions) < 2 {
		t.Fatalf("incomplete binding diagnostic=%+v", rejected.Error)
	}
	for _, fact := range []string{"repository_id", "project_id", "common_dir", "primary_path", "managed_root", "target_ref", "primary_head", "primary_index_digest", "primary_tree_digest"} {
		if _, ok := rejected.Error.ObservedState[fact]; !ok {
			t.Fatalf("binding diagnostic omitted observed %s: %+v", fact, rejected.Error)
		}
		if _, ok := rejected.Error.ExpectedState[fact]; !ok {
			t.Fatalf("binding diagnostic omitted expected %s: %+v", fact, rejected.Error)
		}
	}
	if rejected.Error.ObservedState["common_dir"] == rejected.Error.ExpectedState["common_dir"] || rejected.Error.ObservedState["primary_path"] == rejected.Error.ExpectedState["primary_path"] {
		t.Fatalf("physical mismatch was not reported: %+v", rejected.Error)
	}
	if _, err := os.Stat(filepath.Join(fixture.managedRoot, input.workspace)); !os.IsNotExist(err) {
		t.Fatalf("rejected replay published workspace: %v", err)
	}
	if scenarioGitSucceeds(clone, "show-ref", "--verify", "--quiet", "refs/heads/"+input.branch) {
		t.Fatal("rejected replay created its owned branch in clone B")
	}
	if worktrees := scenarioGit(t, clone, "worktree", "list", "--porcelain"); strings.Contains(worktrees, fixture.managedRoot) {
		t.Fatalf("rejected replay registered a clone-B worktree: %s", worktrees)
	}

	control, err = sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	after, err := control.AuthoritySnapshot(ctx, "work-binding", "revision-1")
	if err != nil {
		t.Fatal(err)
	}
	durableAfter, err := control.Repository(ctx, "repository-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Workspace(ctx, "workspace-binding"); err == nil {
		t.Fatal("rejected replay reserved a workspace")
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	if before != after || durableBefore != durableAfter {
		t.Fatalf("rejected replay mutated repo-A durability: authority before=%+v after=%+v binding before=%+v after=%+v", before, after, durableBefore, durableAfter)
	}
	fixture.assertPrimaryUnchanged(t)

	alias := filepath.Join(t.TempDir(), "repository-alias")
	if err := os.Symlink(fixture.repository, alias); err != nil {
		t.Fatal(err)
	}
	aliasReplay := input
	aliasReplay.physicalRepository = alias
	viaAlias := waitWorkspaceProcess(t, fixture.start(ctx, t, aliasReplay))
	if !viaAlias.OK || viaAlias.Capsule == nil || !viaAlias.Capsule.Replayed || viaAlias.Capsule.Token == "" || viaAlias.Capsule.WorkspaceStage != "ready" {
		t.Fatalf("canonical symlink alias replay=%+v", viaAlias)
	}
	exact := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
	if !exact.OK || exact.Capsule == nil || !exact.Capsule.Replayed || exact.Capsule.RunID != viaAlias.Capsule.RunID || exact.Capsule.LeaseID != viaAlias.Capsule.LeaseID || exact.Capsule.Token != viaAlias.Capsule.Token || exact.Capsule.WorkspacePath != viaAlias.Capsule.WorkspacePath {
		t.Fatalf("exact repo-A replay was not idempotent: alias=%+v exact=%+v", viaAlias, exact)
	}
	fixture.assertPrimaryUnchanged(t)
}

func TestStartRunRejectsInventedLaneAndStaleBaseBeforeAuthorityOrWorkspace(t *testing.T) {
	for _, attack := range []struct {
		name   string
		mutate func(*workspaceStart)
		kind   execution.ErrorKind
	}{
		{"invented-lane", func(input *workspaceStart) { input.lane = "lane-invented" }, execution.KindInvalidCommand},
		{"stale-base", func(input *workspaceStart) { input.base = "9999999999999999999999999999999999999999" }, execution.KindTargetChanged},
	} {
		t.Run(attack.name, func(t *testing.T) {
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-auth", lane: "lane-auth", tracker: "TK-AUTH"}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-auth", work: "work-auth", lane: "lane-auth", actor: "actor-auth", process: "process-auth", workspace: "workspace-auth", branch: "runs/auth", trackerWork: "TK-AUTH"}
			attack.mutate(&input)
			response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
			if response.OK || response.Capsule != nil || response.Error == nil || response.Error.Kind != attack.kind {
				t.Fatalf("attack response=%+v", response)
			}
			control, err := sqlite.Open(ctx, fixture.database)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := control.AuthoritySnapshot(ctx, "work-auth", "revision-1")
			_ = control.Close()
			if err != nil || snapshot.ActiveLeases != 0 || snapshot.Runs != 0 || snapshot.Operations != 0 {
				t.Fatalf("preflight published authority: %+v err=%v", snapshot, err)
			}
			if _, err := os.Stat(filepath.Join(fixture.managedRoot, "workspace-auth")); !os.IsNotExist(err) {
				t.Fatalf("preflight published workspace: %v", err)
			}
		})
	}
}

func TestStartRunPathAttacksFailBeforeOpeningStoreOrMutatingPrimary(t *testing.T) {
	for _, attack := range []string{"database-final-symlink", "tracker-final-symlink", "managed-root-inside-primary"} {
		t.Run(attack, func(t *testing.T) {
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-path", lane: "lane-path", tracker: "TK-PATH"}})
			link := filepath.Join(filepath.Dir(fixture.database), attack)
			switch attack {
			case "database-final-symlink":
				if err := os.Symlink(filepath.Join(fixture.repository, ".git", "index"), link); err != nil {
					t.Fatal(err)
				}
				fixture.database = link
			case "tracker-final-symlink":
				if err := os.Symlink(filepath.Join(fixture.repository, ".git", "index"), link); err != nil {
					t.Fatal(err)
				}
				fixture.tracker = link
			case "managed-root-inside-primary":
				fixture.managedRoot = filepath.Join(fixture.repository, "not-created", "workspaces")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-path", work: "work-path", lane: "lane-path", actor: "actor-path", process: "process-path", workspace: "workspace-path", branch: "runs/path", trackerWork: "TK-PATH"}
			response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
			if response.OK || response.Capsule != nil || response.Error == nil || response.Error.Kind != execution.KindPathUnsafe {
				t.Fatalf("path response=%+v", response)
			}
			fixture.assertPrimaryUnchanged(t)
			if attack == "managed-root-inside-primary" {
				if _, err := os.Stat(filepath.Join(fixture.repository, "not-created")); !os.IsNotExist(err) {
					t.Fatalf("rejected path created primary directory: %v", err)
				}
			}
		})
	}
}

func TestSR09LostResponseReprobesAndQuarantinesWorkspaceDrift(t *testing.T) {
	for _, drift := range []string{"head", "removed", "reregistered"} {
		t.Run(drift, func(t *testing.T) {
			fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-drift", lane: "lane-drift", tracker: "TK-DRIFT"}})
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			input := workspaceStart{operation: "operation-drift", work: "work-drift", lane: "lane-drift", actor: "actor-drift", process: "process-drift", workspace: "workspace-drift", branch: "runs/drift", trackerWork: "TK-DRIFT"}
			crashWorkspaceAt(t, ctx, fixture, "SR-09", input)
			path := filepath.Join(fixture.managedRoot, input.workspace)
			switch drift {
			case "head":
				if err := os.WriteFile(filepath.Join(path, "drift.txt"), []byte("drift\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				scenarioGit(t, path, "add", "drift.txt")
				scenarioGit(t, path, "commit", "-m", "drift")
			case "removed", "reregistered":
				scenarioGit(t, fixture.repository, "worktree", "remove", "--force", path)
				if drift == "reregistered" {
					scenarioGit(t, fixture.repository, "worktree", "add", "-b", "runs/intruder", path, fixture.base)
				}
			}
			response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
			if response.OK || response.Capsule != nil || response.Error == nil || response.Error.Kind != execution.KindWorkspaceNotReady {
				t.Fatalf("drift replay returned cached capsule: %+v", response)
			}
			control, err := sqlite.Open(ctx, fixture.database)
			if err != nil {
				t.Fatal(err)
			}
			record, err := control.Workspace(ctx, "workspace-drift")
			_ = control.Close()
			if err != nil || record.Status != "quarantined" {
				t.Fatalf("drift record=%+v err=%v", record, err)
			}
		})
	}
}

func TestSR09LostResponseReprobesAndQuarantinesTargetDrift(t *testing.T) {
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-target-drift", lane: "lane-target-drift", tracker: "TK-TARGET-DRIFT"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	input := workspaceStart{operation: "operation-target-drift", work: "work-target-drift", lane: "lane-target-drift", actor: "actor-target-drift", process: "process-target-drift", workspace: "workspace-target-drift", branch: "runs/target-drift", trackerWork: "TK-TARGET-DRIFT"}
	crashWorkspaceAt(t, ctx, fixture, "SR-09", input)
	tree := scenarioGit(t, fixture.repository, "rev-parse", fixture.base+"^{tree}")
	newTarget := scenarioGit(t, fixture.repository, "commit-tree", tree, "-p", fixture.base, "-m", "move target during lost response")
	if err := fixture.primary.UpdateTarget(ctx, fixture.base, newTarget); err != nil {
		t.Fatal(err)
	}
	response := waitWorkspaceProcess(t, fixture.start(ctx, t, input))
	if response.OK || response.Capsule != nil || response.Error == nil || response.Error.Kind != execution.KindTargetChanged {
		t.Fatalf("target-drift replay returned cached capsule: %+v", response)
	}
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	record, err := control.Workspace(ctx, "workspace-target-drift")
	_ = control.Close()
	if err != nil || record.Status != "quarantined" {
		t.Fatalf("target-drift record=%+v err=%v", record, err)
	}
}

func crashWorkspaceAt(t *testing.T, ctx context.Context, fixture workspaceScenario, boundary string, input workspaceStart) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("crash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := fixture.start(ctx, t, input)
	if err := waitForBoundaryReached(ctx, fixture.barrier, boundary, 1); err != nil {
		t.Fatal(err)
	}
	err := first.command.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 86 || first.stdout.Len() != 0 {
		t.Fatalf("%s crash exit=%v stdout=%q stderr=%q", boundary, err, first.stdout.String(), first.stderr.String())
	}
	if err := os.WriteFile(filepath.Join(fixture.barrier, boundary+".action"), []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type workspaceItem struct {
	work    ids.WorkID
	lane    ids.LaneID
	tracker string
}

type workspaceScenario struct {
	repository, database, tracker, managedRoot, barrier, base string
	primary                                                   gitrepo.Repository
}

func newWorkspaceScenario(t *testing.T, items []workspaceItem) workspaceScenario {
	t.Helper()
	runtimeRoot := t.TempDir()
	repository := filepath.Join(runtimeRoot, "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, repository, "init", "-b", "main")
	scenarioGit(t, repository, "config", "user.name", "Scenario Agent")
	scenarioGit(t, repository, "config", "user.email", "scenario@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scenarioGit(t, repository, "add", "base.txt")
	scenarioGit(t, repository, "commit", "-m", "base")
	base := scenarioGit(t, repository, "rev-parse", "HEAD")
	managedRoot := filepath.Join(runtimeRoot, "workspaces")
	adapter, err := gitrepo.Bind(context.Background(), gitrepo.Runner{}, "repository-1", "project-1", repository, managedRoot, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(runtimeRoot, "control", "control.db")
	if err := os.MkdirAll(filepath.Dir(database), 0o700); err != nil {
		t.Fatal(err)
	}
	control, err := sqlite.Open(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	draft := workgraph.NewDraft("project-1", "mission-1")
	for _, item := range items {
		if err := draft.AddLane(workgraph.Lane{ID: workgraph.LaneID(item.lane)}); err != nil {
			t.Fatal(err)
		}
		if err := draft.AddWork(workgraph.WorkItem{ID: item.work, LaneID: workgraph.LaneID(item.lane), Criteria: []workgraph.Criterion{{ID: "criterion-" + item.work.String(), Text: "done"}}}); err != nil {
			t.Fatal(err)
		}
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: "revision-1", Number: 1, ActorID: "actor-owner", FinalizedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := finalizeScenarioRevision(context.Background(), control, revision, 0); err != nil {
		t.Fatal(err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	trackerPath := filepath.Join(runtimeRoot, "tracker", "tracker.json")
	trackerStore, err := fake.Open(trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if err := trackerStore.Seed(context.Background(), fake.WorkSeed{Key: item.tracker, Title: item.work.String()}); err != nil {
			t.Fatal(err)
		}
	}
	barrier := filepath.Join(runtimeRoot, "barrier")
	if err := os.MkdirAll(barrier, 0o700); err != nil {
		t.Fatal(err)
	}
	return workspaceScenario{repository: repository, database: database, tracker: trackerPath, managedRoot: managedRoot, barrier: barrier, base: base, primary: *adapter}
}

type workspaceStart struct {
	operation, work, lane, actor, process, workspace, branch, base, trackerWork, editFile, editContent, at string
	repositoryID, revision, provider, dependencies                                                         string
	physicalRepository                                                                                     string
	trackerRevision                                                                                        int64
}

func (f workspaceScenario) start(ctx context.Context, t *testing.T, input workspaceStart) *claimProcess {
	t.Helper()
	if err := os.MkdirAll(f.barrier, 0o700); err != nil {
		t.Fatal(err)
	}
	base := input.base
	if base == "" {
		base = f.base
	}
	repositoryID := input.repositoryID
	if repositoryID == "" {
		repositoryID = "repository-1"
	}
	revision := input.revision
	if revision == "" {
		revision = "revision-1"
	}
	provider := input.provider
	if provider == "" {
		provider = "fake"
	}
	trackerRevision := input.trackerRevision
	if trackerRevision == 0 {
		trackerRevision = 1
	}
	physicalRepository := input.physicalRepository
	if physicalRepository == "" {
		physicalRepository = f.repository
	}
	at := input.at
	if at == "" {
		at = "2026-08-24T12:00:00Z"
	}
	args := []string{"start-run", "--db", f.database, "--tracker-store", f.tracker, "--repository", physicalRepository, "--workspace-root", f.managedRoot, "--barrier", f.barrier,
		"--schema-version", "1", "--correlation-id", input.operation, "--at", at, "--operation", input.operation, "--project", "project-1", "--repository-id", repositoryID, "--mission", "mission-1", "--revision", revision, "--work", input.work, "--lane", input.lane,
		"--actor", input.actor, "--process", input.process, "--workspace", input.workspace, "--branch", input.branch, "--base", base, "--provider", provider, "--tracker-work", input.trackerWork, "--tracker-revision", fmt.Sprint(trackerRevision), "--assignee", input.actor, "--ttl", "1m"}
	if input.dependencies != "" {
		args = append(args, "--dependencies", input.dependencies)
	}
	if input.editFile != "" {
		args = append(args, "--edit-file", input.editFile, "--edit-content", input.editContent)
	}
	command := exec.CommandContext(ctx, helperBinary, args...)
	process := &claimProcess{command: command}
	command.Stdout, command.Stderr = &process.stdout, &process.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	return process
}

func waitWorkspaceProcess(t *testing.T, process *claimProcess) helpercmd.Response {
	t.Helper()
	if err := process.command.Wait(); err != nil {
		t.Fatalf("workspace helper: %v stderr=%s stdout=%s", err, process.stderr.String(), process.stdout.String())
	}
	var response helpercmd.Response
	if err := json.Unmarshal(process.stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode %q: %v", process.stdout.String(), err)
	}
	return response
}

func waitAndRelease(t *testing.T, ctx context.Context, barrier, boundary string, count int) {
	t.Helper()
	if err := waitForBoundaryReached(ctx, barrier, boundary, count); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(barrier, boundary+".release"), []byte("release\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f workspaceScenario) assertPrimaryUnchanged(t *testing.T) {
	t.Helper()
	after, err := f.primary.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != f.primary.Binding().Primary {
		t.Fatalf("primary changed: before=%+v after=%+v", f.primary.Binding().Primary, after)
	}
}

func scenarioGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, stderr.String())
	}
	return string(bytes.TrimSpace(stdout.Bytes()))
}

func scenarioGitClone(t *testing.T, source, target string) {
	t.Helper()
	command := exec.Command("git", "clone", "--no-local", source, target)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("git clone: %v: %s", err, stderr.String())
	}
}

func scenarioGitSucceeds(directory string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", directory}, args...)...).Run() == nil
}
