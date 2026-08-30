package provingground

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type testEligibility struct{ disposition workgraph.Disposition }

func (e testEligibility) WorkEligibilityFact(_ context.Context, revision ids.WorkRevisionID, work ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) (workgraph.EligibilityFact, error) {
	disposition := e.disposition
	if disposition == "" {
		disposition = workgraph.Continue
	}
	return workgraph.NewEligibilityFact("reconciliation-m1", revision, revision, work, status, disposition, operation)
}

func completeTestFact(work ids.WorkID, status workgraph.WorkStatus, operation workgraph.EligibilityOperation) workgraph.EligibilityFact {
	return completeTestFactWithDisposition(work, status, workgraph.Continue, operation)
}

func completeTestFactWithDisposition(work ids.WorkID, status workgraph.WorkStatus, disposition workgraph.Disposition, operation workgraph.EligibilityOperation) workgraph.EligibilityFact {
	fact, err := workgraph.NewEligibilityFact("reconciliation-m1", "revision-one", "revision-one", work, status, disposition, operation)
	if err != nil {
		panic(err)
	}
	return fact
}

type reviewFixture struct {
	journal                       *wp08Store
	runtime                       *Runtime
	capsule                       controljournal.AuthorityCapsuleV1
	repository                    *gitrepo.Repository
	coordinator                   *ReviewCoordinator
	root, base, candidate, branch string
}

func newReviewFixture(t *testing.T, process controljournal.ProcessID) *reviewFixture {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-b", "main")
	gitRun(t, root, "config", "user.email", "test@example.invalid")
	gitRun(t, root, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "base.txt")
	gitRun(t, root, "commit", "-m", "base")
	base := gitRun(t, root, "rev-parse", "HEAD")
	branch := "lane-b"
	if process == "process-c" {
		branch = "lane-c"
	}
	gitRun(t, root, "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(root, "candidate.txt"), []byte(string(process)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "candidate.txt")
	gitRun(t, root, "commit", "-m", "candidate")
	candidate := gitRun(t, root, "rev-parse", "HEAD")
	gitRun(t, root, "checkout", "--detach", base)
	repository, err := gitrepo.Bind(t.Context(), gitrepo.Runner{}, "repository-m1", "project-m1", root, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	journal := newWP08Store()
	runtime, capsule := wp08Runtime(t, journal, root, branch, base, process)
	workID := ids.WorkID("work-b")
	if process == "process-c" {
		workID = "work-a"
	}
	coordinator, err := newReviewCoordinator(t.Context(), runtime, journal, journal, repository, testEligibility{}, map[ids.WorkID][]string{workID: {"criterion-build", "criterion-test"}})
	if err != nil {
		t.Fatal(err)
	}
	return &reviewFixture{journal: journal, runtime: runtime, capsule: capsule, repository: repository, coordinator: coordinator, root: root, base: base, candidate: candidate, branch: branch}
}
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func (h *reviewFixture) handoffRequest() handoff.Request {
	workID := ids.WorkID("work-b")
	if h.capsule.ProcessID == "process-c" {
		workID = "work-a"
	}
	return handoff.Request{SchemaVersion: 1, CorrelationID: "correlation-handoff", OperationID: "operation-handoff", HandoffID: "handoff-one", ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: workID, RunID: "run-m1", WorkspaceID: ids.WorkspaceID(h.capsule.WorkspaceID), LeaseID: ids.LeaseID(h.capsule.LeaseID), LeaseGeneration: int64(h.capsule.LeaseGeneration), Token: "17", ActorID: ids.ActorID(h.capsule.ActorID), ProcessID: ids.ProcessID(h.capsule.ProcessID), Candidate: h.candidate, Contributors: []ids.ActorID{ids.ActorID(h.capsule.ActorID)}, EvidenceDigest: strings.Repeat("e", 64), SubmittedAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC), ExpectedEligibility: completeTestFact(workID, workgraph.StatusActive, workgraph.EligibilityHandoff)}
}
func (h *reviewFixture) reviewRequest(actor ids.ActorID, process ids.ProcessID) basereview.Request {
	workID := h.handoffRequest().WorkID
	return basereview.Request{SchemaVersion: 1, CorrelationID: "correlation-review", OperationID: "operation-review", ReviewID: "review-one", HandoffID: "handoff-one", WorkRevisionID: "revision-one", WorkID: workID, LeaseID: ids.LeaseID(h.capsule.LeaseID), LeaseGeneration: int64(h.capsule.LeaseGeneration), Token: "review-token", ActorID: actor, ProcessID: process, Candidate: h.candidate, Verdict: basereview.Approved, Criteria: []basereview.CriterionResult{{CriterionID: "criterion-build", Status: basereview.Passed, Evidence: "build output digest"}, {CriterionID: "criterion-test", Status: basereview.Passed, Evidence: "test output digest"}}, EvidenceDigest: strings.Repeat("f", 64), SubmittedAt: time.Date(2026, 8, 30, 12, 1, 0, 0, time.UTC), ExpectedEligibility: completeTestFact(workID, workgraph.StatusHandedOff, workgraph.EligibilityReview)}
}

func TestReviewActorIDIndependenceAndImmutableCandidateEvidence(t *testing.T) {
	fixture := newReviewFixture(t, "process-c")
	candidate, err := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, fixture.handoffRequest())
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Candidate != fixture.candidate || len(candidate.Contributors) != 1 || candidate.Contributors[0] != "actor-a" {
		t.Fatalf("candidate=%#v", candidate)
	}
	digestBefore, _ := fixture.coordinator.CandidateDigest("handoff-one")
	sameActor := fixture.reviewRequest("actor-a", "process-d")
	if _, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, sameActor); !basereview.IsKind(err, basereview.KindReviewerNotIndependent) {
		t.Fatalf("same ActorID review survived: %v", err)
	}
	independent := fixture.reviewRequest("reviewer-one", "review-process")
	record, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, independent)
	if err != nil {
		t.Fatal(err)
	}
	if record.Verdict != basereview.Approved || record.ReviewerActorID != "reviewer-one" {
		t.Fatalf("review=%#v", record)
	}
	gitRun(t, fixture.root, "branch", "-f", fixture.branch, fixture.base)
	persisted, _ := fixture.coordinator.Candidate("handoff-one")
	digestAfter, _ := fixture.coordinator.CandidateDigest("handoff-one")
	if persisted.Candidate != fixture.candidate || digestAfter != digestBefore {
		t.Fatal("external branch movement mutated immutable candidate evidence")
	}
}

func TestReviewRejectsW1StaleChangedCandidateMissingEvidenceAndChangedReplay(t *testing.T) {
	fixture := newReviewFixture(t, "process-b")
	w1 := fixture.capsule
	w1.ProcessID = "process-a"
	w1.ActorID = "actor-a"
	request := fixture.handoffRequest()
	request.ProcessID = "process-a"
	request.ActorID = "actor-a"
	request.Contributors = []ids.ActorID{"actor-a"}
	if _, err := fixture.coordinator.SubmitCandidate(t.Context(), w1, request); err == nil {
		t.Fatal("W1 candidate survived")
	}
	if _, err := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, fixture.handoffRequest()); err != nil {
		t.Fatal(err)
	}
	changed := fixture.reviewRequest("reviewer-one", "review-process")
	changed.Candidate = fixture.base
	if _, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, changed); err == nil {
		t.Fatal("changed candidate survived")
	}
	missing := fixture.reviewRequest("reviewer-one", "review-process")
	missing.EvidenceDigest = ""
	if _, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, missing); err == nil {
		t.Fatal("missing evidence survived")
	}
	valid := fixture.reviewRequest("reviewer-one", "review-process")
	if _, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, valid); err != nil {
		t.Fatal(err)
	}
	mutant := valid
	mutant.EvidenceDigest = strings.Repeat("a", 64)
	if _, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, mutant); !basereview.IsKind(err, basereview.KindIdempotencyConflict) {
		t.Fatalf("changed-input review replay survived: %v", err)
	}
}

func TestReviewRefusesStaleCandidateCapsuleBeforeVerdictPublication(t *testing.T) {
	fixture := newReviewFixture(t, "process-b")
	if _, err := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, fixture.handoffRequest()); err != nil {
		t.Fatal(err)
	}
	fixture.runtime.core.works["work-b"].current.AuthorityHeadRecordRevision++
	if _, err := fixture.coordinator.SubmitReview(t.Context(), fixture.capsule, fixture.reviewRequest("reviewer-one", "review-process")); err == nil {
		t.Fatal("stale capsule review survived")
	}
	if _, ok := fixture.coordinator.Review("review-one"); ok {
		t.Fatal("stale review was durably published")
	}
}

func TestHandoffBranchMovementRefusalIsDurableAndExactReplayRevalidatesGitFacts(t *testing.T) {
	fixture := newReviewFixture(t, "process-b")
	request := fixture.handoffRequest()
	gitRun(t, fixture.root, "branch", "-f", fixture.branch, fixture.base)

	if _, err := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, request); !handoff.IsKind(err, handoff.KindCandidateMismatch) {
		t.Fatalf("branch movement was not refused: %v", err)
	}
	entry, ok := fixture.coordinator.store.refusals[request.OperationID]
	if !ok || entry.Refusal.Observed["evidence_ref_root"] == "" {
		t.Fatalf("durable refusal=%#v", entry)
	}

	restored, err := newReviewCoordinator(t.Context(), fixture.runtime, fixture.journal, fixture.journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-b": {"criterion-build", "criterion-test"}})
	if err != nil {
		t.Fatal(err)
	}
	ref := entry.Refusal.Observed["evidence_candidate_ref"]
	gitRun(t, fixture.root, "update-ref", "-d", ref)
	if _, err := restored.SubmitCandidate(t.Context(), fixture.capsule, request); !handoff.IsKind(err, handoff.KindCandidateMismatch) {
		t.Fatalf("exact refusal replay changed outcome: %v", err)
	}
	if got := gitRun(t, fixture.root, "rev-parse", ref+"^{commit}"); got != fixture.candidate {
		t.Fatalf("exact refusal replay did not repair evidence ref: %s", got)
	}

	changed := request
	changed.EvidenceDigest = strings.Repeat("a", 64)
	if _, err := restored.SubmitCandidate(t.Context(), fixture.capsule, changed); !handoff.IsKind(err, handoff.KindIdempotencyConflict) {
		t.Fatalf("changed-input refusal replay survived: %v", err)
	}
}
