---
affected_files: []
cycle_number: 7
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T16:34:11Z'
reviewer_agent: user
wp_id: WP06
---

# WP06 review cycle 7: changes requested

Candidate `854e4bf` closes most of cycle 6 correctly. Add and remove now have
separate exact object receipts, both exact receipts are rejected as dependency
list records, the fake provider emits the right default receipt shapes, and
optional-field skew is tested separately. `graph.go` and `discussion.go` are
unchanged. Focused x10, helper/census x10, full focused packages, and the
architecture/mutant checks pass.

## Remaining blocker: two constants named as real captures are impossible v1.2.2 output

`realV122DependencyList` is now:

```json
[{"id":"bd-distinct-prerequisite","dependency_type":"blocks"}]
```

That is a useful minimal decoder fixture, but it is not an exact Beads v1.2.2
`dep list` capture. The pinned source type is `IssueWithDependencyMetadata`,
which embeds `Issue`; at minimum `title`, `priority`, `created_at`, and
`updated_at` are always serialized, with normal live records also carrying
status/type/owner provenance. A live pinned-binary capture used during review
contained those fields plus `id` and `dependency_type`.

`realV122ReadyList` has the same problem. It includes empty `description` and
`acceptance_criteria` fields that v1.2.2 omits via `omitempty`, while omitting
always-serialized `priority`, `created_at`, and `updated_at`. It therefore
cannot be byte-real provider output either.

This matters because the reopened WP specifically distinguishes provider
captures from synthetic/skew fixtures. The receipt side is now truthful; the
list/ready side still presents hand-built minimal JSON as captured evidence.

### Required remediation

1. Replace the base dependency-list and ready constants with actual complete
   schema-1 `data` payloads captured from the pinned v1.2.2 binary. Preserve
   every emitted field; do not add or remove fields for convenience.
2. If minimal required-field fixtures are useful, keep them under names that
   explicitly say `minimal` or `synthetic`, separate from the real captures.
3. Continue deriving optional-field skew from the exact base capture, as this
   candidate already does.
4. Keep the now-correct exact add/remove receipt tests, exact `text` comment
   capture, fake defaults, production code, direction/removal/replay controls,
   caller census, and authority boundary unchanged.

## Anti-pattern checklist

1. Dead code: **PASS** — the shipped `coordinate-beads` caller remains live.
2. Synthetic-fixture test: **FAIL** — dependency-list and ready fixtures are
   still synthetic while named and relied upon as real v1.2.2 captures.
3. Silent empty return: **PASS**.
4. FR coverage: **FAIL** — exact provider list/ready evidence remains false,
   although the production behavior and other WP06 coverage are green.
5. Frozen surface: **PASS**.
6. Locked decision: **PASS**.
7. Shared-file ownership: **PASS** — the helper-only delta remains a narrow
   public-caller fixture alignment with explicit commit intent.
8. Production fragility: **N/A** — no production code changed.

WP08 and WP09 depend on WP06; propagate only the corrected exact fixture
evidence, not the synthetic minimal records.
