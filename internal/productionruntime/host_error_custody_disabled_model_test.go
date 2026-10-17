//go:build !spec_kitty_expiry_host_qualification || !linux || !amd64

package productionruntime

import (
	"errors"
	"testing"
)

func TestHostErrorCustodyDisabledHooksModel(t *testing.T) {
	// Direct disabled-hook MODEL, separate from the actual Close body tests.
	for _, h := range []*Host{nil, {state: HostReady}} {
		h.revokeExpiryHost()
		h.expiryClosed(errors.New("MODEL ignored disabled-hook diagnostic"))
		h.expiryClosed(nil)
		if h != nil && (h.state != HostReady || h.owner != nil || h.expiry != (hostExpiryState{})) {
			t.Fatal("disabled hooks acquired state or provenance")
		}
	}
}
