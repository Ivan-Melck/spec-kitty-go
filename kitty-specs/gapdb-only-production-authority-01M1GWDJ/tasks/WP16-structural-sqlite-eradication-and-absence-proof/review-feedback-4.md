# WP16 review feedback — cycle 4

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The shell semantic detector associates a split-token assignment with its
invocation only when both occur on the same line. At exact candidate
`0026b488`, this executable source passes `TestM2SQLiteAbsence`:

```sh
review_retired_cli='sql''ite3'
"$review_retired_cli" /tmp/wp16-review-authority.db
```

Parse assignments independently of line-local trailing text, then detect a
later command-position invocation across bounded subsequent shell source. Add
this exact multiline case as a killing regression while preserving the clean
uninvoked-data control. Qualification must apply the corrected source gate to
its own scripts.

## Preserve

- Go concatenation and alias/variable-flow attacks remain killed.
- Same-line shell splitting remains killed and uninvoked data remains clean.
- All sidecar admission refusals and exact admitted digest checks remain.
- Observed runtime evidence remains exactly 23 RUN plus 23 PASS events with no
  skip, fail, or unmatched event.
- The closed 35-artifact inventory and every manifest/source/per-class digest
  remain independently verifiable.
- All prior deletion, architecture, race, parity, build, and static evidence
  remains green.

## Required re-review evidence

- Reproduce and kill both same-line and multiline shell assignment/invocation.
- Preserve a clean multiline split assignment that is never command-invoked.
- Qualify the exact final integrated tree and recompute the complete observed
  runtime/manifest/inventory tuple.
