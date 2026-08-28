package beads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type wp06Comment struct {
	ID, Author, Body, CreatedAt string
}

type wp06Issue struct {
	ID, Title, Description, Metadata, UpdatedAt string
	Comments                                    []wp06Comment
}

func (issue wp06Issue) wire() map[string]any {
	description := issue.Description
	if description == "" {
		description = "description"
	}
	return map[string]any{
		"id": issue.ID, "title": issue.Title, "description": description, "acceptance_criteria": "",
		"metadata": json.RawMessage(issue.Metadata), "updated_at": issue.UpdatedAt,
		"dependency_count": 0, "comment_count": len(issue.Comments),
	}
}

func addDefinitionIssue(t *testing.T, runner *wp06Runner, definition tracker.WorkDefinition, external string) tracker.ExternalWorkRef {
	t.Helper()
	created := ids.OperationID("operation-create-" + definition.WorkID().String())
	metadata, err := mergeMetadata(definition.ProviderMetadata().Bytes(), metadataFromDefinition(definition, created, created))
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	updated := runner.tickLocked()
	runner.issues[external] = wp06Issue{ID: external, Title: definition.Title(), Description: definition.Description(), Metadata: string(metadata), UpdatedAt: updated}
	runner.mu.Unlock()
	return tracker.ExternalWorkRef{ProjectID: definition.ProjectID(), WorkID: definition.WorkID(), Provider: beadsProvider, ExternalID: external, MetadataSchema: 1, CreatedByOperation: created}
}

func graphDefinition(t *testing.T, workID ids.WorkID, dependencies ...ids.WorkID) tracker.WorkDefinition {
	t.Helper()
	metadata, err := tracker.NewOpaqueMetadata([]byte(`{"foreign":{"stable":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	definition, err := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{
		ProjectID: "project-1", WorkID: workID, Title: "Graph " + workID.String(), Description: "Complete graph definition.",
		DesignContext: "Canonical provider graph projection.", Criteria: []tracker.WorkCriterion{{ID: "criterion-1", Text: "graph is exact", Outcome: "graph tests pass", Verifier: tracker.VerifierTest, Required: true}},
		Dependencies: dependencies, WriteScopes: []tracker.WriteScope{{Path: "internal/" + workID.String(), Domain: "graph", Access: tracker.ScopeWriteExclusive}},
		Provenance: []tracker.ScopeRef{{ID: "plan-graph", Kind: tracker.ScopePlanConcern}}, MetadataSchema: 1, ProviderMetadata: metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func TestDefinitionCatalogJoinsBlockingGraphCanonically(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	workA := graphDefinition(t, "work-a")
	workB := graphDefinition(t, "work-b", "work-a")
	addDefinitionIssue(t, runner, workB, "bd-b")
	addDefinitionIssue(t, runner, workA, "bd-a")
	runner.edges = []observationDependency{{Blocked: "bd-b", Blocking: "bd-a", Kind: "blocks", EdgeID: "edge-z"}, {Blocked: "bd-b", Blocking: "bd-a", Kind: "blocks", EdgeID: "edge-a"}}

	var wantCanonical [][]byte
	var wantRevisions []tracker.Revision
	for iteration := 0; iteration < 100; iteration++ {
		runner.mu.Lock()
		if iteration%2 == 1 {
			runner.edges[0], runner.edges[1] = runner.edges[1], runner.edges[0]
		}
		runner.mu.Unlock()
		observations, err := bound.ObserveDefinitionCatalog(context.Background(), "project-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(observations) != 2 || observations[0].Definition().WorkID() != "work-a" || observations[1].Definition().WorkID() != "work-b" || !reflect.DeepEqual(observations[1].Definition().Dependencies(), []ids.WorkID{"work-a"}) {
			t.Fatalf("observations=%#v", observations)
		}
		canonical := [][]byte{observations[0].Definition().CanonicalData(), observations[1].Definition().CanonicalData()}
		revisions := []tracker.Revision{observations[0].ProviderObservation().Revision, observations[1].ProviderObservation().Revision}
		if iteration == 0 {
			wantCanonical, wantRevisions = canonical, revisions
			continue
		}
		if !bytes.Equal(canonical[0], wantCanonical[0]) || !bytes.Equal(canonical[1], wantCanonical[1]) || !reflect.DeepEqual(revisions, wantRevisions) {
			t.Fatalf("iteration=%d canonical or evidence changed", iteration)
		}
	}
}

func TestDefinitionCatalogRejectsGraphMismatchAndCrossProjectMapping(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, *wp06Runner)
	}{
		{"missing blocking edge", func(t *testing.T, runner *wp06Runner) {
			addDefinitionIssue(t, runner, graphDefinition(t, "work-a"), "bd-a")
			addDefinitionIssue(t, runner, graphDefinition(t, "work-b", "work-a"), "bd-b")
		}},
		{"cross project mapping", func(t *testing.T, runner *wp06Runner) {
			addDefinitionIssue(t, runner, graphDefinition(t, "work-a"), "bd-a")
			foreign := graphDefinition(t, "work-b")
			created := ids.OperationID("operation-create-work-b")
			metadata := metadataFromDefinition(foreign, created, created)
			metadata.ProjectID = "project-foreign"
			encoded, err := mergeMetadata(nil, metadata)
			if err != nil {
				t.Fatal(err)
			}
			runner.mu.Lock()
			runner.issues["bd-b"] = wp06Issue{ID: "bd-b", Title: foreign.Title(), Description: foreign.Description(), Metadata: string(encoded), UpdatedAt: runner.tickLocked()}
			runner.mu.Unlock()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := newWP06Adapter(t)
			test.prepare(t, runner)
			if _, err := bound.ObserveDefinitionCatalog(context.Background(), "project-1"); !tracker.IsKind(err, tracker.ErrorRevisionConflict) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type wp06Runner struct {
	mu                        sync.Mutex
	workspace, databaseID     string
	issues                    map[string]wp06Issue
	edges                     []observationDependency
	ready                     []string
	calls                     []CommandKind
	argvs                     [][]string
	clock                     int64
	ambiguousKind             CommandKind
	applyBeforeAmbiguous      bool
	failCommentListAfterWrite bool
	malformedKind             CommandKind
	providerFailureKind       CommandKind
	providerFailureCode       string
	dependencySkewFields      bool
}

func (runner *wp06Runner) run(_ context.Context, request command) commandResult {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls = append(runner.calls, request.kind)
	runner.argvs = append(runner.argvs, request.renderedArgv())
	if runner.providerFailureKind == request.kind {
		return providerFailureResult(request.kind, runner.providerFailureCode)
	}
	if runner.failCommentListAfterWrite && request.kind == CommandCommentList && countKind(runner.calls, CommandCommentAppend) > 0 {
		return providerFailureResult(request.kind, "unavailable")
	}
	if runner.ambiguousKind == request.kind && !runner.applyBeforeAmbiguous {
		runner.ambiguousKind = ""
		return ambiguousResult(request.kind)
	}
	zero := 0
	result := commandResult{kind: request.kind, started: true, exitCode: &zero, mutationOutcome: mutationUnknown}
	var data any
	switch request.kind {
	case CommandVersion:
		data = map[string]any{"version": SupportedBeadsVersion, "build": "wp06-test"}
	case CommandWhere:
		data = map[string]any{"path": runner.workspace, "prefix": runner.databaseID, "database_path": filepath.Join(runner.workspace, "embeddeddolt")}
	case CommandDoltShow:
		data = map[string]any{"backend": "dolt", "data_dir": filepath.Join(runner.workspace, "embeddeddolt"), "database": runner.databaseID, "embedded": true}
	case CommandDoltStatus:
		data = map[string]any{"data_dir": filepath.Join(runner.workspace, "embeddeddolt"), "data_dir_exists": true, "mode": "embedded", "server_running": false}
	case CommandList:
		data = runner.issueWiresLocked(nil)
	case CommandShow:
		issue, present := runner.issues[request.argv[1]]
		if !present {
			return providerFailureResult(request.kind, "not_found")
		}
		data = []map[string]any{issue.wire()}
	case CommandReady:
		data = runner.issueWiresLocked(runner.ready)
	case CommandDependencyList:
		blocked := request.argv[2]
		edges := make([]map[string]any, 0)
		for _, edge := range runner.edges {
			if edge.Blocked == blocked {
				record := map[string]any{"id": edge.Blocking, "dependency_type": edge.Kind}
				if runner.dependencySkewFields {
					record["edge_id"] = edge.EdgeID
					record["future_optional"] = true
				}
				edges = append(edges, record)
			}
		}
		data = edges
	case CommandDependencyAdd:
		runner.edges = append(runner.edges, observationDependency{Blocked: request.argv[2], Blocking: request.argv[3], Kind: "blocks", EdgeID: "edge-1"})
		data = map[string]any{"status": "added", "issue_id": request.argv[2], "depends_on_id": request.argv[3], "type": "blocks"}
		result.mutationOutcome = mutationApplied
	case CommandDependencyRemove:
		blocked, blocker := request.argv[2], request.argv[3]
		kept := runner.edges[:0]
		for _, edge := range runner.edges {
			if edge.Blocked != blocked || edge.Blocking != blocker || edge.Kind != "blocks" {
				kept = append(kept, edge)
			}
		}
		runner.edges = kept
		data = map[string]any{"status": "removed", "issue_id": blocked, "depends_on_id": blocker}
		result.mutationOutcome = mutationApplied
	case CommandCommentList:
		issue := runner.issues[request.argv[1]]
		comments := make([]map[string]any, 0, len(issue.Comments))
		for _, comment := range issue.Comments {
			comments = append(comments, map[string]any{"id": comment.ID, "author": comment.Author, "text": comment.Body, "created_at": comment.CreatedAt})
		}
		data = comments
	case CommandCommentAppend:
		issue := runner.issues[request.argv[2]]
		comment := wp06Comment{ID: fmt.Sprintf("comment-%d", len(issue.Comments)+1), Author: request.argv[5], Body: request.argv[3], CreatedAt: runner.tickLocked()}
		issue.Comments = append(issue.Comments, comment)
		issue.UpdatedAt = comment.CreatedAt
		runner.issues[issue.ID] = issue
		data = map[string]any{"id": comment.ID}
		result.mutationOutcome = mutationApplied
	default:
		data = map[string]any{"ok": true}
	}
	if runner.malformedKind == request.kind {
		result.stdout = []byte("malformed")
		return result
	}
	if runner.ambiguousKind == request.kind {
		runner.ambiguousKind = ""
		return ambiguousResult(request.kind)
	}
	result.stdout, _ = json.Marshal(map[string]any{"schema_version": 1, "data": data})
	return result
}

// The dependency-list and ready data payloads were captured without field
// projection from the pinned v1.2.2 binary whose SHA-256 is
// 0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6.
const realV122DependencyList = `[{"id":"skt-mm3","title":"Pinned blocker capture","status":"open","priority":1,"issue_type":"task","owner":"review@example.invalid","created_at":"2026-08-26T16:37:11Z","created_by":"spec-kitty-review","updated_at":"2026-08-26T16:37:11Z","metadata":{"spec_kitty":{"work_id":"work-blocker","criteria":[],"project_id":"project-1","content_digest":"digest-blocker","design_context":"","last_operation":"operation-create-blocker","schema_version":1,"created_by_operation":"operation-create-blocker"}},"dependency_type":"blocks"}]`

const realV122DependencyAddReceipt = `{"status":"added","issue_id":"bd-asymmetric-blocked","depends_on_id":"bd-distinct-prerequisite","type":"blocks"}`

const realV122DependencyRemoveReceipt = `{"status":"removed","issue_id":"bd-asymmetric-blocked","depends_on_id":"bd-distinct-prerequisite"}`

const realV122ReadyList = `[{"id":"skt-mm3","title":"Pinned blocker capture","status":"open","priority":1,"issue_type":"task","owner":"review@example.invalid","created_at":"2026-08-26T16:37:11Z","created_by":"spec-kitty-review","updated_at":"2026-08-26T16:37:11Z","metadata":{"spec_kitty":{"work_id":"work-blocker","criteria":[],"project_id":"project-1","content_digest":"digest-blocker","design_context":"","last_operation":"operation-create-blocker","schema_version":1,"created_by_operation":"operation-create-blocker"}},"dependency_count":0,"dependent_count":1,"comment_count":0}]`

func assertCapturedFields(t *testing.T, payload json.RawMessage, want ...string) {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(fields))
	for field := range fields {
		got = append(got, field)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("captured fields=%v want=%v", got, want)
	}
}

func TestRealV122DependencyAndReadyPayloadShapes(t *testing.T) {
	var dependencies []json.RawMessage
	if err := json.Unmarshal([]byte(realV122DependencyList), &dependencies); err != nil || len(dependencies) != 1 {
		t.Fatalf("dependency fixture records=%d error=%v", len(dependencies), err)
	}
	assertCapturedFields(t, dependencies[0], "id", "title", "status", "priority", "issue_type", "owner", "created_at", "created_by", "updated_at", "metadata", "dependency_type")
	edge, err := decodeProviderDependency(dependencies[0], "skt-68d")
	if err != nil || edge.Blocked != "skt-68d" || edge.Blocking != "skt-mm3" || edge.Kind != "blocks" {
		t.Fatalf("edge=%#v error=%v", edge, err)
	}
	for name, fixture := range map[string]struct {
		payload string
		want    map[string]string
	}{
		"add":    {payload: realV122DependencyAddReceipt, want: map[string]string{"status": "added", "issue_id": "bd-asymmetric-blocked", "depends_on_id": "bd-distinct-prerequisite", "type": "blocks"}},
		"remove": {payload: realV122DependencyRemoveReceipt, want: map[string]string{"status": "removed", "issue_id": "bd-asymmetric-blocked", "depends_on_id": "bd-distinct-prerequisite"}},
	} {
		t.Run(name+" receipt", func(t *testing.T) {
			var receipt map[string]string
			if err := json.Unmarshal([]byte(fixture.payload), &receipt); err != nil || !reflect.DeepEqual(receipt, fixture.want) {
				t.Fatalf("receipt=%v error=%v", receipt, err)
			}
			if _, err := decodeProviderDependency([]byte(fixture.payload), "bd-asymmetric-blocked"); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
				t.Fatalf("mutation receipt became a dependency list record: %v", err)
			}
		})
	}
	for name, payload := range map[string]string{
		"missing blocker":    `{"dependency_type":"blocks"}`,
		"non-string blocker": `{"id":7,"dependency_type":"blocks"}`,
		"missing kind":       `{"id":"bd-distinct-prerequisite"}`,
		"non-object record":  `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeProviderDependency([]byte(payload), "bd-asymmetric-blocked"); !tracker.IsKind(err, tracker.ErrorMalformedProtocol) {
				t.Fatalf("payload=%s error=%v", payload, err)
			}
		})
	}

	var ready []json.RawMessage
	if err := json.Unmarshal([]byte(realV122ReadyList), &ready); err != nil || len(ready) != 1 {
		t.Fatalf("ready fixture records=%d error=%v", len(ready), err)
	}
	assertCapturedFields(t, ready[0], "id", "title", "status", "priority", "issue_type", "owner", "created_at", "created_by", "updated_at", "metadata", "dependency_count", "dependent_count", "comment_count")
	issue, err := decodeProviderIssue(ready[0])
	if err != nil || issue.externalID != "skt-mm3" || issue.document.kitty.WorkID != "work-blocker" {
		t.Fatalf("ready issue=%#v error=%v", issue, err)
	}

	var dependencySkew map[string]json.RawMessage
	if err := json.Unmarshal(dependencies[0], &dependencySkew); err != nil {
		t.Fatal(err)
	}
	dependencySkew["future_optional"] = json.RawMessage(`{"ignored":true}`)
	dependencySkewPayload, err := json.Marshal(dependencySkew)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeProviderDependency(dependencySkewPayload, "skt-68d"); err != nil {
		t.Fatalf("dependency optional-field skew rejected: %v", err)
	}
	var readySkew map[string]json.RawMessage
	if err := json.Unmarshal(ready[0], &readySkew); err != nil {
		t.Fatal(err)
	}
	readySkew["future_optional"] = json.RawMessage(`{"ignored":true}`)
	readySkewPayload, err := json.Marshal(readySkew)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeProviderIssue(readySkewPayload); err != nil {
		t.Fatalf("ready optional-field skew rejected: %v", err)
	}
}

func (runner *wp06Runner) issueWiresLocked(selected []string) []map[string]any {
	idsToUse := selected
	if idsToUse == nil {
		idsToUse = make([]string, 0, len(runner.issues))
		for id := range runner.issues {
			idsToUse = append(idsToUse, id)
		}
		sort.Strings(idsToUse)
	}
	result := make([]map[string]any, 0, len(idsToUse))
	for _, id := range idsToUse {
		result = append(result, runner.issues[id].wire())
	}
	return result
}

func (runner *wp06Runner) tickLocked() string {
	runner.clock++
	return time.Unix(1_800_000_000+runner.clock, 0).UTC().Format(time.RFC3339Nano)
}

func newWP06Adapter(t *testing.T) (*BoundAdapter, *wp06Runner) {
	t.Helper()
	bound, inspection := testBoundAdapter(t, tracker.ConcurrencyEmbeddedSerialized, "wp06-db")
	runner := &wp06Runner{
		workspace: inspection.workspace, databaseID: "wp06-db", issues: make(map[string]wp06Issue),
		clock: 10,
	}
	bound.adapter = newAdapter(runner)
	bound.locker = &serialCatalogLocker{}
	return bound, runner
}

func addWP06Issue(t *testing.T, runner *wp06Runner, project, work, external string) tracker.ExternalWorkRef {
	t.Helper()
	created := ids.OperationID("operation-create-" + work)
	metadata, err := mergeMetadata(nil, kittyMetadata{
		SchemaVersion: 1, ProjectID: ids.ProjectID(project), WorkID: ids.WorkID(work), CreatedByOperation: created,
		LastOperation: created, Criteria: []tracker.Criterion{}, ContentDigest: "digest-" + work,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	updated := runner.tickLocked()
	runner.issues[external] = wp06Issue{ID: external, Title: work, Metadata: string(metadata), UpdatedAt: updated}
	runner.mu.Unlock()
	return tracker.ExternalWorkRef{ProjectID: ids.ProjectID(project), WorkID: ids.WorkID(work), Provider: beadsProvider, ExternalID: external, MetadataSchema: 1, CreatedByOperation: created}
}

func dependencyRequest(blocked, blocker string) tracker.DependencyRequest {
	return tracker.DependencyRequest{
		ProjectID: "project-1", OperationID: "operation-dependency-1",
		Dependency: tracker.DependencyRef{BlockedWorkID: ids.WorkID(blocked), BlockingWorkID: ids.WorkID(blocker), Kind: tracker.DependencyBlocks},
	}
}

func (runner *wp06Runner) callSnapshot() ([]CommandKind, [][]string) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	argvs := make([][]string, len(runner.argvs))
	for index := range runner.argvs {
		argvs[index] = append([]string(nil), runner.argvs[index]...)
	}
	return append([]CommandKind(nil), runner.calls...), argvs
}

func TestGraphExactDirectionRoundTripReplayAndRemoval(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	blocked := addWP06Issue(t, runner, "project-1", "work-blocked", "bd-asymmetric-blocked")
	addWP06Issue(t, runner, "project-1", "work-blocker", "bd-distinct-prerequisite")
	request := dependencyRequest("work-blocked", "work-blocker")
	mutation, err := bound.PutBlockingDependency(context.Background(), request)
	if err != nil || mutation.Dependency != request.Dependency || mutation.Replayed || mutation.Observation.Revision == 0 {
		calls, argvs := runner.callSnapshot()
		t.Fatalf("mutation=%#v error=%#v calls=%v argvs=%q", mutation, err, calls, argvs)
	}
	_, argvs := runner.callSnapshot()
	found := false
	for _, argv := range argvs {
		if reflect.DeepEqual(argv, []string{"dep", "add", "bd-asymmetric-blocked", "bd-distinct-prerequisite", "--type", "blocks", "--json"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("exact dependency argv absent: %q", argvs)
	}
	edges, err := bound.ListDependencies(context.Background(), blocked)
	if err != nil || len(edges) != 1 || edges[0].BlockedWorkID != "work-blocked" || edges[0].BlockingWorkID != "work-blocker" {
		t.Fatalf("edges=%#v error=%v", edges, err)
	}
	replay, err := bound.PutBlockingDependency(context.Background(), request)
	if err != nil || !replay.Noop || !replay.Replayed {
		t.Fatalf("replay=%#v error=%v", replay, err)
	}
	removed, err := bound.RemoveBlockingDependency(context.Background(), request)
	if err != nil || removed.Noop {
		t.Fatalf("removed=%#v error=%v", removed, err)
	}
	removedReplay, err := bound.RemoveBlockingDependency(context.Background(), request)
	if err != nil || !removedReplay.Noop || !removedReplay.Replayed {
		t.Fatalf("remove replay=%#v error=%v", removedReplay, err)
	}
	calls, _ := runner.callSnapshot()
	if countKind(calls, CommandDependencyAdd) != 1 || countKind(calls, CommandDependencyRemove) != 1 {
		t.Fatalf("mutating calls=%v", calls)
	}
}

func TestGraphValidationAndProviderRefusalsDoNotBlindlyMutate(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *wp06Runner)
		request tracker.DependencyRequest
		kind    tracker.ErrorKind
	}{
		{name: "self edge", request: dependencyRequest("work-a", "work-a"), kind: tracker.ErrorInvalidRequest},
		{name: "unsupported kind", request: func() tracker.DependencyRequest {
			r := dependencyRequest("work-a", "work-b")
			r.Dependency.Kind = "related"
			return r
		}(), kind: tracker.ErrorInvalidRequest},
		{name: "unmapped endpoint", request: dependencyRequest("work-a", "work-missing"), kind: tracker.ErrorNotFound},
		{name: "foreign endpoint", prepare: func(t *testing.T, r *wp06Runner) { addWP06Issue(t, r, "project-2", "work-b", "bd-b") }, request: dependencyRequest("work-a", "work-b"), kind: tracker.ErrorRevisionConflict},
		{name: "cycle rejection", prepare: func(_ *testing.T, r *wp06Runner) {
			r.providerFailureKind, r.providerFailureCode = CommandDependencyAdd, "cycle"
		}, request: dependencyRequest("work-a", "work-b"), kind: tracker.ErrorInvalidRequest},
		{name: "malformed observation", prepare: func(_ *testing.T, r *wp06Runner) { r.malformedKind = CommandDependencyList }, request: dependencyRequest("work-a", "work-b"), kind: tracker.ErrorMalformedProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := newWP06Adapter(t)
			addWP06Issue(t, runner, "project-1", "work-a", "bd-a")
			if test.name != "foreign endpoint" {
				addWP06Issue(t, runner, "project-1", "work-b", "bd-b")
			}
			if test.prepare != nil {
				test.prepare(t, runner)
			}
			_, err := bound.PutBlockingDependency(context.Background(), test.request)
			if !tracker.IsKind(err, test.kind) {
				t.Fatalf("error=%v want=%s", err, test.kind)
			}
			calls, _ := runner.callSnapshot()
			wantMutations := 0
			if test.name == "cycle rejection" {
				wantMutations = 1
			}
			if got := countKind(calls, CommandDependencyAdd); got != wantMutations {
				t.Fatalf("dependency mutations=%d calls=%v", got, calls)
			}
		})
	}
}

func TestGraphUnknownRelationshipNeverBecomesBlocking(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	blocked := addWP06Issue(t, runner, "project-1", "work-a", "bd-a")
	runner.edges = []observationDependency{{Blocked: "bd-a", Blocking: "unmapped-human-link", Kind: "discovered-from", EdgeID: "provenance-1"}}
	edges, err := bound.ListDependencies(context.Background(), blocked)
	if err != nil || len(edges) != 0 {
		t.Fatalf("edges=%#v error=%v", edges, err)
	}
}

func TestGraphDuplicateBlockingFactsNormalizeDeterministically(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	runner.dependencySkewFields = true
	blocked := addWP06Issue(t, runner, "project-1", "work-a", "bd-a")
	addWP06Issue(t, runner, "project-1", "work-b", "bd-b")
	runner.edges = []observationDependency{
		{Blocked: "bd-a", Blocking: "bd-b", Kind: "blocks", EdgeID: "edge-z"},
		{Blocked: "bd-a", Blocking: "bd-b", Kind: "blocks", EdgeID: "edge-a"},
	}
	edges, err := bound.ListDependencies(context.Background(), blocked)
	if err != nil || len(edges) != 1 || edges[0].ExternalEdgeID != "edge-a" {
		t.Fatalf("normalized edges=%#v error=%v", edges, err)
	}
}

func TestGraphAmbiguousMutationUsesObservationWithoutSecondWrite(t *testing.T) {
	for _, test := range []struct {
		name        string
		kind        CommandKind
		apply       bool
		wantSuccess bool
	}{
		{name: "add applied", kind: CommandDependencyAdd, apply: true, wantSuccess: true},
		{name: "add absent", kind: CommandDependencyAdd, apply: false},
		{name: "remove applied", kind: CommandDependencyRemove, apply: true, wantSuccess: true},
		{name: "remove unchanged", kind: CommandDependencyRemove, apply: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound, runner := newWP06Adapter(t)
			addWP06Issue(t, runner, "project-1", "work-a", "bd-a")
			addWP06Issue(t, runner, "project-1", "work-b", "bd-b")
			request := dependencyRequest("work-a", "work-b")
			if test.kind == CommandDependencyRemove {
				runner.edges = []observationDependency{{Blocked: "bd-a", Blocking: "bd-b", Kind: "blocks", EdgeID: "edge-1"}}
			}
			runner.ambiguousKind, runner.applyBeforeAmbiguous = test.kind, test.apply
			var err error
			if test.kind == CommandDependencyAdd {
				_, err = bound.PutBlockingDependency(context.Background(), request)
			} else {
				_, err = bound.RemoveBlockingDependency(context.Background(), request)
			}
			if test.wantSuccess && err != nil || !test.wantSuccess && !tracker.IsKind(err, tracker.ErrorAmbiguous) {
				t.Fatalf("error=%v", err)
			}
			calls, _ := runner.callSnapshot()
			if countKind(calls, test.kind) != 1 {
				t.Fatalf("blind retry calls=%v", calls)
			}
		})
	}
}

func TestReadyProjectionIsDeterministicDiagnosticAndNonAuthoritative(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	addWP06Issue(t, runner, "project-1", "work-a", "bd-a")
	addWP06Issue(t, runner, "project-1", "work-b", "bd-b")
	addWP06Issue(t, runner, "project-2", "work-foreign", "bd-foreign")
	runner.mu.Lock()
	runner.issues["bd-human"] = wp06Issue{ID: "bd-human", Title: "human", Metadata: `{}`, UpdatedAt: runner.tickLocked()}
	runner.ready = []string{"bd-b", "bd-human", "bd-foreign"}
	runner.mu.Unlock()
	pinnedInput := []ids.WorkID{"work-a"}
	projection, err := bound.ReadyProjection(context.Background(), tracker.ReadyRequest{ProjectID: "project-1", PinnedReadyIDs: pinnedInput})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(projection.ProviderReadyIDs, []ids.WorkID{"work-b"}) || projection.Observation.Revision == 0 || len(projection.Diagnostics) != 4 {
		t.Fatalf("projection=%#v", projection)
	}
	if !reflect.DeepEqual(pinnedInput, []ids.WorkID{"work-a"}) {
		t.Fatal("projection mutated pinned graph input")
	}
	for _, diagnostic := range projection.Diagnostics {
		if !strings.Contains(diagnostic.RecoveryCode, "reconcile") {
			t.Fatalf("diagnostic authorizes instead of reconciling: %#v", diagnostic)
		}
	}
	firstRevision := projection.Observation.Revision
	runner.mu.Lock()
	issue := runner.issues["bd-b"]
	issue.UpdatedAt = runner.tickLocked()
	runner.issues[issue.ID] = issue
	runner.mu.Unlock()
	changed, err := bound.ReadyProjection(context.Background(), tracker.ReadyRequest{ProjectID: "project-1", PinnedReadyIDs: []ids.WorkID{"work-a"}})
	if err != nil || changed.Observation.Revision == firstRevision {
		t.Fatalf("stale provider observation not detected: before=%d after=%d error=%v", firstRevision, changed.Observation.Revision, err)
	}
}

func TestReadyProjectionReportsProviderUnavailable(t *testing.T) {
	bound, runner := newWP06Adapter(t)
	runner.providerFailureKind, runner.providerFailureCode = CommandReady, "unavailable"
	projection, err := bound.ReadyProjection(context.Background(), tracker.ReadyRequest{ProjectID: "project-1"})
	if err != nil || len(projection.Diagnostics) != 1 || projection.Diagnostics[0].Kind != tracker.ErrorUnavailable || len(projection.ProviderReadyIDs) != 0 {
		t.Fatalf("projection=%#v error=%v", projection, err)
	}
}

func TestReadyProjectionRefusesConflictingStableMapping(t *testing.T) {
	for _, readyExternal := range []string{"bd-a-one", "bd-a-two"} {
		t.Run(readyExternal, func(t *testing.T) {
			bound, runner := newWP06Adapter(t)
			addWP06Issue(t, runner, "project-1", "work-a", "bd-a-one")
			addWP06Issue(t, runner, "project-1", "work-a", "bd-a-two")
			// Only one conflicting claim is returned by bd ready. The complete
			// catalog observation must still prevent positive readiness.
			runner.ready = []string{readyExternal}
			projection, err := bound.ReadyProjection(context.Background(), tracker.ReadyRequest{ProjectID: "project-1", PinnedReadyIDs: []ids.WorkID{"work-a"}})
			if err != nil || len(projection.ProviderReadyIDs) != 0 {
				t.Fatalf("conflicting catalog mapping became positively ready: projection=%#v error=%v", projection, err)
			}
			found := false
			for _, diagnostic := range projection.Diagnostics {
				if diagnostic.RecoveryCode == "reconcile_mapping" {
					found = true
				}
			}
			if !found {
				t.Fatalf("mapping conflict diagnostic absent: %#v", projection.Diagnostics)
			}
		})
	}
}
