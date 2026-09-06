# WP16 review feedback — cycle 32

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

Heredoc body mode is activated after the physical declaration line even when
backslash-newline continues the logical shell command:

```sh
#!/bin/sh
cat <<EOF \
; sql\ite3 /tmp/continued-command.db
data
EOF
```

The shell treats the second physical line as executable continuation of the
command list, then reads the heredoc body. The proof activates `pending` after
the first physical line and consumes the executable continuation as body data,
reporting false absence.

Do not reconstruct arbitrary logical shell lists. If a physical command line
that declares or accumulates a pending heredoc contains an active backslash-
newline continuation, return typed ambiguity and fail closed before body mode.
Only enter body consumption once a bounded physical line is known to terminate
the declaration command.

## Preserve

- Cycle-32 expansion-enabled body continuation refusal.
- Typed heredoc delimiter/body metadata, quoted-body opacity, global command-
  substitution rule, and command-target allowlist.
- All script refactors and prior receipt/runtime/layer invariants.

## Required re-review evidence

- Reject the exact continued-command reproducer above.
- Cover continuation before/after one or several heredoc declarations,
  multiple pending bodies, `<<-`, LF/CRLF, quoted delimiters, and harmless
  terminated declaration controls.
- Prove no executable logical-command continuation is consumed as body.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
