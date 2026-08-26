package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign"
)

const qualificationEvidenceSchemaPath = "kitty-specs/beads-tracker-integration-01M0YG7E/contracts/qualification-evidence.schema.json"

var requiredProviderScenarios = []string{
	"TestRealBeadsQualification/inspection",
	"TestRealBeadsQualification/catalog_round_trip",
	"TestRealBeadsQualification/content_drift",
	"TestRealBeadsQualification/graph_and_discussion",
	"TestRealBeadsQualification/assignment_safe_refusal",
	"TestRealBeadsQualification/latency",
	"TestRealBeadsWorktreeQualification/shared_identity",
	"TestRealBeadsWorktreeQualification/embedded_serialization",
	"TestRealBeadsWorktreeQualification/server_multiwriter_contention",
	"TestRealBeadsBackupQualification/jsonl_interchange",
	"TestRealBeadsBackupQualification/native_backup_restore",
}

type options struct {
	BinaryPath string
	Digest     string
	Evidence   string
	Repository string
	Timeout    time.Duration
	Keep       bool
}

type scenarioCensus = testcampaign.BeadsScenarioCensus
type hostEvidence = testcampaign.BeadsHostEvidence
type latencyEvidence = testcampaign.BeadsLatencyEvidence
type contentionEvidence = testcampaign.BeadsContentionEvidence
type assignmentPreservationEvidence = testcampaign.BeadsAssignmentEvidence

type authorityAuditEvidence struct {
	Audits   int `json:"audits"`
	Failures int `json:"failures"`
}

type issueEvidence = testcampaign.BeadsIssueEvidence
type qualificationEvidence = testcampaign.BeadsQualificationEvidence

type staleAuthorityEvidence struct {
	Attempted                     int `json:"attempted"`
	Refused                       int `json:"refused"`
	ReplacementWorkspacePreserved int `json:"replacement_workspace_preserved"`
}

type providerTestResult struct {
	Census         scenarioCensus
	Latencies      []latencyEvidence
	Contention     *contentionEvidence
	Assignment     *assignmentPreservationEvidence
	Authority      *authorityAuditEvidence
	StaleAuthority *staleAuthorityEvidence
	Issues         []issueEvidence
	Proof          testcampaign.BeadsEvidenceProof
	Err            error
}

type qualificationFixture interface {
	initialize(context.Context) error
	cleanup() error
}

var (
	verifyQualificationBinary = verifyBinary
	makeQualificationFixture  = func(identity binaryIdentity, keep bool) (qualificationFixture, error) {
		return newFixture(identity, keep)
	}
	executeQualificationProviderTests = runProviderTests
	readQualificationSourceIdentity   = sourceIdentity
	verifyQualificationOffline        = offlineQualificationVerified
)

type testEvent struct {
	Action  string  `json:"Action"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

func runQualification(ctx context.Context, input options) (qualificationEvidence, error) {
	started := time.Now().UTC()
	if input.Timeout <= 0 || input.Timeout > 30*time.Minute {
		return qualificationEvidence{}, errors.New("qualification timeout must be positive and no greater than 30 minutes")
	}
	identity, err := verifyQualificationBinary(ctx, input.BinaryPath, input.Digest)
	if err != nil {
		return qualificationEvidence{}, err
	}
	repository, err := canonicalRepository(input.Repository)
	if err != nil {
		return qualificationEvidence{}, err
	}
	commit, tree, dirty, err := readQualificationSourceIdentity(ctx, repository)
	if err != nil {
		return qualificationEvidence{}, err
	}
	if dirty {
		return qualificationEvidence{}, errors.New("qualification source tree is dirty; evidence would not identify tested bytes")
	}
	evidencePath, err := validateEvidencePath(input.Evidence)
	if err != nil {
		return qualificationEvidence{}, err
	}

	fixture, err := makeQualificationFixture(identity, input.Keep)
	if err != nil {
		return qualificationEvidence{}, err
	}
	defer func() { _ = fixture.cleanup() }()
	if err := fixture.initialize(ctx); err != nil {
		return qualificationEvidence{}, err
	}

	observed := executeQualificationProviderTests(ctx, repository, identity, input.Timeout)
	authorityAudits, authorityFailures := 0, 0
	if observed.Authority != nil {
		authorityAudits, authorityFailures = observed.Authority.Audits, observed.Authority.Failures
	}
	postCommit, postTree, postDirty, postErr := readQualificationSourceIdentity(ctx, repository)
	if postErr != nil {
		observed.Issues = append(observed.Issues, issueEvidence{Kind: "source_rebind_failed", Message: "qualification source could not be rebound after execution"})
	} else if postCommit != commit || postTree != tree || postDirty {
		observed.Issues = append(observed.Issues, issueEvidence{Kind: "source_drift", Message: "qualification source changed or became dirty during execution"})
	}
	sourceClean := postErr == nil && postCommit == commit && postTree == tree && !postDirty
	offline := verifyQualificationOffline()
	observed.Proof.SourceBindings = []testcampaign.BeadsSourceBinding{{Commit: commit, Tree: tree, Dirty: false}}
	if postErr == nil {
		observed.Proof.SourceBindings = append(observed.Proof.SourceBindings, testcampaign.BeadsSourceBinding{Commit: postCommit, Tree: postTree, Dirty: postDirty})
	}
	observed.Proof.ProviderVerified = true
	observed.Proof.OfflineVerified = offline
	observed.Proof.StaleAuthorityVerified = observed.StaleAuthority != nil && observed.StaleAuthority.Attempted == 1 && observed.StaleAuthority.Refused == 1 && observed.StaleAuthority.ReplacementWorkspacePreserved == 1
	observed.Proof.SourceClean = sourceClean
	observed.Proof.AssertionFailed = observed.Census.Failed > 0 || authorityFailures > 0
	observed.Proof.PrerequisiteMissing = !offline || !sourceClean || !observed.Proof.StaleAuthorityVerified || len(observed.Proof.SkippedIDs) != 0 || (len(observed.Issues) != 0 && !observed.Proof.AssertionFailed)
	result := qualificationEvidence{
		SchemaVersion: 1, SourceCommit: commit, SourceTree: tree,
		ProviderVersion: identity.Version, ProviderDigestSHA256: identity.Digest,
		ProtocolSchema: identity.Schema, StorageMode: qualificationStorageMode(),
		Host:      hostEvidence{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Runner: safeRunnerName()},
		StartedAt: started, FinishedAt: time.Now().UTC(), Scenarios: observed.Census, Latency: observed.Latencies, Contention: observed.Contention,
		AssignmentPreservation: observed.Assignment, AuthorityAudits: authorityAudits,
		AuthorityInvariantFailures: authorityFailures, Issues: observed.Issues, Verdict: "non_admissible", Proof: observed.Proof,
	}
	if observed.Census.Started == observed.Census.Expected && observed.Census.Terminal == observed.Census.Expected && observed.Census.Passed == observed.Census.Expected && observed.Census.Failed == 0 && authorityFailures == 0 && len(observed.Issues) == 0 && observed.Err == nil && offline && sourceClean && observed.Proof.StaleAuthorityVerified {
		result.Verdict = "pass"
	} else if observed.Proof.AssertionFailed {
		result.Verdict = "fail"
	}
	result.FinishedAt = time.Now().UTC()
	if err := writeEvidence(evidencePath, result); err != nil {
		return result, err
	}
	if result.Verdict != "pass" {
		if observed.Err != nil {
			return result, fmt.Errorf("qualification %s: %w", result.Verdict, observed.Err)
		}
		return result, fmt.Errorf("qualification %s: required scenario census is incomplete", result.Verdict)
	}
	return result, nil
}

func runProviderTests(ctx context.Context, repository string, identity binaryIdentity, timeout time.Duration) providerTestResult {
	census := scenarioCensus{Expected: len(requiredProviderScenarios)}
	result := providerTestResult{Census: census, Proof: testcampaign.BeadsEvidenceProof{ExpectedIDs: requiredProviderScenariosSorted()}}
	pattern := `^(TestRealBeadsQualification|TestRealBeadsWorktreeQualification|TestRealBeadsBackupQualification)$`
	testCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(testCtx, "go", "test", "-json", "-count=1", "./internal/tracker/beads", "-run", pattern, "-timeout", timeout.String())
	command.Dir = repository
	command.Env = append(qualificationEnv("", ""),
		"BEADS_BIN="+identity.Path,
		"BEADS_DIGEST_SHA256="+identity.Digest,
		"SPEC_KITTY_REAL_BEADS_QUALIFICATION=1",
		"BEADS_SERVER_ID="+os.Getenv("BEADS_SERVER_ID"),
		"BEADS_SERVER_ENDPOINT="+os.Getenv("BEADS_SERVER_ENDPOINT"),
		"DOLT_SERVER_HOST="+os.Getenv("DOLT_SERVER_HOST"),
		"DOLT_SERVER_PORT="+os.Getenv("DOLT_SERVER_PORT"),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		result.Err = err
		return result
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		result.Err = err
		return result
	}
	states := make(map[string]string, len(requiredProviderScenarios))
	latencyByOperation := make(map[string]latencyEvidence, 2)
	var contention *contentionEvidence
	var assignment *assignmentPreservationEvidence
	var authority *authorityAuditEvidence
	var staleAuthority *staleAuthorityEvidence
	issues := make([]issueEvidence, 0)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		var event testEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			issues = append(issues, issueEvidence{Kind: "malformed_test_event", Message: "go test emitted malformed structured output"})
			continue
		}
		if isRequiredScenario(event.Test) {
			switch event.Action {
			case "run":
				if states[event.Test] == "" {
					states[event.Test] = "run"
					census.Started++
				}
			case "pass", "fail", "skip":
				if states[event.Test] == "run" {
					census.Terminal++
				}
				states[event.Test] = event.Action
				if event.Action == "pass" {
					census.Passed++
				} else if event.Action == "fail" {
					census.Failed++
					issues = append(issues, issueEvidence{Kind: "scenario_failed", Message: event.Test})
				} else {
					issues = append(issues, issueEvidence{Kind: "scenario_skipped", Message: event.Test})
				}
			}
		}
		if marker := strings.Index(event.Output, "WP09_LATENCY "); marker >= 0 {
			var sample latencyEvidence
			if err := decodeEvidenceMarker(event.Output[marker+len("WP09_LATENCY "):], &sample); err != nil {
				issues = append(issues, issueEvidence{Kind: "latency_malformed", Message: "latency evidence was malformed"})
			} else if _, duplicate := latencyByOperation[sample.Operation]; duplicate {
				issues = append(issues, issueEvidence{Kind: "latency_duplicate", Message: "latency operation evidence was duplicated"})
			} else {
				latencyByOperation[sample.Operation] = sample
			}
		}
		if marker := strings.Index(event.Output, "WP09_CONTENTION "); marker >= 0 {
			var sample contentionEvidence
			if decodeEvidenceMarker(event.Output[marker+len("WP09_CONTENTION "):], &sample) == nil && contention == nil {
				contention = &sample
			} else {
				issues = append(issues, issueEvidence{Kind: "malformed_contention", Message: "contention evidence was malformed or duplicated"})
			}
		}
		if marker := strings.Index(event.Output, "WP09_ASSIGNMENT_PRESERVATION "); marker >= 0 {
			var sample assignmentPreservationEvidence
			if decodeEvidenceMarker(event.Output[marker+len("WP09_ASSIGNMENT_PRESERVATION "):], &sample) == nil && assignment == nil {
				assignment = &sample
			} else {
				issues = append(issues, issueEvidence{Kind: "assignment_preservation_malformed", Message: "assignment preservation evidence was malformed or duplicated"})
			}
		}
		if marker := strings.Index(event.Output, "WP09_AUTHORITY_AUDIT "); marker >= 0 {
			var sample authorityAuditEvidence
			if decodeEvidenceMarker(event.Output[marker+len("WP09_AUTHORITY_AUDIT "):], &sample) == nil && authority == nil {
				authority = &sample
			} else {
				issues = append(issues, issueEvidence{Kind: "authority_audit_malformed", Message: "authority audit evidence was malformed or duplicated"})
			}
		}
		if marker := strings.Index(event.Output, "WP10_STALE_AUTHORITY "); marker >= 0 {
			var sample staleAuthorityEvidence
			if decodeEvidenceMarker(event.Output[marker+len("WP10_STALE_AUTHORITY "):], &sample) == nil && staleAuthority == nil {
				staleAuthority = &sample
			} else {
				issues = append(issues, issueEvidence{Kind: "stale_authority_malformed", Message: "stale authority evidence was malformed or duplicated"})
			}
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if stderr.overflow {
		issues = append(issues, issueEvidence{Kind: "stderr_overflow", Message: "go test stderr exceeded the qualification bound"})
	}
	for _, name := range requiredProviderScenarios {
		if states[name] == "" {
			issues = append(issues, issueEvidence{Kind: "scenario_missing", Message: name})
		}
	}
	latencies := make([]latencyEvidence, 0, len(latencyByOperation))
	for _, sample := range latencyByOperation {
		latencies = append(latencies, sample)
	}
	issues = append(issues, validateProviderEvidence(qualificationStorageMode(), latencies, contention, assignment, authority)...)
	if staleAuthority == nil {
		issues = append(issues, issueEvidence{Kind: "stale_authority_missing", Message: "real stale lease, fence, and workspace probe was not emitted"})
	} else if staleAuthority.Attempted != 1 || staleAuthority.Refused != 1 || staleAuthority.ReplacementWorkspacePreserved != 1 {
		issues = append(issues, issueEvidence{Kind: "stale_authority_invalid", Message: "real stale authority probe failed its exact census"})
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i].Operation < latencies[j].Operation })
	for name, state := range states {
		result.Proof.StartedIDs = append(result.Proof.StartedIDs, name)
		if state == "pass" || state == "fail" || state == "skip" {
			result.Proof.TerminalIDs = append(result.Proof.TerminalIDs, name)
		}
		switch state {
		case "pass":
			result.Proof.PassedIDs = append(result.Proof.PassedIDs, name)
		case "fail":
			result.Proof.FailedIDs = append(result.Proof.FailedIDs, name)
		case "skip":
			result.Proof.SkippedIDs = append(result.Proof.SkippedIDs, name)
		}
	}
	sort.Strings(result.Proof.StartedIDs)
	sort.Strings(result.Proof.TerminalIDs)
	sort.Strings(result.Proof.PassedIDs)
	sort.Strings(result.Proof.FailedIDs)
	sort.Strings(result.Proof.SkippedIDs)
	result.Census, result.Latencies, result.Contention, result.Assignment, result.Authority, result.StaleAuthority, result.Issues = census, latencies, contention, assignment, authority, staleAuthority, issues
	if scanErr != nil {
		result.Err = scanErr
		return result
	}
	if waitErr != nil {
		result.Err = errors.New("real-provider test process failed; inspect redacted test output locally")
		return result
	}
	return result
}

func decodeEvidenceMarker(encoded string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("evidence marker has trailing JSON")
	}
	return nil
}

func validateProviderEvidence(storageMode string, latencies []latencyEvidence, contention *contentionEvidence, assignment *assignmentPreservationEvidence, authority *authorityAuditEvidence) []issueEvidence {
	issues := make([]issueEvidence, 0)
	expectedLatency := map[string]struct {
		samples int
		p95Max  float64
	}{"preflight": {samples: 100, p95Max: 1000}, "warm_single_item_read": {samples: 200, p95Max: 500}}
	seen := make(map[string]struct{}, len(latencies))
	for _, sample := range latencies {
		expected, present := expectedLatency[sample.Operation]
		_, duplicate := seen[sample.Operation]
		validNumbers := !math.IsNaN(sample.P50MS) && !math.IsNaN(sample.P95MS) && !math.IsNaN(sample.MaxMS) &&
			!math.IsInf(sample.P50MS, 0) && !math.IsInf(sample.P95MS, 0) && !math.IsInf(sample.MaxMS, 0) &&
			sample.P50MS >= 0 && sample.P50MS <= sample.P95MS && sample.P95MS <= sample.MaxMS
		if !present || duplicate || sample.Samples != expected.samples || !validNumbers || sample.P95MS > expected.p95Max {
			issues = append(issues, issueEvidence{Kind: "latency_invalid", Message: "latency evidence failed its exact operation, sample, ordering, or threshold contract"})
		}
		seen[sample.Operation] = struct{}{}
	}
	for operation := range expectedLatency {
		if _, present := seen[operation]; !present {
			issues = append(issues, issueEvidence{Kind: "latency_missing", Message: "required latency operation was not emitted"})
		}
	}
	if storageMode == "server_multiwriter" {
		if contention == nil {
			issues = append(issues, issueEvidence{Kind: "contention_missing", Message: "provider-native contention evidence was not emitted"})
		} else if contention.Mode != "server_multiwriter" || contention.Processes != 8 || contention.Attempted != 1000 || contention.Terminal != 1000 || contention.Passed != 1000 || contention.Collisions != 0 || contention.Duplicates != 0 || contention.DurationMS <= 0 || math.IsNaN(contention.DurationMS) || math.IsInf(contention.DurationMS, 0) {
			issues = append(issues, issueEvidence{Kind: "contention_invalid", Message: "provider-native contention evidence failed its exact census"})
		}
	}
	if assignment == nil {
		issues = append(issues, issueEvidence{Kind: "assignment_preservation_missing", Message: "assignment preservation census was not emitted"})
	} else if assignment.Attempted != 20 || assignment.Terminal != 20 || assignment.Preserved != 20 || assignment.Failed != 0 || assignment.AttributionLost != 10 || assignment.SafeCompensationUnsupported != 10 {
		issues = append(issues, issueEvidence{Kind: "assignment_preservation_invalid", Message: "assignment preservation evidence failed its exact census"})
	}
	if authority == nil {
		issues = append(issues, issueEvidence{Kind: "authority_audit_missing", Message: "production-relevant authority audit was not emitted"})
	} else if authority.Audits < 1 || authority.Failures != 0 {
		issues = append(issues, issueEvidence{Kind: "authority_audit_failed", Message: "production-relevant authority audit reported an invalid census or failure"})
	}
	return issues
}

func isRequiredScenario(name string) bool {
	index := sort.SearchStrings(requiredProviderScenariosSorted(), name)
	names := requiredProviderScenariosSorted()
	return index < len(names) && names[index] == name
}

func requiredProviderScenariosSorted() []string {
	names := append([]string(nil), requiredProviderScenarios...)
	sort.Strings(names)
	return names
}

func canonicalRepository(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("repository must be an absolute clean path")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

func sourceIdentity(ctx context.Context, repository string) (string, string, bool, error) {
	read := func(args ...string) (string, error) {
		stdout, _, err := runBounded(ctx, repository, qualificationEnv("", ""), "git", args...)
		return strings.TrimSpace(string(stdout)), err
	}
	commit, err := read("rev-parse", "HEAD")
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(commit) {
		return "", "", false, errors.New("cannot resolve source commit")
	}
	tree, err := read("rev-parse", "HEAD^{tree}")
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(tree) {
		return "", "", false, errors.New("cannot resolve source tree")
	}
	status, err := read("status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return "", "", false, errors.New("cannot inspect source status")
	}
	return commit, tree, status != "", nil
}

func validateEvidencePath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("evidence path must be absolute and clean")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", errors.New("evidence parent must already exist")
	}
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", errors.New("evidence parent must be a directory")
	}
	if _, err := os.Lstat(path); err == nil {
		return "", errors.New("evidence path must not overwrite an existing file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func writeEvidence(path string, evidence qualificationEvidence) error {
	if _, err := os.Lstat(path); err == nil {
		return errors.New("evidence path must not overwrite an existing file")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return testcampaign.WriteBeadsEvidence(path, evidence)
}

func offlineQualificationVerified() bool {
	if runtime.GOOS != "linux" || os.Getenv("OFFLINE_ENFORCED") != "linux-network-namespace" {
		return false
	}
	self, selfErr := os.Readlink("/proc/self/ns/net")
	init, initErr := os.Readlink("/proc/1/ns/net")
	return selfErr == nil && initErr == nil && self != init
}

func safeRunnerName() string {
	value := os.Getenv("RUNNER_NAME")
	if matched, _ := regexp.MatchString(`^[A-Za-z0-9._-]{1,64}$`, value); matched {
		return value
	}
	return "local"
}

func qualificationStorageMode() string {
	for _, key := range []string{"BEADS_SERVER_ID", "BEADS_SERVER_ENDPOINT", "DOLT_SERVER_HOST", "DOLT_SERVER_PORT"} {
		if os.Getenv(key) == "" {
			return "embedded_serialized"
		}
	}
	return "server_multiwriter"
}
