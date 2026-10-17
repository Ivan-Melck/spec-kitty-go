//go:build spec_kitty_expiry_host_qualification && linux && amd64

package retainedkernel

import (
	"context"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Actual pidfd, not a verified executable/admitted provider/Host witness.
func TestRetainedExpiryQualificationLiveGuard(t *testing.T) {
	for _, mode := range []string{"live", "nil_context", "canceled", "revoked", "closed", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			fd, err := unix.PidfdOpen(os.Getpid(), 0)
			if err != nil {
				t.Fatal(err)
			}
			l := &Lifetime{pidfd: fd}
			t.Cleanup(func() { _ = l.Close() })
			ctx := context.Background()
			var want error
			switch mode {
			case "nil_context":
				ctx, want = nil, ErrRevoked
			case "canceled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx, want = c, context.Canceled
			case "revoked":
				l.Revoke()
				want = ErrRevoked
			case "closed":
				if err := l.Close(); err != nil {
					t.Fatal(err)
				}
				want = ErrRevoked
			case "unsupported":
				if err := unix.Close(fd); err != nil {
					t.Fatal(err)
				}
				l.pidfd = -1
				want = ErrUnsupported
			}
			if got := l.CheckExpiryQualificationLive(ctx); !errors.Is(got, want) {
				t.Fatalf("got %v want %v", got, want)
			}
		})
	}
	var zero *Lifetime
	if !errors.Is(zero.CheckExpiryQualificationLive(context.Background()), ErrRevoked) {
		t.Fatal("nil lifetime accepted")
	}
}
