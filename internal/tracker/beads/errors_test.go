package beads

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestTranslateFailureStableKinds(t *testing.T) {
	exit := 9
	base := commandResult{kind: CommandShow, exitCode: &exit, duration: time.Millisecond, started: true, mutationOutcome: mutationUnknown}
	tests := []struct {
		name     string
		cause    error
		mutate   func(*commandResult)
		kind     tracker.ErrorKind
		recovery string
	}{
		{"validation", &commandFailure{class: failureValidation}, func(r *commandResult) { r.started = false }, tracker.ErrorInvalidRequest, "repair_request"},
		{"spawn", &commandFailure{class: failureSpawn}, func(r *commandResult) { r.started = false }, tracker.ErrorUnavailable, "repair_provider_binding"},
		{"timeout", &commandFailure{class: failureTimeout}, nil, tracker.ErrorTimeout, "observe_then_reconcile"},
		{"cancel", context.Canceled, nil, tracker.ErrorTimeout, "observe_then_reconcile"},
		{"version", &commandFailure{class: failureVersion, version: "9.9.9"}, nil, tracker.ErrorVersionSkew, "install_supported_provider"},
		{"schema", &commandFailure{class: failureSchema, schema: 2}, nil, tracker.ErrorSchemaSkew, "install_supported_provider"},
		{"protocol schema", &protocolError{cause: protocolSchema, schema: 2}, nil, tracker.ErrorSchemaSkew, "install_supported_provider"},
		{"protocol cancel", &protocolError{cause: protocolCanceled}, nil, tracker.ErrorTimeout, "observe_then_reconcile"},
		{"malformed", &protocolError{cause: protocolMalformed}, nil, tracker.ErrorMalformedProtocol, "repair_provider_binding"},
		{"exit", &commandFailure{class: failureExit}, nil, tracker.ErrorMalformedProtocol, "repair_provider_binding"},
		{"overflow", &commandFailure{class: failureOverflow}, nil, tracker.ErrorMalformedProtocol, "repair_provider_binding"},
		{"not found", &commandFailure{class: failureProvider, providerCode: "not_found"}, nil, tracker.ErrorNotFound, "repair_provider_state"},
		{"invalid", &commandFailure{class: failureProvider, providerCode: "cycle"}, nil, tracker.ErrorInvalidRequest, "repair_provider_state"},
		{"locked", &commandFailure{class: failureProvider, providerCode: "database_locked"}, nil, tracker.ErrorLockContended, "repair_provider_state"},
		{"unknown provider", &commandFailure{class: failureProvider, providerCode: "other"}, nil, tracker.ErrorUnavailable, "repair_provider_state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := base
			if test.mutate != nil {
				test.mutate(&result)
			}
			got, diagnostic := translateFailure(result, test.cause)
			if !tracker.IsKind(got, test.kind) || diagnostic.Kind != test.kind || diagnostic.RecoveryCode != test.recovery {
				t.Fatalf("got=%v diagnostic=%+v", got, diagnostic)
			}
			var typed *tracker.Error
			if !errors.As(got, &typed) {
				t.Fatal("not typed")
			}
		})
	}
}

func TestMutationAmbiguityIsMonotonicAndRedacted(t *testing.T) {
	secret := "ERROR_SECRET_SENTINEL"
	result := commandResult{kind: CommandCreate, started: true, mutationOutcome: mutationAmbiguous, stdout: []byte(secret), stderr: []byte(secret), duration: time.Second}
	portable, diagnostic := translateFailure(result, &commandFailure{class: failureValidation, message: secret, cause: errors.New(secret)})
	if !tracker.IsKind(portable, tracker.ErrorAmbiguous) || diagnostic.MutationOutcome != mutationAmbiguous {
		t.Fatalf("portable=%v diagnostic=%+v", portable, diagnostic)
	}
	payload, err := diagnosticJSON(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(portable.Error(), secret) || strings.Contains(string(payload), secret) {
		t.Fatalf("secret leaked: %s / %s", portable, payload)
	}
}

func TestExecutableIdentityCompatibility(t *testing.T) {
	identity, err := decodeExecutableIdentity(json.RawMessage(`{"version":"1.2.2","build":"release","future":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if identity.Version != SupportedBeadsVersion || SupportedProtocolSchema != 1 {
		t.Fatalf("identity=%+v", identity)
	}
	for _, payload := range []string{`{}`, `{"version":"1.2.2"}`, `{"version":"1.2.2","build":""}`, `{"version":"1.2.3","build":"release"}`, `not-json`} {
		_, err := decodeExecutableIdentity(json.RawMessage(payload))
		if err == nil {
			t.Fatalf("payload %s accepted", payload)
		}
	}
	if SupportedBeadsVersion != "1.2.2" || SupportedProtocolSchema != 1 {
		t.Fatalf("compatibility tuple=(%q,%d)", SupportedBeadsVersion, SupportedProtocolSchema)
	}
}

func TestCommandFailureAndProtocolFailureTypedWrapping(t *testing.T) {
	sentinel := errors.New("sentinel")
	commandErr := &commandFailure{class: failureSpawn, cause: sentinel}
	if !errors.Is(commandErr, sentinel) || commandErr.Error() == "" {
		t.Fatal("command wrapping")
	}
	protocolErr := &protocolError{cause: protocolMalformed, underlying: sentinel, record: 2, bytes: 10}
	if !errors.Is(protocolErr, sentinel) || !strings.Contains(protocolErr.Error(), "record=2") {
		t.Fatal("protocol wrapping")
	}
	var nilFailure *commandFailure
	if nilFailure.Error() == "" || nilFailure.Unwrap() != nil {
		t.Fatal("nil failure")
	}
}

func TestAdapterCompositionUsesNarrowRunner(t *testing.T) {
	fake := &fakeRunner{result: commandResult{kind: CommandShow, started: true, exitCode: intPointer(0), stdout: []byte(`{"schema_version":1,"data":[{"id":"x"}]}`)}}
	adapter := newAdapter(fake)
	binding := defaultBinding("/opt/bd", "/srv/beads", "/repo")
	binding.timeout = time.Second
	cmd, err := newShowCommand(binding, "x")
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.execute(context.Background(), cmd)
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 1 || string(response.payload) != `[{"id":"x"}]` {
		t.Fatalf("calls=%d payload=%s", fake.calls, response.payload)
	}
	if got := newAdapter(nil); got.runner == nil {
		t.Fatal("default runner absent")
	}
}

type fakeRunner struct {
	result commandResult
	calls  int
}

func (r *fakeRunner) run(context.Context, command) commandResult { r.calls++; return r.result }
func intPointer(value int) *int                                  { return &value }

func TestAdapterRejectsMissingRunnerAndStreamingMisuse(t *testing.T) {
	var adapter *Adapter
	_, err := adapter.execute(context.Background(), command{})
	if err == nil {
		t.Fatal("expected missing adapter error")
	}
	binding := defaultBinding("/opt/bd", "/srv/beads", "/repo")
	binding.timeout = time.Second
	cmd, err := newExportCommand(binding)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeRunner{result: commandResult{kind: CommandExport, started: true, exitCode: intPointer(0)}}
	_, err = newAdapter(fake).execute(context.Background(), cmd)
	if err == nil {
		t.Fatal("expected streaming seam refusal")
	}
}
