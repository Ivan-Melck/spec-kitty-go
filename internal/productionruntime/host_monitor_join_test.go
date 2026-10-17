package productionruntime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// Component-only owner: no provider, child, descriptor, root, or Native credit.
type monitorJoinOwner struct {
	*hostOwnerProbe
	host *Host
	entered chan struct{}
	cancelled chan error
	release chan struct{}
	releaseOnce sync.Once
	unsafeClose atomic.Bool
}
func (o *monitorJoinOwner) Probe(ctx context.Context) error {
	o.probes.Add(1)
	close(o.entered)
	<-ctx.Done()
	o.cancelled <- ctx.Err()
	<-o.release
	return ctx.Err()
}
func (o *monitorJoinOwner) Close(ctx context.Context) error {
	o.host.mu.Lock()
	done := o.host.monitorDone
	o.host.mu.Unlock()
	select {
	case <-done:
	default: o.unsafeClose.Store(true)
	}
	return o.hostOwnerProbe.Close(ctx)
}
func monitorJoinWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3*time.Second): t.Fatal("component monitor worker did not publish completion")
	}
}
func monitorJoinCloseResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done: return err
	case <-time.After(3*time.Second): t.Fatal("component Close did not return"); return nil
	}
}
func monitorJoinFixture(t *testing.T, database string) (*Host, *monitorJoinOwner) {
	t.Helper()
	h, _ := hostFixture(t, journalv2.DatabaseID(database))
	o := &monitorJoinOwner{hostOwnerProbe: h.owner.(*hostOwnerProbe), host: h,
		entered: make(chan struct{}), cancelled: make(chan error, 1), release: make(chan struct{})}
	h.owner = o
	t.Cleanup(func() {
		o.releaseOnce.Do(func() { close(o.release) })
		h.mu.Lock(); stop, done := h.monitorStop, h.monitorDone; h.mu.Unlock()
		if stop != nil { stop() }
		if done != nil { monitorJoinWait(t, done) }
		_ = h.Close(context.Background())
		// Dispose ONLY this synthetic test registration after actual worker
		// completion. This is NOT a successful Host.Close/owner cleanup receipt.
		h.releaseProcessOwner()
	})
	if err := h.transition(HostReconciling); err != nil { t.Fatal(err) }
	if err := h.transition(HostReady); err != nil { t.Fatal(err) }
	return h, o
}

func TestHostCloseJoinsActualMonitorBeforeOwnerTeardown(t *testing.T) {
	h, o := monitorJoinFixture(t, "database-monitor-actual-join")
	h.startMonitor(context.Background(), time.Millisecond)
	originalDone := h.monitorDone
	h.startMonitor(context.Background(), time.Millisecond)
	if h.monitorDone != originalDone { t.Fatal("second start abandoned original monitor") }
	monitorJoinWait(t, o.entered)
	result := make(chan error, 1)
	go func() { result <- h.Close(t.Context()) }()
	if err := monitorJoinCloseResult(t, o.cancelled); err != context.Canceled && err != context.DeadlineExceeded { t.Fatal("monitor context did not actually end", err) }
	if o.closed.Load() != 0 { t.Fatal("owner closed while actual Probe was held") }
	o.releaseOnce.Do(func() { close(o.release) })
	if err := monitorJoinCloseResult(t, result); err != nil { t.Fatal(err) }
	select { case <-originalDone: default: t.Fatal("Close returned before actual monitor publication") }
	if o.unsafeClose.Load() || o.closed.Load() != 1 || o.probes.Load() != 1 || h.State() != HostClosed { t.Fatal("unjoined or repeated owner teardown") }
	if err := h.Close(t.Context()); err != nil || o.closed.Load() != 1 { t.Fatal("successful close not idempotent", err) }
	h.startMonitor(context.Background(), time.Millisecond)
	if h.monitorDone != originalDone { t.Fatal("closed Host started a new monitor") }
}

func TestHostCloseCancelledMonitorJoinCannotTeardownOwner(t *testing.T) {
	h, o := monitorJoinFixture(t, "database-monitor-cancelled-join")
	h.startMonitor(context.Background(), time.Millisecond)
	monitorJoinWait(t, o.entered)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- h.Close(ctx) }()
	if err := monitorJoinCloseResult(t, o.cancelled); err != context.Canceled && err != context.DeadlineExceeded { t.Fatal("monitor context end not observed", err) }
	cancel() // Actual caller cancellation while the ACTUAL monitor remains held.
	first := monitorJoinCloseResult(t, result)
	var typed *Error
	if !IsKind(first, ErrorShutdown) || !errors.As(first, &typed) || typed.Field != "monitor_join" || !errors.Is(first, context.Canceled) { t.Fatal("unjoined work/caller cause lost", first) }
	select { case <-h.monitorDone: t.Fatal("held worker fabricated completion"); default: }
	if o.closed.Load() != 0 || h.State() != HostClosing { t.Fatal("unjoined monitor authorized owner teardown") }
	liveOwners.Lock(); retained := liveOwners.byDatabase[h.ownerKey] == h; liveOwners.Unlock()
	if !retained { t.Fatal("unjoined work released actual owner reservation") }
	// Existing repeat-nil API is retained, but must never erase incompleteness.
	if h.Close(t.Context()) != nil || h.State() != HostClosing || o.closed.Load() != 0 || first == nil { t.Fatal("repeat nil repaired FIRST incomplete attempt") }
	o.releaseOnce.Do(func() { close(o.release) })
	monitorJoinWait(t, h.monitorDone)
	if h.State() != HostClosing || o.closed.Load() != 0 { t.Fatal("later worker completion invented cleanup") }
}

func TestHostCloseCancelledCommandDrainCannotTeardownOwner(t *testing.T) {
	h, o := monitorJoinFixture(t, "database-monitor-command-drain")
	release, err := h.beginCommand("epoch-current")
	if err != nil { t.Fatal(err) }
	var once sync.Once
	end := func() { once.Do(release) }
	t.Cleanup(end)
	ctx, cancel := context.WithCancel(t.Context()); cancel()
	first := h.Close(ctx)
	var typed *Error
	if !IsKind(first, ErrorShutdown) || !errors.As(first, &typed) || typed.Field != "command_drain" || !errors.Is(first, context.Canceled) { t.Fatal("undrained command/caller cause lost", first) }
	if o.closed.Load() != 0 || h.State() != HostClosing { t.Fatal("undrained command authorized owner teardown") }
	end()
	if h.Close(t.Context()) != nil || o.closed.Load() != 0 || h.State() != HostClosing { t.Fatal("later drain/repeat nil fabricated cleanup") }
}
