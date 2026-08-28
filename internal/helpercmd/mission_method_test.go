package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/missiontype/softwaredev"
)

func TestMissionInitializePersistsExactCreationAuthorityAndReplaysAfterReopen(t *testing.T) {
	definition, err := softwaredev.Definition()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC)
	request := initializeRequest{
		Database: filepath.Join(t.TempDir(), "control.db"), CorrelationID: "correlation-initialize",
		MissionID: "mission-method-initialize", ProjectID: "project-method", DefinitionID: definition.ID(),
		DefinitionVersion: definition.Version(), OperationID: "operation-method-initialize", ActorID: "actor-method",
		ProcessID: "process-method", Governance: methodGovernance(t, "governance-method", at), OccurredAt: at,
	}
	first := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, request)})
	second := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, request)})
	if !first.OK || first.Kind != "mission_initialized" || first.RetrySafety != "replay_same_operation" || first.Recovery != "none" || !second.OK || second.Kind != first.Kind {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	firstResult := resultObject(t, first)
	secondResult := resultObject(t, second)
	if firstResult["actor_id"] != "actor-method" || firstResult["process_id"] != "process-method" || firstResult["governance_context_id"] != "governance-method" || secondResult["replayed"] != true {
		t.Fatalf("first result=%v second result=%v", firstResult, secondResult)
	}

	changed := request
	changed.ProcessID = "process-method-other"
	conflict := invokeMethod(t, RunMission, []string{"initialize", "--request-json", marshalMethod(t, changed)})
	if conflict.OK || conflict.Error == nil || conflict.Error.Kind != "idempotency_conflict" {
		t.Fatalf("changed replay=%+v", conflict)
	}
}

func TestStrictMethodJSONRejectsUnknownDuplicateNullTrailingAndLeftover(t *testing.T) {
	cases := map[string][]string{
		"unknown":   {"next", "--request-json", `{"database":"db","correlation_id":"c","mission_id":"mission-01","extra":1}`},
		"duplicate": {"next", "--request-json", `{"database":"db","database":"other","correlation_id":"c","mission_id":"mission-01"}`},
		"null":      {"next", "--request-json", `{"database":"db","correlation_id":null,"mission_id":"mission-01"}`},
		"trailing":  {"next", "--request-json", `{"database":"db","correlation_id":"c","mission_id":"mission-01"} []`},
		"leftover":  {"next", "--request-json", `{"database":"db","correlation_id":"c","mission_id":"mission-01"}`, "unexpected"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			response := invokeMethodUsage(t, RunMission, args)
			if response.OK || response.Kind != "invalid_invocation" || response.Error == nil || response.Error.Message == "" || len(response.Error.Message) > 512 || response.RetrySafety == "" || response.Recovery == "" {
				t.Fatalf("response=%+v", response)
			}
		})
	}
}

func TestMissionInitializeMissingCreationProvenanceFailsBeforeStoreOpen(t *testing.T) {
	definition, _ := softwaredev.Definition()
	at := time.Date(2026, 8, 28, 15, 30, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*initializeRequest){
		"process":    func(request *initializeRequest) { request.ProcessID = "" },
		"governance": func(request *initializeRequest) { request.Governance = governancecontext.Context{} },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.db")
			request := initializeRequest{Database: path, CorrelationID: "correlation-missing-" + name, MissionID: ids.MissionID("mission-missing-" + name), ProjectID: "project-missing", DefinitionID: definition.ID(), DefinitionVersion: definition.Version(), OperationID: ids.OperationID("operation-missing-" + name), ActorID: "actor-method", ProcessID: "process-method", Governance: methodGovernance(t, "governance-missing-"+name, at), OccurredAt: at}
			mutate(&request)
			args := []string{"initialize", "--request-json", marshalMethod(t, request)}
			response := MethodResponse{}
			if name == "governance" {
				response = invokeMethodUsage(t, RunMission, args)
			} else {
				response = invokeMethod(t, RunMission, args)
			}
			if response.OK || response.Error == nil {
				t.Fatalf("response=%+v", response)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("store opened for missing provenance: %v", err)
			}
		})
	}
}

func invokeMethod(t *testing.T, run func(context.Context, []string, io.Writer, io.Writer) int, args []string) MethodResponse {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response MethodResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", stdout.String(), err)
	}
	return response
}

func invokeMethodUsage(t *testing.T, run func(context.Context, []string, io.Writer, io.Writer) int, args []string) MethodResponse {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var response MethodResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", stdout.String(), err)
	}
	return response
}

func marshalMethod(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func resultObject(t *testing.T, response MethodResponse) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func methodGovernance(t *testing.T, id string, at time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.NewCommitment(governancecontext.AlgorithmSHA256, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", "1", commitment)
	if err != nil {
		t.Fatal(err)
	}
	context, err := governancecontext.NewContext(ids.GovernanceContextID(id), governancecontext.SchemaVersion, []governancecontext.Entry{entry}, at)
	if err != nil {
		t.Fatal(err)
	}
	return context
}
