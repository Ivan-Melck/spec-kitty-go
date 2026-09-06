# WP16 review feedback — cycle 8

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The shell lexer preserves an unquoted backslash in assignment text, while the
assignment recognizer rejects that text and silently ignores the assignment.
POSIX shell removes the backslash, so this executable source constructs and
runs the retired command while the gate passes:

```sh
review_retired_cli=sql\ite3
"$review_retired_cli" /tmp/wp16-review-authority.db
```

Implement sound shell-word unquoting for the supported static assignment
grammar. More generally, every syntactically valid assignment that later feeds
command position must either resolve under the supported grammar or produce a
typed ambiguity finding; unsupported evaluation must never be silently absent.

## Preserve

- Comment, quote, escape, backslash-newline, delimiter, adjacency, horizon, and
  ordering regressions remain correct.
- Uninvoked data remains clean; unterminated/unsupported lexical state fails
  closed.
- Full-file budgets and candidate-script scanning remain active.
- Sidecar admission, 23 observed runtime receipts, closed 35-artifact inventory,
  and every source/per-class digest remain independently verifiable.
- All prior architecture/race/parity/build/tidy/vet/static evidence stays green.

## Required re-review evidence

- Kill the exact unquoted-backslash assignment attack.
- Exercise supported unquoted, single-quoted, double-quoted, concatenated, and
  escaped literal assignment words feeding command position.
- Show unsupported assignment evaluation that reaches command position yields
  typed ambiguity, while unsupported but uninvoked data does not create a
  false executable finding.
- Qualify the exact final integrated tree and recompute the observed runtime and
  complete manifest/inventory tuple.
