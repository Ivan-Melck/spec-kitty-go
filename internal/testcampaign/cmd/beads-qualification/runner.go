package main

import (
	"bufio"
	"bytes"
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

type scenarioCensus struct {
	Expected int `json:"expected"`
	Started  int `json:"started"`
	Terminal int `json:"terminal"`
	Passed   int `json:"passed"`
	Failed   int `json:"failed"`
}

type hostEvidence struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	Runner string `json:"runner,omitempty"`
}

type latencyEvidence struct {
	Operation string  `json:"operation"`
	Samples   int     `json:"samples"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	MaxMS     float64 `json:"max_ms"`
}

type contentionEvidence struct {
	Mode       string  `json:"mode"`
	Processes  int     `json:"processes"`
	Attempted  int     `json:"attempted"`
	Terminal   int     `json:"terminal"`
	Passed     int     `json:"passed"`
	Collisions int     `json:"collisions"`
	Duplicates int     `json:"duplicates"`
	DurationMS float64 `json:"duration_ms"`
}

type assignmentPreservationEvidence struct {
	Attempted                   int `json:"attempted"`
	Terminal                    int `json:"terminal"`
	Preserved                   int `json:"preserved"`
	Failed                      int `json:"failed"`
	AttributionLost             int `json:"attribution_lost"`
	SafeCompensationUnsupported int `json:"safe_compensation_unsupported"`
}

type authorityAuditEvidence struct {
	Audits   int `json:"audits"`
	Failures int `json:"failures"`
}

type issueEvidence struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type qualificationEvidence struct {
	SchemaVersion              int                             `json:"schema_version"`
	SourceCommit               string                          `json:"source_commit"`
	SourceTree                 string                          `json:"source_tree"`
	ProviderVersion            string                          `json:"provider_version"`
	ProviderDigestSHA256       string                          `json:"provider_digest_sha256"`
	ProtocolSchema             int                             `json:"protocol_schema"`
	StorageMode                string                          `json:"storage_mode"`
	Host                       hostEvidence                    `json:"host"`
	StartedAt                  time.Time                       `json:"started_at"`
	FinishedAt                 time.Time                       `json:"finished_at"`
	Scenarios                  scenarioCensus                  `json:"scenarios"`
	Latency                    []latencyEvidence               `json:"latency,omitempty"`
	Contention                 *contentionEvidence             `json:"contention,omitempty"`
	AssignmentPreservation     *assignmentPreservationEvidence `json:"assignment_preservation,omitempty"`
	AuthorityAudits            int                             `json:"authority_audits"`
	AuthorityInvariantFailures int                             `json:"authority_invariant_failures"`
	Issues                     []issueEvidence                 `json:"issues,omitempty"`
	Verdict                    string                          `json:"verdict"`
}

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
	identity, err := verifyBinary(ctx, input.BinaryPath, input.Digest)
	if err != nil {
		return qualificationEvidence{}, err
	}
	repository, err := canonicalRepository(input.Repository)
	if err != nil {
		return qualificationEvidence{}, err
	}
	commit, tree, dirty, err := sourceIdentity(ctx, repository)
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

	fixture, err := newFixture(identity, input.Keep)
	if err != nil {
		return qualificationEvidence{}, err
	}
	defer func() { _ = fixture.cleanup() }()
	if err := fixture.initialize(ctx); err != nil {
		return qualificationEvidence{}, err
	}

	census, latencies, contention, assignment, authority, issues, runErr := runProviderTests(ctx, repository, identity, input.Timeout)
	authorityAudits, authorityFailures := 0, 1
	if authority != nil {
		authorityAudits, authorityFailures = authority.Audits, authority.Failures
	}
	result := qualificationEvidence{
		SchemaVersion: 1, SourceCommit: commit, SourceTree: tree,
		ProviderVersion: identity.Version, ProviderDigestSHA256: identity.Digest,
		ProtocolSchema: identity.Schema, StorageMode: qualificationStorageMode(),
		Host:      hostEvidence{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Runner: safeRunnerName()},
		StartedAt: started, FinishedAt: time.Now().UTC(), Scenarios: census, Latency: latencies, Contention: contention,
		AssignmentPreservation: assignment, AuthorityAudits: authorityAudits,
		AuthorityInvariantFailures: authorityFailures, Issues: issues, Verdict: "non_admissible",
	}
	if census.Started == census.Expected && census.Terminal == census.Expected && census.Passed == census.Expected && census.Failed == 0 && authorityFailures == 0 && len(issues) == 0 && runErr == nil {
		result.Verdict = "pass"
	} else if census.Failed > 0 {
		result.Verdict = "fail"
	}
	result.FinishedAt = time.Now().UTC()
	if err := writeEvidence(evidencePath, result); err != nil {
		return result, err
	}
	if result.Verdict != "pass" {
		if runErr != nil {
			return result, fmt.Errorf("qualification %s: %w", result.Verdict, runErr)
		}
		return result, fmt.Errorf("qualification %s: required scenario census is incomplete", result.Verdict)
	}
	return result, nil
}

func runProviderTests(ctx context.Context, repository string, identity binaryIdentity, timeout time.Duration) (scenarioCensus, []latencyEvidence, *contentionEvidence, *assignmentPreservationEvidence, *authorityAuditEvidence, []issueEvidence, error) {
	census := scenarioCensus{Expected: len(requiredProviderScenarios)}
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
		return census, nil, nil, nil, nil, nil, err
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return census, nil, nil, nil, nil, nil, err
	}
	states := make(map[string]string, len(requiredProviderScenarios))
	latencyByOperation := make(map[string]latencyEvidence, 2)
	var contention *contentionEvidence
	var assignment *assignmentPreservationEvidence
	var authority *authorityAuditEvidence
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
	sort.Slice(latencies, func(i, j int) bool { return latencies[i].Operation < latencies[j].Operation })
	if scanErr != nil {
		return census, latencies, contention, assignment, authority, issues, scanErr
	}
	if waitErr != nil {
		return census, latencies, contention, assignment, authority, issues, errors.New("real-provider test process failed; inspect redacted test output locally")
	}
	return census, latencies, contention, assignment, authority, issues, nil
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

func validateQualificationEvidence(evidence qualificationEvidence) error {
	if evidence.SchemaVersion != 1 || evidence.ProviderVersion != qualifiedVersion || evidence.ProtocolSchema != qualifiedSchema {
		return errors.New("schema or provider compatibility tuple is invalid")
	}
	if !isLowerHex(evidence.SourceCommit, 40) || !isLowerHex(evidence.SourceTree, 40) || !isLowerHex(evidence.ProviderDigestSHA256, 64) {
		return errors.New("source or provider identity is invalid")
	}
	if evidence.StorageMode != "embedded_serialized" && evidence.StorageMode != "server_multiwriter" {
		return errors.New("storage mode is invalid")
	}
	if !boundedText(evidence.Host.GOOS, 1, 64) || !boundedText(evidence.Host.GOARCH, 1, 64) || (evidence.Host.Runner != "" && !boundedText(evidence.Host.Runner, 1, 64)) {
		return errors.New("host evidence is invalid")
	}
	if evidence.StartedAt.IsZero() || evidence.FinishedAt.IsZero() || evidence.FinishedAt.Before(evidence.StartedAt) {
		return errors.New("evidence time interval is invalid")
	}
	if evidence.Scenarios.Expected != len(requiredProviderScenarios) || !boundedInt(evidence.Scenarios.Started, 0, 11) || !boundedInt(evidence.Scenarios.Terminal, 0, 11) || !boundedInt(evidence.Scenarios.Passed, 0, 11) || !boundedInt(evidence.Scenarios.Failed, 0, 11) {
		return errors.New("scenario census is outside its bounded contract")
	}
	if evidence.Scenarios.Terminal > evidence.Scenarios.Started || evidence.Scenarios.Passed+evidence.Scenarios.Failed > evidence.Scenarios.Terminal {
		return errors.New("scenario census is internally inconsistent")
	}
	if len(evidence.Latency) > 2 {
		return errors.New("latency observation count exceeds its bound")
	}
	for _, observation := range evidence.Latency {
		if !boundedText(observation.Operation, 1, 64) || !boundedInt(observation.Samples, 0, 1000000) || !boundedFloat(observation.P50MS, 0, 1800000) || !boundedFloat(observation.P95MS, 0, 1800000) || !boundedFloat(observation.MaxMS, 0, 1800000) {
			return errors.New("latency observation is outside its bounded contract")
		}
	}
	if evidence.Contention != nil {
		observation := evidence.Contention
		if !boundedText(observation.Mode, 1, 64) || !boundedInt(observation.Processes, 0, 1024) || !boundedInt(observation.Attempted, 0, 10000000) || !boundedInt(observation.Terminal, 0, 10000000) || !boundedInt(observation.Passed, 0, 10000000) || !boundedInt(observation.Collisions, 0, 10000000) || !boundedInt(observation.Duplicates, 0, 10000000) || !boundedFloat(observation.DurationMS, 0, 1800000) {
			return errors.New("contention observation is outside its bounded contract")
		}
	}
	if evidence.AssignmentPreservation != nil {
		observation := evidence.AssignmentPreservation
		for _, count := range []int{observation.Attempted, observation.Terminal, observation.Preserved, observation.Failed, observation.AttributionLost, observation.SafeCompensationUnsupported} {
			if !boundedInt(count, 0, 1000000) {
				return errors.New("assignment preservation observation is outside its bounded contract")
			}
		}
	}
	if !boundedInt(evidence.AuthorityAudits, 0, 1000000) || !boundedInt(evidence.AuthorityInvariantFailures, 0, 1000000) {
		return errors.New("authority observation is outside its bounded contract")
	}
	if len(evidence.Issues) > 128 {
		return errors.New("issue count exceeds its bound")
	}
	for _, issue := range evidence.Issues {
		if !boundedText(issue.Kind, 1, 128) || !boundedText(issue.Message, 1, 1024) {
			return errors.New("issue evidence is outside its bounded contract")
		}
	}
	switch evidence.Verdict {
	case "pass":
		if evidence.StorageMode != "server_multiwriter" || evidence.Scenarios.Started != evidence.Scenarios.Expected || evidence.Scenarios.Terminal != evidence.Scenarios.Expected || evidence.Scenarios.Passed != evidence.Scenarios.Expected || evidence.Scenarios.Failed != 0 || len(evidence.Issues) != 0 {
			return errors.New("passing verdict has an incomplete scenario or issue census")
		}
		authority := &authorityAuditEvidence{Audits: evidence.AuthorityAudits, Failures: evidence.AuthorityInvariantFailures}
		if issues := validateProviderEvidence(evidence.StorageMode, evidence.Latency, evidence.Contention, evidence.AssignmentPreservation, authority); len(issues) != 0 {
			return errors.New("passing verdict has incomplete provider evidence")
		}
	case "fail":
		if evidence.Scenarios.Failed == 0 {
			return errors.New("failed verdict has no failed scenario")
		}
	case "non_admissible":
	default:
		return errors.New("verdict is invalid")
	}
	return nil
}

func isLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func boundedText(value string, minimum, maximum int) bool {
	length := len([]rune(value))
	return length >= minimum && length <= maximum
}

func boundedInt(value, minimum, maximum int) bool {
	return value >= minimum && value <= maximum
}

func boundedFloat(value, minimum, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minimum && value <= maximum
}

func writeEvidence(path string, evidence qualificationEvidence) error {
	if err := validateQualificationEvidence(evidence); err != nil {
		return fmt.Errorf("evidence violates typed contract: %w", err)
	}
	payload, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if containsSensitiveEvidence(payload) {
		return errors.New("evidence contains a prohibited body, transcript, environment, or credential field")
	}
	if err := validateSerializedRoundTrip(payload, evidence); err != nil {
		return err
	}
	return publishEvidence(path, payload)
}

func validateSerializedRoundTrip(payload []byte, evidence qualificationEvidence) error {
	var roundTrip qualificationEvidence
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&roundTrip); err != nil {
		return fmt.Errorf("strictly decode serialized evidence: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("serialized evidence contains trailing JSON data")
	}
	roundTripPayload, err := json.MarshalIndent(roundTrip, "", "  ")
	if err != nil {
		return fmt.Errorf("re-encode serialized evidence: %w", err)
	}
	roundTripPayload = append(roundTripPayload, '\n')
	if !bytes.Equal(roundTripPayload, payload) {
		return errors.New("serialized evidence does not round-trip through its typed contract")
	}
	return nil
}

func publishEvidence(path string, payload []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".qualification-evidence-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func containsSensitiveEvidence(payload []byte) bool {
	lower := strings.ToLower(string(payload))
	for _, sentinel := range []string{"comment_body", "description_body", "raw_stderr", "raw_stdout", "environment", "credential", "secret", "token="} {
		if strings.Contains(lower, sentinel) {
			return true
		}
	}
	return false
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
