# WP16 review feedback — cycle 24

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

`command --` terminates wrapper-option parsing, but the generic dispatch parser
continues treating a later option-shaped command name as a wrapper option:

```sh
#!/bin/bash --posix
command -- -p set +o posix
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3 :; }
select_retired_cli
"$review_retired_cli" /tmp/x
```

Bash treats `-p` as the command name after `--`; it fails, the shell continues,
and `set +o posix` is never executed. POSIX mode remains active, the special-
builtin assignment prefix persists, and runtime reaches the retired command.
The proof instead skips `-p` in a later generic dispatch branch, models the
following words as a real `set +o posix`, and reports false absence.

Make command-wrapper parsing one authoritative bounded grammar. Once `--`
terminates options, the immediately following word is the command name even
when option-shaped, and no generic wrapper filter may reinterpret it. A failed
or unsupported command must not lend the semantic effects of its later words
to a nested builtin.

## Preserve

- Both cycle-23 repairs: `command set` invokes the real current-shell builtin,
  and `set --` terminates set-option parsing.
- The cycle-23 scoped mode lattice and all preceding shell/receipt invariants.
- Exact runtime, layer-mutant, ambiguity, relevance, and closed-census checks.

## Required re-review evidence

- Kill the exact `command -- -p set +o posix` runtime reproducer.
- Cover option-shaped command names after `command --`, ordinary `command -p`,
  wrapper query forms, missing command names, and harmless controls.
- Demonstrate there is only one command-wrapper parse result consumed by mode
  transition and dispatch handling.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
