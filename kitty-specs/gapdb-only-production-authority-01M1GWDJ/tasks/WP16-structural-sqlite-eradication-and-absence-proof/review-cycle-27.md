---
affected_files: []
cycle_number: 27
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T16:15:46Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 27

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

Direct and `command`-wrapped quoted variable targets do not share the same
fail-closed rule:

```sh
#!/bin/bash
"$REVIEW_COMMAND" /tmp/x
```

With `REVIEW_COMMAND=sqlite3` in the runtime environment, Bash executes the
retired command. The proof reports no finding because the direct-command path
only evaluates quoted variables present in its local expansion map; unresolved
environment-provided variables fall through. The equivalent
`command "$REVIEW_COMMAND"` path already returns typed ambiguity.

Centralize command-target resolution. Quoted expansion is scalar, but it is
precise only when its value is exactly known in the modeled state. A quoted
unresolved target is typed `ambiguous-command-expansion` when relevant. Direct,
`command`, and admitted wrapper paths must consume the same resolution result.

## Preserve

- Every relevant unquoted variable command target fails closed.
- Quoted exactly resolved targets remain scalar and precise.
- Unified wrapper grammar and every preceding receipt/runtime/layer invariant.

## Required re-review evidence

- Reject the exact environment-provided direct quoted target above.
- Cover unset, empty, locally resolved, environment-resolved/unknown, direct,
  wrapper, nested, and irrelevant quoted expansion cases.
- Prove all command-target paths use one resolution contract.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
