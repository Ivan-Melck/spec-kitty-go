package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestCycle3RepositoryDiagnosticUsesStructuredHelperJSON(t *testing.T) {
	now := time.Date(2026, 8, 24, 15, 0, 0, 0, time.UTC)
	diagnostic := execution.NewDiagnostic(execution.KindControlStoreBusy, "control repository save failed", execution.RetrySafeSameOperation,
		"correlation-helper", ids.OperationID("operation-helper"),
		map[string]string{"actor_id": "actor-helper", "process_id": "process-helper", "expected_provider_revision": "2"},
		map[string]string{"control_repository": "save_failed"}, map[string]string{"control_repository": "durable and available"}, now)
	diagnostic.RecoveryActions = []string{"inspect the same operation", "retry after recovery"}
	diagnostic.Cause = errors.New("disk unavailable")
	var output bytes.Buffer
	if code := encodeTrackerFailure(&output, diagnostic); code != 0 {
		t.Fatalf("structured diagnostic exit code = %d", code)
	}
	var response TrackerResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Message != "" || response.Error.SchemaVersion != execution.ResultSchemaVersion || response.Error.CorrelationID != "correlation-helper" || response.Error.OperationID != "operation-helper" || response.Error.AggregateIDs["actor_id"] != "actor-helper" || response.Error.AggregateIDs["process_id"] != "process-helper" || response.Error.OccurredAt != now || len(response.Error.RecoveryActions) != 2 {
		t.Fatalf("incomplete helper response: %+v", response)
	}
}

func TestTrackerInspectBeadsRunsFromShippedRootAndEmitsTypedJSON(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := helperDatabasePath(t, workspace)
	executable := helperBeadsExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	args := []string{"tracker", "inspect-beads", "--project", "project-1", "--executable", executable, "--workspace", workspace, "--database-identity", databasePath, "--protocol-schema", "1", "--mode", "embedded_serialized", "--read-only=false"}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
	var response BeadsInspectionResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.SchemaVersion != 1 || response.Inspection == nil || response.Error != nil || response.Inspection.Health != tracker.HealthHealthy || response.Inspection.Binding.ProjectID != "project-1" || response.Inspection.Binding.WorkspacePath != workspace || response.Inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersAdapterSerialized {
		t.Fatalf("response=%+v", response)
	}
}

func TestTrackerInspectBeadsServerCredentialsAreNeverSerialized(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := helperDatabasePath(t, workspace)
	executable := helperBeadsExecutable(t, root, tracker.ConcurrencyServerMultiwriter)
	secret := "super-secret-password"
	args := []string{"tracker", "inspect-beads", "--project", "project-1", "--executable", executable, "--workspace", workspace, "--database-identity", databasePath, "--protocol-schema", "1", "--mode", "server_multiwriter", "--read-only=false", "--server-identity", "server-1", "--server-endpoint", "127.0.0.1:3306", "--dolt-server-host", "127.0.0.1", "--dolt-server-port", "3306", "--dolt-remote-user", "operator", "--dolt-remote-password", secret}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) || strings.Contains(stdout.String(), "operator") {
		t.Fatalf("credential leaked: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var response BeadsInspectionResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Inspection == nil || response.Inspection.Capabilities.ConcurrentWriters != tracker.ConcurrentWritersProviderNative {
		t.Fatalf("response=%+v", response)
	}
}

func TestTrackerInspectBeadsCanonicalizesDatabaseAliasAndRefusesDivergence(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := helperDatabasePath(t, workspace)
	databaseAlias := filepath.Join(root, "database-alias")
	if err := os.Symlink(databasePath, databaseAlias); err != nil {
		t.Fatal(err)
	}
	executable := helperBeadsExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	run := func(database string) BeadsInspectionResponse {
		t.Helper()
		args := []string{"tracker", "inspect-beads", "--project", "project-1", "--executable", executable, "--workspace", workspace, "--database-identity", database, "--protocol-schema", "1", "--mode", "embedded_serialized", "--read-only=false"}
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("database=%q code=%d stderr=%q stdout=%q", database, code, stderr.String(), stdout.String())
		}
		var response BeadsInspectionResponse
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	canonical := run(databasePath)
	aliased := run(databaseAlias)
	if canonical.Inspection == nil || aliased.Inspection == nil ||
		canonical.Inspection.Binding.WorkspaceIdentity != aliased.Inspection.Binding.WorkspaceIdentity ||
		canonical.Inspection.Health != tracker.HealthHealthy || aliased.Inspection.Health != tracker.HealthHealthy {
		t.Fatalf("canonical=%+v aliased=%+v", canonical, aliased)
	}
	otherDatabase := filepath.Join(workspace, "other-database")
	if err := os.Mkdir(otherDatabase, 0700); err != nil {
		t.Fatal(err)
	}
	divergent := run(otherDatabase)
	if divergent.Inspection == nil || divergent.Inspection.Health == tracker.HealthHealthy || len(divergent.Inspection.Diagnostics) != 1 || divergent.Inspection.Diagnostics[0].Kind != tracker.ErrorWorkspaceMismatch {
		t.Fatalf("divergent=%+v", divergent)
	}
}

func TestTrackerInspectBeadsHelpNamesDatabaseDirectoryContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunTracker(context.Background(), []string{"inspect-beads", "--help"}, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "absolute existing Beads database directory from where --json") {
		t.Fatalf("help omitted database-directory contract: %q", stderr.String())
	}
}

func TestTrackerInspectBeadsInvalidBindingIsTypedAndSecretFree(t *testing.T) {
	secret := "do-not-log-me"
	args := []string{"tracker", "inspect-beads", "--project", "project-1", "--executable", "relative-bd", "--workspace", "relative-workspace", "--database-identity", "db-1", "--protocol-schema", "1", "--mode", "server_multiwriter", "--read-only=false", "--server-identity", "server-1", "--server-endpoint", "127.0.0.1:3306", "--dolt-server-host", "127.0.0.1", "--dolt-server-port", "3306", "--dolt-remote-password", secret}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), args, &stdout, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var response BeadsInspectionResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Error.Kind != tracker.ErrorInvalidRequest || response.Error.RecoveryCode == "" || strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("response=%+v stdout=%q stderr=%q", response, stdout.String(), stderr.String())
	}
}

func TestTrackerInspectBeadsRequiresExplicitReadOnlyAndUsageNamesCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"tracker", "inspect-beads", "--project", "project-1", "--database-identity", "db-1", "--protocol-schema", "1", "--mode", "embedded_serialized"}
	if code := Run(context.Background(), args, &stdout, &stderr); code != 1 {
		t.Fatalf("code=%d", code)
	}
	var response BeadsInspectionResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Kind != tracker.ErrorInvalidRequest {
		t.Fatalf("response=%+v", response)
	}
	stdout.Reset()
	stderr.Reset()
	if code := RunTracker(context.Background(), nil, &stdout, &stderr); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
	if !strings.Contains(stderr.String(), "inspect-beads") || !strings.Contains(stderr.String(), "catalog-beads") || !strings.Contains(stderr.String(), "coordinate-beads") {
		t.Fatalf("usage=%q", stderr.String())
	}
}

func TestTrackerCoordinateBeadsReachesProviderNeutralGraphAndDiscussionPorts(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := helperDatabasePath(t, workspace)
	executable := helperCoordinationBeadsExecutable(t, root)
	tests := []struct {
		action, request string
		check           func(*testing.T, BeadsCoordinationResponse)
	}{
		{
			action:  "dependency-list",
			request: `{"work_id":"work-a","external_id":"bd-a","metadata_schema":1,"created_by_operation":"operation-create-a"}`,
			check: func(t *testing.T, response BeadsCoordinationResponse) {
				if len(response.Dependencies) != 1 || response.Dependencies[0].BlockedWorkID != "work-a" || response.Dependencies[0].BlockingWorkID != "work-b" {
					t.Fatalf("dependencies=%#v", response.Dependencies)
				}
			},
		},
		{
			action:  "ready",
			request: `{"pinned_ready_ids":["work-a"]}`,
			check: func(t *testing.T, response BeadsCoordinationResponse) {
				if response.Ready == nil || !reflect.DeepEqual(response.Ready.ProviderReadyIDs, []ids.WorkID{"work-a"}) || len(response.Ready.Diagnostics) != 0 {
					t.Fatalf("ready=%#v", response.Ready)
				}
			},
		},
		{
			action:  "discussion-list",
			request: `{"work_id":"work-a","external_id":"bd-a","metadata_schema":1,"created_by_operation":"operation-create-a"}`,
			check: func(t *testing.T, response BeadsCoordinationResponse) {
				if len(response.Discussion) != 1 || response.Discussion[0].ExternalCommentID != "comment-human" || response.Discussion[0].Body != "human discussion" {
					t.Fatalf("discussion=%#v", response.Discussion)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			requestPath := filepath.Join(root, test.action+".json")
			if err := os.WriteFile(requestPath, []byte(test.request), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{
				"tracker", "coordinate-beads", test.action, "--project", "project-1", "--executable", executable,
				"--workspace", workspace, "--database-identity", databasePath, "--protocol-schema", "1",
				"--mode", "embedded_serialized", "--read-only=false", "--request-file", requestPath,
			}
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
			}
			var response BeadsCoordinationResponse
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !response.OK || response.SchemaVersion != 1 || response.Action != test.action || response.Error != nil {
				t.Fatalf("response=%#v", response)
			}
			test.check(t, response)
		})
	}
}

func TestTrackerCoordinateBeadsMutationBranchesCallProviderNeutralPorts(t *testing.T) {
	ctx := context.Background()
	projectID := ids.ProjectID("project-1")
	dependencyRequest := tracker.DependencyRequest{
		ProjectID: projectID, OperationID: "operation-edge",
		Dependency: tracker.DependencyRef{BlockedWorkID: "work-blocked", BlockingWorkID: "work-blocking", Kind: tracker.DependencyBlocks},
	}
	dependencyInput := beadsCoordinationInput{
		OperationID: dependencyRequest.OperationID.String(), BlockedWorkID: dependencyRequest.Dependency.BlockedWorkID.String(),
		BlockingWorkID: dependencyRequest.Dependency.BlockingWorkID.String(),
	}

	t.Run("dependency-put", func(t *testing.T) {
		want := tracker.DependencyMutation{Dependency: dependencyRequest.Dependency, Replayed: true}
		graph := &recordingCoordinationGraph{putResult: want}
		discussion := &recordingCoordinationDiscussion{}
		response, err := executeBeadsCoordination(ctx, "dependency-put", projectID, graph, discussion, dependencyInput)
		if err != nil {
			t.Fatal(err)
		}
		assertCoordinationCallCensus(t, graph, discussion, "dependency-put")
		if !reflect.DeepEqual(graph.putRequests[0], dependencyRequest) {
			t.Fatalf("put request=%#v want=%#v", graph.putRequests[0], dependencyRequest)
		}
		wantResponse := BeadsCoordinationResponse{SchemaVersion: 1, OK: true, Action: "dependency-put", DependencyMutation: &want}
		if !reflect.DeepEqual(response, wantResponse) {
			t.Fatalf("response=%#v want=%#v", response, wantResponse)
		}
	})

	t.Run("dependency-remove", func(t *testing.T) {
		want := tracker.DependencyMutation{Dependency: dependencyRequest.Dependency, Noop: true}
		graph := &recordingCoordinationGraph{removeResult: want}
		discussion := &recordingCoordinationDiscussion{}
		response, err := executeBeadsCoordination(ctx, "dependency-remove", projectID, graph, discussion, dependencyInput)
		if err != nil {
			t.Fatal(err)
		}
		assertCoordinationCallCensus(t, graph, discussion, "dependency-remove")
		if !reflect.DeepEqual(graph.removeRequests[0], dependencyRequest) {
			t.Fatalf("remove request=%#v want=%#v", graph.removeRequests[0], dependencyRequest)
		}
		wantResponse := BeadsCoordinationResponse{SchemaVersion: 1, OK: true, Action: "dependency-remove", DependencyMutation: &want}
		if !reflect.DeepEqual(response, wantResponse) {
			t.Fatalf("response=%#v want=%#v", response, wantResponse)
		}
	})

	t.Run("discussion-append", func(t *testing.T) {
		input := beadsCoordinationInput{
			WorkID: "work-a", ExternalID: "bd-a", MetadataSchema: 1, CreatedByOperation: "operation-create-a",
			OperationID: "operation-comment", Author: "agent-a", Body: "durable note", ExpectedObservation: 41,
		}
		wantRequest := tracker.AppendDiscussionRequest{
			ExternalRef: tracker.ExternalWorkRef{
				ProjectID: projectID, WorkID: "work-a", Provider: "beads", ExternalID: "bd-a", MetadataSchema: 1,
				CreatedByOperation: "operation-create-a", LastObservation: 41,
			},
			OperationID: "operation-comment", Author: "agent-a", Body: "durable note", ExpectedObservation: 41,
		}
		want := tracker.DiscussionMutation{Entry: tracker.DiscussionEntry{ExternalCommentID: "comment-1", Author: "agent-a", Body: "durable note"}, Replayed: true}
		graph := &recordingCoordinationGraph{}
		discussion := &recordingCoordinationDiscussion{appendResult: want}
		response, err := executeBeadsCoordination(ctx, "discussion-append", projectID, graph, discussion, input)
		if err != nil {
			t.Fatal(err)
		}
		assertCoordinationCallCensus(t, graph, discussion, "discussion-append")
		if !reflect.DeepEqual(discussion.appendRequests[0], wantRequest) {
			t.Fatalf("append request=%#v want=%#v", discussion.appendRequests[0], wantRequest)
		}
		wantResponse := BeadsCoordinationResponse{SchemaVersion: 1, OK: true, Action: "discussion-append", DiscussionMutation: &want}
		if !reflect.DeepEqual(response, wantResponse) {
			t.Fatalf("response=%#v want=%#v", response, wantResponse)
		}
	})
}

func TestTrackerCoordinateBeadsMutationBranchesPropagatePortErrors(t *testing.T) {
	sentinel := errors.New("recorded port failure")
	projectID := ids.ProjectID("project-1")
	dependencyRequest := tracker.DependencyRequest{
		ProjectID: projectID, OperationID: "operation-edge",
		Dependency: tracker.DependencyRef{BlockedWorkID: "work-blocked", BlockingWorkID: "work-blocking", Kind: tracker.DependencyBlocks},
	}
	dependencyInput := beadsCoordinationInput{OperationID: "operation-edge", BlockedWorkID: "work-blocked", BlockingWorkID: "work-blocking"}
	discussionInput := beadsCoordinationInput{
		WorkID: "work-a", ExternalID: "bd-a", MetadataSchema: 1, CreatedByOperation: "operation-create-a",
		OperationID: "operation-comment", Author: "agent-a", Body: "durable note", ExpectedObservation: 41,
	}
	discussionRequest := tracker.AppendDiscussionRequest{
		ExternalRef: tracker.ExternalWorkRef{
			ProjectID: projectID, WorkID: "work-a", Provider: "beads", ExternalID: "bd-a", MetadataSchema: 1,
			CreatedByOperation: "operation-create-a", LastObservation: 41,
		},
		OperationID: "operation-comment", Author: "agent-a", Body: "durable note", ExpectedObservation: 41,
	}
	tests := []struct {
		name       string
		action     string
		graph      *recordingCoordinationGraph
		discussion *recordingCoordinationDiscussion
		input      beadsCoordinationInput
	}{
		{
			name: "dependency-put", action: "dependency-put", graph: &recordingCoordinationGraph{putErr: sentinel}, discussion: &recordingCoordinationDiscussion{}, input: dependencyInput,
		},
		{
			name: "dependency-remove", action: "dependency-remove", graph: &recordingCoordinationGraph{removeErr: sentinel}, discussion: &recordingCoordinationDiscussion{}, input: dependencyInput,
		},
		{
			name: "discussion-append", action: "discussion-append", graph: &recordingCoordinationGraph{}, discussion: &recordingCoordinationDiscussion{appendErr: sentinel}, input: discussionInput,
		},
	}
	for index := range tests {
		test := &tests[index]
		t.Run(test.name, func(t *testing.T) {
			response, err := executeBeadsCoordination(context.Background(), test.action, projectID, test.graph, test.discussion, test.input)
			assertCoordinationCallCensus(t, test.graph, test.discussion, test.action)
			requestMatches := false
			switch test.action {
			case "dependency-put":
				requestMatches = reflect.DeepEqual(test.graph.putRequests[0], dependencyRequest)
			case "dependency-remove":
				requestMatches = reflect.DeepEqual(test.graph.removeRequests[0], dependencyRequest)
			case "discussion-append":
				requestMatches = reflect.DeepEqual(test.discussion.appendRequests[0], discussionRequest)
			}
			if err != sentinel || !reflect.DeepEqual(response, BeadsCoordinationResponse{}) || !requestMatches {
				t.Fatalf("response=%#v error=%v request_matches=%t", response, err, requestMatches)
			}
		})
	}
}

func TestCoordinationCallCensusRejectsExtraCrossInterfaceCall(t *testing.T) {
	graph := &recordingCoordinationGraph{}
	discussion := &recordingCoordinationDiscussion{}
	_, _ = graph.PutBlockingDependency(context.Background(), tracker.DependencyRequest{})
	_, _ = discussion.AppendDiscussion(context.Background(), tracker.AppendDiscussionRequest{})
	if err := coordinationCallCensusFor(graph, discussion).validate("dependency-put"); err == nil {
		t.Fatal("census accepted an extra wrong cross-interface discussion call")
	}
}

type coordinationCallCensus struct {
	dependencyPut, dependencyRemove, dependencyList, ready, discussionAppend, discussionList int
}

func coordinationCallCensusFor(graph *recordingCoordinationGraph, discussion *recordingCoordinationDiscussion) coordinationCallCensus {
	return coordinationCallCensus{
		dependencyPut: len(graph.putRequests), dependencyRemove: len(graph.removeRequests),
		dependencyList: graph.listCalls, ready: graph.readyCalls,
		discussionAppend: len(discussion.appendRequests), discussionList: discussion.listCalls,
	}
}

func (census coordinationCallCensus) validate(selected string) error {
	methods := []struct {
		name  string
		calls int
	}{
		{"dependency-put", census.dependencyPut},
		{"dependency-remove", census.dependencyRemove},
		{"dependency-list", census.dependencyList},
		{"ready", census.ready},
		{"discussion-append", census.discussionAppend},
		{"discussion-list", census.discussionList},
	}
	for _, method := range methods {
		want := 0
		if method.name == selected {
			want = 1
		}
		if method.calls != want {
			return fmt.Errorf("coordination method %s calls=%d want=%d (selected %s)", method.name, method.calls, want, selected)
		}
	}
	return nil
}

func assertCoordinationCallCensus(t *testing.T, graph *recordingCoordinationGraph, discussion *recordingCoordinationDiscussion, selected string) {
	t.Helper()
	if err := coordinationCallCensusFor(graph, discussion).validate(selected); err != nil {
		t.Fatal(err)
	}
}

type recordingCoordinationGraph struct {
	putRequests    []tracker.DependencyRequest
	removeRequests []tracker.DependencyRequest
	listCalls      int
	readyCalls     int
	putResult      tracker.DependencyMutation
	removeResult   tracker.DependencyMutation
	putErr         error
	removeErr      error
}

func (graph *recordingCoordinationGraph) PutBlockingDependency(_ context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	graph.putRequests = append(graph.putRequests, request)
	return graph.putResult, graph.putErr
}

func (graph *recordingCoordinationGraph) RemoveBlockingDependency(_ context.Context, request tracker.DependencyRequest) (tracker.DependencyMutation, error) {
	graph.removeRequests = append(graph.removeRequests, request)
	return graph.removeResult, graph.removeErr
}

func (graph *recordingCoordinationGraph) ListDependencies(context.Context, tracker.ExternalWorkRef) ([]tracker.DependencyRef, error) {
	graph.listCalls++
	return nil, nil
}

func (graph *recordingCoordinationGraph) ReadyProjection(context.Context, tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	graph.readyCalls++
	return tracker.ReadyProjection{}, nil
}

type recordingCoordinationDiscussion struct {
	appendRequests []tracker.AppendDiscussionRequest
	appendResult   tracker.DiscussionMutation
	appendErr      error
	listCalls      int
}

func (discussion *recordingCoordinationDiscussion) AppendDiscussion(_ context.Context, request tracker.AppendDiscussionRequest) (tracker.DiscussionMutation, error) {
	discussion.appendRequests = append(discussion.appendRequests, request)
	return discussion.appendResult, discussion.appendErr
}

func (discussion *recordingCoordinationDiscussion) ListDiscussion(context.Context, tracker.ExternalWorkRef) ([]tracker.DiscussionEntry, error) {
	discussion.listCalls++
	return nil, nil
}

func TestTrackerCatalogBeadsReadReachesProductionAdapter(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := helperDatabasePath(t, workspace)
	executable := helperCatalogBeadsExecutable(t, root)
	requestPath := filepath.Join(root, "read-request.json")
	request := []byte(`{"work_id":"work-1","external_id":"bd-live-1","metadata_schema":1,"created_by_operation":"operation-create"}`)
	if err := os.WriteFile(requestPath, request, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"tracker", "catalog-beads", "read", "--project", "project-1", "--executable", executable,
		"--workspace", workspace, "--database-identity", databasePath, "--protocol-schema", "1",
		"--mode", "embedded_serialized", "--read-only=false", "--request-file", requestPath,
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var response BeadsCatalogResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Action != "read" || response.Record == nil || response.Mutation != nil || response.Error != nil ||
		response.Record.Reference.WorkID != "work-1" || response.Record.Reference.ExternalID != "bd-live-1" ||
		response.Record.Title != "Live work" || len(response.Record.Criteria) != 1 || response.Record.Observation.Revision == 0 {
		t.Fatalf("response=%+v stdout=%s", response, stdout.String())
	}
}

func TestTrackerCatalogBeadsRejectsUnboundedOrUnknownRequestBeforeAdapter(t *testing.T) {
	root := t.TempDir()
	requestPath := filepath.Join(root, "bad-request.json")
	if err := os.WriteFile(requestPath, []byte(`{"work_id":"work-1","unknown":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"catalog-beads", "read", "--project", "project-1", "--executable", "/missing/bd",
		"--workspace", "/missing/workspace", "--database-identity", "db-1", "--protocol-schema", "1",
		"--mode", "embedded_serialized", "--read-only=true", "--request-file", requestPath,
	}
	var stdout, stderr bytes.Buffer
	if code := RunTracker(context.Background(), args, &stdout, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var response BeadsCatalogResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || response.Error == nil || response.Error.Kind != tracker.ErrorInvalidRequest {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}

func TestTrackerAssignmentBeadsReachesLiveAdapterSaga(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	databasePath := helperDatabasePath(t, workspace)
	executable := helperAssignmentBeadsExecutable(t, root)
	readPath := filepath.Join(root, "read-request.json")
	if err := os.WriteFile(readPath, []byte(`{"work_id":"work-1","external_id":"bd-live-1","metadata_schema":1,"created_by_operation":"operation-create"}`), 0600); err != nil {
		t.Fatal(err)
	}
	bindingArgs := []string{"--project", "project-1", "--executable", executable, "--workspace", workspace, "--database-identity", databasePath, "--protocol-schema", "1", "--read-only=false"}
	var stdout, stderr bytes.Buffer
	readArgs := append([]string{"tracker", "catalog-beads", "read"}, bindingArgs...)
	readArgs = append(readArgs, "--mode", "embedded_serialized", "--request-file", readPath)
	if code := Run(context.Background(), readArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("catalog read code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var catalog BeadsCatalogResponse
	if err := json.Unmarshal(stdout.Bytes(), &catalog); err != nil || catalog.Record == nil || catalog.Record.Observation.Revision == 0 {
		t.Fatalf("catalog observation=%+v err=%v", catalog, err)
	}
	requestPath := filepath.Join(root, "assignment-request.json")
	request := fmt.Sprintf(`{"correlation_id":"correlation-live-assignment","operation_id":"operation-live-assignment","actor_id":"actor-live-assignment","process_id":"process-live-assignment","work_id":"work-1","external_id":"bd-live-1","metadata_schema":1,"created_by_operation":"operation-create","mapping_observation":%d,"expected_observation":%d,"assignee":"actor-live-assignment","required_guarantee":"unguarded"}`,
		catalog.Record.Observation.Revision, catalog.Record.Observation.Revision)
	if err := os.WriteFile(requestPath, []byte(request), 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	assignArgs := append([]string{"tracker", "assignment-beads", "assign"}, bindingArgs...)
	assignArgs = append(assignArgs, "--db", filepath.Join(root, "control.db"), "--request-file", requestPath)
	if code := Run(context.Background(), assignArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("assignment code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var response TrackerResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil || !response.OK || response.Result == nil || response.Result.Kind != "tracker_effect_confirmed" || response.Result.Assignee != "actor-live-assignment" || response.Result.Revision == 0 {
		t.Fatalf("live assignment response=%+v err=%v stdout=%s", response, err, stdout.String())
	}
	compensateRequest := fmt.Sprintf(`{"correlation_id":"correlation-live-assignment","operation_id":"operation-live-assignment","actor_id":"actor-live-assignment","process_id":"process-live-assignment","work_id":"work-1","external_id":"bd-live-1","metadata_schema":1,"created_by_operation":"operation-create","mapping_observation":%d,"expected_observation":%d,"required_guarantee":"unguarded"}`,
		catalog.Record.Observation.Revision, response.Result.Revision)
	if err := os.WriteFile(requestPath, []byte(compensateRequest), 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	compensateArgs := append([]string{"tracker", "assignment-beads", "compensate"}, bindingArgs...)
	compensateArgs = append(compensateArgs, "--db", filepath.Join(root, "control.db"), "--request-file", requestPath)
	if code := Run(context.Background(), compensateArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("compensation code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var refused TrackerResponse
	if err := json.Unmarshal(stdout.Bytes(), &refused); err != nil || refused.OK || refused.Error == nil || refused.Error.Kind != execution.KindSafeCompensationUnsupported || refused.Error.RetrySafety != execution.RetryNever {
		t.Fatalf("live compensation refusal=%+v err=%v stdout=%s", refused, err, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), readArgs, &stdout, &stderr); code != 0 {
		t.Fatalf("post-refusal read code=%d stderr=%q stdout=%q", code, stderr.String(), stdout.String())
	}
	var preserved BeadsCatalogResponse
	if err := json.Unmarshal(stdout.Bytes(), &preserved); err != nil || preserved.Record == nil || preserved.Record.VisibleAssignee != "actor-live-assignment" || preserved.Record.AssignmentMarker != "operation-live-assignment" || preserved.Record.Observation.Revision != response.Result.Revision {
		t.Fatalf("unsafe Beads compensation mutated state=%+v err=%v", preserved, err)
	}
}

func helperBeadsExecutable(t *testing.T, root string, mode tracker.ConcurrencyMode) string {
	t.Helper()
	path := filepath.Join(root, "bd-helper")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
version) printf '%%s\n' '{"schema_version":1,"data":{"version":"1.2.2","build":"helper"}}' ;;
where) printf '{"schema_version":1,"data":{"path":"%%s","prefix":"db-1","database_path":"%%s/embeddeddolt"}}\n' "$BEADS_DIR" "$BEADS_DIR" ;;
dolt)
  case "$2" in
  show)
    if [ "%s" = "server_multiwriter" ]; then
      printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%%s/embeddeddolt","database":"db-1","embedded":false,"host":"127.0.0.1","port":3306,"user":"operator","shared_server":true,"connection_ok":true}}\n' "$BEADS_DIR"
    else
      printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%%s/embeddeddolt","database":"db-1","embedded":true}}\n' "$BEADS_DIR"
    fi ;;
  status)
    if [ "%s" = "server_multiwriter" ]; then
      printf '%%s\n' '{"schema_version":1,"data":{"mode":"external","host":"127.0.0.1","port":3306,"user":"operator","database":"db-1","tls":false,"running":true,"version":"1.70.0"}}'
    else
      printf '{"schema_version":1,"data":{"data_dir":"%%s/embeddeddolt","data_dir_exists":true,"mode":"embedded","server_running":false}}\n' "$BEADS_DIR"
    fi ;;
  *) exit 64 ;;
  esac ;;
*) exit 64 ;;
esac
`, mode, mode)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func helperCatalogBeadsExecutable(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "bd-catalog-helper")
	script := `#!/bin/sh
set -eu
case "$1" in
version) printf '%s\n' '{"schema_version":1,"data":{"version":"1.2.2","build":"helper-catalog"}}' ;;
where) printf '{"schema_version":1,"data":{"path":"%s","prefix":"db-1","database_path":"%s/embeddeddolt"}}\n' "$BEADS_DIR" "$BEADS_DIR" ;;
dolt)
  case "$2" in
  show) printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%s/embeddeddolt","database":"db-1","embedded":true}}\n' "$BEADS_DIR" ;;
  status) printf '{"schema_version":1,"data":{"data_dir":"%s/embeddeddolt","data_dir_exists":true,"mode":"embedded","server_running":false}}\n' "$BEADS_DIR" ;;
  *) exit 64 ;;
  esac ;;
show)
  printf '%s\n' '{"schema_version":1,"data":{"id":"bd-live-1","title":"Live work","description":"Production caller","acceptance":"criterion-1 — required — test — verified","metadata":{"foreign":{"preserved":true},"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[{"id":"criterion-1","text":"verified","verifier":"test","required":true}],"design_context":"helper boundary","content_digest":"diagnostic-only"}},"updated_at":"2026-08-26T12:00:00Z"}}'
  ;;
*) exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func helperCoordinationBeadsExecutable(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "bd-coordination-helper")
	script := `#!/bin/sh
set -eu
issue_a='{"id":"bd-a","title":"Work A","description":"","acceptance":"","metadata":{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-a","created_by_operation":"operation-create-a","last_operation":"operation-create-a","criteria":[],"design_context":"","content_digest":"digest-a"}},"updated_at":"2026-08-26T12:00:00Z"}'
issue_b='{"id":"bd-b","title":"Work B","description":"","acceptance":"","metadata":{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-b","created_by_operation":"operation-create-b","last_operation":"operation-create-b","criteria":[],"design_context":"","content_digest":"digest-b"}},"updated_at":"2026-08-26T12:00:01Z"}'
case "$1:$2" in
version:--json) printf '%s\n' '{"schema_version":1,"data":{"version":"1.2.2","build":"helper-coordination"}}' ;;
where:--json) printf '{"schema_version":1,"data":{"path":"%s","prefix":"db-1","database_path":"%s/embeddeddolt"}}\n' "$BEADS_DIR" "$BEADS_DIR" ;;
dolt:show) printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%s/embeddeddolt","database":"db-1","embedded":true}}\n' "$BEADS_DIR" ;;
dolt:status) printf '{"schema_version":1,"data":{"data_dir":"%s/embeddeddolt","data_dir_exists":true,"mode":"embedded","server_running":false}}\n' "$BEADS_DIR" ;;
list:--json) printf '{"schema_version":1,"data":[%s,%s]}\n' "$issue_a" "$issue_b" ;;
ready:--json) printf '{"schema_version":1,"data":[%s]}\n' "$issue_a" ;;
show:bd-a) printf '{"schema_version":1,"data":%s}\n' "$issue_a" ;;
dep:list) printf '%s\n' '{"schema_version":1,"data":[{"blocked":"bd-a","blocking":"bd-b","kind":"blocks","edge_id":"edge-1"}]}' ;;
comments:bd-a) printf '%s\n' '{"schema_version":1,"data":[{"id":"comment-human","author":"human","body":"human discussion","created_at":"2026-08-26T12:00:02Z"}]}' ;;
*) exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func helperAssignmentBeadsExecutable(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "bd-assignment-helper")
	script := `#!/bin/sh
set -eu
original='{"schema_version":1,"data":{"id":"bd-live-1","title":"Live work","description":"Production caller","acceptance":"criterion-1 — required — test — verified","metadata":{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-create","criteria":[{"id":"criterion-1","text":"verified","verifier":"test","required":true}],"design_context":"helper boundary","content_digest":"diagnostic-only"}},"assignee":"original","updated_at":"2026-08-26T12:00:00Z"}}'
assigned='{"schema_version":1,"data":{"id":"bd-live-1","title":"Live work","description":"Production caller","acceptance":"criterion-1 — required — test — verified","metadata":{"spec_kitty":{"schema_version":1,"project_id":"project-1","work_id":"work-1","created_by_operation":"operation-create","last_operation":"operation-live-assignment","assignment_marker":"operation-live-assignment","criteria":[{"id":"criterion-1","text":"verified","verifier":"test","required":true}],"design_context":"helper boundary","content_digest":"diagnostic-only"}},"assignee":"actor-live-assignment","updated_at":"2026-08-26T12:01:00Z"}}'
case "$1" in
version) printf '%s\n' '{"schema_version":1,"data":{"version":"1.2.2","build":"helper-assignment"}}' ;;
where) printf '{"schema_version":1,"data":{"path":"%s","prefix":"db-1","database_path":"%s/embeddeddolt"}}\n' "$BEADS_DIR" "$BEADS_DIR" ;;
dolt)
  case "$2" in
  show) printf '{"schema_version":1,"data":{"backend":"dolt","data_dir":"%s/embeddeddolt","database":"db-1","embedded":true}}\n' "$BEADS_DIR" ;;
  status) printf '{"schema_version":1,"data":{"data_dir":"%s/embeddeddolt","data_dir_exists":true,"mode":"embedded","server_running":false}}\n' "$BEADS_DIR" ;;
  *) exit 64 ;;
  esac ;;
show) if [ -f "$BEADS_DIR/assigned" ]; then printf '%s\n' "$assigned"; else printf '%s\n' "$original"; fi ;;
update) : > "$BEADS_DIR/assigned"; printf '%s\n' "$assigned" ;;
*) exit 64 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func helperDatabasePath(t *testing.T, workspace string) string {
	t.Helper()
	path := filepath.Join(workspace, "embeddeddolt")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
