# Data model

| Entity | Ownership and fields |
|---|---|
| Provider tuple | Exact accepted Hubnot source, build recipe/toolchain/target, binary digest, CLI envelope and signed-event versions. Qualification evidence owns admissibility; a dev version string alone is insufficient. |
| Binding | ProjectID, canonical provider workspace identity, explicit executable, expected public signing actor, read-only policy and honest concurrency mode. Separate from lease workspaces and accountable ActorID. |
| External work mapping | Stable ProjectID/WorkID, full opening issue ID, original creation operation and schema. Signed opening facts and current mapping must agree; duplicates or cross-project claims refuse. |
| Work definition | All existing neutral fields preserved. Native title/body/ordered criterion ID/text stay visible. Canonical owned metadata carries richer criteria, context, scopes/provenance, dependencies and opaque JSON, chunked with version/count/digest under documented limits. |
| Native issue state | Status, labels, assignees, unrelated relationships and foreign metadata remain collaborative fields. They are preserved on allowed full-state replacements and do not confer execution authority. |
| Blocking relationship | Source is blocking issue; target is blocked issue. Internal WorkIDs and signed source revision/actor preserve direction and provenance. Definition creation plus required edges is a resumable saga. |
| Discussion | Native comment ID, signed actor, timestamp and exact body. Adapter-authored comments have versioned accountable-author/operation/semantic-digest attribution explicitly asserted by the pinned signer. Native foreign comments retain native attribution. |
| Operation | Action-scoped stable key, original OperationID, semantic digest and signed original event/parents/content. Recovery observes before stale checks and never substitutes the latest state for the original request. |
| Observation | Matching accepted-history snapshot, full canonical state/heads/graph/discussion/mapping, full digest and native token, plus the established equality-only uint64 revision. No process-local sequence counter or authoritative adapter database. |
| Reconciliation | Explicit ambiguous/conflicted/stale/unavailable/attribution-lost outcome. Existing Go Kitty operation and immutable WorkRevision records remain authority. Provider observations do not rewrite them. |

Runtime and artifact schemas remain distinct. This mission introduces adapter payload and qualification schemas; it does not change historical control events or adoption records. Capability declarations retain the existing guarded-update, atomic-claim and conditional-compensation meanings.
