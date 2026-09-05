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
		PredicateWaived, PredicateInapplicable, PredicateAmbiguous,
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

			baseline, err := EvaluateReadiness(input)
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
			again, err := EvaluateReadiness(permuted)
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
