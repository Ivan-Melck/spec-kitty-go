package testcampaign

import (
	"fmt"
	"slices"

	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

const HubnotQualificationPackage = "./internal/testcampaign/cmd/hubnot-qualification"
const HubnotFullTest = "TestRealHubnotQualification"
const HubnotHostedTest = "TestRealHubnotHostedProvider"
const HubnotIdentityTest = "TestRealHubnotMissionIdentity"
const HubnotEvidenceSchema = "go-kitty.hubnot-qualification/1"

// Modes describe the evidence actually exercised, not interchangeable witnesses.
const (
	HubnotPublic      = "compiled-public"
	HubnotPartial     = "public-persisted-partial"
	HubnotInjected    = "source-bound-unit"
	HubnotSourceMode  = "source-and-preservation"
	HubnotPerformance = "compiled-measurement"
)

type HubnotScenario struct {
	ID      string   `json:"id"`
	Family  int      `json:"family"`
	Mode    string   `json:"mode"`
	Package string   `json:"package,omitempty"`
	Tests   []string `json:"tests,omitempty"`
}
type HubnotScenarioManifest struct {
	Schema    string                  `json:"schema"`
	Provider  hubnot.ProviderIdentity `json:"provider"`
	Scenarios []HubnotScenario        `json:"scenarios"`
}

// HubnotManifest is independent of selected or observed rows. Every call owns its
// returned slices. The provider tuple has one admission authority, in the adapter.
func HubnotManifest() HubnotScenarioManifest {
	return HubnotScenarioManifest{Schema: "go-kitty.hubnot-manifest/1", Provider: hubnot.QualifiedProviderIdentity(), Scenarios: []HubnotScenario{
		{ID: "binding.admitted", Family: 1, Mode: HubnotPublic},
		{ID: "binding.refusals", Family: 1, Mode: HubnotPublic},
		{ID: "work.rich-roundtrip", Family: 2, Mode: HubnotPublic},
		{ID: "work.simple-update-guards", Family: 2, Mode: HubnotPublic},
		{ID: "graph.lifecycle", Family: 3, Mode: HubnotPublic},
		{ID: "graph.invalid-suppliers", Family: 3, Mode: HubnotPublic},
		{ID: "graph.partial-definition", Family: 3, Mode: HubnotPartial},
		{ID: "assignment.provenance", Family: 4, Mode: HubnotPublic},
		{ID: "assignment.refusals", Family: 4, Mode: HubnotInjected, Package: "./internal/tracker/hubnot", Tests: []string{"TestAssignmentOriginalRecoveryAndRefusals"}},
		{ID: "discussion.attribution", Family: 5, Mode: HubnotPublic},
		{ID: "discussion.partial-pair", Family: 5, Mode: HubnotPartial},
		{ID: "operation.historical-replay", Family: 6, Mode: HubnotPublic},
		{ID: "operation.key-attempt-refusal", Family: 6, Mode: HubnotPublic},
		{ID: "operation.lookup-boundaries", Family: 6, Mode: HubnotInjected, Package: "./internal/tracker/hubnot", Tests: []string{"TestActorLookupPreservesPinnedValidatorAndBinding", "TestOperationLookupFailureNeverReconstructsOrAppends"}},
		{ID: "bounds.public-input-output", Family: 7, Mode: HubnotPublic},
		{ID: "bounds.snapshot-pages", Family: 7, Mode: HubnotInjected, Package: "./internal/tracker/hubnot", Tests: []string{"TestInventoryRefusesIncompleteOrContradictoryViews", "TestKnownObservationRequiresCurrentSignerAndCompletePages"}},
		{ID: "bounds.large-heads", Family: 7, Mode: HubnotPublic},
		{ID: "bounds.transport", Family: 7, Mode: HubnotInjected, Package: "./internal/tracker/hubnot", Tests: []string{"TestAdapterLogicalCaptureStopsAtRemainingByte", "TestObservationBudgetDoesNotReset", "TestProtocolStrictBoundary", "TestTransportBoundsAndCancellation", "TestTransportStreamsShareLogicalCaptureLimit"}},
		{ID: "replicas.selected-withheld", Family: 8, Mode: HubnotPublic},
		{ID: "replicas.reconstruction", Family: 8, Mode: HubnotPublic},
		{ID: "integration.compiled-dispatch", Family: 9, Mode: HubnotPublic},
		{ID: "integration.authority-boundaries", Family: 9, Mode: HubnotSourceMode},
		{ID: "cutover.active-successor", Family: 10, Mode: HubnotSourceMode},
		{ID: "cutover.frozen-source", Family: 10, Mode: HubnotSourceMode},
		{ID: "cutover.local-recovery", Family: 10, Mode: HubnotSourceMode},
		{ID: "performance.inventory-1000", Family: 11, Mode: HubnotPerformance},
		{ID: "performance.known-reference", Family: 11, Mode: HubnotPerformance},
		{ID: "performance.original-recovery", Family: 11, Mode: HubnotPerformance},
	}}
}

// Hosted scope excludes exactly the one private local recovery observation. It
// never becomes full qualification, even if every selected scenario passes.
func HubnotSelection(scope string) ([]HubnotScenario, error) {
	all := HubnotManifest().Scenarios
	switch scope {
	case "full-local":
		return all, nil
	case "hosted-provider":
		return slices.DeleteFunc(all, func(s HubnotScenario) bool { return s.ID == "cutover.local-recovery" }), nil
	case "identity-smoke":
		return slices.DeleteFunc(all, func(s HubnotScenario) bool {
			return !slices.Contains([]string{"binding.admitted", "work.rich-roundtrip", "assignment.provenance", "discussion.attribution", "replicas.reconstruction"}, s.ID)
		}), nil
	default:
		return nil, fmt.Errorf("unknown explicit Hubnot qualification scope %q", scope)
	}
}

// Required unit supplements are fixed source-bound leaves. They cannot invoke
// any real qualification wrapper or recursively enter campaign admission.
func HubnotUnitWitnesses(id string) []HubnotScenario {
	for _, s := range HubnotManifest().Scenarios {
		if s.ID == id && s.Mode == HubnotInjected {
			return []HubnotScenario{s}
		}
	}
	switch id {
	case "bounds.public-input-output":
		return []HubnotScenario{{Package: "./internal/helpercmd", Tests: []string{"TestProviderRequestDescriptorModesAndRepeatedCleanup", "TestTrackerCanceledInitialDiagnosticHasBoundedDelivery", "TestTrackerOutputBackpressureCancellation", "TestTrackerOutputFailuresAndShortWritesAreUnavailable"}}}
	case "integration.compiled-dispatch":
		return []HubnotScenario{{Package: "./internal/helpercmd", Tests: []string{"TestTrackerEarlyDispatchNeverTouchesHostFactory"}}}
	case "integration.authority-boundaries":
		return []HubnotScenario{
			{Package: HubnotQualificationPackage, Tests: []string{"TestQualificationProductionPlaybackMaterializedCheckpoint"}},
			{Package: "./internal/mission", Tests: []string{"TestMissionProjectionConsumesCheckpointAndSuffixPlaybackV2"}},
			{Package: "./internal/architecture", Tests: []string{"TestDefaultReachabilityRegistryPassesCurrentRepository"}},
			{Package: "./internal/controljournal/v2/playback", Tests: []string{"TestPlaybackDeterministicPermutationAndMoreThan256ReducerInputs", "TestPlaybackProductionSurfaceHasStructurallyNoIO"}},
			{Package: "./internal/productionruntime", Tests: []string{"TestProductionReducerCompositionIsCompleteAndRuntimePlaybackPure"}},
			{Package: "./internal/execution", Tests: []string{"TestExternalEffectTerminalPlaybackCallsNoProviderV2"}}}
	}
	return nil
}
