package retainedkernel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"
)

// Named source-event buffer families ONLY; not a kernel-check/call heap bound.
// C counts attempted checkLocked invocations, including failed prefixes, NOT
// merely completed hashes. Owner/lifecycle startup is outside observation scope.
type retainedKernelBufferLedger struct {
	chunkSlots, procBufferObjects, procBackingSlots, procSuffixSlots, totalCoveredSlotUpper uint64
	fullCallCoverage                                                                        bool
	missingRecipes                                                                          string
}

func retainedKernelBufferAccounting(checks, image uint64) (retainedKernelBufferLedger, bool) {
	zero := retainedKernelBufferLedger{}
	// Existing observation envelope, no new product ceiling. The arithmetic
	// image work stays separate from storage; independent maxima are not joint.
	if image == 0 || image > maxImageBytes || checks > 12883 || checks > 0 && image > (32<<30)/checks {
		return zero, false
	}
	const perCheck = 3*32768 + 2*48 + 2*524288 + 2*65536
	if checks > math.MaxUint64/perCheck {
		return zero, false
	}
	l := retainedKernelBufferLedger{chunkSlots: 3 * 32768 * checks, procBufferObjects: 2 * 48 * checks, procBackingSlots: 2 * 524288 * checks, procSuffixSlots: 2 * 65536 * checks, totalCoveredSlotUpper: perCheck * checks,
		missingRecipes: "Uncovered: Fields/FieldsFunc spans and header arrays, SHA/FIPS state/output, formatting/errors, file/stat/path and net/kernel wrappers, callbacks/context/finalizer/runtime and extra compiler/ABI events. NOT full-call/peak/RSS accounting."}
	return l, true
}

func TestRetainedKernelBufferAccounting(t *testing.T) {
	for _, in := range [][2]uint64{{0, 1}, {1, 64 << 20}, {12883, (32 << 30) / 12883}} {
		l, ok := retainedKernelBufferAccounting(in[0], in[1])
		if !ok || l.totalCoveredSlotUpper != 1278048*in[0] || l.fullCallCoverage || l.missingRecipes == "" {
			t.Fatal("kernel partial-buffer scope/arithmetic", in, l, ok)
		}
		t.Logf("attempted-checks=%d covered-normal-buffer-slot-upper=%d incomplete=true; image work separate", in[0], l.totalCoveredSlotUpper)
	}
	for _, in := range [][2]uint64{{0, 0}, {1, 64<<20 + 1}, {12884, 1}, {12883, 64 << 20}, {math.MaxUint64, 1}, {1, math.MaxUint64}} {
		if l, ok := retainedKernelBufferAccounting(in[0], in[1]); ok || l != (retainedKernelBufferLedger{}) {
			t.Fatal("invalid kernel envelope returned partial", in, l)
		}
	}
}

type retainedPartitionReader struct{ remaining, part int }

func (r *retainedPartitionReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining, r.part)
	clear(p[:n])
	r.remaining -= n
	return n, nil
}

type retainedGrowthBuffer struct {
	buffer     bytes.Buffer
	capacities []int
}

func (b *retainedGrowthBuffer) Write(p []byte) (int, error) {
	before := b.buffer.Cap()
	n, err := b.buffer.Write(p)
	if b.buffer.Cap() != before {
		b.capacities = append(b.capacities, b.buffer.Cap())
	}
	return n, err
}

func TestRetainedKernelBufferGrowthAndShortReads(t *testing.T) {
	retainedKernelAllocationProfile(t)
	// Observe capacity transitions, NOT actual heap allocation or B/op.
	// The wrapper does not read/reset the destination Buffer; off stays zero.
	for _, total := range []int{0, 1, 63, 64, 65, 32767, 32768, 32769, maxProcBytes, maxProcBytes + 1} {
		for _, part := range []int{1, 7, 63, 64, 65, 4096, 16385, 24577, 32768} {
			r := &retainedPartitionReader{total, part}
			b := &retainedGrowthBuffer{}
			err := readChunks(r, maxProcBytes, b, func() error { return nil })
			if (total <= maxProcBytes) != (err == nil) || b.buffer.Len() > maxProcBytes {
				t.Fatal("read/write prefix", total, part, b.buffer.Len(), err)
			}
			if len(b.capacities) > 12 || b.buffer.Cap() > 2*maxProcBytes {
				t.Fatal("proc growth count/cap", total, part, b.capacities)
			}
			prior, sum := 0, 0
			for _, c := range b.capacities {
				if c < 64 || prior != 0 && c < 2*prior {
					t.Fatal("non-geometric write-only growth", b.capacities)
				}
				prior = c
				sum += c
			}
			if sum > 4*maxProcBytes || 2*sum > 8*maxProcBytes {
				t.Fatal("growth backing/temporary reserve", b.capacities, sum)
			}
		}
	}
}

func retainedKernelAllocationProfile(t *testing.T) {
	t.Helper()
	if runtime.Version() != "go1.26.7" || runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("pinned normal buffer recipe requires Go1.26.7/linux/amd64")
	}
}
func TestRetainedKernelBufferPinnedSources(t *testing.T) {
	retainedKernelAllocationProfile(t)
	if unsafe.Sizeof(bytes.Buffer{}) != 40 || maxProcBytes != 65536 || maxImageBytes != 64<<20 {
		t.Fatal("buffer geometry/caps drift")
	}
	for path, want := range map[string]string{
		"lifetime.go":                       "69395b56089d7749029a4b853ef8a818c8ec98a4a6b845d5b1cf7efc24dac61e",
		"file_open.go":                      "777d986dbd63e0a0323580a1849d5f25dc79d6e82db8f339d31fb1875d863b2e",
		"../retained_observation_budget.go": "8e1ce18bb2ac53a93f99e7e254935ab543760988562c1c73ea83c7d1bb8e05a0",
		filepath.Join(runtime.GOROOT(), "src/bytes/buffer.go"):                    "4457385c174f963728d57902c5b474dacf5c2b7226f103045371a2b4cde7f119",
		filepath.Join(runtime.GOROOT(), "src/runtime/msize.go"):                   "0164d15295726608812842b429e241f553a314985a7c6aa48cb1ad24346a52dd",
		filepath.Join(runtime.GOROOT(), "src/internal/runtime/gc/sizeclasses.go"): "7626b00bbb416c25c5279b29aa73a072d74ace3444e58b178a6ff855a2deec2c",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			t.Fatal("kernel buffer source drift; re-audit", path)
		}
		t.Logf("buffer source %s sha256=%s", path, want)
	}
}
