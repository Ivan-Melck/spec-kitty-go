# WP16 review feedback — cycle 12

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

Option arguments can terminate interpreter/wrapper dispatch tracking before a
later tracked executable value:

```sh
review_retired_cli=sql\ite3
/bin/sh -o errexit -c "$review_retired_cli"
```

```sh
review_retired_cli=sql\ite3
/usr/bin/env -C . "$review_retired_cli" /tmp/wp16-review-authority.db
```

Do not let incomplete option arity create absence. For every recognized
interpreter or utility-dispatch wrapper, conservatively inspect every tracked
value in the remaining invocation as executable-capable, or implement a closed
per-command option grammar whose unknown option/argument state fails typed
ambiguity. Ordinary non-dispatch commands may still treat tracked values as
data arguments.

## Preserve

- Basename normalization, nested dispatch, case actions, command-position
  grammar, and prior direct interpreter/wrapper attacks.
- Normal data uses outside dispatch remain clean.
- All word/comment/quote/budget and ambiguity invariants remain intact.
- Exact sidecar admission, 23 observed runtime receipts, 35-artifact inventory,
  source identity, and per-class digests remain independently verifiable.
- All prior focused/race/architecture/parity/build/tidy/vet/static evidence.

## Required re-review evidence

- Kill exact `sh -o errexit -c "$variable"` and
  `env -C . "$variable"` attacks.
- Exercise unknown and known option sequences before tracked values without
  permitting parser-state escape.
- Preserve ordinary `printf`/data-argument controls outside dispatch.
- Qualify the exact final integrated tree and recompute the observed runtime and
  complete manifest/inventory tuple.
