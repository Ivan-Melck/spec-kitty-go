package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/playback"
)

type restoreReader struct {
	heads      []CheckpointHeadV1
	blobs      map[CheckpointID][]byte
	pages      []JournalScanPageV1
	pageIndex  int
	staleCalls int
	starts     []WatermarkV1
}

func (r *restoreReader) CheckpointHeadsV1(context.Context) ([]CheckpointHeadV1, error) {
	return append([]CheckpointHeadV1(nil), r.heads...), nil
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
	if r.pageIndex >= len(r.pages) {
		return JournalScanPageV1{}, fmt.Errorf("unexpected cursor %q", cursor)
	}
	page := r.pages[r.pageIndex]
	r.pageIndex++
	return page, nil
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
	return RestoreInputV1{Reader: reader, Source: testSource(), EventRegistry: testEventRegistry(t), Reducers: testReducers(t)}
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
	aggregates := []AggregateSnapshotV1{snapshot(t, "aggregate-a", uint64(last), controljournalToken("event-prefix", last), fmt.Sprintf(`{"value":%d}`, last))}
	projection, projectionErr := projectionFromAggregates(aggregates)
	if projectionErr != nil {
		t.Fatal(projectionErr)
	}
	authority := testAuthority()
	authority.DurableRevision = uint64(last)
	record := CheckpointRecordV1{SchemaVersion: 1, CheckpointID: "checkpoint-large-prefix", ReducerSetDigest: testSource().ReducerSetDigest, EventSchemaSetDigest: testSource().EventSchemaSetDigest, ProjectID: testSource().ProjectID, MissionID: testSource().MissionID, Watermark: WatermarkV1{ThroughRecordRevision: uint64(last), ThroughEventID: controljournal.EventID(controljournalToken("event-prefix", last)), EventCount: uint64(last), EventPrefixDigest: digestBytes([]byte("trusted-large-prefix"))}, CanonicalProjectionDigest: projection.GlobalDigest(), Codec: CodecCanonicalJSONV1, Aggregates: aggregates, Authority: authority}
	projectionBytes, marshalErr := json.Marshal(checkpointProjectionV1FromRecord(record))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	record.UncompressedSize = uint64(len(projectionBytes))
	recordBytes, encodeErr := EncodeRecordV1(record)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	checkpoint := BuildResultV1{Record: record, RecordBytes: recordBytes, Head: CheckpointHeadV1{SchemaVersion: 1, CheckpointID: record.CheckpointID, CheckpointRecordDigest: digestBytes(recordBytes), ThroughRecordRevision: record.Watermark.ThroughRecordRevision, ThroughEventID: record.Watermark.ThroughEventID}}
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

	exactBytePage := encodedPage(t, []controljournal.StoredEventV1{stored})
	exactBytePage.ObservedRevision = 1
	exactBytePage.RawBytes = MaxRestoreBytes
	reader = &restoreReader{pages: []JournalScanPageV1{exactBytePage}}
	stale = 0
	if _, observation, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err != nil || observation.ScanBytes != MaxRestoreBytes {
		t.Fatalf("exact byte bound obs=%+v err=%v", observation, err)
	}
	plusBytePage := exactBytePage
	plusBytePage.RawBytes = MaxRestoreBytes + 1
	reader = &restoreReader{pages: []JournalScanPageV1{plusBytePage}}
	stale = 0
	if events, _, err = scanJournal(context.Background(), reader, WatermarkV1{}, &stale); err == nil || events != nil {
		t.Fatalf("plus-one byte bound returned partial len=%d err=%v", len(events), err)
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
	restored := RestoreResultV1{Ready: true}
	binding, err := BindRuntimeEpochV1(restored, "run-1", "epoch-old", "epoch-new", 44)
	if err != nil {
		t.Fatal(err)
	}
	if binding.RuntimeEpochID == binding.PredecessorEpochID || binding.RunID != "run-1" || binding.DurableRevision != 44 {
		t.Fatalf("binding=%+v", binding)
	}
	old := AuthorityCapsuleV1{RunID: "run-1", RuntimeEpochID: "epoch-old", DurableRevision: 44}
	if err := ValidateCapsuleEpochV1(binding, old); err == nil {
		t.Fatal("predecessor epoch capsule accepted")
	}
	current := AuthorityCapsuleV1{RunID: "run-1", RuntimeEpochID: "epoch-new", DurableRevision: 44}
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
