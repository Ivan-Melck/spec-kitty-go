package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/beads"
)

type dishonestScenario struct {
	SchemaVersion int               `json:"schema_version"`
	InitialState  map[string]string `json:"initial_state,omitempty"`
	Steps         []dishonestStep   `json:"steps"`
}

type dishonestStep struct {
	ID               string            `json:"id"`
	Argv             []string          `json:"argv"`
	RequiredEnv      map[string]string `json:"required_env"`
	Workspace        string            `json:"workspace"`
	Precondition     map[string]string `json:"precondition,omitempty"`
	TransitionTiming string            `json:"transition_timing,omitempty"`
	Transition       map[string]string `json:"transition,omitempty"`
	Stdout           string            `json:"stdout,omitempty"`
	Stderr           string            `json:"stderr,omitempty"`
	ExitCode         int               `json:"exit_code,omitempty"`
	WaitFor          string            `json:"wait_for,omitempty"`
	NextObservation  map[string]string `json:"next_observation,omitempty"`
}

type dishonestRuntime struct {
	SchemaVersion int               `json:"schema_version"`
	Position      int               `json:"position"`
	State         map[string]string `json:"state"`
	Invocations   []struct {
		StepID string   `json:"step_id"`
		Argv   []string `json:"argv"`
	} `json:"invocations"`
}

type dishonestFixture struct {
	root       string
	executable string
	adapter    *beads.BoundAdapter
	reference  tracker.ExternalWorkRef
}

func buildDishonestExecutable(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	executable := filepath.Join(root, "dishonest-bd")
	source := filepath.Join(repositoryRoot(t), "internal", "tracker", "beads", "testdata", "dishonest_bd.go")
	command := exec.Command("go", "build", "-trimpath", "-o", executable, source)
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build dishonest provider: %v\n%s", err, output)
	}
	return executable
}

func newDishonestFixture(t *testing.T, executable string, steps []dishonestStep, initial map[string]string) dishonestFixture {
	t.Helper()
	return newDishonestFixtureAt(t, t.TempDir(), executable, steps, initial)
}

func newDishonestFixtureAt(t *testing.T, root, executable string, steps []dishonestStep, initial map[string]string) dishonestFixture {
	t.Helper()
	databasePath := filepath.Join(root, "embeddeddolt")
	if err := os.MkdirAll(databasePath, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := range steps {
		steps[index].Workspace = root
		if steps[index].RequiredEnv == nil {
			steps[index].RequiredEnv = safeDishonestEnvironment(root)
		}
	}
	writeDishonestScenario(t, root, dishonestScenario{SchemaVersion: 1, InitialState: initial, Steps: steps})
	adapter, err := beads.Open(beads.BindingConfig{
		ProjectID: "project-fault", ExecutablePath: executable, WorkspacePath: root,
		ProviderDatabaseIdentity: databasePath, ProtocolSchema: 1,
		ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized,
	})
	if err != nil {
		t.Fatal(err)
	}
	return dishonestFixture{root: root, executable: executable, adapter: adapter, reference: tracker.ExternalWorkRef{
		ProjectID: "project-fault", WorkID: "work-fault", Provider: "beads", ExternalID: "bd-fault",
		MetadataSchema: 1, CreatedByOperation: "operation-create-fault",
	}}
}

func safeDishonestEnvironment(root string) map[string]string {
	return map[string]string{"BEADS_DIR": root, "BD_JSON_ENVELOPE": "1", "BD_NON_INTERACTIVE": "1"}
}

func writeDishonestScenario(t *testing.T, root string, scenario dishonestScenario) {
	t.Helper()
	encoded, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenario.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readDishonestRuntime(t *testing.T, root string) dishonestRuntime {
	t.Helper()
	var state dishonestRuntime
	encoded, err := os.ReadFile(filepath.Join(root, "scenario.state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertDishonestConsumed(t *testing.T, root string, expected int) dishonestRuntime {
	t.Helper()
	state := readDishonestRuntime(t, root)
	if state.Position != expected || len(state.Invocations) != expected {
		t.Fatalf("script consumption position=%d invocations=%d expected=%d", state.Position, len(state.Invocations), expected)
	}
	seen := map[string]struct{}{}
	for _, call := range state.Invocations {
		if call.StepID == "" {
			t.Fatal("invocation has no stable step id")
		}
		if _, duplicate := seen[call.StepID]; duplicate {
			t.Fatalf("step consumed twice: %s", call.StepID)
		}
		seen[call.StepID] = struct{}{}
	}
	return state
}

func envelope(data string) string { return `{"schema_version":1,"data":` + data + `}` }

func issueJSON(assignee, marker, project, work, revision string) string {
	return namedIssueJSON("bd-fault", "fault fixture", assignee, marker, project, work, "operation-create-fault", revision)
}

func namedIssueJSON(externalID, title, assignee, marker, project, work, createdOperation, revision string) string {
	metadata := map[string]any{"spec_kitty": map[string]any{
		"schema_version": 1, "project_id": project, "work_id": work,
		"created_by_operation": createdOperation, "last_operation": createdOperation,
		"criteria": []any{}, "assignment_marker": marker,
	}}
	if marker == "" {
		delete(metadata["spec_kitty"].(map[string]any), "assignment_marker")
	}
	value := map[string]any{"id": externalID, "title": title, "description": "", "acceptance_criteria": "", "metadata": metadata, "assignee": assignee, "updated_at": "2026-08-26T12:00:00Z", "revision": revision}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func issueJSONWithMetadata(externalID, title, assignee, metadataJSON, revision string) string {
	var metadata map[string]any
	_ = json.Unmarshal([]byte(metadataJSON), &metadata)
	value := map[string]any{"id": externalID, "title": title, "description": "", "acceptance_criteria": "", "metadata": metadata, "assignee": assignee, "updated_at": "2026-08-26T12:00:00Z", "revision": revision}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func issueEnvelope(assignee, marker, project, work, revision string) string {
	return envelope("[" + issueJSON(assignee, marker, project, work, revision) + "]")
}

func showStep(id, stdout, stderr string, exit int) dishonestStep {
	return dishonestStep{ID: id, Argv: []string{"show", "bd-fault", "--json"}, Stdout: stdout, Stderr: stderr, ExitCode: exit}
}

func preflightSteps(root string) []dishonestStep {
	databasePath := filepath.Join(root, "embeddeddolt")
	return []dishonestStep{
		{ID: "preflight.version", Argv: []string{"version", "--json"}, Stdout: envelope(`{"version":"1.2.2","build":"dishonest-test"}`)},
		{ID: "preflight.where", Argv: []string{"where", "--json"}, Stdout: envelope(fmt.Sprintf(`{"path":%q,"prefix":"db-fault","database_path":%q}`, root, databasePath))},
		{ID: "preflight.dolt-show", Argv: []string{"dolt", "show", "--json"}, Stdout: envelope(fmt.Sprintf(`{"backend":"dolt","data_dir":%q,"database":"db-fault","embedded":true}`, databasePath))},
		{ID: "preflight.dolt-status", Argv: []string{"dolt", "status", "--json"}, Stdout: envelope(fmt.Sprintf(`{"data_dir":%q,"data_dir_exists":true,"mode":"embedded","server_running":false}`, databasePath))},
	}
}

func expectTrackerFault(t *testing.T, err error, kind tracker.ErrorKind, recovery string) *tracker.Error {
	t.Helper()
	var typed *tracker.Error
	if !errors.As(err, &typed) || typed.Kind != kind || typed.RecoveryAction() != recovery {
		t.Fatalf("diagnostic=%v want kind=%s recovery=%s", err, kind, recovery)
	}
	encoded := fmt.Sprintf("%v", err)
	for _, secret := range []string{"secret-provider-body", "arbitrary warning prose", "human-direct-assignment"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("diagnostic leaked provider prose: %s", encoded)
		}
	}
	return typed
}

type faultCase struct {
	ID       string
	Family   string
	Stdout   string
	Stderr   string
	ExitCode int
	Kind     tracker.ErrorKind
	Recovery string
}

func protocolFaultRegistry() []faultCase {
	large := strings.Repeat("x", (4<<20)+1024)
	cases := []faultCase{
		{ID: "malformed.empty", Family: "malformed", Kind: tracker.ErrorMalformedProtocol, Recovery: "repair_provider_binding"},
		{ID: "malformed.truncated", Family: "malformed", Stdout: `{"schema_version":1,"data":`, Kind: tracker.ErrorMalformedProtocol, Recovery: "repair_provider_binding"},
		{ID: "malformed.oversized", Family: "malformed", Stdout: large, Kind: tracker.ErrorMalformedProtocol, Recovery: "repair_provider_binding"},
		{ID: "malformed.multiple", Family: "malformed", Stdout: issueEnvelope("", "", "project-fault", "work-fault", "r1") + `{}`, Kind: tracker.ErrorMalformedProtocol, Recovery: "repair_provider_binding"},
		{ID: "malformed.future-schema", Family: "malformed", Stdout: `{"schema_version":2,"data":{}}`, Kind: tracker.ErrorSchemaSkew, Recovery: "install_supported_provider"},
		{ID: "malformed.wrong-shape", Family: "malformed", Stdout: envelope(`[]`), Kind: tracker.ErrorMalformedProtocol, Recovery: "reconcile_provider_metadata"},
		{ID: "unavailable.documented", Family: "unavailable", Stdout: `{"schema_version":1,"error":"secret-provider-body","code":"database_locked"}`, Stderr: "arbitrary warning prose\n", ExitCode: 7, Kind: tracker.ErrorLockContended, Recovery: "repair_provider_state"},
		{ID: "unavailable.nondocumented", Family: "unavailable", Stderr: "secret-provider-body", ExitCode: 7, Kind: tracker.ErrorMalformedProtocol, Recovery: "repair_provider_binding"},
		{ID: "wrong.foreign-mapping", Family: "wrong", Stdout: issueEnvelope("", "", "project-foreign", "work-fault", "r1"), Kind: tracker.ErrorRevisionConflict, Recovery: "reconcile_mapping"},
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases
}

func exactFaultRegistryIDs() []string {
	ids := []string{
		"apply-timeout.assignment", "apply-timeout.catalog-update", "apply-timeout.comment", "apply-timeout.create", "apply-timeout.dependency",
		"malformed.empty", "malformed.future-schema", "malformed.multiple-values", "malformed.oversized", "malformed.truncated", "malformed.warning-noise", "malformed.wrong-payload-shape",
		"slow.deadline-before-apply", "slow.ignored-cancellation-process-kill", "slow.success-below-deadline",
		"stale.assignment-marker", "stale.mapping", "stale.old-revision-tied-timestamp",
		"unavailable.documented-json-error", "unavailable.spawn-failure", "unavailable.workspace-lock",
		"wrong.changed-operation-marker", "wrong.duplicate-match", "wrong.foreign-project-work-mapping", "wrong.unexpected-assignee",
	}
	sort.Strings(ids)
	return ids
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func staleFaultAuthority(t *testing.T, fixture workspaceScenario, stale phase1StaleAuthority, fault string) universalAssertionState {
	t.Helper()
	state := phase1UniversalState(t, fixture, fault, fixture.primary.Binding().Primary, stale.expected, []string{stale.candidate})
	state.StaleProbe = stale.probe(t, fixture, strings.ToLower(strings.ReplaceAll(fault, ".", "-")))
	state.RequireStaleProbe = true
	return state
}

// quickStaleFaultAuthority still invokes a real public handoff operation, but
// avoids the additional review and integration probes used by the broader
// Phase 1 helper. The hundred-repetition campaign needs one stale-authority
// refusal per repetition, not three redundant refusals per repetition.
func quickStaleFaultAuthority(t *testing.T, fixture workspaceScenario, stale phase1StaleAuthority, fault string) universalAssertionState {
	t.Helper()
	state := phase1UniversalState(t, fixture, fault, fixture.primary.Binding().Primary, stale.expected, []string{stale.candidate})
	state.RequireStaleProbe = true
	state.StaleProbe = func(context.Context) error {
		label := strings.ToLower(strings.ReplaceAll(fault, ".", "-"))
		response := runHandoffHelper(t, fixture, &stale.capsule, stale.candidate, "handoff-stale-wp08-"+label, "operation-stale-wp08-"+label, "")
		if response.OK || response.Error == nil {
			return fmt.Errorf("stale handoff unexpectedly succeeded")
		}
		return response.Error
	}
	return state
}

func stableID(prefix string, repetition int) string {
	return fmt.Sprintf("%s-%03d", prefix, repetition)
}
func humanOperation(repetition int) ids.OperationID {
	return ids.OperationID(stableID("operation-human-preservation", repetition))
}

func boundedContext(t *testing.T, duration time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), duration)
}
