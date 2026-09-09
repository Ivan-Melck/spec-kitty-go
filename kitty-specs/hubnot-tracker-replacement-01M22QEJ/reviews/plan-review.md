# Independent plan review — Hubnot Tracker Replacement

**Final verdict: PASS.** The two initial shared-contract blockers below were corrected and independently re-reviewed. The plan may proceed to task slicing; implementation qualification still depends on independent acceptance of the exact upstream source/binary.

Read `spec.md`, `plan.md`, all three `contracts/*.md`, `occurrence_map.yaml` and `quickstart.md` in `/home/lynn/projects/go-kitty3/kitty-specs/hubnot-tracker-replacement-01M22QEJ`. Compared them with the existing public Hubnot issue protocol and proposed prerequisite contract. No repository/runtime changes or tests.

## B1 — Inventory depends on unavailable public proofs — RESOLVED

**Locations:** `contracts/provider.md`, “Codec and mapping” and “Shared read model”; prerequisite detail-row contract.

The plan requires signed-opening ProjectID/WorkID agreement and “bounded global graph pages,” but the documented prerequisite details return only current state, ID, creator, conflict and head count. Current metadata does not prove what the signed opening mapped after a later rewrite. Existing `hn issue graph` requires an issue ID; no global graph endpoint is contracted. Reading every opening/history or graph individually would contradict the inventory design and its 1,000-item qualification purpose.

**Required amendment:** Agree on a generic public opening-metadata proof in detailed rows, derived from the verified opening event, and bind its exact shape to the independently accepted upstream source. Derive the global graph from the full detailed current states, including unmapped supplier issues, under the same snapshot; do not call an invented endpoint. Define handling of conflicted/unavailable suppliers without hiding them. Keep full per-issue histories exclusive to known-reference observations.

The host has proposed `opening_metadata` and requested upstream confirmation. This is a valid solution direction but remains a pending dependency until incorporated and qualified. Page size must account for opening metadata plus current state: proposed eight rows rather than sixteen, retaining the existing 8 MiB child and 64 MiB cumulative limits. Do not infer that each individually bounded state makes every chosen page size safe.

**Acceptance probe:** Create a valid mapped issue, rewrite its current mapping in a later signed revision, and run public compiled inventory. It must refuse or report the contradictory mapping without per-issue history scans. Include unmapped issues carrying incoming blockers, a conflicted supplier, and a 1,000-item exact definition/mapping comparison.

## B2 — Graph mutation can contradict complete-definition metadata — RESOLVED

**Locations:** `contracts/provider.md`, “Codec and mapping,” “Shared original-operation engine,” and “Portable behavior”; Story 1/FR-004/FR-005.

Complete definitions encode declared dependencies and must validate actual graph facts before they are returned. Link/unlink independently changes a blocker issue's native relation. Unless the dependency representation has one clear owner, the blocked complete definition can retain its old declared list, causing a supported graph action to leave complete work unreadable. The present contract does not specify how it avoids that contradiction. Conversely, silently doing a generic complete-definition replacement would evade the deliberate `GuardedUpdate=false` boundary.

**Required amendment:** Choose one precise model before IC-03/IC-04 split. Prefer making current dependencies derive exclusively from the verified native graph, with no competing mutable dependency list in owned metadata; preserve any initial declaration only as explicitly historical creation intent. Alternatively define a replayable relation/declaration saga, all affected issue writes, child keys, partial-state refusal and recovery, without claiming global guarded replacement. Clarify that graph editing changes the dependency component of mutable work under its own declared guarantee, while generic whole-definition replacement stays unsupported.

**Acceptance probe:** Create two complete definitions, link them, read both definitions/inventory, unlink, and read again. Dependency lists must match actual graph each time, unrelated rich fields must remain equal, and interrupted/retried mutations must either recover or expose a bounded partial state without duplicate writes or silent semantic drift.

## Small exact correction

“Reserve `go-kitty.` metadata namespace” must use concrete Hubnot-valid slash keys, such as `go-kitty/schema` and `go-kitty/payload-000`. Literal dot-only keys violate the provider's required namespace/name syntax. The host agreed to change this; fix all examples and key validation together. This is straightforward and does not justify a new work package.

## Areas that pass

- **Port fidelity and usefulness:** catalog/assignment remain explicitly unguarded; guarded definition updates, atomic claims and compensation refuse. Create-only proposal capability checks remain useful. The command family exposes actual definition, graph, assignment and discussion work rather than an inspection-only shell.
- **Recovery ordering:** one original-operation engine precedes mutable-state checks and retains original signed semantics. Portable retry-time observation preconditions are deliberately excluded from semantic intent; action/target/content still distinguish new intent. Task contracts should test this exact distinction, including a newer portable expected revision and a changed effect request.
- **Accounting and bounds:** fixed logical deadline and cumulative bytes/items span all retries; no child-budget reset can create an unbounded loop. Pre-dispatch refusal differs from retained applied/ambiguous events. Known-reference observation is properly separate from inventory; inventory need not read all discussion history.
- **Consumer boundary:** native tracker dispatch occurs before HostFactory/control-root classification. Existing mission composition remains unavailable and no M2 adoption or M3 completion is implied. Real user initialization/rebinding/sync remains excluded.
- **Ten-concern split:** IC-01/02 establish shared transport/read contracts; IC-03 owns the shared operation engine before independent graph/effect wrappers; consumer, qualification, atomic cutover and integrated acceptance follow. Scanner work is independent. Resolving B1/B2 prevents cross-package circular assumptions; no success stubs or future-method interface assertions are needed.
- **Cutover and historical integrity:** staged successor precedes atomic active replacement. Manual occurrence categories, frozen exclusions, preserved validators, 19-file hash comparison and no branch/stash cleanup are correctly separated. Neutral fixtures may replace old callers only when the test is genuinely provider-independent, not to remove real-provider qualification.
- **Upstream admission:** the exact accepted prerequisite source/build tuple is mandatory. The plan does not claim pending identity/lookup/details functionality has already passed independent review.
- **Qualification:** closed scenario census, non-admissible skips/missing binaries, real compiled reachability, honest unsupported cases, normal successes, loss recovery, replicas and fixture-specific performance evidence provide a sound gate structure.

## Re-review scope

Re-read the amended upstream row contract, mapping/graph derivation, dependency ownership and page sizing. No full plan restart or test suite is necessary at this design checkpoint. Once B1/B2 and the namespace spelling are resolved consistently, this plan can proceed to task slicing.

## Completed re-review

Re-read final `contracts/provider.md`, `contracts/qualification.md`, `data-model.md` and affected `plan.md` clauses after the host amendments:

- Detailed rows now contract `opening_metadata` from the verified signed opening, including `{}` for legacy records and presence during current conflict. Only immutable ProjectID/WorkID fields are compared, so ordinary later payload edits do not become false mapping mismatches. The upstream implementer confirmed availability and was authorized to add the generic field; its independent qualification remains pending rather than presumed.
- Global inventory edges derive from detailed current states, including unmapped suppliers; the nonexistent global graph command was removed. Default detail page size is eight and cumulative bounds remain unchanged.
- Current dependencies derive solely from verified graph relations. The owned payload may retain initial dependency intent only as historical creation/replay provenance. Link/unlink changes graph semantics while preserving title, criteria, context, scopes and opaque metadata; whole-definition replacement remains unsupported.
- Namespace spelling is now `go-kitty/<name>`, consistent with Hubnot syntax.

No remaining planning blocker. Preserve two distinctions in task prompts and acceptance: inventory supplies current definitions plus a common snapshot proof, not the complete known-reference history/provenance proof; `creator` is the opening signer, not necessarily a current head's signer. Known-reference heads/graph reads supply current revision attribution. Also, replay of a previously completed definition-creation saga must recover original child-operation evidence without re-adding a blocker that a human subsequently removed. The existing shared replay and graph-derived-current-state design supports this; test it explicitly.
