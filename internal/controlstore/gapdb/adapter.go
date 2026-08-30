package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

type publicClient interface {
	Get(context.Context, string) (gapdbapi.Record, error)
	Put(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	PutIfAbsent(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	CompareAndSwap(context.Context, string, gapdbapi.Revision, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	DeleteIfRevision(context.Context, string, gapdbapi.Revision, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	AtomicBatch(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error)
	ScanPrefix(context.Context, string, int, string) (gapdbapi.ScanPage, error)
	Watch(context.Context, string, gapdbapi.Revision) (*gapdbapi.Watch, error)
	Status(context.Context) (gapdbapi.StatusResult, error)
	Health(context.Context) (gapdbapi.HealthResult, error)
	DescribeConfig(context.Context) (gapdbapi.ConfigResult, error)
}

var _ publicClient = (*gapdbapi.Client)(nil)
var _ Port = (*Adapter)(nil)

type providerWatch interface {
	Registration() gapdbapi.Revision
	Next(context.Context) (gapdbapi.ChangeEvent, error)
	Close() error
}

type provider interface {
	Get(context.Context, string) (gapdbapi.Record, error)
	Put(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	PutIfAbsent(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	CompareAndSwap(context.Context, string, gapdbapi.Revision, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	DeleteIfRevision(context.Context, string, gapdbapi.Revision, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	AtomicBatch(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error)
	ScanPrefix(context.Context, string, int, string) (gapdbapi.ScanPage, error)
	Watch(context.Context, string, gapdbapi.Revision) (providerWatch, error)
	Status(context.Context) (gapdbapi.StatusResult, error)
	Health(context.Context) (gapdbapi.HealthResult, error)
	DescribeConfig(context.Context) (gapdbapi.ConfigResult, error)
}

// publicProvider is the only production bridge to the admitted public client.
// The internal provider seam exists solely to inject dishonest observations in
// adapter tests without embedding a server.
type publicProvider struct{ client publicClient }

var _ provider = (*publicProvider)(nil)

func (p *publicProvider) Get(ctx context.Context, key string) (gapdbapi.Record, error) {
	return p.client.Get(ctx, key)
}
func (p *publicProvider) Put(ctx context.Context, key string, value []byte, expiry *time.Time, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	return p.client.Put(ctx, key, value, expiry, ack)
}
func (p *publicProvider) PutIfAbsent(ctx context.Context, key string, value []byte, expiry *time.Time, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	return p.client.PutIfAbsent(ctx, key, value, expiry, ack)
}
func (p *publicProvider) CompareAndSwap(ctx context.Context, key string, revision gapdbapi.Revision, value []byte, expiry *time.Time, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	return p.client.CompareAndSwap(ctx, key, revision, value, expiry, ack)
}
func (p *publicProvider) DeleteIfRevision(ctx context.Context, key string, revision gapdbapi.Revision, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	return p.client.DeleteIfRevision(ctx, key, revision, ack)
}
func (p *publicProvider) AtomicBatch(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
	return p.client.AtomicBatch(ctx, batch)
}
func (p *publicProvider) ScanPrefix(ctx context.Context, prefix string, limit int, cursor string) (gapdbapi.ScanPage, error) {
	return p.client.ScanPrefix(ctx, prefix, limit, cursor)
}
func (p *publicProvider) Watch(ctx context.Context, prefix string, revision gapdbapi.Revision) (providerWatch, error) {
	watch, err := p.client.Watch(ctx, prefix, revision)
	if err != nil {
		return nil, err
	}
	return &publicWatch{watch: watch}, nil
}
func (p *publicProvider) Status(ctx context.Context) (gapdbapi.StatusResult, error) {
	return p.client.Status(ctx)
}
func (p *publicProvider) Health(ctx context.Context) (gapdbapi.HealthResult, error) {
	return p.client.Health(ctx)
}
func (p *publicProvider) DescribeConfig(ctx context.Context) (gapdbapi.ConfigResult, error) {
	return p.client.DescribeConfig(ctx)
}

type publicWatch struct{ watch *gapdbapi.Watch }

func (w *publicWatch) Registration() gapdbapi.Revision { return w.watch.RegistrationRevision }
func (w *publicWatch) Next(ctx context.Context) (gapdbapi.ChangeEvent, error) {
	select {
	case <-ctx.Done():
		return gapdbapi.ChangeEvent{}, ctx.Err()
	case event, open := <-w.watch.Events:
		if !open {
			return gapdbapi.ChangeEvent{}, &gapdbapi.TransportError{Operation: "watch", Cause: errors.New("watch event stream closed")}
		}
		return event, nil
	case ended, open := <-w.watch.Ended:
		if open && ended.Error != nil {
			return gapdbapi.ChangeEvent{}, ended.Error
		}
		return gapdbapi.ChangeEvent{}, &gapdbapi.TransportError{Operation: "watch", Cause: errors.New("watch ended")}
	}
}
func (w *publicWatch) Close() error {
	w.watch.Close()
	return nil
}

type Adapter struct {
	client     provider
	endpointID string
	timeout    time.Duration
}

func New(client *gapdbapi.Client, endpoint string) (*Adapter, error) {
	if client == nil {
		return nil, adapterError("new", "", OutcomeCapabilityAbsent, RecoveryOperator, "public client capability is absent", nil)
	}
	return newAdapter(&publicProvider{client: client}, endpoint, DefaultRequestTimeout)
}

func newAdapter(client provider, endpoint string, timeout time.Duration) (*Adapter, error) {
	if client == nil {
		return nil, adapterError("new", "", OutcomeCapabilityAbsent, RecoveryOperator, "public client capability is absent", nil)
	}
	if endpoint == "" {
		return nil, adapterError("new", "", OutcomeInvalid, RecoveryFixRequest, "endpoint identity is required", nil)
	}
	if timeout <= 0 {
		return nil, adapterError("new", "", OutcomeInvalid, RecoveryFixRequest, "request timeout must be positive", nil)
	}
	digest := sha256.Sum256([]byte(endpoint))
	return &Adapter{client: client, endpointID: hex.EncodeToString(digest[:]), timeout: timeout}, nil
}

func (a *Adapter) Get(ctx context.Context, key string) (Record, error) {
	if err := validateKey(key); err != nil {
		return Record{}, adapterError("get", "", OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	record, err := a.client.Get(ctx, key)
	if err != nil {
		return Record{}, translateError("get", "", err)
	}
	converted, err := convertRecord(record, key)
	if err != nil {
		return Record{}, adapterError("get", "", OutcomeMalformedResponse, RecoveryOperator, "provider returned an invalid record", err)
	}
	return converted, nil
}

func (a *Adapter) ConditionalMutate(ctx context.Context, operationID string, mutation Mutation, ack AckMode) (MutationResult, error) {
	if err := validateOperationID(operationID); err != nil {
		return MutationResult{}, adapterError("conditional_mutate", operationID, OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	if err := validateMutation(mutation, MaxOrdinaryValueBytes); err != nil {
		return MutationResult{}, adapterError("conditional_mutate", operationID, OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	providerAck, err := providerAck(ack)
	if err != nil {
		return MutationResult{}, adapterError("conditional_mutate", operationID, OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	ctx = gapdbapi.WithRequestID(ctx, operationID)
	var result gapdbapi.MutationResult
	switch mutation.Kind {
	case MutationPut:
		switch mutation.Condition.Kind {
		case ConditionAny:
			result, err = a.client.Put(ctx, mutation.Key, mutation.Value, cloneTime(mutation.ExpiresAt), providerAck)
		case ConditionAbsent:
			result, err = a.client.PutIfAbsent(ctx, mutation.Key, mutation.Value, cloneTime(mutation.ExpiresAt), providerAck)
		case ConditionRevision:
			result, err = a.client.CompareAndSwap(ctx, mutation.Key, gapdbapi.Revision(mutation.Condition.ExpectedRevision), mutation.Value, cloneTime(mutation.ExpiresAt), providerAck)
		}
	case MutationDelete:
		result, err = a.client.DeleteIfRevision(ctx, mutation.Key, gapdbapi.Revision(mutation.Condition.ExpectedRevision), providerAck)
	}
	if err != nil {
		return MutationResult{}, translateError("conditional_mutate", operationID, err)
	}
	return convertMutationResult("conditional_mutate", operationID, result, 1, providerAck)
}

func (a *Adapter) AtomicBatch(ctx context.Context, batch ProductBatch) (MutationResult, error) {
	providerBatch, rawBytes, err := encodeProductBatch(batch)
	if err != nil {
		return MutationResult{}, adapterError("atomic_batch", batch.OperationID, OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	if rawBytes > MaxRawBatchBytes {
		return MutationResult{}, adapterError("atomic_batch", batch.OperationID, OutcomeInvalid, RecoveryFixRequest, "raw batch byte budget exceeded", nil)
	}
	if err := validateMutationCount(len(providerBatch.Mutations)); err != nil {
		return MutationResult{}, adapterError("atomic_batch", batch.OperationID, OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	if providerBatch.Ack != gapdbapi.AckDurable {
		return MutationResult{}, adapterError("atomic_batch", batch.OperationID, OutcomeInvalid, RecoveryFixRequest, "atomic batch requires durable acknowledgement", nil)
	}
	wire := struct {
		SchemaVersion uint64         `json:"schema_version"`
		RequestID     string         `json:"request_id"`
		Operation     string         `json:"operation"`
		Arguments     gapdbapi.Batch `json:"arguments"`
	}{AdmittedWireVersion, batch.OperationID, "atomic_batch", providerBatch}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return MutationResult{}, adapterError("atomic_batch", batch.OperationID, OutcomeInvalid, RecoveryFixRequest, "wire request cannot be encoded", err)
	}
	if err := validateWireFrameSize(len(encoded)); err != nil {
		return MutationResult{}, adapterError("atomic_batch", batch.OperationID, OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	ctx = gapdbapi.WithRequestID(ctx, batch.OperationID)
	result, err := a.client.AtomicBatch(ctx, providerBatch)
	if err != nil {
		return MutationResult{}, translateError("atomic_batch", batch.OperationID, err)
	}
	return convertMutationResult("atomic_batch", batch.OperationID, result, len(providerBatch.Mutations), gapdbapi.AckDurable)
}

func (a *Adapter) Scan(ctx context.Context, request ScanRequest) (ScanPage, error) {
	if err := validateKey(request.Prefix); err != nil {
		return ScanPage{}, adapterError("scan", "", OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	if request.Limit <= 0 || request.Limit > MaxScanRecords {
		return ScanPage{}, adapterError("scan", "", OutcomeInvalid, RecoveryFixRequest, "scan record limit is outside the product budget", nil)
	}
	if len(request.Cursor) > MaxCursorBytes {
		return ScanPage{}, adapterError("scan", "", OutcomeInvalid, RecoveryFixRequest, "scan cursor exceeds the product budget", nil)
	}
	maximumBytes := request.MaxBytes
	if maximumBytes == 0 {
		maximumBytes = MaxScanBytes
	}
	if maximumBytes <= 0 || maximumBytes > MaxScanBytes {
		return ScanPage{}, adapterError("scan", "", OutcomeInvalid, RecoveryFixRequest, "scan byte limit is outside the product budget", nil)
	}
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	limit := request.Limit
	for {
		page, err := a.client.ScanPrefix(ctx, request.Prefix, limit, request.Cursor)
		if err != nil {
			return ScanPage{}, translateError("scan", "", err)
		}
		converted, pageBytes, err := convertScanPage(page, request.Prefix, limit)
		if err != nil {
			return ScanPage{}, err
		}
		if pageBytes <= maximumBytes {
			return converted, nil
		}
		if len(page.Records) <= 1 || limit <= 1 {
			return ScanPage{}, adapterError("scan", "", OutcomeMalformedResponse, RecoveryOperator, "provider record exceeds the requested scan byte budget", nil)
		}
		// Reissue the same revision-pinned cursor with a smaller record count.
		// This returns a provider cursor for exactly the page we expose, so local
		// byte truncation can never skip records.
		limit = max(1, len(page.Records)/2)
	}
}

func convertScanPage(page gapdbapi.ScanPage, prefix string, limit int) (ScanPage, int, error) {
	converted := ScanPage{ObservedRevision: uint64(page.ObservedRevision), AsOf: page.AsOf.UTC(), Cursor: page.Cursor, Truncated: page.Truncated, Records: make([]Record, len(page.Records))}
	if converted.AsOf.IsZero() || len(converted.Records) > limit || converted.Truncated != (converted.Cursor != "") {
		return ScanPage{}, 0, adapterError("scan", "", OutcomeMalformedResponse, RecoveryOperator, "provider returned malformed scan metadata", nil)
	}
	pageBytes := 0
	previous := ""
	for index, record := range page.Records {
		if !strings.HasPrefix(record.Key, prefix) || index > 0 && previous >= record.Key {
			return ScanPage{}, 0, adapterError("scan", "", OutcomeMalformedResponse, RecoveryOperator, "provider returned unsorted or foreign scan records", nil)
		}
		convertedRecord, err := convertRecord(record, record.Key)
		if err != nil {
			return ScanPage{}, 0, adapterError("scan", "", OutcomeMalformedResponse, RecoveryOperator, "provider returned an invalid scan record", err)
		}
		// GapDB charges each scan record's key and value plus a 32-byte
		// structural envelope, and another 8 bytes when expiry is present.
		pageBytes += len(convertedRecord.Key) + len(convertedRecord.Value) + 32
		if convertedRecord.ExpiresAt != nil {
			pageBytes += 8
		}
		converted.Records[index] = convertedRecord
		previous = record.Key
	}
	return converted, pageBytes, nil
}

func (a *Adapter) Watch(ctx context.Context, prefix string, after uint64) (NotificationWatch, error) {
	if err := validateKey(prefix); err != nil {
		return nil, adapterError("watch", "", OutcomeInvalid, RecoveryFixRequest, err.Error(), err)
	}
	ctx, cancel := a.requestContext(ctx)
	watch, err := a.client.Watch(ctx, prefix, gapdbapi.Revision(after))
	if err != nil {
		cancel()
		return nil, translateError("watch", "", err)
	}
	if watch == nil || uint64(watch.Registration()) < after {
		if watch != nil {
			_ = watch.Close()
		}
		cancel()
		return nil, adapterError("watch", "", OutcomeMalformedResponse, RecoveryOperator, "provider returned an invalid watch registration", nil)
	}
	return &notificationWatch{watch: watch, cancel: cancel, prefix: prefix}, nil
}

func (a *Adapter) Inspect(ctx context.Context) (Inspection, error) {
	ctx, cancel := a.requestContext(ctx)
	defer cancel()
	status, err := a.client.Status(ctx)
	if err != nil {
		return Inspection{}, translateError("inspect_status", "", err)
	}
	health, err := a.client.Health(ctx)
	if err != nil {
		return Inspection{}, translateError("inspect_health", "", err)
	}
	config, err := a.client.DescribeConfig(ctx)
	if err != nil {
		return Inspection{}, translateError("inspect_config", "", err)
	}
	if status.DatabaseID == "" || status.OwnerPID <= 0 || status.OwnerStartedAt == "" || status.CurrentRevision < status.DurableThroughRevision {
		return Inspection{}, adapterError("inspect", "", OutcomeMalformedResponse, RecoveryOperator, "provider returned malformed status", nil)
	}
	if !health.Healthy || health.Lifecycle != status.Lifecycle {
		return Inspection{}, adapterError("inspect", "", OutcomeUnavailable, RecoveryRestartProvider, "provider health does not admit commands", nil)
	}
	if status.Limits != config.Effective.Limits {
		return Inspection{}, adapterError("inspect", "", OutcomeMalformedResponse, RecoveryOperator, "reported limits disagree", nil)
	}
	if err := validateCapabilities(status.Limits); err != nil {
		return Inspection{}, adapterError("inspect", "", OutcomeUnsupportedVersion, RecoveryUpgradeProvider, "reported capabilities are below the admitted product policy", err)
	}
	return Inspection{
		EndpointID: a.endpointID, DatabaseID: status.DatabaseID, Lifecycle: string(status.Lifecycle), Healthy: health.Healthy,
		CurrentRevision: uint64(status.CurrentRevision), DurableThrough: uint64(status.DurableThroughRevision),
		OwnerPID: status.OwnerPID, OwnerStartedAt: status.OwnerStartedAt, ConfigurationSource: config.Source,
		ReportedLimits: providerLimits(status.Limits), SafeCeilings: providerLimits(config.SafeCeilings),
	}, nil
}

func providerLimits(limits gapdbapi.Limits) ProviderLimits {
	return ProviderLimits{
		KeyBytes: limits.MaxKeyBytes, ValueBytes: limits.MaxValueBytes, FrameBytes: limits.MaxFrameBytes,
		BatchBytes: limits.MaxBatchBytes, BatchOperations: limits.MaxBatchOperations,
		ScanRecords: limits.MaxScanRecords, ScanBytes: limits.MaxScanBytes, ConcurrentClients: limits.MaxConcurrentClients,
	}
}

func (a *Adapter) LeaseAuthority(ctx context.Context, mutation AuthorityMutation, ack AckMode) (MutationResult, error) {
	return a.authority(ctx, AuthorityLease, mutation, ack)
}

func (a *Adapter) ReviewAuthority(ctx context.Context, mutation AuthorityMutation, ack AckMode) (MutationResult, error) {
	return a.authority(ctx, AuthorityReview, mutation, ack)
}

func (a *Adapter) WorkflowAuthority(ctx context.Context, mutation AuthorityMutation, ack AckMode) (MutationResult, error) {
	return a.authority(ctx, AuthorityWorkflow, mutation, ack)
}

func (a *Adapter) IntegrationAuthority(ctx context.Context, mutation AuthorityMutation, ack AckMode) (MutationResult, error) {
	return a.authority(ctx, AuthorityIntegration, mutation, ack)
}

func (a *Adapter) authority(ctx context.Context, expected AuthorityNamespace, mutation AuthorityMutation, ack AckMode) (MutationResult, error) {
	if mutation.Namespace != expected || mutation.Codec != AuthorityCodec(expected) {
		return MutationResult{}, adapterError("authority", mutation.OperationID, OutcomeInvalid, RecoveryFixRequest, "authority namespace or codec mismatch", nil)
	}
	if !isCanonicalAuthorityKey(mutation.Mutation.Key, expected) {
		return MutationResult{}, adapterError("authority", mutation.OperationID, OutcomeInvalid, RecoveryFixRequest, "authority key crosses namespace", nil)
	}
	if mutation.Mutation.Condition.Kind == ConditionAny {
		return MutationResult{}, adapterError("authority", mutation.OperationID, OutcomeInvalid, RecoveryFixRequest, "authority mutation requires an absent or revision condition", nil)
	}
	return a.ConditionalMutate(ctx, mutation.OperationID, mutation.Mutation, ack)
}

type notificationWatch struct {
	watch  providerWatch
	cancel context.CancelFunc
	prefix string
}

func (w *notificationWatch) RegistrationRevision() uint64 {
	return uint64(w.watch.Registration())
}

func (w *notificationWatch) Next(ctx context.Context) (Change, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	event, err := w.watch.Next(ctx)
	if err != nil {
		return Change{}, translateError("watch_next", "", err)
	}
	if event.Revision == 0 || !strings.HasPrefix(event.Key, w.prefix) {
		return Change{}, adapterError("watch_next", "", OutcomeMalformedResponse, RecoveryRead, "watch notification is malformed; reconcile with an ordinary read", nil)
	}
	result := Change{Revision: uint64(event.Revision), Order: event.Order, Kind: ChangeKind(event.Kind), Key: event.Key}
	if event.Record != nil {
		record, err := convertRecord(*event.Record, event.Key)
		if err != nil {
			return Change{}, adapterError("watch_next", "", OutcomeMalformedResponse, RecoveryRead, "watch record is malformed; reconcile with an ordinary read", err)
		}
		result.Record = &record
	}
	return result, nil
}

func (w *notificationWatch) Close() error {
	if w == nil {
		return nil
	}
	_ = w.watch.Close()
	w.cancel()
	return nil
}

func encodeProductBatch(batch ProductBatch) (gapdbapi.Batch, int, error) {
	if err := validateOperationID(batch.OperationID); err != nil {
		return gapdbapi.Batch{}, 0, err
	}
	if len(batch.Events) < 1 || len(batch.Events) > MaxEventsPerCommand {
		return gapdbapi.Batch{}, 0, fmt.Errorf("event count must be between 1 and %d", MaxEventsPerCommand)
	}
	if len(batch.AggregateHeads) < 1 || len(batch.AggregateHeads) > MaxHeadsPerCommand {
		return gapdbapi.Batch{}, 0, fmt.Errorf("aggregate head count must be between 1 and %d", MaxHeadsPerCommand)
	}
	if len(batch.EffectIntents) > MaxIntentsPerCommand {
		return gapdbapi.Batch{}, 0, fmt.Errorf("effect intent count must not exceed %d", MaxIntentsPerCommand)
	}
	if (batch.Checkpoint == nil) != (batch.CheckpointHead == nil) {
		return gapdbapi.Batch{}, 0, errors.New("checkpoint record and head must be supplied together")
	}
	if batch.Operation.Condition.Kind != ConditionAbsent {
		return gapdbapi.Batch{}, 0, errors.New("operation record must require absence")
	}
	for _, event := range batch.Events {
		if event.Condition.Kind != ConditionAbsent {
			return gapdbapi.Batch{}, 0, errors.New("event records must require absence")
		}
	}
	for _, head := range batch.AggregateHeads {
		if head.Condition.Kind == ConditionAny {
			return gapdbapi.Batch{}, 0, errors.New("aggregate heads require absent or revision conditions")
		}
	}
	for _, intent := range batch.EffectIntents {
		if intent.Condition.Kind != ConditionAbsent {
			return gapdbapi.Batch{}, 0, errors.New("effect intents must require absence")
		}
	}
	if batch.Checkpoint != nil && batch.Checkpoint.Condition.Kind != ConditionAbsent {
		return gapdbapi.Batch{}, 0, errors.New("checkpoint record must require absence")
	}
	if batch.CheckpointHead != nil && batch.CheckpointHead.Condition.Kind == ConditionAny {
		return gapdbapi.Batch{}, 0, errors.New("checkpoint head requires an absent or revision condition")
	}
	mutations := []Mutation{batch.Operation}
	mutations = append(mutations, batch.Events...)
	mutations = append(mutations, batch.AggregateHeads...)
	if batch.Checkpoint != nil {
		mutations = append(mutations, *batch.Checkpoint, *batch.CheckpointHead)
	}
	mutations = append(mutations, batch.EffectIntents...)
	if err := validateMutationCount(len(mutations)); err != nil {
		return gapdbapi.Batch{}, 0, err
	}
	provider := gapdbapi.Batch{Ack: gapdbapi.AckDurable, Mutations: make([]gapdbapi.Mutation, len(mutations))}
	rawBytes := 0
	seen := make(map[string]struct{}, len(mutations))
	checkpointIndex := -1
	if batch.Checkpoint != nil {
		checkpointIndex = 1 + len(batch.Events) + len(batch.AggregateHeads)
	}
	for index, mutation := range mutations {
		valueMaximum := MaxOrdinaryValueBytes
		if index == checkpointIndex {
			valueMaximum = MaxCheckpointValueBytes
		}
		if err := validateMutation(mutation, valueMaximum); err != nil {
			return gapdbapi.Batch{}, 0, fmt.Errorf("mutation %d: %w", index, err)
		}
		if _, exists := seen[mutation.Key]; exists {
			return gapdbapi.Batch{}, 0, fmt.Errorf("duplicate mutation key at index %d", index)
		}
		seen[mutation.Key] = struct{}{}
		rawBytes += len(mutation.Key) + len(mutation.Value)
		provider.Mutations[index] = providerMutation(mutation)
	}
	return provider, rawBytes, nil
}

func providerMutation(mutation Mutation) gapdbapi.Mutation {
	condition := gapdbapi.Condition{Kind: gapdbapi.ConditionKind(mutation.Condition.Kind), ExpectedRevision: gapdbapi.Revision(mutation.Condition.ExpectedRevision)}
	if mutation.Kind == MutationDelete {
		return gapdbapi.NewDeleteMutation(mutation.Key, gapdbapi.Revision(mutation.Condition.ExpectedRevision))
	}
	return gapdbapi.NewPutMutation(mutation.Key, mutation.Value, condition, cloneTime(mutation.ExpiresAt))
}

func validateMutation(mutation Mutation, maxValue int) error {
	if err := validateKey(mutation.Key); err != nil {
		return err
	}
	switch mutation.Condition.Kind {
	case ConditionAny, ConditionAbsent:
		if mutation.Condition.ExpectedRevision != 0 {
			return errors.New("non-revision condition carries an expected revision")
		}
	case ConditionRevision:
		if mutation.Condition.ExpectedRevision == 0 {
			return errors.New("revision condition requires a positive expected revision")
		}
	default:
		return errors.New("condition kind is invalid")
	}
	switch mutation.Kind {
	case MutationPut:
		if len(mutation.Value) > maxValue {
			return fmt.Errorf("value exceeds %d bytes", maxValue)
		}
	case MutationDelete:
		if mutation.Condition.Kind != ConditionRevision || mutation.Value != nil || mutation.ExpiresAt != nil {
			return errors.New("delete requires a revision condition and no value or expiry")
		}
	default:
		return errors.New("mutation kind is invalid")
	}
	return nil
}

func validateKey(key string) error {
	if key == "" {
		return errors.New("key is required")
	}
	if len(key) > MaxKeyBytes {
		return fmt.Errorf("key exceeds %d bytes", MaxKeyBytes)
	}
	return nil
}

func validateOperationID(operationID string) error {
	if operationID == "" || len(operationID) > MaxOperationIDBytes {
		return fmt.Errorf("operation ID must contain 1-%d bytes", MaxOperationIDBytes)
	}
	return nil
}

func providerAck(ack AckMode) (gapdbapi.AckMode, error) {
	switch ack {
	case AckMemory:
		return gapdbapi.AckMemory, nil
	case AckDurable:
		return gapdbapi.AckDurable, nil
	default:
		return "", errors.New("acknowledgement must be memory or durable")
	}
}

func convertRecord(record gapdbapi.Record, expectedKey string) (Record, error) {
	if record.Key == "" || record.Key != expectedKey || record.Revision == 0 || len(record.Key) > MaxKeyBytes || len(record.Value) > MaxCheckpointValueBytes {
		return Record{}, errors.New("record identity or revision is invalid")
	}
	return Record{Key: record.Key, Value: append([]byte{}, record.Value...), Revision: uint64(record.Revision), ExpiresAt: cloneTime(record.ExpiresAt)}, nil
}

func convertMutationResult(operation, operationID string, result gapdbapi.MutationResult, expectedCount int, expectedAck gapdbapi.AckMode) (MutationResult, error) {
	if result.Revision == 0 || !result.Ack.Valid() || result.Ack != expectedAck ||
		result.DurableThroughRevision > result.Revision ||
		result.Ack == gapdbapi.AckDurable && result.DurableThroughRevision != result.Revision ||
		expectedCount <= 0 || result.MutationCount != expectedCount {
		return MutationResult{}, adapterError(operation, operationID, OutcomeMalformedResponse, RecoveryReconcile, "provider returned an invalid mutation acknowledgement", nil)
	}
	return MutationResult{Revision: uint64(result.Revision), Ack: AckMode(result.Ack), DurableThrough: uint64(result.DurableThroughRevision), MutationCount: result.MutationCount}, nil
}

func validateMutationCount(count int) error {
	if count <= 0 || count > MaxMutationsPerBatch {
		return fmt.Errorf("mutation count must contain 1-%d entries", MaxMutationsPerBatch)
	}
	return nil
}

func validateWireFrameSize(size int) error {
	if size <= 0 || size > MaxWireFrameBytes {
		return fmt.Errorf("wire frame must contain 1-%d bytes", MaxWireFrameBytes)
	}
	return nil
}

func isCanonicalAuthorityKey(key string, expected AuthorityNamespace) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 7 || parts[0] != "spk" || parts[1] != "v1" || parts[4] != "heads" || parts[5] != string(expected) {
		return false
	}
	for _, index := range []int{2, 3, 6} {
		if parts[index] == "" || parts[index] == "." || parts[index] == ".." || strings.IndexByte(parts[index], 0) >= 0 {
			return false
		}
	}
	return true
}

func translateError(operation, operationID string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return adapterError(operation, operationID, OutcomeCanceled, RecoveryReconcile, "request context was canceled", err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return adapterError(operation, operationID, OutcomeDeadlineExceeded, RecoveryReconcile, "request deadline expired", err)
	}
	var transport *gapdbapi.TransportError
	if errors.As(err, &transport) {
		if transport.Ambiguous && operationID != "" {
			return adapterError(operation, operationID, OutcomeAmbiguous, RecoveryReconcile, "delivery outcome is unknown", err)
		}
		if transport.Ambiguous {
			return adapterError(operation, operationID, OutcomeUnavailable, RecoveryRead, "read response is unavailable; retry the read", err)
		}
		return adapterError(operation, operationID, OutcomeUnavailable, RecoveryRetrySameOperation, "request was not delivered", err)
	}
	var provider *gapdbapi.Error
	if errors.As(err, &provider) {
		switch provider.Code {
		case gapdbapi.CodeNotFound:
			return adapterError(operation, operationID, OutcomeNotFound, RecoveryRead, "record is absent", err)
		case gapdbapi.CodeAlreadyExists, gapdbapi.CodeRevisionMismatch, gapdbapi.CodeConditionFailed, gapdbapi.CodeScanStale:
			return adapterError(operation, operationID, OutcomeConflict, RecoveryRead, "conditional state changed", err)
		case gapdbapi.CodeUnsupportedVersion, gapdbapi.CodeUnknownFormat:
			return adapterError(operation, operationID, OutcomeUnsupportedVersion, RecoveryUpgradeProvider, "provider version is unsupported", err)
		case gapdbapi.CodeServerUnavailable, gapdbapi.CodeServerBusy, gapdbapi.CodeServerShuttingDown, gapdbapi.CodeStorageDegraded:
			return adapterError(operation, operationID, OutcomeUnavailable, RecoveryRestartProvider, "provider is unavailable", err)
		default:
			return adapterError(operation, operationID, OutcomeInvalid, RecoveryOperator, "provider refused the request", err)
		}
	}
	return adapterError(operation, operationID, OutcomeMalformedResponse, RecoveryOperator, "provider returned an unclassified failure", err)
}

func adapterError(operation, operationID string, kind OutcomeKind, recovery RecoveryAction, detail string, cause error) *Error {
	return &Error{Kind: kind, Operation: boundedText(operation, 64), OperationID: boundedText(operationID, MaxOperationIDBytes), Recovery: recovery, Detail: boundedText(detail, MaxDiagnosticBytes/2), Cause: cause}
}

func validateCapabilities(limits gapdbapi.Limits) error {
	required := []struct {
		name string
		got  int
		want int
	}{
		{"key", limits.MaxKeyBytes, MaxKeyBytes}, {"value", limits.MaxValueBytes, MaxCheckpointValueBytes},
		{"frame", limits.MaxFrameBytes, MaxWireFrameBytes}, {"batch_bytes", limits.MaxBatchBytes, MaxRawBatchBytes},
		{"batch_operations", limits.MaxBatchOperations, MaxMutationsPerBatch}, {"scan_records", limits.MaxScanRecords, MaxScanRecords},
		{"scan_bytes", limits.MaxScanBytes, MaxScanBytes}, {"concurrent_clients", limits.MaxConcurrentClients, 32},
	}
	for _, item := range required {
		if item.got < item.want {
			return fmt.Errorf("%s capability is below %d", item.name, item.want)
		}
	}
	return nil
}

func (a *Adapter) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= a.timeout {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, a.timeout)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
