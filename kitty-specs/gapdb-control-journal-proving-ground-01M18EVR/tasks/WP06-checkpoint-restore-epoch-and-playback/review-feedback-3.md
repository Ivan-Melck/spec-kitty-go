---
affected_files:
  - internal/controljournal/model.go
  - internal/architecture/m1_dependency_guard_test.go
  - internal/controljournal/authority.go
  - internal/controljournal/authority_test.go
  - internal/controljournal/checkpoint/model.go
  - internal/controljournal/checkpoint/checkpoint.go
  - internal/controljournal/checkpoint/checkpoint_test.go
cycle_number: 3
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command: go test ./internal/controljournal/... ./internal/architecture -count=1
reviewed_at: '2026-08-30T16:47:49Z'
reviewer_agent: reviewer-renata
wp_id: WP06
---

# WP06 review cycle 2 — changes requested

Reviewer: `reviewer-renata`

Implementation: `ee6b05f` (plus prerequisites `5222ec8`, `3f082d2`, and
`3ab764e`)

## Verdict

REJECT. The repair substantially closes all eight prior behavioral findings,
and the repeated, race, architecture-mutant, static-analysis, formatting, and
bounded-fuzz checks pass. Two frozen-contract violations remain: the repair
changes the exact journal authority surface without ratification, and its new
wire codecs do not implement the contract's canonical JSON law.

## Blocking findings

### 1. The linear cursor silently changes an explicitly frozen authority seam

`contracts/control-journal-v1.md:104-155` says the listed Go validation
capability is a frozen architecture seam, lists the exact public methods on
`ValidatedJournalV1`, and states that only `ValidateJournalV1` may produce a
usable capability. Commit `3ab764e` adds exported `JournalCursorV1`, adds
`ValidatedJournalV1.CursorV1`, and makes that method mint a second usable
authority-bearing traversal (`internal/controljournal/model.go:506-519,
634-662`). It then changes the guard itself to declare the new surface exact
(`internal/architecture/m1_dependency_guard_test.go:1148-1173`) without changing
or ratifying the frozen contract.

The cursor's runtime design is defensible: it is sealed, copy-on-access, linear,
and its shape/producers have useful mutant controls. The defect is governance,
not the performance goal. A test cannot amend the contract it is supposed to
enforce. As written, the architecture test now proves conformance to a new
unratified surface while the frozen contract still specifies the old one.

Remediation:

- Remove `JournalCursorV1`, `journalCursorSealV1`, `CursorV1`, and their guard
  exceptions/fixtures. Restore checkpoint, reducer, and playback consumers to
  the frozen `Len`/`At`-only surface. The present M1 bounds make the resulting
  repeated verification cost finite; performance optimization cannot amend a
  frozen authority contract inside this WP.
- Restore a mutant control proving that any alternate exported capability,
  producer, or accessor beyond the frozen `ValidateJournalV1` plus `Len`/`At`
  surface fails the architecture guard.

### 2. Checkpoint and authority bytes are stable but not canonical under the frozen JSON law

`contracts/control-journal-v1.md:9-17` requires every JSON object to have
lexicographically ordered keys with no insignificant whitespace. The new
checkpoint encoder marshals `checkpointWireV1` in declaration order beginning
with `schema_version`, `checkpoint_id`, and `reducer_set_digest`
(`internal/controljournal/checkpoint/model.go:123-138`; the marshal occurs in
`canonicalCheckpoint`). That order is not lexicographic: for example,
`canonical_projection_digest` must precede `checkpoint_id`, and
`schema_version` belongs near the end. The authority encoder has the same
problem: `authorityCapsuleWireV1` begins with `schema_version`, `project_id`,
and `mission_id` and is marshaled directly
(`internal/controljournal/authority.go:27-40,69-86`).

The authority decoder is also not strict. `DecodeAuthorityCapsuleV1` calls
`decodeRequiredObject`, which canonicalizes whitespace and key order, then
decodes the normalized bytes without comparing them to the supplied bytes
(`internal/controljournal/authority.go:89-111`). It therefore accepts wire
forms that the contract says are noncanonical. The current round-trip test
only proves stability of the encoder's own noncanonical order and does not test
lexicographic order or refusal of reordered/whitespace-bearing inputs
(`internal/controljournal/authority_test.go:37-67`).

This leaves prior finding 7 only partially closed: the root type now has the
correct thirteen semantic fields, but it does not round-trip the frozen wire
contract canonically. It also leaves the closed field-set mirror detached from
the schema: `TestAuthorityCapsuleFrozenThirteenFieldContract` hard-codes Go
field names but never reads `authority-capsule-v1.schema.json`, so schema/runtime
drift can make both existing tests pass.

Remediation:

- Encode checkpoint and authority objects with the same strict canonical JSON
  mechanism used by the journal contract, including lexicographic ordering at
  every object depth.
- Require decode input to be byte-equal to the canonical re-encoding; the
  current contract requires refusal rather than normalization.
- Add mutants for top-level reordered keys, insignificant whitespace,
  duplicate keys, trailing JSON, and nested projection/state ordering.
- Add a schema-backed closed-set mirror that compares the authority schema's
  exact required/property names to the runtime wire field set; do not mirror
  one test-local hard-coded list against another.

## Prior blocker disposition

1. **Pristine restore / first epoch — PASS.** A truly empty origin restores as
   ready/pristine with admission closed; first binding requires no predecessor
   and opens only for a newly supplied admitted epoch.
2. **All-invalid checkpoint fallback — PASS.** Invalid candidates are followed
   by one bounded origin replay; known candidates plus empty origin remain
   closed. Tests distinguish successful intact history from refusal.
3. **Structural zero-I/O playback — PASS.** Reducers are a closed declarative
   program set; no callback/interface/function authority is representable, and
   the architecture mutants reject executable fields and constructor inputs.
4. **Sealed checkpoint publication — PASS.** `BuildV1` mints a private sealed,
   copy-on-access capability tied to the exact forming operation. The large
   prefix/suffix test now uses the real build path and scans only the suffix.
5. **Actual registry digests — PASS.** Build and restore derive and compare
   digests from the sealed event and reducer registries before reads/readiness.
6. **8 MiB page / 48 MiB total — PASS.** Independent exact and plus-one bounds
   are enforced before event publication, alongside record/event/page limits.
7. **Canonical 13-field authority capsule — FAIL (finding 2).** The semantic
   field set is correct; canonical wire behavior and a schema-backed mirror are
   not.
8. **Quantitative campaigns — PASS for the WP06 seam.** The suite runs 100
   genuine restore-versus-playback comparisons, 100 production epoch bindings
   with predecessor refusal, and valid 2,001-record multi-page restore both
   normally and after a stale mid-traversal restart. Full owner durability is
   correctly deferred to the WP05/WP09 composed campaign.

## Additional scrutiny

- **Checkpoint current-batch exclusion — PASS.** The forming OperationID is
  rejected anywhere in the sealed prior prefix and is also bound into the
  publication seal/accessor.
- **No prefix I/O — PASS.** The 16,001-event checkpoint is Build-derived; restore
  opens one scan at its exact watermark and observes only one suffix event.
- **Stale accounting — PASS.** A stale cursor discards partial traversal,
  restarts from the same explicit watermark, reports the completed restart
  count, and refuses the third restart.
- **Strict codecs — FAIL for the new wire surfaces (finding 2).** Digest,
  unknown-field, malformed, and tamper checks are present, but lexical
  canonicality is missing.

## Contract round-trip disposition

- `checkpoint-v1.schema.json`: in scope; field semantics and bounds are present,
  but canonical top-level bytes violate the journal contract.
- `control-journal-v1.md`: in scope; recovery precedence and zero-I/O behavior
  pass, while canonical JSON and the frozen Go validation surface fail.
- `authority-capsule-v1.schema.json`: in scope; thirteen required semantic
  fields pass, but canonical bytes and schema/runtime closed-set mirroring fail.
- `runtime-current-v1.schema.json`: in scope; optional predecessor semantics
  now correctly support first admission.
- Other mission contracts are orthogonal to WP06.

## Anti-pattern checklist

1. **Dead code — N/A (staged foundation).** WP07/WP09 are declared consumers;
   the WP05 application integration remains a downstream closure condition.
2. **Synthetic fixture — PASS.** The prior synthetic large checkpoint was
   replaced by the production `BuildV1` path. Quantitative restore/playback
   trials execute production seams.
3. **Silent empty return — PASS.** Empty success is explicit only for a proven
   pristine journal and remains command-closed until epoch admission.
4. **FR/NFR coverage — FAIL.** Canonical bytes are part of deterministic
   checkpoint/capsule behavior and are not covered correctly.
5. **Frozen surface — FAIL.** Finding 1 changes a contract-declared frozen
   public capability by changing its enforcement test.
6. **Locked decision — FAIL.** Findings 1 and 2 contradict explicit MUST/exact
   contract laws.
7. **Shared-file ownership — PASS with coordination caveat.** The root journal
   and architecture-guard prerequisite changes were explicitly included in
   this review handoff, but they still require contract ratification.
8. **Production fragility — PASS.** No panic/bare-raise equivalent or silent
   partial-readiness path was added.

## Verification evidence

Passed:

- `go test ./internal/controljournal/... ./internal/architecture -count=1`
- `go test ./internal/controljournal/checkpoint ./internal/controljournal/playback ./internal/controljournal/reducer -count=10`
- `go test -race ./internal/controljournal/... -count=2`
- `go test ./internal/architecture -run 'M1.*(Dependency|Reducer|Budget|Contract)|M1Reducer' -count=5`
- `go vet ./internal/controljournal/... ./internal/architecture`
- `staticcheck ./internal/controljournal/... ./internal/architecture`
- `gofmt -l internal/controljournal internal/architecture` (empty)
- `git diff --check kitty/mission-gapdb-control-journal-proving-ground-01M18EVR..HEAD`
- `FuzzCheckpointDecodeBounded`, 5 seconds, 203,572 executions
- `FuzzRestoreOrderingBounded`, 5 seconds, 29,471 executions

Downstream impact: WP07 and WP09 depend on WP06. They must refresh from the
corrected WP06 lane after re-review. WP05 must not consume the checkpoint
publication capability until this contract review is resolved.

## Provenance caveat

The governed claim was made with `--agent reviewer-renata`, and the CLI reported
that reviewer identity, but the generated prompt's resolved identity block
still says `implementer-ivan` / `unknown-model`. The verdict is independently
performed under the resolver-loaded `reviewer-renata` profile; the prompt
identity mismatch should be preserved as workflow provenance evidence.
