package mission

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// ReadinessInput is test authoring syntax only. Production evaluation accepts
// the opaque ReadinessSnapshot returned by the complete authority seal.
type ReadinessInput struct {
	MissionID      string
	MissionExists  bool
	Revision       uint64
	Lifecycle      MissionLifecycle
	Contracts      []CapabilityContract
	Facts          []PredicateFact
	Decisions      []ReadinessDecision
	Blockers       []ReadinessBlocker
	Conflicts      []ReadinessConflict
	SnapshotDigest string
}

func evaluateReadinessInput(t *testing.T, input ReadinessInput) (ActionCensus, error) {
	t.Helper()
	definition := testCapabilityDefinition(t, input.Contracts)
	scopes := testScopeCensus(t, input.MissionID, input.Revision, input.Contracts)
	facts := slices.Clone(input.Facts)
	facts = slices.DeleteFunc(facts, func(fact PredicateFact) bool { return fact.Kind == PredicateLifecycleAllows })
	evidence := ReadinessEvidence{Facts: facts, Decisions: input.Decisions, Blockers: input.Blockers, Conflicts: input.Conflicts}
	if !input.MissionExists {
		input.MissionExists = input.Lifecycle != ""
	}
	var snapshot ReadinessSnapshot
	var err error
	if input.MissionExists {
		state := readinessStateForTest(t, input.MissionID, input.Revision, input.Lifecycle)
		snapshot, err = SealReadinessSnapshot(ReadinessAuthorityObservationV2{State: state, StoreRevision: input.Revision, RecordRevision: input.Revision}, definition, scopes, evidence)
	} else {
		snapshot, err = SealPrecreationReadinessSnapshot(input.MissionID, input.Revision, definition, scopes, evidence)
	}
	if err != nil {
		return ActionCensus{}, err
	}
	return EvaluateReadiness(snapshot)
}

func TestCapabilityReadinessReturnsCompleteCensusAndParallelActions(t *testing.T) {
	contracts := mustReadinessContracts(t,
		CapabilityContract{Action: ActionArtifactPublish},
		CapabilityContract{Action: ActionVerificationRun},
		CapabilityContract{Action: ActionWorkFinalize, Predicates: []PredicateRequirement{
			invariantRequirement(PredicateWorkGraphCurrent, ActionWorkGraphObserve),
		}},
	)
	facts := []PredicateFact{
		satisfiedFact(PredicateLifecycleAllows, ActionArtifactPublish),
		satisfiedFact(PredicateLifecycleAllows, ActionVerificationRun),
		unavailableFact(PredicateWorkGraphCurrent, ActionWorkFinalize, "evidence-workgraph", ActionWorkGraphObserve),
	}

	got, err := evaluateReadinessInput(t, ReadinessInput{
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
	if predicate.Status != PredicateUnavailable || predicate.Authority != testAuthority("workgraph_authority") || predicate.EvidenceIdentity != "evidence-workgraph" || predicate.EvidenceDigest != testEvidenceDigest() || predicate.ResponsibleCapability != ActionWorkGraphObserve || predicate.Recovery != RecoveryRunProducer {
		t.Fatalf("blocking predicate is not actionable: %+v", predicate)
	}
	if !lowerHexDigest(got.SnapshotDigest()) || got.SnapshotDigest() == testSnapshotDigest() {
		t.Fatalf("snapshot digest was not derived from sealed membership: %q", got.SnapshotDigest())
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

	got, err := evaluateReadinessInput(t, input)
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
	applicability := invariantRequirement(PredicateApplicable, ActionArtifactObserve)
	contracts := mustReadinessContracts(t,
		CapabilityContract{Action: ActionAcceptanceEvaluate, Applicability: &applicability, Predicates: []PredicateRequirement{
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
	got, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-cli", Revision: 4, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Authorized()) != 0 || len(got.Assessments()) != 1 || got.Assessments()[0].Status != AssessmentInapplicable {
		t.Fatalf("inapplicable action crossed the execution boundary: %s", got.CanonicalBytes())
	}

	missingProducer := slices.Clone(facts)
	missingProducer[1].Producer = ""
	if _, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-cli", Revision: 4, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: missingProducer}); !IsReadinessKind(err, ReadinessInvalidFact) {
		t.Fatalf("producerless invariant accepted: %v", err)
	}
}

func TestCapabilityReadinessPreservesUnavailableFailedStaleWaivedAndAmbiguous(t *testing.T) {
	statuses := []PredicateStatus{PredicateUnavailable, PredicateFailed, PredicateStale, PredicateWaived, PredicateAmbiguous, PredicateSatisfied}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionIntegrationPublish, Predicates: []PredicateRequirement{invariantRequirement(PredicateRepositoryTarget, ActionIntegrationPublish)}})
			fact := unavailableFact(PredicateRepositoryTarget, ActionIntegrationPublish, "evidence-repository", ActionIntegrationPublish)
			fact.Status = status
			if status == PredicateWaived {
				fact.WaiverEvidenceIdentity = "waiver-human-44"
				fact.WaiverEvidenceDigest = testEvidenceDigest()
			}
			got, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-status", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: []PredicateFact{fact}})
			if err != nil {
				t.Fatal(err)
			}
			wantAuthorized := status == PredicateWaived || status == PredicateSatisfied
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
	invalid := unavailableFact(PredicateRepositoryTarget, ActionIntegrationPublish, "evidence-repository", ActionIntegrationPublish)
	invalid.Status = PredicateInapplicable
	if _, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-status", Revision: 2, Lifecycle: MissionOpen, Contracts: mustReadinessContracts(t, CapabilityContract{Action: ActionIntegrationPublish, Predicates: []PredicateRequirement{invariantRequirement(PredicateRepositoryTarget, ActionIntegrationPublish)}}), Facts: []PredicateFact{invalid}}); !IsReadinessKind(err, ReadinessInvalidFact) {
		t.Fatalf("ordinary safety predicate accepted inapplicable status: %v", err)
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
	one, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-order", Revision: 8, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(contracts)
	slices.Reverse(facts)
	two, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-order", Revision: 8, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
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
	paused, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-paused", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionPaused, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paused.Authorized(), []ActionRef{testRef(ActionMissionResume)}) {
		t.Fatalf("paused mission authority=%v", paused.Authorized())
	}
	opened, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-open", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opened.Authorized(), []ActionRef{testRef(ActionArtifactPublish)}) {
		t.Fatalf("existing open mission re-authorized create/resume: %v", opened.Authorized())
	}
	terminal, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-terminal", Revision: 9, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionCompleted, Contracts: contracts, Facts: facts})
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
	census, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-scopes", Revision: 3, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
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
		if _, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-projection", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: []PredicateFact{fact}}); !IsReadinessKind(err, ReadinessInvalidFact) {
			t.Fatalf("non-authoritative source %q minted readiness: %v", source, err)
		}
	}
}

func TestReadinessRequiresCompleteSealedDefinitionScopeCensusAndAuthorityMembership(t *testing.T) {
	if _, err := EvaluateReadiness(ReadinessSnapshot{}); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("zero/unsealed snapshot entered evaluation: %v", err)
	}
	if _, err := NewCapabilityDefinition([]CapabilityTemplate{{Action: ActionMissionCreate, ScopeKind: ScopeMission}}); !IsReadinessKind(err, ReadinessInvalidContract) {
		t.Fatalf("partial action definition was accepted: %v", err)
	}
	contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionMissionCreate})
	definition := testCapabilityDefinition(t, contracts)
	sets := testScopeCensus(t, "mission-create", 3, contracts).Sets()
	if _, err := NewCapabilityScopeCensus(testAuthority("mission_authority"), "scope-census", testEvidenceDigest(), sets[:len(sets)-1]); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("partial scope census was accepted: %v", err)
	}
	scopes := testScopeCensus(t, "mission-create", 3, contracts)
	snapshot, census, err := func() (ReadinessSnapshot, ActionCensus, error) {
		snapshot, sealErr := SealPrecreationReadinessSnapshot("mission-create", 3, definition, scopes, ReadinessEvidence{})
		if sealErr != nil {
			return ReadinessSnapshot{}, ActionCensus{}, sealErr
		}
		value, evaluateErr := EvaluateReadiness(snapshot)
		return snapshot, value, evaluateErr
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(census.Authorized(), []ActionRef{testRef(ActionMissionCreate)}) || snapshot.SnapshotDigest() == testSnapshotDigest() {
		t.Fatalf("precreation census did not derive mission.create authority: %s", census.CanonicalBytes())
	}

	producerContract := mustReadinessContracts(t, CapabilityContract{Action: ActionAcceptanceEvaluate, Predicates: []PredicateRequirement{invariantRequirement(PredicateProjectVerification, ActionVerificationRun)}})
	fact := satisfiedFact(PredicateProjectVerification, ActionAcceptanceEvaluate)
	fact.Producer = ActionArtifactPublish
	if _, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-producer", Revision: 3, Lifecycle: MissionOpen, Contracts: producerContract, Facts: []PredicateFact{fact}}); !IsReadinessKind(err, ReadinessInvalidFact) {
		t.Fatalf("wrong producer minted predicate authority: %v", err)
	}
	fact = satisfiedFact(PredicateProjectVerification, ActionAcceptanceEvaluate)
	fact.Authority.Revision = 4
	if _, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-future", Revision: 3, Lifecycle: MissionOpen, Contracts: producerContract, Facts: []PredicateFact{fact}}); !IsReadinessKind(err, ReadinessInvalidFact) {
		t.Fatalf("future observation entered one-revision snapshot: %v", err)
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
	return PredicateFact{Kind: kind, Action: action, Scope: testScope(), Status: PredicateSatisfied, Authority: testAuthority(predicateAuthorityFamily(kind)), EvidenceIdentity: "evidence-" + string(kind) + "-" + string(action), EvidenceDigest: testEvidenceDigest(), Producer: defaultPredicateProducer(kind), Recovery: RecoveryNone}
}

func unavailableFact(kind PredicateKind, action ActionID, evidence string, producer ActionID) PredicateFact {
	return PredicateFact{Kind: kind, Action: action, Scope: testScope(), Status: PredicateUnavailable, Authority: testAuthority(predicateAuthorityFamily(kind)), EvidenceIdentity: evidence, EvidenceDigest: testEvidenceDigest(), Producer: producer, Recovery: RecoveryRunProducer, Diagnostic: "required authority is unavailable"}
}

func defaultPredicateProducer(kind PredicateKind) ActionID {
	switch kind {
	case PredicateApplicable, PredicateArtifactCurrent:
		return ActionArtifactObserve
	case PredicateWorkGraphComplete, PredicateWorkGraphCurrent, PredicateExecutionEligible:
		return ActionWorkGraphObserve
	case PredicateLeaseCurrent, PredicateLeaseExpired:
		return ActionExecutionHeartbeat
	case PredicateRuntimeEpochCurrent, PredicatePolicyCurrent:
		return ActionPolicyResolve
	case PredicateWorkspaceBound:
		return ActionWorkspaceAllocate
	case PredicateCandidateCurrent:
		return ActionCandidateHandoff
	case PredicateIndependentReview:
		return ActionReviewRecord
	case PredicateAcceptanceCurrent:
		return ActionAcceptanceEvaluate
	case PredicateRepositoryTarget:
		return ActionIntegrationPublish
	case PredicateExternalEffectSafe:
		return ActionEffectReconcile
	case PredicateConflictAbsent:
		return ActionConflictResolve
	case PredicateProjectVerification:
		return ActionVerificationRun
	default:
		return actionForSyntheticPredicate(kind)
	}
}

func actionForSyntheticPredicate(kind PredicateKind) ActionID {
	if kind == PredicateDecisionResolved {
		return ActionDecisionResolve
	}
	if kind == PredicateBlockerCleared {
		return ActionBlockerClear
	}
	return ActionPolicyResolve
}

func testAuthority(family string) ReadinessAuthorityCoordinate {
	return ReadinessAuthorityCoordinate{Family: family, Identity: "authority-observation", Revision: 1}
}

func testSnapshotDigest() string { return strings.Repeat("a", 64) }

func testEvidenceDigest() string { return strings.Repeat("b", 64) }

func testScope() AuthorityScope { return AuthorityScope{Kind: ScopeMission, Identity: "mission-scope"} }

func testRef(action ActionID) ActionRef { return ActionRef{ID: action, Scope: testScope()} }

func testCapabilityDefinition(t *testing.T, contracts []CapabilityContract) CapabilityDefinition {
	t.Helper()
	templates := make([]CapabilityTemplate, 0, len(allActionIDs))
	byAction := make(map[ActionID]CapabilityContract)
	for _, contract := range contracts {
		if previous, ok := byAction[contract.Action]; ok {
			if previous.Scope.Kind != contract.Scope.Kind {
				t.Fatal("test action uses multiple scope kinds")
			}
			continue
		}
		byAction[contract.Action] = contract
	}
	for _, action := range allActionIDs {
		contract, ok := byAction[action]
		if !ok {
			templates = append(templates, CapabilityTemplate{Action: action, ScopeKind: ScopeMission})
			continue
		}
		templates = append(templates, CapabilityTemplate{Action: action, ScopeKind: contract.Scope.Kind, Applicability: contract.Applicability, Predicates: contract.Predicates})
	}
	definition, err := NewCapabilityDefinition(templates)
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func testScopeCensus(t *testing.T, missionID string, revision uint64, contracts []CapabilityContract) CapabilityScopeCensus {
	t.Helper()
	sets := make([]CapabilityScopeSet, 0, len(allActionIDs))
	for _, action := range allActionIDs {
		entry := CapabilityScopeSet{Action: action}
		for _, contract := range contracts {
			if contract.Action == action {
				entry.Scopes = append(entry.Scopes, contract.Scope)
			}
		}
		sets = append(sets, entry)
	}
	census, err := NewCapabilityScopeCensus(ReadinessAuthorityCoordinate{Family: "mission_authority", Identity: missionID, Revision: 1}, "scope-census", testEvidenceDigest(), sets)
	if err != nil {
		t.Fatal(err)
	}
	return census
}

func readinessStateForTest(t *testing.T, missionID string, storeRevision uint64, lifecycle MissionLifecycle) ReadinessAuthorityStateV2 {
	t.Helper()
	digest, err := journalv2.ParseDigest("definition", testEvidenceDigest())
	if err != nil {
		t.Fatal(err)
	}
	state, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: "project-readiness", MissionID: journalv2.MissionID(missionID), DefinitionRevision: "definition-readiness",
		DefinitionDigest: digest, Version: journalv2.LogicalVersion(storeRevision), Lifecycle: lifecycle, LastOperationID: "operation-readiness-create", ObservedAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}
