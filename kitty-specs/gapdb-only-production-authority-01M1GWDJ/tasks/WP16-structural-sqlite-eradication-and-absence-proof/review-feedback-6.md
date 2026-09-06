# WP16 review feedback — cycle 6

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The full-file shell assignment regex consumes the separator semicolon. Because
matches are non-overlapping, an immediately adjacent second assignment begins
at the consumed delimiter and is skipped. At exact candidate `508d3590`, this
passes the absence gate:

```sh
review_benign=native; review_retired_cli='sql''ite3'; "$review_retired_cli" /tmp/wp16-review-authority.db
```

Use a delimiter-preserving single-pass command/assignment segmentation or an
equivalent parser that cannot consume the next event's boundary. Add the exact
adjacent-assignment attack and permutations with whitespace/newlines as killing
regressions.

## Preserve

- Complete-file analysis under explicit byte budgets and explicit oversize
  refusal.
- The 65-line/later invocation attack remains killed; long uninvoked data and
  command-before-assignment remain clean.
- Candidate scripts remain scanned.
- Sidecar admission, 23 observed runtime events, closed 35-artifact inventory,
  and all source/per-class digests remain independently verifiable.
- All prior focused, race, architecture, parity, build, tidy, vet, and static
  evidence remains green.

## Required re-review evidence

- Kill the exact adjacent benign/forbidden assignment attack.
- Exercise consecutive assignments separated by semicolon, newline, and mixed
  whitespace without losing order.
- Preserve command-before-assignment and uninvoked-data negative controls.
- Qualify the exact final integrated tree and recompute the complete observed
  runtime/manifest/inventory tuple.
