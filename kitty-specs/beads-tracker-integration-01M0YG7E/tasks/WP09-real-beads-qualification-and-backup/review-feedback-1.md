# WP09 Review Feedback — Cycle 1

Candidate `45a1bf64d8c5784e2840e6773b2dcd341a8d37ed` is not yet admissible. The supplied evidence hash, candidate source commit/tree, provider version/digest, 11/11 arithmetic, latency distributions, and 8-process/1,000-create contention census all match. Focused unit, byte-real embedded, linked-worktree, backup, 100-repeat, race, vet, and architecture/scope checks passed. The following contract failures still block approval.

## 1. Blocker — the passing evidence is invalid against the mission schema

`internal/testcampaign/cmd/beads-qualification/runner.go` emits a top-level `contention` object, and the signed evidence contains it. `contracts/qualification-evidence.schema.json` has `additionalProperties: false` but defines no `contention` property. Therefore `/tmp/go-kitty3-wp09-final-evidence.dfw27b/qualification.json` does not validate against the required schema even though the runner labels it `pass`.

Coordinate the contract change explicitly (WP09 does not own the schema), add the exact bounded contention shape to the schema, and add a load-bearing test that validates the emitted evidence against the checked-in schema. Regenerate qualification evidence only after the candidate tree contains the matching contract.

## 2. Blocker — external-server workspace identity includes a local path

`internal/tracker/beads/binding.go:deriveServerWorkspaceIdentity` hashes `canonicalPath` in addition to host, port, database, and `shared_server=true`. A true external database has no local data-directory identity; two valid local bindings to the exact same qualified external server/database therefore receive different workspace identities solely because their local `.beads` paths differ. This breaks the required server identity tuple and can produce false workspace divergence.

Preserve the existing embedded canonicalization unchanged. For server mode, derive the provider workspace identity only from the exact observed/configured host, port, logical database, and `shared_server` fact. Continue requiring `connection_ok=true`, matching external status, and explicit `dolt.auto-start=false`. Add a mutant/control showing two different local workspace paths converge for the same server tuple while any tuple-field change diverges.

## 3. Blocker — backup/restore qualification proves only a subset of required state

`internal/tracker/beads/backup_integration_test.go` seeds one isolated work item for JSONL and a separate isolated work item for native backup. The JSONL test asserts only record presence and `work_id`. The restore test asserts only title and one criterion ID. It does not seed and verify the required dependency edge and discussion/comment, does not compare the full structured criterion fields, and never runs restored adapter inspection/preflight to prove the recovered workspace identity and binding health.

Build one representative provider-neutral state graph containing mapped works, complete structured criteria, an asymmetric blocking dependency, and both adapter/human discussion. Verify those facts in bounded JSONL interchange and again through a fresh adapter after native restore, including restored inspection/workspace identity. Keep interchange and native recovery reported as distinct capabilities.

## 4. High — the real assignment scenario omits half of the safe-refusal contract and is not repeated

`TestRealBeadsQualification/assignment_safe_refusal` performs one human-overwrite case and proves `attribution_lost`. It never exercises the equally important matching-pre-read case where the provider state still appears attributable but v1.2.2 lacks mutation-time CAS, so compensation must return `safe_compensation_unsupported` without spawning an update. It also does not repeat the real-provider human-change race or record attempted/terminal/preserved/failed counts as required by T047.

Add real-provider coverage for both refusal branches. Run a bounded repeated campaign with exact counts and zero human overwrites; make that census part of admissibility rather than relying on one successful interleaving.

## 5. High — evidence can pass without proving its latency or authority fields

`runQualification` hard-codes `AuthorityInvariantFailures: 0`; no real-provider scenario audits authority state before/after provider actions. The runner also silently ignores malformed `WP09_LATENCY` markers and permits a passing verdict with missing, duplicate, wrong-operation, or wrong-sample-count latency evidence because latency is optional and is not checked by the pass predicate.

Connect the real scenarios to an actual authority audit (or another concrete, production-relevant invariant observation) and derive the evidence count from that result. Require exactly the two expected latency records with sample counts 100 and 200 and enforce their thresholds in the aggregator. Malformed, missing, or duplicate markers must make the campaign non-admissible.

## 6. High — divergent-workspace refusal is not tested through a mutation entry point

`TestRealBeadsWorktreeQualification/shared_identity` calls `spliced.Preflight` directly and compares two list responses. That proves inspection refuses, but not the WP's required claim that a mutating capability refuses before spawning a mutating command. A regression allowing `CreateWork` or another mutation to bypass preflight would leave this test green.

Invoke a real mutation port using the divergent binding, assert `workspace_mismatch`, and use a command spy/provider census plus before/after state to prove no mutating subprocess was spawned and no provider state changed.

## Non-blocking hardening

`scripts/install-qualified-beads.sh` formats the caller-selected absolute destination directly into JSON with `printf`; quotes, backslashes, or control characters in a valid path can produce invalid machine output. JSON-escape or reject unsupported path bytes and add an adversarial destination-path test.

## Anti-pattern checklist

1. Dead code: **PASS**
2. Synthetic-fixture test: **FAIL** — authority evidence is a hard-coded zero rather than an observed invariant result.
3. Silent empty return: **PASS**
4. FR coverage: **FAIL** — complete interchange/restore state and both real compensation refusal branches are not covered.
5. Frozen surface: **PASS**
6. Locked decision: **FAIL** — server identity includes a local path outside the exact external-server tuple; evidence violates the checked-in schema.
7. Shared-file ownership: **PASS** — the cross-WP WP04/WP06 corrections have an explicit commit rationale, though the schema correction now needs explicit coordination.
8. Production fragility: **PASS**
