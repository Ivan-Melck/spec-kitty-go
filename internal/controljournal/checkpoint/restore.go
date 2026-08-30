package checkpoint

import (
	"context"
	"errors"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

var ErrStaleScanV1 = errors.New("checkpoint restore scan stale")

type JournalScanPageV1 struct {
	ObservedRevision uint64
	Records          [MaxScanPageRecords]controljournal.StoredEventV1
	RecordCount      uint16
	Cursor           string
	Truncated        bool
	RawBytes         uint64
}

// RestoreReaderV1 is a read-only product seam implemented by composition.
// Its local values keep the pure checkpoint package independent of providers.
type RestoreReaderV1 interface {
	CheckpointHeadsV1(context.Context) ([]CheckpointHeadV1, error)
	CheckpointBlobV1(context.Context, CheckpointID) ([]byte, error)
	JournalPageV1(context.Context, WatermarkV1, string) (JournalScanPageV1, error)
}

type RestoreInputV1 struct {
	Reader        RestoreReaderV1
	Source        SourceIdentityV1
	EventRegistry controljournal.EventRegistryV1
	Reducers      reducer.RegistryV1
}

type RestoreObservationV1 struct {
	ScanRecords   uint64
	ScanBytes     uint64
	EventCount    uint64
	ScanPages     uint32
	StaleRestarts uint32
}

type RestoreResultV1 struct {
	Ready                     bool
	SelectedCheckpoint        *CheckpointHeadV1
	Projection                reducer.ProjectionV1
	Authority                 AuthoritySummaryV1
	TerminalWatermark         WatermarkV1
	CanonicalProjectionDigest [32]byte
	Observation               RestoreObservationV1
}

func RestoreV1(ctx context.Context, input RestoreInputV1) (RestoreResultV1, error) {
	if input.Reader == nil {
		return RestoreResultV1{}, notReady(NotReadyAuthorityClosed, "restore_reader", "install a read-only journal source and retry startup")
	}
	if err := validateSource(input.Source); err != nil {
		return RestoreResultV1{}, notReady(NotReadyAuthorityClosed, "source_identity", "repair the admitted source identity")
	}
	heads, err := input.Reader.CheckpointHeadsV1(ctx)
	if err != nil {
		return RestoreResultV1{}, notReady(NotReadyAuthorityClosed, "checkpoint_heads", "preserve the store and retry the read")
	}
	if len(heads) > MaxCheckpointAggregates {
		return RestoreResultV1{}, notReady(NotReadyReplayBound, "checkpoint_candidates", "reduce the explicit candidate set through reviewed policy")
	}
	sort.Slice(heads, func(i, j int) bool {
		if heads[i].ThroughRecordRevision != heads[j].ThroughRecordRevision {
			return heads[i].ThroughRecordRevision > heads[j].ThroughRecordRevision
		}
		return heads[i].CheckpointID > heads[j].CheckpointID
	})
	var staleRestarts uint32
	if len(heads) == 0 {
		events, observation, scanErr := scanJournal(ctx, input.Reader, WatermarkV1{}, &staleRestarts)
		if scanErr != nil {
			return RestoreResultV1{}, scanErr
		}
		journal, validateErr := controljournal.ValidateJournalV1(input.EventRegistry, events)
		if validateErr != nil {
			return RestoreResultV1{}, notReady(NotReadyJournalCorrupt, "journal", "preserve the store and repair or restore an approved backup")
		}
		terminal, watermarkErr := extendWatermark(WatermarkV1{}, journal, "")
		if watermarkErr != nil || terminal.EventCount == 0 {
			return RestoreResultV1{}, notReady(NotReadyJournalCorrupt, "journal_watermark", "preserve the store and inspect the committed journal")
		}
		projection, applyErr := reducer.ApplyV1(input.Reducers, reducer.EmptyProjectionV1(), journal)
		if applyErr != nil {
			return RestoreResultV1{}, notReady(NotReadyJournalCorrupt, "journal_reduction", "install the exact reducer set or restore compatible history")
		}
		authority, authorityErr := deriveAuthority(input.Source.RunID, AuthoritySummaryV1{}, journal)
		if authorityErr != nil {
			return RestoreResultV1{}, notReady(NotReadyJournalCorrupt, "authority_summary", "inspect the authority event history")
		}
		return readyResult(nil, projection, authority, terminal, observation), nil
	}

	for index := range heads {
		head := heads[index]
		record, projection, selectErr := selectCheckpoint(ctx, input, head)
		if selectErr != nil {
			continue
		}
		events, observation, scanErr := scanJournal(ctx, input.Reader, record.Watermark, &staleRestarts)
		if scanErr != nil {
			var notReadyError NotReadyErrorV1
			if errors.As(scanErr, &notReadyError) && (notReadyError.Kind == NotReadyReplayBound || notReadyError.Kind == NotReadyStoreUnstable) {
				return RestoreResultV1{}, scanErr
			}
			continue
		}
		suffix, suffixErr := controljournal.ValidateJournalV1(input.EventRegistry, events)
		if suffixErr != nil {
			continue
		}
		terminal, terminalErr := extendWatermark(record.Watermark, suffix, "")
		if terminalErr != nil {
			continue
		}
		restored := projection
		length, lengthErr := suffix.Len()
		if lengthErr != nil {
			continue
		}
		if length > 0 {
			restored, suffixErr = reducer.ApplyV1(input.Reducers, projection, suffix)
			if suffixErr != nil {
				continue
			}
		}
		selected := head
		authority, authorityErr := deriveAuthority(input.Source.RunID, record.Authority, suffix)
		if authorityErr != nil {
			continue
		}
		return readyResult(&selected, restored, authority, terminal, observation), nil
	}
	return RestoreResultV1{}, notReady(NotReadyInvalidCheckpoint, "checkpoint_candidates", "inspect immutable invalid candidates or provide an explicitly eligible earlier checkpoint")
}

func scanJournal(ctx context.Context, reader RestoreReaderV1, after WatermarkV1, staleRestarts *uint32) ([]controljournal.StoredEventV1, RestoreObservationV1, error) {
	if staleRestarts == nil {
		return nil, RestoreObservationV1{}, notReady(NotReadyAuthorityClosed, "stale_restart_counter", "supply startup-scoped recovery accounting")
	}
	for {
		events := make([]controljournal.StoredEventV1, 0, 512)
		observation := RestoreObservationV1{StaleRestarts: *staleRestarts}
		cursor := ""
		var observed uint64
		for {
			if observation.ScanPages == MaxRestorePages {
				return nil, observation, notReady(NotReadyReplayBound, "scan_pages", "publish an eligible checkpoint or raise the reviewed restore bound")
			}
			page, err := reader.JournalPageV1(ctx, after, cursor)
			if errors.Is(err, ErrStaleScanV1) {
				if *staleRestarts == MaxStaleRestarts {
					return nil, observation, notReady(NotReadyStoreUnstable, "stale_snapshot_restarts", "hold command admission closed and stabilize the owner")
				}
				*staleRestarts++
				break
			}
			if err != nil {
				return nil, observation, notReady(NotReadyAuthorityClosed, "journal_scan", "preserve the store and retry the bounded read")
			}
			if page.ObservedRevision == 0 || observed != 0 && page.ObservedRevision != observed {
				if *staleRestarts == MaxStaleRestarts {
					return nil, observation, notReady(NotReadyStoreUnstable, "observed_revision", "hold command admission closed and stabilize the owner")
				}
				*staleRestarts++
				break
			}
			observed = page.ObservedRevision
			observation.ScanPages++
			if page.RecordCount > MaxScanPageRecords {
				return nil, observation, notReady(NotReadyReplayBound, "scan_page_records", "enforce the admitted fixed page capacity")
			}
			if page.RecordCount == 0 && (page.Truncated || page.Cursor != "") {
				return nil, observation, notReady(NotReadyJournalCorrupt, "scan_progress", "inspect the provider cursor response")
			}
			var measuredRaw uint64
			for index := uint16(0); index < page.RecordCount; index++ {
				record := page.Records[index]
				encoded, encodeErr := controljournal.EncodeEventEnvelopeV1(record.Envelope)
				if encodeErr != nil {
					return nil, observation, notReady(NotReadyJournalCorrupt, "event_envelope", "preserve the store and inspect the corrupt event")
				}
				itemBytes := uint64(len(encoded))
				if itemBytes > MaxRestoreBytes-measuredRaw {
					return nil, observation, notReady(NotReadyReplayBound, "scan_page_bytes", "publish a bounded checkpoint")
				}
				measuredRaw += itemBytes
			}
			if page.RawBytes < measuredRaw {
				return nil, observation, notReady(NotReadyJournalCorrupt, "scan_raw_bytes", "inspect the provider byte accounting")
			}
			if page.RawBytes > MaxRestoreBytes-observation.ScanBytes {
				return nil, observation, notReady(NotReadyReplayBound, "scan_bytes", "publish an eligible checkpoint or raise the reviewed restore bound")
			}
			observation.ScanBytes += page.RawBytes
			if uint64(page.RecordCount) > MaxRestoreEvents-observation.ScanRecords {
				return nil, observation, notReady(NotReadyReplayBound, "scan_records", "publish an eligible checkpoint or raise the reviewed restore bound")
			}
			observation.ScanRecords += uint64(page.RecordCount)
			for index := uint16(0); index < page.RecordCount; index++ {
				record := page.Records[index]
				if observation.EventCount == MaxRestoreEvents {
					return nil, observation, notReady(NotReadyReplayBound, "events", "publish an eligible checkpoint or raise the reviewed restore bound")
				}
				if record.RecordRevision <= after.ThroughRecordRevision {
					return nil, observation, notReady(NotReadyJournalCorrupt, "suffix_revision", "inspect the provider after-watermark scan")
				}
				events = append(events, record)
				observation.EventCount++
			}
			if !page.Truncated {
				if page.Cursor != "" {
					return nil, observation, notReady(NotReadyJournalCorrupt, "terminal_cursor", "inspect the provider cursor response")
				}
				return events, observation, nil
			}
			if page.Cursor == "" || page.Cursor == cursor {
				return nil, observation, notReady(NotReadyJournalCorrupt, "scan_cursor", "inspect the provider cursor response")
			}
			cursor = page.Cursor
		}
	}
}

func selectCheckpoint(ctx context.Context, input RestoreInputV1, head CheckpointHeadV1) (CheckpointRecordV1, reducer.ProjectionV1, error) {
	if err := validateHead(head); err != nil {
		return CheckpointRecordV1{}, reducer.ProjectionV1{}, err
	}
	blob, err := input.Reader.CheckpointBlobV1(ctx, head.CheckpointID)
	if err != nil || len(blob) == 0 || len(blob) > MaxCheckpointBytes || digestBytes(blob) != head.CheckpointRecordDigest {
		return CheckpointRecordV1{}, reducer.ProjectionV1{}, errors.New("invalid checkpoint blob")
	}
	record, err := DecodeRecordV1(blob)
	if err != nil {
		return CheckpointRecordV1{}, reducer.ProjectionV1{}, err
	}
	if record.CheckpointID != head.CheckpointID || record.Watermark.ThroughRecordRevision != head.ThroughRecordRevision || record.Watermark.ThroughEventID != head.ThroughEventID || record.ProjectID != input.Source.ProjectID || record.MissionID != input.Source.MissionID || record.Authority.RunID != input.Source.RunID || record.ReducerSetDigest != input.Source.ReducerSetDigest || record.EventSchemaSetDigest != input.Source.EventSchemaSetDigest {
		return CheckpointRecordV1{}, reducer.ProjectionV1{}, errors.New("checkpoint binding mismatch")
	}
	projection, err := projectionFromAggregates(record.Aggregates)
	if err != nil || projection.GlobalDigest() != record.CanonicalProjectionDigest {
		return CheckpointRecordV1{}, reducer.ProjectionV1{}, errors.New("checkpoint projection mismatch")
	}
	return record, projection, nil
}

func readyResult(head *CheckpointHeadV1, projection reducer.ProjectionV1, authority AuthoritySummaryV1, watermark WatermarkV1, observation RestoreObservationV1) RestoreResultV1 {
	return RestoreResultV1{Ready: true, SelectedCheckpoint: head, Projection: projection, Authority: authority, TerminalWatermark: watermark, CanonicalProjectionDigest: projection.GlobalDigest(), Observation: observation}
}

func deriveAuthority(runID controljournal.RunID, initial AuthoritySummaryV1, journal controljournal.ValidatedJournalV1) (AuthoritySummaryV1, error) {
	summary := initial
	if summary.RunID == "" {
		summary.RunID = runID
	}
	length, err := journal.Len()
	if err != nil {
		return AuthoritySummaryV1{}, err
	}
	for index := 0; index < length; index++ {
		record, atErr := journal.At(index)
		if atErr != nil {
			return AuthoritySummaryV1{}, atErr
		}
		summary.RuntimeEpochID = record.Envelope.RuntimeEpochID
		summary.DurableRevision = record.RecordRevision
		if record.Envelope.LeaseID != "" {
			summary.LeaseID = record.Envelope.LeaseID
			summary.LeaseGeneration = record.Envelope.LeaseGeneration
			summary.FenceToken = record.Envelope.FenceToken
		}
	}
	if summary.RunID != runID || summary.RuntimeEpochID == "" || summary.DurableRevision == 0 {
		return AuthoritySummaryV1{}, errors.New("authority summary incomplete")
	}
	return summary, nil
}
func notReady(kind NotReadyKindV1, field, recovery string) NotReadyErrorV1 {
	return NotReadyErrorV1{Kind: kind, Field: field, Recovery: recovery}
}

func BindRuntimeEpochV1(restored RestoreResultV1, runID controljournal.RunID, predecessor, admitted controljournal.RuntimeEpochID, durableRevision uint64) (EpochBindingV1, error) {
	if !restored.Ready {
		return EpochBindingV1{}, notReady(NotReadyAuthorityClosed, "restore", "complete product restore before admitting an epoch")
	}
	if _, err := controljournal.NewRunID(string(runID)); err != nil {
		return EpochBindingV1{}, err
	}
	if _, err := controljournal.NewRuntimeEpochID(string(predecessor)); err != nil {
		return EpochBindingV1{}, err
	}
	if _, err := controljournal.NewRuntimeEpochID(string(admitted)); err != nil {
		return EpochBindingV1{}, err
	}
	if restored.Authority.RunID != "" && restored.Authority.RunID != runID {
		return EpochBindingV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "runtime_run"}
	}
	if restored.Authority.RuntimeEpochID != "" && restored.Authority.RuntimeEpochID != predecessor {
		return EpochBindingV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "predecessor_epoch"}
	}
	if predecessor == admitted || durableRevision == 0 {
		return EpochBindingV1{}, controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "runtime_epoch"}
	}
	return EpochBindingV1{RunID: runID, RuntimeEpochID: admitted, PredecessorEpochID: predecessor, DurableRevision: durableRevision}, nil
}

func ValidateCapsuleEpochV1(binding EpochBindingV1, capsule AuthorityCapsuleV1) error {
	if capsule.RunID != binding.RunID || capsule.RuntimeEpochID != binding.RuntimeEpochID || capsule.RuntimeEpochID == binding.PredecessorEpochID || capsule.DurableRevision != binding.DurableRevision {
		return controljournal.ValidationError{Kind: controljournal.RefusalInvalidTransition, Field: "runtime_epoch_capsule"}
	}
	return nil
}

// LeaseExpiredV1 records no epoch transition: lease generation is an
// independent authority coordinate.
func LeaseExpiredV1(binding EpochBindingV1) EpochBindingV1 { return binding }
