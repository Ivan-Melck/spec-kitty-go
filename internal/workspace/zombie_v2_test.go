package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestWritableZombieWorkspaceNeverRegainsAuthorityCampaignV2(t *testing.T) {
	root := t.TempDir()
	repositoryPath := filepath.Join(root, "repository")
	managedRoot := filepath.Join(root, "workspaces")
	if err := os.MkdirAll(managedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitV2(t, "", "init", repositoryPath)
	runGitV2(t, repositoryPath, "config", "user.name", "Spec Kitty Test")
	runGitV2(t, repositoryPath, "config", "user.email", "spec-kitty@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "seed.txt"), []byte("seed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitV2(t, repositoryPath, "add", "seed.txt")
	runGitV2(t, repositoryPath, "commit", "-m", "seed")
	for trial := 0; trial < 100; trial++ {
		w1Path := filepath.Join(managedRoot, fmt.Sprintf("w1-%03d", trial))
		w2Path := filepath.Join(managedRoot, fmt.Sprintf("w2-%03d", trial))
		runGitV2(t, repositoryPath, "worktree", "add", "-b", fmt.Sprintf("zombie-%03d", trial), w1Path, "HEAD")
		runGitV2(t, repositoryPath, "worktree", "add", "-b", fmt.Sprintf("replacement-%03d", trial), w2Path, "HEAD")
		w1 := workspaceBindingFixtureV2(t, "17", "/srv/spec-kitty/workspaces/w17")
		w1.ManagedRoot = managedRoot
		w1.CanonicalPath = w1Path
		w1.OwnedBranch = fmt.Sprintf("zombie-%03d", trial)
		w1.PathClaimDigest, _ = WorkspacePathClaimDigestV2(w1.RepositoryID, managedRoot, w1Path)
		w2 := workspaceBindingFixtureV2(t, "18", "/srv/spec-kitty/workspaces/w18")
		w2.ManagedRoot = managedRoot
		w2.CanonicalPath = w2Path
		w2.OwnedBranch = fmt.Sprintf("replacement-%03d", trial)
		w2.PathClaimDigest, _ = WorkspacePathClaimDigestV2(w2.RepositoryID, managedRoot, w2Path)
		old := StateV2{SchemaVersion: 1, LogicalVersion: 8, Binding: w1, Stage: StageQuarantinedV2, QuarantineReason: QuarantineLeaseExpired, OccurredAt: "2026-09-04T12:00:00Z"}
		current := StateV2{SchemaVersion: 1, LogicalVersion: 7, Binding: w2, Stage: StageReadyV2, Observation: readyObservationFixtureV2(w2), OccurredAt: "2026-09-04T12:00:01Z"}
		var wg sync.WaitGroup
		wg.Add(2)
		var staleCommit, currentCommit string
		var staleErr, currentErr error
		go func() {
			defer wg.Done()
			staleCommit, staleErr = commitWorkspaceChangeV2(w1Path, "zombie.txt", fmt.Sprintf("stale-%d\n", trial), "stale zombie commit")
		}()
		go func() {
			defer wg.Done()
			currentCommit, currentErr = commitWorkspaceChangeV2(w2Path, "current.txt", fmt.Sprintf("current-%d\n", trial), "current replacement commit")
		}()
		wg.Wait()
		if staleErr != nil || currentErr != nil || staleCommit == "" || currentCommit == "" || staleCommit == currentCommit {
			t.Fatalf("trial %d git commits stale=%q current=%q errors=%v/%v", trial, staleCommit, currentCommit, staleErr, currentErr)
		}
		oldCapsule := capsuleForBindingV2(w1)
		newCapsule := capsuleForBindingV2(w2)
		if err := ValidateCurrentV2(old, oldCapsule); !IsKind(err, KindStaleAuthority) {
			t.Fatalf("trial %d zombie passed: %v", trial, err)
		}
		if err := ValidateCurrentV2(current, newCapsule); err != nil {
			t.Fatalf("trial %d replacement blocked: %v", trial, err)
		}
		if _, err := os.Stat(filepath.Join(w1Path, "zombie.txt")); err != nil {
			t.Fatalf("trial %d did not exercise real stale write: %v", trial, err)
		}
		if w1.WorkspaceID == w2.WorkspaceID || w1.CanonicalPath == w2.CanonicalPath || w1.PathClaimDigest == w2.PathClaimDigest {
			t.Fatalf("trial %d replacement reused workspace identity", trial)
		}
	}
}

func runGitV2(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	if directory != "" {
		command.Dir = directory
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(output)
}

func commitWorkspaceChangeV2(directory, name, content, message string) (string, error) {
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		return "", err
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-m", message}} {
		command := exec.Command("git", args...)
		command.Dir = directory
		if output, err := command.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %v: %w: %s", args, err, output)
		}
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = directory
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func capsuleForBindingV2(b BindingV2) AuthorityCapsuleV2 {
	return AuthorityCapsuleV2{ProjectID: b.ProjectID, MissionID: b.MissionID, WorkRevisionID: b.WorkRevisionID, WorkID: b.WorkID, RunID: b.RunID, LeaseID: b.LeaseID, Generation: b.Generation, Fence: b.Fence, RuntimeEpochID: b.RuntimeEpochID, WorkspaceID: b.WorkspaceID, PathClaimDigest: b.PathClaimDigest, RepositoryID: b.RepositoryID, CanonicalPath: b.CanonicalPath}
}
