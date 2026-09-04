---
affected_files: []
cycle_number: 2
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T07:52:59Z'
reviewer_agent: codex
wp_id: WP05
---

# WP05 independent review cycle 2: request changes

Reviewed repair: `1db5dd3169b22e5190a3562a0ba15b0ff75e9a75`

The cycle-1 exact-preload blocker and executable-mutant evidence are repaired.

## Blocker: definitive mutation refusal is discarded

When the first provider observation reports absence, `MutateEffect` may return a definitive typed conflict/refusal. If the following observation returns exact-looking effect identity, work identity, value digest, revision, external reference, and operation marker, production currently discards the definitive mutation error and completes.

Introduce a provider-neutral typed mutation outcome/error classification. A definitive refusal or version conflict must seal ambiguity/reconciliation and can never become success. Only an explicitly indeterminate/lost transport response may use exact re-observation for reconciliation. Update the existing lost-response fixture to use the indeterminate classification and add the exact contradictory definitive-conflict regression.
