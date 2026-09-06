# WP16 review feedback — cycle 5

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The multiline shell resolver silently stops at 64 lines. At exact candidate
`6156276a`, a split forbidden executable assignment followed by 65 comment
lines and then a command-position invocation passes the absence gate. A search
budget must never become evidence of absence.

Scan the complete already-size-bounded shell source using a bounded algorithm,
or emit an explicit ambiguity/budget finding whenever analysis cannot cover the
whole file. Add the exact beyond-64-lines attack as a killing regression while
preserving clean long uninvoked-data behavior.

## Preserve

- Same-line and ordinary multiline attacks remain killed.
- Uninvoked data and command-before-assignment remain clean.
- Candidate scripts remain within the source gate.
- Sidecar admission, 23 observed runtime RUN/PASS receipts, and the closed
  35-artifact manifest/inventory remain exact and independently verifiable.
- All prior architecture, absence, race, parity, build, tidy, vet, and static
  evidence remains green.

## Required re-review evidence

- Kill the exact 65-intervening-line attack.
- Demonstrate a clean long shell file with split but uninvoked data passes.
- Show analysis covers the complete allowed file or fails closed at its budget.
- Qualify the exact final integrated tree and recompute the complete observed
  runtime/manifest/inventory tuple.
