# Separate expiry-image Host binding — investigation, NOT permission to implement

**No new Host qualification, admission, launch lane or receipt is implemented.**
The already-approved separate-image adapter experiment is not being re-proposed
for user approval. Its review/execution evidence remains valid only for its
own scope. This record identifies the next technical design work, not a reviewed
implementation handoff or a new admission authority.

## Actual seams and incompatibilities

Read-only planning was followed by parent inspection of these source paths:

- `internal/productionruntime/composition.go:134-198`: both public construction
  roots select a fixed existing verifier. `start` replaces caller-provided
  `Sidecar.Admission` with that result; the new image cannot be smuggled through
  Config. `AuthorityBindingV2` currently writes historical admitted tuple/image
  constants, not a new-image identity.
- `internal/sidecar/gapdb/qualification_candidate.go`: the technical M2 candidate
  verifier pins its own receipt digest, source/module/image coordinates, byte
  length and non-adopted/non-release state. It is not an arbitrary factory.
- `internal/sidecar/gapdb/provenance.go:240-278,342-360,428-445`: exact bytes and
  executable metadata are checked, copied to sealed memfd, checked again before
  launch, and executed through `/proc/self/fd/3`. FD3 is the sealed executable.
- `internal/sidecar/gapdb/lifecycle.go:264-399`: original lifecycle owns verification,
  process startup/rollback, composite observation, execution-image attribution,
  pidfd/socket/database identity and provenance. Environment is only LANG/TZ.
- `scripts/parity/expiry_evidence/gate.go.txt:24-30`: the already-qualified TEST
  image accepts its inherited gate socket specifically at FD3, initialized before
  database ownership. This conflicts with the lifecycle's executable FD3.
- `internal/productionruntime/recovery_compatibility.go:17-22`: the existing
  qualification boolean also chooses the unwrapped versus legacy recovery reader.
  It cannot safely become an unrelated new-image selector by accident.

Therefore adding a productionruntime `_test.go` fixture or setting Config is
insufficient. Its adapter-only lifetime helper MUST NOT become a Host bypass.
Passing a new digest in the old receipt or changing historical constants would
misattribute authority rather than qualify a new composition.

## Candidate direction requiring exact design and independent review

A compile-time, qualification-only lane could bind one exact new receipt/image,
while leaving both normal roots and existing records unchanged. It must reuse
original verification, sealed execution, attribution, restore, epoch admission,
reconciliation, readiness and observation logic. No generic verifier factory,
caller-selected tuple, injectable provider, callback, unrestricted environment or
ExtraFiles escape is acceptable.

The already-tested image is
`83d5db39db928f62c9725253c53fdb8d1264f1b6e317c94db33aa11944f5905b`.
A separate immutable technical record would need its verified size, complete
base-source/module authentication, exact modified-source inventory/delta,
build/toolchain identity, scope-specific review/log digests and explicit
non-adopted/non-release status. Existing `build.json` is NOT such admission.

A fixed qualification descriptor layout could place gate socket at FD3 and
sealed executable at FD4, executing `/proc/self/fd/4`; this avoids changing the
qualified provider image. It must retain verification, close/rollback ownership,
finite release/error behavior and normal-layout absence in ordinary builds.
This is a design possibility, not approved launch plumbing.

Use a fresh isolated database with accurate qualification binding, never retrofit
an existing admitted database or relabel its authority. Preserve explicit recovery
contract selection; a new qualification state must not silently inherit M2's
boolean semantics. Original source/image/receipt/candidate/history remain intact.

## Unresolved before an implementation handoff

1. **Source provenance representation.** The executable has a reviewed source
   delta; old source-revision/tree fields alone cannot pretend its modified bytes
   are the original unmodified provider tree. Define an exact typed new record and
   provenance relation distinguishing authenticated base source from modified
   source inventory, without widening acceptance of old records. Retain the real
   provider client/module identity separately. This needs more than copying the
   M2 verifier and changing an image digest.
2. **Cross-package bridge and normal-build absence.** A `_test.go` helper in the
   sidecar package is not linked as a dependency's test helper into Host tests.
   Any qualification-tagged bridge must be exactly scoped, own its descriptors,
   preserve encapsulation and expose neither raw provider authority/counts nor a
   generic executable/environment/FD selector. Its complete signatures, lifetime
   and rejection controls need review before edits.
3. **Authority binding.** Specify exactly which new receipt/tuple coordinates are
   retained in the new isolated database and frozen owner composition. Do not
   globally edit historical admitted constants, reinterpret an existing tuple
   digest, or accept a new image under an old binding.
4. **Specific S1 witness.** Host tests must distinguish authenticated S1 count
   inequality from RPC, revision, ownership or lifecycle refusal. The adapter's
   private `countMismatch` bit is intentionally not exposed to productionruntime.
   Any qualification-only proof-stage instrumentation needs narrow review; no
   count/diagnostic escape or public refusal change is authorized.
5. **Actual Host scenario.** Use actual coordinator ACKs, overwritten publication
   rows and independently visited private-chain records through the original owned
   adapter. After inserting the TTL extra, establish the matching projection prefix
   by an ordinary publication/refresh BEFORE observing; otherwise revision mismatch
   is not count-inequality evidence. Prove stable held owner/database/revision,
   explicit release acknowledgment/resumed expiry, EOF behavior and lifecycle /
   substitution negatives. No concurrent snapshot/admin writer in the hold.

The missing gate is an independently reviewed **exact technical binding and then
its execution evidence**, not renewed consent for the separate executable. This
investigation does not satisfy that gate. Complete owned-allocation recipes,
negative/performance/P95 qualification and full-layer acceptance also remain open.
