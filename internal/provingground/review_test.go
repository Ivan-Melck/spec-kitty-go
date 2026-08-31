package provingground

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/application"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
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
	registerWP08InvariantCleanup(t, wp08InvariantObservation{runtime: runtime, review: coordinator, repository: repository, requireWP08: true})
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

type reviewBindingGit struct {
	mu      sync.Mutex
	root    string
	created bool
}

type candidateBlockingJournal struct {
	*wp08Store
	gateMu  sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func newCandidateBlockingJournal() *candidateBlockingJournal {
	return &candidateBlockingJournal{wp08Store: newWP08Store(), entered: make(chan struct{}), release: make(chan struct{})}
}

func (j *candidateBlockingJournal) arm() {
	j.gateMu.Lock()
	j.armed = true
	j.gateMu.Unlock()
}

func (j *candidateBlockingJournal) Apply(ctx context.Context, batch application.ValidatedBatchV1) (application.ResultV1, error) {
	j.gateMu.Lock()
	armed := j.armed
	if armed {
		j.armed = false
	}
	j.gateMu.Unlock()
	if armed {
		close(j.entered)
		select {
		case <-j.release:
		case <-ctx.Done():
			return application.ResultV1{}, ctx.Err()
		}
	}
	return j.wp08Store.Apply(ctx, batch)
}

func (g *reviewBindingGit) WorkspacePath(string) (string, error) { return g.root, nil }
func (g *reviewBindingGit) Create(context.Context, workspace.Record) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.created = true
	return nil
}
func (g *reviewBindingGit) Probe(_ context.Context, record workspace.Record) (workspace.Probe, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.created {
		return workspace.Probe{}, nil
	}
	return workspace.Probe{Registered: true, Clean: true, Head: record.PinnedBase, Branch: record.OwnedBranch, GitDir: filepath.Join(record.CanonicalPath, ".git"), IndexPath: filepath.Join(record.CanonicalPath, ".git", "index")}, nil
}
func (*reviewBindingGit) Inspect(_ context.Context, record workspace.Record) (gitrepo.Inspection, error) {
	return gitrepo.Inspection{Known: true, Registered: true, Clean: true, CanonicalPath: record.CanonicalPath, RecordedBranch: record.OwnedBranch, ObservedBranch: record.OwnedBranch, RecordedHead: record.PinnedBase, ObservedHead: record.PinnedBase}, nil
}

func TestCandidateAcceptsHeartbeatRefreshedAuthorityWithoutRebindingWorkspace(t *testing.T) {
	fixture := newReviewFixture(t, "process-c")
	now := time.Date(2026, 8, 30, 11, 58, 0, 0, time.UTC)
	journal := newWP08Store()
	core, err := newService(testConfig(t), journal, &reviewBindingGit{root: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := core.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-c", "heartbeat-handoff", now))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.EnsureWorkspace(t.Context(), WorkspaceRequest{OperationID: "bind-heartbeat-handoff", Authority: claimed, WorkspaceID: "workspace-heartbeat-handoff", RepositoryID: "repository-m1", LaneID: "lane-c", OwnedBranch: fixture.branch, PinnedBase: fixture.base, Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := authorityCapsule(claimed, binding)
	if err != nil {
		t.Fatal(err)
	}
	authority := &executionAuthorityRepository{core: core}
	runtime := &Runtime{core: core, authority: authority, execution: execution.NewService(authority, fixedExecutionIdentities{}, fixedExecutionClock{})}
	coordinator, err := newReviewCoordinator(t.Context(), runtime, journal, journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-a": {"criterion-build", "criterion-test"}})
	if err != nil {
		t.Fatal(err)
	}
	before, ok := runtime.Binding(capsule.WorkspaceID)
	if !ok {
		t.Fatal("fixture workspace binding is absent")
	}

	heartbeatAt := now.Add(2 * time.Second)
	refreshed, err := runtime.Heartbeat(t.Context(), "heartbeat-before-handoff", capsule, heartbeatAt, runtime.core.config.LeaseTTL)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AuthorityHeadRecordRevision == capsule.AuthorityHeadRecordRevision {
		t.Fatal("heartbeat did not mint a refreshed exact authority head")
	}
	request := fixture.handoffRequest()
	request.OperationID, request.HandoffID = "operation-heartbeat-handoff", "handoff-heartbeat"
	request.WorkID, request.WorkspaceID, request.LeaseID = "work-a", "workspace-heartbeat-handoff", ids.LeaseID(capsule.LeaseID)
	request.LeaseGeneration, request.Token = int64(capsule.LeaseGeneration), "1"
	request.ActorID, request.ProcessID, request.SubmittedAt = "actor-a", "process-c", now.Add(3*time.Second)
	request.ExpectedEligibility = completeTestFact("work-a", workgraph.StatusActive, workgraph.EligibilityHandoff)
	if _, err := coordinator.SubmitCandidate(t.Context(), capsule, request); !IsOutcome(err, OutcomeStaleLease) {
		t.Fatalf("pre-heartbeat capsule reached handoff: %v", err)
	}
	if _, ok := coordinator.Candidate(request.HandoffID); ok {
		t.Fatal("pre-heartbeat capsule published candidate evidence")
	}
	if _, err := coordinator.SubmitCandidate(t.Context(), refreshed, request); err != nil {
		t.Fatalf("refreshed exact capsule was refused: %v", err)
	}
	after, ok := runtime.Binding(capsule.WorkspaceID)
	if !ok || !reflect.DeepEqual(after, before) {
		t.Fatalf("heartbeat transferred or rewrote immutable workspace binding: before=%+v after=%+v", before, after)
	}
	assertAuthorityInvariants(t, core)
}

func TestCandidatePublicationAndHeartbeatLinearizeOnOneOwner(t *testing.T) {
	fixture := newReviewFixture(t, "process-c")
	now := time.Date(2026, 8, 30, 11, 58, 0, 0, time.UTC)
	journal := newCandidateBlockingJournal()
	core, err := newService(testConfig(t), journal, &reviewBindingGit{root: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := core.Claim(t.Context(), claimRequest("work-a", "actor-a", "process-c", "candidate-linearize", now))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := core.EnsureWorkspace(t.Context(), WorkspaceRequest{OperationID: "bind-candidate-linearize", Authority: claimed, WorkspaceID: "workspace-candidate-linearize", RepositoryID: "repository-m1", LaneID: "lane-c", OwnedBranch: fixture.branch, PinnedBase: fixture.base, Now: now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := authorityCapsule(claimed, binding)
	if err != nil {
		t.Fatal(err)
	}
	authority := &executionAuthorityRepository{core: core}
	runtime := &Runtime{core: core, authority: authority, execution: execution.NewService(authority, fixedExecutionIdentities{}, fixedExecutionClock{})}
	coordinator, err := newReviewCoordinator(t.Context(), runtime, journal, journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-a": {"criterion-build", "criterion-test"}})
	if err != nil {
		t.Fatal(err)
	}
	request := fixture.handoffRequest()
	request.OperationID, request.HandoffID = "operation-candidate-linearize", "handoff-candidate-linearize"
	request.WorkID, request.WorkspaceID, request.LeaseID = "work-a", "workspace-candidate-linearize", ids.LeaseID(capsule.LeaseID)
	request.LeaseGeneration, request.Token = int64(capsule.LeaseGeneration), "1"
	request.ActorID, request.ProcessID, request.SubmittedAt = "actor-a", "process-c", now.Add(3*time.Second)
	request.ExpectedEligibility = completeTestFact("work-a", workgraph.StatusActive, workgraph.EligibilityHandoff)

	journal.arm()
	candidateDone := make(chan error, 1)
	go func() {
		_, submitErr := coordinator.SubmitCandidate(t.Context(), capsule, request)
		candidateDone <- submitErr
	}()
	<-journal.entered
	state := core.works["work-a"]
	if state.mu.TryLock() {
		state.mu.Unlock()
		close(journal.release)
		t.Fatal("candidate publication did not hold the authority linearization lock")
	}
	heartbeatStarted := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go func() {
		close(heartbeatStarted)
		_, heartbeatErr := runtime.Heartbeat(t.Context(), "heartbeat-after-candidate-validation", capsule, now.Add(4*time.Second), runtime.core.config.LeaseTTL)
		heartbeatDone <- heartbeatErr
	}()
	<-heartbeatStarted
	select {
	case heartbeatErr := <-heartbeatDone:
		close(journal.release)
		t.Fatalf("heartbeat crossed candidate's final authority boundary: %v", heartbeatErr)
	default:
	}
	close(journal.release)
	if err := <-candidateDone; err != nil {
		t.Fatalf("candidate that linearized first failed: %v", err)
	}
	if err := <-heartbeatDone; err != nil {
		t.Fatalf("heartbeat did not linearize after candidate publication: %v", err)
	}
	if _, ok := coordinator.Candidate(request.HandoffID); !ok {
		t.Fatal("linearized candidate publication is absent")
	}
	assertAuthorityInvariants(t, core)
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

func TestCandidateAuthorityRejectsCrossWorkSubstitution(t *testing.T) {
	fixture := newReviewFixture(t, "process-b")
	request := fixture.handoffRequest()
	request.WorkID = "work-a"
	request.ExpectedEligibility = completeTestFact("work-a", workgraph.StatusActive, workgraph.EligibilityHandoff)
	if _, err := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, request); err == nil {
		t.Fatal("cross-work request substituted a workspace authority")
	}
	if _, ok := fixture.coordinator.Candidate(request.HandoffID); ok {
		t.Fatal("cross-work request published candidate evidence")
	}
}

func TestCandidateHR01RaceAdmitsOneExactImmutableEvidenceBinding(t *testing.T) {
	fixture := newReviewFixture(t, "process-b")
	second, err := newReviewCoordinator(t.Context(), fixture.runtime, fixture.journal, fixture.journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-b": {"criterion-build", "criterion-test"}})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fixture.coordinator.handoff.WithFaults(&callbackHook{cut: "HR-01", fn: func() {
		once.Do(func() { close(entered) })
		<-release
	}})
	first := fixture.handoffRequest()
	secondRequest := first
	secondRequest.OperationID = "operation-handoff-competing"
	secondRequest.EvidenceDigest = strings.Repeat("a", 64)
	firstErr := make(chan error, 1)
	go func() {
		_, submitErr := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, first)
		firstErr <- submitErr
	}()
	<-entered
	winner, secondErr := second.SubmitCandidate(t.Context(), fixture.capsule, secondRequest)
	close(release)
	loserErr := <-firstErr
	if secondErr != nil || winner.EvidenceDigest != secondRequest.EvidenceDigest {
		t.Fatalf("competing exact winner=%#v err=%v", winner, secondErr)
	}
	if !handoff.IsKind(loserErr, handoff.KindIdempotencyConflict) {
		t.Fatalf("mixed-evidence loser was not refused: %v", loserErr)
	}
	restored, err := newReviewCoordinator(t.Context(), fixture.runtime, fixture.journal, fixture.journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-b": {"criterion-build", "criterion-test"}})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := restored.Candidate(first.HandoffID)
	if !ok || got.EvidenceDigest != secondRequest.EvidenceDigest {
		t.Fatalf("durable candidate mixed request evidence: %#v", got)
	}
}

func TestWorkflowRestoreRejectsTamperedOperationRequestAndResultIdentity(t *testing.T) {
	for _, mutation := range []string{"request-digest", "canonical-result"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newReviewFixture(t, "process-b")
			if _, err := fixture.coordinator.SubmitCandidate(t.Context(), fixture.capsule, fixture.handoffRequest()); err != nil {
				t.Fatal(err)
			}
			key := fixture.coordinator.journal.prefix() + "operations/operation-handoff"
			fixture.journal.mu.Lock()
			stored := fixture.journal.records[key]
			original := stored
			original.Value = append([]byte(nil), stored.Value...)
			record, err := controljournal.DecodeOperationRecordV1(stored.Value)
			if err != nil {
				fixture.journal.mu.Unlock()
				t.Fatal(err)
			}
			requestDigest := record.RequestDigest
			result := record.CanonicalResult()
			if mutation == "request-digest" {
				requestDigest = sha256.Sum256([]byte("changed request"))
			} else {
				result = []byte(`{"changed":true}`)
			}
			mutant, err := controljournal.NewOperationRecordV1(controljournal.OperationRecordInputV1{OperationID: record.OperationID, CommandType: record.CommandType, RequestDigest: requestDigest, RuntimeEpochID: record.RuntimeEpochID, ExpectedPreconditionsDigest: record.ExpectedPreconditionsDigest, ResultType: record.ResultType, ResultSchemaVersion: record.ResultSchemaVersion, CanonicalResult: result, EventIDs: record.EventIDs(), EffectIDs: record.EffectIDs()})
			if err == nil {
				stored.Value, err = controljournal.EncodeOperationRecordV1(mutant)
				fixture.journal.records[key] = stored
			}
			fixture.journal.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newReviewCoordinator(t.Context(), fixture.runtime, fixture.journal, fixture.journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-b": {"criterion-build", "criterion-test"}}); err == nil {
				t.Fatalf("%s operation identity survived restore", mutation)
			}
			fixture.journal.mu.Lock()
			fixture.journal.records[key] = original
			fixture.journal.mu.Unlock()
		})
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
