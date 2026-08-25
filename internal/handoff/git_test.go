package handoff

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGitVerifierPinsCommitAncestryAndOwnedBranchHead(t *testing.T) {
	repository := t.TempDir()
	git(t, repository, "init", "-b", "main")
	git(t, repository, "config", "user.name", "WP06 Test")
	git(t, repository, "config", "user.email", "wp06@example.test")
	if err := os.WriteFile(filepath.Join(repository, "candidate.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "add", "candidate.txt")
	git(t, repository, "commit", "-m", "base")
	base := git(t, repository, "rev-parse", "HEAD")
	git(t, repository, "switch", "-c", "runs/work-1")
	if err := os.WriteFile(filepath.Join(repository, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "commit", "-am", "candidate")
	candidate := git(t, repository, "rev-parse", "HEAD")

	evidence, err := (GitVerifier{}).Verify(context.Background(), AuthorityScope{RepositoryPath: repository, OwnedBranch: "runs/work-1", PinnedBase: base}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Candidate != candidate || evidence.PinnedBase != base || evidence.ObservedBranchHead != candidate {
		t.Fatalf("evidence=%+v", evidence)
	}

	git(t, repository, "switch", "main")
	if _, err := (GitVerifier{}).Verify(context.Background(), AuthorityScope{RepositoryPath: repository, OwnedBranch: "main", PinnedBase: base}, candidate); !IsKind(err, KindCandidateMismatch) {
		t.Fatalf("candidate not at owned branch head error=%v", err)
	}
}

func TestGitVerifierPreservesMovedBranchFactsAcrossGCAndReplay(t *testing.T) {
	repository := t.TempDir()
	managed := t.TempDir()
	git(t, repository, "init", "-b", "main")
	git(t, repository, "config", "user.name", "WP06 Test")
	git(t, repository, "config", "user.email", "wp06@example.test")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "add", "base.txt")
	git(t, repository, "commit", "-m", "base")
	base := git(t, repository, "rev-parse", "HEAD")
	workspace := filepath.Join(managed, "workspace-1")
	git(t, repository, "worktree", "add", "-b", "runs/work-1", workspace, base)
	if err := os.WriteFile(filepath.Join(workspace, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, workspace, "add", "candidate.txt")
	git(t, workspace, "commit", "-m", "candidate")
	candidate := git(t, workspace, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repository, "moved.txt"), []byte("moved\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "add", "moved.txt")
	git(t, repository, "commit", "-m", "moved branch destination")
	observed := git(t, repository, "rev-parse", "HEAD")
	primaryHead := observed
	primaryStatus := git(t, repository, "status", "--porcelain=v1", "--untracked-files=all")
	git(t, workspace, "reset", "--hard", observed)

	scope := AuthorityScope{
		RepositoryID: "repository-1", RepositoryPath: repository, OwnedBranch: "runs/work-1",
		PinnedBase: base, LaneID: "lane-1", OperationID: "operation-handoff-moved",
		HandoffID: "handoff-moved", WorkRevisionID: "revision-1", WorkID: "work-1",
	}
	verifier := GitVerifier{}
	_, firstErr := verifier.Verify(context.Background(), scope, candidate)
	var first *Error
	if !errors.As(firstErr, &first) || first.Kind != KindCandidateMismatch {
		t.Fatalf("first verification error=%v", firstErr)
	}
	if first.Observed["branch_head"] != observed || first.Observed["divergence_kind"] != BranchMovementDivergence || first.Expected["candidate"] != candidate || first.Expected["branch_head"] != candidate || len(first.RecoveryActions) == 0 {
		t.Fatalf("incomplete movement diagnostic=%+v", first)
	}
	root := first.Observed["evidence_ref_root"]
	if root == "" || first.Observed["evidence_candidate_ref"] != root+"/candidate" || first.Observed["evidence_observed_ref"] != root+"/observed" {
		t.Fatalf("evidence refs=%+v", first.Observed)
	}
	refs := strings.Fields(git(t, repository, "for-each-ref", "--format=%(refname)", root+"/"))
	if len(refs) != 2 {
		t.Fatalf("evidence refs=%v", refs)
	}
	if got := git(t, repository, "rev-parse", root+"/candidate^{commit}"); got != candidate {
		t.Fatalf("candidate evidence=%s want=%s", got, candidate)
	}
	if got := git(t, repository, "rev-parse", root+"/observed^{commit}"); got != observed {
		t.Fatalf("observed evidence=%s want=%s", got, observed)
	}

	_, replayErr := verifier.Verify(context.Background(), scope, candidate)
	var replay *Error
	if !errors.As(replayErr, &replay) || replay.Kind != KindCandidateMismatch || !reflect.DeepEqual(first.Observed, replay.Observed) || !reflect.DeepEqual(first.Expected, replay.Expected) || !reflect.DeepEqual(first.RecoveryActions, replay.RecoveryActions) {
		t.Fatalf("replay diagnostic first=%+v replay=%+v error=%v", first, replay, replayErr)
	}
	if replayRefs := strings.Fields(git(t, repository, "for-each-ref", "--format=%(refname)", root+"/")); !reflect.DeepEqual(refs, replayRefs) {
		t.Fatalf("duplicate or changed evidence refs first=%v replay=%v", refs, replayRefs)
	}

	git(t, repository, "reflog", "expire", "--expire=now", "--all")
	git(t, repository, "gc", "--prune=now")
	for label, commit := range map[string]string{"candidate": candidate, "observed": observed} {
		if got := git(t, repository, "cat-file", "-t", commit); got != "commit" {
			t.Fatalf("%s commit not retained: %s", label, got)
		}
	}
	if got := git(t, repository, "rev-parse", "HEAD"); got != primaryHead {
		t.Fatalf("primary HEAD changed=%s want=%s", got, primaryHead)
	}
	if got := git(t, repository, "status", "--porcelain=v1", "--untracked-files=all"); got != primaryStatus {
		t.Fatalf("primary status changed=%q want=%q", got, primaryStatus)
	}
}

func TestGitVerifierRevalidatesAndAtomicallyRepairsDeletedEvidenceRefs(t *testing.T) {
	fixture := newBranchMovementGitFixture(t)
	verifier := GitVerifier{}
	refusal := fixture.preserve(t, verifier)
	root := refusal.Observed["evidence_ref_root"]

	for _, deleted := range []string{"candidate", "observed"} {
		t.Run(deleted, func(t *testing.T) {
			ref := root + "/" + deleted
			git(t, fixture.repository, "update-ref", "-d", ref)
			if err := verifier.RevalidateRefusal(context.Background(), refusal); err != nil {
				t.Fatalf("repair %s: %v", deleted, err)
			}
			want := fixture.candidate
			if deleted == "observed" {
				want = fixture.observed
			}
			if got := git(t, fixture.repository, "rev-parse", ref+"^{commit}"); got != want {
				t.Fatalf("repaired %s=%s want=%s", deleted, got, want)
			}
		})
	}

	git(t, fixture.repository, "update-ref", "-d", root+"/candidate")
	git(t, fixture.repository, "update-ref", "-d", root+"/observed")
	if err := verifier.RevalidateRefusal(context.Background(), refusal); err != nil {
		t.Fatalf("repair both refs: %v", err)
	}
	if got := git(t, fixture.repository, "rev-parse", root+"/candidate^{commit}"); got != fixture.candidate {
		t.Fatalf("candidate=%s", got)
	}
	if got := git(t, fixture.repository, "rev-parse", root+"/observed^{commit}"); got != fixture.observed {
		t.Fatalf("observed=%s", got)
	}
}

func TestGitVerifierNeverOverwritesMutatedEvidenceRef(t *testing.T) {
	fixture := newBranchMovementGitFixture(t)
	verifier := GitVerifier{}
	refusal := fixture.preserve(t, verifier)
	ref := refusal.Observed["evidence_candidate_ref"]
	git(t, fixture.repository, "update-ref", ref, fixture.base)

	err := verifier.RevalidateRefusal(context.Background(), refusal)
	var conflict *Error
	if !errors.As(err, &conflict) || conflict.Kind != KindCandidateMismatch || conflict.Observed["preservation_kind"] != EvidenceRefMutated || conflict.Observed["evidence_candidate_ref_head"] != fixture.base || conflict.Expected["evidence_candidate_ref_head"] != fixture.candidate || len(conflict.RecoveryActions) < 2 || strings.Contains(strings.ToLower(conflict.Message), "preserved") {
		t.Fatalf("mutation conflict=%+v error=%v", conflict, err)
	}
	if got := git(t, fixture.repository, "rev-parse", ref+"^{commit}"); got != fixture.base {
		t.Fatalf("mutated ref overwritten=%s want=%s", got, fixture.base)
	}
}

func TestGitVerifierReplaySurvivesGCButMissingPrunedObjectRequiresDecision(t *testing.T) {
	t.Run("intact refs", func(t *testing.T) {
		fixture := newBranchMovementGitFixture(t)
		verifier := GitVerifier{}
		refusal := fixture.preserve(t, verifier)
		git(t, fixture.repository, "reflog", "expire", "--expire=now", "--all")
		git(t, fixture.repository, "gc", "--prune=now")
		if err := verifier.RevalidateRefusal(context.Background(), refusal); err != nil {
			t.Fatalf("intact replay: %v", err)
		}
	})

	t.Run("deleted ref and pruned object", func(t *testing.T) {
		fixture := newBranchMovementGitFixture(t)
		verifier := GitVerifier{}
		refusal := fixture.preserve(t, verifier)
		git(t, fixture.repository, "update-ref", "-d", refusal.Observed["evidence_candidate_ref"])
		git(t, fixture.repository, "reflog", "expire", "--expire=now", "--all")
		git(t, fixture.repository, "gc", "--prune=now")
		err := verifier.RevalidateRefusal(context.Background(), refusal)
		var conflict *Error
		if !errors.As(err, &conflict) || conflict.Kind != KindCandidateMismatch || conflict.Observed["preservation_kind"] != EvidenceObjectMissing || conflict.Observed["evidence_candidate_ref_head"] != EvidenceRefMissing || conflict.Expected["evidence_candidate_ref_head"] != fixture.candidate || len(conflict.RecoveryActions) < 2 {
			t.Fatalf("missing object conflict=%+v error=%v", conflict, err)
		}
	})
}

type branchMovementGitFixture struct {
	repository string
	workspace  string
	base       string
	candidate  string
	observed   string
	scope      AuthorityScope
}

func newBranchMovementGitFixture(t *testing.T) branchMovementGitFixture {
	t.Helper()
	repository := t.TempDir()
	managed := t.TempDir()
	git(t, repository, "init", "-b", "main")
	git(t, repository, "config", "user.name", "WP06 Test")
	git(t, repository, "config", "user.email", "wp06@example.test")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "add", "base.txt")
	git(t, repository, "commit", "-m", "base")
	base := git(t, repository, "rev-parse", "HEAD")
	workspace := filepath.Join(managed, "workspace-1")
	git(t, repository, "worktree", "add", "-b", "runs/work-1", workspace, base)
	if err := os.WriteFile(filepath.Join(workspace, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, workspace, "add", "candidate.txt")
	git(t, workspace, "commit", "-m", "candidate")
	candidate := git(t, workspace, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repository, "observed.txt"), []byte("observed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repository, "add", "observed.txt")
	git(t, repository, "commit", "-m", "observed")
	observed := git(t, repository, "rev-parse", "HEAD")
	git(t, workspace, "reset", "--hard", observed)
	return branchMovementGitFixture{
		repository: repository, workspace: workspace, base: base, candidate: candidate, observed: observed,
		scope: AuthorityScope{
			RepositoryID: "repository-1", RepositoryPath: repository, OwnedBranch: "runs/work-1", PinnedBase: base,
			LaneID: "lane-1", OperationID: "operation-handoff-moved", HandoffID: "handoff-moved", WorkRevisionID: "revision-1", WorkID: "work-1",
		},
	}
}

func (f branchMovementGitFixture) preserve(t *testing.T, verifier GitVerifier) Refusal {
	t.Helper()
	_, err := verifier.Verify(context.Background(), f.scope, f.candidate)
	var mismatch *Error
	if !errors.As(err, &mismatch) || mismatch.Cause != nil {
		t.Fatalf("preserve error=%v", err)
	}
	return Refusal{
		OperationID: f.scope.OperationID, Kind: mismatch.Kind, Message: mismatch.Message,
		Observed: mismatch.Observed, Expected: mismatch.Expected, RecoveryActions: mismatch.RecoveryActions,
		RepositoryPath: f.repository,
	}
}

func git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(bytesTrimSpace(output))
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == '\n' || value[start] == '\r' || value[start] == ' ' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == ' ' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
