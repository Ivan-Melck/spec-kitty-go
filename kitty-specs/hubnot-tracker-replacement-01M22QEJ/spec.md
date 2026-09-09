# Mission Specification: Hubnot Tracker Replacement

**Mission Branch**: `feat/hubnot-tracker-replacement`
**Created**: 2026-09-09
**Status**: Specified
**Input**: “Cool. Let's do the beads replacement in go kitty.” Prior delegated design decisions and the richer Hubnot work model inform the intent in `intent.md`.

## User Scenarios & Testing

### Story 1 — Work through one qualified provider (P1)
A person or agent explicitly binds an existing Hubnot workspace and verified executable to a project. A first-party Go Kitty consumer creates and reads complete work, maintains blocking edges, projects assignments and appends discussion. Simple catalog work can be revised while preserving foreign fields; stronger unsupported guarantees fail before writing.

**Independent test:** Run the compiled Go Kitty consumer with real Hubnot in temporary Git repositories. Create two full definitions, add/remove a blocker, assign and discuss work. Compare every criterion/scope/provenance/opaque metadata value after restart. A simple catalog update succeeds and a complete-definition guarded update refuses without a write.

### Story 2 — Recover an uncertain effect (P1)
Hubnot applies an attributed operation but its response is lost. After restart, the adapter recovers the original signed request and result, preserving signer and semantic identity. Later human edits remain intact; conflicting or incomplete history requires reconciliation.

**Independent test:** Drop a real mutation response, edit unrelated state, restart and retry. Verify zero duplicate signed facts. Repeat with changed content, signer drift, newer portable expected observation and a concurrent human revision. Never reconstruct a different full-state mutation under the old operation key.

### Story 3 — Preserve collaborative history and authority (P1)
A collaborator edits foreign metadata, creates another revision or claims approval in a comment. Go Kitty exposes provenance/conflicts while retaining execution and review authority. Control-journal playback never queries Hubnot.

**Independent test:** Use distinct signing actors and selected Git replicas, conflicting heads and missing supplier admission. Preserve all facts and typed reconciliation. One accountable actor using two keys still cannot self-review; a tracker claiming approval cannot mint permission. Playback uses a provider that panics on any call and performs zero calls.

### Story 4 — Operate without Beads (P1)
A fresh installation uses the pinned Hubnot provider, inspection and qualification path. Active installation, runtime and quality campaigns require no Beads/Dolt. Legacy bindings refuse explicitly. Frozen evidence and local recovery material retain their original identity.

**Independent test:** Build and exercise the real consumer with bd/Dolt absent from PATH; verify active reachability and CI census. Reconstruct public work in a fresh selected replica with explicit signer disposition. Preserve the inventoried empty current Beads catalog and backup bytes rather than inventing a migration.

### Edge Cases
Multiple heads/assignees; duplicate or rewritten WorkID mappings; cross-project/reversed/cyclic edges; native/encoded criterion drift; unknown schemas; oversized values; malformed/partial JSON; subprocess failure, timeout or overflow; binary/signer drift; stale pages; duplicate operations; interrupted graph saga; incomplete suppliers; linked worktrees; forged author claims; unsupported guarantees; old provider bindings and unresolved historical operations.

## Requirements

### Functional Requirements
| ID | Title | Requirement | Priority | Status |
|---|---|---|---|---|
| FR-001 | Exact binding | Bind project, canonical provider workspace, public signing actor, reviewed source/binary and schema identities. Inspection is read-only; mismatch refuses without init, sync, repair, PATH fallback or an issue write. | High | Open |
| FR-002 | Lossless work codec | Preserve ProjectID/WorkID/opening issue mapping and every representable full-definition field: design/cross-domain context, criterion outcome/verifier/required/evidence hint, scopes, dependencies, provenance and arbitrary opaque JSON. Use versioned owned metadata; reject malformed, contradictory, duplicate or oversized encodings before writes; preserve unrelated native/foreign fields. | High | Open |
| FR-003 | Useful catalog | Implement create/read and honestly unguarded simple catalog update with stable mapping and replay. Generic update cannot bypass complete-definition guards. | High | Open |
| FR-004 | Complete definitions | Create/read complete definitions through WorkDefinitionPort, including actual graph facts and accurate PriorMutation during saga recovery. GuardedUpdate=false; UpdateDefinition refuses before writes; existing per-proposal capability checks stay intact. | High | Open |
| FR-005 | Blocking graph | Add/remove/list blocker-to-blocked relationships with stable internal IDs and provenance; preserve unrelated relations. Conflicts, cycles, missing targets and partial sagas are explicit; readiness remains diagnostic. | High | Open |
| FR-006 | Assignment | Observe/project unguarded descriptive assignment with operation attribution and foreign-field preservation. Multiple native assignees are ambiguous. Atomic claim and conditional compensation refuse without writes. | High | Open |
| FR-007 | Discussion | Append/list observably idempotent discussion with full original IDs, exact body/time, accountable author and distinct transport signer. Distinguish native third-party comments from signer-attested author claims; metadata grants no host identity. | High | Open |
| FR-008 | Original-request recovery | Derive stable action-scoped operation keys; look up original signed semantics before mutable-state checks. Identical retry after response loss/restart/later edits appends nothing. Divergent reuse, duplicates and incomplete lookup stay conflict/ambiguity. | High | Open |
| FR-009 | Coherent observation | Assemble state, heads, mapping, relevant graph and discussion under matching snapshots with bounded restarts. Retain canonical full digest/provider token and equality-only portable revision. Never treat numeric revision as native ID or incomplete data as empty/ready. | High | Open |
| FR-010 | Real consumer | Ship a discoverable compiled Go Kitty provider inspection/consumer surface using the production adapter and portable ports. Exercise full work, graph, assignment, discussion and recovery without raw-ref manipulation or test-only helpers; distinguish tracker-only actions from unavailable mission execution. | High | Open |
| FR-011 | Active cutover | Replace active Beads adapter/reachability, installer, consumer, current docs and qualification/campaign/CI wiring with Hubnot. Keep frozen receipts/history. Legacy bindings refuse; no fallback or dual-write. | High | Open |
| FR-012 | Reconstruction | Prove complete public-work reconstruction by explicit selected sync into a fresh path, missing-supplier refusal/recovery and restart. State same-owner signer recovery separately. Export/backup capabilities remain unsupported unless the complete advertised contract is qualified. | High | Open |
| FR-013 | Pinned installation | Provide an explicit build/install path pinned to the independently accepted Hubnot prerequisite source, toolchain/build recipe and binary digest. Refuse mutable branch/PATH substitution; distinguish signed-event, JSON-envelope and metadata schemas. | High | Open |
| FR-014 | Qualification | Run portable conformance, adversarial protocol and real public-consumer scenarios with exact started/terminal/passed census and source-bound evidence. Missing prerequisites or required skips cannot yield passing qualification. | High | Open |
| FR-015 | Existing-data disposition | Preserve the nineteen inventoried Beads database/backup files and old stashes/branches. Record verified zero current issues without assuming backups are empty. No automatic old-operation rebinding or importer for this empty catalog. | High | Open |

### Non-Functional Requirements
| ID | Title | Requirement | Category | Priority | Status |
|---|---|---|---|---|---|
| NFR-001 | Bounded boundary | Enforce Hubnot's 256 KiB encoded request ceiling/native limits before mutation. Child bounds: 8 MiB stdout, 64 KiB stderr, 30 seconds. Logical observation: 64 MiB, 10,000 items, 120 seconds including all calls and at most 2 restarts after the initial attempt. Page sizing respects both child and aggregate bounds; never omit a record or truncate fields to fit. Cancellation stops child work. | Safety | High | Open |
| NFR-002 | Query cost | Timed compiled-consumer inventory recovers all 1,000 complete mapped current definitions within 120 seconds on the recorded host; separately measure one known-ref observation and one lost-response recovery. Use detailed bounded pages rather than per-issue full-history scans for mappings. Include real adapter metadata and some graph/discussion state; record field/history counts, page size, build/race mode, host/toolchain, commands and elapsed time. Fixture setup is outside timing. | Performance | High | Open |
| NFR-003 | Regression gates | Formatting, all-package build/static/module checks and applicable complete make quality pass using installed Go 1.26.7. New adapter/codec/recovery tests pass race detection. Real provider qualification is separate from portable stubs. | Quality | High | Open |
| NFR-004 | No-write failures | Invalid binding/input, unsupported guarantees, stale/conflicting requests and read-only failures cause zero unexpected provider/control/ref writes. Pre-dispatch refusal publishes zero adapter facts. A valid append followed by discovered distributed conflict or response loss is retained and reported as applied or ambiguous, never deleted or called unstarted. | Reliability | High | Open |
| NFR-005 | Scanner repair | Inert SQLite filename patterns in plain .gitignore pass; hidden executable SQLite use and actual forbidden imports/dependencies still fail. No blanket exemption for ignored files/provider data. | Quality | High | Open |

### Constraints
| ID | Title | Constraint | Category | Priority | Status |
|---|---|---|---|---|---|
| C-001 | Authority | Existing Go Kitty/GapDB control authority stays intact. Tracker facts grant no lease, review verdict, acceptance or integration permission. Preserve immutable history and zero-provider-I/O playback. | Architecture | High | Open |
| C-002 | Honest guarantees | No global guarded update, atomic assignment or safe compensation follows from actor CAS/local locks. Do not weaken portable meanings. Complete-definition updates stay unsupported while create-only decomposition remains useful. | Concurrency | High | Open |
| C-003 | Public inward boundary | Use pinned public Hubnot commands, no internal Hubnot imports or private key-file reads for runtime binding. Keep provider vocabulary outside inward-owned authority packages. No adapter database, hidden authority cache or mandatory new service. | Architecture | High | Open |
| C-004 | Historical integrity | Preserve old missions, M1/M2/Phase 1–3 receipts, adoption fields and exact identifiers. Classify all Beads-to-Hubnot occurrences; amend active guidance with explicit historical dispositions. | Compatibility | High | Open |
| C-005 | Program scope | This qualifies/replaces the mutable-work provider. It does not fabricate M2 adoption, admit/complete M3, restore API 1.3, implement all API 2.0, add Hubnot governance authority or migrate control-store data. | Product | High | Open |

### Key Entities
Qualified tuple; workspace binding; signing actor; accountable ActorID; stable WorkID; opening issue ID; full work definition; blocker edge; signed operation; attributed comment; snapshot observation; ambiguous effect; reconciliation decision; legacy binding.

## Success Criteria
- SC-001: All four stories pass through real compiled first-party/provider interfaces; useful creation and effects succeed alongside explicit refusals.
- SC-002: Every FR/NFR/C has implementation and independent evidence; useful capabilities are not disabled to make tests pass.
- SC-003: Active installation/runtime/quality works without Beads/Dolt; frozen evidence and local recovery data retain exact content.
- SC-004: Lost response, restart, human edit and signer drift yield attributed recovery or explicit reconciliation, with zero duplicates/destructive undo.
- SC-005: The accepted Go source pins independently accepted Hubnot prerequisite source/binary; mutable-branch feature presence cannot establish readiness.

## Assumptions and Disposition
The user delegated routine design decisions. Bind one explicit canonical provider workspace separately from execution worktrees. Current local Beads issue inventory is empty; preserve recovery material and refuse unsupported legacy bindings. A general importer is separate work. Neutral inputs exceeding documented provider representation bounds receive a typed refusal, never lossy conversion. Native production-host unavailable composition seams remain unavailable until their program admission.

Qualification uses disposable provider workspaces. This mission does not initialize Hubnot in the real project, select/publish real remote histories or alter an existing user's provider binding. Legacy bindings receive actionable refusal and an explicit operator binding path.

The Hubnot prerequisite adds public identity JSON, mutation actor pinning, signed operation lookup and detailed bounded issue listing. Lock its exact contract/source in planning and qualification before admitting the adapter. This replaces active Beads integration with Hubnot; `occurrence_map.yaml` governs all categories and historical exceptions.
