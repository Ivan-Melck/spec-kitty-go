# WP16 review feedback — cycle 23

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

The cycle-23 shell-mode state is scoped correctly, but two Bash command-grammar
forms still make the proof report false absence while runtime executes the
retired command.

`command` bypasses shell functions without bypassing the `set` builtin:

```sh
#!/bin/bash
command set -o posix
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3 :; }
select_retired_cli
"$review_retired_cli" /tmp/x
```

This enters POSIX mode in the current shell. The proof currently suppresses
mode transitions whenever command dispatch is present, so it keeps default
Bash prefix-discard semantics and reports no finding.

`--` terminates `set` option parsing:

```sh
#!/bin/bash --posix
set -- +o posix
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3 :; }
select_retired_cli
"$review_retired_cli" /tmp/x
```

Here `+o posix` is positional data and POSIX mode remains enabled. The proof
continues scanning after `--`, incorrectly changes to prefix-discard mode, and
again reports no finding.

Model the bounded Bash grammar for `set` rather than scanning all command
words. `command set` must retain the builtin's current-shell mode effect;
unsupported dispatch forms must join or fail when relevant. Stop option
interpretation at `--`, preserve the no-argument/listing forms, and retain
ordered transitions and scope behavior.

## Preserve

- Cycle-23 scoped mode lattice and all cycle-22 repairs.
- Direct, shebang, `env -S`, and `POSIXLY_CORRECT` startup semantics.
- Function-summary ordering and group/subshell/pipeline/background scope.
- All closed receipt, layer-mutant, runtime-selector, ambiguity, and relevance
  guarantees.

## Required re-review evidence

- Kill both exact runtime reproducers above.
- Cover `command set`, ordinary `set`, `set --`, no-argument/listing forms,
  option terminators, and relevant unsupported dispatch grammar.
- Preserve harmless and irrelevant controls and update the closed census.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
