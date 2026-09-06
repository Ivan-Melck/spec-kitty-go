// Package helpercmd is the local application adapter for the native
// spec-kitty command. It owns argument decoding and presentation only; all
// authority and recovery remain inside productionruntime.Host.
package helpercmd

import (
	"encoding/json"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
)

const (
	EnvelopeSchemaVersion = uint32(1)
	RequestSchemaVersion  = uint32(1)
	MaxRequestBytes       = int64(4 << 20)
	MaxDriveOperations    = 256
)

type Outcome string

const (
	OutcomeSuccess     Outcome = "success"
	OutcomeStopped     Outcome = "stopped"
	OutcomeInvalid     Outcome = "invalid"
	OutcomeRefused     Outcome = "refused"
	OutcomeNotFound    Outcome = "not_found"
	OutcomeConflict    Outcome = "conflict"
	OutcomeAmbiguous   Outcome = "ambiguous"
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeCanceled    Outcome = "canceled"
)

const (
	ExitSuccess     = 0
	ExitInvalid     = 2
	ExitRefused     = 3
	ExitUnavailable = 4
	ExitAmbiguous   = 5
	ExitConflict    = 6
	ExitNotFound    = 7
	ExitCanceled    = 130
)

type Diagnostic struct {
	Kind            string   `json:"kind"`
	Field           string   `json:"field,omitempty"`
	RetrySafety     string   `json:"retry_safety"`
	RecoveryActions []string `json:"recovery_actions,omitempty"`
}

// Envelope is the only machine output. Result is canonical JSON produced by
// a typed kernel result, never prose parsed back into meaning.
type Envelope struct {
	SchemaVersion      uint32                             `json:"schema_version"`
	Command            string                             `json:"command"`
	Outcome            Outcome                            `json:"outcome"`
	DatabaseID         journalv2.DatabaseID               `json:"database_id,omitempty"`
	RuntimeEpochID     journalv2.RuntimeEpochID           `json:"runtime_epoch_id,omitempty"`
	ProjectionRevision uint64                             `json:"projection_revision,omitempty"`
	RequestSHA256      string                             `json:"request_sha256,omitempty"`
	Host               *productionruntime.HostStatus      `json:"host,omitempty"`
	Progress           *productionruntime.MissionProgress `json:"progress,omitempty"`
	Item               *productionruntime.ProgressItem    `json:"item,omitempty"`
	Result             *TypedResult                       `json:"result,omitempty"`
	Drive              *DriveResult                       `json:"drive,omitempty"`
	Diagnostic         *Diagnostic                        `json:"diagnostic,omitempty"`
}

type OperationKind string

const (
	OperationMission            OperationKind = "mission"
	OperationReadiness          OperationKind = "readiness"
	OperationExecution          OperationKind = "execution"
	OperationWorkspaceRebind    OperationKind = "workspace_rebind"
	OperationHandoff            OperationKind = "handoff"
	OperationReview             OperationKind = "review"
	OperationAcceptance         OperationKind = "acceptance"
	OperationIntegration        OperationKind = "integration"
	OperationIntegrationCleanup OperationKind = "integration_cleanup"
	OperationEffect             OperationKind = "effect"
)

type ResultKind string

const (
	ResultMission            ResultKind = "mission"
	ResultReadiness          ResultKind = "readiness"
	ResultExecution          ResultKind = "execution"
	ResultWorkspace          ResultKind = "workspace"
	ResultHandoff            ResultKind = "handoff"
	ResultReview             ResultKind = "review"
	ResultAcceptance         ResultKind = "acceptance"
	ResultIntegration        ResultKind = "integration"
	ResultIntegrationCleanup ResultKind = "integration_cleanup"
	ResultEffect             ResultKind = "effect"
)

// TypedResult is a closed discriminated result. Payload remains canonical
// capability-owned JSON, but automation never has to infer its schema from
// shape or from the command's prose name.
type TypedResult struct {
	Kind    ResultKind      `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// OperationRequest is a closed tagged union. Payload is decoded by the
// matching capability adapter; unsupported variants fail closed.
type OperationRequest struct {
	SchemaVersion uint32          `json:"schema_version"`
	Action        string          `json:"action"`
	Scope         string          `json:"scope"`
	Kind          OperationKind   `json:"kind"`
	Payload       json.RawMessage `json:"payload"`
}

type DrivePlan struct {
	SchemaVersion uint32             `json:"schema_version"`
	Operations    []OperationRequest `json:"operations"`
}

type DriveStep struct {
	Action        string  `json:"action"`
	Scope         string  `json:"scope"`
	OperationID   string  `json:"operation_id"`
	RequestSHA256 string  `json:"request_sha256"`
	Outcome       Outcome `json:"outcome"`
}

type DriveResult struct {
	SchemaVersion uint32      `json:"schema_version"`
	Terminal      bool        `json:"terminal"`
	Steps         []DriveStep `json:"steps"`
	StopReason    string      `json:"stop_reason,omitempty"`
}
