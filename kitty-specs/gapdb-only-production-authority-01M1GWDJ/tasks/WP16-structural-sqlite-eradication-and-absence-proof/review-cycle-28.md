---
affected_files: []
cycle_number: 28
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T17:00:28Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 28

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The centralized resolver recognizes exact `$NAME`/`${NAME}` command words, but
other parameter-expansion forms still fall through as apparently static:

```sh
#!/bin/bash
"${REVIEW_COMMAND:-native}" /tmp/x
```

with `REVIEW_COMMAND=sqlite3`, and:

```sh
#!/bin/bash
"${REVIEW_COMMAND_PREFIX}3" /tmp/x
```

with `REVIEW_COMMAND_PREFIX=sqlite`, both execute the retired command while the
proof reports no finding. The fallback only searches locally tracked variable
names, so environment-provided names in default/concatenated expansion remain
invisible.

Do not evaluate general parameter expansion. Make command-target precision an
explicit allowlist: literal static command words and quoted exact simple
variables whose scalar value is locally known. Any other executable word that
contains parameter, command, arithmetic, or equivalent runtime expansion is
typed `ambiguous-command-expansion` when relevant. This includes default,
alternate, assignment, error, substring, transformation, concatenated, nested,
and environment-provided forms. Single-quoted or correctly escaped dollar text
remains literal.

## Preserve

- The centralized command-target resolver as the sole semantic authority.
- Exact locally resolved quoted simple variables as scalar values.
- All unquoted variable targets and quoted unresolved variables fail closed.
- Literal command forms, irrelevant query/data uses, and all prior invariants.

## Required re-review evidence

- Reject both exact runtime reproducers above.
- Cover representative default/alternate/assignment/error/concatenated,
  command-substitution, arithmetic-expansion, nested, and environment forms.
- Preserve single-quoted/escaped literal-dollar controls.
- Prove executable expansion syntax cannot bypass the target resolver through a
  locally tracked-name filter.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
