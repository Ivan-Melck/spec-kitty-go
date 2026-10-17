package gapdb

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Qualification-only bookkeeping. This is NOT observer/Host admission or a
// complete allocation bound. The companion productionruntime geometry test
// verifies the actual private record layout; pinned compiler/runtime sources
// bind map header/table/group layout and growth. All pointees/keys/JSON/etc are
// separate uncovered events. Runtime/allocator profile is the existing slot leaf.
const retainedPrivateMapMissing = "Uncovered: map key/string backing, JSON/canonicalization, errors, contexts/timers, hashing setup, compiler/ABI extra storage, other per-call library/kernel objects. Shared/runtime storage separate. NOT full-call/peak/RSS accounting."

type retainedPrivateMapCounts struct{ rows, epochs, operations uint64 }
type retainedPrivateMapLedger struct {
	objects, requested, coveredSlotUpper                        uint64
	groupObjects, tableObjects, headerObjects, directoryObjects uint64
	fullCallCoverage                                            bool
	missingRecipes                                              string
}

func retainedPrivateMapAccounting(counts retainedPrivateMapCounts) (retainedPrivateMapLedger, bool) {
	zero := retainedPrivateMapLedger{}
	if counts.rows > 195 || counts.epochs > 64 || counts.operations > counts.epochs {
		return zero, false
	}
	l := retainedPrivateMapLedger{missingRecipes: retainedPrivateMapMissing}
	charge := func(request uint64) bool {
		s, ok := retainedAllocatorRound(request, true)
		if !ok {
			return false
		}
		l.objects, ok = retainedAccountingAdd(l.objects, 1)
		if !ok {
			return false
		}
		l.requested, ok = retainedAccountingAdd(l.requested, request)
		if !ok {
			return false
		}
		l.coveredSlotUpper, ok = retainedAccountingAdd(l.coveredSlotUpper, s.slotUpper)
		return ok
	}
	// Header48, table32 and directory8 are pinned compiler/runtime layouts.
	// Group520 = ctrl8 + 8*(key16 + record48); group200 includes bool-slot
	// padding: ctrl8 + 8*align8(key16 + bool1). No indirect slot allocations.
	for _, m := range []struct{ n, group uint64 }{{counts.rows, 520}, {counts.epochs, 200}, {counts.operations, 200}} {
		if !charge(48) {
			return zero, false
		}
		l.headerObjects++ // conditional heap header, even empty prefix
		if m.n == 0 {
			continue
		}
		if !charge(m.group) {
			return zero, false
		}
		l.groupObjects++
		if m.n <= 8 {
			continue
		}
		if !charge(8) {
			return zero, false
		}
		l.directoryObjects++
		for capacity := uint64(16); ; capacity *= 2 {
			if capacity > 256 {
				return zero, false
			} // model-domain tripwire, NOT a product cap
			request, ok := retainedAccountingMul(capacity/8, m.group)
			if !ok || !charge(request) || !charge(32) {
				return zero, false
			}
			l.groupObjects++
			l.tableObjects++
			if m.n <= capacity*7/8 {
				break
			}
		}
	}
	return l, true
}

type retainedPrivateCopyLedger struct {
	readAcceptedValues, readAcceptedBytes                                           uint64
	acceptedCopySlots, terminalCopyReserve, coveredSlotUpper, distributionFreeUpper uint64
	fullCallCoverage                                                                bool
	missingRecipes                                                                  string
}

// lengths describes ALL rows passing read's size/ownership/revision guards,
// including a final row whose later schema/canonical validation fails. It is
// NOT only successful complete-chain output. A failed pending Get can first
// incur one adapter conversion copy <=4MiB; work.read then rejects before its
// bytes.Clone. Keep that reserve even for the empty-prefix arithmetic control.
func retainedPrivateCopyAccounting(lengths []uint64) (retainedPrivateCopyLedger, bool) {
	zero := retainedPrivateCopyLedger{}
	if len(lengths) > 195 {
		return zero, false
	}
	l := retainedPrivateCopyLedger{missingRecipes: retainedPrivateMapMissing}
	for _, n := range lengths {
		if n == 0 || n > 4096 {
			return zero, false
		}
		var ok bool
		l.readAcceptedBytes, ok = retainedAccountingAdd(l.readAcceptedBytes, n)
		if !ok || l.readAcceptedBytes > 256<<10 {
			return zero, false
		}
		backing, ok := retainedAllocatorAppendNil(n, 1, false)
		if !ok {
			return zero, false
		}
		two, ok := retainedAccountingMul(2, backing.slotUpper)
		if !ok {
			return zero, false
		}
		l.acceptedCopySlots, ok = retainedAccountingAdd(l.acceptedCopySlots, two)
		if !ok {
			return zero, false
		}
	}
	l.readAcceptedValues = uint64(len(lengths))
	terminal, ok := retainedAllocatorAppendNil(MaxCheckpointValueBytes, 1, false)
	if !ok {
		return zero, false
	}
	l.terminalCopyReserve = terminal.slotUpper
	l.coveredSlotUpper, ok = retainedAccountingAdd(l.acceptedCopySlots, l.terminalCopyReserve)
	if !ok {
		return zero, false
	}
	logical, ok := retainedAccountingMul(2, l.readAcceptedBytes)
	if !ok {
		return zero, false
	}
	slack, ok := retainedAccountingMul(2*639, l.readAcceptedValues)
	if !ok {
		return zero, false
	}
	l.distributionFreeUpper, ok = retainedAccountingSum(logical, slack, l.terminalCopyReserve)
	if !ok || l.coveredSlotUpper > l.distributionFreeUpper {
		return zero, false
	}
	return l, true
}

func TestRetainedPrivateAllocationMapBoundaries(t *testing.T) {
	for _, tc := range []struct{ n, groups, tables uint64 }{{0, 0, 0}, {1, 1, 0}, {8, 1, 0}, {9, 2, 1}, {14, 2, 1}, {15, 3, 2}, {28, 3, 2}, {29, 4, 3}, {56, 4, 3}, {57, 5, 4}, {112, 5, 4}, {113, 6, 5}, {195, 6, 5}} {
		got, ok := retainedPrivateMapAccounting(retainedPrivateMapCounts{rows: tc.n})
		if !ok || got.groupObjects != tc.groups || got.tableObjects != tc.tables || got.headerObjects != 3 || got.fullCallCoverage || got.missingRecipes == "" {
			t.Fatal("occupancy boundary", tc, got, ok)
		}
	}
	got, ok := retainedPrivateMapAccounting(retainedPrivateMapCounts{195, 64, 64})
	if !ok || got.objects != 35 || got.requested != 45744 || got.coveredSlotUpper != 50920 || got.groupObjects != 16 || got.tableObjects != 13 || got.directoryObjects != 3 || got.headerObjects != 3 || got.fullCallCoverage {
		t.Fatal("private direct-map envelope", got, ok)
	}
	t.Logf("private direct-map conditional heap slots=%d requests=%d objects=%d incomplete=true", got.coveredSlotUpper, got.requested, got.objects)
	for _, in := range []retainedPrivateMapCounts{{196, 0, 0}, {0, 65, 0}, {0, 1, 2}, {math.MaxUint64, 0, 0}, {0, math.MaxUint64, 0}, {0, 0, math.MaxUint64}} {
		if bad, ok := retainedPrivateMapAccounting(in); ok || bad != (retainedPrivateMapLedger{}) {
			t.Fatal("invalid map input returned partial", in, bad)
		}
	}
	var prior uint64
	for n := uint64(0); n <= 195; n++ {
		got, ok := retainedPrivateMapAccounting(retainedPrivateMapCounts{n, min(n, 64), min(n, 64)})
		if !ok || got.coveredSlotUpper < prior {
			t.Fatal("nonmonotone prefix", n, got)
		}
		prior = got.coveredSlotUpper
	}
}

func TestRetainedPrivateAllocationCopies(t *testing.T) {
	for _, in := range [][]uint64{nil, {1}, {1, 7, 8, 15, 16, 513, 3457, 4096}} {
		got, ok := retainedPrivateCopyAccounting(in)
		if !ok || got.terminalCopyReserve != 4<<20 || got.fullCallCoverage || got.missingRecipes == "" || got.coveredSlotUpper > got.distributionFreeUpper {
			t.Fatal("private copies", got, ok)
		}
	}
	// Max P and cardinality together, not195 independent4KiB values.
	lengths := make([]uint64, 195)
	for i := range lengths {
		lengths[i] = 1344
	}
	lengths[0] += 64 // exactly256KiB
	got, ok := retainedPrivateCopyAccounting(lengths)
	if !ok || got.readAcceptedBytes != 256<<10 || got.readAcceptedValues != 195 || got.distributionFreeUpper != 4967802 {
		t.Fatal("joint private-copy envelope", got, ok)
	}
	t.Logf("private value copies scoped-slots=%d distribution-free-upper=%d reserve=%d incomplete=true", got.coveredSlotUpper, got.distributionFreeUpper, got.terminalCopyReserve)
	for _, in := range [][]uint64{{0}, {4097}, {math.MaxUint64}, make([]uint64, 196)} {
		if got, ok := retainedPrivateCopyAccounting(in); ok || got != (retainedPrivateCopyLedger{}) {
			t.Fatal("invalid copy input returned partial", in, got)
		}
	}
	over := append([]uint64(nil), lengths...)
	over[0]++
	if got, ok := retainedPrivateCopyAccounting(over); ok || got != (retainedPrivateCopyLedger{}) {
		t.Fatal("aggregate private-copy overflow", got)
	}
	for _, n := range []uint64{0, 1, 8, 15, 16, 4096, 4097, 32760, 32761, 4<<20 - 1, 4 << 20} {
		s, ok := retainedAllocatorAppendNil(n, 1, false)
		if !ok || s.slotUpper > 4<<20 {
			t.Fatal("terminal copy reserve underbounds", n, s)
		}
	}
}

func TestRetainedPrivateAllocationPinnedSources(t *testing.T) {
	retainedAllocatorPinnedRuntime(t)
	g := filepath.Join(runtime.GOROOT(), "src")
	// Whole-file hashes bind inspected growth/control flow, not unread paths.
	for path, want := range map[string]string{
		filepath.Join("..", "..", "productionruntime", "retained_private_records.go"): "e29bc68b019d666e1cb8bf38043e02ce9e81e03b3e7c047aaee06a4a2c47d47e",
		"adapter.go": "eb33f436a3a37652bd3abf96b2e6c45b3d6df314324b8260946c6d8e9a7a4a5f",
		filepath.Join("..", "..", "controljournal", "v2", "model.go"): "c57bced12d85843eb02a40e2291a6e52d0e1526b5ffc53b2046e91dcb43d22ca",
		filepath.Join(g, "internal/abi/map.go"):                       "e3b5f05fab86b2002ea4884f998e32b84093e8569bc68d9976d59faa1e80c554",
		filepath.Join(g, "internal/runtime/maps/map.go"):              "cec1f588f261caf8fe4a9b310fae11abcc162d5c975be21d243f9ce727151432",
		filepath.Join(g, "internal/runtime/maps/table.go"):            "d15567aa1735a8221599af3b11f857292a7b270daaa2820ff4e8053d4f172112",
		filepath.Join(g, "internal/runtime/maps/group.go"):            "cd99fe64dc1c3aa3a80516a070e6826afc424ddb11e883cd9dd761a2d5570aef",
		filepath.Join(g, "internal/runtime/maps/runtime_faststr.go"):  "9a4abcef59b9a68f01a20da7d68b783c0b3c276a0b876810e45cdc5b4965e1ab",
		filepath.Join(g, "cmd/compile/internal/reflectdata/map.go"):   "7427d5deaa4faa5a981f61a9e0fba32d77c05175744d299db74cc47c2b92557f",
		filepath.Join(g, "cmd/compile/internal/walk/builtin.go"):      "25feb9dedc767bf2564fb4e83b49162a2da6d6ad35f8d817f53f3fd9807049f9",
		filepath.Join(g, "bytes/bytes.go"):                            "2228844174e0f9b5e751bade463cb9ad5f0cd8ef209f5cabcb3cc18d8844438c",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("private allocation source drift; re-audit", path)
		}
		t.Logf("source %s sha256=%s", path, want)
	}
}
