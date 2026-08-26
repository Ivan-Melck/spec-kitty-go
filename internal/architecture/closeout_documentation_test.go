package architecture

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestCloseoutBinaryAndReviewPolicyDocumentation(t *testing.T) {
	root := closeoutRepositoryRoot(t)
	ignore := closeoutRead(t, root, ".gitignore")
	if !slices.Contains(strings.Split(ignore, "\n"), "bin/") {
		t.Fatal(".gitignore must contain an exact bin/ entry")
	}

	historical := closeoutRead(t, root, "kitty-specs/phase1-two-agent-authority-proving-ground-01M0Q4A8/spec.md")
	if !strings.Contains(closeoutFlat(historical), "absent an explicit visible waiver") {
		t.Fatal("immutable Phase 1 specification no longer preserves the clause corrected by the erratum")
	}

	living := map[string]string{
		"README.md":                         closeoutRead(t, root, "README.md"),
		"docs/TECHNICAL_PROJECT_PLAN.md":    closeoutRead(t, root, "docs/TECHNICAL_PROJECT_PLAN.md"),
		"docs/CONCURRENCY_SCENARIOS.md":     closeoutRead(t, root, "docs/CONCURRENCY_SCENARIOS.md"),
		"docs/MINI_REVIEW_RESOLUTION.md":    closeoutRead(t, root, "docs/MINI_REVIEW_RESOLUTION.md"),
		"docs/PYTHON_BEHAVIOR_INVENTORY.md": closeoutRead(t, root, "docs/PYTHON_BEHAVIOR_INVENTORY.md"),
		"docs/PHASE1_ACCEPTANCE_ERRATA.md":  closeoutRead(t, root, "docs/PHASE1_ACCEPTANCE_ERRATA.md"),
	}
	required := map[string][]string{
		"README.md":                         {"Phase 1 has no waiver", "PHASE1_ACCEPTANCE_ERRATA.md"},
		"docs/TECHNICAL_PROJECT_PLAN.md":    {"Phase 1 has no waiver or bypass", "Phase 1 has no same-actor waiver"},
		"docs/CONCURRENCY_SCENARIOS.md":     {"Phase 1 defines no waiver or bypass"},
		"docs/MINI_REVIEW_RESOLUTION.md":    {"Phase 1 requires the review ActorID", "implements no waiver or bypass"},
		"docs/PYTHON_BEHAVIOR_INVENTORY.md": {"Phase 1 implements no review-independence waiver", "Phase 1 review independence fails closed and has no waiver mechanism"},
		"docs/PHASE1_ACCEPTANCE_ERRATA.md":  {"Review independence is unconditional in this release", "future waiver capability would be a new governed feature"},
	}
	for path, fragments := range required {
		flat := closeoutFlat(living[path])
		for _, fragment := range fragments {
			if !strings.Contains(flat, fragment) {
				t.Errorf("%s lacks required Phase 1 closeout statement %q", path, fragment)
			}
		}
	}

	for path, content := range living {
		flat := closeoutFlat(content)
		for _, stale := range []string{
			"Reviewer actor differs from implementer actor unless policy explicitly permits otherwise",
			"A mission policy may explicitly permit same-actor review",
			"A mission policy may create a visible waiver",
			"Explicit same-actor waivers are visible",
			"unless an explicit visible mission waiver applies",
			"waiver policy where allowed",
			"open/passed/waived results",
		} {
			if strings.Contains(flat, stale) {
				t.Errorf("%s retains stale waiver promise %q", path, stale)
			}
		}
	}
}

func TestCloseoutOrchestratorAPI13ProjectionContract(t *testing.T) {
	document := closeoutRead(t, closeoutRepositoryRoot(t), "docs/ORCHESTRATOR_API_1_3_PROJECTION.md")

	wantEnvelope := []string{
		"contract_version", "command", "timestamp", "correlation_id", "success", "error_code", "data",
	}
	gotEnvelope := closeoutTableFirstColumn(document, "## Envelope and parsing contract", "Contract 1.3 adds")
	if !slices.Equal(gotEnvelope, wantEnvelope) {
		t.Fatalf("API envelope fields=%v want=%v", gotEnvelope, wantEnvelope)
	}

	wantCommands := map[string]string{
		"contract-version":     "Direct",
		"mission-state":        "Deferred",
		"list-ready":           "Translated",
		"resolve-workspace":    "Translated",
		"start-implementation": "Translated",
		"start-review":         "Partial / deferred",
		"transition":           "Partial",
		"append-history":       "Deferred",
		"accept-mission":       "Deferred",
		"merge-mission":        "Partial",
	}
	gotCommands := closeoutCommandClasses(t, document)
	if len(gotCommands) != len(wantCommands) {
		t.Fatalf("projection command count=%d want=%d: %v", len(gotCommands), len(wantCommands), gotCommands)
	}
	for command, class := range wantCommands {
		if gotCommands[command] != class {
			t.Errorf("projection class for %s=%q want=%q", command, gotCommands[command], class)
		}
	}

	flat := closeoutFlat(document)
	for _, required := range []string{
		"Return exactly `api_version` and `min_supported_provider_version`",
		"Runtime version and required/supported actions are consumer-local negotiation inputs",
		"Phase 1 has no review claim/start lifecycle",
		"review claim/start lifecycle",
		"structured `--review-result-json`",
		"orchestrator_id", "orchestrator_version", "agent_family", "approval_mode",
		"sandbox_mode", "network_mode", "dangerous_flags", "tool_restrictions",
		"open SQLite or a future Gapdb control store directly",
		"choose, transfer, or reactivate a workspace",
		"mutate branches, evidence refs, or target refs outside the Git adapter",
	} {
		if !strings.Contains(flat, required) {
			t.Errorf("API projection lacks required fact %q", required)
		}
	}
	if strings.Contains(flat, "Return 1.3 compatibility, runtime version, and supported actions") {
		t.Error("contract-version still claims consumer-local runtime/action facts are returned on the wire")
	}
}

func closeoutRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve closeout test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func closeoutRead(t *testing.T, root, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func closeoutFlat(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func closeoutTableFirstColumn(document, start, end string) []string {
	section := document
	if index := strings.Index(section, start); index >= 0 {
		section = section[index+len(start):]
	}
	if index := strings.Index(section, end); index >= 0 {
		section = section[:index]
	}
	var values []string
	for _, line := range strings.Split(section, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 4 {
			continue
		}
		value := strings.Trim(strings.TrimSpace(cells[1]), "`")
		if value == "" || value == "Field" || strings.HasPrefix(value, "---") {
			continue
		}
		values = append(values, value)
	}
	return values
}

func closeoutCommandClasses(t *testing.T, document string) map[string]string {
	t.Helper()
	sectionStart := strings.Index(document, "## Command projection matrix")
	sectionEnd := strings.Index(document, "## Generation-scoped workspace translation")
	if sectionStart < 0 || sectionEnd <= sectionStart {
		t.Fatal("command projection matrix boundaries are missing")
	}
	classes := make(map[string]string)
	for _, line := range strings.Split(document[sectionStart:sectionEnd], "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 6 {
			continue
		}
		command := strings.Trim(strings.TrimSpace(cells[1]), "`")
		if command == "" || command == "API 1.3 command" || strings.HasPrefix(command, "---") {
			continue
		}
		if _, exists := classes[command]; exists {
			t.Fatalf("duplicate command projection for %s", command)
		}
		classes[command] = strings.TrimSpace(cells[3])
	}
	return classes
}
