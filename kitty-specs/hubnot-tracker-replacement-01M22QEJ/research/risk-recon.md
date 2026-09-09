# Go Kitty → Hubnot replacement: independent risk reconnaissance

Date: 2026-09-09. Read-only pre-spec review by the independent reviewer agent. No repository/runtime state changed, no implementation or full test suite run.

Sources: `/home/lynn/projects/go-kitty3` at the supplied main baseline `94e7c854`, especially `docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md`, `docs/PRODUCTION_RUNTIME_PROGRAM.md`, `internal/tracker/{provider,capabilities,work_definition}.go`, current Beads adapter, and `internal/execution/effect_authority_v2.go`. Hubnot sources are `/home/lynn/projects/nichthub`, supplied merged baseline `5a0e9cda`, especially `docs/{issues-v1,identity-v0,replication-v0,threat-model}.md` and issue/identity/Git implementation.

During review, Go Kitty HEAD advanced to `034324e45de931d63cbcba402a8f74bfe2c062f3` through parent coordination. A read-only diff confirmed the cited architecture/program docs, tracker subtree and effect-authority source remain unchanged from `94e7c854`. Hubnot HEAD was `5a0e9cda0639241cfb45d2780e1b50c487109747`.

## Assessment

Replacing the default mutable-work provider is feasible, but substituting CLI names or matching happy-path CRUD is insufficient. The new Go Kitty architecture remains canonical: Hubnot owns mutable descriptions and discussion; Go Kitty owns immutable WorkRevision generations, execution, independent review, acceptance, integration and recovery decisions; GapDB owns ordered durable control storage. Hubnot's signed issue history does not become a replacement control journal.

Two decisions block an honest implementation spec: **which guarantee can be advertised for concurrent Hubnot writes**, and **how a complete Go Kitty WorkDefinition fits losslessly within Hubnot's bounded schema**. The mission should settle these explicitly before claiming existing provider capability parity. The remaining obligations below define the bounded adversarial qualification needed for a replacement.

## Critical contract gaps and proof obligations

### P01 — Signing identity is not the accountable actor

**Evidence.** Go Kitty architecture §§3, 4 and 12 require reviewer ActorID to differ from every candidate contributor ActorID regardless of ProcessID. `EffectIntentV2` records ActorID and ProcessID separately. Hubnot `identity-v0.md` defines an actor as one Ed25519 public-key fingerprint, expressly disclaims that distinct keys prove distinct people, and treats device/successor continuity as signed relationships without inferred policy authority. Issue assignees are arbitrary descriptive strings.

**Obligation.** Keep a host-owned, explicit binding among provider signer, project/provider workspace and accountable Go Kitty actor context. A bot signer may transport actions for several accountable subjects, and one subject may use several signers; neither mapping should be inferred from names, assignees, metadata, key rotation or device edges. Provider-native author provenance must remain visible alongside any recorded accountable-actor attribution. Imported self-authored metadata cannot grant actor identity or review independence.

**Disproof probes.** One accountable actor uses two Hubnot keys and two ProcessIDs: self-review still refuses. Two accountable actors use one service signer: do not collapse their Go Kitty identities. Change the active signer during response-loss recovery: no second operation is emitted under the new key. Forge the adapter's attribution metadata in a valid third-party signed issue: it must not acquire host authority.

### P02 — `--expect` is not global issue CAS

**Evidence.** `tracker.UpdateLevel` defines guarded mutation as preconditions enforced at the provider mutation boundary; `RequireCatalogUpdate` explicitly forbids emulating a missing guard with a local read followed by write. `CompensationConditional` requires revision, value and operation attribution checks in the same critical section. Hubnot documents snapshot preflight plus actor-ref CAS, not a cross-actor issue lock. Another actor can write after the snapshot and create a valid sibling; mutation `conflict/head_count` is only observed catalog plus own event.

**Obligation.** Do not advertise `UpdateGuarded`, `AssignmentAtomicClaim`, `CompensationConditional`, or broad provider-native serialization merely because the CLI accepts expected heads. Specify the exact writer boundary. A host adapter lock only serializes cooperating local adapter calls; it cannot exclude direct CLI writers, sync, or disconnected replicas. Options are to expose a precisely named weaker observation/append guarantee and require reconciliation, retain explicit unsupported capabilities where the current port demands stronger semantics, or make a separately proven provider-side primitive change. Do not weaken existing capability meanings silently.

**Disproof probes.** Pause after snapshot verification, append from another actor, then allow the adapter append. Both heads must survive; the adapter must not report global exclusivity or manufacture a single final work state. Repeat with human assignment, relation edits, and synchronization during compensation. Provider capability reporting and effect outcomes must agree with observed behavior. An optional single-writer deployment restriction must be enforced and tested, not merely stated.

### P03 — Complete WorkDefinition mapping and bounded losslessness

**Evidence.** `WorkDefinition` includes design context, cross-domain reason, criteria outcome/verifier/required/evidence hint, write scopes including domain/access/serialization key, provenance, dependencies and opaque metadata. Hubnot native criteria contain only ID/text; its metadata is at most 64 string pairs with 4096 bytes per value, total requests 256 KiB, criteria 64 and relations 128. Go Kitty `OpaqueMetadata` accepts JSON up to 1 MiB, and its current WorkDefinition constructor does not impose Hubnot's collection cardinalities.

**Obligation.** Define a versioned adapter-owned namespace and exact encoding, including all fields and explicit empty values. Preserve other Hubnot labels, assignees, relationships and metadata during full-state replacements. Define duplicate or contradictory native/encoded fields as errors, or specify a single authoritative representation for mutable planning. Do not reconstruct required scope or verifier semantics from prose. Bound encoding before any append; an unsupported-size definition must fail visibly without truncation, silent splitting, or orphan issue creation. Full existing-domain parity may require an explicit provider limit contract or a narrow separately authorized Hubnot enhancement.

**Disproof probes.** Round-trip maximum valid definitions, every criterion field, read/write scopes, provenance and foreign metadata. Exercise 65 criteria, 129 links, 4097-byte encoded metadata values, and a definition valid for the neutral port but too large for Hubnot. Mutate only a foreign field between observation and write: it must survive or produce reconciliation. Missing/unknown schema, malformed JSON-in-string and contradictory mapping fields must not become executable work.

### P04 — Observe a complete, coherent provider revision

**Evidence.** Go Kitty `Revision` is an opaque uint64; it is not a timestamp or ordering guarantee. `ProviderObservation` can also retain canonical fields, full digest and provider token. Beads currently derives a complete content observation including discussion/dependencies. Hubnot state heads are full SHA-256 IDs, but comment changes do not create new state heads, and graph/read pages share repository accepted-ref snapshots. Conflicted issues expose `state:null`.

**Obligation.** Define the observation's canonical content and scope: state, full head set, attribution, comments, relevant incoming/outgoing dependencies, mapping and any recovery markers. A head ID alone is insufficient if the port's mutation expectation covers comments or incoming graph changes. Assemble multi-command/multi-page observations under one matching snapshot, restart boundedly on staleness, and never combine inconsistent pages. Preserve full provider identity/digest for recovery; a folded uint64 must not be reversible by guesswork or depend on a process-local counter/cache.

**Disproof probes.** Restart between observation and mutation; add only a comment or incoming blocker; create a conflict; alter a page boundary; supply two full observations with the same injected folded revision. Fail closed rather than select one head, drop records, or use a numeric revision as a Hubnot ID. No portable contract should assume numeric ordering.

### P05 — Response loss must replay the original signed request

**Evidence.** Go Kitty architecture §§6 and 11 persist effect intent before dispatch and permit retries only under provider idempotency. Hubnot operation keys are signer-scoped across issue commands and bind intent, target, expected parents and canonical requested state. Identical replay precedes current-state checks. New signer or changed parents/content denotes a different operation or a conflict. Existing Go Kitty effects preserve stable operation identity and explicit uncertain observations.

**Obligation.** Persist the exact semantic request needed for retry, signer binding and operation derivation before dispatch, using the existing application/outbox seam. Do not rebuild a full-state mutation from the latest issue on recovery and reuse the old operation key. Distinguish read-only observation, replay of the original request, and a new deliberate reconciliation mutation. Scope child keys for multi-step create/graph operations deterministically and within Hubnot's 128-byte limit. Validate returned issue/event IDs and bind observable history to the exact operation, signer and request; marker text alone is insufficient.

**Disproof probes.** Kill after provider append but before response; restart Go Kitty and retry without another event. Repeat after human revision, closure/reopening, a comment, partial multi-step graph creation, and signer rotation. Reuse the key with changed content/target/parents: explicit conflict, no write. Inject a plausible success envelope identifying another issue/event and malformed/truncated stdout after actual append: observe/reconcile, never assume success or safely retry a new request.

### P06 — Compensation must not erase later human work

**Evidence.** Go Kitty compensation permits undo only when revision, current value and attribution remain exact; ambiguity yields `decision_required`. Current Beads assignment code already refuses atomic claim when it cannot attach the operation marker atomically. Hubnot can append assignee plus marker in a full-state revision, but another signed revision can copy that marker, and remote siblings can remain unseen.

**Obligation.** Bind attribution to the exact signed revision and signer/request evidence, not merely a retained namespaced marker and matching assignee. Refuse destructive compensation if another revision, conflict, missing supplier or uncertain outcome intervenes. Restoring one assignment must preserve all unrelated state. Explicitly unsupported compensation is safer than overstating the provider guarantee and is acceptable only if the mission's required consumer behavior handles that capability honestly.

**Disproof probes.** A human edits description while retaining marker/assignee; another actor copies the marker; a human unassigns then reassigns; a remote sibling arrives immediately before compensation; an unrelated label appears. No overwrite of newer work and no invented confirmed compensation. Test zero mutation on refusal and the resulting host reconciliation decision.

### P07 — Preserve dependency meaning and WorkID identity

**Evidence.** `ExternalWorkRef` explicitly separates internal WorkID from provider ExternalID. Go Kitty dependencies name blocking and blocked WorkIDs. Hubnot `blocks` is source blocks target; `parent` is source child of target; `related` is informational. Hubnot requires relation targets to exist and can retain post-sync cycles and ambiguous per-head links.

**Obligation.** Pin direction mapping in the contract. WorkDefinition prerequisites cannot be inferred from arbitrary parent/related links. Build a project-scoped one-to-one WorkID↔full issue-ID mapping and reject duplicates or cross-project claims; do not allocate new internal WorkIDs simply because the provider changed. Multi-issue creation/edge installation needs an explicit resumable saga, because target IDs must exist and creation cannot be globally atomic. Conflicts, cycles, unresolved targets and incomplete selected histories must block finalization or request reconciliation; provider readiness is diagnostic, not execution permission.

**Disproof probes.** Reverse one edge, create cross-project/duplicate mapping claims, migrate diamond dependencies, lose a response between node creation and edge installation, and import a distributed cycle. Existing pinned WorkRevision stays immutable and no falsely ready work is scheduled.

### P08 — Clone, selected sync, backup and restart are different contracts

**Evidence.** Hubnot transports public signed histories only. Active signer, private keys, selections, pending admission receipts and rotation journals live in private `.git/hn`; the Git directory can be worktree-specific. No saved selection uses bounded compatibility-all. `hn sync` imports and then publishes local collaboration refs; accepted remote histories are not republished as local facts. Exact missing suppliers stay quarantined. Ordinary primary-branch push/clone is not documented as a complete provider backup. Go Kitty playback must invoke zero external adapters; restart performs explicit reconciliation under a fresh epoch.

**Obligation.** Specify separate portable export, complete public-history backup, same-owner private signer recovery, new-device onboarding, and host-control recovery. Do not claim `RestorableBackup` from a Git clone alone. Preserve custom refs and exact accepted-projection provenance, distinguish unaccepted/pending state, and never copy signer keys to create a second device. Pin the project provider workspace separately from lease worktrees. Query methods must not sync, publish, broaden selections or repair state implicitly. A restart may observe Hubnot through the existing external reconciliation boundary; playback must use recorded observations only.

**Disproof probes.** Restore into a fresh path without private state; restore with missing custom refs; recover an interrupted admission; withhold the exact actor supplying a revision or relation target; run from a linked worktree; rotate then restart; use a read-only clone. Restore either reconstructs a complete verified catalog with explicit signer disposition or stops truthfully. Replay canonical host history with a provider that panics on any call and compare the projection digest.

### P09 — Migration must not rewrite executable history

**Evidence.** Go Kitty pins immutable WorkRevision generations and external mappings; tracker drift requires continue/supersede/invalidate/fork reconciliation. Historical Phase 2 Beads and M2 evidence are source-bound and do not qualify a replacement. The user authorized replacement, but that does not make historical tracker objects safe to delete or live operations safe to rebind.

**Obligation.** Inventory whether any live Beads catalog/binding requires migration. If none does, say so and qualify a fresh Hubnot binding without fabricating a migration. If data exists, define an explicit bounded offline transfer with dry-run manifest, original WorkIDs, full old/new external mapping, preserved content/discussion attribution and semantic checks. Imported Beads authorship remains provenance asserted by the importer, not a newly fabricated signature by the historical author. Choose a cutover boundary with no unresolved external effects or make their old binding remain addressable for observation. Do not rewrite old WorkRevision/candidate/review/control events to look Hubnot-native. No uncontrolled dual-write or automatic fallback.

**Disproof probes.** Partial import/retry creates no duplicates; missing dependency and oversize metadata abort safely; an old response-loss operation remains bound to its original provider; conflicting migration manifest is rejected. A historical candidate/review remains inspectable with original identifiers after cutover. Rollback does not discard already signed Hubnot events or rewrite frozen Beads qualification.

### P10 — Bind the executable/protocol/workspace and fail closed

**Evidence.** Current `TrackerBinding` pins executable/version/digest, protocol schema, project and canonical provider workspace independent of lease workspace. Current concurrency enum is shaped as embedded-serialized/server-multiwriter. Hubnot public machine protocol is `hn.issue/1` while signed event protocol is `hn/0`; metadata mapping requires a third independently versioned adapter schema. Hubnot has bounded stdout semantics and distinct invalid_history/resource_limit/stale/repository diagnostics.

**Obligation.** Admit an exact Hubnot source/binary tuple and protocol evidence, not `PATH`, mutable branch or product name. Define honest concurrency/binding vocabulary rather than label Git actor histories a database server. Separate CLI envelope, signed event and adapter metadata versions. Strictly validate envelope/EOF/exit, bound stdout/stderr/time and sanitize environment/Git redirection; raw issue text remains data. Preserve resource-limit, unavailable, malformed, conflict and pending-admission distinctions instead of translating all failures to not-found or retryable contention.

**Disproof probes.** Replace executable after binding, redirect `GIT_DIR` or working directory, change active signer, emit mixed stdout or unknown envelope version, flood output, and return a valid foreign-repository issue. No provider effect is attributed to the wrong binding. Never log private signer material or interpret issue content as commands.

## Scope recommendation and admission boundary

1. Make this a **provider replacement and qualification mission** against current inward-owned ports, with narrowly justified provider-neutral contract amendments where existing vocabulary cannot express the truth. Keep concrete Hubnot code outside the authority kernel and do not import Hubnot internals merely to bypass public protocol gaps.
2. Specify a lossless schema and capability matrix before implementation. Identify any required Hubnot enhancement as a separate dependency; the Go mission must not assume unshipped guarantees from the just-merged richer-issues feature.
3. Preserve the newer production architecture's authority laws. Reconcile active architectural/default-provider documentation and first-party composition intentionally; do not bulk-rewrite historical Beads evidence or replace old exact source names inside frozen receipts.
4. Include read/create/update, full WorkDefinition, dependencies, discussion/replay, binding/health, permitted assignment behavior, explicit unsupported compensation where applicable, and backup/restore truth in acceptance. A green example that only opens/shows an issue is insufficient replacement evidence.
5. Use real compiled Hubnot subprocesses and temporary Git replicas plus dishonest-provider boundary tests. Required campaigns should target P01–P10; reuse existing conformance where it constrains the right production semantics. Avoid turning this into a broad Hubnot feature expansion or a complete production-runtime launch.
6. Treat actual Beads data transfer as explicit conditional scope based on inventory. An offline mutable-catalog transfer is distinct from a GapDB/control-store importer; architecture §18's prohibition on storage importers, fallback and dual-write remains intact.

**M2/M3 admission:** `docs/PRODUCTION_RUNTIME_PROGRAM.md` §10 states M2 is technically release-ready but successor admission still requires attributable human adoption binding the exact candidate tuple. `docs/programs/evidence/m2-gapdb-qualification-candidate.json` remains `adopted=false`. This mission authorization must not silently turn that into an adoption record. Preparatory adapter development/qualification can proceed without claiming M3 has been admitted. If the mission intends real production-host composition as M3, capture the exact prerequisite adoption through the existing program mechanism first. Source presence, a green adapter test, or a changed default-provider label is not admission.

Evidence-reading caution: that earlier qualification-candidate JSON also has `release_ready=false`; it is not the final release receipt. The active program text and `m2-qualification.json` distinguish producer eligibility from outer-review `release_ready_not_adopted`. Preserve each artifact's source/time scope rather than treating a preliminary candidate field as the current release verdict.

## Suggested falsification target

After durable host intent, lose a Hubnot mutation response, restart the host, introduce an independently signed human edit plus signer drift, and attempt recovery/compensation. The system must preserve all signed work, keep the original WorkID/WorkRevision and accountable actor, issue no duplicate operation or destructive undo, and return either an exactly attributed recovered observation or a durable reconciliation decision. Run a normal successful case alongside it so “always refuse” cannot pass qualification.

This review identifies integration proof obligations, not defects in the completed Hubnot mission. Its published limitations are consistent; the risk is a new adapter claiming stronger or more complete semantics than those limitations permit.
