# WP12 Review Feedback — Cycle 1

**Verdict:** Rejected to `planned`

**Reviewed implementation:** `8a75e952` (merged lane state through `15d5b8de`)

The package is thoughtfully isolated and its existing suite passes, but three deterministic probes show that the classifier can authorize initialization or recovery when the inspected root is not demonstrably safe. Those outcomes violate the locked fail-closed and zero-mutation-precondition contract.

## Blocking findings

### 1. A post-enumeration insertion is classified as an empty supported root

`Classifier.walk` validates a directory only immediately before and after `ReadDirectory`; it does not revalidate the root and visited directories after their children have been inspected. A deterministic `Source` probe inserted a valid SQLite `control.db` immediately after the root directory listing returned. `Classify` reported `empty_supported` with the `initialize` action and no findings.

This is a TOCTOU authorization defect: a historical authority can appear after enumeration yet before the caller performs the authorized startup mutation.

**Required repair:** bind traversal to no-follow identities and terminally revalidate the root and every visited directory. Any insertion, removal, rename, identity change, or symlink substitution during classification must yield a bounded ambiguous/unsafe refusal. Add deterministic root and nested-directory mutation probes, including rename-to-symlink substitution.

### 2. Directories disappear from the observation and can conceal marker/type conflicts

The directory branch recurses without recording the directory as an observed entry. Static probes using empty directories named `IDENTITY`, `control.db`, and `unknown-authority` all returned `empty_supported`/`initialize` with no findings.

That loses both type evidence and unknown material from the canonical observation. A marker with the wrong object type must not look absent, and an unknown non-allowlisted directory must not authorize initialization.

**Required repair:** include every encountered entry—including directories and special types—in the bounded canonical observation and digest. Required markers with the wrong type must classify malformed/unsafe; unknown material must classify ambiguous according to the closed precedence policy. Add exact wrong-type and unknown-directory fixtures.

### 3. GapDB current-layout validation is too shallow to establish a coherent recoverable root

`validateGapSnapshotHeader` accepts a 64-byte header without validating the snapshot body/trailer, while `decodeGapManifest` does not authenticate the manifest's `snapshot_sha256` against the referenced snapshot. Two deterministic mutants were therefore classified `current_supported`/`recover`:

- the referenced snapshot was truncated to exactly its 64-byte header;
- the manifest's snapshot digest was replaced with 64 `f` characters and its CRC was recomputed, while the snapshot remained unchanged.

The pinned GapDB v1 format requires the complete snapshot shape and trailer digest, the manifest-to-snapshot digest binding, and a WAL start revision consecutive with the snapshot revision. Header-name agreement alone is not a coherent storage generation.

**Required repair:** independently validate the admitted GapDB v1 on-disk contract without executing GapDB or database code: canonical manifest representation, complete snapshot size/count/payload/trailer consistency, manifest digest binding, and snapshot/WAL revision continuity. If the inspection budget cannot establish those facts, return bounded ambiguity/refusal rather than `recover`. Add a mutant for each binding.

## Evidence-strengthening requirement

The current census binds mutant identifiers, but it does not bind the classification fixture matrix or mutant construction/expected outcomes. Removing a matrix case or changing a mutant's behavior can leave the census green. Extend the stable evidence material to cover exact fixture identities, construction material, expected classification/action/detail, and mutant semantics, with deletion/substitution controls.

## Verified strengths and gates

- The package uses a read-only inspection seam and imports no SQL, SQLite driver, GapDB runtime, subprocess, importer, fallback, or migration executor.
- Existing symlink, ownership, permission, special-file, device, hard-link, path-bound, budget, cancellation, precedence, and zero-mutation tests pass.
- The approved WP01 registry and ordinary architecture/helper gates pass on the combined tree.
- Focused repeated tests, package race tests, `go vet`, `staticcheck`, compile-only repository tests, formatting, and `git diff --check` pass.
- Review probes were transient and removed after execution.

## Anti-pattern assessment

1. Dead production scaffolding: not applicable; WP12 intentionally stages the classifier for WP13 integration.
2. Synthetic-only proof: production entry points are exercised, but the missing adversarial cases above are material.
3. Silent empty fallback: no general silent fallback found; the directory omission produces the equivalent unsafe result and is blocking.
4. Requirement coverage: failed for concurrent change, wrong-type/unknown directories, and complete GapDB coherence.
5. Frozen/generated behavior: no improper generated artifact edit found.
6. Locked decisions: failed because uncertain roots can receive `initialize` or `recover`.
7. Shared ownership: no ownership violation found.
8. Production-path fragility: no integration edit was in WP12 scope; the classifier defects must be corrected before WP13 consumes it.
