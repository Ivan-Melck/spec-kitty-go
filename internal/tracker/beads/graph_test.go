package beads

import (
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
	ID, Title, Metadata, UpdatedAt string
	Comments                       []wp06Comment
}

func (issue wp06Issue) wire() map[string]any {
	comments := make([]map[string]any, 0, len(issue.Comments))
	for _, comment := range issue.Comments {
		comments = append(comments, map[string]any{
			"id": comment.ID, "author": comment.Author, "body": comment.Body,
			"created_at": comment.CreatedAt, "future_optional": true,
		})
	}
	return map[string]any{
		"id": issue.ID, "title": issue.Title, "description": "description", "acceptance": "",
		"metadata": json.RawMessage(issue.Metadata), "comments": comments, "updated_at": issue.UpdatedAt,
		"future_optional": map[string]any{"ignored": true},
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
	commentAuthor             string
	ambiguousKind             CommandKind
	applyBeforeAmbiguous      bool
	failCommentListAfterWrite bool
	malformedKind             CommandKind
	providerFailureKind       CommandKind
	providerFailureCode       string
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
		data = issue.wire()
	case CommandReady:
		data = runner.issueWiresLocked(runner.ready)
	case CommandDependencyList:
		blocked := request.argv[2]
		edges := make([]observationDependency, 0)
		for _, edge := range runner.edges {
			if edge.Blocked == blocked {
				edges = append(edges, edge)
			}
		}
		data = edges
	case CommandDependencyAdd:
		runner.edges = append(runner.edges, observationDependency{Blocked: request.argv[2], Blocking: request.argv[3], Kind: "blocks", EdgeID: "edge-1"})
		data = map[string]any{"ok": true}
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
		data = map[string]any{"ok": true}
		result.mutationOutcome = mutationApplied
	case CommandCommentList:
		issue := runner.issues[request.argv[1]]
		comments := make([]map[string]any, 0, len(issue.Comments))
		for _, comment := range issue.Comments {
			comments = append(comments, map[string]any{"id": comment.ID, "author": comment.Author, "body": comment.Body, "created_at": comment.CreatedAt, "unknown": 1})
		}
		data = comments
	case CommandCommentAppend:
		issue := runner.issues[request.argv[2]]
		comment := wp06Comment{ID: fmt.Sprintf("comment-%d", len(issue.Comments)+1), Author: runner.commentAuthor, Body: request.argv[3], CreatedAt: runner.tickLocked()}
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
		commentAuthor: "agent-a", clock: 10,
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
