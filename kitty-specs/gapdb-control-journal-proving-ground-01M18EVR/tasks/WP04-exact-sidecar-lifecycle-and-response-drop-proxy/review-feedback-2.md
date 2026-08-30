---
affected_files:
  - internal/sidecar/gapdb/lifecycle.go
  - internal/sidecar/gapdb/lifecycle_test.go
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reviewed_at: '2026-08-30T14:59:36Z'
reviewer_agent: renata-wp04-cycle2
reviewed_commit: a786447c8f3c87b6b0df8e22b57afc272c350692
wp_id: WP04
---

# WP04 review cycle 2 — changes requested

Reviewer: `renata-wp04-cycle2`  
Implementation: `a786447c8f3c87b6b0df8e22b57afc272c350692`

## Blocking finding

1. **Accepted lifecycle identities can produce evidence forbidden by the frozen schema.** `normalizeLifecycleConfig` checks only that `SupervisorRunID` is nonempty/bounded and that `ProductPolicySHA256` has 64 hex-decodable characters (`lifecycle.go:853-860`). It therefore accepts `SupervisorRunID="invalid run identity"` and an uppercase 64-character hexadecimal policy digest. The frozen provenance schema requires the run identity to match `^[A-Za-z0-9][A-Za-z0-9._:-]*$` and every SHA-256 to match lowercase `^[a-f0-9]{64}$` (`sidecar-provenance-v1.schema.json:129-136`). A temporary reviewer probe demonstrated that both invalid values pass normalization. `Start` can consequently admit a lifecycle whose `Evidence()` cannot conform to the exact frozen evidence contract. This blocks FR-003 and the WP completion requirement for publishable bounded provenance. Validate the exact schema grammar before process creation and add deletion-sensitive negative tests for both values.

Reproduction probe:

```go
config := testLifecycleConfig(t, privateShortRoot(t), "schema-probe")
config.SupervisorRunID = "invalid run identity"
// normalizeLifecycleConfig(&config) currently returns nil.

config = testLifecycleConfig(t, privateShortRoot(t), "schema-probe")
config.ProductPolicySHA256 = strings.Repeat("A", 64)
// normalizeLifecycleConfig(&config) currently returns nil.
```

## Cycle-1 finding disposition

All six prior blockers are closed by behavioral evidence:

1. Sealed `memfd` bytes, command-time revalidation, source mutation, write refusal, and `Attach` executed-image provenance pass; deleting command-time revalidation produces a red test.
2. Each Status/Health/DescribeConfig/closing-Status connection validates peer process and socket identity, including the real GapDB connected endpoint; swaps after each evidence boundary are refused. Deleting connected-endpoint attribution produces a red test.
3. Symlink, foreign-directory, and socket replacements at the runtime path are preserved and cleanup fails closed. Replacing descriptor-safe cleanup with pathname removal produces a red test.
4. Two-to-one mutation results and foreign database IDs never record GJ-02; an exact complete durable response records one cut after one forward with no product bytes, and ordinary follow-up traffic succeeds. Deleting database/cardinality binding produces a red test.
5. Git ancestors, NFS/shared, unknown, and split-FSID storage are refused; the local allowlist, same FSID, and sticky `/tmp` isolation boundary pass. Deleting each topology guard produces a red test.
6. Pidfd success, `ENOSYS`/`EINVAL` observation-only fallback, hard errors, identity drift, no-pidfd signaling refusal, and post-start termination/reaping all pass behaviorally. Deleting identity revalidation, no-pidfd refusal, or child reaping produces a red test.

## Gate evidence

- PASS: exact admitted `gapdbd` rebuilt from the pinned module recipe; SHA-256 `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`, size 5,300,621 bytes.
- PASS: sidecar package with the exact binary, `-count=5`; focused prior-defect selectors, `-count=10`; reviewer probe suite, `-count=3` before removal.
- PASS: sidecar race detector, `-count=3`.
- PASS: sidecar strict-wire fuzz, 218,824 executions; control-journal fuzz, 77,692 executions; GapDB adapter fuzz, 203,199 executions.
- PASS: `go test ./internal/architecture/...`, including M1 guards and mutants.
- PASS: `go test ./internal/controljournal/... ./internal/controlstore/gapdb/...`.
- PASS: repository compile-only census, `go vet ./...`, `staticcheck ./...`, `go mod verify`, module diff, `gofmt`, `git diff --check`, and forbidden-import/source guards.
- PASS: reviewer probes, mutants, temporary binary/data, sockets, and processes were removed; the six implementation files equal reviewed commit `a786447` after restoration.
- Not run by design: the full legacy scenario suite, per WP review guidance.

## WP anti-pattern checklist

1. Dead code — **N/A / staged-integration disposition recorded**: the ratified plan assigns the composition root to `internal/provingground`, and WP05 explicitly consumes WP04 lifecycle endpoints and response-drop proxy. This exception is limited to staged delivery; WP05 must provide the first live production caller.
2. Synthetic-fixture test — **PASS**: the repaired safety paths are exercised against the exact external binary, real Unix connections, filesystem replacements, and syscall seams; deletion mutants prove the assertions depend on production behavior.
3. Silent empty return — **PASS**: no unaccounted silent-empty error path was introduced.
4. FR coverage — **FAIL** for FR-003 until accepted identity values are guaranteed to project into the frozen evidence schema; the other cited requirements pass this cycle.
5. Frozen surface — **PASS**: commit `a786447` changes only the six owned sidecar files and modifies no frozen contract.
6. Locked decision — **FAIL**: admitting evidence identities outside the frozen schema contradicts the exact provenance decision.
7. Shared-file ownership — **PASS**: the implementation commit stays within WP04's six owned files.
8. Production fragility — **N/A**: Go production code adds no language-level `raise`; typed fail-closed errors are used.

Verdict: **REJECT / return WP04 to implementation for the single evidence-grammar blocker.**
