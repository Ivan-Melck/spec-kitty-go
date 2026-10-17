package gapdb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

// ONLY named private-helper JSON events, not transport JSON or full-call memory.
// Reserve one decoder/reader at every Core entry even when its fast path creates
// neither; do not mistake this conservative envelope for an actual call census.
type retainedPrivateJSONInput struct{ strict, core []uint64 }
type retainedPrivateJSONLedger struct {
	decoderCandidates, refillRequested, refillSlotUpper, decoderObjects, validScannerObjects, fastCopySlots, totalCoveredSlotUpper uint64
	fullCallCoverage                                                                                                               bool
	missingRecipes                                                                                                                 string
}

const retainedPrivateJSONMissing = "Uncovered owned events: schema/target/error objects; scanner/token/span/array/map backings; unquote/fold/number/string/reflection/boxing; Marshal buffers/output/sorting/scratch; formatting/keys; other codec/context/connection/kernel/compiler events. Pool/shared/runtime/stack costs separately disclosed. NOT full-call/peak/RSS."

// The capacity in make([]byte,len,2*cap+512) is EXACTLY the requested Go
// capacity, NOT its physical size class. Free>=512 precludes further growth,
// including the next bytes.Reader EOF read. This deliberately reserves EOF
// growth even for a small scalar/whitespace prefix that never fills its buffer.
func retainedPrivateJSONRefill(n uint64) (requested, slots uint64, ok bool) {
	if n == 0 || n > 4096 {
		return 0, 0, false
	}
	var capacity uint64
	for capacity < n+512 {
		capacity = 2*capacity + 512 // domain proves <=7680; no wrapped inputs
		s, valid := retainedAllocatorRound(capacity, false)
		if !valid {
			return 0, 0, false
		}
		requested += capacity
		slots += s.slotUpper
	}
	return requested, slots, true
}
func retainedPrivateJSONAccounting(in retainedPrivateJSONInput) (retainedPrivateJSONLedger, bool) {
	zero := retainedPrivateJSONLedger{}
	if len(in.strict) > 65 || len(in.core) > 260 {
		return zero, false
	}
	var accepted uint64
	for _, n := range in.strict {
		if n == 0 || n > 4096 || n > (256<<10)-accepted {
			return zero, false
		}
		accepted += n
	}
	l := retainedPrivateJSONLedger{missingRecipes: retainedPrivateJSONMissing}
	for _, family := range [][]uint64{in.strict, in.core} {
		for _, n := range family {
			r, s, ok := retainedPrivateJSONRefill(n)
			if !ok {
				return zero, false
			}
			l.decoderCandidates++
			l.refillRequested += r
			l.refillSlotUpper += s
		}
	}
	// Classic Go1.26.7 Decoder304 -> scan320; bytes.Reader40 -> scan48.
	// json.Valid's pooled scanner64 is separately reserved per Core entry.
	// Pointees, backing stacks and pool/cache growth are NOT included here.
	l.decoderObjects = 368 * l.decoderCandidates
	l.validScannerObjects = 64 * uint64(len(in.core))
	for _, n := range in.core {
		copy, ok := retainedAllocatorAppendNil(n, 1, false)
		if !ok {
			return zero, false
		}
		l.fastCopySlots += copy.slotUpper // one source append on fast path only
	}
	l.totalCoveredSlotUpper = l.refillSlotUpper + l.decoderObjects + l.validScannerObjects + l.fastCopySlots
	return l, true // bounded counts/lengths prove all sums < 6MiB, no unchecked unbounded product
}

func TestRetainedPrivateJSONAccounting(t *testing.T) {
	for _, row := range []struct{ n, requested, slots uint64 }{{1, 2048, 2048}, {512, 2048, 2048}, {1024, 2048, 2048}, {1025, 5632, 6144}, {3072, 5632, 6144}, {3073, 13312, 14336}, {4096, 13312, 14336}} {
		r, s, ok := retainedPrivateJSONRefill(row.n)
		if !ok || r != row.requested || s != row.slots {
			t.Fatal("EOF/capacity/per-event rounding", row, r, s, ok)
		}
	}
	strict, core := make([]uint64, 65), make([]uint64, 260)
	for i := range strict {
		strict[i] = 3073
	}
	for i := range core {
		core[i] = 4096
	}
	l, ok := retainedPrivateJSONAccounting(retainedPrivateJSONInput{strict, core})
	if !ok || l.decoderCandidates != 325 || l.refillSlotUpper != 4659200 || l.totalCoveredSlotUpper != 5860400 || l.fullCallCoverage || l.missingRecipes == "" {
		t.Fatal("private JSON conditional envelope", l, ok)
	}
	z, ok := retainedPrivateJSONAccounting(retainedPrivateJSONInput{})
	if !ok || z.totalCoveredSlotUpper != 0 || z.fullCallCoverage || z.missingRecipes == "" {
		t.Fatal("zero branch disclosure")
	}
	for _, bad := range []retainedPrivateJSONInput{{strict: make([]uint64, 66)}, {core: make([]uint64, 261)}, {strict: []uint64{0}}, {core: []uint64{0}}, {strict: []uint64{4097}}, {core: []uint64{math.MaxUint64}}, {strict: append(bytesToJSONLengths(64, 4096), 1)}} {
		if got, ok := retainedPrivateJSONAccounting(bad); ok || got != (retainedPrivateJSONLedger{}) {
			t.Fatal("invalid ledger not entirely zero", bad, got)
		}
	}
	t.Logf("conditional private JSON family upper=%d; source reserves, not actual allocations/full call", l.totalCoveredSlotUpper)
}
func bytesToJSONLengths(count int, n uint64) []uint64 {
	v := make([]uint64, count)
	for i := range v {
		v[i] = n
	}
	return v
}

type retainedJSONReadTrace struct {
	reader     *bytes.Reader
	decoder    *json.Decoder
	delivered  int
	capacities []int
}

func (r *retainedJSONReadTrace) Read(p []byte) (int, error) {
	// During refill, InputOffset includes the prefix just slid away. Thus
	// remaining delivered bytes + this read window = actual Go capacity.
	// This uses a public scalar getter, not unsafe private-buffer inspection.
	capacity := r.delivered - int(r.decoder.InputOffset()) + len(p)
	if len(r.capacities) == 0 || capacity != r.capacities[len(r.capacities)-1] {
		r.capacities = append(r.capacities, capacity)
	}
	n, err := r.reader.Read(p)
	r.delivered += n
	return n, err
}
func TestRetainedPrivateJSONDecoderEOFCapacities(t *testing.T) {
	retainedAllocatorPinnedRuntime(t)
	for _, n := range []int{1, 2, 511, 512, 513, 1024, 1025, 1536, 3072, 3073, 3584, 4096} {
		r := &retainedJSONReadTrace{reader: bytes.NewReader(bytes.Repeat([]byte{' '}, n))}
		var target struct{}
		r.decoder = json.NewDecoder(r)
		if err := r.decoder.Decode(&target); err != io.EOF {
			t.Fatal("whitespace decoder did not reach EOF", n, err)
		}
		var requested uint64
		prior := 0
		for _, c := range r.capacities {
			if c != 2*prior+512 {
				t.Fatal("make capacity drift", r.capacities)
			}
			requested += uint64(c)
			prior = c
		}
		want, _, ok := retainedPrivateJSONRefill(uint64(n))
		if !ok || requested != want {
			t.Fatal("EOF-prefix event reserve", n, r.capacities, requested, want)
		}
	}
	// Multiple Decode and Token operations, malformed/trailing data and escaped
	// fields share one decoder. Reader records are NOT heap/B-op measurements.
	for _, raw := range []string{`{} {}`, `{"x":"\\\\u003c"}`, `{"a":1,"a":2}`, `[1,2,3]`, stringsOfJSONOpeners(4096)} {
		for _, tokens := range []bool{false, true} {
			r := &retainedJSONReadTrace{reader: bytes.NewReader([]byte(raw))}
			r.decoder = json.NewDecoder(r)
			if tokens {
				for i := 0; i < 2*len(raw)+1; i++ {
					if _, err := r.decoder.Token(); err != nil {
						break
					}
				}
			} else {
				var x any
				_ = r.decoder.Decode(&x)
				_ = r.decoder.Decode(&x)
			}
			var requested uint64
			prior := 0
			for _, c := range r.capacities {
				if c != 2*prior+512 {
					t.Fatal("sliding/token make-capacity drift", r.capacities)
				}
				requested += uint64(c)
				prior = c
			}
			bound, _, ok := retainedPrivateJSONRefill(uint64(len(raw)))
			if !ok || requested > bound || prior > 7680 {
				t.Fatal("shared decoder corpus exceeded buffer reserve", len(raw), tokens, r.capacities, requested, bound)
			}
		}
	}
}
func stringsOfJSONOpeners(n int) string { return string(bytes.Repeat([]byte{'['}, n)) }

func TestRetainedPrivateJSONPinnedSourcesAndObjects(t *testing.T) {
	retainedAllocatorPinnedRuntime(t)
	type scannerMirror struct {
		step       func(any, byte) int
		endTop     bool
		parseState []int
		err        error
		bytes      int64
	}
	for name, pair := range map[string][2]uintptr{"decoder": {unsafe.Sizeof(json.Decoder{}), 304}, "bytes_reader": {unsafe.Sizeof(bytes.Reader{}), 40}, "source_scanner_mirror": {unsafe.Sizeof(scannerMirror{}), 64}} {
		if pair[0] != pair[1] {
			t.Fatal("private JSON object profile drift", name, pair)
		}
	}
	for _, row := range []struct{ request, slot uint64 }{{304, 320}, {40, 48}, {64, 64}} {
		s, ok := retainedAllocatorRound(row.request, true)
		if !ok || s.slotUpper != row.slot {
			t.Fatal("scanned JSON object rounding", row, s)
		}
	}
	// Active standard library selection, not just environment's experiment text.
	raw, err := exec.Command("go", "list", "-json", "encoding/json").Output()
	if err != nil {
		t.Fatal(err)
	}
	var active struct{ GoFiles []string }
	if err := json.Unmarshal(raw, &active); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"decode.go", "stream.go", "encode.go"} {
		found := false
		for _, f := range active.GoFiles {
			found = found || f == name
		}
		if !found {
			t.Fatal("classic JSON implementation absent", name)
		}
	}
	g := filepath.Join(runtime.GOROOT(), "src")
	for path, want := range map[string]string{
		filepath.Join("..", "..", "productionruntime", "retained_private_records.go"): "e29bc68b019d666e1cb8bf38043e02ce9e81e03b3e7c047aaee06a4a2c47d47e",
		filepath.Join("..", "..", "productionruntime", "recovery.go"):                 "45cc1fa018b7be97fc2c8bff225a52f183c687ada5eebe61b8b033475699d839",
		filepath.Join("..", "..", "controljournal", "v2", "codec.go"):                 "23ee6a6a4b5e97d416205bb928fd4b86b5c582ef7c4e3daed0bcf1973b769054",
		filepath.Join(g, "encoding/json/stream.go"):                                   "bb26b2442db6a686fc7f823ea9cc89babe26ac988ec7d8caf4a157aeb60d92f2",
		filepath.Join(g, "encoding/json/decode.go"):                                   "a91535f18f9a2c8380448b1c84c4c2017dbe3d530a10faf8babf03494042da48",
		filepath.Join(g, "encoding/json/scanner.go"):                                  "2b16dd215274dfa8e0806b53cca144684d5b9c8a02319cf5ce10120c50e6eef2",
		filepath.Join(g, "bytes/reader.go"):                                           "5b42a3100971836c10b1e0d3699b46cffd41c8be23d09e1642ee42ff795dc6ab",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("JSON recipe source drift; re-audit", path)
		}
		t.Logf("source %s sha256=%s", path, want)
	}
	if reflect.TypeOf(json.Decoder{}).Size() != 304 {
		t.Fatal("decoder embedded/header geometry")
	}
}
