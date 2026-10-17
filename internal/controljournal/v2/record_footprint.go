package v2

import "context"

// SealedRecordFootprintV2 is a scalar size observation of private sealed records.
// Assertions are charged separately and are not members of RecordCount.
// Key bytes are conservative MaxProductKeyBytes (1 KiB) charges, NOT actual
// encoded key lengths. ProductRawBytesUpperBound includes that charge plus each
// record value length, and a key charge plus 20 bytes per assertion.
// This is not peak memory, whole-store cardinality, semantic/ACK validation,
// seal authentication, or a complete budget for subsequent processing.
type SealedRecordFootprintV2 struct {
	RecordCount               uint64
	CheckpointRecordCount     uint64
	AssertionCount            uint64
	RecordValueBytes          uint64
	LargestRecordValueBytes   uint64
	CanonicalPlanBytes        uint64
	RecordKeyBytesUpperBound  uint64
	ProductRawBytesUpperBound uint64
}

// MeasureRecordFootprintV2 reads only private lengths and cheap seal markers;
// it neither clones nor hashes nor decodes canonical data. Successful calls
// allocate no heap objects. Every refusal returns the zero footprint.
func (p SealedCommitPlanV2) MeasureRecordFootprintV2(ctx context.Context) (SealedRecordFootprintV2, error) {
	if ctx == nil {
		return SealedRecordFootprintV2{}, invalid(ErrorInvalidCanonicalData, "record_footprint_context")
	}
	if err := ctx.Err(); err != nil {
		return SealedRecordFootprintV2{}, err
	}
	// Subtract only after independently bounding counts; never add unbounded
	// lengths or enter loops before bounding both counts and canonical bytes.
	if len(p.records) > MaxMembers || len(p.assertions) > MaxConditionsPerPlan ||
		len(p.assertions) > MaxMembers-len(p.records) {
		return SealedRecordFootprintV2{}, invalid(ErrorBudgetExceeded, "sealed_plan_operations")
	}
	if len(p.canonical) > MaxCommitPlanBytes {
		return SealedRecordFootprintV2{}, invalid(ErrorBudgetExceeded, "commit_plan_bytes")
	}
	if len(p.records) == 0 || len(p.canonical) == 0 || p.digest.IsZero() || p.recordSetDigest.IsZero() {
		return SealedRecordFootprintV2{}, invalid(ErrorInvalidDigest, "record_footprint_seal_markers")
	}
	if _, err := DecodePublicationID(string(p.publicationID)); err != nil {
		return SealedRecordFootprintV2{}, err
	}

	footprint := SealedRecordFootprintV2{
		RecordCount: uint64(len(p.records)), AssertionCount: uint64(len(p.assertions)),
		CanonicalPlanBytes: uint64(len(p.canonical)),
		// The bounded count makes this multiplication at most 256 KiB.
		RecordKeyBytesUpperBound: uint64(len(p.records)) * uint64(MaxProductKeyBytes),
	}
	budget := productRawBudget{}
	for i := range p.records {
		if err := ctx.Err(); err != nil {
			return SealedRecordFootprintV2{}, err
		}
		record := &p.records[i]
		switch record.role {
		case RecordCheckpointRecord:
			if record.valueClass != RecordValueCheckpoint {
				return SealedRecordFootprintV2{}, invalid(ErrorInvalidCanonicalData, "record_footprint_value_class")
			}
			footprint.CheckpointRecordCount++
		case RecordOperationBinding, RecordJournalEvent, RecordOperationHead, RecordAggregateHead,
			RecordEffectIntent, RecordEligibilityGuard, RecordCheckpointSlot, RecordWorkspacePathClaim, RecordPublicationWitness:
			if record.valueClass != RecordValueOrdinary {
				return SealedRecordFootprintV2{}, invalid(ErrorInvalidCanonicalData, "record_footprint_value_class")
			}
		default:
			return SealedRecordFootprintV2{}, invalid(ErrorInvalidCanonicalData, "record_footprint_role")
		}
		valueBytes := uint64(len(record.canonical))
		if valueBytes == 0 {
			return SealedRecordFootprintV2{}, invalid(ErrorInvalidCanonicalData, "record_footprint_value")
		}
		// Strict role/class checks above avoid the raw helper's ordinary fallback.
		// Its per-value bound and subtraction-before-add raw charge prove that
		// the subsequent value sum cannot overflow (it is <= the 6 MiB charge).
		if err := budget.addRecord(valueBytes, record.valueClass); err != nil {
			return SealedRecordFootprintV2{}, err
		}
		footprint.RecordValueBytes += valueBytes
		if valueBytes > footprint.LargestRecordValueBytes {
			footprint.LargestRecordValueBytes = valueBytes
		}
	}
	for range p.assertions {
		if err := ctx.Err(); err != nil {
			return SealedRecordFootprintV2{}, err
		}
		if err := budget.addAssertion(); err != nil {
			return SealedRecordFootprintV2{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return SealedRecordFootprintV2{}, err
	}
	footprint.ProductRawBytesUpperBound = budget.value
	return footprint, nil
}
