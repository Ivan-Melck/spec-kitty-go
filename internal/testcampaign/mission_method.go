package testcampaign

import (
	"fmt"
	"slices"
	"strings"
)

const (
	MissionContractProfile     = "mission-contract"
	MissionAcceptanceProfile   = "mission-acceptance"
	MissionAdversarialProfile  = "mission-adversarial"
	MissionRealIdentityProfile = "mission-real-identity"
	MissionFullCampaign        = "mission_full"
)

var missionProfiles = map[string]Profile{
	MissionContractProfile: {
		Name: MissionContractProfile, Package: "./internal/scenario",
		Pattern: "^(TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation|TestMissionMethodQualificationContract|TestQualityCampaignUniversalInvariants|TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries)$",
		ExpectedTests: []string{
			"TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation",
			"TestMissionMethodQualificationContract",
			"TestQualityCampaignUniversalInvariants",
			"TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries",
		},
	},
	MissionRealIdentityProfile: {
		Name: MissionRealIdentityProfile, Package: "./internal/scenario",
		Pattern:       "^(TestMissionMethodBeadsQualificationProfileIdentity)$",
		ExpectedTests: []string{"TestMissionMethodBeadsQualificationProfileIdentity"},
	},
	MissionAcceptanceProfile: {
		Name: MissionAcceptanceProfile, Package: "./internal/scenario",
		Pattern: "^(TestMissionMethodNormalizationAndPureQueries100|TestMissionMethodProjectionAndGovernanceIndependence100|TestMissionMethodProviderParityPortableCampaign)$",
		ExpectedTests: []string{
			"TestMissionMethodNormalizationAndPureQueries100",
			"TestMissionMethodProjectionAndGovernanceIndependence100",
			"TestMissionMethodProviderParityPortableCampaign",
		},
	},
	MissionAdversarialProfile: {
		Name: MissionAdversarialProfile, Package: "./internal/scenario",
		Pattern: "^(TestDishonestMissionAuthorityDetectsSameCountOutboxMutation|TestMissionMethodBeadsDishonestProviderRefusesBeforePublication|TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse|TestMissionMethodCrashBoundaryCensus|TestMissionMethodFinalizationProcessRaces100|TestMissionMethodSemanticDriftMutations1000)$",
		ExpectedTests: []string{
			"TestDishonestMissionAuthorityDetectsSameCountOutboxMutation",
			"TestMissionMethodBeadsDishonestProviderRefusesBeforePublication",
			"TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse",
			"TestMissionMethodCrashBoundaryCensus",
			"TestMissionMethodFinalizationProcessRaces100",
			"TestMissionMethodSemanticDriftMutations1000",
		},
	},
}

func init() {
	// Freeze the historical Phase 1/2 all-scenario selector before registering
	// Phase 3 tests. Its expected census and meaning remain unchanged; the new
	// mission_full campaign owns the additional top-level tests explicitly.
	historical := profiles["full-scenario"]
	if historical.Pattern == "" {
		historical.Pattern = "^(" + strings.Join(historical.ExpectedTests, "|") + ")$"
		profiles["full-scenario"] = historical
	}
	for name, profile := range missionProfiles {
		profiles[name] = profile
	}
	campaign := Campaign{
		Name:             MissionFullCampaign,
		Profiles:         []string{MissionAcceptanceProfile, MissionAdversarialProfile, MissionContractProfile},
		RequiredProfiles: []string{MissionAcceptanceProfile, MissionAdversarialProfile, MissionContractProfile},
		UniversalTest:    "TestQualityCampaignUniversalInvariants",
		RequiredTests: []string{
			"TestDishonestMissionAuthorityDetectsSameCountOutboxMutation",
			"TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation",
			"TestMissionMethodBeadsDishonestProviderRefusesBeforePublication",
			"TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse",
			"TestMissionMethodCrashBoundaryCensus",
			"TestMissionMethodFinalizationProcessRaces100",
			"TestMissionMethodNormalizationAndPureQueries100",
			"TestMissionMethodProjectionAndGovernanceIndependence100",
			"TestMissionMethodProviderParityPortableCampaign",
			"TestMissionMethodQualificationContract",
			"TestMissionMethodSemanticDriftMutations1000",
			"TestQualityCampaignUniversalInvariants",
			"TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries",
		},
		RequireUniversal: true, RequireStaleProbe: true,
	}
	if err := ValidateMissionCampaign(campaign); err != nil {
		panic(err)
	}
	campaigns[MissionFullCampaign] = campaign
}

// ValidateMissionCampaign freezes the Phase 3 selector census. Generic
// campaign validation proves internal consistency; this additional check
// prevents a simultaneously weakened profile and required-test list from
// silently redefining qualification.
func ValidateMissionCampaign(campaign Campaign) error {
	wantProfiles := []string{MissionAcceptanceProfile, MissionAdversarialProfile, MissionContractProfile}
	wantTests := []string{
		"TestDishonestMissionAuthorityDetectsSameCountOutboxMutation",
		"TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation",
		"TestMissionMethodBeadsDishonestProviderRefusesBeforePublication",
		"TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse",
		"TestMissionMethodCrashBoundaryCensus",
		"TestMissionMethodFinalizationProcessRaces100",
		"TestMissionMethodNormalizationAndPureQueries100",
		"TestMissionMethodProjectionAndGovernanceIndependence100",
		"TestMissionMethodProviderParityPortableCampaign",
		"TestMissionMethodQualificationContract",
		"TestMissionMethodSemanticDriftMutations1000",
		"TestQualityCampaignUniversalInvariants",
		"TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries",
	}
	if campaign.Name != MissionFullCampaign || !slices.Equal(campaign.Profiles, wantProfiles) || !slices.Equal(campaign.RequiredProfiles, wantProfiles) {
		return fmt.Errorf("mission campaign profile census changed")
	}
	if !slices.Equal(campaign.RequiredTests, wantTests) {
		return fmt.Errorf("mission campaign test census changed")
	}
	if campaign.UniversalTest != "TestQualityCampaignUniversalInvariants" || !campaign.RequireUniversal || !campaign.RequireStaleProbe {
		return fmt.Errorf("mission campaign universal invariant changed")
	}
	return ValidateCampaign(campaign)
}

// ValidateMissionReport adds the Phase 3 delivery law to the generic census:
// every required test must pass. A skip is terminal evidence, but it is never
// qualification. The campaign runner must apply this validation before it can
// publish a mission-profile pass.
func ValidateMissionReport(report Report) error {
	if report.Verdict != "pass" {
		return fmt.Errorf("mission report did not pass its exact census")
	}
	for _, name := range report.ExpectedTests {
		if report.TerminalTests[name] != "pass" {
			return fmt.Errorf("mission report required test %s terminated as %s", name, report.TerminalTests[name])
		}
	}
	if !slices.Equal(report.ExpectedTests, sortedCopy(report.ExpectedTests)) {
		return fmt.Errorf("mission report expected census is not canonical")
	}
	return nil
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
	slices.Sort(result)
	return result
}
