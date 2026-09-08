package scenario

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

const familyCutTrials = 100

var familyPublicationCuts = []string{
	"before-publication",
	"durable-before-acknowledgement",
	"acknowledgement-before-projection",
	"lost-acknowledgement",
	"malformed-acknowledgement",
}

// capturedPlanSequence records the actual reducer-compatible plans emitted by
// typed production capabilities. It is observer-only: no callback can change
// publication control flow, grant authority, or alter a sealed plan.
type capturedPlanSequence struct {
	mu    sync.Mutex
	plans []journalv2.SealedCommitPlanV2
}

func (c *capturedPlanSequence) ObservePublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) {
	if snapshot.Stage != journalv2.PublicationRegisteredBeforeCommitV2 {
		return
	}
	c.mu.Lock()
	c.plans = append(c.plans, snapshot.Plan)
	c.mu.Unlock()
}

func (c *capturedPlanSequence) snapshot() []journalv2.SealedCommitPlanV2 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]journalv2.SealedCommitPlanV2(nil), c.plans...)
}

func TestM2FamilyBoundPublicationCutCampaign(t *testing.T) {
	capture := &capturedPlanSequence{}
	effectReconciler := &scenarioEffectReconciler{}
	h := newPortableHarness(t, ProductionConfig{
		ExecutablePath: productionExecutable(t), Seed: t.Name(), Reconciler: effectReconciler,
		PublicationBoundaryObserver: capture, CandidateVerifier: sourceBoundCandidateVerifier{},
	})
	projectID, missionID := "project-family-cut", "mission-family-cut"
	revision, governance := twoItemRevision(t, projectID, missionID)
	missionResult := h.createMission(projectID, missionID, "operation-family-cut-mission")
	result := continueTwoItemMissionScenario(t, h, missionResult, revision, governance, effectReconciler, false)
	if result.Lifecycle != "completed" {
		t.Fatalf("captured production mission incomplete: %+v", result)
	}

	want := []string{"acceptance", "artifact_catalog", "artifact_revision", "execution_slot", "external_effect", "handoff_slot", "integration_attempt", "integration_target", "mission", "operation", "review_slot", "workgraph", "workspace"}
	seen := map[string]struct{}{}
	plans := capture.snapshot()
	for _, plan := range plans {
		for _, event := range plan.Events() {
			seen[string(event.Family())] = struct{}{}
		}
	}
	var got []string
	for family := range seen {
		if family != "runtime_host" {
			got = append(got, family)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("typed production plan family census differs: got=%v want=%v plans=%d", got, want, len(plans))
	}

	definitionID := iterationreceipt.DefinitionID("fault/family-bound-publication-cuts")
	for _, family := range want {
		indices := planIndicesForFamily(plans, family)
		if len(indices) == 0 {
			t.Fatalf("no real production plan emits %s", family)
		}
		for _, cut := range familyPublicationCuts {
			for trial := 0; trial < familyCutTrials; trial++ {
				planIndex := indices[trial%len(indices)]
				started := time.Now()
				facts, invariants, outcome := executeFamilyCut(t, plans, planIndex, family, cut)
				plan := plans[planIndex]
				emitted := planFamilies(plan)
				seed := fmt.Sprintf("%s/%s/%03d/%d", family, cut, trial, planIndex)
				record, err := iterationreceipt.NewFamilyCut(definitionID, family, cut, trial, seed, plan.Digest().String(), string(plan.PublicationID()), emitted, outcome, time.Since(started), facts, invariants)
				if err != nil {
					t.Fatal(err)
				}
				iterationreceipt.EmitFamilyCut(t, record)
			}
		}
	}
}

func planIndicesForFamily(plans []journalv2.SealedCommitPlanV2, family string) []int {
	var result []int
	for index, plan := range plans {
		if slices.Contains(planFamilies(plan), family) {
			result = append(result, index)
		}
	}
	return result
}

func planFamilies(plan journalv2.SealedCommitPlanV2) []string {
	set := map[string]struct{}{}
	for _, event := range plan.Events() {
		set[string(event.Family())] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for family := range set {
		result = append(result, family)
	}
	slices.Sort(result)
	return result
}

type familyCutProbe struct {
	publication journalv2.PublicationID
	stage       journalv2.PublicationBoundaryStageV2
	fired       bool
}

func (p *familyCutProbe) PublicationBoundary(_ context.Context, snapshot journalv2.PublicationBoundarySnapshotV2) error {
	if !p.fired && snapshot.Plan.PublicationID() == p.publication && snapshot.Stage == p.stage {
		p.fired = true
		return journalv2.InterruptQualificationPublicationV2(snapshot.Stage)
	}
	return nil
}

type familyCutPort struct {
	revision    uint64
	target      journalv2.PublicationID
	mode        string
	calls       int
	observation journalv2.CommitObservationV2
}

func (p *familyCutPort) Commit(_ context.Context, plan journalv2.SealedCommitPlanV2) (journalv2.CommitObservationV2, error) {
	p.calls++
	p.revision++
	observation, err := journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount(), plan.AssertionCount())
	if err != nil {
		return journalv2.CommitObservationV2{}, err
	}
	if plan.PublicationID() != p.target {
		return observation, nil
	}
	p.observation = observation
	switch p.mode {
	case "lost-acknowledgement":
		return journalv2.CommitObservationV2{}, errors.New("qualification: acknowledgement lost after durable commit")
	case "malformed-acknowledgement":
		return journalv2.NewCommitObservationWithAssertionsV2(plan.PublicationID(), plan.RecordSetDigest(), plan.RecordSetDigest(), p.revision, p.revision, plan.MutationCount(), plan.AssertionCount())
	default:
		return observation, nil
	}
}

func executeFamilyCut(t *testing.T, plans []journalv2.SealedCommitPlanV2, targetIndex int, family, cut string) (map[string]any, map[string]any, string) {
	t.Helper()
	target := plans[targetIndex]
	tracker, coordinator, port := newFamilyCutReplay(t, target, cut)
	for index := 0; index < targetIndex; index++ {
		if _, err := coordinator.Publish(t.Context(), plans[index]); err != nil {
			t.Fatalf("replay prerequisite %d for %s: %v", index, cut, err)
		}
	}
	beforeCalls, beforeLedger := port.calls, tracker.LedgerSize()
	_, cutErr := coordinator.Publish(t.Context(), target)
	if cutErr == nil {
		t.Fatalf("family cut %s did not interrupt", cut)
	}
	if coordinator.UnresolvedCount() != 1 || tracker.LedgerSize() != beforeLedger {
		t.Fatalf("family cut %s partially projected: unresolved=%d ledger=%d/%d", cut, coordinator.UnresolvedCount(), tracker.LedgerSize(), beforeLedger)
	}
	wantCalls := beforeCalls + 1
	if cut == "before-publication" {
		wantCalls = beforeCalls
	}
	if port.calls != wantCalls {
		t.Fatalf("family cut %s provider calls=%d want=%d", cut, port.calls, wantCalls)
	}

	if cut == "before-publication" {
		// Registration was only volatile. A fresh owner replays the exact
		// durable prefix and executes the target once.
		freshTracker, freshCoordinator, freshPort := newFamilyCutReplay(t, target, "")
		for index := 0; index < targetIndex; index++ {
			if _, err := freshCoordinator.Publish(t.Context(), plans[index]); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := freshCoordinator.Publish(t.Context(), target); err != nil {
			t.Fatalf("fresh-owner target publication: %v", err)
		}
		tracker, coordinator, port = freshTracker, freshCoordinator, freshPort
	} else {
		if cut != "lost-acknowledgement" && cut != "malformed-acknowledgement" {
			// The port observation was valid but the campaign interrupted before
			// projection. The port retains the exact immutable observation.
			if port.observation.PublicationID() == "" {
				t.Fatal("durable observation missing at injected cut")
			}
		}
		if port.observation.PublicationID() == "" {
			t.Fatal("cut did not retain exact durable observation")
		}
		foreign, err := journalv2.NewCommitObservationWithAssertionsV2(target.PublicationID(), target.RecordSetDigest(), target.RecordSetDigest(), port.observation.Revision(), port.observation.DurableThroughRevision(), target.MutationCount(), target.AssertionCount())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := coordinator.ReconcileComplete(target, foreign); err == nil || coordinator.UnresolvedCount() != 1 || tracker.LedgerSize() != beforeLedger {
			t.Fatalf("substituted observation reconciled at %s: %v", cut, err)
		}
		if _, err := coordinator.ReconcileComplete(target, port.observation); err != nil {
			t.Fatalf("exact observation did not reconcile at %s: %v", cut, err)
		}
		if _, err := coordinator.ReconcileComplete(target, port.observation); err == nil {
			t.Fatalf("duplicate reconciliation minted a second result at %s", cut)
		}
	}
	if coordinator.UnresolvedCount() != 0 || tracker.LedgerSize() != targetIndex+1 {
		t.Fatalf("terminal family cut invariant failed: unresolved=%d ledger=%d want=%d", coordinator.UnresolvedCount(), tracker.LedgerSize(), targetIndex+1)
	}
	facts := map[string]any{"cut": cut, "qualified_family": family, "plan_index": targetIndex, "provider_calls": port.calls, "publication_id": target.PublicationID(), "emitted_families": planFamilies(target)}
	invariants := map[string]any{"unresolved": coordinator.UnresolvedCount(), "ledger_size": tracker.LedgerSize(), "expected_ledger_size": targetIndex + 1, "one_terminal_projection": true, "substitution_refused": true}
	return facts, invariants, "interrupted-reconciled-once"
}

func newFamilyCutReplay(t *testing.T, target journalv2.SealedCommitPlanV2, cut string) (*journalv2.ProjectionTrackerV2, *journalv2.PublicationCoordinatorV2, *familyCutPort) {
	t.Helper()
	registry := journalv2.DefaultEventRegistryV2()
	reducers, err := productionruntime.BuildReducerSetV2(registry)
	if err != nil {
		t.Fatal(err)
	}
	tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
	if err != nil {
		t.Fatal(err)
	}
	port := &familyCutPort{target: target.PublicationID(), mode: cut}
	var probe journalv2.QualificationPublicationBoundaryProbeV2
	switch cut {
	case "before-publication":
		probe = &familyCutProbe{publication: target.PublicationID(), stage: journalv2.PublicationRegisteredBeforeCommitV2}
	case "durable-before-acknowledgement":
		probe = &familyCutProbe{publication: target.PublicationID(), stage: journalv2.PublicationDurableObservedBeforeAckV2}
	case "acknowledgement-before-projection":
		probe = &familyCutProbe{publication: target.PublicationID(), stage: journalv2.PublicationAckCreatedBeforeProjectionV2}
	}
	coordinator, err := journalv2.NewQualificationPublicationCoordinatorV2(port, tracker, probe)
	if err != nil {
		t.Fatal(err)
	}
	return tracker, coordinator, port
}
