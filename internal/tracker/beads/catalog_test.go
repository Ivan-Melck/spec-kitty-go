package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type storedCatalogIssue struct {
	ID          string
	Title       string
	Description string
	Acceptance  string
	Metadata    json.RawMessage
	Assignee    string
	UpdatedAt   string
}

func (issue storedCatalogIssue) wire() map[string]any {
	return map[string]any{
		"id": issue.ID, "title": issue.Title, "description": issue.Description,
		"acceptance_criteria": issue.Acceptance, "metadata": issue.Metadata, "assignee": issue.Assignee,
		"updated_at": issue.UpdatedAt, "dependency_count": 0, "comment_count": 0,
		"future_optional": map[string]any{"ignored": true},
	}
}

type catalogRunner struct {
	mu                   sync.Mutex
	workspace            string
	databaseID           string
	issues               map[string]storedCatalogIssue
	edges                []observationDependency
	calls                []CommandKind
	argvs                [][]string
	nextID               uint64
	clock                uint64
	ambiguousKind        CommandKind
	applyBeforeAmbiguous bool
	afterCreate          func(*storedCatalogIssue)
	duplicateListID      string
	mode                 tracker.ConcurrencyMode
	serverID             string
	serverEndpoint       string
}

func (runner *catalogRunner) run(_ context.Context, request command) commandResult {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, request.kind)
	runner.argvs = append(runner.argvs, request.renderedArgv())
	result := commandResult{kind: request.kind, started: true, mutationOutcome: mutationUnknown}
	zero := 0
	result.exitCode = &zero
	var data any
	switch request.kind {
	case CommandVersion:
		data = map[string]any{"version": SupportedBeadsVersion, "build": "catalog-test"}
	case CommandWhere:
		data = map[string]any{"path": runner.workspace, "prefix": runner.databaseID, "database_path": filepath.Join(runner.workspace, "embeddeddolt")}
	case CommandDoltShow:
		if runner.mode == tracker.ConcurrencyServerMultiwriter {
			data = map[string]any{"backend": "dolt", "database": runner.databaseID, "embedded": false, "host": "127.0.0.1", "port": 3306, "user": "dolt", "shared_server": true, "connection_ok": true}
		} else {
			data = map[string]any{"backend": "dolt", "data_dir": filepath.Join(runner.workspace, "embeddeddolt"), "database": runner.databaseID, "embedded": true}
		}
	case CommandDoltStatus:
		if runner.mode == tracker.ConcurrencyServerMultiwriter {
			data = map[string]any{"mode": "external", "host": "127.0.0.1", "port": 3306, "user": "dolt", "database": runner.databaseID, "tls": false, "running": true, "version": "1.70.0"}
		} else {
			data = map[string]any{"data_dir": filepath.Join(runner.workspace, "embeddeddolt"), "data_dir_exists": true, "mode": "embedded", "server_running": false}
		}
	case CommandDoltAutoStart:
		data = map[string]any{"key": "dolt.auto-start", "value": "false", "location": "config.yaml"}
	case CommandList:
		ids := make([]string, 0, len(runner.issues))
		for id := range runner.issues {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		items := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			items = append(items, runner.issues[id].wire())
			if id == runner.duplicateListID {
				items = append(items, runner.issues[id].wire())
			}
		}
		data = items
	case CommandShow:
		id := request.argv[1]
		issue, ok := runner.issues[id]
		if !ok {
			return providerFailureResult(request.kind, "not_found")
		}
		data = []map[string]any{issue.wire()}
	case CommandDependencyList:
		blocked := request.argv[2]
		edges := make([]map[string]any, 0)
		for _, edge := range runner.edges {
			if edge.Blocked == blocked {
				edges = append(edges, map[string]any{"id": edge.Blocking, "dependency_type": edge.Kind, "edge_id": edge.EdgeID})
			}
		}
		data = edges
	case CommandDependencyAdd:
		runner.edges = append(runner.edges, observationDependency{Blocked: request.argv[2], Blocking: request.argv[3], Kind: "blocks", EdgeID: fmt.Sprintf("edge-%d", len(runner.edges)+1)})
		data = map[string]any{"status": "added", "issue_id": request.argv[2], "depends_on_id": request.argv[3], "type": "blocks"}
		result.mutationOutcome = mutationApplied
	case CommandCreate:
		if runner.ambiguousKind == CommandCreate && !runner.applyBeforeAmbiguous {
			runner.ambiguousKind = ""
			return ambiguousResult(request.kind)
		}
		issue := runner.createIssue(request.argv)
		data = issue.wire()
		if runner.afterCreate != nil {
			runner.afterCreate(&issue)
			runner.issues[issue.ID] = issue
		}
		result.mutationOutcome = mutationApplied
		if runner.ambiguousKind == CommandCreate {
			runner.ambiguousKind = ""
			return ambiguousResult(request.kind)
		}
	case CommandUpdate:
		if runner.ambiguousKind == CommandUpdate && !runner.applyBeforeAmbiguous {
			runner.ambiguousKind = ""
			return ambiguousResult(request.kind)
		}
		id := request.argv[1]
		issue, ok := runner.issues[id]
		if !ok {
			return providerFailureResult(request.kind, "not_found")
		}
		applyUpdateArgv(&issue, request.argv)
		issue.UpdatedAt = runner.tick()
		runner.issues[id] = issue
		data = []map[string]any{issue.wire()}
		result.mutationOutcome = mutationApplied
		if runner.ambiguousKind == CommandUpdate {
			runner.ambiguousKind = ""
			return ambiguousResult(request.kind)
		}
	default:
		data = map[string]any{"ok": true}
	}
	payload, _ := json.Marshal(map[string]any{"schema_version": 1, "data": data})
	result.stdout = payload
	return result
}

func (runner *catalogRunner) createIssue(argv []string) storedCatalogIssue {
	runner.nextID++
	id := fmt.Sprintf("bd-%016x", runner.nextID*0x9e3779b97f4a7c15)
	issue := storedCatalogIssue{ID: id, UpdatedAt: runner.tick()}
	applyUpdateArgv(&issue, argv)
	runner.issues[id] = issue
	return issue
}

func (runner *catalogRunner) tick() string {
	runner.clock++
	return time.Unix(1_800_000_000, int64(runner.clock)).UTC().Format(time.RFC3339Nano)
}

func applyUpdateArgv(issue *storedCatalogIssue, argv []string) {
	for index := 0; index+1 < len(argv); index++ {
		switch argv[index] {
		case "--title":
			issue.Title = argv[index+1]
		case "--description":
			issue.Description = argv[index+1]
		case "--acceptance":
			issue.Acceptance = argv[index+1]
		case "--metadata":
			issue.Metadata = append(json.RawMessage(nil), argv[index+1]...)
		case "--assignee":
			issue.Assignee = argv[index+1]
		}
	}
}

func ambiguousResult(kind CommandKind) commandResult {
	return commandResult{kind: kind, started: true, cancelCause: context.DeadlineExceeded, mutationOutcome: mutationAmbiguous}
}

func providerFailureResult(kind CommandKind, code string) commandResult {
	exit := 7
	return commandResult{
		kind: kind, started: true, exitCode: &exit, mutationOutcome: mutationNotApplied,
		stdout: []byte(fmt.Sprintf(`{"schema_version":1,"error":"provider refusal","code":%q}`, code)),
		stderr: []byte("human provider refusal"),
	}
}

func (runner *catalogRunner) snapshotCalls() []CommandKind {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]CommandKind(nil), runner.calls...)
}

func (runner *catalogRunner) mutationCount() int {
	count := 0
	for _, kind := range runner.snapshotCalls() {
		if kind == CommandCreate || kind == CommandUpdate {
			count++
		}
	}
	return count
}

func (runner *catalogRunner) issueCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return len(runner.issues)
}

type serialCatalogLocker struct{ mutex sync.Mutex }
type serialCatalogLock struct{ mutex *sync.Mutex }

func (locker *serialCatalogLocker) acquire(context.Context, tracker.TrackerBinding) (acquiredWriterLock, error) {
	locker.mutex.Lock()
	return &serialCatalogLock{mutex: &locker.mutex}, nil
}
func (lock *serialCatalogLock) release() { lock.mutex.Unlock() }

func newCatalogTestAdapter(t *testing.T) (*BoundAdapter, *catalogRunner) {
	return newCatalogTestAdapterMode(t, tracker.ConcurrencyEmbeddedSerialized)
}

func newCatalogTestAdapterMode(t *testing.T, mode tracker.ConcurrencyMode) (*BoundAdapter, *catalogRunner) {
	t.Helper()
	bound, inspection := testBoundAdapter(t, mode, "catalog-db")
	runner := &catalogRunner{
		workspace: inspection.workspace, databaseID: "catalog-db", issues: make(map[string]storedCatalogIssue), mode: mode,
		serverID: "server-1", serverEndpoint: "127.0.0.1:3306",
	}
	bound.adapter = newAdapter(runner)
	bound.locker = &serialCatalogLocker{}
	return bound, runner
}

type mappingBarrierLocker struct {
	guard      sync.Mutex
	held       sync.Mutex
	attempts   int
	identities []string
	ready      chan struct{}
}

func newMappingBarrierLocker() *mappingBarrierLocker {
	return &mappingBarrierLocker{ready: make(chan struct{})}
}

func (locker *mappingBarrierLocker) acquire(_ context.Context, binding tracker.TrackerBinding) (acquiredWriterLock, error) {
	locker.guard.Lock()
	locker.attempts++
	locker.identities = append(locker.identities, binding.WorkspaceIdentity)
	if locker.attempts == 2 {
		close(locker.ready)
	}
	ready := locker.ready
	locker.guard.Unlock()
	<-ready
	locker.held.Lock()
	return &serialCatalogLock{mutex: &locker.held}, nil
}

func (locker *mappingBarrierLocker) snapshot() (int, []string) {
	locker.guard.Lock()
	defer locker.guard.Unlock()
	return locker.attempts, append([]string(nil), locker.identities...)
}

func catalogCriterion() []tracker.Criterion {
	return []tracker.Criterion{{ID: "criterion-1", Text: "the behavior is verified", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "focused test"}}
}

const realV122CatalogRecord = `{"id":"skt-a1b2","title":"Real catalog","description":"Provider shape","acceptance_criteria":"criterion-1 — required — test — verified","metadata":{"foreign":{"number":9007199254740993},"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[{"id":"criterion-1","text":"verified","verifier":"test","required":true}],"design_context":"real provider","content_digest":"digest"}},"assignee":"","updated_at":"2026-08-26T12:00:00Z","dependency_count":0,"comment_count":0,"future_optional":{"ignored":true}}`

const realV122CatalogSingularArray = `[` + realV122CatalogRecord + `]`

func TestRealV122CatalogPayloadShapesAndSingularCardinality(t *testing.T) {
	id, err := decodeMutationExternalID([]byte(realV122CatalogRecord))
	if err != nil || id != "skt-a1b2" {
		t.Fatalf("create object id=%q error=%v", id, err)
	}
	for _, operation := range []string{"show", "update"} {
		t.Run(operation, func(t *testing.T) {
			issue, err := decodeSingularProviderIssue([]byte(realV122CatalogSingularArray))
			if err != nil {
				t.Fatal(err)
			}
			if issue.externalID != "skt-a1b2" || pointerValue(issue.acceptance) != "criterion-1 — required — test — verified" {
				t.Fatalf("issue=%#v", issue)
			}
			if !bytes.Contains(issue.metadata, []byte(`9007199254740993`)) {
				t.Fatalf("lossless foreign number missing from metadata: %s", issue.metadata)
			}
		})
	}
	for _, payload := range []string{realV122CatalogRecord, `[]`, `[` + realV122CatalogRecord + `,` + realV122CatalogRecord + `]`} {
		if _, err := decodeSingularProviderIssue([]byte(payload)); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
			t.Fatalf("singular mutant %s error=%v", payload, err)
		}
	}
	legacy := strings.Replace(realV122CatalogRecord, `"acceptance_criteria"`, `"acceptance"`, 1)
	issue, err := decodeProviderIssue([]byte(legacy))
	if err != nil || issue.acceptance != nil {
		t.Fatalf("legacy acceptance field became provider state: issue=%#v error=%v", issue, err)
	}
	if _, err := decodeMutationExternalID([]byte(realV122CatalogSingularArray)); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
		t.Fatalf("create accepted array payload: %v", err)
	}
}

func createRequest(index int) tracker.CreateWorkRequest {
	return tracker.CreateWorkRequest{
		ProjectID: "project-1", WorkID: ids.WorkID(fmt.Sprintf("work-%d", index)), OperationID: ids.OperationID(fmt.Sprintf("operation-create-%d", index)),
		Title: fmt.Sprintf("Work %d", index), Description: "description", DesignContext: "design", Criteria: catalogCriterion(), MetadataSchema: 1,
	}
}

func completeDefinition(t *testing.T, workID ids.WorkID) tracker.WorkDefinition {
	t.Helper()
	metadata, err := tracker.NewOpaqueMetadata([]byte(`{"foreign":{"keep":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project-1", WorkID: workID, Title: "Complete " + workID.String(), Description: "Provider-neutral description.",
		DesignContext: "Provider-neutral design context.", CrossDomainReason: "Shared graph policy requires coordination.",
		Criteria:       []tracker.WorkCriterion{{ID: "criterion-1", Text: "The behavior is verified", Outcome: "focused tests pass", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./..."}},
		WriteScopes:    []tracker.WriteScope{{Path: "internal/tracker/beads", Domain: "tracker", Access: tracker.ScopeWriteExclusive, SerializationKey: "beads-adapter"}},
		Provenance:     []tracker.ScopeRef{{ID: "plan-provider", Kind: tracker.ScopePlanConcern}, {ID: "requirement-parity", Kind: tracker.ScopeRequiredOutcome}},
		MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, ProviderMetadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestCatalogProjectsCompleteProviderNeutralDefinitionDefensively(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	definition := completeDefinition(t, "work-1")
	metadata, err := mergeMetadata(definition.ProviderMetadata().Bytes(), metadataFromDefinition(definition, "operation-create-definition", "operation-create-definition"))
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.issues["bd-definition"] = storedCatalogIssue{ID: "bd-definition", Title: definition.Title(), Description: definition.Description(), Acceptance: "human prose must not be parsed", Metadata: metadata, UpdatedAt: runner.tick()}
	runner.mu.Unlock()
	reference := tracker.ExternalWorkRef{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Provider: beadsProvider, ExternalID: "bd-definition", MetadataSchema: tracker.WorkDefinitionMetadataSchemaV1, CreatedByOperation: "operation-create-definition", LastObservation: 1}
	observed, err := bound.ObserveDefinition(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(observed.Definition().CanonicalData(), definition.CanonicalData()) || observed.ExternalRef().ExternalID != "bd-definition" || observed.ProviderObservation().Revision == 0 {
		t.Fatalf("observation=%#v definition=%s", observed, observed.Definition().CanonicalData())
	}
	criteria := observed.Definition().Criteria()
	criteria[0].Outcome = "caller mutation"
	scopes := observed.Definition().WriteScopes()
	scopes[0].Path = "caller/mutation"
	if bytes.Equal(observed.Definition().CanonicalData(), tracker.WorkDefinition{}.CanonicalData()) || observed.Definition().Criteria()[0].Outcome != "focused tests pass" || observed.Definition().WriteScopes()[0].Path != "internal/tracker/beads" {
		t.Fatal("observation exposed provider-backed definition slices")
	}

	runner.mu.Lock()
	issue := runner.issues["bd-definition"]
	issue.Acceptance = "different human prose at the same timestamp"
	runner.issues[issue.ID] = issue
	runner.mu.Unlock()
	after, err := bound.ObserveDefinition(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after.Definition().CanonicalData(), definition.CanonicalData()) || after.ProviderObservation().Revision == observed.ProviderObservation().Revision {
		t.Fatalf("presentation prose became authority or escaped observation identity: before=%d after=%d", observed.ProviderObservation().Revision, after.ProviderObservation().Revision)
	}
}

func TestCatalogCreatesCompleteDefinitionAndHonestlyRefusesGuardedUpdate(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	definition := completeDefinition(t, "work-create-definition")
	request := tracker.CreateWorkDefinitionRequest{OperationID: "operation-create-definition", Definition: definition}
	created, err := bound.CreateDefinition(context.Background(), request)
	if err != nil || created.Replayed || !bytes.Equal(created.Observation.Definition().CanonicalData(), definition.CanonicalData()) {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	replayed, err := bound.CreateDefinition(context.Background(), request)
	if err != nil || !replayed.Replayed || runner.mutationCount() != 1 {
		t.Fatalf("replayed=%#v err=%v mutations=%d", replayed, err, runner.mutationCount())
	}
	capabilities, err := bound.DefinitionCapabilities(context.Background())
	if err != nil || !capabilities.Create || capabilities.GuardedUpdate || !capabilities.BlockingGraph {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	before := runner.mutationCount()
	if _, err := bound.UpdateDefinition(context.Background(), tracker.UpdateWorkDefinitionRequest{OperationID: "operation-update-definition", ExternalRef: created.Observation.ExternalRef(), ExpectedObservation: created.Observation.ProviderObservation().Revision, Definition: definition}); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) || runner.mutationCount() != before {
		t.Fatalf("guarded update err=%v mutations=%d", err, runner.mutationCount())
	}
	legacyCriteria := make([]tracker.Criterion, len(definition.Criteria()))
	for index, criterion := range definition.Criteria() {
		legacyCriteria[index] = tracker.Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}
	}
	if _, err := bound.UpdateWork(context.Background(), tracker.UpdateWorkRequest{ExternalRef: created.Observation.ExternalRef(), OperationID: "operation-legacy-update-definition", ExpectedObservation: created.Observation.ProviderObservation().Revision, RequiredGuard: tracker.UpdateUnguarded, Title: definition.Title(), Description: "must not partially replace a complete definition", DesignContext: definition.DesignContext(), Criteria: legacyCriteria}); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) || runner.mutationCount() != before {
		t.Fatalf("legacy complete-definition update err=%v mutations=%d", err, runner.mutationCount())
	}
}

func TestCatalogCreateDefinitionPersistsBlockingGraphAndReplaysEveryChild(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	foundation := completeDefinition(t, "work-foundation")
	if _, err := bound.CreateDefinition(context.Background(), tracker.CreateWorkDefinitionRequest{OperationID: "operation-foundation", Definition: foundation}); err != nil {
		t.Fatal(err)
	}
	featureBase := completeDefinition(t, "work-feature")
	feature, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: featureBase.ProjectID(), WorkID: featureBase.WorkID(), Title: featureBase.Title(), Description: featureBase.Description(), DesignContext: featureBase.DesignContext(),
		CrossDomainReason: featureBase.CrossDomainReason(), Criteria: featureBase.Criteria(), Dependencies: []ids.WorkID{"work-foundation"}, WriteScopes: featureBase.WriteScopes(), Provenance: featureBase.Provenance(), MetadataSchema: featureBase.MetadataSchema(), ProviderMetadata: featureBase.ProviderMetadata(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := tracker.CreateWorkDefinitionRequest{OperationID: "operation-feature", Definition: feature}
	created, err := bound.CreateDefinition(context.Background(), request)
	if err != nil || created.Replayed || !bytes.Equal(created.Observation.Definition().CanonicalData(), feature.CanonicalData()) {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	replay, err := bound.CreateDefinition(context.Background(), request)
	if err != nil || !replay.Replayed || len(runner.edges) != 1 {
		t.Fatalf("replay=%#v err=%v edges=%#v", replay, err, runner.edges)
	}
	observed, err := bound.ObserveDefinitionCatalog(context.Background(), "project-1")
	if err != nil || len(observed) != 2 || observed[0].Definition().WorkID() != "work-feature" && observed[1].Definition().WorkID() != "work-feature" {
		t.Fatalf("catalog=%#v err=%v", observed, err)
	}
}

func TestCatalogLegacyDefinitionObservationFailsClosedWithoutMutation(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	request := createRequest(1)
	created, err := bound.CreateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	before := runner.mutationCount()
	if _, err := bound.ObserveDefinition(context.Background(), created.Record.ExternalRef); !tracker.IsKind(err, tracker.ErrorSchemaSkew) || runner.mutationCount() != before {
		t.Fatalf("legacy observation error=%v mutations=%d", err, runner.mutationCount())
	}
}

func updateRequest(record tracker.WorkRecord, operation string) tracker.UpdateWorkRequest {
	return tracker.UpdateWorkRequest{
		ExternalRef: record.ExternalRef, OperationID: ids.OperationID(operation), ExpectedObservation: record.Observation.Revision,
		RequiredGuard: tracker.UpdateUnguarded, Title: record.Title + " updated", Description: record.Description + " updated",
		DesignContext: record.DesignContext + " updated", Criteria: catalogCriterion(),
	}
}

func TestCatalogCreateReadReplayAndChangedReplay(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	request := createRequest(1)
	created, err := bound.CreateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Replayed || created.Record.ExternalRef.WorkID != request.WorkID || created.Record.ExternalRef.ExternalID == "" || created.Record.Observation.Revision == 0 || len(created.Record.Observation.Digest) != 64 {
		t.Fatalf("created=%#v", created)
	}
	if got, want := runner.snapshotCalls(), []CommandKind{CommandVersion, CommandWhere, CommandDoltShow, CommandDoltStatus, CommandList, CommandCreate, CommandShow}; !reflect.DeepEqual(got, want) {
		t.Fatalf("create command order=%v want=%v", got, want)
	}
	read, err := bound.ReadWork(context.Background(), created.Record.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	if read.Title != request.Title || read.Description != request.Description || read.DesignContext != request.DesignContext || len(read.Criteria()) != 1 || read.Observation.Revision != created.Record.Observation.Revision {
		t.Fatalf("read=%#v criteria=%#v", read, read.Criteria())
	}
	replayed, err := bound.CreateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.Replayed || !replayed.Noop || replayed.Record.ExternalRef.ExternalID != created.Record.ExternalRef.ExternalID || runner.issueCount() != 1 {
		t.Fatalf("replay=%#v count=%d", replayed, runner.issueCount())
	}
	request.Title = "changed replay"
	if _, err := bound.CreateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorInvalidRequest) || runner.issueCount() != 1 {
		t.Fatalf("changed replay error=%v count=%d", err, runner.issueCount())
	}
	if got := countKind(runner.snapshotCalls(), CommandCreate); got != 1 {
		t.Fatalf("create commands=%d calls=%v", got, runner.snapshotCalls())
	}
}

func TestCatalogCapabilitiesDescribeImplementedGuarantees(t *testing.T) {
	bound, _ := newCatalogTestAdapter(t)
	capabilities, err := bound.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.CatalogRead != tracker.SupportSupported || capabilities.CatalogCreate != tracker.SupportSupported ||
		capabilities.CatalogUpdate != tracker.UpdateUnguarded || capabilities.ObservationRevision != tracker.ObservationRevisionContentDerived ||
		capabilities.ConcurrentWriters != tracker.ConcurrentWritersAdapterSerialized || capabilities.SafeCompensation != tracker.CompensationUnsupported ||
		capabilities.BlockingGraph != tracker.SupportSupported || capabilities.Discussion != tracker.DiscussionAppendOnly {
		t.Fatalf("capabilities=%#v", capabilities)
	}
}

func TestCatalogDuplicateMappingIsAConflictWithoutSecondMutation(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	request := createRequest(1)
	created, err := bound.CreateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	duplicate := runner.issues[created.Record.ExternalRef.ExternalID]
	duplicate.ID = "bd-duplicate"
	runner.issues[duplicate.ID] = duplicate
	runner.mu.Unlock()
	before := runner.mutationCount()
	if _, err := bound.CreateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.mutationCount() != before {
		t.Fatalf("error=%v mutations=%d", err, runner.mutationCount())
	}
}

func TestCatalogByteIdenticalDuplicateListEntryIsAConflict(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	created, err := bound.CreateWork(context.Background(), createRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.duplicateListID = created.Record.ExternalRef.ExternalID
	runner.mu.Unlock()
	before := runner.mutationCount()
	if _, err := bound.ObserveDefinitionCatalog(context.Background(), created.Record.ExternalRef.ProjectID); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.mutationCount() != before {
		t.Fatalf("duplicate list error=%v mutations=%d", err, runner.mutationCount())
	}
}

func TestCatalogCreatePreservesCallerOwnedProviderMetadata(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	metadata, err := tracker.NewOpaqueMetadata([]byte(`{"human":{"nested":{"keep":true}},"provider_extension":[1,"two"]}`))
	if err != nil {
		t.Fatal(err)
	}
	request := createRequest(1)
	request.ProviderMetadata = metadata
	created, err := bound.CreateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	stored := runner.issues[created.Record.ExternalRef.ExternalID]
	runner.mu.Unlock()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(stored.Metadata, &root); err != nil {
		t.Fatal(err)
	}
	if string(root["human"]) != `{"nested":{"keep":true}}` || string(root["provider_extension"]) != `[1,"two"]` || len(root[metadataNamespace]) == 0 {
		t.Fatalf("metadata=%s", stored.Metadata)
	}
	owned, err := tracker.NewOpaqueMetadata([]byte(`{"spec_kitty":{"stolen":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	request = createRequest(2)
	request.ProviderMetadata = owned
	before := runner.mutationCount()
	if _, err := bound.CreateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorInvalidRequest) || runner.mutationCount() != before {
		t.Fatalf("namespace theft error=%v mutations=%d", err, runner.mutationCount())
	}
}

func TestCatalogConfirmedCreateRejectsDishonestLiveProviderState(t *testing.T) {
	metadata, err := tracker.NewOpaqueMetadata([]byte(`{"human":{"approval":"keep","sequence":9007199254740992},"provider_extension":[1,"two"]}`))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		corrupt func(*storedCatalogIssue)
	}{
		{name: "title", corrupt: func(issue *storedCatalogIssue) { issue.Title = "provider-stored-wrong-title" }},
		{name: "description", corrupt: func(issue *storedCatalogIssue) { issue.Description = "provider-stored-wrong-description" }},
		{name: "acceptance", corrupt: func(issue *storedCatalogIssue) { issue.Acceptance = "provider-stored-wrong-acceptance" }},
		{name: "design context", corrupt: corruptKittyMetadata(t, func(namespace map[string]json.RawMessage) {
			namespace["design_context"] = json.RawMessage(`"provider-stored-wrong-design"`)
		})},
		{name: "structured criteria", corrupt: corruptKittyMetadata(t, func(namespace map[string]json.RawMessage) {
			namespace["criteria"] = json.RawMessage(`[{"id":"criterion-1","text":"provider-stored-wrong-criterion","verifier":"test","required":true}]`)
		})},
		{name: "work mapping", corrupt: corruptKittyMetadata(t, func(namespace map[string]json.RawMessage) {
			namespace["work_id"] = json.RawMessage(`"work-other"`)
		})},
		{name: "project mapping", corrupt: corruptKittyMetadata(t, func(namespace map[string]json.RawMessage) {
			namespace["project_id"] = json.RawMessage(`"project-other"`)
		})},
		{name: "creation operation mapping", corrupt: corruptKittyMetadata(t, func(namespace map[string]json.RawMessage) {
			namespace["created_by_operation"] = json.RawMessage(`"operation-other"`)
		})},
		{name: "last operation provenance", corrupt: corruptKittyMetadata(t, func(namespace map[string]json.RawMessage) {
			namespace["last_operation"] = json.RawMessage(`"operation-other"`)
		})},
		{name: "caller metadata removed", corrupt: corruptRootMetadata(t, func(root map[string]json.RawMessage) {
			delete(root, "human")
		})},
		{name: "caller metadata changed", corrupt: corruptRootMetadata(t, func(root map[string]json.RawMessage) {
			root["human"] = json.RawMessage(`{"approval":"changed","sequence":9007199254740992}`)
		})},
		{name: "caller large integer changed", corrupt: corruptRootMetadata(t, func(root map[string]json.RawMessage) {
			root["human"] = json.RawMessage(`{"approval":"keep","sequence":9007199254740993}`)
		})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := newCatalogTestAdapter(t)
			runner.afterCreate = test.corrupt
			request := createRequest(1)
			request.ProviderMetadata = metadata
			_, err := bound.CreateWork(context.Background(), request)
			if !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.issueCount() != 1 || runner.mutationCount() != 1 {
				t.Fatalf("confirmed dishonest create error=%v issues=%d mutations=%d", err, runner.issueCount(), runner.mutationCount())
			}
		})
	}
}

func corruptRootMetadata(t *testing.T, corrupt func(map[string]json.RawMessage)) func(*storedCatalogIssue) {
	t.Helper()
	return func(issue *storedCatalogIssue) {
		var root map[string]json.RawMessage
		if err := json.Unmarshal(issue.Metadata, &root); err != nil {
			t.Fatalf("decode root metadata: %v", err)
		}
		corrupt(root)
		issue.Metadata, _ = json.Marshal(root)
	}
}

func corruptKittyMetadata(t *testing.T, corrupt func(map[string]json.RawMessage)) func(*storedCatalogIssue) {
	t.Helper()
	return corruptRootMetadata(t, func(root map[string]json.RawMessage) {
		var namespace map[string]json.RawMessage
		if err := json.Unmarshal(root[metadataNamespace], &namespace); err != nil {
			t.Fatalf("decode adapter metadata: %v", err)
		}
		corrupt(namespace)
		root[metadataNamespace], _ = json.Marshal(namespace)
	})
}

func TestSemanticJSONEqualIsLosslessAndStructural(t *testing.T) {
	tests := []struct {
		name        string
		left, right string
		equal       bool
	}{
		{name: "distinct integers beyond float precision", left: `9007199254740992`, right: `9007199254740993`, equal: false},
		{name: "equivalent map order", left: `{"a":1,"b":2}`, right: `{"b":2,"a":1}`, equal: true},
		{name: "nested structural equality", left: `{"a":[1,{"x":true,"y":null}],"b":{"n":1.25}}`, right: `{"b":{"n":1.2500},"a":[1e0,{"y":null,"x":true}]}`, equal: true},
		{name: "mathematically equal number forms", left: `1`, right: `1.0e0`, equal: true},
		{name: "large exponent stays bounded", left: `1e100000`, right: `10e99999`, equal: true},
		{name: "signed zero is equivalent", left: `-0.000e99999`, right: `0`, equal: true},
		{name: "array order remains significant", left: `[1,2]`, right: `[2,1]`, equal: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := semanticJSONEqual([]byte(test.left), []byte(test.right)); got != test.equal {
				t.Fatalf("semanticJSONEqual(%s, %s)=%t want=%t", test.left, test.right, got, test.equal)
			}
		})
	}
}

func TestCatalogReplayRefusesLiveProviderDrift(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		bound, runner := newCatalogTestAdapter(t)
		request := createRequest(1)
		created, err := bound.CreateWork(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		runner.mu.Lock()
		issue := runner.issues[created.Record.ExternalRef.ExternalID]
		issue.Title = "human drift"
		runner.issues[issue.ID] = issue
		runner.mu.Unlock()
		before := runner.mutationCount()
		if _, err := bound.CreateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.mutationCount() != before {
			t.Fatalf("error=%v mutations=%d", err, runner.mutationCount())
		}
	})

	t.Run("create caller large integer", func(t *testing.T) {
		bound, runner := newCatalogTestAdapter(t)
		metadata, err := tracker.NewOpaqueMetadata([]byte(`{"human":{"sequence":9007199254740992}}`))
		if err != nil {
			t.Fatal(err)
		}
		request := createRequest(1)
		request.ProviderMetadata = metadata
		created, err := bound.CreateWork(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		runner.mu.Lock()
		issue := runner.issues[created.Record.ExternalRef.ExternalID]
		corruptRootMetadata(t, func(root map[string]json.RawMessage) {
			root["human"] = json.RawMessage(`{"sequence":9007199254740993}`)
		})(&issue)
		runner.issues[issue.ID] = issue
		runner.mu.Unlock()
		before := runner.mutationCount()
		if _, err := bound.CreateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.mutationCount() != before {
			t.Fatalf("large-integer replay drift error=%v mutations=%d", err, runner.mutationCount())
		}
	})

	t.Run("update", func(t *testing.T) {
		bound, runner := newCatalogTestAdapter(t)
		created, err := bound.CreateWork(context.Background(), createRequest(1))
		if err != nil {
			t.Fatal(err)
		}
		request := updateRequest(created.Record, "operation-update")
		updated, err := bound.UpdateWork(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		runner.mu.Lock()
		issue := runner.issues[updated.Record.ExternalRef.ExternalID]
		issue.Acceptance = "human drift"
		runner.issues[issue.ID] = issue
		runner.mu.Unlock()
		drifted, err := bound.ReadWork(context.Background(), updated.Record.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		request.ExpectedObservation = drifted.Observation.Revision
		before := runner.mutationCount()
		if _, err := bound.UpdateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.mutationCount() != before {
			t.Fatalf("error=%v mutations=%d", err, runner.mutationCount())
		}
	})
}

func TestCatalogUpdateCanClearDescriptionAndAcceptanceAtomically(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	created, err := bound.CreateWork(context.Background(), createRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	request := updateRequest(created.Record, "operation-clear")
	request.Title = created.Record.Title
	request.Description = ""
	request.DesignContext = ""
	request.Criteria = nil
	mutation, err := bound.UpdateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.Record.Description != "" || mutation.Record.DesignContext != "" || len(mutation.Record.Criteria()) != 0 {
		t.Fatalf("record=%#v criteria=%#v", mutation.Record, mutation.Record.Criteria())
	}
	runner.mu.Lock()
	stored := runner.issues[created.Record.ExternalRef.ExternalID]
	runner.mu.Unlock()
	if stored.Description != "" || stored.Acceptance != "" {
		t.Fatalf("provider retained cleared values: %#v", stored)
	}
	var updateArgv []string
	for index, kind := range runner.snapshotCalls() {
		if kind == CommandUpdate {
			runner.mu.Lock()
			updateArgv = append([]string(nil), runner.argvs[index]...)
			runner.mu.Unlock()
		}
	}
	if !containsAdjacent(updateArgv, "--description", "") || !containsAdjacent(updateArgv, "--acceptance", "") {
		t.Fatalf("clear argv=%q", updateArgv)
	}
	document, err := decodeMetadata(stored.Metadata)
	if err != nil || document.kitty.LastOperation != "operation-clear" || len(document.kitty.Criteria) != 0 {
		t.Fatalf("metadata=%s document=%#v error=%v", stored.Metadata, document.kitty, err)
	}
}

func TestCatalogServerModeSerializesOnlyIdenticalStableMapping(t *testing.T) {
	bound, runner := newCatalogTestAdapterMode(t, tracker.ConcurrencyServerMultiwriter)
	locker := newMappingBarrierLocker()
	bound.locker = locker
	request := createRequest(1)
	start := make(chan struct{})
	results := make(chan tracker.WorkMutation, 2)
	errs := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			<-start
			mutation, err := bound.CreateWork(context.Background(), request)
			results <- mutation
			errs <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	firstErr, secondErr := <-errs, <-errs
	if firstErr != nil || secondErr != nil {
		t.Fatalf("errors=%v/%v", firstErr, secondErr)
	}
	if first.Replayed == second.Replayed || runner.issueCount() != 1 || runner.mutationCount() != 1 {
		t.Fatalf("first=%#v second=%#v issues=%d mutations=%d", first, second, runner.issueCount(), runner.mutationCount())
	}
	attempts, identities := locker.snapshot()
	if attempts != 2 || len(identities) != 2 || identities[0] != identities[1] || identities[0] == bound.binding.WorkspaceIdentity {
		t.Fatalf("mapping lock attempts=%d identities=%v base=%s", attempts, identities, bound.binding.WorkspaceIdentity)
	}
}

func containsAdjacent(values []string, left, right string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == left && values[index+1] == right {
			return true
		}
	}
	return false
}

func TestCatalogAmbiguousCreateRecoveryAndAbsenceProof(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	runner.ambiguousKind, runner.applyBeforeAmbiguous = CommandCreate, true
	mutation, err := bound.CreateWork(context.Background(), createRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	if !mutation.Replayed || mutation.Noop || runner.issueCount() != 1 || runner.mutationCount() != 1 {
		t.Fatalf("recovered=%#v count=%d mutations=%d", mutation, runner.issueCount(), runner.mutationCount())
	}

	bound, runner = newCatalogTestAdapter(t)
	runner.ambiguousKind, runner.applyBeforeAmbiguous = CommandCreate, false
	_, err = bound.CreateWork(context.Background(), createRequest(2))
	var portable *tracker.Error
	if !tracker.IsKind(err, tracker.ErrorAmbiguous) || !errors.As(err, &portable) || portable.RecoveryAction() != "retry_create_after_complete_absence" || runner.issueCount() != 0 || runner.mutationCount() != 1 {
		t.Fatalf("error=%v portable=%#v count=%d mutations=%d", err, portable, runner.issueCount(), runner.mutationCount())
	}
}

func TestCatalogReadDetectsEqualTimestampContentDriftAndMappingConflict(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	created, err := bound.CreateWork(context.Background(), createRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	before := created.Record.Observation.Revision
	runner.mu.Lock()
	issue := runner.issues[created.Record.ExternalRef.ExternalID]
	issue.Title = "human changed at same timestamp"
	runner.issues[issue.ID] = issue
	runner.mu.Unlock()
	after, err := bound.ReadWork(context.Background(), created.Record.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	if before == after.Observation.Revision || created.Record.Observation.ProviderUpdatedAt != after.Observation.ProviderUpdatedAt {
		t.Fatalf("before=%d after=%d timestamps=%s/%s", before, after.Observation.Revision, created.Record.Observation.ProviderUpdatedAt, after.Observation.ProviderUpdatedAt)
	}
	conflicting := created.Record.ExternalRef
	conflicting.WorkID = "other-work"
	if _, err := bound.ReadWork(context.Background(), conflicting); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
		t.Fatalf("mapping conflict=%v", err)
	}
}

func TestCatalogGuardedUpdateRefusesBeforePreflightLockOrMutation(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	request := tracker.UpdateWorkRequest{
		ExternalRef: tracker.ExternalWorkRef{ProjectID: "project-1", WorkID: "work-1", Provider: beadsProvider, ExternalID: "bd-1", MetadataSchema: 1, CreatedByOperation: "operation-create"},
		OperationID: "operation-update", ExpectedObservation: 1, RequiredGuard: tracker.UpdateGuarded,
		Title: "title", Criteria: catalogCriterion(),
	}
	before := len(runner.snapshotCalls())
	if _, err := bound.UpdateWork(context.Background(), request); !tracker.IsKind(err, tracker.ErrorGuardedUpdateUnsupported) {
		t.Fatalf("error=%v", err)
	}
	if calls := runner.snapshotCalls()[before:]; len(calls) != 0 {
		t.Fatalf("guarded refusal executed commands: %v", calls)
	}
}

func TestCatalogUnguardedUpdatePreservesMetadataAndHonestGuarantee(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	created, err := bound.CreateWork(context.Background(), createRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Record.ExternalRef.ExternalID
	runner.mu.Lock()
	issue := runner.issues[id]
	var root map[string]json.RawMessage
	_ = json.Unmarshal(issue.Metadata, &root)
	root["human"] = json.RawMessage(`{"keep":true}`)
	var namespace map[string]json.RawMessage
	_ = json.Unmarshal(root[metadataNamespace], &namespace)
	namespace["future"] = json.RawMessage(`{"keep":true}`)
	root[metadataNamespace], _ = json.Marshal(namespace)
	issue.Metadata, _ = json.Marshal(root)
	runner.issues[id] = issue
	runner.mu.Unlock()
	fresh, err := bound.ReadWork(context.Background(), created.Record.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	request := updateRequest(fresh, "operation-update")
	mutation, err := bound.UpdateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if mutation.Guarantee != tracker.UpdateUnguarded || mutation.Replayed || mutation.Record.Title != request.Title || mutation.Record.ExternalRef.CreatedByOperation != created.Record.ExternalRef.CreatedByOperation {
		t.Fatalf("mutation=%#v", mutation)
	}
	runner.mu.Lock()
	updated := runner.issues[id]
	runner.mu.Unlock()
	_ = json.Unmarshal(updated.Metadata, &root)
	_ = json.Unmarshal(root[metadataNamespace], &namespace)
	if string(root["human"]) != `{"keep":true}` || string(namespace["future"]) != `{"keep":true}` {
		t.Fatalf("metadata=%s", updated.Metadata)
	}
	document, err := decodeMetadata(updated.Metadata)
	if err != nil || document.kitty.CreatedByOperation != "operation-create-1" || document.kitty.LastOperation != "operation-update" {
		t.Fatalf("document=%#v error=%v", document.kitty, err)
	}
}

func TestCatalogStaleAndAmbiguousUpdateClassification(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	created, err := bound.CreateWork(context.Background(), createRequest(1))
	if err != nil {
		t.Fatal(err)
	}
	stale := updateRequest(created.Record, "operation-stale")
	stale.ExpectedObservation++
	beforeMutations := runner.mutationCount()
	if _, err := bound.UpdateWork(context.Background(), stale); !tracker.IsKind(err, tracker.ErrorRevisionConflict) || runner.mutationCount() != beforeMutations {
		t.Fatalf("stale error=%v mutations=%d", err, runner.mutationCount())
	}

	fresh, err := bound.ReadWork(context.Background(), created.Record.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.ambiguousKind, runner.applyBeforeAmbiguous = CommandUpdate, true
	runner.mu.Unlock()
	request := updateRequest(fresh, "operation-ambiguous")
	mutation, err := bound.UpdateWork(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !mutation.Replayed || mutation.Guarantee != tracker.UpdateUnguarded || mutation.Record.Title != request.Title {
		t.Fatalf("mutation=%#v", mutation)
	}
}

func TestCatalogReadLatency100Items(t *testing.T) {
	bound, _ := newCatalogTestAdapter(t)
	references := make([]tracker.ExternalWorkRef, 100)
	for index := range references {
		created, err := bound.CreateWork(context.Background(), createRequest(index+1))
		if err != nil {
			t.Fatal(err)
		}
		references[index] = created.Record.ExternalRef
	}
	durations := make([]time.Duration, 200)
	for index := range durations {
		started := time.Now()
		if _, err := bound.ReadWork(context.Background(), references[(index*37)%len(references)]); err != nil {
			t.Fatal(err)
		}
		durations[index] = time.Since(started)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p50, p95, maximum := durations[len(durations)/2], durations[(len(durations)*95)/100], durations[len(durations)-1]
	t.Logf("catalog latency samples=%d p50=%s p95=%s max=%s runtime=%s/%s provider=portable-memory", len(durations), p50, p95, maximum, runtime.GOOS, runtime.GOARCH)
	if os.Getenv("SPEC_KITTY_QUALIFICATION") == "1" && p95 > 500*time.Millisecond {
		t.Fatalf("qualification p95=%s exceeds 500ms", p95)
	}
}

func TestConcurrentCreateIdentity1000(t *testing.T) {
	bound, runner := newCatalogTestAdapter(t)
	const count = 1000
	const workers = 20
	requests := make(chan int)
	results := make(chan tracker.WorkMutation, count)
	errors := make(chan error, count)
	barrier := make(chan struct{})
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-barrier
			for index := range requests {
				mutation, err := bound.CreateWork(context.Background(), createRequest(index))
				if err != nil {
					errors <- err
					continue
				}
				results <- mutation
			}
		}()
	}
	close(barrier)
	go func() {
		for index := 1; index <= count; index++ {
			requests <- index
		}
		close(requests)
		group.Wait()
		close(results)
		close(errors)
	}()
	seenExternal := make(map[string]struct{}, count)
	seenWork := make(map[ids.WorkID]struct{}, count)
	for mutation := range results {
		seenExternal[mutation.Record.ExternalRef.ExternalID] = struct{}{}
		seenWork[mutation.Record.ExternalRef.WorkID] = struct{}{}
		if _, err := bound.ReadWork(context.Background(), mutation.Record.ExternalRef); err != nil {
			t.Fatal(err)
		}
	}
	for err := range errors {
		t.Errorf("create: %v", err)
	}
	if len(seenExternal) != count || len(seenWork) != count || runner.issueCount() != count {
		t.Fatalf("external=%d work=%d issues=%d", len(seenExternal), len(seenWork), runner.issueCount())
	}
	for index := 1; index <= 25; index++ {
		mutation, err := bound.CreateWork(context.Background(), createRequest(index*37))
		if err != nil || !mutation.Replayed {
			t.Fatalf("replay index=%d mutation=%#v error=%v", index, mutation, err)
		}
	}
	if runner.issueCount() != count {
		t.Fatalf("replay created duplicates: %d", runner.issueCount())
	}
	t.Logf("identity creates=%d workers=%d unique_external=%d topology=%s provider=portable-memory", count, workers, len(seenExternal), tracker.ConcurrentWritersAdapterSerialized)
}

func countKind(calls []CommandKind, want CommandKind) int {
	count := 0
	for _, kind := range calls {
		if kind == want {
			count++
		}
	}
	return count
}
