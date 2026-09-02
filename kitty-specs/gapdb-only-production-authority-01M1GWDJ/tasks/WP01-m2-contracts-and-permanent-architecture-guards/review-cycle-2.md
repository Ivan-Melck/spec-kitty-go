---
affected_files: []
cycle_number: 2
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T13:35:56Z'
reviewer_agent: user
wp_id: WP01
---

# WP01 Cycle-2 Review Feedback — reviewer-renata

## Verdict

Reject to `planned`. Commit `2da72f2` closes the five direct cycle-1 reproductions, and the focused, race, architecture, vet, staticcheck, and diff gates pass. However, adversarial variants still demonstrate six production-relevant fail-open paths in the permanent M2 guards.

## Confirmed cycle-1 fixes

- A new non-exempt SQLite edge is rejected even while the exact frozen legacy SQLite files remain present.
- Direct and composed compile-time eligibility constants with the wrong tuple are rejected.
- Pure journal access to `os`, scenario access to GapDB, and tracker access to execution/Git authority are rejected in the original fixture shapes.
- Event-family validation now rejects a normalized but wrongly owned event tuple.
- The signed M1 adoption tuple remains exact and locally pinned.

These improvements are necessary but not sufficient because the checks below can still be bypassed by equivalent shapes required by the WP.

## Issue 1 — eligibility evidence is globally pooled and activation is fail-open

`inspectM2EligibilityModule` pools all constant values from the complete `workgraph` plus `controlstore/gapdb` reachable closure and accepts the union. A broken authoritative GapDB eligibility seam using the wrong version, contract hash, and shard count was accepted when an unused workgraph file supplied valid decoy constants. The same inspector also returned success for a live key constructed at runtime from a package variable and function concatenation, because no single compile-time constant contained the complete marker and the no-seam branch returns success.

This violates the requirement that every production-relevant eligibility seam be validated independently and that renames, wrappers, or alternate construction cannot make the guard disappear.

Required fix:

- Discover production eligibility seams from typed callable/key-codec ownership rather than from a global substring census.
- Associate each seam with its own version, contract-hash, shard-count, and full M1 tuple evidence; never satisfy one seam using constants from another file/package.
- Fail closed when an existing production seam cannot be classified or its key is assembled through variables, helper calls, wrappers, aliases, or embeddings.
- Add executable mutants for broken authoritative seam plus valid unused decoy in both packages and lexical orders, multiple clean seams, runtime-built keys, renamed functions/types, and wrapper-based construction.

## Issue 2 — canonical keyspace paths are self-anchored

The keyspace validator derives its expected rows from `m2KeyspaceRegistry()` itself. Only the list of IDs is independently fixed. Changing `K-CHECKPOINT-SLOT` from `spk/v2/checkpoints/slots/<slot-id>` to the semantically different `spk/v2/snapshots/slots/<slot-id>` left the focused keyspace, mutant-census, and evidence-census tests green.

Required fix:

- Compare the registry to an independent exact contract anchor (typed fixture, separately digested normative artifact, or equivalent non-self-derived evidence).
- Bind every canonical base path, scope, suffix/version rule, and budget independently of the production registry constructor.
- Add one mutation control per canonical path and prove deletion, substitution, and plausible prefix renaming are rejected.

## Issue 3 — raw-key/god-store APIs have no active semantic guard

An actual production file under `internal/mission` declaring `Get(string) ([]byte, error)` and `Put(string, []byte) error` passed `TestM2CurrentRepositoryActivatesCompleteGuard`. The current repository guard therefore does not enforce the locked prohibition on caller-controlled raw production keys and generic god-store repositories. Equivalent table/row/query vocabulary is likewise not covered by an active typed census.

Required fix:

- Add repository-wide typed/source inspection for production capability ports and adapters, including interfaces, functions, aliases, embedding, generics, wrappers, generated files, and build-tag variants.
- Reject caller-controlled raw keys and generic storage vocabulary at the consumer/provider boundary while retaining explicit legal aggregate-owned ports as positive controls.
- Exercise this rule against the real repository and named mutants, not only synthetic graph edges.

## Issue 4 — the allowed GapDB adapter can import provider-private server internals

The dependency evaluator accepted `internal/controlstore/gapdb -> github.com/spec-kitty/gapdb/internal/server`. The exact caller allowlist is applied to the whole GapDB module prefix, so a legal adapter can cross the explicit server-internals prohibition.

Required fix:

- Reject GapDB private/internal server namespaces before applying the public-provider allowance.
- Restrict the adapter to public pinned provider APIs and keep qualification-only exceptions separately owned and non-production.
- Add an exact allowed-adapter/private-server mutant and a public API positive control.

## Issue 5 — executable mutant census allows semantic substitution

`validateM2ExecutableMutants` binds only each mutant's ID and declared rule into the digest, then checks that `Run` is non-nil. Replacing a registered mutant callback with `func(*testing.T) string { return wantRule }` is accepted, and the runner passes because it trusts the callback's self-reported rule. Deletion is detected, but substitution with a non-executing/self-reporting mutant is not.

Required fix:

- Bind each mutant ID to independently anchored fixture construction, expected structured violation identity, and the validator actually invoked.
- Have the harness observe a violation produced by the real repository validator instead of accepting a rule string returned by the mutant callback.
- Add a mutant-of-the-mutant control that replaces, swaps, or no-ops a callback and must fail the census/execution gate.

## Issue 6 — unified permanent proof remains incomplete

Issues 1–5 mean the unified census can remain green after substituting a canonical key path, hiding a broken eligibility seam with decoy evidence, introducing a generic raw-key port, importing GapDB private server code, or replacing an executable mutant with self-reported success. The evidence therefore does not yet prove deletion/substitution sensitivity across all locked surfaces.

Required fix:

- Extend the independently anchored census to cover each semantic validator, exact registry datum, and executable mutant identity/fixture/expected violation.
- Re-run deletion and substitution mutations for every newly added rule, not merely row-count or digest deletion checks.

## Anti-pattern checklist

1. Dead code or backwards-compatibility shims: N/A; WP01 is test/guard-only.
2. Tests that only exercise synthetic fixtures: **FAIL**; the mutant callback can self-report, and a real production raw-key seam is not inspected.
3. Silent exception swallowing: **PASS**, but the explicit no-eligibility-seam success path is itself the fail-open behavior in Issue 1.
4. Requirements not demonstrated by tests: **FAIL** for the locked eligibility, keyspace, provider-private, raw-key, and deletion/substitution guarantees.
5. Frozen architecture contradicted: **PASS** for the exact M1 tuple and event-family tuple ownership exercised in this cycle.
6. Locked decisions weakened: **FAIL** for provider-private imports, generic raw-key storage, and independently canonical keyspace/eligibility ownership.
7. Shared ownership boundaries violated: **PASS**; the implementation diff remains within the four WP01-owned architecture-test files.
8. Fragile runtime/UI selectors: N/A.

## Verification evidence

Passing baseline gates:

- `go test ./internal/architecture -run 'TestM2' -count=3`
- `go test -race ./internal/architecture -run 'TestM2' -count=1`
- `go test ./internal/architecture -count=1`
- `go vet ./internal/architecture`
- `staticcheck ./internal/architecture`
- `git diff --check`

Adversarial probes reproduced the six issues above. Every temporary production file, synthetic fixture, and source mutation was restored before this verdict; no implementation code was modified or committed by the reviewer.
