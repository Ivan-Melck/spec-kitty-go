package v2

import (
	"context"
	"testing"
)

func TestProjectionRefusesDigestMismatchWithoutAdvancing(t *testing.T) {
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	plan.heads[1].CanonicalStateDigest = digest("wrong", nil)
	// Resealing is intentionally impossible after this internal controlled
	// mutant, so construct a correctly attributed acknowledgement against the
	// mutant's still-original sealed identity and prove projection validation is
	// independently fail-closed.
	tracker := fixtureProjectionTracker(t)
	tracker.pending[plan.PublicationID()] = plan.Digest()
	observation, _ := NewCommitObservationV2(plan.PublicationID(), plan.Digest(), plan.RecordSetDigest(), 1, 1, plan.MutationCount())
	seal, _ := acknowledgementSeal(plan, observation)
	ack := AcknowledgedPublicationV2{plan: plan, observation: observation, seal: seal}
	if err := tracker.applyAcknowledged(ack); !IsKind(err, ErrorInvalidDigest) {
		t.Fatalf("planned-head digest mismatch advanced projection: %v", err)
	}
	if tracker.LedgerSize() != 0 || tracker.HighestObservedRevision() != 0 || len(tracker.states) != 0 {
		t.Fatal("failed projection application left partial state")
	}
}

func TestProjectionRejectsConflictingProviderRevision(t *testing.T) {
	left := projectionPlan(t, "operation-01", "mission-01", "left")
	right := projectionPlan(t, "operation-02", "mission-02", "right")
	tracker := fixtureProjectionTracker(t)
	port := &fixedRevisionPortV2{revisions: []uint64{7, 7}}
	coordinator, _ := NewPublicationCoordinatorV2(port, tracker)
	if _, err := coordinator.Publish(context.Background(), left); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Publish(context.Background(), right); !IsKind(err, ErrorInvalidTransition) {
		t.Fatalf("conflicting provider revision accepted: %v", err)
	}
	if tracker.LedgerSize() != 1 || coordinator.UnresolvedCount() != 1 {
		t.Fatal("conflicting revision mutated acknowledged ledger or lost ambiguity")
	}
}

func TestSealedProjectionPrefixRejectsZeroPartialAndSubstitution(t *testing.T) {
	plan := projectionPlan(t, "operation-01", "mission-01", "planned")
	tracker := fixtureProjectionTracker(t)
	port := &fixedRevisionPortV2{revisions: []uint64{9}}
	coordinator, _ := NewPublicationCoordinatorV2(port, tracker)
	if _, err := coordinator.Publish(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	sealed, err := coordinator.SealProjectionPrefix()
	if err != nil || sealed.Validate() != nil {
		t.Fatalf("valid projection seal rejected: %v", err)
	}
	if err := (SealedProjectionPrefixV2{}).Validate(); err == nil {
		t.Fatal("zero projection seal accepted")
	}
	partial := sealed
	partial.seal = Digest{}
	if err := partial.Validate(); err == nil {
		t.Fatal("partial projection seal accepted")
	}
	substituted := sealed
	substituted.entries[0].canonical = []byte(`{"substituted":true}`)
	if err := substituted.Validate(); err == nil {
		t.Fatal("substituted projection state accepted")
	}
	summary := sealed.OperationSummary()
	if summary.Total() != 1 || summary.StatusCount(StatusAccepted) != 1 || len(summary.LeafDigests()) != 1 || summary.Digest().IsZero() {
		t.Fatalf("operation summary is not compact and complete: %+v", summary)
	}
}
