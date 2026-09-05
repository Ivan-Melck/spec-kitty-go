package mission

import (
	"bytes"
	"context"
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
	input.Contracts = cloneContracts(input.Contracts)
	for index := range input.Contracts {
		if input.Contracts[index].Scope.Kind == ScopeMission {
			input.Contracts[index].Scope.Identity = input.MissionID
		}
	}
	definition := testCapabilityDefinition(t, input.Contracts)
	facts := slices.Clone(input.Facts)
	facts = slices.DeleteFunc(facts, func(fact PredicateFact) bool { return fact.Kind == PredicateLifecycleAllows })
	for index := range facts {
		facts[index].Authority.Revision = input.Revision
		if facts[index].Scope.Kind == ScopeMission {
			facts[index].Scope.Identity = input.MissionID
		}
	}
	for index := range input.Decisions {
		if input.Decisions[index].Affects[0].Scope.Kind == ScopeMission {
			input.Decisions[index].Affects[0].Scope.Identity = input.MissionID
		}
	}
	for index := range input.Blockers {
		if input.Blockers[index].Affects[0].Scope.Kind == ScopeMission {
			input.Blockers[index].Affects[0].Scope.Identity = input.MissionID
		}
	}
	for index := range input.Conflicts {
		if input.Conflicts[index].Affects[0].Scope.Kind == ScopeMission {
			input.Conflicts[index].Affects[0].Scope.Identity = input.MissionID
		}
	}
	capture, err := journalv2.NewStoreRevisionV2("database-readiness", input.Revision, input.Revision)
	if err != nil {
		t.Fatal(err)
	}
	observations := testObservationSet(t, capture, "project-readiness", journalv2.MissionID(input.MissionID), input.Contracts, facts)
	if !input.MissionExists {
		input.MissionExists = input.Lifecycle != ""
	}
	var snapshot ReadinessSnapshot
	if input.MissionExists {
		state := readinessStateForTestWithFacts(t, input.MissionID, input.Revision, input.Lifecycle, input.Decisions, input.Blockers, input.Conflicts)
		snapshot, err = SealReadinessSnapshot(readinessObservationForTest(t, state, capture, input.Revision), definition, observations)
	} else {
		snapshot, err = SealPrecreationReadinessSnapshot("project-readiness", journalv2.MissionID(input.MissionID), capture, definition, observations)
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
	if want := []ActionRef{testRefFor(ActionArtifactPublish, "mission-autonomy"), testRefFor(ActionVerificationRun, "mission-autonomy")}; !slices.Equal(got.Authorized(), want) {
		t.Fatalf("authorized=%v want=%v", got.Authorized(), want)
	}
	unavailable := got.Unavailable()
	if len(unavailable) != 1 || unavailable[0].Action != testRefFor(ActionWorkFinalize, "mission-autonomy") || len(unavailable[0].Predicates) != 1 {
		t.Fatalf("complete unavailable census lost: %+v", unavailable)
	}
	predicate := unavailable[0].Predicates[0]
	wantAuthority := testAuthority("workgraph_authority")
	wantAuthority.Revision = 9
	if predicate.Status != PredicateUnavailable || predicate.Authority != wantAuthority || predicate.EvidenceIdentity != "evidence-workgraph" || predicate.EvidenceDigest != testEvidenceDigest() || predicate.ResponsibleCapability != ActionWorkGraphObserve || predicate.Recovery != RecoveryRunProducer {
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
	if !slices.Equal(got.Authorized(), []ActionRef{testRefFor(ActionBlockerClear, "mission-scoped"), testRefFor(ActionDecisionResolve, "mission-scoped")}) {
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
	if !slices.Equal(paused.Authorized(), []ActionRef{testRefFor(ActionMissionResume, "mission-paused")}) {
		t.Fatalf("paused mission authority=%v", paused.Authorized())
	}
	opened, err := evaluateReadinessInput(t, ReadinessInput{MissionID: "mission-open", Revision: 2, SnapshotDigest: testSnapshotDigest(), Lifecycle: MissionOpen, Contracts: contracts, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(opened.Authorized(), []ActionRef{testRefFor(ActionArtifactPublish, "mission-open")}) {
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
	capture, _ := journalv2.NewStoreRevisionV2("database-readiness", 3, 3)
	observations := testCapabilityObservations(t, capture, "project-readiness", "mission-create", contracts, nil)
	if _, err := SealCapabilityObservationSetV2(capture, "project-readiness", "mission-create", observations[:len(observations)-1]); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("partial capability observation set was accepted: %v", err)
	}
	observationSet, err := SealCapabilityObservationSetV2(capture, "project-readiness", "mission-create", observations)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, census, err := func() (ReadinessSnapshot, ActionCensus, error) {
		snapshot, sealErr := SealPrecreationReadinessSnapshot("project-readiness", "mission-create", capture, definition, observationSet)
		if sealErr != nil {
			return ReadinessSnapshot{}, ActionCensus{}, sealErr
		}
		value, evaluateErr := EvaluateReadiness(snapshot)
		return snapshot, value, evaluateErr
	}()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(census.Authorized(), []ActionRef{testRefFor(ActionMissionCreate, "mission-create")}) || snapshot.SnapshotDigest() == testSnapshotDigest() {
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
	if _, err := observeCapabilityEvidenceForTest(capture, "project-readiness", "mission-future", fact.Action, nil, []PredicateFact{fact}, true, "absence", testEvidenceDigest()); !IsReadinessKind(err, ReadinessInvalidFact) {
		t.Fatalf("future observation entered one-revision snapshot: %v", err)
	}
}

func TestReadinessSealBindsDatabaseMissionAndExactCaptureAndDerivesMissionScope(t *testing.T) {
	capture, _ := journalv2.NewStoreRevisionV2("database-authority-a", 7, 7)
	contracts := mustReadinessContracts(t, CapabilityContract{Action: ActionIntegrationPublish})
	definition := testCapabilityDefinition(t, contracts)
	observations := testObservationSet(t, capture, "project-readiness", "mission-bound", contracts, nil)
	state := readinessStateForTest(t, "mission-bound", 7, MissionOpen)
	snapshot, err := SealReadinessSnapshot(readinessObservationForTest(t, state, capture, 7), definition, observations)
	if err != nil {
		t.Fatal(err)
	}
	census, err := EvaluateReadiness(snapshot)
	if err != nil || snapshot.DatabaseID() != "database-authority-a" || !slices.Contains(census.Authorized(), testRefFor(ActionIntegrationPublish, "mission-bound")) {
		t.Fatalf("mission scope was not derived from exact observation: census=%v database=%s err=%v", census.Authorized(), snapshot.DatabaseID(), err)
	}

	wrongDatabase, _ := journalv2.NewStoreRevisionV2("database-authority-b", 7, 7)
	if _, err := SealReadinessSnapshot(readinessObservationForTest(t, state, wrongDatabase, 7), definition, observations); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("cross-database capability evidence entered snapshot: %v", err)
	}
	if _, err := NewReadinessAuthorityObservationV2(state, capture, 8, ""); !IsMissionAuthorityKindV2(err, MissionAuthorityInvalidRequestV2) {
		t.Fatalf("future record revision entered snapshot: %v", err)
	}
	wrongSource := capabilityReadinessSourceFuncV2(func(_ context.Context, query CapabilityReadinessQueryV2) (CapabilityReadinessEvidenceV2, error) {
		return CapabilityReadinessEvidenceV2{
			Capture: wrongDatabase, ProjectID: query.ProjectID(), MissionID: query.MissionID(), Action: query.Action(),
			ObservedAbsent: true, EvidenceIdentity: "absence", EvidenceDigest: testEvidenceDigest(),
		}, nil
	})
	wrongObserver, _ := NewCapabilityAuthorityObserverV2(wrongSource)
	if _, err := wrongObserver.Observe(context.Background(), capture, "project-readiness", "mission-bound", ActionExecutionClaim); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("capability reader echoed a different database capture: %v", err)
	}
	if _, err := observeCapabilityEvidenceForTest(capture, "project-readiness", "mission-bound", ActionExecutionClaim, nil, nil, true, "", ""); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("empty dynamic membership without absence evidence was accepted: %v", err)
	}
	forgedMissionScope, err := observeCapabilityEvidenceForTest(capture, "project-readiness", "mission-bound", ActionIntegrationPublish, []AuthorityScope{{Kind: ScopeMission, Identity: "mission-forged"}}, nil, false, "", "")
	if err != nil {
		t.Fatal(err)
	}
	values := testCapabilityObservations(t, capture, "project-readiness", "mission-bound", contracts, nil)
	for index := range values {
		if values[index].action == ActionIntegrationPublish {
			values[index] = forgedMissionScope
		}
	}
	forgedSet, err := SealCapabilityObservationSetV2(capture, "project-readiness", "mission-bound", values)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SealReadinessSnapshot(readinessObservationForTest(t, state, capture, 7), definition, forgedSet); !IsReadinessKind(err, ReadinessInvalidInput) {
		t.Fatalf("caller-provided mission scope replaced derived mission authority: %v", err)
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

func testRefFor(action ActionID, missionID string) ActionRef {
	return ActionRef{ID: action, Scope: AuthorityScope{Kind: ScopeMission, Identity: missionID}}
}

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
			// Unmentioned actions use an empty dynamic scope in unit fixtures so
			// each test can isolate the contracts it is exercising.
			templates = append(templates, CapabilityTemplate{Action: action, ScopeKind: ScopeAction})
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

func testCapabilityObservations(t *testing.T, capture journalv2.StoreRevisionV2, projectID journalv2.ProjectID, missionID journalv2.MissionID, contracts []CapabilityContract, facts []PredicateFact) []CapabilityAuthorityObservationV2 {
	t.Helper()
	values := make([]CapabilityAuthorityObservationV2, 0, len(allActionIDs))
	for _, action := range allActionIDs {
		var scopes []AuthorityScope
		for _, contract := range contracts {
			if contract.Action == action {
				if contract.Scope.Kind != ScopeMission {
					scopes = append(scopes, contract.Scope)
				}
			}
		}
		var actionFacts []PredicateFact
		for _, fact := range facts {
			if fact.Action == action {
				actionFacts = append(actionFacts, fact)
			}
		}
		absenceID, absenceDigest := "", ""
		if len(scopes) == 0 {
			absenceID, absenceDigest = "absence-"+string(action), testEvidenceDigest()
		}
		observation, err := observeCapabilityEvidenceForTest(capture, projectID, missionID, action, scopes, actionFacts, len(scopes) == 0, absenceID, absenceDigest)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, observation)
	}
	return values
}

type capabilityReadinessSourceFuncV2 func(context.Context, CapabilityReadinessQueryV2) (CapabilityReadinessEvidenceV2, error)

func (f capabilityReadinessSourceFuncV2) ObserveCapabilityReadiness(ctx context.Context, query CapabilityReadinessQueryV2) (CapabilityReadinessEvidenceV2, error) {
	return f(ctx, query)
}

func observeCapabilityEvidenceForTest(capture journalv2.StoreRevisionV2, projectID journalv2.ProjectID, missionID journalv2.MissionID, action ActionID, scopes []AuthorityScope, facts []PredicateFact, observedAbsent bool, evidenceIdentity, evidenceDigest string) (CapabilityAuthorityObservationV2, error) {
	source := capabilityReadinessSourceFuncV2(func(_ context.Context, query CapabilityReadinessQueryV2) (CapabilityReadinessEvidenceV2, error) {
		return CapabilityReadinessEvidenceV2{
			Capture: query.Capture(), ProjectID: query.ProjectID(), MissionID: query.MissionID(), Action: query.Action(),
			Scopes: scopes, Facts: facts, ObservedAbsent: observedAbsent, EvidenceIdentity: evidenceIdentity, EvidenceDigest: evidenceDigest,
		}, nil
	})
	observer, err := NewCapabilityAuthorityObserverV2(source)
	if err != nil {
		return CapabilityAuthorityObservationV2{}, err
	}
	return observer.Observe(context.Background(), capture, projectID, missionID, action)
}

func testObservationSet(t *testing.T, capture journalv2.StoreRevisionV2, projectID journalv2.ProjectID, missionID journalv2.MissionID, contracts []CapabilityContract, facts []PredicateFact) CapabilityObservationSetV2 {
	t.Helper()
	census, err := SealCapabilityObservationSetV2(capture, projectID, missionID, testCapabilityObservations(t, capture, projectID, missionID, contracts, facts))
	if err != nil {
		t.Fatal(err)
	}
	return census
}

func readinessStateForTest(t *testing.T, missionID string, storeRevision uint64, lifecycle MissionLifecycle) ReadinessAuthorityStateV2 {
	return readinessStateForTestWithFacts(t, missionID, storeRevision, lifecycle, nil, nil, nil)
}

func readinessObservationForTest(t *testing.T, state ReadinessAuthorityStateV2, capture journalv2.StoreRevisionV2, recordRevision uint64) ReadinessAuthorityObservationV2 {
	t.Helper()
	observation, err := NewReadinessAuthorityObservationV2(state, capture, recordRevision, "")
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func readinessStateForTestWithFacts(t *testing.T, missionID string, storeRevision uint64, lifecycle MissionLifecycle, decisions []ReadinessDecision, blockers []ReadinessBlocker, conflicts []ReadinessConflict) ReadinessAuthorityStateV2 {
	t.Helper()
	digest, err := journalv2.ParseDigest("definition", testEvidenceDigest())
	if err != nil {
		t.Fatal(err)
	}
	active := make([]ScopedMissionFactV2, 0, len(decisions)+len(blockers)+len(conflicts))
	for _, value := range decisions {
		active = append(active, ScopedMissionFactV2{Kind: ScopedFactDecisionV2, FactID: value.ID, Scope: value.Affects[0], EvidenceIdentity: value.EvidenceIdentity, EvidenceDigest: value.EvidenceDigest})
	}
	for _, value := range blockers {
		active = append(active, ScopedMissionFactV2{Kind: ScopedFactBlockerV2, FactID: value.ID, Scope: value.Affects[0], EvidenceIdentity: value.EvidenceIdentity, EvidenceDigest: value.EvidenceDigest})
	}
	for _, value := range conflicts {
		active = append(active, ScopedMissionFactV2{Kind: ScopedFactConflictV2, FactID: value.ID, Scope: value.Affects[0], EvidenceIdentity: value.EvidenceIdentity, EvidenceDigest: value.EvidenceDigest})
	}
	state, err := NewReadinessAuthorityStateV2(ReadinessAuthorityStateInputV2{
		ProjectID: "project-readiness", MissionID: journalv2.MissionID(missionID), DefinitionRevision: "definition-readiness",
		DefinitionDigest: digest, Version: journalv2.LogicalVersion(storeRevision), Lifecycle: lifecycle, ActiveFacts: active, LastOperationID: "operation-readiness-create", ObservedAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}
