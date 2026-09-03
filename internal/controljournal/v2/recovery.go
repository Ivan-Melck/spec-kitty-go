package v2

const (
	MaxRecoveryPageRecords = 500
	MaxRecoveryPageBytes   = 8 * 1024 * 1024
	MaxRecoveryCursorBytes = 16 * 1024
)

// DatabaseID is the durable store incarnation identity. A revision is only
// meaningful together with this identity.
type DatabaseID string

type StoreRevisionV2 struct {
	databaseID      DatabaseID
	currentRevision uint64
	durableThrough  uint64
}

func NewStoreRevisionV2(databaseID DatabaseID, currentRevision, durableThrough uint64) (StoreRevisionV2, error) {
	if validateIdentity("database_id", string(databaseID)) != nil {
		return StoreRevisionV2{}, invalid(ErrorInvalidIdentity, "database_id")
	}
	if currentRevision == 0 || durableThrough < currentRevision {
		return StoreRevisionV2{}, invalid(ErrorInvalidVersion, "store_revision")
	}
	return StoreRevisionV2{databaseID: databaseID, currentRevision: currentRevision, durableThrough: durableThrough}, nil
}

func (r StoreRevisionV2) DatabaseID() DatabaseID         { return r.databaseID }
func (r StoreRevisionV2) CurrentRevision() uint64        { return r.currentRevision }
func (r StoreRevisionV2) DurableThroughRevision() uint64 { return r.durableThrough }

type RecordAddressKindV2 string

const (
	AddressAggregateHead    RecordAddressKindV2 = "aggregate_head"
	AddressJournalEvent     RecordAddressKindV2 = "journal_event"
	AddressOperationBinding RecordAddressKindV2 = "operation_binding"
	AddressOperationHead    RecordAddressKindV2 = "operation_head"
	AddressCheckpointRecord RecordAddressKindV2 = "checkpoint_record"
	AddressCheckpointSlot   RecordAddressKindV2 = "checkpoint_slot"
)

// RecordAddressV2 is a semantic address. It has no raw key constructor and
// carries no provider namespace, escaping, or prefix detail.
type RecordAddressV2 struct {
	kind             RecordAddressKindV2
	coordinate       AggregateCoordinate
	logicalVersion   LogicalVersion
	operationID      OperationID
	checkpointID     CheckpointID
	checkpointSlotID CheckpointSlotID
}

func NewAggregateHeadAddressV2(coordinate AggregateCoordinate) (RecordAddressV2, error) {
	if _, err := NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope); err != nil || coordinate.Kind == AggregateKind("operation") {
		return RecordAddressV2{}, invalid(ErrorInvalidCanonicalData, "record_address")
	}
	return RecordAddressV2{kind: AddressAggregateHead, coordinate: coordinate}, nil
}

func NewJournalEventAddressV2(coordinate AggregateCoordinate, version LogicalVersion) (RecordAddressV2, error) {
	if _, err := NewAggregateCoordinate(coordinate.Kind, coordinate.ID, coordinate.Scope); err != nil || version == 0 {
		return RecordAddressV2{}, invalid(ErrorInvalidCanonicalData, "record_address")
	}
	return RecordAddressV2{kind: AddressJournalEvent, coordinate: coordinate, logicalVersion: version}, nil
}

func NewOperationBindingAddressV2(operationID OperationID) (RecordAddressV2, error) {
	if validateIdentity("operation_id", string(operationID)) != nil {
		return RecordAddressV2{}, invalid(ErrorInvalidIdentity, "operation_id")
	}
	return RecordAddressV2{kind: AddressOperationBinding, operationID: operationID}, nil
}

func NewOperationHeadAddressV2(operationID OperationID) (RecordAddressV2, error) {
	if validateIdentity("operation_id", string(operationID)) != nil {
		return RecordAddressV2{}, invalid(ErrorInvalidIdentity, "operation_id")
	}
	return RecordAddressV2{kind: AddressOperationHead, operationID: operationID}, nil
}

func NewCheckpointRecordAddressV2(checkpointID CheckpointID) (RecordAddressV2, error) {
	if validateIdentity("checkpoint_id", string(checkpointID)) != nil {
		return RecordAddressV2{}, invalid(ErrorInvalidIdentity, "checkpoint_id")
	}
	return RecordAddressV2{kind: AddressCheckpointRecord, checkpointID: checkpointID}, nil
}

func NewCheckpointSlotAddressV2(slotID CheckpointSlotID) (RecordAddressV2, error) {
	if validateIdentity("checkpoint_slot_id", string(slotID)) != nil {
		return RecordAddressV2{}, invalid(ErrorInvalidIdentity, "checkpoint_slot_id")
	}
	return RecordAddressV2{kind: AddressCheckpointSlot, checkpointSlotID: slotID}, nil
}

func (a RecordAddressV2) Kind() RecordAddressKindV2          { return a.kind }
func (a RecordAddressV2) Coordinate() AggregateCoordinate    { return a.coordinate }
func (a RecordAddressV2) LogicalVersion() LogicalVersion     { return a.logicalVersion }
func (a RecordAddressV2) OperationID() OperationID           { return a.operationID }
func (a RecordAddressV2) CheckpointID() CheckpointID         { return a.checkpointID }
func (a RecordAddressV2) CheckpointSlotID() CheckpointSlotID { return a.checkpointSlotID }

func (a RecordAddressV2) validate() error {
	switch a.kind {
	case AddressAggregateHead:
		_, err := NewAggregateHeadAddressV2(a.coordinate)
		return err
	case AddressJournalEvent:
		_, err := NewJournalEventAddressV2(a.coordinate, a.logicalVersion)
		return err
	case AddressOperationBinding:
		_, err := NewOperationBindingAddressV2(a.operationID)
		return err
	case AddressOperationHead:
		_, err := NewOperationHeadAddressV2(a.operationID)
		return err
	case AddressCheckpointRecord:
		_, err := NewCheckpointRecordAddressV2(a.checkpointID)
		return err
	case AddressCheckpointSlot:
		_, err := NewCheckpointSlotAddressV2(a.checkpointSlotID)
		return err
	default:
		return invalid(ErrorInvalidCanonicalData, "record_address")
	}
}

type ScanTargetKindV2 string

const (
	ScanAllAggregateHeads   ScanTargetKindV2 = "all_aggregate_heads"
	ScanAllOperationRecords ScanTargetKindV2 = "all_operation_records"
	ScanCheckpointSlots     ScanTargetKindV2 = "checkpoint_slots"
)

type ScanTargetV2 struct{ kind ScanTargetKindV2 }

func AggregateHeadScanTargetV2() ScanTargetV2   { return ScanTargetV2{kind: ScanAllAggregateHeads} }
func OperationRecordScanTargetV2() ScanTargetV2 { return ScanTargetV2{kind: ScanAllOperationRecords} }
func CheckpointSlotScanTargetV2() ScanTargetV2  { return ScanTargetV2{kind: ScanCheckpointSlots} }
func (t ScanTargetV2) Kind() ScanTargetKindV2   { return t.kind }
func (t ScanTargetV2) validate() error {
	switch t.kind {
	case ScanAllAggregateHeads, ScanAllOperationRecords, ScanCheckpointSlots:
		return nil
	default:
		return invalid(ErrorInvalidCanonicalData, "scan_target")
	}
}

type ReadRequestV2 struct {
	address RecordAddressV2
	attempt StoreRevisionV2
}

func NewReadRequestV2(address RecordAddressV2, attempt StoreRevisionV2) (ReadRequestV2, error) {
	if err := address.validate(); err != nil {
		return ReadRequestV2{}, err
	}
	if _, err := NewStoreRevisionV2(attempt.databaseID, attempt.currentRevision, attempt.durableThrough); err != nil {
		return ReadRequestV2{}, err
	}
	return ReadRequestV2{address: address, attempt: attempt}, nil
}

func (r ReadRequestV2) Address() RecordAddressV2         { return r.address }
func (r ReadRequestV2) AttemptRevision() StoreRevisionV2 { return r.attempt }

type ScanRequestV2 struct {
	target   ScanTargetV2
	attempt  StoreRevisionV2
	cursor   []byte
	limit    int
	maxBytes int
}

func NewScanRequestV2(target ScanTargetV2, attempt StoreRevisionV2, cursor []byte, limit, maxBytes int) (ScanRequestV2, error) {
	if err := target.validate(); err != nil {
		return ScanRequestV2{}, err
	}
	if _, err := NewStoreRevisionV2(attempt.databaseID, attempt.currentRevision, attempt.durableThrough); err != nil {
		return ScanRequestV2{}, err
	}
	if len(cursor) > MaxRecoveryCursorBytes || limit <= 0 || limit > MaxRecoveryPageRecords || maxBytes <= 0 || maxBytes > MaxRecoveryPageBytes {
		return ScanRequestV2{}, invalid(ErrorBudgetExceeded, "scan_request")
	}
	return ScanRequestV2{target: target, attempt: attempt, cursor: append([]byte(nil), cursor...), limit: limit, maxBytes: maxBytes}, nil
}

func (r ScanRequestV2) Target() ScanTargetV2             { return r.target }
func (r ScanRequestV2) AttemptRevision() StoreRevisionV2 { return r.attempt }
func (r ScanRequestV2) Cursor() []byte                   { return append([]byte(nil), r.cursor...) }
func (r ScanRequestV2) Limit() int                       { return r.limit }
func (r ScanRequestV2) MaxBytes() int                    { return r.maxBytes }

type RecoveryRecordV2 struct {
	address   RecordAddressV2
	canonical []byte
	revision  uint64
	expiresAt string
}

func NewRecoveryRecordV2(address RecordAddressV2, canonical []byte, revision uint64, expiresAt string) (RecoveryRecordV2, error) {
	if err := address.validate(); err != nil {
		return RecoveryRecordV2{}, err
	}
	if revision == 0 || len(canonical) == 0 || len(canonical) > MaxCheckpointValueBytes {
		return RecoveryRecordV2{}, invalid(ErrorBudgetExceeded, "recovery_record")
	}
	value, err := CanonicalObject(canonical)
	if err != nil || len(value) != len(canonical) {
		return RecoveryRecordV2{}, invalid(ErrorMalformed, "recovery_record")
	}
	if expiresAt != "" && !canonicalUTCInstant(expiresAt) {
		return RecoveryRecordV2{}, invalid(ErrorInvalidCanonicalData, "recovery_record_expiry")
	}
	return RecoveryRecordV2{address: address, canonical: append([]byte(nil), value...), revision: revision, expiresAt: expiresAt}, nil
}

func (r RecoveryRecordV2) Address() RecordAddressV2 { return r.address }
func (r RecoveryRecordV2) CanonicalValue() []byte   { return append([]byte(nil), r.canonical...) }
func (r RecoveryRecordV2) RecordRevision() uint64   { return r.revision }
func (r RecoveryRecordV2) ExpiresAt() string        { return r.expiresAt }

type RecoveryPageV2 struct {
	observedRevision uint64
	asOf             string
	records          []RecoveryRecordV2
	cursor           []byte
	truncated        bool
}

func NewRecoveryPageV2(observedRevision uint64, asOf string, records []RecoveryRecordV2, cursor []byte, truncated bool) (RecoveryPageV2, error) {
	if observedRevision == 0 || !canonicalUTCInstant(asOf) || len(records) > MaxRecoveryPageRecords || len(cursor) > MaxRecoveryCursorBytes {
		return RecoveryPageV2{}, invalid(ErrorBudgetExceeded, "recovery_page")
	}
	if truncated != (len(cursor) != 0) {
		return RecoveryPageV2{}, invalid(ErrorMalformed, "recovery_page_cursor")
	}
	total := 0
	copied := make([]RecoveryRecordV2, len(records))
	for index, record := range records {
		if err := record.address.validate(); err != nil || record.revision == 0 || len(record.canonical) == 0 {
			return RecoveryPageV2{}, invalid(ErrorMalformed, "recovery_page_record")
		}
		total += len(record.canonical)
		if total > MaxRecoveryPageBytes {
			return RecoveryPageV2{}, invalid(ErrorBudgetExceeded, "recovery_page_bytes")
		}
		copied[index] = cloneRecoveryRecord(record)
	}
	return RecoveryPageV2{observedRevision: observedRevision, asOf: asOf, records: copied, cursor: append([]byte(nil), cursor...), truncated: truncated}, nil
}

// canonicalUTCInstant admits exactly the UTC representation produced by
// RFC3339Nano. The package's pure-foundation import boundary excludes clock
// packages, so the grammar below independently validates the calendar and
// RFC3339Nano's unique non-zero fractional representation, including year zero.
func canonicalUTCInstant(value string) bool {
	if len(value) == 20 {
		return value[19] == 'Z' && canonicalUTCRecoverySecond(value[:19])
	}
	if len(value) < 22 || len(value) > 30 || value[19] != '.' || value[len(value)-1] != 'Z' || !canonicalUTCRecoverySecond(value[:19]) {
		return false
	}
	fraction := value[20 : len(value)-1]
	for index := range fraction {
		if fraction[index] < '0' || fraction[index] > '9' {
			return false
		}
	}
	// RFC3339Nano removes trailing zeroes. A final non-zero digit therefore
	// proves both a non-zero fraction and the absence of an alternate spelling.
	return fraction[len(fraction)-1] != '0'
}

func canonicalUTCRecoverySecond(value string) bool {
	if len(value) != 19 || value[4] != '-' || value[7] != '-' || value[10] != 'T' || value[13] != ':' || value[16] != ':' {
		return false
	}
	for _, index := range [...]int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	digit := func(index int) int { return int(value[index] - '0') }
	number := func(a, b int) int { return digit(a)*10 + digit(b) }
	year := digit(0)*1000 + digit(1)*100 + digit(2)*10 + digit(3)
	month, day := number(5, 6), number(8, 9)
	hour, minute, second := number(11, 12), number(14, 15), number(17, 18)
	if month < 1 || month > 12 || hour > 23 || minute > 59 || second > 59 {
		return false
	}
	days := [...]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	if month == 2 && (year%400 == 0 || year%4 == 0 && year%100 != 0) {
		days[2] = 29
	}
	return day >= 1 && day <= days[month]
}

func cloneRecoveryRecord(record RecoveryRecordV2) RecoveryRecordV2 {
	copy := record
	copy.canonical = append([]byte(nil), record.canonical...)
	return copy
}

func (p RecoveryPageV2) ObservedRevision() uint64 { return p.observedRevision }
func (p RecoveryPageV2) AsOf() string             { return p.asOf }
func (p RecoveryPageV2) Records() []RecoveryRecordV2 {
	result := make([]RecoveryRecordV2, len(p.records))
	for index := range p.records {
		result[index] = cloneRecoveryRecord(p.records[index])
	}
	return result
}
func (p RecoveryPageV2) Cursor() []byte  { return append([]byte(nil), p.cursor...) }
func (p RecoveryPageV2) Truncated() bool { return p.truncated }

// ValidateStableRecoveryAttemptV2 validates the enforceable GapDB protocol. It
// does not imply or request as-of Get semantics: equal capture/final store
// coordinates prove that latest-value reads were coherent during the attempt.
func ValidateStableRecoveryAttemptV2(capture StoreRevisionV2, pages []RecoveryPageV2, records []RecoveryRecordV2, final StoreRevisionV2) error {
	if _, err := NewStoreRevisionV2(capture.databaseID, capture.currentRevision, capture.durableThrough); err != nil {
		return err
	}
	if _, err := NewStoreRevisionV2(final.databaseID, final.currentRevision, final.durableThrough); err != nil {
		return err
	}
	if capture.databaseID != final.databaseID || capture.currentRevision != final.currentRevision {
		return invalid(ErrorRevisionMoved, "recovery_attempt")
	}
	validateRecord := func(record RecoveryRecordV2) error {
		if record.revision == 0 || record.revision > capture.currentRevision {
			return invalid(ErrorRevisionMoved, "record_revision")
		}
		return record.address.validate()
	}
	for _, page := range pages {
		if page.observedRevision != capture.currentRevision {
			return invalid(ErrorRevisionMoved, "scan_revision")
		}
		for _, record := range page.records {
			if err := validateRecord(record); err != nil {
				return err
			}
		}
	}
	for _, record := range records {
		if err := validateRecord(record); err != nil {
			return err
		}
	}
	return nil
}
