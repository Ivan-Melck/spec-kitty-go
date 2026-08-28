package workgraph

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

var driftObservedAt = time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)

func TestDriftSeparatesCanonicalSemanticsFromProviderNoise(t *testing.T) {
	base := driftRevision(t, nil)
	clean := driftRevision(t, nil)
	report, err := DetectDrift(base, clean, driftObservedAt)
	if err != nil || report.SemanticStatus() != SemanticClean || len(report.Changes()) != 0 || len(report.AffectedOldWorkIDs()) != 0 {
		t.Fatalf("clean report=%#v err=%v", report, err)
	}

	noise := driftRevision(t, func(params *RevisionV2Params) {
		params.ProviderObservations[0].Revision = "provider-revision-noise"
		params.ProviderObservations[0], params.ProviderObservations[1] = params.ProviderObservations[1], params.ProviderObservations[0]
	})
	noiseReport, err := DetectDrift(base, noise, driftObservedAt)
	if err != nil || noiseReport.SemanticStatus() != SemanticObservationOnly || len(noiseReport.Changes()) != 0 || len(noiseReport.ProviderRevisions()) != 2 {
		t.Fatalf("noise report=%#v err=%v", noiseReport, err)
	}
	if noiseReport.ProviderRevisions()[0].OldRevision == noiseReport.ProviderRevisions()[0].NewRevision {
		t.Fatal("provider revision audit evidence was lost")
	}

	equalTimestampChange := driftRevision(t, func(params *RevisionV2Params) { params.WorkItems[0].Title = "Changed at the same observation instant" })
	changed, err := DetectDrift(base, equalTimestampChange, driftObservedAt)
	if err != nil || changed.SemanticStatus() != SemanticChanged || !reportHasField(changed, FieldTitle) {
		t.Fatalf("equal-timestamp semantic change was missed: %#v err=%v", changed, err)
	}
}

func TestDriftCoversEveryExecutableRevisionV2Field(t *testing.T) {
	tests := map[string]struct {
		field  SemanticField
		mutate func(*RevisionV2Params)
	}{
		"planning source":    {FieldPlanningSourceCommit, func(params *RevisionV2Params) { params.PlanningSourceCommit = strings.Repeat("c", 40) }},
		"mission definition": {FieldMissionDefinition, func(params *RevisionV2Params) { params.MissionDefinition.Digest = strings.Repeat("d", 64) }},
		"title":              {FieldTitle, func(params *RevisionV2Params) { params.WorkItems[0].Title = "Changed bounded title" }},
		"description":        {FieldDescription, func(params *RevisionV2Params) { params.WorkItems[0].Description = "Changed bounded description." }},
		"design context":     {FieldDesignContext, func(params *RevisionV2Params) { params.WorkItems[0].DesignContext = "Changed bounded design." }},
		"criterion identity": {FieldCriterionIdentity, addCriterion},
		"criterion text":     {FieldCriterionText, func(params *RevisionV2Params) { params.WorkItems[0].Criteria[0].Text = "Run another bounded test." }},
		"criterion verifier": {FieldCriterionVerifier, func(params *RevisionV2Params) { params.WorkItems[0].Criteria[0].VerifierKind = "review" }},
		"criterion required": {FieldCriterionRequired, func(params *RevisionV2Params) { params.WorkItems[0].Criteria[0].Required = false }},
		"criterion outcome": {FieldCriterionMetadata, func(params *RevisionV2Params) {
			params.WorkItems[0].Criteria[0].Outcome = "A different result is observed."
		}},
		"criterion evidence":      {FieldCriterionMetadata, func(params *RevisionV2Params) { params.WorkItems[0].Criteria[0].EvidenceHint = "different evidence" }},
		"criterion metadata":      {FieldCriterionMetadata, func(params *RevisionV2Params) { params.WorkItems[0].Criteria[0].Metadata["new"] = "value" }},
		"dependency":              {FieldDependencies, removeFixtureDependency},
		"scope path":              {FieldWriteScopes, func(params *RevisionV2Params) { params.WorkItems[0].WriteScopes[0].Path = "internal/a2" }},
		"scope domain":            {FieldWriteScopes, func(params *RevisionV2Params) { params.WorkItems[0].WriteScopes[0].Domain = "changed-domain" }},
		"scope access":            {FieldWriteScopes, func(params *RevisionV2Params) { params.WorkItems[0].WriteScopes[0].Access = ScopeReadOnly }},
		"scope serialization":     {FieldWriteScopes, func(params *RevisionV2Params) { params.WorkItems[0].WriteScopes[0].SerializationKey = "changed-serial" }},
		"provider":                {FieldProviderMapping, func(params *RevisionV2Params) { params.ProviderObservations[0].Provider = "other-provider" }},
		"external mapping":        {FieldProviderMapping, func(params *RevisionV2Params) { params.ProviderObservations[0].ExternalID = "other-external" }},
		"derived lane membership": {FieldLaneProjection, mergeFixtureLanes},
		"work addition":           {FieldWorkIdentity, addFixtureWork},
		"work removal":            {FieldWorkIdentity, removeFixtureWork},
	}
	base := driftRevision(t, nil)
	baseBytes, _ := base.Encode()
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := driftRevision(t, test.mutate)
			report, err := DetectDrift(base, candidate, driftObservedAt)
			if err != nil || report.SemanticStatus() != SemanticChanged || !reportHasField(report, test.field) {
				t.Fatalf("field=%s report=%#v err=%v", test.field, report, err)
			}
			assertCompleteReasons(t, report)
		})
	}
	after, _ := base.Encode()
	if string(baseBytes) != string(after) {
		t.Fatal("drift campaign mutated the immutable base revision")
	}

	invalidPolicy := revisionV2Fixture()
	invalidPolicy.DependencyPolicy = "future-policy"
	if _, err := NewRevisionV2(invalidPolicy); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("unsupported dependency policy reached drift comparison: %v", err)
	}
}

func TestDriftAffectedSetsIncludeDependencyAndLaneMeaningWithoutUnrelatedWork(t *testing.T) {
	base := driftRevision(t, addIndependentFixtureWork)
	candidate := driftRevision(t, func(params *RevisionV2Params) {
		addIndependentFixtureWork(params)
		insertFixtureDependency(params)
	})
	report, err := DetectDrift(base, candidate, driftObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.AffectedOldWorkIDs(), []ids.WorkID{"work-b"}) || !reflect.DeepEqual(report.AffectedNewWorkIDs(), []ids.WorkID{"work-b", "work-c"}) {
		t.Fatalf("dependency affected sets old=%v new=%v", report.AffectedOldWorkIDs(), report.AffectedNewWorkIDs())
	}
	if len(report.OldReasons("work-independent")) != 0 || len(report.NewReasons("work-independent")) != 0 || !reportHasField(report, FieldDependencyAncestry) {
		t.Fatalf("unrelated work was affected or ancestry reason missing: %#v", report)
	}
	assertCompleteReasons(t, report)

	merged := driftRevision(t, mergeFixtureLanes)
	laneReport, err := DetectDrift(driftRevision(t, nil), merged, driftObservedAt)
	if err != nil || !reflect.DeepEqual(laneReport.AffectedOldWorkIDs(), []ids.WorkID{"work-a", "work-b"}) || !reflect.DeepEqual(laneReport.AffectedNewWorkIDs(), []ids.WorkID{"work-a", "work-b"}) {
		t.Fatalf("lane affected sets=%v/%v err=%v", laneReport.AffectedOldWorkIDs(), laneReport.AffectedNewWorkIDs(), err)
	}
}

func TestDriftIsDeterministicBoundedAndDefensivelyCopied(t *testing.T) {
	base := driftRevision(t, nil)
	wantCandidate := driftRevision(t, func(params *RevisionV2Params) { params.WorkItems[0].Description = "seeded-private-description" })
	want, err := DetectDrift(base, wantCandidate, driftObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := want.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 100; seed++ {
		candidate := driftRevision(t, func(params *RevisionV2Params) {
			params.WorkItems[0].Description = "seeded-private-description"
			random := rand.New(rand.NewSource(seed))
			random.Shuffle(len(params.WorkItems), func(i, j int) { params.WorkItems[i], params.WorkItems[j] = params.WorkItems[j], params.WorkItems[i] })
			random.Shuffle(len(params.Lanes), func(i, j int) { params.Lanes[i], params.Lanes[j] = params.Lanes[j], params.Lanes[i] })
			random.Shuffle(len(params.ProviderObservations), func(i, j int) {
				params.ProviderObservations[i], params.ProviderObservations[j] = params.ProviderObservations[j], params.ProviderObservations[i]
			})
		})
		got, detectErr := DetectDrift(base, candidate, driftObservedAt)
		gotBytes, encodeErr := got.Encode()
		if detectErr != nil || encodeErr != nil || got.ID() != want.ID() || string(gotBytes) != string(wantBytes) || !sameChanges(got.Changes(), want.Changes()) {
			t.Fatalf("seed %d changed report: id=%s/%s err=%v", seed, got.ID(), want.ID(), detectErr)
		}
	}
	for _, change := range want.Changes() {
		if len(change.ID()) != 64 || len(change.OldCommitment()) != 64 || len(change.NewCommitment()) != 64 || strings.Contains(fmt.Sprintf("%#v", change), "seeded-private-description") {
			t.Fatalf("change evidence is unbounded or leaked private content: %#v", change)
		}
	}
	changes := want.Changes()
	oldIDs := want.AffectedOldWorkIDs()
	newIDs := want.AffectedNewWorkIDs()
	reasons := want.OldReasons("work-a")
	evidence := want.ProviderRevisions()
	changes[0].field = FieldSemanticDigest
	oldIDs[0] = "work-mutated"
	newIDs[0] = "work-mutated"
	reasons[0] = "mutated"
	evidence[0].NewRevision = "mutated"
	if want.Changes()[0].Field() == FieldSemanticDigest || want.AffectedOldWorkIDs()[0] == "work-mutated" || want.AffectedNewWorkIDs()[0] == "work-mutated" || want.OldReasons("work-a")[0] == "mutated" || want.ProviderRevisions()[0].NewRevision == "mutated" {
		t.Fatal("caller mutation changed immutable drift evidence")
	}
}

func TestDriftMutationCampaignDetectsOneThousandSemanticChangesAndProviderNoise(t *testing.T) {
	base := driftRevision(t, nil)
	baseBytes, _ := base.Encode()
	mutants := []func(*RevisionV2Params, int){
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].Title = fmt.Sprintf("Changed title %d", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].Description = fmt.Sprintf("Changed description %d.", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].DesignContext = fmt.Sprintf("Changed design %d.", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].Criteria[0].Text = fmt.Sprintf("Run changed test %d.", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].Criteria[0].Outcome = fmt.Sprintf("Changed outcome %d.", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].Criteria[0].EvidenceHint = fmt.Sprintf("changed evidence %d", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].Criteria[0].Metadata["campaign"] = index
		},
		func(params *RevisionV2Params, _ int) { params.WorkItems[0].Criteria[0].Required = false },
		func(params *RevisionV2Params, _ int) { params.WorkItems[0].Criteria[0].VerifierKind = "review" },
		func(params *RevisionV2Params, _ int) { removeFixtureDependency(params) },
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].WriteScopes[0].Path = fmt.Sprintf("internal/a%d", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].WriteScopes[0].Domain = fmt.Sprintf("domain-%d", index)
		},
		func(params *RevisionV2Params, index int) {
			params.WorkItems[0].WriteScopes[0].SerializationKey = fmt.Sprintf("serial-%d", index)
		},
		func(params *RevisionV2Params, _ int) { params.WorkItems[0].WriteScopes[0].Access = ScopeReadOnly },
		func(params *RevisionV2Params, index int) {
			params.ProviderObservations[0].ExternalID = fmt.Sprintf("external-%d", index)
		},
		func(params *RevisionV2Params, index int) {
			params.ProviderObservations[0].Provider = fmt.Sprintf("provider-%d", index)
		},
		func(params *RevisionV2Params, index int) { params.PlanningSourceCommit = fmt.Sprintf("%040x", index+1) },
		func(params *RevisionV2Params, index int) {
			params.MissionDefinition.Digest = fmt.Sprintf("%064x", index+1)
		},
		func(params *RevisionV2Params, _ int) { addCriterion(params) },
		func(params *RevisionV2Params, _ int) { mergeFixtureLanes(params) },
		func(params *RevisionV2Params, _ int) { addFixtureWork(params) },
		func(params *RevisionV2Params, _ int) { removeFixtureWork(params) },
	}
	for index := 0; index < 1000; index++ {
		mutant := mutants[index%len(mutants)]
		candidate := driftRevision(t, func(params *RevisionV2Params) { mutant(params, index+1) })
		report, err := DetectDrift(base, candidate, driftObservedAt)
		if err != nil || report.SemanticStatus() != SemanticChanged || len(report.Changes()) == 0 {
			t.Fatalf("mutation %d was silent: status=%s err=%v", index, report.SemanticStatus(), err)
		}
		noise := driftRevision(t, func(params *RevisionV2Params) {
			params.ProviderObservations[0].Revision = fmt.Sprintf("noise-%d", index)
		})
		noiseReport, noiseErr := DetectDrift(base, noise, driftObservedAt)
		if noiseErr != nil || noiseReport.SemanticStatus() != SemanticObservationOnly {
			t.Fatalf("provider noise %d became authority: status=%s err=%v", index, noiseReport.SemanticStatus(), noiseErr)
		}
	}
	after, _ := base.Encode()
	if string(after) != string(baseBytes) {
		t.Fatal("1,000-mutation campaign mutated the base revision")
	}
}

func TestDriftRejectsWrongMissionAndMalformedObservationBoundary(t *testing.T) {
	base := driftRevision(t, nil)
	wrong := driftRevision(t, func(params *RevisionV2Params) { params.MissionID = "mission-other" })
	if _, err := DetectDrift(base, wrong, driftObservedAt); !IsKind(err, KindInvalidRevision) || strings.Contains(err.Error(), "provider-revision") {
		t.Fatalf("wrong mission error=%v", err)
	}
	if _, err := DetectDrift(base, driftRevision(t, nil), time.Time{}); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("zero observation time error=%v", err)
	}
	malformed := driftRevision(t, nil)
	malformed.dependencyPolicy = "future-policy"
	if _, err := DetectDrift(base, malformed, driftObservedAt); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("malformed candidate error=%v", err)
	}
}

func driftRevision(t *testing.T, mutate func(*RevisionV2Params)) RevisionV2 {
	t.Helper()
	params := revisionV2Fixture()
	params.ID = "revision-candidate"
	if mutate != nil {
		mutate(&params)
	}
	revision, err := NewRevisionV2(params)
	if err != nil {
		t.Fatalf("build drift fixture: %v", err)
	}
	return revision
}

func addCriterion(params *RevisionV2Params) {
	params.WorkItems[0].Criteria = append(params.WorkItems[0].Criteria, CriterionV2Params{ID: "criterion-added", Text: "Run the added test.", VerifierKind: "test", Required: true, Outcome: "The added test passes."})
}

func removeFixtureDependency(params *RevisionV2Params) {
	params.WorkItems[1].Dependencies = []ids.WorkID{}
	params.Lanes[1].dependencyLaneIDs = []ids.LaneID{}
}

func mergeFixtureLanes(params *RevisionV2Params) {
	params.Lanes = []LaneV2{{id: stableLaneID([]ids.WorkID{"work-a", "work-b"}), workIDs: []ids.WorkID{"work-a", "work-b"}}}
}

func addFixtureWork(params *RevisionV2Params) {
	params.WorkItems = append(params.WorkItems, WorkItemV2Params{WorkID: "work-c", Title: "Implement C", Description: "Implement the added bounded unit.", Criteria: []CriterionV2Params{{ID: "criterion-c", Text: "Run C.", VerifierKind: "test", Required: true, Outcome: "C passes."}}, WriteScopes: []NormalizedWriteScope{{Path: "internal/c", Domain: "workgraph", Access: ScopeWriteExclusive}}})
	params.Lanes = append(params.Lanes, LaneV2{id: stableLaneID([]ids.WorkID{"work-c"}), workIDs: []ids.WorkID{"work-c"}})
	params.ProviderObservations = append(params.ProviderObservations, ProviderObservationV2{WorkID: "work-c", Provider: "durable_fake", ExternalID: "external-c", Revision: "provider-revision-c"})
}

func removeFixtureWork(params *RevisionV2Params) {
	params.WorkItems = params.WorkItems[:1]
	params.Lanes = params.Lanes[:1]
	params.ProviderObservations = params.ProviderObservations[:1]
}

func addIndependentFixtureWork(params *RevisionV2Params) {
	params.WorkItems = append(params.WorkItems, WorkItemV2Params{WorkID: "work-independent", Title: "Independent", Description: "Remain independent.", Criteria: []CriterionV2Params{{ID: "criterion-independent", Text: "Run independent.", VerifierKind: "test", Required: true, Outcome: "Independent passes."}}, WriteScopes: []NormalizedWriteScope{{Path: "internal/independent", Domain: "other", Access: ScopeWriteExclusive}}})
	params.Lanes = append(params.Lanes, LaneV2{id: stableLaneID([]ids.WorkID{"work-independent"}), workIDs: []ids.WorkID{"work-independent"}})
	params.ProviderObservations = append(params.ProviderObservations, ProviderObservationV2{WorkID: "work-independent", Provider: "durable_fake", ExternalID: "external-independent", Revision: "provider-revision-independent"})
}

func insertFixtureDependency(params *RevisionV2Params) {
	params.WorkItems = append(params.WorkItems, WorkItemV2Params{WorkID: "work-c", Title: "Middle dependency", Description: "Insert a bounded dependency.", Criteria: []CriterionV2Params{{ID: "criterion-c", Text: "Run C.", VerifierKind: "test", Required: true, Outcome: "C passes."}}, Dependencies: []ids.WorkID{"work-a"}, WriteScopes: []NormalizedWriteScope{{Path: "internal/c", Domain: "workgraph", Access: ScopeWriteExclusive}}})
	for index := range params.WorkItems {
		if params.WorkItems[index].WorkID == "work-b" {
			params.WorkItems[index].Dependencies = []ids.WorkID{"work-c"}
		}
	}
	laneA := LaneV2{id: stableLaneID([]ids.WorkID{"work-a"}), workIDs: []ids.WorkID{"work-a"}}
	laneB := LaneV2{id: stableLaneID([]ids.WorkID{"work-b"}), workIDs: []ids.WorkID{"work-b"}}
	laneC := LaneV2{id: stableLaneID([]ids.WorkID{"work-c"}), workIDs: []ids.WorkID{"work-c"}}
	laneB.dependencyLaneIDs = []ids.LaneID{laneC.id}
	laneC.dependencyLaneIDs = []ids.LaneID{laneA.id}
	independent := LaneV2{id: stableLaneID([]ids.WorkID{"work-independent"}), workIDs: []ids.WorkID{"work-independent"}}
	params.Lanes = []LaneV2{laneA, laneB, laneC, independent}
	params.ProviderObservations = append(params.ProviderObservations, ProviderObservationV2{WorkID: "work-c", Provider: "durable_fake", ExternalID: "external-c", Revision: "provider-revision-c"})
}

func reportHasField(report DriftReport, field SemanticField) bool {
	for _, change := range report.Changes() {
		if change.Field() == field {
			return true
		}
	}
	return false
}

func assertCompleteReasons(t *testing.T, report DriftReport) {
	t.Helper()
	known := map[string]bool{}
	for _, change := range report.Changes() {
		known[change.ID()] = true
	}
	for _, workID := range report.AffectedOldWorkIDs() {
		if len(report.OldReasons(workID)) == 0 {
			t.Fatalf("old affected work %s has no reason", workID)
		}
		for _, reason := range report.OldReasons(workID) {
			if !known[reason] {
				t.Fatalf("old reason %s is not a change", reason)
			}
		}
	}
	for _, workID := range report.AffectedNewWorkIDs() {
		if len(report.NewReasons(workID)) == 0 {
			t.Fatalf("new affected work %s has no reason", workID)
		}
		for _, reason := range report.NewReasons(workID) {
			if !known[reason] {
				t.Fatalf("new reason %s is not a change", reason)
			}
		}
	}
}

func sameChanges(left, right []DriftChange) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID() != right[index].ID() {
			return false
		}
	}
	return true
}
