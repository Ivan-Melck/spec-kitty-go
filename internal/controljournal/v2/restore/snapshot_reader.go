package restore

import (
	"context"
	"encoding/binary"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

const snapshotCursorVersion byte = 1

type snapshotAttemptReaderV2 struct {
	base     ReaderV2
	attempt  journal.StoreRevisionV2
	asOf     string
	snapshot journal.RecoverySnapshotV2
	byScan   map[journal.ScanTargetKindV2][]journal.RecoveryRecordV2
}

func recoveryAttemptReader(ctx context.Context, base ReaderV2, attempt journal.StoreRevisionV2) (ReaderV2, error) {
	capability, ok := base.(RecoverySnapshotReaderV2)
	if !ok {
		return base, nil
	}
	request, err := journal.NewRecoverySnapshotRequestV2(attempt, journal.MaxRecoverySnapshotRecords, journal.MaxRecoverySnapshotBytes)
	if err != nil {
		return nil, err
	}
	snapshot, err := capability.ReadRecoverySnapshotV2(ctx, request)
	if err != nil {
		// A structurally absent capability is the only admitted fallback. A
		// present but stale, corrupt, or over-budget snapshot fails closed.
		if journal.IsKind(err, journal.ErrorUnavailable) {
			return base, nil
		}
		return nil, err
	}
	if snapshot.ObservedRevision() != attempt.CurrentRevision() {
		return nil, unstable("recovery_snapshot_revision")
	}
	reader := &snapshotAttemptReaderV2{
		base: base, attempt: attempt, asOf: snapshot.AsOf(),
		snapshot: snapshot,
		byScan:   make(map[journal.ScanTargetKindV2][]journal.RecoveryRecordV2),
	}
	err = snapshot.VisitRecordsV2(func(record journal.RecoveryRecordV2) error {
		address := record.Address()
		switch address.Kind() {
		case journal.AddressAggregateHead:
			reader.byScan[journal.ScanAllAggregateHeads] = append(reader.byScan[journal.ScanAllAggregateHeads], record)
		case journal.AddressOperationBinding, journal.AddressOperationHead:
			reader.byScan[journal.ScanAllOperationRecords] = append(reader.byScan[journal.ScanAllOperationRecords], record)
		case journal.AddressCheckpointSlot:
			reader.byScan[journal.ScanCheckpointSlots] = append(reader.byScan[journal.ScanCheckpointSlots], record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reader, nil
}

func (r *snapshotAttemptReaderV2) CaptureRevisionV2(ctx context.Context) (journal.StoreRevisionV2, error) {
	return r.base.CaptureRevisionV2(ctx)
}

func (r *snapshotAttemptReaderV2) VerifyRevisionV2(ctx context.Context, expected journal.StoreRevisionV2) error {
	return r.base.VerifyRevisionV2(ctx, expected)
}

func (r *snapshotAttemptReaderV2) ReadV2(ctx context.Context, request journal.ReadRequestV2) (journal.RecoveryRecordV2, error) {
	if err := ctx.Err(); err != nil {
		return journal.RecoveryRecordV2{}, err
	}
	if request.AttemptRevision() != r.attempt {
		return journal.RecoveryRecordV2{}, &journal.Error{Kind: journal.ErrorRevisionMoved, Field: "recovery_snapshot_read"}
	}
	record, ok := r.snapshot.LookupRecordV2(request.Address())
	if !ok {
		return journal.RecoveryRecordV2{}, &journal.Error{Kind: journal.ErrorNotFound, Field: "recovery_snapshot_read"}
	}
	return record, nil
}

func (r *snapshotAttemptReaderV2) ReadManyV2(ctx context.Context, request journal.ReadManyRequestV2) (journal.RecoveryReadManyResultV2, error) {
	if err := ctx.Err(); err != nil {
		return journal.RecoveryReadManyResultV2{}, err
	}
	if request.AttemptRevision() != r.attempt {
		return journal.RecoveryReadManyResultV2{}, &journal.Error{Kind: journal.ErrorRevisionMoved, Field: "recovery_snapshot_read_many"}
	}
	requests := request.Requests()
	records := make([]journal.RecoveryRecordV2, len(requests))
	bytesUsed := 0
	for index, item := range requests {
		record, ok := r.snapshot.LookupRecordV2(item.Address())
		if !ok {
			return journal.RecoveryReadManyResultV2{}, &journal.Error{Kind: journal.ErrorNotFound, Field: "recovery_snapshot_read_many"}
		}
		bytesUsed += record.CanonicalSizeV2()
		if bytesUsed > request.MaxBytes() {
			return journal.RecoveryReadManyResultV2{}, &journal.Error{Kind: journal.ErrorBudgetExceeded, Field: "recovery_snapshot_read_many"}
		}
		records[index] = record
	}
	return journal.NewRecoveryReadManyResultV2(r.attempt.CurrentRevision(), r.asOf, records)
}

func (r *snapshotAttemptReaderV2) ScanV2(ctx context.Context, request journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	if err := ctx.Err(); err != nil {
		return journal.RecoveryPageV2{}, err
	}
	if request.AttemptRevision() != r.attempt {
		return journal.RecoveryPageV2{}, &journal.Error{Kind: journal.ErrorRevisionMoved, Field: "recovery_snapshot_scan"}
	}
	records, ok := r.byScan[request.Target().Kind()]
	if !ok {
		records = nil
	}
	start, err := decodeSnapshotCursor(request.Cursor(), request.Target().Kind(), len(records))
	if err != nil {
		return journal.RecoveryPageV2{}, err
	}
	end := start
	bytesUsed := 0
	for end < len(records) && end-start < request.Limit() {
		size := records[end].CanonicalSizeV2()
		if size > request.MaxBytes()-bytesUsed {
			if end == start {
				return journal.RecoveryPageV2{}, &journal.Error{Kind: journal.ErrorBudgetExceeded, Field: "recovery_snapshot_scan"}
			}
			break
		}
		bytesUsed += size
		end++
	}
	var cursor []byte
	if end < len(records) {
		cursor = encodeSnapshotCursor(request.Target().Kind(), end)
	}
	return journal.NewRecoveryPageV2(r.attempt.CurrentRevision(), r.asOf, records[start:end], cursor, len(cursor) != 0)
}

func encodeSnapshotCursor(kind journal.ScanTargetKindV2, position int) []byte {
	result := make([]byte, 6)
	result[0] = snapshotCursorVersion
	result[1] = snapshotTargetCode(kind)
	binary.BigEndian.PutUint32(result[2:], uint32(position))
	return result
}

func decodeSnapshotCursor(cursor []byte, kind journal.ScanTargetKindV2, length int) (int, error) {
	if len(cursor) == 0 {
		return 0, nil
	}
	if len(cursor) != 6 || cursor[0] != snapshotCursorVersion || cursor[1] != snapshotTargetCode(kind) {
		return 0, &journal.Error{Kind: journal.ErrorStaleCursor, Field: "recovery_snapshot_cursor"}
	}
	position := int(binary.BigEndian.Uint32(cursor[2:]))
	if position <= 0 || position >= length {
		return 0, &journal.Error{Kind: journal.ErrorStaleCursor, Field: "recovery_snapshot_cursor"}
	}
	return position, nil
}

func snapshotTargetCode(kind journal.ScanTargetKindV2) byte {
	switch kind {
	case journal.ScanAllAggregateHeads:
		return 1
	case journal.ScanAllOperationRecords:
		return 2
	case journal.ScanCheckpointSlots:
		return 3
	default:
		return 0
	}
}

var _ ReaderV2 = (*snapshotAttemptReaderV2)(nil)
var _ ReadManyReaderV2 = (*snapshotAttemptReaderV2)(nil)
