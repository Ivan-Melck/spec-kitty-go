//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

func TestExpiryC4RecordedCleanupErrorMissingReport(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    *ExpiryQualification
	}{
		{"nil", nil},
		{"zero", &ExpiryQualification{}},
		{"missingReport", &ExpiryQualification{state: &expiryGateState{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, full := tc.q.RecordedCleanupError(), tc.q.CleanupError()
			var queryError, fullError *Error
			if !errors.As(got, &queryError) || !errors.As(full, &fullError) ||
				*queryError != *fullError || queryError.Kind != FailureCleanup ||
				queryError.Operation != "expiry_cleanup" || queryError.Reason != "fixed cleanup report is absent" || queryError.Cause != nil {
				t.Fatalf("missing report must match full refusal: query=%v full=%v", got, full)
			}
		})
	}
}

func TestExpiryC4RecordedCleanupErrorIncompleteModelReadOnly(t *testing.T) {
	// Explicit report models only: none of these flags/counters claims a live
	// Host, actual close, scheduling cutoff or native first-close outcome.
	type snapshot struct {
		counts                                       [expiryCleanupCategories][3]uint64
		first                                        *expiryCleanupFailure
		operation, startup, teardown, bound, retired bool
		pending                                      uint64
	}
	sample := func(r *expiryCleanupReport) snapshot {
		s := snapshot{first: r.first.Load(), operation: r.operationPending.Load(), startup: r.startupPending.Load(),
			teardown: r.teardownPending.Load(), bound: r.lifecycleBound.Load(), retired: r.lifecycleRetired.Load(), pending: r.lifecyclePending.Load()}
		for i := range r.counts {
			c := &r.counts[i]
			s.counts[i] = [3]uint64{c.required.Load(), c.recorded.Load(), c.completed.Load()}
		}
		return s
	}
	for _, model := range []string{"operation", "startup", "external", "teardown", "unretired", "unattempted", "inflight"} {
		t.Run(model, func(t *testing.T) {
			r := new(expiryCleanupReport)
			switch model {
			case "operation":
				r.operationPending.Store(true)
			case "startup":
				r.startupPending.Store(true)
			case "external":
				r.lifecyclePending.Store(1)
			case "teardown":
				r.teardownPending.Store(true)
			case "unretired":
				r.lifecycleBound.Store(true)
			case "unattempted", "inflight":
				r.acquired(expiryLifecycleMetadata)
				if model == "inflight" {
					r.closing(expiryLifecycleMetadata)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			q := &ExpiryQualification{state: &expiryGateState{ctx: ctx, report: r}}
			alias := *q
			before := sample(r)
			// Even a held gate lock cannot turn the query into a wait. The exact
			// source guard below separately excludes every I/O/callback path.
			q.state.operationMu.Lock()
			for i := 0; i < 3; i++ {
				if err := alias.RecordedCleanupError(); err != nil {
					t.Error("incompleteness is not a recorded failure", err)
				}
			}
			q.state.operationMu.Unlock()
			if q.state.report != r || sample(r) != before || ctx.Err() != nil || alias.Done() != ctx.Done() {
				t.Fatal("query mutated report, cancellation or handle identity")
			}
			if !IsFailure(q.CleanupError(), FailureCleanup) {
				t.Fatal("full final query certified incomplete model")
			}
		})
	}
}

func TestExpiryC4RecordedCleanupErrorActualHelperSticky(t *testing.T) {
	r := new(expiryCleanupReport)
	q := &ExpiryQualification{state: &expiryGateState{report: r}}
	if q.RecordedCleanupError() != nil || q.CleanupError() != nil || r.first.Load() != nil {
		t.Fatal("fresh valid report acquired a failure or pending work")
	}
	f, err := os.CreateTemp(t.TempDir(), "actual-first-error")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	r.localOpened() // actual successful acquisition, not fabricated counters
	if q.RecordedCleanupError() != nil || !IsFailure(q.CleanupError(), FailureCleanup) {
		t.Fatal("owned file must remain incomplete without a recorded failure")
	}
	// Existing fixed helper's actual Close must fail on this already closed
	// file. This is not a forced fault at every OS site or a native witness.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	first := r.closeLocalFile(f)
	if first == nil || !errors.Is(q.RecordedCleanupError(), first) {
		t.Fatal("actual fixed-helper first error was not observed", first)
	}
	sticky := r.first.Load()
	later, err := os.CreateTemp(t.TempDir(), "actual-later-success")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = later.Close() })
	r.localOpened()
	if err := r.closeLocalFile(later); err != nil {
		t.Fatal("later real first Close did not succeed", err)
	}
	for i := 0; i < 3; i++ {
		if !errors.Is(q.RecordedCleanupError(), first) || !errors.Is(q.CleanupError(), first) || r.first.Load() != sticky {
			t.Fatal("later real nil Close or query repaired sticky failure")
		}
	}
	c := &r.counts[expiryLifecycleMetadata]
	if c.required.Load() != 2 || c.recorded.Load() != 2 || c.completed.Load() != 2 {
		t.Fatal("query changed actual close accounting")
	}
}

func TestExpiryC4RecordedCleanupErrorExactSource(t *testing.T) {
	source, err := os.ReadFile("expiry_qualification_launch.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(source), "//go:build spec_kitty_expiry_host_qualification && linux && amd64\n") {
		t.Fatal("query escaped exact E selection")
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, "expiry_qualification_launch.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Whole declarations, not substring bans: the query can only validate the
	// fixed report then call its existing scalar observer; full cleanup stays
	// separate. Bind firstError too, excluding hidden I/O/waits/mutation there.
	expected := map[string]string{
		"RecordedCleanupError": `func (q *ExpiryQualification) RecordedCleanupError() error {
			if q == nil || q.state == nil || q.state.report == nil {
				return failure(FailureCleanup, "expiry_cleanup", "fixed cleanup report is absent", nil)
			}
			return q.state.report.firstError()
		}`,
		"CleanupError": `func (q *ExpiryQualification) CleanupError() error {
			if q == nil || q.state == nil || q.state.report == nil {
				return failure(FailureCleanup, "expiry_cleanup", "fixed cleanup report is absent", nil)
			}
			return q.state.report.cleanupError()
		}`,
		"firstError": `func (r *expiryCleanupReport) firstError() error {
			if r == nil { return nil }
			if first := r.first.Load(); first != nil {
				return failure(FailureCleanup, "expiry_cleanup", fmt.Sprintf("first close failed in fixed category %d", first.category), first.err)
			}
			return nil
		}`,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		want, exists := expected[fn.Name.Name]
		if !exists {
			continue
		}
		got := string(source[set.Position(fn.Pos()).Offset:set.Position(fn.End()).Offset])
		if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(want), " ") {
			t.Errorf("fixed whole declaration changed: %s", fn.Name.Name)
		}
		delete(expected, fn.Name.Name)
	}
	if len(expected) != 0 {
		t.Fatal("fixed declarations missing", expected)
	}
}

// Provider-free actual owned resources and existing private process seams only.
// No test executes a provider, signals self, constructs a Host, or measures cost.
func TestExpiryC3ActualLocalCloseSites(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(strconv.FormatBool(bad), func(t *testing.T) {
			r := new(expiryCleanupReport)
			f, err := os.CreateTemp(t.TempDir(), "local")
			if err != nil {
				t.Fatal(err)
			}
			r.localOpened()
			if bad {
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = r.closeLocalFile(f)
			if (err != nil) != bad {
				t.Fatal("actual file outcome", err)
			}
			fd, err := unix.Open(t.TempDir(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			r.localOpened()
			if bad {
				// Returned-error component control after ONE successful owned
				// raw close; never re-close a number the fixture no longer owns.
				injected := errors.New("C3 component returned raw-close error")
				var actual error
				err = r.closeLocalFDWith(fd, func(owned int) error {
					actual = unix.Close(owned)
					return errors.Join(actual, injected)
				})
				if actual != nil || !errors.Is(err, injected) {
					t.Fatal("returned-error control", actual, err)
				}
			} else {
				err = r.closeLocalFD(fd)
			}
			if (err != nil) != bad {
				t.Fatal("raw-close returned outcome, not forced syscall failure", err)
			}
			pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(pair[1])
			original := os.NewFile(uintptr(pair[0]), "conn-input")
			conn, err := net.FileConn(original)
			if err != nil {
				t.Fatal(err)
			}
			if err := original.Close(); err != nil {
				t.Fatal(err)
			}
			r.localOpened()
			if bad {
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
			}
			err = r.closeLocalConnection(conn)
			if (err != nil) != bad {
				t.Fatal("actual connection outcome", err)
			}
			c := &r.counts[expiryLifecycleMetadata]
			if c.required.Load() != 3 || c.recorded.Load() != 3 || c.completed.Load() != 3 {
				t.Fatal("actual close accounting")
			}
			if (r.cleanupError() != nil) != bad {
				t.Fatal(r.cleanupError())
			}
		})
	}
}

func TestExpiryC3SharedDefaultAcquisitionRoutes(t *testing.T) {
	r := new(expiryCleanupReport)
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := statFilesystemWithReport(root, r); err != nil {
		t.Fatal(err)
	}
	if err := validatePrivateRootWithReport(root, r); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(root, "data")
	if err := preparePrivateDirectoryWithReport(data, r); err != nil {
		t.Fatal(err)
	}
	if err := preparePrivateDirectoryWithReport(data, r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := processExecutableWithReport(os.Getpid(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := processExecutableIdentityWithReport(os.Getpid(), r); err != nil {
		t.Fatal(err)
	}
	if _, err := currentExecutableIdentityWithReport(r); err != nil {
		t.Fatal(err)
	}
	c := &r.counts[expiryLifecycleMetadata]
	// stat + private + absent(parent,new) + existing + full + identity + self.
	if c.required.Load() != 8 || c.recorded.Load() != 8 || c.completed.Load() != 8 || r.cleanupError() != nil {
		t.Fatal("shared actual routes", c.required.Load(), r.cleanupError())
	}
	// Nil wrappers preserve direct self/default identity policy, not processAPI.
	a, err := currentExecutableIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := currentExecutableIdentityWithReport(nil)
	if err != nil || a != b {
		t.Fatal("ordinary extraction", err)
	}
}

func TestExpiryC3RuntimeMetadataAndStrictConnectionRoutes(t *testing.T) {
	for _, branch := range []string{"absent", "foreign", "empty"} {
		t.Run(branch, func(t *testing.T) {
			r := new(expiryCleanupReport)
			parent := t.TempDir()
			runtime := filepath.Join(parent, "runtime")
			l := &Lifecycle{expiry: lifecycleExpiryState{report: r}, provenance: SidecarProvenance{RuntimeDirectory: runtime}}
			want := uint64(1)
			if branch != "absent" {
				if err := os.Mkdir(runtime, 0700); err != nil {
					t.Fatal(err)
				}
				info, err := os.Lstat(runtime)
				if err != nil {
					t.Fatal(err)
				}
				l.runtimeID, err = identityFromInfo(info)
				if err != nil {
					t.Fatal(err)
				}
				want = 2
				if branch == "foreign" {
					if err := os.WriteFile(filepath.Join(runtime, "foreign"), []byte("preserve"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := l.cleanupRuntime(true)
			if (err != nil) != (branch == "foreign") {
				t.Fatal("primary cleanup return changed", err)
			}
			c := &r.counts[expiryLifecycleMetadata]
			if c.required.Load() != want || c.recorded.Load() != want || c.completed.Load() != want || r.cleanupError() != nil {
				t.Fatal("early/final runtime metadata close", r.cleanupError())
			}
		})
	}
	l := ownedLifecycleFixture(t)
	r := new(expiryCleanupReport)
	var status gapdbapi.StatusResult
	if _, err := observeStrictUnaryWithReport(t.Context(), l.processes, l.ownedEndpoint, l.socketID, l.process, "status", &status, time.Second, r); err != nil {
		t.Fatal(err)
	}
	foreign := l.process
	foreign.PID++
	if _, err := observeStrictUnaryWithReport(t.Context(), l.processes, l.ownedEndpoint, l.socketID, foreign, "status", &status, time.Second, r); err == nil {
		t.Fatal("foreign strict peer accepted")
	}
	c := &r.counts[expiryLifecycleMetadata]
	if c.required.Load() != 2 || c.completed.Load() != 2 || r.cleanupError() != nil {
		t.Fatal("strict success/refusal actual connection close", r.cleanupError())
	}
}

func TestExpiryC3NormalizationFixedDefaultSitesAndSuppliedSeams(t *testing.T) {
	root := t.TempDir()
	config := LifecycleConfig{ExecutablePath: filepath.Join(root, "exe"), AdmissionRecordPath: filepath.Join(root, "record"), RuntimeRoot: root, DataDirectory: root, SupervisorRunID: "unit", ProductPolicySHA256: strings.Repeat("a", 64)}
	r := new(expiryCleanupReport)
	// The filesystem type may refuse in this environment; the actual default
	// stat close still has to reach this preexisting report before verification.
	_ = normalizeLifecycleConfigWithReport(&config, r)
	if r.counts[expiryLifecycleMetadata].required.Load() == 0 || r.firstError() != nil {
		t.Fatal("preverification topology route", r.firstError())
	}
	before := r.counts[expiryLifecycleMetadata].required.Load()
	if _, _, err := config.processes.executable(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if _, err := config.processes.executableIdentity(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if r.counts[expiryLifecycleMetadata].required.Load() != before+2 {
		t.Fatal("fixed default process captures lost report")
	}
	var fsCalls, exeCalls int
	config.filesystem = func(string) (filesystemIdentity, error) {
		fsCalls++
		return filesystemIdentity{Type: unix.EXT4_SUPER_MAGIC}, nil
	}
	config.processes.startTicks = func(int) (uint64, error) { return 1, nil }
	config.processes.executable = func(int) (fileIdentity, string, error) { exeCalls++; return fileIdentity{}, "supplied", nil }
	config.processes.executableIdentity = nil
	r2 := new(expiryCleanupReport)
	if err := normalizeLifecycleConfigWithReport(&config, r2); err != nil {
		t.Fatal(err)
	}
	_, digest, _ := config.processes.executable(os.Getpid())
	if fsCalls != 2 || exeCalls != 1 || digest != "supplied" {
		t.Fatal("supplied policy replaced")
	}
	if _, err := config.processes.executableIdentity(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if r2.counts[expiryLifecycleMetadata].required.Load() != 1 {
		t.Fatal("only old identity fallback may be captured")
	}
}

func TestExpiryC3SupervisorTemporaryTransferAndFirstError(t *testing.T) {
	x := ExecutedImageProvenance{Device: 3, Inode: 5, Size: 7, SHA256: strings.Repeat("a", 64)}
	id := fileIdentity{device: 3, inode: 5, size: 7}
	for _, branch := range []string{"secondBracket", "finalBracket", "installed", "fd0", "errorInteger", "fallbackInteger"} {
		t.Run(branch, func(t *testing.T) {
			r := new(expiryCleanupReport)
			api := systemProcessAPI
			var ticks, closes int
			api.startTicks = func(int) (uint64, error) {
				ticks++
				if branch == "secondBracket" && ticks == 2 {
					return 12, nil
				}
				return 11, nil
			}
			api.executable = func(int) (fileIdentity, string, error) { return id, x.SHA256, nil }
			api.executableIdentity = func(int) (fileIdentity, error) { return fileIdentity{}, errors.New("final bracket refuses") }
			api.pidfdOpen = func(int, int) (int, error) {
				if branch == "errorInteger" {
					return 0, unix.EPERM
				}
				if branch == "fallbackInteger" {
					return 0, unix.ENOSYS
				}
				if branch == "fd0" {
					return 0, nil
				}
				return 47, nil
			}
			first := errors.New("actual existing process close seam outcome")
			api.closeFD = func(fd int) error {
				closes++
				if fd != 47 && !(branch == "fd0" && fd == 0) {
					t.Error("wrong transferred fd", fd)
				}
				return first
			}
			l := &Lifecycle{pidfd: -1, processes: api, command: &exec.Cmd{Process: &os.Process{Pid: 123}}, expiry: lifecycleExpiryState{report: r}}
			if branch == "finalBracket" {
				if l.establishStartedProcess(x) == nil {
					t.Fatal("final bracket succeeded")
				}
			} else {
				_, fd, err := observeStartedProcessWithReport(api, 123, x, r)
				if branch == "secondBracket" || branch == "errorInteger" {
					if err == nil {
						t.Fatal("failed bracket succeeded")
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if branch == "installed" || branch == "fd0" {
					l.pidfd = fd
					l.closePidfd()
					l.closePidfd()
				}
			}
			c := &r.counts[expirySupervisor]
			if branch == "errorInteger" || branch == "fallbackInteger" {
				if closes != 0 || c.required.Load() != 0 {
					t.Fatal("error-associated integer became owned")
				}
				return
			}
			if closes != 1 || c.required.Load() != 1 || c.completed.Load() != 1 || !errors.Is(r.cleanupError(), first) {
				t.Fatal("first temporary/installed result lost", closes, r.cleanupError())
			}
		})
	}
}

func TestExpiryC3ActualLifetimeClaimBothCallersAndNoRepeatNil(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(strconv.FormatBool(bad), func(t *testing.T) {
			q, _, l := expiryLaunchUnitGate(t)
			life := l.retained.Load()
			// Fixture registration is before Store, just like successful DialOwned.
			c := l.expiry.claim.Load()
			if c == nil || c.lifetime != life || c.state.Load() != expiryLifetimeUnclaimed {
				t.Fatal("canonical registration")
			}
			// The error branch models returned-error propagation through the
			// SAME canonical claim, not an actual failed kernel close or both
			// production caller routes. No process-wide fd enumeration/closure.
			injected := errors.New("C3 component returned lifetime-close error")
			var actualCalls atomic.Uint32
			var actualClose error
			returnedClose := func(owned *retainedkernel.Lifetime) error {
				actualCalls.Add(1)
				actualClose = owned.Close()
				return errors.Join(actualClose, injected)
			}
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if bad {
						if !l.closeExpiryLifetimeWith(life, returnedClose) {
							t.Error("canonical component claim declined")
						}
					} else if i%2 == 0 {
						l.closeOwned()
					} else {
						if !l.closeExpiryLifetime(life) {
							t.Error("marked helper declined")
						}
					}
				}(i)
			}
			wg.Wait()
			select {
			case <-c.done:
			default:
				t.Fatal("actual result not completed")
			}
			counts := &q.state.report.counts[expiryLifetime]
			if c.state.Load() != expiryLifetimeCompleted || counts.required.Load() != 1 || counts.recorded.Load() != 1 || counts.completed.Load() != 1 {
				t.Fatal("multiple actual first closes")
			}
			if (q.state.report.firstError() != nil) != bad {
				t.Fatal("claim's FIRST returned outcome", q.state.report.firstError())
			}
			if bad && (actualCalls.Load() != 1 || actualClose != nil || !errors.Is(q.state.report.firstError(), injected)) {
				t.Fatal("one actual successful lifetime close plus returned error required", actualCalls.Load(), actualClose, q.state.report.firstError())
			}
			if life.Close() != nil {
				t.Fatal("successful underlying close repeat changed; injected error belongs to claim report")
			}
			outer := l.closeOwned()
			if bad && !errors.Is(outer, injected) || !bad && outer != nil {
				t.Fatal("outward closeOwned lost the canonical FIRST returned result", outer)
			}
			if (q.state.report.firstError() != nil) != bad {
				t.Fatal("repeat nil repaired first outcome")
			}
		})
	}
}

func TestExpiryC3GateStageVersusFullFinalAndRetiredNoOpen(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	if err := q.Close(); err != nil {
		t.Fatal("intentionally retained lifecycle is not early gate failure", err)
	}
	if q.CleanupError() == nil {
		t.Fatal("full final certified retained pidfds")
	}
	l.processes.startTicks = func(int) (uint64, error) { return 0, os.ErrNotExist }
	first := l.Stop(t.Context())
	if !IsFailure(first, FailureCleanup) || l.shutdownWait.observed || l.shutdownWait.reapProven {
		t.Fatal("synthetic self-pidfd fixture fabricated actual child reap", first)
	}
	if l.pidfd != -1 || q.CleanupError() == nil { t.Fatal("safe descriptor closure/FIRST refusal missing") }
	before := q.state.report.counts[expiryLifecycleMetadata].required.Load()
	if l.CleanupStale() == nil {
		t.Fatal("postretirement cleanup reopened")
	}
	if l.InspectOwnership().State != OwnershipIndeterminate {
		t.Fatal("postretirement inspect admitted")
	}
	if _, _, err := l.DialOwned(t.Context(), time.Second); err == nil {
		t.Fatal("postretirement dial admitted")
	}
	if _, err := l.ProductRestoreBindingV1(); err == nil {
		t.Fatal("postretirement restore admitted")
	}
	if _, err := l.BindOwnerStartupEpochV1("epoch-retired"); err == nil {
		t.Fatal("postretirement startup admitted")
	}
	if err := l.Stop(t.Context()); err != first {
		t.Fatal("completed repeat Stop changed FIRST refusal", err)
	}
	if l.stopOwnedUnclean(t.Context()) == nil {
		t.Fatal("completed unclean repeat admitted")
	}
	if q.state.report.counts[expiryLifecycleMetadata].required.Load() != before || q.CleanupError() == nil {
		t.Fatal("retired route performed new metadata work")
	}
}

func TestExpiryC3StopClaimsBeforeMutexAndDrainsPublicInspection(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	l.processes.startTicks = func(int) (uint64, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return 0, os.ErrNotExist
	}
	inspected := make(chan OwnershipDiagnostic, 1)
	go func() { inspected <- l.InspectOwnership() }()
	<-entered // actual external entry is paused while holding l.mu
	stopped := make(chan error, 1)
	go func() { stopped <- l.Stop(t.Context()) }()
	select {
	case <-q.Done():
	case <-time.After(time.Second):
		close(release)
		t.Fatal("BEGIN revoke waited for mutex")
	}
	if _, err := l.beginExpiryWork(); err == nil {
		close(release)
		t.Fatal("atomic teardown claim did not bar admission before mutex")
	}
	select {
	case err := <-stopped:
		close(release)
		t.Fatal("closed under admitted work", err)
	default:
	}
	close(release)
	<-inspected
	select {
	case err := <-stopped:
		if !IsFailure(err, FailureCleanup) || l.shutdownWait.observed || l.shutdownWait.reapProven {
			t.Fatal("synthetic fixture must refuse missing actual Wait after draining", err)
		}
	case <-time.After(time.Second):
		t.Fatal("public work drain deadlocked")
	}
	if q.CleanupError() == nil { t.Fatal("missing actual reap certified cleanup") }
}

func TestExpiryC3RestoreAndStartupSameReportWork(t *testing.T) {
	l := startupProofLifecycle("c3-self-image")
	r := new(expiryCleanupReport)
	l.expiry = lifecycleExpiryState{report: r, work: newExpiryLifecycleWork()}
	r.lifecycleBound.Store(true)
	admission := testRestoreAdmission(t, l, "c3-project", "c3-mission")
	before := r.counts[expiryLifecycleMetadata].required.Load()
	if _, err := l.MarkProductRecovered(admission); err != nil {
		t.Fatal(err)
	}
	if r.counts[expiryLifecycleMetadata].required.Load() != before+1 || r.lifecyclePending.Load() != 0 {
		t.Fatal("nested binding did not share one complete work token")
	}
	if _, err := l.BindOwnerStartupEpochV1("c3-epoch"); err != nil {
		t.Fatal(err)
	}
	if r.counts[expiryLifecycleMetadata].required.Load() != before+2 || r.lifecyclePending.Load() != 0 {
		t.Fatal("startup self image lost report or work completion")
	}
	// Hold the EXISTING registry lock to pause the actual public binding after
	// self-image close, before registry/state publication. Stop must not finish.
	ownerStartupRegistryV1.Lock()
	bound := make(chan error, 1)
	go func() { _, err := l.BindOwnerStartupEpochV1("c3-epoch"); bound <- err }()
	deadline := time.After(time.Second)
	for r.counts[expiryLifecycleMetadata].completed.Load() != before+3 {
		select {
		case <-deadline:
			ownerStartupRegistryV1.Unlock()
			t.Fatal("binding did not reach registry tail")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	l.mu.Lock()
	l.stopped = true
	l.mu.Unlock() // never signal the synthetic daemon
	stopped := make(chan error, 1)
	go func() { stopped <- l.Stop(t.Context()) }()
	for !l.expiry.work.owner.Load() {
		select {
		case <-deadline:
			ownerStartupRegistryV1.Unlock()
			t.Fatal("Stop not reserved")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-stopped:
		ownerStartupRegistryV1.Unlock()
		t.Fatal("startup publication tail outlived teardown", err)
	default:
	}
	ownerStartupRegistryV1.Unlock()
	if err := <-bound; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; !IsFailure(err, FailureCleanup) {
		t.Fatal("synthetic stopped bit must not establish actual child reap", err)
	}
	if r.cleanupError() == nil { t.Fatal("synthetic reap certified") }
}

func TestExpiryC3BoundedDrainRetainsUnclaimedLifetimeAndSupervisor(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	token, err := l.beginExpiryWork()
	if err != nil {
		t.Fatal(err)
	}
	fd := l.pidfd
	// The fixed production budget is used unchanged; no configurable timeout
	// or cleanup callback seam is introduced for this residual correctness test.
	err = l.Stop(t.Context())
	if err == nil || l.pidfd != fd || l.expiry.claim.Load().state.Load() != expiryLifetimeUnclaimed {
		t.Fatal("timeout released resources under active work", err)
	}
	if q.CleanupError() == nil {
		t.Fatal("timeout certified success")
	}
	l.finishExpiryWork(token)
	if again := l.Stop(t.Context()); again != err {
		t.Fatal("completed repeated Stop must retain entire FIRST timeout", again)
	}
	if again := l.CleanupStale(); !errors.Is(again, err) {
		t.Fatal("late reconciliation lost FIRST timeout custody", again)
	}
	if l.pidfd != fd || l.expiry.claim.Load().state.Load() != expiryLifetimeUnclaimed || q.CleanupError() == nil {
		t.Fatal("repeat repaired or assisted residual teardown")
	}
}
