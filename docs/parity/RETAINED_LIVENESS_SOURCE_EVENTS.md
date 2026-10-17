# Retained liveness source events — PARTIAL conditional leaf

`retainedkernel/live_accounting_test.go` is a test-only source-event model. No
production change or new fault seam. `fullCallCoverage=false` ALWAYS. It cannot
certify heap escape, compiler/ABI extra copies, actual allocation-call count,
cumulative allocated memory, simultaneous live/peak heap, RSS or full kernel cost.

## Scheduled envelope, not arbitrary public-call coverage

Go1.26.7/linux/amd64 ordinary allocator, x/sys v0.47.0. Let A be attempted fixed
RPCs<=4294, C attempted full kernel checks<=3A+1<=12883, 0<I<=64MiB with
C*I<=32GiB (independent maxima NOT jointly assumed), V connection verifications
<=2A, T fixed Consume guard entries<=1. Failed prefixes are included. Preparation/
Lifetime.New is baseline, not repeated per observation. Future V3 outer CheckHeld
guards and other direct callers are NOT covered by T; their additional owned
work needs separate schedule/composition accounting. No old certificate transfers.

For bounded proc P=65536, read iterations per full check are at most I+2P+3
(including EOF/overflow probes), with at most two liveness callbacks per iteration.
Fixed checks are initial/final2, configured endpoint4 and three one-shot opens'
SIX pre/post checks. Thus

    L <= 2*C*(I+2P+3) + 12*C + 5*V + T
      = 2*C*I + (4P+18)*C + 5*V + T.

Use this as a conservative source-entry envelope, not a tight actual schedule or
wall-time bound. Inputs are bounded before multiplication. Max expression<2^37;
all derived source-event products fit uint64. No cap is increased.

## Important multiplicity correction

liveWithPoll constructs ONE length1 PollFd backing per reached requireFD branch,
OUTSIDE the retry loop. PollFd is8 bytes noscan; conditional fresh ordinary tiny
block reserve16. This is NOT64 backings per entry. Its up-to64 inspector calls
use timeout0. x/sys unix.Poll constructs `new(Timespec)` on EVERY nonnegative-
timeout call, INCLUDING0, before ppoll. Therefore at most64L separate16-byte
Timespec SOURCE candidates, not one Timespec per liveness entry. Even failed EINTR
prefixes construct the next candidate. The resulting conditional source terms are

    requested candidates <= (8 + 64*16)*L = 1032*L
    conditional slot candidates <= (16 + 64*16)*L = 1040*L.

These source requests may be stack/optimized; they are NOT proved heap allocations.
PollFd/Timespec have no pointer fields. Existing slot recipes apply PER candidate,
not after summing. No grand observer/kernel sum is made or deduplicated here.

liveWithPoll invokes borrowed ctx.Err at most1+2*64=129 times per entry. Accepted
Consume guard adds one final Err, so call bound129L+T. Finite invocation counts do
NOT bound arbitrary caller context method allocation, blocking, recursion or lazy
error behavior. These borrowed callbacks are disclosed, not assigned fake zero
cost or used to exclude exclusive stdlib/SDK work. Context-library private wrapper
allocations, errors.Is/compiler interfaces, syscall/runtime/netpoll/GC/stack and
new tagged root/gate/cleanup-report costs remain OPEN.

## Evidence

Source derivation: unchanged lifetime.go liveWithPoll/checkLocked/startTicks/
readChunks/configuredSocketIdentity/VerifyConnection; file_open.go's actual extra
pre/post callbacks; pinned x/sys syscall_linux.go Poll/Ppoll and Timespec layout.
The prior kernel research registry is a lead, not an approval or a read certificate.
Independent actual-source review is required before accepting this new leaf.

New controls check zero/refusal/joint envelopes, exact layout/profile and products;
existing private liveWithPoll seam drives64 EINTR callbacks, confirms the SAME poll
backing and129 borrowed Err calls. That synthetic test neither calls unix.Poll nor
establishes Timespec escape/heap multiplicity or a real kernel/Host witness. Race
is correctness only; no allocation/performance execution or whole-layer completion.
