//go:build linux && amd64

package retainedkernel

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Conditional SOURCE events only. This is not a compiler/ABI allocation count,
// actual total allocation, simultaneous heap, RSS, or complete kernel ledger.
type retainedLiveLedger struct {
	liveEntries, pollArrays, timespecRequests, borrowedErrCalls uint64
	requestedCandidates, conditionalSlotCandidates              uint64
	fullCallCoverage                                            bool
}

func retainedLiveEvents(attempts, checks, image, verifies, terminalGuard uint64) (retainedLiveLedger, bool) {
	if attempts > 4294 || checks > 3*attempts+1 || verifies > 2*attempts || terminalGuard > 1 || image == 0 || image > 64<<20 || checks != 0 && image > (32<<30)/checks {
		return retainedLiveLedger{}, false
	}
	// Includes two readCallbacks per positive-byte read plus EOF/overflow probes,
	// three one-shot opens' SIX extra checks, both bounded proc streams, endpoint
	// and connection checks. C and I independent maxima may NOT be multiplied.
	// All factors are bounded above BEFORE multiplication; max expression <2^37.
	live := 2*checks*image + (4*65536+18)*checks + 5*verifies + terminalGuard
	polls := 64 * live
	// One []PollFd backing per live(requireFD), NOT one per retry. But unix.Poll
	// constructs a NEW Timespec on EVERY timeout>=0 call, including timeout0.
	// These are conditional source candidates, possibly stack/optimized away.
	return retainedLiveLedger{liveEntries: live, pollArrays: live, timespecRequests: polls,
		borrowedErrCalls:          129*live + terminalGuard,
		requestedCandidates:       8*live + 16*polls,
		conditionalSlotCandidates: 16*live + 16*polls}, true
}

func TestRetainedLiveConditionalSourceEvents(t *testing.T) {
	if runtime.Version() != "go1.26.7" || unsafe.Sizeof(unix.PollFd{}) != 8 || unsafe.Sizeof(unix.Timespec{}) != 16 || maxPollAttempts != 64 {
		t.Fatal("pinned profile/layout/attempt bound drift")
	}
	for _, in := range [][5]uint64{{0, 0, 1, 0, 0}, {1, 4, 5327126, 2, 1}, {4294, 12883, (32 << 30) / 12883, 8588, 1}} {
		got, ok := retainedLiveEvents(in[0], in[1], in[2], in[3], in[4])
		want := 2*in[1]*in[2] + (4*65536+18)*in[1] + 5*in[3] + in[4]
		if !ok || got.fullCallCoverage || got.liveEntries != want || got.pollArrays != want || got.timespecRequests != 64*want || got.borrowedErrCalls != 129*want+in[4] || got.requestedCandidates != 1032*want || got.conditionalSlotCandidates != 1040*want {
			t.Fatal("partial liveness recipe drift", in, got)
		}
	}
	for _, in := range [][5]uint64{{4295, 0, 1, 0, 0}, {0, 2, 1, 0, 0}, {1, 0, 1, 3, 0}, {1, 0, 1, 0, 2}, {1, 1, 0, 0, 0}, {1, 1, 64<<20 + 1, 0, 0}, {4294, 12883, 64 << 20, 8588, 1}, {^uint64(0), 1, 1, 1, 1}} {
		if got, ok := retainedLiveEvents(in[0], in[1], in[2], in[3], in[4]); ok || got != (retainedLiveLedger{}) {
			t.Fatal("invalid source-event schedule earned a ledger", in, got)
		}
	}
}

type retainedErrCountingContext struct {
	context.Context
	calls int
}

func (c *retainedErrCountingContext) Err() error { c.calls++; return nil }

func TestRetainedLiveRetryAndBorrowedCallMultiplicity(t *testing.T) {
	// Existing private per-call seam only. No new production injection surface,
	// no kernel/admitted executable/native witness, no actual Timespec heap count.
	l := &Lifetime{pidfd: 0}
	ctx := &retainedErrCountingContext{Context: context.Background()}
	attempts := 0
	var first *unix.PollFd
	err := l.liveWithPoll(ctx, true, func(fds []unix.PollFd, timeout int) (int, error) {
		attempts++
		if len(fds) != 1 || fds[0].Fd != 0 || timeout != 0 || fds[0].Events != unix.POLLIN {
			t.Fatal("poll shape drift")
		}
		if first == nil {
			first = &fds[0]
		} else if first != &fds[0] {
			t.Fatal("retry creates a different poll backing")
		}
		return 0, unix.EINTR
	})
	if !errors.Is(err, unix.EINTR) || attempts != 64 || ctx.calls != 129 {
		t.Fatal("EINTR prefix accounting", err, attempts, ctx.calls)
	}
}
