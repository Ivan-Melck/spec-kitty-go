package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestSimultaneousCreatesAcrossRepositoryInstancesSerializeWorktreeMetadata(t *testing.T) {
	repository, base := newRepository(t)
	managed := filepath.Join(t.TempDir(), "workspaces")
	first, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	records := []workspace.Record{workspaceRecord(t, first, "workspace-concurrent-a", "runs/concurrent-a", base), workspaceRecord(t, second, "workspace-concurrent-b", "runs/concurrent-b", base)}
	start := make(chan struct{})
	errs := make(chan error, 2)
	for index, adapter := range []*Repository{first, second} {
		index, adapter := index, adapter
		go func() {
			<-start
			errs <- adapter.Create(context.Background(), records[index])
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	for index, adapter := range []*Repository{first, second} {
		probe, err := adapter.Probe(context.Background(), records[index])
		if err != nil || !probe.Registered || probe.Branch != records[index].OwnedBranch || probe.Head != base {
			t.Fatalf("workspace %d probe=%+v err=%v", index, probe, err)
		}
	}
}

func TestWorktreeMetadataLockWaitHonorsContextCancellation(t *testing.T) {
	repository, base := newRepository(t)
	managed := filepath.Join(t.TempDir(), "workspaces")
	holder, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	waiter, err := Bind(context.Background(), Runner{}, "repository-1", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- holder.withWorktreeMetadataLock(context.Background(), func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = waiter.Create(ctx, workspaceRecord(t, waiter, "workspace-canceled", "runs/canceled", base))
	if !workspace.IsKind(err, workspace.KindGitFailure) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled worktree lock error=%v", err)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(managed, "workspace-canceled")); !os.IsNotExist(statErr) {
		t.Fatalf("canceled waiter mutated workspace path: %v", statErr)
	}
}

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
	exerciseRegistrationCensus(t, adapter, record, base)
}

func exerciseRegistrationCensus(t *testing.T, adapter *Repository, existing workspace.Record, base string) {
	t.Helper()
	countPath := filepath.Join(t.TempDir(), "worktree-list.count")
	wrapperPath := filepath.Join(t.TempDir(), "git-census-wrapper")
	wrapper := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-C" ] && [ "$3" = "worktree" ] && [ "$4" = "list" ] && [ "$5" = "--porcelain" ] && [ "$6" = "-z" ]; then
  printf x >> %q
fi
exec git "$@"
`, countPath)
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	counting := &Repository{runner: Runner{Executable: wrapperPath}, binding: adapter.binding}

	t.Run("one-real-list-has-probe-parity-and-no-cross-attempt-cache", func(t *testing.T) {
		first, err := counting.RegistrationCensus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertWorktreeListCalls(t, countPath, 1)
		registered, err := first.Registered(existing.CanonicalPath)
		if err != nil || !registered {
			t.Fatalf("existing registration=%t err=%v", registered, err)
		}
		probe, err := adapter.Probe(t.Context(), existing)
		if err != nil || registered != probe.Registered {
			t.Fatalf("census/probe parity registered=%t probe=%+v err=%v", registered, probe, err)
		}
		primary, err := first.Registered(adapter.binding.PrimaryPath)
		if err != nil || !primary {
			t.Fatalf("primary registration=%t err=%v", primary, err)
		}

		later := workspaceRecord(t, adapter, "workspace-census-later", "runs/census-later", base)
		registered, err = first.Registered(later.CanonicalPath)
		if err != nil || registered {
			t.Fatalf("future registration before create=%t err=%v", registered, err)
		}
		missingProbe, err := adapter.Probe(t.Context(), later)
		if err != nil || registered != missingProbe.Registered {
			t.Fatalf("missing census/probe parity registered=%t probe=%+v err=%v", registered, missingProbe, err)
		}
		if err := adapter.Create(t.Context(), later); err != nil {
			t.Fatal(err)
		}
		if registered, err := first.Registered(later.CanonicalPath); err != nil || registered {
			t.Fatalf("immutable first census changed after create: registered=%t err=%v", registered, err)
		}
		second, err := counting.RegistrationCensus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertWorktreeListCalls(t, countPath, 2)
		if registered, err := second.Registered(later.CanonicalPath); err != nil || !registered {
			t.Fatalf("fresh census omitted new registration=%t err=%v", registered, err)
		}
		runGit(t, adapter.binding.CommonDir, "worktree", "remove", "--force", later.CanonicalPath)
		if registered, err := second.Registered(later.CanonicalPath); err != nil || !registered {
			t.Fatalf("immutable second census changed after removal: registered=%t err=%v", registered, err)
		}
		third, err := counting.RegistrationCensus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		assertWorktreeListCalls(t, countPath, 3)
		if registered, err := third.Registered(later.CanonicalPath); err != nil || registered {
			t.Fatalf("fresh census retained removed registration=%t err=%v", registered, err)
		}
	})

	record := func(path string) string {
		return "worktree " + path + "\x00HEAD " + base + "\x00branch refs/heads/main\x00\x00"
	}
	t.Run("malformed-duplicate-and-escaping-registries-fail-closed", func(t *testing.T) {
		managedEntry := filepath.Join(adapter.binding.ManagedRoot, "parser-entry")
		if err := os.MkdirAll(managedEntry, 0o700); err != nil {
			t.Fatal(err)
		}
		escape := t.TempDir()
		alias := filepath.Join(t.TempDir(), "primary-alias")
		if err := os.Symlink(adapter.binding.PrimaryPath, alias); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name   string
			output string
			kind   workspace.ErrorKind
		}{
			{name: "missing-terminal-record-delimiter", output: strings.TrimSuffix(record(adapter.binding.PrimaryPath), "\x00"), kind: workspace.KindGitFailure},
			{name: "missing-head", output: "worktree " + adapter.binding.PrimaryPath + "\x00branch refs/heads/main\x00\x00", kind: workspace.KindGitFailure},
			{name: "duplicate-head", output: "worktree " + adapter.binding.PrimaryPath + "\x00HEAD " + base + "\x00HEAD " + base + "\x00branch refs/heads/main\x00\x00", kind: workspace.KindGitFailure},
			{name: "ambiguous-identity", output: "worktree " + adapter.binding.PrimaryPath + "\x00HEAD " + base + "\x00branch refs/heads/main\x00detached\x00\x00", kind: workspace.KindGitFailure},
			{name: "unexpected-attribute", output: "worktree " + adapter.binding.PrimaryPath + "\x00surprise value\x00\x00", kind: workspace.KindGitFailure},
			{name: "records-not-separated", output: "worktree " + adapter.binding.PrimaryPath + "\x00HEAD " + base + "\x00worktree " + managedEntry + "\x00\x00", kind: workspace.KindGitFailure},
			{name: "missing-path", output: record(filepath.Join(adapter.binding.ManagedRoot, "absent")), kind: workspace.KindGitFailure},
			{name: "missing-primary", output: record(managedEntry), kind: workspace.KindGitFailure},
			{name: "duplicate-literal", output: record(adapter.binding.PrimaryPath) + record(adapter.binding.PrimaryPath), kind: workspace.KindGitFailure},
			{name: "duplicate-canonical-alias", output: record(adapter.binding.PrimaryPath) + record(alias), kind: workspace.KindGitFailure},
			{name: "escape", output: record(adapter.binding.PrimaryPath) + record(escape), kind: workspace.KindPathUnsafe},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				_, err := parseRegistrationCensus(adapter.binding, test.output)
				if !workspace.IsKind(err, test.kind) {
					t.Fatalf("error=%v want kind=%s", err, test.kind)
				}
			})
		}
	})

	t.Run("query-escape-and-path-replacement-fail-closed", func(t *testing.T) {
		census, err := adapter.RegistrationCensus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := census.Registered(t.TempDir()); !workspace.IsKind(err, workspace.KindPathUnsafe) {
			t.Fatalf("escaping query error=%v", err)
		}
		replaced := filepath.Join(adapter.binding.ManagedRoot, "replaced-after-census")
		if err := os.Mkdir(replaced, 0o700); err != nil {
			t.Fatal(err)
		}
		output := record(adapter.binding.PrimaryPath) + record(replaced)
		before, err := parseRegistrationCensus(adapter.binding, output)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(replaced); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(adapter.binding.PrimaryPath, replaced); err != nil {
			t.Fatal(err)
		}
		if registered, err := before.Registered(replaced); registered || !workspace.IsKind(err, workspace.KindPathUnsafe) {
			t.Fatalf("replaced path inherited stale registration=%t err=%v", registered, err)
		}
	})
}

func assertWorktreeListCalls(t *testing.T, path string, want int) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != want {
		t.Fatalf("real worktree-list calls=%d want=%d", len(content), want)
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
