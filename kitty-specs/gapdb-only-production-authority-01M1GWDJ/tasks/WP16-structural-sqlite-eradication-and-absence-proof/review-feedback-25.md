# WP16 review feedback — cycle 25

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The unified wrapper parser erases the distinction between quoted scalar
expansion and unquoted field-splitting expansion for a resolved command target:

```sh
#!/bin/bash
review_command='set -o posix'
command $review_command
review_retired_cli=native
review_make_retired() { review_retired_cli=sql\ite3 :; }
review_make_retired
"$review_retired_cli" /tmp/x
```

Bash splits the unquoted expansion into `command set -o posix`, enters POSIX
mode, persists the later special-builtin prefix, and executes the retired
command. The proof substitutes the resolved value as one scalar command name,
models no mode transition, and reports false absence.

This is a proof-boundary defect, not a request to implement general shell
expansion. Preserve quotation context in the typed wrapper parse result.
Quoted exact-variable targets may remain scalar. An unquoted expansion may be
resolved precisely only when a small, explicit safe-atom contract proves field
splitting, pathname expansion, and active `IFS` cannot change its one-word
meaning. Otherwise, when the target or its possible builtin effects matter to
absence, return typed ambiguity and fail closed. Do not grow a bespoke shell
expansion interpreter.

## Preserve

- The cycle-25 single authoritative command-wrapper parse result.
- `command --` termination and option-shaped command-name semantics.
- Real `command set` builtin effects, `set --` termination, scoped mode state,
  and all prior receipt/runtime/layer invariants.
- Harmless quoted and provably one-word unquoted variable targets.

## Required re-review evidence

- Reject the exact unquoted multiword-variable runtime reproducer above.
- Distinguish it from the quoted form, which attempts one command name and does
  not change shell mode.
- Cover safe one-word unquoted values, whitespace/IFS-sensitive values,
  pathname-expansion characters, unresolved values, and relevance controls.
- Demonstrate ambiguous expansion is a typed fail-closed result rather than a
  guessed transition or a false clean pass.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
