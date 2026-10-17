//go:build !spec_kitty_expiry_host_qualification || !linux || !amd64

package productionruntime

// Empty complements keep ordinary bindings comparable; no marked authority,
// parser, root, or gate API is available in this build.
type authorityQualificationBinding struct{}
type ownerExpiryState struct{}
type hostExpiryState struct{}

func qualificationCanonical(AuthorityBindingV2) ([]byte, bool, error) { return nil, false, nil }
func (*productionOwner) noteExpiryOwnerClose(error)                   {}
func (*Host) revokeExpiryHost()                                       {}
func (*Host) expiryClosed(error)                                      {}
