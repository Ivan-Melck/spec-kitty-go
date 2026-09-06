---
affected_files: []
cycle_number: 15
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T06:24:05Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 15

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

A brace group normally shares the current shell, but becomes isolated when it
is a pipeline or background component. The statement splitter commits an
assignment inside the group before it sees the operator following `}`.

Pipeline group:

```sh
review_retired_cli=sql\ite3
{ review_retired_cli=native; } | "$review_retired_cli" /tmp/x
```

Background group:

```sh
review_retired_cli=sql\ite3
{ review_retired_cli=native; } & "$review_retired_cli" /tmp/x
```

Both siblings expand the parent value. The group assignment cannot persist,
but the current proof reports no finding.

Compound-command scope must remain unresolved until the closing delimiter and
its following operator are classified. An ordinary current-shell brace group
must retain assignment persistence; a brace group used in a pipeline or
background component must isolate its mutations. If bounded lookahead cannot
classify a construct, refuse it with typed shell ambiguity.

## Preserve

- Cycle-15 sequential, simple-pipeline, background, subshell, nested-subshell,
  command-prefix, and assignment-expansion behavior.
- Current-shell brace-group persistence where no isolating operator applies.
- The independently accepted observed receipt and schema-3 manifest design.
- Every prior static/source/module/SBOM/binary/install/help/runtime/sidecar,
  source-identity, lexer, ambiguity, wrapper, and data-control invariant.

## Required re-review evidence

- Kill the exact brace-group pipeline and background attacks.
- Exercise nested groups, groups within pipelines/background jobs, and ordinary
  current-shell groups with positive and clean controls.
- Preserve 165-test/8-layer/23-runtime closed receipt semantics, updating only
  the source-bound expected census for genuinely added tests.
- Qualify the exact final integrated tree and recompute the complete manifest
  and inventory tuple.
