// Package playback performs deterministic in-memory reduction over already
// materialized journal facts. Its API intentionally has no interface or
// function value through which storage, network, filesystem, clock, random,
// agent, effect, or durable-write I/O can be represented.
package playback

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"sort"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const (
	// A restore may contain the independent maxima of 10,000 product
	// aggregates and 10,000 compact operation aggregates.
	MaxPlaybackAggregates = 20_000
	MaxPlaybackEvents     = 100_000
	MaxPlaybackBytes      = 128 * 1024 * 1024
)

type StateV2 struct {
	coordinate        journal.AggregateCoordinate
	version           journal.LogicalVersion
	lastEventID       journal.EventID
	canonical         []byte
	digest            journal.Digest
	tombstone         bool
	lastPublicationID journal.PublicationID
}

func NewStateV2(coordinate journal.AggregateCoordinate, version journal.LogicalVersion, lastEventID journal.EventID, canonical []byte, digest journal.Digest, tombstone bool) (StateV2, error) {
	if _, err := journal.CanonicalAggregateCoordinate(coordinate); err != nil {
		return StateV2{}, err
	}
	state, err := journal.CanonicalObject(canonical)
	if err != nil {
		return StateV2{}, err
	}
	actual, err := journal.ReducerStateDigestV2(state)
	if err != nil || version == 0 || lastEventID == "" || actual != digest {
		return StateV2{}, &Error{"INVALID_BASE_STATE"}
	}
	return StateV2{coordinate: coordinate, version: version, lastEventID: lastEventID, canonical: state, digest: digest, tombstone: tombstone}, nil
}

func (s StateV2) Coordinate() journal.AggregateCoordinate  { return s.coordinate }
func (s StateV2) LogicalVersion() journal.LogicalVersion   { return s.version }
func (s StateV2) LastEventID() journal.EventID             { return s.lastEventID }
func (s StateV2) CanonicalState() []byte                   { return append([]byte(nil), s.canonical...) }
func (s StateV2) StateDigest() journal.Digest              { return s.digest }
func (s StateV2) Tombstone() bool                          { return s.tombstone }
func (s StateV2) LastPublicationID() journal.PublicationID { return s.lastPublicationID }

type MaterializedEventV2 struct {
	revision      uint64
	envelope      journal.EventEnvelopeV2
	tombstone     bool
	canonicalSize int
}

func NewMaterializedEventV2(revision uint64, envelope journal.EventEnvelopeV2, terminalTombstone bool) (MaterializedEventV2, error) {
	if revision == 0 {
		return MaterializedEventV2{}, &Error{"INVALID_EVENT_REVISION"}
	}
	canonicalSize := envelope.CanonicalSizeV2()
	if canonicalSize == 0 {
		encoded, err := journal.EncodeEventEnvelopeV2(envelope)
		if err != nil {
			return MaterializedEventV2{}, err
		}
		canonicalSize = len(encoded)
	}
	return MaterializedEventV2{revision, envelope, terminalTombstone, canonicalSize}, nil
}
func (e MaterializedEventV2) RecordRevision() uint64            { return e.revision }
func (e MaterializedEventV2) Envelope() journal.EventEnvelopeV2 { return e.envelope }
func (e MaterializedEventV2) TerminalTombstone() bool           { return e.tombstone }

type InputV2 struct {
	Base                          []StateV2
	Events                        []MaterializedEventV2
	EventRegistry                 journal.EventRegistryV2
	Reducers                      journal.ReducerSetV2
	ExpectedEventRegistryDigest   journal.Digest
	ExpectedReducerRegistryDigest journal.Digest
	MaximumAggregates             int
	MaximumEvents                 int
	MaximumBytes                  int
}

type ResultV2 struct {
	states                []StateV2
	throughRevision       uint64
	eventCount            uint64
	projectionDigest      journal.Digest
	eventRegistryDigest   journal.Digest
	reducerRegistryDigest journal.Digest
	seal                  journal.Digest
}

func (r ResultV2) States() []StateV2                         { return cloneStates(r.states) }
func (r ResultV2) ThroughRecordRevision() uint64             { return r.throughRevision }
func (r ResultV2) EventCount() uint64                        { return r.eventCount }
func (r ResultV2) CanonicalProjectionDigest() journal.Digest { return r.projectionDigest }
func (r ResultV2) EventRegistryDigest() journal.Digest       { return r.eventRegistryDigest }
func (r ResultV2) ReducerRegistryDigest() journal.Digest     { return r.reducerRegistryDigest }

type Error struct{ Field string }

func (e *Error) Error() string { return "PLAYBACK_REFUSED: " + e.Field }

func RunV2(input InputV2) (ResultV2, error) {
	maxAggregates, maxEvents, maxBytes := input.MaximumAggregates, input.MaximumEvents, input.MaximumBytes
	if maxAggregates == 0 {
		maxAggregates = MaxPlaybackAggregates
	}
	if maxEvents == 0 {
		maxEvents = MaxPlaybackEvents
	}
	if maxBytes == 0 {
		maxBytes = MaxPlaybackBytes
	}
	if maxAggregates < 1 || maxAggregates > MaxPlaybackAggregates || maxEvents < 1 || maxEvents > MaxPlaybackEvents || maxBytes < 1 || maxBytes > MaxPlaybackBytes || len(input.Base) > maxAggregates || len(input.Events) > maxEvents {
		return ResultV2{}, &Error{"budget"}
	}
	eventDescriptor, err := input.EventRegistry.Descriptor()
	if err != nil {
		return ResultV2{}, err
	}
	reducerDescriptor, err := input.Reducers.Descriptor()
	if err != nil {
		return ResultV2{}, err
	}
	if eventDescriptor.Digest() != input.ExpectedEventRegistryDigest || reducerDescriptor.Digest() != input.ExpectedReducerRegistryDigest {
		return ResultV2{}, &Error{"registry_digest"}
	}

	states := make(map[string]StateV2, len(input.Base))
	bytesUsed := 0
	for _, base := range input.Base {
		keyBytes, err := journal.CanonicalAggregateCoordinate(base.coordinate)
		if err != nil {
			return ResultV2{}, err
		}
		key := string(keyBytes)
		if _, exists := states[key]; exists {
			return ResultV2{}, &Error{"duplicate_base"}
		}
		actual, err := journal.ReducerStateDigestV2(base.canonical)
		if err != nil || actual != base.digest || base.version == 0 {
			return ResultV2{}, &Error{"base_digest"}
		}
		if len(base.canonical) > maxBytes-bytesUsed {
			return ResultV2{}, &Error{"bytes"}
		}
		bytesUsed += len(base.canonical)
		base.canonical = append([]byte(nil), base.canonical...)
		states[key] = base
	}
	events := append([]MaterializedEventV2(nil), input.Events...)
	sort.Slice(events, func(i, k int) bool {
		if events[i].revision != events[k].revision {
			return events[i].revision < events[k].revision
		}
		if events[i].envelope.EventIndex() != events[k].envelope.EventIndex() {
			return events[i].envelope.EventIndex() < events[k].envelope.EventIndex()
		}
		return events[i].envelope.EventID() < events[k].envelope.EventID()
	})
	var through uint64
	for index, material := range events {
		if material.revision == 0 || index > 0 && compareMaterialized(events[index-1], material) >= 0 {
			return ResultV2{}, &Error{"event_order"}
		}
		if material.canonicalSize <= 0 || material.canonicalSize > maxBytes-bytesUsed {
			return ResultV2{}, &Error{"bytes"}
		}
		bytesUsed += material.canonicalSize
		coordinateBytes, err := journal.CanonicalAggregateCoordinate(material.envelope.Coordinate())
		if err != nil {
			return ResultV2{}, err
		}
		key := string(coordinateBytes)
		prior := []byte(`{}`)
		priorVersion := journal.LogicalVersion(0)
		if current, exists := states[key]; exists {
			prior, priorVersion = current.canonical, current.version
		}
		if uint64(priorVersion) == ^uint64(0) || material.envelope.LogicalVersion() != priorVersion+1 {
			return ResultV2{}, &Error{"logical_version"}
		}
		output, outputDigest, err := input.Reducers.ApplyPlaybackV2(prior, material.envelope)
		if err != nil {
			return ResultV2{}, err
		}
		states[key] = StateV2{coordinate: material.envelope.Coordinate(), version: material.envelope.LogicalVersion(), lastEventID: material.envelope.EventID(), canonical: append([]byte(nil), output...), digest: outputDigest, tombstone: material.tombstone, lastPublicationID: material.envelope.PublicationID()}
		if material.revision > through {
			through = material.revision
		}
		if len(states) > maxAggregates {
			return ResultV2{}, &Error{"aggregates"}
		}
	}
	ordered := make([]StateV2, 0, len(states))
	for _, state := range states {
		ordered = append(ordered, state)
	}
	sort.Slice(ordered, func(i, k int) bool {
		left, _ := journal.CanonicalAggregateCoordinate(ordered[i].coordinate)
		right, _ := journal.CanonicalAggregateCoordinate(ordered[k].coordinate)
		return bytes.Compare(left, right) < 0
	})
	projectionDigest, err := digestProjection(ordered)
	if err != nil {
		return ResultV2{}, err
	}
	result := ResultV2{cloneStates(ordered), through, uint64(len(events)), projectionDigest, eventDescriptor.Digest(), reducerDescriptor.Digest(), journal.Digest{}}
	result.seal = resultSeal(result)
	return result, nil
}

func (r ResultV2) Validate() error {
	if r.seal.IsZero() || resultSeal(r) != r.seal {
		return &Error{"result_seal"}
	}
	digest, err := digestProjection(r.states)
	if err != nil || digest != r.projectionDigest {
		return &Error{"result_projection"}
	}
	return nil
}

func compareMaterialized(a, b MaterializedEventV2) int {
	if a.revision < b.revision {
		return -1
	}
	if a.revision > b.revision {
		return 1
	}
	if a.envelope.EventIndex() < b.envelope.EventIndex() {
		return -1
	}
	if a.envelope.EventIndex() > b.envelope.EventIndex() {
		return 1
	}
	return bytes.Compare([]byte(a.envelope.EventID()), []byte(b.envelope.EventID()))
}

func digestProjection(states []StateV2) (journal.Digest, error) {
	type wire struct {
		Coordinate  journal.AggregateCoordinate `json:"coordinate"`
		Version     uint64                      `json:"version"`
		LastEventID string                      `json:"last_event_id"`
		Tombstone   bool                        `json:"tombstone"`
		State       []byte                      `json:"state"`
		StateSHA256 string                      `json:"state_sha256"`
	}
	values := make([]wire, 0, len(states))
	for _, state := range states {
		if state.coordinate.Kind == journal.AggregateKind("operation") {
			continue
		}
		actual, err := journal.ReducerStateDigestV2(state.canonical)
		if err != nil || actual != state.digest {
			return journal.Digest{}, &Error{"state_digest"}
		}
		values = append(values, wire{state.coordinate, uint64(state.version), string(state.lastEventID), state.tombstone, append([]byte(nil), state.canonical...), state.digest.String()})
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return journal.Digest{}, err
	}
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty.projection.v2"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(encoded)
	var result journal.Digest
	copy(result[:], h.Sum(nil))
	return result, nil
}

func resultSeal(r ResultV2) journal.Digest {
	h := sha256.New()
	_, _ = h.Write([]byte("spec-kitty.playback-result.v2"))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(r.projectionDigest[:])
	_, _ = h.Write(r.eventRegistryDigest[:])
	_, _ = h.Write(r.reducerRegistryDigest[:])
	var scratch [16]byte
	for i := range scratch[:8] {
		scratch[7-i] = byte(r.throughRevision >> (8 * i))
		scratch[15-i] = byte(r.eventCount >> (8 * i))
	}
	_, _ = h.Write(scratch[:])
	for _, state := range r.states {
		_, _ = h.Write(state.digest[:])
	}
	var result journal.Digest
	copy(result[:], h.Sum(nil))
	return result
}

func cloneStates(states []StateV2) []StateV2 {
	result := make([]StateV2, len(states))
	for i, state := range states {
		state.canonical = append([]byte(nil), state.canonical...)
		result[i] = state
	}
	return result
}
