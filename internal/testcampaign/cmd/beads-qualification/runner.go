package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

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

type issueEvidence struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type qualificationEvidence struct {
	SchemaVersion              int                 `json:"schema_version"`
	SourceCommit               string              `json:"source_commit"`
	SourceTree                 string              `json:"source_tree"`
	ProviderVersion            string              `json:"provider_version"`
	ProviderDigestSHA256       string              `json:"provider_digest_sha256"`
	ProtocolSchema             int                 `json:"protocol_schema"`
	StorageMode                string              `json:"storage_mode"`
	Host                       hostEvidence        `json:"host"`
	StartedAt                  time.Time           `json:"started_at"`
	FinishedAt                 time.Time           `json:"finished_at"`
	Scenarios                  scenarioCensus      `json:"scenarios"`
	Latency                    []latencyEvidence   `json:"latency,omitempty"`
	Contention                 *contentionEvidence `json:"contention,omitempty"`
	AuthorityInvariantFailures int                 `json:"authority_invariant_failures"`
	Issues                     []issueEvidence     `json:"issues,omitempty"`
	Verdict                    string              `json:"verdict"`
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

	census, latencies, contention, issues, runErr := runProviderTests(ctx, repository, identity, input.Timeout)
	result := qualificationEvidence{
		SchemaVersion: 1, SourceCommit: commit, SourceTree: tree,
		ProviderVersion: identity.Version, ProviderDigestSHA256: identity.Digest,
		ProtocolSchema: identity.Schema, StorageMode: qualificationStorageMode(),
		Host:      hostEvidence{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Runner: safeRunnerName()},
		StartedAt: started, FinishedAt: time.Now().UTC(), Scenarios: census, Latency: latencies, Contention: contention,
		AuthorityInvariantFailures: 0, Issues: issues, Verdict: "non_admissible",
	}
	if census.Started == census.Expected && census.Terminal == census.Expected && census.Passed == census.Expected && census.Failed == 0 && len(issues) == 0 && runErr == nil {
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

func runProviderTests(ctx context.Context, repository string, identity binaryIdentity, timeout time.Duration) (scenarioCensus, []latencyEvidence, *contentionEvidence, []issueEvidence, error) {
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
		return census, nil, nil, nil, err
	}
	var stderr limitedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return census, nil, nil, nil, err
	}
	states := make(map[string]string, len(requiredProviderScenarios))
	latencies := make([]latencyEvidence, 0, 2)
	var contention *contentionEvidence
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
			if json.Unmarshal([]byte(strings.TrimSpace(event.Output[marker+len("WP09_LATENCY "):])), &sample) == nil {
				latencies = append(latencies, sample)
			}
		}
		if marker := strings.Index(event.Output, "WP09_CONTENTION "); marker >= 0 {
			var sample contentionEvidence
			if json.Unmarshal([]byte(strings.TrimSpace(event.Output[marker+len("WP09_CONTENTION "):])), &sample) == nil && contention == nil {
				contention = &sample
			} else {
				issues = append(issues, issueEvidence{Kind: "malformed_contention", Message: "contention evidence was malformed or duplicated"})
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
	if qualificationStorageMode() == "server_multiwriter" {
		if contention == nil {
			issues = append(issues, issueEvidence{Kind: "contention_missing", Message: "provider-native contention evidence was not emitted"})
		} else if contention.Mode != "server_multiwriter" || contention.Processes != 8 || contention.Attempted != 1000 || contention.Terminal != 1000 || contention.Passed != 1000 || contention.Collisions != 0 || contention.Duplicates != 0 || contention.DurationMS <= 0 {
			issues = append(issues, issueEvidence{Kind: "contention_invalid", Message: "provider-native contention evidence failed its exact census"})
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i].Operation < latencies[j].Operation })
	if scanErr != nil {
		return census, latencies, contention, issues, scanErr
	}
	if waitErr != nil {
		return census, latencies, contention, issues, errors.New("real-provider test process failed; inspect redacted test output locally")
	}
	return census, latencies, contention, issues, nil
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
	payload, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if containsSensitiveEvidence(payload) {
		return errors.New("evidence contains a prohibited body, transcript, environment, or credential field")
	}
	return os.WriteFile(path, payload, 0o600)
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
