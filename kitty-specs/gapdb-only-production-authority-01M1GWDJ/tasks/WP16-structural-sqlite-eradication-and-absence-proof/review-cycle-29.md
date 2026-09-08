---
affected_files: []
cycle_number: 29
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T18:08:22Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 29

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The executable-position allowlist is sound for parameter expansion, but the
query/data carveout treats command substitution as inert result text even
though computing that result executes a nested command:

```sh
#!/bin/bash
REVIEW_COMMAND=sql\ite3
command -v "$("$REVIEW_COMMAND" /tmp/query.db)"
printf '%s\n' "$("$REVIEW_COMMAND" /tmp/data.db)"
```

Both substitutions execute the retired command. The outer query or print may
be harmless, but its argument production is not. The proof returns early for a
query target or skips non-command-position words and reports false absence.

Do not interpret nested shell programs. Command substitution (`$(...)` or
backticks) is executable syntax wherever it occurs in a command word, argument,
assignment value, redirection, or query/data position. If the bounded lexer
observes such substitution and cannot prove it is inert quoted/escaped text,
return typed ambiguity and fail closed. Parameter expansion used only as data
may retain its relevance carveout because it does not itself execute a command.

## Preserve

- The cycle-29 explicit command-target precision allowlist.
- Static literal and quoted exact locally known scalar target behavior.
- Single-quoted/escaped literal substitution text remains inert.
- Exact governance exemptions and all prior receipt/runtime/layer invariants.

## Required re-review evidence

- Reject both exact query/data substitution reproducers above.
- Cover command and backtick substitution in target, argument, assignment,
  redirection, query, and nested positions.
- Preserve inert single-quoted/escaped dollar/backtick controls.
- Prove the command-substitution scan runs before query/data early returns and
  is independent of locally tracked variable names.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
