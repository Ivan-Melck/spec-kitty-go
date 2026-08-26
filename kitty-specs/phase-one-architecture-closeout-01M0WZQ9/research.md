# Research: Phase One Architecture Closeout

## Decision 1: Convert the scope guard into a directional capability policy

**Decision**: Classify production packages by capability role and enforce allowed local/external import edges. Unknown production roots fail closed.

**Rationale**: The existing flat allowlist protects Phase 1 only by forbidding every future integration name. Phase 2 would force its deletion. A directional policy permits provider code only in adapter packages while retaining the stronger kernel prohibition.

**Alternatives considered**:

- Keep extending the Phase 1 allowlist: rejected because it cannot distinguish a valid adapter from authority leakage.
- Adopt the donor port's horizontal directory architecture: rejected because this redesign is capability-oriented.
- Rely on review convention: rejected because the current mechanically enforced boundary is a product advantage.

## Decision 2: Implement architecture checks in Go

**Decision**: Use Go package metadata and syntax trees from a Go test/helper, with temporary-module fixtures for mutations.

**Rationale**: The donor port demonstrates excellent rejecting-fixture discipline, but its shell/Python/tar-copy machinery is slow and unsuitable for a Go-native redesign. A Go implementation is portable, typed, and easier to exercise without shared-tree mutation.

**Alternatives considered**:

- Copy the donor scripts: rejected by architecture, dependency, and licensing boundaries.
- Grep imports and package names: rejected because text matching confuses comments, fixtures, and real dependency edges.

## Decision 3: Keep reachability checks curated

**Decision**: Census authority-critical guards and concrete port constructors explicitly, with marker or registry metadata and a mutant whose only caller is a test.

**Rationale**: “Implements an interface” does not prove production wiring. Conversely, demanding a production caller for every concrete type would bind tests to harmless internal structure.

**Alternatives considered**:

- No reachability gate: rejected because shadow implementations can pass contract tests without shipping behavior.
- Census every exported symbol: rejected as high-friction and implementation-coupled.

## Decision 4: Split validation by feedback purpose

**Decision**: A bounded quality gate runs on pull requests; the complete fault/soak campaign runs nightly and on manual dispatch. Both use the same invariant helpers.

**Rationale**: The current scenario package takes roughly twelve minutes for one full pass. Nano Kitty demonstrates a useful gate ladder and isolated CI execution, while the donor port demonstrates that zero-match `go test` invocations must be verified from JSON transcripts.

**Alternatives considered**:

- Run everything on every commit: rejected because feedback latency encourages skipping.
- Exclude expensive scenarios permanently: rejected because concurrency and recovery are the product's crown-jewel evidence.

## Decision 5: Remove the contention test's minimum duration

**Decision**: Assert the correct busy/timeout diagnostic and a safe upper bound only; repeat the isolated test twenty times.

**Rationale**: The lower bound fails frequently because driver scheduling returns earlier than the nominal timeout. Sibling-suite contention masking that failure is evidence of a test defect, not product behavior.

**Alternatives considered**:

- Lower the minimum again: rejected because another environment can still violate it while behaving correctly.
- Delete all timing bounds: rejected because an upper bound still detects hangs or ignored cancellation.

## Decision 6: Store one governance context per run

**Decision**: Add a portable immutable context aggregate with ordered entries and framed algorithm-qualified commitments. Runs reference it; leases do not duplicate it.

**Rationale**: Governance is an input to run admission. Replacement leases are authority generations within the same run context. Duplicated lease fields invite drift and complicate migration. The donor port's distinction between resolved context and delivered model bytes is useful; this mission implements only the former and makes no delivery claim.

**Alternatives considered**:

- Add doctrine/profile strings to both runs and leases: rejected as denormalized and semantically weak.
- Defer provenance until the governance phase: rejected because persistent provider-backed runs would already exist.
- Implement exact model-input delivery evidence: deferred as a stronger, separate capability.

## Decision 7: Represent historical absence honestly

**Decision**: Existing runs migrate with a null context reference interpreted as `unknown_pre_provenance`. New run admission requires a valid stored context.

**Rationale**: Backfilling current doctrine into historical runs would fabricate authority evidence. Forward migration must preserve records without inventing facts.

## Decision 8: Treat Orchestrator API 1.3 as a translation target

**Decision**: Map the current kernel onto the canonical Python Spec Kitty 1.3 contract and Nano Kitty's strict consumer behavior. Do not reuse the donor port's 1.1 adapter.

**Rationale**: Contract 1.3 adds structured review results and retains host-owned workspace resolution. The redesign's generation-scoped workspaces are stronger than the current host's lane reuse, so future translation must preserve redesign authority rather than imitate legacy storage.

**Alternatives considered**:

- Defer all analysis to Phase 6: rejected because result-envelope design is being stabilized now.
- Implement the API now: rejected by mission scope and the lack of the full mission engine.

## Decision 9: Remove, rather than implement, the waiver promise

**Decision**: Reconcile Phase 1 acceptance language to state that no waiver mechanism exists.

**Rationale**: There is no approved waiver policy. Adding a bypass solely to satisfy stale wording would weaken the proven gate and create unaudited authority.

## Decision 10: Use donor material only as design evidence

**Decision**: Independently implement useful patterns. Do not copy source or test bodies.

**Rationale**: The donor repository explicitly provides no repository-level license grant. Its concurrency model is also weaker than the redesign's lease-scoped workspace law.
