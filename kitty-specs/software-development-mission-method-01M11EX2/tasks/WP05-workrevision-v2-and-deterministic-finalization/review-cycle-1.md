---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T23:58:01Z'
reviewer_agent: user
wp_id: WP05
---

# WP05 Review Feedback — Round 1

Verdict: changes requested.

## Issue 1 — A corrupt replay bypasses the persistence contract (blocker)

`FinalizationService.Finalize` returns a `ReplayFinalization(found=true)` value
at `internal/mission/finalization.go:114-120` without applying
`validateFinalizationPostconditions`. The same service does validate the value
returned by `PublishGenerationOne`, so replay currently trusts a weaker
persistence boundary than initial publication.

An adversarial store was seeded with a same-operation, same-request-digest
record and then independently substituted with each of the following:

- a different/zero `RevisionV2`;
- a different mission identity;
- an incorrect current-revision pointer.

All three replay calls returned success and crossed no provider calls. This can
reassert corrupt or cross-mission durable authority as a successful idempotent
result, contrary to T027/T028's complete committed-generation replay contract.

Remediation evidence required:

- validate every found replay against the command and the durable revision it
  purports to represent before returning success;
- return the existing bounded `persistence_contract` / `repair_persistence`
  outcome for a corrupt replay, without observing the provider;
- retain the replay-before-provider law and add exact revision, mission,
  current-pointer, and state-version substitution mutants.

## Issue 2 — The v2 decoder is not schema-exact for scalar nullability and required booleans (blocker)

The published schema requires `criterion.required` and declares it strictly
boolean; optional `design_context` and `predecessor_id` are string-valued when
present. `validateV2JSONShape` at
`internal/workgraph/revision_v2.go:384-441` checks collection/object shapes but
does not check scalar presence or nullability. Go decoding then collapses
missing or `null` scalar values into zero values.

A valid revision fixture deliberately used `required=false`, empty design
context, and generation-1 empty predecessor. Each of these independent wire
mutants decoded successfully:

- replace `"required":false` with `"required":null`;
- remove `required` entirely;
- add `"design_context":null`;
- add `"predecessor_id":null`.

These mutations preserve the reconstructed semantic zero values, so digest
recomputation cannot detect them. This violates the schema at
`contracts/work-revision-v2.schema.json:7-15,33-56,95-100` and T023/T024's
strict authority-boundary requirement.

Remediation evidence required:

- enforce required-field presence and the exact JSON type/nullability of every
  v2 field recursively, not only arrays and metadata objects;
- add false-valued required controls so digest mismatch cannot accidentally
  stand in for schema validation;
- cover missing, explicit-null, wrong-scalar-type, duplicate, unknown, and
  trailing-value mutants at nested boundaries.

## Issue 3 — The frozen v1 decoder accepts a non-characterized nested shape (blocker)

`requireLegacyEnvelopeShape` at `internal/workgraph/revision.go:306-320`
checks only the seven top-level field names. It does not enforce the exact
nested graph/lane/work/criterion/source-revision shape characterized by the
golden bytes.

Removing the required `"dependencies":[]` member from the first work item in
the frozen golden v1 value still decodes successfully. The restored value then
re-encodes with the member reintroduced, so accepted historical input is not
byte-identical on decode/encode. This contradicts T023 steps 1-4 and the
load-bearing promise that only exact characterized unversioned envelopes route
to v1.

Remediation evidence required:

- validate the complete nested legacy envelope shape before dispatch, including
  presence and exact JSON types of lanes, work items, criteria, dependencies,
  and source revisions;
- add missing nested-field, null, wrong-type, unknown, and duplicate-key
  mutants while retaining the exact golden byte round trip;
- do not normalize malformed or partial unversioned input into canonical v1.

## Issue 4 — A locally unsupported mission-definition version crosses both persistence and provider ports (high)

`validateFinalizationCommand` at
`internal/mission/finalization.go:150-184` validates the definition ID and
commitment but not the only supported definition version. The v2 constructor
later enforces `Version == 1` at
`internal/workgraph/revision_v2.go:223-224`, after replay lookup and the first
provider observation.

With an otherwise valid command and `ExpectedMission.DefinitionVersion = 2`,
the service made one replay-store call and one provider-observation call before
returning invalid input. Short and uppercase planning-source commits correctly
made zero port calls, demonstrating the intended local refusal boundary.

This violates T027's local validation ordering and the established WP04 law
that locally detectable command/mapping defects are refused before provider
preparation or mutation.

Remediation evidence required:

- validate the exact supported mission-definition key/version/commitment in
  `validateFinalizationCommand` before request replay or observation;
- add zero-replay, zero-provider, and zero-publish assertions for unsupported
  definition versions alongside the existing planning-commit controls.

## Issue 5 — Provider mapping identity is not one-to-one (high)

Both `NewFinalizationObservation` at
`internal/workgraph/finalization.go:36-77` and
`validateAndCloneProviderObservations` at
`internal/workgraph/revision_v2.go:653-684` enforce uniqueness only by
`WorkID`. Neither rejects two different work IDs bound to the same
`(provider, external_id)` identity.

Reviewer probes made `work-a` and `work-b` share the same provider and external
ID. Both the raw finalization observation and `NewRevisionV2` accepted the
ambiguous mapping. That fails T024 step 4's exact complete mapping law: one
external ticket cannot provide stable independent identity for two executable
work items.

Remediation evidence required:

- reject duplicate `(provider, external_id)` mapping identities in both raw
  observation construction and revision restoration/construction;
- preserve the existing exactly-once WorkID coverage checks;
- add positive controls showing that the same opaque revision across distinct
  external IDs remains legal, because revision is audit evidence rather than
  mapping identity.

## Validation evidence

The implementation's desirable contracts remain green and should be preserved:

- `go test ./internal/workgraph ./internal/mission -count=1` — pass;
- the same focused packages at `-count=10` — pass;
- `go test -race ./internal/workgraph ./internal/mission -count=1` — pass;
- concurrent same- and distinct-operation mission tests under race at
  `-count=10` — pass;
- `go test ./internal/architecture -count=1` — pass;
- the permanent Phase-3 direction campaign, including exact
  workgraph-to-tracker denial, mission-to-tracker positive control, and deletion
  mutant, at `-count=10` — pass;
- repository-wide internal compile-only test — pass;
- `go vet`, `staticcheck`, `go mod verify`, formatting, and diff hygiene — pass.

The full-scenario exception is independently non-attributable. Product commit
`16b938a` changes no `internal/scenario` file, and the directory tree object is
byte-identical before and after the commit
(`cf52671e1bbbead61b728bf110d77847424e4e12`). The known exact-census failure
reproduces, while isolated
`TestPhase1NamedFaultBoundaryMatrix/integration/IG-02` passes in 14.31 seconds.

Product scope is exact: commit `16b938a` changes only the ten WP05-owned files.
Topology commit `6c51da7` contains planning/status evidence separately. No
reviewer probe remains in the tree.

## Anti-pattern checklist

1. Dead code: **PASS** — mission finalization is a real production caller of
   workgraph candidate construction; the store remains an intentional WP08
   forward port.
2. Synthetic-fixture test: **FAIL** — the happy fake store masks corrupt replay
   results, and normal fixtures do not exercise zero-valued schema ambiguity or
   duplicate provider mapping identity.
3. Silent empty return: **PASS** — no new silent empty-return failure path was
   found.
4. FR coverage: **FAIL** — FR-012/FR-013/FR-014 strict identity and replay laws
   are incomplete at the five boundaries above.
5. Frozen surface: **FAIL** — the frozen v1 surface accepts a nested shape that
   is outside its byte-characterized envelope.
6. Locked decision: **PASS** — generation-1 authority, opaque-revision
   separation, double observation, and provider-neutral package direction stay
   aligned with the plan.
7. Shared-file ownership: **PASS** — the product delta is exactly the ten owned
   files, with topology cleanup separate.
8. Production fragility: **PASS** — no panic/fail-fast transient path was
   introduced; errors are typed and provider output remains bounded/non-leaking.
