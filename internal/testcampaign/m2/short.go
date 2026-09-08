package m2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ShortDefinition is the frozen, sidecar-free PR qualification census.  The
// command owns selector membership rather than trusting a successful `go
// test -run` whose selector may have matched nothing.
type ShortDefinition struct {
	Group         string   `json:"group"`
	Package       string   `json:"package"`
	Selectors     []string `json:"selectors"`
	ExpectedTests []string `json:"expected_tests"`
	Race          bool     `json:"race"`
}

var shortGateDefinitions = []ShortDefinition{
	shortDefinition("core", "./internal/testcampaign/m2", false,
		[]string{"TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound", "TestManifestCensusAndArtifactMutantsFailClosed", "TestFrozenCensusRejectsDeletionReorderAndSubstitution"},
		[]string{"TestManifestCensusAndArtifactMutantsFailClosed/aliased-wp16-sidecar-bytes", "TestManifestCensusAndArtifactMutantsFailClosed/caller-verdict", "TestManifestCensusAndArtifactMutantsFailClosed/duplicate-entry", "TestManifestCensusAndArtifactMutantsFailClosed/missing-entry", "TestManifestCensusAndArtifactMutantsFailClosed/missing-qualification-candidate", "TestManifestCensusAndArtifactMutantsFailClosed/missing-wp16-sidecar", "TestManifestCensusAndArtifactMutantsFailClosed/mixed-run", "TestManifestCensusAndArtifactMutantsFailClosed/mixed-source", "TestManifestCensusAndArtifactMutantsFailClosed/mutated-qualification-candidate", "TestManifestCensusAndArtifactMutantsFailClosed/substituted-wp16-sidecar-path", "TestManifestCensusAndArtifactMutantsFailClosed/substitution", "TestManifestCensusAndArtifactMutantsFailClosed/swapped-sidecar-roles", "TestManifestCensusAndArtifactMutantsFailClosed/unknown-wp16-sidecar-kind", "TestManifestCensusAndArtifactMutantsFailClosed/wrong-admission", "TestManifestCensusAndArtifactMutantsFailClosed/wrong-digest", "TestManifestCensusAndArtifactMutantsFailClosed/zero-observed"}),
	shortDefinition("core", "./internal/architecture", false,
		[]string{"TestM2ContractCensusAndSchemas", "TestM2ContractEventFamilyRegistry", "TestM2ContractAuthorityJournalLaws", "TestM2ContractPublicationWitnessAndBoundaryCorrection", "TestM2DependencyPolicyAcceptsOnlyBoundedProviderComposition", "TestM2DependencyRestoreDeadlineIsOneExactEdge", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly", "TestM2GateSourcesDoNotShelterExecutableBehavior", "TestM2SQLiteAbsence"},
		[]string{
			"TestM2ContractCensusAndSchemas/historical-disposition", "TestM2ContractCensusAndSchemas/historical-findings-over-bound", "TestM2ContractCensusAndSchemas/historical-invalid-enum", "TestM2ContractCensusAndSchemas/operation-binding", "TestM2ContractCensusAndSchemas/operation-date-invalid", "TestM2ContractCensusAndSchemas/operation-id-over-bound", "TestM2ContractCensusAndSchemas/operation-kind-invalid", "TestM2ContractCensusAndSchemas/qualification", "TestM2ContractCensusAndSchemas/qualification-recovery-under-bound", "TestM2ContractCensusAndSchemas/qualification-run-over-bound",
			"TestM2ContractEventFamilyRegistry/duplicate-family", "TestM2ContractEventFamilyRegistry/event-version-change", "TestM2ContractEventFamilyRegistry/missing-family", "TestM2ContractEventFamilyRegistry/open-publication", "TestM2ContractEventFamilyRegistry/operation-reducer-1", "TestM2ContractEventFamilyRegistry/operation-schema-1", "TestM2ContractEventFamilyRegistry/reducer-version-change", "TestM2ContractEventFamilyRegistry/registry-id", "TestM2ContractEventFamilyRegistry/registry-schema-version", "TestM2ContractEventFamilyRegistry/renamed-family", "TestM2ContractEventFamilyRegistry/unexpected-family", "TestM2ContractEventFamilyRegistry/unnormalized-owner", "TestM2ContractEventFamilyRegistry/wrong-normalized-owner", "TestM2ContractEventFamilyRegistry/wrong-order", "TestM2ContractEventFamilyRegistry/zero-reducer",
			"TestM2DependencyRestoreDeadlineIsOneExactEdge/checkpoint", "TestM2DependencyRestoreDeadlineIsOneExactEdge/foundation", "TestM2DependencyRestoreDeadlineIsOneExactEdge/nested_restore_owner", "TestM2DependencyRestoreDeadlineIsOneExactEdge/other_v2_package", "TestM2DependencyRestoreDeadlineIsOneExactEdge/playback",
			"TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/ambient_clock", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/blank_time_import", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/bounded_structural_capability", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/control_flow_owned_by_wp04", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/deadline_function_escape", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/generic_input_constraint_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/malformed_input_container_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/misplaced_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/missing_direct_deadline_call", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_array_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_channel_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_generic_union_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_interface_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_map_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_pointer_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_signature_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/named_slice_duration_state", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/package_scope_erased_duration_initializer", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/package_scope_inferred_duration", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/package_scope_inferred_duration_constant", "TestM2DependencyRestoreTimeUseIsDeadlineBudgetOnly/transitive_result_budget",
			"TestM2GateSourcesDoNotShelterExecutableBehavior/inspect.sh", "TestM2GateSourcesDoNotShelterExecutableBehavior/m1_crown_test.go",
		}),
	shortDefinition("core", "./internal/helpercmd", false, []string{"TestDriveExecutesIndependentWorkScopesInOneMissionConcurrently"}, nil),
	shortDefinition("core", "./internal/productionruntime", false, []string{"TestProgressReportsCompleteMissionWithoutExclusiveCurrentStep"}, nil),
	shortDefinition("race", "./internal/controljournal/application", true, []string{"TestServiceAggregateConcurrencyIsPerHeadNotGlobal"}, nil),
	shortDefinition("race", "./internal/integration", true, []string{"TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2"}, nil),
	shortDefinition("race", "./internal/testcampaign/m2/iterationreceipt", true, []string{"TestRecordBindsExecutingIterationFacts"}, nil),
	shortDefinition("mutants", "./internal/architecture", false, []string{"TestM2AutonomyMutationCampaignCoversCeremonyFailureClasses", "TestM2AbsenceLayerDetectors"}, nil),
	shortDefinition("capabilities-full", "./internal/helpercmd", false, []string{"TestDriveExecutesIndependentWorkScopesInOneMissionConcurrently"}, nil),
	shortDefinition("capabilities-full", "./internal/productionruntime", false, []string{"TestProgressReportsCompleteMissionWithoutExclusiveCurrentStep"}, nil),
	shortDefinition("capabilities-full", "./internal/scenario", false, []string{"TestConcurrentIndependentAuthoritySmoke"}, nil),
	shortDefinition("mutants-full", "./internal/productionruntime", false, []string{"TestWP13MandatoryProductionMutantCampaign"}, []string{
		"TestWP13MandatoryProductionMutantCampaign/arbitrary_executable", "TestWP13MandatoryProductionMutantCampaign/classifier_after_mutation", "TestWP13MandatoryProductionMutantCampaign/epoch_before_restore", "TestWP13MandatoryProductionMutantCampaign/lost_memory_loses_state", "TestWP13MandatoryProductionMutantCampaign/playback_external_io", "TestWP13MandatoryProductionMutantCampaign/possible_mutation_inherits_cancellation", "TestWP13MandatoryProductionMutantCampaign/possible_mutation_refresh_omitted", "TestWP13MandatoryProductionMutantCampaign/predecessor_client", "TestWP13MandatoryProductionMutantCampaign/projection_before_ack", "TestWP13MandatoryProductionMutantCampaign/query_mints_authority", "TestWP13MandatoryProductionMutantCampaign/raw_handle_escape", "TestWP13MandatoryProductionMutantCampaign/ready_before_recovery", "TestWP13MandatoryProductionMutantCampaign/refusal_publishes_checkpoint", "TestWP13MandatoryProductionMutantCampaign/second_host", "TestWP13MandatoryProductionMutantCampaign/sqlite_backend_selector", "TestWP13MandatoryProductionMutantCampaign/unadopted_record", "TestWP13MandatoryProductionMutantCampaign/watch_as_durable",
	}),
}

func shortDefinition(group, pkg string, race bool, selectors, children []string) ShortDefinition {
	expected := append(append([]string(nil), selectors...), children...)
	slices.Sort(expected)
	return ShortDefinition{Group: group, Package: pkg, Selectors: selectors, ExpectedTests: expected, Race: race}
}

type ShortReceipt struct {
	SchemaVersion int      `json:"schema_version"`
	Group         string   `json:"group"`
	Package       string   `json:"package"`
	Selectors     []string `json:"selectors"`
	Race          bool     `json:"race"`
	RawSHA256     string   `json:"raw_sha256"`
	Verdict       string   `json:"verdict"`
}

type ShortReport struct {
	SchemaVersion int            `json:"schema_version"`
	Receipts      []ShortReceipt `json:"receipts"`
	Verdict       string         `json:"verdict"`
}

func FrozenShortDefinitions() []ShortDefinition {
	result := make([]ShortDefinition, len(shortGateDefinitions))
	for index, definition := range shortGateDefinitions {
		result[index] = definition
		result[index].Selectors = append([]string(nil), definition.Selectors...)
		result[index].ExpectedTests = append([]string(nil), definition.ExpectedTests...)
	}
	return result
}

// RunShortGate executes every frozen selector and verifies exact structured
// run/pass membership. Missing, renamed, skipped, failed, duplicated, or
// unexpected test events are incomplete evidence even when go test exits 0.
func RunShortGate(ctx context.Context, repository, group string, timeout time.Duration) (ShortReport, error) {
	report := ShortReport{SchemaVersion: 1, Verdict: "fail"}
	if !filepath.IsAbs(repository) || timeout <= 0 || !slices.Contains([]string{"core", "mutants", "capabilities-full", "mutants-full"}, group) {
		return report, fmt.Errorf("short gate requires an absolute repository, positive timeout, and a closed gate group")
	}
	for _, definition := range shortGateDefinitions {
		if group == "core" && definition.Group != "core" && definition.Group != "race" || group != "core" && definition.Group != group {
			continue
		}
		pattern := "^(" + strings.Join(escapedSelectors(definition.Selectors), "|") + ")$"
		arguments := []string{"test", "-json", "-count=1", "-run", pattern}
		if definition.Race {
			arguments = append(arguments, "-race", "-p=1")
		}
		arguments = append(arguments, definition.Package)
		commandCtx, cancel := context.WithTimeout(ctx, timeout)
		command := exec.CommandContext(commandCtx, "go", arguments...)
		command.Dir = repository
		raw, runErr := command.CombinedOutput()
		timedOut := commandCtx.Err() != nil
		cancel()
		receipt, verifyErr := VerifyShortTranscript(raw, definition)
		report.Receipts = append(report.Receipts, receipt)
		if timedOut {
			return report, fmt.Errorf("short gate %s timed out", definition.Package)
		}
		if runErr != nil {
			return report, fmt.Errorf("short gate %s: %w", definition.Package, runErr)
		}
		if verifyErr != nil {
			return report, verifyErr
		}
	}
	if len(report.Receipts) == 0 {
		return report, fmt.Errorf("short gate selected no frozen definitions")
	}
	report.Verdict = "pass"
	return report, nil
}

func VerifyShortTranscript(raw []byte, definition ShortDefinition) (ShortReceipt, error) {
	receipt := ShortReceipt{SchemaVersion: 1, Group: definition.Group, Package: definition.Package, Selectors: append([]string(nil), definition.Selectors...), Race: definition.Race, RawSHA256: SHA256(raw), Verdict: "fail"}
	want := make(map[string]struct{}, len(definition.ExpectedTests))
	for _, selector := range definition.ExpectedTests {
		if selector == "" {
			return receipt, fmt.Errorf("short gate has empty selector")
		}
		if _, duplicate := want[selector]; duplicate {
			return receipt, fmt.Errorf("short gate selector %q is duplicated", selector)
		}
		want[selector] = struct{}{}
	}
	type counts struct{ run, pass, skip, fail int }
	observed := make(map[string]*counts, len(want))
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			return receipt, fmt.Errorf("short gate transcript contains non-JSON: %w", err)
		}
		if event.Test == "" {
			continue
		}
		if _, exists := want[event.Test]; !exists {
			return receipt, fmt.Errorf("short gate observed unexpected selector %q", event.Test)
		}
		if observed[event.Test] == nil {
			observed[event.Test] = &counts{}
		}
		switch event.Action {
		case "run":
			observed[event.Test].run++
		case "pass":
			observed[event.Test].pass++
		case "skip":
			observed[event.Test].skip++
		case "fail":
			observed[event.Test].fail++
		}
	}
	for _, selector := range definition.ExpectedTests {
		counts := observed[selector]
		if counts == nil || counts.run != 1 || counts.pass != 1 || counts.skip != 0 || counts.fail != 0 {
			return receipt, fmt.Errorf("short gate selector %q lacks one exact run/pass receipt", selector)
		}
	}
	receipt.Verdict = "pass"
	return receipt, nil
}

func escapedSelectors(selectors []string) []string {
	result := make([]string, len(selectors))
	for index, selector := range selectors {
		result[index] = regexp.QuoteMeta(selector)
	}
	return result
}

func validateShortDefinitions(definitions []ShortDefinition) error {
	want := FrozenShortDefinitions()
	if !slices.EqualFunc(definitions, want, func(left, right ShortDefinition) bool {
		return left.Group == right.Group && left.Package == right.Package && left.Race == right.Race && slices.Equal(left.Selectors, right.Selectors) && slices.Equal(left.ExpectedTests, right.ExpectedTests)
	}) {
		return fmt.Errorf("short gate census is reordered, removed, or substituted")
	}
	return nil
}
