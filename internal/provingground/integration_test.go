package provingground

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	baseintegration "github.com/spec-kitty/go-kitty3/internal/integration"
	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

type twoCandidateFixture struct {
	journal     *wp08Store
	runtime     *Runtime
	review      *ReviewCoordinator
	integration *IntegrationCoordinator
	repository  *gitrepo.Repository
	root, base  string
	capsules    []controljournal.AuthorityCapsuleV1
	candidates  []string
	reviews     []ids.ReviewID
}

func newTwoCandidateFixture(t *testing.T) *twoCandidateFixture {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-b", "main")
	gitRun(t, root, "config", "user.email", "test@example.invalid")
	gitRun(t, root, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o600)
	gitRun(t, root, "add", "base.txt")
	gitRun(t, root, "commit", "-m", "base")
	base := gitRun(t, root, "rev-parse", "HEAD")
	branches := []string{"lane-b", "lane-c"}
	processes := []controljournal.ProcessID{"process-b", "process-c"}
	candidates := make([]string, 2)
	for i, branch := range branches {
		gitRun(t, root, "checkout", "-B", branch, base)
		os.WriteFile(filepath.Join(root, branch+".txt"), []byte(branch+"\n"), 0o600)
		gitRun(t, root, "add", branch+".txt")
		gitRun(t, root, "commit", "-m", branch)
		candidates[i] = gitRun(t, root, "rev-parse", "HEAD")
	}
	gitRun(t, root, "checkout", "--detach", base)
	repository, err := gitrepo.Bind(t.Context(), gitrepo.Runner{}, "repository-m1", "project-m1", root, filepath.Join(t.TempDir(), "managed"), "refs/heads/main", time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	journal := newWP08Store()
	config := testConfig(t)
	core, err := newService(config, journal, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{core: core}
	t.Cleanup(func() { assertAuthorityInvariants(t, runtime.core) })
	capsules := make([]controljournal.AuthorityCapsuleV1, 2)
	works := []controljournal.WorkID{"work-b", "work-a"}
	actors := []controljournal.ActorID{"actor-b", "actor-a"}
	leases := []controljournal.LeaseID{"lease-b", "lease-c"}
	workspaces := []controljournal.WorkspaceID{"workspace-b", "workspace-c"}
	for i := range branches {
		authority := Authority{ProjectID: config.ProjectID, MissionID: config.MissionID, WorkRevisionID: config.WorkRevisionID, WorkID: works[i], ActorID: actors[i], ProcessID: processes[i], RunID: config.RunID, LeaseID: leases[i], RuntimeEpochID: config.RuntimeEpochID, Generation: 1, FenceToken: uint64(20 + i), AuthorityHeadRecordRevision: uint64(3 + i), Deadline: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), Status: "active", OperationID: controljournal.OperationID("claim-" + branches[i])}
		state := core.works[works[i]]
		state.current = &authority
		state.logical = 1
		state.headRevision = authority.AuthorityHeadRecordRevision
		binding := WorkspaceBinding{WorkspaceID: workspaces[i], RepositoryID: "repository-m1", LaneID: ids.LaneID(branches[i]), CanonicalPath: root, OwnedBranch: branches[i], PinnedBase: base, ActorID: actors[i], ProcessID: processes[i], RunID: config.RunID, LeaseID: leases[i], Generation: 1, FenceToken: authority.FenceToken, AuthorityHeadRecordRevision: authority.AuthorityHeadRecordRevision, WorkID: works[i], WorkRevisionID: config.WorkRevisionID, RuntimeEpochID: config.RuntimeEpochID, LogicalVersion: 1, RecordRevision: authority.AuthorityHeadRecordRevision, Status: "active-bound", OperationID: controljournal.OperationID("bind-" + branches[i]), RequestedAt: time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC), CommandDigest: digestJSON(branches[i])}
		core.workspaces[workspaces[i]] = binding
		capsules[i], err = authorityCapsule(authority, binding)
		if err != nil {
			t.Fatal(err)
		}
	}
	reviewCoordinator, err := newReviewCoordinator(t.Context(), runtime, journal, journal, repository, testEligibility{}, map[ids.WorkID][]string{"work-a": {"criterion"}, "work-b": {"criterion"}})
	if err != nil {
		t.Fatal(err)
	}
	reviewIDs := []ids.ReviewID{"review-b", "review-a"}
	for i := range branches {
		handoffRequest := handoff.Request{SchemaVersion: 1, CorrelationID: "handoff", OperationID: ids.OperationID("operation-handoff-" + branches[i]), HandoffID: ids.HandoffID("handoff-" + branches[i]), ProjectID: "project-m1", MissionID: "mission-m1", WorkRevisionID: "revision-one", WorkID: ids.WorkID(works[i]), RunID: "run-m1", WorkspaceID: ids.WorkspaceID(workspaces[i]), LeaseID: ids.LeaseID(leases[i]), LeaseGeneration: 1, Token: string(rune('0' + 20 + i)), ActorID: ids.ActorID(actors[i]), ProcessID: ids.ProcessID(processes[i]), Candidate: candidates[i], Contributors: []ids.ActorID{ids.ActorID(actors[i])}, EvidenceDigest: strings.Repeat(string(rune('a'+i)), 64), SubmittedAt: time.Date(2026, 8, 30, 12, 0, i, 0, time.UTC), ExpectedEligibility: completeTestFact(ids.WorkID(works[i]), workgraph.StatusActive, workgraph.EligibilityHandoff)}
		handoffRequest.Token = fmtInt(20 + i)
		if _, err := reviewCoordinator.SubmitCandidate(t.Context(), capsules[i], handoffRequest); err != nil {
			t.Fatal(err)
		}
		reviewRequest := basereview.Request{SchemaVersion: 1, CorrelationID: "review", OperationID: ids.OperationID("operation-review-" + branches[i]), ReviewID: reviewIDs[i], HandoffID: handoffRequest.HandoffID, WorkRevisionID: "revision-one", WorkID: ids.WorkID(works[i]), LeaseID: ids.LeaseID(leases[i]), LeaseGeneration: 1, Token: "review", ActorID: ids.ActorID("reviewer-" + branches[i]), ProcessID: ids.ProcessID("review-process-" + branches[i]), Candidate: candidates[i], Verdict: basereview.Approved, Criteria: []basereview.CriterionResult{{CriterionID: "criterion", Status: basereview.Passed, Evidence: "evidence"}}, EvidenceDigest: strings.Repeat(string(rune('f'-i)), 64), SubmittedAt: time.Date(2026, 8, 30, 12, 1, i, 0, time.UTC), ExpectedEligibility: completeTestFact(ids.WorkID(works[i]), workgraph.StatusHandedOff, workgraph.EligibilityReview)}
		if _, err := reviewCoordinator.SubmitReview(t.Context(), capsules[i], reviewRequest); err != nil {
			t.Fatal(err)
		}
	}
	integrationCoordinator, _ := NewIntegrationCoordinator(reviewCoordinator)
	return &twoCandidateFixture{journal: journal, runtime: runtime, review: reviewCoordinator, integration: integrationCoordinator, repository: repository, root: root, base: base, capsules: capsules, candidates: candidates, reviews: reviewIDs}
}
func fmtInt(v int) string {
	if v < 10 {
		return string(rune('0' + v))
	}
	return string(rune('0'+v/10)) + string(rune('0'+v%10))
}
func (f *twoCandidateFixture) item(i int, operation string) IntegrationItem {
	reviewRecord, ok := f.review.Review(f.reviews[i])
	if !ok {
		panic("fixture review is absent")
	}
	workID := reviewRecord.WorkID
	return IntegrationItem{Capsule: f.capsules[i], Request: baseintegration.Request{SchemaVersion: 1, CorrelationID: "integration", OperationID: ids.OperationID(operation), ReviewID: f.reviews[i], RepositoryID: "repository-m1", TargetRef: "refs/heads/main", ActorID: "integrator", ProcessID: ids.ProcessID("integrator-" + operation), Candidate: f.candidates[i], LeaseTTL: time.Minute, StartedAt: time.Date(2026, 8, 30, 12, 2, i, 0, time.UTC), ExpectedEligibility: completeTestFact(workID, workgraph.StatusApproved, workgraph.EligibilityIntegration)}}
}

func TestIntegrationTwoCandidatesSerializeAndPublishExactlyOnce(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	records, err := fixture.integration.IntegrateApproved(t.Context(), []IntegrationItem{fixture.item(1, "operation-integrate-a"), fixture.item(0, "operation-integrate-b")})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records=%d", len(records))
	}
	for _, record := range records {
		if record.Status != baseintegration.StatusDone || record.ResultCommit == "" {
			t.Fatalf("record=%#v", record)
		}
	}
	head := gitRun(t, fixture.root, "rev-parse", "refs/heads/main")
	for _, candidate := range fixture.candidates {
		gitRun(t, fixture.root, "merge-base", "--is-ancestor", candidate, head)
	}
	before := head
	if _, err := fixture.integration.Integrate(t.Context(), fixture.item(0, "operation-duplicate-candidate")); !baseintegration.IsKind(err, baseintegration.KindIdempotencyConflict) {
		t.Fatalf("duplicate candidate survived: %v", err)
	}
	if after := gitRun(t, fixture.root, "rev-parse", "refs/heads/main"); after != before {
		t.Fatal("duplicate candidate moved target")
	}
}

func TestIntegrationCompetingInvalidAndManualMovementFailClosed(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	invalid := fixture.item(0, "operation-invalid")
	invalid.Request.ReviewID = "missing-review"
	if _, err := fixture.integration.Integrate(t.Context(), invalid); !baseintegration.IsKind(err, baseintegration.KindNotApproved) {
		t.Fatalf("invalid review: %v", err)
	}
	if refs := gitRun(t, fixture.root, "for-each-ref", "--format=%(refname)", "refs/spec-kitty/fences/"); refs != "" {
		t.Fatalf("fence acquired before refusal: %s", refs)
	}
	items := []IntegrationItem{fixture.item(0, "operation-race-one"), fixture.item(0, "operation-race-two")}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range items {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, errs[i] = fixture.integration.Integrate(t.Context(), items[i]) }(i)
	}
	wg.Wait()
	success := 0
	for _, err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("competing successes=%d errors=%v", success, errs)
	}
	second := newTwoCandidateFixture(t)
	movement := second.item(0, "operation-manual-move")
	hook := &callbackHook{cut: "IG-03", fn: func() { gitRun(t, second.root, "update-ref", "refs/heads/main", second.candidates[1], second.base) }}
	second.integration.WithFaults(hook)
	if _, err := second.integration.Integrate(t.Context(), movement); !baseintegration.IsKind(err, baseintegration.KindTargetMoved) {
		t.Fatalf("manual movement did not fail closed: %v", err)
	}
}

func TestIntegrationInvalidatedRevisionRefusesBeforeRepositoryFence(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	fixture.review.store.eligibility = testEligibility{disposition: workgraph.Invalidate}
	item := fixture.item(0, "operation-invalidated-revision")
	item.Request.ExpectedEligibility = completeTestFactWithDisposition(
		item.Request.ExpectedEligibility.WorkID(),
		workgraph.StatusApproved,
		workgraph.Invalidate,
		workgraph.EligibilityIntegration,
	)

	if _, err := fixture.integration.Integrate(t.Context(), item); !baseintegration.IsKind(err, baseintegration.KindRevisionIneligible) {
		t.Fatalf("invalidated revision survived: %v", err)
	}
	if refs := gitRun(t, fixture.root, "for-each-ref", "--format=%(refname)", "refs/spec-kitty/fences/"); refs != "" {
		t.Fatalf("eligibility refusal occurred after fence acquisition: %s", refs)
	}
	if head := gitRun(t, fixture.root, "rev-parse", "refs/heads/main"); head != fixture.base {
		t.Fatalf("eligibility refusal moved target: %s", head)
	}
}

func TestIntegrationPlanRefusesManualMovementBetweenCandidates(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	fixture.integration.WithFaults(&callbackHook{cut: "IGP-02", fn: func() {
		gitRun(t, fixture.root, "update-ref", "refs/heads/main", fixture.base)
	}})
	results, err := fixture.integration.IntegrateApproved(t.Context(), []IntegrationItem{
		fixture.item(1, "operation-plan-movement-a"),
		fixture.item(0, "operation-plan-movement-b"),
	})
	if !baseintegration.IsKind(err, baseintegration.KindTargetMoved) || len(results) != 1 {
		t.Fatalf("between-candidate movement results=%d err=%v", len(results), err)
	}
	done := 0
	fixture.review.store.mu.Lock()
	for _, entry := range fixture.review.store.integrations {
		if entry.Record.Status == baseintegration.StatusDone {
			done++
		}
	}
	fixture.review.store.mu.Unlock()
	if done != 1 {
		t.Fatalf("manual movement produced %d completed publications", done)
	}
}

func TestCompetingWholeIntegrationPlansCannotInterleave(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	plans := [][]IntegrationItem{
		{fixture.item(0, "operation-whole-plan-a1"), fixture.item(1, "operation-whole-plan-a2")},
		{fixture.item(0, "operation-whole-plan-b1"), fixture.item(1, "operation-whole-plan-b2")},
	}
	var wg sync.WaitGroup
	errs := make([]error, len(plans))
	for index := range plans {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, errs[index] = fixture.integration.IntegrateApproved(t.Context(), plans[index])
		}(index)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("competing plan successes=%d errors=%v", successes, errs)
	}
	done := 0
	fixture.review.store.mu.Lock()
	for _, entry := range fixture.review.store.integrations {
		if entry.Record.Status == baseintegration.StatusDone {
			done++
		}
	}
	fixture.review.store.mu.Unlock()
	if done != 2 {
		t.Fatalf("competing plans produced %d completed publications", done)
	}
}

func TestIntegrationPlanCrashFenceRefusesDifferentPlanAndExactPlanResumes(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	exact := []IntegrationItem{
		fixture.item(0, "operation-crash-plan-a1"),
		fixture.item(1, "operation-crash-plan-a2"),
	}
	crash := errors.New("crash after first candidate")
	fixture.integration.WithFaults(&callbackHook{cut: "IGP-02", err: crash})
	partial, err := fixture.integration.IntegrateApproved(t.Context(), exact)
	if !errors.Is(err, crash) || len(partial) != 1 {
		t.Fatalf("partial plan results=%d err=%v", len(partial), err)
	}
	restoredReview, restoreErr := newReviewCoordinator(t.Context(), fixture.runtime, fixture.journal, fixture.journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-a": {"criterion"}, "work-b": {"criterion"}})
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	restoredIntegration, restoreErr := NewIntegrationCoordinator(restoredReview)
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	different := []IntegrationItem{
		fixture.item(0, "operation-crash-plan-b1"),
		fixture.item(1, "operation-crash-plan-b2"),
	}
	if _, err := restoredIntegration.IntegrateApproved(t.Context(), different); !baseintegration.IsKind(err, baseintegration.KindAuthorityBusy) {
		t.Fatalf("different plan crossed durable crash authority: %v", err)
	}
	resumed, err := restoredIntegration.IntegrateApproved(t.Context(), exact)
	if err != nil || len(resumed) != 2 {
		t.Fatalf("exact plan resume results=%d err=%v", len(resumed), err)
	}
	authorityID := planAuthorityAggregateID("repository-m1", "refs/heads/main")
	restoredReview.store.mu.Lock()
	authority := restoredReview.store.planAuthorities[authorityID]
	restoredReview.store.mu.Unlock()
	if authority.Status != planAuthorityCompleted || authority.CompletedTarget != gitRun(t, fixture.root, "rev-parse", "refs/heads/main") {
		t.Fatalf("completed plan authority=%#v", authority)
	}
}

type callbackHook struct {
	mu    sync.Mutex
	cut   string
	fn    func()
	err   error
	fired bool
}

func (h *callbackHook) Reach(_ context.Context, cut string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cut == h.cut && !h.fired {
		h.fired = true
		if h.fn != nil {
			h.fn()
		}
		return h.err
	}
	return nil
}
func TestIntegrationCrashAfterGitPublicationReconcilesFromFact(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	crash := errors.New("crash after git publication")
	hook := &callbackHook{cut: "IG-06", err: crash}
	fixture.integration.WithFaults(hook)
	item := fixture.item(0, "operation-crash-reconcile")
	if _, err := fixture.integration.Integrate(t.Context(), item); !errors.Is(err, crash) {
		t.Fatalf("want crash: %v", err)
	}
	published := gitRun(t, fixture.root, "rev-parse", "refs/heads/main")
	restoredReview, restoreErr := newReviewCoordinator(t.Context(), fixture.runtime, fixture.journal, fixture.journal, fixture.repository, testEligibility{}, map[ids.WorkID][]string{"work-a": {"criterion"}, "work-b": {"criterion"}})
	if restoreErr != nil {
		t.Fatal(restoreErr)
	}
	restoredIntegration, _ := NewIntegrationCoordinator(restoredReview)
	record, err := restoredIntegration.Integrate(t.Context(), item)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != baseintegration.StatusDone || record.ObservedTarget != published {
		t.Fatalf("reconciled=%#v", record)
	}
	if got := gitRun(t, fixture.root, "rev-parse", "refs/heads/main"); got != published {
		t.Fatal("recovery duplicated publication")
	}
}

func TestIntegrationRevalidatesCapsuleImmediatelyBeforePublication(t *testing.T) {
	fixture := newTwoCandidateFixture(t)
	item := fixture.item(0, "operation-prepublication-stale")
	hook := &callbackHook{cut: "IG-05", fn: func() {
		state := fixture.runtime.core.works["work-b"]
		state.mu.Lock()
		state.current.AuthorityHeadRecordRevision++
		state.mu.Unlock()
	}}
	fixture.integration.WithFaults(hook)
	if _, err := fixture.integration.Integrate(t.Context(), item); err == nil {
		t.Fatal("stale capsule published after pre-publication check")
	}
	if head := gitRun(t, fixture.root, "rev-parse", "refs/heads/main"); head != fixture.base {
		t.Fatalf("target moved despite stale capsule: %s", head)
	}
}

func TestWP08FocusedCampaignHooksEnforceOneHundredRepetitions(t *testing.T) {
	for name, campaign := range map[string]func(context.Context, int, func(context.Context, int) error) error{
		"attribution-reassignment": IntegrationAttributionCampaign,
		"competing-integration":    CompetingIntegrationCampaign,
	} {
		t.Run(name, func(t *testing.T) {
			var calls int
			if err := campaign(t.Context(), 100, func(context.Context, int) error {
				calls++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if calls != 100 {
				t.Fatalf("campaign calls=%d", calls)
			}
			if err := campaign(t.Context(), 99, func(context.Context, int) error { return nil }); err == nil {
				t.Fatal("campaign admitted fewer than 100 repetitions")
			}
		})
	}
}
