package helpercmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
)

// These tests exercise the existing native entry point, not a parallel parser
// or a compatibility facade. Invalid wire identity must fail before authority.
func TestNativeRejectsAmbiguousRequestIdentityBeforeAuthority(t *testing.T) {
	request, err := json.Marshal(missionCreateRequest(t, "operation-wire-identity"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := json.Marshal(DrivePlan{SchemaVersion: RequestSchemaVersion, Operations: []OperationRequest{missionCreateRequest(t, "operation-wire-plan")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
		wire string
	}{
		{"duplicate action", []string{"operation", "execute", "--request", "-"}, strings.Replace(string(request), `"action":`, `"action":"mission.cancel","action":`, 1)},
		{"escaped duplicate action", []string{"operation", "resume", "--request", "-"}, strings.Replace(string(request), `"action":`, `"\u0061ction":"mission.cancel","action":`, 1)},
		{"case folded action", []string{"operation", "execute", "--request", "-"}, strings.Replace(string(request), `"action":`, `"Action":`, 1)},
		{"duplicate plan schema", []string{"drive", "--plan", "-"}, strings.Replace(string(plan), `"schema_version":1`, `"schema_version":2,"schema_version":1`, 1)},
		{"duplicate operation in plan", []string{"drive", "--plan", "-"}, strings.Replace(string(plan), `"action":`, `"action":"mission.cancel","action":`, 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			factory := admittedFactory(defaultSession())
			runner, err := NewRunner(factory)
			if err != nil {
				t.Fatal(err)
			}
			args := append([]string{"--control-root", t.TempDir()}, test.args...)
			var stdout, stderr bytes.Buffer
			code := runner.Run(t.Context(), args, strings.NewReader(test.wire), &stdout, &stderr)
			var envelope Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if code != ExitInvalid || envelope.Outcome != OutcomeInvalid || envelope.Diagnostic == nil || envelope.Diagnostic.Kind != "INVALID_COMMAND" {
				t.Fatalf("ambiguous wire accepted: code=%d output=%s", code, stdout.Bytes())
			}
			if factory.classified != 0 || factory.observed != 0 || factory.started != 0 || factory.session.executes != 0 {
				t.Fatal("invalid request crossed the authority boundary")
			}
		})
	}
}

func TestNativeBoundedDecoderPreservesUnicodeRatherThanRepairingIt(t *testing.T) {
	for _, wire := range []string{
		`{"action":"` + string([]byte{0xff}) + `"}`,
		`{"action":"\ud800"}`,
		`{"action":"\udc00"}`,
		`{"action":"\ud800\u0041"}`,
	} {
		var request OperationRequest
		if err := readBoundedJSON(strings.NewReader(wire), &request); err == nil {
			t.Errorf("malformed Unicode was repaired: %q -> %q", wire, request.Action)
		}
	}
	var request OperationRequest
	if err := readBoundedJSON(strings.NewReader(`{"action":"雪\ud83d\ude00"}`), &request); err != nil || request.Action != "雪😀" {
		t.Fatalf("valid Unicode lost: action=%q err=%v", request.Action, err)
	}
}

func TestDriveReportsEveryOutcomeInFailedConcurrentWave(t *testing.T) {
	session := defaultSession()
	session.progress.Lifecycle = string(mission.MissionOpen)
	session.progress.Actions = []productionruntime.ActionAvailability{
		{Action: string(mission.ActionExecutionClaim), Scope: "work:work-left", Status: "authorized"},
		{Action: string(mission.ActionExecutionClaim), Scope: "work:work-right", Status: "authorized"},
	}
	session.executeFn = func(operation decodedOperation) (TypedResult, error) {
		if operation.scope == "work:work-left" {
			return TypedResult{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityVersionConflictV2, Field: "version"}
		}
		return TypedResult{Kind: ResultExecution, Payload: json.RawMessage(`{"committed":true}`)}, nil
	}
	plan, err := json.Marshal(DrivePlan{SchemaVersion: RequestSchemaVersion, Operations: []OperationRequest{
		executionClaimRequest(t, "work-left", "operation-wave-failed"),
		executionClaimRequest(t, "work-right", "operation-wave-committed"),
		executionClaimRequest(t, "work-right", "operation-next-wave-must-not-run"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	runner, _ := NewRunner(admittedFactory(session))
	var stdout, stderr bytes.Buffer
	code := runner.Run(t.Context(), []string{"--control-root", t.TempDir(), "drive", "--plan", "-"}, bytes.NewReader(plan), &stdout, &stderr)
	var envelope Envelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if code != ExitRefused || envelope.Drive == nil || envelope.Drive.Terminal || envelope.Drive.StopReason != "operation_failed" || len(envelope.Drive.Steps) != 2 {
		t.Fatalf("failed wave lost committed outcomes: code=%d output=%s", code, stdout.Bytes())
	}
	steps := envelope.Drive.Steps
	if steps[0].OperationID != "operation-wave-failed" || steps[0].Outcome != OutcomeConflict || steps[1].OperationID != "operation-wave-committed" || steps[1].Outcome != OutcomeSuccess || steps[1].Result == nil || !bytes.Equal(steps[1].Result.Payload, []byte(`{"committed":true}`)) {
		t.Fatalf("wave outcomes were changed: %+v", steps)
	}
	if session.executes != 2 {
		t.Fatalf("later wave ran after failure: executes=%d", session.executes)
	}
}

func TestShutdownFailurePreservesCommittedCommandEvidence(t *testing.T) {
	for _, verb := range []string{"execute", "drive"} {
		t.Run(verb, func(t *testing.T) {
			session := defaultSession()
			session.closeErr = &productionruntime.Error{Kind: productionruntime.ErrorShutdown, Field: "owner"}
			session.progress.Lifecycle = string(mission.MissionCompleted)
			session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionMissionCreate), Scope: "mission:mission-1", Status: "authorized"}}
			session.executeFn = func(decodedOperation) (TypedResult, error) {
				session.mu.Lock()
				session.status.ProjectionRevision = 42
				session.mu.Unlock()
				return TypedResult{Kind: ResultMission, Payload: json.RawMessage(`{"committed":true}`)}, nil
			}
			request := missionCreateRequest(t, "operation-close-failure")
			wire, _ := json.Marshal(request)
			args := []string{"--control-root", t.TempDir(), "operation", verb, "--request", "-"}
			if verb == "drive" {
				wire, _ = json.Marshal(DrivePlan{SchemaVersion: RequestSchemaVersion, Operations: []OperationRequest{request}})
				args = []string{"--control-root", t.TempDir(), "drive", "--plan", "-"}
			}
			runner, _ := NewRunner(admittedFactory(session))
			var stdout, stderr bytes.Buffer
			code := runner.Run(t.Context(), args, bytes.NewReader(wire), &stdout, &stderr)
			var envelope Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if code != ExitUnavailable || envelope.Outcome != OutcomeUnavailable || envelope.Diagnostic == nil || envelope.Diagnostic.Kind != string(productionruntime.ErrorShutdown) || envelope.ProjectionRevision != 42 || envelope.DatabaseID != session.status.DatabaseID || envelope.RuntimeEpochID != session.status.RuntimeEpochID {
				t.Fatalf("shutdown discarded command coordinate: code=%d output=%s", code, stdout.Bytes())
			}
			result := envelope.Result
			if verb == "drive" {
				if envelope.Drive == nil || len(envelope.Drive.Steps) != 1 || envelope.Drive.Steps[0].Outcome != OutcomeSuccess {
					t.Fatalf("shutdown discarded drive outcome: %s", stdout.Bytes())
				}
				result = envelope.Drive.Steps[0].Result
			} else if envelope.RequestSHA256 == "" {
				t.Fatal("shutdown discarded request identity")
			}
			if result == nil || !bytes.Equal(result.Payload, []byte(`{"committed":true}`)) {
				t.Fatalf("shutdown discarded committed result: %s", stdout.Bytes())
			}
		})
	}
}

func TestMutationEnvelopeReportsPostCommandProjectionRevision(t *testing.T) {
	for _, verb := range []string{"execute", "resume", "drive"} {
		for _, failed := range []bool{false, true} {
			t.Run(verb+"/"+map[bool]string{false: "success", true: "failure"}[failed], func(t *testing.T) {
				session := defaultSession()
				session.status.ProjectionRevision = 10
				session.progress.Lifecycle = string(mission.MissionCompleted)
				session.progress.Actions = []productionruntime.ActionAvailability{{Action: string(mission.ActionMissionCreate), Scope: "mission:mission-1", Status: "authorized"}}
				session.executeFn = func(decodedOperation) (TypedResult, error) {
					// Even a refused external saga can have durable intent to inspect.
					session.mu.Lock()
					session.status.ProjectionRevision = 11
					session.mu.Unlock()
					if failed {
						return TypedResult{}, &mission.MissionAuthorityErrorV2{Kind: mission.MissionAuthorityVersionConflictV2, Field: "version"}
					}
					return TypedResult{Kind: ResultMission, Payload: json.RawMessage(`{"committed":true}`)}, nil
				}
				request := missionCreateRequest(t, "operation-post-command")
				var wire []byte
				args := []string{"--control-root", t.TempDir()}
				if verb == "drive" {
					wire, _ = json.Marshal(DrivePlan{SchemaVersion: RequestSchemaVersion, Operations: []OperationRequest{request}})
					args = append(args, "drive", "--plan", "-")
				} else {
					wire, _ = json.Marshal(request)
					args = append(args, "operation", verb, "--request", "-")
				}
				runner, _ := NewRunner(admittedFactory(session))
				var stdout, stderr bytes.Buffer
				code := runner.Run(t.Context(), args, bytes.NewReader(wire), &stdout, &stderr)
				var envelope Envelope
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if (!failed && code != ExitSuccess) || (failed && code == ExitSuccess) || envelope.ProjectionRevision != 11 || envelope.DatabaseID != journalv2.DatabaseID(session.status.DatabaseID) || envelope.RuntimeEpochID != session.status.RuntimeEpochID {
					t.Fatalf("stale post-command coordinate: code=%d output=%s", code, stdout.Bytes())
				}
			})
		}
	}
}
