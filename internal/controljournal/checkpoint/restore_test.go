package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/playback"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/reducer"
)

type restoreReader struct {
	heads       []CheckpointHeadV1
	blobs       map[CheckpointID][]byte
	pages       []JournalScanPageV1
	pageIndex   int
	headCalls   int
	staleCalls  int
	staleAtPage int
	staleOnce   bool
	staleDone   bool
	starts      []WatermarkV1
}

func (r *restoreReader) CheckpointHeadsV1(context.Context) ([]CheckpointHeadV1, error) {
	r.headCalls++
	return append([]CheckpointHeadV1(nil), r.heads...), nil
}

func TestRestoreCheckpointOnlyBindsActualRegistriesBeforeRead(t *testing.T) {
	first := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	checkpoint := buildFixture(t, []controljournal.StoredEventV1{first}, []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, "checkpoint-registry")
	wrongEvents, err := controljournal.NewEventRegistryV1([]controljournal.EventVersionV1{
		{EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1"},
		{EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrongReducers, err := reducer.NewRegistryV1([]reducer.EntryV1{
		{AggregateKind: "mission", EventType: "mission.updated", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
		{AggregateKind: "mission", EventType: "mission.created", EventSchemaVersion: 1, ReducerVersion: "mission-v1", Program: reducer.ProgramReplaceWithCanonicalEventPayloadV1},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*RestoreInputV1){
		"event registry":   func(input *RestoreInputV1) { input.EventRegistry = wrongEvents },
		"reducer registry": func(input *RestoreInputV1) { input.Reducers = wrongReducers },
	} {
		t.Run(name, func(t *testing.T) {
			reader := &restoreReader{heads: []CheckpointHeadV1{checkpoint.Head}, blobs: map[CheckpointID][]byte{checkpoint.Head.CheckpointID: checkpoint.RecordBytes}, pages: []JournalScanPageV1{encodedPage(t, nil)}}
			input := restoreInput(reader, t)
			mutate(&input)
			result, err := RestoreV1(context.Background(), input)
			if err == nil || result.Ready || reader.headCalls != 0 {
				t.Fatalf("registry mismatch read checkpoint result=%+v head_calls=%d err=%v", result, reader.headCalls, err)
			}
		})
	}
}
func (r *restoreReader) CheckpointBlobV1(_ context.Context, id CheckpointID) ([]byte, error) {
	value, ok := r.blobs[id]
	if !ok {
		return nil, errors.New("missing")
	}
	return append([]byte(nil), value...), nil
}
func (r *restoreReader) JournalPageV1(_ context.Context, after WatermarkV1, cursor string) (JournalScanPageV1, error) {
	if cursor == "" {
		r.pageIndex = 0
		r.starts = append(r.starts, after)
	}
	if r.staleCalls > 0 {
		r.staleCalls--
		r.pageIndex = 0
		return JournalScanPageV1{}, ErrStaleScanV1
	}
	if r.staleOnce && !r.staleDone && r.pageIndex == r.staleAtPage {
		r.staleDone = true
		r.pageIndex = 0
		return JournalScanPageV1{}, ErrStaleScanV1
	}
	if r.pageIndex >= len(r.pages) {
		return JournalScanPageV1{}, fmt.Errorf("unexpected cursor %q", cursor)
	}
	page := r.pages[r.pageIndex]
	r.pageIndex++
	return page, nil
}

func encodedPages(t *testing.T, records []controljournal.StoredEventV1) []JournalScanPageV1 {
	t.Helper()
	if len(records) == 0 {
		return []JournalScanPageV1{encodedPage(t, nil)}
	}
	pages := make([]JournalScanPageV1, 0, (len(records)+MaxScanPageRecords-1)/MaxScanPageRecords)
	for start := 0; start < len(records); start += MaxScanPageRecords {
		end := start + MaxScanPageRecords
		if end > len(records) {
			end = len(records)
		}
		page := encodedPage(t, records[start:end])
		page.Truncated = end < len(records)
		if page.Truncated {
			page.Cursor = fmt.Sprintf("page-%d", len(pages)+1)
		}
		pages = append(pages, page)
	}
	return pages
}

func encodedPage(t *testing.T, records []controljournal.StoredEventV1) JournalScanPageV1 {
	t.Helper()
	if len(records) > MaxScanPageRecords {
		t.Fatalf("test page records=%d exceeds fixed capacity", len(records))
	}
	page := JournalScanPageV1{ObservedRevision: 100, RecordCount: uint16(len(records))}
	for index, record := range records {
		encoded, err := controljournal.EncodeEventEnvelopeV1(record.Envelope)
		if err != nil {
			t.Fatal(err)
		}
		page.Records[index] = record
		page.RawBytes += uint64(len(encoded))
	}
	return page
}

func restoreInput(reader RestoreReaderV1, t *testing.T) RestoreInputV1 {
	return RestoreInputV1{Reader: reader, Source: testSource(t), EventRegistry: testEventRegistry(t), Reducers: testReducers(t)}
}

func TestRestoreSuffixOnlyCheckpointOnlyAndCheckpointPlusSuffix(t *testing.T) {
	first := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	second := testStoredEvent(t, 8, "event-b", "operation-b", "aggregate-a", 2, 0, 1, `{"value":2}`)
	checkpoint := buildFixture(t, []controljournal.StoredEventV1{first}, []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, "checkpoint-1")
	for _, test := range []struct {
		name           string
		heads          []CheckpointHeadV1
		blobs          map[CheckpointID][]byte
		records        []controljournal.StoredEventV1
		scanRecords    []controljournal.StoredEventV1
		wantCheckpoint bool
	}{
		{name: "suffix-only", records: []controljournal.StoredEventV1{first, second}, scanRecords: []controljournal.StoredEventV1{first, second}},
		{name: "checkpoint-only", heads: []CheckpointHeadV1{checkpoint.Head}, blobs: map[CheckpointID][]byte{"checkpoint-1": checkpoint.RecordBytes}, records: []controljournal.StoredEventV1{first}, wantCheckpoint: true},
		{name: "checkpoint-plus-suffix", heads: []CheckpointHeadV1{checkpoint.Head}, blobs: map[CheckpointID][]byte{"checkpoint-1": checkpoint.RecordBytes}, records: []controljournal.StoredEventV1{first, second}, scanRecords: []controljournal.StoredEventV1{second}, wantCheckpoint: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &restoreReader{heads: test.heads, blobs: test.blobs, pages: []JournalScanPageV1{encodedPage(t, test.scanRecords)}}
			result, err := RestoreV1(context.Background(), restoreInput(reader, t))
			if err != nil {
				t.Fatal(err)
			}
			if !result.Ready || (result.SelectedCheckpoint != nil) != test.wantCheckpoint || result.Observation.EventCount != uint64(len(test.scanRecords)) {
				t.Fatalf("result=%+v", result)
			}
			if len(reader.starts) == 0 {
				t.Fatal("restore never opened a journal scan")
			}
			if test.wantCheckpoint && reader.starts[0] != checkpoint.Record.Watermark {
				t.Fatalf("checkpoint restore scanned from %+v want %+v", reader.starts[0], checkpoint.Record.Watermark)
			}
			if !test.wantCheckpoint && reader.starts[0] != (WatermarkV1{}) {
				t.Fatalf("full replay did not scan from origin: %+v", reader.starts[0])
			}
			if result.CanonicalProjectionDigest == [32]byte{} {
				t.Fatal("missing restored digest")
			}
			journal := testJournal(t, test.records...)
			played, playbackErr := playback.RunV1(playback.InputV1{Journal: journal, Reducers: testReducers(t)})
			if playbackErr != nil {
				t.Fatal(playbackErr)
			}
			if err := playback.CompareDigestV1(result.CanonicalProjectionDigest, played); err != nil {
				t.Fatalf("restore/playback mismatch: %v", err)
			}
		})
	}
}

func TestRestoreCheckpointPrefixMayExceedSuffixBoundsWithoutPrefixIO(t *testing.T) {
	last := MaxRestoreEvents + 1
	records := make([]controljournal.StoredEventV1, last)
	for index := range records {
		number := index + 1
		records[index] = testStoredEvent(t, uint64(number), controljournalToken("event-prefix", number), controljournalToken("operation-prefix", number), "aggregate-a", uint64(number), 0, 1, fmt.Sprintf(`{"value":%d}`, number))
	}
	checkpoint := buildFixture(t, records, []AggregateSnapshotV1{snapshot(t, "aggregate-a", uint64(last), controljournalToken("event-prefix", last), fmt.Sprintf(`{"value":%d}`, last))}, "checkpoint-large-prefix")
	suffix := testStoredEvent(t, uint64(last+1), "event-suffix", "operation-suffix", "aggregate-a", uint64(last+1), 0, 1, fmt.Sprintf(`{"value":%d}`, last+1))
	reader := &restoreReader{heads: []CheckpointHeadV1{checkpoint.Head}, blobs: map[CheckpointID][]byte{checkpoint.Head.CheckpointID: checkpoint.RecordBytes}, pages: []JournalScanPageV1{encodedPage(t, []controljournal.StoredEventV1{suffix})}}
	result, err := RestoreV1(context.Background(), restoreInput(reader, t))
	if err != nil || !result.Ready {
		t.Fatalf("bounded suffix restore result=%+v err=%v", result, err)
	}
	if result.Observation.EventCount != 1 || len(reader.starts) != 1 || reader.starts[0] != checkpoint.Record.Watermark {
		t.Fatalf("prefix was read during suffix restore observation=%+v starts=%+v", result.Observation, reader.starts)
	}
	if result.TerminalWatermark.EventCount != uint64(MaxRestoreEvents+2) || result.TerminalWatermark.ThroughEventID != "event-suffix" {
		t.Fatalf("terminal watermark=%+v", result.TerminalWatermark)
	}
}

func controljournalToken(prefix string, number int) string {
	return fmt.Sprintf("%s-%d", prefix, number)
}

func TestRestoreExplicitEarlierFallbackAndAllInvalidRefusal(t *testing.T) {
	first := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	valid := buildFixture(t, []controljournal.StoredEventV1{first}, []AggregateSnapshotV1{snapshot(t, "aggregate-a", 1, "event-a", `{"value":1}`)}, "checkpoint-old")
	newest := valid.Head
	newest.CheckpointID = "checkpoint-new"
	newest.ThroughRecordRevision = 8
	newest.CheckpointRecordDigest[0] ^= 1
	reader := &restoreReader{heads: []CheckpointHeadV1{valid.Head, newest}, blobs: map[CheckpointID][]byte{"checkpoint-old": valid.RecordBytes, "checkpoint-new": []byte(`{"broken":true}`)}, pages: []JournalScanPageV1{encodedPage(t, nil)}}
	result, err := RestoreV1(context.Background(), restoreInput(reader, t))
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedCheckpoint == nil || result.SelectedCheckpoint.CheckpointID != "checkpoint-old" {
		t.Fatalf("did not choose explicit earlier checkpoint: %+v", result.SelectedCheckpoint)
	}

	reader = &restoreReader{heads: []CheckpointHeadV1{newest}, blobs: map[CheckpointID][]byte{"checkpoint-new": []byte(`{"broken":true}`)}, pages: []JournalScanPageV1{encodedPage(t, []controljournal.StoredEventV1{first})}}
	result, err = RestoreV1(context.Background(), restoreInput(reader, t))
	if err != nil || !result.Ready || result.SelectedCheckpoint != nil || len(reader.starts) != 1 || reader.starts[0] != (WatermarkV1{}) {
		t.Fatalf("bounded origin fallback result=%+v starts=%+v err=%v", result, reader.starts, err)
	}

	reader = &restoreReader{heads: []CheckpointHeadV1{newest}, blobs: map[CheckpointID][]byte{"checkpoint-new": []byte(`{"broken":true}`)}, pages: []JournalScanPageV1{encodedPage(t, nil)}}
	result, err = RestoreV1(context.Background(), restoreInput(reader, t))
	var notReadyError NotReadyErrorV1
	if result.Ready || !errors.As(err, &notReadyError) || notReadyError.Kind != NotReadyInvalidCheckpoint {
		t.Fatalf("all-invalid candidates became ready result=%+v err=%v", result, err)
	}
}

func TestRestoreDuplicateMissingAndCorruptEventsFailClosed(t *testing.T) {
	first := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 2, `{"value":1}`)
	badFirst := testStoredEvent(t, 7, "event-c", "operation-b", "aggregate-a", 2, 0, 2, `{"value":2}`)
	badSecond := testStoredEvent(t, 7, "event-d", "operation-b", "aggregate-a", 1, 1, 2, `{"value":1}`)
	cases := map[string][]controljournal.StoredEventV1{"duplicate": {first, first}, "missing": {first}, "invalid aggregate order": {badSecond, badFirst}}
	zeroRevision := testStoredEvent(t, 8, "event-e", "operation-e", "aggregate-a", 1, 0, 1, `{"value":1}`)
	zeroRevision.RecordRevision = 0
	cases["revision-fill-in"] = []controljournal.StoredEventV1{zeroRevision}
	for name, records := range cases {
		t.Run(name, func(t *testing.T) {
			reader := &restoreReader{pages: []JournalScanPageV1{encodedPage(t, records)}}
			result, err := RestoreV1(context.Background(), restoreInput(reader, t))
			if result.Ready || err == nil {
				t.Fatalf("corrupt history became ready result=%+v", result)
			}
		})
	}
}

func TestRestoreBoundExactAndPlusOneBeforeAppend(t *testing.T) {
	event := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	stored := encodedPage(t, []controljournal.StoredEventV1{event}).Records[0]
	encoded, err := controljournal.EncodeEventEnvelopeV1(stored.Envelope)
	if err != nil {
		t.Fatal(err)
	}

	page := JournalScanPageV1{ObservedRevision: 1, RecordCount: MaxScanPageRecords, RawBytes: MaxScanPageRecords * uint64(len(encoded))}
	for index := 0; index < MaxScanPageRecords; index++ {
		page.Records[index] = stored
	}
	pages := make([]JournalScanPageV1, MaxRestorePages)
	for index := range pages {
		pages[index] = page
		pages[index].Truncated = index+1 < len(pages)
		if pages[index].Truncated {
			pages[index].Cursor = fmt.Sprintf("event-bound-%d", index+1)
		}
	}
	reader := &restoreReader{pages: pages}
	stale := uint32(0)
	events, observation, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale)
	if err != nil || len(events) != MaxRestoreEvents || observation.EventCount != MaxRestoreEvents {
		t.Fatalf("exact event bound len=%d obs=%+v err=%v", len(events), observation, err)
	}
	plusPages := append([]JournalScanPageV1(nil), pages...)
	plusPages[len(plusPages)-1].Truncated = true
	plusPages[len(plusPages)-1].Cursor = "event-bound-plus"
	plusPages = append(plusPages, encodedPage(t, []controljournal.StoredEventV1{stored}))
	reader = &restoreReader{pages: plusPages}
	stale = 0
	if events, _, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil || events != nil {
		t.Fatalf("plus-one event bound returned partial len=%d err=%v", len(events), err)
	}

	exactPage := encodedPage(t, []controljournal.StoredEventV1{stored})
	exactPage.ObservedRevision = 1
	exactPage.RawBytes = MaxScanPageBytes
	reader = &restoreReader{pages: []JournalScanPageV1{exactPage}}
	stale = 0
	if _, observation, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err != nil || observation.ScanBytes != MaxScanPageBytes {
		t.Fatalf("exact page byte bound obs=%+v err=%v", observation, err)
	}
	plusBytePage := exactPage
	plusBytePage.RawBytes = MaxScanPageBytes + 1
	reader = &restoreReader{pages: []JournalScanPageV1{plusBytePage}}
	stale = 0
	if events, _, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil || events != nil {
		t.Fatalf("plus-one page byte bound returned partial len=%d err=%v", len(events), err)
	}

	totalPages := make([]JournalScanPageV1, MaxRestoreBytes/MaxScanPageBytes)
	for index := range totalPages {
		totalPages[index] = exactPage
		totalPages[index].Truncated = index+1 < len(totalPages)
		if totalPages[index].Truncated {
			totalPages[index].Cursor = fmt.Sprintf("total-%d", index+1)
		}
	}
	reader = &restoreReader{pages: totalPages}
	stale = 0
	if _, observation, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err != nil || observation.ScanBytes != MaxRestoreBytes {
		t.Fatalf("exact total byte bound obs=%+v err=%v", observation, err)
	}
	totalPages[len(totalPages)-1].Truncated = true
	totalPages[len(totalPages)-1].Cursor = "total-plus"
	plusTotal := encodedPage(t, []controljournal.StoredEventV1{stored})
	plusTotal.ObservedRevision = 1
	plusTotal.RawBytes = 1
	totalPages = append(totalPages, plusTotal)
	reader = &restoreReader{pages: totalPages}
	stale = 0
	if events, _, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil || events != nil {
		t.Fatalf("plus-one total byte bound returned partial len=%d err=%v", len(events), err)
	}
}

func TestRestoreFixedPageCountRejectsImpossibleCountAndIgnoresTail(t *testing.T) {
	event := testStoredEvent(t, 7, "event-tail", "operation-tail", "aggregate-a", 1, 0, 1, `{"value":1}`)
	impossible := JournalScanPageV1{ObservedRevision: 1, RecordCount: MaxScanPageRecords + 1}
	reader := &restoreReader{pages: []JournalScanPageV1{impossible}}
	stale := uint32(0)
	if events, _, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil || events != nil {
		t.Fatalf("impossible fixed-page count returned events=%d err=%v", len(events), err)
	}
	tail := JournalScanPageV1{ObservedRevision: 1, RecordCount: 0}
	tail.Records[0] = event
	reader = &restoreReader{pages: []JournalScanPageV1{tail}}
	stale = 0
	events, observation, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale)
	if err != nil || len(events) != 0 || observation.EventCount != 0 || observation.ScanRecords != 0 {
		t.Fatalf("unused array tail became journal authority events=%d obs=%+v err=%v", len(events), observation, err)
	}
}

func TestRestorePristineJournalThenFirstEpochAdmission(t *testing.T) {
	reader := &restoreReader{pages: []JournalScanPageV1{encodedPage(t, nil)}}
	restored, err := RestoreV1(context.Background(), restoreInput(reader, t))
	if err != nil || !restored.Ready || !restored.Pristine || restored.CommandAdmissionOpen ||
		restored.Authority != (AuthoritySummaryV1{}) || restored.TerminalWatermark != (WatermarkV1{}) ||
		restored.SelectedCheckpoint != nil || restored.CanonicalProjectionDigest == [32]byte{} {
		t.Fatalf("pristine restore=%+v err=%v", restored, err)
	}
	binding, err := BindRuntimeEpochV1(restored, "run-1", "", "epoch-first", 1)
	if err != nil || !binding.FirstAdmission || !binding.CommandAdmissionOpen || binding.PredecessorEpochID != "" {
		t.Fatalf("first binding=%+v err=%v", binding, err)
	}
	if err := ValidateCapsuleEpochV1(binding, epochTestCapsule("epoch-first", 1)); err != nil {
		t.Fatalf("first admitted capsule refused: %v", err)
	}
	if _, err := BindRuntimeEpochV1(restored, "run-1", "epoch-invented", "epoch-first", 1); err == nil {
		t.Fatal("pristine restore accepted an invented predecessor")
	}
}

func TestRestoreBoundPagesAndStaleRestartCap(t *testing.T) {
	event := testStoredEvent(t, 7, "event-a", "operation-a", "aggregate-a", 1, 0, 1, `{"value":1}`)
	base := encodedPage(t, []controljournal.StoredEventV1{event})
	pages := make([]JournalScanPageV1, MaxRestorePages)
	for index := range pages {
		pages[index] = base
		pages[index].Truncated = index+1 < len(pages)
		if pages[index].Truncated {
			pages[index].Cursor = fmt.Sprintf("cursor-%d", index+1)
		}
	}
	reader := &restoreReader{pages: pages}
	stale := uint32(0)
	if _, observation, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err != nil || observation.ScanPages != MaxRestorePages {
		t.Fatalf("exact page bound obs=%+v err=%v", observation, err)
	}
	pages = append(pages[:MaxRestorePages-1], append([]JournalScanPageV1{pages[MaxRestorePages-1]}, pages[MaxRestorePages-1])...)
	for index := range pages {
		pages[index].Truncated = index+1 < len(pages)
		pages[index].Cursor = ""
		if pages[index].Truncated {
			pages[index].Cursor = fmt.Sprintf("cursor-plus-%d", index+1)
		}
	}
	reader = &restoreReader{pages: pages}
	stale = 0
	if _, _, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil {
		t.Fatal("33rd scan page accepted")
	}

	reader = &restoreReader{pages: []JournalScanPageV1{base}, staleCalls: 2}
	stale = 0
	if _, observation, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err != nil || observation.StaleRestarts != 2 {
		t.Fatalf("two stale restarts obs=%+v err=%v", observation, err)
	}
	reader = &restoreReader{pages: []JournalScanPageV1{base}, staleCalls: 3}
	stale = 0
	if events, _, err := scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil || events != nil {
		t.Fatalf("third stale restart returned partial len=%d err=%v", len(events), err)
	}
}

func TestEpochBindingRefusesPredecessorAndLeaseExpiryDoesNotRotate(t *testing.T) {
	restored := RestoreResultV1{Ready: true, Authority: AuthoritySummaryV1{RunID: "run-1", RuntimeEpochID: "epoch-old", DurableRevision: 44}}
	binding, err := BindRuntimeEpochV1(restored, "run-1", "epoch-old", "epoch-new", 44)
	if err != nil {
		t.Fatal(err)
	}
	if binding.RuntimeEpochID == binding.PredecessorEpochID || binding.RunID != "run-1" || binding.DurableRevision != 44 {
		t.Fatalf("binding=%+v", binding)
	}
	old := epochTestCapsule("epoch-old", 44)
	if err := ValidateCapsuleEpochV1(binding, old); err == nil {
		t.Fatal("predecessor epoch capsule accepted")
	}
	current := epochTestCapsule("epoch-new", 44)
	if err := ValidateCapsuleEpochV1(binding, current); err != nil {
		t.Fatal(err)
	}
	if after := LeaseExpiredV1(binding); after != binding {
		t.Fatalf("lease expiry rotated epoch before=%+v after=%+v", binding, after)
	}
	if _, err := BindRuntimeEpochV1(restored, "run-1", "epoch-old", "epoch-old", 44); err == nil {
		t.Fatal("reused epoch accepted")
	}
	checkpointRestored := RestoreResultV1{Ready: true, Authority: AuthoritySummaryV1{RunID: "run-1", RuntimeEpochID: "epoch-old"}}
	if _, err := BindRuntimeEpochV1(checkpointRestored, "run-2", "epoch-old", "epoch-new", 44); err == nil {
		t.Fatal("owner restart escaped umbrella RunID")
	}
	if _, err := BindRuntimeEpochV1(checkpointRestored, "run-1", "epoch-wrong", "epoch-new", 44); err == nil {
		t.Fatal("wrong predecessor epoch admitted")
	}
}

func epochTestCapsule(epoch controljournal.RuntimeEpochID, revision uint64) controljournal.AuthorityCapsuleV1 {
	return controljournal.AuthorityCapsuleV1{
		SchemaVersion: 1, ProjectID: "project-1", MissionID: "mission-1",
		WorkRevisionID: "revision-1", ActorID: "actor-1", ProcessID: "process-1",
		RunID: "run-1", LeaseID: "lease-1", WorkspaceID: "workspace-1",
		RuntimeEpochID: epoch, LeaseGeneration: 2, FenceToken: 3,
		AuthorityHeadRecordRevision: revision,
	}
}

func TestRestoreReadSeamCannotUseWatchAsJournal(t *testing.T) {
	seam := reflect.TypeOf((*RestoreReaderV1)(nil)).Elem()
	want := map[string]bool{"CheckpointHeadsV1": true, "CheckpointBlobV1": true, "JournalPageV1": true}
	if seam.NumMethod() != len(want) {
		t.Fatalf("restore seam changed methods=%d", seam.NumMethod())
	}
	for index := 0; index < seam.NumMethod(); index++ {
		method := seam.Method(index)
		if !want[method.Name] || method.Name == "Watch" {
			t.Fatalf("watch-as-journal or unknown restore capability: %s", method.Name)
		}
	}
}

func TestRestorePlaybackDigestEqualityAcrossOneHundredValidHistories(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		count := trial%5 + 1
		records := make([]controljournal.StoredEventV1, count)
		for index := range records {
			number := index + 1
			records[index] = testStoredEvent(t, uint64(number), fmt.Sprintf("event-%d-%d", trial, number), fmt.Sprintf("operation-%d-%d", trial, number), "aggregate-a", uint64(number), 0, 1, fmt.Sprintf(`{"trial":%d,"value":%d}`, trial, number))
		}
		reader := &restoreReader{pages: encodedPages(t, records)}
		restored, err := RestoreV1(context.Background(), restoreInput(reader, t))
		if err != nil || !restored.Ready {
			t.Fatalf("trial %d restore=%+v err=%v", trial, restored, err)
		}
		played, err := playback.RunV1(playback.InputV1{Journal: testJournal(t, records...), Reducers: testReducers(t)})
		if err != nil {
			t.Fatalf("trial %d playback: %v", trial, err)
		}
		if err := playback.CompareDigestV1(restored.CanonicalProjectionDigest, played); err != nil {
			t.Fatalf("trial %d restore/playback mismatch: %v", trial, err)
		}
	}
}

func TestRestoreValidTwoThousandOneRecordTraversalAndStaleRestart(t *testing.T) {
	const count = 2001
	records := make([]controljournal.StoredEventV1, count)
	for index := range records {
		number := index + 1
		records[index] = testStoredEvent(t, uint64(number), controljournalToken("event-campaign", number), controljournalToken("operation-campaign", number), "aggregate-a", uint64(number), 0, 1, fmt.Sprintf(`{"value":%d}`, number))
	}
	want := snapshot(t, "aggregate-a", count, controljournalToken("event-campaign", count), fmt.Sprintf(`{"value":%d}`, count)).StateDigest
	for _, test := range []struct {
		name         string
		stale        bool
		wantStarts   int
		wantRestarts uint32
	}{
		{name: "complete", wantStarts: 1},
		{name: "stale cursor full restart", stale: true, wantStarts: 2, wantRestarts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &restoreReader{pages: encodedPages(t, records), staleOnce: test.stale, staleAtPage: 2}
			result, err := RestoreV1(context.Background(), restoreInput(reader, t))
			if err != nil || !result.Ready || result.Observation.EventCount != count || result.Observation.StaleRestarts != test.wantRestarts || len(reader.starts) != test.wantStarts {
				t.Fatalf("result=%+v starts=%d err=%v", result, len(reader.starts), err)
			}
			state, ok := result.Projection.State(reducer.AggregateKeyV1{Kind: "mission", ID: "aggregate-a"})
			if !ok || state.Digest() != want {
				t.Fatalf("terminal projection state ok=%v digest=%x want=%x", ok, state.Digest(), want)
			}
		})
	}
}

func TestEpochOneHundredCleanAndUncleanRestarts(t *testing.T) {
	seen := make(map[controljournal.RuntimeEpochID]bool, 100)
	for trial := 0; trial < 100; trial++ {
		predecessor := controljournal.RuntimeEpochID(fmt.Sprintf("epoch-old-%d", trial))
		admitted := controljournal.RuntimeEpochID(fmt.Sprintf("epoch-new-%d", trial))
		revision := uint64(trial + 10)
		restored := RestoreResultV1{Ready: true, Authority: AuthoritySummaryV1{RunID: "run-1", RuntimeEpochID: predecessor, DurableRevision: revision}}
		if trial%2 == 0 {
			restored.SelectedCheckpoint = &CheckpointHeadV1{CheckpointID: CheckpointID(fmt.Sprintf("clean-%d", trial))}
		}
		binding, err := BindRuntimeEpochV1(restored, "run-1", predecessor, admitted, revision)
		if err != nil || binding.FirstAdmission || !binding.CommandAdmissionOpen || seen[binding.RuntimeEpochID] {
			t.Fatalf("trial %d binding=%+v err=%v", trial, binding, err)
		}
		seen[binding.RuntimeEpochID] = true
		if err := ValidateCapsuleEpochV1(binding, epochTestCapsule(predecessor, revision)); err == nil {
			t.Fatalf("trial %d predecessor capsule accepted", trial)
		}
		if err := ValidateCapsuleEpochV1(binding, epochTestCapsule(admitted, revision)); err != nil {
			t.Fatalf("trial %d current capsule refused: %v", trial, err)
		}
		if LeaseExpiredV1(binding) != binding {
			t.Fatalf("trial %d lease expiry rotated binding", trial)
		}
	}
}

func FuzzRestoreOrderingBounded(f *testing.F) {
	f.Add(false, uint64(7), uint64(8))
	f.Fuzz(func(t *testing.T, swap bool, firstRevision, secondRevision uint64) {
		if firstRevision == 0 || secondRevision == 0 || firstRevision == secondRevision {
			return
		}
		records := []controljournal.StoredEventV1{
			testStoredEvent(t, firstRevision, "event-fuzz-a", "operation-fuzz-a", "aggregate-a", 1, 0, 1, `{"value":1}`),
			testStoredEvent(t, secondRevision, "event-fuzz-b", "operation-fuzz-b", "aggregate-a", 2, 0, 1, `{"value":2}`),
		}
		if swap {
			records[0], records[1] = records[1], records[0]
		}
		reader := &restoreReader{pages: []JournalScanPageV1{encodedPage(t, records)}}
		result, err := RestoreV1(context.Background(), restoreInput(reader, t))
		if err == nil && (!result.Ready || result.Observation.EventCount != 2 || result.CanonicalProjectionDigest == [32]byte{}) {
			t.Fatalf("invalid ready result=%+v", result)
		}
	})
}
