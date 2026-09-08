---
affected_files: []
cycle_number: 3
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T14:24:09Z'
reviewer_agent: user
wp_id: WP01
---

# WP01 Cycle-3 Review Feedback — reviewer-renata

## Verdict

Reject to `planned` and escalate to arbitration. Commit `32c48eb` closes the cycle-2 reproductions in their original forms, and every required baseline gate passes. Equivalent production-relevant shapes still demonstrate three fail-open guards, while the mutant and terminal evidence anchors do not fully bind the semantics they claim.

## Confirmed improvements

- Cross-file eligibility decoys in both lexical orders, runtime-built keys, wrapper-owned seams, renamed functions/types, and multiple seams in separate files are exercised.
- Canonical keyspace paths now compare against an independently digested typed contract and reject plausible path, scope, mutability, suffix, and budget substitutions.
- Direct generic byte-oriented raw stores, aliases, embedding, generics, generated files, build variants, and wrappers are rejected.
- Root GapDB `internal` and `server` prefixes are denied before public-provider allowances.
- The executable mutant table rejects row deletion, metadata substitution, unknown/no-op validator names, and direct fixture swaps.
- The exact M1 tuple and exact event-family tuples remain correct and locally pinned.

## Issue 1 — eligibility evidence remains file-scoped rather than seam-scoped

`inspectM2EligibilityModule` identifies multiple callables in a file but computes one `m2FileTypedConstantValues` set for the whole file and applies that set to every callable. A file containing a broken authoritative codec with policy version v2 and shard count 128 plus valid, unused v1/64 decoy evidence and an unused valid decoy callable is accepted. The broken and valid seams are independently observable, but the valid constants satisfy both because evidence is associated only with the file.

Reproduction: an isolated `internal/workgraph/codecs.go` with `BrokenCodec.Compose`, `NewBroken` binding the wrong tuple, and a valid unused decoy caused `inspectM2EligibilityModule` to return nil. This is the same masking defect as cycle 2 at a narrower, still reasonable multiple-seam boundary.

Required correction:

- Associate the complete tuple with each discovered callable/codec object and its actual value/dataflow, not with all constants in its file.
- Reject any production-relevant seam whose own reachable construction cannot prove the exact template, contract digest, M1 tuple, policy/hash versions, shard/budget values, and contention evidence.
- Add same-file broken-plus-valid-decoy mutants in both declaration orders, two independently clean seams in one file, and alias/wrapper variants that prove per-object ownership.

## Issue 2 — nested GapDB private paths bypass the prefix guard

The provider denial recognizes only `github.com/spec-kitty/gapdb/internal...` and `github.com/spec-kitty/gapdb/server...`. The exact allowed adapter can import `github.com/spec-kitty/gapdb/gapdb/internal/server` without a violation because the path begins with neither private prefix and the broad public module allowance then accepts it.

Reproduction: a graph edge from `internal/controlstore/gapdb` to `github.com/spec-kitty/gapdb/gapdb/internal/server` produced zero violations.

Required correction:

- Classify any private/internal or server-private namespace beneath the admitted GapDB module before applying caller allowances, with slash-bounded path-segment semantics.
- Add nested and root private-path mutants plus public `gapdb`/`gapdbapi` positive controls.

## Issue 3 — raw-key authority detection is coupled to direct byte payloads

The raw-store inspector recognizes a read only when a string-like key returns a direct byte-like value and a write only when the same owner accepts a direct byte-like value. A generic repository remains caller-keyed if its opaque record wraps the bytes; the storage authority does not become aggregate-owned merely because the payload has a named envelope.

Reproduction: a production capability interface with `Get(string) (Record, error)` and `Put(string, Record) error`, where `Record` contains `Payload []byte`, was accepted by both the typed and all-source paths.

Required correction:

- Detect generic caller-controlled raw-key repository shape independently of the concrete payload representation.
- Distinguish legal aggregate-owned ports by typed aggregate identity and operations, rather than treating every non-direct-byte value as safe.
- Add opaque/named record, pointer record, generic record, and separately declared `Get`/`Put` mutants while retaining the legal `Load(MissionID)`/`Save(MissionID, MissionState)` positive control.

## Issue 4 — some fixture digests do not bind generated mutant semantics

`m2FixtureDigest` hashes only the fixture label for policy, event, and legacy validators. Their actual mutation behavior lives in separate switch statements inside the observer functions. Swapping two event mutations that both produce `EVENT_REGISTRY`, or replacing a policy mutation with another policy-shape violation, preserves the stored fixture digest and expected observation. The census therefore detects metadata edits but not semantic callback substitution for these families.

Required correction:

- Derive each fixture SHA from a canonical serialization of the actual generated mutated policy, registry, graph, or source fixture consumed by the validator.
- Ensure the observer consumes exactly that bound material rather than regenerating behavior from an unbound label switch.
- Add semantic swap/no-op controls within event and policy families, including substitutions that still return the same broad violation code.

## Issue 5 — terminal evidence claims are not execution-bound

`TestM2FocusedEvidenceCensus` executes the mutant table, then compares literal terminal evidence IDs. Entries such as contract-schema compilation, M1 entry tuple, and release archive coordinates are marked `Terminal: true` without the census invoking or receiving a result from their validators. Removing or disabling one of those independent tests does not change the literal terminal registry.

Required correction:

- Build terminal evidence from recorded executions of every claimed validator, including contracts, M1 admission/archive, event tuples, repository dependency/raw-key/eligibility guards, keyspace, and mutants.
- Bind each evidence ID to validator identity and terminal result; reject missing, duplicate, skipped, substituted, or self-declared terminal results.
- Add deletion/substitution controls that remove a validator execution, not only a literal registry row.

## Anti-pattern checklist

1. Dead code: **N/A** — WP01 adds architecture tests, not production APIs.
2. Synthetic-fixture test: **FAIL** — event/policy fixture identities do not bind generated mutation behavior, and terminal claims can exist without validator execution.
3. Silent empty return: **PASS** — no unexplained silent Go return was introduced; the eligibility acceptance is an evidence-association flaw.
4. FR coverage: **FAIL** — FR-031/FR-033 and NFR-014/NFR-015 remain unproven under the reproduced eligibility, raw-key, and provider-private bypasses.
5. Frozen surface: **PASS** — the implementation diff remains confined to the four WP01-owned files; frozen mission contracts and M1 evidence were not modified.
6. Locked decision: **FAIL** — per-seam eligibility ownership, private GapDB denial, raw-key prohibition, and deletion/substitution sensitivity can still be violated while the gate remains green.
7. Shared-file ownership: **PASS** — no implementation file outside the declared ownership surface changed.
8. Production fragility: **N/A** — no production request/worker/CLI panic path was added.

## Verification evidence

Passing gates:

- `go test ./internal/architecture -run 'TestM2' -count=3`
- `go test -race ./internal/architecture -run 'TestM2' -count=1`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/architecture`
- `staticcheck ./internal/architecture`
- `git diff --check`

Historical replay evidence in the unified table passes for the original cycle-1 and cycle-2 forms: legacy SQLite masking, ambient journal imports, broad GapDB callers, tracker authority, exact event tuples, cross-file eligibility decoys, runtime/wrapper/rename cases, canonical key substitutions, direct raw-store forms, root private paths, and metadata deletion/substitution.

The three cycle-3 probes above failed exactly because the guards accepted the forbidden shapes. All temporary fixture files and source mutations were restored before this verdict. The reviewer made no implementation commit; the lane is clean apart from runtime-owned `.spec-kitty/` state.
