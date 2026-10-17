package gapdb

import (
	"context"
	"io"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
)

// OriginalProcessIdentity reports executed-image identity, not configured-source
// inode. Provenance stays frozen; this method does not grant lifetime authority.
func (l *Lifecycle) OriginalProcessIdentity() ProcessIdentity {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.process
}

// DialOwned is the production factory. No caller can supply provenance, anchor,
// endpoint, or bootstrap database. Stop BEGIN irreversibly disables this factory
// even when shutdown fails or is cancelled; the ordinary startup fallback stays
// available but cannot construct an attributed adapter without pidfd support.
func (l *Lifecycle) DialOwned(ctx context.Context, timeout time.Duration) (adapter *storegapdb.Adapter, closer io.Closer, resultErr error) {
	work, err := l.beginExpiryWork()
	if err != nil {
		return nil, nil, err
	}
	defer l.finishExpiryWork(work)
	releaseRevoked := false
	defer func() {
		// Registered before mu.Unlock: close/join only AFTER releasing l.mu.
		if releaseRevoked { resultErr = withStartupCustody(resultErr, l.closeOwned()) }
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return nil, nil, failure(FailureInvalidConfiguration, "dial_owned", "positive owned factory timeout required", nil)
	}
	// Bound initial lifetime/hash acquisition as well as the transport's RPC.
	// An earlier caller deadline wins; kernel EINTR also has a finite attempt cap.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, failure(FailureIdentityUnprovable, "dial_owned", "owned factory context ended", err)
	}
	if l.ownedRevoked.Load() || l.stopped || l.pidfd < 0 || l.ownedEndpoint == "" ||
		l.ownedEndpoint != l.provenance.ConnectedSocket || l.ownedEndpoint != l.provenance.ConfiguredSocket ||
		!l.provenance.Readiness.Admitted || l.ownedBootstrap.Lifecycle != gapdbapi.LifecycleReady ||
		l.ownedBootstrap.DatabaseID == "" || l.ownedBootstrap.OwnerPID != l.process.PID {
		return nil, nil, failure(FailureIdentityUnprovable, "dial_owned", "original direct ready lifetime unavailable", nil)
	}
	if l.retained.Load() == nil {
		p := l.process // private, originally observed EXECUTED image (possibly memfd)
		anchor := retainedkernel.Anchor{PID: p.PID, StartTicks: p.StartTicks, ImageDevice: p.Device, ImageInode: p.Inode, ImageSize: p.Size, ImageSHA256: p.SHA256,
			SocketPath: l.ownedEndpoint, SocketDevice: l.socketID.device, SocketInode: l.socketID.inode,
			DirectoryPath: l.provenance.RuntimeDirectory, DirectoryDevice: l.runtimeID.device, DirectoryInode: l.runtimeID.inode}
		lifetime, err := retainedkernel.New(ctx, anchor)
		if err != nil {
			return nil, nil, failure(FailureIdentityUnprovable, "dial_owned", "retained identity unavailable", err)
		}
		l.registerExpiryLifetime(lifetime)
		l.retained.Store(lifetime)
		if l.ownedRevoked.Load() {
			releaseRevoked = true
			return nil, nil, failure(FailureIdentityUnprovable, "dial_owned", "lifetime revoked during acquisition", nil)
		}
	}
	s := l.ownedBootstrap // never reconstructed from caller-provided provenance
	adapter, closer, err = storegapdb.DialOwned(ctx, l.ownedEndpoint, timeout, l.retained.Load(), s.DatabaseID, s.OwnerPID, s.OwnerStartedAt)
	if l.ownedRevoked.Load() {
		return nil, nil, revokedDialOwnedResult(err, closer)
	}
	return adapter, closer, err
}

// Called at shutdown entry, even before l.mu: a factory may be hashing or in an
// RPC while holding that mutex. Atomic publication plus its postcheck closes the
// acquisition race. Descriptor release belongs to lifecycle cleanup.
// closeOwned releases only observation's descriptor; the supervisor descriptor
// remains available for safely validated inspection/cleanup after failed Stop.
// Called after releasing l.mu so descriptor cleanup never adds a lock inversion.
func (l *Lifecycle) closeOwned() error {
	ctx, cancel := context.WithDeadline(context.Background(), l.originalNonEscalatingCleanupCutoff(context.Background()))
	defer cancel()
	return l.closeOwnedBefore(ctx)
}
func (l *Lifecycle) closeOwnedBefore(ctx context.Context) error {
	if retained := l.retained.Load(); retained != nil {
		if !l.closeExpiryLifetime(retained) { return retained.Close() }
		return l.expiryLifetimeCloseResultBefore(ctx, retained)
	}
	return nil
}

// Fixed result-only seam, no dial/factory selector and no retained closer.
func revokedDialOwnedResult(constructionErr error, closer io.Closer) error {
	var closeErr error
	if closer != nil { closeErr = closer.Close() }
	primary := failure(FailureIdentityUnprovable, "dial_owned", "lifetime revoked during construction", nil)
	return withStartupCustody(primary, joinStartupCustody(constructionErr, closeErr))
}

func (l *Lifecycle) revokeOwned() {
	l.ownedRevoked.Store(true) // shared admission cutoff BEFORE q.Done notification
	l.revokeExpiry()
	if retained := l.retained.Load(); retained != nil {
		retained.Revoke()
	}
}
