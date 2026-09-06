# WP16 review feedback — cycle 7

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The delimiter-preserving shell statement scanner has quote and escape state but
no shell-comment state. An apostrophe in an ordinary comment is treated as an
opening quote, suppressing later boundaries and hiding the forbidden command:

```sh
# don't merge comment quoting into executable parsing
review_retired_cli='sql''ite3'
"$review_retired_cli" /tmp/wp16-review-authority.db
```

Implement explicit POSIX shell comment handling: an unquoted `#` beginning a
word starts a comment through newline, comment quote characters have no lexical
effect, and newline resumes statement parsing. Unterminated quote/escape state
or unsupported constructs that prevent sound command resolution must emit a
typed parse/ambiguity finding rather than evidence of absence.

## Preserve

- Adjacent semicolon/newline/mixed-whitespace assignments and 65-line/later
  invocations remain killed.
- Command-before-assignment and long uninvoked data remain clean.
- Full-file analysis, explicit size refusal, and own-script scanning remain.
- Sidecar admission, 23 observed runtime receipts, closed 35-artifact inventory,
  source identity, and every per-class digest remain independently verifiable.
- All prior focused/race/static/parity/build evidence remains green.

## Required re-review evidence

- Kill the exact apostrophe-in-comment attack.
- Exercise comments after statements, indented comments, quoted literal `#`,
  escaped `#`, and comments between assignment and command.
- Prove unterminated lexical state fails closed.
- Qualify the exact final integrated tree and recompute observed runtime and the
  complete manifest/inventory tuple.
