# WP16 review feedback — cycle 22

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

Bash family alone does not determine POSIX special-builtin prefix semantics;
the active shell mode does.

Shebang-selected POSIX mode:

```sh
#!/bin/bash --posix
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3 :; }
select_retired_cli
"$review_retired_cli" /tmp/x
```

Runtime-selected POSIX mode:

```sh
#!/bin/bash
set -o posix
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3 :; }
select_retired_cli
"$review_retired_cli" /tmp/x
```

Both persist the special-builtin prefix and execute the retired value. The
proof ignores Bash mode and reports absence.

Carry shell mode, not only shell family, as scoped abstract state. Recognize the
closed shebang option forms that enter POSIX mode, including bounded `env -S`
forms. Model deterministic `set -o posix` and `set +o posix` transitions in the
current shell; isolated scopes must not leak mode changes. Conditional or
unmodeled mode changes must join possible modes or fail typed ambiguity when
the distinction can affect tracked authority.

Environment-selected mode or unsupported option syntax must never silently be
treated as ordinary Bash when relevant.

## Preserve

- Cycle-22 relevance-sensitive time handling and special-builtin registry.
- Explicit Dash, generic shell, and default Bash semantics where mode is known.
- All function/return/heredoc/redirection/scope/assignment/dispatch/lexer and
  evidence invariants.

## Required re-review evidence

- Kill both exact Bash POSIX-mode attacks.
- Exercise default Bash, shebang `--posix`/`-o posix`, `env -S`, runtime
  `set -o/+o posix`, conditional mode changes, and isolated mode changes.
- Preserve harmless/irrelevant controls and update the closed census.
- Qualify the exact integrated tree and recompute all evidence digests.
