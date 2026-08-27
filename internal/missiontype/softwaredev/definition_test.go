package softwaredev_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

func TestDefinitionPinsCompletePhase3Law(t *testing.T) {
	t.Parallel()

	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	if definition.ID() != softwaredev.DefinitionID || definition.Version() != softwaredev.DefinitionVersion {
		t.Fatalf("selection=%s@%d", definition.ID(), definition.Version())
	}
	if definition.InitialStep() != mission.StepInitialized || !reflect.DeepEqual(definition.TerminalSteps(), []mission.StepKey{mission.StepWorkFinalized}) {
		t.Fatalf("initial=%s terminals=%v", definition.InitialStep(), definition.TerminalSteps())
	}

	wantSteps := []mission.StepKey{
		mission.StepDecomposing,
		mission.StepInitialized,
		mission.StepPlanApproved,
		mission.StepPlanning,
		mission.StepReconciliationNeeded,
		mission.StepSpecificationApproved,
		mission.StepSpecifying,
		mission.StepWorkDraftReady,
		mission.StepWorkFinalized,
	}
	sort.Slice(wantSteps, func(i, j int) bool { return wantSteps[i] < wantSteps[j] })
	gotSteps := definition.Steps()
	if len(gotSteps) != len(wantSteps) {
		t.Fatalf("step count=%d want=%d", len(gotSteps), len(wantSteps))
	}
	for index := range gotSteps {
		if gotSteps[index].Key() != wantSteps[index] {
			t.Fatalf("step[%d]=%s want=%s", index, gotSteps[index].Key(), wantSteps[index])
		}
	}

	wantEdges := []string{
		"decomposing->work_draft_ready",
		"initialized->specifying",
		"plan_approved->decomposing",
		"planning->plan_approved",
		"reconciliation_required->work_finalized",
		"specification_approved->planning",
		"specifying->specification_approved",
		"work_draft_ready->reconciliation_required",
		"work_draft_ready->work_finalized",
	}
	var gotEdges []string
	for _, contract := range gotSteps {
		spec := contract.Spec()
		for _, successor := range spec.Successors {
			gotEdges = append(gotEdges, spec.Key.String()+"->"+successor.String())
		}
		if spec.Mutating && spec.GovernanceAction == "" {
			t.Fatalf("mutating step %s has no governance action", spec.Key)
		}
		if spec.Key != mission.StepInitialized && !spec.Mutating {
			t.Fatalf("production transition step %s is not mutating", spec.Key)
		}
	}
	sort.Strings(gotEdges)
	sort.Strings(wantEdges)
	if !reflect.DeepEqual(gotEdges, wantEdges) {
		t.Fatalf("edges=%v want=%v", gotEdges, wantEdges)
	}
}

func TestDefinitionCommitmentAndGateCensusAreStable(t *testing.T) {
	t.Parallel()

	first, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 100; iteration++ {
		next, err := softwaredev.Definition()
		if err != nil {
			t.Fatal(err)
		}
		if next.Commitment() != first.Commitment() || !reflect.DeepEqual(next.Steps(), first.Steps()) {
			t.Fatalf("iteration %d changed static definition", iteration)
		}
	}

	specification, _ := first.Step(mission.StepSpecificationApproved)
	wantSpecification := []mission.EvidenceKind{
		mission.EvidenceAcceptanceFraming,
		mission.EvidenceAssumptions,
		mission.EvidenceBoundaries,
		mission.EvidenceIntent,
		mission.EvidenceRequirements,
		mission.EvidenceScope,
		mission.EvidenceSpecificationOK,
	}
	if !reflect.DeepEqual(specification.Spec().RequiredEvidence, wantSpecification) || specification.Spec().ApprovalPolicy != mission.ApprovalSpecification {
		t.Fatalf("specification gate=%+v", specification.Spec())
	}

	plan, _ := first.Step(mission.StepPlanApproved)
	wantPlan := []mission.EvidenceKind{
		mission.EvidenceArchitecture,
		mission.EvidenceCompatibility,
		mission.EvidenceDataFlow,
		mission.EvidenceMigration,
		mission.EvidencePlanOK,
		mission.EvidenceRisk,
		mission.EvidenceValidationStrategy,
	}
	if !reflect.DeepEqual(plan.Spec().RequiredEvidence, wantPlan) || plan.Spec().ApprovalPolicy != mission.ApprovalPlan {
		t.Fatalf("plan gate=%+v", plan.Spec())
	}

	registry, err := mission.NewRegistry(first)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve(softwaredev.DefinitionID, softwaredev.DefinitionVersion)
	if err != nil || resolved.Commitment() != first.Commitment() {
		t.Fatalf("exact resolution=%+v err=%v", resolved, err)
	}
}
