// Package reducer applies validated journal capabilities to disposable,
// deterministic projections. It has no storage or effect boundary.
package reducer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
)

type AggregateKeyV1 struct {
	Kind controljournal.AggregateKind
	ID   controljournal.AggregateID
}

type StateV1 struct {
	canonical []byte
}

func NewStateV1(object []byte) (StateV1, error) {
	canonical, err := controljournal.CanonicalJSONObject(object)
	if err != nil {
		return StateV1{}, err
	}
	return StateV1{canonical: canonical}, nil
}

func (s StateV1) CanonicalBytes() []byte { return append([]byte(nil), s.canonical...) }
func (s StateV1) Digest() [32]byte       { return sha256.Sum256(s.canonical) }

// ReducerProgramV1 is a closed declarative reducer instruction. It is data,
// not executable caller code: adding behavior requires adding and reviewing a
// package-owned interpreter opcode.
type ReducerProgramV1 string

const ProgramReplaceWithCanonicalEventPayloadV1 ReducerProgramV1 = "replace_with_canonical_event_payload_v1"

type EntryV1 struct {
	AggregateKind      controljournal.AggregateKind
	EventType          controljournal.EventType
	EventSchemaVersion uint32
	ReducerVersion     controljournal.ReducerVersion
	Program            ReducerProgramV1
}

type RegistryV1 struct {
	programs map[string]ReducerProgramV1
	digest   [32]byte
}

type EventApplicationErrorV1 struct {
	EventOrdinal int
	Kind         controljournal.RefusalKind
}

func (e EventApplicationErrorV1) Error() string {
	return string(e.Kind) + ": event_ordinal"
}

func NewRegistryV1(entries []EntryV1) (RegistryV1, error) {
	registry := RegistryV1{programs: make(map[string]ReducerProgramV1, len(entries))}
	if len(entries) == 0 {
		return RegistryV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "reducer_registry"}
	}
	for _, entry := range entries {
		if err := controljournal.ValidateReducerCoordinateV1(entry.AggregateKind, entry.EventType, entry.EventSchemaVersion, entry.ReducerVersion); err != nil {
			return RegistryV1{}, err
		}
		if entry.Program != ProgramReplaceWithCanonicalEventPayloadV1 {
			return RegistryV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "reducer_entry"}
		}
		key := reducerKey(entry.AggregateKind, entry.EventType, entry.EventSchemaVersion, entry.ReducerVersion)
		if _, duplicate := registry.programs[key]; duplicate {
			return RegistryV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "reducer_duplicate"}
		}
		registry.programs[key] = entry.Program
	}
	registry.digest = registryDigest(registry.programs)
	return registry, nil
}

func registryDigest(programs map[string]ReducerProgramV1) [32]byte {
	keys := make([]string, 0, len(programs))
	for key := range programs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var encoded bytes.Buffer
	for _, key := range keys {
		writeFoldPart(&encoded, []byte(key))
		writeFoldPart(&encoded, []byte(programs[key]))
	}
	return sha256.Sum256(encoded.Bytes())
}

func (r RegistryV1) valid() bool {
	if len(r.programs) == 0 || r.digest == [32]byte{} || registryDigest(r.programs) != r.digest {
		return false
	}
	for _, program := range r.programs {
		if program != ProgramReplaceWithCanonicalEventPayloadV1 {
			return false
		}
	}
	return true
}

func (r RegistryV1) Digest() ([32]byte, error) {
	if !r.valid() {
		return [32]byte{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "reducer_registry"}
	}
	return r.digest, nil
}

func reducerKey(kind controljournal.AggregateKind, eventType controljournal.EventType, schema uint32, version controljournal.ReducerVersion) string {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], schema)
	return string(kind) + "\x00" + string(eventType) + "\x00" + string(encoded[:]) + "\x00" + string(version)
}

type ProjectionV1 struct {
	states map[AggregateKeyV1]StateV1
	heads  map[AggregateKeyV1]uint64
}

func NewProjectionV1(initial map[AggregateKeyV1]StateV1, heads map[AggregateKeyV1]uint64) (ProjectionV1, error) {
	projection := ProjectionV1{states: make(map[AggregateKeyV1]StateV1, len(initial)), heads: make(map[AggregateKeyV1]uint64, len(heads))}
	for key, state := range initial {
		if err := controljournal.ValidateAggregateCoordinateV1(key.Kind, key.ID); err != nil {
			return ProjectionV1{}, err
		}
		if !json.Valid(state.canonical) {
			return ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "projection_state"}
		}
		projection.states[key] = StateV1{canonical: state.CanonicalBytes()}
	}
	for key, head := range heads {
		if err := controljournal.ValidateAggregateCoordinateV1(key.Kind, key.ID); err != nil {
			return ProjectionV1{}, err
		}
		if head == 0 {
			return ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "projection_head"}
		}
		projection.heads[key] = head
	}
	if len(projection.states) != len(projection.heads) {
		return ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "projection_head_set"}
	}
	for key := range projection.states {
		if _, ok := projection.heads[key]; !ok {
			return ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "projection_head_set"}
		}
	}
	return projection, nil
}

func EmptyProjectionV1() ProjectionV1 {
	return ProjectionV1{states: make(map[AggregateKeyV1]StateV1), heads: make(map[AggregateKeyV1]uint64)}
}

func (p ProjectionV1) State(key AggregateKeyV1) (StateV1, bool) {
	state, ok := p.states[key]
	return StateV1{canonical: state.CanonicalBytes()}, ok
}

func (p ProjectionV1) Head(key AggregateKeyV1) (uint64, bool) {
	head, ok := p.heads[key]
	return head, ok
}

func (p ProjectionV1) GlobalDigest() [32]byte {
	keys := make([]AggregateKeyV1, 0, len(p.states))
	for key := range p.states {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].ID < keys[j].ID
	})
	var folded bytes.Buffer
	for _, key := range keys {
		writeFoldPart(&folded, []byte(key.Kind))
		writeFoldPart(&folded, []byte(key.ID))
		digest := p.states[key].Digest()
		folded.Write(digest[:])
		var logical [8]byte
		binary.BigEndian.PutUint64(logical[:], p.heads[key])
		folded.Write(logical[:])
	}
	return sha256.Sum256(folded.Bytes())
}

func writeFoldPart(buffer *bytes.Buffer, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	buffer.Write(length[:])
	buffer.Write(value)
}

// ApplyV1 verifies the capability before cloning or reducing any caller state.
// It publishes no partial projection on any failure.
func ApplyV1(registry RegistryV1, initial ProjectionV1, journal controljournal.ValidatedJournalV1) (ProjectionV1, error) {
	length, err := journal.Len()
	if err != nil {
		return ProjectionV1{}, err
	}
	if !registry.valid() {
		return ProjectionV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidValue, Field: "reducer_registry"}
	}
	working := ProjectionV1{states: make(map[AggregateKeyV1]StateV1, len(initial.states)), heads: make(map[AggregateKeyV1]uint64, len(initial.heads))}
	for key, state := range initial.states {
		working.states[key] = StateV1{canonical: state.CanonicalBytes()}
	}
	for key, head := range initial.heads {
		working.heads[key] = head
	}
	for index := 0; index < length; index++ {
		record, err := journal.At(index)
		if err != nil {
			return ProjectionV1{}, err
		}
		event := record.Envelope
		key := AggregateKeyV1{Kind: event.AggregateKind, ID: event.AggregateID}
		expectedVersion := working.heads[key] + 1
		if event.AggregateLogicalVersion != expectedVersion {
			return ProjectionV1{}, EventApplicationErrorV1{EventOrdinal: index, Kind: controljournal.RefusalInvalidTransition}
		}
		program := registry.programs[reducerKey(event.AggregateKind, event.EventType, event.EventSchemaVersion, event.ReducerVersion)]
		if program == "" {
			return ProjectionV1{}, EventApplicationErrorV1{EventOrdinal: index, Kind: controljournal.RefusalReducerMissing}
		}
		var next StateV1
		switch program {
		case ProgramReplaceWithCanonicalEventPayloadV1:
			next, err = NewStateV1(event.Payload())
		default:
			return ProjectionV1{}, EventApplicationErrorV1{EventOrdinal: index, Kind: controljournal.RefusalReducerMissing}
		}
		if err != nil {
			return ProjectionV1{}, EventApplicationErrorV1{EventOrdinal: index, Kind: controljournal.RefusalInvalidTransition}
		}
		canonical, err := controljournal.CanonicalJSONObject(next.canonical)
		if err != nil {
			return ProjectionV1{}, EventApplicationErrorV1{EventOrdinal: index, Kind: controljournal.RefusalInvalidTransition}
		}
		working.states[key] = StateV1{canonical: canonical}
		working.heads[key] = event.AggregateLogicalVersion
	}
	return working, nil
}
