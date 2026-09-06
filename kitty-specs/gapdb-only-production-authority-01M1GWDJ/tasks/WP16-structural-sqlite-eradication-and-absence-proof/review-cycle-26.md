---
affected_files: []
cycle_number: 26
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T15:54:46Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 26

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

The one-word safe-atom exception for unquoted command variables assumes `IFS`
is unchanged, but top-level special-builtin mutation is not completely tracked:

```sh
#!/bin/bash
export IFS=:
review_command='set:-o:posix'
command $review_command
review_retired_cli=native
review_make_retired() { review_retired_cli=sql\ite3 :; }
review_make_retired
"$review_retired_cli" /tmp/x
```

Runtime splits the value into `command set -o posix`, enters POSIX mode, and
later executes the retired command. The proof considers IFS unchanged, treats
the value as one safe atom, and reports false absence.

Do not extend the proof into complete IFS provenance. Remove the safe-atom
exception for command targets. Any unquoted variable expansion in command-name
position is dynamically field-split/pathname-expanded and must be typed
ambiguous when relevant, regardless of its observed value. Only quoted exact
variable expansion may be resolved as one scalar command name. Product/release
scripts that require a dynamic command name must quote it or use an explicitly
modeled literal form.

## Preserve

- Quoted exact-variable command targets as scalar values.
- The unified wrapper grammar, option/terminator semantics, real builtin
  effects, scoped mode state, and all preceding proof invariants.
- Typed relevance-sensitive ambiguity rather than guessed shell behavior.

## Required re-review evidence

- Reject the exact `export IFS=:` reproducer and the cycle-25 whitespace case.
- Cover alternate IFS characters, glob-shaped values, one-word-looking values,
  unresolved values, and irrelevant unquoted expansions.
- Prove no IFS-tracking or safe-atom path can authorize an unquoted dynamic
  command target.
- Preserve quoted scalar behavior and literal command forms.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
