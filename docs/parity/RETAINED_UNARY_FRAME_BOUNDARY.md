# Consumed unary frame vs unread second wire frame

Status: bounded source/fixture review approved (`7ff6cb77-6c08-4e7`);
**normative second-frame obligation and full negative matrix remain OPEN**.
This documents actual behavior, not permission to weaken or silently discharge
G-M02. An explicit contract disposition is still required where stream-level
second-frame rejection/absence was required.

## Actual source contract

`internal/controlstore/gapdb/retained_transport.go: exchange` opens a fresh Unix
connection per fixed read RPC, writes one bounded request, reads exactly one
length-prefixed response payload and strictly validates that payload/envelope.
The defer performs original-peer/lifetime/context/closed checks, unregisters the
connection and closes it. No response connection is reused for a subsequent RPC,
and malformed first payloads cannot be repaired by reading a second response.

`retainedReadFrameContext` consumes exactly4header bytes plus declared payload
length. It does **not** read EOF, peek, reject a queued second frame, reserve that
unread second frame's length, or prove absence of bytes arriving later. Strict
trailing-JSON rejection WITHIN the consumed payload is a different property.
No claim about absence of a second complete wire frame follows from it.

A nonblocking peek cannot prove future absence; waiting for EOF changes the
protocol and can stall a peer permitted to keep its endpoint open. Do not add
such a check or change conservative `TransportError.Ambiguous=true` merely to
obtain a synthetic green. `PROXY_UNARY_COMPATIBILITY.md` describes another
surface (sequential product requests relayed over fresh daemon connections),
not a stream-exhaustion rule for this owned observation client.

## New executable controls

`internal/controlstore/gapdb/retained_unary_frame_test.go`:

1. Pure reader consumes first payload and leaves the entire complete second
   frame byte-for-byte untouched, including its header.
2. Real Unix socket, **in-process simulated endpoint**, queues two complete
   response frames in ONE actual Write and requires full actual byte count/nil
   error. Two client RPCs yield first-reply revisions11/12, never forged999, and
   require exactly two accepted connections. No stale second reply becomes the
   next RPC's input.
3. Malformed first framed JSON plus a complete otherwise-valid second response
   still returns the existing conservative TransportError and entirely zero
   status. Exactly one connection; no fallback, repair or hidden second exchange.

Three selected unit tests and three race tests PASS with Go1.26.7; logs:
`all-open-observation-MqMkr75l/{unary-unit.log,unary-race.log}`. These are not
admitted-provider/native-Host execution or stream EOF certificates. Bounded
independent review approves only the named source/fixture properties.

## Contract disposition still needed

A defensible narrow clarification would require strict validation of the ONE
consumed unary payload and demonstrate that unread bytes never contribute
proof authority or cross RPC lifetimes. It would explicitly make no stream-level
absence/rejection claim. The controls above support that scoped clarification;
they do not unilaterally approve it or prove full semantic unreachability across
all future paths/providers. If the normative requirement instead demands actual
second-frame detection/refusal, transport/protocol design and separately reviewed
native evidence are still needed. No mapping count or green control closes that
choice; G-M02 remains OPEN rather than silently waived.
