package workgraph

import (
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestDispositionConsequencesAreExplicitForEveryWorkStatus(t *testing.T) {
	t.Parallel()
	oldRevision := finalizedSingle(t, "revision-1", 1, "work-old")
	newRevision := finalizedSingle(t, "revision-2", 2, "work-old")
	statuses := []WorkStatus{StatusActive, StatusHandedOff, StatusApproved, StatusIntegrated}
	dispositions := []Disposition{Continue, Supersede, Invalidate}
	for _, disposition := range dispositions {
		reconciliation, err := Reconcile(ReconcileRequest{
			ID: "reconciliation-" + string(disposition), Old: oldRevision, New: newRevision,
			Disposition: disposition, ActorID: "actor-owner", Rationale: "explicit owner decision",
			OldWorkIDs: []ids.WorkID{"work-old"}, NewWorkIDs: []ids.WorkID{"work-old"}, OccurredAt: time.Unix(1, 0),
		})
		if err != nil {
			t.Fatalf("%s: %v", disposition, err)
		}
		for _, status := range statuses {
			consequence, ok := reconciliation.Consequence("work-old", status)
			if !ok || consequence.Reason == "" {
				t.Fatalf("%s/%s has no queryable reason: %+v", disposition, status, consequence)
			}
			if status == StatusIntegrated && !consequence.PreservesIntegratedHistory {
				t.Fatalf("%s rewrites integrated history: %+v", disposition, consequence)
			}
			switch disposition {
			case Continue:
				if status != StatusIntegrated && (!consequence.RunMayContinue || !consequence.HandoffEligible || !consequence.IntegrationEligible) {
					t.Fatalf("continue/%s = %+v", status, consequence)
				}
			case Supersede:
				if status != StatusIntegrated && (!consequence.RunMayContinue || consequence.HandoffEligible || consequence.IntegrationEligible) {
					t.Fatalf("supersede/%s = %+v", status, consequence)
				}
			case Invalidate:
				if status == StatusActive && (!consequence.ExpireAuthority || !consequence.QuarantineWorkspace || consequence.RunMayContinue) {
					t.Fatalf("invalidate/active = %+v", consequence)
				}
				if status != StatusIntegrated && (consequence.HandoffEligible || consequence.IntegrationEligible) {
					t.Fatalf("invalidate/%s remains eligible: %+v", status, consequence)
				}
			}
		}
	}
}

func TestForkRequiresDistinctWorkIDsAndExplicitEdges(t *testing.T) {
	t.Parallel()
	oldRevision := finalizedSingle(t, "revision-1", 1, "work-old")
	newRevision := finalizedMany(t, "revision-2", 2, []ids.WorkID{"work-left", "work-right"})
	request := ReconcileRequest{
		ID: "reconciliation-fork", Old: oldRevision, New: newRevision, Disposition: Fork,
		ActorID: "actor-owner", Rationale: "split valid lines", OldWorkIDs: []ids.WorkID{"work-old"},
		NewWorkIDs: []ids.WorkID{"work-left", "work-right"}, OccurredAt: time.Unix(1, 0),
	}
	if _, err := Reconcile(request); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("fork without edges error = %v", err)
	}
	request.ForkEdges = []ForkEdge{{FromOldWorkID: "work-old", ToNewWorkID: "work-left"}, {FromOldWorkID: "work-old", ToNewWorkID: "work-right"}}
	reconciliation, err := Reconcile(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciliation.ForkEdges()) != 2 {
		t.Fatalf("fork edges = %+v", reconciliation.ForkEdges())
	}
	if consequence, ok := reconciliation.Consequence("work-old", StatusActive); !ok || !consequence.RunMayContinue || !consequence.HandoffEligible {
		t.Fatalf("fork consequence = %+v, %v", consequence, ok)
	}

	request.NewWorkIDs = []ids.WorkID{"work-old", "work-left"}
	request.ForkEdges = []ForkEdge{{FromOldWorkID: "work-old", ToNewWorkID: "work-old"}, {FromOldWorkID: "work-old", ToNewWorkID: "work-left"}}
	if _, err := Reconcile(request); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("fork reusing old WorkID error = %v", err)
	}
}

func finalizedSingle(t *testing.T, id ids.WorkRevisionID, number int64, workID ids.WorkID) Revision {
	t.Helper()
	return finalizedMany(t, id, number, []ids.WorkID{workID})
}

func finalizedMany(t *testing.T, id ids.WorkRevisionID, number int64, workIDs []ids.WorkID) Revision {
	t.Helper()
	draft := NewDraft("project-1", "mission-1")
	must(t, draft.AddLane(Lane{ID: "lane-a"}))
	for _, workID := range workIDs {
		must(t, draft.AddWork(WorkItem{ID: workID, Title: workID.String(), LaneID: "lane-a", Criteria: []Criterion{{ID: "criterion-" + workID.String(), Text: "done"}}}))
	}
	revision, err := draft.Finalize(testMetadata(id, number))
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
