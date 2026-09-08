---
affected_files: []
cycle_number: 11
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T05:26:41Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 11

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

Two equivalent executable-dispatch positions remain outside the model:

```sh
review_retired_cli=sql\ite3
sh -c "$review_retired_cli"
```

The tracked value is command text, but only `eval`/`xargs` are currently treated
as indirect execution.

```sh
review_retired_cli=sql\ite3
/usr/bin/env "$review_retired_cli" /tmp/wp16-review-authority.db
```

Bare `env` is modeled, but path-spelled wrappers are not basename-normalized.

Add a closed, reviewed registry of supported shell command-string/interpreter
positions and utility-dispatch wrappers. Normalize safe literal command paths to
their basename before classification. Any tracked value reaching an indirect
command-text or unclassified executable slot must fail closed.

## Preserve

- Case actions, reserved/list/pipeline/grouping command positions, and existing
  command/exec/env/nohup/nice/time wrapper behavior.
- Static word resolution, ambiguity handling, comments, budgets, and all prior
  negative controls.
- Sidecar admission, 23 observed runtime receipts, closed 35-artifact inventory,
  exact source identity, and every per-class digest.
- All architecture/race/parity/build/tidy/vet/static evidence.

## Required re-review evidence

- Kill exact `sh -c "$variable"` and `/usr/bin/env "$variable"` attacks.
- Exercise admitted interpreter/wrapper basenames with absolute paths, options,
  and nesting; distinguish ordinary data arguments from command text.
- Unknown path-spelled dispatch involving a tracked value must fail ambiguity.
- Qualify the exact final integrated tree and recompute observed runtime and the
  complete manifest/inventory tuple.
