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
	executable := helperBeadsExecutable(t, root, tracker.ConcurrencyEmbeddedSerialized)
	args := []string{"tracker", "inspect-beads", "--project", "project-1", "--executable", executable, "--workspace", workspace, "--database-identity", "db-1", "--protocol-schema", "1", "--mode", "embedded_serialized", "--read-only=false"}
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
	executable := helperBeadsExecutable(t, root, tracker.ConcurrencyServerMultiwriter)
	secret := "super-secret-password"
	args := []string{"tracker", "inspect-beads", "--project", "project-1", "--executable", executable, "--workspace", workspace, "--database-identity", "db-1", "--protocol-schema", "1", "--mode", "server_multiwriter", "--read-only=false", "--server-identity", "server-1", "--server-endpoint", "127.0.0.1:3306", "--dolt-server-host", "127.0.0.1", "--dolt-server-port", "3306", "--dolt-remote-user", "operator", "--dolt-remote-password", secret}
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
				"--workspace", workspace, "--database-identity", "db-1", "--protocol-schema", "1",
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

func TestTrackerCatalogBeadsReadReachesProductionAdapter(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	executable := helperCatalogBeadsExecutable(t, root)
	requestPath := filepath.Join(root, "read-request.json")
	request := []byte(`{"work_id":"work-1","external_id":"bd-live-1","metadata_schema":1,"created_by_operation":"operation-create"}`)
	if err := os.WriteFile(requestPath, request, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"tracker", "catalog-beads", "read", "--project", "project-1", "--executable", executable,
		"--workspace", workspace, "--database-identity", "db-1", "--protocol-schema", "1",
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

func helperBeadsExecutable(t *testing.T, root string, mode tracker.ConcurrencyMode) string {
	t.Helper()
	path := filepath.Join(root, "bd-helper")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
case "$1" in
version) printf '%%s\n' '{"schema_version":1,"data":{"version":"1.2.2","build":"helper"}}' ;;
where) printf '{"schema_version":1,"data":{"workspace_path":"%%s","database_id":"db-1"}}\n' "$BEADS_DIR" ;;
info) printf '{"schema_version":1,"data":{"protocol_schema":1,"storage_mode":"%s","read_only":false,"server_id":"server-1","server_endpoint":"127.0.0.1:3306"}}\n' ;;
doctor) printf '%%s\n' '{"schema_version":1,"data":{"healthy":true,"server_id":"server-1","server_endpoint":"127.0.0.1:3306"}}' ;;
*) exit 64 ;;
esac
`, mode)
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
