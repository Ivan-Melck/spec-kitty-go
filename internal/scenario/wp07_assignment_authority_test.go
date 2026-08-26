package scenario

import (
	"context"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/review"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

// This WP-owned scenario is intentionally narrow: it reuses the Phase 1
// authority fixture and stale-publication probe to prove that tracker evidence
// remains external evidence. Provider recovery cannot manufacture local
// authority or turn an expired workspace into the replacement workspace.
func TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newWorkspaceScenario(t, []workspaceItem{
		{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"},
		{work: "work-wp07-review", lane: "lane-wp07-review", tracker: "TK-WP07-REVIEW"},
	})
	stale := provisionPhase1StaleAuthority(t, fixture, "wp07")

	reviewInput := workspaceStart{
		operation: "operation-wp07-review", work: "work-wp07-review", lane: "lane-wp07-review",
		actor: "actor-wp07-implementer", process: "process-wp07-implementer", workspace: "workspace-wp07-review",
		branch: "runs/wp07-review", trackerWork: "TK-WP07-REVIEW", editFile: "wp07.txt", editContent: "candidate\n",
	}
	reviewCandidate := waitWorkspaceProcess(t, fixture.start(ctx, t, reviewInput))
	if !reviewCandidate.OK || reviewCandidate.Capsule == nil || reviewCandidate.Work == nil {
		t.Fatalf("review candidate=%+v", reviewCandidate)
	}
	const handoffID = "handoff-wp07-review"
	if handoff := runHandoffHelper(t, fixture, reviewCandidate.Capsule, reviewCandidate.Work.Commit, handoffID, "operation-handoff-wp07-review", ""); !handoff.OK {
		t.Fatalf("review handoff=%+v", handoff)
	}

	expected := stale.expected.clone()
	expected.addCapsule(reviewCandidate.Capsule, true)
	staleProbe := stale.probe(t, fixture, "wp07")
	assertWP07Authority := func(fault string) {
		t.Helper()
		state := phase1UniversalState(t, fixture, fault, fixture.primary.Binding().Primary, expected, []string{stale.candidate, reviewCandidate.Work.Commit})
		state.StaleProbe, state.RequireStaleProbe = staleProbe, true
		if err := assertUniversalInvariants(ctx, state); err != nil {
			t.Fatal(err)
		}
	}
	assertWP07Authority("WP07-BEFORE-PROVIDER-OUTAGE")

	providerStore, err := fake.Open(fixture.tracker)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := fake.NewProvider(providerStore).Read(ctx, "TK-PHASE1-MATRIX-STALE")
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Inject(ctx, fake.FailureUnavailable); err != nil {
		t.Fatal(err)
	}
	control, err := sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	request := execution.TrackerAssignRequest{
		SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-wp07-provider-restart",
		OperationID: "operation-wp07-provider-restart", ActorID: "actor-wp07-replacement", ProcessID: "process-wp07-replacement",
		Now: time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC), Provider: "fake", WorkKey: observed.Key,
		ExpectedRevision: observed.Revision, Assignee: "actor-wp07-replacement",
	}
	if _, err := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).Assign(ctx, request); !execution.IsKind(err, execution.KindTrackerUnavailable) {
		t.Fatalf("provider outage diagnostic=%v", err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	assertWP07Authority("WP07-PROVIDER-OUTAGE")

	// Reopen both sides to make recovery depend only on durable facts.
	providerStore, err = fake.Open(fixture.tracker)
	if err != nil {
		t.Fatal(err)
	}
	control, err = sqlite.Open(ctx, fixture.database)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).Assign(ctx, request)
	if err != nil || recovered.Revision <= observed.Revision {
		t.Fatalf("provider restart result=%+v err=%v", recovered, err)
	}
	staleRequest := request
	staleRequest.CorrelationID = "correlation-wp07-stale-process-evidence"
	staleRequest.OperationID = "operation-wp07-stale-process-evidence"
	staleRequest.ActorID = ids.ActorID(stale.capsule.AggregateIDs["actor_id"])
	staleRequest.ProcessID = ids.ProcessID(stale.capsule.AggregateIDs["process_id"])
	staleRequest.ExpectedRevision = recovered.Revision
	staleRequest.Assignee = staleRequest.ActorID.String()
	staleRequest.Now = request.Now.Add(time.Second)
	staleEvidence, err := execution.NewTrackerSaga(control, fake.NewProvider(providerStore), execution.SystemClock{}).Assign(ctx, staleRequest)
	if err != nil || staleEvidence.Revision <= recovered.Revision {
		t.Fatalf("stale-process tracker evidence=%+v err=%v", staleEvidence, err)
	}
	if err := control.Close(); err != nil {
		t.Fatal(err)
	}
	postReplay, err := fake.NewProvider(providerStore).Read(ctx, observed.Key)
	if err != nil || postReplay.Revision != staleEvidence.Revision || postReplay.Assignee != staleRequest.Assignee {
		t.Fatalf("stale replay changed provider state=%+v err=%v", postReplay, err)
	}
	assertWP07Authority("WP07-PROVIDER-RESTART-AND-STALE-PROCESS")

	selfReview := runReviewHelper(t, fixture, reviewCandidate.Capsule, reviewCandidate.Work.Commit, handoffID,
		"review-wp07-self", "operation-review-wp07-self", reviewCandidate.Capsule.AggregateIDs["actor_id"],
		"process-wp07-self-review", review.Approved, "")
	if selfReview.OK || selfReview.Error == nil || selfReview.Error.Kind != execution.ErrorKind(review.KindReviewerNotIndependent) {
		t.Fatalf("assignment recovery weakened reviewer independence=%+v", selfReview)
	}
	integrationResponse := runExternalIntegration(t, integrationArgs(fixture, fixture.database,
		"operation-integration-wp07-unapproved", "review-wp07-self", reviewCandidate.Work.Commit,
		"actor-wp07-integrator", "process-wp07-integrator", time.Now().UTC()))
	if integrationResponse.OK || integrationResponse.Error == nil || integrationResponse.Error.Kind != execution.ErrorKind(integration.KindNotApproved) {
		t.Fatalf("assignment evidence bypassed review/integration authority=%+v", integrationResponse)
	}
	assertWP07Authority("WP07-REVIEW-AND-INTEGRATION-REFUSAL")
}
