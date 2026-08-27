package softwaredev_test

import (
	"reflect"
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

	want := []mission.StepContractSpec{
		{
			Key: mission.StepDecomposing, RequiredInputs: []mission.ArtifactKind{mission.ArtifactPlan},
			ProducedOutputs: []mission.ArtifactKind{mission.ArtifactWorkDraft}, ApprovalPolicy: mission.ApprovalNone,
			Predecessors: []mission.StepKey{mission.StepPlanApproved}, Successors: []mission.StepKey{mission.StepWorkDraftReady},
			GovernanceAction: "mission.decompose", Mutating: true,
		},
		{
			Key: mission.StepInitialized, ProducedOutputs: []mission.ArtifactKind{mission.ArtifactIntent},
			ApprovalPolicy: mission.ApprovalNone, Successors: []mission.StepKey{mission.StepSpecifying},
		},
		{
			Key: mission.StepPlanApproved, RequiredInputs: []mission.ArtifactKind{mission.ArtifactPlan},
			RequiredEvidence: []mission.EvidenceKind{
				mission.EvidenceArchitecture, mission.EvidenceCompatibility, mission.EvidenceDataFlow,
				mission.EvidenceMigration, mission.EvidencePlanOK, mission.EvidenceRisk, mission.EvidenceValidationStrategy,
			},
			ValidationPolicy: []mission.ValidationRequirement{mission.ValidationPlanComplete}, ApprovalPolicy: mission.ApprovalPlan,
			Predecessors: []mission.StepKey{mission.StepPlanning}, Successors: []mission.StepKey{mission.StepDecomposing},
			GovernanceAction: "mission.approve-plan", Mutating: true,
		},
		{
			Key: mission.StepPlanning, RequiredInputs: []mission.ArtifactKind{mission.ArtifactSpecification},
			ProducedOutputs: []mission.ArtifactKind{mission.ArtifactPlan}, ApprovalPolicy: mission.ApprovalNone,
			Predecessors: []mission.StepKey{mission.StepSpecificationApproved}, Successors: []mission.StepKey{mission.StepPlanApproved},
			GovernanceAction: "mission.plan", Mutating: true,
		},
		{
			Key: mission.StepReconciliationRequired, RequiredInputs: []mission.ArtifactKind{mission.ArtifactWorkDraft},
			ApprovalPolicy: mission.ApprovalNone, Predecessors: []mission.StepKey{mission.StepWorkDraftReady},
			Successors: []mission.StepKey{mission.StepWorkFinalized}, GovernanceAction: "mission.reconcile", Mutating: true,
		},
		{
			Key: mission.StepSpecificationApproved, RequiredInputs: []mission.ArtifactKind{mission.ArtifactSpecification},
			RequiredEvidence: []mission.EvidenceKind{
				mission.EvidenceAcceptanceFraming, mission.EvidenceAssumptions, mission.EvidenceBoundaries,
				mission.EvidenceIntent, mission.EvidenceRequirements, mission.EvidenceScope, mission.EvidenceSpecificationOK,
			},
			ValidationPolicy: []mission.ValidationRequirement{mission.ValidationSpecificationComplete}, ApprovalPolicy: mission.ApprovalSpecification,
			Predecessors: []mission.StepKey{mission.StepSpecifying}, Successors: []mission.StepKey{mission.StepPlanning},
			GovernanceAction: "mission.approve-specification", Mutating: true,
		},
		{
			Key: mission.StepSpecifying, RequiredInputs: []mission.ArtifactKind{mission.ArtifactIntent},
			ProducedOutputs: []mission.ArtifactKind{mission.ArtifactSpecification}, ApprovalPolicy: mission.ApprovalNone,
			Predecessors: []mission.StepKey{mission.StepInitialized}, Successors: []mission.StepKey{mission.StepSpecificationApproved},
			GovernanceAction: "mission.specify", Mutating: true,
		},
		{
			Key: mission.StepWorkDraftReady, RequiredInputs: []mission.ArtifactKind{mission.ArtifactWorkDraft},
			ApprovalPolicy: mission.ApprovalNone, Predecessors: []mission.StepKey{mission.StepDecomposing},
			Successors:       []mission.StepKey{mission.StepReconciliationRequired, mission.StepWorkFinalized},
			GovernanceAction: "mission.validate-work", Mutating: true,
		},
		{
			Key: mission.StepWorkFinalized, RequiredInputs: []mission.ArtifactKind{mission.ArtifactWorkDraft},
			ProducedOutputs: []mission.ArtifactKind{mission.ArtifactWorkRevision}, RequiredEvidence: []mission.EvidenceKind{mission.EvidenceWorkGraph},
			ValidationPolicy: []mission.ValidationRequirement{mission.ValidationWorkGraphComplete}, ApprovalPolicy: mission.ApprovalNone,
			Predecessors:     []mission.StepKey{mission.StepReconciliationRequired, mission.StepWorkDraftReady},
			GovernanceAction: "mission.finalize-work", Mutating: true, TerminalOutcome: mission.OutcomeWorkFinalized,
		},
	}
	gotContracts := definition.Steps()
	got := make([]mission.StepContractSpec, len(gotContracts))
	for index := range gotContracts {
		got[index] = gotContracts[index].Spec()
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete static law changed:\ngot:  %#v\nwant: %#v", got, want)
	}
	const wantCommitment = "3f1c507babb21f02235797dcdec853c798dbbaa5af28124a1fd1ab020efeed29"
	if definition.Commitment().String() != wantCommitment {
		t.Fatalf("production commitment=%s want=%s", definition.Commitment(), wantCommitment)
	}

	for _, reserved := range []mission.StepKey{mission.StepExecuteReserved, mission.StepReviewReserved, mission.StepIntegrateReserved} {
		if !reserved.Reserved() {
			t.Fatalf("reserved step became actionable: %s", reserved)
		}
		if _, found := definition.Step(reserved); found {
			t.Fatalf("reserved step entered production definition: %s", reserved)
		}
	}
	for _, production := range mission.Phase3StepKeys() {
		if production.Reserved() {
			t.Fatalf("production step marked reserved: %s", production)
		}
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
