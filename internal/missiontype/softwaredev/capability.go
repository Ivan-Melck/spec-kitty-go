package softwaredev

import "github.com/spec-kitty/go-kitty3/internal/mission"

// CapabilityDefinition returns unordered capability law. It deliberately has
// no initial action, successor edges, terminal phase, prompt, path, language,
// framework, or provider assumption.
func CapabilityDefinition() mission.CapabilityDefinition {
	invariant := func(kind mission.PredicateKind, producer mission.ActionID) mission.PredicateRequirement {
		return mission.PredicateRequirement{Kind: kind, Authority: mission.InstructionInvariant, ResponsibleCapability: producer}
	}
	templates := []mission.CapabilityTemplate{
		{Action: mission.ActionAcceptanceEvaluate, ScopeKind: mission.ScopeMission, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateWorkGraphCurrent, mission.ActionWorkGraphObserve),
			invariant(mission.PredicateIndependentReview, mission.ActionReviewRecord),
			invariant(mission.PredicateProjectVerification, mission.ActionVerificationRun),
			invariant(mission.PredicatePolicyCurrent, mission.ActionPolicyResolve),
		}},
		{Action: mission.ActionArtifactObserve, ScopeKind: mission.ScopeArtifact},
		{Action: mission.ActionArtifactPublish, ScopeKind: mission.ScopeArtifact, Applicability: applicability(invariant), Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicatePolicyCurrent, mission.ActionPolicyResolve),
		}},
		{Action: mission.ActionBlockerClear, ScopeKind: mission.ScopeAction},
		{Action: mission.ActionCandidateHandoff, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateWorkGraphCurrent, mission.ActionWorkGraphObserve),
			invariant(mission.PredicateLeaseCurrent, mission.ActionExecutionHeartbeat),
			invariant(mission.PredicateWorkspaceBound, mission.ActionWorkspaceAllocate),
		}},
		{Action: mission.ActionConflictResolve, ScopeKind: mission.ScopeAction},
		{Action: mission.ActionDecisionResolve, ScopeKind: mission.ScopeAction},
		{Action: mission.ActionEffectDispatch, ScopeKind: mission.ScopeAction, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateExternalEffectSafe, mission.ActionEffectReconcile),
		}},
		{Action: mission.ActionEffectReconcile, ScopeKind: mission.ScopeAction},
		{Action: mission.ActionExecutionClaim, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateWorkGraphCurrent, mission.ActionWorkGraphObserve),
			invariant(mission.PredicateExecutionEligible, mission.ActionWorkGraphObserve),
			invariant(mission.PredicateRuntimeEpochCurrent, mission.ActionPolicyResolve),
		}},
		{Action: mission.ActionExecutionFinish, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateLeaseCurrent, mission.ActionExecutionHeartbeat),
		}},
		{Action: mission.ActionExecutionHeartbeat, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateLeaseCurrent, mission.ActionExecutionHeartbeat),
		}},
		{Action: mission.ActionIntegrationPublish, ScopeKind: mission.ScopeMission, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateAcceptanceCurrent, mission.ActionAcceptanceEvaluate),
			invariant(mission.PredicateRepositoryTarget, mission.ActionIntegrationPublish),
			invariant(mission.PredicateConflictAbsent, mission.ActionConflictResolve),
		}},
		{Action: mission.ActionMissionCreate, ScopeKind: mission.ScopeMission},
		{Action: mission.ActionMissionCancel, ScopeKind: mission.ScopeMission},
		{Action: mission.ActionMissionComplete, ScopeKind: mission.ScopeMission, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateAcceptanceCurrent, mission.ActionAcceptanceEvaluate),
		}},
		{Action: mission.ActionMissionFail, ScopeKind: mission.ScopeMission},
		{Action: mission.ActionMissionPause, ScopeKind: mission.ScopeMission},
		{Action: mission.ActionMissionResume, ScopeKind: mission.ScopeMission},
		{Action: mission.ActionPolicyResolve, ScopeKind: mission.ScopePolicy},
		{Action: mission.ActionReviewRecord, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateCandidateCurrent, mission.ActionCandidateHandoff),
			invariant(mission.PredicateIndependentReview, mission.ActionReviewRecord),
		}},
		{Action: mission.ActionVerificationRun, ScopeKind: mission.ScopeCriterion, Applicability: applicability(invariant), Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateArtifactCurrent, mission.ActionArtifactObserve),
		}},
		{Action: mission.ActionWorkFinalize, ScopeKind: mission.ScopeMission, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateWorkGraphComplete, mission.ActionWorkGraphObserve),
			invariant(mission.PredicatePolicyCurrent, mission.ActionPolicyResolve),
		}},
		{Action: mission.ActionWorkGraphObserve, ScopeKind: mission.ScopeMission},
		{Action: mission.ActionWorkReconcile, ScopeKind: mission.ScopeMission, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateWorkGraphCurrent, mission.ActionWorkGraphObserve),
		}},
		{Action: mission.ActionWorkspaceAllocate, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateLeaseCurrent, mission.ActionExecutionClaim),
		}},
		{Action: mission.ActionWorkspaceQuarantine, ScopeKind: mission.ScopeWork, Predicates: []mission.PredicateRequirement{
			invariant(mission.PredicateLeaseExpired, mission.ActionExecutionHeartbeat),
			invariant(mission.PredicateWorkspaceBound, mission.ActionWorkspaceAllocate),
		}},
	}
	definition, err := mission.NewCapabilityDefinition(templates)
	if err != nil {
		panic("software-development capability definition is invalid: " + err.Error())
	}
	return definition
}

func applicability(invariant func(mission.PredicateKind, mission.ActionID) mission.PredicateRequirement) *mission.PredicateRequirement {
	value := invariant(mission.PredicateApplicable, mission.ActionArtifactObserve)
	return &value
}
