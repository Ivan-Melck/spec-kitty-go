# Provider boundary contract

## Binding and transport

Explicit versioned JSON binding schema go-kitty.hubnot-binding/1: provider hubnot, ProjectID, canonical existing absolute workspace, canonical executable, expected lowercase 64-hex actor, exact accepted source/toolchain/GOOS/GOARCH/build-recipe/binary-SHA256 tuple. Reject unknown/duplicate keys, invalid UTF-8 and legacy Beads binding. No PATH/cwd/dev-version fallback. Inspection never initializes or repairs resources. Constructor returns bound transport without future port assertions.

Validate executable identity and public hn identity show --json (hn.identity/1; data.actor/name/public_key). Never read private key material/location. Every mutation passes --actor EXPECTED enforced against captured signer. Pre-dispatch drift is unstarted; valid append followed by failure retains applied/ambiguous accounting. If neutral enums need it, add distinct git_native mode; preserve old meanings and declare ConcurrentWriters=none.

Use argv without shell, fixed workspace/executable and bounded environment. Reap children on cancellation. Decode exactly one strict complete JSON envelope, including nonzero typed error exits; reject trailing values, duplicates, malformed Unicode/schema/inconsistent outcome. Each child: 8 MiB stdout, 64 KiB stderr, 30 seconds. Logical observation: one 120-second context, cumulative 64 MiB and 10,000 returned items across all calls and failed attempts; at most two restarts. Never reset budget on stale snapshot. Default details page size 8. Encoded mutation/native limits checked before write, maximum 256 KiB.

Accepted prerequisite:
- issue list --details --json: bounded rows {id,creator,opening_metadata,state:null|full_state,conflict,head_count}, ordinary snapshot/cursor; detail mode binds cursor. Heads separately paginated. opening_metadata is the full metadata map from the verified signed opening event, or {} for legacy openings; retain it even when current state is null/conflicted. Compare immutable ProjectID/WorkID fields with current mapping, not every mutable payload field.
- issue operation --actor ACTOR --operation KEY --json: hn.issue/1 snapshot and unique original signed data {issue_id,event_id,actor,operation,intent,kind,timestamp,parents,state?,body?,request}; absent not_found, duplicate operation_conflict, incomplete/oversized typed refusal.
- All mutations optional actor pin, always supplied by adapter. Native clients preserve default behavior.
Capture exact remaining argv and wire spelling from independently accepted upstream; do not invent it.

## Codec and mapping

Reserve go-kitty/<name> metadata namespace: schema, immutable ProjectID/WorkID, complete/simple marker, canonical JSON chunks with byte/chunk counts and SHA256. Values <=4096 bytes at UTF-8 boundaries; all owned and foreign keys together <=64; complete encoded mutation <=256 KiB. Choose maximum chunk count from actual fixed-key overhead. Foreign keys consume capacity and are never dropped to fit.

Native title/body/ordered criterion ID/text remain useful. Rich criteria/context/scopes/provenance/opaque JSON round-trip in owned payload. Preserve semantic arbitrary JSON, numeric precision, null and nested values without float64 coercion. Reject malformed/duplicate/unknown version/count/digest and native-versus-owned contradictions. Preserve unrelated native labels/status/assignees/relations/foreign metadata during allowed replacement. Return immutable independent decoded values.

Signed opening mapping must agree with current state. Duplicate/cross-project WorkID claims, conflicting heads and missing accepted suppliers refuse. Current definition dependencies derive exclusively from verified blocker-source to blocked-target graph relations and provenance. The codec does not store a competing current dependency list. Original requested dependencies may remain immutable creation-saga/replay intent; they do not override later graph edits. Link/unlink changes only graph semantics and must leave complete definitions readable without altering title, criteria, context, scopes or opaque metadata. Full-definition replacement remains unsupported.

## Shared read model

Internal immutable observation: full current native state, mapped definition, full signed head IDs, normalized graph/discussion, common accepted-history snapshot, canonical full digest and provider token. Known-reference reads collect all relevant bounded pages and verify snapshot equality; stale pages restart within original budget. Canonical order removes pagination artifacts, retains all semantic facts. Existing uint64 Revision is equality-only folded digest; retain full proof and detect inconsistent evidence instead of ordering or treating it as globally unique.

Add small neutral inventory port internal/tracker/inventory.go: request ProjectID, result complete mapped current definitions/external references/common proof. Inventory includes complete current definitions, not all comment histories. Read detailed pages once and derive the global graph from their complete current states, including unmapped suppliers, matching snapshots (no global graph CLI exists); no per-issue history scan for 1,000 items. Inventory common-snapshot/current-definition proof is distinct from full known-reference proof: detail rows do not expose current head signer/ID, and opening creator must never be reused as current-edge attribution. Known-reference reads obtain that proof through paginated heads/graph. Known-reference observation and operation recovery remain separate actions. Unmapped native issues need not appear but cannot hide required suppliers or conflicting mappings.

## Shared original-operation engine

Derive stable action-scoped key from portable OperationID and action identity. Semantic digest covers original portable effect, excludes retry-time observation preconditions. Lookup pinned actor/key BEFORE mutable state/revision checks. Unique matching signed original request returns original effects/IDs after response loss, restart, human edit or newer expected portable revision. Divergent reuse, duplicates, attribution contradiction and incomplete lookup refuse; never append to resolve uncertainty.

New action validates inputs/limits/capability, reads coherent state, prepares full foreign-preserving request, then invokes pinned actor and original key/native expected head. Actor-local head check does not upgrade portable guarantee. Lost response triggers original lookup; absence without complete certainty stays ambiguous. Distinguish unstarted, applied, prior saga mutation and ambiguous. Errors do not imply zero writes.

All mutation families share this engine. Low-level relation replacement belongs here so complete-definition create need not call a later graph method. Issue creation and each required edge use deterministic child keys; resume without duplication, report PriorMutation/incomplete until all observed dependencies exist. No fictional rollback. A historically completed saga is proved from all original child operations; replay does not re-add an edge a human later removed. Distinguish historical replay result from a fresh read of current graph.

## Portable behavior

Catalog create/read/simple unguarded update. Complete definition create/read and blocking graph; guarded update false and generic update on complete definitions refuses before mutation. Preserve Validate and Require semantics, enabling create-only proposals.

Graph preserves unrelated relations, correct direction, provenance; cycles/missing/cross-project/conflicted suppliers refuse. Ready diagnostics confer no authority. Assignment is unguarded descriptive projection; multiassignee ambiguous. Atomic claim and conditional compensation refuse.

Discussion is observably idempotent only after signed-key evidence. Preserve comment ID/exact body/time/signer. Adapter comments carry versioned accountable-author/operation/semantic-digest attribution attested by pinned signer. Native foreign comments keep native attribution. Signer is not independent reviewer; no comment text grants authority.

Final declarations: catalog unguarded, assignment unguarded, discussion observably_idempotent, revision content_derived, safe compensation unsupported, concurrent writers none, portable export/restorable backup unsupported. Reconstruction tests do not qualify stronger backup ports.
