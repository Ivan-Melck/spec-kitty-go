# Typed restart graph — next proof boundaries

Status: architectural handoff, not oracle integration or qualification. The
original strict portable/crown red evidence remains authoritative. A family
name, well-formed SHA or successful reducer is not cross-trace correspondence.

Follow-up designs: [physical repository ownership](PHYSICAL_REPOSITORY_GRAPH_HANDOFF.md)
and [aggregate-head census proposal](AGGREGATE_HEAD_CENSUS_PROPOSAL.md).
Independent census review identified that current admitted scans/snapshots prove
only live membership at pinned AsOf, not physical persisted completeness in the
presence of hidden expired extras. ACKs also lack original producer DatabaseID.
Neither limitation is repaired by private historical replay or a logical
workspace/execution join; no production census seam is implemented. The later
[conditional global cardinality route](GLOBAL_CARDINALITY_CENSUS_ALTERNATIVE.md)
is independently design-reviewed viable with complete fixed readback and actual
RPC attribution; no new provider API is inherently needed. Its
[product seam handoff](RETAINED_MEMBERSHIP_IMPLEMENTATION_HANDOFF.md) remains
unimplemented, with interface/budget approval still required.

## Construction order

1. Verify each complete ACK history and one exact capture independently. Replay
   every aggregate from its first event, retaining database, full scope,
   aggregate ID, logical version, provider revision, event and publication.
   Assert head digest/summary domains separately from reconstructed state bytes.
2. Close each owner using the capability's decoder, coordinate constructor and
   transition law. Mission/work binding ownership and physical host-only
   operation storage are distinct. Host bindings do not inherit mission
   ownership from a neighboring mission event.
3. Close references against the retained historical bytes, never the latest
   head or a global string-to-string digest dictionary. Restrict lookup to the
   same captured graph and exact owner; fail on absent or nonunique matches.
   Also prove authority/currentness **at the referencing publication**: match
   its sealed assertion revision/logical version and original head-value digest,
   separately from the reconstructed state digest, plus eligibility guards.
   Historical existence alone is insufficient. Revalidation at requested and
   granted acceptance publications must each retain its own proof.
4. Pair owner roots injectively only after the original permanent claim bytes,
   digest and creation revision are proved. Also prove the original configured
   repository binding linking project/repository, primary checkout, Git common
   directory and managed root, workspace-ID-derived paths, Git/index ancestry
   and root separation. Self-declared PathIdentity flags and relative layout
   alone cannot prove these relationships. Preserve workspace generations,
   repository/lane/run/lease, actor/process, fences, relative layout and Git SHAs.
5. Independently close non-operation head/capture membership and scope-filtered
   progress multiset before using this graph in the oracle. Operation census
   closure alone is insufficient. Preserve distinct full coordinates even if
   their progress DTOs collide; reject ambiguous coordinate-to-DTO mappings.
   Do not claim physical-head absence from live-only scans.
6. Derive comparison-only tokens for specific proved references. Do not mint
   replacement durable claim/state digests or modify receipts. Compare all
   remaining business fields and exact integers, including unknown fields.
7. Integrate only after full family/result/reference coverage and independent
   review; create a new reviewed allowlist candidate and run each meaningful
   changed actual scenario once. Do not retarget the frozen clean candidate.

## Still-unclosed surfaces

The [bound execution logical join](BOUND_EXECUTION_FOUNDATION.md) is now
scoped-reviewed after correction: publication-atomic exact historical workspace/
claim links and bidirectional current-bound expiry coupling. This is not pairing
or qualification; the broader boundaries below still apply. The later
[configured repository origin](CONFIGURED_REPOSITORY_FOUNDATION.md) is also
scoped-reviewed: original configuration/scalar association is preserved, but
historical physical observations, root pairing and tokens remain unproved.

| Surface | Required additional evidence / constraint |
|---|---|
| Workspace-bound execution correspondence | Logical historical workspace/claim resolution, exact authority fields, atomic publication views and old/new rebind snapshots are now closed **relative to collected history**. Still require independently complete physical census, historical physical repository/root/effect proof beyond the now-reviewed configured-origin capture, private epoch admission and injective cross-trace owner pairing before field-specific tokens or outcome comparison. |
| Handoff | `handoff.StateV2` owns repository/lane/workspace/run/lease facts. Resolve its historical workspace/execution owner and replacement lineage. Replacement predecessor logical version and digest must prove exact predecessor bytes. Candidate, pinned base, contributor/criteria/repository evidence remain business commitments. |
| Review | `review.StateV2` owns reviewer identity, handoff identity and candidate/criteria commitments. Preserve reviewer independence and exact policy exceptions. Criterion EvidenceDigest/ArtifactDigest are not automatically journal-state references. |
| Acceptance | Resolve every satisfied `AcceptanceEvidenceV2` at its exact coordinate/logical version/record revision/state digest and its publication's exact head assertion. `AcceptedReviewV2` cites review ID, actor, version and state digest but **no provider revision**: obtain revision/currentness evidence from the actual sealed assertion and prove the unique historical review owner/preimage; do not invent a field. Waived/inapplicable evidence has no synthetic artifact coordinate, but still requires historical resolved mission decision owners, exact detail digests/assertions and applicable validity windows for applicability, waivers, acceptance policy and independence exceptions. |
| Integration target | `IntegrationTargetStateV2` has repository/target/acceptance/operation ownership but no project/mission field or physical repository root. Require closed configured repository ownership plus exact proved attempt/acceptance linkage. Neither ID prefixes nor declared repository names alone prove a physical root. |
| Integration attempt | Close AcceptanceID and the field-specific AcceptanceDigest domain before substitution; retain repository target, ExpectedOld/Candidate/ObservedTarget Git SHAs, generation, fence and external workspace/conflict/cleanup facts. |
| External effects | Prove exact capability-owned intent, execution authority, original effect ID and adoption/result history. Physical paths embedded in effect results need the same owned root graph; no recursive field whitelist. |
| Runtime / checkpoint host roots | Harness config, immutable authority binding, admitted status and exact historical epoch/checkpoint evidence need their own closed owner proof. Runtime admission is a separate five-record owner-only batch (`productionruntime/recovery.go`), not an ordinary operation ACK; immutable authority-binding creation is another mutation. Require closed batch membership, shared revision, durable acknowledgement, predecessor CAS and recovered-prefix/projection linkage. Until available, refuse runtime-root correspondence: neither `runtime_host` playback, an ordinary ACK nor a ready value establishes it. Checkpoint host operations can publish with operation-only event membership; do not admit them by name. |
| Mission/workgraph/artifacts | Close actual typed project/mission/work-revision owners and immutable configuration/artifact commitments for every observed family/result. No omitted census entries. |

### Digest domains must follow the actual producer path

The normal integration service computes `ReducerStateDigestV2` of exact granted
acceptance bytes in `requireIntegrationAuthorityV2`, then supplies that digest
for the first attempt; later stages preserve the existing attempt digest.
Separately, `buildIntegrationPlanV2` has an all-zero-seed fallback computing
SHA-256 of `"acceptance:" + AcceptanceID`
(`internal/integration/integration_authority_v2.go`). Do not mistake this builder
fallback for the normal production path, or accept it as a snapshot preimage.
The graph must prove the actual ACK's reference domain and exact acceptance
bytes, not infer them from the field name `acceptance_digest_sha256`. Accepted
review and acceptance evidence state digests have their own explicit historical
preimages.

## Controls required before wiring the real oracle

Use genuinely sealed/published invalid-owner plans where the generic publisher
permits them; prove rejection is at the owner/reference layer. Cover foreign
owners, missing first versions, capture mixing, revision collisions, old-version
references with latest digests, unresolved/ambiguous review references, reused
claims/generations, noninjective roots, wrong fences/actors, altered Git SHAs,
unknown business fields and uint64 values above 2^53. Include actual typed
service plans so synthetic fixture head summaries cannot mask domain mismatches.
A successful individual-history proof is never an outcome-equivalence verdict.
