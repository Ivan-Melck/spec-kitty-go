# Research: Hubnot Tracker Replacement

Go Kitty baseline: `94e7c8542d7e543558391ab6fffda7a28e6e3759`. Hubnot rich-issue baseline: `5a0e9cda0639241cfb45d2780e1b50c487109747`. These are source identities, not interchangeable release/adoption claims.

## Decisions and evidence

| Decision | Evidence and rationale |
|---|---|
| Preserve newer authority architecture | `docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md` and `docs/PRODUCTION_RUNTIME_PROGRAM.md` separate mutable tracker observations from GapDB/control-journal authority. M2 successor adoption remains a separate attributable human decision. |
| Deliver actual provider consumer, not M3 composition | Both binaries use `internal/helpercmd`; `production.go` deliberately supplies unavailable readiness/decomposition/integration providers. A bounded native tracker-only command family can be real and useful without silently changing those mission seams or restoring retired API 1.3 grammar. |
| Keep guarantee meanings | `internal/tracker/capabilities.go` forbids inventing guarded mutation via read-then-write. Hubnot checks a snapshot and CAS on its signing actor, so different actors may create siblings. GuardedUpdate, atomic assignment and safe compensation remain unsupported. |
| Keep create-only decomposition useful | `WorkDefinitionCapabilities.Require` checks each proposal's actual needs. Create-only work need not demand guarded update. `Validate` remains strict; `UpdateDefinition` refuses. Generic catalog updates cannot bypass full-definition guards, matching the established Beads restriction. |
| Encode every newer work field | `work_definition.go` includes outcomes, verifiers, evidence hints, scopes, provenance and arbitrary JSON absent from Hubnot's native issue state. Use a versioned canonical owned payload with bounded chunks and a digest, mirrored native ID/text criteria and explicit contradiction rejection. Reject representability overflow rather than truncate. |
| Improve the public prerequisite first | Hubnot `pinned-actor-operation-lookup-01M22QQS` supplies public identity JSON, mutation actor pinning, operation lookup and detailed list pages. This closes signer-race and lost-create lookup gaps and prevents per-issue full-history scans for WorkID discovery. No private key parsing or Hubnot internal imports are needed. |
| Recover before reconstructing requests | Existing catalog conformance retries an update with a newer expected observation. Hubnot replay binds original parents and full state. Observe the original signed actor/key operation, validate portable intent, recover original request and only then return a fresh coherent observation. Never recompute changed content with the old key. |
| Store edges in the correct direction | Hubnot blocks links are source blocker → target blocked. A definition listing dependencies requires mutations on blocker issues. Creation is therefore a resumable multi-event saga with accurate PriorMutation, not atomic multi-issue creation. |
| Preserve signer versus accountable author | Hubnot fingerprints identify keys, not people. Explicit signed attribution records remain assertions by the bound transport signer. Go Kitty actor identity/reviewer independence is never inferred from keys, display names, assignment or metadata. |
| Make observations complete | A native head alone misses comment and incoming-edge changes. Canonical observations cover relevant state, heads, mappings, graph/discussion and full provenance under matching accepted-ref snapshots. Keep full digest/token alongside equality-only uint64 revision. |
| Do not invent a data migration | Isolated-copy inventory using the exact qualified Beads binary exported zero current issues and returned an empty list. Original database/backup hashes matched for nineteen files. Preserve historical backup content, which was not restored or classified. |
| Fix the actual local quality false positive | Baseline TestM2SQLiteAbsence fails solely on inert `.beads/.gitignore` sqlite3 filename patterns. Exempt inert patterns precisely; retain tests for executable invocations in hidden locations and forbidden dependencies. |
| Preserve historical evidence | Existing kitty-specs, source-bound Phase 1–3/M1/M2 receipts and old operation identities remain frozen. Current product/campaign/provider docs change deliberately, with rejecting guards retaining legacy tokens when those tokens are what they detect. |

## Upstream contract dependency

Provisional public contract from the prerequisite mission (must be independently accepted before qualification):

- `hn identity show --json`: `hn.identity/1`, public actor/name/public key only.
- `hn issue <mutation> ... --actor ACTOR --json`: optional backward-compatible expected signer, checked against the identity actually used. Mismatch gives `actor_mismatch`, zero append.
- `hn issue operation --actor ACTOR --operation KEY --json`: `hn.issue/1` plus snapshot, returning unique original issue/event ID, actor, operation, intent, kind, timestamp, parents, full state or body and signed request digest. Absent is `not_found`, duplicate is `operation_conflict`.
- `hn issue list --details`: bounded full-state rows `{id,creator,state,conflict,head_count}`, with details bound to continuation identity. Conflicted state is null; head inspection remains separately paged, avoiding unbounded nested head lists.

The qualified source commit/build digest is deliberately not guessed here. Planning records the wire contract; qualification pins the accepted prerequisite source and exact build identity.

## Evidence and scope

Read-only reports are copied into `research/` for audit. The original executable data inventory and baseline scanner failure are retained in the host evidence directory `/home/lynn/Documents/Codex/2026-09-09/h/work/`, under `go-kitty-hubnot-*`. No original Beads database command was run: commands operated only on an isolated copy with metrics/event flush disabled and backup redirection removed from that copy.

The earlier Go Kitty evaluation passed the full quality aggregate on a clean checkout at the same application source and diagnosed the original-checkout scanner failure. That is baseline evidence only; this mission must qualify its own integrated candidate. The prior Hubnot richer-issue race suite is likewise upstream baseline evidence, not Go Kitty conformance.

## Risks carried into planning

Exact signer and workspace binding; schema bounds; coherent multipage reads; collision-aware observation identity; duplicate/rewritten mappings; edge-saga recovery; forged attribution; response loss after apply; bounded child cleanup; real CLI reachability; active versus historical campaign classification. These are explicit acceptance obligations, not reasons to advertise stronger guarantees or disable useful capabilities.
