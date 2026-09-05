package gapdb

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestWorkgraphAuthorityAdmitsOnlyExactOwnedSealedPlan(t *testing.T) {
	plan := gapdbWorkgraphPlanV2(t)
	sealed := workgraphSealedPlanV2(t, plan)
	port := &recordingAuthorityPortV2{}
	authority := newWorkgraphAuthorityV2ForTest(port)
	_, err := authority.Publish(t.Context(), plan)
	if err != nil || port.calls != 1 || port.plan.Digest() != sealed.Digest() {
		t.Fatalf("workgraph plan was not delegated exactly once: calls=%d err=%v", port.calls, err)
	}
	if _, err := authority.Publish(t.Context(), workgraph.AuthorityPlanV2{}); !IsKind(err, OutcomeInvalid) || port.calls != 1 {
		t.Fatalf("unminted workgraph capability admitted: calls=%d err=%v", port.calls, err)
	}
}
func TestWorkgraphAuthorityRefusesNil(t *testing.T) {
	if _, err := newWorkgraphAuthorityV2(nil); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("nil coordinator admitted: %v", err)
	}
	var authority *workgraphAuthorityV2
	if _, err := authority.Publish(t.Context(), gapdbWorkgraphPlanV2(t)); !IsKind(err, OutcomeInvalid) {
		t.Fatalf("nil receiver admitted: %v", err)
	}
}
func TestWorkgraphAuthorityPublishesCompleteBatchThroughCoordinatorAndGapDB(t *testing.T) {
	plan := gapdbWorkgraphPlanV2(t)
	assertGapDBAuthorityPublication(t, workgraphSealedPlanV2(t, plan), func(c *journalv2.PublicationCoordinatorV2) (journalv2.AcknowledgedPublicationV2, error) {
		a, _ := newWorkgraphAuthorityV2(c)
		return a.Publish(t.Context(), plan)
	})
}

func TestWorkgraphAuthorityHeadCASAllowsOneWinnerAndIndependentMissions(t *testing.T) {
	shared := newV2AtomicMemory()
	sharedAuthority := openWorkgraphAuthorityTestV2(t, shared)
	sharedPlans := []workgraph.AuthorityPlanV2{
		gapdbWorkgraphPlanForV2(t, "shared-a", "mission-workgraph-shared", "work-shared-a"),
		gapdbWorkgraphPlanForV2(t, "shared-b", "mission-workgraph-shared", "work-shared-b"),
	}
	success := 0
	for _, err := range publishWorkgraphPlansConcurrentlyV2(t, sharedAuthority, sharedPlans) {
		if err == nil {
			success++
		} else if !IsKind(err, OutcomeConflict) {
			t.Fatalf("same workgraph head returned unstable outcome: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("same workgraph head admitted %d winners", success)
	}

	independent := newV2AtomicMemory()
	independentAuthority := openWorkgraphAuthorityTestV2(t, independent)
	independentPlans := []workgraph.AuthorityPlanV2{
		gapdbWorkgraphPlanForV2(t, "independent-a", "mission-workgraph-a", "work-independent-a"),
		gapdbWorkgraphPlanForV2(t, "independent-b", "mission-workgraph-b", "work-independent-b"),
	}
	for _, err := range publishWorkgraphPlansConcurrentlyV2(t, independentAuthority, independentPlans) {
		if err != nil {
			t.Fatalf("independent mission was globally serialized: %v", err)
		}
	}
}

func gapdbWorkgraphPlanV2(t *testing.T) workgraph.AuthorityPlanV2 {
	t.Helper()
	return gapdbWorkgraphPlanForV2(t, "gapdb", "mission-gapdb", "work-gapdb")
}

func gapdbWorkgraphPlanForV2(t *testing.T, label, missionID, workID string) workgraph.AuthorityPlanV2 {
	t.Helper()
	work, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-gapdb", WorkID: ids.WorkID(workID), Title: "Implement GapDB authority", Description: "Publish one bounded workgraph through GapDB.", DesignContext: "Use the consumer-owned authority port.", Criteria: []workgraph.CriterionObservation{{ID: "criterion-gapdb", Text: "Run the adapter test.", Outcome: "The adapter delegates once.", Verifier: "test", Requirement: workgraph.CriterionRequired}}, WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/controlstore/gapdb", Domain: "controlstore", Access: workgraph.ScopeWriteExclusive}}, Provenance: []workgraph.ScopeReference{{ID: "plan-gapdb", Kind: workgraph.ScopePlanConcern}}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workgraph.NewDraftObservation("project-gapdb", ids.MissionID(missionID), "artifact-plan-"+label, strings.Repeat("b", 64), []workgraph.ApprovedScope{{ID: "plan-gapdb", Kind: workgraph.ScopePlanConcern, MissionID: ids.MissionID(missionID)}}, []workgraph.WorkObservation{work})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(workID))
	observation, err := workgraph.NewFinalizationObservation(draft, []workgraph.ObservationEvidence{{WorkID: ids.WorkID(workID), Provider: "durable_fake", ExternalID: "external-" + label, Revision: "provider-gapdb", MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", sum), CriterionEvidenceHints: map[string]string{"criterion-gapdb": "go test output"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := workgraph.BuildGenerationOneCandidateV3(workgraph.GenerationOneCandidateV3Params{Observation: observation, PlanningSourceCommit: strings.Repeat("c", 40), MissionDefinition: workgraph.MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("d", 64)}, DependencyPolicy: workgraph.DependencyApprovedOrDone, FinalizedBy: "actor-gapdb", FinalizedAt: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC), GovernanceContextID: "governance-gapdb"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := workgraph.BuildFinalizationAuthorityV2(workgraph.FinalizationPlanInputV2{Context: workgraph.AuthorityContextV2{OperationID: journalv2.OperationID("operation-workgraph-" + label), RuntimeEpochID: "epoch-gapdb", ActorID: "actor-gapdb", ProcessID: "process-gapdb", DoctrineRevision: "doctrine-gapdb", ProfileRevision: "profile-gapdb", PolicyRevision: "policy-gapdb", Governance: []string{"governance-gapdb"}, ObservedCreatedAt: "2026-09-05T01:00:00Z", OperationEventID: journalv2.EventID("event-operation-workgraph-" + label), DomainEventID: journalv2.EventID("event-workgraph-" + label)}, Revision: revision})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func workgraphSealedPlanV2(t *testing.T, plan workgraph.AuthorityPlanV2) journalv2.SealedCommitPlanV2 {
	t.Helper()
	sealed, err := plan.Publication()
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func openWorkgraphAuthorityTestV2(t *testing.T, memory *v2AtomicMemory) *workgraphAuthorityV2 {
	t.Helper()
	adapter, err := newAdapter(memory.provider(), "/explicit/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := gapdbAuthorityProjectionTrackerV2()
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := journalv2.NewPublicationCoordinatorV2(adapter, tracker)
	if err != nil {
		t.Fatal(err)
	}
	authority, err := newWorkgraphAuthorityV2(coordinator)
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func publishWorkgraphPlansConcurrentlyV2(t *testing.T, authority *workgraphAuthorityV2, plans []workgraph.AuthorityPlanV2) []error {
	t.Helper()
	start := make(chan struct{})
	results := make([]error, len(plans))
	var workers sync.WaitGroup
	workers.Add(len(plans))
	for index := range plans {
		go func(index int) {
			defer workers.Done()
			<-start
			_, results[index] = authority.Publish(t.Context(), plans[index])
		}(index)
	}
	close(start)
	workers.Wait()
	return results
}
