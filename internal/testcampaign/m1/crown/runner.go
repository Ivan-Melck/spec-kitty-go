package crown

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	basereview "github.com/spec-kitty/go-kitty3/internal/review"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/external"
	faultcampaign "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/faults"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/focused"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/mutants"
)

const DefaultRepetitions = 100
const admittedProductPolicySHA256 = "dce7f209a705ea77420ee249a571c182d7d5969eb5e96e0414356baf63ae4c1b"

type Config struct {
	RunID               string
	Seed                uint64
	RepositoryRoot      string
	GapDBDPath          string
	AdmissionRecordPath string
	ProductPolicyPath   string
	RuntimeRoot         string
	DataRoot            string
	EvidenceRoot        string
	ProvingBinaryPath   string
	Repetitions         int
	StartTimeout        time.Duration
	StopTimeout         time.Duration
}

type Result struct {
	RunID        string              `json:"run_id"`
	Outcome      string              `json:"outcome"`
	Publication  m1.Publication      `json:"publication"`
	Verification m1.Verification     `json:"verification"`
	Manifest     m1.EvidenceManifest `json:"manifest"`
}

type runEvidenceState struct {
	Source            m1.SourceBinding
	InitialProvenance sidecargapdb.SidecarProvenance
	External          external.Result
	Ordered           faultcampaign.CrownExecution
	Focused           []focused.Summary
	Mutants           []mutants.Result
}

type PreflightResult struct {
	RunID                 string `json:"run_id"`
	RepositoryRoot        string `json:"repository_root"`
	GapDBDSHA256          string `json:"gapdbd_sha256"`
	AdmissionRecordSHA256 string `json:"admission_record_sha256"`
	ProductPolicySHA256   string `json:"product_policy_sha256"`
	ProvingBinarySHA256   string `json:"proving_binary_sha256"`
	Ready                 bool   `json:"ready"`
}

type Progress struct {
	SchemaVersion  int       `json:"schema_version"`
	RunID          string    `json:"run_id"`
	Phase          string    `json:"phase"`
	Completed      int       `json:"completed_segments"`
	Total          int       `json:"total_segments"`
	Outcome        string    `json:"outcome"`
	StartedAt      time.Time `json:"started_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	LastDiagnostic string    `json:"last_diagnostic,omitempty"`
}

func Preflight(config Config) (PreflightResult, error) {
	result := PreflightResult{RunID: config.RunID, RepositoryRoot: config.RepositoryRoot}
	if config.Repetitions == 0 {
		config.Repetitions = DefaultRepetitions
	}
	if config.RunID == "" || config.Seed == 0 || config.Repetitions < focused.MinimumRepetitions {
		return result, fmt.Errorf("RunID, nonzero seed, and at least %d repetitions are required", focused.MinimumRepetitions)
	}
	for name, path := range map[string]string{"repository": config.RepositoryRoot, "gapdbd": config.GapDBDPath, "admission": config.AdmissionRecordPath, "policy": config.ProductPolicyPath, "runtime": config.RuntimeRoot, "data": config.DataRoot, "evidence": config.EvidenceRoot, "proving binary": config.ProvingBinaryPath} {
		if !filepath.IsAbs(path) {
			return result, fmt.Errorf("%s path must be absolute", name)
		}
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return result, fmt.Errorf("M1 proving ground requires linux/amd64")
	}
	resolvedRoots, err := validateExclusiveRoots(config.RuntimeRoot, config.DataRoot, config.EvidenceRoot)
	if err != nil {
		return result, err
	}
	_ = resolvedRoots
	for _, reserved := range []string{filepath.Join(config.EvidenceRoot, config.RunID), filepath.Join(config.EvidenceRoot, ".run-"+config.RunID+".lock"), progressPath(config.EvidenceRoot, config.RunID)} {
		if _, statErr := os.Lstat(reserved); statErr == nil {
			return result, fmt.Errorf("RunID output already exists: %s", reserved)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return result, statErr
		}
	}
	head, err := cleanRepositoryHead(config.RepositoryRoot)
	if err != nil {
		return result, err
	}
	if result.GapDBDSHA256, err = fileSHA(config.GapDBDPath); err != nil {
		return result, err
	}
	if result.GapDBDSHA256 != m1.AdmittedSidecarSHA256 {
		return result, fmt.Errorf("gapdbd SHA-256 is not the admitted M1 image")
	}
	if result.AdmissionRecordSHA256, err = fileSHA(config.AdmissionRecordPath); err != nil {
		return result, err
	}
	if result.AdmissionRecordSHA256 != m1.AdmittedTupleSHA256 {
		return result, fmt.Errorf("admission record SHA-256 is not the admitted tuple")
	}
	if result.ProductPolicySHA256, err = fileSHA(config.ProductPolicyPath); err != nil {
		return result, err
	}
	if err := validateProductPolicy(config.RepositoryRoot, config.ProductPolicyPath, result.ProductPolicySHA256); err != nil {
		return result, err
	}
	if result.ProvingBinarySHA256, err = fileSHA(config.ProvingBinaryPath); err != nil {
		return result, err
	}
	if err := validateProvingBinary(config.ProvingBinaryPath, head); err != nil {
		return result, err
	}
	if stat, statErr := os.Stat(config.RepositoryRoot); statErr != nil || !stat.IsDir() {
		return result, fmt.Errorf("repository root is unavailable: %w", statErr)
	}
	result.Ready = true
	return result, nil
}

func validateProductPolicy(repositoryRoot, policyPath, digest string) error {
	want := filepath.Join(repositoryRoot, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts", "m1-product-budgets-v1.json")
	actualResolved, err := filepath.EvalSymlinks(policyPath)
	if err != nil {
		return err
	}
	wantResolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		return err
	}
	if actualResolved != wantResolved || digest != admittedProductPolicySHA256 {
		return fmt.Errorf("product policy is not the frozen source-bound M1 budget contract")
	}
	return nil
}

func validateProvingBinary(path, sourceHead string) error {
	actual, err := os.Executable()
	if err != nil {
		return err
	}
	actualInfo, err := os.Stat(actual)
	if err != nil {
		return err
	}
	providedInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(actualInfo, providedInfo) {
		return fmt.Errorf("proving binary path does not identify the executing image")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return fmt.Errorf("executing proving binary lacks Go build information")
	}
	revision, modified := "", ""
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			revision = setting.Value
		}
		if setting.Key == "vcs.modified" {
			modified = setting.Value
		}
	}
	if revision != sourceHead || modified != "false" {
		return fmt.Errorf("proving binary VCS binding differs from clean source HEAD")
	}
	return nil
}

func cleanRepositoryHead(root string) (string, error) {
	if stat, err := os.Stat(root); err != nil || !stat.IsDir() {
		return "", fmt.Errorf("repository root is unavailable: %w", err)
	}
	head, err := gitOutput(root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	status, err := gitOutput(root, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	ignored, err := gitOutput(root, "ls-files", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return "", err
	}
	if status != "" || ignored != "" {
		return "", fmt.Errorf("repository contains tracked, untracked, or ignored bytes outside clean HEAD")
	}
	tracked, err := gitOutput(root, "ls-files", "-v")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(tracked, "\n") {
		if line != "" && (len(line) < 3 || line[0] != 'H' || line[1] != ' ') {
			return "", fmt.Errorf("repository contains assume-unchanged, skip-worktree, or unexpected tracked index state")
		}
	}
	return head, nil
}

func gitOutput(root string, arguments ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func validateExclusiveRoots(paths ...string) ([]string, error) {
	resolved := make([]string, len(paths))
	for i, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("exclusive root %q is unavailable: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("exclusive root %q must be a real directory", path)
		}
		resolved[i], err = filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
	}
	for i := range resolved {
		for j := i + 1; j < len(resolved); j++ {
			if pathsOverlap(resolved[i], resolved[j]) {
				return nil, fmt.Errorf("runtime, data, and evidence roots must not overlap")
			}
		}
	}
	return resolved, nil
}

func pathsOverlap(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	rel, err := filepath.Rel(a, b)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	rel, err = filepath.Rel(b, a)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func Run(ctx context.Context, config Config) (result Result, runErr error) {
	result = Result{RunID: config.RunID, Outcome: "incomplete"}
	if config.Repetitions == 0 {
		config.Repetitions = DefaultRepetitions
	}
	if config.StartTimeout == 0 {
		config.StartTimeout = 10 * time.Second
	}
	if config.StopTimeout == 0 {
		config.StopTimeout = 10 * time.Second
	}
	preflight, err := Preflight(config)
	if err != nil {
		return result, err
	}
	for _, directory := range []string{config.RuntimeRoot, config.DataRoot, config.EvidenceRoot} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return result, err
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return result, err
		}
	}
	lockPath := filepath.Join(config.EvidenceRoot, ".run-"+config.RunID+".lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return result, fmt.Errorf("acquire exclusive RunID execution: %w", err)
	}
	_ = lock.Close()
	defer os.Remove(lockPath)
	startedAt := time.Now().UTC()
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "starting", 0, "running", startedAt, "")); err != nil {
		return result, err
	}
	lifecycle, err := sidecargapdb.Start(ctx, sidecargapdb.LifecycleConfig{ExecutablePath: config.GapDBDPath, AdmissionRecordPath: config.AdmissionRecordPath, RuntimeRoot: config.RuntimeRoot, DataDirectory: filepath.Join(config.DataRoot, "database"), SupervisorRunID: config.RunID, ProductPolicySHA256: preflight.ProductPolicySHA256, StartTimeout: config.StartTimeout, StopTimeout: config.StopTimeout})
	if err != nil {
		return result, err
	}
	executor := &faultcampaign.ProductionExecutor{Root: config.RepositoryRoot, Lifecycle: lifecycle, RunID: config.RunID}
	state := &runEvidenceState{}
	defer func() {
		_ = executor.Close()
		if executor.Lifecycle != nil {
			_ = executor.Lifecycle.Stop(context.Background())
		}
	}()
	defer func() {
		if runErr == nil || result.Publication.Directory != "" {
			return
		}
		publication, manifest, publishErr := publishNonPass(ctx, config, preflight, executor.Lifecycle, state, runErr)
		if publishErr != nil {
			runErr = fmt.Errorf("%v; publish incomplete evidence: %w", runErr, publishErr)
			return
		}
		result.Publication, result.Manifest = publication, manifest
		verification, _, verifyErr := m1.VerifyEvidence(ctx, publication.Directory, m1.VerifyOptions{ExpectedSource: sourceOption(state.Source)})
		if verifyErr != nil {
			runErr = fmt.Errorf("%v; verify incomplete evidence: %w", runErr, verifyErr)
			return
		}
		result.Verification = verification
		completed := len(state.Ordered.Segments)
		if state.InitialProvenance.SupervisorRunID != "" {
			completed++
		}
		if len(state.External.Records) == len(m1.ScenarioIDs) {
			completed++
		}
		_ = writeProgress(config.EvidenceRoot, newProgress(config.RunID, "terminal", completed, result.Verification.Outcome, startedAt, bounded(runErr.Error())))
	}()

	initialSource, err := m1.ObserveSource(ctx, config.RepositoryRoot, lifecycle)
	if err != nil {
		return result, err
	}
	state.Source = initialSource
	initialProvenance := lifecycle.Provenance()
	state.InitialProvenance = initialProvenance
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "admission", 1, "running", startedAt, "")); err != nil {
		return result, err
	}
	externalResult, err := external.Run(ctx, config.RunID, lifecycle)
	state.External = externalResult
	if externalResult.Lifecycle != nil {
		executor.Lifecycle = externalResult.Lifecycle
	}
	if err != nil {
		return result, err
	}
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "external-14", 2, "running", startedAt, "")); err != nil {
		return result, err
	}
	ordered, err := executor.RunOrderedCrown(ctx, faultcampaign.CrownInput{RunID: config.RunID, Seed: config.Seed})
	state.Ordered = ordered
	if err != nil {
		return result, err
	}
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "ordered-crown", 21, "running", startedAt, "")); err != nil {
		return result, err
	}

	segments := make([]m1.CrownSegment, 0, len(m1.CrownSegmentIDs))
	segmentArtifacts := make([]m1.ArtifactInput, 0, len(m1.CrownSegmentIDs))
	admissionArtifact, _ := m1.CanonicalJSON(initialProvenance)
	segments = append(segments, observedSegment(config.RunID, "admission", uint64(initialProvenance.Observation.Status.CurrentRevision), uint64(initialProvenance.Observation.Status.CurrentRevision), admissionArtifact))
	externalAggregate, _ := m1.CanonicalJSON(externalResult.Records)
	segments = append(segments, observedSegment(config.RunID, "external-14", externalResult.Records[0].BeforeRevision, externalResult.Records[len(externalResult.Records)-1].AfterRevision, externalAggregate))
	segmentArtifacts = append(segmentArtifacts, m1.ArtifactInput{Kind: "crown-segment", Data: admissionArtifact}, m1.ArtifactInput{Kind: "crown-segment", Data: externalAggregate})
	for i := range ordered.Segments {
		segments = append(segments, ordered.Segments[i])
		segmentArtifacts = append(segmentArtifacts, m1.ArtifactInput{Kind: "crown-segment", Data: ordered.Artifacts[i]})
	}

	focusedSummaries := make([]focused.Summary, 0, len(focused.Definitions()))
	focusedInputs := make([]m1.ArtifactInput, 0, len(focused.Definitions()))
	beforeFocused, err := currentRevision(ctx, executor)
	if err != nil {
		return result, err
	}
	for index, definition := range focused.Definitions() {
		summary := (focused.Runner{RunID: config.RunID, Seed: config.Seed + 100 + uint64(index), Repetitions: config.Repetitions, Scenario: &focused.ProductionScenario{Executor: executor}}).Run(ctx, definition.ID)
		focusedSummaries = append(focusedSummaries, summary)
		state.Focused = append([]focused.Summary(nil), focusedSummaries...)
		artifact, marshalErr := m1.CanonicalJSON(summary)
		if marshalErr != nil {
			return result, marshalErr
		}
		focusedInputs = append(focusedInputs, m1.ArtifactInput{Kind: "focused-summary", Data: artifact})
		if !summary.Admissible() {
			return result, fmt.Errorf("focused campaign %s is not admissible: %s", definition.ID, summary.FirstDiagnostic)
		}
		if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "focused/"+definition.ID, 21, "running", startedAt, "")); err != nil {
			return result, err
		}
	}
	afterFocused, err := currentRevision(ctx, executor)
	if err != nil {
		return result, err
	}
	focusedAggregate, _ := m1.CanonicalJSON(focusedSummaries)
	segments = append(segments, observedSegment(config.RunID, "focused-8x100", beforeFocused, afterFocused, focusedAggregate))
	segmentArtifacts = append(segmentArtifacts, m1.ArtifactInput{Kind: "crown-segment", Data: focusedAggregate})
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "focused-8x100", 22, "running", startedAt, "")); err != nil {
		return result, err
	}

	mutantResults := (mutants.Runner{Root: config.RepositoryRoot}).RunAll(ctx, config.RunID)
	state.Mutants = mutantResults
	mutantInputs := make([]m1.ArtifactInput, 0, len(mutantResults))
	for _, mutant := range mutantResults {
		if mutant.Outcome != mutants.OutcomeKilled {
			return result, fmt.Errorf("mutant %s was not killed: %s", mutant.ID, mutant.Reason)
		}
		mutantInputs = append(mutantInputs, m1.ArtifactInput{Kind: "mutant", Data: mutant.Artifact})
	}
	mutantAggregate, _ := m1.CanonicalJSON(mutantResults)
	segments = append(segments, observedSegment(config.RunID, "mutants-19", afterFocused, afterFocused, mutantAggregate))
	segmentArtifacts = append(segmentArtifacts, m1.ArtifactInput{Kind: "crown-segment", Data: mutantAggregate})
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "mutants-19", 23, "running", startedAt, "")); err != nil {
		return result, err
	}
	terminalArtifact, _ := m1.CanonicalJSON(ordered.Invariants)
	segments = append(segments, observedSegment(config.RunID, "terminal-universals", afterFocused, afterFocused, terminalArtifact))
	segmentArtifacts = append(segmentArtifacts, m1.ArtifactInput{Kind: "crown-segment", Data: terminalArtifact})
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "terminal-universals", 24, "running", startedAt, "")); err != nil {
		return result, err
	}
	for i := range segments {
		segments[i].Sequence = i + 1
	}
	crownArtifact, err := m1.SealCrownArtifact(m1.CrownArtifact{SchemaVersion: 1, RunID: config.RunID, Segments: segments, Trial: ordered.Trial})
	if err != nil {
		return result, err
	}
	if err := m1.ValidateCrownArtifact(crownArtifact); err != nil {
		return result, err
	}
	crownBytes, _ := m1.CanonicalJSON(crownArtifact)

	source, err := m1.ObserveSource(ctx, config.RepositoryRoot, executor.Lifecycle)
	if err != nil {
		return result, err
	}
	if source.Commit != initialSource.Commit || source.Tree != initialSource.Tree || source.WorktreeTree != initialSource.WorktreeTree || source.WorktreeStatusSHA256 != initialSource.WorktreeStatusSHA256 || source.DatabaseID != initialSource.DatabaseID {
		return result, fmt.Errorf("source/database binding changed during the crown execution")
	}
	provenanceEvidence, err := executor.Lifecycle.Evidence()
	if err != nil {
		return result, err
	}
	sourceBytes, _ := m1.CanonicalJSON(source)
	provenanceBytes, _ := m1.CanonicalJSON(provenanceEvidence)
	contractsBytes, err := contractBundle(config.RepositoryRoot, config.ProductPolicyPath)
	if err != nil {
		return result, err
	}
	budgetsBytes, err := os.ReadFile(config.ProductPolicyPath)
	if err != nil {
		return result, err
	}
	provingBytes, err := os.ReadFile(config.ProvingBinaryPath)
	if err != nil {
		return result, err
	}

	manifest := m1.EvidenceManifest{SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", RunID: config.RunID, SourceCommit: source.Commit, SourceTree: source.Tree, AdmittedTupleSHA256: m1.AdmittedTupleSHA256, SidecarProvenanceSHA256: source.SidecarProvenanceSHA256, OrderedTrial: crownArtifact.Trial, InvariantSummary: m1.EvidenceInvariants(ordered.Invariants), Outcome: "pass"}
	inputs := []m1.ArtifactInput{{Kind: "source-binding", Data: sourceBytes}, {Kind: "sidecar-provenance", Data: provenanceBytes}, {Kind: "proving-binary", Data: provingBytes}, {Kind: "contracts", Data: contractsBytes}, {Kind: "budgets", Data: budgetsBytes}, {Kind: "crown-ledger", Data: crownBytes}}
	for i, record := range externalResult.Records {
		manifest.ScenarioResults = append(manifest.ScenarioResults, m1.ScenarioResult{ScenarioID: record.ScenarioID, RunID: record.RunID, Seed: config.Seed + uint64(i), Started: record.Started, Terminal: record.Terminal, Outcome: string(record.Outcome), ArtifactSHA256: record.ArtifactSHA256})
		inputs = append(inputs, m1.ArtifactInput{Kind: "scenario", Data: externalResult.Artifacts[i]})
	}
	for _, record := range ordered.Faults {
		artifact, marshalErr := m1.CanonicalJSON(record)
		if marshalErr != nil {
			return result, marshalErr
		}
		manifest.FaultResults = append(manifest.FaultResults, m1.FaultResult{FaultID: record.FaultID, RunID: record.RunID, Seed: record.Seed, Cut: record.Cut, Started: record.Started, Terminal: record.Terminal, Outcome: string(record.Outcome), ArtifactSHA256: m1.SHA256Hex(artifact)})
		inputs = append(inputs, m1.ArtifactInput{Kind: "fault", Data: artifact})
	}
	for i, summary := range focusedSummaries {
		manifest.FocusedSubtrialSummaries = append(manifest.FocusedSubtrialSummaries, m1.FocusedSummary{ID: summary.ID, RunID: summary.RunID, RepetitionsStarted: summary.RepetitionsStarted, RepetitionsTerminal: summary.RepetitionsTerminal, RepetitionsPassed: summary.RepetitionsPassed, ArtifactSHA256: m1.SHA256Hex(focusedInputs[i].Data)})
	}
	for _, mutant := range mutantResults {
		manifest.MutantResults = append(manifest.MutantResults, m1.MutantResult{ID: mutant.ID, RunID: mutant.RunID, Outcome: string(mutant.Outcome), ArtifactSHA256: mutant.ArtifactSHA256})
	}
	independentReview, reviewArtifact, err := deriveCampaignReview(crownArtifact, ordered.Reviews)
	if err != nil {
		return result, err
	}
	manifest.IndependentReview = independentReview
	inputs = appendUniqueSegmentArtifacts(inputs, segmentArtifacts)
	inputs = append(inputs, focusedInputs...)
	inputs = append(inputs, mutantInputs...)
	inputs = append(inputs, m1.ArtifactInput{Kind: "independent-review", Data: reviewArtifact})
	publication, err := m1.PublishEvidence(config.EvidenceRoot, manifest, inputs)
	if err != nil {
		return result, err
	}
	verification, verifiedManifest, err := m1.VerifyEvidence(ctx, publication.Directory, m1.VerifyOptions{ExpectedSource: &source})
	if err != nil {
		return result, err
	}
	result.Outcome, result.Publication, result.Verification, result.Manifest = "pass", publication, verification, verifiedManifest
	if err := writeProgress(config.EvidenceRoot, newProgress(config.RunID, "terminal", 24, "pass", startedAt, "")); err != nil {
		return result, err
	}
	return result, nil
}

func ReadProgress(evidenceRoot, runID string) (Progress, error) {
	if !filepath.IsAbs(evidenceRoot) || runID == "" {
		return Progress{}, fmt.Errorf("absolute evidence root and RunID are required")
	}
	contents, err := os.ReadFile(progressPath(evidenceRoot, runID))
	if err != nil {
		return Progress{}, err
	}
	var progress Progress
	if err := json.Unmarshal(contents, &progress); err != nil {
		return Progress{}, err
	}
	validOutcome := progress.Outcome == "running" || progress.Outcome == "pass" || progress.Outcome == "fail" || progress.Outcome == "incomplete"
	if progress.SchemaVersion != 1 || progress.RunID != runID || progress.Total != len(m1.CrownSegmentIDs) || progress.Completed < 0 || progress.Completed > progress.Total || progress.Phase == "" || !validOutcome || progress.StartedAt.IsZero() || progress.UpdatedAt.Before(progress.StartedAt) {
		return Progress{}, fmt.Errorf("progress record is invalid")
	}
	return progress, nil
}

func newProgress(runID, phase string, completed int, outcome string, startedAt time.Time, diagnostic string) Progress {
	return Progress{SchemaVersion: 1, RunID: runID, Phase: phase, Completed: completed, Total: len(m1.CrownSegmentIDs), Outcome: outcome, StartedAt: startedAt, UpdatedAt: time.Now().UTC(), LastDiagnostic: diagnostic}
}

func writeProgress(evidenceRoot string, progress Progress) error {
	contents, err := m1.CanonicalJSON(progress)
	if err != nil {
		return err
	}
	target := progressPath(evidenceRoot, progress.RunID)
	temporary := target + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	return nil
}

func progressPath(evidenceRoot, runID string) string {
	return filepath.Join(evidenceRoot, ".status-"+runID+".json")
}

func currentRevision(ctx context.Context, executor *faultcampaign.ProductionExecutor) (uint64, error) {
	access, err := executor.Prepare(ctx)
	if err != nil {
		return 0, err
	}
	inspection, err := access.Port.Inspect(ctx)
	return inspection.CurrentRevision, err
}

func sourceOption(source m1.SourceBinding) *m1.SourceBinding {
	if !source.Valid() {
		return nil
	}
	return &source
}

func publishNonPass(ctx context.Context, config Config, preflight PreflightResult, lifecycle *sidecargapdb.Lifecycle, state *runEvidenceState, cause error) (m1.Publication, m1.EvidenceManifest, error) {
	_ = ctx
	outcome := "fail"
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		outcome = "incomplete"
	}
	trial := state.Ordered.Trial
	if trial.RunID == "" {
		trial = m1.OrderedTrial{SchemaVersion: 1, RunID: config.RunID, TrialID: m1.OrderedTrialID, Started: true}
	}
	trial.Terminal, trial.Outcome, trial.FR044Satisfied, trial.SC013Satisfied = true, outcome, false, false
	segments := make([]m1.CrownSegment, 0, 2+len(state.Ordered.Segments))
	segmentInputs := make([]m1.ArtifactInput, 0, 2+len(state.Ordered.Artifacts))
	if state.InitialProvenance.SupervisorRunID != "" {
		artifact, _ := m1.CanonicalJSON(state.InitialProvenance)
		revision := uint64(state.InitialProvenance.Observation.Status.CurrentRevision)
		segments = append(segments, observedSegment(config.RunID, "admission", revision, revision, artifact))
		segmentInputs = append(segmentInputs, m1.ArtifactInput{Kind: "crown-segment", Data: artifact})
	}
	if len(state.External.Records) > 0 {
		artifact, _ := m1.CanonicalJSON(state.External.Records)
		segments = append(segments, observedSegment(config.RunID, "external-14", state.External.Records[0].BeforeRevision, state.External.Records[len(state.External.Records)-1].AfterRevision, artifact))
		segmentInputs = append(segmentInputs, m1.ArtifactInput{Kind: "crown-segment", Data: artifact})
	}
	for i, segment := range state.Ordered.Segments {
		segments = append(segments, segment)
		if i < len(state.Ordered.Artifacts) {
			segmentInputs = append(segmentInputs, m1.ArtifactInput{Kind: "crown-segment", Data: state.Ordered.Artifacts[i]})
		}
	}
	for i := range segments {
		segments[i].Sequence = i + 1
	}
	crownArtifact, err := m1.SealCrownArtifact(m1.CrownArtifact{SchemaVersion: 1, RunID: config.RunID, Segments: segments, Trial: trial})
	if err != nil {
		return m1.Publication{}, m1.EvidenceManifest{}, err
	}
	crownBytes, _ := m1.CanonicalJSON(crownArtifact)
	sourceBytes, _ := m1.CanonicalJSON(state.Source)
	provenanceBytes, _ := m1.CanonicalJSON(state.InitialProvenance)
	if lifecycle != nil && state.InitialProvenance.SupervisorRunID == "" {
		if evidence, evidenceErr := lifecycle.Evidence(); evidenceErr == nil {
			provenanceBytes, _ = m1.CanonicalJSON(evidence)
		}
	}
	provingBytes, err := os.ReadFile(config.ProvingBinaryPath)
	if err != nil {
		return m1.Publication{}, m1.EvidenceManifest{}, err
	}
	contractsBytes, err := contractBundle(config.RepositoryRoot, config.ProductPolicyPath)
	if err != nil {
		return m1.Publication{}, m1.EvidenceManifest{}, err
	}
	budgetsBytes, err := os.ReadFile(config.ProductPolicyPath)
	if err != nil {
		return m1.Publication{}, m1.EvidenceManifest{}, err
	}
	diagnostic, _ := m1.CanonicalJSON(struct {
		SchemaVersion int    `json:"schema_version"`
		RunID         string `json:"run_id"`
		Outcome       string `json:"outcome"`
		Diagnostic    string `json:"diagnostic"`
	}{1, config.RunID, outcome, bounded(cause.Error())})
	manifest := m1.EvidenceManifest{SchemaVersion: 1, ProgramID: "production-runtime", MissionID: "M1", RunID: config.RunID, SourceCommit: state.Source.Commit, SourceTree: state.Source.Tree, AdmittedTupleSHA256: preflight.AdmissionRecordSHA256, SidecarProvenanceSHA256: state.Source.SidecarProvenanceSHA256, OrderedTrial: crownArtifact.Trial, InvariantSummary: m1.EvidenceInvariants(state.Ordered.Invariants), IndependentReview: m1.IndependentReview{Verdict: "fail"}, Outcome: outcome}
	inputs := []m1.ArtifactInput{{Kind: "source-binding", Data: sourceBytes}, {Kind: "sidecar-provenance", Data: provenanceBytes}, {Kind: "proving-binary", Data: provingBytes}, {Kind: "contracts", Data: contractsBytes}, {Kind: "budgets", Data: budgetsBytes}, {Kind: "crown-ledger", Data: crownBytes}, {Kind: "diagnostic", Data: diagnostic}}
	for i, record := range state.External.Records {
		if i >= len(state.External.Artifacts) {
			break
		}
		manifest.ScenarioResults = append(manifest.ScenarioResults, m1.ScenarioResult{ScenarioID: record.ScenarioID, RunID: record.RunID, Seed: config.Seed + uint64(i), Started: record.Started, Terminal: record.Terminal, Outcome: string(record.Outcome), ArtifactSHA256: record.ArtifactSHA256})
		inputs = append(inputs, m1.ArtifactInput{Kind: "scenario", Data: state.External.Artifacts[i]})
	}
	for _, record := range state.Ordered.Faults {
		artifact, marshalErr := m1.CanonicalJSON(record)
		if marshalErr != nil {
			return m1.Publication{}, m1.EvidenceManifest{}, marshalErr
		}
		manifest.FaultResults = append(manifest.FaultResults, m1.FaultResult{FaultID: record.FaultID, RunID: record.RunID, Seed: record.Seed, Cut: record.Cut, Started: record.Started, Terminal: record.Terminal, Outcome: string(record.Outcome), ArtifactSHA256: m1.SHA256Hex(artifact)})
		inputs = append(inputs, m1.ArtifactInput{Kind: "fault", Data: artifact})
	}
	for _, summary := range state.Focused {
		artifact, _ := m1.CanonicalJSON(summary)
		manifest.FocusedSubtrialSummaries = append(manifest.FocusedSubtrialSummaries, m1.FocusedSummary{ID: summary.ID, RunID: summary.RunID, RepetitionsStarted: summary.RepetitionsStarted, RepetitionsTerminal: summary.RepetitionsTerminal, RepetitionsPassed: summary.RepetitionsPassed, ArtifactSHA256: m1.SHA256Hex(artifact)})
		inputs = append(inputs, m1.ArtifactInput{Kind: "focused-summary", Data: artifact})
	}
	for _, mutant := range state.Mutants {
		if len(mutant.Artifact) == 0 {
			continue
		}
		manifest.MutantResults = append(manifest.MutantResults, m1.MutantResult{ID: mutant.ID, RunID: mutant.RunID, Outcome: string(mutant.Outcome), ArtifactSHA256: mutant.ArtifactSHA256})
		inputs = append(inputs, m1.ArtifactInput{Kind: "mutant", Data: mutant.Artifact})
	}
	manifest.ScenarioResults, inputs = completeScenarioCensus(config, manifest.ScenarioResults, inputs, outcome)
	manifest.FaultResults, inputs = completeFaultCensus(config, manifest.FaultResults, inputs, outcome)
	manifest.FocusedSubtrialSummaries, inputs = completeFocusedCensus(config, manifest.FocusedSubtrialSummaries, inputs)
	manifest.MutantResults, inputs = completeMutantCensus(config, manifest.MutantResults, inputs)
	inputs = appendUniqueSegmentArtifacts(inputs, segmentInputs)
	publication, err := m1.PublishEvidence(config.EvidenceRoot, manifest, inputs)
	return publication, manifest, err
}

func appendUniqueSegmentArtifacts(inputs, segments []m1.ArtifactInput) []m1.ArtifactInput {
	seen := make(map[string]struct{}, len(inputs)+len(segments))
	for _, input := range inputs {
		seen[m1.SHA256Hex(input.Data)] = struct{}{}
	}
	for _, segment := range segments {
		digest := m1.SHA256Hex(segment.Data)
		if _, duplicate := seen[digest]; duplicate {
			continue
		}
		seen[digest] = struct{}{}
		inputs = append(inputs, segment)
	}
	return inputs
}

func completeScenarioCensus(config Config, existing []m1.ScenarioResult, inputs []m1.ArtifactInput, outcome string) ([]m1.ScenarioResult, []m1.ArtifactInput) {
	byID := make(map[string]m1.ScenarioResult, len(existing))
	for _, record := range existing {
		byID[record.ScenarioID] = record
	}
	result := make([]m1.ScenarioResult, 0, len(m1.ScenarioIDs))
	for index, id := range m1.ScenarioIDs {
		if record, ok := byID[id]; ok {
			result = append(result, record)
			continue
		}
		artifact, _ := m1.CanonicalJSON(struct {
			RunID, ScenarioID, Outcome string
			Started, Terminal          bool
		}{config.RunID, id, outcome, false, true})
		record := m1.ScenarioResult{ScenarioID: id, RunID: config.RunID, Seed: config.Seed + uint64(index), Terminal: true, Outcome: outcome, ArtifactSHA256: m1.SHA256Hex(artifact)}
		result, inputs = append(result, record), append(inputs, m1.ArtifactInput{Kind: "scenario", Data: artifact})
	}
	return result, inputs
}

func completeFaultCensus(config Config, existing []m1.FaultResult, inputs []m1.ArtifactInput, outcome string) ([]m1.FaultResult, []m1.ArtifactInput) {
	byID := make(map[string]m1.FaultResult, len(existing))
	for _, record := range existing {
		byID[record.FaultID] = record
	}
	result := make([]m1.FaultResult, 0, len(m1.FaultIDs))
	for index, id := range m1.FaultIDs {
		if record, ok := byID[id]; ok {
			result = append(result, record)
			continue
		}
		artifact, _ := m1.CanonicalJSON(struct {
			RunID, FaultID, Cut, Outcome string
			Started, Terminal            bool
		}{config.RunID, id, m1.FaultCuts[index], outcome, false, true})
		record := m1.FaultResult{FaultID: id, RunID: config.RunID, Seed: config.Seed + uint64(index), Cut: m1.FaultCuts[index], Terminal: true, Outcome: outcome, ArtifactSHA256: m1.SHA256Hex(artifact)}
		result, inputs = append(result, record), append(inputs, m1.ArtifactInput{Kind: "fault", Data: artifact})
	}
	return result, inputs
}

func completeFocusedCensus(config Config, existing []m1.FocusedSummary, inputs []m1.ArtifactInput) ([]m1.FocusedSummary, []m1.ArtifactInput) {
	byID := make(map[string]m1.FocusedSummary, len(existing))
	for _, record := range existing {
		byID[record.ID] = record
	}
	result := make([]m1.FocusedSummary, 0, len(m1.FocusedIDs))
	for _, id := range m1.FocusedIDs {
		if record, ok := byID[id]; ok {
			result = append(result, record)
			continue
		}
		artifact, _ := m1.CanonicalJSON(struct {
			RunID, ID, Outcome string
		}{config.RunID, id, "not_run"})
		record := m1.FocusedSummary{ID: id, RunID: config.RunID, ArtifactSHA256: m1.SHA256Hex(artifact)}
		result, inputs = append(result, record), append(inputs, m1.ArtifactInput{Kind: "focused-summary", Data: artifact})
	}
	return result, inputs
}

func completeMutantCensus(config Config, existing []m1.MutantResult, inputs []m1.ArtifactInput) ([]m1.MutantResult, []m1.ArtifactInput) {
	byID := make(map[string]m1.MutantResult, len(existing))
	for _, record := range existing {
		byID[record.ID] = record
	}
	result := make([]m1.MutantResult, 0, len(m1.MutantIDs))
	for _, id := range m1.MutantIDs {
		if record, ok := byID[id]; ok {
			result = append(result, record)
			continue
		}
		artifact, _ := m1.CanonicalJSON(struct{ RunID, ID, Outcome string }{config.RunID, id, "not_run"})
		record := m1.MutantResult{ID: id, RunID: config.RunID, Outcome: "not_run", ArtifactSHA256: m1.SHA256Hex(artifact)}
		result, inputs = append(result, record), append(inputs, m1.ArtifactInput{Kind: "mutant", Data: artifact})
	}
	return result, inputs
}

func bounded(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		return value[:1024]
	}
	return value
}

func observedSegment(runID, id string, before, after uint64, artifact []byte) m1.CrownSegment {
	return m1.CrownSegment{RunID: runID, ID: id, BeforeDurableRevision: before, AfterDurableRevision: after, Started: true, Terminal: true, Outcome: "pass", ArtifactSHA256: m1.SHA256Hex(artifact)}
}

// deriveCampaignReview consumes the two records admitted by the production
// ReviewCoordinator. It cannot accept a caller-provided verdict: pass follows
// only from independent actors, approved exact candidates, and a valid sealed
// crown ledger.
func deriveCampaignReview(crown m1.CrownArtifact, records []basereview.Record) (m1.IndependentReview, []byte, error) {
	result := m1.IndependentReview{Verdict: "fail"}
	if err := m1.ValidateCrownArtifact(crown); err != nil {
		return result, nil, err
	}
	if len(records) != 2 {
		return result, nil, fmt.Errorf("campaign review requires two actual production review records")
	}
	expected := append([]string(nil), crown.Trial.Integration.CandidateIDs...)
	observed := make([]string, len(records))
	contributors := make(map[string]struct{})
	for _, review := range crown.Trial.Reviews {
		for _, actor := range review.ContributorActorIDs {
			contributors[actor] = struct{}{}
		}
	}
	for index, record := range records {
		observed[index] = record.Candidate
		if record.Verdict != basereview.Approved || record.ReviewerActorID == "" || record.ReviewerProcessID == "" {
			return result, nil, fmt.Errorf("production review %d is absent or non-approved", index)
		}
		if _, sameActor := contributors[record.ReviewerActorID.String()]; sameActor {
			return result, nil, fmt.Errorf("production reviewer is also a candidate contributor")
		}
	}
	sort.Strings(expected)
	sort.Strings(observed)
	for i := range expected {
		if expected[i] != observed[i] {
			return result, nil, fmt.Errorf("production reviews differ from exact integrated candidates")
		}
	}
	result.ReviewerActorID = records[0].ReviewerActorID.String()
	result.ReviewerProcessID = records[0].ReviewerProcessID.String()
	result.Verdict = "pass"
	artifact, err := m1.CanonicalJSON(struct {
		SchemaVersion int                 `json:"schema_version"`
		RunID         string              `json:"run_id"`
		TrialSHA256   string              `json:"trial_sha256"`
		Reviews       []basereview.Record `json:"production_reviews"`
		Verdict       string              `json:"derived_verdict"`
	}{1, crown.RunID, crown.Trial.ArtifactSHA256, records, result.Verdict})
	if err != nil {
		return m1.IndependentReview{}, nil, err
	}
	result.ArtifactSHA256 = m1.SHA256Hex(artifact)
	return result, artifact, nil
}

func fileSHA(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:]), nil
}

func contractBundle(root, budgetPath string) ([]byte, error) {
	contractRoot := filepath.Join(root, "kitty-specs", "gapdb-control-journal-proving-ground-01M18EVR", "contracts")
	paths, err := filepath.Glob(filepath.Join(contractRoot, "*"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	type entry struct {
		Path     string `json:"path"`
		SHA256   string `json:"sha256"`
		Contents []byte `json:"contents"`
	}
	bundle := struct {
		SchemaVersion int     `json:"schema_version"`
		Entries       []entry `json:"entries"`
	}{SchemaVersion: 1}
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(budgetPath) {
			continue
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		digest := sha256.Sum256(contents)
		bundle.Entries = append(bundle.Entries, entry{Path: strings.TrimPrefix(filepath.ToSlash(path), filepath.ToSlash(root)+"/"), SHA256: hex.EncodeToString(digest[:]), Contents: contents})
	}
	for _, relative := range []string{"go.mod", "go.sum"} {
		contents, readErr := os.ReadFile(filepath.Join(root, relative))
		if readErr != nil {
			return nil, readErr
		}
		digest := sha256.Sum256(contents)
		bundle.Entries = append(bundle.Entries, entry{Path: relative, SHA256: hex.EncodeToString(digest[:]), Contents: contents})
	}
	sort.Slice(bundle.Entries, func(i, j int) bool { return bundle.Entries[i].Path < bundle.Entries[j].Path })
	return m1.CanonicalJSON(bundle)
}
