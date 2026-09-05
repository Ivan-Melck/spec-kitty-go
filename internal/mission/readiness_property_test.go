package mission

import (
	"bytes"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

func TestAutonomousReadinessModelAcrossPermutationsAndScopedFacts(t *testing.T) {
	statuses := []PredicateStatus{
		PredicateSatisfied, PredicateUnavailable, PredicateFailed, PredicateStale,
		PredicateWaived, PredicateAmbiguous,
	}
	lifecycles := []MissionLifecycle{MissionOpen, MissionPaused, MissionCompleted, MissionCanceled, MissionFailed}
	for seed := int64(0); seed < 256; seed++ {
		t.Run(fmt.Sprintf("seed-%03d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic property-test permutations.
			workA := AuthorityScope{Kind: ScopeWork, Identity: "work-a"}
			workB := AuthorityScope{Kind: ScopeWork, Identity: "work-b"}
			criterion := AuthorityScope{Kind: ScopeCriterion, Identity: "criterion-project-verification"}
			missionScope := AuthorityScope{Kind: ScopeMission, Identity: "mission-model"}
			contracts := []CapabilityContract{
				{Action: ActionExecutionClaim, Scope: workA},
				{Action: ActionExecutionClaim, Scope: workB},
				{Action: ActionVerificationRun, Scope: criterion, Predicates: []PredicateRequirement{invariantRequirement(PredicateProjectVerification, ActionVerificationRun)}},
				{Action: ActionDecisionResolve, Scope: missionScope},
				{Action: ActionBlockerClear, Scope: missionScope},
				{Action: ActionConflictResolve, Scope: missionScope},
				{Action: ActionMissionCancel, Scope: missionScope},
				{Action: ActionMissionResume, Scope: missionScope},
			}
			status := statuses[seed%int64(len(statuses))]
			fact := PredicateFact{
				Kind: PredicateProjectVerification, Action: ActionVerificationRun, Scope: criterion,
				Status: status, Authority: testAuthority("verifier_authority"), EvidenceIdentity: "evidence-verifier", EvidenceDigest: testEvidenceDigest(),
				Producer: ActionVerificationRun, Recovery: RecoveryNone,
			}
			if status.blocks() {
				fact.Recovery = RecoveryRunProducer
			}
			if status == PredicateWaived {
				fact.WaiverEvidenceIdentity = "waiver-human-policy"
				fact.WaiverEvidenceDigest = testEvidenceDigest()
			}
			input := ReadinessInput{
				MissionID: "mission-model", Revision: uint64(seed + 1), SnapshotDigest: testSnapshotDigest(), Lifecycle: lifecycles[seed%int64(len(lifecycles))],
				Contracts: contracts, Facts: []PredicateFact{fact},
			}
			workARef := ActionRef{ID: ActionExecutionClaim, Scope: workA}
			workBRef := ActionRef{ID: ActionExecutionClaim, Scope: workB}
			if seed&1 != 0 {
				input.Decisions = []ReadinessDecision{{ID: "decision-rework", Affects: []ActionRef{workARef}, Authority: testAuthority("mission_decision"), EvidenceIdentity: "evidence-decision", EvidenceDigest: testEvidenceDigest()}}
			}
			if seed&2 != 0 {
				input.Blockers = []ReadinessBlocker{{ID: "blocker-dependency", Affects: []ActionRef{workBRef}, Authority: testAuthority("mission_blocker"), EvidenceIdentity: "evidence-blocker", EvidenceDigest: testEvidenceDigest(), ResponsibleCapability: ActionBlockerClear, Recovery: RecoveryRestoreDependency}}
			}
			if seed&4 != 0 {
				input.Conflicts = []ReadinessConflict{{ID: "conflict-work-a", Affects: []ActionRef{workARef}, Authority: testAuthority("mission_conflict"), EvidenceIdentity: "evidence-conflict", EvidenceDigest: testEvidenceDigest()}}
			}

			baseline, err := evaluateReadinessInput(t, input)
			if err != nil {
				t.Fatal(err)
			}
			want := readinessModelAuthorized(input, status, workARef, workBRef, criterion, missionScope)
			if !slices.Equal(baseline.Authorized(), want) || len(baseline.Assessments()) != len(contracts) {
				t.Fatalf("model mismatch: lifecycle=%s status=%s got=%v want=%v census=%s", input.Lifecycle, status, baseline.Authorized(), want, baseline.CanonicalBytes())
			}

			permuted := input
			permuted.Contracts = slices.Clone(input.Contracts)
			permuted.Facts = slices.Clone(input.Facts)
			permuted.Decisions = cloneDecisions(input.Decisions)
			permuted.Blockers = cloneBlockers(input.Blockers)
			permuted.Conflicts = cloneConflicts(input.Conflicts)
			random.Shuffle(len(permuted.Contracts), func(i, j int) {
				permuted.Contracts[i], permuted.Contracts[j] = permuted.Contracts[j], permuted.Contracts[i]
			})
			for index := range permuted.Decisions {
				random.Shuffle(len(permuted.Decisions[index].Affects), func(i, j int) {
					permuted.Decisions[index].Affects[i], permuted.Decisions[index].Affects[j] = permuted.Decisions[index].Affects[j], permuted.Decisions[index].Affects[i]
				})
			}
			again, err := evaluateReadinessInput(t, permuted)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(baseline.CanonicalBytes(), again.CanonicalBytes()) {
				t.Fatalf("input order changed readiness:\n%s\n%s", baseline.CanonicalBytes(), again.CanonicalBytes())
			}
		})
	}
}

func readinessModelAuthorized(input ReadinessInput, verificationStatus PredicateStatus, workA, workB ActionRef, criterion, missionScope AuthorityScope) []ActionRef {
	if input.Lifecycle.Terminal() {
		return nil
	}
	if input.Lifecycle == MissionPaused {
		return []ActionRef{{ID: ActionMissionResume, Scope: missionScope}}
	}
	authorized := []ActionRef{
		workA, workB,
		{ID: ActionVerificationRun, Scope: criterion},
		{ID: ActionDecisionResolve, Scope: missionScope},
		{ID: ActionBlockerClear, Scope: missionScope},
		{ID: ActionConflictResolve, Scope: missionScope},
		{ID: ActionMissionCancel, Scope: missionScope},
	}
	blocked := make(map[string]bool)
	if len(input.Decisions) != 0 || len(input.Conflicts) != 0 {
		blocked[actionRefKey(workA)] = true
	}
	if len(input.Blockers) != 0 {
		blocked[actionRefKey(workB)] = true
	}
	if verificationStatus.blocks() {
		blocked[actionRefKey(ActionRef{ID: ActionVerificationRun, Scope: criterion})] = true
	}
	authorized = slices.DeleteFunc(authorized, func(ref ActionRef) bool { return blocked[actionRefKey(ref)] })
	sort.Slice(authorized, func(i, j int) bool { return actionRefKey(authorized[i]) < actionRefKey(authorized[j]) })
	return authorized
}

func TestAutonomousDriverSelectsOnlyAuthorizedCapabilitiesUntilTerminalConvergence(t *testing.T) {
	var terminal []byte
	for seed := int64(0); seed < 32; seed++ {
		got := runAutonomousReadinessDriver(t, seed, false)
		if terminal == nil {
			terminal = got
		} else if !bytes.Equal(terminal, got) {
			t.Fatalf("legal capability order %d changed terminal census", seed)
		}
	}
	canceled := runAutonomousReadinessDriver(t, 99, true)
	if bytes.Equal(terminal, canceled) {
		t.Fatal("cancellation collapsed into successful completion")
	}
}

type autonomousReadinessModel struct {
	lifecycle      MissionLifecycle
	pausedOnce     bool
	decisionOpen   bool
	blockerOpen    bool
	reconciled     bool
	claimed        map[string]bool
	reviewAttempts map[string]int
	approved       map[string]bool
	verifierTries  int
	verifierStatus PredicateStatus
	conflictOpen   bool
	accepted       bool
	integrated     bool
	steps          uint64
}

func runAutonomousReadinessDriver(t *testing.T, seed int64, cancel bool) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic legal-order exploration.
	work := []AuthorityScope{{Kind: ScopeWork, Identity: "work-a"}, {Kind: ScopeWork, Identity: "work-b"}}
	if random.Intn(2) == 1 {
		slices.Reverse(work)
	}
	missionScope := AuthorityScope{Kind: ScopeMission, Identity: "mission-driver"}
	criterion := AuthorityScope{Kind: ScopeCriterion, Identity: "criterion-driver"}
	actionScope := AuthorityScope{Kind: ScopeAction, Identity: "driver-recovery"}
	contracts := []CapabilityContract{
		{Action: ActionMissionPause, Scope: missionScope}, {Action: ActionMissionResume, Scope: missionScope},
		{Action: ActionMissionCancel, Scope: missionScope},
		{Action: ActionMissionComplete, Scope: missionScope, Predicates: []PredicateRequirement{invariantRequirement(PredicateAcceptanceCurrent, ActionAcceptanceEvaluate)}},
		{Action: ActionDecisionResolve, Scope: actionScope}, {Action: ActionBlockerClear, Scope: actionScope},
		{Action: ActionConflictResolve, Scope: actionScope}, {Action: ActionWorkReconcile, Scope: missionScope},
		{Action: ActionVerificationRun, Scope: criterion, Predicates: []PredicateRequirement{invariantRequirement(PredicateArtifactCurrent, ActionArtifactObserve)}},
		{Action: ActionAcceptanceEvaluate, Scope: missionScope, Predicates: []PredicateRequirement{
			invariantRequirement(PredicateWorkGraphCurrent, ActionWorkGraphObserve),
			invariantRequirement(PredicateIndependentReview, ActionReviewRecord),
			invariantRequirement(PredicateProjectVerification, ActionVerificationRun),
			invariantRequirement(PredicatePolicyCurrent, ActionPolicyResolve),
		}},
		{Action: ActionIntegrationPublish, Scope: missionScope, Predicates: []PredicateRequirement{
			invariantRequirement(PredicateAcceptanceCurrent, ActionAcceptanceEvaluate),
			invariantRequirement(PredicateRepositoryTarget, ActionIntegrationPublish),
			invariantRequirement(PredicateConflictAbsent, ActionConflictResolve),
		}},
	}
	for _, scope := range work {
		contracts = append(contracts,
			CapabilityContract{Action: ActionExecutionClaim, Scope: scope, Predicates: []PredicateRequirement{
				invariantRequirement(PredicateWorkGraphCurrent, ActionWorkGraphObserve),
				invariantRequirement(PredicateExecutionEligible, ActionWorkGraphObserve),
				invariantRequirement(PredicateRuntimeEpochCurrent, ActionPolicyResolve),
			}},
			CapabilityContract{Action: ActionReviewRecord, Scope: scope, Predicates: []PredicateRequirement{
				invariantRequirement(PredicateCandidateCurrent, ActionCandidateHandoff),
				invariantRequirement(PredicateIndependentReview, ActionReviewRecord),
			}},
		)
	}
	model := autonomousReadinessModel{lifecycle: MissionOpen, decisionOpen: true, blockerOpen: true, claimed: map[string]bool{}, reviewAttempts: map[string]int{}, approved: map[string]bool{}, verifierStatus: PredicateUnavailable, conflictOpen: true}
	for model.steps = 1; model.steps < 64; model.steps++ {
		input := autonomousReadinessInput(contracts, work, missionScope, criterion, actionScope, model)
		census, err := evaluateReadinessInput(t, input)
		if err != nil {
			t.Fatal(err)
		}
		// A restart reconstructs from the same immutable authority membership;
		// activity/input order is not scheduling authority.
		restarted := input
		restarted.Contracts = slices.Clone(input.Contracts)
		restarted.Facts = slices.Clone(input.Facts)
		slices.Reverse(restarted.Contracts)
		slices.Reverse(restarted.Facts)
		afterRestart, err := evaluateReadinessInput(t, restarted)
		if err != nil || !bytes.Equal(census.CanonicalBytes(), afterRestart.CanonicalBytes()) {
			t.Fatalf("restart changed census at step %d: %v", model.steps, err)
		}
		if model.lifecycle.Terminal() {
			if len(census.Authorized()) != 0 {
				t.Fatalf("terminal mission retained authority: %v", census.Authorized())
			}
			return census.CanonicalBytes()
		}
		if cancel && model.steps == 3 {
			applyAutonomousAction(t, census, ActionRef{ID: ActionMissionCancel, Scope: missionScope}, &model)
			continue
		}
		chosen := chooseAutonomousAction(work, missionScope, criterion, actionScope, model)
		applyAutonomousAction(t, census, chosen, &model)
	}
	t.Fatal("autonomous capability driver did not converge")
	return nil
}

func autonomousReadinessInput(contracts []CapabilityContract, work []AuthorityScope, missionScope, criterion, actionScope AuthorityScope, model autonomousReadinessModel) ReadinessInput {
	facts := make([]PredicateFact, 0)
	add := func(action ActionID, scope AuthorityScope, kind PredicateKind, producer ActionID, status PredicateStatus) {
		recovery := RecoveryNone
		if status.blocks() {
			recovery = RecoveryRunProducer
		}
		facts = append(facts, PredicateFact{Kind: kind, Action: action, Scope: scope, Status: status, Authority: testAuthority(predicateAuthorityFamily(kind)), EvidenceIdentity: "driver-" + string(action) + "-" + scope.Identity + "-" + string(kind), EvidenceDigest: testEvidenceDigest(), Producer: producer, Recovery: recovery})
	}
	current := PredicateStale
	if model.reconciled {
		current = PredicateSatisfied
	}
	for _, scope := range work {
		add(ActionExecutionClaim, scope, PredicateWorkGraphCurrent, ActionWorkGraphObserve, current)
		add(ActionExecutionClaim, scope, PredicateExecutionEligible, ActionWorkGraphObserve, PredicateSatisfied)
		add(ActionExecutionClaim, scope, PredicateRuntimeEpochCurrent, ActionPolicyResolve, PredicateSatisfied)
		candidate := PredicateUnavailable
		if model.claimed[scope.Identity] {
			candidate = PredicateSatisfied
		}
		add(ActionReviewRecord, scope, PredicateCandidateCurrent, ActionCandidateHandoff, candidate)
		add(ActionReviewRecord, scope, PredicateIndependentReview, ActionReviewRecord, PredicateSatisfied)
	}
	add(ActionVerificationRun, criterion, PredicateArtifactCurrent, ActionArtifactObserve, PredicateSatisfied)
	add(ActionAcceptanceEvaluate, missionScope, PredicateWorkGraphCurrent, ActionWorkGraphObserve, current)
	reviews := PredicateUnavailable
	if model.approved["work-a"] && model.approved["work-b"] {
		reviews = PredicateSatisfied
	}
	add(ActionAcceptanceEvaluate, missionScope, PredicateIndependentReview, ActionReviewRecord, reviews)
	add(ActionAcceptanceEvaluate, missionScope, PredicateProjectVerification, ActionVerificationRun, model.verifierStatus)
	add(ActionAcceptanceEvaluate, missionScope, PredicatePolicyCurrent, ActionPolicyResolve, PredicateSatisfied)
	acceptance := PredicateUnavailable
	if model.accepted {
		acceptance = PredicateSatisfied
	}
	add(ActionIntegrationPublish, missionScope, PredicateAcceptanceCurrent, ActionAcceptanceEvaluate, acceptance)
	add(ActionIntegrationPublish, missionScope, PredicateRepositoryTarget, ActionIntegrationPublish, PredicateSatisfied)
	conflict := PredicateSatisfied
	if model.conflictOpen {
		conflict = PredicateAmbiguous
	}
	add(ActionIntegrationPublish, missionScope, PredicateConflictAbsent, ActionConflictResolve, conflict)
	add(ActionMissionComplete, missionScope, PredicateAcceptanceCurrent, ActionAcceptanceEvaluate, acceptance)
	input := ReadinessInput{MissionID: "mission-driver", Revision: model.steps, Lifecycle: model.lifecycle, Contracts: contracts, Facts: facts}
	claimA := ActionRef{ID: ActionExecutionClaim, Scope: work[0]}
	claimB := ActionRef{ID: ActionExecutionClaim, Scope: work[1]}
	if model.decisionOpen {
		input.Decisions = []ReadinessDecision{{ID: "decision-driver", Affects: []ActionRef{claimA}, Authority: testAuthority("mission_decision"), EvidenceIdentity: "decision-driver", EvidenceDigest: testEvidenceDigest()}}
	}
	if model.blockerOpen {
		input.Blockers = []ReadinessBlocker{{ID: "blocker-driver", Affects: []ActionRef{claimB}, Authority: testAuthority("mission_blocker"), EvidenceIdentity: "blocker-driver", EvidenceDigest: testEvidenceDigest(), ResponsibleCapability: ActionBlockerClear, Recovery: ReadinessRecoverBlocker}}
	}
	if model.conflictOpen {
		input.Conflicts = []ReadinessConflict{{ID: "conflict-driver", Affects: []ActionRef{{ID: ActionIntegrationPublish, Scope: missionScope}}, Authority: testAuthority("mission_conflict"), EvidenceIdentity: "conflict-driver", EvidenceDigest: testEvidenceDigest()}}
	}
	return input
}

func chooseAutonomousAction(work []AuthorityScope, missionScope, criterion, actionScope AuthorityScope, model autonomousReadinessModel) ActionRef {
	if !model.pausedOnce {
		return ActionRef{ID: ActionMissionPause, Scope: missionScope}
	}
	if model.lifecycle == MissionPaused {
		return ActionRef{ID: ActionMissionResume, Scope: missionScope}
	}
	if model.decisionOpen {
		return ActionRef{ID: ActionDecisionResolve, Scope: actionScope}
	}
	if model.blockerOpen {
		return ActionRef{ID: ActionBlockerClear, Scope: actionScope}
	}
	if !model.reconciled {
		return ActionRef{ID: ActionWorkReconcile, Scope: missionScope}
	}
	for _, scope := range work {
		if !model.claimed[scope.Identity] {
			return ActionRef{ID: ActionExecutionClaim, Scope: scope}
		}
	}
	for _, scope := range work {
		if !model.approved[scope.Identity] {
			return ActionRef{ID: ActionReviewRecord, Scope: scope}
		}
	}
	if model.verifierTries < 3 {
		return ActionRef{ID: ActionVerificationRun, Scope: criterion}
	}
	if model.conflictOpen {
		return ActionRef{ID: ActionConflictResolve, Scope: actionScope}
	}
	if !model.accepted {
		return ActionRef{ID: ActionAcceptanceEvaluate, Scope: missionScope}
	}
	if !model.integrated {
		return ActionRef{ID: ActionIntegrationPublish, Scope: missionScope}
	}
	return ActionRef{ID: ActionMissionComplete, Scope: missionScope}
}

func applyAutonomousAction(t *testing.T, census ActionCensus, chosen ActionRef, model *autonomousReadinessModel) {
	t.Helper()
	if !slices.Contains(census.Authorized(), chosen) {
		t.Fatalf("driver selected unauthorized action %+v from %v; unavailable=%+v", chosen, census.Authorized(), census.Unavailable())
	}
	switch chosen.ID {
	case ActionMissionPause:
		model.pausedOnce, model.lifecycle = true, MissionPaused
	case ActionMissionResume:
		model.lifecycle = MissionOpen
	case ActionMissionCancel:
		model.lifecycle = MissionCanceled
	case ActionDecisionResolve:
		model.decisionOpen = false
	case ActionBlockerClear:
		model.blockerOpen = false
	case ActionWorkReconcile:
		model.reconciled = true
	case ActionExecutionClaim:
		model.claimed[chosen.Scope.Identity] = true
	case ActionReviewRecord:
		model.reviewAttempts[chosen.Scope.Identity]++
		if chosen.Scope.Identity == "work-a" && model.reviewAttempts[chosen.Scope.Identity] == 1 {
			model.claimed[chosen.Scope.Identity] = false
		} else {
			model.approved[chosen.Scope.Identity] = true
		}
	case ActionVerificationRun:
		model.verifierTries++
		model.verifierStatus = []PredicateStatus{PredicateUnavailable, PredicateFailed, PredicateSatisfied}[model.verifierTries-1]
	case ActionConflictResolve:
		model.conflictOpen = false
	case ActionAcceptanceEvaluate:
		model.accepted = true
	case ActionIntegrationPublish:
		model.integrated = true
	case ActionMissionComplete:
		model.lifecycle = MissionCompleted
	}
}
