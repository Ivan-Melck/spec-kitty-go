package workgraph

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/ids"
)

func TestWorkgraphV2FinalizationAndMixedReconciliationAreAtomicAuthority(t *testing.T) {
	request := mixedReconcileFixtureV3(t)
	first, err := BuildFinalizationPlanV2(FinalizationPlanInputV2{
		Context:  workgraphV2Context("finalize"),
		Revision: request.Old,
	})
	if err != nil {
		t.Fatal(err)
	}
	initialGuards := first.EligibilityGuards()
	if got := workgraphFamilies(first); got != "operation,workgraph" || len(first.Heads()) != 2 || len(first.Conditions()) != 2+len(initialGuards) || first.MutationCount() != 6+len(initialGuards) {
		t.Fatalf("finalization membership is not exact: families=%s heads=%d conditions=%d mutations=%d", got, len(first.Heads()), len(first.Conditions()), first.MutationCount())
	}
	prior := reduceWorkgraphEvent(t, nil, workgraphEventByFamily(t, first, "workgraph"))
	state, err := DecodeWorkgraphStateV2(prior)
	if err != nil || state.LogicalVersion != 1 {
		t.Fatalf("first generation did not reduce: %+v %v", state, err)
	}

	reconciliation, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	guards, err := BuildEligibilityGuardShards(request.New, reconciliation, allEligibilityOperationsV2())
	if err != nil {
		t.Fatal(err)
	}
	observedGuards := make(map[journalv2.EligibilityShardID]EligibilityShardObservationV2, len(guards))
	changedGuardIDs := make(map[journalv2.EligibilityShardID]struct{}, len(guards))
	for _, guard := range guards {
		changedGuardIDs[guard.ShardID()] = struct{}{}
		absent, absentErr := ObserveAbsentEligibilityShardV2(request.New.MissionID(), guard.ShardID())
		if absentErr != nil {
			t.Fatal(absentErr)
		}
		observedGuards[guard.ShardID()] = absent
	}
	for index, guard := range initialGuards {
		if _, changed := changedGuardIDs[guard.ShardID]; !changed {
			continue
		}
		observed, observeErr := NewObservedEligibilityShardV2(guard.Canonical, uint64(index+10))
		if observeErr != nil {
			t.Fatal(observeErr)
		}
		present, presentErr := ObservePresentEligibilityShardV2(observed)
		if presentErr != nil {
			t.Fatal(presentErr)
		}
		observedGuards[guard.ShardID] = present
	}
	reconciled, err := BuildReconciliationPlanV2(ReconciliationPlanInputV2{
		Context:                    workgraphV2Context("reconcile"),
		PriorState:                 prior,
		Successor:                  request.New,
		Reconciliation:             reconciliation,
		ExpectedHeadRecordRevision: 9,
		ObservedGuards:             observedGuards,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := workgraphFamilies(reconciled); got != "operation,workgraph" || len(reconciled.EligibilityGuards()) != len(guards) || len(reconciled.Conditions()) != 2+len(guards) || reconciled.MutationCount() != 6+len(guards) {
		t.Fatalf("reconciliation is not one exact batch: families=%s guards=%d conditions=%d mutations=%d", got, len(reconciled.EligibilityGuards()), len(reconciled.Conditions()), reconciled.MutationCount())
	}
	for _, transition := range reconciled.EligibilityGuards() {
		if observed, ok := observedGuards[transition.ShardID]; ok {
			_, wantRevision := observed.prior()
			if transition.ExpectedRecordRevision != wantRevision {
				t.Fatalf("guard %s lost provider revision fence", transition.ShardID)
			}
		}
	}
	assertMergedEligibilityV2(t, request, reconciled.EligibilityGuards(), "work-super", request.Old.ID(), false)
	assertMergedEligibilityV2(t, request, reconciled.EligibilityGuards(), "work-invalid", request.Old.ID(), false)
	assertMergedEligibilityV2(t, request, reconciled.EligibilityGuards(), "work-continue", request.Old.ID(), true)
	assertMergedEligibilityV2(t, request, reconciled.EligibilityGuards(), "work-fork", request.Old.ID(), true)
	next := reduceWorkgraphEvent(t, prior, workgraphEventByFamily(t, reconciled, "workgraph"))
	nextState, err := DecodeWorkgraphStateV2(next)
	if err != nil || nextState.LogicalVersion != 2 || len(nextState.Reconciliation) == 0 {
		t.Fatalf("reconciliation did not advance exact generation: %+v %v", nextState, err)
	}
	current, err := DecodeRevisionV2(nextState.CurrentRevision)
	if err != nil || current.ID() != request.New.ID() || current.PredecessorID() != request.Old.ID() {
		t.Fatalf("current revision lost lineage: %s <- %s (%v)", current.ID(), current.PredecessorID(), err)
	}
	for shardID := range observedGuards {
		delete(observedGuards, shardID)
		break
	}
	if _, err := BuildReconciliationPlanV2(ReconciliationPlanInputV2{Context: workgraphV2Context("reconcile-incomplete"), PriorState: prior, Successor: request.New, Reconciliation: reconciliation, ExpectedHeadRecordRevision: 9, ObservedGuards: observedGuards}); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("omitted changed shard was interpreted as provider absence: %v", err)
	}
}

func TestWorkgraphV2EligibilityGuardsCoverAllDispositionOperationPairs(t *testing.T) {
	request := mixedReconcileFixtureV3(t)
	reconciliation, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	shards, err := BuildEligibilityGuardShards(request.New, reconciliation, []EligibilityOperation{EligibilityIntegration, EligibilityStart, EligibilityHandoff, EligibilityAcceptance, EligibilityReview})
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) == 0 || len(shards) > EligibilityShardCountV2 {
		t.Fatalf("unexpected fixed shard fanout %d", len(shards))
	}
	seen := make(map[Disposition]map[EligibilityOperation]bool)
	entryCount := 0
	revoked := make(map[ids.WorkID]ids.WorkRevisionID)
	for _, shard := range shards {
		if len(shard.Entries()) > EligibilityMaxEntriesPerShardV2 {
			t.Fatalf("shard %s exceeded fixed budget", shard.ShardID())
		}
		for _, revocation := range shard.revocationSet() {
			revoked[revocation.WorkID] = revocation.WorkRevisionID
		}
		if len(shard.revocationSet()) > 0 {
			if _, err := shard.CanonicalBytes(); !IsKind(err, KindInvalidReconciliation) {
				t.Fatalf("unapplied eligibility revocation was publishable: %v", err)
			}
		}
		for _, entry := range shard.Entries() {
			entryCount++
			for _, generation := range entry.generations {
				if generation.ReconciliationID != reconciliation.ID() {
					t.Fatalf("incomplete eligibility lineage: %+v", generation)
				}
				if generation.WorkRevisionID != request.Old.ID() {
					continue
				}
				if seen[generation.Disposition] == nil {
					seen[generation.Disposition] = make(map[EligibilityOperation]bool)
				}
				for _, operation := range allEligibilityOperationsV2() {
					permitted, _, reason, ok := entry.Permission(generation.WorkRevisionID, operation)
					if !ok || !permitted || reason == "" {
						t.Fatalf("incomplete permitted eligibility authority: %+v/%s", generation, operation)
					}
					seen[generation.Disposition][operation] = true
				}
			}
		}
	}
	for _, workID := range []ids.WorkID{"work-super", "work-invalid"} {
		if revoked[workID] != request.Old.ID() {
			t.Fatalf("denied old generation %s lacks an exact revocation", workID)
		}
	}
	// The successor graph has five WorkIDs. Continue shares its WorkID across
	// generations in one fact; fork retains one distinct old line. Supersede and
	// invalidate are denied by absence, with their evidence in reconciliation.
	if entryCount != len(request.New.WorkItems())+1 {
		t.Fatalf("compact guard entries=%d want=%d", entryCount, len(request.New.WorkItems())+1)
	}
	for _, disposition := range []Disposition{Continue, Fork} {
		for _, operation := range allEligibilityOperationsV2() {
			if !seen[disposition][operation] {
				t.Fatalf("missing %s/%s eligibility authority", disposition, operation)
			}
		}
	}
}

func TestWorkgraphV2RevocationCanPublishAnExplicitEmptyShard(t *testing.T) {
	oldRevisionID := ids.WorkRevisionID("wrv3-" + strings.Repeat("c", 64))
	entry, err := newEligibilityGuardEntryV2(oldRevisionID, "operation-initial", "work-only", Continue)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeGuardEntriesV2([]EligibilityGuardEntryV2{entry}, nil, []eligibilityRevocationV2{{WorkID: "work-only", WorkRevisionID: oldRevisionID}})
	if err != nil || len(merged) != 0 {
		t.Fatalf("exact old generation was not removed: entries=%d err=%v", len(merged), err)
	}
	shardID, err := NewEligibilityPolicy().ShardFor("mission-empty-guard", "work-only")
	if err != nil {
		t.Fatal(err)
	}
	shard := EligibilityGuardShardV2{
		schemaVersion: eligibilityGuardSchemaVersionV2,
		policyVersion: EligibilityPolicyVersionV2,
		missionID:     "mission-empty-guard",
		shardID:       shardID,
		entries:       merged,
	}
	canonical, err := shard.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	observed, err := NewObservedEligibilityShardV2(canonical, 17)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observed.Token("work-only", oldRevisionID, EligibilityStart); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("revoked sole generation survived explicit empty shard: %v", err)
	}
}

func TestWorkgraphV2RetainsFiveSuccessiveContinueGenerations(t *testing.T) {
	const reconciliationCount = 5
	workID := ids.WorkID("work-five-continues")
	entries := []EligibilityGuardEntryV2{}
	revisionIDs := make([]ids.WorkRevisionID, 0, reconciliationCount+1)
	for generation := 0; generation <= reconciliationCount; generation++ {
		revisionID := ids.WorkRevisionID(fmt.Sprintf("wrv3-%064x", generation+1))
		revisionIDs = append(revisionIDs, revisionID)
		entry, err := newEligibilityGuardEntryV2(revisionID, fmt.Sprintf("operation-continue-%d", generation), workID, Continue)
		if err != nil {
			t.Fatal(err)
		}
		entries, err = mergeGuardEntriesV2(entries, []EligibilityGuardEntryV2{entry}, nil)
		if err != nil {
			t.Fatalf("continue generation %d was refused: %v", generation, err)
		}
	}
	shardID, err := NewEligibilityPolicy().ShardFor("mission-five-continues", workID)
	if err != nil {
		t.Fatal(err)
	}
	shard := EligibilityGuardShardV2{
		schemaVersion: eligibilityGuardSchemaVersionV2,
		policyVersion: EligibilityPolicyVersionV2,
		missionID:     "mission-five-continues",
		shardID:       shardID,
		entries:       entries,
	}
	canonical, err := shard.CanonicalBytes()
	if err != nil {
		t.Fatalf("six retained named generations exceeded an invented ceiling: %v", err)
	}
	observed, err := NewObservedEligibilityShardV2(canonical, 23)
	if err != nil {
		t.Fatal(err)
	}
	for _, revisionID := range revisionIDs {
		for _, operation := range allEligibilityOperationsV2() {
			if _, err := observed.Token(workID, revisionID, operation); err != nil {
				t.Fatalf("continued generation %s lost %s authority: %v", revisionID, operation, err)
			}
		}
	}
}

func TestWorkgraphV2EligibilityPolicyIsFrozenAndRestartStable(t *testing.T) {
	policy := NewEligibilityPolicy()
	if policy.PersistentTemplate() != eligibilityPersistentTemplateV2 || policy.ContractSHA256() != EligibilityContractSHA256V2 || policy.AdmittedM1TupleSHA256() != EligibilityAdmittedM1TupleV2 || policy.Version() != EligibilityPolicyVersionV2 || policy.HashAlgorithm() != EligibilityHashAlgorithmV2 || policy.ShardCount() != 64 || policy.MaxEntriesPerShard() != 256 {
		t.Fatalf("frozen eligibility policy drifted: %+v", policy)
	}
	first, err := policy.ShardFor("mission-mixed", "work-fork")
	if err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < EligibilityRequiredTrialsV2; restart++ {
		got, err := NewEligibilityPolicy().ShardFor("mission-mixed", "work-fork")
		if err != nil || got != first {
			t.Fatalf("restart %d changed shard %s to %s: %v", restart, first, got, err)
		}
	}
}

func TestWorkgraphV2TenThousandWorkFactsRemainBoundedWithRetainedContinueGeneration(t *testing.T) {
	const workCount = 10_000
	missionID := ids.MissionID("mission-large-eligibility")
	oldRevisionID := ids.WorkRevisionID("wrv3-" + strings.Repeat("a", 64))
	newRevisionID := ids.WorkRevisionID("wrv3-" + strings.Repeat("b", 64))
	items := make([]WorkItemV2, workCount)
	retained := make([]EligibilityGuardEntryV2, 0, workCount*2)
	for index := 0; index < workCount; index++ {
		workID := ids.WorkID(fmt.Sprintf("work-%05d", index))
		items[index] = WorkItemV2{workID: workID}
		oldEntry, err := newEligibilityGuardEntryV2(oldRevisionID, "operation-large-reconcile", workID, Continue)
		if err != nil {
			t.Fatal(err)
		}
		newEntry, err := newEligibilityGuardEntryV2(newRevisionID, "operation-large-reconcile", workID, Continue)
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, oldEntry, newEntry)
	}
	revision := RevisionV2{schemaVersion: RevisionSchemaV3, id: newRevisionID, missionID: missionID, workItems: items}
	initial, err := BuildInitialEligibilityGuardShards(revision, "operation-large-initial", allEligibilityOperationsV2())
	if err != nil {
		t.Fatal(err)
	}
	continued, err := groupEligibilityGuardShardsV2(missionID, allEligibilityOperationsV2(), retained)
	if err != nil {
		t.Fatal(err)
	}
	for label, shards := range map[string][]EligibilityGuardShardV2{"initial": initial, "continued": continued} {
		entries := 0
		for _, shard := range shards {
			entries += len(shard.Entries())
			if len(shard.Entries()) > EligibilityMaxEntriesPerShardV2 {
				t.Fatalf("%s shard %s has %d entries", label, shard.ShardID(), len(shard.Entries()))
			}
			canonical, err := shard.CanonicalBytes()
			if err != nil {
				t.Fatalf("%s shard %s is not publishable: %v", label, shard.ShardID(), err)
			}
			if len(canonical) > journalv2.MaxCanonicalPayloadBytes {
				t.Fatalf("%s shard %s exceeds ordinary value budget: %d", label, shard.ShardID(), len(canonical))
			}
		}
		if entries != workCount || len(shards) > EligibilityShardCountV2 {
			t.Fatalf("%s eligibility expanded %d work items into %d entries/%d shards", label, workCount, entries, len(shards))
		}
	}
}

func TestWorkgraphV2ObservedShardIssuesOpaqueExactGenerationToken(t *testing.T) {
	request := mixedReconcileFixtureV3(t)
	reconciliation, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	transitions := mergedReconciliationEligibilityV2(t, request, reconciliation)
	policy := NewEligibilityPolicy()
	shardID, err := policy.ShardFor(request.New.MissionID(), "work-continue")
	if err != nil {
		t.Fatal(err)
	}
	var canonical []byte
	for _, transition := range transitions {
		if transition.ShardID == shardID {
			canonical = transition.Canonical
			break
		}
	}
	if len(canonical) == 0 {
		t.Fatalf("merged guard transition %s is absent", shardID)
	}
	observed, err := NewObservedEligibilityShardV2(canonical, 41)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := observed.Token("work-continue", request.Old.ID(), EligibilityHandoff)
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := observed.Token("work-continue", request.New.ID(), EligibilityHandoff)
	if err != nil {
		t.Fatal(err)
	}
	if oldToken.Transition().ExpectedRecordRevision != 41 || newToken.Transition().ShardID != shardID {
		t.Fatal("exact old/new generation tokens did not coexist")
	}
	if _, err := observed.Token("work-continue", "revision-absent", EligibilityHandoff); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("missing generation did not fail closed: %v", err)
	}
	mutated := bytes.Replace(canonical, []byte(`"start":true`), []byte(`"start":false`), 1)
	if bytes.Equal(mutated, canonical) {
		mutated = bytes.Replace(canonical, []byte(`"start":false`), []byte(`"start":true`), 1)
	}
	if _, err := NewObservedEligibilityShardV2(mutated, 41); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("forged semantic bytes received a provider revision: %v", err)
	}
}

func TestWorkgraphV2GuardsStartAndAcceptanceWithoutConfusingContinuation(t *testing.T) {
	request := mixedReconcileFixtureV3(t)
	reconciliation, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	transitions := mergedReconciliationEligibilityV2(t, request, reconciliation)
	tokens := make(map[string]EligibilityGuardTokenV2)
	denied := make(map[string]error)
	for index, transition := range transitions {
		observed, observeErr := NewObservedEligibilityShardV2(transition.Canonical, uint64(index+1))
		if observeErr != nil {
			t.Fatal(observeErr)
		}
		for _, target := range []struct {
			workID     ids.WorkID
			revisionID ids.WorkRevisionID
			operation  EligibilityOperation
		}{
			{"work-super", request.Old.ID(), EligibilityStart},
			{"work-super", request.Old.ID(), EligibilityAcceptance},
			{"work-super-new", request.New.ID(), EligibilityStart},
			{"work-super-new", request.New.ID(), EligibilityAcceptance},
		} {
			token, tokenErr := observed.Token(target.workID, target.revisionID, target.operation)
			if tokenErr == nil {
				tokens[target.workID.String()+"/"+string(target.operation)] = token
			} else {
				denied[target.workID.String()+"/"+string(target.operation)] = tokenErr
			}
		}
	}
	for _, key := range []string{"work-super-new/start", "work-super-new/acceptance"} {
		if _, ok := tokens[key]; !ok {
			t.Fatalf("missing exact-generation guard token %s", key)
		}
	}
	for _, key := range []string{"work-super/start", "work-super/acceptance"} {
		if !IsKind(denied[key], KindInvalidReconciliation) {
			t.Fatalf("superseded generation received commit-capable %s authority: %v", key, denied[key])
		}
	}
	consequence, ok := reconciliation.Consequence("work-super", StatusActive)
	if !ok || !consequence.RunMayContinue {
		t.Fatal("guarding a fresh start erased the frozen already-running continuation consequence")
	}
	transition := tokens["work-super-new/start"].Transition()
	transition.Canonical[0] ^= 0xff
	if bytes.Equal(transition.Canonical, tokens["work-super-new/start"].Transition().Canonical) {
		t.Fatal("opaque eligibility token leaked mutable canonical authority")
	}
}

func mergedReconciliationEligibilityV2(t *testing.T, request MixedReconcileRequest, reconciliation MixedReconciliation) []journalv2.EligibilityGuardTransition {
	t.Helper()
	first, err := BuildFinalizationPlanV2(FinalizationPlanInputV2{Context: workgraphV2Context("merged-guard-finalize"), Revision: request.Old})
	if err != nil {
		t.Fatal(err)
	}
	prior := reduceWorkgraphEvent(t, nil, workgraphEventByFamily(t, first, "workgraph"))
	changed, err := BuildEligibilityGuardShards(request.New, reconciliation, allEligibilityOperationsV2())
	if err != nil {
		t.Fatal(err)
	}
	observed := make(map[journalv2.EligibilityShardID]EligibilityShardObservationV2, len(changed))
	for _, shard := range changed {
		absence, absentErr := ObserveAbsentEligibilityShardV2(request.New.MissionID(), shard.ShardID())
		if absentErr != nil {
			t.Fatal(absentErr)
		}
		observed[shard.ShardID()] = absence
	}
	for index, transition := range first.EligibilityGuards() {
		if _, changedShard := observed[transition.ShardID]; !changedShard {
			continue
		}
		priorGuard, observeErr := NewObservedEligibilityShardV2(transition.Canonical, uint64(index+1))
		if observeErr != nil {
			t.Fatal(observeErr)
		}
		presence, presentErr := ObservePresentEligibilityShardV2(priorGuard)
		if presentErr != nil {
			t.Fatal(presentErr)
		}
		observed[transition.ShardID] = presence
	}
	plan, err := BuildReconciliationPlanV2(ReconciliationPlanInputV2{
		Context:                    workgraphV2Context("merged-guard-reconcile"),
		PriorState:                 prior,
		Successor:                  request.New,
		Reconciliation:             reconciliation,
		ExpectedHeadRecordRevision: 9,
		ObservedGuards:             observed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan.EligibilityGuards()
}

func assertMergedEligibilityV2(t *testing.T, request MixedReconcileRequest, transitions []journalv2.EligibilityGuardTransition, workID ids.WorkID, revisionID ids.WorkRevisionID, wantPermitted bool) {
	t.Helper()
	shardID, err := NewEligibilityPolicy().ShardFor(request.New.MissionID(), workID)
	if err != nil {
		t.Fatal(err)
	}
	for index, transition := range transitions {
		if transition.ShardID != shardID {
			continue
		}
		observed, observeErr := NewObservedEligibilityShardV2(transition.Canonical, uint64(index+100))
		if observeErr != nil {
			t.Fatal(observeErr)
		}
		for _, operation := range allEligibilityOperationsV2() {
			_, tokenErr := observed.Token(workID, revisionID, operation)
			if wantPermitted && tokenErr != nil {
				t.Fatalf("retained %s/%s eligibility was lost: %v", workID, operation, tokenErr)
			}
			if !wantPermitted && !IsKind(tokenErr, KindInvalidReconciliation) {
				t.Fatalf("revoked %s/%s eligibility survived merged state: %v", workID, operation, tokenErr)
			}
		}
		return
	}
	t.Fatalf("changed eligibility shard %s for %s is absent", shardID, workID)
}

func TestWorkgraphV2RefusesIncompleteEligibilityAndWrongLineage(t *testing.T) {
	request := mixedReconcileFixtureV3(t)
	reconciliation, err := ReconcileMixed(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildEligibilityGuardShards(request.New, reconciliation, []EligibilityOperation{EligibilityHandoff, EligibilityReview}); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("incomplete operation set admitted: %v", err)
	}
	shards, err := BuildEligibilityGuardShards(request.New, reconciliation, allEligibilityOperationsV2())
	if err != nil {
		t.Fatal(err)
	}
	var malformed EligibilityGuardShardV2
	for _, shard := range shards {
		if len(shard.entries) > 0 && len(shard.revocations) == 0 {
			malformed = shard
			break
		}
	}
	if len(malformed.entries) == 0 {
		t.Fatal("fixture has no directly serializable eligibility shard")
	}
	malformed.entries[0].generations[0].Permissions.Start = !malformed.entries[0].generations[0].Permissions.Start
	if _, err := malformed.CanonicalBytes(); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("caller-authored permission was serialized: %v", err)
	}
	misplaced := shards[0]
	misplaced.shardID = "ff"
	if _, err := misplaced.CanonicalBytes(); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("misplaced guard entry was serialized: %v", err)
	}
	first, err := BuildFinalizationPlanV2(FinalizationPlanInputV2{Context: workgraphV2Context("finalize"), Revision: request.Old})
	if err != nil {
		t.Fatal(err)
	}
	prior := reduceWorkgraphEvent(t, nil, workgraphEventByFamily(t, first, "workgraph"))
	other := reconcileFixtureV3(t, additionOnlyReconcileFixture(t))
	if _, err := BuildReconciliationPlanV2(ReconciliationPlanInputV2{Context: workgraphV2Context("wrong"), PriorState: prior, Successor: other.New, Reconciliation: reconciliation}); !IsKind(err, KindInvalidReconciliation) {
		t.Fatalf("wrong mission successor admitted: %v", err)
	}
}

func TestWorkgraphV2ExactReplayPrecedesEpochAndChangedBindingConflicts(t *testing.T) {
	request := mixedReconcileFixtureV3(t)
	plan, err := BuildFinalizationPlanV2(FinalizationPlanInputV2{Context: workgraphV2Context("finalize"), Revision: request.Old})
	if err != nil {
		t.Fatal(err)
	}
	binding := plan.BindingCreate()
	encoded, _ := journalv2.EncodeOperationBindingV2(*binding)
	state := plan.OperationState()
	resolution, err := ResolveWorkgraphRetryV2(*binding, encoded, true, &state, "epoch-replacement")
	if err != nil || resolution.Decision != journalv2.RetryReplay || !bytes.Equal(resolution.Result, state.Result()) {
		t.Fatalf("exact retry did not replay before stale epoch: %+v %v", resolution, err)
	}

	changedContext := workgraphV2Context("finalize")
	changedContext.ActorID = "different-actor"
	changed, err := BuildFinalizationPlanV2(FinalizationPlanInputV2{Context: changedContext, Revision: request.Old})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err = ResolveWorkgraphRetryV2(*changed.BindingCreate(), encoded, true, &state, binding.OriginatingEpoch())
	if resolution.Decision != journalv2.RetryConflict || !journalv2.IsKind(err, journalv2.ErrorIdempotencyConflict) {
		t.Fatalf("changed binding was not earliest refusal: %+v %v", resolution, err)
	}
}

func TestWorkgraphV2PlaybackRefusesHistoricalRevisionSchemaAsCurrentAuthority(t *testing.T) {
	legacy, err := NewRevisionV2(revisionV2Fixture())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := legacy.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []workgraphEventKindV2{workgraphFinalizedV2, workgraphReconciledV2} {
		payload, encodeErr := canonicalWorkgraphEventV2(workgraphEventV2{SchemaVersion: workgraphStateSchemaV2, Kind: kind, Revision: encoded, Reconciliation: json.RawMessage(`{"legacy":true}`)})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if _, reduceErr := reduceDraftWorkgraphV2(nil, FinalizationPlanInputV2{Revision: legacy}, payload); !IsKind(reduceErr, KindInvalidRevision) {
			t.Fatalf("%s replay admitted WorkRevision schema v2: %v", kind, reduceErr)
		}
	}
	state, err := encodeWorkgraphStateV2(WorkgraphStateV2{SchemaVersion: workgraphStateSchemaV2, ProjectID: legacy.ProjectID(), MissionID: legacy.MissionID(), LogicalVersion: 1, CurrentRevision: encoded})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWorkgraphStateV2(state); !IsKind(err, KindInvalidRevision) {
		t.Fatalf("schema-v2 current-state projection was accepted: %v", err)
	}
}

func workgraphV2Context(label string) AuthorityContextV2 {
	return AuthorityContextV2{
		OperationID:       journalv2.OperationID("operation-" + label),
		RuntimeEpochID:    "epoch-workgraph",
		ActorID:           "actor-workgraph",
		ProcessID:         "process-workgraph",
		DoctrineRevision:  "doctrine-workgraph",
		ProfileRevision:   "profile-workgraph",
		PolicyRevision:    "policy-workgraph",
		Governance:        []string{"governance-workgraph"},
		ObservedCreatedAt: "2026-09-05T00:00:00Z",
		OperationEventID:  journalv2.EventID("event-operation-" + label),
		DomainEventID:     journalv2.EventID("event-workgraph-" + label),
	}
}

func workgraphEventByFamily(t *testing.T, plan journalv2.SealedCommitPlanV2, family string) journalv2.EventEnvelopeV2 {
	t.Helper()
	for _, event := range plan.Events() {
		if event.Family() == family {
			return event
		}
	}
	t.Fatalf("event family %s is absent", family)
	return journalv2.EventEnvelopeV2{}
}

func reduceWorkgraphEvent(t *testing.T, prior []byte, event journalv2.EventEnvelopeV2) []byte {
	t.Helper()
	result, err := ReduceWorkgraphEventV2(prior, event)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func workgraphFamilies(plan journalv2.SealedCommitPlanV2) string {
	families := make([]string, 0, len(plan.Events()))
	for _, event := range plan.Events() {
		families = append(families, event.Family())
	}
	return strings.Join(families, ",")
}

func mixedReconcileFixtureV3(t *testing.T) MixedReconcileRequest {
	t.Helper()
	return reconcileFixtureV3(t, mixedReconcileFixture(t))
}

func reconcileFixtureV3(t *testing.T, request MixedReconcileRequest) MixedReconcileRequest {
	t.Helper()
	old := upgradeRevisionFixtureV3(t, request.Old, 1, "")
	new := upgradeRevisionFixtureV3(t, request.New, 2, old.ID())
	drift, err := DetectDrift(old, new, request.Drift.ObservedAt())
	if err != nil {
		t.Fatal(err)
	}
	request.Old = old
	request.New = new
	request.Drift = drift
	return request
}

func upgradeRevisionFixtureV3(t *testing.T, base RevisionV2, generation uint64, predecessor ids.WorkRevisionID) RevisionV2 {
	t.Helper()
	items := make([]WorkItemV2Params, 0, len(base.WorkItems()))
	for _, item := range base.WorkItems() {
		criteria := make([]CriterionV2Params, 0, len(item.Criteria()))
		for _, criterion := range item.Criteria() {
			metadata := criterion.Metadata()
			outcome, _ := metadata[CriterionMetadataOutcome].(string)
			hint, _ := metadata[CriterionMetadataEvidenceHint].(string)
			criteria = append(criteria, CriterionV2Params{ID: criterion.ID(), Text: criterion.Text(), VerifierKind: criterion.VerifierKind(), Required: criterion.Required(), Outcome: outcome, EvidenceHint: hint})
		}
		items = append(items, WorkItemV2Params{WorkID: item.WorkID(), Title: item.Title(), Description: item.Description(), DesignContext: item.DesignContext(), Criteria: criteria, Dependencies: item.Dependencies(), WriteScopes: item.WriteScopes()})
	}
	providerEvidence := make([]ProviderObservationV3, 0, len(base.ProviderObservations()))
	for _, observation := range base.ProviderObservations() {
		digest := sha256.Sum256([]byte(observation.WorkID))
		providerEvidence = append(providerEvidence, ProviderObservationV3{WorkID: observation.WorkID, Provider: observation.Provider, ExternalID: observation.ExternalID, Revision: observation.Revision, MetadataSchema: 1, DefinitionDigest: fmt.Sprintf("%x", digest), CriterionEvidenceHints: map[string]string{}})
	}
	revision, err := NewRevisionV3(RevisionV3Params{ProjectID: base.ProjectID(), MissionID: base.MissionID(), Generation: generation, PredecessorID: predecessor, PlanningSourceCommit: base.PlanningSourceCommit(), MissionDefinition: base.MissionDefinition(), DependencyPolicy: base.DependencyPolicy(), WorkItems: items, Lanes: base.Lanes(), ArtifactObservations: []ArtifactObservationV3{{Kind: "plan", ArtifactRevisionID: "plan-revision-authority", ContentDigest: strings.Repeat("e", 64)}}, ProviderEvidence: providerEvidence, FinalizedBy: base.FinalizedBy(), FinalizedAt: base.FinalizedAt(), GovernanceContextID: base.GovernanceContextID()})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
