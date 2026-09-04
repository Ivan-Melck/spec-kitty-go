package v2

import (
	"strings"
	"testing"
)

func TestWorkScopedAuthorityAssertionBudgetUsesBoundedSemanticComponents(t *testing.T) {
	component := func(prefix string) string { return prefix + strings.Repeat("x", MaxIdentityBytes-len(prefix)) }
	coordinate, err := NewAggregateCoordinate("execution_slot", AggregateID(component("slot-")), Scope{Kind: ScopeWork, ProjectID: ProjectID(component("project-")), MissionID: MissionID(component("mission-")), WorkRevisionID: WorkRevisionID(component("revision-")), WorkID: WorkID(component("work-"))})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ReducerStateDigestV2([]byte(`{"state":"observed"}`))
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := NewAuthorityAssertionV2(AuthorityAssertionInputV2{Target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: coordinate}, Kind: ConditionRecordRevision, ObservedRevision: 17, ExpectedLogicalVersion: 3, ObservedValueDigest: digest})
	if err != nil {
		t.Fatal(err)
	}
	if len(authorityAssertionKey(assertion)) <= MaxIdentityBytes {
		t.Fatal("fixture did not exceed one identity while retaining bounded components")
	}
	input := fixturePlanInput(t)
	input.Assertions = []AuthorityAssertionV2{assertion}
	if _, err := SealCommitPlanV2(input, DefaultEventRegistryV2()); err != nil {
		t.Fatalf("valid work-scoped assertion refused: %v", err)
	}
	over := coordinate
	over.ID = AggregateID(strings.Repeat("x", MaxIdentityBytes+1))
	input.Assertions[0] = AuthorityAssertionV2{target: ConditionTarget{Kind: ConditionTargetAggregateHead, Coordinate: over}, kind: ConditionRecordRevision, observedRevision: 17, expectedLogicalVersion: 3, observedValueDigest: digest}
	if _, err := SealCommitPlanV2(input, DefaultEventRegistryV2()); err == nil {
		t.Fatal("overlong semantic component was accepted")
	}
}
