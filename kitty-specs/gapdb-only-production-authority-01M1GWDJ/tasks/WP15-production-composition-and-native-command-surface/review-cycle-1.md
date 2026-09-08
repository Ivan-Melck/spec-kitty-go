---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T23:15:14Z'
reviewer_agent: user
wp_id: WP15
---

# WP15 review cycle 1 — rejected

Reviewer: Maxwell (independent)

Frozen product/test revision: `35f6dca79d2a0da3de24f92d59c34f6ef0a5a854`

## Blockers

1. **Drive is not full or scope-bound.** The command registry admits only
   mission/readiness operations, the readiness wire omits activity,
   WorkRevision selection, and related-fact inputs, action availability is
   matched only by action string while its exact scope is discarded, and the
   one-action-per-mission wave prevents independent work in one mission.
2. **Live query purity is false.** Status/progress/operation inspection starts
   the production host, which durably admits a new runtime epoch. Current
   black-box tests assert this mutation instead of proving zero authoritative
   events, leases, fences, epochs, or effects.
3. **Real recovery evidence is missing.** Add a real GapDB native-command
   execute -> lost acknowledgement -> process/host restart -> same-request
   resume -> exact committed result proof, plus changed-request OperationID
   conflict, through the live command boundary.
4. **Result and exit contracts are not closed.** Add an explicit result
   discriminator; prevent JSON outcome `stopped` from disagreeing with the
   process exit category.
5. **Architecture smoke is red.** The executable
   `TestBeadsDocumentationLinksMermaidAndCommands` still invokes the retired
   `tracker inspect-beads`. Re-own/reconcile the executable test now; README
   wording may be completed by its downstream WP17 owner.

## Green evidence retained

Focused helper/native tests, helper race, real GapDB status/restart, CGO-off
builds, vet, staticcheck, and owned-code diff checks passed. The native binary
has no SQLite dependency.
