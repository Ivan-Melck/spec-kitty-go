# Retained kernel path-open allocation boundary

Status: **exact design independently approved; implementation present, source
review/qualification pending**. Full observer-owned accounting remains incomplete;
the existing partial slot/private-map work does not close per-call kernel/library
allocations. Design review (before edits):
`/home/user/.cache/spec-kitty-parity-resume-followup/kernel-open-design-review-c8h_4os1/NOTE.md`.
This status paragraph is a parent follow-up to that preserved design review.

## Source finding

`retainedkernel/lifetime.go` currently uses `os.Open` for the executable and
`startTicks` proc file. One `checkLocked` can reach three opens (two stat, one exe).
Pinned Go1.26.7 `os.openFileNolog` retries EINTR without a bound, calling `open` again
inside `ignoringEINTR`. The path-to-NUL buffer is inside the syscall wrapper on
every attempt. No finite event multiplicity follows from the outer image/proc
byte caps or the separately bounded pidfd poll. Compiler elision/stack placement
cannot be assumed to turn every potentially owned event into shared/free storage.
The existing cancellation callback is outside that library retry loop.

Research and inspected ranges/hashes:
`/home/user/.cache/spec-kitty-parity-resume-followup/lifetime-buffer-research-pf4jxmu0/`.
Parent additionally inspected actual lifetime source, stdlib open/NewFile, x/sys
v0.47.0 `Open -> openat`, and generated syscall wrapper. Other library/compiler
sites remain uncovered: repairing this one site is not full resource acceptance.

## Exact proposed correction

Only `internal/controlstore/gapdb/retainedkernel` product code may change:

1. Replace the two lexical os.Open sites with a private file-open helper, shared
   by executable hashing and startTicks. Check the existing liveness/cancellation
   callback before opening.
2. Make ONE `unix.Open(path, O_RDONLY|O_CLOEXEC, 0)` call. The pinned wrapper makes
   one syscall and returns EINTR; do not retry it or add an arbitrary numeric cap.
   Any failure is a failed observation, not evidence of a stable owner.
3. Only a successful nonnegative returned FD is owned. Wrap it with `os.NewFile`;
   if wrapping fails, close only that successfully acquired FD. A failed syscall's
   return value must not be closed as though it were owned.
4. Recheck liveness/cancellation after wrapping; on refusal close the owned file
   and return no usable file. Preserve relevant cleanup errors. On success the
   existing caller owns defer Close and continues unchanged identity/fstat/hash /
   start-tick/socket/pidfd checks. No descriptor escapes from public APIs.
5. A private test-only syscall seam may inject EINTR/failures to prove exactly one
   attempt. It is not a public callback/provider/selector API. Tests must prove
   pre-cancel/revoke avoids opening, EINTR fails closed without retry, post-open
   cancellation/revocation closes the owned descriptor, flags/CLOEXEC and normal
   proc/exe reads still work. Existing genuine-peer/memfd/lifetime/kernel tests
   must pass; no existing refusal budget is increased.

This intentionally permits a conservative refusal on EINTR rather than silently
spinning. It does not change TTL, writes, wire framing, count comparison, original
admission/lifecycle identity, image bytes or authority tuples. NewFile's regular
file wrapper differs from Open's attempted poll registration, so that change
needs explicit review and actual proc/exe regression tests. No hard-wall/deadline
or complete stdlib allocation guarantee is claimed.

Independent exact-design review is required before product edits, followed by
separate source review and fresh source-bound unit/race/vet/architecture and an
appropriately scoped original-image Host regression. Prior failed sources/logs
must remain preserved. No Host expiry binding, new receipt or image admission is
authorized by this correction.
