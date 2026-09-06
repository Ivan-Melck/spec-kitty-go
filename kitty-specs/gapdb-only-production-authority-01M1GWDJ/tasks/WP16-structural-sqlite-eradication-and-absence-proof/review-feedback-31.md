# WP16 review feedback — cycle 31

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

An expansion-enabled heredoc removes backslash-newline pairs before expansion.
That preprocessing can synthesize active command-substitution syntax after the
detector has inspected the original bytes:

```sh
#!/bin/bash
REVIEW_COMMAND=sql\ite3
cat <<EOF
$\
("$REVIEW_COMMAND" /tmp/continued.db)
EOF
```

Runtime joins the `$` and `(` and executes the nested command. The proof skips
the escaped newline and reports no finding.

Do not grow a heredoc expansion evaluator. In an expansion-enabled heredoc,
any unquoted backslash-newline continuation is a typed ambiguity and fails
closed before substitution scanning. A quoted/escaped delimiter makes the body
literal, so the same bytes remain inert there. Preserve ordinary escaped-dollar
behavior that cannot cross a continuation boundary.

## Preserve

- Typed heredoc body/delimiter metadata and the cycle-31 direct substitution
  detector.
- Quoted-delimiter heredocs remain opaque literal data.
- Global substitution law, command-target allowlist, script refactors, and all
  preceding invariants.

## Required re-review evidence

- Reject the exact continuation reproducer above.
- Cover CRLF/LF continuations, `<<`/`<<-`, multiple joins, joins adjacent to
  dollar/backtick syntax, ordinary escaped dollars, and quoted delimiters.
- Demonstrate fail-closed preprocessing happens before active-substitution
  classification without interpreting the joined program.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
