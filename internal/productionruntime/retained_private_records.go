package productionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

const (
	retainedPrivateHeadKey   = "spk/runtime-admission/v2/head"
	retainedPrivateMaxEpochs = 64
	retainedPrivateMaxRows   = 195 // Binding, current, head, and 64 independently read triples.
	// Actual fixed schemas, even with maximum owner tokens, are below 1 KiB.
	// These narrower PRIVATE ceilings do not widen the product's 256 KiB limit.
	retainedPrivateMaxValueBytes = 4 << 10
	retainedPrivateMaxTotalBytes = 256 << 10
	retainedPrivateMaxRPCBytes   = 1 << 20
	retainedPrivateMaxDuration   = 5 * time.Second
)

// Only the production owner may supply this policy; callers cannot select keys
// or manufacture identities. Nonzero limits may tighten, never widen, ceilings.
type retainedPrivatePolicy struct {
	maximumEpochs     int
	maximumRows       int
	maximumValueBytes uint64
	maximumTotalBytes uint64
	maximumRPCBytes   uint64
	maximumDuration   time.Duration
}

func defaultRetainedPrivatePolicy() retainedPrivatePolicy {
	return retainedPrivatePolicy{retainedPrivateMaxEpochs, retainedPrivateMaxRows,
		retainedPrivateMaxValueBytes, retainedPrivateMaxTotalBytes,
		retainedPrivateMaxRPCBytes, retainedPrivateMaxDuration}
}

func (p retainedPrivatePolicy) valid() bool {
	return p.maximumEpochs > 0 && p.maximumEpochs <= retainedPrivateMaxEpochs &&
		p.maximumRows > 0 && p.maximumRows <= retainedPrivateMaxRows &&
		p.maximumValueBytes > 0 && p.maximumValueBytes <= retainedPrivateMaxValueBytes &&
		p.maximumValueBytes <= storegapdb.MaxOrdinaryValueBytes &&
		p.maximumTotalBytes > 0 && p.maximumTotalBytes <= retainedPrivateMaxTotalBytes &&
		p.maximumRPCBytes > 0 && p.maximumRPCBytes <= retainedPrivateMaxRPCBytes &&
		p.maximumDuration > 0 && p.maximumDuration <= retainedPrivateMaxDuration
}

// Owned copies for a later private Host union, with no public accessor or raw
// key port. Every member was independently read; no predicted cardinality is
// credited. This does NOT authenticate RPCs, original writers/CAS/batch ACKs,
// erased history, disconnected epochs, or a complete global retained census.
type retainedPrivateRecord struct {
	key      string
	value    []byte
	revision uint64
}

type retainedPrivateRecords struct {
	rows map[string]retainedPrivateRecord
}

func retainedPrivateRefusal() error { return proofIncomplete("retained_private_records") }

// observeRetainedPrivateRecords consumes only trusted owner expectations and
// the EXISTING owner-only authorityReader. Identities used in derived lookups
// come only from strict, source-format-validated authority readback.
func observeRetainedPrivateRecords(parent context.Context, reader authorityReader, expected AuthorityBindingV2, expectedEpoch journalv2.RuntimeEpochID, captured journalv2.StoreRevisionV2, policy retainedPrivatePolicy) (retainedPrivateRecords, error) {
	if parent == nil || reader == nil || !policy.valid() {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	ctx, cancel := context.WithTimeout(parent, policy.maximumDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return retainedPrivateRecords{}, err
	}
	// Bootstrap the database from trusted owner arguments, never the binding row.
	if _, err := journalv2.NewStoreRevisionV2(expected.DatabaseID, captured.CurrentRevision(), captured.DurableThroughRevision()); err != nil || captured.DatabaseID() != expected.DatabaseID {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	if _, err := journalv2.NewHostAuthorityID(string(expected.HostAuthorityID)); err != nil {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	if _, err := journalv2.NewOperationID(expected.CreatedOperationID); err != nil || !retainedPrivateGeneratedID(string(expectedEpoch), "epoch-") {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	bindingBytes, err := expected.canonical()
	if err != nil {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	work := retainedPrivateWork{ctx: ctx, reader: reader, policy: policy,
		capturedRevision: captured.CurrentRevision(), rows: make(map[string]retainedPrivateRecord)}
	result, err := work.observe(expected, expectedEpoch, bindingBytes)
	if err != nil {
		// Never return partial rows or provider/content diagnostics, including
		// providers wrapping a cancellation sentinel in content-bearing errors.
		if ctx.Err() != nil {
			return retainedPrivateRecords{}, ctx.Err()
		}
		if errors.Is(err, context.Canceled) {
			return retainedPrivateRecords{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return retainedPrivateRecords{}, context.DeadlineExceeded
		}
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	if err := ctx.Err(); err != nil {
		return retainedPrivateRecords{}, err
	}
	return result, nil
}

type retainedPrivateWork struct {
	ctx              context.Context
	reader           authorityReader
	policy           retainedPrivatePolicy
	capturedRevision uint64
	totalBytes       uint64
	rpcBytes         uint64
	rows             map[string]retainedPrivateRecord
}

func (w *retainedPrivateWork) read(key string) (retainedPrivateRecord, error) {
	if err := w.ctx.Err(); err != nil {
		return retainedPrivateRecord{}, err
	}
	// Reserve the EXISTING ordinary-value ceiling BEFORE Get, not the smaller
	// private schema ceiling. After a valid response, retain its actual charge.
	// Sequential reads therefore budget the accepted prefix plus one worst
	// response, not 195 simultaneously live worst responses. Oversized/error
	// responses terminate with no refund or partial result. The transport must
	// independently enforce its response allocation ceiling; Get has no limit
	// parameter and this helper cannot prevent a provider-side allocation.
	if len(w.rows) >= w.policy.maximumRows || w.rpcBytes > w.policy.maximumRPCBytes ||
		storegapdb.MaxOrdinaryValueBytes > w.policy.maximumRPCBytes-w.rpcBytes {
		return retainedPrivateRecord{}, retainedPrivateRefusal()
	}
	if _, exists := w.rows[key]; exists {
		return retainedPrivateRecord{}, retainedPrivateRefusal()
	}
	w.rpcBytes += storegapdb.MaxOrdinaryValueBytes
	record, err := w.reader.Get(w.ctx, key)
	if err != nil {
		return retainedPrivateRecord{}, err
	}
	if err := w.ctx.Err(); err != nil {
		return retainedPrivateRecord{}, err
	}
	size := uint64(len(record.Value))
	if record.Key != key || record.Revision == 0 || record.Revision > w.capturedRevision || record.ExpiresAt != nil ||
		size == 0 || size > w.policy.maximumValueBytes || w.totalBytes > w.policy.maximumTotalBytes || size > w.policy.maximumTotalBytes-w.totalBytes {
		return retainedPrivateRecord{}, retainedPrivateRefusal()
	}
	w.totalBytes += size
	w.rpcBytes -= storegapdb.MaxOrdinaryValueBytes - size
	if err := w.ctx.Err(); err != nil {
		return retainedPrivateRecord{}, err
	}
	owned := retainedPrivateRecord{key: key, value: bytes.Clone(record.Value), revision: record.Revision}
	w.rows[key] = owned
	return owned, nil
}

func (w *retainedPrivateWork) observe(binding AuthorityBindingV2, expectedEpoch journalv2.RuntimeEpochID, bindingBytes []byte) (retainedPrivateRecords, error) {
	bindingRow, err := w.read(authorityBindingKey)
	if err != nil {
		return retainedPrivateRecords{}, err
	}
	if !bytes.Equal(bindingRow.value, bindingBytes) {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	currentRow, err := w.read(runtimeCurrentKey)
	if err != nil {
		return retainedPrivateRecords{}, err
	}
	var current runtimeCurrentV2
	if err := w.decode(currentRow.value, &current); err != nil {
		return retainedPrivateRecords{}, err
	}
	if !retainedPrivateCurrentValid(current, binding, bindingRow.revision, currentRow.revision) || current.RuntimeEpochID != expectedEpoch {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	if err := w.ctx.Err(); err != nil {
		return retainedPrivateRecords{}, err
	}
	currentBytes, err := retainedPrivateCanonical(current)
	if err != nil || !bytes.Equal(currentBytes, currentRow.value) {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}
	head, err := w.read(retainedPrivateHeadKey)
	if err != nil {
		return retainedPrivateRecords{}, err
	}
	if err := w.ctx.Err(); err != nil {
		return retainedPrivateRecords{}, err
	}
	headBytes, err := retainedPrivateHeadBytes(current.RuntimeEpochID)
	if err != nil || head.revision != currentRow.revision || !bytes.Equal(head.value, headBytes) {
		return retainedPrivateRecords{}, retainedPrivateRefusal()
	}

	epochs := make(map[journalv2.RuntimeEpochID]bool)
	operations := make(map[string]bool)
	wanted := current.RuntimeEpochID
	var successor runtimeCurrentV2
	var successorRevision uint64
	for {
		if err := w.ctx.Err(); err != nil {
			return retainedPrivateRecords{}, err
		}
		// Refuse a 65th epoch before even deriving its key. Reserve three row
		// slots and enough RPC budget for two accepted maximum private values
		// followed by a worst ordinary-value response, before deriving keys.
		if len(epochs) >= w.policy.maximumEpochs || epochs[wanted] ||
			w.policy.maximumRows-len(w.rows) < 3 || w.rpcBytes > w.policy.maximumRPCBytes ||
			storegapdb.MaxOrdinaryValueBytes+2*w.policy.maximumValueBytes > w.policy.maximumRPCBytes-w.rpcBytes {
			return retainedPrivateRecords{}, retainedPrivateRefusal()
		}
		epochs[wanted] = true
		epochRow, err := w.read(runtimeEpochRecordKey(wanted))
		if err != nil {
			return retainedPrivateRecords{}, err
		}
		var wrapper struct {
			SchemaVersion uint32           `json:"schema_version"`
			Current       runtimeCurrentV2 `json:"current"`
		}
		if err := w.decode(epochRow.value, &wrapper); err != nil {
			return retainedPrivateRecords{}, err
		}
		rowCurrent := wrapper.Current
		if wrapper.SchemaVersion != 2 || rowCurrent.RuntimeEpochID != wanted ||
			!retainedPrivateCurrentValid(rowCurrent, binding, bindingRow.revision, epochRow.revision) || operations[rowCurrent.CreatedOperationID] {
			return retainedPrivateRecords{}, retainedPrivateRefusal()
		}
		if successorRevision == 0 {
			if epochRow.revision != currentRow.revision || rowCurrent != current {
				return retainedPrivateRecords{}, retainedPrivateRefusal()
			}
		} else if epochRow.revision >= successorRevision || epochRow.revision > successor.RecoveredStoreRevision {
			return retainedPrivateRecords{}, retainedPrivateRefusal()
		}
		if err := w.ctx.Err(); err != nil {
			return retainedPrivateRecords{}, err
		}
		epochBytes, err := runtimeEpochRecordBytes(rowCurrent)
		if err != nil || !bytes.Equal(epochRow.value, epochBytes) {
			return retainedPrivateRecords{}, retainedPrivateRefusal()
		}
		operations[rowCurrent.CreatedOperationID] = true
		if err := w.ctx.Err(); err != nil {
			return retainedPrivateRecords{}, err
		}
		operation, err := w.read("spk/runtime-admission/v2/operations/" + rowCurrent.CreatedOperationID)
		if err != nil {
			return retainedPrivateRecords{}, err
		}
		if err := w.ctx.Err(); err != nil {
			return retainedPrivateRecords{}, err
		}
		operationBytes, err := retainedPrivateOperationBytes(rowCurrent)
		if err != nil || operation.revision != epochRow.revision || !bytes.Equal(operation.value, operationBytes) {
			return retainedPrivateRecords{}, retainedPrivateRefusal()
		}
		if err := w.ctx.Err(); err != nil {
			return retainedPrivateRecords{}, err
		}
		event, err := w.read("spk/runtime-admission/v2/events/" + string(rowCurrent.RuntimeEpochID))
		if err != nil {
			return retainedPrivateRecords{}, err
		}
		if err := w.ctx.Err(); err != nil {
			return retainedPrivateRecords{}, err
		}
		eventBytes, err := retainedPrivateEventBytes(rowCurrent)
		if err != nil || event.revision != epochRow.revision || !bytes.Equal(event.value, eventBytes) {
			return retainedPrivateRecords{}, retainedPrivateRefusal()
		}
		if rowCurrent.PredecessorEpochID == "" {
			break
		}
		wanted = rowCurrent.PredecessorEpochID
		successor, successorRevision = rowCurrent, epochRow.revision
	}
	return retainedPrivateRecords{rows: w.rows}, nil
}

func retainedPrivateCurrentValid(c runtimeCurrentV2, binding AuthorityBindingV2, bindingRevision, ownRevision uint64) bool {
	return c.SchemaVersion == 2 && c.Status == "ready" && c.HostAuthorityID == binding.HostAuthorityID && c.DatabaseID == binding.DatabaseID &&
		hexDigest(c.ProjectionSHA256) && retainedPrivateGeneratedID(string(c.RuntimeEpochID), "epoch-") &&
		(c.PredecessorEpochID == "" || retainedPrivateGeneratedID(string(c.PredecessorEpochID), "epoch-")) &&
		retainedPrivateGeneratedID(c.CreatedOperationID, "runtime-admit-") && retainedPrivateGeneratedID(c.RecoveryAttemptID, "restore-") &&
		c.RecoveredStoreRevision >= bindingRevision && c.RecoveredStoreRevision < ownRevision
}

func retainedPrivateGeneratedID(value, prefix string) bool {
	if len(value) != len(prefix)+32 || !strings.HasPrefix(value, prefix) {
		return false
	}
	for i := len(prefix); i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (w *retainedPrivateWork) decode(value []byte, target any) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if len(value) == 0 || uint64(len(value)) > w.policy.maximumValueBytes {
		return retainedPrivateRefusal()
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return retainedPrivateRefusal()
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	// In addition to strict known-schema decoding, reject duplicate fields at
	// EVERY depth and all noncanonical encodings before reconstructing bytes.
	canonical, err := journalv2.CanonicalObject(value)
	if err != nil || !bytes.Equal(canonical, value) {
		return retainedPrivateRefusal()
	}
	return w.ctx.Err()
}

func retainedPrivateCanonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return journalv2.CanonicalObject(raw)
}

// Exact source values emitted by admitFreshEpoch, not new authority schemas.
func retainedPrivateHeadBytes(epoch journalv2.RuntimeEpochID) ([]byte, error) {
	return journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"runtime_epoch_id":%q,"schema_version":2,"status":"ready"}`, epoch)))
}
func retainedPrivateOperationBytes(c runtimeCurrentV2) ([]byte, error) {
	return journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"operation_id":%q,"runtime_epoch_id":%q,"schema_version":2,"status":"resolved"}`, c.CreatedOperationID, c.RuntimeEpochID)))
}
func retainedPrivateEventBytes(c runtimeCurrentV2) ([]byte, error) {
	return journalv2.CanonicalObject([]byte(fmt.Sprintf(`{"event_type":"runtime_host.epoch_admitted","operation_id":%q,"runtime_epoch_id":%q,"schema_version":2}`, c.CreatedOperationID, c.RuntimeEpochID)))
}
