package mission

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestCapabilityReadinessReturnsCompleteCensusAndParallelActions(t *testing.T) {
	contracts := mustReadinessContracts(t,
		CapabilityContract{Action: ActionArtifactPublish},
		CapabilityContract{Action: ActionVerificationRun},
		CapabilityContract{Action: ActionWorkFinalize, Predicates: []PredicateRequirement{
			invariantRequirement(PredicateWorkGraphCurrent, ActionWorkFinalize),
		}},
	)
	facts := []PredicateFact{
		satisfiedFact(PredicateLifecycleAllows, ActionArtifactPublish),
		satisfiedFact(PredicateLifecycleAllows, ActionVerificationRun),
		unavailableFact(PredicateWorkGraphCurrent, ActionWorkFinalize, "evidence-workgraph", ActionWorkGraphObserve),
	}

	got, err := EvaluateReadiness(ReadinessInput{
		MissionID: "mission-autonomy", Revision: 9, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen,
		Contracts: contracts, Facts: facts,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ActionRef{testRef(ActionArtifactPublish), testRef(ActionVerificationRun)}; !slices.Equal(got.Authorized(), want) {
		t.Fatalf("authorized=%v want=%v", got.Authorized(), want)
	}
	unavailable := got.Unavailable()
	if len(unavailable) != 1 || unavailable[0].Action != testRef(ActionWorkFinalize) || len(unavailable[0].Predicates) != 1 {
		t.Fatalf("complete unavailable census lost: %+v", unavailable)
	}
	predicate := unavailable[0].Predicates[0]
	if predicate.Status != PredicateUnavailable || predicate.Authority != testAuthority("mission_authority") || predicate.EvidenceIdentity != "evidence-workgraph" || predicate.EvidenceDigest != testEvidenceDigest() || predicate.ResponsibleCapability != ActionWorkGraphObserve || predicate.Recovery != RecoveryRunProducer {
		t.Fatalf("blocking predicate is not actionable: %+v", predicate)
	}
	if got.SnapshotDigest() != testSnapshotDigest() {
		t.Fatalf("snapshot digest was not preserved: %q", got.SnapshotDigest())
	}
	if got.Terminal() || got.Lifecycle() != MissionOpen {
		t.Fatal("open mission reported terminal")
	}
}

func TestCapabilityReadinessScopesDecisionsAndBlockersToAffectedActions(t *testing.T) {
	contracts := mustReadinessContracts(t,
		CapabilityContract{Action: ActionArtifactPublish},
		CapabilityContract{Action: ActionVerificationRun},
		CapabilityContract{Action: ActionDecisionResolve},
		CapabilityContract{Action: ActionBlockerClear},
	)
	input := ReadinessInput{
		MissionID: "mission-scoped", Revision: 3, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts,
		Facts: []PredicateFact{
			satisfiedFact(PredicateLifecycleAllows, ActionArtifactPublish),
			satisfiedFact(PredicateLifecycleAllows, ActionVerificationRun),
			satisfiedFact(PredicateLifecycleAllows, ActionDecisionResolve),
			satisfiedFact(PredicateLifecycleAllows, ActionBlockerClear),
		},
		Decisions: []ReadinessDecision{{ID: "decision-api-shape", Affects: []ActionRef{testRef(ActionArtifactPublish)}, Authority: testAuthority("mission_decision"), EvidenceIdentity: "evidence-decision", EvidenceDigest: testEvidenceDigest()}},
		Blockers:  []ReadinessBlocker{{ID: "blocker-verifier", Affects: []ActionRef{testRef(ActionVerificationRun)}, Authority: testAuthority("mission_blocker"), EvidenceIdentity: "evidence-blocker", EvidenceDigest: testEvidenceDigest(), ResponsibleCapability: ActionVerificationRun, Recovery: RecoveryRestoreDependency}},
	}

	got, err := EvaluateReadiness(input)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Authorized(), []ActionRef{testRef(ActionBlockerClear), testRef(ActionDecisionResolve)}) {
		t.Fatalf("scoped facts became a global ceremony gate: %v", got.Authorized())
	}
	if len(got.DecisionsRequired()) != 1 || len(got.Conflicts()) != 0 {
		t.Fatalf("decision/conflict projection lost: decisions=%v conflicts=%v", got.DecisionsRequired(), got.Conflicts())
	}
}

func TestCapabilityReadinessApplicabilityAndInstructionAuthority(t *testing.T) {
	contracts := mustReadinessContracts(t,
		CapabilityContract{Action: ActionAcceptanceEvaluate, Predicates: []PredicateRequirement{
			{Kind: PredicateApplicable, Authority: InstructionInvariant, ResponsibleCapability: ActionArtifactObserve},
			{Kind: PredicateProjectVerification, Authority: InstructionInvariant, ResponsibleCapability: ActionVerificationRun},
			{Kind: PredicatePolicyCurrent, Authority: InstructionDefault, ResponsibleCapability: ActionPolicyResolve},
			{Kind: PredicateArtifactCurrent, Authority: InstructionExample, ResponsibleCapability: ActionArtifactPublish},
		}},
	)
	facts := []PredicateFact{
		{Kind: PredicateApplicable, Action: ActionAcceptanceEvaluate, Scope: testScope(), Status: PredicateInapplicable, Authority: testAuthority("project_policy"), EvidenceIdentity: "applicability-no-network-api", EvidenceDigest: testEvidenceDigest(), Producer: ActionArtifactObserve, Recovery: RecoveryNone},
		{Kind: PredicateProjectVerification, Action: ActionAcceptanceEvaluate, Scope: testScope(), Status: PredicateSatisfied, Authority: testAuthority("verifier_authority"), EvidenceIdentity: "verification-44", EvidenceDigest: testEvidenceDigest(), Producer: ActionVerificationRun, Recovery: RecoveryNone},
		{Kind: PredicatePolicyCurrent, Action: ActionAcceptanceEvaluate, Scope: testScope(), Status: PredicateFailed, Authority: testAuthority("project_policy"), EvidenceIdentity: "policy-default", EvidenceDigest: testEvidenceDigest(), Producer: ActionPolicyResolve, Recovery: RecoveryRefreshEvidence},
		{Kind: PredicateArtifactCurrent, Action: ActionAcceptanceEvaluate, Scope: testScope(), Status: PredicateUnavailable, Authority: testAuthority("artifact_authority"), EvidenceIdentity: "example-only", EvidenceDigest: testEvidenceDigest(), Producer: ActionArtifactPublish, Recovery: RecoveryRunProducer},
	}
	got, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-cli", Revision: 4, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Authorized(), []ActionRef{testRef(ActionAcceptanceEvaluate)}) {
		t.Fatalf("inapplicable/default/example guidance became authority: %+v", got.Unavailable())
	}

	missingProducer := slices.Clone(facts)
	missingProducer[1].Producer = ""
	if _, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-cli", Revision: 4, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: missingProducer}); !IsReadinessKind(err, ReadinessInvalidFact) {
		t.Fatalf("producerless invariant accepted: %v", err)
	}
}

func TestCapabilityReadinessPreservesUnavailableFailedStaleWaivedAndAmbiguous(t *testing.T) {
	statuses := []PredicateStatus{PredicateUnavailable, PredicateFailed, PredicateStale, PredicateWaived, PredicateAmbiguous, PredicateSatisfied, PredicateInapplicable}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionIntegrationPublish, Predicates: []PredicateRequirement{invariantRequirement(PredicateRepositoryTarget, ActionIntegrationPublish)}})
			fact := unavailableFact(PredicateRepositoryTarget, ActionIntegrationPublish, "evidence-repository", ActionIntegrationPublish)
			fact.Status = status
			if status == PredicateWaived {
				fact.WaiverEvidenceIdentity = "waiver-human-44"
				fact.WaiverEvidenceDigest = testEvidenceDigest()
			}
			got, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-status", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: []PredicateFact{fact}})
			if err != nil {
				t.Fatal(err)
			}
			wantAuthorized := status == PredicateWaived || status == PredicateSatisfied || status == PredicateInapplicable
			if (len(got.Authorized()) == 1) != wantAuthorized {
				t.Fatalf("status=%s authorized=%v unavailable=%+v", status, got.Authorized(), got.Unavailable())
			}
			if !wantAuthorized {
				unavailable := got.Unavailable()
				if len(unavailable) != 1 || len(unavailable[0].Predicates) != 1 || unavailable[0].Predicates[0].Status != status {
					t.Fatalf("status=%s was collapsed in the readiness result: %+v", status, unavailable)
				}
			}
		})
	}
}

func TestCapabilityReadinessIsOrderIndependentAndCanonical(t *testing.T) {
	contracts := mustReadinessContracts(t,
		CapabilityContract{Action: ActionVerificationRun, Predicates: []PredicateRequirement{invariantRequirement(PredicateArtifactCurrent, ActionArtifactObserve)}},
		CapabilityContract{Action: ActionArtifactPublish},
		CapabilityContract{Action: ActionWorkFinalize, Predicates: []PredicateRequirement{invariantRequirement(PredicateWorkGraphComplete, ActionWorkGraphObserve)}},
	)
	facts := []PredicateFact{
		satisfiedFact(PredicateArtifactCurrent, ActionVerificationRun),
		unavailableFact(PredicateWorkGraphComplete, ActionWorkFinalize, "evidence-workgraph", ActionWorkGraphObserve),
		satisfiedFact(PredicateLifecycleAllows, ActionArtifactPublish),
	}
	one, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-order", Revision: 8, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(contracts)
	slices.Reverse(facts)
	two, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-order", Revision: 8, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one.CanonicalBytes(), two.CanonicalBytes()) {
		t.Fatalf("activity/input order changed readiness:\n%s\n%s", one.CanonicalBytes(), two.CanonicalBytes())
	}
}

func TestCapabilityReadinessLifecycleIsCoarseAndTerminal(t *testing.T) {
	contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionMissionCreate}, CapabilityContract{Action: ActionMissionResume}, CapabilityContract{Action: ActionArtifactPublish})
	facts := []PredicateFact{satisfiedFact(PredicateLifecycleAllows, ActionMissionCreate), satisfiedFact(PredicateLifecycleAllows, ActionMissionResume), satisfiedFact(PredicateLifecycleAllows, ActionArtifactPublish)}
	paused, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-paused", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionPaused, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paused.Authorized(), []ActionRef{testRef(ActionMissionResume)}) {
		t.Fatalf("paused mission authority=%v", paused.Authorized())
	}
	opened, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-open", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opened.Authorized(), []ActionRef{testRef(ActionArtifactPublish)}) {
		t.Fatalf("existing open mission re-authorized create/resume: %v", opened.Authorized())
	}
	terminal, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-terminal", Revision: 9, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionCompleted, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !terminal.Terminal() || terminal.Lifecycle() != MissionCompleted || len(terminal.Authorized()) != 0 || len(terminal.Unavailable()) != len(contracts) {
		t.Fatalf("terminal census=%s", terminal.CanonicalBytes())
	}
}

func TestCapabilityReadinessKeepsScopedWorkInstancesAndInapplicableActionsVisible(t *testing.T) {
	workA := AuthorityScope{Kind: ScopeWork, Identity: "work-a"}
	workB := AuthorityScope{Kind: ScopeWork, Identity: "work-b"}
	applicability := invariantRequirement(PredicateApplicable, ActionArtifactObserve)
	contracts := []CapabilityContract{
		{Action: ActionExecutionClaim, Scope: workA},
		{Action: ActionExecutionClaim, Scope: workB},
		{Action: ActionVerificationRun, Scope: AuthorityScope{Kind: ScopeCriterion, Identity: "criterion-no-api"}, Applicability: &applicability},
	}
	facts := []PredicateFact{{
		Kind: PredicateApplicable, Action: ActionVerificationRun, Scope: contracts[2].Scope,
		Status: PredicateInapplicable, Authority: testAuthority("project_policy"), EvidenceIdentity: "applicability-no-api", EvidenceDigest: testEvidenceDigest(),
		Producer: ActionArtifactObserve, Recovery: RecoveryNone,
	}}
	census, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-scopes", Revision: 3, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	want := []ActionRef{{ID: ActionExecutionClaim, Scope: workA}, {ID: ActionExecutionClaim, Scope: workB}}
	if !slices.Equal(census.Authorized(), want) || len(census.Assessments()) != 3 || census.Assessments()[2].Status != AssessmentInapplicable {
		t.Fatalf("scoped or inapplicable census collapsed: %s", census.CanonicalBytes())
	}
}

func TestCapabilityReadinessRejectsProjectionAndProseAsAuthority(t *testing.T) {
	contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionIntegrationPublish, Predicates: []PredicateRequirement{invariantRequirement(PredicateAcceptanceCurrent, ActionAcceptanceEvaluate)}})
	for _, source := range []string{"projection", "status.json", "markdown", "prompt", "tracker_prose", "git_commit_message", "capability_contract", "mission_blocker"} {
		fact := satisfiedFact(PredicateAcceptanceCurrent, ActionIntegrationPublish)
		fact.Authority.Family = source
		if _, err := EvaluateReadiness(ReadinessInput{MissionID: "mission-projection", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: []PredicateFact{fact}}); !IsReadinessKind(err, ReadinessInvalidFact) {
			t.Fatalf("non-authoritative source %q minted readiness: %v", source, err)
		}
	}
}

func mustReadinessContracts(t *testing.T, values ...CapabilityContract) []CapabilityContract {
	t.Helper()
	for index := range values {
		values[index].Scope = testScope()
	}
	return values
}

func invariantRequirement(kind PredicateKind, producer ActionID) PredicateRequirement {
	return PredicateRequirement{Kind: kind, Authority: InstructionInvariant, ResponsibleCapability: producer}
}

func satisfiedFact(kind PredicateKind, action ActionID) PredicateFact {
	return PredicateFact{Kind: kind, Action: action, Scope: testScope(), Status: PredicateSatisfied, Authority: testAuthority("mission_authority"), EvidenceIdentity: "evidence-" + string(kind) + "-" + string(action), EvidenceDigest: testEvidenceDigest(), Producer: action, Recovery: RecoveryNone}
}

func unavailableFact(kind PredicateKind, action ActionID, evidence string, producer ActionID) PredicateFact {
	return PredicateFact{Kind: kind, Action: action, Scope: testScope(), Status: PredicateUnavailable, Authority: testAuthority("mission_authority"), EvidenceIdentity: evidence, EvidenceDigest: testEvidenceDigest(), Producer: producer, Recovery: RecoveryRunProducer, Diagnostic: "required authority is unavailable"}
}

func testAuthority(family string) ReadinessAuthorityCoordinate {
	return ReadinessAuthorityCoordinate{Family: family, Identity: "authority-observation", Revision: 7}
}

func testSnapshotDigest() string { return strings.Repeat("a", 64) }

func testEvidenceDigest() string { return strings.Repeat("b", 64) }

func testScope() AuthorityScope { return AuthorityScope{Kind: ScopeMission, Identity: "mission-scope"} }

func testRef(action ActionID) ActionRef { return ActionRef{ID: action, Scope: testScope()} }
