package gitrepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestIntegrationWorkspaceWaitsForCommonWorktreeMetadataLock(t *testing.T) {
	repository, base := newRepository(t)
	candidate := commitOnBranch(t, repository, "lane-metadata-lock", "candidate.txt", "candidate\n")
	runGit(t, repository, "checkout", "--detach", "main")
	managed := filepath.Join(t.TempDir(), "managed")
	holder, err := Bind(context.Background(), Runner{}, "repository-integration", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	waiter, err := Bind(context.Background(), Runner{}, "repository-integration", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- holder.withWorktreeMetadataLock(context.Background(), func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = waiter.EnsureIntegrationWorkspace(ctx, "operation-metadata-lock", base, candidate)
	if !workspace.IsKind(err, workspace.KindGitFailure) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled integration metadata wait error=%v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(managed, "integration-"+operationSuffix("operation-metadata-lock"))
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("integration waiter mutated worktree path: %v", statErr)
	}
}

func TestDetachedIntegrationWorkspaceUsesExpectedOldAndPreservesPrimary(t *testing.T) {
	repository, base := newRepository(t)
	candidate := commitOnBranch(t, repository, "lane-candidate", "candidate.txt", "candidate\n")
	runGit(t, repository, "checkout", "--detach", "main")
	managed := filepath.Join(t.TempDir(), "managed")
	adapter, err := Bind(context.Background(), Runner{}, "repository-integration", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, err := adapter.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := adapter.EnsureIntegrationWorkspace(context.Background(), "operation-integration", base, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.Registered || !facts.Detached || !facts.Clean || facts.Head != base || facts.Path == repository || !within(facts.Path, managed) {
		t.Fatalf("workspace facts=%+v", facts)
	}
	result, conflict, err := adapter.IntegrateCandidate(context.Background(), facts, base, candidate)
	if err != nil || conflict != nil || result == "" || result == base || result == candidate {
		t.Fatalf("result=%s conflict=%+v err=%v", result, conflict, err)
	}
	if valid, err := adapter.VerifyIntegrationResult(context.Background(), result, base, candidate); err != nil || !valid {
		t.Fatalf("verify intended result=%v err=%v", valid, err)
	}
	if valid, err := adapter.VerifyIntegrationResult(context.Background(), candidate, base, candidate); err != nil || valid {
		t.Fatalf("candidate-only mutant valid=%v err=%v", valid, err)
	}
	record := fencedIntegrationRecord(t, adapter, "operation-integration", candidate)
	if err := adapter.PublishIntegrationTarget(context.Background(), record, base, result, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := adapter.TargetHead(context.Background()); err != nil || got != result {
		t.Fatalf("target=%s err=%v want=%s", got, err, result)
	}
	if err := adapter.PublishIntegrationTarget(context.Background(), record, base, candidate, nil); !integration.IsKind(err, integration.KindTargetMoved) {
		t.Fatalf("stale expected-old update error=%v", err)
	}
	after, err := adapter.SnapshotPrimary(context.Background())
	if err != nil || after != before {
		t.Fatalf("primary changed: before=%+v after=%+v err=%v", before, after, err)
	}
	preserved := runGit(t, repository, "rev-parse", "refs/spec-kitty/candidates/"+operationSuffix("operation-integration"))
	if preserved != candidate {
		t.Fatalf("preserved candidate=%s want=%s", preserved, candidate)
	}
	cleanupRecord := integration.Record{OperationID: "operation-integration", Candidate: candidate, WorkspacePath: facts.Path, ResultCommit: result, ObservedTarget: result, Status: integration.StatusDone, CleanupStatus: integration.CleanupEligible}
	if err := adapter.CleanupIntegrationWorkspace(context.Background(), cleanupRecord); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationConflictPreservesReachabilityWorkspaceAndRepairLines(t *testing.T) {
	repository, base := newRepository(t)
	runGit(t, repository, "checkout", "-B", "lane-conflict", base)
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("candidate line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "base.txt")
	runGit(t, repository, "commit", "-m", "candidate conflict")
	candidate := runGit(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "checkout", "main")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("target line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "base.txt")
	runGit(t, repository, "commit", "-m", "target conflict")
	target := runGit(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "checkout", "--detach", "HEAD")
	managed := filepath.Join(t.TempDir(), "managed")
	adapter, err := Bind(context.Background(), Runner{}, "repository-conflict", "project-1", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, err := adapter.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := adapter.EnsureIntegrationWorkspace(context.Background(), "operation-conflict", target, candidate)
	if err != nil {
		t.Fatal(err)
	}
	result, evidence, err := adapter.IntegrateCandidate(context.Background(), facts, target, candidate)
	if result != "" || !integration.IsKind(err, integration.KindConflict) || evidence == nil || evidence.Base != base || evidence.Target != target || evidence.Candidate != candidate || evidence.Workspace != facts.Path || len(evidence.ConflictFiles) != 1 || evidence.ConflictFiles[0] != "base.txt" || evidence.GitMessage == "" {
		t.Fatalf("result=%q evidence=%+v err=%v", result, evidence, err)
	}
	if got, err := adapter.TargetHead(context.Background()); err != nil || got != target {
		t.Fatalf("conflict moved target=%s err=%v", got, err)
	}
	if got := runGit(t, repository, "rev-parse", "refs/spec-kitty/candidates/"+operationSuffix("operation-conflict")); got != candidate {
		t.Fatalf("candidate preservation ref=%s", got)
	}
	cleanupRecord := integration.Record{OperationID: "operation-conflict", Candidate: candidate, WorkspacePath: facts.Path, Status: integration.StatusRepairRequired}
	if err := adapter.CleanupIntegrationWorkspace(context.Background(), cleanupRecord); !integration.IsKind(err, integration.KindRepairRequired) {
		t.Fatalf("dirty conflict cleanup error=%v", err)
	}
	after, err := adapter.SnapshotPrimary(context.Background())
	if err != nil || after != before {
		t.Fatalf("primary changed: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestIntegrationRefusesTargetAttachedToPrimaryCheckout(t *testing.T) {
	repository, base := newRepository(t)
	candidate := commitOnBranch(t, repository, "lane-attached", "candidate.txt", "candidate\n")
	runGit(t, repository, "checkout", "main")
	adapter, err := Bind(context.Background(), Runner{}, "repository-attached", "project-1", repository, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.EnsureIntegrationWorkspace(context.Background(), "operation-attached", base, candidate); !integration.IsKind(err, integration.KindRepairRequired) {
		t.Fatalf("attached target error=%v", err)
	}
	runGit(t, repository, "checkout", "--detach", "main")
	if _, err := adapter.EnsureIntegrationWorkspace(context.Background(), "operation-reattached", base, candidate); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "checkout", "main")
	record := fencedIntegrationRecord(t, adapter, "operation-reattached", candidate)
	if err := adapter.PublishIntegrationTarget(context.Background(), record, base, candidate, nil); !integration.IsKind(err, integration.KindRepairRequired) {
		t.Fatalf("reattached target mutation error=%v", err)
	}
	if got := runGit(t, repository, "rev-parse", "refs/heads/main"); got != base {
		t.Fatalf("reattached primary target moved=%s want=%s", got, base)
	}
}

func TestIntegrationRechecksPrimaryAttachmentAfterWaitingForRepositoryLock(t *testing.T) {
	repository, base := newRepository(t)
	candidate := commitOnBranch(t, repository, "lane-lock-wait", "candidate.txt", "candidate\n")
	runGit(t, repository, "checkout", "--detach", "main")
	adapter, err := Bind(context.Background(), Runner{}, "repository-lock-wait", "project-1", repository, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	record := fencedIntegrationRecord(t, adapter, "operation-lock-wait", candidate)
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- adapter.WithTargetAuthority(context.Background(), func(string) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	published := make(chan error, 1)
	go func() {
		published <- adapter.PublishIntegrationTarget(context.Background(), record, base, candidate, nil)
	}()
	time.Sleep(50 * time.Millisecond)
	runGit(t, repository, "checkout", "main")
	attached, err := adapter.SnapshotPrimary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	if err := <-published; !integration.IsKind(err, integration.KindRepairRequired) {
		t.Fatalf("lock-wait attachment publication error=%v", err)
	}
	if got := runGit(t, repository, "rev-parse", "refs/heads/main"); got != base {
		t.Fatalf("target moved after protected primary attached: got=%s want=%s", got, base)
	}
	after, err := adapter.SnapshotPrimary(context.Background())
	if err != nil || after != attached {
		t.Fatalf("refusal changed attached primary: before=%+v after=%+v err=%v", attached, after, err)
	}
}

func TestIntegrationFenceAndExpectedOldTargetUpdateAtomically(t *testing.T) {
	repository, base := newRepository(t)
	candidate := commitOnBranch(t, repository, "lane-fence", "candidate.txt", "candidate\n")
	runGit(t, repository, "checkout", "--detach", "main")
	adapter, err := Bind(context.Background(), Runner{}, "repository-fence", "project-1", repository, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	stale := fencedIntegrationRecord(t, adapter, "operation-fence-stale", candidate)
	current, err := adapter.IntegrationFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := adapter.InstallIntegrationFence(context.Background(), stale, current); err != nil || replayed.Ref != stale.FenceRef || replayed.Object != stale.FenceObject {
		t.Fatalf("fence-install replay=%+v err=%v", replayed, err)
	}
	replacement := fencedIntegrationRecord(t, adapter, "operation-fence-replacement", candidate)
	if stale.FenceRef != replacement.FenceRef || stale.FenceObject == replacement.FenceObject {
		t.Fatalf("stale fence=%+v replacement=%+v", stale, replacement)
	}
	if err := adapter.PublishIntegrationTarget(context.Background(), stale, base, candidate, nil); !integration.IsKind(err, integration.KindTargetMoved) {
		t.Fatalf("stale fenced publication error=%v", err)
	}
	if got := runGit(t, repository, "rev-parse", "refs/heads/main"); got != base {
		t.Fatalf("stale fence moved target=%s want=%s", got, base)
	}
	if err := adapter.PublishIntegrationTarget(context.Background(), replacement, base, candidate, nil); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, repository, "rev-parse", "refs/heads/main"); got != candidate {
		t.Fatalf("replacement fence target=%s want=%s", got, candidate)
	}
}

func TestIntegrationFenceInstallationIsStrictlyMonotonic(t *testing.T) {
	repository, _ := newRepository(t)
	runGit(t, repository, "checkout", "--detach", "main")
	adapter, err := Bind(context.Background(), Runner{}, "repository-monotonic-fence", "project-1", repository, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	initial, err := adapter.IntegrationFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstIntent := integrationFenceIntent(t, adapter, "operation-fence-1", initial, 1)
	first, err := adapter.InstallIntegrationFence(context.Background(), firstIntent, initial)
	if err != nil {
		t.Fatal(err)
	}
	staleSecondIntent := integrationFenceIntent(t, adapter, "operation-fence-2-stale", first, 2)
	winningSecondIntent := integrationFenceIntent(t, adapter, "operation-fence-2-winning", first, 2)
	second, err := adapter.InstallIntegrationFence(context.Background(), winningSecondIntent, first)
	if err != nil {
		t.Fatal(err)
	}
	thirdIntent := integrationFenceIntent(t, adapter, "operation-fence-3", second, 3)
	third, err := adapter.InstallIntegrationFence(context.Background(), thirdIntent, second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.InstallIntegrationFence(context.Background(), staleSecondIntent, first); !integration.IsKind(err, integration.KindAuthorityBusy) {
		t.Fatalf("delayed generation-2 installer error=%v", err)
	}
	if _, err := adapter.InstallIntegrationFence(context.Background(), firstIntent, initial); !integration.IsKind(err, integration.KindAuthorityBusy) {
		t.Fatalf("generation-1 rollback installer error=%v", err)
	}
	current, err := adapter.IntegrationFence(context.Background())
	if err != nil || current.Object != third.Object || current.Generation != 3 || current.OperationID != thirdIntent.OperationID {
		t.Fatalf("current fence=%+v third=%+v err=%v", current, third, err)
	}
	if len(current.SupersededOperations) != 2 || current.SupersededOperations[0] != winningSecondIntent.OperationID || current.SupersededOperations[1] != firstIntent.OperationID {
		t.Fatalf("fence predecessor operation history=%v", current.SupersededOperations)
	}
}

func TestConcurrentIntegrationFenceInstallersHaveOneCASWinner(t *testing.T) {
	repository, _ := newRepository(t)
	runGit(t, repository, "checkout", "--detach", "main")
	adapter, err := Bind(context.Background(), Runner{}, "repository-fence-race", "project-1", repository, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	current, err := adapter.IntegrationFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	intents := []integration.Record{
		integrationFenceIntent(t, adapter, "operation-fence-race-a", current, 1),
		integrationFenceIntent(t, adapter, "operation-fence-race-b", current, 1),
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, intent := range intents {
		intent := intent
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := adapter.InstallIntegrationFence(context.Background(), intent, current)
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	winners, losers := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case integration.IsKind(err, integration.KindAuthorityBusy):
			losers++
		default:
			t.Fatalf("unexpected installer result=%v", err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d", winners, losers)
	}
	installed, err := adapter.IntegrationFence(context.Background())
	if err != nil || installed.Generation != 1 || (installed.OperationID != intents[0].OperationID && installed.OperationID != intents[1].OperationID) {
		t.Fatalf("installed fence=%+v err=%v", installed, err)
	}
}

func fencedIntegrationRecord(t *testing.T, adapter *Repository, operation, candidate string) integration.Record {
	t.Helper()
	current, err := adapter.IntegrationFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	record := integrationFenceIntent(t, adapter, operation, current, current.Generation+1)
	record.Candidate = candidate
	fence, err := adapter.InstallIntegrationFence(context.Background(), record, current)
	if err != nil {
		t.Fatal(err)
	}
	record.FenceRef, record.FenceObject = fence.Ref, fence.Object
	return record
}

func integrationFenceIntent(t *testing.T, adapter *Repository, operation string, predecessor integration.FenceFacts, generation int64) integration.Record {
	t.Helper()
	preflight, err := adapter.TargetHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return integration.Record{OperationID: ids.OperationID(operation), RepositoryID: adapter.binding.RepositoryID, TargetRef: adapter.binding.TargetRef, PreflightTarget: preflight, MutationGeneration: generation, FencePredecessor: predecessor.Object, FenceRequestDigest: "digest-" + operation, Token: "test-fence-token-" + operation, LeaseDeadline: time.Now().UTC().Add(time.Minute)}
}
