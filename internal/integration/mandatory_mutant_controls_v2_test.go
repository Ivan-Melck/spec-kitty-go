package integration

import (
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestAcceptanceCannotOmitArtifactEvidenceV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	omitted := authority.Evidence[0].Evidence.Coordinate
	authority.Evidence = nil
	assertions := make([]journalv2.AuthorityAssertionV2, 0, len(authority.Assertions)-1)
	for _, assertion := range authority.Assertions {
		if assertion.Target().Coordinate != omitted {
			assertions = append(assertions, assertion)
		}
	}
	authority.Assertions = assertions
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority}
	service, err := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Accept(t.Context(), request); !IsKind(err, KindEvidenceUnavailable) || len(port.plans) != 0 {
		t.Fatalf("required artifact omission accepted: error=%v plans=%d", err, len(port.plans))
	}
}

func TestAcceptancePublicationCannotOmitWorkgraphGuardV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority}
	service, err := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Accept(t.Context(), request); err != nil {
		t.Fatalf("workgraph guard omitted: %v", err)
	}
	for _, plan := range port.plans {
		if len(plan.EligibilityGuards()) != 1 {
			t.Fatalf("workgraph guard omitted: guards=%d", len(plan.EligibilityGuards()))
		}
	}
}

func TestAcceptanceCannotOmitPolicyAuthorityV2(t *testing.T) {
	request, authority := acceptanceFixtureV2(t)
	omitted := authority.PolicyFacts[0].Assertion.Target().Coordinate
	authority.PolicyFacts = authority.PolicyFacts[1:]
	assertions := make([]journalv2.AuthorityAssertionV2, 0, len(authority.Assertions)-1)
	for _, assertion := range authority.Assertions {
		if assertion.Target().Coordinate != omitted {
			assertions = append(assertions, assertion)
		}
	}
	authority.Assertions = assertions
	port := &acceptancePortV2{resolution: AcceptanceOperationResolutionV2{Kind: AcceptanceOperationAbsentV2}, authority: authority}
	service, err := NewAcceptanceAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Accept(t.Context(), request); !IsKind(err, KindPolicyRefused) || len(port.plans) != 0 {
		t.Fatalf("required policy authority omission accepted: error=%v plans=%d", err, len(port.plans))
	}
}

func TestIntegrationStageRetryKeepsOperationIDV2(t *testing.T) {
	request, authority := integrationFixtureV2(t)
	port := newIntegrationPortV2(authority)
	git := newIntegrationGitFixtureV2(request)
	service, err := NewIntegrationAuthorityServiceV2(port, acceptanceEpochV2(request.RuntimeEpochID), gitAdapterV2{integrationGitV2: git}, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Integrate(t.Context(), request); err != nil {
		t.Fatalf("stage retry changed operation ID: %v", err)
	}
	for _, plan := range port.plans {
		if plan.OperationState().OperationID() != request.OperationID {
			t.Fatalf("stage retry changed operation ID: got=%s want=%s", plan.OperationState().OperationID(), request.OperationID)
		}
	}
}

func TestIntegrationStageRetryKeepsStableEffectIDsV2(t *testing.T) {
	request, _ := integrationFixtureV2(t)
	_, first, err := prepareIntegrationAuthorityRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	_, retry, err := prepareIntegrationAuthorityRequestV2(request)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := []journalv2.EffectID{first.FenceEffectID, first.WorkspaceEffectID, first.MergeEffectID, first.PublishEffectID, first.CleanupEffectID}
	retryIDs := []journalv2.EffectID{retry.FenceEffectID, retry.WorkspaceEffectID, retry.MergeEffectID, retry.PublishEffectID, retry.CleanupEffectID}
	for index := range firstIDs {
		if firstIDs[index] != retryIDs[index] {
			t.Fatalf("stage retry changed stable effect ID: first=%v retry=%v", firstIDs, retryIDs)
		}
	}
}
