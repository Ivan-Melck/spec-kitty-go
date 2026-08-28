package readmodel

import (
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func fixtureParams() SnapshotParams {
	return SnapshotParams{MissionID: "mission-1", RevisionID: "revision-1", Generation: 2, GraphDigest: strings.Repeat("a", 64), DriftStatus: DriftNotChecked,
		WorkItems: []WorkItemParams{
			{WorkID: "WP02", Title: "second", Description: "desc", Dependencies: []ids.WorkID{"WP01"}, Criteria: []CriterionParams{{ID: "AC-2", Text: "two", VerifierKind: "test", Required: true, Metadata: map[string]any{"z": 2, "a": "x"}}}, WriteScopes: []ScopeParams{{Path: "internal/b", Domain: "code", Access: "write_exclusive"}}},
			{WorkID: "WP01", Title: "first", DesignContext: "context", Criteria: []CriterionParams{{ID: "AC-1", Text: "one", VerifierKind: "review", Required: false}}, WriteScopes: []ScopeParams{{Path: "internal/a", Domain: "code", Access: "read_only"}}},
		},
		Lanes:           []LaneParams{{LaneID: "lane-b", WorkIDs: []ids.WorkID{"WP02"}, DependencyLaneIDs: []ids.LaneID{"lane-a"}}, {LaneID: "lane-a", WorkIDs: []ids.WorkID{"WP01"}}},
		Reconciliations: []ReconciliationParams{{PreviousWorkID: "old-1", Disposition: ReconciliationSupersede, CurrentWorkIDs: []ids.WorkID{"WP02", "WP01"}, Rationale: "split"}},
	}
}

func TestSnapshotCanonicalAndDefensive(t *testing.T) {
	params := fixtureParams()
	snapshot, err := NewSnapshot(params)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.WorkItems()[0].WorkID() != "WP01" || snapshot.Lanes()[0].LaneID() != "lane-a" || snapshot.Reconciliations()[0].CurrentWorkIDs()[0] != "WP01" {
		t.Fatal("values are not canonical")
	}
	params.WorkItems[0].Title = "mutated"
	params.WorkItems[0].Criteria[0].Metadata["a"] = "mutated"
	params.Lanes[0].WorkIDs[0] = "WP01"
	work := snapshot.WorkItems()
	work[0].dependencies = append(work[0].dependencies, "WP02")
	work[1].criteria[0].metadataJSON[0] = 'x'
	if snapshot.WorkItems()[0].Title() != "first" || string(snapshot.WorkItems()[1].Criteria()[0].MetadataJSON()) != "{\"a\":\"x\",\"z\":2}" || len(snapshot.WorkItems()[0].Dependencies()) != 0 {
		t.Fatal("caller mutation escaped")
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDriftStatusClosedAndNotCheckedDistinct(t *testing.T) {
	for _, value := range []DriftStatus{DriftNotChecked, DriftClean, DriftObservationOnly, DriftChanged} {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := DriftStatus("unknown").Validate(); err == nil {
		t.Fatal("unknown drift accepted")
	}
	if DriftNotChecked == DriftClean {
		t.Fatal("absence was inferred clean")
	}
}

func TestSnapshotRejectsBrokenTopology(t *testing.T) {
	for name, mutate := range map[string]func(*SnapshotParams){
		"digest":               func(v *SnapshotParams) { v.GraphDigest = "ABC" },
		"drift":                func(v *SnapshotParams) { v.DriftStatus = "wrong" },
		"duplicate work":       func(v *SnapshotParams) { v.WorkItems = append(v.WorkItems, v.WorkItems[0]) },
		"missing dependency":   func(v *SnapshotParams) { v.WorkItems[0].Dependencies = []ids.WorkID{"missing"} },
		"duplicate membership": func(v *SnapshotParams) { v.Lanes[0].WorkIDs = append(v.Lanes[0].WorkIDs, "WP01") },
		"missing membership":   func(v *SnapshotParams) { v.Lanes[1].WorkIDs = nil },
		"missing lane edge":    func(v *SnapshotParams) { v.Lanes[0].DependencyLaneIDs = nil },
		"lane cycle":           func(v *SnapshotParams) { v.Lanes[1].DependencyLaneIDs = []ids.LaneID{"lane-b"} },
	} {
		t.Run(name, func(t *testing.T) {
			v := fixtureParams()
			mutate(&v)
			if _, err := NewSnapshot(v); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
}
