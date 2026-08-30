// Package playback performs full-history deterministic reduction. Its public
// input contains only already materialized immutable journal and reducer
// capabilities; no adapter, filesystem, clock, random, network, authority, or
// writer capability is representable here.
package playback

import (
	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

const (
	maxPlaybackEvents = 16_000
	maxPlaybackBytes  = 48 << 20
)

type InputV1 struct {
	Journal  controljournal.ValidatedJournalV1
	Reducers reducer.RegistryV1
}

type ObservationV1 struct {
	AdapterCalls  uint64
	DurableWrites uint64
}

type ResultV1 struct {
	Projection                reducer.ProjectionV1
	ThroughRecordRevision     uint64
	ThroughEventID            controljournal.EventID
	EventCount                uint64
	CanonicalProjectionDigest [32]byte
	Observation               ObservationV1
}

func RunV1(input InputV1) (ResultV1, error) {
	length, err := input.Journal.Len()
	if err != nil {
		return ResultV1{}, err
	}
	encodedBytes := 0
	var count int
	var terminal controljournal.StoredEventV1
	for index := 0; index < length; index++ {
		record, atErr := input.Journal.At(index)
		if atErr != nil {
			return ResultV1{}, atErr
		}
		if count == maxPlaybackEvents {
			return ResultV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "playback_events"}
		}
		encoded, encodeErr := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		if encodeErr != nil {
			return ResultV1{}, encodeErr
		}
		if len(encoded) > maxPlaybackBytes-encodedBytes {
			return ResultV1{}, controljournal.ValidationError{Kind: controljournal.RefusalBudgetExceeded, Field: "playback_bytes"}
		}
		encodedBytes += len(encoded)
		terminal = record
		count++
	}
	projection, err := reducer.ApplyV1(input.Reducers, reducer.EmptyProjectionV1(), input.Journal)
	if err != nil {
		return ResultV1{}, err
	}
	result := ResultV1{Projection: projection, EventCount: uint64(count), CanonicalProjectionDigest: projection.GlobalDigest(), Observation: ObservationV1{AdapterCalls: 0, DurableWrites: 0}}
	if count > 0 {
		result.ThroughRecordRevision, result.ThroughEventID = terminal.RecordRevision, terminal.Envelope.EventID
	}
	return result, nil
}

type DigestMismatchErrorV1 struct {
	Restored [32]byte
	Playback [32]byte
}

func (DigestMismatchErrorV1) Error() string {
	return "restore/playback canonical projection digest mismatch"
}

func CompareDigestV1(restored [32]byte, result ResultV1) error {
	if restored != result.CanonicalProjectionDigest {
		return DigestMismatchErrorV1{Restored: restored, Playback: result.CanonicalProjectionDigest}
	}
	return nil
}
