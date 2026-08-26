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
		fixture := newDishonestFixtureAt(t, root, executable, nil, nil)
		store, err := fake.Open(filepath.Join(root, "portable-fake.json"))
		if err != nil {
			return conformance.Fixture{}, err
		}
		if err := store.Seed(ctx, fake.WorkSeed{Key: "external-1", Title: "portable contract", Criteria: []string{"observable result"}, Assignee: "original"}); err != nil {
			return conformance.Fixture{}, err
		}
		semantic := fake.NewProvider(store)
		reference := tracker.ExternalWorkRef{ProjectID: "project-1", WorkID: "work-1", Provider: "durable_fake", ExternalID: "external-1", MetadataSchema: 1}
		if caseID == "assignment.compensation.attribution" {
			before, observeErr := semantic.ObserveAssignment(ctx, reference)
			if observeErr != nil {
				return conformance.Fixture{}, observeErr
			}
			if _, projectErr := semantic.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: reference, OperationID: "operation-conformance-compensable", Assignee: "actor-compensable", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentAtomicClaim}); projectErr != nil {
				return conformance.Fixture{}, projectErr
			}
		}
		provider := &dishonestPortableProvider{t: t, caseID: caseID, fixture: fixture, store: store, semantic: semantic}
		capabilities := tracker.ProviderCapabilities{
			CatalogRead: tracker.SupportSupported, CatalogCreate: tracker.SupportSupported, CatalogUpdate: tracker.UpdateUnguarded,
			BlockingGraph: tracker.SupportSupported, Discussion: tracker.DiscussionAppendOnly, Assignment: tracker.AssignmentUnguarded,
			ObservationRevision: tracker.ObservationRevisionContentDerived, SafeCompensation: tracker.CompensationUnsupported,
			ConcurrentWriters: tracker.ConcurrentWritersAdapterSerialized, PortableExport: tracker.SupportUnsupported, RestorableBackup: tracker.SupportUnsupported,
		}
		return conformance.Fixture{
			Capabilities: capabilities,
			Reference:    reference,
			Catalog:      provider, Graph: provider, Discussion: provider, Assignment: provider,
			MutationCount: provider.mutationCount,
			Fault:         provider.exerciseFault,
			Close:         provider.close,
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

// dishonestPortableProvider makes every shared provider-neutral operation
// cross the production bounded-command decoder before applying its portable
// semantic assertion to an isolated durable store. The scripted child is not
// a function mock: every probe is an exact argv/environment/workspace step,
// and close proves no expected step was bypassed.
type dishonestPortableProvider struct {
	t         *testing.T
	caseID    string
	fixture   dishonestFixture
	store     *fake.Store
	semantic  *fake.Provider
	mu        sync.Mutex
	steps     []dishonestStep
	mutations int
	created   map[ids.OperationID]tracker.WorkMutation
	updated   map[ids.OperationID]tracker.WorkMutation
	edges     []tracker.DependencyRef
}

func (provider *dishonestPortableProvider) probe(ctx context.Context, operation string) error {
	_, err := provider.probePayload(ctx, operation, issueEnvelope("", "", "project-fault", "work-fault", "r1"))
	return err
}

func (provider *dishonestPortableProvider) probePayload(ctx context.Context, operation, payload string) (tracker.WorkRecord, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	step := showStep(fmt.Sprintf("conformance.%s.%03d.%s", provider.caseID, len(provider.steps)+1, operation), payload, "", 0)
	step.Workspace = provider.fixture.root
	step.RequiredEnv = safeDishonestEnvironment(provider.fixture.root)
	provider.steps = append(provider.steps, step)
	writeDishonestScenario(provider.t, provider.fixture.root, dishonestScenario{SchemaVersion: 1, Steps: provider.steps})
	return provider.fixture.adapter.ReadWork(ctx, provider.fixture.reference)
}

func (provider *dishonestPortableProvider) exerciseFault(ctx context.Context, id string) error {
	switch id {
	case "unavailable":
		moved := provider.fixture.executable + ".offline"
		if err := os.Rename(provider.fixture.executable, moved); err != nil {
			return err
		}
		_, err := provider.fixture.adapter.ReadWork(ctx, provider.fixture.reference)
		restoreErr := os.Rename(moved, provider.fixture.executable)
		if restoreErr != nil {
			return restoreErr
		}
		if !tracker.IsKind(err, tracker.ErrorUnavailable) {
			return fmt.Errorf("unavailable diagnostic=%v", err)
		}
		return nil
	case "stale":
		record, err := provider.probePayload(ctx, "stale", issueEnvelope("actor-old", "operation-old", "project-fault", "work-fault", "r-old"))
		if err != nil {
			return err
		}
		if record.Observation.ProviderToken != "r-old" || record.VisibleAssignee != "actor-old" || record.AssignmentMarker != "operation-old" {
			return fmt.Errorf("stale provider response was normalized")
		}
		state := readDishonestRuntime(provider.t, provider.fixture.root)
		if state.Position != len(provider.steps) {
			return fmt.Errorf("stale script was not consumed")
		}
		return nil
	case "wrong":
		_, err := provider.probePayload(ctx, "wrong", issueEnvelope("", "", "project-foreign", "work-fault", "r1"))
		if !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
			return fmt.Errorf("wrong-state diagnostic=%v", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown portable fault %q", id)
	}
}

func (provider *dishonestPortableProvider) close() error {
	if len(provider.steps) == 0 {
		return nil
	}
	state := readDishonestRuntime(provider.t, provider.fixture.root)
	if state.Position != len(provider.steps) || len(state.Invocations) != len(provider.steps) {
		return fmt.Errorf("dishonest conformance script consumption=%d/%d invocations=%d", state.Position, len(provider.steps), len(state.Invocations))
	}
	return nil
}

func (provider *dishonestPortableProvider) CreateWork(ctx context.Context, request tracker.CreateWorkRequest) (tracker.WorkMutation, error) {
	if err := provider.probe(ctx, "catalog-create"); err != nil {
		return tracker.WorkMutation{}, err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.created == nil {
		provider.created = map[ids.OperationID]tracker.WorkMutation{}
	}
	if prior, ok := provider.created[request.OperationID]; ok {
		prior.Replayed, prior.Noop = true, true
		return prior, nil
	}
	reference := tracker.ExternalWorkRef{ProjectID: request.ProjectID, WorkID: request.WorkID, Provider: "durable_fake", ExternalID: "created-" + request.WorkID.String(), MetadataSchema: request.MetadataSchema, CreatedByOperation: request.OperationID}
	record, err := portableDishonestRecord(reference, request.Title, request.Description, request.DesignContext, request.Criteria, 2)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	result := tracker.WorkMutation{Record: record, Guarantee: tracker.UpdateUnguarded}
	provider.created[request.OperationID], provider.mutations = result, provider.mutations+1
	return result, nil
}
func (provider *dishonestPortableProvider) ReadWork(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.WorkRecord, error) {
	if err := provider.probe(ctx, "catalog-read"); err != nil {
		return tracker.WorkRecord{}, err
	}
	return provider.semantic.ReadWork(ctx, reference)
}
func (provider *dishonestPortableProvider) UpdateWork(ctx context.Context, request tracker.UpdateWorkRequest) (tracker.WorkMutation, error) {
	if err := provider.probe(ctx, "catalog-update"); err != nil {
		return tracker.WorkMutation{}, err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.updated == nil {
		provider.updated = map[ids.OperationID]tracker.WorkMutation{}
	}
	if prior, ok := provider.updated[request.OperationID]; ok {
		prior.Replayed, prior.Noop = true, true
		return prior, nil
	}
	record, err := portableDishonestRecord(request.ExternalRef, request.Title, request.Description, request.DesignContext, request.Criteria, request.ExpectedObservation+1)
	if err != nil {
		return tracker.WorkMutation{}, err
	}
	result := tracker.WorkMutation{Record: record, Guarantee: tracker.UpdateUnguarded}
	provider.updated[request.OperationID], provider.mutations = result, provider.mutations+1
	return result, nil
}
func (provider *dishonestPortableProvider) PutBlockingDependency(ctx context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	if err := provider.probe(ctx, "dependency-put"); err != nil {
		return tracker.DependencyMutation{}, err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.edges = append(provider.edges, request.Dependency)
	provider.mutations++
	observation, _ := tracker.NewProviderObservation(provider.fixture.reference.ExternalID, []byte("dependency-put"), tracker.Revision(2), "", "dependency-2", time.Now().UTC())
	return tracker.DependencyMutation{Dependency: request.Dependency, Observation: observation}, nil
}
func (provider *dishonestPortableProvider) RemoveBlockingDependency(ctx context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	if err := provider.probe(ctx, "dependency-remove"); err != nil {
		return tracker.DependencyMutation{}, err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for index, edge := range provider.edges {
		if edge == request.Dependency {
			provider.edges = append(provider.edges[:index], provider.edges[index+1:]...)
			provider.mutations++
			break
		}
	}
	observation, _ := tracker.NewProviderObservation(provider.fixture.reference.ExternalID, []byte("dependency-remove"), tracker.Revision(3), "", "dependency-3", time.Now().UTC())
	return tracker.DependencyMutation{Dependency: request.Dependency, Observation: observation}, nil
}
func (provider *dishonestPortableProvider) ListDependencies(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DependencyRef, error) {
	if err := provider.probe(ctx, "dependency-list"); err != nil {
		return nil, err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]tracker.DependencyRef(nil), provider.edges...), nil
}
func (provider *dishonestPortableProvider) ReadyProjection(ctx context.Context, request tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	if err := provider.probe(ctx, "ready"); err != nil {
		return tracker.ReadyProjection{}, err
	}
	observation, _ := tracker.NewProviderObservation(provider.fixture.reference.ExternalID, []byte("ready"), tracker.Revision(1), "", "ready-1", time.Now().UTC())
	return tracker.ReadyProjection{ProviderReadyIDs: append([]ids.WorkID(nil), request.PinnedReadyIDs...), Observation: observation}, nil
}
func (provider *dishonestPortableProvider) AppendDiscussion(ctx context.Context, request tracker.AppendDiscussionRequest) (tracker.DiscussionMutation, error) {
	if err := provider.probe(ctx, "discussion-append"); err != nil {
		return tracker.DiscussionMutation{}, err
	}
	return provider.semantic.AppendDiscussion(ctx, request)
}
func (provider *dishonestPortableProvider) ListDiscussion(ctx context.Context, reference tracker.ExternalWorkRef) ([]tracker.DiscussionEntry, error) {
	if err := provider.probe(ctx, "discussion-list"); err != nil {
		return nil, err
	}
	return provider.semantic.ListDiscussion(ctx, reference)
}
func (provider *dishonestPortableProvider) ObserveAssignment(ctx context.Context, reference tracker.ExternalWorkRef) (tracker.AssignmentObservation, error) {
	if err := provider.probe(ctx, "assignment-observe"); err != nil {
		return tracker.AssignmentObservation{}, err
	}
	return provider.semantic.ObserveAssignment(ctx, reference)
}
func (provider *dishonestPortableProvider) ProjectAssignment(ctx context.Context, request tracker.ProjectAssignmentRequest) (tracker.AssignmentMutation, error) {
	if err := provider.probe(ctx, "assignment-project"); err != nil {
		return tracker.AssignmentMutation{}, err
	}
	return provider.semantic.ProjectAssignment(ctx, request)
}
func (provider *dishonestPortableProvider) CompensateAssignment(ctx context.Context, request tracker.CompensateAssignmentRequest) (tracker.AssignmentMutation, error) {
	if err := provider.probe(ctx, "assignment-compensate"); err != nil {
		return tracker.AssignmentMutation{}, err
	}
	actual, err := provider.semantic.ObserveAssignment(ctx, request.ExternalRef)
	if err != nil {
		return tracker.AssignmentMutation{}, err
	}
	return tracker.AssignmentMutation{Observation: actual, Guarantee: tracker.AssignmentUnguarded, Noop: true}, &tracker.Error{Kind: tracker.ErrorSafeCompensationUnsupported, Capability: tracker.CapabilitySafeCompensation, RecoveryCode: "reconcile_without_mutation", Message: "provider cannot conditionally compensate assignment"}
}

func (provider *dishonestPortableProvider) mutationCount(ctx context.Context) (int, error) {
	history, err := provider.semanticStoreHistory(ctx)
	if err != nil {
		return 0, err
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(history) + provider.mutations, nil
}

func (provider *dishonestPortableProvider) semanticStoreHistory(ctx context.Context) ([]fake.HistoryEntry, error) {
	return provider.store.History(ctx)
}

func portableDishonestRecord(reference tracker.ExternalWorkRef, title, description, design string, criteria []tracker.Criterion, revision tracker.Revision) (tracker.WorkRecord, error) {
	observation, err := tracker.NewProviderObservation(reference.ExternalID, []byte(fmt.Sprintf("%s\x00%d", reference.ExternalID, revision)), revision, "", fmt.Sprintf("revision-%d", revision), time.Now().UTC())
	if err != nil {
		return tracker.WorkRecord{}, err
	}
	reference.LastObservation = revision
	return tracker.NewWorkRecord(reference, title, description, design, criteria, nil, "", "", nil, observation)
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
			if family == "apply-timeout" {
				switch caseID {
				case "apply-timeout.create":
					runCatalogCreateApplyTimeout(t, executable)
				case "apply-timeout.catalog-update":
					runCatalogUpdateApplyTimeout(t, executable)
				case "apply-timeout.dependency":
					runDependencyApplyTimeout(t, executable)
				case "apply-timeout.comment":
					runCommentApplyTimeout(t, executable)
				case "apply-timeout.assignment":
					runAssignmentApplyTimeout(t, executable)
				default:
					t.Fatalf("unhandled apply-timeout case %s", caseID)
				}
			} else {
				runNamedReadFault(t, executable, caseID)
			}
			operation, diagnostic, recovery, mutation, retry := faultEvidenceSummary(caseID)
			result := conformance.Result{ID: caseID, Disposition: conformance.Passed, CapabilityDisposition: "supported", Operation: operation, DiagnosticKind: diagnostic, RecoveryCode: recovery, MutationOutcome: mutation, RetrySafety: retry}
			if faultRequiresAuthority(caseID) {
				result.UniversalRequired, result.UniversalStarted = true, true
				result.StaleProbeRequired, result.StaleProbeStarted = true, true
				state := quickStaleFaultAuthority(t, authorityFixture, staleAuthority, "WP08."+caseID)
				if err := auditBeadsFault(context.Background(), state); err != nil {
					t.Fatal(err)
				}
				result.UniversalPassed, result.StaleProbePassed = true, true
			}
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

func faultRequiresAuthority(caseID string) bool {
	family, _, _ := strings.Cut(caseID, ".")
	return family != "malformed"
}

func faultEvidenceSummary(caseID string) (operation, diagnostic, recovery, mutation, retry string) {
	operation, mutation = "catalog_read", "none"
	if strings.HasPrefix(caseID, "apply-timeout.") {
		operation = strings.TrimPrefix(caseID, "apply-timeout.")
		return operation, string(tracker.ErrorAmbiguous), "observe_then_reconcile", "applied_before_response_loss", "observe_before_retry"
	}
	switch caseID {
	case "slow.deadline-before-apply", "slow.ignored-cancellation-process-kill":
		return operation, string(tracker.ErrorTimeout), "observe_then_reconcile", "not_applied", "observe_before_retry"
	case "unavailable.spawn-failure":
		return operation, string(tracker.ErrorUnavailable), "repair_provider_binding", mutation, "repair_then_retry"
	case "unavailable.documented-json-error", "unavailable.workspace-lock":
		return operation, string(tracker.ErrorLockContended), "repair_provider_state", mutation, "repair_then_retry"
	case "malformed.future-schema":
		return operation, string(tracker.ErrorSchemaSkew), "install_supported_provider", mutation, "repair_then_retry"
	case "malformed.empty", "malformed.truncated", "malformed.oversized", "malformed.multiple-values":
		return operation, string(tracker.ErrorMalformedProtocol), "repair_provider_binding", mutation, "repair_then_retry"
	case "malformed.wrong-payload-shape", "wrong.duplicate-match":
		return operation, string(tracker.ErrorMalformedProtocol), "reconcile_provider_metadata", mutation, "reconcile"
	case "stale.mapping", "wrong.foreign-project-work-mapping":
		return operation, string(tracker.ErrorRevisionConflict), "reconcile_mapping", mutation, "reconcile"
	default:
		return operation, "", "", mutation, "safe"
	}
}

func runNamedReadFault(t *testing.T, executable, caseID string) {
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
		return
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
	ctx := context.Background()
	cancel := func() {}
	if step.WaitFor != "" {
		ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
	}
	record, err := fixture.adapter.ReadWork(ctx, fixture.reference)
	cancel()
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
	ctx, cancel := boundedContext(t, 75*time.Millisecond)
	defer cancel()
	_, err := fixture.adapter.ReadWork(ctx, fixture.reference)
	expectTrackerFault(t, err, tracker.ErrorTimeout, "observe_then_reconcile")
	assertDishonestConsumed(t, fixture.root, 1)
}

func TestBeadsDishonestCatalogCreateApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runCatalogCreateApplyTimeout(t, buildDishonestExecutable(t))
}

func runCatalogCreateApplyTimeout(t *testing.T, executable string) {
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
	ctx, cancel := boundedContext(t, 100*time.Millisecond)
	defer cancel()
	_, err := fixture.adapter.CreateWork(ctx, tracker.CreateWorkRequest{ProjectID: "project-fault", WorkID: "work-timeout", OperationID: "operation-create-timeout", Title: "portable timeout", MetadataSchema: 1})
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
	rawCtx, rawCancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer rawCancel()
	command := execCommandContext(rawCtx, raw.executable, "update", "bd-fault", "--claim", "--json")
	command.Dir = raw.root
	command.Env = []string{"BEADS_DIR=" + raw.root, "BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1"}
	_ = command.Run()
	rawState := assertDishonestConsumed(t, raw.root, 1)
	if rawState.State["applied"] != "true" {
		t.Fatalf("apply-before-loss absent: %+v", rawState.State)
	}
}

func TestBeadsDishonestAssignmentApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runAssignmentApplyTimeout(t, buildDishonestExecutable(t))
}

func runAssignmentApplyTimeout(t *testing.T, executable string) {
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
	ctx, cancel := boundedContext(t, 100*time.Millisecond)
	_, err = fixture.adapter.ProjectAssignment(ctx, tracker.ProjectAssignmentRequest{ExternalRef: fixture.reference, OperationID: operation, Assignee: "actor-timeout", ExpectedObservation: before.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded})
	cancel()
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
}

func TestBeadsDishonestCommentApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runCommentApplyTimeout(t, buildDishonestExecutable(t))
}

func runCommentApplyTimeout(t *testing.T, executable string) {
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
	ctx, cancel := boundedContext(t, 100*time.Millisecond)
	_, err = fixture.adapter.AppendDiscussion(ctx, tracker.AppendDiscussionRequest{ExternalRef: fixture.reference, OperationID: operation, Author: "actor-timeout", Body: "portable body", ExpectedObservation: record.Observation.Revision})
	cancel()
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
}

func TestBeadsDishonestDependencyApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runDependencyApplyTimeout(t, buildDishonestExecutable(t))
}

func runDependencyApplyTimeout(t *testing.T, executable string) {
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
	ctx, cancel := boundedContext(t, 100*time.Millisecond)
	_, err := fixture.adapter.PutBlockingDependency(ctx, tracker.DependencyRequest{ProjectID: "project-fault", OperationID: "operation-dependency-timeout", Dependency: edge})
	cancel()
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
}

func TestBeadsDishonestCatalogUpdateApplyTimeoutObservedBeforeRetry(t *testing.T) {
	runCatalogUpdateApplyTimeout(t, buildDishonestExecutable(t))
}

func runCatalogUpdateApplyTimeout(t *testing.T, executable string) {
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
	ctx, cancel := boundedContext(t, 100*time.Millisecond)
	_, err = fixture.adapter.UpdateWork(ctx, tracker.UpdateWorkRequest{ExternalRef: fixture.reference, OperationID: operation, ExpectedObservation: before.Observation.Revision, RequiredGuard: tracker.UpdateUnguarded, Title: "portable update"})
	cancel()
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
			root := t.TempDir()
			store, err := fake.Open(filepath.Join(root, "tracker.json"))
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
			controlPath := filepath.Join(root, "control.db")
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
			authority := quickStaleFaultAuthority(t, authorityFixture, stale, "WP08."+caseID)
			if err := auditBeadsFault(context.Background(), authority); err != nil {
				t.Fatal(err)
			}
			idsTerminal = append(idsTerminal, caseID)
			idsPassed = append(idsPassed, caseID)
			results = append(results, map[string]any{"id": caseID, "outcome": string(effect.CompensationStatus), "expected_revision": effect.AppliedRevision, "actual_revision": effect.ConflictRevision, "expected_assignee": effect.AppliedValue, "actual_assignee": effect.ConflictValue, "expected_marker": effect.Marker, "actual_marker": effect.ConflictMarker, "compensation_mutations": 0, "human_preserved": true, "universal_required": true, "universal_started": true, "universal_passed": true, "stale_probe_required": true, "stale_probe_started": true, "stale_probe_passed": true})
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
	applicable := []string{"apply-timeout.assignment", "slow.deadline-before-apply", "stale.assignment-marker", "unavailable.provider", "wrong.unexpected-assignee"}
	for _, fault := range applicable {
		fault := fault
		t.Run(fault, func(t *testing.T) {
			state := staleFaultAuthority(t, fixture, stale, "WP08."+fault)
			if err := auditBeadsFault(ctx, state); err != nil {
				t.Fatal(err)
			}
		})
	}

	base := staleFaultAuthority(t, fixture, stale, "WP08.mutant")
	mutants := []struct {
		name   string
		mutate func(*universalAssertionState)
	}{
		{"missing-callback", func(state *universalAssertionState) { state.StaleProbe = nil }},
		{"requirement-disabled", func(state *universalAssertionState) { state.RequireStaleProbe = false; state.StaleProbe = nil }},
		{"stale-succeeds", func(state *universalAssertionState) { state.StaleProbe = func(context.Context) error { return nil } }},
	}
	for _, mutant := range mutants {
		mutant := mutant
		t.Run("mutant-"+mutant.name, func(t *testing.T) {
			state := base
			mutant.mutate(&state)
			err := auditBeadsFault(ctx, state)
			if err == nil || !strings.Contains(err.Error(), "stale_token_refusal") {
				t.Fatalf("mutant error=%v", err)
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
