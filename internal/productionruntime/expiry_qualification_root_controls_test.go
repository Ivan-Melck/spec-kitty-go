//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

// Kernel/component entry controls, NOT genuine-Host or Native evidence. Only
// newly created paths are touched. Strong custody deliberately survives Fatal:
// restoration failure must not trigger deferred cancellation/descriptor teardown.
var expiryRootControlCustody struct {
	sync.Mutex
	roots []*expiryRootControlFixture
}

type expiryRootControlFixture struct {
	base, parent, root, lock string
	lease *expiryHostLease
	contendingLeases []*expiryHostLease // retain BOTH results before any receipt can fail
	contender *os.File
	firstRecorded bool
}

func expiryNewRootControl(t *testing.T) *expiryRootControlFixture {
	t.Helper()
	base, err := os.MkdirTemp("", "expiry-root-controls-")
	if err != nil { t.Fatal(err) }
	f := &expiryRootControlFixture{base: base, parent: filepath.Join(base, "parent")}
	f.root = filepath.Join(f.parent, "authority")
	f.lock = filepath.Join(f.parent, ".authority"+expiryLeaseSuffix)
	expiryRootControlCustody.Lock()
	expiryRootControlCustody.roots = append(expiryRootControlCustody.roots, f)
	expiryRootControlCustody.Unlock()
	t.Logf("NEW preserved root control: %s; manual retirement only; no cleanup registered", base)
	if err := os.Chmod(base, 0700); err != nil { t.Fatal(err) }
	if err := os.Mkdir(f.parent, 0700); err != nil { t.Fatal(err) }
	return f
}

// O_EXCL FIRST records: Write, Sync and Close are each actually invoked and
// their separate results checked. An absent outcome never means attempted nil.
func (f *expiryRootControlFixture) receipt(t *testing.T, name, value string) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(f.base, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil { t.Fatal("preserve receipt before any teardown:", err) }
	n, writeErr := file.WriteString(value+"\n")
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || n != len(value)+1 || syncErr != nil || closeErr != nil {
		t.Fatalf("receipt FIRST write=%d/%d err=%v fsync=%v close=%v; STOP, resources retained", n, len(value)+1, writeErr, syncErr, closeErr)
	}
}

func (f *expiryRootControlFixture) reserve(t *testing.T) {
	t.Helper()
	var err error
	// Not t.Context: failed restoration must not cause test-end cancellation.
	f.lease, err = reserveExpiryHostLease(context.Background(), Config{ControlRoot: f.root})
	if err != nil { t.Fatal(err) }
	f.receipt(t, "RESERVED", "parent/data/lease held by original lease; no Host/provider constructed")
}

func expiryRootControlField(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" {
		if err != nil { t.Fatal("unexpected refusal:", err) }
		return
	}
	var got *Error
	if !errors.As(err, &got) || got.Kind != ErrorStartup || got.State != HostRecovering || got.Field != want {
		t.Fatalf("FIRST error=%#v; want recovering startup/%s", err, want)
	}
}

func (f *expiryRootControlFixture) firstSnapshot(t *testing.T, snapshot expiryPathSnapshot, want string) {
	t.Helper()
	if f.firstRecorded { t.Fatal("FIRST snapshot already consumed") }
	err := f.lease.checkPathSnapshot(snapshot) // ACTUAL first read, not a dry run
	f.firstRecorded = true
	f.receipt(t, "FIRST-snapshot", fmt.Sprintf("error=%#v expected=%s", err, want))
	expiryRootControlField(t, err, want)
}

func (f *expiryRootControlFixture) restoredAndClose(t *testing.T) {
	t.Helper()
	// Explicitly ordered AFTER all restoration/quarantine operations. If any
	// restoration or this check fails, no rollback, cancel or Close follows.
	if err := f.lease.checkPathSnapshot(f.lease.snapshotPathsLocked()); err != nil {
		f.receipt(t, "RESTORATION-FAILED", fmt.Sprintf("%#v", err))
		t.Fatal("STOP before teardown/cancel; retained original descriptors:", err)
	}
	f.receipt(t, "RESTORED", "original descriptor/path identities restored; handing custody to original rollback controller")
	cause := errors.New("root-control-explicit-retirement")
	result := f.lease.rollbackControl(f.lease.control, cause)
	f.receipt(t, "FIRST-retirement", fmt.Sprintf("%#v", result))
	if !errors.Is(result, cause) { t.Fatal("retirement lost original cause", result) }
	// errors.Join(cause,nil) is not literally cause. Check the actual reports
	// and terminal controller, rather than inferring closes from the error.
	if err := f.lease.cleanup.outcome(true); err != nil { t.Fatal(err) }
	for _, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
		c := &f.lease.cleanup.categories[kind]
		if c.acquired.Load() != 1 || c.attempted.Load() != 1 || c.completed.Load() != 1 {
			t.Fatalf("required close set kind=%d acquired=%d attempted=%d completed=%d", kind, c.acquired.Load(), c.attempted.Load(), c.completed.Load())
		}
	}
	if f.lease.control.stage != expiryControlTerminalClosed { t.Fatal("original controller did not close") }
	for _, path := range []string{f.root, f.lock} {
		if _, err := os.Lstat(path); err != nil { t.Fatal("inspection artifact lost", err) }
	}
}

func expiryRootControlMust(t *testing.T, err error) {
	t.Helper()
	if err != nil { t.Fatal("STOP before teardown/cancel; newly owned artifacts retained:", err) }
}

func TestExpiryRootControlsActualPathSnapshot(t *testing.T) {
	cases := []struct{ name, field string }{
		{"missing_lease_descriptor", "expiry_root_lease"},
		{"missing_lease_path", "expiry_root_path"},
		{"missing_flock_reacquires_without_contender", ""},
		{"missing_flock_competing_holder", "expiry_lease_lock"},
		{"lease_mode", "expiry_root_identity"},
		{"data_mode", "expiry_root_identity"},
		{"parent_mode", "expiry_root_identity"},
		{"lease_hardlink", "expiry_root_identity"},
		{"parent_inode", "expiry_parent_path"},
		{"parent_symlink", "expiry_parent_path"},
		{"data_inode", "expiry_root_path"},
		{"data_symlink", "expiry_root_path"},
		{"lease_inode", "expiry_root_path"},
		{"lease_symlink", "expiry_root_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := expiryNewRootControl(t)
			f.reserve(t)
			snapshot := f.lease.snapshotPathsLocked() // exclusive fixture, no metadata workers
			var restore func()
			switch tc.name {
			case "missing_lease_descriptor":
				snapshot.lock = nil // original owned lock remains held, not stolen
				restore = func() {}
			case "missing_lease_path":
				expiryRootControlMust(t, os.Rename(f.lock, f.lock+".original"))
				restore = func() { expiryRootControlMust(t, os.Rename(f.lock+".original", f.lock)) }
			case "missing_flock_reacquires_without_contender", "missing_flock_competing_holder":
				expiryRootControlMust(t, unix.Flock(int(f.lease.lock.Fd()), unix.LOCK_UN))
				if tc.name == "missing_flock_competing_holder" {
					var err error
					f.contender, err = os.OpenFile(f.lock, os.O_RDWR|unix.O_NOFOLLOW, 0)
					expiryRootControlMust(t, err)
					expiryRootControlMust(t, unix.Flock(int(f.contender.Fd()), unix.LOCK_EX|unix.LOCK_NB))
					f.receipt(t, "CONTENDER-held", "independent open-file-description owns flock before FIRST snapshot")
				}
				restore = func() {
					if f.contender != nil {
						expiryRootControlMust(t, unix.Flock(int(f.contender.Fd()), unix.LOCK_UN))
						closeErr := f.contender.Close()
						f.receipt(t, "FIRST-contender-close", fmt.Sprintf("%#v", closeErr))
						expiryRootControlMust(t, closeErr)
					}
					expiryRootControlMust(t, unix.Flock(int(f.lease.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB))
				}
			case "lease_mode", "data_mode", "parent_mode":
				path, mode := f.lock, os.FileMode(0600)
				if tc.name == "data_mode" { path, mode = f.root, 0700 }
				if tc.name == "parent_mode" { path, mode = f.parent, 0700 }
				expiryRootControlMust(t, os.Chmod(path, mode|0040))
				restore = func() { expiryRootControlMust(t, os.Chmod(path, mode)) }
			case "lease_hardlink":
				alias := filepath.Join(f.base, "new-owned-lease-alias")
				expiryRootControlMust(t, os.Link(f.lock, alias))
				restore = func() {
					// Only this exact freshly created link is unlinked, never data
					// or unknown artifacts. nlink=1 is necessary before retirement.
					expiryRootControlMust(t, os.Remove(alias))
				}
			default:
				path, directory := f.lock, false
				if strings.HasPrefix(tc.name, "parent_") { path, directory = f.parent, true }
				if strings.HasPrefix(tc.name, "data_") { path, directory = f.root, true }
				original, quarantine := path+".original", path+".replacement-quarantine"
				expiryRootControlMust(t, os.Rename(path, original))
				if strings.HasSuffix(tc.name, "_symlink") {
					expiryRootControlMust(t, os.Symlink(original, path))
				} else if directory {
					expiryRootControlMust(t, os.Mkdir(path, 0700))
				} else {
					expiryRootControlMust(t, os.WriteFile(path, []byte("new-owned-replacement"), 0600))
				}
				restore = func() {
					// Preserve replacement bytes/link, then restore the ORIGINAL.
					expiryRootControlMust(t, os.Rename(path, quarantine))
					expiryRootControlMust(t, os.Rename(original, path))
				}
			}
			f.firstSnapshot(t, snapshot, tc.field)
			restore() // no defer: any failed restoration is fail-stop
			f.restoredAndClose(t)
		})
	}
}

func TestExpiryRootControlsActualReservationRefusals(t *testing.T) {
	for _, name := range []string{"existing_empty", "existing_nonempty", "lease_EEXIST_failed_root_retry", "long_lease_name", "parent_symlink", "parent_public_mode"} {
		t.Run(name, func(t *testing.T) {
			f := expiryNewRootControl(t)
			root, field := f.root, "expiry_root_exclusive"
			sentinel := []byte("new-control-owned-preserve-bytes")
			var preserved string
			switch name {
			case "existing_empty", "existing_nonempty":
				expiryRootControlMust(t, os.Mkdir(root, 0700))
				if name == "existing_nonempty" {
					preserved = filepath.Join(root, "owned-sentinel")
					expiryRootControlMust(t, os.WriteFile(preserved, sentinel, 0600))
				}
			case "lease_EEXIST_failed_root_retry":
				preserved, field = f.lock, "expiry_lease_exclusive"
				expiryRootControlMust(t, os.WriteFile(preserved, sentinel, 0600))
			case "long_lease_name":
				root, field = filepath.Join(f.parent, strings.Repeat("a", 255-len(expiryLeaseSuffix))), "expiry_lease_name"
			case "parent_symlink":
				link := filepath.Join(f.base, "parent-link")
				expiryRootControlMust(t, os.Symlink(f.parent, link))
				root, field = filepath.Join(link, "authority"), "expiry_root_parent"
			case "parent_public_mode":
				expiryRootControlMust(t, os.Chmod(f.parent, 0750))
				field = "expiry_root_parent"
			}
			lease, first := reserveExpiryHostLease(context.Background(), Config{ControlRoot: root})
			f.lease = lease
			f.receipt(t, "FIRST-reservation", fmt.Sprintf("lease=%p error=%#v", lease, first))
			if lease != nil { t.Fatal("refused entry returned authority; retained without teardown") }
			expiryRootControlField(t, first, field)
			if name == "lease_EEXIST_failed_root_retry" {
				retry, retryErr := reserveExpiryHostLease(context.Background(), Config{ControlRoot: root})
				f.receipt(t, "FIRST-failed-root-retry", fmt.Sprintf("lease=%p error=%#v", retry, retryErr))
				if retry != nil { t.Fatal("failed root minted a new lease") }
				expiryRootControlField(t, retryErr, "expiry_root_exclusive")
			}
			if preserved != "" {
				got, err := os.ReadFile(preserved) // only this NEW control's file
				expiryRootControlMust(t, err)
				if string(got) != string(sentinel) { t.Fatal("refusal overwrote owned sentinel") }
			}
		})
	}
}

func TestExpiryRootControlsConcurrentEEXIST(t *testing.T) {
	f := expiryNewRootControl(t)
	type result struct { lease *expiryHostLease; err error }
	start, results := make(chan struct{}), make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: f.root})
			results <- result{l, err}
		}()
	}
	close(start)
	first, second := <-results, <-results // join BOTH before handoff/teardown
	f.contendingLeases = []*expiryHostLease{first.lease, second.lease}
	f.receipt(t, "FIRST-contenders", fmt.Sprintf("first=%p/%#v second=%p/%#v", first.lease, first.err, second.lease, second.err))
	if first.lease == nil { first, second = second, first }
	f.lease = first.lease
	if first.lease == nil || first.err != nil || second.lease != nil {
		t.Fatal("expected exactly one exclusive root owner; no teardown on ambiguous custody")
	}
	expiryRootControlField(t, second.err, "expiry_root_exclusive")
	f.firstSnapshot(t, f.lease.snapshotPathsLocked(), "")
	f.restoredAndClose(t)
}

func TestExpiryRootControlsOriginalOwnershipPredicate(t *testing.T) {
	// Direct component predicate, not forged provider authentication or chown.
	for _, tc := range []struct { name string; change func(*expiryRootIdentity) }{
		{"wrong_uid", func(id *expiryRootIdentity) { id.uid ^= 1 }},
		{"wrong_gid", func(id *expiryRootIdentity) { id.gid ^= 1 }},
		{"wrong_mode", func(id *expiryRootIdentity) { id.mode = unix.S_IFDIR|0750 }},
		{"wrong_type", func(id *expiryRootIdentity) { id.mode = unix.S_IFREG|0700 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := expiryRootIdentity{mode: unix.S_IFDIR|0700, uid: uint32(os.Geteuid()), gid: uint32(os.Getegid())}
			if !expiryPrivateIdentity(id, unix.S_IFDIR|0700) { t.Fatal("original owner predicate refused current ownership") }
			tc.change(&id)
			if expiryPrivateIdentity(id, unix.S_IFDIR|0700) { t.Fatal("ownership predicate widened") }
		})
	}
}
