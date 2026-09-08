// Package restore coordinates bounded provider-neutral recovery. It performs
// all reads before handing immutable facts to the pure playback package.
package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/checkpoint"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
)

type ReaderV2 interface {
	CaptureRevisionV2(context.Context) (journal.StoreRevisionV2, error)
	VerifyRevisionV2(context.Context, journal.StoreRevisionV2) error
	ReadV2(context.Context, journal.ReadRequestV2) (journal.RecoveryRecordV2, error)
	ScanV2(context.Context, journal.ScanRequestV2) (journal.RecoveryPageV2, error)
}

type BudgetsV2 struct {
	MaximumRestarts     int
	MaximumPages        int
	MaximumRecords      int
	MaximumValueBytes   int
	MaximumKeyBytes     int
	MaximumSuffixEvents int
	MaximumWorkers      int
	MaximumDuration     time.Duration
}

func DefaultBudgetsV2() BudgetsV2 {
	return BudgetsV2{MaximumRestarts: 3, MaximumPages: 256, MaximumRecords: 30_000, MaximumValueBytes: 256 * 1024 * 1024,
		MaximumKeyBytes: 16 * 1024 * 1024, MaximumSuffixEvents: 100_000, MaximumWorkers: 8, MaximumDuration: 30 * time.Second}
}

type InputV2 struct {
	Reader              ReaderV2
	EventRegistry       journal.EventRegistryV2
	Reducers            journal.ReducerSetV2
	HostAuthorityID     journal.HostAuthorityID
	PriorRuntimeEpochID journal.RuntimeEpochID
	RecoveryAttemptID   string
	Budgets             BudgetsV2
}

type ErrorKind string

const (
	ErrorUnstableStore   ErrorKind = "UNSTABLE_STORE"
	ErrorJournalCorrupt  ErrorKind = "JOURNAL_CORRUPT"
	ErrorBudgetExceeded  ErrorKind = "RESTORE_BUDGET_EXCEEDED"
	ErrorAuthorityClosed ErrorKind = "AUTHORITY_CLOSED"
)

type Error struct {
	Kind     ErrorKind
	Field    string
	Attempts int
}

func (e *Error) Error() string {
	if e.Field == "" {
		return string(e.Kind)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Field)
}

type AdmissionRequestV2 struct {
	databaseID            journal.DatabaseID
	hostAuthorityID       journal.HostAuthorityID
	throughRevision       uint64
	projectionDigest      journal.Digest
	priorEpochID          journal.RuntimeEpochID
	eventRegistryDigest   journal.Digest
	reducerRegistryDigest journal.Digest
	codecVersion          uint32
	attemptID             string
}

func (a AdmissionRequestV2) DatabaseID() journal.DatabaseID              { return a.databaseID }
func (a AdmissionRequestV2) HostAuthorityID() journal.HostAuthorityID    { return a.hostAuthorityID }
func (a AdmissionRequestV2) ThroughRecordRevision() uint64               { return a.throughRevision }
func (a AdmissionRequestV2) CanonicalProjectionDigest() journal.Digest   { return a.projectionDigest }
func (a AdmissionRequestV2) PriorRuntimeEpochID() journal.RuntimeEpochID { return a.priorEpochID }
func (a AdmissionRequestV2) EventRegistryDigest() journal.Digest         { return a.eventRegistryDigest }
func (a AdmissionRequestV2) ReducerRegistryDigest() journal.Digest       { return a.reducerRegistryDigest }
func (a AdmissionRequestV2) CanonicalCodecVersion() uint32               { return a.codecVersion }
func (a AdmissionRequestV2) RecoveryAttemptID() string                   { return a.attemptID }

type ResultV2 struct {
	playback        playback.ResultV2
	attempt         journal.StoreRevisionV2
	rollingPrefix   journal.Digest
	hostAuthorityID journal.HostAuthorityID
	priorEpochID    journal.RuntimeEpochID
	attemptID       string
	seal            journal.Digest
}

func (r ResultV2) Playback() playback.ResultV2            { return r.playback }
func (r ResultV2) StoreRevision() journal.StoreRevisionV2 { return r.attempt }
func (r ResultV2) RollingPrefixDigest() journal.Digest    { return r.rollingPrefix }
func (r ResultV2) FreshEpochAdmissionRequestV2() (AdmissionRequestV2, error) {
	if r.seal.IsZero() || sealResult(r) != r.seal || r.playback.Validate() != nil {
		return AdmissionRequestV2{}, &Error{Kind: ErrorAuthorityClosed, Field: "restore_seal"}
	}
	return AdmissionRequestV2{r.attempt.DatabaseID(), r.hostAuthorityID, r.attempt.CurrentRevision(), r.playback.CanonicalProjectionDigest(), r.priorEpochID,
		r.playback.EventRegistryDigest(), r.playback.ReducerRegistryDigest(), journal.CanonicalCodecVersionV2, r.attemptID}, nil
}

type scanEvidence struct {
	pages   []journal.RecoveryPageV2
	records []journal.RecoveryRecordV2
}
type aggregateCurrent struct {
	head   journal.AggregateHeadV2
	record journal.RecoveryRecordV2
}
type operationCurrent struct {
	binding                    journal.OperationBindingV2
	state                      journal.OperationStateV2
	bindingRecord, stateRecord journal.RecoveryRecordV2
}

func RestoreV2(ctx context.Context, input InputV2) (ResultV2, error) {
	if input.Reader == nil {
		return ResultV2{}, &Error{Kind: ErrorAuthorityClosed, Field: "reader"}
	}
	if _, err := journal.NewHostAuthorityID(string(input.HostAuthorityID)); err != nil {
		return ResultV2{}, err
	}
	if input.PriorRuntimeEpochID != "" {
		if _, err := journal.NewRuntimeEpochID(string(input.PriorRuntimeEpochID)); err != nil {
			return ResultV2{}, err
		}
	}
	if input.RecoveryAttemptID == "" || len(input.RecoveryAttemptID) > journal.MaxIdentityBytes {
		return ResultV2{}, &Error{Kind: ErrorAuthorityClosed, Field: "attempt_id"}
	}
	budgets := input.Budgets
	if budgets == (BudgetsV2{}) {
		budgets = DefaultBudgetsV2()
	}
	if err := validateBudgets(budgets); err != nil {
		return ResultV2{}, err
	}
	for attemptNumber := 1; attemptNumber <= budgets.MaximumRestarts; attemptNumber++ {
		result, err := restoreAttempt(ctx, input, budgets)
		if err == nil {
			return result, nil
		}
		if parentErr := ctx.Err(); parentErr != nil {
			return ResultV2{}, parentErr
		}
		if !retryable(err) {
			return ResultV2{}, err
		}
		if attemptNumber == budgets.MaximumRestarts {
			return ResultV2{}, &Error{Kind: ErrorUnstableStore, Field: "restarts", Attempts: attemptNumber}
		}
	}
	return ResultV2{}, &Error{Kind: ErrorUnstableStore, Field: "restarts", Attempts: budgets.MaximumRestarts}
}

func restoreAttempt(parent context.Context, input InputV2, budgets BudgetsV2) (ResultV2, error) {
	ctx, cancel := context.WithTimeout(parent, budgets.MaximumDuration)
	defer cancel()
	attempt, err := input.Reader.CaptureRevisionV2(ctx)
	if err != nil {
		return ResultV2{}, err
	}
	if attempt.CurrentRevision() == 0 || attempt.DurableThroughRevision() < attempt.CurrentRevision() {
		return ResultV2{}, &Error{Kind: ErrorUnstableStore, Field: "capture"}
	}
	evidence := &scanEvidence{}
	eventDescriptor, err := input.EventRegistry.Descriptor()
	if err != nil {
		return ResultV2{}, err
	}
	reducerDescriptor, err := input.Reducers.Descriptor()
	if err != nil {
		return ResultV2{}, err
	}
	compatibility := checkpoint.CompatibilityV2{EventRegistryDigest: eventDescriptor.Digest(), ReducerRegistryDigest: reducerDescriptor.Digest(), CanonicalCodecVersion: journal.CanonicalCodecVersionV2}
	selection, err := selectCheckpoint(ctx, input.Reader, attempt, compatibility, budgets, evidence)
	if err != nil {
		return ResultV2{}, err
	}
	aggregates, aggregatePages, err := scanAggregates(ctx, input.Reader, attempt, budgets)
	if err != nil {
		return ResultV2{}, err
	}
	evidence.pages = append(evidence.pages, aggregatePages...)
	operations, operationPages, err := scanOperations(ctx, input.Reader, attempt, budgets)
	if err != nil {
		return ResultV2{}, err
	}
	evidence.pages = append(evidence.pages, operationPages...)

	watermark := uint64(0)
	prefix := journal.ProjectionPrefixGenesisV2()
	var summary journal.OperationSummaryV2
	base := make([]playback.StateV2, 0)
	baseVersions := make(map[string]journal.LogicalVersion)
	boundaryStates := make(map[journal.OperationID]journal.OperationStateV2)
	if !selection.IsOrigin() {
		c := selection.Checkpoint()
		watermark, prefix, summary = c.ThroughRecordRevision(), c.RollingPrefixDigest(), c.OperationSummary()
		for _, entry := range c.Aggregates() {
			key := coordinateKey(entry.Coordinate())
			current, exists := aggregates[key]
			if !exists || current.head.LogicalVersion() < entry.LogicalVersion() {
				return ResultV2{}, corrupt("checkpoint_census")
			}
			state, err := playback.NewStateV2(entry.Coordinate(), entry.LogicalVersion(), entry.LastEventID(), entry.CanonicalState(), entry.StateDigest(), entry.Tombstone())
			if err != nil {
				return ResultV2{}, err
			}
			base = append(base, state)
			baseVersions[key] = entry.LogicalVersion()
		}
		// Operation aggregates are intentionally excluded from the canonical
		// product projection. Preserve the exact product-only base here so the
		// checkpoint projection proof does not redundantly replay the compact
		// operation census; operation seeds are appended below for suffix replay.
		projectionBase := append([]playback.StateV2(nil), base...)
		operationIDsByDigest := make(map[journal.Digest]journal.OperationID, len(operations))
		for operationID, current := range operations {
			singleton, err := journal.BuildOperationSummaryV2([]journal.OperationStateV2{current.state})
			if err != nil || len(singleton.Boundaries()) != 1 {
				return ResultV2{}, corrupt("operation_lookup")
			}
			lookup := singleton.Boundaries()[0].OperationIDLookupDigest()
			if prior, duplicate := operationIDsByDigest[lookup]; duplicate && prior != operationID {
				return ResultV2{}, corrupt("operation_boundary_collision")
			}
			operationIDsByDigest[lookup] = operationID
		}
		matched := make(map[journal.OperationID]bool)
		boundaryStateList := make([]journal.OperationStateV2, 0, len(summary.Boundaries()))
		for _, boundary := range summary.Boundaries() {
			found := operationIDsByDigest[boundary.OperationIDLookupDigest()]
			if found == "" || !boundary.MatchesOperationID(found) || matched[found] || operations[found].state.LogicalVersion() < boundary.LogicalVersion() {
				return ResultV2{}, corrupt("operation_boundary")
			}
			matched[found] = true
			coordinate := operationCoordinate(found)
			record, event, err := exactEvent(ctx, input.Reader, attempt, input.EventRegistry, coordinate, boundary.LogicalVersion(), 0, watermark)
			if err != nil {
				return ResultV2{}, err
			}
			evidence.records = append(evidence.records, record)
			state, err := journal.ReconstructOperationStateFromEventV2(event)
			if err != nil || state.OperationID() != found || state.LogicalVersion() != boundary.LogicalVersion() {
				return ResultV2{}, corrupt("operation_boundary_state")
			}
			boundaryStateList = append(boundaryStateList, state)
			boundaryStates[found] = state
			encoded, err := journal.EncodeOperationStateV2(state)
			if err != nil {
				return ResultV2{}, err
			}
			stateDigest, _ := journal.ReducerStateDigestV2(encoded)
			seed, err := playback.NewStateV2(coordinate, state.LogicalVersion(), event.EventID(), encoded, stateDigest, false)
			if err != nil {
				return ResultV2{}, err
			}
			base = append(base, seed)
			baseVersions[coordinateKey(coordinate)] = state.LogicalVersion()
		}
		if err := journal.ValidateOperationSummaryV2(summary, boundaryStateList); err != nil || len(matched) != len(summary.Boundaries()) {
			return ResultV2{}, corrupt("operation_summary")
		}
		basePlayback, err := playback.RunV2(playback.InputV2{Base: projectionBase, EventRegistry: input.EventRegistry, Reducers: input.Reducers,
			ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest(),
			MaximumAggregates: playback.MaxPlaybackAggregates, MaximumEvents: budgets.MaximumSuffixEvents,
			MaximumBytes: min(budgets.MaximumValueBytes, playback.MaxPlaybackBytes)})
		if err != nil || basePlayback.CanonicalProjectionDigest() != c.CanonicalProjectionDigest() {
			return ResultV2{}, corrupt("checkpoint_projection_digest")
		}
	}

	requests, err := suffixPlan(aggregates, operations, baseVersions, budgets.MaximumSuffixEvents)
	if err != nil {
		return ResultV2{}, err
	}
	material, records, err := fetchEvents(ctx, input.Reader, attempt, input.EventRegistry, requests, watermark, budgets)
	if err != nil {
		return ResultV2{}, err
	}
	evidence.records = append(evidence.records, records...)
	ordered, finalOperations, nextPrefix, witnessRecords, err := validateMembership(ctx, input.Reader, attempt, material, operations, boundaryStates, prefix, watermark, budgets)
	if err != nil {
		return ResultV2{}, err
	}
	evidence.records = append(evidence.records, witnessRecords...)
	played, err := playback.RunV2(playback.InputV2{Base: base, Events: ordered, EventRegistry: input.EventRegistry, Reducers: input.Reducers,
		ExpectedEventRegistryDigest: eventDescriptor.Digest(), ExpectedReducerRegistryDigest: reducerDescriptor.Digest(), MaximumAggregates: playback.MaxPlaybackAggregates,
		MaximumEvents: budgets.MaximumSuffixEvents, MaximumBytes: min(budgets.MaximumValueBytes, playback.MaxPlaybackBytes)})
	if err != nil {
		return ResultV2{}, err
	}
	if err := validateTerminal(played, aggregates, operations, finalOperations); err != nil {
		return ResultV2{}, err
	}
	if len(material) == 0 && !selection.IsOrigin() && nextPrefix != prefix {
		return ResultV2{}, corrupt("rolling_prefix")
	}
	if !selection.IsOrigin() {
		foundWriter := false
		for _, item := range material {
			if item.record.RecordRevision() == selection.CommitRevision() {
				foundWriter = true
				break
			}
		}
		if !foundWriter {
			return ResultV2{}, corrupt("checkpoint_writer_suffix")
		}
	}
	if err := journal.ValidateStableRecoveryAttemptV2(attempt, evidence.pages, evidence.records, attempt); err != nil {
		return ResultV2{}, err
	}
	if err := input.Reader.VerifyRevisionV2(ctx, attempt); err != nil {
		return ResultV2{}, err
	}
	result := ResultV2{played, attempt, nextPrefix, input.HostAuthorityID, input.PriorRuntimeEpochID, input.RecoveryAttemptID, journal.Digest{}}
	result.seal = sealResult(result)
	return result, nil
}

func scanAll(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, target journal.ScanTargetV2, budgets BudgetsV2) ([]journal.RecoveryRecordV2, []journal.RecoveryPageV2, error) {
	var records []journal.RecoveryRecordV2
	var pages []journal.RecoveryPageV2
	var cursor []byte
	seen := map[string]bool{}
	bytesUsed := 0
	keyBytesUsed := 0
	for {
		if len(pages) >= budgets.MaximumPages {
			return nil, nil, budget("pages")
		}
		remainingRecords, remainingBytes := budgets.MaximumRecords-len(records), budgets.MaximumValueBytes-bytesUsed
		if remainingRecords <= 0 || remainingBytes <= 0 {
			return nil, nil, budget("scan")
		}
		request, err := journal.NewScanRequestV2(target, attempt, cursor, min(remainingRecords, journal.MaxRecoveryPageRecords), min(remainingBytes, journal.MaxRecoveryPageBytes))
		if err != nil {
			return nil, nil, err
		}
		page, err := reader.ScanV2(ctx, request)
		if err != nil {
			return nil, nil, err
		}
		if page.ObservedRevision() != attempt.CurrentRevision() {
			return nil, nil, unstable("scan_revision")
		}
		pageRecords := page.Records()
		for _, record := range pageRecords {
			if record.RecordRevision() == 0 || record.RecordRevision() > attempt.CurrentRevision() || record.ExpiresAt() != "" {
				return nil, nil, corrupt("scan_record")
			}
			if record.CanonicalSizeV2() > budgets.MaximumValueBytes-bytesUsed {
				return nil, nil, budget("value_bytes")
			}
			bytesUsed += record.CanonicalSizeV2()
			keyBytesUsed += semanticAddressBytes(record.Address())
			if keyBytesUsed > budgets.MaximumKeyBytes {
				return nil, nil, budget("key_bytes")
			}
		}
		if len(pageRecords) > budgets.MaximumRecords-len(records) {
			return nil, nil, budget("records")
		}
		records = append(records, pageRecords...)
		pages = append(pages, page)
		next := page.Cursor()
		if !page.Truncated() {
			if len(next) != 0 {
				return nil, nil, corrupt("cursor")
			}
			break
		}
		if len(next) == 0 || seen[string(next)] {
			return nil, nil, unstable("cursor_cycle")
		}
		seen[string(next)] = true
		cursor = next
	}
	return records, pages, nil
}

func semanticAddressBytes(address journal.RecordAddressV2) int {
	switch address.Kind() {
	case journal.AddressAggregateHead, journal.AddressJournalEvent:
		value, _ := journal.CanonicalAggregateCoordinate(address.Coordinate())
		return len(value) + 8
	case journal.AddressOperationBinding, journal.AddressOperationHead:
		return len(address.OperationID()) + 1
	case journal.AddressCheckpointRecord:
		return len(address.CheckpointID()) + 1
	case journal.AddressCheckpointSlot:
		return len(address.CheckpointSlotID()) + 1
	case journal.AddressPublicationWitness:
		return len(address.PublicationID()) + 1
	default:
		return budgetsImpossibleAddressBytes
	}
}

const budgetsImpossibleAddressBytes = 1 << 30

func selectCheckpoint(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, compatibility checkpoint.CompatibilityV2, budgets BudgetsV2, evidence *scanEvidence) (checkpoint.SelectionV2, error) {
	records, pages, err := scanAll(ctx, reader, attempt, journal.CheckpointSlotScanTargetV2(), budgets)
	if err != nil {
		return checkpoint.SelectionV2{}, err
	}
	evidence.pages = append(evidence.pages, pages...)
	if len(records) == 0 {
		return checkpoint.OriginV2(), nil
	}
	if len(records) > checkpoint.RetainedCandidateSlots {
		return checkpoint.SelectionV2{}, budget("checkpoint_candidates")
	}
	candidates := make([]checkpoint.CandidateV2, 0, len(records))
	seenSlots := make(map[journal.CheckpointSlotID]bool, len(records))
	invalidEvidence := false
	for _, slotRecord := range records {
		slot, err := checkpoint.DecodeSlotV2(slotRecord.CanonicalValue())
		if err != nil {
			invalidEvidence = true
			continue
		}
		if seenSlots[slot.SlotID()] {
			return checkpoint.SelectionV2{}, corrupt("duplicate_checkpoint_slot")
		}
		seenSlots[slot.SlotID()] = true
		address, _ := journal.NewCheckpointRecordAddressV2(slot.CheckpointID())
		request, _ := journal.NewReadRequestV2(address, attempt)
		checkpointRecord, err := reader.ReadV2(ctx, request)
		if err != nil {
			if !journal.IsKind(err, journal.ErrorNotFound) {
				return checkpoint.SelectionV2{}, err
			}
			invalidEvidence = true
			continue
		}
		c, err := checkpoint.DecodeV2(checkpointRecord.CanonicalValue())
		if err != nil {
			invalidEvidence = true
			continue
		}
		witnessAddress, err := c.CoveredThroughPublicationID().WitnessAddressV2()
		if err != nil {
			invalidEvidence = true
			continue
		}
		witnessRequest, _ := journal.NewReadRequestV2(witnessAddress, attempt)
		witness, err := reader.ReadV2(ctx, witnessRequest)
		if err != nil {
			if !journal.IsKind(err, journal.ErrorNotFound) {
				return checkpoint.SelectionV2{}, err
			}
			invalidEvidence = true
			continue
		}
		candidates = append(candidates, checkpoint.CandidateV2{
			SlotRecord:       slotRecord,
			CheckpointRecord: checkpointRecord,
			CoveredWitness:   witness,
		})
		evidence.records = append(evidence.records, checkpointRecord, witness)
	}
	if len(candidates) == 0 {
		return checkpoint.SelectionV2{}, corrupt("checkpoint_candidates")
	}
	selection, err := checkpoint.SelectV2(candidates, compatibility, attempt)
	if err == nil && selection.IsOrigin() && invalidEvidence {
		return checkpoint.SelectionV2{}, corrupt("checkpoint_candidates")
	}
	return selection, err
}

func scanAggregates(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, budgets BudgetsV2) (map[string]aggregateCurrent, []journal.RecoveryPageV2, error) {
	records, pages, err := scanAll(ctx, reader, attempt, journal.AggregateHeadScanTargetV2(), budgets)
	if err != nil {
		return nil, nil, err
	}
	result := make(map[string]aggregateCurrent, len(records))
	for _, record := range records {
		if record.Address().Kind() != journal.AddressAggregateHead {
			return nil, nil, corrupt("aggregate_address")
		}
		head, err := journal.DecodeAggregateHeadV2(record.CanonicalValue())
		if err != nil {
			return nil, nil, corrupt("aggregate_head_codec")
		}
		if head.Coordinate() != record.Address().Coordinate() {
			return nil, nil, corrupt("aggregate_head_coordinate")
		}
		if head.LogicalVersion() == 0 {
			return nil, nil, corrupt("aggregate_head_version")
		}
		key := coordinateKey(head.Coordinate())
		if _, exists := result[key]; exists {
			return nil, nil, corrupt("duplicate_head")
		}
		result[key] = aggregateCurrent{head, record}
	}
	return result, pages, nil
}

func scanOperations(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, budgets BudgetsV2) (map[journal.OperationID]operationCurrent, []journal.RecoveryPageV2, error) {
	records, pages, err := scanAll(ctx, reader, attempt, journal.OperationRecordScanTargetV2(), budgets)
	if err != nil {
		return nil, nil, err
	}
	type pair struct {
		binding                    journal.OperationBindingV2
		state                      journal.OperationStateV2
		bindingRecord, stateRecord journal.RecoveryRecordV2
		hasBinding, hasState       bool
	}
	pairs := map[journal.OperationID]pair{}
	for _, record := range records {
		id := record.Address().OperationID()
		item := pairs[id]
		switch record.Address().Kind() {
		case journal.AddressOperationBinding:
			if item.hasBinding {
				return nil, nil, corrupt("duplicate_operation_binding")
			}
			value, err := journal.DecodeOperationBindingRecordV2(record)
			if err != nil || value.OperationID() != id {
				return nil, nil, corrupt("operation_binding")
			}
			item.binding, item.bindingRecord, item.hasBinding = value, record, true
		case journal.AddressOperationHead:
			if item.hasState {
				return nil, nil, corrupt("duplicate_operation_head")
			}
			value, err := journal.DecodeOperationStateRecordV2(record)
			if err != nil || value.OperationID() != id {
				return nil, nil, corrupt("operation_head")
			}
			item.state, item.stateRecord, item.hasState = value, record, true
		default:
			return nil, nil, corrupt("operation_address")
		}
		pairs[id] = item
	}
	result := make(map[journal.OperationID]operationCurrent, len(pairs))
	for id, item := range pairs {
		if !item.hasBinding || !item.hasState {
			return nil, nil, corrupt("operation_pair")
		}
		result[id] = operationCurrent{item.binding, item.state, item.bindingRecord, item.stateRecord}
	}
	return result, pages, nil
}

type eventRequest struct {
	coordinate        journal.AggregateCoordinate
	coordinateKey     string
	version           journal.LogicalVersion
	terminalTombstone bool
}

func suffixPlan(aggregates map[string]aggregateCurrent, operations map[journal.OperationID]operationCurrent, base map[string]journal.LogicalVersion, maximum int) ([]eventRequest, error) {
	requests := make([]eventRequest, 0)
	add := func(coordinate journal.AggregateCoordinate, current journal.LogicalVersion, tombstone bool) error {
		prior := base[coordinateKey(coordinate)]
		if current < prior {
			return corrupt("head_regression")
		}
		difference := uint64(current - prior)
		if difference > uint64(maximum-len(requests)) {
			return budget("suffix_events")
		}
		encodedCoordinate, err := journal.CanonicalAggregateCoordinate(coordinate)
		if err != nil {
			return err
		}
		sortKey := string(encodedCoordinate)
		for version := prior + 1; version <= current; version++ {
			requests = append(requests, eventRequest{coordinate: coordinate, coordinateKey: sortKey, version: version, terminalTombstone: tombstone && version == current})
			if version == journal.LogicalVersion(^uint64(0)) {
				return budget("logical_version")
			}
		}
		return nil
	}
	for _, current := range aggregates {
		if err := add(current.head.Coordinate(), current.head.LogicalVersion(), current.head.Tombstone()); err != nil {
			return nil, err
		}
	}
	for id, current := range operations {
		if err := add(operationCoordinate(id), current.state.LogicalVersion(), false); err != nil {
			return nil, err
		}
	}
	sort.Slice(requests, func(i, k int) bool {
		if cmp := strings.Compare(requests[i].coordinateKey, requests[k].coordinateKey); cmp != 0 {
			return cmp < 0
		}
		return requests[i].version < requests[k].version
	})
	return requests, nil
}

func exactEvent(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, registry journal.EventRegistryV2, coordinate journal.AggregateCoordinate, version journal.LogicalVersion, lower, upper uint64) (journal.RecoveryRecordV2, journal.EventEnvelopeV2, error) {
	address, err := journal.NewJournalEventAddressV2(coordinate, version)
	if err != nil {
		return journal.RecoveryRecordV2{}, journal.EventEnvelopeV2{}, err
	}
	request, _ := journal.NewReadRequestV2(address, attempt)
	record, err := reader.ReadV2(ctx, request)
	if err != nil {
		if journal.IsKind(err, journal.ErrorNotFound) {
			return journal.RecoveryRecordV2{}, journal.EventEnvelopeV2{}, corrupt("missing_event")
		}
		return journal.RecoveryRecordV2{}, journal.EventEnvelopeV2{}, err
	}
	if record.Address() != address || record.ExpiresAt() != "" || record.RecordRevision() <= lower || record.RecordRevision() > upper {
		return journal.RecoveryRecordV2{}, journal.EventEnvelopeV2{}, corrupt("event_record")
	}
	event, err := journal.DecodeEventEnvelopeRecordV2(record, registry)
	if err != nil || event.Coordinate() != coordinate || event.LogicalVersion() != version {
		return journal.RecoveryRecordV2{}, journal.EventEnvelopeV2{}, corrupt("event_envelope")
	}
	return record, event, nil
}

type fetchedEvent struct {
	material playback.MaterializedEventV2
	record   journal.RecoveryRecordV2
}

func fetchEvents(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, registry journal.EventRegistryV2, requests []eventRequest, watermark uint64, budgets BudgetsV2) ([]fetchedEvent, []journal.RecoveryRecordV2, error) {
	if len(requests) == 0 {
		return nil, nil, nil
	}
	keyBytes := 0
	for _, item := range requests {
		address, err := journal.NewJournalEventAddressV2(item.coordinate, item.version)
		if err != nil {
			return nil, nil, err
		}
		size := semanticAddressBytes(address)
		if size > budgets.MaximumKeyBytes-keyBytes {
			return nil, nil, budget("suffix_key_bytes")
		}
		keyBytes += size
	}
	workers := min(budgets.MaximumWorkers, len(requests))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tasks := make(chan eventRequest)
	results := make(chan fetchedEvent, len(requests))
	errorsOut := make(chan error, 1)
	var wg sync.WaitGroup
	var bytesMu sync.Mutex
	bytesUsed := 0
	worker := func() {
		defer wg.Done()
		for request := range tasks {
			record, event, err := exactEvent(ctx, reader, attempt, registry, request.coordinate, request.version, watermark, attempt.CurrentRevision())
			if err != nil {
				select {
				case errorsOut <- err:
					cancel()
				default:
				}
				continue
			}
			valueSize := record.CanonicalSizeV2()
			bytesMu.Lock()
			if valueSize > budgets.MaximumValueBytes-bytesUsed {
				bytesMu.Unlock()
				select {
				case errorsOut <- budget("suffix_bytes"):
					cancel()
				default:
				}
				continue
			}
			bytesUsed += valueSize
			bytesMu.Unlock()
			material, err := playback.NewMaterializedEventV2(record.RecordRevision(), event, request.terminalTombstone)
			if err != nil {
				select {
				case errorsOut <- err:
					cancel()
				default:
				}
				continue
			}
			select {
			case results <- fetchedEvent{material, record}:
			case <-ctx.Done():
				return
			}
		}
	}
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}
	go func() {
		defer close(tasks)
		for _, request := range requests {
			select {
			case tasks <- request:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(results)
	select {
	case err := <-errorsOut:
		return nil, nil, err
	default:
	}
	fetched := make([]fetchedEvent, 0, len(requests))
	records := make([]journal.RecoveryRecordV2, 0, len(requests))
	observedBytes := 0
	for item := range results {
		valueSize := item.record.CanonicalSizeV2()
		if valueSize > budgets.MaximumValueBytes-observedBytes {
			return nil, nil, budget("suffix_bytes")
		}
		observedBytes += valueSize
		fetched = append(fetched, item)
		records = append(records, item.record)
	}
	if len(fetched) != len(requests) {
		return nil, nil, corrupt("missing_event")
	}
	sort.Slice(fetched, func(i, k int) bool { return compareEvents(fetched[i].material, fetched[k].material) < 0 })
	return fetched, records, nil
}

func validateMembership(ctx context.Context, reader ReaderV2, attempt journal.StoreRevisionV2, fetched []fetchedEvent, operations map[journal.OperationID]operationCurrent, boundaryStates map[journal.OperationID]journal.OperationStateV2, prefix journal.Digest, watermark uint64, budgets BudgetsV2) ([]playback.MaterializedEventV2, map[journal.OperationID]journal.OperationStateV2, journal.Digest, []journal.RecoveryRecordV2, error) {
	type group struct {
		events           []fetchedEvent
		revision         uint64
		operationID      journal.OperationID
		operationVersion journal.LogicalVersion
		state            journal.OperationStateV2
	}
	groups := map[journal.PublicationID]*group{}
	for _, item := range fetched {
		e := item.material.Envelope()
		g := groups[e.PublicationID()]
		if g == nil {
			g = &group{revision: item.material.RecordRevision(), operationID: e.OperationID(), operationVersion: e.OperationVersion()}
			groups[e.PublicationID()] = g
		}
		if g.revision != item.material.RecordRevision() || g.operationID != e.OperationID() || g.operationVersion != e.OperationVersion() {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_group")
		}
		g.events = append(g.events, item)
	}
	orderedGroups := make([]struct {
		id    journal.PublicationID
		group *group
	}, 0, len(groups))
	for id, g := range groups {
		orderedGroups = append(orderedGroups, struct {
			id    journal.PublicationID
			group *group
		}{id, g})
	}
	sort.Slice(orderedGroups, func(i, k int) bool {
		if orderedGroups[i].group.revision != orderedGroups[k].group.revision {
			return orderedGroups[i].group.revision < orderedGroups[k].group.revision
		}
		return orderedGroups[i].id < orderedGroups[k].id
	})
	states := make(map[journal.OperationID]journal.OperationStateV2, len(boundaryStates))
	for id, state := range boundaryStates {
		states[id] = state
	}
	witnessRecords := make([]journal.RecoveryRecordV2, 0, len(groups))
	result := make([]playback.MaterializedEventV2, 0, len(fetched))
	lastRevision := watermark
	for _, entry := range orderedGroups {
		id, g := entry.id, entry.group
		if g.revision <= lastRevision {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_revision")
		}
		lastRevision = g.revision
		sort.Slice(g.events, func(i, k int) bool {
			return g.events[i].material.Envelope().EventIndex() < g.events[k].material.Envelope().EventIndex()
		})
		if len(g.events) == 0 || len(g.events) > journal.MaxEventsPerPublication {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_membership")
		}
		ids := make([]journal.EventID, len(g.events))
		var opEvent journal.EventEnvelopeV2
		seenCoordinates := map[string]bool{}
		for index, item := range g.events {
			e := item.material.Envelope()
			if int(e.EventCount()) != len(g.events) || int(e.EventIndex()) != index {
				return nil, nil, journal.Digest{}, nil, corrupt("event_indices")
			}
			key := coordinateKey(e.Coordinate())
			if seenCoordinates[key] {
				return nil, nil, journal.Digest{}, nil, corrupt("duplicate_aggregate")
			}
			seenCoordinates[key] = true
			ids[index] = e.EventID()
			if e.Family() == "operation" {
				if opEvent.EventID() != "" {
					return nil, nil, journal.Digest{}, nil, corrupt("operation_event")
				}
				opEvent = e
			}
			result = append(result, item.material)
		}
		if opEvent.EventID() == "" || opEvent.OperationID() != g.operationID || opEvent.OperationVersion() != g.operationVersion {
			return nil, nil, journal.Digest{}, nil, corrupt("operation_event")
		}
		state, err := journal.ReconstructOperationStateFromEventV2(opEvent)
		if err != nil {
			return nil, nil, journal.Digest{}, nil, err
		}
		if state.OperationID() != g.operationID || state.LogicalVersion() != g.operationVersion || !equalEventIDs(state.EventIDs(), ids, states[g.operationID].EventIDs()) {
			return nil, nil, journal.Digest{}, nil, corrupt("operation_membership")
		}
		if prior, ok := states[g.operationID]; ok {
			if _, err := journal.AdvanceOperationState(prior, journal.OperationTransition{ExpectedVersion: prior.LogicalVersion(), NextVersion: state.LogicalVersion(), Status: state.Status(), ResultKind: state.ResultKind(), ResultSchemaVersion: state.ResultSchemaVersion(), CanonicalResult: state.Result(), EventIDs: state.EventIDs(), EffectIDs: state.EffectIDs(), LastPublicationID: state.LastPublicationID()}); err != nil {
				return nil, nil, journal.Digest{}, nil, corrupt("operation_transition")
			}
		}
		states[g.operationID] = state
		g.state = state
		address, _ := id.WitnessAddressV2()
		request, _ := journal.NewReadRequestV2(address, attempt)
		record, err := reader.ReadV2(ctx, request)
		if err != nil {
			if journal.IsKind(err, journal.ErrorNotFound) {
				return nil, nil, journal.Digest{}, nil, corrupt("missing_publication_witness")
			}
			return nil, nil, journal.Digest{}, nil, err
		}
		if record.Address() != address || record.ExpiresAt() != "" || record.RecordRevision() != g.revision {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_witness_record")
		}
		witness, err := journal.DecodePublicationWitnessRecordV2(record)
		if err != nil {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_witness_codec")
		}
		if journal.ValidatePublicationWitnessV2(witness) != nil {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_witness_digest")
		}
		if witness.PublicationID() != id || witness.OperationID() != g.operationID || witness.OperationLogicalVersion() != g.operationVersion {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_witness_coordinate")
		}
		if witness.AuthorityRecordCount() < uint32(2*len(g.events)) {
			return nil, nil, journal.Digest{}, nil, corrupt("publication_witness_membership")
		}
		prefix, err = journal.AdvanceProjectionPrefixV2(prefix, g.revision, id, witness.PlanDigest(), witness.AuthorityRecordSetDigest())
		if err != nil {
			return nil, nil, journal.Digest{}, nil, err
		}
		witnessRecords = append(witnessRecords, record)
	}
	for id, current := range operations {
		if state, ok := states[id]; !ok || !sameOperation(state, current.state) {
			return nil, nil, journal.Digest{}, nil, corrupt("terminal_operation")
		}
	}
	return result, states, prefix, witnessRecords, nil
}

func validateTerminal(result playback.ResultV2, aggregates map[string]aggregateCurrent, operations map[journal.OperationID]operationCurrent, operationStates map[journal.OperationID]journal.OperationStateV2) error {
	states := result.States()
	seen := map[string]bool{}
	for _, state := range states {
		if state.Coordinate().Kind == journal.AggregateKind("operation") {
			continue
		}
		key := coordinateKey(state.Coordinate())
		head, ok := aggregates[key]
		if !ok || state.LogicalVersion() != head.head.LogicalVersion() || state.LastEventID() != head.head.LastEventID() || state.StateDigest() != head.head.CanonicalStateDigest() || state.Tombstone() != head.head.Tombstone() || state.LastPublicationID() != "" && state.LastPublicationID() != head.head.LastPublicationID() {
			return corrupt("terminal_head")
		}
		seen[key] = true
	}
	if len(seen) != len(aggregates) {
		return corrupt("terminal_census")
	}
	for id, current := range operations {
		if state, ok := operationStates[id]; !ok || !sameOperation(state, current.state) {
			return corrupt("terminal_operation")
		}
	}
	return nil
}

func sameOperation(a, b journal.OperationStateV2) bool {
	return journal.EqualOperationStatesV2(a, b)
}
func equalEventIDs(current, group, prior []journal.EventID) bool {
	if len(current) != len(prior)+len(group) {
		return false
	}
	want := append(append([]journal.EventID(nil), prior...), group...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	got := append([]journal.EventID(nil), current...)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}
func operationCoordinate(id journal.OperationID) journal.AggregateCoordinate {
	return journal.AggregateCoordinate{Kind: journal.AggregateKind("operation"), ID: journal.AggregateID(id), Scope: journal.Scope{Kind: journal.ScopeHost}}
}
func coordinateKey(c journal.AggregateCoordinate) string {
	value, _ := journal.CanonicalAggregateCoordinate(c)
	return string(value)
}
func compareEvents(a, b playback.MaterializedEventV2) int {
	if a.RecordRevision() < b.RecordRevision() {
		return -1
	}
	if a.RecordRevision() > b.RecordRevision() {
		return 1
	}
	if a.Envelope().EventIndex() < b.Envelope().EventIndex() {
		return -1
	}
	if a.Envelope().EventIndex() > b.Envelope().EventIndex() {
		return 1
	}
	return bytes.Compare([]byte(a.Envelope().EventID()), []byte(b.Envelope().EventID()))
}
func retryable(err error) bool {
	return journal.IsKind(err, journal.ErrorRevisionMoved) || journal.IsKind(err, journal.ErrorStaleCursor) || errors.Is(err, context.DeadlineExceeded)
}
func validateBudgets(b BudgetsV2) error {
	if b.MaximumRestarts < 1 || b.MaximumRestarts > 16 || b.MaximumPages < 1 || b.MaximumPages > 1024 || b.MaximumRecords < 1 || b.MaximumRecords > 100_000 || b.MaximumValueBytes < 1 || b.MaximumValueBytes > 512*1024*1024 || b.MaximumKeyBytes < 1 || b.MaximumKeyBytes > 64*1024*1024 || b.MaximumSuffixEvents < 1 || b.MaximumSuffixEvents > playback.MaxPlaybackEvents || b.MaximumWorkers < 1 || b.MaximumWorkers > 64 || b.MaximumDuration <= 0 || b.MaximumDuration > 5*time.Minute {
		return budget("configuration")
	}
	return nil
}
func corrupt(field string) error { return &Error{Kind: ErrorJournalCorrupt, Field: field} }
func unstable(field string) error {
	return &journal.Error{Kind: journal.ErrorRevisionMoved, Field: field}
}
func budget(field string) error { return &Error{Kind: ErrorBudgetExceeded, Field: field} }
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func sealResult(r ResultV2) journal.Digest {
	hash := sha256.New()
	_, _ = hash.Write([]byte("spec-kitty.restore-result.v2"))
	_, _ = hash.Write([]byte{0})
	for _, value := range []string{string(r.attempt.DatabaseID()), string(r.hostAuthorityID), string(r.priorEpochID), r.attemptID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], r.attempt.CurrentRevision())
	_, _ = hash.Write(revision[:])
	d := r.playback.CanonicalProjectionDigest()
	_, _ = hash.Write(d[:])
	_, _ = hash.Write(r.rollingPrefix[:])
	var result journal.Digest
	copy(result[:], hash.Sum(nil))
	return result
}
