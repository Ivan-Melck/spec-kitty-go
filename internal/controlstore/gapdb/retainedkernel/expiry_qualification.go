//go:build spec_kitty_expiry_host_qualification && linux && amd64

package retainedkernel

import "context"

// CheckExpiryQualificationLive is a fixed tagged liveness guard, not authority
// or an image check. It exposes no descriptors, counts, selectors or callbacks.
// No ordinary build has this method. Poll retries use the existing bounded path;
// neither that bound nor mutex waiting is a hard-wall promise.
func (l *Lifetime) CheckExpiryQualificationLive(ctx context.Context) error {
	if l == nil || ctx == nil {
		return ErrRevoked
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.live(ctx, true); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.revoked.Load() || l.closed {
		return ErrRevoked
	}
	// Last scalar liveness cutoff, NOT atomic with a caller's later CAS or
	// future cancellation/revocation. No proc/image read or hash occurs here.
	return nil
}
