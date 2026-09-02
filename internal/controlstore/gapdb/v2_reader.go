package gapdb

import (
	"bytes"
	"context"
	"strings"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

// V2Record preserves provider metadata without exporting provider types.
// CanonicalValue and ExpiresAt return copies so restore cannot observe aliases
// into client buffers.
type V2Record struct {
	key       V2Key
	canonical []byte
	revision  uint64
	expiresAt *time.Time
}

func (r V2Record) Key() V2Key             { return r.key }
func (r V2Record) CanonicalValue() []byte { return append([]byte(nil), r.canonical...) }
func (r V2Record) Revision() uint64       { return r.revision }
func (r V2Record) ExpiresAt() *time.Time  { return cloneTime(r.expiresAt) }

type V2ScanRequest struct {
	Prefix   V2Prefix
	Limit    int
	Cursor   string
	MaxBytes int
}

type V2ScanPage struct {
	observedRevision uint64
	asOf             time.Time
	records          []V2Record
	cursor           string
	truncated        bool
}

func (p V2ScanPage) ObservedRevision() uint64 { return p.observedRevision }
func (p V2ScanPage) AsOf() time.Time          { return p.asOf }
func (p V2ScanPage) Cursor() string           { return p.cursor }
func (p V2ScanPage) Truncated() bool          { return p.truncated }
func (p V2ScanPage) Records() []V2Record {
	result := make([]V2Record, len(p.records))
	for index, record := range p.records {
		result[index] = cloneV2Record(record)
	}
	return result
}

func (a *Adapter) ReadV2(ctx context.Context, key V2Key) (V2Record, error) {
	canonical, err := validateV2KeyCapability(key)
	if err != nil {
		return V2Record{}, err
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	record, err := a.client.Get(ctx, canonical.String())
	if err != nil {
		return V2Record{}, translateError("v2_get", "", err)
	}
	return convertV2Record(record, canonical)
}

func (a *Adapter) ScanV2(ctx context.Context, request V2ScanRequest) (V2ScanPage, error) {
	prefix, err := validateV2PrefixCapability(request.Prefix)
	if err != nil {
		return V2ScanPage{}, err
	}
	if request.Limit <= 0 || request.Limit > MaxScanRecords || len(request.Cursor) > MaxCursorBytes {
		return V2ScanPage{}, adapterError("v2_scan", "", OutcomeInvalid, RecoveryFixRequest, "scan count or cursor budget exceeded", nil)
	}
	maximumBytes := request.MaxBytes
	if maximumBytes == 0 {
		maximumBytes = MaxScanBytes
	}
	if maximumBytes <= 0 || maximumBytes > MaxScanBytes {
		return V2ScanPage{}, adapterError("v2_scan", "", OutcomeInvalid, RecoveryFixRequest, "scan byte budget exceeded", nil)
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	limit := request.Limit
	var observed uint64
	var prior *V2ScanPage
	for {
		page, err := a.client.ScanPrefix(ctx, prefix.String(), limit, request.Cursor)
		if err != nil {
			return V2ScanPage{}, translateError("v2_scan", "", err)
		}
		converted, bytes, err := convertV2ScanPage(page, prefix, limit)
		if err != nil {
			return V2ScanPage{}, err
		}
		if observed == 0 {
			observed = converted.observedRevision
		} else if observed != converted.observedRevision {
			return V2ScanPage{}, malformedV2Read("scan revision changed while repaging")
		}
		if prior != nil && !v2PagePrefix(*prior, converted) {
			return V2ScanPage{}, malformedV2Read("scan membership changed while repaging")
		}
		if bytes <= maximumBytes {
			return converted, nil
		}
		if len(page.Records) <= 1 || limit <= 1 {
			return V2ScanPage{}, malformedV2Read("provider record exceeds requested scan budget")
		}
		prior = &converted
		limit = max(1, len(page.Records)/2)
	}
}

func v2PagePrefix(prior, current V2ScanPage) bool {
	if len(current.records) > len(prior.records) {
		return false
	}
	for index, record := range current.records {
		before := prior.records[index]
		if record.key.String() != before.key.String() || record.revision != before.revision || !bytes.Equal(record.canonical, before.canonical) || !equalV2Expiry(record.expiresAt, before.expiresAt) {
			return false
		}
	}
	return true
}

func equalV2Expiry(left, right *time.Time) bool {
	return left == nil && right == nil || left != nil && right != nil && left.Equal(*right)
}

func convertV2ScanPage(page gapdbapi.ScanPage, prefix V2Prefix, limit int) (V2ScanPage, int, error) {
	asOf := page.AsOf.UTC().Round(0)
	if page.ObservedRevision == 0 || !validV2Instant(asOf) || len(page.Records) > limit || len(page.Records) > MaxScanRecords ||
		len(page.Cursor) > MaxCursorBytes || page.Truncated != (page.Cursor != "") {
		return V2ScanPage{}, 0, malformedV2Read("provider returned malformed scan metadata")
	}
	result := V2ScanPage{observedRevision: uint64(page.ObservedRevision), asOf: asOf, cursor: page.Cursor, truncated: page.Truncated, records: make([]V2Record, len(page.Records))}
	bytes := 0
	previous := ""
	for index, record := range page.Records {
		if !strings.HasPrefix(record.Key, prefix.String()) || index > 0 && previous >= record.Key || record.Revision > page.ObservedRevision {
			return V2ScanPage{}, 0, malformedV2Read("provider returned foreign, unsorted, or future scan record")
		}
		key, err := ParseV2Key(record.Key)
		if err != nil {
			return V2ScanPage{}, 0, malformedV2Read("provider returned noncanonical scan key")
		}
		converted, err := convertV2Record(record, key)
		if err != nil {
			return V2ScanPage{}, 0, err
		}
		if converted.expiresAt != nil && !converted.expiresAt.After(asOf) {
			return V2ScanPage{}, 0, malformedV2Read("provider returned expired scan record")
		}
		recordBytes := len(record.Key) + len(record.Value) + 32
		if record.ExpiresAt != nil {
			recordBytes += 8
		}
		if recordBytes > MaxScanBytes-bytes {
			bytes = MaxScanBytes + 1
		} else {
			bytes += recordBytes
		}
		result.records[index] = converted
		previous = record.Key
	}
	return result, bytes, nil
}

func convertV2Record(record gapdbapi.Record, expected V2Key) (V2Record, error) {
	if record.Key != expected.String() || record.Revision == 0 || len(record.Value) > v2ValueMaximum(expected.Kind()) {
		return V2Record{}, malformedV2Read("provider returned invalid record identity, revision, or value")
	}
	parsed, err := ParseV2Key(record.Key)
	if err != nil || parsed.String() != expected.String() || parsed.Kind() != expected.Kind() {
		return V2Record{}, malformedV2Read("provider returned noncanonical record key")
	}
	var expiry *time.Time
	if record.ExpiresAt != nil {
		value := record.ExpiresAt.UTC().Round(0)
		if !validV2Instant(value) {
			return V2Record{}, malformedV2Read("provider returned impossible expiry metadata")
		}
		expiry = &value
	}
	return V2Record{key: parsed, canonical: append([]byte(nil), record.Value...), revision: uint64(record.Revision), expiresAt: expiry}, nil
}

func v2ValueMaximum(kind V2KeyKind) int {
	if kind == V2KeyCheckpointRecord {
		return MaxCheckpointValueBytes
	}
	return MaxOrdinaryValueBytes
}

func validV2Instant(value time.Time) bool {
	return !value.IsZero() && value.Year() >= 1 && value.Year() <= 9999
}

func cloneV2Record(record V2Record) V2Record {
	record.canonical = append([]byte(nil), record.canonical...)
	record.expiresAt = cloneTime(record.expiresAt)
	return record
}

func validateV2KeyCapability(key V2Key) (V2Key, error) {
	parsed, err := ParseV2Key(key.String())
	if err != nil || parsed.kind != key.kind {
		return V2Key{}, adapterError("v2_get", "", OutcomeInvalid, RecoveryFixRequest, "invalid canonical v2 key capability", err)
	}
	return parsed, nil
}

func validateV2PrefixCapability(prefix V2Prefix) (V2Prefix, error) {
	parsed, err := ParseV2Prefix(prefix.String())
	if err != nil || parsed.kind != prefix.kind {
		return V2Prefix{}, adapterError("v2_scan", "", OutcomeInvalid, RecoveryFixRequest, "invalid canonical v2 prefix capability", err)
	}
	return parsed, nil
}

func malformedV2Read(detail string) *Error {
	return adapterError("v2_read", "", OutcomeMalformedResponse, RecoveryOperator, detail, nil)
}

// V2Notification contains only a wake-up coordinate. Values from watch are
// deliberately discarded; consumers must reconcile with ReadV2 or ScanV2.
type V2Notification struct {
	Revision uint64
	Order    uint32
	Kind     ChangeKind
	Key      V2Key
}

type V2NotificationWatch interface {
	RegistrationRevision() uint64
	Next(context.Context) (V2Notification, error)
	Close() error
}

func (a *Adapter) WatchV2(ctx context.Context, prefix V2Prefix, after uint64) (V2NotificationWatch, error) {
	canonical, err := validateV2PrefixCapability(prefix)
	if err != nil {
		return nil, err
	}
	ctx, cancel := a.requestContext(ctx)
	watch, err := a.client.Watch(ctx, canonical.String(), gapdbapi.Revision(after))
	if err != nil {
		cancel()
		return nil, translateError("v2_watch", "", err)
	}
	if watch == nil || uint64(watch.Registration()) < after {
		if watch != nil {
			_ = watch.Close()
		}
		cancel()
		return nil, malformedV2Read("provider returned invalid watch registration")
	}
	return &v2NotificationWatch{watch: watch, cancel: cancel, prefix: canonical}, nil
}

type v2NotificationWatch struct {
	watch  providerWatch
	cancel context.CancelFunc
	prefix V2Prefix
}

func (w *v2NotificationWatch) RegistrationRevision() uint64 { return uint64(w.watch.Registration()) }

func (w *v2NotificationWatch) Next(ctx context.Context) (V2Notification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	event, err := w.watch.Next(ctx)
	if err != nil {
		return V2Notification{}, translateError("v2_watch_next", "", err)
	}
	key, parseErr := ParseV2Key(event.Key)
	if event.Revision == 0 || parseErr != nil || !strings.HasPrefix(event.Key, w.prefix.String()) {
		return V2Notification{}, malformedV2Read("provider returned malformed watch notification")
	}
	switch event.Kind {
	case gapdbapi.ChangePut:
		if event.Record == nil || event.Record.Key != event.Key || event.Record.Revision != event.Revision {
			return V2Notification{}, malformedV2Read("provider returned inconsistent put notification")
		}
	case gapdbapi.ChangeDelete, gapdbapi.ChangeExpire:
		if event.Record != nil {
			return V2Notification{}, malformedV2Read("provider returned value on removal notification")
		}
	default:
		return V2Notification{}, malformedV2Read("provider returned unknown watch notification")
	}
	return V2Notification{Revision: uint64(event.Revision), Order: event.Order, Kind: ChangeKind(event.Kind), Key: key}, nil
}

func (w *v2NotificationWatch) Close() error {
	if w == nil {
		return nil
	}
	err := w.watch.Close()
	w.cancel()
	return err
}
