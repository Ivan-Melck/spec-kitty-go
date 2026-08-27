package workgraph_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
)

func TestValidationAcceptsCompleteTwoItemObservationDeterministically(t *testing.T) {
	valid := validDraft(t, false)
	if findings := workgraph.ValidateDraft(valid, workgraph.DefaultQualityPolicy()); len(findings) != 0 {
		t.Fatalf("valid findings = %+v", findings)
	}
	reordered := validDraft(t, true)
	if findings := workgraph.ValidateDraft(reordered, workgraph.DefaultQualityPolicy()); len(findings) != 0 {
		t.Fatalf("reordered findings = %+v", findings)
	}
	projection := workgraph.LaneProjection{Lanes: []workgraph.ProjectedLane{{ID: "lane-foundation", Members: []ids.WorkID{"work-foundation"}}, {ID: "lane-feature", Members: []ids.WorkID{"work-feature"}}}, Edges: []workgraph.ProjectedLaneEdge{{BlockingLaneID: "lane-foundation", BlockedLaneID: "lane-feature"}}}
	if findings := workgraph.ValidateLaneProjection(valid, projection); len(findings) != 0 {
		t.Fatalf("valid lane findings = %+v", findings)
	}
}

func TestValidationQualityCoverageOwnershipAndDependencyMutants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*workFixture)
		want   workgraph.FindingKind
	}{
		{"criterion activity only", func(f *workFixture) { f.feature.Criteria[0].Outcome = f.feature.Criteria[0].Text }, workgraph.FindingCriterionNotObservable},
		{"missing coverage", func(f *workFixture) {
			f.feature.Provenance = withoutScope(f.feature.Provenance, "requirement-portable")
		}, workgraph.FindingMissingCoverage},
		{"orphan work", func(f *workFixture) { f.feature.Provenance = withoutScope(f.feature.Provenance, "plan-feature") }, workgraph.FindingOrphanWork},
		{"unknown scope", func(f *workFixture) {
			f.feature.Provenance = append(f.feature.Provenance, tracker.ScopeRef{ID: "unknown-scope", Kind: tracker.ScopePlanConcern})
		}, workgraph.FindingUnknownScope},
		{"ownership conflict", func(f *workFixture) {
			f.feature.WriteScopes = []tracker.WriteScope{{Path: "internal/shared", Domain: "feature", Access: tracker.ScopeWriteExclusive}}
			f.foundation.WriteScopes = []tracker.WriteScope{{Path: "internal/shared", Domain: "foundation", Access: tracker.ScopeWriteExclusive}}
		}, workgraph.FindingOwnershipConflict},
		{"unknown dependency", func(f *workFixture) { f.feature.Dependencies = []ids.WorkID{"work-absent"} }, workgraph.FindingUnknownDependency},
		{"dependency cycle", func(f *workFixture) { f.foundation.Dependencies = []ids.WorkID{"work-feature"} }, workgraph.FindingDependencyCycle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := baseFixture()
			test.mutate(&fixture)
			draft := fixture.draft(t, false)
			findings := workgraph.ValidateDraft(draft, workgraph.DefaultQualityPolicy())
			if !hasFinding(findings, test.want) {
				t.Fatalf("findings=%+v want=%s", findings, test.want)
			}
		})
	}
}

func TestPostLaneValidationRejectsMembershipConstraintAndCycleMutants(t *testing.T) {
	draft := validDraft(t, false)
	tests := []struct {
		name       string
		projection workgraph.LaneProjection
		want       workgraph.FindingKind
	}{
		{"missing membership", workgraph.LaneProjection{Lanes: []workgraph.ProjectedLane{{ID: "lane-a", Members: []ids.WorkID{"work-foundation"}}}}, workgraph.FindingMissingLaneMembership},
		{"duplicate membership", workgraph.LaneProjection{Lanes: []workgraph.ProjectedLane{{ID: "lane-a", Members: []ids.WorkID{"work-foundation", "work-feature"}}, {ID: "lane-b", Members: []ids.WorkID{"work-feature"}}}}, workgraph.FindingDuplicateLaneMembership},
		{"lost constraint", workgraph.LaneProjection{Lanes: []workgraph.ProjectedLane{{ID: "lane-a", Members: []ids.WorkID{"work-foundation"}}, {ID: "lane-b", Members: []ids.WorkID{"work-feature"}}}}, workgraph.FindingOrderingConstraintLost},
		{"self edge", workgraph.LaneProjection{Lanes: []workgraph.ProjectedLane{{ID: "lane-a", Members: []ids.WorkID{"work-foundation", "work-feature"}}}, Edges: []workgraph.ProjectedLaneEdge{{BlockingLaneID: "lane-a", BlockedLaneID: "lane-a"}}}, workgraph.FindingLaneSelfEdge},
		{"collapse cycle", workgraph.LaneProjection{Lanes: []workgraph.ProjectedLane{{ID: "lane-a", Members: []ids.WorkID{"work-foundation"}}, {ID: "lane-b", Members: []ids.WorkID{"work-feature"}}}, Edges: []workgraph.ProjectedLaneEdge{{BlockingLaneID: "lane-a", BlockedLaneID: "lane-b"}, {BlockingLaneID: "lane-b", BlockedLaneID: "lane-a"}}}, workgraph.FindingLaneCycle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := workgraph.ValidateLaneProjection(draft, test.projection)
			if !hasFinding(findings, test.want) {
				t.Fatalf("findings=%+v want=%s", findings, test.want)
			}
		})
	}
}

func TestDependencyGraphRawMutantsReturnExactKindsAndIdentities(t *testing.T) {
	nodes := []ids.WorkID{"work-a", "work-b", "work-c"}
	tests := []struct {
		name          string
		edges         []workgraph.DependencyEdge
		want          workgraph.FindingKind
		work, related ids.WorkID
	}{
		{"unknown endpoint", []workgraph.DependencyEdge{{BlockedWorkID: "work-b", BlockingWorkID: "work-unknown"}}, workgraph.FindingUnknownDependency, "work-b", "work-unknown"},
		{"self edge", []workgraph.DependencyEdge{{BlockedWorkID: "work-a", BlockingWorkID: "work-a"}}, workgraph.FindingSelfDependency, "work-a", "work-a"},
		{"duplicate edge", []workgraph.DependencyEdge{{BlockedWorkID: "work-b", BlockingWorkID: "work-a"}, {BlockedWorkID: "work-b", BlockingWorkID: "work-a"}}, workgraph.FindingDuplicateDependency, "work-b", "work-a"},
		{"direct cycle", []workgraph.DependencyEdge{{BlockedWorkID: "work-b", BlockingWorkID: "work-a"}, {BlockedWorkID: "work-a", BlockingWorkID: "work-b"}}, workgraph.FindingDependencyCycle, "work-a", ""},
		{"long cycle", []workgraph.DependencyEdge{{BlockedWorkID: "work-b", BlockingWorkID: "work-a"}, {BlockedWorkID: "work-c", BlockingWorkID: "work-b"}, {BlockedWorkID: "work-a", BlockingWorkID: "work-c"}}, workgraph.FindingDependencyCycle, "work-a", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findings := workgraph.ValidateDependencyGraph(nodes, test.edges)
			matched := false
			for _, finding := range findings {
				if finding.Kind == test.want {
					matched = true
					if finding.WorkID != test.work || finding.RelatedWorkID != test.related {
						t.Fatalf("finding identities=%+v", finding)
					}
				}
			}
			if !matched {
				t.Fatalf("findings=%+v want=%s", findings, test.want)
			}
		})
	}
	if findings := workgraph.ValidateDependencyGraph(nodes, []workgraph.DependencyEdge{{BlockedWorkID: "work-b", BlockingWorkID: "work-a"}}); len(findings) != 0 {
		t.Fatalf("disconnected acyclic graph findings=%+v", findings)
	}
}

func TestValidationFindingOrderIsProviderOrderIndependent(t *testing.T) {
	one := baseFixture()
	one.feature.Dependencies = []ids.WorkID{"work-absent"}
	one.feature.Provenance = append(one.feature.Provenance, tracker.ScopeRef{ID: "unknown", Kind: tracker.ScopePlanConcern})
	two := one
	left := workgraph.ValidateDraft(one.draft(t, false), workgraph.DefaultQualityPolicy())
	right := workgraph.ValidateDraft(two.draft(t, true), workgraph.DefaultQualityPolicy())
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("finding order changed:\n%+v\n%+v", left, right)
	}
}

type definitionFixture struct {
	WorkID                            ids.WorkID
	Title, Description, DesignContext string
	Criteria                          []tracker.WorkCriterion
	Dependencies                      []ids.WorkID
	WriteScopes                       []tracker.WriteScope
	Provenance                        []tracker.ScopeRef
}
type workFixture struct{ foundation, feature definitionFixture }

func baseFixture() workFixture {
	return workFixture{
		foundation: definitionFixture{WorkID: "work-foundation", Title: "Build foundation", Description: "Build the provider-neutral foundation.", DesignContext: "Stable identity remains authoritative.", Criteria: []tracker.WorkCriterion{{ID: "criterion-foundation", Text: "Test foundation", Outcome: "foundation persists", Verifier: tracker.VerifierTest, Required: true}}, WriteScopes: []tracker.WriteScope{{Path: "internal/foundation", Domain: "foundation", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-foundation", Kind: tracker.ScopePlanConcern}}},
		feature:    definitionFixture{WorkID: "work-feature", Title: "Build feature", Description: "Build the consumer feature.", DesignContext: "Consume only provider-neutral observations.", Criteria: []tracker.WorkCriterion{{ID: "criterion-feature", Text: "Test feature", Outcome: "feature consumes the observation", Verifier: tracker.VerifierTest, Required: true}}, Dependencies: []ids.WorkID{"work-foundation"}, WriteScopes: []tracker.WriteScope{{Path: "internal/feature", Domain: "feature", Access: tracker.ScopeWriteExclusive}}, Provenance: []tracker.ScopeRef{{ID: "plan-feature", Kind: tracker.ScopePlanConcern}, {ID: "requirement-portable", Kind: tracker.ScopeRequiredOutcome}}},
	}
}
func validDraft(t *testing.T, reversed bool) workgraph.DraftObservation {
	f := baseFixture()
	return f.draft(t, reversed)
}
func (f workFixture) draft(t *testing.T, reversed bool) workgraph.DraftObservation {
	t.Helper()
	items := []tracker.WorkDefinitionObservation{observed(t, f.foundation, 1), observed(t, f.feature, 2)}
	if reversed {
		items[0], items[1] = items[1], items[0]
	}
	approved := []workgraph.ApprovedScope{{ID: "plan-foundation", Kind: tracker.ScopePlanConcern, MissionID: "mission-kitty"}, {ID: "plan-feature", Kind: tracker.ScopePlanConcern, MissionID: "mission-kitty"}, {ID: "requirement-portable", Kind: tracker.ScopeRequiredOutcome, MissionID: "mission-kitty"}}
	draft, err := workgraph.NewDraftObservation("project-kitty", "mission-kitty", "plan-revision-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", approved, items)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}
func observed(t *testing.T, f definitionFixture, revision tracker.Revision) tracker.WorkDefinitionObservation {
	t.Helper()
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: "project-kitty", WorkID: f.WorkID, Title: f.Title, Description: f.Description, DesignContext: f.DesignContext, Criteria: f.Criteria, Dependencies: f.Dependencies, WriteScopes: f.WriteScopes, Provenance: f.Provenance, MetadataSchema: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	external := "external-" + f.WorkID.String()
	provider, err := tracker.NewProviderObservation(external, definition.CanonicalData(), revision, "", "token", now)
	if err != nil {
		t.Fatal(err)
	}
	reference := tracker.ExternalWorkRef{ProjectID: "project-kitty", WorkID: f.WorkID, Provider: "fake", ExternalID: external, MetadataSchema: 1, CreatedByOperation: ids.OperationID("operation-" + f.WorkID.String()), LastObservation: revision}
	result, err := tracker.NewWorkDefinitionObservation(definition, reference, provider, now)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func withoutScope(values []tracker.ScopeRef, id string) []tracker.ScopeRef {
	result := []tracker.ScopeRef{}
	for _, value := range values {
		if value.ID != id {
			result = append(result, value)
		}
	}
	return result
}
func hasFinding(findings []workgraph.Finding, kind workgraph.FindingKind) bool {
	for _, finding := range findings {
		if finding.Kind == kind {
			return true
		}
	}
	return false
}
