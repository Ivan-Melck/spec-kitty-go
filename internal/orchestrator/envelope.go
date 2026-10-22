// Package orchestrator holds the machine contract of `spec-kitty
// orchestrator-api` (upstream specify_cli.orchestrator_api): the canonical
// JSON response envelope, the --policy metadata validation, the vendored
// contract allow-lists and the mission resolution / identity helpers every
// verb shares.
package orchestrator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ContractVersion is the orchestrator-api contract version (envelope.py).
const ContractVersion = "1.18.0"

// MinProviderVersion is the minimum supported provider version.
const MinProviderVersion = "0.1.0"

// JSON is json.dumps(obj) with default options (one line, ", " / ": "
// separators, ensure_ascii).
var JSON = jsonx.Options{Indent: -1, EnsureASCII: true}

// NewCorrelationID returns "corr-" + uuid4().hex.
func NewCorrelationID() string { return "corr-" + randomHex() }

// NewHistoryEntryID returns "hist-" + uuid4().hex.
func NewHistoryEntryID() string { return "hist-" + randomHex() }

func randomHex() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[:])
}

// Envelope builds the canonical 7-key response envelope (make_envelope).
// A nil data becomes {}; an empty code means error_code null.
func Envelope(command string, success bool, data *jsonx.Object, errorCode string) *jsonx.Object {
	if data == nil {
		data = jsonx.NewObject()
	}
	var code any
	if errorCode != "" {
		code = errorCode
	}
	return jsonx.NewObject(
		"contract_version", ContractVersion,
		"command", "orchestrator-api."+command,
		"timestamp", clock.NowUTCISO(),
		"correlation_id", NewCorrelationID(),
		"success", success,
		"error_code", code,
		"data", data,
	)
}

// Emit prints an envelope as one json.dumps line.
func Emit(w io.Writer, env *jsonx.Object) {
	fmt.Fprintln(w, jsonx.Dumps(env, JSON))
}

// Failure is the data payload of a failed verb: data merged with the
// canonical message last-wins (_fail).
func Failure(message string, data *jsonx.Object) *jsonx.Object {
	out := jsonx.NewObject()
	if data != nil {
		out = data.Clone()
	}
	out.Set("message", message)
	return out
}

// Fail prints a failure envelope for command (exit code is the caller's: 1).
func Fail(w io.Writer, command, code, message string, data *jsonx.Object) {
	Emit(w, Envelope(command, false, Failure(message, data), code))
}

// UsageError prints the USAGE_ERROR envelope (command "unknown") used for
// every parser-level failure of the group.
func UsageError(w io.Writer, message string) {
	Emit(w, Envelope("unknown", false, jsonx.NewObject("message", message), "USAGE_ERROR"))
}
