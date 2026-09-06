# WP16 review feedback — cycle 18

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

Function summaries are applied without proving that the function executes or
that the summarized effects are reachable.

```sh
review_retired_cli=sql\ite3
select_native_cli() { review_retired_cli=native; }
false && select_native_cli
"$review_retired_cli" /tmp/x
```

```sh
review_retired_cli=sql\ite3
select_native_cli() { return 0; review_retired_cli=native; }
select_native_cli
"$review_retired_cli" /tmp/x
```

```sh
review_retired_cli=sql\ite3
select_native_cli() { review_retired_cli=native; }
command select_native_cli || :
"$review_retired_cli" /tmp/x
```

All three execute the prior retired value, but the proof reports absence.

Narrow the sound-summary boundary. Apply effects only for a proven direct,
straight-line function call with reachable deterministic effects. Conditional,
short-circuit, branching, early-return, recursive, or otherwise uncertain
execution must preserve a join of prior/effect state or fail typed ambiguity.
`command` and other lookup/dispatch wrappers must follow their actual function
lookup semantics and must not accidentally invoke a shell-function summary.

## Preserve

- The correct unconditional direct-call function behavior and definition-time
  isolation from cycle 18.
- Multiline functions, fd/Bash redirects, heredoc data handling, compound
  scopes, assignments, dispatch, lexer, ambiguity, and ordinary data controls.
- The independently accepted observed receipt and schema-3 evidence design.
- All source/module/SBOM/binary/install/help/runtime/sidecar/source-identity,
  manifest, and inventory invariants.

## Required re-review evidence

- Kill all three exact function-control attacks.
- Exercise `&&`, `||`, conditionals, early `return`, wrapper lookup bypass,
  recursion, direct deterministic calls, and safe/no-effect functions.
- Update and verify the closed observed census with zero anomalies.
- Qualify the exact final integrated tree and recompute all evidence digests.
