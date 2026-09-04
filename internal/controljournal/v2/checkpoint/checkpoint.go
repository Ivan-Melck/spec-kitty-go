package checkpoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const checkpointDomainV2 = "spec-kitty.checkpoint.v2"

type aggregateWireV2 struct {
	Coordinate     journal.AggregateCoordinate `json:"coordinate"`
	LogicalVersion uint64                      `json:"logical_version"`
	LastEventID    string                      `json:"last_event_id"`
	State          json.RawMessage             `json:"state"`
	StateSHA256    string                      `json:"state_sha256"`
	Tombstone      bool                        `json:"tombstone"`
}

type checkpointWireV2 struct {
	SchemaVersion               uint32            `json:"schema_version"`
	CheckpointID                string            `json:"checkpoint_id"`
	ThroughRecordRevision       uint64            `json:"through_record_revision"`
	EventRegistrySHA256         string            `json:"event_registry_sha256"`
	ReducerRegistrySHA256       string            `json:"reducer_registry_sha256"`
	CanonicalCodecVersion       uint32            `json:"canonical_codec_version"`
	RollingPrefixSHA256         string            `json:"rolling_prefix_sha256"`
	Aggregates                  []aggregateWireV2 `json:"aggregates"`
	OperationSummary            json.RawMessage   `json:"operation_summary"`
	CanonicalProjectionSHA256   string            `json:"canonical_projection_sha256"`
	CoveredThroughPublicationID string            `json:"covered_through_publication_id"`
	CheckpointSHA256            string            `json:"checkpoint_sha256,omitempty"`
}

type slotWireV2 struct {
	SchemaVersion         uint32 `json:"schema_version"`
	SlotID                string `json:"slot_id"`
	CheckpointID          string `json:"checkpoint_id"`
	CheckpointSHA256      string `json:"checkpoint_sha256"`
	ThroughRecordRevision uint64 `json:"through_record_revision"`
}

func digest(domain string, payload []byte) journal.Digest {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	var result journal.Digest
	copy(result[:], h.Sum(nil))
	return result
}

func BuildV2(checkpointID journal.CheckpointID, sealed journal.SealedProjectionPrefixV2) (CheckpointV2, error) {
	if _, err := journal.NewCheckpointRecordAddressV2(checkpointID); err != nil {
		return CheckpointV2{}, err
	}
	if err := sealed.Validate(); err != nil {
		return CheckpointV2{}, err
	}
	entries := sealed.Entries()
	if len(entries) > MaxCheckpointAggregates {
		return CheckpointV2{}, &Error{ErrorBudget, "aggregates"}
	}
	aggregates := make([]AggregateEntryV2, len(entries))
	for i, entry := range entries {
		aggregates[i] = AggregateEntryV2{entry.Coordinate(), entry.LogicalVersion(), entry.LastEventID(), entry.CanonicalState(), entry.StateDigest(), entry.Tombstone()}
	}
	sort.Slice(aggregates, func(i, k int) bool {
		cmp, _ := journal.CompareAggregateCoordinates(aggregates[i].coordinate, aggregates[k].coordinate)
		return cmp < 0
	})
	for i := 1; i < len(aggregates); i++ {
		if cmp, _ := journal.CompareAggregateCoordinates(aggregates[i-1].coordinate, aggregates[i].coordinate); cmp >= 0 {
			return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "aggregate_order"}
		}
	}
	checkpoint := CheckpointV2{checkpointID: checkpointID, throughRevision: sealed.Watermark(), eventRegistry: sealed.EventRegistryDigest(),
		reducerRegistry: sealed.ReducerRegistryDigest(), codecVersion: sealed.CodecVersion(), rollingPrefix: sealed.RollingPrefixDigest(),
		aggregates: aggregates, operationSummary: sealed.OperationSummary(), projectionDigest: sealed.ProjectionDigest(), coveredThrough: sealed.CoveredThroughPublicationID()}
	encoded, checksum, err := encodeCheckpoint(checkpoint)
	if err != nil {
		return CheckpointV2{}, err
	}
	checkpoint.canonical, checkpoint.checkpointDigest = encoded, checksum
	checkpoint.seal = checkpointSeal(checkpoint)
	return checkpoint, nil
}

func EncodeV2(checkpoint CheckpointV2) ([]byte, error) {
	if err := checkpoint.Validate(); err != nil {
		return nil, err
	}
	return checkpoint.CanonicalBytes(), nil
}

func DecodeV2(encoded []byte) (CheckpointV2, error) {
	if len(encoded) == 0 || len(encoded) > journal.MaxCheckpointValueBytes {
		return CheckpointV2{}, &Error{ErrorBudget, "bytes"}
	}
	var wire checkpointWireV2
	if err := decodeStrict(encoded, &wire); err != nil {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "canonical"}
	}
	checkpoint, err := checkpointFromWire(wire)
	if err != nil {
		return CheckpointV2{}, err
	}
	reencoded, checksum, err := encodeCheckpoint(checkpoint)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "canonical"}
	}
	checkpoint.canonical, checkpoint.checkpointDigest = append([]byte(nil), encoded...), checksum
	checkpoint.seal = checkpointSeal(checkpoint)
	return checkpoint, nil
}

func (c CheckpointV2) Validate() error {
	if c.seal.IsZero() || checkpointSeal(c) != c.seal {
		return &Error{ErrorInvalidCheckpoint, "seal"}
	}
	decoded, err := DecodeV2Unsealed(c.canonical)
	if err != nil || decoded.checkpointDigest != c.checkpointDigest || decoded.checkpointID != c.checkpointID {
		return &Error{ErrorInvalidCheckpoint, "seal"}
	}
	return nil
}

// DecodeV2Unsealed is internal validation that deliberately does not call the
// capability validator recursively.
func DecodeV2Unsealed(encoded []byte) (CheckpointV2, error) {
	if len(encoded) == 0 || len(encoded) > journal.MaxCheckpointValueBytes {
		return CheckpointV2{}, &Error{ErrorBudget, "bytes"}
	}
	var wire checkpointWireV2
	if err := decodeStrict(encoded, &wire); err != nil {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "canonical"}
	}
	c, err := checkpointFromWire(wire)
	if err != nil {
		return CheckpointV2{}, err
	}
	reencoded, checksum, err := encodeCheckpoint(c)
	if err != nil || !bytes.Equal(reencoded, encoded) {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "canonical"}
	}
	c.canonical, c.checkpointDigest = append([]byte(nil), encoded...), checksum
	return c, nil
}

func checkpointFromWire(w checkpointWireV2) (CheckpointV2, error) {
	if w.SchemaVersion != SchemaVersionV2 || w.ThroughRecordRevision == 0 || w.CanonicalCodecVersion != journal.CanonicalCodecVersionV2 || len(w.Aggregates) > MaxCheckpointAggregates {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "version_or_budget"}
	}
	id := journal.CheckpointID(w.CheckpointID)
	if _, err := journal.NewCheckpointRecordAddressV2(id); err != nil {
		return CheckpointV2{}, err
	}
	eventDigest, err := journal.ParseDigest("event_registry_sha256", w.EventRegistrySHA256)
	if err != nil {
		return CheckpointV2{}, err
	}
	reducerDigest, err := journal.ParseDigest("reducer_registry_sha256", w.ReducerRegistrySHA256)
	if err != nil {
		return CheckpointV2{}, err
	}
	prefix, err := journal.ParseDigest("rolling_prefix_sha256", w.RollingPrefixSHA256)
	if err != nil {
		return CheckpointV2{}, err
	}
	projection, err := journal.ParseDigest("canonical_projection_sha256", w.CanonicalProjectionSHA256)
	if err != nil {
		return CheckpointV2{}, err
	}
	wantChecksum, err := journal.ParseDigest("checkpoint_sha256", w.CheckpointSHA256)
	if err != nil {
		return CheckpointV2{}, err
	}
	covered := journal.PublicationID(w.CoveredThroughPublicationID)
	if _, err := journal.DecodePublicationID(string(covered)); err != nil {
		return CheckpointV2{}, err
	}
	summary, err := journal.DecodeOperationSummaryV2(w.OperationSummary)
	if err != nil {
		return CheckpointV2{}, err
	}
	aggregates := make([]AggregateEntryV2, len(w.Aggregates))
	var prior []byte
	for i, item := range w.Aggregates {
		coordinate, err := journal.CanonicalAggregateCoordinate(item.Coordinate)
		if err != nil || item.LogicalVersion == 0 || item.LastEventID == "" {
			return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "aggregate"}
		}
		if i > 0 && bytes.Compare(prior, coordinate) >= 0 {
			return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "aggregate_order"}
		}
		prior = coordinate
		state, err := journal.CanonicalObject(item.State)
		if err != nil {
			return CheckpointV2{}, err
		}
		stateDigest, err := journal.ParseDigest("state_sha256", item.StateSHA256)
		actualStateDigest, stateErr := journal.ReducerStateDigestV2(state)
		if err != nil || stateErr != nil || stateDigest != actualStateDigest {
			return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "state_digest"}
		}
		aggregates[i] = AggregateEntryV2{item.Coordinate, journal.LogicalVersion(item.LogicalVersion), journal.EventID(item.LastEventID), state, stateDigest, item.Tombstone}
	}
	c := CheckpointV2{id, w.ThroughRecordRevision, eventDigest, reducerDigest, w.CanonicalCodecVersion, prefix, aggregates, summary, projection, covered, wantChecksum, nil, journal.Digest{}}
	_, actualChecksum, err := encodeCheckpoint(c)
	if err != nil || actualChecksum != wantChecksum {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "checkpoint_digest"}
	}
	return c, nil
}

func encodeCheckpoint(c CheckpointV2) ([]byte, journal.Digest, error) {
	summary, err := journal.EncodeOperationSummaryV2(c.operationSummary)
	if err != nil {
		return nil, journal.Digest{}, err
	}
	if len(c.aggregates) > MaxCheckpointAggregates || len(summary) > journal.MaxCheckpointValueBytes {
		return nil, journal.Digest{}, &Error{ErrorBudget, "bytes"}
	}
	valueBytes := len(summary)
	aggregates := make([]aggregateWireV2, len(c.aggregates))
	for i, item := range c.aggregates {
		if len(item.state) > journal.MaxCheckpointValueBytes-valueBytes {
			return nil, journal.Digest{}, &Error{ErrorBudget, "bytes"}
		}
		valueBytes += len(item.state)
		aggregates[i] = aggregateWireV2{item.coordinate, uint64(item.version), string(item.lastEventID), json.RawMessage(item.state), item.stateDigest.String(), item.tombstone}
	}
	wire := checkpointWireV2{SchemaVersionV2, string(c.checkpointID), c.throughRevision, c.eventRegistry.String(), c.reducerRegistry.String(), c.codecVersion,
		c.rollingPrefix.String(), aggregates, json.RawMessage(summary), c.projectionDigest.String(), string(c.coveredThrough), ""}
	unsigned, err := json.Marshal(wire)
	if err != nil {
		return nil, journal.Digest{}, err
	}
	unsigned, err = canonicalJSONBounded(unsigned, journal.MaxCheckpointValueBytes)
	if err != nil {
		return nil, journal.Digest{}, err
	}
	checksum := digest(checkpointDomainV2, unsigned)
	wire.CheckpointSHA256 = checksum.String()
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, journal.Digest{}, err
	}
	encoded, err = canonicalJSONBounded(encoded, journal.MaxCheckpointValueBytes)
	if err != nil {
		return nil, journal.Digest{}, &Error{ErrorBudget, "bytes"}
	}
	return encoded, checksum, nil
}

func checkpointSeal(c CheckpointV2) journal.Digest {
	return digest("spec-kitty.checkpoint-capability.v2", append(append([]byte(nil), c.canonical...), c.checkpointDigest[:]...))
}

func NewSlotV2(slotID journal.CheckpointSlotID, checkpoint CheckpointV2) (SlotV2, []byte, error) {
	if _, err := journal.NewCheckpointSlotAddressV2(slotID); err != nil {
		return SlotV2{}, nil, err
	}
	if err := checkpoint.Validate(); err != nil {
		return SlotV2{}, nil, err
	}
	slot := SlotV2{slotID, checkpoint.checkpointID, checkpoint.checkpointDigest, checkpoint.throughRevision}
	encoded, err := json.Marshal(slotWireV2{SchemaVersionV2, string(slotID), string(slot.checkpointID), slot.checkpointDigest.String(), slot.throughRevision})
	if err != nil {
		return SlotV2{}, nil, err
	}
	encoded, err = canonicalJSONBounded(encoded, journal.MaxCanonicalPayloadBytes)
	return slot, encoded, err
}

func decodeSlot(encoded []byte) (SlotV2, error) {
	var w slotWireV2
	if err := decodeStrict(encoded, &w); err != nil || w.SchemaVersion != SchemaVersionV2 || w.ThroughRecordRevision == 0 {
		return SlotV2{}, &Error{ErrorInvalidCheckpoint, "slot"}
	}
	slotID := journal.CheckpointSlotID(w.SlotID)
	if _, err := journal.NewCheckpointSlotAddressV2(slotID); err != nil {
		return SlotV2{}, err
	}
	id := journal.CheckpointID(w.CheckpointID)
	if _, err := journal.NewCheckpointRecordAddressV2(id); err != nil {
		return SlotV2{}, err
	}
	d, err := journal.ParseDigest("checkpoint_sha256", w.CheckpointSHA256)
	if err != nil {
		return SlotV2{}, err
	}
	reencoded, err := json.Marshal(w)
	if err != nil {
		return SlotV2{}, err
	}
	reencoded, err = canonicalJSONBounded(reencoded, journal.MaxCanonicalPayloadBytes)
	if err != nil {
		return SlotV2{}, err
	}
	if !bytes.Equal(reencoded, encoded) {
		return SlotV2{}, &Error{ErrorInvalidCheckpoint, "slot_canonical"}
	}
	return SlotV2{slotID, id, d, w.ThroughRecordRevision}, nil
}

// DecodeSlotV2 validates one canonical retained-ring reference. It exposes
// semantic identities only; provider addresses remain foundation-owned.
func DecodeSlotV2(encoded []byte) (SlotV2, error) { return decodeSlot(encoded) }

func ValidateCandidateV2(candidate CandidateV2, compatibility CompatibilityV2, attempt journal.StoreRevisionV2) (CheckpointV2, error) {
	slot, err := decodeSlot(candidate.SlotRecord.CanonicalValue())
	if err != nil {
		return CheckpointV2{}, err
	}
	c, err := DecodeV2(candidate.CheckpointRecord.CanonicalValue())
	if err != nil {
		return CheckpointV2{}, err
	}
	if candidate.SlotRecord.Address().Kind() != journal.AddressCheckpointSlot || candidate.SlotRecord.Address().CheckpointSlotID() != slot.slotID ||
		candidate.CheckpointRecord.Address().Kind() != journal.AddressCheckpointRecord || candidate.CheckpointRecord.Address().CheckpointID() != c.checkpointID ||
		slot.checkpointID != c.checkpointID || slot.checkpointDigest != c.checkpointDigest || slot.throughRevision != c.throughRevision {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "candidate_identity"}
	}
	commitRevision := candidate.SlotRecord.RecordRevision()
	if commitRevision != candidate.CheckpointRecord.RecordRevision() || commitRevision <= c.throughRevision || commitRevision > attempt.CurrentRevision() || commitRevision > attempt.DurableThroughRevision() || candidate.SlotRecord.ExpiresAt() != "" || candidate.CheckpointRecord.ExpiresAt() != "" {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "candidate_revision"}
	}
	if compatibility.EventRegistryDigest != c.eventRegistry || compatibility.ReducerRegistryDigest != c.reducerRegistry || compatibility.CanonicalCodecVersion != c.codecVersion {
		return CheckpointV2{}, &Error{ErrorIncompatible, "registries"}
	}
	w := candidate.CoveredWitness
	if w.Address().Kind() != journal.AddressPublicationWitness || w.Address().PublicationID() != c.coveredThrough || w.RecordRevision() != c.throughRevision || w.ExpiresAt() != "" {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "covered_witness"}
	}
	witness, err := journal.DecodePublicationWitnessV2(w.CanonicalValue())
	if err != nil || journal.ValidatePublicationWitnessV2(witness) != nil || witness.PublicationID() != c.coveredThrough {
		return CheckpointV2{}, &Error{ErrorInvalidCheckpoint, "covered_witness"}
	}
	return c, nil
}

func SelectV2(candidates []CandidateV2, compatibility CompatibilityV2, attempt journal.StoreRevisionV2) (SelectionV2, error) {
	if len(candidates) == 0 {
		return OriginV2(), nil
	}
	if len(candidates) > RetainedCandidateSlots {
		return SelectionV2{}, &Error{ErrorBudget, "candidates"}
	}
	type validated struct {
		checkpoint     CheckpointV2
		commitRevision uint64
	}
	valid := make([]validated, 0, len(candidates))
	invalidCandidate := false
	for _, candidate := range candidates {
		if c, err := ValidateCandidateV2(candidate, compatibility, attempt); err == nil {
			valid = append(valid, validated{c, candidate.CheckpointRecord.RecordRevision()})
		} else {
			var checkpointError *Error
			if !errors.As(err, &checkpointError) || checkpointError.Kind != ErrorIncompatible {
				invalidCandidate = true
			}
		}
	}
	if len(valid) == 0 {
		if !invalidCandidate {
			return OriginV2(), nil
		}
		return SelectionV2{}, &Error{ErrorInvalidCheckpoint, "candidates"}
	}
	sort.Slice(valid, func(i, k int) bool {
		if valid[i].checkpoint.throughRevision != valid[k].checkpoint.throughRevision {
			return valid[i].checkpoint.throughRevision > valid[k].checkpoint.throughRevision
		}
		return valid[i].checkpoint.checkpointID < valid[k].checkpoint.checkpointID
	})
	selected := valid[0]
	for _, peer := range valid[1:] {
		if peer.checkpoint.throughRevision != selected.checkpoint.throughRevision {
			break
		}
		if peer.checkpoint.checkpointDigest != selected.checkpoint.checkpointDigest {
			return SelectionV2{}, &Error{ErrorContradictory, "watermark"}
		}
	}
	return SelectionV2{checkpoint: selected.checkpoint, commitRevision: selected.commitRevision}, nil
}

func decodeStrict(encoded []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err == nil {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func canonicalJSONBounded(encoded []byte, maximum int) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > maximum {
		return nil, &Error{ErrorBudget, "bytes"}
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, &Error{ErrorInvalidCheckpoint, "canonical"}
	}
	if len(bytes.TrimSpace(encoded[decoder.InputOffset():])) != 0 {
		return nil, &Error{ErrorInvalidCheckpoint, "canonical"}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(canonical) > maximum {
		return nil, &Error{ErrorBudget, "bytes"}
	}
	return canonical, nil
}
