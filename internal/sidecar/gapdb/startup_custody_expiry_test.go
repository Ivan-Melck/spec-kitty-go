//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

// UNRUN E-only report/work COMPONENT, not an admitted gate/Host fixture.
import (
	"errors"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

func TestExpiryConcurrentFirstTeardownPublishesEntireOutcomeBeforeDone(t *testing.T) {
	l := custodyRuntime(t)
	l.expiry.work = newExpiryLifecycleWork()
	l.expiry.report = &expiryCleanupReport{}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	results := make(chan error, 8)
	go func() {
		err, _ := l.firstTeardown(func() (result error) {
			calls.Add(1)
			defer func() { result = withStartupCustody(result, unix.EIO) }()
			close(entered); <-release
			return nil
		})
		results <- err
	}()
	<-entered
	for i := 0; i < 7; i++ { go func(i int) { if i%2 == 0 { results <- l.Stop(t.Context()) } else { results <- l.CleanupStale() } }(i) }
	close(release)
	<-l.expiry.work.done // publication must precede this EXISTING E completion
	if !errors.Is(l.teardownCustodyErr, unix.EIO) || l.expiry.report.teardownPending.Load() { t.Fatal("E done preceded entire FIRST result") }
	first := l.teardownCustodyErr
	for i := 0; i < 8; i++ { if got := <-results; !errors.Is(got, first) { t.Fatal("E loser lost FIRST custody branch", got) } }
	if calls.Load() != 1 || !errors.Is(first, unix.EIO) { t.Fatal("duplicate teardown", first) }
	got := l.CleanupStale()
	primary := failure(FailureOwnership, "expiry_work", "lifecycle admission retired", nil)
	if !errors.Is(got, first) || got.Error() != primary.Error() { t.Fatal("late E reconciliation lost primary/FIRST custody", got) }
}
