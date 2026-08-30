// Package checkpoint defines the pure, versioned checkpoint and recovery
// model. Storage-specific records are translated into these values by the
// application composition layer; this package has no provider capability.
package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

const (
	SchemaVersionV1          = uint32(1)
	CodecCanonicalJSONV1     = "canonical-json-v1"
	MaxCheckpointBytes       = 4 << 20
	MaxCheckpointAggregates  = 16_000
	CheckpointCommandCadence = uint64(128)
	CheckpointEventCadence   = uint64(512)
	CheckpointByteCadence    = uint64(4 << 20)
	MaxRestoreEvents         = 16_000
	MaxRestoreBytes          = 48 << 20
	MaxScanPageBytes         = 8 << 20
	MaxRestorePages          = 32
	MaxStaleRestarts         = 2
	MaxScanPageRecords       = 500
)

type CheckpointID string

type WatermarkV1 struct {
	ThroughRecordRevision uint64
	ThroughEventID        controljournal.EventID
	EventCount            uint64
	EventPrefixDigest     [32]byte
}

type AggregateSnapshotV1 struct {
	Kind           controljournal.AggregateKind
	ID             controljournal.AggregateID
	LogicalVersion uint64
	RuntimeEpochID controljournal.RuntimeEpochID
	LastEventID    controljournal.EventID
	State          []byte
	StateDigest    [32]byte
}

type AuthoritySummaryV1 struct {
	RunID           controljournal.RunID
	RuntimeEpochID  controljournal.RuntimeEpochID
	LeaseID         controljournal.LeaseID
	LeaseGeneration uint64
	FenceToken      uint64
	DurableRevision uint64
}

type CheckpointRecordV1 struct {
	SchemaVersion             uint32
	CheckpointID              CheckpointID
	ReducerSetDigest          [32]byte
	EventSchemaSetDigest      [32]byte
	ProjectID                 controljournal.ProjectID
	MissionID                 controljournal.MissionID
	Watermark                 WatermarkV1
	CanonicalProjectionDigest [32]byte
	UncompressedSize          uint64
	Codec                     string
	Aggregates                []AggregateSnapshotV1
	Authority                 AuthoritySummaryV1
}

type CheckpointHeadV1 struct {
	SchemaVersion          uint32
	CheckpointID           CheckpointID
	CheckpointRecordDigest [32]byte
	ThroughRecordRevision  uint64
	ThroughEventID         controljournal.EventID
}

type SourceIdentityV1 struct {
	ProjectID            controljournal.ProjectID
	MissionID            controljournal.MissionID
	RunID                controljournal.RunID
	ReducerSetDigest     [32]byte
	EventSchemaSetDigest [32]byte
}

// RestoreBindingV1 identifies the admitted product store and owner lifetime
// whose read-only contents are being restored. The checkpoint package binds
// these values into its sealed result; the sidecar subsequently compares them
// with its independently admitted lifecycle provenance.
type RestoreBindingV1 struct {
	DatabaseID     string
	HostInstanceID string
}

// RestoreAdmissionFactsV1 is the copy-only evidence carried by a successful
// restore. It contains no storage or mutation capability.
type RestoreAdmissionFactsV1 struct {
	Source                    SourceIdentityV1
	Binding                   RestoreBindingV1
	ObservedStoreRevision     uint64
	TerminalWatermark         WatermarkV1
	Pristine                  bool
	CanonicalProjectionDigest [32]byte
	SelectedCheckpointID      CheckpointID
	SelectedCheckpointDigest  [32]byte
}

type restoreAdmissionSealV1 struct {
	version uint8
	digest  [32]byte
}

// RestoreAdmissionV1 is constructor-inaccessible evidence that RestoreV1
// completed successfully over the exact frozen read seam.
type RestoreAdmissionV1 struct {
	facts RestoreAdmissionFactsV1
	seal  restoreAdmissionSealV1
}

type InvalidRestoreAdmissionErrorV1 struct{}

func (InvalidRestoreAdmissionErrorV1) Error() string { return "invalid restore admission" }

type EpochBindingV1 struct {
	RunID                controljournal.RunID
	RuntimeEpochID       controljournal.RuntimeEpochID
	PredecessorEpochID   controljournal.RuntimeEpochID
	DurableRevision      uint64
	FirstAdmission       bool
	CommandAdmissionOpen bool
}

type NotReadyKindV1 string

const (
	NotReadyInvalidCheckpoint NotReadyKindV1 = "invalid_checkpoint"
	NotReadyJournalCorrupt    NotReadyKindV1 = "journal_corrupt"
	NotReadyReplayBound       NotReadyKindV1 = "replay_bound_exceeded"
	NotReadyStoreUnstable     NotReadyKindV1 = "store_unstable"
	NotReadyAuthorityClosed   NotReadyKindV1 = "authority_closed"
)

type NotReadyErrorV1 struct {
	Kind     NotReadyKindV1
	Field    string
	Recovery string
}

func (e NotReadyErrorV1) Error() string {
	if e.Field == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Field
}

type checkpointWireV1 struct {
	CanonicalProjectionDigest string                 `json:"canonical_projection_digest"`
	CheckpointID              string                 `json:"checkpoint_id"`
	Codec                     string                 `json:"codec"`
	EventCount                uint64                 `json:"event_count"`
	EventPrefixDigest         string                 `json:"event_prefix_digest"`
	EventSchemaSetDigest      string                 `json:"event_schema_set_digest"`
	MissionID                 string                 `json:"mission_id"`
	ProjectID                 string                 `json:"project_id"`
	Projection                checkpointProjectionV1 `json:"projection"`
	ReducerSetDigest          string                 `json:"reducer_set_digest"`
	SchemaVersion             uint32                 `json:"schema_version"`
	ThroughEventID            string                 `json:"through_event_id"`
	ThroughRecordRevision     uint64                 `json:"through_record_revision"`
	UncompressedSize          uint64                 `json:"uncompressed_size"`
}

type checkpointProjectionV1 struct {
	Aggregates []aggregateWireV1 `json:"aggregates"`
	Authority  authorityWireV1   `json:"authority_summary"`
}

type aggregateWireV1 struct {
	AggregateID    string          `json:"aggregate_id"`
	AggregateKind  string          `json:"aggregate_kind"`
	LastEventID    string          `json:"last_event_id"`
	LogicalVersion uint64          `json:"logical_version"`
	RuntimeEpochID string          `json:"runtime_epoch_id"`
	State          json.RawMessage `json:"state"`
	StateDigest    string          `json:"state_digest"`
}

type authorityWireV1 struct {
	DurableRevision uint64 `json:"durable_revision"`
	FenceToken      uint64 `json:"fence_token"`
	LeaseGeneration uint64 `json:"lease_generation"`
	LeaseID         string `json:"lease_id"`
	RunID           string `json:"run_id"`
	RuntimeEpochID  string `json:"runtime_epoch_id"`
}

func canonicalCheckpoint(record CheckpointRecordV1) ([]byte, error) {
	aggregates := append([]AggregateSnapshotV1(nil), record.Aggregates...)
	sort.Slice(aggregates, func(i, j int) bool {
		if aggregates[i].Kind != aggregates[j].Kind {
			return aggregates[i].Kind < aggregates[j].Kind
		}
		return aggregates[i].ID < aggregates[j].ID
	})
	wireAggregates := make([]aggregateWireV1, len(aggregates))
	for index, aggregate := range aggregates {
		wireAggregates[index] = aggregateWireV1{
			AggregateID: string(aggregate.ID), AggregateKind: string(aggregate.Kind),
			LastEventID: string(aggregate.LastEventID), LogicalVersion: aggregate.LogicalVersion,
			RuntimeEpochID: string(aggregate.RuntimeEpochID), State: aggregate.State,
			StateDigest: hex.EncodeToString(aggregate.StateDigest[:]),
		}
	}
	wire := checkpointWireV1{
		SchemaVersion: record.SchemaVersion, CheckpointID: string(record.CheckpointID),
		ReducerSetDigest:     hex.EncodeToString(record.ReducerSetDigest[:]),
		EventSchemaSetDigest: hex.EncodeToString(record.EventSchemaSetDigest[:]),
		ProjectID:            string(record.ProjectID), MissionID: string(record.MissionID),
		ThroughRecordRevision: record.Watermark.ThroughRecordRevision,
		ThroughEventID:        string(record.Watermark.ThroughEventID), EventCount: record.Watermark.EventCount,
		EventPrefixDigest:         hex.EncodeToString(record.Watermark.EventPrefixDigest[:]),
		CanonicalProjectionDigest: hex.EncodeToString(record.CanonicalProjectionDigest[:]),
		UncompressedSize:          record.UncompressedSize, Codec: record.Codec,
		Projection: checkpointProjectionV1{Aggregates: wireAggregates, Authority: authorityWireV1{
			RunID: string(record.Authority.RunID), RuntimeEpochID: string(record.Authority.RuntimeEpochID),
			LeaseID: string(record.Authority.LeaseID), LeaseGeneration: record.Authority.LeaseGeneration,
			FenceToken: record.Authority.FenceToken, DurableRevision: record.Authority.DurableRevision,
		}},
	}
	return json.Marshal(wire)
}

func projectionFromAggregates(aggregates []AggregateSnapshotV1) (reducer.ProjectionV1, error) {
	if len(aggregates) > MaxCheckpointAggregates {
		return reducer.ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "checkpoint_aggregate_count"}
	}
	states := make(map[reducer.AggregateKeyV1]reducer.StateV1, len(aggregates))
	heads := make(map[reducer.AggregateKeyV1]uint64, len(aggregates))
	for _, aggregate := range aggregates {
		if err := controljournal.ValidateAggregateCoordinateV1(aggregate.Kind, aggregate.ID); err != nil {
			return reducer.ProjectionV1{}, err
		}
		if aggregate.LogicalVersion == 0 || aggregate.StateDigest == [32]byte{} {
			return reducer.ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "checkpoint_aggregate"}
		}
		if _, err := controljournal.NewRuntimeEpochID(string(aggregate.RuntimeEpochID)); err != nil {
			return reducer.ProjectionV1{}, err
		}
		if _, err := controljournal.NewEventID(string(aggregate.LastEventID)); err != nil {
			return reducer.ProjectionV1{}, err
		}
		state, err := reducer.NewStateV1(aggregate.State)
		if err != nil || !bytes.Equal(state.CanonicalBytes(), aggregate.State) || state.Digest() != aggregate.StateDigest {
			return reducer.ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_state_digest"}
		}
		key := reducer.AggregateKeyV1{Kind: aggregate.Kind, ID: aggregate.ID}
		if _, duplicate := states[key]; duplicate {
			return reducer.ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalJournalCorrupt, Field: "checkpoint_aggregate_duplicate"}
		}
		states[key], heads[key] = state, aggregate.LogicalVersion
	}
	return reducer.NewProjectionV1(states, heads)
}

func parseDigest(field, value string) ([32]byte, error) {
	if len(value) != 64 {
		return [32]byte{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: field}
	}
	for _, char := range []byte(value) {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return [32]byte{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: field}
		}
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return [32]byte{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: field}
	}
	var digest [32]byte
	copy(digest[:], decoded)
	if digest == [32]byte{} {
		return [32]byte{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: field}
	}
	return digest, nil
}

func validateAuthority(summary AuthoritySummaryV1) error {
	if _, err := controljournal.NewRunID(string(summary.RunID)); err != nil {
		return err
	}
	if _, err := controljournal.NewRuntimeEpochID(string(summary.RuntimeEpochID)); err != nil {
		return err
	}
	if _, err := controljournal.NewLeaseID(string(summary.LeaseID)); err != nil {
		return err
	}
	if summary.LeaseGeneration == 0 || summary.FenceToken == 0 || summary.DurableRevision == 0 {
		return controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "authority_summary"}
	}
	return nil
}

func digestBytes(value []byte) [32]byte { return sha256.Sum256(value) }
