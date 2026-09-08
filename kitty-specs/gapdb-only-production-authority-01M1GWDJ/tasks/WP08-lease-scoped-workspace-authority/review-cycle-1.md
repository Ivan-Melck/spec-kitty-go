---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T18:02:28Z'
reviewer_agent: user
wp_id: WP08
---

# WP08 review feedback — cycle 1

Candidate `6ec1f7243b1d5390f1b6195488d4bf05e44d089a` (tree
`c71a389589ad220cc79a8b69a27133d5ac67cea7`) is rejected pending these
contract repairs:

1. `ObserveWorkspace` trusts one aggregate-head `AuthoritySummary` instead of
   deriving authority from WP04 checkpoint-plus-suffix/bounded-origin restore.
   Add real restore-backed observation/evidence and verify restored state
   against the exact current head metadata before returning it.
2. `AuthorityCapsuleV2` and `ValidateCurrentV2` omit the complete immutable
   repository binding. Include and defensively compare at least `LaneID`,
   managed root, owned branch/ref, pinned base, and the exact dependency set in
   addition to the existing coordinates.
3. The new authority graph has no non-test composition or filesystem/Git
   preparation adapter. Supply the production adapter/composition seam required
   by WP08 without bypassing the planned WP13 single-owner host composition;
   if those phase responsibilities conflict, ratify the narrow boundary before
   editing rather than creating a second composition root.
4. The controlled-mutant manifest has 17 entries but the governed list has 18.
   Add the missing host-scope-to-work-scope mutation and ensure every listed
   entry exercises a production decision with a passing control rather than a
   tautological assertion of the correct path.
5. The real-Git zombie test begins from literal quarantined/ready states. Drive
   the mandatory chronology through the production GapDB bind,
   expiry-plus-quarantine, replacement/fresh binding, restore-backed
   observation, and current-authority paths; retain real concurrent commits in
   W1 and W2 for at least 100 trials.

Preserve the already-green assertion-budget repair, atomic CAS campaigns,
staged intent/action/observation chronology, and all existing qualification
gates. Reviewer identity: `mill`; implementer identity: `gibbs`.
