//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

// UNRUN E COMPONENT conjunctions with actual owned test-child Wait. No admitted
// E workload, provider, positive E Attach/proxy, failed kernel-close or Host credit.
import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func revision3MarkChild(t *testing.T, l *Lifecycle) *expiryCleanupReport {
	t.Helper()
	r := new(expiryCleanupReport)
	v := &VerifiedExecutable{expiry: verifiedExpiryState{identity: ExpiryTechnicalIdentity{InputRecordSHA256:"component-not-admission"}, report:r}}
	if err := v.makeExpiryPair(context.Background()); err != nil { t.Fatal(err) }
	v.expiryCommandStarted()
	l.initExpiry(v)
	r.supervisorOpened() // actual pidfd of the test-owned child
	return r
}
func TestRevision3HistoricalSourceImageCloseStillStopsActualChild(t *testing.T) {
	for _, category := range []int{expirySource, expiryImage} {
		l, _, calls := revision3AttributedChild(t, nil)
		r := revision3MarkChild(t, l)
		owned, err := os.Open(os.Args[0])
		if err != nil { t.Fatal(err) }
		r.acquired(category)
		r.closing(category)
		closeResult := withStartupCustody(owned.Close(), unix.EIO) // actual close, returned-error component
		r.closed(category, closeResult)
		first := l.Stop(t.Context())
		if !errors.Is(first, unix.EIO) || calls.Load() != 1 || !l.shutdownWait.observed || !l.shutdownWait.reapProven || l.pidfd != -1 { t.Fatal("historical close suppressed safe body", first, l.shutdownWait) }
		<-l.expiry.work.done
		if l.teardownCustodyErr != first || !errors.Is(r.firstError(), unix.EIO) { t.Fatal("FIRST/report publication lost") }
		if l.Stop(t.Context()) != first { t.Fatal("repeat repaired history") }
	}
}
func TestRevision3GenuinelyIncompleteDrainSkipsUnsafeBody(t *testing.T) {
	l, _, calls := revision3AttributedChild(t, nil)
	r := revision3MarkChild(t, l)
	work, err := l.beginExpiryWork()
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	first := l.Stop(ctx)
	cancel()
	if !errors.Is(first, context.DeadlineExceeded) || calls.Load() != 0 || l.pidfd < 0 || l.shutdownWait.attempted { t.Fatal("incomplete work authorized teardown", first) }
	l.finishExpiryWork(work)
	if got := l.Stop(t.Context()); got != first || r.cleanupError() == nil { t.Fatal("late drain repaired FIRST", got) }
	// Fixture stdin cleanup owns the child. Release only this known fixture fd,
	// not a product retry, foreign artifact deletion or durable residual proof.
	l.mu.Lock(); closeErr := l.closePidfd(); l.mu.Unlock()
	if closeErr != nil { t.Fatal(closeErr) }
}
func TestRevision3AdmissionCutoffBeforeDoneBeforeOwnerWhileMutexHeld(t *testing.T) {
	q, _, l := expiryLaunchUnitGate(t)
	l.mu.Lock()
	l.revokeOwned() // deterministic old notification-to-owner interval
	select { case <-q.Done(): default: l.mu.Unlock(); t.Fatal("notification absent") }
	if l.expiry.work.owner.Load() { l.mu.Unlock(); t.Fatal("test did not expose pre-owner interval") }
	// This call MUST return without waiting for the mutex held by this thread.
	work, err := l.beginExpiryWork()
	l.mu.Unlock()
	if err == nil || work != nil { t.Fatal("notification preceded admission cutoff") }
	if l.expiry.report.lifecycleRetired.Load() { t.Fatal("revocation fabricated retirement") }
}
func TestRevision3OriginalCleanupCutoffSharedNotRefreshed(t *testing.T) {
	r := new(expiryCleanupReport)
	outer, cancel := context.WithDeadline(t.Context(), time.Now().Add(time.Second))
	defer cancel()
	first := r.ownCleanupCutoff(outer, time.Now().Add(expiryCleanupBudget))
	later := r.ownCleanupCutoff(context.Background(), time.Now().Add(30*time.Second))
	if later != first { t.Fatal("new observation/cleanup budget granted") }
	short, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	if got := r.ownCleanupCutoff(short, later); !got.Before(first) { t.Fatal("earlier governing deadline ignored") }
	if *r.cleanupCutoff.Load() != first { t.Fatal("original envelope rewritten") }
}
func TestRevision3AdmissionAndQuiescenceSourceContracts(t *testing.T) {
	owned, err := os.ReadFile("owned.go")
	if err != nil { t.Fatal(err) }
	_, body, ok := strings.Cut(string(owned), "func (l *Lifecycle) revokeOwned() {")
	if !ok || !strings.HasPrefix(strings.TrimSpace(body), "l.ownedRevoked.Store(true)") { t.Fatal("shared cutoff must precede notification") }
	work, err := os.ReadFile("expiry_qualification_work.go")
	if err != nil { t.Fatal(err) }
	if strings.Count(string(work), "l.expiryAdmissionRetired() || w.owner.Load()") != 2 { t.Fatal("admission must check before and under mutex") }
	if strings.Contains(string(work), "gateErr := (&ExpiryQualification") { t.Fatal("historical Close error reused as rundown authority") }
	prior, err := os.ReadFile("expiry_qualification_work_test.go")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(prior), "func TestExpiryC3StopClaimsBeforeMutexAndDrainsPublicInspection") { t.Fatal("old ordering obligation deleted") }
}
