package gapdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"reflect"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestRetainedObservationBudgetWorkExactPlusOneOverflow(t *testing.T) {
	for _, records := range []uint64{0, 1, 4096} {
		calls := records + 195 + 3
		image := retainedObservationHashes / (calls*3 + 1)
		if got, ok := retainedObservationWork(image, records); !ok || got != calls {
			t.Fatal("exact planned work refused")
		}
		if _, ok := retainedObservationWork(image+1, records); ok {
			t.Fatal("work ceiling plus one accepted")
		}
	}
	for _, pair := range [][2]uint64{{0, 0}, {1, 4097}, {math.MaxUint64, 0}, {1, math.MaxUint64}} {
		if _, ok := retainedObservationWork(pair[0], pair[1]); ok {
			t.Fatal("overflow/invalid work accepted")
		}
	}
	// Arithmetic control ONLY, not hardcoded production identity or qualification.
	if _, ok := retainedObservationWork(5426793, 4096); ok {
		t.Fatal("qualification-sized full-map workload should refuse development ceiling")
	}
	b := &retainedObservationBudget{transport: &retainedTransport{}, planned: retainedObservationCalls, image: 1}
	for i := uint64(0); i < retainedObservationCalls; i++ {
		if b.attempt(b.transport) != nil {
			t.Fatal("exact attempt budget refused")
		}
	}
	if b.attempt(b.transport) == nil || b.attempts != retainedObservationCalls || b.finishHash() != nil || b.finishHash() == nil || b.hashed != 3*retainedObservationCalls+1 {
		t.Fatal("attempt/terminal budget bypassed")
	}
}

// Reader panics on payload access: proves refusal happens after header but
// BEFORE any payload allocation/read, without inferring a peak-memory bound.
type retainedHeaderOnly struct {
	prefix [4]byte
	read   bool
}

func (r *retainedHeaderOnly) Read(p []byte) (int, error) {
	if r.read || len(p) != 4 {
		panic("payload read before budget refusal")
	}
	r.read = true
	copy(p, r.prefix[:])
	return 4, nil
}
func TestRetainedObservationBudgetFramesBeforeAllocationIncludingMalformed(t *testing.T) {
	b := &retainedObservationBudget{}
	ctx := context.WithValue(t.Context(), retainedObservationBudgetKey{}, b)
	for _, n := range []uint64{12 << 20, 12 << 20, 12 << 20, 12 << 20, 12 << 20, 4 << 20} {
		if b.frame(n) != nil {
			t.Fatal("exact frame aggregate refused")
		}
	}
	var r retainedHeaderOnly
	binary.BigEndian.PutUint32(r.prefix[:], 1)
	// The existing io.Reader header buffer escapes (one 4-byte allocation).
	// No payload allocation/read is permitted; reader panics if attempted.
	var value []byte
	var err error
	if allocs := testing.AllocsPerRun(1, func() { r.read = false; value, err = retainedReadFrameContext(ctx, &r) }); allocs != 1 || value != nil || err == nil || b.frames != retainedObservationFrames {
		t.Fatalf("allocation/refund before frame refusal: %v %v", allocs, err)
	}
	b = &retainedObservationBudget{}
	ctx = context.WithValue(t.Context(), retainedObservationBudgetKey{}, b)
	for _, n := range []uint32{MaxWireFrameBytes + 1, 0} {
		binary.BigEndian.PutUint32(r.prefix[:], n)
		r.read = false
		if value, err := retainedReadFrameContext(ctx, &r); value != nil || err == nil {
			t.Fatal("malformed frame accepted")
		}
	}
	if b.frames != MaxWireFrameBytes+1 {
		t.Fatal("malformed reply not charged")
	}
	b.frames = 0
	var buffer bytes.Buffer
	if err := retainedWriteFrame(&buffer, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if value, err := retainedReadFrameContext(ctx, &buffer); err != nil || string(value) != `{}` || b.frames != 2 {
		t.Fatal("valid frame reservation", err)
	}
}

func TestRetainedObservationBudgetStructuralHashUnderflowAndTerminal(t *testing.T) {
	transport := &retainedTransport{}
	for _, used := range []uint64{retainedObservationHashes, retainedObservationHashes + 1, math.MaxUint64} {
		b := &retainedObservationBudget{transport: transport, planned: 1, image: 1, hashed: used}
		if b.attempt(transport) == nil || b.attempts != 0 || b.hashed != used {
			t.Fatal("hash subtraction underflow/overflow admitted")
		}
	}
	b := &retainedObservationBudget{transport: transport, planned: 1, image: 1, terminal: true}
	if b.attempt(transport) == nil {
		t.Fatal("attempt after terminal admitted")
	}
}

func TestRetainedObservationBudgetAttemptBeforeEndpointAndWrongTransport(t *testing.T) {
	tpt := &retainedTransport{} // nil lifetime: cannot reach endpoint or any socket
	b := &retainedObservationBudget{transport: tpt, planned: 1, image: 1}
	ctx := context.WithValue(t.Context(), retainedObservationBudgetKey{}, b)
	if _, err := tpt.status(ctx); err == nil || b.attempts != 1 || b.hashed != 3 {
		t.Fatal("refused endpoint attempt not reserved")
	}
	if _, err := tpt.status(ctx); err == nil || b.attempts != 1 {
		t.Fatal("attempt over limit charged/read")
	}
	if _, err := (&retainedTransport{}).status(ctx); err == nil || b.attempts != 1 {
		t.Fatal("budget moved to another transport")
	}
}

func TestRetainedObservationBudgetActualACKWorkPreflightNoCopiesReads(t *testing.T) {
	// Use all 256 genuinely sealed coordinator publications, 4096 occurrences.
	actual := makeACKsForObservationBudget(t)
	a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
	capture, _ = journalv2.NewStoreRevisionV2(capture.DatabaseID(), 257, 257)
	if _, ok := retainedObservationWork(a.lifetime.ImageSize(), 4096); ok {
		t.Fatal("test executable unexpectedly tiny; no defensible over-budget fixture")
	}
	var session RetainedObservationV2
	var err error
	allocs := testing.AllocsPerRun(1, func() { session, err = a.PrepareRetainedObservationV2(context.Background(), capture, actual) })
	// Exactly the fixed opaque ACK array, separately byte-measured by the
	// snapshot test; no variable plan/record/key/value copy before refusal.
	if err == nil || !reflect.ValueOf(session).IsZero() || allocs != 1 || accepted.Load() != 0 {
		t.Fatalf("hash work refused after copy/read: allocations=%v err=%v", allocs, err)
	}
}

func makeACKsForObservationBudget(t *testing.T) []journalv2.AcknowledgedPublicationV2 {
	t.Helper()
	acks := make([]journalv2.AcknowledgedPublicationV2, retainedPublicationLimitV2)
	port := &retainedFixturePortV2{}
	coordinator := retainedCoordinatorV2(t, port)
	for i := range acks {
		input := retainedFixtureInputV2(t, fmt.Sprintf("observe-budget-%03d", i), nil, 0)
		retainedAddGuardsV2(t, &input, 5)
		retainedAddAssertionsV2(t, &input, 16)
		port.revision = uint64(i + 1)
		acks[i] = retainedPublishV2(t, coordinator, input)
	}
	return acks
}

func TestRetainedObservationBudgetFrozenOriginalRecordMeter(t *testing.T) {
	a, capture, accepted := retainedObservationFixture(t, nil, nil, nil)
	original := make([]journalv2.AcknowledgedPublicationV2, 16)
	replacements := make([]journalv2.AcknowledgedPublicationV2, 16)
	for i := range original {
		original[i] = retainedACKV2(t, fmt.Sprintf("snapshot-work-small-%02d", i), uint64(i+1))
		input := retainedFixtureInputV2(t, fmt.Sprintf("snapshot-work-large-%02d", i), nil, 0)
		retainedAddGuardsV2(t, &input, 245) // 256 genuine records per replacement
		replacements[i] = retainedPublishV2(t, retainedCoordinatorV2(t, &retainedFixturePortV2{uint64(i + 1)}), input)
	}
	if _, ok := retainedObservationWork(a.lifetime.ImageSize(), 4096); ok {
		t.Fatal("fixture no longer exceeds work cap")
	}
	want := retainedCopiedOracleV2(t, original)
	planned, ok := retainedObservationWork(a.lifetime.ImageSize(), 16*11)
	if !ok {
		t.Fatal("original fixture no longer fits work cap")
	}
	for _, at := range []int{1, 2, 10, 17, 18} {
		input := append([]journalv2.AcknowledgedPublicationV2(nil), original...)
		ctx := &retainedSnapshotContextV2{Context: context.Background(), method: 0, at: at, swap: func() { copy(input, replacements) }}
		s, err := a.PrepareRetainedObservationV2(ctx, capture, input)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retainedDenseOracleViewV2(s.state.expected), want) || s.state.budget.planned != planned || s.state.budget.attempts != 0 || accepted.Load() != 0 || input[0].PublicationID() != replacements[0].PublicationID() {
			t.Fatal("replacement changed original record/hashwork meter or copied map")
		}
		s.Close()
	}
}
