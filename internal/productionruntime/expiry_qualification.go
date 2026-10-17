//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	"github.com/spec-kitty/go-kitty3/internal/controlroot"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
	"golang.org/x/sys/unix"
)

// These are technical qualification coordinates, never executable admission.
const (
	expiryInputSHA         = "53fa2099c5798c6ac7d05ca782deba422412605fc9a9a9696271823652138b57"
	expiryTupleSHA         = "9d65f8bd363b4f416688a2038714e531e4bfe7a7a81976decb0c8eaae0f834e7"
	expiryInventorySHA     = "1f80dde08a349ccc16e3ffef825cabfe8a8c7c2db50444ee15c9598c68ecffbf"
	expiryExecutableSHA    = "83d5db39db928f62c9725253c53fdb8d1264f1b6e317c94db33aa11944f5905b"
	expiryRecoveryContract = "reader-v2-only/no-optional-accelerators"
	expiryBindingMaximum   = 1061 // 677 fixed bytes + three maximum 128-byte ASCII tokens
	expiryLeaseSuffix      = ".expiry-host-qualification.lease"
)

type authorityQualificationBinding struct {
	inputRecordSHA256, technicalTupleSHA256, modifiedInventorySHA256 string
	recoveryContract                                                 string
}

type expiryAuthorityBindingV1 struct {
	SchemaVersion                 uint32                    `json:"schema_version"`
	Kind                          string                    `json:"kind"`
	HostAuthorityID               journalv2.HostAuthorityID `json:"host_authority_id"`
	DatabaseID                    journalv2.DatabaseID      `json:"database_id"`
	CreatedOperationID            string                    `json:"created_operation_id"`
	InputRecordSHA256             string                    `json:"input_record_sha256"`
	TechnicalTupleSHA256          string                    `json:"technical_tuple_sha256"`
	ModifiedSourceInventorySHA256 string                    `json:"modified_source_inventory_sha256"`
	ExecutableSHA256              string                    `json:"executable_sha256"`
	WireProtocol                  uint64                    `json:"wire_protocol"`
	StorageProtocol               uint64                    `json:"storage_protocol"`
	RecoveryContract              string                    `json:"recovery_contract"`
	ProductionAdmission           bool                      `json:"production_admission"`
	Adopted                       bool                      `json:"adopted"`
	ReleaseReady                  bool                      `json:"release_ready"`
}

func expiryFailure(field string) error {
	return &Error{Kind: ErrorStartup, State: HostRecovering, Field: field}
}

func qualificationCanonical(b AuthorityBindingV2) ([]byte, bool, error) {
	q := b.qualification
	if q == (authorityQualificationBinding{}) {
		return nil, false, nil
	}
	refuse := func() ([]byte, bool, error) { return nil, true, expiryFailure("expiry_binding") }
	if q != (authorityQualificationBinding{expiryInputSHA, expiryTupleSHA, expiryInventorySHA, expiryRecoveryContract}) ||
		b.SchemaVersion != 1 || b.AdmittedTupleSHA != "" || b.SidecarSHA != expiryExecutableSHA || b.WireProtocol != 1 || b.StorageProtocol != 1 {
		return refuse()
	}
	if _, err := journalv2.NewHostAuthorityID(string(b.HostAuthorityID)); err != nil {
		return refuse()
	}
	if _, err := journalv2.NewOperationID(b.CreatedOperationID); err != nil {
		return refuse()
	}
	// Identity syntax ONLY. This is not a bootstrap or a credited record revision.
	if _, err := journalv2.NewStoreRevisionV2(b.DatabaseID, 1, 1); err != nil {
		return refuse()
	}
	raw, err := json.Marshal(expiryAuthorityBindingV1{
		SchemaVersion: 1, Kind: "expiry-host-qualification-authority-binding",
		HostAuthorityID: b.HostAuthorityID, DatabaseID: b.DatabaseID, CreatedOperationID: b.CreatedOperationID,
		InputRecordSHA256: q.inputRecordSHA256, TechnicalTupleSHA256: q.technicalTupleSHA256,
		ModifiedSourceInventorySHA256: q.modifiedInventorySHA256, ExecutableSHA256: b.SidecarSHA,
		WireProtocol: 1, StorageProtocol: 1, RecoveryContract: q.recoveryContract,
	})
	if err != nil {
		return nil, true, err
	}
	canonical, err := journalv2.CanonicalObject(raw)
	if err != nil || len(canonical) > expiryBindingMaximum {
		return refuse()
	}
	return canonical, true, nil
}

// Interface embedding deliberately hides BOTH optional recovery accelerators.
// This is selected independently of the ordinary M2 boolean, at all four edges.
type expiryRecoveryReaderV2 struct{ journalrestore.ReaderV2 }

var _ journalrestore.ReaderV2 = expiryRecoveryReaderV2{}

type ownerExpiryState struct{ run *expiryHostRun }
type hostExpiryState struct{ run *expiryHostRun }

const (
	expiryLeaseFresh uint8 = iota
	expiryLeaseRunning
	expiryLeaseRestarting
	expiryLeaseClosing
	expiryLeaseFailed
	expiryLeaseClosed
)

// No copy after initialization. Canonical identity is checked against lease.current.
// Contexts of later generations derive from lease.parent, NEVER this run's ctx.
type expiryHostRun struct {
	operationMu                                  sync.Mutex
	window                                       *expiryWorkWindow // immutable generation window
	invalid                                      atomic.Bool
	controlledTeardown                           atomic.Bool
	gateCloseOnce                                sync.Once
	gateCloseRecorded                            atomic.Bool
	firstGateCloseErr                            atomic.Pointer[expiryRootCloseError]
	ctx                                          context.Context
	cancel                                       context.CancelFunc
	host                                         *Host
	owner                                        *productionOwner
	composition                                  *retainedHostComposition
	gate                                         *sidecargapdb.ExpiryQualification
	identity                                     sidecargapdb.ExpiryTechnicalIdentity
	lease                                        *expiryHostLease
	firstOwnerCloseErr, firstHostCloseErr        error
	ownerCloseRecorded, hostCloseRecorded        bool // guarded by lease.mu, including nil
	negativeClosed, released, matchedAfterExpiry bool
	expiryRevision                               uint64
}

type expiryRootIdentity struct {
	device, inode  uint64
	mode, uid, gid uint32
}

// ONE authority-bearing lease (C1's expiryRootLease is this object). config and
// parent are set ONCE. Interface referents remain borrowed, not deep-immutable.
type expiryHostLease struct {
	mu                                              sync.Mutex
	config                                          Config
	parent                                          context.Context
	ctx                                             context.Context
	cancel                                          context.CancelFunc
	parentDirectory, directory, lock                *os.File
	parentIdentity, directoryIdentity, lockIdentity expiryRootIdentity
	controlRoot                                     string
	database                                        journalv2.DatabaseID
	hostAuthority                                   journalv2.HostAuthorityID
	binding                                         AuthorityBindingV2
	current                                         *expiryHostRun
	control                                         *expiryLeaseControl // independent of sticky failed/phase
	residualNext                                    *expiryHostLease    // once-linked, process-lifetime strong custody
	residualPinned                                  bool                // lease.mu; never reset
	phase                                           uint8
	failed                                          bool
	cleanup                                         expiryRootCleanup
	closeOnce                                       sync.Once
}

// Fixed scalar accounting, no report locks/callbacks/reentry. Every newly owned
// descriptor increments acquired; results are published only after actual Close.
// A successful snapshot requires all required closes completed, not repeat nil.
type expiryRootDescriptorKind uint8

const (
	expiryRootParent expiryRootDescriptorKind = iota
	expiryRootData
	expiryRootLock
	expiryRootMetadata
)

type expiryRootCloseCategory struct {
	acquired, attempted, completed atomic.Uint64
}
type expiryRootCleanup struct {
	categories [4]expiryRootCloseCategory
	first      atomic.Pointer[expiryRootCloseError]
}
type expiryRootCloseError struct{ err error }

func (r *expiryRootCleanup) increment(counter *atomic.Uint64) {
	for {
		old := counter.Load()
		if old == ^uint64(0) {
			r.record(expiryFailure("expiry_cleanup_overflow"))
			return
		}
		if counter.CompareAndSwap(old, old+1) {
			return
		}
	}
}
func (r *expiryRootCleanup) record(err error) {
	if err != nil {
		r.first.CompareAndSwap(nil, &expiryRootCloseError{err})
	}
}
func (r *expiryRootCleanup) acquiredFile(kind expiryRootDescriptorKind) {
	r.increment(&r.categories[kind].acquired)
}
func (r *expiryRootCleanup) closeFile(file *os.File, kind expiryRootDescriptorKind) {
	if file == nil {
		return
	}
	category := &r.categories[kind]
	r.increment(&category.attempted)
	err := file.Close()
	r.record(err)
	r.increment(&category.completed)
}
func (r *expiryRootCleanup) outcome(final bool) error {
	var result error
	if first := r.first.Load(); first != nil {
		result = first.err
	}
	for i := range r.categories {
		category := &r.categories[i]
		if category.attempted.Load() != category.completed.Load() || final && category.acquired.Load() != category.completed.Load() {
			result = errors.Join(result, expiryFailure("expiry_cleanup_incomplete"))
		}
	}
	return result
}

func expiryRootStat(file *os.File) (expiryRootIdentity, uint64, error) {
	var stat unix.Stat_t
	if file == nil {
		return expiryRootIdentity{}, 0, expiryFailure("expiry_root_descriptor")
	}
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return expiryRootIdentity{}, 0, err
	}
	return expiryRootIdentity{uint64(stat.Dev), stat.Ino, stat.Mode, stat.Uid, stat.Gid}, uint64(stat.Nlink), nil
}
func expiryPrivateIdentity(id expiryRootIdentity, mode uint32) bool {
	return id.mode == mode && id.uid == uint32(os.Geteuid()) && id.gid == uint32(os.Getegid())
}
func (l *expiryHostLease) ownFD(fd int, name string, kind expiryRootDescriptorKind) *os.File {
	file := os.NewFile(uintptr(fd), name)
	l.cleanup.acquiredFile(kind)
	return file
}
func (l *expiryHostLease) openDirectory(path string, kind expiryRootDescriptorKind) (*os.File, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, path, &unix.OpenHow{
		Flags:   uint64(unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC),
		Resolve: uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS),
	})
	if err != nil {
		return nil, err
	}
	return l.ownFD(fd, "expiry-root-directory", kind), nil
}

func reserveExpiryHostLease(parent context.Context, config Config) (_ *expiryHostLease, resultErr error) {
	ctx, cancel := context.WithCancel(parent)
	l := &expiryHostLease{config: config, parent: parent, ctx: ctx, cancel: cancel,
		controlRoot: config.ControlRoot, hostAuthority: config.HostAuthorityID, phase: expiryLeaseFresh}
	c, err := l.reserveControl(nil, expiryControlFresh, parent)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = l.rollbackControl(c, resultErr)
		}
	}()
	if err := l.moveControl(c, expiryControlAcquiringRoot, expiryControlReserved); err != nil {
		return nil, err
	}
	base := filepath.Base(config.ControlRoot)
	if len("."+base+expiryLeaseSuffix) > 255 {
		return nil, expiryFailure("expiry_lease_name")
	}
	l.parentDirectory, err = l.openDirectory(filepath.Dir(config.ControlRoot), expiryRootParent)
	if err != nil {
		return nil, expiryFailure("expiry_root_parent")
	}
	l.parentIdentity, _, err = expiryRootStat(l.parentDirectory)
	if err != nil || !expiryPrivateIdentity(l.parentIdentity, unix.S_IFDIR|0700) {
		return nil, expiryFailure("expiry_root_parent")
	}
	parentFD := int(l.parentDirectory.Fd())
	if err := unix.Mkdirat(parentFD, base, 0700); err != nil {
		return nil, expiryFailure("expiry_root_exclusive")
	}
	fd, err := unix.Openat(parentFD, base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, expiryFailure("expiry_root_open")
	}
	l.directory = l.ownFD(fd, "expiry-root", expiryRootData)
	l.directoryIdentity, _, err = expiryRootStat(l.directory)
	if err != nil || !expiryPrivateIdentity(l.directoryIdentity, unix.S_IFDIR|0700) {
		return nil, expiryFailure("expiry_root_mode")
	}
	// Read at most one entry: an existing or nonempty root is never made fresh.
	entries, readErr := l.directory.ReadDir(1)
	if len(entries) != 0 || readErr != io.EOF {
		return nil, expiryFailure("expiry_root_not_empty")
	}
	fd, err = unix.Openat(parentFD, "."+base+expiryLeaseSuffix, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, expiryFailure("expiry_lease_exclusive")
	}
	l.lock = l.ownFD(fd, "expiry-root-lease", expiryRootLock)
	var links uint64
	l.lockIdentity, links, err = expiryRootStat(l.lock)
	if err != nil || links != 1 || !expiryPrivateIdentity(l.lockIdentity, unix.S_IFREG|0600) {
		return nil, expiryFailure("expiry_lease_mode")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, expiryFailure("expiry_lease_lock")
	}
	if err := l.moveControl(c, expiryControlChecking, expiryControlAcquiringRoot); err != nil {
		return nil, err
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, err
	}
	return l, nil
}

// Only counted metadata routes call this body. Borrowed references are captured
// AFTER admission and retained through actual transient Close publication.
func (l *expiryHostLease) checkPathSnapshot(snapshot expiryPathSnapshot) error {
	if snapshot.parent == nil || snapshot.directory == nil || snapshot.lock == nil {
		return expiryFailure("expiry_root_lease")
	}
	for _, pair := range []struct {
		file     *os.File
		expected expiryRootIdentity
	}{
		{snapshot.parent, snapshot.parentIdentity}, {snapshot.directory, snapshot.directoryIdentity}, {snapshot.lock, snapshot.lockIdentity},
	} {
		id, links, err := expiryRootStat(pair.file)
		if err != nil || id != pair.expected || id.mode&unix.S_IFMT == unix.S_IFREG && links != 1 {
			return expiryFailure("expiry_root_identity")
		}
	}
	parent, err := l.openDirectory(filepath.Dir(snapshot.root), expiryRootMetadata)
	if err != nil {
		return expiryFailure("expiry_parent_path")
	}
	defer l.cleanup.closeFile(parent, expiryRootMetadata)
	id, _, err := expiryRootStat(parent)
	if err != nil || id != snapshot.parentIdentity {
		return expiryFailure("expiry_parent_path")
	}
	for _, entry := range []struct {
		name     string
		expected expiryRootIdentity
		regular  bool
	}{
		{filepath.Base(snapshot.root), snapshot.directoryIdentity, false},
		{"." + filepath.Base(snapshot.root) + expiryLeaseSuffix, snapshot.lockIdentity, true},
	} {
		var stat unix.Stat_t
		if err := unix.Fstatat(int(parent.Fd()), entry.name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return expiryFailure("expiry_root_path")
		}
		actual := expiryRootIdentity{uint64(stat.Dev), stat.Ino, stat.Mode, stat.Uid, stat.Gid}
		if actual != entry.expected || entry.regular && stat.Nlink != 1 {
			return expiryFailure("expiry_root_path")
		}
	}
	// Reapply nonblocking flock to the SAME still-held descriptor, never reopen it.
	if err := unix.Flock(int(snapshot.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return expiryFailure("expiry_lease_lock")
	}
	return l.cleanup.outcome(false)
}

func (l *expiryHostLease) closeDescriptors(c *expiryLeaseControl) error {
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlRollback, expiryControlValidating) || c.metadata != nil ||
		!l.quiescentLocked(c.predecessor) || !l.quiescentLocked(c.candidate) {
		l.failLocked()
		l.mu.Unlock()
		return expiryFailure("expiry_root_close_embargo")
	}
	c.stage = expiryControlRootClosing // bars ALL further metadata before FD release
	l.mu.Unlock()
	l.closeOnce.Do(func() {
		l.cancel()
		// Durable data and the unique lease pathname remain for inspection.
		l.cleanup.closeFile(l.lock, expiryRootLock)
		l.cleanup.closeFile(l.directory, expiryRootData)
		l.cleanup.closeFile(l.parentDirectory, expiryRootParent)
	})
	outcome := l.cleanup.outcome(true)
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlRootClosing) {
		l.failLocked()
		l.mu.Unlock()
		return expiryFailure("expiry_root_close_owner")
	}
	if outcome != nil {
		l.failLocked()
	}
	// Keep rootClosing until the controller's final reports/result are published.
	l.mu.Unlock()
	return outcome
}

func (r *expiryHostRun) invalidate() {
	if r == nil {
		return
	}
	r.invalid.Store(true)
	if r.cancel != nil {
		r.cancel()
	}
	if r.gate != nil {
		r.gateCloseOnce.Do(func() {
			if err := r.gate.Close(); err != nil {
				r.firstGateCloseErr.Store(&expiryRootCloseError{err})
			}
			r.gateCloseRecorded.Store(true)
		})
	}
}
func (r *expiryHostRun) fail(err error) error {
	if r == nil {
		return expiryFailure("expiry_generation")
	}
	// Failure is not cleanup authority: losers never join gate.Close.
	if l := r.lease; l != nil {
		l.mu.Lock()
		l.failLocked()
		l.mu.Unlock()
	}
	r.invalid.Store(true)
	if r.cancel != nil {
		r.cancel()
	}
	if err == nil {
		err = expiryFailure("expiry_generation")
	}
	return err
}
func (o *productionOwner) noteExpiryOwnerClose(err error) {
	if o == nil || o.expiry.run == nil {
		return
	}
	r := o.expiry.run
	l := r.lease
	l.mu.Lock()
	if !r.ownerCloseRecorded {
		r.ownerCloseRecorded, r.firstOwnerCloseErr = true, err
	}
	if err != nil {
		l.failed = true
	}
	l.mu.Unlock()
}
func (h *Host) revokeExpiryHost() {
	if h == nil || h.expiry.run == nil {
		return
	}
	r := h.expiry.run
	r.invalidate() // entry: before Host locks/drain/closeOnce
	l := r.lease
	l.mu.Lock()
	if !r.controlledTeardown.Load() {
		l.failLocked()
	}
	l.mu.Unlock()
}
func (h *Host) expiryClosed(err error) {
	if h == nil {
		return
	}
	r := h.expiry.run
	var constructingOwner *productionOwner
	if r == nil {
		var ok bool
		constructingOwner, ok = h.owner.(*productionOwner)
		if !ok || constructingOwner == nil {
			return
		}
		r = constructingOwner.expiry.run
		if r == nil || r.lease == nil {
			return
		}
	}
	l := r.lease
	l.mu.Lock()
	if constructingOwner != nil {
		// Resolve only the existing validating candidate association. Owner
		// cleanup may already have failed/revoked it. These association reads
		// rely on synchronous internal Host.Close and the constructor joining
		// before canonical rollback, NOT on lease.mu publishing constructor
		// writes to owner/run/host. Never install h.expiry.run here.
		c := l.control
		if !l.controlLocked(c, expiryControlValidating) || c.candidate != r || r.owner != constructingOwner || r.host != nil && r.host != h {
			l.mu.Unlock()
			return
		}
	}
	if !r.hostCloseRecorded {
		r.hostCloseRecorded, r.firstHostCloseErr = true, err
	}
	if err != nil {
		l.failed = true
	}
	l.mu.Unlock()
}

func startExpiryHostQualification(ctx context.Context, config Config) (_ *Host, _ *expiryHostRun, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	decision, err := preflightHostRoot(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	if err := validateHostDependencies(config); err != nil {
		return nil, nil, err
	}
	if decision.Action != controlroot.Initialize {
		return nil, nil, expiryFailure("expiry_root_not_fresh")
	}
	if _, err := os.Lstat(config.ControlRoot); !errors.Is(err, os.ErrNotExist) {
		return nil, nil, expiryFailure("expiry_root_exists")
	}
	lease, err := reserveExpiryHostLease(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if resultErr != nil {
			resultErr = lease.rollbackControl(lease.control, resultErr)
		}
	}()
	// A second classification verifies the newly exclusive empty root. This
	// is NEVER reasserted for a successor's already-existing database.
	decision, err = preflightHostRoot(ctx, config)
	if err != nil || decision.Action != controlroot.Initialize {
		return nil, nil, expiryFailure("expiry_root_reclassification")
	}
	return lease.startGeneration(lease.control, ctx, config.Sidecar.SupervisorRunID)
}

func (l *expiryHostLease) startGeneration(c *expiryLeaseControl, ctx context.Context, supervisor string) (_ *Host, _ *expiryHostRun, resultErr error) {
	if ctx == nil || ctx.Err() != nil || l.parent.Err() != nil {
		return nil, nil, expiryFailure("expiry_start_context")
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlChecking) || l.failed || c.kind == expiryControlFinal || c.candidate != nil {
		l.failLocked()
		l.mu.Unlock()
		return nil, nil, expiryFailure("expiry_generation_lease")
	}
	initial := c.kind == expiryControlFresh
	predecessorRun := c.predecessor
	config := l.config // original value and parent remain ONCE
	config.Sidecar.SupervisorRunID = supervisor
	generationContext, cancel := context.WithCancel(l.parent)
	r := &expiryHostRun{ctx: generationContext, cancel: cancel, lease: l, window: newExpiryWorkWindow()}
	c.candidate, c.stage = r, expiryControlConstructing // BEFORE candidate resources
	l.mu.Unlock()
	decision, err := preflightHostRoot(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	if initial && decision.Action != controlroot.Initialize || !initial && decision.Action != controlroot.Recover {
		return nil, nil, expiryFailure("expiry_generation_classification")
	}
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlConstructing) {
		l.failLocked()
		l.mu.Unlock()
		return nil, nil, expiryFailure("expiry_start_controller")
	}
	c.opaqueStartup = true // no returned owner/report exists if this constructor fails
	l.mu.Unlock()
	lifecycle, gate, err := sidecargapdb.StartExpiryQualification(generationContext, config.Sidecar)
	if err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	c.opaqueStartup = false
	l.mu.Unlock()
	r.gate, r.identity = gate, gate.Identity()
	r.owner = &productionOwner{expiry: ownerExpiryState{run: r}, lifecycle: lifecycle}
	if !expiryIdentityValid(r.identity) {
		return nil, nil, expiryFailure("expiry_identity")
	}
	p := lifecycle.Provenance()
	if expiryBootstrapRefused(p, l.controlRoot, supervisor, r.identity) {
		return nil, nil, expiryFailure("expiry_bootstrap")
	}
	if expiryBootstrapNotZero(initial, p) {
		return nil, nil, expiryFailure("expiry_bootstrap_not_zero")
	}
	adapter, closer, err := lifecycle.DialOwned(ctx, storegapdb.DefaultRequestTimeout)
	if err != nil {
		return nil, nil, err
	}
	database := journalv2.DatabaseID(p.DatabaseID)
	r.owner.adapter, r.owner.closer, r.owner.database = adapter, closer, database
	if initial {
		// Authenticated original owned Get; no NewStoreRevisionV2 at revision0.
		for _, key := range []string{authorityBindingKey, runtimeCurrentKey} {
			if _, err := adapter.Get(ctx, key); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				return nil, nil, expiryFailure("expiry_bootstrap_records")
			}
		}
	} else if database != l.database {
		return nil, nil, expiryFailure("expiry_restart_database")
	}
	binding := AuthorityBindingV2{qualification: authorityQualificationBinding{expiryInputSHA, expiryTupleSHA, expiryInventorySHA, expiryRecoveryContract},
		SchemaVersion: 1, HostAuthorityID: config.HostAuthorityID, DatabaseID: database, SidecarSHA: r.identity.ExecutableSHA256,
		WireProtocol: 1, StorageProtocol: 1, CreatedOperationID: config.AuthorityBindingOperationID}
	if !initial && binding != l.binding {
		return nil, nil, expiryFailure("expiry_restart_binding")
	}
	if err := ensureAuthorityBinding(ctx, adapter, binding, initial); err != nil {
		return nil, nil, err
	}
	r.owner.binding = binding
	if err := l.moveControl(c, expiryControlValidating, expiryControlConstructing); err != nil {
		return nil, nil, err
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	host, err := completeHostStartup(ctx, config, r.owner, expiryRecoveryReaderV2{ReaderV2: adapter})
	if err != nil {
		return nil, nil, err
	}
	r.host, r.composition = host, host.retained
	host.expiry = hostExpiryState{run: r}
	if _, ok := host.retainedCapture(r.composition); !ok {
		return nil, nil, expiryFailure("expiry_composition")
	}
	if !initial {
		if predecessorRun == nil || predecessorRun.composition == nil || predecessorRun.owner == nil {
			return nil, nil, expiryFailure("expiry_predecessor")
		}
		predecessor := predecessorRun.owner.lifecycle.Provenance()
		current, _, readErr := readCurrentEpoch(ctx, adapter)
		if expiryRestartFreshnessRefused(readErr, current, predecessorRun.composition.epoch, p, predecessor, r.identity, predecessorRun.identity) {
			return nil, nil, expiryFailure("expiry_restart_freshness")
		}
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	if predecessorRun != nil {
		if err := predecessorRun.cleanupOutcomes(); err != nil {
			return nil, nil, err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.controlLocked(c, expiryControlValidating) || c.candidate != r || c.metadata != nil || !l.quiescentLocked(predecessorRun) ||
		ctx.Err() != nil || generationContext.Err() != nil || l.parent.Err() != nil || l.ctx.Err() != nil || l.failed || !expiryOriginalDoneOpen(r) ||
		l.cleanup.outcome(false) != nil || r.window.retired {
		return nil, nil, expiryFailure("expiry_root_return")
	}
	// Separate same-gate scalar cutoff, NOT a joint lease/report snapshot or
	// proof that an in-flight/future close cannot fail. Keep the full final union.
	if err := r.gate.RecordedCleanupError(); err != nil {
		return nil, nil, err
	}
	if initial {
		l.database, l.binding = database, binding
	}
	l.current, l.phase, c.stage, l.control = r, expiryLeaseRunning, expiryControlHandedOff, nil
	return host, r, nil
}

// DRAFT PENDING DESIGN REVIEW: pure extraction of the original bootstrap
// refusal. Inputs are typed value copies, never authenticated provenance by
// construction. The caller retains lifecycle ownership and exact error policy.
func expiryBootstrapRefused(p sidecargapdb.SidecarProvenance, controlRoot, supervisor string, identity sidecargapdb.ExpiryTechnicalIdentity) bool {
	return p.DatabaseID == "" || p.DataDirectory != controlRoot || p.SupervisorRunID != supervisor || p.ExecutedImage.SHA256 != identity.ExecutableSHA256 ||
		p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady || p.Observation.Status.OwnerPID != p.Process.PID || p.Observation.Status.DatabaseID != p.DatabaseID
}

// initial comes from the original canonical controller kind, not caller input.
// Successors deliberately retain the original allowance for nonzero status.
func expiryBootstrapNotZero(initial bool, p sidecargapdb.SidecarProvenance) bool {
	return initial && (p.Observation.Status.CurrentRevision != 0 || p.Observation.Status.DurableThroughRevision != 0 || p.Observation.Status.RecordCount != 0)
}

// Pure private extraction of the existing post-readCurrentEpoch refusal only.
// Callers retain all predecessor/composition/bootstrap checks and error policy.
func expiryRestartFreshnessRefused(readErr error, current runtimeCurrentV2, predecessorEpoch journalv2.RuntimeEpochID,
	p, predecessor sidecargapdb.SidecarProvenance, identity, predecessorIdentity sidecargapdb.ExpiryTechnicalIdentity) bool {
	return readErr != nil || current.PredecessorEpochID != predecessorEpoch || current.RuntimeEpochID == predecessorEpoch ||
		p.DatabaseID != predecessor.DatabaseID || p.RuntimeDirectory == predecessor.RuntimeDirectory || p.ConfiguredSocket == predecessor.ConfiguredSocket ||
		p.Process == predecessor.Process || p.Process.PID == predecessor.Process.PID || p.Process.StartTicks == predecessor.Process.StartTicks || identity != predecessorIdentity
}

// Arguments are evaluated at the original restart callsite, in its original
// order. No cleanup is performed here and no error is translated or discarded.
func expiryRestartCleanupUnion(crashErr, hostErr, outcomesErr, contextErr error) error {
	return errors.Join(crashErr, hostErr, outcomesErr, contextErr)
}

func expiryIdentityValid(i sidecargapdb.ExpiryTechnicalIdentity) bool {
	return i.InputRecordSHA256 == expiryInputSHA && i.TechnicalTupleSHA256 == expiryTupleSHA && i.ModifiedInventorySHA256 == expiryInventorySHA &&
		i.ExecutableSHA256 == expiryExecutableSHA && i.ExecutableBytes == 5491681 && i.ClientModule == "github.com/spec-kitty/gapdb" &&
		i.ClientVersion == "v0.0.0-20260907114832-006e9d469d40" && i.ClientModuleH1 == "h1:vTBY6qolbiwbtAX/CFGAgNlmoQ9VpxGPAce+84BjSbM=" &&
		i.ClientGoModH1 == "h1:8xNZ3k+SQIqcxLkS8bjbooOAv9xzZcbg5arjKaV6fcQ="
}

func (r *expiryHostRun) cleanupOutcomes() error {
	l := r.lease
	l.mu.Lock()
	var result error
	if !r.ownerCloseRecorded || !r.hostCloseRecorded {
		result = expiryFailure("expiry_owner_cleanup_incomplete")
	}
	result = errors.Join(result, r.firstOwnerCloseErr, r.firstHostCloseErr, l.cleanup.outcome(false))
	l.mu.Unlock()
	if r.gate == nil || !r.gateCloseRecorded.Load() {
		return errors.Join(result, expiryFailure("expiry_gate_cleanup_incomplete"))
	}
	if first := r.firstGateCloseErr.Load(); first != nil {
		result = errors.Join(result, first.err)
	}
	return errors.Join(result, r.gate.CleanupError())
}

func (r *expiryHostRun) restartClean(ctx context.Context, supervisor string) (*Host, *expiryHostRun, error) {
	return r.restart(ctx, supervisor, false)
}
func (r *expiryHostRun) restartUnclean(ctx context.Context, supervisor string) (*Host, *expiryHostRun, error) {
	return r.restart(ctx, supervisor, true)
}
func (r *expiryHostRun) restart(ctx context.Context, supervisor string, unclean bool) (_ *Host, _ *expiryHostRun, resultErr error) {
	if r == nil || r.lease == nil {
		return nil, nil, expiryFailure("expiry_generation")
	}
	// Standalone preflight uses nested metadata admission and completes BEFORE
	// reservation. It cannot mistake an idle operationMu for completed work.
	if err := r.checkCurrent(ctx); err != nil {
		return nil, nil, r.fail(err)
	}
	if _, err := journalv2.NewOperationID(supervisor); err != nil || supervisor == r.owner.lifecycle.Provenance().SupervisorRunID ||
		r.owner.lifecycle.InspectOwnership().State != sidecargapdb.OwnershipOwnedLive {
		return nil, nil, r.fail(expiryFailure("expiry_supervisor"))
	}
	l := r.lease
	kind := expiryControlRestartClean
	if unclean {
		kind = expiryControlRestartUnclean
	}
	c, err := l.reserveControl(r, kind, ctx)
	if err != nil {
		return nil, nil, r.fail(err)
	} // LOSER: no teardown
	defer func() {
		if resultErr != nil {
			resultErr = l.rollbackControl(c, r.fail(resultErr))
		}
	}()
	cleanup, cancel := expiryCleanupContext()
	defer cancel()
	r.invalidate() // all named locks released BEFORE joining gate.Close
	if err := l.drainControl(cleanup, c); err != nil {
		return nil, nil, err
	}
	if err := l.moveControl(c, expiryControlChecking, expiryControlDrained); err != nil {
		return nil, nil, err
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	predecessor := r.owner.lifecycle.Provenance()
	var crashErr error
	if unclean {
		// Original owned crash BEFORE Host.Close; never ordinary RestartOwned.
		crashErr = r.owner.lifecycle.CrashExpiryQualification(cleanup)
	}
	hostErr := r.host.Close(cleanup)
	if err := expiryRestartCleanupUnion(crashErr, hostErr, r.cleanupOutcomes(), cleanup.Err()); err != nil {
		return nil, nil, l.residual(c, err)
	}
	for _, path := range []string{predecessor.ConfiguredSocket, predecessor.RuntimeDirectory} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil, nil, expiryFailure("expiry_predecessor_residual")
		}
	}
	if ctx.Err() != nil || l.parent.Err() != nil {
		return nil, nil, expiryFailure("expiry_restart_context")
	}
	return l.startGeneration(c, ctx, supervisor)
}

func (r *expiryHostRun) close(ctx context.Context) error {
	if r == nil || r.lease == nil {
		return expiryFailure("expiry_generation")
	}
	l := r.lease
	c, err := l.reserveControl(r, expiryControlFinal, ctx)
	if err != nil {
		return r.fail(err)
	} // LOSER: no teardown or assistance
	cleanup, cancel := expiryCleanupContext()
	defer cancel()
	r.invalidate()
	if err := l.drainControl(cleanup, c); err != nil {
		return err
	}
	if err := l.moveControl(c, expiryControlChecking, expiryControlDrained); err != nil {
		return err
	}
	pathErr := l.checkPathsForControl(c)
	var hostErr error
	if r.host != nil {
		hostErr = r.host.Close(cleanup)
	} else {
		hostErr = expiryFailure("expiry_composition")
	}
	outcome := errors.Join(pathErr, hostErr, r.cleanupOutcomes(), cleanup.Err())
	if outcome != nil {
		return l.residual(c, outcome)
	}
	if err := l.moveControl(c, expiryControlValidating, expiryControlChecking); err != nil {
		return err
	}
	outcome = errors.Join(l.checkPathsForControl(c), r.cleanupOutcomes(), cleanup.Err())
	if outcome != nil {
		return l.residual(c, outcome)
	}
	outcome = l.closeDescriptors(c)
	// Required named close-set, not merely empty aggregate equality.
	for _, kind := range []expiryRootDescriptorKind{expiryRootParent, expiryRootData, expiryRootLock} {
		category := &l.cleanup.categories[kind]
		if category.acquired.Load() != 1 || category.attempted.Load() != 1 || category.completed.Load() != 1 {
			outcome = errors.Join(outcome, expiryFailure("expiry_required_root_closes"))
		}
	}
	// Fresh final reports after all report-producing work is barred/quiescent.
	outcome = errors.Join(outcome, r.cleanupOutcomes(), l.cleanup.outcome(true), cleanup.Err())
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlRootClosing) || !c.scenario || l.failed || ctx == nil || ctx.Err() != nil || l.parent.Err() != nil {
		outcome = errors.Join(outcome, expiryFailure("expiry_scenario_incomplete"))
	}
	if outcome != nil {
		l.failLocked()
		if c.firstErr == nil {
			c.firstErr = outcome
		}
	}
	if l.controlLocked(c, expiryControlRootClosing) {
		c.stage, l.phase = expiryControlTerminalClosed, expiryLeaseClosed
	}
	l.mu.Unlock()
	return outcome
}
