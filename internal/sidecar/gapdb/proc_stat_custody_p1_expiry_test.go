//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

// AUTHORED UNRUN E local census/returned-error components, not a physical
// failed-FIRST kernel Close witness or final constructor membership proof.
import (
	"errors"
	"os"
	"syscall"
	"testing"
)

func procStatP1Counts(t *testing.T, r *expiryCleanupReport, want uint64) {
	t.Helper()
	for category := range r.counts {
		n := uint64(0)
		if category == expiryLifecycleMetadata { n = want }
		c := &r.counts[category]
		if c.required.Load() != n || c.recorded.Load() != n || c.completed.Load() != n { t.Fatal("local acquisition/one-attempt/completion or other category changed", category, c.required.Load(), c.recorded.Load(), c.completed.Load(), n) }
	}
}

func TestProcStatP1ExpiryActualDefaultBindingAndOpenFailure(t *testing.T) {
	r := new(expiryCleanupReport)
	config, stop := procStatP1Config(t, processAPI{})
	if err := normalizeLifecycleConfigWithReport(&config, r); !errors.Is(err, stop) { t.Fatal(err) }
	procStatP1Counts(t, r, 0)
	want, err := processStartTicks(os.Getpid())
	if want == 0 || err != nil { t.Fatal(want, err) }
	for index := uint64(1); index <= 2; index++ {
		if got, err := config.processes.startTicks(os.Getpid()); err != nil || got != want { t.Fatal(got, err) }
		procStatP1Counts(t, r, index)
	}
	if got, err := config.processes.startTicks(-1); got != 0 || !errors.Is(err, os.ErrNotExist) { t.Fatal(got, err) }
	procStatP1Counts(t, r, 2) // failed open never transferred an owner
	if r.first.Load() != nil || r.firstError() != nil { t.Fatal("successful local closes fabricated FIRST", r.firstError()) }
	custom, stop := procStatP1Config(t, processAPI{startTicks: func(int) (uint64, error) { return 7, nil }})
	if err := normalizeLifecycleConfigWithReport(&custom, r); !errors.Is(err, stop) { t.Fatal(err) }
	if ticks, err := custom.processes.startTicks(os.Getpid()); ticks != 7 || err != nil { t.Fatal(ticks, err) }
	procStatP1Counts(t, r, 2) // no bypass of inherited custom test hook
}

func TestProcStatP1ExpiryActualProductionOwnedControls(t *testing.T) {
	for _, mode := range []string{"valid", "malformed", "numeric", "read-directory", "closed-object"} {
		t.Run(mode, func(t *testing.T) {
			raw := procStatP1Bytes("17")
			if mode == "malformed" { raw = []byte("malformed") }
			if mode == "numeric" { raw = procStatP1Bytes("not-numeric") }
			path := procStatP1Path(t, raw)
			if mode == "read-directory" { path = t.TempDir() }
			file := procStatP1Open(t, path, mode == "closed-object")
			r := new(expiryCleanupReport)
			ticks, got := processStartTicksFileWithReport(file, r) // ACTUAL fixed production method
			procStatP1Consumed(t, file)
			procStatP1Counts(t, r, 1)
			if mode == "valid" {
				if ticks != 17 || got != nil { t.Fatal(ticks, got) }
			} else if ticks != 0 || got == nil { t.Fatal("unusable scalar escaped", ticks, got) }
			if mode == "closed-object" {
				var primary *os.PathError
				if !errors.As(got, &primary) || primary.Op != "read" || !errors.Is(got, os.ErrClosed) { t.Fatal("read primary changed", got) }
				first := r.first.Load()
				var closeError *os.PathError
				if first == nil || first.category != expiryLifecycleMetadata || !errors.As(first.err, &closeError) || closeError.Op != "close" || !errors.Is(r.firstError(), os.ErrClosed) { t.Fatal("actual closed-object close outcome missing", r.firstError()) }
				return
			}
			// Parse/read primaries alone are NOT misreported as close failures.
			if r.first.Load() != nil || r.firstError() != nil { t.Fatal("successful actual Close reported failure", r.firstError()) }
			if mode == "read-directory" && !errors.Is(got, syscall.EISDIR) { t.Fatal("actual directory read error missing", got) }
		})
	}
}

func TestProcStatP1ExpiryReturnedClosePrimaryAndStickyFirst(t *testing.T) {
	for _, raw := range [][]byte{procStatP1Bytes("29"), []byte("malformed"), procStatP1Bytes("bad"), procStatP1Bytes("0")} {
		path := procStatP1Path(t, raw)
		r := new(expiryCleanupReport)
		file := procStatP1Open(t, path, false)
		calls := 0
		var actual error
		ticks, got := processStartTicksFileWithClose(file, r, procStatP1ReturnedClose(t, &calls, &actual, syscall.EIO))
		if ticks != 0 || calls != 1 || actual != nil || !errors.Is(got, syscall.EIO) { t.Fatal("actual Close + modeled return conjunction lost", ticks, got, calls, actual) }
		procStatP1Consumed(t, file)
		procStatP1Counts(t, r, 1)
		first := r.first.Load()
		if first == nil || first.category != expiryLifecycleMetadata || first.err != syscall.EIO || !errors.Is(r.firstError(), syscall.EIO) { t.Fatal("local FIRST missing", r.firstError()) }
		if string(raw) == "malformed" && got.Error() != "process stat is malformed" { t.Fatal("parse primary prose changed", got) }
		if string(raw) != string(procStatP1Bytes("29")) {
			var union *startupCustodyError
			if !errors.As(got, &union) || !errors.Is(got, union.primary) || !errors.Is(got, union.custody) { t.Fatal("primary Is/As ancestry lost", got) }
		}
		// Same report, separate newly acquired files. Never repeat Close on a
		// consumed object; later success/error cannot erase or replace FIRST.
		good := procStatP1Path(t, procStatP1Bytes("31"))
		if ticks, err := processStartTicksFileWithReport(procStatP1Open(t, good, false), r); ticks != 31 || err != nil { t.Fatal(ticks, err) }
		laterCalls := 0
		var laterActual error
		if ticks, err := processStartTicksFileWithClose(procStatP1Open(t, good, false), r, procStatP1ReturnedClose(t, &laterCalls, &laterActual, syscall.ENOSPC)); ticks != 0 || !errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EIO) { t.Fatal("call result must retain its own close outcome, not substituted report FIRST", ticks, err) }
		if laterCalls != 1 || laterActual != nil || r.first.Load() != first || !errors.Is(r.firstError(), syscall.EIO) || errors.Is(r.firstError(), syscall.ENOSPC) { t.Fatal("immutable report FIRST replaced", r.firstError()) }
		procStatP1Counts(t, r, 3)
	}
}

func TestProcStatP1ExpiryReadPrimaryPlusReturnedClose(t *testing.T) {
	file := procStatP1Open(t, t.TempDir(), false)
	r := new(expiryCleanupReport)
	calls := 0
	var actual error
	ticks, got := processStartTicksFileWithClose(file, r, procStatP1ReturnedClose(t, &calls, &actual, syscall.EIO))
	var primary *os.PathError
	if ticks != 0 || calls != 1 || actual != nil || !errors.As(got, &primary) || primary.Op != "read" || got.Error() != primary.Error() || !errors.Is(got, syscall.EISDIR) || !errors.Is(got, syscall.EIO) { t.Fatal("actual read primary/type/Is/As + modeled close failed", ticks, got, calls, actual) }
	procStatP1Counts(t, r, 1)
	if !errors.Is(r.firstError(), syscall.EIO) || errors.Is(r.firstError(), syscall.EISDIR) { t.Fatal("report must retain returned Close, not read primary", r.firstError()) }
}

func TestProcStatP1ExpirySelectedActualCloseSource(t *testing.T) {
	procStatP1SelectedPin(t, "expiry_qualification_work.go", "554adfe4be10f4f87c22ac11aeb18978a4ab89826258c76e2519d6b82c927fac")
	procStatP1SelectedPin(t, "expiry_qualification_launch.go", "e053358097f0b3fd5771fe649333781095b8aa5658a15423acdd733c440c79d1")
	procStatP1FunctionEquals(t, "expiry_qualification_work.go", "closeLocalFile", `func (r *expiryCleanupReport) closeLocalFile(file *os.File) error {
		err := file.Close()
		r.localClosed(err)
		return err
	}`)
	procStatP1FunctionEquals(t, "expiry_qualification_work.go", "localOpened", `func (r *expiryCleanupReport) localOpened() { r.acquired(expiryLifecycleMetadata) }`)
	procStatP1FunctionEquals(t, "expiry_qualification_work.go", "localClosed", `func (r *expiryCleanupReport) localClosed(err error) { r.closing(expiryLifecycleMetadata); r.closed(expiryLifecycleMetadata, err) }`)
}
