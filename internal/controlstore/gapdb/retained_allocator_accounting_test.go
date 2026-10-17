package gapdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"unsafe"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// Test-only leaf model for Go1.26.7/linux/amd64, ordinary allocation paths.
// Slots are NOT spans, RSS, stack placement, instrumentation overhead, or a
// complete call bound. ASAN/MSAN and debug.efence are outside this profile.
// A source allocation eliminated/stack-placed by the compiler may cost zero
// heap; we conservatively model one heap instance of each covered source event.
const retainedAllocatorPage = uint64(8192)
const retainedAllocatorSmall = uint64(32768 - 8)
const retainedAllocatorMaxRequest = uint64(64 << 20)
const retainedAllocatorMissingRecipes = "Uncovered owned events: keys/codecs/scanners/private maps; per-call state/context/connections/callbacks/kernel checks; compiler-generated temporaries/ABI multiplicity and other sites in OBSERVER_OWNED_ACCOUNTING.md. Shared/runtime storage separately disclosed; no full-call/peak/RSS bound."

var retainedAllocatorClasses = [...]uint64{
	0, 8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192,
	208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512, 576,
	640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536, 1792, 2048,
	2304, 2688, 3072, 3200, 3456, 4096, 4864, 5376, 6144, 6528,
	6784, 6912, 8192, 9472, 9728, 10240, 10880, 12288, 13568,
	14336, 16384, 18432, 19072, 20480, 21760, 24576, 27264, 28672, 32768,
}

type retainedAllocatorSlot struct {
	// growthPayload is runtime.roundupsize's result, not the allocator slot.
	// For tiny noscan requests it can be 8 while slotUpper is conservatively 16.
	growthPayload, slotUpper, header uint64
}

func retainedAllocatorRound(request uint64, scanned bool) (retainedAllocatorSlot, bool) {
	if request > retainedAllocatorMaxRequest {
		return retainedAllocatorSlot{}, false
	}
	if request == 0 {
		return retainedAllocatorSlot{}, true
	}
	if request <= retainedAllocatorSmall {
		header := uint64(0)
		if scanned && request > 512 {
			header = 8
		}
		need, ok := retainedAccountingAdd(request, header)
		if !ok {
			return retainedAllocatorSlot{}, false
		}
		for _, class := range retainedAllocatorClasses[1:] {
			if class >= need {
				slot := class
				// Ordinary tiny allocation may consume a new 16-byte block.
				// Charging 8 for every one-byte event would not be a safe bound.
				if !scanned && request < 16 {
					slot = max(slot, 16)
				}
				return retainedAllocatorSlot{class - header, slot, header}, true
			}
		}
		return retainedAllocatorSlot{}, false
	}
	rounded, ok := retainedAccountingAdd(request, retainedAllocatorPage-1)
	if !ok {
		return retainedAllocatorSlot{}, false
	}
	rounded &^= retainedAllocatorPage - 1
	return retainedAllocatorSlot{rounded, rounded, 0}, true
}

type retainedAllocatorAppend struct{ capacity, requested, slotUpper uint64 }

// ONLY append from nil/zero capacity. General repeated append growth is NOT
// modeled here. For a bulk append from nil nextslicecap returns the new length.
// Non-power-of-two element sizes truncate rounded payload to full elements;
// mallocgc then rounds that actual capacity*elementSize request AGAIN.
func retainedAllocatorAppendNil(count, elementSize uint64, scanned bool) (retainedAllocatorAppend, bool) {
	if count > math.MaxInt64 || elementSize > retainedAllocatorMaxRequest {
		return retainedAllocatorAppend{}, false
	}
	if count == 0 || elementSize == 0 {
		return retainedAllocatorAppend{capacity: count}, true
	}
	raw, ok := retainedAccountingMul(count, elementSize)
	if !ok {
		return retainedAllocatorAppend{}, false
	}
	first, ok := retainedAllocatorRound(raw, scanned)
	if !ok {
		return retainedAllocatorAppend{}, false
	}
	capacity := first.growthPayload / elementSize
	request, ok := retainedAccountingMul(capacity, elementSize)
	if !ok || capacity < count {
		return retainedAllocatorAppend{}, false
	}
	second, ok := retainedAllocatorRound(request, scanned)
	if !ok {
		return retainedAllocatorAppend{}, false
	}
	return retainedAllocatorAppend{capacity, request, second.slotUpper}, true
}

type retainedBuilderSlotInput struct {
	// Structural size envelope, not ACK validation or observation admission.
	planRecords  []uint64
	valueLengths []uint64 // one length per occurrence, INCLUDING overwritten rows
	attempts     uint64
}

type retainedBuilderSlotLedger struct {
	snapshot, order, occurrences, records, values, nodes uint64
	coveredSlotUpper, distributionFreeUpper              uint64
	fullCallCoverage                                     bool // ALWAYS false: uncovered source/ABI/library events remain.
	missingRecipes                                       string
}

func retainedBuilderSlots(in retainedBuilderSlotInput) (retainedBuilderSlotLedger, bool) {
	zero := retainedBuilderSlotLedger{}
	if len(in.planRecords) > retainedPublicationLimitV2 || len(in.valueLengths) > retainedRecordLimitV2 {
		return zero, false
	}
	var records, values uint64
	for _, n := range in.planRecords {
		var ok bool
		if n == 0 {
			return zero, false
		}
		records, ok = retainedAccountingAdd(records, n)
		if !ok || records > retainedRecordLimitV2 {
			return zero, false
		}
	}
	if records != uint64(len(in.valueLengths)) || in.attempts > records+retainedObservationPrivateRows+3 {
		return zero, false
	}
	for _, n := range in.valueLengths {
		var ok bool
		if n == 0 || n > MaxCheckpointValueBytes {
			return zero, false
		}
		values, ok = retainedAccountingAdd(values, n)
		if !ok || values > retainedValueBytesLimitV2 {
			return zero, false
		}
	}
	l := retainedBuilderSlotLedger{missingRecipes: retainedAllocatorMissingRecipes}
	snapshot, ok := retainedAllocatorRound(uint64(unsafe.Sizeof([retainedPublicationLimitV2]journalv2.AcknowledgedPublicationV2{})), true)
	if !ok {
		return zero, false
	}
	l.snapshot = snapshot.slotUpper
	order, ok := retainedAllocatorRound(uint64(unsafe.Sizeof([retainedPublicationLimitV2]retainedPublicationOrderV2{})), true)
	if !ok {
		return zero, false
	}
	l.order = order.slotUpper
	raw, ok := retainedAccountingMul(records, uint64(unsafe.Sizeof(retainedPublicationOccurrenceV2{})))
	if !ok {
		return zero, false
	}
	occurrences, ok := retainedAllocatorRound(raw, true)
	if !ok {
		return zero, false
	}
	l.occurrences, ok = retainedAccountingMul(2, occurrences.slotUpper)
	if !ok {
		return zero, false
	}
	for _, n := range in.planRecords {
		backing, valid := retainedAllocatorAppendNil(n, uint64(unsafe.Sizeof(journalv2.SealedRecordV2{})), true)
		if !valid {
			return zero, false
		}
		l.records, ok = retainedAccountingAdd(l.records, backing.slotUpper)
		if !ok {
			return zero, false
		}
	}
	for _, n := range in.valueLengths {
		backing, valid := retainedAllocatorAppendNil(n, 1, false)
		if !valid {
			return zero, false
		}
		two, valid := retainedAccountingMul(2, backing.slotUpper)
		if !valid {
			return zero, false
		}
		l.values, ok = retainedAccountingAdd(l.values, two)
		if !ok {
			return zero, false
		}
	}
	node, ok := retainedAllocatorRound(uint64(unsafe.Sizeof(retainedConnectionNode{})), true)
	if !ok {
		return zero, false
	}
	l.nodes, ok = retainedAccountingMul(node.slotUpper, in.attempts)
	if !ok {
		return zero, false
	}
	l.coveredSlotUpper, ok = retainedAccountingSum(l.snapshot, l.order, l.occurrences, l.records, l.values, l.nodes)
	if !ok {
		return zero, false
	}
	// A distribution-independent bound for THESE events: every ordinary slot
	// request costs at most requested bytes +8191. Apply slack PER event, not
	// to aggregate V/r. Byte/header clones and per-plan Records remain separate.
	occurrenceLogical, ok := retainedAccountingMul(2*uint64(unsafe.Sizeof(retainedPublicationOccurrenceV2{})), records)
	if !ok {
		return zero, false
	}
	recordLogical, ok := retainedAccountingMul(uint64(unsafe.Sizeof(journalv2.SealedRecordV2{})), records)
	if !ok {
		return zero, false
	}
	valueLogical, ok := retainedAccountingMul(2, values)
	if !ok {
		return zero, false
	}
	events := uint64(len(in.planRecords)) + 2*records
	if records > 0 {
		events += 2
	}
	slack, ok := retainedAccountingMul(events, retainedAllocatorPage-1)
	if !ok {
		return zero, false
	}
	l.distributionFreeUpper, ok = retainedAccountingSum(l.snapshot, l.order, occurrenceLogical, recordLogical, valueLogical, l.nodes, slack)
	if !ok || l.coveredSlotUpper > l.distributionFreeUpper {
		return zero, false
	}
	return l, true
}

// These are only the named large-buffer families in retainedTransport.exchange,
// NOT parser/codec/state/connection allocations. Each metered attempt reads at
// most one frame and decodes at most one value. F charges even refused headers.
// D<=floor(3F/4) includes failed decode backings, not just successful results.
// Each decoder has one 4096-byte chunk; C<=floor(F/4096)+min(A,F) flushes.
// EncodeToString's two buffers plus the conservatively charged string(part)
// each request <=4096 bytes. Their noscan slot slack is <=639 (tested below).
type retainedTransportSlotLedger struct {
	frames, decoded, sdkClone, roundtrip, chunks uint64
	coveredSlotUpper, chunkEventsUpper           uint64
	fullCallCoverage                             bool
	missingRecipes                               string
}

func retainedTransportSlots(frames, records, attempts uint64) (retainedTransportSlotLedger, bool) {
	zero := retainedTransportSlotLedger{}
	if frames > retainedObservationFrames || records > retainedRecordLimitV2 || attempts > records+retainedObservationPrivateRows+3 || attempts == 0 && frames > 0 {
		return zero, false
	}
	active := min(attempts, frames) // a nonempty response contributes >=1 charged byte
	largeSlack, ok := retainedAccountingMul(active, retainedAllocatorPage-1)
	if !ok {
		return zero, false
	}
	l := retainedTransportSlotLedger{missingRecipes: retainedAllocatorMissingRecipes}
	l.frames, ok = retainedAccountingAdd(frames, largeSlack)
	if !ok {
		return zero, false
	}
	threeFrames, ok := retainedAccountingMul(3, frames)
	if !ok {
		return zero, false
	}
	l.decoded, ok = retainedAccountingAdd(threeFrames/4, largeSlack)
	if !ok {
		return zero, false
	}
	l.sdkClone = l.decoded
	l.chunkEventsUpper, ok = retainedAccountingAdd(frames/retainedDecodeBatch, active)
	if !ok {
		return zero, false
	}
	chunkSlack, ok := retainedAccountingMul(3*639, l.chunkEventsUpper)
	if !ok {
		return zero, false
	}
	l.roundtrip, ok = retainedAccountingAdd(threeFrames, chunkSlack)
	if !ok {
		return zero, false
	}
	l.chunks, ok = retainedAccountingMul(retainedDecodeBatch, active)
	if !ok {
		return zero, false
	}
	l.coveredSlotUpper, ok = retainedAccountingSum(l.frames, l.decoded, l.sdkClone, l.roundtrip, l.chunks)
	if !ok {
		return zero, false
	}
	return l, true
}

func TestRetainedAllocatorTransportLargeBufferEnvelope(t *testing.T) {
	for _, tc := range []struct{ f, r, a, want uint64 }{
		{0, 0, 0, 0}, {0, 4096, 4294, 0}, {5, 3, 2, 61198}, {64 << 20, 4096, 4294, 531843164},
	} {
		got, ok := retainedTransportSlots(tc.f, tc.r, tc.a)
		if !ok || got.coveredSlotUpper != tc.want || got.fullCallCoverage || got.missingRecipes == "" {
			t.Fatal("transport leaf scope/arithmetic", tc, got, ok)
		}
		t.Logf("large-buffer-only F=%d A=%d covered-normal-slot-upper=%d chunk-events-upper=%d incomplete=true", tc.f, tc.a, got.coveredSlotUpper, got.chunkEventsUpper)
	}
	for _, v := range [][3]uint64{{1, 0, 0}, {64<<20 + 1, 0, 1}, {1, 4097, 1}, {1, 0, 199}, {math.MaxUint64, 0, 1}, {0, math.MaxUint64, 1}, {0, 0, math.MaxUint64}} {
		if got, ok := retainedTransportSlots(v[0], v[1], v[2]); ok || got != (retainedTransportSlotLedger{}) {
			t.Fatal("invalid transport envelope returned partial/success", v, got)
		}
	}
	var largest uint64
	for n := uint64(0); n <= retainedDecodeBatch; n++ {
		s, ok := retainedAllocatorRound(n, false)
		if !ok || s.slotUpper < n || s.slotUpper > n+639 {
			t.Fatal("chunk slack underbounds", n, s)
		}
		largest = max(largest, s.slotUpper-n)
	}
	if largest != 639 {
		t.Fatal("audited chunk slot slack drift", largest)
	}
	for _, counts := range [][]uint64{{}, {0}, {4}, {4096}, {4100}, {4096, 4, 8196}, {0, 4, 4096, 4100}} {
		var f, actual uint64
		for _, n := range counts {
			f += n + 2
			actual += (n + retainedDecodeBatch - 1) / retainedDecodeBatch
		}
		l, ok := retainedTransportSlots(f, 0, uint64(len(counts)))
		if !ok || actual > l.chunkEventsUpper {
			t.Fatal("chunk partition count escaped bound", counts, l)
		}
	}
}

func TestRetainedAllocatorThresholds(t *testing.T) {
	for _, tc := range []struct {
		n                     uint64
		scanned               bool
		payload, slot, header uint64
	}{
		{0, false, 0, 0, 0}, {1, false, 8, 16, 0}, {8, false, 8, 16, 0}, {15, false, 16, 16, 0}, {16, false, 16, 16, 0},
		{512, true, 512, 512, 0}, {513, true, 568, 576, 8}, {8192, true, 9464, 9472, 8},
		{32760, true, 32760, 32768, 8}, {32761, true, 32768, 32768, 0},
		{32768, false, 32768, 32768, 0}, {32769, false, 40960, 40960, 0},
		{64 << 20, false, 64 << 20, 64 << 20, 0},
	} {
		got, ok := retainedAllocatorRound(tc.n, tc.scanned)
		if !ok || got != (retainedAllocatorSlot{tc.payload, tc.slot, tc.header}) {
			t.Fatalf("%+v -> %+v %v", tc, got, ok)
		}
	}
	for _, n := range []uint64{64<<20 + 1, math.MaxUint64} {
		if got, ok := retainedAllocatorRound(n, true); ok || got != (retainedAllocatorSlot{}) {
			t.Fatal("out-of-envelope allocation accepted", n, got)
		}
	}
	for _, scanned := range []bool{false, true} {
		var prior uint64
		for n := uint64(0); n <= 65536; n++ {
			s, ok := retainedAllocatorRound(n, scanned)
			if !ok || s.growthPayload < n || s.slotUpper < n || s.slotUpper < prior || s.slotUpper > n+8191 {
				t.Fatal("slot/slack invariant", n, scanned, s)
			}
			prior = s.slotUpper
		}
	}
}

func TestRetainedAllocatorAppendAndPerEventRounding(t *testing.T) {
	if got, ok := retainedAllocatorAppendNil(2, 360, true); !ok || got != (retainedAllocatorAppend{2, 720, 768}) {
		t.Fatal("record append lost header/truncation", got, ok)
	}
	if got, ok := retainedAllocatorAppendNil(256, 360, true); !ok || got != (retainedAllocatorAppend{273, 98280, 98304}) {
		t.Fatal("large record append geometry", got, ok)
	}
	if got, ok := retainedAllocatorAppendNil(1, 1, false); !ok || got != (retainedAllocatorAppend{8, 8, 16}) {
		t.Fatal("tiny append capacity is not tiny block size", got, ok)
	}
	for _, input := range [][2]uint64{{math.MaxUint64, 360}, {4096, math.MaxUint64}, {64 << 20, 360}} {
		if got, ok := retainedAllocatorAppendNil(input[0], input[1], true); ok || got != (retainedAllocatorAppend{}) {
			t.Fatal("invalid append escaped", input, got)
		}
	}
	for _, input := range [][2]uint64{{0, 360}, {9, 0}} {
		got, ok := retainedAllocatorAppendNil(input[0], input[1], false)
		if !ok || got.slotUpper != 0 || got.capacity != input[0] {
			t.Fatal("zero-byte backing charged heap", input, got)
		}
	}
	one, _ := retainedAllocatorRound(1, false)
	combined, _ := retainedAllocatorRound(2, false)
	if 2*one.slotUpper <= combined.slotUpper {
		t.Fatal("test no longer distinguishes per-event tiny-block upper from aggregate rounding")
	}
	separate, _ := retainedAllocatorAppendNil(12, 360, true)
	merged, _ := retainedAllocatorAppendNil(24, 360, true)
	if 2*separate.slotUpper <= merged.slotUpper {
		t.Fatal("test no longer distinguishes plan partition rounding")
	}
}

func TestRetainedAllocatorBuilderEnvelope(t *testing.T) {
	for _, in := range []retainedBuilderSlotInput{
		{}, {planRecords: []uint64{1}, valueLengths: []uint64{1}},
		{planRecords: []uint64{3, 1}, valueLengths: []uint64{1, 513, 8192, 32761}, attempts: 7},
	} {
		got, ok := retainedBuilderSlots(in)
		if !ok || got.fullCallCoverage || got.snapshot != 204800 || got.order != 9472 || got.coveredSlotUpper > got.distributionFreeUpper || got.missingRecipes == "" {
			t.Fatal("inexact/complete slot ledger", in, got)
		}
	}
	in := retainedBuilderSlotInput{planRecords: make([]uint64, 16), valueLengths: make([]uint64, 4096), attempts: 4294}
	for i := range in.planRecords {
		in.planRecords[i] = 256
	}
	for i := range in.valueLengths {
		in.valueLengths[i] = 8192
	}
	got, ok := retainedBuilderSlots(in)
	if !ok || got.occurrences != 3014656 || got.records != 1572864 || got.values != 67108864 || got.nodes != 137408 || got.coveredSlotUpper != 72048064 || got.fullCallCoverage {
		t.Fatal("maximum structural leaf geometry", got, ok)
	}
	t.Logf("covered normal-allocator slots=%d distribution-free upper=%d; NOT complete observer heap/RSS/peak bound", got.coveredSlotUpper, got.distributionFreeUpper)
	for name, bad := range map[string]retainedBuilderSlotInput{
		"empty_plan":       {planRecords: []uint64{0}},
		"missing_value":    {planRecords: []uint64{1}},
		"extra_value":      {valueLengths: []uint64{1}},
		"zero_value":       {planRecords: []uint64{1}, valueLengths: []uint64{0}},
		"oversize_value":   {planRecords: []uint64{1}, valueLengths: []uint64{MaxCheckpointValueBytes + 1}},
		"too_many_plans":   {planRecords: make([]uint64, 257)},
		"too_many_records": {planRecords: []uint64{4097}, valueLengths: make([]uint64, 4097)},
		"attempts":         {attempts: 199},
		"overflow":         {planRecords: []uint64{math.MaxUint64}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := retainedBuilderSlots(bad); ok || got != (retainedBuilderSlotLedger{}) {
				t.Fatal("invalid input returned nonzero/success", got)
			}
		})
	}
	over := in
	over.valueLengths = append([]uint64(nil), in.valueLengths...)
	over.valueLengths[0]++
	if got, ok := retainedBuilderSlots(over); ok || got != (retainedBuilderSlotLedger{}) {
		t.Fatal("aggregate value limit bypassed", got)
	}
}

func TestRetainedAllocatorGenuineOverwriteCoverage(t *testing.T) {
	port := &retainedFixturePortV2{revision: 7}
	coordinator := retainedCoordinatorV2(t, port)
	first := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "slot-chain", nil, 0))
	prior := first.Plan().OperationState()
	port.revision = 29
	second := retainedPublishV2(t, coordinator, retainedFixtureInputV2(t, "slot-chain", &prior, 7))
	acks := []journalv2.AcknowledgedPublicationV2{second, first}
	in := retainedBuilderSlotInput{}
	for _, ack := range acks {
		records := ack.Plan().Records() // fixture enumeration OUTSIDE observation/timing
		in.planRecords = append(in.planRecords, uint64(len(records)))
		for _, record := range records {
			in.valueLengths = append(in.valueLengths, uint64(len(record.CanonicalValue())))
		}
	}
	got, ok := retainedBuilderSlots(in)
	if !ok || got.fullCallCoverage {
		t.Fatal("genuine footprint not modeled", got)
	}
	_, budget, err := preflightRetainedPublicationsV2(context.Background(), acks)
	if err != nil || uint64(len(in.valueLengths)) != budget.records {
		t.Fatal("all-occurrence input drift", err)
	}
	rows, err := buildRetainedPublicationMapV2(context.Background(), acks)
	if err != nil || len(rows) >= len(in.valueLengths) {
		t.Fatal("fixture did not overwrite", err)
	}
	var finalValueSlots uint64
	for _, row := range rows {
		a, ok := retainedAllocatorAppendNil(uint64(len(row.canonical)), 1, false)
		if !ok {
			t.Fatal("final value envelope")
		}
		finalValueSlots += 2 * a.slotUpper
	}
	if got.values <= finalValueSlots {
		t.Fatal("overwritten value allocations disappeared")
	}
	t.Logf("actual opaque fixture ACKs=%d occurrences=%d final=%d covered-slot-upper=%d incomplete=true", len(acks), budget.records, len(rows), got.coveredSlotUpper)
}

func retainedAllocatorPinnedRuntime(t *testing.T) {
	t.Helper()
	if runtime.Version() != "go1.26.7" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("runtime-capacity/source witness requires pinned go1.26.7/linux/amd64; arithmetic model is source-specific")
	}
}

func TestRetainedAllocatorRuntimeAppendCapacity(t *testing.T) {
	retainedAllocatorPinnedRuntime(t)
	for _, n := range []int{0, 1, 2, 3, 7, 8, 15, 16, 63, 64, 65, 90, 91, 92, 128, 255, 256, 512, 513, 1024, 4096} {
		want, ok := retainedAllocatorAppendNil(uint64(n), 1, false)
		actual := append([]byte(nil), make([]byte, n)...)
		if !ok || uint64(cap(actual)) != want.capacity {
			t.Fatal("actual byte append cap", n, cap(actual), want)
		}
		want, ok = retainedAllocatorAppendNil(uint64(n), uint64(unsafe.Sizeof(journalv2.SealedRecordV2{})), true)
		records := append([]journalv2.SealedRecordV2(nil), make([]journalv2.SealedRecordV2, n)...)
		if !ok || uint64(cap(records)) != want.capacity {
			t.Fatal("actual record append cap", n, cap(records), want)
		}
		want, ok = retainedAllocatorAppendNil(uint64(n), uint64(unsafe.Sizeof(retainedPublicationOccurrenceV2{})), true)
		occurrences := append([]retainedPublicationOccurrenceV2(nil), make([]retainedPublicationOccurrenceV2, n)...)
		if !ok || uint64(cap(occurrences)) != want.capacity {
			t.Fatal("actual occurrence append cap", n, cap(occurrences), want)
		}
	}
}

// Exact source custody plus an independently indexed reference to roundupsize's
// generated tables. Hashes bind inspected source, not proof of unread code paths.
func TestRetainedAllocatorPinnedSource(t *testing.T) {
	retainedAllocatorPinnedRuntime(t)
	expected := map[string]string{
		"encoding/base64/base64.go":          "e6437c7716ecd1ae91c9d10037362cab186e5873a6365b713e2d8c57d8fc32fc",
		"runtime/msize.go":                   "0164d15295726608812842b429e241f553a314985a7c6aa48cb1ad24346a52dd",
		"runtime/malloc.go":                  "67e74184452cc6cb5bedee04fbbd6f395822e1cffbb4f91ad4a484796d7f021f",
		"runtime/mcache.go":                  "9c27d457c7464565dccc3ad1900f0c177c57eec3cd35b7465c00971b6339da0b",
		"runtime/slice.go":                   "ac595f1a822620fdc52271ef84f06b19c1f0abc0e53c0eb049dbad90b86abdbd",
		"internal/runtime/gc/malloc.go":      "889e4ee48345fd246b261a9352a0f8b9deb2c595ab59629c0ca70f7eaf06462c",
		"internal/runtime/gc/sizeclasses.go": "7626b00bbb416c25c5279b29aa73a072d74ace3444e58b178a6ff855a2deec2c",
	}
	for name, want := range expected {
		raw, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "src", name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("allocator source drift; re-audit", name)
		}
		t.Logf("pinned source %s sha256=%s", name, want)
	}
	path := filepath.Join(runtime.GOROOT(), "src/internal/runtime/gc/sizeclasses.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tables := map[string][]uint64{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.ValueSpec)
		if !ok || len(decl.Names) != 1 || len(decl.Values) != 1 {
			return true
		}
		name := decl.Names[0].Name
		if name != "SizeClassToSize" && name != "SizeToSizeClass8" && name != "SizeToSizeClass128" {
			return true
		}
		list, ok := decl.Values[0].(*ast.CompositeLit)
		if !ok {
			t.Fatal("class table shape drift")
		}
		for _, item := range list.Elts {
			literal, ok := item.(*ast.BasicLit)
			if !ok {
				t.Fatal("class table expression drift")
			}
			value, err := strconv.ParseUint(literal.Value, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			tables[name] = append(tables[name], value)
		}
		return true
	})
	if !reflect.DeepEqual(tables["SizeClassToSize"], retainedAllocatorClasses[:]) {
		t.Fatal("audited class table drift")
	}
	for _, scan := range []bool{false, true} {
		for n := uint64(1); n <= retainedAllocatorSmall; n++ {
			header := uint64(0)
			if scan && n > 512 {
				header = 8
			}
			need := n + header
			var index uint64
			if need <= 1016 {
				index = tables["SizeToSizeClass8"][(need+7)/8]
			} else {
				index = tables["SizeToSizeClass128"][(need+127-1024)/128]
			}
			want := tables["SizeClassToSize"][index] - header
			actual, ok := retainedAllocatorRound(n, scan)
			if !ok || actual.growthPayload != want {
				t.Fatal("indexed runtime source disagrees", n, scan, actual, want)
			}
		}
	}
	// Build metadata is reported separately; this is not an executable admission.
	command := exec.Command("go", "version")
	raw, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("active toolchain: %s", raw)
}
