package beads

import (
	"bytes"
	"context"
	"encoding/json"
)

const (
	SupportedBeadsVersion          = "1.2.2"
	SupportedProtocolSchema uint16 = 1
)

// Adapter is deliberately small in WP03. Capability implementations added by
// later work packages share this one bounded runner and protocol boundary.
type Adapter struct{ runner commandRunner }

func newAdapter(runner commandRunner) *Adapter {
	if runner == nil {
		runner = newProcessRunner()
	}
	return &Adapter{runner: runner}
}

type commandResponse struct {
	payload  json.RawMessage
	metadata protocolMetadata
	result   commandResult
}

func (adapter *Adapter) execute(ctx context.Context, request command) (commandResponse, error) {
	if adapter == nil || adapter.runner == nil {
		result := commandResult{kind: request.kind, mutationOutcome: mutationNotStarted}
		portable, _ := translateFailure(result, validationError("adapter runner is missing"))
		return commandResponse{result: result}, portable
	}
	result := adapter.runner.run(ctx, request)
	if !result.started {
		portable, _ := translateFailure(result, result.cancelCause)
		return commandResponse{result: result}, portable
	}
	if result.exitCode == nil {
		result.mutationOutcome = ambiguousIfMutation(request, result)
		portable, _ := translateFailure(result, result.cancelCause)
		return commandResponse{result: result}, portable
	}
	if isContextFailure(result.cancelCause) {
		result.mutationOutcome = ambiguousIfMutation(request, result)
		portable, _ := translateFailure(result, result.cancelCause)
		return commandResponse{result: result}, portable
	}
	if result.signaled || result.stdoutTruncated || result.stderrTruncated {
		result.mutationOutcome = ambiguousIfMutation(request, result)
		portable, _ := translateFailure(result, &commandFailure{class: failureOverflow, message: "provider process was signaled or its output was truncated"})
		return commandResponse{result: result}, portable
	}
	metadata := protocolMetadata{warningsPresent: len(bytes.TrimSpace(result.stderr)) > 0, warningCount: countNonEmptyLines(result.stderr)}
	if *result.exitCode != 0 {
		decoded, err := decodeDocumentedError(result.stdout, result.stdoutTruncated)
		if err == nil {
			if request.mutating {
				result.mutationOutcome = mutationNotApplied
			}
			providerCode := decoded.providerCode
			if decoded.shape == documentedDataErrorEnvelope && request.kind == CommandShow {
				providerCode = "not_found"
			}
			cause := &commandFailure{class: failureProvider, providerCode: providerCode, schema: decoded.schemaVersion}
			portable, _ := translateFailure(result, cause)
			metadata.schemaVersion = decoded.schemaVersion
			return commandResponse{metadata: metadata, result: result}, portable
		}
		result.mutationOutcome = ambiguousIfMutation(request, result)
		portable, _ := translateFailure(result, err)
		return commandResponse{result: result}, portable
	}
	if request.shape == expectedEmpty {
		if len(bytes.TrimSpace(result.stdout)) != 0 {
			result.mutationOutcome = ambiguousIfMutation(request, result)
			portable, _ := translateFailure(result, &commandFailure{class: failureProtocol})
			return commandResponse{metadata: metadata, result: result}, portable
		}
		if request.mutating {
			result.mutationOutcome = mutationApplied
		}
		return commandResponse{metadata: metadata, result: result}, nil
	}
	if request.shape == expectedJSONL {
		portable, _ := translateFailure(result, validationError("export requires the streaming decoder"))
		return commandResponse{metadata: metadata, result: result}, portable
	}
	payload, decodedMetadata, err := decodeEnvelope(result.stdout, request.shape)
	decodedMetadata.warningsPresent, decodedMetadata.warningCount = metadata.warningsPresent, metadata.warningCount
	if err != nil {
		result.mutationOutcome = ambiguousIfMutation(request, result)
		portable, _ := translateFailure(result, err)
		return commandResponse{metadata: decodedMetadata, result: result}, portable
	}
	if request.mutating {
		result.mutationOutcome = mutationApplied
	}
	return commandResponse{payload: payload, metadata: decodedMetadata, result: result}, nil
}

func ambiguousIfMutation(request command, result commandResult) mutationOutcome {
	if request.mutating && result.started {
		return mutationAmbiguous
	}
	return result.mutationOutcome
}
