package beads

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type failureClass string

const (
	failureValidation failureClass = "validation"
	failureSpawn      failureClass = "spawn"
	failureTimeout    failureClass = "timeout"
	failureCanceled   failureClass = "canceled"
	failureExit       failureClass = "exit"
	failureOverflow   failureClass = "overflow"
	failureProtocol   failureClass = "protocol"
	failureProvider   failureClass = "provider"
	failureSchema     failureClass = "schema"
	failureVersion    failureClass = "version"
)

type commandFailure struct {
	class        failureClass
	message      string
	cause        error
	providerCode string
	schema       uint16
	version      string
}

func (failure *commandFailure) Error() string {
	if failure == nil {
		return "beads command failure"
	}
	if failure.message != "" {
		return "beads command failure: " + failure.message
	}
	return "beads command failure: " + string(failure.class)
}

func (failure *commandFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

// commandDiagnostic is deliberately payload-free. It is safe to serialize as
// routine operator evidence.
type commandDiagnostic struct {
	Kind            tracker.ErrorKind `json:"kind"`
	RecoveryCode    string            `json:"recovery_code"`
	Command         CommandKind       `json:"command"`
	Started         bool              `json:"started"`
	Signaled        bool              `json:"signaled"`
	ExitCode        *int              `json:"exit_code,omitempty"`
	MutationOutcome mutationOutcome   `json:"mutation_outcome"`
	Schema          uint16            `json:"schema,omitempty"`
	Version         string            `json:"version,omitempty"`
	Duration        time.Duration     `json:"duration"`
	StdoutTruncated bool              `json:"stdout_truncated"`
	StderrTruncated bool              `json:"stderr_truncated"`
}

func translateFailure(result commandResult, cause error) (*tracker.Error, commandDiagnostic) {
	diagnostic := commandDiagnostic{
		Command: result.kind, Started: result.started, Signaled: result.signaled, ExitCode: result.exitCode,
		MutationOutcome: result.mutationOutcome, Duration: result.duration,
		StdoutTruncated: result.stdoutTruncated, StderrTruncated: result.stderrTruncated,
	}
	kind, recovery, message := tracker.ErrorMalformedProtocol, "repair_provider_binding", "provider response violated the automation protocol"
	var protocol *protocolError
	var failure *commandFailure
	switch {
	case errors.As(cause, &protocol) && protocol.cause == protocolSchema:
		kind, recovery, message = tracker.ErrorSchemaSkew, "install_supported_provider", "provider protocol schema is unsupported"
		diagnostic.Schema = protocol.schema
	case errors.As(cause, &protocol) && protocol.cause == protocolCanceled:
		kind, recovery, message = tracker.ErrorTimeout, "observe_then_reconcile", "provider operation was canceled"
	case errors.As(cause, &protocol):
		kind, recovery = tracker.ErrorMalformedProtocol, "repair_provider_binding"
	case errors.As(cause, &failure):
		diagnostic.Schema, diagnostic.Version = failure.schema, failure.version
		switch failure.class {
		case failureValidation:
			kind, recovery, message = tracker.ErrorInvalidRequest, "repair_request", "provider command request is invalid"
		case failureSpawn:
			kind, recovery, message = tracker.ErrorUnavailable, "repair_provider_binding", "provider executable is unavailable"
		case failureTimeout, failureCanceled:
			kind, recovery, message = tracker.ErrorTimeout, "observe_then_reconcile", "provider command deadline expired"
		case failureVersion:
			kind, recovery, message = tracker.ErrorVersionSkew, "install_supported_provider", "provider executable version is unsupported"
		case failureSchema:
			kind, recovery, message = tracker.ErrorSchemaSkew, "install_supported_provider", "provider protocol schema is unsupported"
		case failureProvider:
			kind, recovery, message = providerCodeKind(failure.providerCode), "repair_provider_state", "provider rejected the command"
		case failureExit, failureOverflow, failureProtocol:
			kind, recovery = tracker.ErrorMalformedProtocol, "repair_provider_binding"
		}
	case errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, context.Canceled):
		kind, recovery, message = tracker.ErrorTimeout, "observe_then_reconcile", "provider command deadline expired"
	}
	if result.started && result.mutationOutcome == mutationAmbiguous {
		kind, recovery, message = tracker.ErrorAmbiguous, "observe_then_reconcile", "provider mutation outcome is ambiguous"
	}
	diagnostic.Kind, diagnostic.RecoveryCode = kind, recovery
	return &tracker.Error{Kind: kind, Message: message, RecoveryCode: recovery, Cause: cause}, diagnostic
}

func providerCodeKind(code string) tracker.ErrorKind {
	switch code {
	case "not_found", "issue_not_found":
		return tracker.ErrorNotFound
	case "invalid_request", "validation_error", "cycle":
		return tracker.ErrorInvalidRequest
	case "lock_contended", "database_locked":
		return tracker.ErrorLockContended
	default:
		return tracker.ErrorUnavailable
	}
}

type executableIdentity struct {
	Version string `json:"version"`
	Build   string `json:"build"`
}

func decodeExecutableIdentity(payload json.RawMessage) (executableIdentity, error) {
	var identity executableIdentity
	if err := json.Unmarshal(payload, &identity); err != nil || identity.Version == "" {
		return executableIdentity{}, &commandFailure{class: failureProtocol, message: "executable identity payload is malformed", cause: err}
	}
	if identity.Version != SupportedBeadsVersion {
		return executableIdentity{}, &commandFailure{class: failureVersion, version: identity.Version, message: "executable version is unsupported"}
	}
	return identity, nil
}

func diagnosticJSON(diagnostic commandDiagnostic) ([]byte, error) {
	payload, err := json.Marshal(diagnostic)
	if err != nil {
		return nil, fmt.Errorf("encode command diagnostic: %w", err)
	}
	return payload, nil
}
