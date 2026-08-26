package testcampaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const BeadsEvidenceSchemaVersion = 1

type BeadsHostEvidence struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	Runner string `json:"runner,omitempty"`
}

type BeadsScenarioCensus struct {
	Expected int `json:"expected"`
	Started  int `json:"started"`
	Terminal int `json:"terminal"`
	Passed   int `json:"passed"`
	Failed   int `json:"failed"`
}

type BeadsLatencyEvidence struct {
	Operation string  `json:"operation"`
	Samples   int     `json:"samples"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	MaxMS     float64 `json:"max_ms"`
}

type BeadsContentionEvidence struct {
	Mode       string  `json:"mode"`
	Processes  int     `json:"processes"`
	Attempted  int     `json:"attempted"`
	Terminal   int     `json:"terminal"`
	Passed     int     `json:"passed"`
	Collisions int     `json:"collisions"`
	Duplicates int     `json:"duplicates"`
	DurationMS float64 `json:"duration_ms"`
}

type BeadsAssignmentEvidence struct {
	Attempted                   int `json:"attempted"`
	Terminal                    int `json:"terminal"`
	Preserved                   int `json:"preserved"`
	Failed                      int `json:"failed"`
	AttributionLost             int `json:"attribution_lost"`
	SafeCompensationUnsupported int `json:"safe_compensation_unsupported"`
}

type BeadsIssueEvidence struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type BeadsSourceBinding struct {
	Commit string
	Tree   string
	Dirty  bool
}

// BeadsEvidenceProof carries validation inputs that are required to justify a
// verdict but are deliberately excluded from the public schema-1 document.
type BeadsEvidenceProof struct {
	ExpectedIDs         []string
	StartedIDs          []string
	TerminalIDs         []string
	PassedIDs           []string
	FailedIDs           []string
	SkippedIDs          []string
	SourceBindings      []BeadsSourceBinding
	ProviderVerified    bool
	OfflineVerified     bool
	SourceClean         bool
	AssertionFailed     bool
	PrerequisiteMissing bool
}

// BeadsQualificationEvidence is the permanent schema-1 adoption record. Proof
// is local validation context and never expands the external JSON contract.
type BeadsQualificationEvidence struct {
	SchemaVersion              int                      `json:"schema_version"`
	SourceCommit               string                   `json:"source_commit"`
	SourceTree                 string                   `json:"source_tree"`
	ProviderVersion            string                   `json:"provider_version"`
	ProviderDigestSHA256       string                   `json:"provider_digest_sha256"`
	ProtocolSchema             int                      `json:"protocol_schema"`
	StorageMode                string                   `json:"storage_mode"`
	Host                       BeadsHostEvidence        `json:"host"`
	StartedAt                  time.Time                `json:"started_at"`
	FinishedAt                 time.Time                `json:"finished_at"`
	Scenarios                  BeadsScenarioCensus      `json:"scenarios"`
	Latency                    []BeadsLatencyEvidence   `json:"latency,omitempty"`
	Contention                 *BeadsContentionEvidence `json:"contention,omitempty"`
	AssignmentPreservation     *BeadsAssignmentEvidence `json:"assignment_preservation,omitempty"`
	AuthorityAudits            int                      `json:"authority_audits"`
	AuthorityInvariantFailures int                      `json:"authority_invariant_failures"`
	Issues                     []BeadsIssueEvidence     `json:"issues,omitempty"`
	Verdict                    string                   `json:"verdict"`
	Proof                      BeadsEvidenceProof       `json:"-"`
}

func (e BeadsQualificationEvidence) Validate() error {
	if e.SchemaVersion != BeadsEvidenceSchemaVersion || e.ProviderVersion != "1.2.2" || e.ProtocolSchema != 1 {
		return errors.New("beads evidence compatibility tuple must be schema 1, provider 1.2.2, protocol 1")
	}
	if !lowerHex(e.SourceCommit, 40) || !lowerHex(e.SourceTree, 40) {
		return errors.New("beads evidence requires lowercase 40-character source commit and tree")
	}
	if !lowerHex(e.ProviderDigestSHA256, 64) {
		return errors.New("beads evidence requires a lowercase 64-character provider digest")
	}
	if e.StorageMode != "embedded_serialized" && e.StorageMode != "server_multiwriter" {
		return fmt.Errorf("beads evidence storage mode %q is not qualified", e.StorageMode)
	}
	if !boundedSafeText(e.Host.GOOS, 1, 64) || !boundedSafeText(e.Host.GOARCH, 1, 64) || (e.Host.Runner != "" && !boundedSafeText(e.Host.Runner, 1, 64)) {
		return errors.New("beads evidence host identity is invalid")
	}
	if e.StartedAt.IsZero() || e.FinishedAt.IsZero() || e.StartedAt.Location() != time.UTC || e.FinishedAt.Location() != time.UTC || e.FinishedAt.Before(e.StartedAt) {
		return errors.New("beads evidence requires ordered UTC timestamps")
	}
	if err := validateBeadsScenarioCensus(e.Scenarios, e.Proof); err != nil {
		return err
	}
	if err := validateBeadsLatency(e.Latency); err != nil {
		return err
	}
	if err := validateBeadsOptionalCensuses(e.Contention, e.AssignmentPreservation, e.AuthorityAudits, e.AuthorityInvariantFailures); err != nil {
		return err
	}
	if len(e.Issues) > 128 {
		return errors.New("beads evidence issue census exceeds 128")
	}
	for _, issue := range e.Issues {
		if !boundedSafeText(issue.Kind, 1, 128) || !boundedSafeText(issue.Message, 1, 1024) || unsafeEvidenceText(issue.Kind+" "+issue.Message) {
			return fmt.Errorf("beads evidence issue %q is unsafe", issue.Kind)
		}
	}
	for _, binding := range e.Proof.SourceBindings {
		if binding.Commit != e.SourceCommit || binding.Tree != e.SourceTree || binding.Dirty {
			return errors.New("beads evidence contains mixed or dirty source binding")
		}
	}
	return e.validateVerdict()
}

func (e BeadsQualificationEvidence) validateVerdict() error {
	complete := e.Scenarios.Started == e.Scenarios.Expected && e.Scenarios.Terminal == e.Scenarios.Expected && e.Scenarios.Passed == e.Scenarios.Expected && e.Scenarios.Failed == 0
	switch e.Verdict {
	case "pass":
		if !complete || len(e.Proof.SkippedIDs) != 0 || len(e.Proof.FailedIDs) != 0 || e.AuthorityInvariantFailures != 0 || len(e.Issues) != 0 {
			return errors.New("pass requires the complete successful scenario census with zero skips, failures, invariant failures, or issues")
		}
		if !e.Proof.ProviderVerified || !e.Proof.OfflineVerified || !e.Proof.SourceClean || len(e.Proof.SourceBindings) == 0 {
			return errors.New("pass requires verified provider, offline boundary, and clean source binding")
		}
		if e.StorageMode != "server_multiwriter" || !exactPassingLatency(e.Latency) || !exactPassingContention(e.Contention) || !exactPassingAssignment(e.AssignmentPreservation) || e.AuthorityAudits < 1 {
			return errors.New("pass requires exact server, latency, contention, assignment, and authority evidence")
		}
	case "fail":
		if !e.Proof.AssertionFailed && e.Scenarios.Failed == 0 && e.AuthorityInvariantFailures == 0 {
			return errors.New("fail requires an observed assertion, scenario, or authority failure")
		}
	case "non_admissible":
		if e.Proof.AssertionFailed || e.Scenarios.Failed != 0 || e.AuthorityInvariantFailures != 0 {
			return errors.New("an observed assertion, scenario, or authority failure requires verdict fail")
		}
		inadmissible := e.Proof.PrerequisiteMissing || !e.Proof.ProviderVerified || !e.Proof.OfflineVerified || !e.Proof.SourceClean || !complete || len(e.Proof.SkippedIDs) != 0
		if !inadmissible {
			return errors.New("non_admissible requires a missing prerequisite or incomplete qualification proof")
		}
	default:
		return fmt.Errorf("beads evidence verdict %q is invalid", e.Verdict)
	}
	return nil
}

func validateBeadsScenarioCensus(c BeadsScenarioCensus, proof BeadsEvidenceProof) error {
	if c.Expected != 11 || c.Started < 0 || c.Terminal < 0 || c.Passed < 0 || c.Failed < 0 || c.Started > c.Expected || c.Terminal > c.Started || c.Passed+c.Failed > c.Terminal {
		return errors.New("beads evidence scenario arithmetic is incoherent")
	}
	sets := []struct {
		name   string
		values []string
		count  int
	}{
		{"expected", proof.ExpectedIDs, c.Expected}, {"started", proof.StartedIDs, c.Started}, {"terminal", proof.TerminalIDs, c.Terminal},
		{"passed", proof.PassedIDs, c.Passed}, {"failed", proof.FailedIDs, c.Failed},
	}
	for _, set := range sets {
		if len(set.values) != set.count || !stableUniqueIDs(set.values) {
			return fmt.Errorf("beads evidence %s scenario IDs do not match aggregate census", set.name)
		}
	}
	if !subsetOf(proof.StartedIDs, proof.ExpectedIDs) || !subsetOf(proof.TerminalIDs, proof.StartedIDs) || !subsetOf(proof.PassedIDs, proof.TerminalIDs) || !subsetOf(proof.FailedIDs, proof.TerminalIDs) || !subsetOf(proof.SkippedIDs, proof.TerminalIDs) {
		return errors.New("beads evidence scenario ID sets are not nested")
	}
	dispositions := append(append(append([]string(nil), proof.PassedIDs...), proof.FailedIDs...), proof.SkippedIDs...)
	if !sameSet(dispositions, proof.TerminalIDs) {
		return errors.New("beads evidence terminal scenario IDs lack one exact disposition")
	}
	return nil
}

func validateBeadsLatency(values []BeadsLatencyEvidence) error {
	if len(values) > 2 {
		return errors.New("beads evidence latency observation count exceeds 2")
	}
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !boundedSafeText(value.Operation, 1, 64) || seen[value.Operation] || value.Samples < 1 || value.Samples > 1_000_000 || !orderedFinite(value.P50MS, value.P95MS, value.MaxMS) || value.MaxMS > 1_800_000 {
			return fmt.Errorf("beads evidence latency operation %q is invalid or duplicated", value.Operation)
		}
		seen[value.Operation] = true
	}
	return nil
}

func validateBeadsOptionalCensuses(contention *BeadsContentionEvidence, assignment *BeadsAssignmentEvidence, audits, failures int) error {
	if audits < 0 || audits > 1_000_000 || failures < 0 || failures > 1_000_000 || failures > audits {
		return errors.New("beads evidence authority census is outside its bounded coherent contract")
	}
	if contention != nil {
		if !boundedSafeText(contention.Mode, 1, 64) || contention.Processes < 0 || contention.Processes > 1024 ||
			contention.Attempted < 0 || contention.Attempted > 10_000_000 || contention.Terminal < 0 || contention.Terminal > contention.Attempted ||
			contention.Passed < 0 || contention.Passed > contention.Terminal || contention.Collisions < 0 || contention.Collisions > contention.Attempted ||
			contention.Duplicates < 0 || contention.Duplicates > contention.Attempted || !finiteNonnegative(contention.DurationMS) || contention.DurationMS > 1_800_000 {
			return errors.New("beads evidence contention census is invalid")
		}
	}
	if assignment != nil {
		for _, count := range []int{assignment.Attempted, assignment.Terminal, assignment.Preserved, assignment.Failed, assignment.AttributionLost, assignment.SafeCompensationUnsupported} {
			if count < 0 || count > 1_000_000 {
				return errors.New("beads evidence assignment census is outside its bounded contract")
			}
		}
		if assignment.Terminal > assignment.Attempted || assignment.Preserved+assignment.Failed > assignment.Terminal || assignment.AttributionLost+assignment.SafeCompensationUnsupported > assignment.Attempted {
			return errors.New("beads evidence assignment census is incoherent")
		}
	}
	return nil
}

func EncodeBeadsEvidence(e BeadsQualificationEvidence) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	e.Latency = append([]BeadsLatencyEvidence(nil), e.Latency...)
	e.Issues = append([]BeadsIssueEvidence(nil), e.Issues...)
	slices.SortFunc(e.Latency, func(a, b BeadsLatencyEvidence) int { return strings.Compare(a.Operation, b.Operation) })
	slices.SortFunc(e.Issues, func(a, b BeadsIssueEvidence) int {
		if comparison := strings.Compare(a.Kind, b.Kind); comparison != 0 {
			return comparison
		}
		return strings.Compare(a.Message, b.Message)
	})
	payload, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	if containsProhibitedEvidence(string(payload)) {
		return nil, errors.New("beads evidence serialization contains prohibited human or secret material")
	}
	return payload, nil
}

func WriteBeadsEvidence(path string, e BeadsQualificationEvidence) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("beads evidence path must be absolute and clean")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return errors.New("beads evidence parent must exist")
	}
	payload, err := EncodeBeadsEvidence(e)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".beads-evidence-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(parent, filepath.Base(path))); err != nil {
		return err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func exactPassingLatency(values []BeadsLatencyEvidence) bool {
	if len(values) != 2 {
		return false
	}
	byName := make(map[string]BeadsLatencyEvidence, 2)
	for _, value := range values {
		byName[value.Operation] = value
	}
	preflight, preflightOK := byName["preflight"]
	warm, warmOK := byName["warm_single_item_read"]
	return preflightOK && warmOK && preflight.Samples == 100 && preflight.P95MS <= 1000 && warm.Samples == 200 && warm.P95MS <= 500
}

func exactPassingContention(value *BeadsContentionEvidence) bool {
	return value != nil && value.Mode == "server_multiwriter" && value.Processes == 8 && value.Attempted == 1000 && value.Terminal == 1000 && value.Passed == 1000 && value.Collisions == 0 && value.Duplicates == 0 && value.DurationMS > 0
}

func exactPassingAssignment(value *BeadsAssignmentEvidence) bool {
	return value != nil && value.Attempted == 20 && value.Terminal == 20 && value.Preserved == 20 && value.Failed == 0 && value.AttributionLost == 10 && value.SafeCompensationUnsupported == 10
}

func lowerHex(value string, length int) bool {
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

func boundedSafeText(value string, minimum, maximum int) bool {
	length := len([]rune(value))
	return length >= minimum && length <= maximum && !strings.ContainsAny(value, "\r\n")
}

func unsafeEvidenceText(value string) bool {
	return containsProhibitedEvidence(value) || strings.ContainsAny(value, "\r\n")
}

func containsProhibitedEvidence(value string) bool {
	lower := strings.ToLower(value)
	for _, prohibited := range []string{"raw_stdout", "raw_stderr", "stdout", "stderr", "environment", "credential", "password", "secret", "token=", "ghp_", "sk_live", "comment_body", "description_body", "acceptance text", "arbitrary metadata"} {
		if strings.Contains(lower, prohibited) {
			return true
		}
	}
	return false
}

func orderedFinite(p50, p95, maximum float64) bool {
	return finiteNonnegative(p50) && finiteNonnegative(p95) && finiteNonnegative(maximum) && p50 <= p95 && p95 <= maximum
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func stableUniqueIDs(values []string) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for index, value := range values {
		if !boundedSafeText(value, 1, 256) || (index > 0 && values[index-1] == value) {
			return false
		}
	}
	return true
}

func subsetOf(values, superset []string) bool {
	for _, value := range values {
		if !slices.Contains(superset, value) {
			return false
		}
	}
	return true
}

func sameSet(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	return slices.Equal(left, right) && stableUniqueIDs(left)
}
