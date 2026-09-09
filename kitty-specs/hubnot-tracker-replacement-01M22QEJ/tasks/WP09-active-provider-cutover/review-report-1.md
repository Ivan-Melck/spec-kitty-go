# WP09 independent review — PASS

Reviewer: `codex-independent-cutover-review`, acting under resolved `reviewer-renata` governance. The generated prompt's inherited implementer profile is template context, not this reviewer's role. The reviewer made no application changes.

Approval reference: `1ef3a97ab68733ad86f801b3a3c0e1f995cf3555`.
Qualified application source: `81f3d26a8000e222ca9bbf99f754ff936890ed4f`, tree `ffc704b6c469782f399d3289020f50b986a48f46`.

## Source and coordination

All 70 owned path states were independently checked against the final evidence index. The later approval reference changes exactly six planning/runtime documents: dossier snapshot, analysis report, lanes, plan, tasks outline and package manifest. Their before/after hashes and canonical-source equality are recorded in `planning-reconciliation.json` and the independent `formal-final-binding.json`. Application bytes are identical. Receipts remain bound to the clean qualified source; they are not relabeled as qualification of the later planning commit.

Inherited approved provider/consumer and qualification dependencies were checked separately; WP09 does not take credit for their implementation. The approved WP07 measurement refresh remains byte-identical in the dependency merge. The two subsequent WP09 corrections affect only Makefile/census tests and removal of one unused historical-link wrapper.

The first review claim correctly refused two unclassified paths. Canonical commit `b906ffef3d6c3ef0a2f7d305dfb3512be988b750` adds only literal `manual_review` exceptions for Makefile's atomic active dispatch switch and deletion of the retired installer. Prior classification bytes remain an exact prefix; no default or historical exemption changed. The failed claim and the subsequent normal successful claim are retained. Runtime dossier changes and the lane review lock are preserved.

## Requirement coverage

| Requirement | Verdict | Behavior and evidence |
|---|---|---|
| FR-011 | PASS | Live Beads adapter, qualifier, installer and workflow are removed; active Make, campaign, CLI guidance and hosted wiring select the compiled Hubnot installer/consumer. Actual normal Make installation, repeat installation, unrelated-destination refusal, help and identity selection pass. There is no fallback or dual-write path. |
| FR-014 | PASS | Offline portable/adversarial selectors contain exactly 2/32 top-level tests. Hosted/full/identity selections remain fixed at 27/28/5 rows with explicit non-full selection semantics. Independent missing/extra/skipped/zero-match transcript probes exercise the actual census verifier. The real wrapper and compiled public identity selection run successfully. |
| FR-015 | PASS | No original provider data, stashes or old branches are imported, rebound or deleted. Root's independently executed preservation report checks all 19 original files and original refs/stashes against their recorded baselines, including nonempty historical recovery material despite zero current issues. |
| NFR-003 | PASS | Exact-source complete Go quality, race campaigns, focused cutover/control races and external all-package build pass with the qualified toolchain. Real public execution is distinguished from negative fixtures and offline unit tests. |
| C-004 | PASS | All 829 frozen historical paths remain preserved. Historical evidence validator relocation is byte-for-byte. Phase2/3 corruption tests still exercise mutated explicit historical inputs; current frozen publication files, checksums and census remain checked. Historical 16 exemption rows retain their digest, while the live 11 grant set removes precisely five obsolete edges. Actual evaluator tests reject all five withdrawn edges. |
| C-005 | PASS | Current docs distinguish tracker-only operation from unchanged unavailable mission composition and unfinished M3. No control-store migration, M2 adoption, API 1.3 restoration or full API 2.0 claim is introduced. |

## Anti-pattern checklist

1. **Dead code — PASS.** New campaign profiles and dispatch branches have actual CLI/Make/workflow callers. The explicitly historical validators retain active integrity-check callers. A discovered unused 13-line wrapper was removed; live historical/current link validators and their call sites are unchanged.
2. **Synthetic-fixture test — PASS.** Actual compiled installer, consumer, normal campaign wrapper and documented create/read requests were exercised. Negative census, documentation and dependency fixtures call the production validators/evaluator; literal fixtures are not presented as positive provider qualification.
3. **Silent empty return — PASS.** Missing historical Git objects, invalid selection, incomplete census and missing provider prerequisites refuse explicitly. No new empty-success fallback replaces unavailable evidence. Full-history checkout is an explicit prerequisite, with no current-file fallback for historical grammar.
4. **FR coverage — PASS.** The requirement table identifies behavioral assertions for every declared reference. Local selected proof remains separate from final mission-wide qualification.
5. **Frozen surface — PASS.** Owned-path and historical-byte audits found no frozen-source or receipt rewrite. Historical tuple/digests and the unowned deletion census remain unchanged. Root's actual 829-path/19-file/ref preservation result is attributed separately from this reviewer's own audits.
6. **Locked decision — PASS.** Compiled installation remains canonical; no redundant shell launcher or scanner exemption expansion is introduced. Selected hosted evidence cannot pass the full-local validator. There is no authority coupling or automatic migration/adoption.
7. **Shared-file ownership — PASS.** Application edits match the 70 owned path states. Approved dependency refresh, the six exact planning-only reconciliations and two additive path classifications are explicitly coordinated above; no unreviewed shared application edit is hidden in them.
8. **Production fragility — PASS.** An actual normal-run failure exposed missing evidence-parent preparation; all three Make targets now create only the parent before dispatch. The qualifier retains exclusive ownership of the new scope directory, so existing runs cannot be overwritten. Missing prerequisites and invalid histories fail with explicit evidence rather than panic or silent success. Bounded tests verified setup ordering and preservation of existing receipt bytes.

## Verified gates and retained evidence

All paths below are relative to `/home/lynn/Documents/Codex/2026-09-09/h/` unless stated otherwise.

- Owner final index: `work/go-wp09-evidence/81f3d26a/final-evidence-index.json`.
- Complete `make quality`: exit 0, 540.968 seconds; GapDB race 290.245 seconds. Independent audit verifies nine exact-source receipts and seven raw censuses: 120 top-level tests and 549 subtests, with no required skip, failure, omission or duplicate. Full log SHA-256: `5935dc6f517a6f46b7e593a5e5439f71231fb4509235e5c310df829b929908a4`.
- Campaign CLI race: exit 0, 2.294 seconds. Cutover/control race: exit 0, 108.706 seconds. External all-package build: exit 0, 0.596 seconds. Exact invocations, source, exits and log hashes were checked in `work/go-wp09-review/formal-final-binding.json`.
- Actual normal identity selection: all five rows pass, explicitly non-full; source and reproduced binary identities agree. Independent audit verifies 89 commands, 267 durable records and 482 artifact files totaling 32,818,951 bytes. Manifest SHA-256: `756e08ef5bd01b54731f5bbd9ff234fdc28065cde2ab121a52e15527b73448a4`.
- Offline contract run: exact 2 portable and 32 adversarial top-level tests pass with provider activation removed and no retired provider executables on PATH.
- Independent bounded census race: 1.056 seconds. Historical/live-guard race: 1.141 seconds. Exact historical-exemption test: 1.033 seconds. Actual documented create/read smoke preserves the returned large revision value; Make parent-preparation probes preserve existing evidence bytes.
- Root preservation result: `work/go-kitty-preservation-WP09-81f3d26a.json`, pass for 829 historical paths, 19 original files and recorded branches/stashes.
- Independent reports and scripts: `work/go-wp09-review/{bounded-review.md,final-initial-evidence-review.md,final-quality-evidence-review.md,formal-final-binding.json}` and their referenced raw audits.

Earlier failures remain evidence: the 42e732d9 normal-run missing-parent failure; the 9c2e077e unused-helper static failure and explicitly terminated incomplete quality run; initial classification/readiness refusals; and probe harness corrections or zero-match attempts. None is relabeled as passing qualification. Later normal readiness and review events take precedence over the final index's preserved earlier readiness snapshot.

## Limits and verdict

PASS for WP09, with no unresolved package blocker. This is local cutover approval, not hosted-run or branch-protection completion. WP10 still must perform final integrated full-local 28-row qualification, including the 1,000-issue and staged-resolution obligations. Earlier measured candidates do not qualify this source by relabeling.

The optional Python review gate reports `no_coverage`; existing outbox/publication warnings remain separate from the verified Go gates and local verdict. M2 remains unadopted and M3 incomplete. Record approval normally with the exact approval reference above and no force flag; do not advance another package in this review.
