package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestRepositoryBindingRejectsManagedRootAndStoreEscapes(t *testing.T) {
	t.Parallel()
	repository, base := newRepository(t)
	ctx := context.Background()
	if _, err := Bind(ctx, Runner{}, "repository-unsafe", "project-1", repository, filepath.Join(repository, "workspaces"), "refs/heads/main", time.Now()); !workspace.IsKind(err, workspace.KindPathUnsafe) {
		t.Fatalf("in-repository managed root error = %v", err)
	}
	root := t.TempDir()
	adapter, err := Bind(ctx, Runner{}, "repository-safe", "project-1", repository, filepath.Join(root, "workspaces"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Binding().Primary.Head != base || adapter.Binding().Primary.IndexDigest == "" || adapter.Binding().Primary.TreeDigest == "" {
		t.Fatalf("incomplete primary snapshot: %+v", adapter.Binding().Primary)
	}
	if err := adapter.ValidateExternalPath(filepath.Join(repository, "control.db")); !workspace.IsKind(err, workspace.KindPathUnsafe) {
		t.Fatalf("in-Git control store error = %v", err)
	}
	if err := adapter.ValidateExternalPath(filepath.Join(root, "control.db")); err != nil {
		t.Fatalf("external control store rejected: %v", err)
	}
}

func TestRejectedProspectivePathsNeverMutatePrimaryAndResolveEverySymlink(t *testing.T) {
	repository, _ := newRepository(t)
	ctx := context.Background()
	root := t.TempDir()
	headBefore := runGit(t, repository, "rev-parse", "HEAD")
	indexBefore, err := os.ReadFile(filepath.Join(repository, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	treeBefore, err := digestTree(repository)
	if err != nil {
		t.Fatal(err)
	}
	entriesBefore := walkNames(t, repository)

	ancestorLink := filepath.Join(root, "repo-link")
	if err := os.Symlink(repository, ancestorLink); err != nil {
		t.Fatal(err)
	}
	finalLink := filepath.Join(root, "managed-link")
	if err := os.Symlink(filepath.Join(repository, ".git"), finalLink); err != nil {
		t.Fatal(err)
	}
	for _, managed := range []string{
		filepath.Join(repository, "not-created", "workspaces"),
		filepath.Join(ancestorLink, "not-created", "workspaces"),
		finalLink,
	} {
		if _, err := Bind(ctx, Runner{}, "repository-unsafe", "project-1", repository, managed, "refs/heads/main", time.Now()); !workspace.IsKind(err, workspace.KindPathUnsafe) {
			t.Fatalf("managed path %q error=%v", managed, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "not-created")); !os.IsNotExist(err) {
		t.Fatalf("rejected managed root created primary content: %v", err)
	}

	adapter, err := Bind(ctx, Runner{}, "repository-safe", "project-1", repository, filepath.Join(root, "workspaces"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	storeAncestor := filepath.Join(root, "store-repo-link")
	if err := os.Symlink(repository, storeAncestor); err != nil {
		t.Fatal(err)
	}
	storeFinal := filepath.Join(root, "control.db")
	if err := os.Symlink(filepath.Join(repository, ".git", "index"), storeFinal); err != nil {
		t.Fatal(err)
	}
	for _, storePath := range []string{filepath.Join(storeAncestor, "new", "control.db"), storeFinal} {
		if err := adapter.ValidateExternalPath(storePath); !workspace.IsKind(err, workspace.KindPathUnsafe) {
			t.Fatalf("store path %q error=%v", storePath, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repository, "new")); !os.IsNotExist(err) {
		t.Fatalf("store validation created primary content: %v", err)
	}
	if got := runGit(t, repository, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD changed: %s != %s", got, headBefore)
	}
	indexAfter, _ := os.ReadFile(filepath.Join(repository, ".git", "index"))
	treeAfter, _ := digestTree(repository)
	if string(indexAfter) != string(indexBefore) || treeAfter != treeBefore || strings.Join(walkNames(t, repository), "\n") != strings.Join(entriesBefore, "\n") {
		t.Fatal("rejected path mutated primary index, tree, or file set")
	}
}

func walkNames(t *testing.T, root string) []string {
	t.Helper()
	var result []string
	if err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err == nil {
			result = append(result, relative)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRealWorktreeProbeAndDependencyPreparationAreIsolatedAndAtomic(t *testing.T) {
	t.Parallel()
	repository, base := newRepository(t)
	dependencyA := commitOnBranch(t, repository, "dependency-a", "a.txt", "a\n")
	dependencyB := commitOnBranch(t, repository, "dependency-b", "b.txt", "b\n")
	conflictA := commitOnBranch(t, repository, "conflict-a", "same.txt", "left\n")
	conflictB := commitOnBranch(t, repository, "conflict-b", "same.txt", "right\n")
	runGit(t, repository, "checkout", "main")

	root := t.TempDir()
	adapter, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, filepath.Join(root, "workspaces"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	before := adapter.Binding().Primary
	record := workspaceRecord(t, adapter, "workspace-good", "runs/good", base)
	if err := adapter.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	probe, err := adapter.Probe(context.Background(), record)
	if err != nil || !probe.Registered || probe.Head != base || probe.Branch != record.OwnedBranch || probe.GitDir == "" || probe.IndexPath == "" || !probe.Clean {
		t.Fatalf("initial probe=%+v err=%v", probe, err)
	}
	primaryIndex := runGit(t, repository, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if probe.IndexPath == primaryIndex {
		t.Fatalf("workspace shared primary index %q", primaryIndex)
	}
	dependencies := []workspace.Dependency{{WorkID: "work-b", Candidate: dependencyB}, {WorkID: "work-a", Candidate: dependencyA}}
	head, err := adapter.PrepareDependencies(context.Background(), record, dependencies)
	if err != nil || head == base {
		t.Fatalf("prepare dependencies head=%q err=%v", head, err)
	}
	for _, candidate := range []string{dependencyA, dependencyB} {
		command := exec.Command("git", "-C", record.CanonicalPath, "merge-base", "--is-ancestor", candidate, head)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("dependency %s is not in prepared head %s: %v %s", candidate, head, err, output)
		}
	}
	if replay, err := adapter.PrepareDependencies(context.Background(), record, dependencies); err != nil || replay != head {
		t.Fatalf("dependency replay=%q err=%v want=%q", replay, err, head)
	}

	conflictRecord := workspaceRecord(t, adapter, "workspace-conflict", "runs/conflict", base)
	if err := adapter.Create(context.Background(), conflictRecord); err != nil {
		t.Fatal(err)
	}
	_, err = adapter.PrepareDependencies(context.Background(), conflictRecord, []workspace.Dependency{{WorkID: "work-left", Candidate: conflictA}, {WorkID: "work-right", Candidate: conflictB}})
	if !workspace.IsKind(err, workspace.KindDependencyConflict) {
		t.Fatalf("dependency conflict error = %v", err)
	}
	if got := runGit(t, conflictRecord.CanonicalPath, "rev-parse", "HEAD"); got != base {
		t.Fatalf("partial dependency head was published: got=%s base=%s", got, base)
	}
	if err := os.WriteFile(filepath.Join(conflictRecord.CanonicalPath, "unexpected.txt"), []byte("movement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, conflictRecord.CanonicalPath, "add", "unexpected.txt")
	runGit(t, conflictRecord.CanonicalPath, "commit", "-m", "unexpected movement")
	if _, err := adapter.PrepareDependencies(context.Background(), conflictRecord, nil); !workspace.IsKind(err, workspace.KindTargetMoved) {
		t.Fatalf("unexpected base movement error = %v", err)
	}
	after, err := adapter.SnapshotPrimary(context.Background())
	if err != nil || after != before {
		t.Fatalf("primary checkout changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestCooperatingTargetUpdateCannotCrossPublicationAuthorityLock(t *testing.T) {
	repository, base := newRepository(t)
	target := commitOnBranch(t, repository, "future-target", "future.txt", "future\n")
	runGit(t, repository, "checkout", "main")
	adapter, err := Bind(context.Background(), Runner{}, "repository-lock", "project-1", repository, filepath.Join(t.TempDir(), "workspaces"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	published := make(chan error, 1)
	go func() {
		published <- adapter.WithTargetAuthority(context.Background(), func(observed string) error {
			if observed != base {
				return &workspace.Error{Kind: workspace.KindTargetMoved, Message: "unexpected locked target"}
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	updated := make(chan error, 1)
	go func() { updated <- adapter.UpdateTarget(context.Background(), base, target) }()
	select {
	case err := <-updated:
		t.Fatalf("cooperating target update crossed publication lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-published; err != nil {
		t.Fatal(err)
	}
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if got, err := adapter.TargetHead(context.Background()); err != nil || got != target {
		t.Fatalf("serialized target update=%s err=%v want=%s", got, err, target)
	}
}

func workspaceRecord(t *testing.T, adapter *Repository, workspaceID, branch, base string) workspace.Record {
	t.Helper()
	path, err := adapter.WorkspacePath(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.Record{Reservation: workspace.Reservation{OperationID: ids.OperationID("operation-" + workspaceID), RunID: ids.RunID("run-" + workspaceID), WorkspaceID: ids.WorkspaceID(workspaceID), RepositoryID: "repository-1", LaneID: "lane-1", Authority: workspace.Authority{LeaseID: ids.LeaseID("lease-" + workspaceID), Generation: 1, Token: "token"}, OwnedBranch: branch, PinnedBase: base, Now: time.Now()}, CanonicalPath: path, Stage: workspace.StageReserved, Status: "preparing"}
}

func newRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "Test Agent")
	runGit(t, repository, "config", "user.email", "test@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "base.txt")
	runGit(t, repository, "commit", "-m", "base")
	return repository, runGit(t, repository, "rev-parse", "HEAD")
}

func commitOnBranch(t *testing.T, repository, branch, file, content string) string {
	t.Helper()
	runGit(t, repository, "checkout", "-B", branch, "main")
	if err := os.WriteFile(filepath.Join(repository, file), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", file)
	runGit(t, repository, "commit", "-m", branch)
	return runGit(t, repository, "rev-parse", "HEAD")
}

func runGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
