//go:build !spec_kitty_expiry_host_qualification || !linux || !amd64

package gapdb

// First empty field preserves the ordinary state geometry, not a heap claim.
type retainedExpiryObservationState struct{}

func (*retainedObservationState) noteRetainedExpiryS1Mismatch() {}

func (s *retainedObservationState) finishRetainedObservation(pubs uint64, err error) (uint64, error) {
	if err := s.end(err); err != nil {
		return 0, err
	}
	return pubs, nil
}
