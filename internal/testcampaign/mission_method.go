package testcampaign

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	MissionContractProfile     = "mission-contract"
	MissionAcceptanceProfile   = "mission-acceptance"
	MissionAdversarialProfile  = "mission-adversarial"
	MissionRealIdentityProfile = "mission-real-identity"
	MissionFullCampaign        = "mission_full"
	Phase3ArchitectureProfile  = "phase3-architecture"
)

var phase3ArchitectureTests = []string{
	"TestBeadsDocumentationDeliveryClaims",
	"TestBeadsDocumentationLinksMermaidAndCommands",
	"TestCloseoutBinaryAndReviewPolicyDocumentation",
	"TestCloseoutOrchestratorAPI13ProjectionContract",
	"TestCurrentProductionGraphPassesClosedPolicy",
	"TestDefaultPolicyClassifiesCampaignToolsWithoutExposingThemToAuthority",
	"TestDefaultPolicyPinsTrackerConformanceAsOneWayQualityTooling",
	"TestDefaultPolicyRejectsAuthorityImportOfGitAdapter",
	"TestDefaultReachabilityRegistryPassesCurrentRepository",
	"TestDeferredRuntimeSurfaceDoesNotGrepMarkdownProse",
	"TestDeferredRuntimeSurfaceDoesNotGuessAmbiguousMarkdownFlows",
	"TestDeferredRuntimeSurfaceRejectsConstantAliasesAndDotImport",
	"TestDeferredRuntimeSurfaceRejectsLocalShortDeclarationAlias",
	"TestDeferredRuntimeSurfaceRejectsMarkdownAuthorityParsing",
	"TestDeferredRuntimeSurfaceRejectsPackageAliasDeclaredInAnotherFile",
	"TestDeferredRuntimeSurfaceRejectsProductionPythonButIgnoresTests",
	"TestLoadGraphDistinguishesProductionAndTestImports",
	"TestLoadGraphReturnsTypedAndBoundedFailures",
	"TestLoadedUnknownRootCannotDisappearThroughGraphExemption",
	"TestPackagePatternsAreSlashBoundedAndAmbiguityIsDeterministic",
	"TestPhase2EvidenceDocumentationMutants",
	"TestPhase2EvidenceIntegrity",
	"TestPolicyAcceptsProviderOnlyFromClassifiedAdapter",
	"TestPolicyRejectingFixturesNameExactRuleAndEdge",
	"TestPolicyValidationConfinesProviderAllowancesToAdapters",
	"TestReachabilityCallerPolicies",
	"TestReachabilityHonorsActiveCustomBuildTags",
	"TestReachabilityRejectsBuildExcludedCallers",
	"TestReachabilityRejectsGuardCalledOnlyByTest",
	"TestReachabilityRejectsMissingRegisteredTarget",
	"TestReachabilityRejectsShadowedTargetCalls",
	"TestReachabilityRejectsTargetSelfRecursion",
	"TestReachabilityRejectsUnrootedMutualRecursion",
	"TestRemovingEachRegisteredTargetsOnlyProductionCallerReddensFixture",
	"TestRemovingRealAuthorityToContractRuleNamesRealEdge",
	"TestTierRuleAndViolationValueValidation",
	"TestTypedProductionSourceLoadFailuresAreTyped",
	"TestTypedProductionSourceLoaderCancelsStalledGoList",
}

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
	// The signed Phase 3 publication records the architecture census that ran
	// at that point in time. Keep that historical selector immutable while the
	// live architecture profile grows with later mission guards.
	profiles[Phase3ArchitectureProfile] = Profile{
		Name:          Phase3ArchitectureProfile,
		Package:       "./internal/architecture",
		Pattern:       "^(" + strings.Join(phase3ArchitectureTests, "|") + ")$",
		ExpectedTests: append([]string(nil), phase3ArchitectureTests...),
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

const phase3PublicationSchema = 3

const phase3EvidenceFileLimit = 2 << 20

var (
	phase3CommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	phase3DigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	phase3LinkPattern   = regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	phase3TestPattern   = regexp.MustCompile("`(Test[A-Za-z0-9_]+)`")
)

type phase3Counts struct {
	Expected   int    `json:"expected"`
	Started    int    `json:"started"`
	Terminal   int    `json:"terminal"`
	Passed     int    `json:"passed"`
	Failed     int    `json:"failed"`
	Skipped    int    `json:"skipped,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	TimeoutMS  int64  `json:"timeout_ms,omitempty"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	Verdict    string `json:"verdict,omitempty"`
}

type phase3Qualification struct {
	SchemaVersion       int    `json:"schema_version"`
	Verdict             string `json:"verdict"`
	FailureHistoryCount int    `json:"failure_history_count"`
	FailureHistorySHA   string `json:"failure_history_sha256"`
	TestedSource        struct {
		Commit string `json:"commit"`
		Tree   string `json:"tree"`
		Clean  bool   `json:"clean"`
	} `json:"tested_source"`
	Publication struct {
		PayloadCommit *string `json:"payload_commit"`
		Disposition   string  `json:"disposition"`
	} `json:"publication"`
	Host struct {
		GOOS               string  `json:"goos"`
		GOARCH             string  `json:"goarch"`
		GoVersion          string  `json:"go_version"`
		GitVersion         string  `json:"git_version"`
		StaticcheckVersion string  `json:"staticcheck_version"`
		GovulncheckVersion string  `json:"govulncheck_version"`
		HostedBaseline     *string `json:"hosted_baseline"`
	} `json:"host"`
	PortableCampaign struct {
		StartedAt              string       `json:"started_at"`
		FinishedAt             string       `json:"finished_at"`
		Quality                phase3Counts `json:"quality"`
		HistoricalFullScenario phase3Counts `json:"historical_full_scenario"`
		MissionFull            phase3Counts `json:"mission_full"`
	} `json:"portable_campaign"`
	MissionProfiles struct {
		Contract    phase3Counts `json:"mission_contract"`
		Acceptance  phase3Counts `json:"mission_acceptance"`
		Adversarial phase3Counts `json:"mission_adversarial"`
	} `json:"mission_profiles"`
	ScaleProofs struct {
		SemanticDrift struct {
			Seed                     int64 `json:"seed"`
			GeneratorSchema          int   `json:"generator_schema"`
			Expected                 int   `json:"expected"`
			Changed                  int   `json:"changed"`
			Silent                   int   `json:"silent"`
			NormalizationRepetitions int   `json:"normalization_repetitions"`
		} `json:"semantic_drift"`
		TwoProcessFinalization struct {
			Attempts                   int `json:"attempts"`
			CurrentGenerationCountEach int `json:"current_generation_count_each"`
			UnexpectedErrors           int `json:"unexpected_errors"`
			RestartAudits              int `json:"restart_audits"`
			Winners                    int `json:"winners,omitempty"`
			Replays                    int `json:"replays,omitempty"`
			Conflicts                  int `json:"conflicts,omitempty"`
		} `json:"two_process_finalization"`
		CrashBoundaries struct {
			Expected int                 `json:"expected"`
			Started  int                 `json:"started"`
			Terminal int                 `json:"terminal"`
			Passed   int                 `json:"passed"`
			Failed   int                 `json:"failed"`
			Faults   []phase3FaultResult `json:"faults,omitempty"`
		} `json:"crash_boundaries"`
		ProjectionTrials struct {
			Expected           int                  `json:"expected"`
			Passed             int                  `json:"passed"`
			AuthorityMutations int                  `json:"authority_mutations"`
			TrialKinds         map[string]int       `json:"trial_kinds,omitempty"`
			Mutants            []phase3MutantResult `json:"mutants,omitempty"`
		} `json:"projection_trials"`
		GovernanceHistoryTrials struct {
			Expected         int `json:"expected"`
			Passed           int `json:"passed"`
			HistoryMutations int `json:"history_mutations"`
		} `json:"governance_history_trials"`
		PureNextQueries struct {
			Expected int `json:"expected"`
			Passed   int `json:"passed"`
			Writes   int `json:"writes"`
		} `json:"pure_next_queries"`
	} `json:"scale_proofs"`
	ProviderQualification struct {
		Provider                string `json:"provider"`
		Version                 string `json:"version"`
		BinarySHA256            string `json:"binary_sha256"`
		SourceTagCommit         string `json:"source_tag_commit"`
		ProtocolSchema          int    `json:"protocol_schema"`
		StorageMode             string `json:"storage_mode"`
		OfflineNetworkNamespace bool   `json:"offline_network_namespace"`
		Expected                int    `json:"expected"`
		Started                 int    `json:"started"`
		Terminal                int    `json:"terminal"`
		Passed                  int    `json:"passed"`
		Failed                  int    `json:"failed"`
		StartedAt               string `json:"started_at"`
		FinishedAt              string `json:"finished_at"`
		ArtifactSHA256          string `json:"artifact_sha256"`
		Contention              struct {
			Processes  int     `json:"processes"`
			Attempted  int     `json:"attempted"`
			Terminal   int     `json:"terminal"`
			Passed     int     `json:"passed"`
			Collisions int     `json:"collisions"`
			Duplicates int     `json:"duplicates"`
			DurationMS float64 `json:"duration_ms"`
		} `json:"contention"`
		AuthorityAudits            int `json:"authority_audits"`
		AuthorityInvariantFailures int `json:"authority_invariant_failures"`
		MissionMethodTwoItem       struct {
			Expected                   int    `json:"expected"`
			Started                    int    `json:"started"`
			Terminal                   int    `json:"terminal"`
			Passed                     int    `json:"passed"`
			Failed                     int    `json:"failed"`
			DurationMS                 int64  `json:"duration_ms"`
			RawArtifactRetainedOutside bool   `json:"raw_artifact_retained_outside_repository"`
			RawArtifactSHA256          string `json:"raw_artifact_sha256"`
		} `json:"mission_method_two_item"`
	} `json:"provider_qualification"`
	QualityGates []struct {
		Name              string `json:"name"`
		Verdict           string `json:"verdict"`
		DurationMS        *int64 `json:"duration_ms"`
		ReachableFindings *int   `json:"reachable_findings,omitempty"`
	} `json:"quality_gates"`
	Privacy struct {
		AbsoluteHomePaths      int `json:"absolute_home_paths"`
		Credentials            int `json:"credentials"`
		EnvironmentDumps       int `json:"environment_dumps"`
		PrivateTranscripts     int `json:"private_transcripts"`
		RawProviderDatabases   int `json:"raw_provider_databases"`
		UnrestrictedToolOutput int `json:"unrestricted_tool_output"`
	} `json:"privacy"`
}

type phase3FaultResult struct {
	ID       string `json:"id"`
	Expected int    `json:"expected"`
	Started  int    `json:"started"`
	Terminal int    `json:"terminal"`
	Verdict  string `json:"verdict"`
}

type phase3MutantResult struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
}

type phase3Census struct {
	SchemaVersion       int                     `json:"schema_version"`
	Verdict             string                  `json:"verdict"`
	SourceCommit        string                  `json:"source_commit"`
	SourceTree          string                  `json:"source_tree"`
	SourceClean         bool                    `json:"source_clean"`
	FailureHistoryCount int                     `json:"failure_history_count"`
	FailureHistorySHA   string                  `json:"failure_history_sha256"`
	Profiles            []phase3ProfileEvidence `json:"profiles"`
	Supporting          []phase3ProfileEvidence `json:"supporting_profiles"`
	Campaigns           []struct {
		Name           string `json:"name"`
		Verdict        string `json:"verdict"`
		DurationMS     int64  `json:"duration_ms"`
		ArtifactSHA256 string `json:"artifact_sha256"`
	} `json:"campaigns"`
	RealProvider struct {
		Phase2FrozenRunner phase3ProviderRun `json:"phase2_frozen_runner"`
		Phase3TwoItem      phase3ProviderRun `json:"phase3_two_item_selector"`
	} `json:"real_provider"`
	FailedAttempts []phase3FailedAttempt `json:"failed_or_non_admissible_attempts"`
	HostedBaseline struct {
		RequiredSamples int    `json:"required_samples"`
		ObservedSamples int    `json:"observed_samples"`
		DurationMS      *int64 `json:"duration_ms"`
		Verdict         string `json:"verdict"`
	} `json:"hosted_baseline"`
}

type phase3FailedAttempt struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

type phase3ProfileEvidence struct {
	Name           string   `json:"name"`
	Class          string   `json:"class,omitempty"`
	Expected       int      `json:"expected"`
	Started        int      `json:"started"`
	Terminal       int      `json:"terminal"`
	Passed         int      `json:"passed"`
	Failed         int      `json:"failed"`
	Skipped        int      `json:"skipped"`
	DurationMS     int64    `json:"duration_ms"`
	ArtifactSHA256 string   `json:"artifact_sha256"`
	Tests          []string `json:"tests,omitempty"`
}

type phase3ProviderRun struct {
	Expected       int    `json:"expected"`
	Started        int    `json:"started"`
	Terminal       int    `json:"terminal"`
	Passed         int    `json:"passed"`
	Failed         int    `json:"failed"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

// ValidatePhase3Publication verifies the checked-out Phase 3 publication as a
// closed, source-bound evidence record. It intentionally validates bytes and
// claims without assigning qualification to the checkout that happens to
// contain them; source, payload, and attestation remain distinct identities.
func ValidatePhase3Publication(root string) error {
	evidencePath := filepath.Join(root, "docs", "phase3-evidence")
	var evidence phase3Qualification
	if err := phase3DecodeClosed(filepath.Join(evidencePath, "qualification-evidence.json"), &evidence); err != nil {
		return fmt.Errorf("phase3 qualification schema: %w", err)
	}
	var census phase3Census
	if err := phase3DecodeClosed(filepath.Join(evidencePath, "campaign-census.json"), &census); err != nil {
		return fmt.Errorf("phase3 census schema: %w", err)
	}
	if err := validatePhase3MachineEvidence(evidence, census); err != nil {
		return err
	}
	if err := validatePhase3Checksums(evidencePath); err != nil {
		return err
	}
	if err := validatePhase3Documents(root, evidencePath, census); err != nil {
		return err
	}
	return nil
}

func phase3DecodeClosed(path string, target any) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > phase3EvidenceFileLimit {
		return fmt.Errorf("evidence JSON exceeds %d-byte regular-file limit", phase3EvidenceFileLimit)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = phase3RejectDuplicateJSONKeys(contents); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func phase3RejectDuplicateJSONKeys(contents []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return keyErr
				}
				key, keyOK := keyToken.(string)
				if !keyOK || seen[key] {
					return fmt.Errorf("duplicate or malformed JSON object key")
				}
				seen[key] = true
				if err = scan(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err = scan(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		closing, closeErr := decoder.Token()
		if closeErr != nil || closing != json.Delim(map[json.Delim]rune{'{': '}', '[': ']'}[delim]) {
			return fmt.Errorf("malformed JSON container")
		}
		return nil
	}
	if err := scan(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func validatePhase3MachineEvidence(e phase3Qualification, c phase3Census) error {
	if e.SchemaVersion < 1 || e.SchemaVersion > phase3PublicationSchema || c.SchemaVersion != e.SchemaVersion {
		return fmt.Errorf("phase3 schema version mismatch")
	}
	if e.Verdict != "pass" || c.Verdict != "pass" || !e.TestedSource.Clean || !c.SourceClean {
		return fmt.Errorf("phase3 pass requires clean passing source records")
	}
	if !phase3CommitPattern.MatchString(e.TestedSource.Commit) || !phase3CommitPattern.MatchString(e.TestedSource.Tree) || e.TestedSource.Commit != c.SourceCommit || e.TestedSource.Tree != c.SourceTree {
		return fmt.Errorf("phase3 source identity mismatch")
	}
	if strings.TrimSpace(e.Publication.Disposition) == "" || e.Host.GOOS != "linux" || e.Host.GOARCH != "amd64" || e.Host.GoVersion != "go1.26.5" || e.Host.HostedBaseline != nil || e.PortableCampaign.StartedAt == "" || e.PortableCampaign.FinishedAt == "" {
		return fmt.Errorf("phase3 provenance record is incomplete")
	}
	for name, counts := range map[string]phase3Counts{
		"historical_full_scenario": e.PortableCampaign.HistoricalFullScenario,
		"mission_full":             e.PortableCampaign.MissionFull,
		"mission_contract":         e.MissionProfiles.Contract,
		"mission_acceptance":       e.MissionProfiles.Acceptance,
		"mission_adversarial":      e.MissionProfiles.Adversarial,
	} {
		if err := validatePhase3PassingCounts(name, counts); err != nil {
			return err
		}
	}
	if e.PortableCampaign.Quality.Verdict != "pass" || e.PortableCampaign.Quality.DurationMS <= 0 {
		return fmt.Errorf("phase3 quality result is not a measured pass")
	}
	if e.ScaleProofs.SemanticDrift.Expected != 1000 || e.ScaleProofs.SemanticDrift.Changed != 1000 || e.ScaleProofs.SemanticDrift.Silent != 0 || e.ScaleProofs.SemanticDrift.NormalizationRepetitions != 100 || e.ScaleProofs.SemanticDrift.Seed == 0 {
		return fmt.Errorf("phase3 semantic drift scale proof changed")
	}
	races := e.ScaleProofs.TwoProcessFinalization
	if races.Attempts != 100 || races.CurrentGenerationCountEach != 1 || races.UnexpectedErrors != 0 || races.RestartAudits != 100 {
		return fmt.Errorf("phase3 finalization race proof changed")
	}
	if e.SchemaVersion >= 2 && (races.Winners != 100 || races.Replays != 50 || races.Conflicts != 50) {
		return fmt.Errorf("phase3 finalization outcomes incomplete")
	}
	crashes := e.ScaleProofs.CrashBoundaries
	if crashes.Expected != 9 || crashes.Started != 9 || crashes.Terminal != 9 || crashes.Passed != 9 || crashes.Failed != 0 {
		return fmt.Errorf("phase3 crash census changed")
	}
	if e.SchemaVersion >= 2 {
		if err := validatePhase3Faults(crashes.Faults); err != nil {
			return err
		}
		if err := validatePhase3ProjectionEvidence(e.ScaleProofs.ProjectionTrials.TrialKinds, e.ScaleProofs.ProjectionTrials.Mutants); err != nil {
			return err
		}
	}
	if e.ScaleProofs.ProjectionTrials.Expected != 100 || e.ScaleProofs.ProjectionTrials.Passed != 100 || e.ScaleProofs.ProjectionTrials.AuthorityMutations != 0 || e.ScaleProofs.GovernanceHistoryTrials.Expected != 100 || e.ScaleProofs.GovernanceHistoryTrials.Passed != 100 || e.ScaleProofs.GovernanceHistoryTrials.HistoryMutations != 0 || e.ScaleProofs.PureNextQueries.Expected != 100 || e.ScaleProofs.PureNextQueries.Passed != 100 || e.ScaleProofs.PureNextQueries.Writes != 0 {
		return fmt.Errorf("phase3 projection/governance/query scale proof changed")
	}
	if err := validatePhase3Provider(e.ProviderQualification); err != nil {
		return err
	}
	if err := validatePhase3Profiles(c, e.SchemaVersion); err != nil {
		return err
	}
	if err := validatePhase3Campaigns(c); err != nil {
		return err
	}
	if err := validatePhase3History(c.FailedAttempts, e.FailureHistoryCount, e.FailureHistorySHA, c.FailureHistoryCount, c.FailureHistorySHA); err != nil {
		return err
	}
	if c.HostedBaseline.RequiredSamples != 10 || c.HostedBaseline.ObservedSamples != 0 || c.HostedBaseline.DurationMS != nil || c.HostedBaseline.Verdict != "pending" {
		return fmt.Errorf("phase3 hosted baseline was overstated")
	}
	if e.Privacy.AbsoluteHomePaths != 0 || e.Privacy.Credentials != 0 || e.Privacy.EnvironmentDumps != 0 || e.Privacy.PrivateTranscripts != 0 || e.Privacy.RawProviderDatabases != 0 || e.Privacy.UnrestrictedToolOutput != 0 {
		return fmt.Errorf("phase3 privacy scan is not clean")
	}
	return validatePhase3QualityGates(e.QualityGates)
}

func validatePhase3PassingCounts(name string, counts phase3Counts) error {
	if counts.Expected <= 0 || counts.Expected != counts.Started || counts.Started != counts.Terminal || counts.Terminal != counts.Passed || counts.Failed != 0 || counts.Skipped != 0 || counts.DurationMS <= 0 || counts.TimedOut {
		return fmt.Errorf("phase3 %s census is not an exact pass", name)
	}
	return nil
}

func validatePhase3Faults(actual []phase3FaultResult) error {
	want := []string{
		"MM-ARTIFACT-APPROVAL-TX-ABORT", "MM-ARTIFACT-POSTCOMMIT-RESPONSE", "MM-DECOMPOSITION-APPLY-LOST-RESPONSE",
		"MM-FINALIZATION-PREWRITE-PROVIDER", "MM-FINALIZATION-TX-ABORT", "MM-FINALIZATION-POSTCOMMIT-RESPONSE",
		"MM-RECONCILIATION-DRIFT-TX-ABORT", "MM-RECONCILIATION-LATE-TX-ABORT", "MM-RECONCILIATION-POSTCOMMIT-RESPONSE",
	}
	if len(actual) != len(want) {
		return fmt.Errorf("phase3 fault identity census changed")
	}
	for index, id := range want {
		got := actual[index]
		if got.ID != id || got.Expected != 1 || got.Started != 1 || got.Terminal != 1 || got.Verdict != "pass" {
			return fmt.Errorf("phase3 fault %s is not an exact pass", id)
		}
	}
	return nil
}

func validatePhase3ProjectionEvidence(kinds map[string]int, mutants []phase3MutantResult) error {
	wantKinds := map[string]int{"delete": 13, "truncate": 13, "edit": 13, "checkbox": 13, "stale": 12, "invalid-utf8": 12, "symlink": 12, "other-mission": 12}
	if len(kinds) != len(wantKinds) {
		return fmt.Errorf("phase3 projection trial-kind census changed")
	}
	for kind, count := range wantKinds {
		if kinds[kind] != count {
			return fmt.Errorf("phase3 projection trial kind %s changed", kind)
		}
	}
	wantMutants := []string{"projection_checkbox_authority", "projection_import_path", "governance_current_source", "governance_commitment_omission"}
	if len(mutants) != len(wantMutants) {
		return fmt.Errorf("phase3 projection mutant census changed")
	}
	for index, id := range wantMutants {
		if mutants[index].ID != id || mutants[index].Verdict != "killed" {
			return fmt.Errorf("phase3 projection mutant %s survived", id)
		}
	}
	return nil
}

func validatePhase3Provider(provider struct {
	Provider                string `json:"provider"`
	Version                 string `json:"version"`
	BinarySHA256            string `json:"binary_sha256"`
	SourceTagCommit         string `json:"source_tag_commit"`
	ProtocolSchema          int    `json:"protocol_schema"`
	StorageMode             string `json:"storage_mode"`
	OfflineNetworkNamespace bool   `json:"offline_network_namespace"`
	Expected                int    `json:"expected"`
	Started                 int    `json:"started"`
	Terminal                int    `json:"terminal"`
	Passed                  int    `json:"passed"`
	Failed                  int    `json:"failed"`
	StartedAt               string `json:"started_at"`
	FinishedAt              string `json:"finished_at"`
	ArtifactSHA256          string `json:"artifact_sha256"`
	Contention              struct {
		Processes  int     `json:"processes"`
		Attempted  int     `json:"attempted"`
		Terminal   int     `json:"terminal"`
		Passed     int     `json:"passed"`
		Collisions int     `json:"collisions"`
		Duplicates int     `json:"duplicates"`
		DurationMS float64 `json:"duration_ms"`
	} `json:"contention"`
	AuthorityAudits            int `json:"authority_audits"`
	AuthorityInvariantFailures int `json:"authority_invariant_failures"`
	MissionMethodTwoItem       struct {
		Expected                   int    `json:"expected"`
		Started                    int    `json:"started"`
		Terminal                   int    `json:"terminal"`
		Passed                     int    `json:"passed"`
		Failed                     int    `json:"failed"`
		DurationMS                 int64  `json:"duration_ms"`
		RawArtifactRetainedOutside bool   `json:"raw_artifact_retained_outside_repository"`
		RawArtifactSHA256          string `json:"raw_artifact_sha256"`
	} `json:"mission_method_two_item"`
}) error {
	if provider.Provider != "beads" || provider.Version != "1.2.2" || !phase3DigestPattern.MatchString(provider.BinarySHA256) || !phase3CommitPattern.MatchString(provider.SourceTagCommit) || provider.ProtocolSchema != 1 || provider.StorageMode != "server_multiwriter" || !provider.OfflineNetworkNamespace {
		return fmt.Errorf("phase3 provider identity changed")
	}
	if provider.Expected != 11 || provider.Started != 11 || provider.Terminal != 11 || provider.Passed != 11 || provider.Failed != 0 || !phase3DigestPattern.MatchString(provider.ArtifactSHA256) || provider.AuthorityAudits != 23 || provider.AuthorityInvariantFailures != 0 {
		return fmt.Errorf("phase3 provider qualification is incomplete")
	}
	if provider.Contention.Processes != 8 || provider.Contention.Attempted != 1000 || provider.Contention.Terminal != 1000 || provider.Contention.Passed != 1000 || provider.Contention.Collisions != 0 || provider.Contention.Duplicates != 0 || provider.Contention.DurationMS <= 0 {
		return fmt.Errorf("phase3 provider contention proof changed")
	}
	two := provider.MissionMethodTwoItem
	if two.Expected != 1 || two.Started != 1 || two.Terminal != 1 || two.Passed != 1 || two.Failed != 0 || two.DurationMS <= 0 || !two.RawArtifactRetainedOutside || !phase3DigestPattern.MatchString(two.RawArtifactSHA256) {
		return fmt.Errorf("phase3 provider two-item proof changed")
	}
	return nil
}

func validatePhase3Profiles(c phase3Census, schema int) error {
	// Phase 3 is an immutable historical publication. Its evidence must remain
	// valid when the live campaign registry evolves, while substitutions in the
	// published census must still fail closed. These commitments are the SHA-256
	// of each publication-time, newline-joined test list (without a trailing
	// newline); they deliberately do not consult ProfileByName.
	type frozenProfile struct {
		count  int
		digest string
	}
	wantPrimary := map[string]frozenProfile{
		MissionContractProfile:     {count: 4, digest: "c55011aaa9983d3e136600dc697d2aa7ab98acd8ab2b0b90ddb9a9bfd53b192c"},
		MissionAcceptanceProfile:   {count: 3, digest: "6885265520f1d69f09811ff1a6e2426e76b065301d0f73b2c41c8d45464a1d9e"},
		MissionAdversarialProfile:  {count: 6, digest: "a4c136f49a063832044e21d19938097e6520b3e931a51645cbda877cbc25b2b9"},
		MissionRealIdentityProfile: {count: 1, digest: "a137b9a4db990c41a3674db6424349631fb70b376102b1c497a171ab7167489d"},
	}
	if len(c.Profiles) != 4 {
		return fmt.Errorf("phase3 primary profile census changed")
	}
	seen := map[string]bool{}
	for _, profile := range c.Profiles {
		want, ok := wantPrimary[profile.Name]
		if !ok {
			return fmt.Errorf("phase3 profile %s changed", profile.Name)
		}
		if err := validatePhase3ProfileEvidence(profile); err != nil {
			return err
		}
		seen[profile.Name] = true
		if profile.Expected != want.count || (schema >= 2 && phase3TestIdentityDigest(profile.Tests) != want.digest) {
			return fmt.Errorf("phase3 profile %s test identities changed", profile.Name)
		}
	}
	for _, name := range []string{MissionContractProfile, MissionAcceptanceProfile, MissionAdversarialProfile, MissionRealIdentityProfile} {
		if !seen[name] {
			return fmt.Errorf("phase3 profile %s absent", name)
		}
	}
	if len(c.Supporting) != 5 {
		return fmt.Errorf("phase3 supporting profile census changed")
	}
	wantSupporting := map[string]frozenProfile{
		"provider-portable":        {count: 2, digest: "3f234767b486a1c026d6bbda8e384f53e60847023b5bf1b40ce5339a42d747c7"},
		"provider-adversarial":     {count: 20, digest: "295e8fc3bbc4e319687ebfad0f15894b50d83778978ae1c2d1756ae5e55e5f84"},
		"architecture":             {count: 38, digest: "25846ae2b39f94f242d4b5996e93e4de00bb73221d190f9354c52c53e45ebe3c"},
		"scope":                    {count: 2, digest: "6374a2baf5a872ed1dbc06f5f0494bcd72d02e7d32c7f3693f772e90dfe329a8"},
		"historical-full-scenario": {count: 73, digest: "9e47b5eb97e50940eda21b40acd2fc5ddb3844226eb3657952cd5e4bcb825ae9"},
	}
	for _, profile := range c.Supporting {
		want, ok := wantSupporting[profile.Name]
		if !ok {
			return fmt.Errorf("phase3 supporting profile %s changed", profile.Name)
		}
		if profile.Expected != want.count {
			return fmt.Errorf("phase3 supporting profile %s changed", profile.Name)
		}
		if err := validatePhase3ProfileEvidence(profile); err != nil {
			return err
		}
		if schema >= 2 && phase3TestIdentityDigest(profile.Tests) != want.digest {
			return fmt.Errorf("phase3 supporting profile %s lacks exact test identities", profile.Name)
		}
		delete(wantSupporting, profile.Name)
	}
	if len(wantSupporting) != 0 {
		return fmt.Errorf("phase3 supporting profile missing")
	}
	return nil
}

func phase3TestIdentityDigest(tests []string) string {
	digest := sha256.Sum256([]byte(strings.Join(tests, "\n")))
	return hex.EncodeToString(digest[:])
}

func validatePhase3Campaigns(c phase3Census) error {
	wantCampaigns := []string{"quality", "provider_bounded", "full_campaign", "mission_full"}
	if len(c.Campaigns) != len(wantCampaigns) {
		return fmt.Errorf("phase3 campaign evidence census changed")
	}
	for index, name := range wantCampaigns {
		campaign := c.Campaigns[index]
		if campaign.Name != name || campaign.Verdict != "pass" || campaign.DurationMS <= 0 || !phase3DigestPattern.MatchString(campaign.ArtifactSHA256) {
			return fmt.Errorf("phase3 campaign %s is not a measured pass", name)
		}
	}
	for name, run := range map[string]phase3ProviderRun{"phase2_frozen_runner": c.RealProvider.Phase2FrozenRunner, "phase3_two_item_selector": c.RealProvider.Phase3TwoItem} {
		if run.Expected <= 0 || run.Expected != run.Started || run.Started != run.Terminal || run.Terminal != run.Passed || run.Failed != 0 || !phase3DigestPattern.MatchString(run.ArtifactSHA256) {
			return fmt.Errorf("phase3 %s evidence is not an exact pass", name)
		}
	}
	if c.RealProvider.Phase2FrozenRunner.Expected != 11 || c.RealProvider.Phase3TwoItem.Expected != 1 {
		return fmt.Errorf("phase3 real-provider census changed")
	}
	return nil
}

func validatePhase3ProfileEvidence(profile phase3ProfileEvidence) error {
	if profile.Name == "" || profile.Expected <= 0 || profile.Expected != profile.Started || profile.Started != profile.Terminal || profile.Terminal != profile.Passed || profile.Failed != 0 || profile.Skipped != 0 || profile.DurationMS <= 0 || (profile.ArtifactSHA256 != "" && !phase3DigestPattern.MatchString(profile.ArtifactSHA256)) {
		return fmt.Errorf("phase3 profile %s is not an exact pass", profile.Name)
	}
	if len(profile.Tests) > 0 && (len(profile.Tests) != profile.Expected || !slices.Equal(profile.Tests, sortedCopy(profile.Tests))) {
		return fmt.Errorf("phase3 profile %s tests are incomplete or noncanonical", profile.Name)
	}
	return nil
}

func validatePhase3History(attempts []phase3FailedAttempt, evidenceCount int, evidenceDigest string, censusCount int, censusDigest string) error {
	requiredPrefix := []string{
		"01", "02", "03", "04", "05", "06", "07", "08", "09",
		"real-11", "real-12", "real-13", "real-14", "real-15", "real-16", "real-17",
		"review-cycle-1", "20", "21", "real-23", "review-cycle-2", "25",
		"real-27", "real-28", "real-29", "real-30", "review-cycle-3",
	}
	if len(attempts) < len(requiredPrefix) {
		return fmt.Errorf("phase3 failed-attempt history omitted required prefix")
	}
	seen := make(map[string]bool, len(attempts))
	allowed := map[string]bool{"fail": true, "non_admissible": true, "superseded_pass": true, "diagnostic_fail": true, "diagnostic_pass": true}
	for index, attempt := range attempts {
		if attempt.ID == "" || seen[attempt.ID] || !allowed[attempt.Verdict] || attempt.Source == "" || strings.TrimSpace(attempt.Reason) == "" || len(attempt.Reason) > 256 {
			return fmt.Errorf("phase3 failed-attempt ledger is invalid")
		}
		if index < len(requiredPrefix) && attempt.ID != requiredPrefix[index] {
			return fmt.Errorf("phase3 failed-attempt history prefix changed at %s", requiredPrefix[index])
		}
		seen[attempt.ID] = true
	}
	digest, err := phase3FailureHistoryDigest(attempts)
	if err != nil {
		return err
	}
	if evidenceCount != len(attempts) || censusCount != len(attempts) || evidenceDigest != digest || censusDigest != digest {
		return fmt.Errorf("phase3 failed-attempt history count or commitment changed")
	}
	return nil
}

func phase3FailureHistoryDigest(attempts []phase3FailedAttempt) (string, error) {
	canonical, err := json.Marshal(attempts)
	if err != nil {
		return "", fmt.Errorf("canonicalize phase3 failed-attempt history: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("spec-kitty/phase3/failure-history/v1\x00"))
	_, _ = hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validatePhase3QualityGates(gates []struct {
	Name              string `json:"name"`
	Verdict           string `json:"verdict"`
	DurationMS        *int64 `json:"duration_ms"`
	ReachableFindings *int   `json:"reachable_findings,omitempty"`
}) error {
	want := []string{"compile_all", "race", "go_vet", "staticcheck", "go_mod_verify", "govulncheck", "format", "build", "architecture", "scope"}
	if len(gates) != len(want) {
		return fmt.Errorf("phase3 quality gate census changed")
	}
	for index, name := range want {
		if gates[index].Name != name || gates[index].Verdict != "pass" || (gates[index].DurationMS != nil && *gates[index].DurationMS <= 0) {
			return fmt.Errorf("phase3 quality gate %s is not a pass", name)
		}
		if name == "govulncheck" && (gates[index].ReachableFindings == nil || *gates[index].ReachableFindings != 0) {
			return fmt.Errorf("phase3 vulnerability evidence changed")
		}
	}
	return nil
}

func validatePhase3Checksums(evidencePath string) error {
	manifest, err := os.Open(filepath.Join(evidencePath, "SHA256SUMS"))
	if err != nil {
		return err
	}
	defer manifest.Close()
	want := map[string]bool{"qualification-evidence.json": true, "campaign-census.json": true, "results.md": true, "limitations.md": true}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(io.LimitReader(manifest, 64<<10))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !phase3DigestPattern.MatchString(fields[0]) || !want[fields[1]] || seen[fields[1]] {
			return fmt.Errorf("phase3 checksum manifest is not exact")
		}
		contents, readErr := os.ReadFile(filepath.Join(evidencePath, fields[1]))
		if readErr != nil {
			return readErr
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != fields[0] {
			return fmt.Errorf("phase3 checksum mismatch for %s", fields[1])
		}
		seen[fields[1]] = true
	}
	if err = scanner.Err(); err != nil || len(seen) != len(want) {
		return fmt.Errorf("phase3 checksum census incomplete")
	}
	return nil
}

func validatePhase3Documents(root, evidencePath string, census phase3Census) error {
	paths := []string{filepath.Join(root, "README.md"), filepath.Join(evidencePath, "README.md"), filepath.Join(evidencePath, "results.md"), filepath.Join(evidencePath, "limitations.md")}
	contents := make(map[string]string, len(paths))
	for _, path := range paths {
		value, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents[path] = string(value)
		if err = validatePhase3Links(path, string(value)); err != nil {
			return err
		}
		if err = validatePhase3Mermaid(path, string(value)); err != nil {
			return err
		}
	}
	limitations := contents[filepath.Join(evidencePath, "limitations.md")]
	for _, required := range []string{"## Explicit non-claims", "Phase 4", "Phase 5", "Phase 6", "SQLite", "Gapdb", "hosted", "Qualification is non-transitive"} {
		if !strings.Contains(limitations, required) {
			return fmt.Errorf("phase3 limitations omitted %q", required)
		}
	}
	results := contents[filepath.Join(evidencePath, "results.md")]
	knownTests := map[string]bool{}
	for _, profile := range append(append([]phase3ProfileEvidence(nil), census.Profiles...), census.Supporting...) {
		for _, test := range profile.Tests {
			knownTests[test] = true
		}
	}
	for _, match := range phase3TestPattern.FindAllStringSubmatch(results, -1) {
		if !knownTests[match[1]] {
			return fmt.Errorf("phase3 claim names uncatalogued test %s", match[1])
		}
	}
	for _, required := range []string{"TestMissionMethodSemanticDriftMutations1000", "TestMissionMethodFinalizationProcessRaces100", "TestMissionMethodCrashBoundaryCensus", "TestMissionMethodProjectionAndGovernanceIndependence100", "TestMissionMethodNormalizationAndPureQueries100", "TestMissionMethodRealBeadsTwoItemQualification"} {
		if !strings.Contains(results, "`"+required+"`") {
			return fmt.Errorf("phase3 result claim omitted proof %s", required)
		}
	}
	makefileBytes, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		return err
	}
	targets := phase3MakeTargets(string(makefileBytes))
	for _, value := range contents {
		if err = validatePhase3DocumentedCommands(root, value, targets); err != nil {
			return err
		}
	}
	return nil
}

func validatePhase3Links(path, contents string) error {
	for _, match := range phase3LinkPattern.FindAllStringSubmatch(contents, -1) {
		target := strings.SplitN(match[1], "#", 2)[0]
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		if _, err := os.Stat(filepath.Clean(filepath.Join(filepath.Dir(path), target))); err != nil {
			return fmt.Errorf("phase3 broken link %s in %s", match[1], filepath.Base(path))
		}
	}
	return nil
}

func validatePhase3Mermaid(path, contents string) error {
	lines := strings.Split(contents, "\n")
	inBlock := false
	found := false
	declaration := ""
	body := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock && trimmed == "```mermaid" {
			inBlock, found, declaration = true, true, ""
			continue
		}
		if inBlock && strings.HasPrefix(trimmed, "```") {
			if declaration == "" || !phase3MermaidDeclaration(declaration) || validatePhase3MermaidBody(declaration, body) != nil {
				return fmt.Errorf("phase3 malformed Mermaid in %s", filepath.Base(path))
			}
			inBlock, body = false, nil
			continue
		}
		if inBlock && declaration == "" && trimmed != "" {
			declaration = trimmed
			continue
		}
		if inBlock && trimmed != "" {
			body = append(body, trimmed)
		}
	}
	if inBlock || (strings.Contains(contents, "mermaid") && !found) {
		return fmt.Errorf("phase3 malformed Mermaid fence in %s", filepath.Base(path))
	}
	return nil
}

func validatePhase3MermaidBody(declaration string, lines []string) error {
	if len(lines) == 0 {
		return fmt.Errorf("empty Mermaid body")
	}
	if strings.HasPrefix(declaration, "flowchart ") {
		node := `[A-Za-z][A-Za-z0-9_]*(?:\[[^\n]*\])?`
		arrow := regexp.MustCompile(`^` + node + `\s+-->\s*(?:\|[^|]+\|\s*)?` + node + `$`)
		nodeOnly := regexp.MustCompile(`^` + node + `$`)
		subgraph := regexp.MustCompile(`^subgraph\s+` + node + `$`)
		depth := 0
		for _, line := range lines {
			if subgraph.MatchString(line) {
				depth++
				continue
			}
			if line == "end" {
				if depth == 0 {
					return fmt.Errorf("unmatched flowchart end")
				}
				depth--
				continue
			}
			if nodeOnly.MatchString(line) || arrow.MatchString(line) {
				continue
			}
			return fmt.Errorf("invalid flowchart statement")
		}
		if depth != 0 {
			return fmt.Errorf("unterminated flowchart subgraph")
		}
		return nil
	}
	if declaration == "sequenceDiagram" {
		participant := regexp.MustCompile(`^participant\s+[A-Za-z][A-Za-z0-9_]*\s+as\s+[^\n]+$`)
		note := regexp.MustCompile(`^Note\s+(?:over|right of|left of)\s+[A-Za-z][A-Za-z0-9_]*(?:,[A-Za-z][A-Za-z0-9_]*)?:\s*\S.*$`)
		arrow := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(?:->>|-->>|->|-->)[A-Za-z][A-Za-z0-9_]*:\s*\S.*$`)
		for _, line := range lines {
			if participant.MatchString(line) || note.MatchString(line) || arrow.MatchString(line) {
				continue
			}
			return fmt.Errorf("invalid sequence statement")
		}
		return nil
	}
	return fmt.Errorf("unsupported Mermaid body")
}

func validatePhase3DocumentedCommands(root, contents string, targets map[string]bool) error {
	blocks, err := phase3ExecutableBlocks(contents)
	if err != nil {
		return err
	}
	allowed := map[string]bool{
		"cd": true, "git": true, "go": true, "govulncheck": true, "jq": true,
		"make": true, "mktemp": true, "pwd": true, "sha256sum": true, "staticcheck": true,
	}
	for _, block := range blocks {
		for _, command := range phase3ShellCommands(block) {
			name := command[0]
			if name == "$beads_bin" || name == `"$beads_bin"` {
				if err := validatePhase3CommandShape(root, command); err != nil {
					return err
				}
				continue
			}
			if strings.HasPrefix(name, "scripts/") {
				if filepath.Clean(name) != name || filepath.IsAbs(name) {
					return fmt.Errorf("phase3 documentation names unsafe command %s", name)
				}
				info, statErr := os.Stat(filepath.Join(root, name))
				if statErr != nil || !info.Mode().IsRegular() {
					return fmt.Errorf("phase3 documentation names nonexistent command %s", name)
				}
				if err := validatePhase3ScriptShape(command); err != nil {
					return err
				}
				continue
			}
			if !allowed[name] {
				return fmt.Errorf("phase3 documentation names unapproved command %s", name)
			}
			if err := validatePhase3CommandShape(root, command); err != nil {
				return err
			}
			if name == "make" {
				if len(command) < 2 || !targets[command[1]] {
					return fmt.Errorf("phase3 documentation names nonexistent make target in %q", strings.Join(command, " "))
				}
			}
			if name == "go" && len(command) >= 3 && (command[1] == "run" || command[1] == "install") && strings.HasPrefix(command[2], "./") {
				if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(command[2], "./"))); err != nil {
					return fmt.Errorf("phase3 documentation names nonexistent Go command %s", command[2])
				}
			}
		}
	}
	return nil
}

func validatePhase3CommandShape(root string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("phase3 documentation contains empty command")
	}
	core, err := phase3CommandCore(command)
	if err != nil {
		return err
	}
	unsupported := func() error {
		return fmt.Errorf("phase3 documentation names unsupported command shape %q", strings.Join(command, " "))
	}
	switch core[0] {
	case "cd":
		if len(core) != 2 || !phase3DocumentedDirectory(root, core[1]) {
			return unsupported()
		}
		return nil
	case "git":
		if len(core) >= 2 && core[1] == "-C" {
			if len(core) != 4 || !phase3DocumentedDirectory(root, core[2]) || core[3] != "init" {
				return unsupported()
			}
			return nil
		}
		if len(core) == 3 && core[1] == "diff" && core[2] == "--check" {
			return nil
		}
		if len(core) == 4 && core[1] == "show" && core[2] == "--stat" && phase3CommitPattern.MatchString(phase3ShellValue(core[3])) {
			return nil
		}
		if len(core) == 3 && core[1] == "checkout" && phase3CommitPattern.MatchString(phase3ShellValue(core[2])) {
			return nil
		}
		return unsupported()
	case "go":
		if len(core) == 3 && core[1] == "mod" && slices.Contains([]string{"download", "verify"}, core[2]) {
			return nil
		}
		if len(core) == 6 && core[1] == "build" && core[2] == "-trimpath" && core[3] == "-o" {
			allowed := map[string]string{
				"bin/spec-kitty":           "./cmd/spec-kitty",
				"bin/spk-m2-qualification": "./cmd/spk-m2-qualification",
			}
			if allowed[phase3ShellValue(core[4])] == phase3ShellValue(core[5]) && phase3ExistingGoPath(root, core[5]) {
				return nil
			}
		}
		if len(core) == 4 && core[1] == "install" && core[2] == "./cmd/spec-kitty" && core[3] == "./cmd/spk-m2-qualification" && phase3ExistingGoPath(root, core[2]) && phase3ExistingGoPath(root, core[3]) {
			return nil
		}
		if len(core) == 3 && core[1] == "install" && core[2] == "./cmd/spk-phase1-helper" && phase3ExistingGoPath(root, core[2]) {
			return nil
		}
		if len(core) >= 3 && core[1] == "run" && core[2] == "./cmd/spk-phase1-helper" && phase3ExistingGoPath(root, core[2]) {
			if validatePhase3HelperArgs(core[3:]) == nil {
				return nil
			}
		}
		if len(core) == 3 && core[1] == "vet" && core[2] == "./..." {
			return nil
		}
		if len(core) >= 3 && core[1] == "test" && validatePhase3GoTestArgs(core[2:]) {
			return nil
		}
		return unsupported()
	case "jq":
		if len(core) == 3 && core[1] == "." && phase3SafeShellPath(core[2]) {
			return nil
		}
		if len(core) == 4 && core[1] == "-r" && core[2] == ".executable_path" && phase3SafeShellPath(core[3]) {
			return nil
		}
		return unsupported()
	case "sha256sum":
		if len(core) == 3 && core[1] == "--" && phase3SafeShellPath(core[2]) {
			return nil
		}
		if len(core) == 3 && core[1] == "--check" && filepath.Base(phase3ShellValue(core[2])) == "SHA256SUMS" {
			return nil
		}
		return unsupported()
	case "staticcheck", "govulncheck":
		if len(core) == 2 && core[1] == "./..." {
			return nil
		}
		return unsupported()
	case "mktemp":
		if len(core) == 2 && strings.HasPrefix(phase3ShellValue(core[1]), "/tmp/") {
			return nil
		}
		if len(core) == 3 && core[1] == "-d" && strings.HasPrefix(phase3ShellValue(core[2]), "/tmp/") {
			return nil
		}
		return unsupported()
	case "make":
		if len(core) < 2 {
			return unsupported()
		}
		for _, argument := range core[2:] {
			if phase3MakeAssignment(argument) {
				continue
			}
			if !regexp.MustCompile(`^[A-Z][A-Z0-9_]*=\S+$`).MatchString(argument) {
				return unsupported()
			}
		}
		return nil
	case "pwd":
		if len(core) == 1 {
			return nil
		}
		return unsupported()
	default:
		if core[0] == "$beads_bin" || core[0] == `"$beads_bin"` {
			return validatePhase3ProviderCommand(core)
		}
		return unsupported()
	}
}

func phase3CommandCore(command []string) ([]string, error) {
	for index, token := range command {
		if token != ">" {
			continue
		}
		if index == 0 || index+2 != len(command) || !phase3SafeShellPath(command[index+1]) {
			return nil, fmt.Errorf("phase3 documentation names unsafe redirection in %q", strings.Join(command, " "))
		}
		return command[:index], nil
	}
	return command, nil
}

func phase3ShellValue(value string) string {
	return strings.Trim(value, `"'`)
}

func phase3SafeShellPath(value string) bool {
	value = phase3ShellValue(value)
	if phase3ShellVariable(value) {
		return true
	}
	return value != "" && !filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.HasPrefix(value, "..") && !strings.ContainsAny(value, "\x00\n")
}

func phase3DocumentedDirectory(root, value string) bool {
	value = phase3ShellValue(value)
	if phase3ShellVariable(value) {
		return true
	}
	if regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`).MatchString(value) {
		return true
	}
	if value == "" || filepath.IsAbs(value) || filepath.Clean(value) != value || strings.HasPrefix(value, "..") {
		return false
	}
	info, err := os.Stat(filepath.Join(root, value))
	return err == nil && info.IsDir()
}

func phase3ShellVariable(value string) bool {
	if len(value) < 2 || value[0] != '$' || !((value[1] >= 'A' && value[1] <= 'Z') || (value[1] >= 'a' && value[1] <= 'z') || value[1] == '_') {
		return false
	}
	for _, character := range value[2:] {
		if !((character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func phase3MakeAssignment(value string) bool {
	name, assigned, ok := strings.Cut(value, "=")
	if !ok || assigned == "" || name == "" || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for _, character := range name[1:] {
		if !((character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func phase3ExistingGoPath(root, value string) bool {
	value = phase3ShellValue(value)
	if !strings.HasPrefix(value, "./") || filepath.Clean(value) != strings.TrimPrefix(value, "./") {
		return false
	}
	_, err := os.Stat(filepath.Join(root, strings.TrimPrefix(value, "./")))
	return err == nil
}

func validatePhase3GoTestArgs(arguments []string) bool {
	foundPackage := false
	for index := 0; index < len(arguments); index++ {
		argument := phase3ShellValue(arguments[index])
		switch argument {
		case "-race":
		case "-run", "-count":
			index++
			if index >= len(arguments) || phase3ShellValue(arguments[index]) == "" {
				return false
			}
		default:
			if strings.HasPrefix(argument, "-count=") && regexp.MustCompile(`^[0-9]+$`).MatchString(strings.TrimPrefix(argument, "-count=")) {
				continue
			}
			if !strings.HasPrefix(argument, "./") {
				return false
			}
			foundPackage = true
		}
	}
	return foundPackage
}

func validatePhase3HelperArgs(arguments []string) error {
	if len(arguments) < 2 || arguments[0] != "tracker" || arguments[1] != "inspect-beads" {
		return fmt.Errorf("unsupported helper command")
	}
	allowed := map[string]bool{"--project": true, "--executable": true, "--workspace": true, "--database-identity": true, "--protocol-schema": true, "--mode": true, "--expected-digest": true}
	for index := 2; index < len(arguments); index++ {
		if arguments[index] == "--read-only=true" {
			continue
		}
		if !allowed[arguments[index]] || index+1 >= len(arguments) {
			return fmt.Errorf("unsupported helper argument")
		}
		index++
	}
	return nil
}

func validatePhase3ProviderCommand(command []string) error {
	unsupported := func() error {
		return fmt.Errorf("phase3 documentation names unsupported provider command shape %q", strings.Join(command, " "))
	}
	if len(command) == 3 && slices.Contains([]string{"version", "where", "export"}, command[1]) && command[2] == "--json" {
		return nil
	}
	if len(command) == 4 && command[1] == "dolt" && slices.Contains([]string{"show", "status"}, command[2]) && command[3] == "--json" {
		return nil
	}
	if len(command) >= 2 && command[1] == "init" {
		allowed := map[string]bool{"--stealth": true, "--skip-agents": true, "--skip-hooks": true, "--non-interactive": true}
		for index := 2; index < len(command); index++ {
			if allowed[command[index]] {
				continue
			}
			if command[index] == "--prefix" && index+1 < len(command) {
				index++
				continue
			}
			return unsupported()
		}
		return nil
	}
	if len(command) >= 3 && command[1] == "backup" {
		if len(command) == 4 && command[2] == "init" && phase3SafeShellPath(command[3]) {
			return nil
		}
		if len(command) == 3 && slices.Contains([]string{"sync", "status"}, command[2]) {
			return nil
		}
	}
	return unsupported()
}

func validatePhase3ScriptShape(command []string) error {
	core, err := phase3CommandCore(command)
	if err != nil {
		return err
	}
	if len(core) == 3 && core[0] == "scripts/install-qualified-beads.sh" && core[1] == "--dest" && phase3SafeShellPath(core[2]) {
		return nil
	}
	return fmt.Errorf("phase3 documentation names unsupported script command shape %q", strings.Join(command, " "))
}

func phase3ExecutableBlocks(contents string) ([]string, error) {
	lines := strings.Split(contents, "\n")
	inBlock := false
	var current []string
	var blocks []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inBlock && (trimmed == "```bash" || trimmed == "```sh" || trimmed == "```shell") {
			inBlock, current = true, nil
			continue
		}
		if inBlock && strings.HasPrefix(trimmed, "```") {
			blocks = append(blocks, strings.Join(current, "\n"))
			inBlock = false
			continue
		}
		if inBlock {
			current = append(current, line)
		}
	}
	if inBlock {
		return nil, fmt.Errorf("phase3 documentation contains unterminated executable block")
	}
	return blocks, nil
}

func phase3ShellCommands(block string) [][]string {
	block = strings.ReplaceAll(block, "\\\n", " ")
	var result [][]string
	substitutions := regexp.MustCompile(`\$\(([^)]+)\)`)
	statements := strings.ReplaceAll(strings.ReplaceAll(block, "&&", "\n"), ";", "\n")
	for _, line := range strings.Split(statements, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "(")
		line = strings.TrimSuffix(line, ")")
		if line == "" || line == "(" || line == ")" {
			continue
		}
		for _, substitution := range substitutions.FindAllStringSubmatch(line, -1) {
			if fields := strings.Fields(substitution[1]); len(fields) > 0 {
				result = append(result, fields)
			}
		}
		line = substitutions.ReplaceAllString(line, "VALUE")
		fields := strings.Fields(line)
		for len(fields) > 0 && strings.Contains(fields[0], "=") {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		result = append(result, fields)
	}
	return result
}

func phase3MermaidDeclaration(value string) bool {
	if value == "sequenceDiagram" || value == "stateDiagram-v2" {
		return true
	}
	fields := strings.Fields(value)
	return len(fields) == 2 && fields[0] == "flowchart" && slices.Contains([]string{"TD", "TB", "BT", "RL", "LR"}, fields[1])
}

func phase3MakeTargets(makefile string) map[string]bool {
	targets := map[string]bool{}
	for _, line := range strings.Split(makefile, "\n") {
		if strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") || !strings.Contains(line, ":") {
			continue
		}
		left := strings.SplitN(line, ":", 2)[0]
		for _, name := range strings.Fields(left) {
			if name != "" && !strings.ContainsAny(name, "$%") {
				targets[name] = true
			}
		}
	}
	return targets
}

// ValidatePhase3PublicationCheckout proves the payload/attestation layering in
// a clean checkout. It is separate from content validation so a frozen source
// candidate can legitimately qualify the prior publication before generating
// its own later payload.
func ValidatePhase3PublicationCheckout(root, payloadCommit, attestationCommit string) error {
	if !phase3CommitPattern.MatchString(payloadCommit) || !phase3CommitPattern.MatchString(attestationCommit) {
		return fmt.Errorf("phase3 checkout commit identity malformed")
	}
	git := func(args ...string) (string, error) {
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := command.Output()
		return strings.TrimSpace(string(output)), err
	}
	status, err := git("status", "--porcelain")
	if err != nil || status != "" {
		return fmt.Errorf("phase3 publication checkout is not clean")
	}
	if _, err = git("merge-base", "--is-ancestor", payloadCommit, attestationCommit); err != nil {
		return fmt.Errorf("phase3 attestation does not descend from payload")
	}
	head, err := git("rev-parse", "HEAD")
	if err != nil || head != attestationCommit {
		return fmt.Errorf("phase3 checkout HEAD is not the asserted attestation")
	}
	headTree, err := git("rev-parse", "HEAD^{tree}")
	if err != nil || !phase3CommitPattern.MatchString(headTree) {
		return fmt.Errorf("phase3 checkout tree identity unavailable")
	}
	attestationTree, err := git("rev-parse", attestationCommit+"^{tree}")
	if err != nil || attestationTree != headTree {
		return fmt.Errorf("phase3 checkout tree does not match asserted attestation")
	}
	var evidence phase3Qualification
	if err = phase3DecodeClosed(filepath.Join(root, "docs", "phase3-evidence", "qualification-evidence.json"), &evidence); err != nil {
		return err
	}
	if payloadCommit == attestationCommit || payloadCommit == evidence.TestedSource.Commit || attestationCommit == evidence.TestedSource.Commit {
		return fmt.Errorf("phase3 source, payload, and attestation identities are not distinct")
	}
	if _, err = git("merge-base", "--is-ancestor", evidence.TestedSource.Commit, payloadCommit); err != nil {
		return fmt.Errorf("phase3 payload does not descend from tested source")
	}
	changed, err := git("diff", "--name-only", payloadCommit, attestationCommit)
	if err != nil {
		return err
	}
	for _, path := range strings.Fields(changed) {
		if path != "README.md" && path != "docs/phase3-evidence/README.md" {
			return fmt.Errorf("phase3 attestation changed non-documentation path %s", path)
		}
	}
	return ValidatePhase3Publication(root)
}
