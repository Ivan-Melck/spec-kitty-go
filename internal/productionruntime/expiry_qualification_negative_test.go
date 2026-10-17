//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Provider-free custody/negative controls. These are NOT genuine Host evidence.
func TestExpiryHostLeaseExclusiveCustodyAndPreservedArtifacts(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	config := Config{ControlRoot: filepath.Join(parent, "authority")}
	lease, err := reserveExpiryHostLease(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.rollbackControl(lease.control, expiryFailure("test_disposal")) }()
	config.ControlRoot = filepath.Join(parent, "not-the-stored-root")
	if lease.config.ControlRoot != filepath.Join(parent, "authority") || lease.parent != t.Context() {
		t.Fatal("original scalar config/parent copy changed")
	}
	config.ControlRoot = lease.config.ControlRoot
	if err := lease.checkPathsForControl(lease.control); err != nil {
		t.Fatal(err)
	}
	leasePath := filepath.Join(parent, ".authority"+expiryLeaseSuffix)
	other, err := os.OpenFile(leasePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(other.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		t.Fatal("lease not held exclusively")
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveExpiryHostLease(t.Context(), config); err == nil {
		t.Fatal("existing empty root accepted as fresh")
	}
	c := lease.control
	if err := lease.moveControl(c, expiryControlRollback, expiryControlChecking); err != nil {
		t.Fatal(err)
	}
	if err := lease.closeDescriptors(c); err != nil {
		t.Fatal(err)
	}
	before := lease.cleanup.categories[expiryRootLock].completed.Load()
	if err := lease.closeDescriptors(c); err == nil {
		t.Fatal("repeat controller close unexpectedly eligible")
	}
	if lease.cleanup.categories[expiryRootLock].completed.Load() != before {
		t.Fatal("repeat closed owned lease again")
	}
	for _, path := range []string{config.ControlRoot, leasePath} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("durable inspection artifact deleted", err)
		}
	}
}

func TestExpiryHostLeaseRefusesUnsafeOrExistingRoots(t *testing.T) {
	for _, name := range []string{"existing_empty", "existing_nonempty", "symlink_parent", "public_parent", "long_name", "lease_exists"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "authority")
			switch name {
			case "existing_empty", "existing_nonempty":
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if name == "existing_nonempty" {
					if err := os.WriteFile(filepath.Join(root, "foreign"), []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink_parent":
				link := filepath.Join(parent, "linked")
				if err := os.Symlink(parent, link); err != nil {
					t.Fatal(err)
				}
				root = filepath.Join(link, "authority")
			case "public_parent":
				if err := os.Chmod(parent, 0755); err != nil {
					t.Fatal(err)
				}
			case "long_name":
				root = filepath.Join(parent, strings.Repeat("a", 255-len(expiryLeaseSuffix)))
			case "lease_exists":
				if err := os.WriteFile(filepath.Join(parent, ".authority"+expiryLeaseSuffix), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := reserveExpiryHostLease(t.Context(), Config{ControlRoot: root}); err == nil {
				t.Fatal("unsafe/existing reservation accepted")
			}
		})
	}
}

func TestExpiryHostLeaseDetectsDirectoryAndLockSubstitution(t *testing.T) {
	for _, name := range []string{"directory", "lock", "mode", "hardlink"} {
		t.Run(name, func(t *testing.T) {
			parent := h06ConstructorDirectory(t) // no automatic removal of unsafe metadata residual
			if err := os.Chmod(parent, 0700); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "authority")
			lease, err := reserveExpiryHostLease(t.Context(), Config{ControlRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = lease.rollbackControl(lease.control, expiryFailure("test_disposal")) }()
			lock := filepath.Join(parent, ".authority"+expiryLeaseSuffix)
			switch name {
			case "directory":
				if err := os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			case "lock":
				if err := os.Rename(lock, lock+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(lock, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(lock, lock+"-alias"); err != nil {
					t.Fatal(err)
				}
			}
			if err := lease.checkPathsForControl(lease.control); err == nil {
				t.Fatal("substitution accepted")
			}
		})
	}
}

func TestExpiryHostRootCleanupFirstOutcomeAndRequiredCompletion(t *testing.T) {
	var report expiryRootCleanup
	file, err := os.CreateTemp(t.TempDir(), "owned-")
	if err != nil {
		t.Fatal(err)
	}
	report.acquiredFile(expiryRootMetadata)
	if err := file.Close(); err != nil {
		t.Fatal(err)
	} // real externally broken descriptor, no close callback seam
	report.closeFile(file, expiryRootMetadata)
	first := report.outcome(true)
	if first == nil {
		t.Fatal("actual first Close failure lost")
	}
	later, err := os.CreateTemp(t.TempDir(), "later-")
	if err != nil {
		t.Fatal(err)
	}
	report.acquiredFile(expiryRootMetadata)
	report.closeFile(later, expiryRootMetadata)
	if report.outcome(true) == nil {
		t.Fatal("later nil repaired first error")
	}
	// Explicit first nil cannot hide a DIFFERENT later failed resource in the
	// same category. Exercise real File.Close results, not a fault callback.
	var nilThenError expiryRootCleanup
	good, err := os.CreateTemp(t.TempDir(), "good-")
	if err != nil {
		t.Fatal(err)
	}
	nilThenError.acquiredFile(expiryRootMetadata)
	nilThenError.closeFile(good, expiryRootMetadata)
	if nilThenError.outcome(true) != nil {
		t.Fatal("first nil not recorded")
	}
	broken, err := os.CreateTemp(t.TempDir(), "broken-")
	if err != nil {
		t.Fatal(err)
	}
	nilThenError.acquiredFile(expiryRootMetadata)
	if err := broken.Close(); err != nil {
		t.Fatal(err)
	}
	nilThenError.closeFile(broken, expiryRootMetadata)
	if nilThenError.outcome(true) == nil {
		t.Fatal("first nil hid later same-category failure")
	}
	var pending expiryRootCleanup
	pending.acquiredFile(expiryRootLock)
	pending.increment(&pending.categories[expiryRootLock].attempted)
	if pending.outcome(true) == nil || pending.outcome(false) == nil {
		t.Fatal("in-flight Close treated as complete")
	}
	pending.increment(&pending.categories[expiryRootLock].completed)
	if pending.outcome(true) != nil {
		t.Fatal("explicit first nil completion not recognized")
	}
	// A missing owned close is distinct from an empty category's zero equality.
	pending.acquiredFile(expiryRootParent)
	if pending.outcome(true) == nil {
		t.Fatal("unattempted owned descriptor accepted")
	}
}

func TestExpiryHostFirstOwnerAndHostResultsCannotBeRepaired(t *testing.T) {
	lease := &expiryHostLease{}
	run := &expiryHostRun{lease: lease}
	owner := &productionOwner{expiry: ownerExpiryState{run: run}}
	host := &Host{expiry: hostExpiryState{run: run}}
	first := errors.New("first owned cleanup")
	owner.noteExpiryOwnerClose(first)
	owner.noteExpiryOwnerClose(nil)
	host.expiryClosed(first)
	host.expiryClosed(nil)
	if !run.ownerCloseRecorded || !run.hostCloseRecorded || run.firstOwnerCloseErr != first || run.firstHostCloseErr != first || !lease.failed {
		t.Fatal("first outcome repaired")
	}
	// This is report plumbing only, NOT a constructed Host qualification.
}

func TestExpiryHostEmptyCopiedAndCanceledHandlesRefuseWithoutProvider(t *testing.T) {
	for _, run := range []*expiryHostRun{nil, {}, {lease: &expiryHostLease{}}} {
		if err := run.observeS1Refusal(context.Background(), nil); err == nil {
			t.Fatal("empty run earned classification")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&expiryHostRun{}).checkCurrent(ctx); err == nil {
		t.Fatal("canceled empty run accepted")
	}
}
