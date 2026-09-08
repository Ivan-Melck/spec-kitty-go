---
affected_files: []
cycle_number: 19
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T08:01:01Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 19

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

### Subshell return incorrectly terminates the enclosing function summary

```sh
review_retired_cli=native
select_retired_cli()
{
  ( return 0 )
  review_retired_cli=sql\ite3
}
select_retired_cli
"$review_retired_cli" /tmp/x
```

The `return` exits only the isolated subshell. The later assignment is reached
when the function continues, but the proof walks through the isolated scope,
marks the whole function unreachable, and misses the retired execution.

Control-transfer reachability must stop at the correct shell scope. A return in
`(...)` cannot terminate an enclosing function. A return in a current-shell
brace group can.

### Bare `time` and external `time` have different lookup semantics

```sh
review_retired_cli=native
select_retired_cli() { review_retired_cli=sql\ite3; }
time select_retired_cli
"$review_retired_cli" /tmp/x
```

In Bash, bare `time` is a reserved-word pipeline prefix and can invoke a shell
function in the current shell. The proof treats every basename `time` as an
external dispatch wrapper and suppresses the function effect. Path-spelled
`/usr/bin/time` remains an external wrapper.

Classify `time` from syntax and path, not basename alone. Bare reserved-word
forms must preserve shell-function lookup and relevant effects; external/path
forms must not invent it.

## Preserve

- The cycle-19 conditional state lattice, ordered joins, reachability handling,
  and `command`/`env` wrapper bypass behavior.
- All prior function, heredoc, redirection, scope, assignment, dispatch, lexer,
  ambiguity, and data controls.
- The independently accepted observed receipt/schema-3 evidence system and all
  source/module/SBOM/binary/install/help/runtime/sidecar/source identities.

## Required re-review evidence

- Kill both exact attacks.
- Exercise return inside nested subshells versus current-shell groups/functions.
- Exercise bare `time`, `time -p`, path-spelled external time, and nested wrapper
  combinations around defined functions.
- Update and verify the closed observed census and exact-tree qualification.
