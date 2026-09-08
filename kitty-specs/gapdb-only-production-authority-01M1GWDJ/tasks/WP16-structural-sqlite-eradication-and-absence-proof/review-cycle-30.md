---
affected_files: []
cycle_number: 30
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T18:36:06Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 30

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The global command-substitution detector runs one expansion-enabled shell
carrier because every heredoc body is removed before the detector runs:

```sh
#!/bin/bash
REVIEW_COMMAND=sql\ite3
cat <<EOF
$("$REVIEW_COMMAND" /tmp/heredoc.db)
EOF
```

An unquoted heredoc delimiter permits command substitution, so runtime executes
the retired command and the outer script can still exit successfully. The proof
reports no finding. A quoted delimiter such as `<<'EOF'` disables expansion and
is correctly inert.

Preserve heredoc delimiter quotation/expansion metadata before stripping bodies
from the outer command stream. Apply the same lexical command/backtick
substitution detector to every expansion-enabled body. Quoted-delimiter bodies
remain opaque literal data. Do not interpret or execute nested shell text.

## Preserve

- The cycle-30 global substitution law and all script refactors.
- Arithmetic expansion remains distinct.
- Quoted heredocs, escaped substitution text, comments, and single-quoted data
  remain inert.
- Command-target allowlist and all prior receipt/runtime/layer invariants.

## Required re-review evidence

- Reject the exact unquoted-heredoc reproducer above.
- Cover `<<`, `<<-`, quoted/escaped delimiters, multiple heredocs, indentation,
  nested substitution, malformed/unterminated bodies, and inert literal text.
- Prove body expansion metadata survives sanitization and is scanned before any
  query/data early return.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
