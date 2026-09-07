package m2

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// TestRealGitTwoItemCrownUsesConcreteAdapterAndWorktrees is the Git half of
// the orthogonal M2 crown. The companion WP14 scenario proves authority and
// restart behavior against the admitted GapDB sidecar; this test proves that
// the same two-item shape uses real commits, refs, worktrees, and the concrete
// Git adapter rather than an in-memory ref-update double.
func TestRealGitTwoItemCrownUsesConcreteAdapterAndWorktrees(t *testing.T) {
	t.Parallel()
	requireAdmittedCrownSidecar(t)

	ctx := t.Context()
	root := t.TempDir()
	primary := filepath.Join(root, "repository")
	managed := filepath.Join(root, "managed")
	if err := os.Mkdir(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	realGit(t, primary, "init", "-b", "main")
	realGit(t, primary, "config", "user.name", "M2 Crown")
	realGit(t, primary, "config", "user.email", "m2@example.invalid")
	writeCrownFile(t, primary, "base.txt", "base\n")
	realGit(t, primary, "add", "base.txt")
	realGit(t, primary, "commit", "-m", "base")
	base := realGit(t, primary, "rev-parse", "HEAD")

	repository, err := gitrepo.Bind(ctx, gitrepo.Runner{}, "repository-m2-crown", "project-m2-crown", primary, managed, "refs/heads/main", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	before := repository.Binding().Primary
	records := []workspace.Record{
		crownWorkspaceRecord(t, repository, "workspace-crown-a", "runs/crown-a", base, "lane-crown-a"),
		crownWorkspaceRecord(t, repository, "workspace-crown-b", "runs/crown-b", base, "lane-crown-b"),
	}

	start := make(chan struct{})
	errs := make(chan error, len(records))
	var group sync.WaitGroup
	for index := range records {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			errs <- repository.Create(ctx, records[index])
		}(index)
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	candidates := make([]string, len(records))
	for index, record := range records {
		probe, err := repository.Probe(ctx, record)
		if err != nil || !probe.Registered || !probe.Clean || probe.Branch != record.OwnedBranch || probe.Head != base || probe.IndexPath == "" {
			t.Fatalf("workspace %d probe=%+v err=%v", index, probe, err)
		}
		file := "item-a.txt"
		if index == 1 {
			file = "item-b.txt"
		}
		writeCrownFile(t, record.CanonicalPath, file, "accepted\n")
		realGit(t, record.CanonicalPath, "add", file)
		realGit(t, record.CanonicalPath, "commit", "-m", "candidate "+file)
		candidates[index] = realGit(t, record.CanonicalPath, "rev-parse", "HEAD")
	}
	if records[0].CanonicalPath == records[1].CanonicalPath || candidates[0] == candidates[1] {
		t.Fatal("parallel work items did not receive distinct mutable workspaces and candidates")
	}
	listed := realGit(t, primary, "worktree", "list", "--porcelain")
	for _, record := range records {
		if !strings.Contains(listed, "worktree "+record.CanonicalPath) {
			t.Fatalf("real Git registry omits %s", record.CanonicalPath)
		}
	}

	realGit(t, primary, "checkout", "--detach", base)
	current := base
	for index, candidate := range candidates {
		facts, err := repository.EnsureIntegrationWorkspace(ctx, "operation-crown-"+string(rune('a'+index)), current, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if !facts.Registered || !facts.Detached || !facts.Clean || facts.Head != current || facts.Path == primary {
			t.Fatalf("integration %d facts=%+v", index, facts)
		}
		result, conflict, err := repository.IntegrateCandidate(ctx, facts, current, candidate)
		if err != nil || conflict != nil {
			t.Fatalf("integration %d result=%s conflict=%+v err=%v", index, result, conflict, err)
		}
		valid, err := repository.VerifyIntegrationResult(ctx, result, current, candidate)
		if err != nil || !valid {
			t.Fatalf("integration %d result validation=%v err=%v", index, valid, err)
		}
		if err := repository.UpdateTarget(ctx, current, result); err != nil {
			t.Fatal(err)
		}
		observed, err := repository.TargetHead(ctx)
		if err != nil || observed != result {
			t.Fatalf("serialized target %d=%s err=%v want=%s", index, observed, err, result)
		}
		current = result
	}

	for _, path := range []string{"item-a.txt", "item-b.txt"} {
		if got := realGit(t, primary, "show", "refs/heads/main:"+path); got != "accepted" {
			t.Fatalf("integrated %s=%q", path, got)
		}
	}
	after, err := repository.SnapshotPrimary(ctx)
	if err != nil || after != before {
		t.Fatalf("protected primary changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestRealGitCrownCannotRegressToSyntheticRefUpdates(t *testing.T) {
	source, err := os.ReadFile("real_git_crown_test.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{"gitrepo.Bind", "repository.Create", "repository.Probe", "repository.EnsureIntegrationWorkspace", "repository.IntegrateCandidate", "repository.VerifyIntegrationResult", "repository.UpdateTarget", "worktree\", \"list"} {
		if !strings.Contains(text, required) {
			t.Fatalf("real-Git crown lost concrete proof seam %q", required)
		}
	}
	for _, forbidden := range []string{"attributable" + "Git", "/man" + "aged/"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("real-Git crown admitted synthetic fixture %q", forbidden)
		}
	}
}

func requireAdmittedCrownSidecar(t *testing.T) {
	t.Helper()
	path := os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE")
	if !filepath.IsAbs(path) {
		t.Fatal("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE must name the admitted absolute sidecar")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if SHA256(data) != QualificationCandidateSidecarSHA256 {
		t.Fatal("real-Git crown sidecar differs from admitted assertion extension")
	}
}

func crownWorkspaceRecord(t *testing.T, repository *gitrepo.Repository, workspaceID, branch, base, lane string) workspace.Record {
	t.Helper()
	path, err := repository.WorkspacePath(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return workspace.Record{Reservation: workspace.Reservation{
		OperationID: ids.OperationID("operation-" + workspaceID), ProjectID: "project-m2-crown", MissionID: "mission-m2-crown",
		WorkRevisionID: "revision-m2-crown", WorkID: ids.WorkID("work-" + workspaceID), RunID: ids.RunID("run-" + workspaceID),
		WorkspaceID: ids.WorkspaceID(workspaceID), RepositoryID: "repository-m2-crown", LaneID: ids.LaneID(lane),
		Authority:   workspace.Authority{LeaseID: ids.LeaseID("lease-" + workspaceID), Generation: 1, Token: "token-" + workspaceID},
		OwnedBranch: branch, PinnedBase: base, Now: time.Now().UTC(),
	}, CanonicalPath: path, Stage: workspace.StageReserved, Status: "preparing"}
}

func realGit(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, directory, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeCrownFile(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
