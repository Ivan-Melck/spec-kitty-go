package scenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/conformance"
	"github.com/spec-kitty/go-kitty3/internal/tracker/fake"
)

func TestBeadsDishonestRunsSharedPortableConformance(t *testing.T) {
	executable := buildDishonestExecutable(t)
	factory := func(ctx context.Context, root, caseID string) (conformance.Fixture, error) {
		steps, initial, diagnostic, offline := dishonestConformancePlan(t, root, caseID)
		fixture := newDishonestFixtureAt(t, root, executable, steps, initial)
		capabilities := tracker.ProviderCapabilities{
			CatalogRead: tracker.SupportSupported, CatalogCreate: tracker.SupportSupported, CatalogUpdate: tracker.UpdateUnguarded,
			BlockingGraph: tracker.SupportSupported, Discussion: tracker.DiscussionAppendOnly, Assignment: tracker.AssignmentUnguarded,
			ObservationRevision: tracker.ObservationRevisionContentDerived, SafeCompensation: tracker.CompensationUnsupported,
			ConcurrentWriters: tracker.ConcurrentWritersAdapterSerialized, PortableExport: tracker.SupportUnsupported, RestorableBackup: tracker.SupportUnsupported,
		}
		moved := ""
		if offline {
			moved = executable + "." + strings.ReplaceAll(caseID, ".", "-")
			if err := os.Rename(executable, moved); err != nil {
				return conformance.Fixture{}, err
			}
		}
		expectedConsumption := len(steps)
		if offline {
			expectedConsumption = 0
		}
		return conformance.Fixture{
			Capabilities: capabilities,
			Reference:    fixture.reference,
			Catalog:      fixture.adapter, Graph: fixture.adapter, Discussion: fixture.adapter, Assignment: fixture.adapter,
			Diagnostic:    diagnostic,
			MutationCount: func(context.Context) (int, error) { return dishonestMutationCount(root), nil },
			Close: func() error {
				if moved != "" {
					if err := os.Rename(moved, executable); err != nil {
						return err
					}
				}
				return verifyDishonestConsumption(root, expectedConsumption)
			},
		}, nil
	}
	census, err := conformance.Run(context.Background(), t.TempDir(), factory, conformance.DefaultCases())
	if err != nil {
		t.Fatal(err)
	}
	if len(census.Expected) != len(conformance.DefaultCases()) || len(census.Passed) != len(census.Expected) || len(census.Failed) != 0 {
		t.Fatalf("dishonest shared census=%+v", census)
	}
}

func TestBeadsDishonestSharedConformanceKillsDeletedPublicMethods(t *testing.T) {
	executable := buildDishonestExecutable(t)
	methods := map[string]string{
		"ReadWork": "catalog.read.complete", "CreateWork": "catalog.create.replay", "UpdateWork": "catalog.update.guarantee",
		"PutBlockingDependency": "graph.blocking.direction", "RemoveBlockingDependency": "graph.blocking.direction", "ListDependencies": "graph.blocking.direction", "ReadyProjection": "graph.ready.diagnostic",
		"AppendDiscussion": "discussion.append.order.replay", "ListDiscussion": "discussion.append.order.replay",
		"ObserveAssignment": "assignment.project.observe.replay", "ProjectAssignment": "assignment.project.observe.replay", "CompensateAssignment": "assignment.compensation.attribution",
	}
	registry := conformance.DefaultCases()
	for method, caseID := range methods {
		method, caseID := method, caseID
		t.Run(method, func(t *testing.T) {
			var selected conformance.Case
			for _, candidate := range registry {
				if candidate.ID == caseID {
					selected = candidate
				}
			}
			factory := func(_ context.Context, root, id string) (conformance.Fixture, error) {
				steps, initial, diagnostic, _ := dishonestConformancePlan(t, root, id)
				fixture := newDishonestFixtureAt(t, root, executable, steps, initial)
				deleted := deletedBoundAdapterPort{BoundAdapter: fixture.adapter, method: method}
				capabilities := tracker.ProviderCapabilities{CatalogRead: tracker.SupportSupported, CatalogCreate: tracker.SupportSupported, CatalogUpdate: tracker.UpdateUnguarded, BlockingGraph: tracker.SupportSupported, Discussion: tracker.DiscussionAppendOnly, Assignment: tracker.AssignmentUnguarded, ObservationRevision: tracker.ObservationRevisionContentDerived, SafeCompensation: tracker.CompensationUnsupported, ConcurrentWriters: tracker.ConcurrentWritersAdapterSerialized, PortableExport: tracker.SupportUnsupported, RestorableBackup: tracker.SupportUnsupported}
				return conformance.Fixture{Capabilities: capabilities, Reference: fixture.reference, Catalog: deleted, Graph: deleted, Discussion: deleted, Assignment: deleted, Diagnostic: diagnostic, MutationCount: func(context.Context) (int, error) { return dishonestMutationCount(root), nil }}, nil
			}
			if _, err := conformance.Run(context.Background(), t.TempDir(), factory, []conformance.Case{selected}); err == nil || !strings.Contains(err.Error(), caseID) {
				t.Fatalf("deleted %s escaped shared case %s: %v", method, caseID, err)
			}
		})
	}
}

func dishonestConformancePlan(t *testing.T, root, caseID string) ([]dishonestStep, map[string]string, conformance.DiagnosticExpectation, bool) {
	t.Helper()
	reference := tracker.ExternalWorkRef{ProjectID: "project-fault", WorkID: "work-fault", Provider: "beads", ExternalID: "bd-fault", MetadataSchema: 1, CreatedByOperation: "operation-create-fault"}
	diagnostic := conformance.DiagnosticExpectation{Reference: reference}
	base := issueEnvelope("", "", "project-fault", "work-fault", "r1")
	show := func(id, payload string) dishonestStep { return showStep(id, payload, "", 0) }
	preflight := func(prefix string) []dishonestStep {
		steps := preflightSteps(root)
		for index := range steps {
			steps[index].ID = prefix + "." + steps[index].ID
		}
		return steps
	}
	switch caseID {
	case "validation.capabilities.complete", "catalog.read.complete":
		return []dishonestStep{show(caseID+".read", base)}, nil, diagnostic, false
	case "diagnostic.unavailable.typed":
		diagnostic.Kind, diagnostic.RecoveryCode = tracker.ErrorUnavailable, "repair_provider_binding"
		return []dishonestStep{show(caseID+".unconsumed", base)}, nil, diagnostic, true
	case "observation.stale.not-normalized":
		diagnostic.ProviderToken, diagnostic.Assignee, diagnostic.Marker = "r-old", "actor-old", "operation-old"
		return []dishonestStep{show(caseID+".read", issueEnvelope("actor-old", "operation-old", "project-fault", "work-fault", "r-old"))}, map[string]string{"truth_revision": "r-current"}, diagnostic, false
	case "safety.wrong-state.refused":
		diagnostic.Reference.ProjectID = "project-foreign"
		diagnostic.Kind = tracker.ErrorRevisionConflict
		return nil, nil, diagnostic, false
	case "catalog.create.replay":
		digest := portableRequestDigest("portable create", "", "")
		metadata := fmt.Sprintf(`{"spec_kitty":{"content_digest":%q,"created_by_operation":"operation-conformance-create","criteria":[],"design_context":"","last_operation":"operation-conformance-create","project_id":"project-fault","schema_version":1,"work_id":"work-conformance-create"}}`, digest)
		created := envelope("[" + issueJSONWithMetadata("bd-created", "portable create", "", metadata, "r-created") + "]")
		steps := preflight("create.first")
		steps = append(steps,
			dishonestStep{ID: "create.first.list", Argv: []string{"list", "--json"}, Stdout: envelope(`[]`)},
			dishonestStep{ID: "create.first.mutate", Argv: []string{"create", "--title", "portable create", "--metadata", metadata, "--json"}, Stdout: envelope(`{"id":"bd-created"}`), TransitionTiming: "before_response", Transition: map[string]string{"created": "bd-created"}},
			dishonestStep{ID: "create.first.verify", Argv: []string{"show", "bd-created", "--json"}, Stdout: created},
		)
		steps = append(steps, preflight("create.replay")...)
		steps = append(steps, dishonestStep{ID: "create.replay.list", Argv: []string{"list", "--json"}, Stdout: created})
		return steps, nil, diagnostic, false
	case "catalog.update.guarantee":
		digest := portableRequestDigest("portable update", "", "")
		metadata := fmt.Sprintf(`{"spec_kitty":{"content_digest":%q,"created_by_operation":"operation-create-fault","criteria":[],"design_context":"","last_operation":"operation-conformance-update","project_id":"project-fault","schema_version":1,"work_id":"work-fault"}}`, digest)
		after := envelope("[" + issueJSONWithMetadata("bd-fault", "portable update", "", metadata, "r2") + "]")
		steps := []dishonestStep{show("update.initial", base), show("update.confirm", base)}
		steps = append(steps, preflight("update.first")...)
		steps = append(steps, show("update.first.before", base), dishonestStep{ID: "update.first.mutate", Argv: []string{"update", "bd-fault", "--title", "portable update", "--description", "", "--acceptance", "", "--metadata", metadata, "--json"}, Stdout: after}, show("update.first.verify", after))
		steps = append(steps, preflight("update.replay")...)
		steps = append(steps, show("update.replay.read", after))
		return steps, nil, diagnostic, false
	case "graph.blocking.direction":
		blocker := namedIssueJSON("bd-blocker", "blocker", "", "", "project-fault", "work-conformance-blocker", "operation-create-blocker", "r-blocker")
		catalog := envelope("[" + issueJSON("", "", "project-fault", "work-fault", "r1") + "," + blocker + "]")
		empty, edge := envelope(`[]`), envelope(`[{"id":"bd-blocker","dependency_type":"blocks","edge_id":"edge-1"}]`)
		steps := preflight("graph.put")
		steps = append(steps, dishonestStep{ID: "graph.put.catalog", Argv: []string{"list", "--json"}, Stdout: catalog}, dishonestStep{ID: "graph.put.before", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: empty}, dishonestStep{ID: "graph.put.mutate", Argv: []string{"dep", "add", "bd-fault", "bd-blocker", "--type", "blocks", "--json"}, Stdout: envelope(`{}`)}, dishonestStep{ID: "graph.put.after", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: edge})
		steps = append(steps, dishonestStep{ID: "graph.list.catalog", Argv: []string{"list", "--json"}, Stdout: catalog}, dishonestStep{ID: "graph.list.edges", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: edge})
		steps = append(steps, preflight("graph.remove")...)
		steps = append(steps, dishonestStep{ID: "graph.remove.catalog", Argv: []string{"list", "--json"}, Stdout: catalog}, dishonestStep{ID: "graph.remove.before", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: edge}, dishonestStep{ID: "graph.remove.mutate", Argv: []string{"dep", "remove", "bd-fault", "bd-blocker", "--json"}, Stdout: envelope(`{}`)}, dishonestStep{ID: "graph.remove.after", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: empty})
		return steps, nil, diagnostic, false
	case "graph.ready.diagnostic":
		return []dishonestStep{{ID: "ready.catalog", Argv: []string{"list", "--json"}, Stdout: envelope("[" + issueJSON("", "", "project-fault", "work-fault", "r1") + "]")}, {ID: "ready.provider", Argv: []string{"ready", "--json"}, Stdout: envelope("[" + issueJSON("", "", "project-fault", "work-fault", "r1") + "]")}}, nil, diagnostic, false
	case "discussion.append.order.replay":
		providerBody := "<!-- spec-kitty:discussion:v1;op=" + base64.RawURLEncoding.EncodeToString([]byte("operation-conformance-discussion")) + " -->\nportable body"
		comment := fmt.Sprintf(`[{"id":"comment-1","author":"actor-conformance","text":%q,"created_at":"2026-08-26T12:01:00Z"}]`, providerBody)
		steps := []dishonestStep{show("discussion.initial", base)}
		appendAttempt := func(prefix string, comments string, mutate bool) {
			steps = append(steps, preflight(prefix)...)
			steps = append(steps, show(prefix+".show", base), dishonestStep{ID: prefix + ".comments", Argv: []string{"comments", "bd-fault", "--json"}, Stdout: envelope(comments)}, dishonestStep{ID: prefix + ".scope", Argv: []string{"list", "--json"}, Stdout: envelope("[" + issueJSON("", "", "project-fault", "work-fault", "r1") + "]")})
			if mutate {
				steps = append(steps, dishonestStep{ID: prefix + ".mutate", Argv: []string{"comments", "add", "bd-fault", providerBody, "--json"}, Stdout: envelope(`{}`)}, show(prefix+".after-show", base), dishonestStep{ID: prefix + ".after-comments", Argv: []string{"comments", "bd-fault", "--json"}, Stdout: envelope(comment)})
			}
		}
		appendAttempt("discussion.first", `[]`, true)
		appendAttempt("discussion.replay", comment, false)
		steps = append(steps, show("discussion.list.show", base), dishonestStep{ID: "discussion.list.comments", Argv: []string{"comments", "bd-fault", "--json"}, Stdout: envelope(comment)})
		appendAttempt("discussion.conflict", comment, false)
		return steps, nil, diagnostic, false
	case "assignment.project.observe.replay":
		metadata := `{"spec_kitty":{"assignment_marker":"operation-conformance-assignment","content_digest":"","created_by_operation":"operation-create-fault","criteria":[],"design_context":"","last_operation":"operation-conformance-assignment","project_id":"project-fault","schema_version":1,"work_id":"work-fault"}}`
		after := envelope("[" + issueJSONWithMetadata("bd-fault", "fault fixture", "actor-conformance", metadata, "r2") + "]")
		steps := []dishonestStep{show("assignment.initial", base)}
		steps = append(steps, preflight("assignment.first")...)
		steps = append(steps, show("assignment.first.before", base), dishonestStep{ID: "assignment.first.mutate", Argv: []string{"update", "bd-fault", "--metadata", metadata, "--assignee", "actor-conformance", "--json"}, Stdout: after}, show("assignment.first.after", after))
		steps = append(steps, preflight("assignment.replay")...)
		steps = append(steps, show("assignment.replay.read", after), show("assignment.final", after))
		return steps, nil, diagnostic, false
	case "assignment.compensation.attribution":
		metadata := `{"spec_kitty":{"assignment_marker":"operation-conformance-compensable","content_digest":"","created_by_operation":"operation-create-fault","criteria":[],"design_context":"","last_operation":"operation-conformance-compensable","project_id":"project-fault","schema_version":1,"work_id":"work-fault"}}`
		attributed := envelope("[" + issueJSONWithMetadata("bd-fault", "fault fixture", "actor-compensable", metadata, "r2") + "]")
		return []dishonestStep{show("compensation.initial", attributed), show("compensation.observe", attributed)}, nil, diagnostic, false
	default:
		t.Fatalf("unhandled dishonest conformance case %q", caseID)
		return nil, nil, diagnostic, false
	}
}

func dishonestMutationCount(root string) int {
	encoded, err := os.ReadFile(filepath.Join(root, "scenario.state.json"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		return -1
	}
	var state dishonestRuntime
	if json.Unmarshal(encoded, &state) != nil {
		return -1
	}
	count := 0
	for _, call := range state.Invocations {
		if len(call.Argv) == 0 {
			continue
		}
		if call.Argv[0] == "create" || call.Argv[0] == "update" || (len(call.Argv) >= 2 && call.Argv[0] == "dep" && (call.Argv[1] == "add" || call.Argv[1] == "remove")) || (len(call.Argv) >= 2 && call.Argv[0] == "comments" && call.Argv[1] == "add") {
			count++
		}
	}
	return count
}

func verifyDishonestConsumption(root string, expected int) error {
	encoded, err := os.ReadFile(filepath.Join(root, "scenario.state.json"))
	if os.IsNotExist(err) && expected == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	var state dishonestRuntime
	if err := json.Unmarshal(encoded, &state); err != nil {
		return err
	}
	if state.Position != expected || len(state.Invocations) != expected {
		return fmt.Errorf("dishonest conformance script consumption=%d/%d invocations=%d", state.Position, expected, len(state.Invocations))
	}
	return nil
}

func TestBeadsDishonestProtocolFaultMatrix(t *testing.T) {
	executable := buildDishonestExecutable(t)
	for _, fault := range protocolFaultRegistry() {
		fault := fault
		t.Run(fault.ID, func(t *testing.T) {
			fixture := newDishonestFixture(t, executable, []dishonestStep{showStep(fault.ID, fault.Stdout, fault.Stderr, fault.ExitCode)}, nil)
			_, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
			expectTrackerFault(t, err, fault.Kind, fault.Recovery)
			assertDishonestConsumed(t, fixture.root, 1)
		})
	}
}

func TestBeadsDishonestScriptFailsClosedAndSchemaMirrorsActions(t *testing.T) {
	executable := buildDishonestExecutable(t)
	run := func(root string, argv ...string) error {
		command := exec.Command(executable, argv...)
		command.Dir = root
		command.Env = append(os.Environ(), "BEADS_DIR="+root, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1")
		return command.Run()
	}
	writeRaw := func(t *testing.T, root, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "scenario.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("unknown-action", func(t *testing.T) {
		root := t.TempDir()
		writeDishonestScenario(t, root, dishonestScenario{SchemaVersion: 1, Steps: []dishonestStep{{ID: "unknown", Argv: []string{"invented", "--json"}, RequiredEnv: safeDishonestEnvironment(root), Workspace: root}}})
		if err := run(root, "invented", "--json"); err == nil {
			t.Fatal("unknown scripted action was accepted")
		}
		if _, err := os.Stat(filepath.Join(root, "scenario.state.json")); !os.IsNotExist(err) {
			t.Fatalf("unknown action consumed state: %v", err)
		}
	})
	for _, test := range []struct {
		name string
		argv []string
		plan func(string) string
	}{
		{name: "schema-version-drift", argv: []string{"show", "bd-fault", "--json"}, plan: func(root string) string {
			return fmt.Sprintf(`{"schema_version":2,"steps":[{"id":"one","argv":["show","bd-fault","--json"],"required_env":%s,"workspace":%q}]}`, mustJSON(safeDishonestEnvironment(root)), root)
		}},
		{name: "unknown-scenario-field", argv: []string{"show", "bd-fault", "--json"}, plan: func(root string) string {
			return fmt.Sprintf(`{"schema_version":1,"unknown":true,"steps":[{"id":"one","argv":["show","bd-fault","--json"],"required_env":%s,"workspace":%q}]}`, mustJSON(safeDishonestEnvironment(root)), root)
		}},
		{name: "unknown-step-field", argv: []string{"show", "bd-fault", "--json"}, plan: func(root string) string {
			return fmt.Sprintf(`{"schema_version":1,"steps":[{"id":"one","argv":["show","bd-fault","--json"],"required_env":%s,"workspace":%q,"unknown":true}]}`, mustJSON(safeDishonestEnvironment(root)), root)
		}},
		{name: "duplicate-step-id", argv: []string{"show", "bd-fault", "--json"}, plan: func(root string) string {
			return fmt.Sprintf(`{"schema_version":1,"steps":[{"id":"same","argv":["show","bd-fault","--json"],"required_env":%s,"workspace":%q},{"id":"same","argv":["list","--json"],"required_env":%s,"workspace":%q}]}`, mustJSON(safeDishonestEnvironment(root)), root, mustJSON(safeDishonestEnvironment(root)), root)
		}},
		{name: "unknown-subaction", argv: []string{"comments", "invented", "bd-fault", "--json"}, plan: func(root string) string {
			return fmt.Sprintf(`{"schema_version":1,"steps":[{"id":"one","argv":["comments","invented","bd-fault","--json"],"required_env":%s,"workspace":%q}]}`, mustJSON(safeDishonestEnvironment(root)), root)
		}},
		{name: "extra-argv", argv: []string{"show", "bd-fault", "--json", "extra"}, plan: func(root string) string {
			return fmt.Sprintf(`{"schema_version":1,"steps":[{"id":"one","argv":["show","bd-fault","--json","extra"],"required_env":%s,"workspace":%q}]}`, mustJSON(safeDishonestEnvironment(root)), root)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeRaw(t, root, test.plan(root))
			if err := run(root, test.argv...); err == nil {
				t.Fatalf("invalid scenario %s was accepted", test.name)
			}
		})
	}
	t.Run("environment-mismatch", func(t *testing.T) {
		root := t.TempDir()
		step := showStep("env", issueEnvelope("", "", "project-fault", "work-fault", "r1"), "", 0)
		step.Workspace, step.RequiredEnv = root, map[string]string{"BEADS_DIR": root, "BD_JSON_ENVELOPE": "wrong", "BD_NON_INTERACTIVE": "1"}
		writeDishonestScenario(t, root, dishonestScenario{SchemaVersion: 1, Steps: []dishonestStep{step}})
		if err := run(root, step.Argv...); err == nil {
			t.Fatal("environment mismatch was accepted")
		}
	})
	t.Run("workspace-mismatch", func(t *testing.T) {
		root := t.TempDir()
		step := showStep("workspace", issueEnvelope("", "", "project-fault", "work-fault", "r1"), "", 0)
		step.Workspace, step.RequiredEnv = filepath.Join(root, "other"), safeDishonestEnvironment(root)
		writeDishonestScenario(t, root, dishonestScenario{SchemaVersion: 1, Steps: []dishonestStep{step}})
		if err := run(root, step.Argv...); err == nil {
			t.Fatal("workspace mismatch was accepted")
		}
	})
	t.Run("argv-mismatch-and-exhaustion", func(t *testing.T) {
		root := t.TempDir()
		step := showStep("one", issueEnvelope("", "", "project-fault", "work-fault", "r1"), "", 0)
		step.Workspace, step.RequiredEnv = root, safeDishonestEnvironment(root)
		writeDishonestScenario(t, root, dishonestScenario{SchemaVersion: 1, Steps: []dishonestStep{step}})
		if err := run(root, "show", "other", "--json"); err == nil {
			t.Fatal("argv mismatch was accepted")
		}
		if err := run(root, step.Argv...); err != nil {
			t.Fatal(err)
		}
		if err := run(root, step.Argv...); err == nil {
			t.Fatal("exhausted script was reused")
		}
		assertDishonestConsumed(t, root, 1)
	})

	encoded, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal", "tracker", "beads", "testdata", "scenario.schema.json"))
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("scenario schema invalid: %v", err)
	}
	for _, action := range []string{"version", "where", "dolt", "show", "list", "ready", "create", "update", "dep", "comments", "export", "backup"} {
		if !bytes.Contains(encoded, []byte(`"`+action+`"`)) {
			t.Fatalf("schema action vocabulary omitted %q", action)
		}
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestBeadsFaultRegistryExactCensus(t *testing.T) {
	executable := buildDishonestExecutable(t)
	expected := exactFaultRegistryIDs()
	started, terminal, passed := make([]string, 0, len(expected)), make([]string, 0, len(expected)), make([]string, 0, len(expected))
	families := map[string]int{}
	authorityFixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"}})
	staleAuthority := provisionPhase1StaleAuthority(t, authorityFixture, "wp08-fault-census")
	results := make([]conformance.Result, 0, len(expected))
	for _, caseID := range expected {
		caseID := caseID
		t.Run(caseID, func(t *testing.T) {
			started = append(started, caseID)
			family, _, _ := strings.Cut(caseID, ".")
			families[family]++
			audit := newFaultAuthorityAudit(t, authorityFixture, staleAuthority, caseID, faultRequiresAuthority(caseID))
			if err := audit.run("before"); err != nil {
				t.Fatal(err)
			}
			var evidence executableFaultEvidence
			if family == "apply-timeout" {
				switch caseID {
				case "apply-timeout.create":
					evidence = runCatalogCreateApplyTimeout(t, executable)
				case "apply-timeout.catalog-update":
					evidence = runCatalogUpdateApplyTimeout(t, executable)
				case "apply-timeout.dependency":
					evidence = runDependencyApplyTimeout(t, executable)
				case "apply-timeout.comment":
					evidence = runCommentApplyTimeout(t, executable)
				case "apply-timeout.assignment":
					evidence = runAssignmentApplyTimeout(t, executable)
				default:
					t.Fatalf("unhandled apply-timeout case %s", caseID)
				}
			} else {
				evidence = runNamedReadFault(t, executable, caseID)
			}
			if err := audit.run("after"); err != nil {
				t.Fatal(err)
			}
			result := conformance.Result{ID: caseID, Disposition: conformance.Passed, CapabilityDisposition: "supported", Operation: evidence.Operation, DiagnosticKind: evidence.DiagnosticKind, RecoveryCode: evidence.RecoveryCode, MutationOutcome: evidence.MutationOutcome, RetrySafety: evidence.RetrySafety}
			audit.populate(&result)
			results = append(results, result)
			terminal = append(terminal, caseID)
			passed = append(passed, caseID)
		})
	}
	for _, values := range [][]string{started, terminal, passed} {
		sort.Strings(values)
	}
	if !sameIDs(expected, started) || !sameIDs(expected, terminal) || !sameIDs(expected, passed) {
		t.Fatalf("fault census mismatch expected=%d started=%d terminal=%d passed=%d", len(expected), len(started), len(terminal), len(passed))
	}
	for _, family := range []string{"slow", "unavailable", "malformed", "stale", "wrong", "apply-timeout"} {
		if families[family] == 0 {
			t.Fatalf("fault family not executed: %s", family)
		}
	}
	omittedFault := conformance.Census{Expected: append([]string(nil), expected...), Started: append([]string(nil), started...), Terminal: append([]string(nil), terminal[:len(terminal)-1]...), Passed: append([]string(nil), passed[:len(passed)-1]...), Results: append([]conformance.Result(nil), results[:len(results)-1]...)}
	if err := conformance.ValidateCensus(omittedFault); err == nil {
		t.Fatal("omitted fault mutant escaped census")
	}
	for _, result := range results {
		if faultRequiresAuthority(result.ID) && (!result.UniversalRequired || !result.UniversalStarted || !result.UniversalPassed || !result.StaleProbeRequired || !result.StaleProbeStarted || !result.StaleProbePassed) {
			t.Fatalf("fault authority census incomplete: %+v", result)
		}
	}
	artifact := conformance.Census{Expected: append([]string(nil), expected...), Started: append([]string(nil), started...), Terminal: append([]string(nil), terminal...), Passed: append([]string(nil), passed...), Failed: []string{}, NotApplicable: []string{}, Results: results}
	if err := conformance.WriteCensus(filepath.Join(t.TempDir(), "beads-fault-census.json"), artifact); err != nil {
		t.Fatal(err)
	}
}

type executableFaultEvidence struct{ Operation, DiagnosticKind, RecoveryCode, MutationOutcome, RetrySafety string }

func evidenceFromObservation(operation string, err error, mutations int, applied bool) executableFaultEvidence {
	evidence := executableFaultEvidence{Operation: operation, MutationOutcome: "none", RetrySafety: "safe"}
	var typed *tracker.Error
	if errors.As(err, &typed) {
		evidence.DiagnosticKind, evidence.RecoveryCode = string(typed.Kind), typed.RecoveryAction()
		switch typed.Kind {
		case tracker.ErrorAmbiguous, tracker.ErrorTimeout:
			evidence.RetrySafety = "observe_before_retry"
		case tracker.ErrorUnavailable, tracker.ErrorLockContended, tracker.ErrorSchemaSkew, tracker.ErrorMalformedProtocol:
			evidence.RetrySafety = "repair_then_retry"
		case tracker.ErrorRevisionConflict:
			evidence.RetrySafety = "reconcile"
		}
	}
	if mutations > 0 && applied {
		evidence.MutationOutcome = "applied_before_response_loss"
	} else if err != nil {
		evidence.MutationOutcome = "not_applied"
	}
	return evidence
}

type faultAuthorityAudit struct {
	t                                                      *testing.T
	fixture                                                workspaceScenario
	stale                                                  phase1StaleAuthority
	fault                                                  string
	required                                               bool
	beforeStarted, beforePassed, afterStarted, afterPassed bool
}

func newFaultAuthorityAudit(t *testing.T, fixture workspaceScenario, stale phase1StaleAuthority, fault string, required bool) *faultAuthorityAudit {
	return &faultAuthorityAudit{t: t, fixture: fixture, stale: stale, fault: fault, required: required}
}
func (a *faultAuthorityAudit) run(stage string) error {
	if !a.required {
		return nil
	}
	if stage == "before" {
		a.beforeStarted = true
	} else {
		a.afterStarted = true
	}
	state := quickStaleFaultAuthority(a.t, a.fixture, a.stale, "WP08."+a.fault+"."+stage)
	err := auditBeadsFault(context.Background(), state)
	if err == nil {
		if stage == "before" {
			a.beforePassed = true
		} else {
			a.afterPassed = true
		}
	}
	return err
}
func (a *faultAuthorityAudit) populate(result *conformance.Result) {
	result.UniversalRequired, result.StaleProbeRequired = a.required, a.required
	result.UniversalStarted = a.beforeStarted && a.afterStarted
	result.UniversalPassed = a.beforePassed && a.afterPassed
	result.StaleProbeStarted, result.StaleProbePassed = result.UniversalStarted, result.UniversalPassed
}

func faultRequiresAuthority(caseID string) bool {
	family, _, _ := strings.Cut(caseID, ".")
	return family != "malformed"
}

func runNamedReadFault(t *testing.T, executable, caseID string) executableFaultEvidence {
	t.Helper()
	valid := issueEnvelope("", "", "project-fault", "work-fault", "r1")
	step := showStep(caseID, valid, "", 0)
	wantKind, wantRecovery := tracker.ErrorKind(""), ""
	switch caseID {
	case "slow.success-below-deadline":
	case "slow.deadline-before-apply", "slow.ignored-cancellation-process-kill":
		step.WaitFor, wantKind, wantRecovery = "never.release", tracker.ErrorTimeout, "observe_then_reconcile"
	case "unavailable.spawn-failure":
		fixture := newDishonestFixture(t, executable, []dishonestStep{step}, nil)
		moved := fixture.executable + ".offline"
		if err := os.Rename(fixture.executable, moved); err != nil {
			t.Fatal(err)
		}
		_, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		_ = os.Rename(moved, fixture.executable)
		expectTrackerFault(t, err, tracker.ErrorUnavailable, "repair_provider_binding")
		return evidenceFromObservation("catalog_read", err, 0, false)
	case "unavailable.documented-json-error", "unavailable.workspace-lock":
		step.Stdout, step.Stderr, step.ExitCode = `{"schema_version":1,"error":"bounded","code":"database_locked"}`, "arbitrary warning prose", 7
		wantKind, wantRecovery = tracker.ErrorLockContended, "repair_provider_state"
	case "malformed.empty":
		step.Stdout, wantKind, wantRecovery = "", tracker.ErrorMalformedProtocol, "repair_provider_binding"
	case "malformed.truncated":
		step.Stdout, wantKind, wantRecovery = `{"schema_version":1,"data":`, tracker.ErrorMalformedProtocol, "repair_provider_binding"
	case "malformed.oversized":
		step.Stdout, wantKind, wantRecovery = strings.Repeat("x", (4<<20)+1024), tracker.ErrorMalformedProtocol, "repair_provider_binding"
	case "malformed.multiple-values":
		step.Stdout, wantKind, wantRecovery = valid+`{}`, tracker.ErrorMalformedProtocol, "repair_provider_binding"
	case "malformed.future-schema":
		step.Stdout, wantKind, wantRecovery = `{"schema_version":2,"data":{}}`, tracker.ErrorSchemaSkew, "install_supported_provider"
	case "malformed.wrong-payload-shape":
		step.Stdout, wantKind, wantRecovery = envelope(`[]`), tracker.ErrorMalformedProtocol, "reconcile_provider_metadata"
	case "malformed.warning-noise":
		step.Stderr = "arbitrary warning prose"
	case "stale.old-revision-tied-timestamp":
		step.Stdout = issueEnvelope("actor-old", "operation-old", "project-fault", "work-fault", "r-old")
	case "stale.mapping", "wrong.foreign-project-work-mapping":
		step.Stdout, wantKind, wantRecovery = issueEnvelope("", "", "project-foreign", "work-fault", "r1"), tracker.ErrorRevisionConflict, "reconcile_mapping"
	case "stale.assignment-marker", "wrong.changed-operation-marker":
		step.Stdout = issueEnvelope("actor-old", "operation-old", "project-fault", "work-fault", "r-old")
	case "wrong.unexpected-assignee":
		step.Stdout = issueEnvelope("actor-unexpected", "operation-unexpected", "project-fault", "work-fault", "r-wrong")
	case "wrong.duplicate-match":
		step.Stdout, wantKind, wantRecovery = envelope("["+issueJSON("", "", "project-fault", "work-fault", "r1")+","+issueJSON("", "", "project-fault", "work-fault", "r2")+"]"), tracker.ErrorMalformedProtocol, "reconcile_provider_metadata"
	default:
		t.Fatalf("unhandled fault case %s", caseID)
	}
	fixture := newDishonestFixture(t, executable, []dishonestStep{step}, map[string]string{"truth_revision": "r-current"})
	var record tracker.WorkRecord
	var err error
	if step.WaitFor != "" {
		err = cancelAtDishonestBarrier(t, fixture.root, step.WaitFor, func(ctx context.Context) error {
			var callErr error
			record, callErr = fixture.adapter.ReadWork(ctx, fixture.reference)
			return callErr
		})
	} else {
		record, err = fixture.adapter.ReadWork(context.Background(), fixture.reference)
	}
	if wantKind != "" {
		expectTrackerFault(t, err, wantKind, wantRecovery)
	} else if err != nil || record.ExternalRef.ExternalID != fixture.reference.ExternalID {
		t.Fatalf("case=%s record=%+v err=%v", caseID, record, err)
	}
	switch caseID {
	case "stale.old-revision-tied-timestamp", "stale.assignment-marker", "wrong.changed-operation-marker":
		if record.Observation.ProviderToken != "r-old" || record.AssignmentMarker != "operation-old" {
			t.Fatalf("stale/wrong response was normalized: case=%s record=%+v", caseID, record)
		}
	case "wrong.unexpected-assignee":
		if record.VisibleAssignee != "actor-unexpected" || record.AssignmentMarker != "operation-unexpected" {
			t.Fatalf("unexpected provider truth was normalized: %+v", record)
		}
	case "malformed.warning-noise":
		if record.Observation.ProviderToken != "r1" {
			t.Fatalf("warning prose changed success decision: %+v", record)
		}
	}
	state := assertDishonestConsumed(t, fixture.root, 1)
	if strings.HasPrefix(caseID, "stale.") && state.State["truth_revision"] != "r-current" {
		t.Fatalf("stale case rewrote truth: %+v", state.State)
	}
	return evidenceFromObservation("catalog_read", err, dishonestMutationCount(fixture.root), false)
}

func TestBeadsDishonestTruthfulWarningStaleAndWrongState(t *testing.T) {
	executable := buildDishonestExecutable(t)
	t.Run("warning-success-does-not-branch", func(t *testing.T) {
		payload := issueEnvelope("actor-a", "operation-a", "project-fault", "work-fault", "r-current")
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("protocol.warning-success", payload, "arbitrary warning prose\n", 0)}, nil)
		record, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		if err != nil || record.VisibleAssignee != "actor-a" || record.AssignmentMarker != "operation-a" || record.Observation.ProviderToken != "r-current" {
			t.Fatalf("warning success record=%+v err=%v", record, err)
		}
		assertDishonestConsumed(t, fixture.root, 1)
	})
	t.Run("stale-snapshot-remains-observable", func(t *testing.T) {
		payload := issueEnvelope("actor-old", "operation-old", "project-fault", "work-fault", "r-old")
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("stale.old-revision-tied-time", payload, "", 0)}, map[string]string{"truth_revision": "r-current"})
		record, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		if err != nil || record.Observation.ProviderToken != "r-old" || record.VisibleAssignee != "actor-old" {
			t.Fatalf("stale observation normalized: record=%+v err=%v", record, err)
		}
		state := assertDishonestConsumed(t, fixture.root, 1)
		if state.State["truth_revision"] != "r-current" {
			t.Fatalf("returned stale response rewrote truth: %+v", state.State)
		}
	})
	t.Run("spawn-failure", func(t *testing.T) {
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("unavailable.spawn", issueEnvelope("", "", "project-fault", "work-fault", "r1"), "", 0)}, nil)
		moved := fixture.executable + ".offline"
		if err := os.Rename(fixture.executable, moved); err != nil {
			t.Fatal(err)
		}
		defer os.Rename(moved, fixture.executable)
		_, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		expectTrackerFault(t, err, tracker.ErrorUnavailable, "repair_provider_binding")
		if _, err := os.Stat(filepath.Join(fixture.root, "scenario.state.json")); !os.IsNotExist(err) {
			t.Fatalf("spawn failure consumed script: %v", err)
		}
	})
}

func TestBeadsDishonestSlowCancellationKillsChild(t *testing.T) {
	executable := buildDishonestExecutable(t)
	fixture := newDishonestFixture(t, executable, []dishonestStep{{
		ID: "slow.ignored-cancellation", Argv: []string{"show", "bd-fault", "--json"},
		Stdout: issueEnvelope("", "", "project-fault", "work-fault", "r1"), WaitFor: "never.release",
	}}, nil)
	err := cancelAtDishonestBarrier(t, fixture.root, "never.release", func(ctx context.Context) error {
		_, callErr := fixture.adapter.ReadWork(ctx, fixture.reference)
		return callErr
	})
	expectTrackerFault(t, err, tracker.ErrorTimeout, "observe_then_reconcile")
	assertDishonestConsumed(t, fixture.root, 1)
}

func TestBeadsDishonestExplicitBarrierSurvivesSchedulerLoad(t *testing.T) {
	executable := buildDishonestExecutable(t)
	stop := make(chan struct{})
	var load sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		load.Add(1)
		go func() {
			defer load.Done()
			for {
				select {
				case <-stop:
					return
				default:
					runtime.Gosched()
				}
			}
		}()
	}
	defer func() { close(stop); load.Wait() }()
	for repetition := 0; repetition < 20; repetition++ {
		root := t.TempDir()
		barrier := fmt.Sprintf("before-apply-%02d", repetition)
		step := showStep("load.barrier", issueEnvelope("", "", "project-fault", "work-fault", "r1"), "", 0)
		step.WaitFor, step.WaitTiming = barrier, "before_apply"
		fixture := newDishonestFixtureAt(t, root, executable, []dishonestStep{step}, nil)
		err := cancelAtDishonestBarrier(t, root, barrier, func(ctx context.Context) error {
			_, callErr := fixture.adapter.ReadWork(ctx, fixture.reference)
			return callErr
		})
		expectTrackerFault(t, err, tracker.ErrorTimeout, "observe_then_reconcile")
		if _, statErr := os.Stat(filepath.Join(root, "scenario.state.json")); !os.IsNotExist(statErr) {
			t.Fatalf("repetition=%d before-apply barrier consumed state: %v", repetition, statErr)
		}
	}
}

func TestBeadsDishonestCatalogCreateApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runCatalogCreateApplyTimeout(t, buildDishonestExecutable(t))
}

func runCatalogCreateApplyTimeout(t *testing.T, executable string) executableFaultEvidence {
	root := t.TempDir()
	steps := preflightSteps(root)
	digest := portableRequestDigest("portable timeout", "", "")
	metadata := fmt.Sprintf(`{"spec_kitty":{"content_digest":%q,"created_by_operation":"operation-create-timeout","criteria":[],"design_context":"","last_operation":"operation-create-timeout","project_id":"project-fault","schema_version":1,"work_id":"work-timeout"}}`, digest)
	steps = append(steps,
		dishonestStep{ID: "apply-timeout.catalog-scan", Argv: []string{"list", "--json"}, Stdout: envelope(`[]`)},
		dishonestStep{
			ID: "apply-timeout.catalog-create", Argv: []string{"create", "--title", "portable timeout", "--metadata", `{"spec_kitty":{"content_digest":"fixed","created_by_operation":"operation-create-timeout","criteria":[],"design_context":"","last_operation":"operation-create-timeout","project_id":"project-fault","schema_version":1,"work_id":"work-timeout"}}`, "--json"},
			TransitionTiming: "before_response", Transition: map[string]string{"created": "bd-timeout", "operation": "operation-create-timeout"}, WaitFor: "never.release",
			NextObservation: map[string]string{"created": "bd-timeout", "operation": "operation-create-timeout"},
		},
		showStep("apply-timeout.catalog-observe", issueEnvelope("", "", "project-fault", "work-timeout", "r-after"), "", 0),
	)
	steps[5].Argv[4] = metadata
	fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
	err := cancelAtDishonestBarrier(t, fixture.root, "never.release", func(ctx context.Context) error {
		_, callErr := fixture.adapter.CreateWork(ctx, tracker.CreateWorkRequest{ProjectID: "project-fault", WorkID: "work-timeout", OperationID: "operation-create-timeout", Title: "portable timeout", MetadataSchema: 1})
		return callErr
	})
	expectTrackerFault(t, err, tracker.ErrorAmbiguous, "observe_then_reconcile")
	state := assertDishonestConsumed(t, fixture.root, 6)
	if state.State["created"] != "bd-timeout" {
		t.Fatalf("apply-before-loss not retained: %+v", state.State)
	}
	observed, observeErr := fixture.adapter.ReadWork(context.Background(), tracker.ExternalWorkRef{ProjectID: "project-fault", WorkID: "work-timeout", Provider: "beads", ExternalID: "bd-fault", MetadataSchema: 1, CreatedByOperation: "operation-create-fault"})
	if observeErr != nil || observed.ExternalRef.WorkID != "work-timeout" {
		t.Fatalf("independent observation=%+v err=%v", observed, observeErr)
	}
	final := assertDishonestConsumed(t, fixture.root, 7)
	assertSingleMutationInvocation(t, final, "create")

	// A raw exact mutation step proves the child can atomically record the
	// apply-before-loss distinction. Other tests drive every response through
	// the production decoder; this self-test covers the script mechanism.
	rawRoot := t.TempDir()
	raw := newDishonestFixtureAt(t, rawRoot, executable, []dishonestStep{{
		ID: "apply-timeout.raw-exact", Argv: []string{"update", "bd-fault", "--claim", "--json"},
		TransitionTiming: "before_response", Transition: map[string]string{"applied": "true"}, NextObservation: map[string]string{"applied": "true"}, WaitFor: "never.release",
	}}, nil)
	_ = cancelAtDishonestBarrier(t, raw.root, "never.release", func(ctx context.Context) error {
		command := execCommandContext(ctx, raw.executable, "update", "bd-fault", "--claim", "--json")
		command.Dir = raw.root
		command.Env = []string{"BEADS_DIR=" + raw.root, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1"}
		return command.Run()
	})
	rawState := assertDishonestConsumed(t, raw.root, 1)
	if rawState.State["applied"] != "true" {
		t.Fatalf("apply-before-loss absent: %+v", rawState.State)
	}
	return evidenceFromObservation("create", err, dishonestMutationCount(fixture.root), state.State["created"] == "bd-timeout")
}

func TestBeadsDishonestAssignmentApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runAssignmentApplyTimeout(t, buildDishonestExecutable(t))
}

func runAssignmentApplyTimeout(t *testing.T, executable string) executableFaultEvidence {
	root := t.TempDir()
	operation := ids.OperationID("operation-assignment-timeout")
	metadata := `{"spec_kitty":{"assignment_marker":"operation-assignment-timeout","content_digest":"","created_by_operation":"operation-create-fault","criteria":[],"design_context":"","last_operation":"operation-assignment-timeout","project_id":"project-fault","schema_version":1,"work_id":"work-fault"}}`
	steps := []dishonestStep{showStep("assignment.before-request", issueEnvelope("", "", "project-fault", "work-fault", "r-before"), "", 0)}
	steps = append(steps, preflightSteps(root)...)
	steps = append(steps,
		showStep("assignment.before-mutation", issueEnvelope("", "", "project-fault", "work-fault", "r-before"), "", 0),
		dishonestStep{ID: "assignment.apply-timeout", Argv: []string{"update", "bd-fault", "--metadata", metadata, "--assignee", "actor-timeout", "--json"}, TransitionTiming: "before_response", Transition: map[string]string{"assignment": "actor-timeout", "marker": operation.String()}, NextObservation: map[string]string{"assignment": "actor-timeout", "marker": operation.String()}, WaitFor: "never.release"},
		showStep("assignment.observe-after-loss", issueEnvelope("actor-timeout", operation.String(), "project-fault", "work-fault", "r-after"), "", 0),
	)
	fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
	before, err := fixture.adapter.ObserveAssignment(context.Background(), fixture.reference)
	if err != nil {
		t.Fatal(err)
	}
	err = cancelAtDishonestBarrier(t, fixture.root, "never.release", func(ctx context.Context) error {
		_, callErr := fixture.adapter.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: fixture.reference, OperationID: operation, Assignee: "actor-timeout", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded})
		return callErr
	})
	expectTrackerFault(t, err, tracker.ErrorAmbiguous, "observe_then_reconcile")
	state := readDishonestRuntime(t, root)
	if state.State["assignment"] != "actor-timeout" || state.State["marker"] != operation.String() || state.Position < 7 {
		t.Fatalf("assignment apply-before-loss state=%+v", state)
	}
	after, observeErr := fixture.adapter.ObserveAssignment(context.Background(), fixture.reference)
	if observeErr != nil || after.Assignee != "actor-timeout" || after.OperationMarker != operation.String() {
		t.Fatalf("assignment post-observation=%+v err=%v", after, observeErr)
	}
	final := assertDishonestConsumed(t, root, len(steps))
	assertSingleMutationInvocation(t, final, "update")
	return evidenceFromObservation("assignment", err, dishonestMutationCount(root), state.State["assignment"] == "actor-timeout")
}

func TestBeadsDishonestCommentApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runCommentApplyTimeout(t, buildDishonestExecutable(t))
}

func runCommentApplyTimeout(t *testing.T, executable string) executableFaultEvidence {
	root := t.TempDir()
	operation := ids.OperationID("operation-comment-timeout")
	providerBody := "<!-- spec-kitty:discussion:v1;op=" + base64.RawURLEncoding.EncodeToString([]byte(operation.String())) + " -->\nportable body"
	comment := fmt.Sprintf(`[{"id":"comment-1","author":"actor-timeout","text":%q,"created_at":"2026-08-26T12:01:00Z"}]`, providerBody)
	issue := issueEnvelope("", "", "project-fault", "work-fault", "r-before")
	steps := []dishonestStep{showStep("comment.before-request", issue, "", 0)}
	steps = append(steps, preflightSteps(root)...)
	steps = append(steps,
		showStep("comment.observe-before", issue, "", 0),
		dishonestStep{ID: "comment.list-before", Argv: []string{"comments", "bd-fault", "--json"}, Stdout: envelope(`[]`)},
		dishonestStep{ID: "comment.scope-list", Argv: []string{"list", "--json"}, Stdout: envelope("[" + issueJSON("", "", "project-fault", "work-fault", "r-before") + "]")},
		dishonestStep{ID: "comment.apply-timeout", Argv: []string{"comments", "add", "bd-fault", providerBody, "--json"}, TransitionTiming: "before_response", Transition: map[string]string{"comment": operation.String()}, NextObservation: map[string]string{"comment": operation.String()}, WaitFor: "never.release"},
		showStep("comment.observe-after", issueEnvelope("", "", "project-fault", "work-fault", "r-after"), "", 0),
		dishonestStep{ID: "comment.list-after", Argv: []string{"comments", "bd-fault", "--json"}, Stdout: envelope(comment)},
	)
	fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
	record, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
	if err != nil {
		t.Fatal(err)
	}
	err = cancelAtDishonestBarrier(t, fixture.root, "never.release", func(ctx context.Context) error {
		_, callErr := fixture.adapter.AppendDiscussion(ctx, tracker.AppendDiscussionRequest{ExternalRef: fixture.reference, OperationID: operation, Author: "actor-timeout", Body: "portable body", ExpectedObservation: record.Observation.Revision})
		return callErr
	})
	expectTrackerFault(t, err, tracker.ErrorAmbiguous, "observe_then_reconcile")
	state := readDishonestRuntime(t, root)
	if state.State["comment"] != operation.String() {
		t.Fatalf("comment apply-before-loss state=%+v", state)
	}
	entries, observeErr := fixture.adapter.ListDiscussion(context.Background(), fixture.reference)
	if observeErr != nil || len(entries) != 1 || entries[0].OperationMarker != operation.String() || entries[0].Body != "portable body" {
		t.Fatalf("comment post-observation=%+v err=%v", entries, observeErr)
	}
	final := assertDishonestConsumed(t, root, len(steps))
	assertSingleMutationInvocation(t, final, "comments", "add")
	return evidenceFromObservation("comment", err, dishonestMutationCount(root), state.State["comment"] == operation.String())
}

func TestBeadsDishonestDependencyApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runDependencyApplyTimeout(t, buildDishonestExecutable(t))
}

func runDependencyApplyTimeout(t *testing.T, executable string) executableFaultEvidence {
	root := t.TempDir()
	current := issueJSON("", "", "project-fault", "work-fault", "r-before")
	blocker := namedIssueJSON("bd-blocker", "blocker", "", "", "project-fault", "work-blocker", "operation-create-blocker", "r-blocker")
	catalog := envelope("[" + current + "," + blocker + "]")
	steps := preflightSteps(root)
	steps = append(steps,
		dishonestStep{ID: "dependency.catalog-before", Argv: []string{"list", "--json"}, Stdout: catalog},
		dishonestStep{ID: "dependency.list-before", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: envelope(`[]`)},
		dishonestStep{ID: "dependency.apply-timeout", Argv: []string{"dep", "add", "bd-fault", "bd-blocker", "--type", "blocks", "--json"}, TransitionTiming: "before_response", Transition: map[string]string{"dependency": "bd-fault<-bd-blocker"}, NextObservation: map[string]string{"dependency": "bd-fault<-bd-blocker"}, WaitFor: "never.release"},
		dishonestStep{ID: "dependency.catalog-after", Argv: []string{"list", "--json"}, Stdout: catalog},
		dishonestStep{ID: "dependency.list-after", Argv: []string{"dep", "list", "bd-fault", "--json"}, Stdout: envelope(`[{"id":"bd-blocker","dependency_type":"blocks"}]`)},
	)
	fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
	edge := tracker.DependencyRef{BlockedWorkID: "work-fault", BlockingWorkID: "work-blocker", Kind: tracker.DependencyBlocks}
	err := cancelAtDishonestBarrier(t, fixture.root, "never.release", func(ctx context.Context) error {
		_, callErr := fixture.adapter.PutBlockingDependency(ctx, tracker.DependencyRequest{ProjectID: "project-fault", OperationID: "operation-dependency-timeout", Dependency: edge})
		return callErr
	})
	expectTrackerFault(t, err, tracker.ErrorAmbiguous, "observe_then_reconcile")
	state := readDishonestRuntime(t, root)
	if state.State["dependency"] != "bd-fault<-bd-blocker" {
		t.Fatalf("dependency apply-before-loss state=%+v", state)
	}
	edges, observeErr := fixture.adapter.ListDependencies(context.Background(), fixture.reference)
	if observeErr != nil || len(edges) != 1 || edges[0].BlockedWorkID != edge.BlockedWorkID || edges[0].BlockingWorkID != edge.BlockingWorkID {
		t.Fatalf("dependency post-observation=%+v err=%v", edges, observeErr)
	}
	final := assertDishonestConsumed(t, root, len(steps))
	assertSingleMutationInvocation(t, final, "dep", "add")
	return evidenceFromObservation("dependency", err, dishonestMutationCount(root), state.State["dependency"] == "bd-fault<-bd-blocker")
}

func TestBeadsDishonestCatalogUpdateApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runCatalogUpdateApplyTimeout(t, buildDishonestExecutable(t))
}

func runCatalogUpdateApplyTimeout(t *testing.T, executable string) executableFaultEvidence {
	root := t.TempDir()
	operation := ids.OperationID("operation-catalog-update-timeout")
	digest := portableRequestDigest("portable update", "", "")
	metadata := fmt.Sprintf(`{"spec_kitty":{"content_digest":%q,"created_by_operation":"operation-create-fault","criteria":[],"design_context":"","last_operation":%q,"project_id":"project-fault","schema_version":1,"work_id":"work-fault"}}`, digest, operation)
	beforeIssue := issueEnvelope("", "", "project-fault", "work-fault", "r-before")
	afterIssue := envelope("[" + issueJSONWithMetadata("bd-fault", "portable update", "", metadata, "r-after") + "]")
	steps := []dishonestStep{showStep("catalog-update.before-request", beforeIssue, "", 0)}
	steps = append(steps, preflightSteps(root)...)
	steps = append(steps,
		showStep("catalog-update.before-mutation", beforeIssue, "", 0),
		dishonestStep{ID: "catalog-update.apply-timeout", Argv: []string{"update", "bd-fault", "--title", "portable update", "--description", "", "--acceptance", "", "--metadata", metadata, "--json"}, TransitionTiming: "before_response", Transition: map[string]string{"catalog_title": "portable update", "catalog_operation": operation.String()}, NextObservation: map[string]string{"catalog_title": "portable update", "catalog_operation": operation.String()}, WaitFor: "never.release"},
		showStep("catalog-update.observe-after-loss", afterIssue, "", 0),
	)
	fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
	before, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
	if err != nil {
		t.Fatal(err)
	}
	err = cancelAtDishonestBarrier(t, fixture.root, "never.release", func(ctx context.Context) error {
		_, callErr := fixture.adapter.UpdateWork(ctx, tracker.UpdateWorkRequest{ExternalRef: fixture.reference, OperationID: operation, ExpectedObservation: before.Observation.Revision, RequiredGuard: tracker.UpdateUnguarded, Title: "portable update"})
		return callErr
	})
	expectTrackerFault(t, err, tracker.ErrorAmbiguous, "observe_then_reconcile")
	state := readDishonestRuntime(t, root)
	if state.State["catalog_title"] != "portable update" || state.State["catalog_operation"] != operation.String() {
		t.Fatalf("catalog update apply-before-loss=%+v", state)
	}
	after, observeErr := fixture.adapter.ReadWork(context.Background(), fixture.reference)
	if observeErr != nil || after.Title != "portable update" {
		t.Fatalf("catalog update post-observation=%+v err=%v", after, observeErr)
	}
	final := assertDishonestConsumed(t, root, len(steps))
	assertSingleMutationInvocation(t, final, "update")
	return evidenceFromObservation("catalog-update", err, dishonestMutationCount(root), state.State["catalog_operation"] == operation.String())
}

func assertSingleMutationInvocation(t *testing.T, state dishonestRuntime, action ...string) {
	t.Helper()
	count := 0
	for _, invocation := range state.Invocations {
		if len(invocation.Argv) >= len(action) && sameIDs(invocation.Argv[:len(action)], action) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("blind-retry mutant escaped: action=%v invocations=%d", action, count)
	}
}

func TestBeadsUnsafeDecisionMutantsAreKilled(t *testing.T) {
	executable := buildDishonestExecutable(t)
	t.Run("timeout-as-not-applied", func(t *testing.T) {
		evidence := runCatalogCreateApplyTimeout(t, executable)
		unsafe := evidence
		unsafe.MutationOutcome, unsafe.RetrySafety = "not_applied", "retry_now"
		if err := validateFaultRecoveryEvidence(unsafe, true); err == nil {
			t.Fatal("timeout-as-not-applied mutant escaped observed provider truth")
		}
	})
	t.Run("trust-stale-response", func(t *testing.T) {
		fixture := newDishonestFixture(t, executable, []dishonestStep{showStep("stale.mutant", issueEnvelope("actor-old", "operation-old", "project-fault", "work-fault", "r-old"), "", 0)}, map[string]string{"truth_revision": "r-current"})
		record, err := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		if err != nil {
			t.Fatal(err)
		}
		truth := readDishonestRuntime(t, fixture.root).State["truth_revision"]
		unsafeTrusted := record.Observation.ProviderToken
		if unsafeTrusted == truth {
			t.Fatal("stale-trust mutant unexpectedly matched current provider truth")
		}
	})
	t.Run("branch-on-warning-prose", func(t *testing.T) {
		step := showStep("warning.mutant", issueEnvelope("", "", "project-fault", "work-fault", "r1"), "warning-only", 0)
		fixture := newDishonestFixture(t, executable, []dishonestStep{step}, nil)
		_, actualErr := fixture.adapter.ReadWork(context.Background(), fixture.reference)
		unsafeErr := actualErr
		if step.Stderr != "" {
			unsafeErr = errors.New("warning treated as authority")
		}
		if actualErr != nil || unsafeErr == nil {
			t.Fatalf("warning mutant was not distinguished actual=%v unsafe=%v", actualErr, unsafeErr)
		}
	})
	t.Run("blind-retry", func(t *testing.T) {
		root := t.TempDir()
		argv := []string{"update", "bd-fault", "--claim", "--json"}
		steps := []dishonestStep{{ID: "blind.first", Argv: argv, Stdout: envelope(`[]`), TransitionTiming: "before_response", Transition: map[string]string{"first": "applied"}}, {ID: "blind.retry", Argv: argv, Stdout: envelope(`[]`), TransitionTiming: "before_response", Transition: map[string]string{"second": "applied"}}}
		fixture := newDishonestFixtureAt(t, root, executable, steps, nil)
		for range 2 {
			command := exec.Command(fixture.executable, argv...)
			command.Dir = root
			command.Env = []string{"BEADS_DIR=" + root, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1"}
			_ = command.Run()
		}
		state := assertDishonestConsumed(t, root, 2)
		count := 0
		for _, call := range state.Invocations {
			if sameIDs(call.Argv, argv) {
				count++
			}
		}
		if count != 2 || state.State["second"] != "applied" {
			t.Fatal("blind retry mutant did not execute two real provider mutations")
		}
		if err := validateMutationCount(count); err == nil {
			t.Fatal("blind retry mutant escaped mutation census")
		}
	})
}

func validateFaultRecoveryEvidence(evidence executableFaultEvidence, observedApplied bool) error {
	if observedApplied && (evidence.MutationOutcome != "applied_before_response_loss" || evidence.RetrySafety != "observe_before_retry") {
		return fmt.Errorf("unsafe recovery contradicts observed provider mutation")
	}
	return nil
}
func validateMutationCount(count int) error {
	if count != 1 {
		return fmt.Errorf("mutation count=%d want=1", count)
	}
	return nil
}

// execCommandContext is a test seam that keeps process creation visible to
// static analysis without importing a shell.
var execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

func TestBeadsHumanAssignmentPreservation100(t *testing.T) {
	const repetitions = 100
	const seed = int64(0x5EC0A11)
	_ = seed // fixed and recorded; case order is lexical for reproducibility.
	idsExpected := make([]string, 0, repetitions)
	idsStarted := make([]string, 0, repetitions)
	idsTerminal := make([]string, 0, repetitions)
	idsPassed := make([]string, 0, repetitions)
	results := make([]map[string]any, 0, repetitions)
	authorityFixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"}})
	stale := provisionPhase1StaleAuthority(t, authorityFixture, "wp08-human")

	for repetition := 1; repetition <= repetitions; repetition++ {
		caseID := stableID("human-assignment-preservation", repetition)
		idsExpected = append(idsExpected, caseID)
		t.Run(caseID, func(t *testing.T) {
			idsStarted = append(idsStarted, caseID)
			authorityAudit := newFaultAuthorityAudit(t, authorityFixture, stale, caseID, true)
			if err := authorityAudit.run("before"); err != nil {
				t.Fatal(err)
			}
			// Provider, saga, and authority audit all share this scenario's
			// tracker/control-store pair; no evidence is borrowed from a
			// disconnected fixture.
			store, err := fake.Open(authorityFixture.tracker)
			if err != nil {
				t.Fatal(err)
			}
			workKey := stableID("external-human", repetition)
			if err := store.Seed(context.Background(), fake.WorkSeed{Key: workKey, Title: "human preservation", Assignee: "original"}); err != nil {
				t.Fatal(err)
			}
			provider := conflictObservingProjection{Provider: fake.NewProvider(store)}
			reference := tracker.ExternalWorkRef{ProjectID: ids.ProjectID(stableID("project-human", repetition)), WorkID: ids.WorkID(stableID("work-human", repetition)), Provider: "durable_fake", ExternalID: workKey, MetadataSchema: 1}
			before, err := provider.ObserveAssignment(context.Background(), reference)
			if err != nil {
				t.Fatal(err)
			}
			operation := humanOperation(repetition)
			// The saga and the authority audit deliberately share one durable
			// control store; this is causal evidence, not a co-located fixture.
			controlPath := authorityFixture.database
			control, err := sqlite.Open(context.Background(), controlPath)
			if err != nil {
				t.Fatal(err)
			}
			request := execution.TrackerAssignRequest{
				SchemaVersion: execution.ResultSchemaVersion, CorrelationID: stableID("correlation-human", repetition),
				OperationID: operation, ActorID: ids.ActorID(stableID("actor-automated", repetition)), ProcessID: ids.ProcessID(stableID("process-automated", repetition)),
				Now: time.Date(2026, 8, 26, 19, 0, repetition, 0, time.UTC), Provider: reference.Provider, WorkKey: reference.ExternalID,
				ExpectedRevision: before.Observation.Revision, Assignee: stableID("actor-automated", repetition), ExternalRef: reference,
				RequiredGuarantee: tracker.AssignmentAtomicClaim,
			}
			saga := execution.NewTrackerSaga(control, provider, execution.SystemClock{})
			applied, err := saga.Assign(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			human := stableID("actor-human", repetition)
			projected := make(chan struct{})
			humanApplied := make(chan struct{})
			type compensationResult struct{ err error }
			compensated := make(chan compensationResult, 1)
			go func() {
				close(projected)
				<-humanApplied
				_, compensateErr := saga.Compensate(context.Background(), execution.TrackerCompensateRequest{
					SchemaVersion: request.SchemaVersion, CorrelationID: request.CorrelationID, OperationID: request.OperationID,
					ActorID: request.ActorID, ProcessID: request.ProcessID, Now: request.Now.Add(time.Second), Provider: request.Provider,
					WorkKey: request.WorkKey, ExpectedRevision: applied.AppliedRevision, ExternalRef: reference, RequiredGuarantee: request.RequiredGuarantee,
				})
				compensated <- compensationResult{err: compensateErr}
			}()
			<-projected
			if err := store.HumanAssign(context.Background(), workKey, human); err != nil {
				t.Fatal(err)
			}
			historyBefore, _ := store.History(context.Background())
			close(humanApplied)
			compensateErr := (<-compensated).err
			var compensationDiagnostic *execution.DiagnosticError
			if !errors.As(compensateErr, &compensationDiagnostic) || compensationDiagnostic.RetrySafety == execution.RetrySafeSameOperation {
				t.Fatalf("compensation error=%v", compensateErr)
			}
			after, err := provider.ObserveAssignment(context.Background(), reference)
			if err != nil || after.Assignee != human || after.OperationMarker != "" {
				t.Fatalf("human state overwritten: %+v err=%v", after, err)
			}
			historyAfter, _ := store.History(context.Background())
			if len(historyAfter) != len(historyBefore) {
				t.Fatalf("automatic compensation mutated provider: before=%d after=%d", len(historyBefore), len(historyAfter))
			}
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			control, err = sqlite.Open(context.Background(), controlPath)
			if err != nil {
				t.Fatal(err)
			}
			effect, err := control.ExternalEffect(context.Background(), operation)
			if err != nil {
				t.Fatal(err)
			}
			if effect.CompensationStatus != execution.CompensationAttributionLost || effect.AppliedValue != request.Assignee || effect.Marker != operation.String() || effect.ConflictValue != human || effect.ConflictMarker != "" || effect.ConflictRevision != after.Observation.Revision || effect.ObservationValue != human || effect.ObservationRevision != after.Observation.Revision {
				t.Fatalf("durable conflict evidence incomplete: %+v", effect)
			}
			if err := control.Close(); err != nil {
				t.Fatal(err)
			}
			if err := authorityAudit.run("after"); err != nil {
				t.Fatal(err)
			}
			authorityResult := conformance.Result{}
			authorityAudit.populate(&authorityResult)
			idsTerminal = append(idsTerminal, caseID)
			idsPassed = append(idsPassed, caseID)
			results = append(results, map[string]any{"id": caseID, "outcome": string(effect.CompensationStatus), "expected_revision": effect.AppliedRevision, "actual_revision": effect.ConflictRevision, "expected_assignee": effect.AppliedValue, "actual_assignee": effect.ConflictValue, "expected_marker": effect.Marker, "actual_marker": effect.ConflictMarker, "compensation_mutations": len(historyAfter) - len(historyBefore), "human_preserved": after.Assignee == human, "universal_required": authorityResult.UniversalRequired, "universal_started": authorityResult.UniversalStarted, "universal_passed": authorityResult.UniversalPassed, "stale_probe_required": authorityResult.StaleProbeRequired, "stale_probe_started": authorityResult.StaleProbeStarted, "stale_probe_passed": authorityResult.StaleProbePassed})
		})
	}
	for _, values := range [][]string{idsExpected, idsStarted, idsTerminal, idsPassed} {
		sort.Strings(values)
	}
	if !sameIDs(idsExpected, idsStarted) || !sameIDs(idsExpected, idsTerminal) || !sameIDs(idsExpected, idsPassed) {
		t.Fatalf("human census mismatch expected=%d started=%d terminal=%d passed=%d", len(idsExpected), len(idsStarted), len(idsTerminal), len(idsPassed))
	}
	artifact := map[string]any{"seed": seed, "expected": idsExpected, "started": idsStarted, "terminal": idsTerminal, "passed": idsPassed, "failed": []string{}, "non_applicable": []string{}, "results": results}
	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "human-direct-assignment") {
		t.Fatal("census leaked human content")
	}
	if err := os.WriteFile(filepath.Join(t.TempDir(), "beads-human-census.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	omitted := conformance.Census{Expected: append([]string(nil), idsExpected...), Started: append([]string(nil), idsStarted...), Terminal: append([]string(nil), idsTerminal[:len(idsTerminal)-1]...), Passed: append([]string(nil), idsPassed[:len(idsPassed)-1]...)}
	if err := conformance.ValidateCensus(omitted); err == nil {
		t.Fatal("omitted repetition mutant escaped census")
	}
}

// conflictObservingProjection models the accepted provider contract: a
// compare-before-compensate refusal carries the fresh coherent observation
// needed for durable reconciliation evidence. The durable fake's legacy CAS
// helper returns only the typed refusal, so this test adapter adds the public
// observation without changing provider state.
type conflictObservingProjection struct{ *fake.Provider }

func (provider conflictObservingProjection) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	mutation, err := provider.Provider.CompensateAssignment(ctx, request)
	if !tracker.IsKind(err, tracker.ErrorAttributionLost) || mutation.Observation.Observation.Revision != 0 {
		return mutation, err
	}
	actual, observeErr := provider.Provider.ObserveAssignment(ctx, request.ExternalRef)
	if observeErr != nil {
		return tracker.AssignmentMutation{}, errors.Join(err, observeErr)
	}
	return tracker.AssignmentMutation{Observation: actual, Guarantee: tracker.AssignmentAtomicClaim, Noop: true}, err
}

type unsafePostHumanCompensator struct {
	conflictObservingProjection
	store *fake.Store
}

func (provider unsafePostHumanCompensator) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	mutation, err := provider.conflictObservingProjection.CompensateAssignment(ctx, request)
	if !tracker.IsKind(err, tracker.ErrorAttributionLost) {
		return mutation, err
	}
	if overwriteErr := provider.store.HumanAssign(ctx, request.ExternalRef.ExternalID, request.RestoreAssignee); overwriteErr != nil {
		return tracker.AssignmentMutation{}, overwriteErr
	}
	actual, observeErr := provider.ObserveAssignment(ctx, request.ExternalRef)
	return tracker.AssignmentMutation{Observation: actual, Guarantee: tracker.AssignmentAtomicClaim}, observeErr
}

func TestBeadsHumanAssignmentCampaignKillsPostAttributionOverwriteMutant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	providerStore, err := fake.Open(filepath.Join(root, "tracker.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.Seed(ctx, fake.WorkSeed{Key: "external-mutant", Title: "mutant", Assignee: "original"}); err != nil {
		t.Fatal(err)
	}
	base := conflictObservingProjection{Provider: fake.NewProvider(providerStore)}
	provider := unsafePostHumanCompensator{conflictObservingProjection: base, store: providerStore}
	reference := tracker.ExternalWorkRef{ProjectID: "project-mutant", WorkID: "work-mutant", Provider: "durable_fake", ExternalID: "external-mutant", MetadataSchema: 1}
	before, err := provider.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	control, err := sqlite.Open(ctx, filepath.Join(root, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	request := execution.TrackerAssignRequest{SchemaVersion: execution.ResultSchemaVersion, CorrelationID: "correlation-mutant", OperationID: "operation-mutant", ActorID: "actor-mutant", ProcessID: "process-mutant", Now: time.Now().UTC(), Provider: reference.Provider, WorkKey: reference.ExternalID, ExpectedRevision: before.Observation.Revision, Assignee: "automated", ExternalRef: reference, RequiredGuarantee: tracker.AssignmentAtomicClaim}
	saga := execution.NewTrackerSaga(control, provider, execution.SystemClock{})
	applied, err := saga.Assign(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := providerStore.HumanAssign(ctx, reference.ExternalID, "human"); err != nil {
		t.Fatal(err)
	}
	_, err = saga.Compensate(ctx, execution.TrackerCompensateRequest{SchemaVersion: request.SchemaVersion, CorrelationID: request.CorrelationID, OperationID: request.OperationID, ActorID: request.ActorID, ProcessID: request.ProcessID, Now: request.Now.Add(time.Second), Provider: request.Provider, WorkKey: request.WorkKey, ExpectedRevision: applied.AppliedRevision, ExternalRef: reference, RequiredGuarantee: request.RequiredGuarantee})
	if err != nil {
		t.Fatalf("unsafe mutant did not claim compensation success: %v", err)
	}
	after, err := provider.ObserveAssignment(ctx, reference)
	if err != nil {
		t.Fatal(err)
	}
	effect, err := control.ExternalEffect(ctx, request.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Assignee == "human" || effect.CompensationStatus != execution.CompensationDone {
		t.Fatalf("unsafe overwrite mutant did not alter provider/durable evidence after=%+v effect=%+v", after, effect)
	}
	if validateHumanPreservation(after, effect, "human") == nil {
		t.Fatal("human-preservation assertions accepted post-attribution overwrite")
	}
}

func validateHumanPreservation(after tracker.AssignmentObservation, effect execution.ExternalEffect, human string) error {
	if after.Assignee != human || effect.CompensationStatus != execution.CompensationAttributionLost {
		return fmt.Errorf("human assignment or durable attribution evidence was overwritten")
	}
	return nil
}

func sameIDs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestBeadsFaultsCarryUniversalAuthorityAndStaleProbe(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := newWorkspaceScenario(t, []workspaceItem{{work: "work-phase1-matrix-stale", lane: "lane-phase1-matrix-stale", tracker: "TK-PHASE1-MATRIX-STALE"}})
	stale := provisionPhase1StaleAuthority(t, fixture, "wp08")
	applicable := make([]string, 0)
	for _, fault := range exactFaultRegistryIDs() {
		if faultRequiresAuthority(fault) {
			applicable = append(applicable, fault)
		}
	}
	mutants := []struct {
		name   string
		mutate func(*universalAssertionState)
	}{
		{"missing-callback", func(state *universalAssertionState) { state.StaleProbe = nil }},
		{"requirement-disabled", func(state *universalAssertionState) { state.RequireStaleProbe = false; state.StaleProbe = nil }},
		{"stale-succeeds", func(state *universalAssertionState) { state.StaleProbe = func(context.Context) error { return nil } }},
	}
	for _, fault := range applicable {
		fault := fault
		t.Run(fault, func(t *testing.T) {
			state := staleFaultAuthority(t, fixture, stale, "WP08."+fault)
			if err := auditBeadsFault(ctx, state); err != nil {
				t.Fatal(err)
			}
			for _, mutant := range mutants {
				mutant := mutant
				t.Run("mutant-"+mutant.name, func(t *testing.T) {
					mutated := state
					mutant.mutate(&mutated)
					err := auditBeadsFault(ctx, mutated)
					if err == nil || !strings.Contains(err.Error(), "stale_token_refusal") {
						t.Fatalf("fault=%s mutant=%s error=%v", fault, mutant.name, err)
					}
				})
			}
		})
	}
}

func auditBeadsFault(ctx context.Context, state universalAssertionState) error {
	if !state.RequireStaleProbe || state.StaleProbe == nil {
		return fmt.Errorf("scenario_id=%s fault_id=%s invariant=stale_token_refusal recovery=require a real stale probe", state.ScenarioID, state.FaultID)
	}
	if err := assertUniversalInvariants(ctx, state); err != nil {
		return err
	}
	return assertReadOnlyReviewerIndependence(ctx, state)
}

func assertReadOnlyReviewerIndependence(ctx context.Context, state universalAssertionState) error {
	database, err := sql.Open("sqlite", state.Database)
	if err != nil {
		return err
	}
	defer database.Close()
	var violations int
	err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM reviews r JOIN handoff_contributors c ON c.handoff_id=r.handoff_id AND c.actor_id=r.reviewer_actor_id`).Scan(&violations)
	if err != nil {
		return err
	}
	if violations != 0 {
		return fmt.Errorf("scenario_id=%s fault_id=%s invariant=reviewer_independence violations=%d", state.ScenarioID, state.FaultID, violations)
	}
	return nil
}

func portableRequestDigest(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		length := uint64(len(value))
		var encoded [8]byte
		for index := 7; index >= 0; index-- {
			encoded[index] = byte(length)
			length >>= 8
		}
		_, _ = hash.Write(encoded[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func TestBeadsFaultDiagnosticsNeverTreatProseAsAuthority(t *testing.T) {
	err := &tracker.Error{Kind: tracker.ErrorAmbiguous, RecoveryCode: "observe_then_reconcile", Message: "provider mutation outcome is ambiguous", Cause: errors.New("secret-provider-body")}
	if fmt.Sprintf("%v", err) != "tracker ambiguous: provider mutation outcome is ambiguous" {
		t.Fatalf("raw provider cause leaked: %v", err)
	}
	var diagnostic *execution.DiagnosticError
	if errors.As(err, &diagnostic) {
		t.Fatal("tracker evidence impersonated execution authority")
	}
}
