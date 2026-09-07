package gapdb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// CaptureRevisionV2 begins a recovery attempt at one durable GapDB
// incarnation/revision coordinate. It deliberately exposes no provider type.
func (a *Adapter) CaptureRevisionV2(ctx context.Context) (journalv2.StoreRevisionV2, error) {
	status, err := a.v2Status(ctx, "capture_revision")
	if err != nil {
		return journalv2.StoreRevisionV2{}, err
	}
	return storeRevisionFromStatus(status)
}

// VerifyRevisionV2 closes a recovery attempt only when the database
// incarnation and current revision are unchanged and R is still durable.
func (a *Adapter) VerifyRevisionV2(ctx context.Context, expected journalv2.StoreRevisionV2) error {
	if _, err := journalv2.NewStoreRevisionV2(expected.DatabaseID(), expected.CurrentRevision(), expected.DurableThroughRevision()); err != nil {
		return recoveryError(journalv2.ErrorInvalidVersion, "store_revision")
	}
	status, err := a.v2Status(ctx, "verify_revision")
	if err != nil {
		return err
	}
	if status.DatabaseID != string(expected.DatabaseID()) || uint64(status.CurrentRevision) != expected.CurrentRevision() ||
		uint64(status.DurableThroughRevision) < expected.CurrentRevision() {
		return recoveryError(journalv2.ErrorRevisionMoved, "store_revision")
	}
	return nil
}

func (a *Adapter) v2Status(ctx context.Context, operation string) (gapdbapi.StatusResult, error) {
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	status, err := a.client.Status(ctx)
	if err != nil {
		return gapdbapi.StatusResult{}, translateRecoveryError(operation, err)
	}
	return status, nil
}

func storeRevisionFromStatus(status gapdbapi.StatusResult) (journalv2.StoreRevisionV2, error) {
	revision, err := journalv2.NewStoreRevisionV2(
		journalv2.DatabaseID(status.DatabaseID),
		uint64(status.CurrentRevision),
		uint64(status.DurableThroughRevision),
	)
	if err != nil {
		return journalv2.StoreRevisionV2{}, recoveryError(journalv2.ErrorMalformed, "provider_status")
	}
	return revision, nil
}

// ReadV2 performs a latest-value Get inside an attempt whose stability is
// proved by capture/final verification. GapDB does not offer an as-of Get.
func (a *Adapter) ReadV2(ctx context.Context, request journalv2.ReadRequestV2) (journalv2.RecoveryRecordV2, error) {
	key, err := v2KeyForRecoveryAddress(request.Address())
	if err != nil || request.AttemptRevision().CurrentRevision() == 0 {
		return journalv2.RecoveryRecordV2{}, recoveryError(journalv2.ErrorInvalidCanonicalData, "read_request")
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	record, err := a.client.Get(ctx, key.String())
	if err != nil {
		return journalv2.RecoveryRecordV2{}, translateRecoveryError("read", err)
	}
	return convertV2RecoveryRecord(record, key, request.Address(), request.AttemptRevision().CurrentRevision())
}

// ReadManyV2 translates one exact semantic read set into GapDB's atomic
// ordered observation. Missing members fail the complete domain read; no
// partial prefix is returned to recovery.
func (a *Adapter) ReadManyV2(ctx context.Context, request journalv2.ReadManyRequestV2) (journalv2.RecoveryReadManyResultV2, error) {
	requests := request.Requests()
	if len(requests) == 0 || request.AttemptRevision().CurrentRevision() == 0 || request.MaxBytes() <= 0 {
		return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorInvalidCanonicalData, "read_many_request")
	}
	keys := make([]string, len(requests))
	keyModels := make([]v2Key, len(requests))
	for index, item := range requests {
		key, err := v2KeyForRecoveryAddress(item.Address())
		if err != nil || item.AttemptRevision() != request.AttemptRevision() {
			return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorInvalidCanonicalData, "read_many_request")
		}
		keys[index], keyModels[index] = key.String(), key
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	observed, err := a.client.ReadMany(ctx, keys)
	if err != nil {
		return journalv2.RecoveryReadManyResultV2{}, translateReadManyRecoveryError(err)
	}
	if uint64(observed.ObservedRevision) != request.AttemptRevision().CurrentRevision() || observed.AsOf.IsZero() || len(observed.Entries) != len(requests) {
		return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorMalformed, "read_many_result")
	}
	asOf := observed.AsOf.UTC().Format(time.RFC3339Nano)
	records := make([]journalv2.RecoveryRecordV2, len(observed.Entries))
	bytesUsed := 0
	for index, entry := range observed.Entries {
		if entry.Key != keys[index] || !entry.Found || entry.Record == nil {
			if entry.Key == keys[index] && !entry.Found && entry.Record == nil {
				return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorNotFound, "read_many")
			}
			return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorMalformed, "read_many_membership")
		}
		record, convertErr := convertV2RecoveryRecord(*entry.Record, keyModels[index], requests[index].Address(), request.AttemptRevision().CurrentRevision())
		if convertErr != nil {
			return journalv2.RecoveryReadManyResultV2{}, convertErr
		}
		if record.ExpiresAt() != "" {
			expires, parseErr := time.Parse(time.RFC3339Nano, record.ExpiresAt())
			if parseErr != nil || !expires.After(observed.AsOf) {
				return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorMalformed, "read_many_expiry")
			}
		}
		bytesUsed += record.CanonicalSizeV2()
		if bytesUsed > request.MaxBytes() {
			return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorBudgetExceeded, "read_many_bytes")
		}
		records[index] = record
	}
	result, err := journalv2.NewRecoveryReadManyResultV2(uint64(observed.ObservedRevision), asOf, records)
	if err != nil {
		return journalv2.RecoveryReadManyResultV2{}, recoveryError(journalv2.ErrorMalformed, "read_many_result")
	}
	return result, nil
}

func translateReadManyRecoveryError(err error) error {
	var providerErr *gapdbapi.Error
	if errors.As(err, &providerErr) && (providerErr.Code == gapdbapi.CodeFrameTooLarge || providerErr.Code == gapdbapi.CodeBatchTooLarge) {
		return recoveryError(journalv2.ErrorBudgetExceeded, "read_many")
	}
	return translateRecoveryError("read_many", err)
}

// ScanV2 translates a provider-pinned prefix page into the foundation
// recovery contract. Every returned page must report the attempt's R.
func (a *Adapter) ScanV2(ctx context.Context, request journalv2.ScanRequestV2) (journalv2.RecoveryPageV2, error) {
	prefix, err := v2PrefixForScanTarget(request.Target())
	if err != nil || request.AttemptRevision().CurrentRevision() == 0 || request.Limit() <= 0 || request.MaxBytes() <= 0 ||
		len(request.Cursor()) > journalv2.MaxRecoveryCursorBytes || !utf8.Valid(request.Cursor()) {
		return journalv2.RecoveryPageV2{}, recoveryError(journalv2.ErrorInvalidCanonicalData, "scan_request")
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	limit := request.Limit()
	var prior *v2RecoveryPage
	for {
		page, callErr := a.client.ScanPrefix(ctx, prefix.String(), limit, string(request.Cursor()))
		if callErr != nil {
			return journalv2.RecoveryPageV2{}, translateRecoveryError("scan", callErr)
		}
		converted, measured, convertErr := convertV2RecoveryPage(page, prefix, request.Target(), request.AttemptRevision(), limit)
		if convertErr != nil {
			return journalv2.RecoveryPageV2{}, convertErr
		}
		if prior != nil && !v2RecoveryPagePrefix(*prior, converted) {
			return journalv2.RecoveryPageV2{}, recoveryError(journalv2.ErrorMalformed, "scan_repage_membership")
		}
		if page.Truncated && len(request.Cursor()) != 0 && bytes.Equal([]byte(page.Cursor), request.Cursor()) {
			return journalv2.RecoveryPageV2{}, recoveryError(journalv2.ErrorStaleCursor, "scan_cursor_progress")
		}
		if measured <= request.MaxBytes() {
			result, resultErr := journalv2.NewRecoveryPageV2(converted.observedRevision, converted.asOf, converted.records, converted.cursor, converted.truncated)
			if resultErr != nil {
				return journalv2.RecoveryPageV2{}, recoveryError(journalv2.ErrorMalformed, "scan_page")
			}
			return result, nil
		}
		if len(page.Records) <= 1 || limit <= 1 {
			return journalv2.RecoveryPageV2{}, recoveryError(journalv2.ErrorBudgetExceeded, "scan_page_bytes")
		}
		prior = &converted
		limit = max(1, len(page.Records)/2)
	}
}

type v2RecoveryPage struct {
	observedRevision uint64
	asOf             string
	records          []journalv2.RecoveryRecordV2
	cursor           []byte
	truncated        bool
}

func convertV2RecoveryPage(page gapdbapi.ScanPage, prefix v2Prefix, target journalv2.ScanTargetV2, attempt journalv2.StoreRevisionV2, limit int) (v2RecoveryPage, int, error) {
	if page.ObservedRevision == 0 || uint64(page.ObservedRevision) != attempt.CurrentRevision() || page.AsOf.IsZero() ||
		len(page.Records) > limit || len(page.Records) > journalv2.MaxRecoveryPageRecords || len(page.Cursor) > journalv2.MaxRecoveryCursorBytes ||
		page.Truncated != (page.Cursor != "") || page.Truncated && len(page.Records) == 0 || !utf8.ValidString(page.Cursor) {
		return v2RecoveryPage{}, 0, recoveryError(journalv2.ErrorMalformed, "scan_page")
	}
	asOf := page.AsOf.UTC().Format(time.RFC3339Nano)
	records := make([]journalv2.RecoveryRecordV2, len(page.Records))
	measured := 0
	previous := ""
	for index, providerRecord := range page.Records {
		if !strings.HasPrefix(providerRecord.Key, prefix.String()) || index > 0 && previous >= providerRecord.Key ||
			providerRecord.Revision == 0 || uint64(providerRecord.Revision) > attempt.CurrentRevision() {
			return v2RecoveryPage{}, 0, recoveryError(journalv2.ErrorMalformed, "scan_record_order")
		}
		key, parseErr := parseV2Key(providerRecord.Key)
		address, addressErr := v2RecoveryAddressForKey(key)
		if parseErr != nil || addressErr != nil || !v2AddressAdmittedByTarget(address, target) {
			return v2RecoveryPage{}, 0, recoveryError(journalv2.ErrorMalformed, "scan_record_address")
		}
		record, recordErr := convertV2RecoveryRecord(providerRecord, key, address, attempt.CurrentRevision())
		if recordErr != nil {
			return v2RecoveryPage{}, 0, recordErr
		}
		if record.ExpiresAt() != "" {
			expires, _ := time.Parse(time.RFC3339Nano, record.ExpiresAt())
			observed, _ := time.Parse(time.RFC3339Nano, asOf)
			if !expires.After(observed) {
				return v2RecoveryPage{}, 0, recoveryError(journalv2.ErrorMalformed, "scan_record_expiry")
			}
		}
		recordBytes := len(providerRecord.Key) + len(providerRecord.Value) + 32
		if providerRecord.ExpiresAt != nil {
			recordBytes += 8
		}
		if recordBytes > journalv2.MaxRecoveryPageBytes-measured {
			measured = journalv2.MaxRecoveryPageBytes + 1
		} else {
			measured += recordBytes
		}
		records[index] = record
		previous = providerRecord.Key
	}
	result := v2RecoveryPage{observedRevision: uint64(page.ObservedRevision), asOf: asOf, records: records, cursor: []byte(page.Cursor), truncated: page.Truncated}
	return result, measured, nil
}

func convertV2RecoveryRecord(record gapdbapi.Record, expected v2Key, address journalv2.RecordAddressV2, maximumRevision uint64) (journalv2.RecoveryRecordV2, error) {
	if record.Key != expected.String() || record.Revision == 0 || uint64(record.Revision) > maximumRevision ||
		len(record.Value) == 0 || len(record.Value) > v2ValueMaximum(expected.Kind()) ||
		expected.Kind() == v2KeyPublicationWitness && record.ExpiresAt != nil {
		return journalv2.RecoveryRecordV2{}, recoveryError(journalv2.ErrorMalformed, "recovery_record")
	}
	parsed, err := parseV2Key(record.Key)
	if err != nil || parsed.String() != expected.String() || parsed.Kind() != expected.Kind() {
		return journalv2.RecoveryRecordV2{}, recoveryError(journalv2.ErrorMalformed, "recovery_record_key")
	}
	expiresAt := ""
	if record.ExpiresAt != nil {
		if record.ExpiresAt.IsZero() || record.ExpiresAt.Year() < 0 || record.ExpiresAt.Year() > 9999 {
			return journalv2.RecoveryRecordV2{}, recoveryError(journalv2.ErrorMalformed, "recovery_record_expiry")
		}
		expiresAt = record.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	converted, err := journalv2.NewRecoveryRecordV2(address, record.Value, uint64(record.Revision), expiresAt)
	if err != nil {
		return journalv2.RecoveryRecordV2{}, recoveryError(journalv2.ErrorMalformed, "recovery_record")
	}
	return converted, nil
}

func v2RecoveryAddressForKey(key v2Key) (journalv2.RecordAddressV2, error) {
	switch key.Kind() {
	case v2KeyAggregateHead:
		return journalv2.NewAggregateHeadAddressV2(key.Coordinate())
	case v2KeyJournalEvent:
		return journalv2.NewJournalEventAddressV2(key.Coordinate(), key.LogicalVersion())
	case v2KeyOperationBinding:
		return journalv2.NewOperationBindingAddressV2(key.OperationID())
	case v2KeyOperationHead:
		return journalv2.NewOperationHeadAddressV2(key.OperationID())
	case v2KeyPublicationWitness:
		return key.PublicationID().WitnessAddressV2()
	case v2KeyCheckpointRecord:
		return journalv2.NewCheckpointRecordAddressV2(key.CheckpointID())
	case v2KeyCheckpointSlot:
		return journalv2.NewCheckpointSlotAddressV2(key.CheckpointSlotID())
	default:
		return journalv2.RecordAddressV2{}, recoveryError(journalv2.ErrorInvalidCanonicalData, "record_address")
	}
}

func v2AddressAdmittedByTarget(address journalv2.RecordAddressV2, target journalv2.ScanTargetV2) bool {
	switch target.Kind() {
	case journalv2.ScanAllAggregateHeads:
		return address.Kind() == journalv2.AddressAggregateHead
	case journalv2.ScanAllOperationRecords:
		return address.Kind() == journalv2.AddressOperationBinding || address.Kind() == journalv2.AddressOperationHead
	case journalv2.ScanCheckpointSlots:
		return address.Kind() == journalv2.AddressCheckpointSlot
	default:
		return false
	}
}

func v2RecoveryPagePrefix(prior, current v2RecoveryPage) bool {
	if prior.observedRevision != current.observedRevision || prior.asOf != current.asOf {
		return false
	}
	before, after := prior.records, current.records
	if len(after) > len(before) {
		return false
	}
	for index := range after {
		if after[index].Address() != before[index].Address() || after[index].RecordRevision() != before[index].RecordRevision() ||
			after[index].ExpiresAt() != before[index].ExpiresAt() || !bytes.Equal(after[index].CanonicalValue(), before[index].CanonicalValue()) {
			return false
		}
	}
	return true
}

func v2ValueMaximum(kind v2KeyKind) int {
	if kind == v2KeyCheckpointRecord {
		return MaxCheckpointValueBytes
	}
	return MaxOrdinaryValueBytes
}

func recoveryError(kind journalv2.ErrorKind, field string) *journalv2.Error {
	return &journalv2.Error{Kind: kind, Field: field}
}

func translateRecoveryError(operation string, err error) error {
	if errors.Is(err, context.Canceled) {
		return recoveryError(journalv2.ErrorCanceled, operation)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return recoveryError(journalv2.ErrorUnavailable, operation)
	}
	var transport *gapdbapi.TransportError
	if errors.As(err, &transport) {
		return recoveryError(journalv2.ErrorUnavailable, operation)
	}
	var providerErr *gapdbapi.Error
	if errors.As(err, &providerErr) {
		switch providerErr.Code {
		case gapdbapi.CodeNotFound:
			return recoveryError(journalv2.ErrorNotFound, operation)
		case gapdbapi.CodeInvalidCursor, gapdbapi.CodeScanStale:
			return recoveryError(journalv2.ErrorStaleCursor, operation)
		case gapdbapi.CodeServerUnavailable, gapdbapi.CodeServerBusy, gapdbapi.CodeServerShuttingDown, gapdbapi.CodeStorageDegraded, gapdbapi.CodeDeadlineExceeded:
			return recoveryError(journalv2.ErrorUnavailable, operation)
		default:
			return recoveryError(journalv2.ErrorMalformed, operation)
		}
	}
	return recoveryError(journalv2.ErrorMalformed, operation)
}
