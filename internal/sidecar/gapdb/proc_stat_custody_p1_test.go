//go:build linux && amd64

package gapdb

// AUTHORED UNRUN ordinary/E components. No child, signal, raw-FD fault,
// physical failed-FIRST Close, Host/Native or timing evidence is claimed.
import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func procStatP1Bytes(value string) []byte {
	fields := strings.Fields("S 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 " + value + " 99")
	return []byte("123 (name with ) nested) " + strings.Join(fields, " ") + "\n")
}

func procStatP1Path(t *testing.T, raw []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(path, raw, 0600); err != nil { t.Fatal(err) }
	return path
}

func procStatP1Open(t *testing.T, path string, preclosed bool) *os.File {
	t.Helper()
	file, err := os.Open(path)
	if err != nil { t.Fatal(err) }
	if preclosed {
		// CLOSED-OBJECT control only: the product later calls this File object,
		// never a broken/reused descriptor integer. Not a failed FIRST Close.
		if err := file.Close(); err != nil { t.Fatal(err) }
	}
	return file
}

func procStatP1Consumed(t *testing.T, file *os.File) {
	t.Helper()
	// Metadata observation only: do NOT Close again to prove the product Close.
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) { t.Fatal("owned file not consumed", err) }
}

func TestProcStatP1RealSelfAndOpenRefusal(t *testing.T) {
	a, err := processStartTicks(os.Getpid())
	if err != nil || a == 0 { t.Fatal("real self proc start not positive", a, err) }
	b, err := processStartTicksWithReport(os.Getpid(), nil)
	if err != nil || b != a { t.Fatal("ordinary delegation changed real start", b, err) }
	for _, call := range []func(int) (uint64, error){processStartTicks, func(pid int) (uint64, error) { return processStartTicksWithReport(pid, nil) }} {
		ticks, err := call(-1)
		var pathErr *os.PathError
		if ticks != 0 || !errors.Is(err, os.ErrNotExist) || !errors.As(err, &pathErr) || pathErr.Op != "open" || pathErr.Path != "/proc/-1/stat" { t.Fatal("open error/scalar/path changed", ticks, err) }
	}
}

func TestProcStatP1OriginalParserAndActualProductionClose(t *testing.T) {
	for _, row := range []struct { name string; raw []byte; want uint64; message string }{
		{"positive-last-paren-field19", procStatP1Bytes("987654321"), 987654321, ""},
		{"maximum", procStatP1Bytes("18446744073709551615"), ^uint64(0), ""},
		{"no-paren", []byte("x"), 0, "process stat is malformed"},
		{"empty", nil, 0, "process stat is malformed"},
		{"paren-only", []byte(")"), 0, "process stat is malformed"},
		{"paren-space", []byte(") "), 0, "process stat is malformed"},
		{"short", []byte("1 (name) S 1 2"), 0, "process stat lacks start time"},
		{"nineteen-fields", []byte("1 (name) " + strings.TrimSpace(strings.Repeat("1 ", 19))), 0, "process stat lacks start time"},
		{"zero", procStatP1Bytes("0"), 0, "process start time is invalid"},
		{"negative", procStatP1Bytes("-1"), 0, "process start time is invalid"},
		{"not-number", procStatP1Bytes("invalid"), 0, "process start time is invalid"},
		{"overflow", procStatP1Bytes("18446744073709551616"), 0, "process start time is invalid"},
	} {
		t.Run(row.name, func(t *testing.T) {
			file := procStatP1Open(t, procStatP1Path(t, row.raw), false)
			ticks, err := processStartTicksFileWithReport(file, nil)
			procStatP1Consumed(t, file)
			if ticks != row.want { t.Fatal("original index/value semantics changed", ticks) }
			if row.message == "" { if err != nil { t.Fatal(err) }; return }
			if err == nil || err.Error() != row.message { t.Fatal("original parser diagnostic changed", err) }
			var numeric *strconv.NumError
			if errors.As(err, &numeric) { t.Fatal("original normalized numeric error leaked", err) }
		})
	}
}

// This immediate closer models a returned-error boundary AFTER successful real
// File.Close and publishes that modeled returned error at the same local report
// boundary. It does not intercept production's literal method or fail a syscall.
func procStatP1ReturnedClose(t *testing.T, calls *int, actual *error, injected error) func(*expiryCleanupReport, *os.File) error {
	t.Helper()
	return func(report *expiryCleanupReport, file *os.File) error {
		(*calls)++
		*actual = file.Close()
		err := withStartupCustody(*actual, injected)
		report.localClosed(err)
		return err
	}
}

func TestProcStatP1ActualReadAndReturnedCloseConjunctions(t *testing.T) {
	for _, mode := range []string{"valid", "malformed", "short", "numeric", "zero", "read-directory", "closed-object"} {
		t.Run(mode, func(t *testing.T) {
			raw := procStatP1Bytes("41")
			switch mode {
			case "malformed": raw = []byte("not proc stat")
			case "short": raw = []byte("1 (name) S")
			case "numeric": raw = procStatP1Bytes("bad")
			case "zero": raw = procStatP1Bytes("0")
			}
			path := procStatP1Path(t, raw)
			if mode == "read-directory" { path = t.TempDir() }
			baselineFile := procStatP1Open(t, path, mode == "closed-object")
			_, primary := processStartTicksFileWithReport(baselineFile, nil)
			procStatP1Consumed(t, baselineFile)
			file := procStatP1Open(t, path, mode == "closed-object")
			calls := 0
			var actual error
			ticks, got := processStartTicksFileWithClose(file, nil, procStatP1ReturnedClose(t, &calls, &actual, syscall.EIO))
			if ticks != 0 || calls != 1 || !errors.Is(got, syscall.EIO) { t.Fatal("failed final scalar/cause/attempt", ticks, got, calls) }
			procStatP1Consumed(t, file)
			if mode == "closed-object" {
				if !errors.Is(actual, os.ErrClosed) || !errors.Is(got, os.ErrClosed) { t.Fatal("closed-object result lost", actual, got) }
			} else if actual != nil { t.Fatal("actual owned Close must succeed in this component", actual) }
			if mode == "valid" {
				if primary != nil || got != syscall.EIO { t.Fatal("nil primary did not return exact close-only error", primary, got) }
				return
			}
			var union *startupCustodyError
			if primary == nil || !errors.As(got, &union) || got.Error() != primary.Error() || !errors.Is(got, union.primary) || !errors.Is(got, union.custody) { t.Fatal("primary text/type/Is/As policy lost", primary, got) }
			if mode == "closed-object" {
				// Baseline also contains its genuine closed-object close error.
				var old *startupCustodyError
				if !errors.As(primary, &old) || reflect.TypeOf(union.primary) != reflect.TypeOf(old.primary) { t.Fatal("read primary type changed", got) }
			} else if reflect.TypeOf(union.primary) != reflect.TypeOf(primary) { t.Fatal("primary concrete type changed", primary, got) }
			if mode == "read-directory" || mode == "closed-object" {
				var p *os.PathError
				if !errors.As(got, &p) || p.Op != "read" || p.Path != path { t.Fatal("primary PathError ancestry lost", got) }
				if mode == "read-directory" && !errors.Is(got, syscall.EISDIR) { t.Fatal("actual read error lost", got) }
			} else {
				var numeric *strconv.NumError
				if errors.As(got, &numeric) { t.Fatal("numeric normalization changed", got) }
			}
		})
	}
}

func procStatP1Config(t *testing.T, api processAPI) (LifecycleConfig, error) {
	t.Helper()
	root := t.TempDir()
	stop := errors.New("component stops before storage admission")
	return LifecycleConfig{
		ExecutablePath: filepath.Join(root, "exe"), AdmissionRecordPath: filepath.Join(root, "admission"),
		RuntimeRoot: root, DataDirectory: root, SupervisorRunID: "proc-stat-p1", ProductPolicySHA256: strings.Repeat("a", 64),
		processes: api, filesystem: func(string) (filesystemIdentity, error) { return filesystemIdentity{}, stop },
	}, stop
}

func TestProcStatP1DefaultAndCustomBinding(t *testing.T) {
	config, stop := procStatP1Config(t, processAPI{})
	if err := normalizeLifecycleConfigWithReport(&config, nil); !errors.Is(err, stop) { t.Fatal("fixture missed topology stop", err) }
	if reflect.ValueOf(config.processes.startTicks).Pointer() != reflect.ValueOf(processStartTicks).Pointer() { t.Fatal("ordinary nil-report default replaced") }
	want, err := processStartTicks(os.Getpid())
	if err != nil || want == 0 { t.Fatal(want, err) }
	if got, err := config.processes.startTicks(os.Getpid()); err != nil || got != want { t.Fatal(got, err) }
	calls := 0
	hook := func(int) (uint64, error) { calls++; return 73, nil }
	for _, report := range []*expiryCleanupReport{nil, new(expiryCleanupReport)} {
		custom, stop := procStatP1Config(t, processAPI{startTicks: hook})
		if err := normalizeLifecycleConfigWithReport(&custom, report); !errors.Is(err, stop) { t.Fatal(err) }
		if reflect.ValueOf(custom.processes.startTicks).Pointer() != reflect.ValueOf(hook).Pointer() { t.Fatal("custom startTicks hook overwritten") }
		if got, err := custom.processes.startTicks(1); got != 73 || err != nil { t.Fatal(got, err) }
	}
	if calls != 2 { t.Fatal("custom hook call count", calls) }
}

func TestProcStatP1CallerPrimaryAndRefusal(t *testing.T) {
	// Custom processAPI remains an inherited test hook, not a production selector.
	// Each call consumes a separately opened real File, never repeats a close.
	path := procStatP1Path(t, []byte("malformed"))
	api := processAPI{startTicks: func(int) (uint64, error) {
		file := procStatP1Open(t, path, false)
		calls := 0
		var actual error
		ticks, err := processStartTicksFileWithClose(file, nil, procStatP1ReturnedClose(t, &calls, &actual, syscall.EIO))
		if calls != 1 || actual != nil || ticks != 0 { t.Fatal(calls, actual, ticks) }
		return ticks, err
	}, executable: func(int) (fileIdentity, string, error) { t.Fatal("unusable ticks reached executable/pidfd"); return fileIdentity{}, "", nil }}
	identity, fd, err := observeStartedProcessWithReport(api, os.Getpid(), ExecutedImageProvenance{}, nil)
	var diagnostic *Error
	if identity != (ProcessIdentity{}) || fd != -1 || !errors.Is(err, syscall.EIO) || !errors.As(err, &diagnostic) || diagnostic.Reason != "process start identity unavailable" || err.Error() != failure(FailureIdentityUnprovable, "process", diagnostic.Reason, nil).Error() { t.Fatal("attribution primary/scalar changed", identity, fd, err) }
	err = validateProcessIdentityWith(api, ProcessIdentity{PID: os.Getpid(), StartTicks: 1})
	if !errors.Is(err, syscall.EIO) || !errors.As(err, &diagnostic) || diagnostic.Reason != "daemon process start differs" { t.Fatal("proxy identity primary lost", err) }
	l := &Lifecycle{processes: api, process: ProcessIdentity{PID: os.Getpid(), StartTicks: 1}, pidfd: -1}
	if got := l.InspectOwnership(); got.State != OwnershipIndeterminate { t.Fatal("failed scalar granted live/exited ownership", fmt.Sprint(got)) }
	if got := l.inspectOwnershipLocked(); got.State != OwnershipIndeterminate { t.Fatal("stale precheck granted authority", got) }
}
