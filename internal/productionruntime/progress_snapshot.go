package productionruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// This query used the reducer's domain historically. Retain that wire digest
// contract, not reducer authority: these bytes are never admitted durable state.
const legacyProgressSnapshotDomain = "spec-kitty.reducer-state.v2"

func progressSnapshotBudgetError() error {
	return &ProgressError{Kind: ProgressIncomplete, Field: "snapshot_budget"}
}

// The byte charge follows the same destinations that classify each item. It
// includes base64 expansion, escaped strings, and each array occurrence before
// making its defensive copy. Metadata and derived arrays are checked at encoding.
type progressSnapshotBudget struct{ used int }

func (b *progressSnapshotBudget) append(item ProgressItem, targets ...*[]ProgressItem) error {
	wire, err := json.Marshal(item)
	if err != nil {
		return &ProgressError{Kind: ProgressIncomplete, Field: "encoding"}
	}
	remaining := MaxProgressSnapshotBytes - sha256.Size*2 - b.used
	charge := len(wire) + 1 // array separator; conservative by one per first item
	if len(targets) == 0 || charge > remaining/len(targets) {
		return progressSnapshotBudgetError()
	}
	b.used += charge * len(targets)
	for _, target := range targets {
		*target = append(*target, cloneProgressItem(item))
	}
	return nil
}

// These private views declare every owned JSON field in lexical tag order.
// A single ordinary Marshal therefore has the historical sorted-map preimage,
// including default escaping, exact integer types, and nil/empty semantics.
// Schema and conversion parity are gated against the public DTOs in tests.
type canonicalMissionProgress struct {
	Acceptance            []canonicalProgressItem       `json:"acceptance"`
	Actions               []canonicalActionAvailability `json:"actions"`
	Blockers              []canonicalProgressItem       `json:"blockers"`
	Complete              bool                          `json:"complete"`
	DatabaseID            journalv2.DatabaseID          `json:"database_id"`
	Decisions             []canonicalProgressItem       `json:"decisions"`
	Effects               []canonicalProgressItem       `json:"effects"`
	Generation            uint64                        `json:"generation,omitempty"`
	Integration           []canonicalProgressItem       `json:"integration"`
	Items                 []canonicalProgressItem       `json:"items"`
	Lifecycle             string                        `json:"lifecycle,omitempty"`
	MissionID             journalv2.MissionID           `json:"mission_id"`
	Operations            []canonicalProgressItem       `json:"operations"`
	PendingReconciliation []canonicalProgressItem       `json:"pending_reconciliation"`
	ProjectID             journalv2.ProjectID           `json:"project_id,omitempty"`
	ProjectionRevision    uint64                        `json:"projection_revision"`
	ProjectionSHA256      string                        `json:"projection_sha256"`
	Reviews               []canonicalProgressItem       `json:"reviews"`
	Running               []canonicalRunningAuthority   `json:"running"`
	RuntimeEpochID        journalv2.RuntimeEpochID      `json:"runtime_epoch_id"`
	SchemaVersion         uint32                        `json:"schema_version"`
	SnapshotSHA256        string                        `json:"snapshot_sha256"`
	TotalWork             int                           `json:"total_work"`
	WorkCounts            []canonicalStateCount         `json:"work_counts"`
	WorkRevisionID        journalv2.WorkRevisionID      `json:"work_revision_id,omitempty"`
}

type canonicalProgressItem struct {
	AggregateID       string `json:"aggregate_id"`
	AuthorityState    string `json:"authority_state,omitempty"`
	CanonicalState    []byte `json:"canonical_state"`
	Family            string `json:"family"`
	LastPublicationID string `json:"last_publication_id"`
	LogicalVersion    uint64 `json:"logical_version"`
	ObservedState     string `json:"observed_state,omitempty"`
}

type canonicalActionAvailability struct {
	Action               string   `json:"action"`
	BlockingPredicates   []string `json:"blocking_predicates,omitempty"`
	ResponsibleProducers []string `json:"responsible_producers,omitempty"`
	Scope                string   `json:"scope,omitempty"`
	Status               string   `json:"status"`
}

type canonicalStateCount struct {
	Count int    `json:"count"`
	State string `json:"state"`
}

type canonicalRunningAuthority struct {
	ActorID   string `json:"actor_id"`
	LeaseID   string `json:"lease_id"`
	ProcessID string `json:"process_id,omitempty"`
	RunID     string `json:"run_id"`
	WorkID    string `json:"work_id,omitempty"`
}

// Only bounded element headers are allocated, not payload copies. The query
// already owns defensive copies; these views are consumed synchronously.
func canonicalProgressSlice[P, C any](values []P, convert func(P) C) []C {
	if values == nil {
		return nil
	}
	result := make([]C, len(values))
	for i, value := range values {
		result[i] = convert(value)
	}
	return result
}

func canonicalProgressItemView(value ProgressItem) canonicalProgressItem {
	return canonicalProgressItem{
		AggregateID: value.AggregateID, AuthorityState: value.AuthorityState,
		CanonicalState: value.CanonicalState, Family: value.Family,
		LastPublicationID: value.LastPublicationID, LogicalVersion: value.LogicalVersion,
		ObservedState: value.ObservedState,
	}
}

func canonicalActionAvailabilityView(value ActionAvailability) canonicalActionAvailability {
	return canonicalActionAvailability{
		Action: value.Action, BlockingPredicates: value.BlockingPredicates,
		ResponsibleProducers: value.ResponsibleProducers, Scope: value.Scope, Status: value.Status,
	}
}

func canonicalStateCountView(value StateCount) canonicalStateCount {
	return canonicalStateCount{Count: value.Count, State: value.State}
}

func canonicalRunningAuthorityView(value RunningAuthority) canonicalRunningAuthority {
	return canonicalRunningAuthority{
		ActorID: value.ActorID, LeaseID: value.LeaseID, ProcessID: value.ProcessID,
		RunID: value.RunID, WorkID: value.WorkID,
	}
}

func canonicalMissionProgressView(value MissionProgress) canonicalMissionProgress {
	return canonicalMissionProgress{
		Acceptance: canonicalProgressSlice(value.Acceptance, canonicalProgressItemView),
		Actions:    canonicalProgressSlice(value.Actions, canonicalActionAvailabilityView),
		Blockers:   canonicalProgressSlice(value.Blockers, canonicalProgressItemView),
		Complete:   value.Complete, DatabaseID: value.DatabaseID,
		Decisions:   canonicalProgressSlice(value.Decisions, canonicalProgressItemView),
		Effects:     canonicalProgressSlice(value.Effects, canonicalProgressItemView),
		Generation:  value.Generation,
		Integration: canonicalProgressSlice(value.Integration, canonicalProgressItemView),
		Items:       canonicalProgressSlice(value.Items, canonicalProgressItemView),
		Lifecycle:   value.Lifecycle, MissionID: value.MissionID,
		Operations:            canonicalProgressSlice(value.Operations, canonicalProgressItemView),
		PendingReconciliation: canonicalProgressSlice(value.PendingReconciliation, canonicalProgressItemView),
		ProjectID:             value.ProjectID, ProjectionRevision: value.ProjectionRevision,
		ProjectionSHA256: value.ProjectionSHA256,
		Reviews:          canonicalProgressSlice(value.Reviews, canonicalProgressItemView),
		Running:          canonicalProgressSlice(value.Running, canonicalRunningAuthorityView),
		RuntimeEpochID:   value.RuntimeEpochID, SchemaVersion: value.SchemaVersion,
		SnapshotSHA256: "", // never commit the hash itself or mutate the caller
		TotalWork:      value.TotalWork,
		WorkCounts:     canonicalProgressSlice(value.WorkCounts, canonicalStateCountView),
		WorkRevisionID: value.WorkRevisionID,
	}
}

// progressSnapshotDigest accepts only the owned DTO, not untrusted JSON.
// CanonicalState remains opaque base64 bytes; no business JSON is interpreted.
// No durable parser, payload budget, codec or commitment API is widened here.
func progressSnapshotDigest(value MissionProgress) (string, error) {
	canonical, err := json.Marshal(canonicalMissionProgressView(value))
	if err != nil {
		return "", &ProgressError{Kind: ProgressIncomplete, Field: "encoding"}
	}
	// Replacing the empty hash string adds exactly 64 bytes to the returned DTO.
	maximum := MaxProgressSnapshotBytes - sha256.Size*2
	if len(canonical) > maximum {
		return "", progressSnapshotBudgetError()
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(legacyProgressSnapshotDomain))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}
