# WP16 review feedback — cycle 9

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The shell word evaluator resolves the forbidden escaped assignment, but command
use detection is anchored only at statement start. This valid script invokes
the retired executable while the gate passes:

```sh
review_retired_cli=sql\ite3
if "$review_retired_cli" /tmp/wp16-review-authority.db; then
  :
fi
```

Implement explicit command-position tracking across supported POSIX reserved
words, lists, pipelines, negation, grouping, and newlines. Any tracked variable
that appears in a command position must resolve and be inspected; a construct
that can invoke it but cannot be classified must fail with typed ambiguity.

## Preserve

- Static POSIX word evaluation and unresolved direct-command refusal.
- All comment, quote, escape, continuation, delimiter, horizon, ordering, and
  byte-budget controls.
- Non-command and uninvoked data remain clean.
- Exact sidecar admission, 23 observed runtime receipts, the closed 35-artifact
  inventory, and every source/per-class digest remain independently verifiable.
- All architecture/race/parity/build/tidy/vet/static evidence stays green.

## Required re-review evidence

- Kill exact `if "$variable"`, plus command positions following `then`, `elif`,
  `else`, `while`, `until`, `do`, `!`, `&&`, `||`, `|`, `;`, newline, and
  supported grouping.
- Preserve data arguments such as `printf '%s' "$variable"` as non-command
  uses.
- Unsupported command-position syntax involving a tracked variable must fail
  closed, not disappear.
- Qualify the exact final integrated tree and recompute observed runtime and the
  complete manifest/inventory tuple.
