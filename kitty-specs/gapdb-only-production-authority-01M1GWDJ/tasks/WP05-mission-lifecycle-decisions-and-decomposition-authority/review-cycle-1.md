---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T07:36:18Z'
reviewer_agent: codex
wp_id: WP05
---

# WP05 independent review: request changes

Reviewed implementation: `c209ea3b3ee89174e8696abec67c6fa5ca773c6f`

## Blocker: unattributed preexisting provider result

A locally fresh operation (`OperationAbsentV2`) can observe a provider object whose deterministic effect ID, child marker, work ID, value digest, revision, and external reference all match, then mark decomposition complete without attempting `MutateEffect`. Exact matching is sufficient for an in-progress replay, but it cannot attribute provider state that predates a fresh local operation. Preserve whether initial resolution was absent; on that path, a result observed before this execution attempts mutation must reconcile or refuse. Add the exact-marker preloaded-provider regression.

## High: mutant campaign is self-certifying

The mandatory mutant matrix currently verifies string labels rather than executing changed behavior. Replace it with controlled behavioral seams or overlays. Each mandatory mutant needs a passing production control, a mutated execution, and an assertion for the exact typed refusal or invariant failure.
