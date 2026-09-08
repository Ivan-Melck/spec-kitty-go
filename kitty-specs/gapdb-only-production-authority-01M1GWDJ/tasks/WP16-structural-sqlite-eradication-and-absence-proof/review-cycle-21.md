---
affected_files: []
cycle_number: 21
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T08:41:55Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 21

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

### Interpreter ambiguity is not relevance-sensitive

```sh
#!/bin/sh
show() { printf '%s\n' native; }
time show
```

This clean script is rejected as ambiguous even though the function has no
tracked state effects and contains no forbidden execution. Compute the function
summary first. Cross-shell lookup ambiguity should widen or block only when the
summary can change executable authority.

### POSIX special-builtin prefix assignments can persist

```sh
#!/bin/dash
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3 :; }
select_retired_cli
"$review_retired_cli" /tmp/x
```

Dash persists a prefix assignment on the POSIX special builtin `:`. The proof
currently discards every prefix assignment once a command has started, so it
drops the function effect and reports absence.

Model the closed POSIX special-builtin set and interpreter-dependent persistence
semantics in the existing state lattice. Explicitly proven shells may apply the
correct behavior; generic or ambiguous shells must join possible states when
the difference is relevant. Ordinary external/regular-builtin prefixes remain
command-scoped. Opaque stateful builtins must fail typed ambiguity rather than
silently discard effects.

## Preserve

- Cycle-21 shebang identity and direct Bash/Dash/generic `time` behavior.
- Function control-flow joins, return scopes, wrapper lookup, heredoc,
  redirection, compound scope, lexer, ambiguity, and ordinary data controls.
- The observed receipt/schema-3 evidence system and all artifact/source/runtime
  identities.

## Required re-review evidence

- Accept the exact harmless generic-shell `time` helper.
- Kill the exact Dash special-builtin prefix attack.
- Exercise the closed special-builtin set, ordinary builtins/external commands,
  explicit Bash/Dash, and generic-shell joins with relevant and irrelevant
  effects.
- Update the closed census and qualify the exact integrated tree.
