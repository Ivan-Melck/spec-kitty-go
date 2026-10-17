package productionruntime

import (
	"context"
	"runtime"
	"testing"
	"unsafe"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// Companion to gapdb's private-map source-event ledger. No private type, record,
// count, provider or authority is exported. These are compiler layout checks,
// NOT unsafe inspection of live runtime map internals or heap placement proof.
func TestRetainedPrivateAllocationGeometry(t *testing.T) {
	if runtime.Version() != "go1.26.7" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("qualified private geometry is pinned Go1.26.7/linux/amd64")
	}
	type rowSlot struct {
		key  string
		elem retainedPrivateRecord
	}
	type rowGroup struct {
		ctrl  uint64
		slots [8]rowSlot
	}
	type boolSlot struct {
		key  journalv2.RuntimeEpochID
		elem bool
	}
	type boolGroup struct {
		ctrl  uint64
		slots [8]boolSlot
	}
	// Mirror the source-pinned compiler's MapType/mapTableType fields. Their
	// code has its own runtime layout assertions; no abi.MapType descriptor
	// is charged as a per-map object. These mirrors are conditional storage.
	type header struct {
		used                  uint64
		seed                  uintptr
		dir                   unsafe.Pointer
		length                int
		depth, shift, writing uint8
		tombstone             bool
		clearSeq              uint64
	}
	type table struct {
		used, capacity, growthLeft uint16
		depth                      uint8
		index                      int
		data                       unsafe.Pointer
		mask                       uint64
	}
	for name, pair := range map[string][2]uintptr{
		"record":                 {unsafe.Sizeof(retainedPrivateRecord{}), 48},
		"record_value_offset":    {unsafe.Offsetof(retainedPrivateRecord{}.value), 16},
		"record_revision_offset": {unsafe.Offsetof(retainedPrivateRecord{}.revision), 40},
		"key_string":             {unsafe.Sizeof(journalv2.RuntimeEpochID("")), 16},
		"row_slot":               {unsafe.Sizeof(rowSlot{}), 64}, "row_group": {unsafe.Sizeof(rowGroup{}), 520},
		"bool_slot": {unsafe.Sizeof(boolSlot{}), 24}, "bool_group": {unsafe.Sizeof(boolGroup{}), 200},
		"header_mirror": {unsafe.Sizeof(header{}), 48}, "table_mirror": {unsafe.Sizeof(table{}), 32},
		"directory_pointer": {unsafe.Sizeof([1]unsafe.Pointer{}), 8},
	} {
		if pair[0] != pair[1] {
			t.Fatal("private source geometry drift; re-audit", name, pair)
		}
	}
	if retainedPrivateMaxRows != 195 || retainedPrivateMaxEpochs != 64 || retainedPrivateMaxValueBytes != 4096 || retainedPrivateMaxTotalBytes != 256<<10 {
		t.Fatal("private limit drift")
	}
	t.Log("actual private record/named-key geometry verified; mirrored map header/table source-bound separately; no heap placement assertion")
}

func TestRetainedPrivateAllocationFailurePrefixes(t *testing.T) {
	for _, epochs := range []int{1, 2, 64} {
		f := newRetainedPrivateFixture(t, epochs)
		var bytes uint64
		for _, r := range f.reader.rows {
			bytes += uint64(len(r.Value))
		}
		result, err := f.observe(context.Background(), defaultRetainedPrivatePolicy())
		if err != nil || len(result.rows) != 3+3*epochs || len(f.reader.trace) != len(result.rows) || bytes > retainedPrivateMaxTotalBytes {
			t.Fatal("private fixture footprint", epochs, len(result.rows), err)
		}
		// The same source-valid input fails after independently accepted read
		// prefixes. These fake-reader calls are NOT native provider receipts.
		for _, limit := range []int{1, 2, 3, max(3, len(result.rows)-1)} {
			prefix := newRetainedPrivateFixture(t, epochs)
			policy := defaultRetainedPrivatePolicy()
			policy.maximumRows = limit
			got, err := prefix.observe(context.Background(), policy)
			retainedPrivateAssertRefused(t, got, err)
			if len(prefix.reader.trace) > limit {
				t.Fatal("read after prefix cap", epochs, limit, len(prefix.reader.trace))
			}
		}
		t.Logf("source-valid fake-reader epochs=%d rows=%d bytes=%d; failed-prefix refusal without provider IO", epochs, len(result.rows), bytes)
	}
}
