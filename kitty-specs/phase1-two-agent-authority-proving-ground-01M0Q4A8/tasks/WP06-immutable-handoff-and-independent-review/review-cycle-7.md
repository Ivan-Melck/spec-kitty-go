---
affected_files: []
cycle_number: 7
mission_slug: phase1-two-agent-authority-proving-ground-01M0Q4A8
reproduction_command:
reviewed_at: '2026-08-25T04:45:48Z'
reviewer_agent: reviewer-renata
wp_id: WP06
---

# WP06 Branch-Movement Preservation — Review Feedback

Candidate `6c5aea3e710175958c8a63c66e34a6ba152654d1` is rejected because replay can
falsely claim that product-owned Git evidence remains preserved.

On an identical OperationID replay, revalidate the stored candidate and
observed evidence refs against exact `C` and `B`. Atomically recreate an absent
ref when its exact object is still available. Never overwrite a ref that points
elsewhere; return a typed decision-required preservation conflict containing
exact observed/expected facts and recovery guidance. Never return preservation
language while Git disagrees.

Add deletion, mutation, partial-ref, replay, reflog-expiry, and `git gc
--prune=now` tests. Replace the scenario's direct SQL lease-expiry update with
the public expiry command/service.

Preserve the integration migration union as real v8, v9, WP07's digest-pinned
v10, then v11 with current schema version 11.
