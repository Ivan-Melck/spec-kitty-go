package workgraph_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

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

func TestValidationEmitsClosedTypedFR010FindingsForRawProviderMutants(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*workgraph.WorkObservationParams)
		kind   workgraph.FindingKind
		field  string
	}{
		{"missing objective", func(p *workgraph.WorkObservationParams) { p.Title = "" }, workgraph.FindingMissingContext, "objective"},
		{"missing description", func(p *workgraph.WorkObservationParams) { p.Description = "" }, workgraph.FindingMissingContext, "description"},
		{"missing design context", func(p *workgraph.WorkObservationParams) { p.DesignContext = "" }, workgraph.FindingMissingContext, "design_context"},
		{"missing criterion id", func(p *workgraph.WorkObservationParams) { p.Criteria[0].ID = "" }, workgraph.FindingMalformedCriterion, "criteria.id"},
		{"missing criterion text", func(p *workgraph.WorkObservationParams) { p.Criteria[0].Text = "" }, workgraph.FindingMalformedCriterion, "criteria.text"},
		{"missing criterion outcome", func(p *workgraph.WorkObservationParams) { p.Criteria[0].Outcome = "" }, workgraph.FindingMalformedCriterion, "criteria.outcome"},
		{"invalid criterion verifier", func(p *workgraph.WorkObservationParams) { p.Criteria[0].Verifier = "shell_transcript" }, workgraph.FindingMalformedCriterion, "criteria.verifier"},
		{"missing required meaning", func(p *workgraph.WorkObservationParams) { p.Criteria[0].Requirement = "" }, workgraph.FindingMalformedCriterion, "criteria.requirement"},
		{"duplicate criterion", func(p *workgraph.WorkObservationParams) { p.Criteria = append(p.Criteria, p.Criteria[0]) }, workgraph.FindingMalformedCriterion, "criteria.id"},
		{"absent write scope", func(p *workgraph.WorkObservationParams) { p.WriteScopes = nil }, workgraph.FindingInvalidBoundary, "write_scopes"},
		{"invalid write scope", func(p *workgraph.WorkObservationParams) { p.WriteScopes[0].Path = "../outside" }, workgraph.FindingInvalidBoundary, "write_scopes.path"},
		{"absent cross-domain reason", func(p *workgraph.WorkObservationParams) { addSecondOwnershipDomain(p) }, workgraph.FindingUnrelatedOwnership, "cross_domain_reason"},
		{"whitespace cross-domain reason", func(p *workgraph.WorkObservationParams) { addSecondOwnershipDomain(p); p.CrossDomainReason = "   " }, workgraph.FindingUnrelatedOwnership, "cross_domain_reason"},
		{"oversized cross-domain reason", func(p *workgraph.WorkObservationParams) {
			addSecondOwnershipDomain(p)
			p.CrossDomainReason = strings.Repeat("x", 513)
		}, workgraph.FindingUnrelatedOwnership, "cross_domain_reason"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := validRawObservationParams()
			test.mutate(&params)
			item, err := workgraph.NewWorkObservation(params)
			if err != nil {
				t.Fatalf("raw provider observation was rejected before typed validation: %v", err)
			}
			draft := rawDraft(t, item)
			findings := workgraph.ValidateDraft(draft, workgraph.DefaultQualityPolicy())
			if !hasExactFinding(findings, test.kind, test.field) {
				t.Fatalf("findings=%+v want kind=%s field=%s", findings, test.kind, test.field)
			}
			if test.kind == workgraph.FindingUnrelatedOwnership {
				for _, finding := range findings {
					if finding.Kind == test.kind && (len(finding.Message) > 256 || (params.CrossDomainReason != "" && strings.Contains(finding.Message, params.CrossDomainReason))) {
						t.Fatalf("cross-domain finding leaked untrusted reason: %+v", finding)
					}
				}
			}
		})
	}
}

func TestValidationAcceptsBoundedCrossDomainReasonControls(t *testing.T) {
	for _, reason := range []string{"Coordinates the two ownership domains.", strings.Repeat("x", 512)} {
		params := validRawObservationParams()
		addSecondOwnershipDomain(&params)
		params.CrossDomainReason = reason
		item, err := workgraph.NewWorkObservation(params)
		if err != nil {
			t.Fatal(err)
		}
		findings := workgraph.ValidateDraft(rawDraft(t, item), workgraph.DefaultQualityPolicy())
		if hasFinding(findings, workgraph.FindingUnrelatedOwnership) {
			t.Fatalf("valid %d-byte cross-domain reason rejected: %+v", len(reason), findings)
		}
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

func TestLaneDiagnosticsAreCanonicalAndBoundedAcrossPermutations(t *testing.T) {
	draft := validDraft(t, false)
	left := workgraph.LaneProjection{
		Lanes: []workgraph.ProjectedLane{
			{ID: "lane-a", Members: []ids.WorkID{"work-feature", "work-foundation"}},
			{ID: "lane-b", Members: []ids.WorkID{"work-feature"}},
		},
		Edges: []workgraph.ProjectedLaneEdge{{BlockingLaneID: "lane-a", BlockedLaneID: "lane-b"}},
	}
	right := workgraph.LaneProjection{
		Lanes: []workgraph.ProjectedLane{
			{ID: "lane-b", Members: []ids.WorkID{"work-feature"}},
			{ID: "lane-a", Members: []ids.WorkID{"work-foundation", "work-feature"}},
		},
		Edges: []workgraph.ProjectedLaneEdge{{BlockingLaneID: "lane-a", BlockedLaneID: "lane-b"}},
	}
	leftFindings := workgraph.ValidateLaneProjection(draft, left)
	rightFindings := workgraph.ValidateLaneProjection(draft, right)
	leftJSON, _ := json.Marshal(leftFindings)
	rightJSON, _ := json.Marshal(rightFindings)
	if !reflect.DeepEqual(leftJSON, rightJSON) {
		t.Fatalf("equivalent projections changed diagnostics:\n%s\n%s", leftJSON, rightJSON)
	}
	if !hasFinding(leftFindings, workgraph.FindingDuplicateLaneMembership) {
		t.Fatalf("duplicate membership finding missing: %+v", leftFindings)
	}

	secret := "foreign-provider-output-" + strings.Repeat("x", 6000)
	malformed := workgraph.LaneProjection{
		Lanes: []workgraph.ProjectedLane{{ID: secret, Members: []ids.WorkID{"work-foundation", "work-feature"}}},
		Edges: []workgraph.ProjectedLaneEdge{{BlockingLaneID: secret, BlockedLaneID: "missing-lane"}},
	}
	findings := workgraph.ValidateLaneProjection(draft, malformed)
	encoded, _ := json.Marshal(findings)
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("untrusted lane/provider text leaked into findings")
	}
	for _, finding := range findings {
		if len(finding.Field) > 160 || len(finding.Message) > 256 {
			t.Fatalf("unbounded finding: %+v", finding)
		}
	}
}

func TestRawDependencyDiagnosticsDoNotEchoUnboundedProviderText(t *testing.T) {
	secret := "foreign-provider-output-" + strings.Repeat("x", 6000)
	params := validRawObservationParams()
	params.Dependencies = []ids.WorkID{ids.WorkID(secret)}
	item, err := workgraph.NewWorkObservation(params)
	if err != nil {
		t.Fatal(err)
	}
	findings := workgraph.ValidateDraft(rawDraft(t, item), workgraph.DefaultQualityPolicy())
	encoded, _ := json.Marshal(findings)
	if !hasFinding(findings, workgraph.FindingUnknownDependency) || strings.Contains(string(encoded), secret) {
		t.Fatalf("dependency findings are missing or leaked provider text: %s", encoded)
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
	items := []workgraph.WorkObservation{observed(t, f.foundation), observed(t, f.feature)}
	if reversed {
		items[0], items[1] = items[1], items[0]
	}
	approved := []workgraph.ApprovedScope{{ID: "plan-foundation", Kind: workgraph.ScopePlanConcern, MissionID: "mission-kitty"}, {ID: "plan-feature", Kind: workgraph.ScopePlanConcern, MissionID: "mission-kitty"}, {ID: "requirement-portable", Kind: workgraph.ScopeRequiredOutcome, MissionID: "mission-kitty"}}
	draft, err := workgraph.NewDraftObservation("project-kitty", "mission-kitty", "plan-revision-1", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", approved, items)
	if err != nil {
		t.Fatal(err)
	}
	return draft
}
func observed(t *testing.T, f definitionFixture) workgraph.WorkObservation {
	t.Helper()
	criteria := make([]workgraph.CriterionObservation, len(f.Criteria))
	for index, criterion := range f.Criteria {
		requirement := workgraph.CriterionOptional
		if criterion.Required {
			requirement = workgraph.CriterionRequired
		}
		criteria[index] = workgraph.CriterionObservation{ID: criterion.ID, Text: criterion.Text, Outcome: criterion.Outcome, Verifier: criterion.Verifier.String(), Requirement: requirement}
	}
	scopes := make([]workgraph.WriteScopeObservation, len(f.WriteScopes))
	for index, scope := range f.WriteScopes {
		scopes[index] = workgraph.WriteScopeObservation{Path: scope.Path, Domain: scope.Domain, Access: workgraph.ScopeAccess(scope.Access), SerializationKey: scope.SerializationKey}
	}
	provenance := make([]workgraph.ScopeReference, len(f.Provenance))
	for index, reference := range f.Provenance {
		provenance[index] = workgraph.ScopeReference{ID: reference.ID, Kind: workgraph.ScopeKind(reference.Kind)}
	}
	result, err := workgraph.NewWorkObservation(workgraph.WorkObservationParams{ProjectID: "project-kitty", WorkID: f.WorkID, Title: f.Title, Description: f.Description, DesignContext: f.DesignContext, Criteria: criteria, Dependencies: f.Dependencies, WriteScopes: scopes, Provenance: provenance})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func validRawObservationParams() workgraph.WorkObservationParams {
	return workgraph.WorkObservationParams{
		ProjectID: "project-kitty", WorkID: "work-raw", Title: "Deliver raw work", Description: "Deliver an independently understandable outcome.", DesignContext: "Provider data is validated before finalization.",
		Criteria:    []workgraph.CriterionObservation{{ID: "criterion-result", Text: "Verify the result", Outcome: "the result is observable", Verifier: "test", Requirement: workgraph.CriterionRequired}},
		WriteScopes: []workgraph.WriteScopeObservation{{Path: "internal/raw", Domain: "raw", Access: workgraph.ScopeWriteExclusive}},
		Provenance:  []workgraph.ScopeReference{{ID: "plan-raw", Kind: workgraph.ScopePlanConcern}},
	}
}

func addSecondOwnershipDomain(params *workgraph.WorkObservationParams) {
	params.WriteScopes = append(params.WriteScopes, workgraph.WriteScopeObservation{Path: "internal/second", Domain: "second", Access: workgraph.ScopeWriteExclusive})
}

func rawDraft(t *testing.T, item workgraph.WorkObservation) workgraph.DraftObservation {
	t.Helper()
	draft, err := workgraph.NewDraftObservation("project-kitty", "mission-kitty", "plan-revision-1", strings.Repeat("a", 64), []workgraph.ApprovedScope{{ID: "plan-raw", Kind: workgraph.ScopePlanConcern, MissionID: "mission-kitty"}}, []workgraph.WorkObservation{item})
	if err != nil {
		t.Fatal(err)
	}
	return draft
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

func hasExactFinding(findings []workgraph.Finding, kind workgraph.FindingKind, field string) bool {
	for _, finding := range findings {
		if finding.Kind == kind && finding.Field == field {
			return true
		}
	}
	return false
}
