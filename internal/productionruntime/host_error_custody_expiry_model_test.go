//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Association MODEL only: no gate, lifecycle, adapter, authority, or startup.
// Construction fields are set synchronously before Close, as in the constructor;
// lease.mu alone does not publish these unguarded constructor writes.
func errorCustodyExpiryModel() (*expiryHostLease, *expiryLeaseControl, *expiryHostRun, *Host) {
	l := &expiryHostLease{phase: expiryLeaseFresh}
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
	o := &productionOwner{expiry: ownerExpiryState{run: r}}
	r.owner = o
	h := &Host{owner: o, state: HostReconciling}
	c := &expiryLeaseControl{lease: l, candidate: r, kind: expiryControlFresh, stage: expiryControlValidating}
	l.control = c
	return l, c, r, h
}

func TestHostErrorCustodyExpiryActualCloseModel(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, failing := range []bool{false, true} {
			t.Run(fmt.Sprintf("installed=%t/error=%t", installed, failing), func(t *testing.T) {
				l, _, r, h := errorCustodyExpiryModel()
				var ownerCause error
				if failing {
					ownerCause = errors.New("MODEL actual closer failure")
				}
				r.owner.closer = errorCustodyModelCloser{ownerCause}
				if installed {
					r.host, h.expiry.run = h, r
					l.control = nil // installed path must not need the fallback guard
				}
				first := h.Close(context.Background()) // actual existing Close body
				if !r.ownerCloseRecorded || !r.hostCloseRecorded || r.firstHostCloseErr != first || (first != nil) != failing {
					t.Fatal("actual first close result not retained", first)
				}
				if failing && (!errors.Is(r.firstOwnerCloseErr, ownerCause) || !l.failed) {
					t.Fatal("owner error/failed bit lost")
				}
				if !installed && (h.expiry.run != nil || r.host != nil || r.invalid.Load()) {
					t.Fatal("fallback installed provenance or changed entry revocation")
				}
				if installed && (!r.invalid.Load() || !l.failed) {
					t.Fatal("installed entry revocation changed")
				}
				if h.Close(context.Background()) != nil || r.firstHostCloseErr != first || !r.hostCloseRecorded {
					t.Fatal("actual repeat-nil Close changed the first result")
				}
			})
		}
	}
}

func TestHostErrorCustodyExpiryDirectRecorderModelFirstResult(t *testing.T) {
	// Direct recorder MODELs are intentionally separate from actual Close tests.
	firstError := errors.New("MODEL first Host result")
	laterError := errors.New("MODEL later result")
	for _, first := range []error{nil, firstError} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			l, _, r, h := errorCustodyExpiryModel()
			// A revoked/failed candidate is expected after owner cleanup; it
			// must still record. Health cannot be an association prerequisite.
			l.failed = true
			r.invalid.Store(true)
			h.expiryClosed(first)
			for _, repeat := range []error{nil, laterError, nil} {
				h.expiryClosed(repeat)
				if !r.hostCloseRecorded || r.firstHostCloseErr != first || !l.failed || h.expiry.run != nil || r.host != nil {
					t.Fatal("first result/custody changed by repeat")
				}
			}
		})
	}
	l, _, r, h := errorCustodyExpiryModel()
	r.host = h // already associated same Host is explicitly non-conflicting
	h.expiryClosed(nil)
	if !r.hostCloseRecorded || r.firstHostCloseErr != nil || l.failed {
		t.Fatal("same Host association did not record actual nil")
	}
	h.expiryClosed(laterError)
	h.expiryClosed(nil)
	if !l.failed || r.firstHostCloseErr != nil {
		t.Fatal("later nonnil must be sticky without replacing recorded nil")
	}
}

// Embedding does not make this the exact private productionOwner type.
type errorCustodyForeignOwner struct{ *productionOwner }

func TestHostErrorCustodyExpiryDirectRecorderModelRefusals(t *testing.T) {
	cases := map[string]func(*expiryHostLease, *expiryLeaseControl, *expiryHostRun, **Host){
		"nil_host":  func(_ *expiryHostLease, _ *expiryLeaseControl, _ *expiryHostRun, h **Host) { *h = nil },
		"nil_owner": func(_ *expiryHostLease, _ *expiryLeaseControl, _ *expiryHostRun, h **Host) { (*h).owner = nil },
		"typed_nil_owner": func(_ *expiryHostLease, _ *expiryLeaseControl, _ *expiryHostRun, h **Host) {
			(*h).owner = (*productionOwner)(nil)
		},
		"foreign_owner_type": func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, h **Host) {
			(*h).owner = errorCustodyForeignOwner{r.owner}
		},
		"nil_owner_run":  func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, _ **Host) { r.owner.expiry.run = nil },
		"nil_run_lease":  func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, _ **Host) { r.lease = nil },
		"nil_controller": func(l *expiryHostLease, _ *expiryLeaseControl, _ *expiryHostRun, _ **Host) { l.control = nil },
		"foreign_controller_lease": func(_ *expiryHostLease, c *expiryLeaseControl, _ *expiryHostRun, _ **Host) {
			c.lease = &expiryHostLease{}
		},
		"foreign_run_lease": func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, _ **Host) {
			r.lease = &expiryHostLease{}
		},
		"stale_predecessor": func(l *expiryHostLease, _ *expiryLeaseControl, _ *expiryHostRun, _ **Host) {
			l.current = &expiryHostRun{}
		},
		"nil_candidate": func(_ *expiryHostLease, c *expiryLeaseControl, _ *expiryHostRun, _ **Host) { c.candidate = nil },
		"foreign_candidate": func(l *expiryHostLease, c *expiryLeaseControl, _ *expiryHostRun, _ **Host) {
			c.candidate = &expiryHostRun{lease: l}
		},
		"nil_run_owner": func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, _ **Host) { r.owner = nil },
		"foreign_run_owner": func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, _ **Host) {
			r.owner = &productionOwner{}
		},
		"conflicting_host": func(_ *expiryHostLease, _ *expiryLeaseControl, r *expiryHostRun, _ **Host) { r.host = &Host{} },
	}
	for stage := expiryControlReserved; stage <= expiryControlTerminalResidual; stage++ {
		if stage != expiryControlValidating {
			cases[fmt.Sprintf("stage_%d", stage)] = func(_ *expiryHostLease, c *expiryLeaseControl, _ *expiryHostRun, _ **Host) { c.stage = stage }
		}
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			l, c, r, h := errorCustodyExpiryModel()
			corrupt(l, c, r, &h)
			h.expiryClosed(errors.New("MODEL refused result"))
			if r.hostCloseRecorded || r.firstHostCloseErr != nil || l.failed {
				t.Fatal("malformed association recorded a result or changed lease failure")
			}
		})
	}
}

func TestHostErrorCustodyExpiryRollbackRecordedUnpublishedModel(t *testing.T) {
	for _, installed := range []bool{false, true} {
		for _, recorded := range []bool{false, true} {
			for _, failing := range []bool{false, true} {
				t.Run(fmt.Sprintf("installed=%t/recorded=%t/error=%t", installed, recorded, failing), func(t *testing.T) {
					_, c, r, h := errorCustodyExpiryModel()
					c.stage = expiryControlRollback
					// Explicit bookkeeping MODEL, not an executed close/retirement.
					r.ownerCloseRecorded = true
					if installed {
						r.host = h
					}
					r.hostCloseRecorded = recorded
					first := errors.New("MODEL recorded Host failure")
					if failing && recorded {
						r.firstHostCloseErr = first
					}
					got := r.rollbackCleanupOutcomes()
					if (got != nil) != (installed && !recorded || failing && recorded) || errors.Is(got, first) != (failing && recorded) {
						t.Fatal("unpublished/recorded/installed distinction lost", got)
					}
				})
			}
		}
	}
}

func TestHostErrorCustodyExpiryActualCloseRollbackResidualModel(t *testing.T) {
	// Real root FDs + actual owner/Host Close and rollback; still NO authentic
	// Host/lifecycle/gate or native workload. No synthetic resource counters.
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.MkdirTemp(cache, "host-error-custody-MODEL-residual-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("preserved private MODEL residual parent: %s; no lifecycle/process", parent)
	l, err := reserveExpiryHostLease(context.Background(), Config{ControlRoot: filepath.Join(parent, "root")})
	if err != nil {
		t.Fatal(err)
	}
 // No automatic cancellation or descriptor disposal after unsafe metadata.
	// Do not dispose the residual through a fixture back door. After the
	// actual rollback below, the existing process-lifetime residual chain
	// retains these three CLOEXEC root FDs until this MODEL test process exits.
	// The private path also survives unexpected assertions; no RemoveAll or
	// later nil/fixture close may masquerade as product retirement. This is
	// still not authenticated Host/native or post-process cleanup evidence.
	r := &expiryHostRun{lease: l, window: newExpiryWorkWindow()}
	ownerCause := errors.New("MODEL actual owner cleanup failure")
	r.owner = &productionOwner{expiry: ownerExpiryState{run: r}, closer: errorCustodyModelCloser{ownerCause}}
	h := &Host{owner: r.owner, state: HostReconciling}
	c := l.control
	c.candidate, c.stage = r, expiryControlValidating // MODEL constructor association
	restoreErr := errorCustodyModelRestore(t)
	first := h.Close(context.Background())
	if first == nil || r.firstHostCloseErr != first || !r.hostCloseRecorded || r.host != nil || h.expiry.run != nil {
		t.Fatal("internal unpublished Host's actual Close result missing", first)
	}
	cause := &reconciliationRestoreFailure{
		primary:    Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation_restore"},
		restoreErr: restoreErr, closeErr: first,
	}
	if h.Close(context.Background()) != nil {
		t.Fatal("expected ordinary repeat nil")
	}
	got := l.rollbackControl(c, cause)
	for _, required := range []error{cause, restoreErr, first, ownerCause} {
		if !errors.Is(got, required) || !errors.Is(l.cleanup.outcome(false), required) {
			t.Fatal("C5 union/residual dropped an original identity", required, got)
		}
	}
	if l.control != c || c.candidate != r || c.firstErr != cause || c.stage != expiryControlTerminalResidual || !l.residualPinned {
		t.Fatal("canonical residual custody lost")
	}
	if !r.window.retired || !r.window.completed {
		t.Fatal("actual rollback failed to retire/drain candidate")
	}
	select {
	case <-r.window.done:
	default:
		t.Fatal("actual candidate retirement not published")
	}
	for _, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
		category := &l.cleanup.categories[kind]
		if category.acquired.Load() != 1 || category.attempted.Load() != 0 || category.completed.Load() != 0 {
			t.Fatal("residual released a root or fabricated close accounting")
		}
	}
	for _, file := range []*os.File{l.lock, l.directory, l.parentDirectory} {
		if _, _, err := expiryRootStat(file); err != nil {
			t.Fatal("root FD not retained", err)
		}
	}
	errorCustodyAssertTree(t, got)
	if repeated := l.rollbackControl(c, nil); !errors.Is(repeated, cause) || !errors.Is(repeated, first) {
		t.Fatal("terminal residual repeat lost first failure", repeated)
	}
}
