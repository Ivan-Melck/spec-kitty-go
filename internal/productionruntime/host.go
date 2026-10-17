// Package productionruntime owns the single local Spec Kitty control host.
// It composes authority capabilities but does not own their domain policy.
package productionruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/projection"
)

// HostState is deliberately about availability, not workflow ceremony. A
// ready host may run every capability whose own authority predicates pass.
type HostState string

const (
	HostRecovering  HostState = "recovering"
	HostReconciling HostState = "reconciling"
	HostReady       HostState = "ready"
	HostDegraded    HostState = "degraded"
	HostClosing     HostState = "closing"
	HostClosed      HostState = "closed"
)

type ErrorKind string

const (
	ErrorInvalidConfiguration ErrorKind = "INVALID_CONFIGURATION"
	ErrorNotReady             ErrorKind = "HOST_NOT_READY"
	ErrorStaleEpoch           ErrorKind = "STALE_RUNTIME_EPOCH"
	ErrorOwnerConflict        ErrorKind = "OWNER_CONFLICT"
	ErrorStartup              ErrorKind = "STARTUP_REFUSED"
	ErrorProjection           ErrorKind = "PROJECTION_REFUSED"
	ErrorShutdown             ErrorKind = "SHUTDOWN_INCOMPLETE"
)

// Error carries bounded machine meaning and intentionally excludes provider
// transcripts, absolute paths, environment values, and secrets.
type Error struct {
	Kind  ErrorKind `json:"kind"`
	State HostState `json:"state,omitempty"`
	Field string    `json:"field,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("production runtime: kind=%s state=%s field=%s", e.Kind, e.State, e.Field)
}

func IsKind(err error, kind ErrorKind) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == kind
}

type ownerLifecycle interface {
	DatabaseID() journalv2.DatabaseID
	Probe(context.Context) error
	Close(context.Context) error
}

type operationOriginReader interface {
	OperationOrigin(context.Context, journalv2.OperationID) (journalv2.RuntimeEpochID, bool, error)
}

// CommandContext is the copy-only authority capsule supplied after the common
// host barrier. Capability services must still evaluate their own predicates.
type CommandContext struct {
	RuntimeEpochID     journalv2.RuntimeEpochID
	ProjectionRevision uint64
	ProjectionCursor   journalv2.Digest
}

type ProjectionCursor struct {
	Revision uint64
	Token    string
}

// HostStatus is a copy-only observation of the local owner barrier. It is not
// a workflow phase and confers no authority.
type HostStatus struct {
	SchemaVersion      uint32                   `json:"schema_version"`
	State              HostState                `json:"state"`
	DatabaseID         journalv2.DatabaseID     `json:"database_id"`
	RuntimeEpochID     journalv2.RuntimeEpochID `json:"runtime_epoch_id"`
	ProjectionRevision uint64                   `json:"projection_revision"`
	ProjectionSHA256   string                   `json:"projection_sha256,omitempty"`
	Diagnosis          string                   `json:"diagnosis,omitempty"`
}

// Host is the only normal owner of the local authority composition. It never
// returns its sidecar, durable adapter, projection mutator, or repositories.
type Host struct {
	expiry       hostExpiryState
	mu           sync.Mutex
	commandMu    sync.RWMutex
	refreshMu    sync.Mutex
	state        HostState
	epoch        journalv2.RuntimeEpochID
	databaseID   journalv2.DatabaseID
	diagnosis    string
	inflight     int
	owner        ownerLifecycle
	projection   *projection.AuthorityV2
	progress     *ProgressService
	capabilities runtimeCapabilities
	refresh      func(context.Context) error
	monitorStop  context.CancelFunc
	monitorDone  <-chan struct{} // actual monitor's final completion publication
	ownerKey     string
	closeOnce    sync.Once
	retained     *retainedHostComposition // set only by successful production start
}

var liveOwners = struct {
	sync.Mutex
	byDatabase map[string]*Host
}{byDatabase: make(map[string]*Host)}

func newHost(owner ownerLifecycle, epoch journalv2.RuntimeEpochID, authority *projection.AuthorityV2, progress *ProgressService) (*Host, error) {
	if owner == nil || authority == nil || progress == nil || owner.DatabaseID() == "" || epoch == "" {
		return nil, &Error{Kind: ErrorInvalidConfiguration, State: HostRecovering, Field: "composition"}
	}
	host := &Host{state: HostRecovering, epoch: epoch, databaseID: owner.DatabaseID(), owner: owner, projection: authority, progress: progress, ownerKey: string(owner.DatabaseID())}
	if err := host.claimProcessOwner(); err != nil {
		return nil, err
	}
	return host, nil
}

func (h *Host) claimProcessOwner() error {
	liveOwners.Lock()
	defer liveOwners.Unlock()
	if current := liveOwners.byDatabase[h.ownerKey]; current != nil && current != h {
		return &Error{Kind: ErrorOwnerConflict, State: HostRecovering, Field: "database_owner"}
	}
	liveOwners.byDatabase[h.ownerKey] = h
	return nil
}

func (h *Host) releaseProcessOwner() {
	liveOwners.Lock()
	if liveOwners.byDatabase[h.ownerKey] == h {
		delete(liveOwners.byDatabase, h.ownerKey)
	}
	liveOwners.Unlock()
}

func (h *Host) transition(next HostState) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !validHostTransition(h.state, next) {
		return &Error{Kind: ErrorStartup, State: h.state, Field: "lifecycle_transition"}
	}
	h.state = next
	return nil
}

func validHostTransition(from, to HostState) bool {
	switch from {
	case HostRecovering:
		return to == HostReconciling || to == HostDegraded || to == HostClosing
	case HostReconciling:
		return to == HostReady || to == HostDegraded || to == HostClosing
	case HostReady:
		return to == HostDegraded || to == HostClosing
	case HostDegraded:
		return to == HostClosing
	case HostClosing:
		return to == HostClosed
	default:
		return false
	}
}

func (h *Host) State() HostState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *Host) RuntimeEpochID() journalv2.RuntimeEpochID {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.epoch
}

func (h *Host) DatabaseID() journalv2.DatabaseID { return h.databaseID }

// OperationOrigin returns the immutable epoch attribution already bound to an
// operation. An absent operation is deliberately distinct from a failed read:
// command adapters use the current epoch only for the first durable attempt,
// and preserve the original attribution on every replay after restart.
func (h *Host) OperationOrigin(ctx context.Context, operationID journalv2.OperationID) (journalv2.RuntimeEpochID, bool, error) {
	reader, ok := h.owner.(operationOriginReader)
	if !ok {
		return "", false, &Error{Kind: ErrorInvalidConfiguration, State: h.State(), Field: "operation_origin"}
	}
	return reader.OperationOrigin(ctx, operationID)
}

func (h *Host) Status() HostStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	status := HostStatus{SchemaVersion: 1, State: h.state, DatabaseID: h.databaseID, RuntimeEpochID: h.epoch, Diagnosis: h.diagnosis}
	if snapshot := h.projection.Snapshot(); snapshot != nil {
		status.ProjectionRevision = snapshot.Revision()
		status.ProjectionSHA256 = snapshot.ProjectionDigest().String()
	}
	return status
}

// LiveProjectionCursor is the constant-size acknowledged-prefix identity used
// by runtime instrumentation and cursors. It is deliberately distinct from
// Status.ProjectionSHA256, which remains the canonical flat projection digest.
func (h *Host) LiveProjectionCursor() ProjectionCursor {
	attribution := h.projection.Attribution()
	return ProjectionCursor{Revision: attribution.Revision, Token: attribution.CursorToken.String()}
}

func (h *Host) beginCommand(expectedEpoch journalv2.RuntimeEpochID) (func(), error) {
	// Independent commands share this read lock. The health monitor takes the
	// write side so a transient provider state inside an admitted atomic command
	// cannot be mistaken for owner loss.
	h.commandMu.RLock()
	h.mu.Lock()
	if h.state != HostReady {
		state := h.state
		h.mu.Unlock()
		h.commandMu.RUnlock()
		return nil, &Error{Kind: ErrorNotReady, State: state, Field: "command_barrier"}
	}
	if expectedEpoch != h.epoch {
		h.mu.Unlock()
		h.commandMu.RUnlock()
		return nil, &Error{Kind: ErrorStaleEpoch, State: HostReady, Field: "runtime_epoch_id"}
	}
	attribution := h.projection.Attribution()
	if attribution.RuntimeEpochID != h.epoch || attribution.DatabaseID != h.databaseID || attribution.Revision == 0 || attribution.DurableThrough < attribution.Revision {
		h.mu.Unlock()
		h.commandMu.RUnlock()
		h.degrade("projection_binding")
		return nil, &Error{Kind: ErrorProjection, State: HostDegraded, Field: "projection_binding"}
	}
	h.inflight++
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		h.inflight--
		h.mu.Unlock()
		h.commandMu.RUnlock()
	}, nil
}

// executeBarrierProbe exists only for authority-law tests. Production callers
// use the typed capability methods in capabilities.go.
func (h *Host) executeBarrierProbe(expectedEpoch journalv2.RuntimeEpochID, command func(CommandContext) error) error {
	if command == nil {
		return &Error{Kind: ErrorInvalidConfiguration, State: h.State(), Field: "command"}
	}
	release, err := h.beginCommand(expectedEpoch)
	if err != nil {
		return err
	}
	defer release()
	h.mu.Lock()
	attribution := h.projection.Attribution()
	capsule := CommandContext{RuntimeEpochID: h.epoch, ProjectionRevision: attribution.Revision, ProjectionCursor: attribution.CursorToken}
	h.mu.Unlock()
	return command(capsule)
}

func (h *Host) refreshProjection(ctx context.Context) error {
	h.refreshMu.Lock()
	defer h.refreshMu.Unlock()
	if h.refresh == nil {
		h.degrade("projection_refresh")
		return &Error{Kind: ErrorProjection, State: HostDegraded, Field: "projection_refresh"}
	}
	var refreshErr error
	for attempt := 0; attempt < 32; attempt++ {
		refreshErr = h.refresh(ctx)
		if refreshErr == nil || ctx.Err() != nil {
			break
		}
		// A concurrent independent publication can invalidate a restore's
		// stable-prefix proof after the command itself has committed. Retry
		// from durable truth without sleeping or replaying the command.
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if refreshErr != nil {
		h.degrade("projection_refresh")
		return &Error{Kind: ErrorProjection, State: HostDegraded, Field: "projection_refresh"}
	}
	h.mu.Lock()
	if h.state != HostReady {
		state := h.state
		h.mu.Unlock()
		return &Error{Kind: ErrorNotReady, State: state, Field: "projection_refresh"}
	}
	h.mu.Unlock()
	return nil
}

func (h *Host) degrade(field string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == HostRecovering || h.state == HostReconciling || h.state == HostReady {
		h.state = HostDegraded
		h.diagnosis = boundedDiagnostic(field)
	}
}

func (h *Host) startMonitor(parent context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	probeTimeout := interval
	if probeTimeout < time.Second {
		probeTimeout = time.Second
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	h.mu.Lock()
	if h.monitorDone != nil || h.state == HostClosing || h.state == HostClosed {
		h.mu.Unlock()
		cancel()
		return
	}
	h.monitorStop, h.monitorDone = cancel, done
	h.mu.Unlock()
	go func() {
		defer close(done) // after probe/unlock and ticker Stop; no work after publication
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		consecutiveFailures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.commandMu.Lock()
				if ctx.Err() != nil {
					h.commandMu.Unlock()
					return
				}
				// Waiting for an admitted command to leave the provider boundary is
				// not part of the owner probe. Starting the deadline before this
				// lock would turn a slow but healthy command into an already-expired
				// probe and could manufacture consecutive owner-loss evidence.
				probe, stop := context.WithTimeout(ctx, probeTimeout)
				err := h.owner.Probe(probe)
				h.commandMu.Unlock()
				stop()
				if err != nil {
					consecutiveFailures++
					if consecutiveFailures >= 3 {
						h.degrade("owner_health")
						return
					}
					continue
				}
				consecutiveFailures = 0
			}
		}
	}()
}

// Progress exposes only the query authority. The returned service contains no
// durable mutation or epoch-minting capability.
func (h *Host) Progress() *ProgressService {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.progress
}

// New wait refusals retain the actual caller context error without changing
// the public Error vocabulary or exposing context/provider prose.
// Channel/wrapper storage and joining work remain OWNED_UNQUALIFIED.
type hostShutdownWaitError struct { primary *Error; cause error }
func (e *hostShutdownWaitError) Error() string { return e.primary.Error() }
func (e *hostShutdownWaitError) Unwrap() []error { return []error{e.primary, e.cause} }
func hostShutdownWaitRefusal(field string, cause error) error {
	return &hostShutdownWaitError{primary: &Error{Kind: ErrorShutdown, State: HostClosing, Field: field}, cause: cause}
}

// Close shuts admission, drains admitted commands, and joins the actual monitor
// before owner teardown. Cancellation may end either wait, but never authorizes
// teardown of unjoined work. Such an attempt remains incomplete/HostClosing;
// the process-owner reservation is retained, and a repeat nil is not completion.
// Durable data is never deleted. No new deadline or cleanup budget is created.
func (h *Host) Close(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.revokeExpiryHost() // before locks, drain, or the idempotent cleanup gate
	var result error
	h.closeOnce.Do(func() {
		defer func() { h.expiryClosed(result) }() // record FIRST, including unjoined work
		h.mu.Lock()
		if h.state != HostClosed && h.state != HostClosing {
			h.state = HostClosing
		}
		if h.monitorStop != nil {
			h.monitorStop()
		}
		for h.inflight > 0 && ctx.Err() == nil {
			h.mu.Unlock()
			select {
			case <-time.After(time.Millisecond):
			case <-ctx.Done():
			}
			h.mu.Lock()
		}
		drained, monitorDone := h.inflight == 0, h.monitorDone
		h.mu.Unlock()
		if !drained {
			result = hostShutdownWaitRefusal("command_drain", ctx.Err())
			return
		}
		if monitorDone != nil {
			select {
			case <-monitorDone:
			default:
				select {
				case <-monitorDone:
				case <-ctx.Done():
					// Prefer an actual completion receive if both are ready.
					select {
					case <-monitorDone:
					default:
						result = hostShutdownWaitRefusal("monitor_join", ctx.Err())
						return
					}
				}
			}
		}
		if err := h.owner.Close(ctx); err != nil {
			result = &Error{Kind: ErrorShutdown, State: HostClosing, Field: "owner_cleanup"}
		}
		h.mu.Lock()
		h.state = HostClosed
		h.mu.Unlock()
		h.releaseProcessOwner()
	})
	return result
}

func boundedDiagnostic(value string) string {
	const maximum = 128
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
