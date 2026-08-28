package workgraph

import (
	"math/rand"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestCollapseLanesIsTransitiveDeterministicAndKeepsIndependentWork(t *testing.T) {
	base := laneObservation(t, []laneWork{
		{id: "work-a", path: "internal/a"},
		{id: "work-b", path: "internal/a/sub"},
		{id: "work-c", path: "internal/a/sub/deep"},
		{id: "work-d", path: "internal/d", dependencies: []ids.WorkID{"work-a"}},
	})
	want, err := CollapseLanes(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 2 {
		t.Fatalf("lanes = %d, want 2", len(want))
	}
	var collapsed, singleton LaneV2
	for _, lane := range want {
		if len(lane.WorkIDs()) == 3 {
			collapsed = lane
		} else {
			singleton = lane
		}
	}
	if len(collapsed.WorkIDs()) != 3 || len(singleton.WorkIDs()) != 1 || len(singleton.DependencyLaneIDs()) != 1 || singleton.DependencyLaneIDs()[0] != collapsed.ID() {
		t.Fatalf("unexpected projection: %#v", want)
	}

	for seed := int64(0); seed < 100; seed++ {
		items := []laneWork{{id: "work-a", path: "internal/a"}, {id: "work-b", path: "internal/a/sub"}, {id: "work-c", path: "internal/a/sub/deep"}, {id: "work-d", path: "internal/d", dependencies: []ids.WorkID{"work-a"}}}
		rand.New(rand.NewSource(seed)).Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
		got, collapseErr := CollapseLanes(laneObservation(t, items))
		if collapseErr != nil {
			t.Fatalf("seed %d: %v", seed, collapseErr)
		}
		if !equalLanesV2(got, want) {
			t.Fatalf("seed %d changed lanes: %#v / %#v", seed, got, want)
		}
	}
}

func TestCollapseLanesUsesSerializationIdentityAndRejectsCollapseOnlyCycle(t *testing.T) {
	observation := laneObservation(t, []laneWork{
		{id: "work-a", path: "a", serialization: "pair-1", dependencies: []ids.WorkID{"work-b"}},
		{id: "work-b", path: "b", serialization: "pair-2"},
		{id: "work-c", path: "c", serialization: "pair-2", dependencies: []ids.WorkID{"work-d"}},
		{id: "work-d", path: "d", serialization: "pair-1"},
	})
	if _, err := CollapseLanes(observation); !IsKind(err, KindLaneCycle) {
		t.Fatalf("CollapseLanes error = %v, want %s", err, KindLaneCycle)
	} else {
		graphError := err.(*Error)
		if graphError.Fields["lane_id"] == "" || graphError.Fields["work_ids"] == "" || len(graphError.Fields["lane_id"]) > 128 || len(graphError.Fields["work_ids"]) > 128 {
			t.Fatalf("cycle evidence is not bounded and attributable: %#v", graphError.Fields)
		}
	}
}

func TestCollapseLanesFailsClosedOnInvalidRawScope(t *testing.T) {
	observation := laneObservation(t, []laneWork{{id: "work-a", path: "../outside"}})
	if _, err := CollapseLanes(observation); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("invalid scope error = %v, want %s", err, KindInvalidRevision)
	}
}

func TestCollapseLanesRejectsMissingDependencyAndWorkCycleDistinctly(t *testing.T) {
	missing := laneObservation(t, []laneWork{{id: "work-a", path: "a", dependencies: []ids.WorkID{"work-missing"}}})
	if _, err := CollapseLanes(missing); !IsKind(err, KindMissingNode) {
		t.Fatalf("missing dependency error = %v", err)
	}
	cycle := laneObservation(t, []laneWork{{id: "work-a", path: "a", dependencies: []ids.WorkID{"work-b"}}, {id: "work-b", path: "b", dependencies: []ids.WorkID{"work-a"}}})
	if _, err := CollapseLanes(cycle); !IsKind(err, KindCycle) {
		t.Fatalf("work cycle error = %v", err)
	}
}

type laneWork struct {
	id            ids.WorkID
	path          string
	serialization string
	dependencies  []ids.WorkID
}

func laneObservation(t *testing.T, definitions []laneWork) DraftObservation {
	t.Helper()
	work := make([]WorkObservation, 0, len(definitions))
	approved := make([]ApprovedScope, 0, len(definitions))
	for _, definition := range definitions {
		scopeID := "scope-" + definition.id.String()
		item, err := NewWorkObservation(WorkObservationParams{
			ProjectID: "project-lanes", WorkID: definition.id, Title: "Implement " + definition.id.String(),
			Description: "Complete the bounded work item.", DesignContext: "Use the approved design.",
			Criteria:     []CriterionObservation{{ID: "criterion-" + definition.id.String(), Text: "Run the test", Outcome: "The test passes", Verifier: "test", Requirement: CriterionRequired}},
			Dependencies: definition.dependencies,
			WriteScopes:  []WriteScopeObservation{{Path: definition.path, Domain: "domain", Access: ScopeWriteExclusive, SerializationKey: definition.serialization}},
			Provenance:   []ScopeReference{{ID: scopeID, Kind: ScopePlanConcern}},
		})
		if err != nil {
			t.Fatal(err)
		}
		work = append(work, item)
		approved = append(approved, ApprovedScope{ID: scopeID, Kind: ScopePlanConcern, MissionID: "mission-lanes"})
	}
	observation, err := NewDraftObservation("project-lanes", "mission-lanes", "plan-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", approved, work)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func equalLanesV2(left, right []LaneV2) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID() != right[i].ID() || !equalWorkIDs(left[i].WorkIDs(), right[i].WorkIDs()) || !equalLaneIDs(left[i].DependencyLaneIDs(), right[i].DependencyLaneIDs()) {
			return false
		}
	}
	return true
}

func equalWorkIDs(left, right []ids.WorkID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
func equalLaneIDs(left, right []ids.LaneID) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
