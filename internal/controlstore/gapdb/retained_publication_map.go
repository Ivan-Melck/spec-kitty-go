package gapdb

import (
	"context"
	"errors"
	"strings"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const (
	retainedPublicationLimitV2 = 256
	retainedRecordLimitV2      = 4096
	retainedValueBytesLimitV2  = 32 << 20
	retainedPlanBytesLimitV2   = 32 << 20
	retainedKeyBytesLimitV2    = 4 << 20
	retainedOperationLimitV2   = 8192
)

// All refusals are deliberately independent of keys, values, counts and provider
// diagnostics. Cancellation also uses this fixed error (wrapping ctx.Err would
// admit caller-supplied diagnostics from a custom context).
var errRetainedPublicationMapV2 = errors.New("retained publication map refused")

type retainedPublicationEntryV2 struct {
	canonical []byte
	revision  uint64
	role      journalv2.SealedRecordRole
	target    journalv2.SealedRecordTargetV2
}

// Dense final rows reuse one of the two exact occurrence backings. No hash
// directory, append growth, third backing, or payload copying occurs in sorting.
type retainedPublicationOccurrenceV2 struct {
	key string
	retainedPublicationEntryV2
	condition   journalv2.ConditionKind
	predecessor uint64
}
type retainedPublicationMapV2 []retainedPublicationOccurrenceV2

// Private scalar controls expose the algorithmic ledger to package tests only;
// no caller comparator, observer port, or authority factory is introduced.
type retainedPublicationWorkV2 struct {
	passes, mergeComparisons, mergeAssignments, collapseComparisons, collapseWrites uint64
	mergeKeyBytes, collapseKeyBytes                                                 uint64
}

// Witnesses are immutable and retained, so the largest final entry revision is
// also the largest collected publication revision. This is not a database R.
func (m retainedPublicationMapV2) maxProviderRevision() uint64 {
	var maximum uint64
	for _, entry := range m {
		if entry.revision > maximum {
			maximum = entry.revision
		}
	}
	return maximum
}

type retainedPublicationBudgetV2 struct {
	records, values, plans, keys, operations uint64
}

// charge performs subtraction before addition, even for structural probes with
// arbitrary uint64s. A failed charge leaves the entire budget unchanged.
func (b *retainedPublicationBudgetV2) charge(f journalv2.SealedRecordFootprintV2) bool {
	if b.records > retainedRecordLimitV2 || f.RecordCount > retainedRecordLimitV2-b.records ||
		b.values > retainedValueBytesLimitV2 || f.RecordValueBytes > retainedValueBytesLimitV2-b.values ||
		b.plans > retainedPlanBytesLimitV2 || f.CanonicalPlanBytes > retainedPlanBytesLimitV2-b.plans ||
		b.keys > retainedKeyBytesLimitV2 || f.RecordKeyBytesUpperBound > retainedKeyBytesLimitV2-b.keys ||
		b.operations > retainedOperationLimitV2 || f.RecordCount > retainedOperationLimitV2-b.operations ||
		f.AssertionCount > retainedOperationLimitV2-b.operations-f.RecordCount {
		return false
	}
	b.records += f.RecordCount
	b.values += f.RecordValueBytes
	b.plans += f.CanonicalPlanBytes
	b.keys += f.RecordKeyBytesUpperBound
	b.operations += f.RecordCount + f.AssertionCount
	return true
}

type retainedPublicationOrderV2 struct {
	index       int
	revision    uint64
	publication journalv2.PublicationID
}

// buildRetainedPublicationMapV2 is pure expected-membership construction, NOT
// readback/census, cryptographic revalidation, or original producer/database
// attestation. Only Core can mint nonzero immutable ACKs. Cheap scalar checks
// defend consistency; no full JSON, seal hashing or canonical re-encoding occurs.
//
// ALL size preflight precedes Records/CanonicalValue copies and key encoding.
// Record OCCURRENCES (including overwritten values) are charged; assertions are
// operations but not retained rows. The key cap is the meter's conservative
// record-key bound. These bounds cover this expected map, not a complete peak
// memory proof: integration still needs whole-hash/transport/private budgets.
func preflightRetainedPublicationsV2(ctx context.Context, acknowledgements []journalv2.AcknowledgedPublicationV2) (order [retainedPublicationLimitV2]retainedPublicationOrderV2, budget retainedPublicationBudgetV2, resultErr error) {
	if ctx == nil || ctx.Err() != nil || len(acknowledgements) > retainedPublicationLimitV2 {
		return order, budget, errRetainedPublicationMapV2
	}
	// Fixed bounded scalar metadata; never sort/mutate the caller's ACK array.
	for i, ack := range acknowledgements {
		if ctx.Err() != nil {
			return order, budget, errRetainedPublicationMapV2
		}
		plan := ack.Plan()
		footprint, err := plan.MeasureRecordFootprintV2(ctx)
		if err != nil || !budget.charge(footprint) {
			return order, budget, errRetainedPublicationMapV2
		}
		// These accessors are scalar, including OperationState and Witness; do
		// not call variable-sized getters or claim to recompute the ACK seal.
		state, witness := plan.OperationState(), plan.PublicationWitness()
		if ack.PublicationID() != plan.PublicationID() || ack.PlanDigest() != plan.Digest() ||
			ack.RecordSetDigest() != plan.RecordSetDigest() || ack.MutationCount() <= 0 || ack.AssertionCount() < 0 ||
			uint64(ack.MutationCount()) != footprint.RecordCount || uint64(ack.AssertionCount()) != footprint.AssertionCount ||
			plan.MutationCount() != ack.MutationCount() || plan.AssertionCount() != ack.AssertionCount() ||
			ack.ProviderRevision() == 0 || ack.DurableThroughRevision() < ack.ProviderRevision() ||
			state.OperationID() == "" || state.LogicalVersion() == 0 || state.LastPublicationID() != plan.PublicationID() ||
			witness.PublicationID() != plan.PublicationID() || witness.OperationID() != state.OperationID() ||
			witness.OperationLogicalVersion() != state.LogicalVersion() || witness.PlanDigest() != plan.Digest() ||
			witness.AuthorityRecordSetDigest() != plan.RecordSetDigest() || witness.WitnessDigest().IsZero() ||
			uint64(witness.AuthorityRecordCount()) != footprint.RecordCount-1 {
			return order, budget, errRetainedPublicationMapV2
		}
		for _, prior := range order[:i] {
			if prior.publication == ack.PublicationID() || prior.revision == ack.ProviderRevision() {
				return order, budget, errRetainedPublicationMapV2
			}
		}
		order[i] = retainedPublicationOrderV2{i, ack.ProviderRevision(), ack.PublicationID()}
	}
	if ctx.Err() != nil {
		return order, budget, errRetainedPublicationMapV2
	}
	return order, budget, nil
}

func buildRetainedPublicationMapV2(ctx context.Context, acknowledgements []journalv2.AcknowledgedPublicationV2) (retainedPublicationMapV2, error) {
	if ctx == nil || len(acknowledgements) > retainedPublicationLimitV2 {
		return nil, errRetainedPublicationMapV2
	}
	// Freeze opaque values before ANY caller-context callback. Their sealed
	// internals are immutable; this copies no plan/record/key/value payload.
	// All subsequent metering and copying must use these exact same objects.
	var snapshot [retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2
	copy(snapshot[:], acknowledgements)
	acknowledgements = snapshot[:len(acknowledgements)]
	order, budget, err := preflightRetainedPublicationsV2(ctx, acknowledgements)
	if err != nil {
		return nil, err
	}
	return copyRetainedPublicationMapV2(ctx, acknowledgements, &order, budget)
}

func copyRetainedPublicationMapV2(ctx context.Context, acknowledgements []journalv2.AcknowledgedPublicationV2, order *[retainedPublicationLimitV2]retainedPublicationOrderV2, budget retainedPublicationBudgetV2) (retainedPublicationMapV2, error) {
	if budget.records > retainedRecordLimitV2 {
		return nil, errRetainedPublicationMapV2
	}
	rows := make(retainedPublicationMapV2, int(budget.records))
	scratch := make(retainedPublicationMapV2, int(budget.records))
	filled := 0
	for _, publication := range order[:len(acknowledgements)] {
		if ctx.Err() != nil {
			return nil, errRetainedPublicationMapV2
		}
		// Records clones only this bounded plan's records, not all plan data.
		for _, record := range acknowledgements[publication.index].Plan().Records() {
			if ctx.Err() != nil {
				return nil, errRetainedPublicationMapV2
			}
			key, err := v2KeyForSealedRecord(record)
			if err != nil {
				return nil, errRetainedPublicationMapV2
			}
			if _, err := v2ProviderCondition(record); err != nil {
				return nil, errRetainedPublicationMapV2
			}
			if record.Role() == journalv2.RecordPublicationWitness && key.PublicationID() != publication.publication {
				return nil, errRetainedPublicationMapV2
			}
			class := record.ValueClass()
			if (record.Role() == journalv2.RecordCheckpointRecord) != (class == journalv2.RecordValueCheckpoint) ||
				(class != journalv2.RecordValueOrdinary && class != journalv2.RecordValueCheckpoint) {
				return nil, errRetainedPublicationMapV2
			}
			value := record.CanonicalValue() // exact bytes, independently owned; never normalize
			maximum := MaxOrdinaryValueBytes
			if class == journalv2.RecordValueCheckpoint {
				maximum = MaxCheckpointValueBytes
			}
			if len(value) == 0 || len(value) > maximum || len(key.String()) > MaxKeyBytes {
				return nil, errRetainedPublicationMapV2
			}
			entry := retainedPublicationEntryV2{value, publication.revision, record.Role(), record.Target()}
			row := retainedPublicationOccurrenceV2{key.String(), entry, record.Condition().Kind(), record.Condition().ObservedRevision()}
			if filled == len(rows) || !validRetainedPublicationOccurrenceV2(row) {
				return nil, errRetainedPublicationMapV2
			}
			rows[filled] = row
			filled++
		}
	}
	if filled != len(rows) || ctx.Err() != nil {
		return nil, errRetainedPublicationMapV2
	}
	return sortCollapseRetainedPublicationsV2(ctx, rows, scratch, nil)
}

// Each occurrence is checked before insertion, including those later overwritten.
func validRetainedPublicationOccurrenceV2(row retainedPublicationOccurrenceV2) bool {
	if row.revision == 0 || len(row.canonical) == 0 || len(row.key) > MaxKeyBytes {
		return false
	}
	parsed, err := parseV2Key(row.key)
	return err == nil && parsed.String() == row.key && retainedPublicationNamespaceV2(parsed, row.role)
}

func retainedMutableRoleV2(role journalv2.SealedRecordRole) bool {
	switch role {
	case journalv2.RecordOperationHead, journalv2.RecordAggregateHead, journalv2.RecordEligibilityGuard, journalv2.RecordCheckpointSlot:
		return true
	default:
		return false
	}
}

// r charged occurrences, L=ceil(log2(max(1,r))): comparisons <=rL,
// logical key examinations <=2*MaxKeyBytes*rL, header assignments <=rL.
// Compacts whichever array is sorted, without an odd-pass final copy.
func sortCollapseRetainedPublicationsV2(ctx context.Context, rows, scratch retainedPublicationMapV2, work *retainedPublicationWorkV2) (retainedPublicationMapV2, error) {
	r := len(rows)
	if ctx == nil || r > retainedRecordLimitV2 || cap(rows) != r || len(scratch) != r || cap(scratch) != r {
		return nil, errRetainedPublicationMapV2
	}
	for width := 1; width < r; width *= 2 {
		if work != nil {
			work.passes++
		}
		for start := 0; start < r; start += 2 * width {
			middle, end := min(start+width, r), min(start+2*width, r)
			i, j, out := start, middle, start
			for i < middle && j < end {
				if ctx.Err() != nil {
					return nil, errRetainedPublicationMapV2
				}
				// Exactly ONE lexical comparison per decision; revisions unsigned.
				comparison := strings.Compare(rows[i].key, rows[j].key)
				if work != nil {
					work.mergeComparisons++
					work.mergeKeyBytes += uint64(len(rows[i].key) + len(rows[j].key))
					work.mergeAssignments++
				}
				if comparison < 0 || (comparison == 0 && rows[i].revision <= rows[j].revision) {
					scratch[out] = rows[i]
					i++
				} else {
					scratch[out] = rows[j]
					j++
				}
				out++
			}
			if ctx.Err() != nil {
				return nil, errRetainedPublicationMapV2
			}
			copied := copy(scratch[out:end], rows[i:middle])
			if ctx.Err() != nil {
				return nil, errRetainedPublicationMapV2
			}
			copied += copy(scratch[out+copied:end], rows[j:end])
			if work != nil {
				work.mergeAssignments += uint64(copied)
			}
		}
		rows, scratch = scratch, rows
	}
	// Single equal-key boundary scan, reused for validation AND compaction.
	// M=max(0,r-1) comparisons, <=2*MaxKeyBytes*M logical key bytes.
	used := 0
	var previous retainedPublicationOccurrenceV2
	for i := 0; i < r; i++ {
		if ctx.Err() != nil {
			return nil, errRetainedPublicationMapV2
		}
		next := rows[i]
		same := false
		if i > 0 {
			same = strings.Compare(previous.key, next.key) == 0
			if work != nil {
				work.collapseComparisons++
				work.collapseKeyBytes += uint64(len(previous.key) + len(next.key))
			}
		}
		if same {
			if previous.role != next.role || previous.target != next.target || previous.revision >= next.revision ||
				!retainedMutableRoleV2(next.role) || next.condition != journalv2.ConditionRecordRevision || next.predecessor == 0 || next.predecessor != previous.revision {
				return nil, errRetainedPublicationMapV2
			}
		} else {
			if next.condition != journalv2.ConditionAbsent || next.predecessor != 0 {
				return nil, errRetainedPublicationMapV2
			}
			if i > 0 {
				rows[used] = previous
				used++
				if work != nil {
					work.collapseWrites++
				}
			}
		}
		previous = next
	}
	if ctx.Err() != nil {
		return nil, errRetainedPublicationMapV2
	}
	if r > 0 {
		rows[used] = previous
		used++
		if work != nil {
			work.collapseWrites++
		}
	}
	// u compactions + (r-u) tail clears + r other-buffer clears = 2r.
	if ctx.Err() != nil {
		return nil, errRetainedPublicationMapV2
	}
	clear(rows[used:])
	if ctx.Err() != nil {
		return nil, errRetainedPublicationMapV2
	}
	clear(scratch)
	if work != nil {
		work.collapseWrites += uint64(r - used + r)
	}
	if ctx.Err() != nil {
		return nil, errRetainedPublicationMapV2
	}
	return rows[:used], nil
}
