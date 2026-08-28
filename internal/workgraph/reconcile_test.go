package workgraph

import (
	"reflect"
	"slices"
	"strings"
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

func TestMixedReconciliationSupportsAllDispositionsAndDerivesEveryConsequence(t *testing.T) {
	request := mixedReconcileFixture(t)
	plan, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	wantOld := []ids.WorkID{"work-continue", "work-fork", "work-invalid", "work-super"}
	wantNew := []ids.WorkID{"work-continue", "work-fork-left", "work-fork-right", "work-repair", "work-super-new"}
	if plan.ID() != request.ID || plan.DriftReportID() != request.Drift.ID() || !slices.Equal(plan.OldWorkIDs(), wantOld) || !slices.Equal(plan.NewWorkIDs(), wantNew) || len(plan.Decisions()) != 4 || len(plan.RequestDigest()) != 71 || !strings.HasPrefix(plan.RequestDigest(), "sha256:") {
		t.Fatalf("mixed plan identity is incomplete: old=%v new=%v decisions=%#v", plan.OldWorkIDs(), plan.NewWorkIDs(), plan.Decisions())
	}
	wantDisposition := map[ids.WorkID]Disposition{"work-continue": Continue, "work-super": Supersede, "work-invalid": Invalidate, "work-fork": Fork}
	for workID, disposition := range wantDisposition {
		decision, ok := plan.Decision(workID)
		if !ok || decision.Disposition() != disposition || decision.Rationale() == "" {
			t.Fatalf("decision %s=%#v/%v", workID, decision, ok)
		}
		for _, status := range allStatuses {
			got, exists := plan.Consequence(workID, status)
			want := consequenceFor(disposition, status)
			if !exists || got != want || got.Reason == "" {
				t.Fatalf("consequence %s/%s=%+v want %+v", workID, status, got, want)
			}
			if status == StatusIntegrated && !got.PreservesIntegratedHistory {
				t.Fatalf("%s rewrites integrated history", disposition)
			}
		}
	}
	invalidated, _ := plan.Decision("work-invalid")
	if status, ok := plan.AssignedStatus("work-invalid"); !ok || status != StatusIntegrated || !slices.Equal(invalidated.NewWorkIDs(), []ids.WorkID{"work-repair"}) {
		t.Fatalf("integrated repair relationship/status lost: %s/%#v", status, invalidated)
	}
}

func TestMixedReconciliationRejectsIncompleteDuplicateAndExtraAuthority(t *testing.T) {
	tests := map[string]func(*MixedReconcileRequest){
		"missing decision mutant": func(request *MixedReconcileRequest) { request.Decisions = request.Decisions[:len(request.Decisions)-1] },
		"duplicate decision": func(request *MixedReconcileRequest) {
			request.Decisions = append(request.Decisions, request.Decisions[0])
		},
		"extra unaffected decision": func(request *MixedReconcileRequest) {
			request.Decisions = append(request.Decisions, WorkDispositionDecisionParams{OldWorkID: "work-unaffected", Disposition: Invalidate, Rationale: "not in drift"})
		},
		"missing status":   func(request *MixedReconcileRequest) { request.Statuses = request.Statuses[:len(request.Statuses)-1] },
		"duplicate status": func(request *MixedReconcileRequest) { request.Statuses = append(request.Statuses, request.Statuses[0]) },
		"extra status": func(request *MixedReconcileRequest) {
			request.Statuses = append(request.Statuses, WorkStatusAssignment{WorkID: "work-unaffected", Status: StatusActive})
		},
		"unknown status":      func(request *MixedReconcileRequest) { request.Statuses[0].Status = "provider-open" },
		"unknown disposition": func(request *MixedReconcileRequest) { request.Decisions[0].Disposition = "replace-ish" },
		"unknown successor": func(request *MixedReconcileRequest) {
			decisionByID(request, "work-super").NewWorkIDs = []ids.WorkID{"work-unknown"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := cloneMixedReconcileRequest(mixedReconcileFixture(t))
			mutate(&request)
			if _, err := ReconcileMixed(request); !IsKind(err, KindInvalidReconciliation) {
				t.Fatalf("mutant accepted: %v", err)
			}
		})
	}
	t.Run("existing but unaffected successor", func(t *testing.T) {
		request := mixedReconcileFixtureWithUnaffected(t)
		decisionByID(&request, "work-a").NewWorkIDs = []ids.WorkID{"work-unaffected"}
		if _, err := ReconcileMixed(request); !IsKind(err, KindInvalidReconciliation) {
			t.Fatalf("unaffected successor accepted: %v", err)
		}
	})
}

func TestMixedReconciliationRejectsIncompatibleContinueAndMalformedRelationships(t *testing.T) {
	t.Run("incompatible continue", func(t *testing.T) {
		request := mixedReconcileFixtureWith(t, func(params *RevisionV2Params) {
			for index := range params.WorkItems {
				if params.WorkItems[index].WorkID == "work-continue" {
					params.WorkItems[index].Title = "Changed continue contract"
				}
			}
		})
		if _, err := ReconcileMixed(request); !IsKind(err, KindInvalidReconciliation) {
			t.Fatalf("incompatible continue accepted: %v", err)
		}
	})
	tests := map[string]func(*MixedReconcileRequest){
		"continue remapped": func(request *MixedReconcileRequest) {
			decisionByID(request, "work-continue").NewWorkIDs = []ids.WorkID{"work-super-new"}
		},
		"supersede missing replacement":        func(request *MixedReconcileRequest) { decisionByID(request, "work-super").NewWorkIDs = nil },
		"invalidate integrated without repair": func(request *MixedReconcileRequest) { decisionByID(request, "work-invalid").NewWorkIDs = nil },
		"fork one target": func(request *MixedReconcileRequest) {
			decision := decisionByID(request, "work-fork")
			decision.NewWorkIDs = decision.NewWorkIDs[:1]
			decision.ForkEdges = decision.ForkEdges[:1]
		},
		"fork missing edge": func(request *MixedReconcileRequest) {
			decisionByID(request, "work-fork").ForkEdges = decisionByID(request, "work-fork").ForkEdges[:1]
		},
		"fork duplicate edge": func(request *MixedReconcileRequest) {
			decision := decisionByID(request, "work-fork")
			decision.ForkEdges[1] = decision.ForkEdges[0]
		},
		"fork reused old identity": func(request *MixedReconcileRequest) { decisionByID(request, "work-fork").NewWorkIDs[0] = "work-fork" },
		"non-fork edge": func(request *MixedReconcileRequest) {
			decisionByID(request, "work-super").ForkEdges = []ForkEdge{{FromOldWorkID: "work-super", ToNewWorkID: "work-super-new"}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := cloneMixedReconcileRequest(mixedReconcileFixture(t))
			mutate(&request)
			if _, err := ReconcileMixed(request); !IsKind(err, KindInvalidReconciliation) {
				t.Fatalf("malformed relationship accepted: %v", err)
			}
		})
	}
}

func TestMixedReconciliationCanonicalizesAndDefendsAllCollections(t *testing.T) {
	request := mixedReconcileFixture(t)
	want, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	permuted := cloneMixedReconcileRequest(request)
	slices.Reverse(permuted.Statuses)
	slices.Reverse(permuted.Decisions)
	fork := decisionByID(&permuted, "work-fork")
	slices.Reverse(fork.NewWorkIDs)
	slices.Reverse(fork.ForkEdges)
	got, err := ReconcileMixed(permuted)
	if err != nil || got.RequestDigest() != want.RequestDigest() || !reflect.DeepEqual(got.Decisions(), want.Decisions()) {
		t.Fatalf("semantic permutation changed authority: digest=%s/%s err=%v", got.RequestDigest(), want.RequestDigest(), err)
	}

	mutations := map[string]func(*MixedReconcileRequest){
		"decision rationale": func(value *MixedReconcileRequest) {
			decisionByID(value, "work-super").Rationale = "different replacement authority"
		},
		"global rationale": func(value *MixedReconcileRequest) { value.Rationale = "different complete plan rationale" },
		"assigned status":  func(value *MixedReconcileRequest) { statusByID(value, "work-continue").Status = StatusApproved },
		"actor":            func(value *MixedReconcileRequest) { value.ActorID = "actor-other" },
		"time":             func(value *MixedReconcileRequest) { value.OccurredAt = value.OccurredAt.Add(time.Second) },
		"disposition": func(value *MixedReconcileRequest) {
			decision := decisionByID(value, "work-super")
			decision.Disposition = Invalidate
		},
		"successor mapping": func(value *MixedReconcileRequest) {
			decisionByID(value, "work-super").NewWorkIDs = []ids.WorkID{"work-repair"}
		},
		"fork mapping": func(value *MixedReconcileRequest) {
			decision := decisionByID(value, "work-fork")
			decision.NewWorkIDs[1] = "work-repair"
			decision.ForkEdges[1].ToNewWorkID = "work-repair"
		},
		"drift evidence": func(value *MixedReconcileRequest) {
			value.Drift, _ = DetectDrift(value.Old, value.New, value.Drift.ObservedAt().Add(time.Second))
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := cloneMixedReconcileRequest(request)
			mutate(&changed)
			plan, buildErr := ReconcileMixed(changed)
			if buildErr != nil || plan.RequestDigest() == want.RequestDigest() {
				t.Fatalf("semantic mutation did not change digest: %s/%s err=%v", plan.RequestDigest(), want.RequestDigest(), buildErr)
			}
		})
	}

	originalDigest := want.RequestDigest()
	request.Statuses[0].Status = "mutated"
	request.Decisions[0].NewWorkIDs[0] = "work-mutated"
	decisionByID(&request, "work-fork").ForkEdges[0].ToNewWorkID = "work-mutated"
	returned := want.Decisions()
	returned[0].newWorkIDs[0] = "work-mutated"
	returned[1].forkEdges = append(returned[1].forkEdges, ForkEdge{FromOldWorkID: "work-mutated", ToNewWorkID: "work-mutated"})
	oldIDs, newIDs, edges := want.OldWorkIDs(), want.NewWorkIDs(), want.ForkEdges()
	oldIDs[0], newIDs[0], edges[0].ToNewWorkID = "work-mutated", "work-mutated", "work-mutated"
	if want.RequestDigest() != originalDigest || want.Decisions()[0].NewWorkIDs()[0] == "work-mutated" || want.OldWorkIDs()[0] == "work-mutated" || want.NewWorkIDs()[0] == "work-mutated" || want.ForkEdges()[0].ToNewWorkID == "work-mutated" {
		t.Fatal("caller mutation altered immutable mixed reconciliation")
	}
}

func TestMixedReconciliationDerivesEveryAssignedStatusDispositionPair(t *testing.T) {
	for _, status := range allStatuses {
		for _, disposition := range []Disposition{Continue, Supersede, Invalidate, Fork} {
			t.Run(string(status)+"/"+string(disposition), func(t *testing.T) {
				request := cloneMixedReconcileRequest(mixedReconcileFixture(t))
				statusByID(&request, "work-continue").Status = status
				decision := decisionByID(&request, "work-continue")
				decision.Disposition = disposition
				switch disposition {
				case Continue:
					decision.NewWorkIDs = []ids.WorkID{"work-continue"}
					decision.ForkEdges = nil
				case Supersede:
					decision.NewWorkIDs = []ids.WorkID{"work-super-new"}
					decision.ForkEdges = nil
				case Invalidate:
					decision.NewWorkIDs = nil
					decision.ForkEdges = nil
					if status == StatusIntegrated {
						decision.NewWorkIDs = []ids.WorkID{"work-repair"}
					}
				case Fork:
					decision.NewWorkIDs = []ids.WorkID{"work-fork-left", "work-fork-right"}
					decision.ForkEdges = []ForkEdge{{"work-continue", "work-fork-left"}, {"work-continue", "work-fork-right"}}
				}
				plan, err := ReconcileMixed(request)
				if err != nil {
					t.Fatal(err)
				}
				got, ok := plan.Consequence("work-continue", status)
				if !ok || got != consequenceFor(disposition, status) {
					t.Fatalf("derived consequence=%+v/%v", got, ok)
				}
			})
		}
	}
}

func TestEligibilityFactsDistinguishCompleteMixedAuthorityFromLegacyProjection(t *testing.T) {
	request := mixedReconcileFixture(t)
	plan, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := plan.EligibilityFact("work-invalid", StatusActive, EligibilityHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if fact.LegacyV1() || fact.Permitted() || fact.ReconciliationID() != plan.ID() || fact.OldRevisionID() != plan.OldRevisionID() || fact.NewRevisionID() != plan.NewRevisionID() || fact.WorkID() != "work-invalid" || fact.Status() != StatusActive || fact.Disposition() != Invalidate || fact.Operation() != EligibilityHandoff || fact.Reason() == "" {
		t.Fatalf("incomplete mixed eligibility fact: %#v", fact)
	}
	if !fact.MatchesComplete(plan.ID(), plan.OldRevisionID(), plan.NewRevisionID(), "work-invalid", StatusActive, Invalidate, EligibilityHandoff) || fact.MatchesComplete(plan.ID(), plan.OldRevisionID(), "revision-substituted", "work-invalid", StatusActive, Invalidate, EligibilityHandoff) {
		t.Fatal("complete fact matching did not bind every authority field")
	}
	legacy, err := NewLegacyEligibilityFact("", plan.OldRevisionID(), "work-invalid", StatusActive, EligibilityHandoff, Consequence{HandoffEligible: true, Reason: "no reconciliation restricts this WorkRevision generation"})
	if err != nil || !legacy.LegacyV1() || !legacy.MatchesLegacy(plan.OldRevisionID(), "work-invalid", StatusActive, EligibilityHandoff) || legacy.NewRevisionID() != "" || legacy.Disposition() != "" {
		t.Fatalf("legacy eligibility projection is not explicit: %#v err=%v", legacy, err)
	}
}

func TestMixedEligibilityPermitsOnlyDispositionOperationConsequences(t *testing.T) {
	plan, err := ReconcileMixed(mixedReconcileFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		workID    ids.WorkID
		status    WorkStatus
		operation EligibilityOperation
		permitted bool
	}{
		{"work-continue", StatusActive, EligibilityHandoff, true},
		{"work-fork", StatusHandedOff, EligibilityReview, true},
		{"work-fork", StatusApproved, EligibilityIntegration, true},
		{"work-super", StatusActive, EligibilityHandoff, false},
		{"work-super", StatusApproved, EligibilityIntegration, false},
		{"work-invalid", StatusActive, EligibilityHandoff, false},
	}
	for _, test := range tests {
		fact, buildErr := plan.EligibilityFact(test.workID, test.status, test.operation)
		if buildErr != nil || fact.Permitted() != test.permitted {
			t.Fatalf("%s/%s/%s permitted=%v want=%v err=%v", test.workID, test.status, test.operation, fact.Permitted(), test.permitted, buildErr)
		}
	}
	if _, err := plan.EligibilityFact("work-fork", StatusActive, EligibilityIntegration); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("operation/status mismatch accepted: %v", err)
	}
}

func TestMixedReconciliationAcceptsIndependentAdditionWithExactEmptyOldAuthority(t *testing.T) {
	request := additionOnlyReconcileFixture(t)
	plan, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.OldWorkIDs()) != 0 || len(plan.Decisions()) != 0 || len(plan.NewWorkIDs()) != 1 || plan.NewWorkIDs()[0] != "work-added" || plan.OldRevisionID() != request.Old.ID() || plan.NewRevisionID() != request.New.ID() {
		t.Fatalf("addition-only reconciliation lost exact authority: old=%v new=%v decisions=%v", plan.OldWorkIDs(), plan.NewWorkIDs(), plan.Decisions())
	}

	mutants := map[string]func(*MixedReconcileRequest){
		"invented status": func(value *MixedReconcileRequest) {
			value.Statuses = []WorkStatusAssignment{{WorkID: "work-existing", Status: StatusActive}}
		},
		"invented decision": func(value *MixedReconcileRequest) {
			value.Decisions = []WorkDispositionDecisionParams{{OldWorkID: "work-existing", Disposition: Continue, NewWorkIDs: []ids.WorkID{"work-existing"}, Rationale: "invented old authority"}}
		},
		"unknown affected new identity": func(value *MixedReconcileRequest) {
			value.Drift.affectedNew = []ids.WorkID{"work-unknown"}
		},
		"clean report": func(value *MixedReconcileRequest) {
			value.Drift.status = SemanticClean
			value.Drift.affectedNew = nil
		},
		"observation only report": func(value *MixedReconcileRequest) {
			value.Drift.status = SemanticObservationOnly
			value.Drift.affectedNew = nil
		},
		"changed without affected work": func(value *MixedReconcileRequest) {
			value.Drift.affectedNew = nil
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := additionOnlyReconcileFixture(t)
			mutate(&candidate)
			if _, err := ReconcileMixed(candidate); !IsKind(err, KindInvalidReconciliation) {
				t.Fatalf("mutant survived: %v", err)
			}
		})
	}
}

func TestCompleteEligibilityFactDerivesAndValidatesEveryAuthorityField(t *testing.T) {
	fact, err := NewEligibilityFact("reconciliation-complete", "revision-old", "revision-new", "work-a", StatusActive, Continue, EligibilityHandoff)
	if err != nil || !fact.Complete() || fact.LegacyV1() || !fact.Permitted() {
		t.Fatalf("complete fact=%#v err=%v", fact, err)
	}
	mutants := map[string]struct {
		mutate                    func(*EligibilityFact)
		remainsInternallyComplete bool
	}{
		"successor":  {func(value *EligibilityFact) { value.newRevisionID = "revision-other" }, true},
		"permission": {func(value *EligibilityFact) { value.permitted = !value.permitted }, false},
		"reason":     {func(value *EligibilityFact) { value.reason = "caller-authored reason" }, false},
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			changed := fact
			mutant.mutate(&changed)
			if changed.Complete() != mutant.remainsInternallyComplete || fact.SameComplete(changed) {
				t.Fatal("complete fact accepted caller-authored authority mutation")
			}
		})
	}
	if !fact.SameComplete(fact) {
		t.Fatal("complete fact does not equal itself")
	}
}

func TestLegacyReconcileSurfaceRemainsBehaviorCompatible(t *testing.T) {
	oldRevision := finalizedSingle(t, "revision-legacy-old", 1, "work-legacy")
	newRevision := finalizedSingle(t, "revision-legacy-new", 2, "work-legacy")
	request := ReconcileRequest{ID: "reconciliation-legacy", Old: oldRevision, New: newRevision, Disposition: Continue, ActorID: "actor-owner", Rationale: "preserve historical contract", OldWorkIDs: []ids.WorkID{"work-legacy"}, NewWorkIDs: []ids.WorkID{"work-legacy"}, OccurredAt: time.Unix(1, 0)}
	legacy, err := Reconcile(request)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Disposition() != Continue || !slices.Equal(legacy.OldWorkIDs(), request.OldWorkIDs) || !slices.Equal(legacy.NewWorkIDs(), request.NewWorkIDs) || len(legacy.RequestDigest()) != 71 || !strings.HasPrefix(legacy.RequestDigest(), "sha256:") {
		t.Fatalf("legacy projection changed: %#v", legacy)
	}
	for _, status := range allStatuses {
		got, ok := legacy.Consequence("work-legacy", status)
		if !ok || got != consequenceFor(Continue, status) {
			t.Fatalf("legacy consequence %s=%+v/%v", status, got, ok)
		}
	}
}

func mixedReconcileFixture(t *testing.T) MixedReconcileRequest {
	t.Helper()
	return mixedReconcileFixtureWith(t, nil)
}

func additionOnlyReconcileFixture(t *testing.T) MixedReconcileRequest {
	t.Helper()
	build := func(id ids.WorkRevisionID, generation uint64, predecessor ids.WorkRevisionID, items []WorkItemV2Params) RevisionV2 {
		lanes := make([]LaneV2, 0, len(items))
		observations := make([]ProviderObservationV2, 0, len(items))
		for _, item := range items {
			lanes = append(lanes, LaneV2{id: stableLaneID([]ids.WorkID{item.WorkID}), workIDs: []ids.WorkID{item.WorkID}})
			observations = append(observations, ProviderObservationV2{WorkID: item.WorkID, Provider: "durable_fake", ExternalID: "external-" + item.WorkID.String(), Revision: "provider-" + item.WorkID.String()})
		}
		revision, err := NewRevisionV2(RevisionV2Params{
			ID: id, ProjectID: "project-addition", MissionID: "mission-addition", Generation: generation, PredecessorID: predecessor,
			PlanningSourceCommit: strings.Repeat("a", 40), MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("b", 64)},
			DependencyPolicy: DependencyApprovedOrDone, WorkItems: items, Lanes: lanes, ProviderObservations: observations,
			FinalizedBy: "actor-addition", FinalizedAt: time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC), GovernanceContextID: "governance-addition",
		})
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	existing := mixedWorkItem("work-existing")
	added := mixedWorkItem("work-added")
	oldRevision := build("revision-addition-old", 1, "", []WorkItemV2Params{existing})
	newRevision := build("revision-addition-new", 2, oldRevision.ID(), []WorkItemV2Params{existing, added})
	observedAt := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	report, err := DetectDrift(oldRevision, newRevision, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.AffectedOldWorkIDs()) != 0 || len(report.AffectedNewWorkIDs()) != 1 || report.AffectedNewWorkIDs()[0] != "work-added" {
		t.Fatalf("fixture is not independent addition-only drift: old=%v new=%v", report.AffectedOldWorkIDs(), report.AffectedNewWorkIDs())
	}
	return MixedReconcileRequest{ID: "reconciliation-addition", Old: oldRevision, New: newRevision, Drift: report, ActorID: "actor-addition", Rationale: "publish independent added work", OccurredAt: observedAt}
}

func mixedReconcileFixtureWith(t *testing.T, mutateNew func(*RevisionV2Params)) MixedReconcileRequest {
	t.Helper()
	oldItems := []WorkItemV2Params{
		mixedWorkItem("work-continue"),
		mixedWorkItem("work-fork"),
		mixedWorkItem("work-invalid"),
		mixedWorkItem("work-super"),
	}
	newItems := []WorkItemV2Params{
		mixedWorkItem("work-continue"),
		mixedWorkItem("work-fork-left"),
		mixedWorkItem("work-fork-right"),
		mixedWorkItem("work-repair"),
		mixedWorkItem("work-super-new"),
	}
	buildParams := func(id ids.WorkRevisionID, generation uint64, predecessor ids.WorkRevisionID, commit string, items []WorkItemV2Params) RevisionV2Params {
		lanes := make([]LaneV2, 0, len(items))
		observations := make([]ProviderObservationV2, 0, len(items))
		for _, item := range items {
			lanes = append(lanes, LaneV2{id: stableLaneID([]ids.WorkID{item.WorkID}), workIDs: []ids.WorkID{item.WorkID}})
			observations = append(observations, ProviderObservationV2{WorkID: item.WorkID, Provider: "durable_fake", ExternalID: "external-" + item.WorkID.String(), Revision: "provider-revision-" + item.WorkID.String()})
		}
		return RevisionV2Params{
			ID: id, ProjectID: "project-mixed", MissionID: "mission-mixed", Generation: generation,
			PredecessorID: predecessor, PlanningSourceCommit: commit,
			MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("b", 64)},
			DependencyPolicy:  DependencyApprovedOrDone, WorkItems: items, Lanes: lanes, ProviderObservations: observations,
			FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC), GovernanceContextID: "governance-context-mixed",
		}
	}
	oldParams := buildParams("revision-mixed-old", 1, "", strings.Repeat("a", 40), oldItems)
	newParams := buildParams("revision-mixed-new", 2, "revision-mixed-old", strings.Repeat("c", 40), newItems)
	if mutateNew != nil {
		mutateNew(&newParams)
	}
	oldRevision, err := NewRevisionV2(oldParams)
	if err != nil {
		t.Fatalf("build old mixed fixture: %v", err)
	}
	newRevision, err := NewRevisionV2(newParams)
	if err != nil {
		t.Fatalf("build new mixed fixture: %v", err)
	}
	report, err := DetectDrift(oldRevision, newRevision, time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("build mixed drift fixture: %v", err)
	}
	return MixedReconcileRequest{
		ID: "reconciliation-mixed", Old: oldRevision, New: newRevision, Drift: report,
		Statuses: []WorkStatusAssignment{{"work-continue", StatusActive}, {"work-super", StatusHandedOff}, {"work-fork", StatusApproved}, {"work-invalid", StatusIntegrated}},
		Decisions: []WorkDispositionDecisionParams{
			{OldWorkID: "work-continue", Disposition: Continue, NewWorkIDs: []ids.WorkID{"work-continue"}, Rationale: "continue the unchanged bounded contract"},
			{OldWorkID: "work-super", Disposition: Supersede, NewWorkIDs: []ids.WorkID{"work-super-new"}, Rationale: "replace the old bounded contract"},
			{OldWorkID: "work-invalid", Disposition: Invalidate, NewWorkIDs: []ids.WorkID{"work-repair"}, Rationale: "preserve integrated evidence and schedule repair"},
			{OldWorkID: "work-fork", Disposition: Fork, NewWorkIDs: []ids.WorkID{"work-fork-left", "work-fork-right"}, ForkEdges: []ForkEdge{{"work-fork", "work-fork-left"}, {"work-fork", "work-fork-right"}}, Rationale: "split the old contract into explicit lines"},
		},
		ActorID: "actor-owner", Rationale: "reconcile the complete affected work set", OccurredAt: time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
	}
}

func mixedReconcileFixtureWithUnaffected(t *testing.T) MixedReconcileRequest {
	t.Helper()
	build := func(id ids.WorkRevisionID, generation uint64, predecessor ids.WorkRevisionID, items []WorkItemV2Params) RevisionV2 {
		lanes := make([]LaneV2, 0, len(items))
		observations := make([]ProviderObservationV2, 0, len(items))
		for _, item := range items {
			lanes = append(lanes, LaneV2{id: stableLaneID([]ids.WorkID{item.WorkID}), workIDs: []ids.WorkID{item.WorkID}})
			observations = append(observations, ProviderObservationV2{WorkID: item.WorkID, Provider: "durable_fake", ExternalID: "external-" + item.WorkID.String(), Revision: "revision-" + item.WorkID.String()})
		}
		revision, err := NewRevisionV2(RevisionV2Params{
			ID: id, ProjectID: "project-mixed", MissionID: "mission-mixed", Generation: generation, PredecessorID: predecessor,
			PlanningSourceCommit: strings.Repeat("d", 40), MissionDefinition: MissionDefinitionIdentity{Key: "software-development", Version: 1, Digest: strings.Repeat("e", 64)},
			DependencyPolicy: DependencyApprovedOrDone, WorkItems: items, Lanes: lanes, ProviderObservations: observations,
			FinalizedBy: "actor-finalizer", FinalizedAt: time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC), GovernanceContextID: "governance-context-mixed",
		})
		if err != nil {
			t.Fatalf("build unaffected fixture revision: %v", err)
		}
		return revision
	}
	oldRevision := build("revision-unaffected-old", 1, "", []WorkItemV2Params{mixedWorkItem("work-a"), mixedWorkItem("work-b"), mixedWorkItem("work-unaffected")})
	newRevision := build("revision-unaffected-new", 2, "revision-unaffected-old", []WorkItemV2Params{mixedWorkItem("work-a-new"), mixedWorkItem("work-b-new"), mixedWorkItem("work-unaffected")})
	report, err := DetectDrift(oldRevision, newRevision, time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return MixedReconcileRequest{
		ID: "reconciliation-unaffected", Old: oldRevision, New: newRevision, Drift: report,
		Statuses: []WorkStatusAssignment{{"work-a", StatusActive}, {"work-b", StatusApproved}},
		Decisions: []WorkDispositionDecisionParams{
			{OldWorkID: "work-a", Disposition: Supersede, NewWorkIDs: []ids.WorkID{"work-a-new"}, Rationale: "replace work a"},
			{OldWorkID: "work-b", Disposition: Supersede, NewWorkIDs: []ids.WorkID{"work-b-new"}, Rationale: "replace work b"},
		},
		ActorID: "actor-owner", Rationale: "preserve the unrelated work contract", OccurredAt: time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
	}
}

func mixedWorkItem(workID ids.WorkID) WorkItemV2Params {
	return WorkItemV2Params{
		WorkID: workID, Title: "Implement " + workID.String(), Description: "Implement the bounded " + workID.String() + " contract.",
		Criteria:    []CriterionV2Params{{ID: "criterion-" + workID.String(), Text: "Verify " + workID.String() + ".", VerifierKind: "test", Required: true, Outcome: workID.String() + " passes."}},
		WriteScopes: []NormalizedWriteScope{{Path: "internal/" + workID.String(), Domain: "workgraph", Access: ScopeWriteExclusive}},
	}
}

func cloneMixedReconcileRequest(input MixedReconcileRequest) MixedReconcileRequest {
	result := input
	result.Statuses = append([]WorkStatusAssignment(nil), input.Statuses...)
	result.Decisions = make([]WorkDispositionDecisionParams, len(input.Decisions))
	for index, decision := range input.Decisions {
		result.Decisions[index] = decision
		result.Decisions[index].NewWorkIDs = append([]ids.WorkID(nil), decision.NewWorkIDs...)
		result.Decisions[index].ForkEdges = append([]ForkEdge(nil), decision.ForkEdges...)
	}
	return result
}

func decisionByID(request *MixedReconcileRequest, workID ids.WorkID) *WorkDispositionDecisionParams {
	for index := range request.Decisions {
		if request.Decisions[index].OldWorkID == workID {
			return &request.Decisions[index]
		}
	}
	panic("mixed fixture decision not found: " + workID.String())
}

func statusByID(request *MixedReconcileRequest, workID ids.WorkID) *WorkStatusAssignment {
	for index := range request.Statuses {
		if request.Statuses[index].WorkID == workID {
			return &request.Statuses[index]
		}
	}
	panic("mixed fixture status not found: " + workID.String())
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
