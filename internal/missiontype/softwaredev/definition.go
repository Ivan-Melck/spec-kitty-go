// Package softwaredev provides the statically compiled software-development
// mission method. It contains data only and performs no I/O or registration.
package softwaredev

import (
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

const (
	DefinitionID      ids.MissionDefinitionID   = "software-development"
	DefinitionVersion mission.DefinitionVersion = 1
)

// Definition constructs and validates a fresh immutable definition value.
func Definition() (mission.MissionDefinition, error) {
	return mission.NewDefinition(mission.DefinitionSpec{
		ID:            DefinitionID,
		Version:       DefinitionVersion,
		InitialStep:   mission.StepInitialized,
		TerminalSteps: []mission.StepKey{mission.StepWorkFinalized},
		Steps: []mission.StepContractSpec{
			{
				Key:             mission.StepInitialized,
				ProducedOutputs: []mission.ArtifactKind{mission.ArtifactIntent},
				ApprovalPolicy:  mission.ApprovalNone,
				Successors:      []mission.StepKey{mission.StepSpecifying},
			},
			{
				Key:              mission.StepSpecifying,
				RequiredInputs:   []mission.ArtifactKind{mission.ArtifactIntent},
				ProducedOutputs:  []mission.ArtifactKind{mission.ArtifactSpecification},
				ApprovalPolicy:   mission.ApprovalNone,
				Predecessors:     []mission.StepKey{mission.StepInitialized},
				Successors:       []mission.StepKey{mission.StepSpecificationApproved},
				GovernanceAction: "mission.specify", Mutating: true,
			},
			{
				Key:            mission.StepSpecificationApproved,
				RequiredInputs: []mission.ArtifactKind{mission.ArtifactSpecification},
				RequiredEvidence: []mission.EvidenceKind{
					mission.EvidenceIntent, mission.EvidenceScope, mission.EvidenceRequirements,
					mission.EvidenceAcceptanceFraming, mission.EvidenceAssumptions,
					mission.EvidenceBoundaries, mission.EvidenceSpecificationOK,
				},
				ValidationPolicy: []mission.ValidationRequirement{mission.ValidationSpecificationComplete},
				ApprovalPolicy:   mission.ApprovalSpecification,
				Predecessors:     []mission.StepKey{mission.StepSpecifying},
				Successors:       []mission.StepKey{mission.StepPlanning},
				GovernanceAction: "mission.approve-specification", Mutating: true,
			},
			{
				Key:              mission.StepPlanning,
				RequiredInputs:   []mission.ArtifactKind{mission.ArtifactSpecification},
				ProducedOutputs:  []mission.ArtifactKind{mission.ArtifactPlan},
				ApprovalPolicy:   mission.ApprovalNone,
				Predecessors:     []mission.StepKey{mission.StepSpecificationApproved},
				Successors:       []mission.StepKey{mission.StepPlanApproved},
				GovernanceAction: "mission.plan", Mutating: true,
			},
			{
				Key:            mission.StepPlanApproved,
				RequiredInputs: []mission.ArtifactKind{mission.ArtifactPlan},
				RequiredEvidence: []mission.EvidenceKind{
					mission.EvidenceArchitecture, mission.EvidenceDataFlow, mission.EvidenceRisk,
					mission.EvidenceCompatibility, mission.EvidenceMigration,
					mission.EvidenceValidationStrategy, mission.EvidencePlanOK,
				},
				ValidationPolicy: []mission.ValidationRequirement{mission.ValidationPlanComplete},
				ApprovalPolicy:   mission.ApprovalPlan,
				Predecessors:     []mission.StepKey{mission.StepPlanning},
				Successors:       []mission.StepKey{mission.StepDecomposing},
				GovernanceAction: "mission.approve-plan", Mutating: true,
			},
			{
				Key:              mission.StepDecomposing,
				RequiredInputs:   []mission.ArtifactKind{mission.ArtifactPlan},
				ProducedOutputs:  []mission.ArtifactKind{mission.ArtifactWorkDraft},
				ApprovalPolicy:   mission.ApprovalNone,
				Predecessors:     []mission.StepKey{mission.StepPlanApproved},
				Successors:       []mission.StepKey{mission.StepWorkDraftReady},
				GovernanceAction: "mission.decompose", Mutating: true,
			},
			{
				Key:              mission.StepWorkDraftReady,
				RequiredInputs:   []mission.ArtifactKind{mission.ArtifactWorkDraft},
				ApprovalPolicy:   mission.ApprovalNone,
				Predecessors:     []mission.StepKey{mission.StepDecomposing},
				Successors:       []mission.StepKey{mission.StepReconciliationRequired, mission.StepWorkFinalized},
				GovernanceAction: "mission.validate-work", Mutating: true,
			},
			{
				Key:              mission.StepReconciliationRequired,
				RequiredInputs:   []mission.ArtifactKind{mission.ArtifactWorkDraft},
				ApprovalPolicy:   mission.ApprovalNone,
				Predecessors:     []mission.StepKey{mission.StepWorkDraftReady},
				Successors:       []mission.StepKey{mission.StepWorkFinalized},
				GovernanceAction: "mission.reconcile", Mutating: true,
			},
			{
				Key:              mission.StepWorkFinalized,
				RequiredInputs:   []mission.ArtifactKind{mission.ArtifactWorkDraft},
				ProducedOutputs:  []mission.ArtifactKind{mission.ArtifactWorkRevision},
				RequiredEvidence: []mission.EvidenceKind{mission.EvidenceWorkGraph},
				ValidationPolicy: []mission.ValidationRequirement{mission.ValidationWorkGraphComplete},
				ApprovalPolicy:   mission.ApprovalNone,
				Predecessors:     []mission.StepKey{mission.StepWorkDraftReady, mission.StepReconciliationRequired},
				GovernanceAction: "mission.finalize-work", Mutating: true,
				TerminalOutcome: mission.OutcomeWorkFinalized,
			},
		},
	})
}
