// Package checkpoint defines provider-neutral, bounded checkpoint values for
// the v2 authority journal. Persistence addresses and provider revisions are
// supplied by the journal foundation; this package never exposes store keys.
package checkpoint

import (
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const (
	SchemaVersionV2         = uint32(2)
	RetainedCandidateSlots  = 8
	MaxCheckpointAggregates = 10_000
)

type AggregateEntryV2 struct {
	coordinate  v2.AggregateCoordinate
	version     v2.LogicalVersion
	lastEventID v2.EventID
	state       []byte
	stateDigest v2.Digest
	tombstone   bool
}

func (e AggregateEntryV2) Coordinate() v2.AggregateCoordinate { return e.coordinate }
func (e AggregateEntryV2) LogicalVersion() v2.LogicalVersion  { return e.version }
func (e AggregateEntryV2) LastEventID() v2.EventID            { return e.lastEventID }
func (e AggregateEntryV2) CanonicalState() []byte             { return append([]byte(nil), e.state...) }
func (e AggregateEntryV2) StateDigest() v2.Digest             { return e.stateDigest }
func (e AggregateEntryV2) Tombstone() bool                    { return e.tombstone }

type CheckpointV2 struct {
	checkpointID     v2.CheckpointID
	throughRevision  uint64
	eventRegistry    v2.Digest
	reducerRegistry  v2.Digest
	codecVersion     uint32
	rollingPrefix    v2.Digest
	aggregates       []AggregateEntryV2
	operationSummary v2.OperationSummaryV2
	projectionDigest v2.Digest
	coveredThrough   v2.PublicationID
	checkpointDigest v2.Digest
	canonical        []byte
	seal             v2.Digest
}

func (c CheckpointV2) CheckpointID() v2.CheckpointID                 { return c.checkpointID }
func (c CheckpointV2) ThroughRecordRevision() uint64                 { return c.throughRevision }
func (c CheckpointV2) EventRegistryDigest() v2.Digest                { return c.eventRegistry }
func (c CheckpointV2) ReducerRegistryDigest() v2.Digest              { return c.reducerRegistry }
func (c CheckpointV2) CanonicalCodecVersion() uint32                 { return c.codecVersion }
func (c CheckpointV2) RollingPrefixDigest() v2.Digest                { return c.rollingPrefix }
func (c CheckpointV2) CanonicalProjectionDigest() v2.Digest          { return c.projectionDigest }
func (c CheckpointV2) CoveredThroughPublicationID() v2.PublicationID { return c.coveredThrough }
func (c CheckpointV2) CheckpointDigest() v2.Digest                   { return c.checkpointDigest }
func (c CheckpointV2) CanonicalBytes() []byte                        { return append([]byte(nil), c.canonical...) }
func (c CheckpointV2) OperationSummary() v2.OperationSummaryV2       { return c.operationSummary }
func (c CheckpointV2) Aggregates() []AggregateEntryV2 {
	result := make([]AggregateEntryV2, len(c.aggregates))
	for i, entry := range c.aggregates {
		entry.state = append([]byte(nil), entry.state...)
		result[i] = entry
	}
	return result
}

type SlotV2 struct {
	slotID           v2.CheckpointSlotID
	checkpointID     v2.CheckpointID
	checkpointDigest v2.Digest
	throughRevision  uint64
}

func (s SlotV2) SlotID() v2.CheckpointSlotID   { return s.slotID }
func (s SlotV2) CheckpointID() v2.CheckpointID { return s.checkpointID }
func (s SlotV2) CheckpointDigest() v2.Digest   { return s.checkpointDigest }
func (s SlotV2) ThroughRecordRevision() uint64 { return s.throughRevision }

// CandidateV2 is materialized provider evidence. The record and slot are
// required to share one later provider revision; the covered witness is
// required at the checkpoint watermark itself.
type CandidateV2 struct {
	SlotRecord       v2.RecoveryRecordV2
	CheckpointRecord v2.RecoveryRecordV2
	CoveredWitness   v2.RecoveryRecordV2
}

type CompatibilityV2 struct {
	EventRegistryDigest   v2.Digest
	ReducerRegistryDigest v2.Digest
	CanonicalCodecVersion uint32
}

type SelectionV2 struct {
	checkpoint     CheckpointV2
	commitRevision uint64
	origin         bool
}

func OriginV2() SelectionV2                    { return SelectionV2{origin: true} }
func (s SelectionV2) IsOrigin() bool           { return s.origin }
func (s SelectionV2) Checkpoint() CheckpointV2 { return s.checkpoint }
func (s SelectionV2) CommitRevision() uint64   { return s.commitRevision }

type ErrorKind string

const (
	ErrorInvalidCheckpoint ErrorKind = "INVALID_CHECKPOINT"
	ErrorIncompatible      ErrorKind = "INCOMPATIBLE_CHECKPOINT"
	ErrorContradictory     ErrorKind = "CONTRADICTORY_CHECKPOINTS"
	ErrorBudget            ErrorKind = "CHECKPOINT_BUDGET_EXCEEDED"
)

type Error struct {
	Kind  ErrorKind
	Field string
}

func (e *Error) Error() string {
	if e.Field == "" {
		return string(e.Kind)
	}
	return string(e.Kind) + ": " + e.Field
}
