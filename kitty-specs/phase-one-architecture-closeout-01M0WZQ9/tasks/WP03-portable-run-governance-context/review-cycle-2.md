---
affected_files: []
cycle_number: 2
mission_slug: phase-one-architecture-closeout-01M0WZQ9
reproduction_command:
reviewed_at: '2026-08-25T18:48:10Z'
reviewer_agent: user
wp_id: WP03
---

**Issue 1 — same-operation claim replay can be rebound to a different governance context (FR-010 / T014)**

`internal/execution/lease.go:202-208` leaves a non-empty caller-supplied `CommandDigest` unchanged. The live control-store request identity includes that digest but not the `GovernanceContext` independently. Consequently, two direct `Service.Start` calls can reuse the same operation ID and caller digest while supplying different valid contexts; the repository sees the same request identity, and `bindRunGovernanceContext` accepts a replay capsule with empty governance fields and fills it from the second request. That violates the locked contract that replacement/replay cannot change the run-owned context.

Make the governance context ID and algorithm-qualified aggregate participate in every claim operation identity, including when a broader command digest is already present. On replay, require the repository result to agree with the originally bound context rather than treating absent governance fields as permission to bind the current request. Add a regression that performs an exact replay and then reuses the same operation ID and same pre-populated caller digest with a different context: the exact replay must return the original context, and the changed context must produce an idempotency/conflict diagnostic.

**Issue 2 — replay/replacement coverage does not exercise the behavior it names (T014 / anti-pattern: synthetic-fixture test)**

`TestStartReturnsRunOwnedGovernanceContextOnReplay` marks a literal capsule `Replayed: true`, while `TestReplacementLeaseGenerationRetainsRunGovernanceContext` invokes `bindRunGovernanceContext` on two literal capsules. Neither collaborator persists the first context nor compares it during the second operation, so both tests pass even though the production request-identity path permits the rebind described above. Replace or extend these with a stateful repository collaborator (or the live claim path) that records the first operation/run context and exercises exact replay plus a changed-context replay/replacement.

**Issue 3 — new portable public seams have zero production callers (anti-pattern: dead code)**

Targeted non-test call-site searches find no production use of `RegisterEntryKind`, `CheckIdentityConflict`, `controlstore.GovernanceContextReader`, `ErrGovernanceContextConflict`, or `GovernanceContextConflictError`. The WP checklist explicitly treats zero production hits for new public code as dead code. Remove speculative exported seams until a production consumer exists, narrow them to package-private helpers where appropriate, or wire them into the live claim/conflict boundary without moving SQLite persistence from WP04.

Validation note: the WP-prescribed unit and race commands, helper-command tests, formatting, and vet pass. `go test ./...` currently fails only in WP04-owned SQLite recovery/workgraph fixtures that still carry zero governance contexts; WP04 must update those after rebasing onto the corrected WP03.

Downstream note: WP04 depends on WP03 and must rebase after these corrections land.
