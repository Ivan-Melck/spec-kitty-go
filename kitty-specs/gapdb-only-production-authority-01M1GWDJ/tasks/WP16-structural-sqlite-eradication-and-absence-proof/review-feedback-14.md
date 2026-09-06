# WP16 review feedback — cycle 14

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

Assignment-only state is propagated across shell isolation boundaries where it
cannot affect the parent or sibling command.

Pipeline component:

```sh
review_retired_cli=sql\ite3
review_retired_cli=native | "$review_retired_cli" /tmp/x
```

The right-hand component expands the prior value; the left-hand assignment
cannot persist into it. The proof currently commits the pending assignment at
the pipeline boundary and misses the execution.

Subshell:

```sh
review_retired_cli=sql\ite3
( review_retired_cli=native )
"$review_retired_cli" /tmp/x
```

The subshell assignment cannot persist to the parent. The proof currently
commits it at statement completion and misses the later execution.

Make tracked assignment state scope-aware. Assignment-only simple commands
persist across ordinary sequential `;` and newline boundaries in the current
shell. They must not escape pipeline components or subshells. A construct whose
scope cannot be modeled within the bounded grammar must fail with typed shell
ambiguity rather than creating absence.

## Preserve

- The correct pre-command snapshot and command-prefix/environment assignment
  expansion behavior from cycle 14.
- The generalized interpreter, utility, and indirect-dispatch rule.
- The independently accepted source-bound receipt design: 155 absence receipts,
  8 detector-layer receipts, raw and derived digests, exact membership, and
  zero skip/fail/unmatched/missing/duplicate states.
- All prior source/module/SBOM/binary/install/help/runtime/sidecar/manifest and
  shell lexer/ambiguity/data-control invariants.

## Required re-review evidence

- Kill both exact pipeline and subshell attacks.
- Preserve standalone assignment persistence across sequential `;` and newline
  boundaries.
- Prove grouped current-shell state and isolated pipeline/subshell state with
  positive and clean controls.
- Qualify the exact final integrated tree and recompute every evidence digest
  and inventory entry.
