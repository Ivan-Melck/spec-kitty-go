---
verdict: pass
review_scope: final-hosted-and-local-provider-followthrough
reviewer: codex-independent-reviewer-renata
reviewed_source: 782f75579a2349dd438c7519297cbb452425349f
reviewed_tree: 6e8c99fed859647695696b21112f4cfea9264093
---

# Final independent hosted qualification review

**PASS WITH NOTES.** The fresh normal local qualification passes all 28 required rows with `full_admissible=true`. The fresh hosted qualification passes its fixed 27 rows with `full_admissible=false`. The standard PR quality, race and M2 jobs also pass. These are actual completed executions on the source identified above, independently checked against retained raw evidence. Earlier unsuccessful attempts retain their original outcomes and source identities.

This supplements the mission review and the chronological `hosted-namespace-prerequisite.md`; it closes their pending hosted-execution and final trace follow-through at this measured source. It does not alter past approvals, claim main-branch merge, activate M3/M4, or change the existing M2 `adopted=false` state. Root owns subsequent publication, Op closure and dossier updates.

## Source and executable identity

The local and hosted receipts both bind clean commit `782f75579a2349dd438c7519297cbb452425349f`, tree `6e8c99fed859647695696b21112f4cfea9264093`, Go 1.26.7, Linux amd64/v1, CGO disabled and the recorded sanitized build environment. Reproduction uses the explicit compiler with `-trimpath -buildvcs=false -mod=readonly -ldflags=-s -w`.

| Executable | SHA-256 |
| --- | --- |
| Product | `ecd5fc241b72ea30afe8572fcfab40df99deb312b0ed90c415e48e6fcef7e898` |
| Current qualifier | `2671dce14c6a62b06f6967f33b20b391bc05b59d51319c9cb5de038886f8b2d5` |
| Accepted HN | `077058c8be07cfcd28994210ab483f739fb194b6db417f70b455d6c3a3524cf3` |

The HN source remains pinned to `baf350d12accd01d4d7e7221cf859c81b2c9622b`. The upstream PR3 merge does not silently move that accepted build tuple. All three retained local executable files were independently hashed and size-checked. Hosted receipts record the same identities and actual reproducible-build/install results, but the hosted archives do not retain these executable bytes; remote paths were never resolved to local substitutes. The qualifier differs from earlier e9/410d and 885d builds, so this report relies on the fresh782f qualifications rather than claiming old binary equivalence for it. The product and HN digests remain unchanged.

The five-file affinity implementation and its synchronized central/independent validators previously passed bounded review, including real child affinity/signal/exec tests, malformed/null/index/mask/tool/prefix refusals and explicit historical receipt compatibility. The post-execution `docs/HUBNOT_QUALIFICATION.md` update was separately reviewed: it documents actual tool prerequisites, diagnostic affinity, retained failures and strict historical/current forms. It is a documentation change after qualified782f, not a relabeling of its receipts. No Go source changed during this final evidence review.

## Fresh local full qualification

The normal external process exited0 in **934.408223 seconds**. Its exact receipt contains all 28 closed rows, `scope=full-local`, `verdict=pass`, `full_admissible=true`. Ten outer build/install/idempotence/smoke/full-run/audit records have actual terminal exit 0 with matching source/tree and log hashes. The full-run log SHA-256 is `78fbef497a05904a62b98e7e3520542c4848ecb093e85642eb52125278523020`.

Independent auditing verified **8,650 contiguous commands, 25,950 durable intent/launch/terminal records, 44,086 artifact references and 46,444 unique qualification files totaling 111,386,188 bytes**. It checked original argv/input/directory, source/run/scenario identity, confirmed starts, real terminal outcomes, bounded capture lengths, row/command intervals, orphan artifacts and worker/final receipt equality. There were zero integrity errors. Sorted manifest SHA-256: `1cf51474a1de1cd93f948bd7e5baaa8b23abba56f29198eb595f61ac21df3de2`.

Both explicitly enabled same-receipt tests, `TestHubnotIntegratedPublicAcceptance` and `TestHubnotIntegratedPerformanceEvidence`, actually ran and passed without skips; outer exit 0 in 2.670798 seconds, log SHA-256 `65128b9b8e3dfab9e6017c1d350be1f73cfb0a4a28ff9d5b348ea827d89bb3b2`. The independent raw supplemental census also confirms22 top-level tests and 30 subtests, each started and passed once. These audits inspect the fresh receipt; they are not a second provider execution.

The actual local preservation row records comparison of 19 original files in the original repository, alongside 829 frozen tracked paths. Root's additional fresh observation `work/go-kitty-preservation-hosted-782f7557.json` has the exact source/tree, authentic baseline hashes, zero failures and PASS for 829 paths, 19 files, five allocation refs, two other refs and two stashes. It was independently inspected, not rerun by the reviewer. Ref checkpoint provenance remains the recorded WP06 observation, not an invented pre-mission timestamp.

## Fresh hosted provider qualification

[Run34427078951](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34427078951), attempt1, completed successfully. Actual real-provider job102714496655 executed the wrapper; its raw required test started once, passed once and did not skip. Bounded job102714496432 also passed its actual fixed2+32 top-level tests. Final run/jobs API evidence binds published782f, and the actual wrapper report binds the clean same source/tree.

The original provider archive is 29,584,490 bytes, SHA-256 `5fc92b8a657dd2a328a6ad5eab1fe4ab5fa758ce4eed078cf331d1a60ef78b3a`. The bounded archive SHA-256 is `907a748d7f15d187e5a7d6df52648849bf50a0d11432ed5d6a5cc9fa34323619`. Both match their GitHub API digests; all 46,424 file members across these two archives were independently compared with extracted bytes. Additional downloaded job logs are retained separately.

The provider receipt has **27/27 PASS**, `scope=hosted-provider`, `full_admissible=false`. Its exact selection excludes only `cutover.local-recovery`; no absent local file is counted as verified. The complete manifest retains all 28 definitions, with actual selected row modes matching it. Public operations, public persisted-partial recovery and explicitly injected source-bound tests remain distinguished.

Independent auditing verified **8,640 commands, 25,920 durable records, 44,046 artifact references and 46,393 qualification files totaling 100,874,510 bytes**, with zero integrity errors. Sorted manifest SHA-256: `e4a1b7b09417890bc3bba65d95e531a6a88ca757f95b314d6f13996c026aa8ab`. The raw supplemental 22/30 census also passes. Hosted success is not substituted for the separate local preservation requirement.

Actual namespace evidence proves the reviewed fixed no-attachment named profile was added with `apparmor_parser --add --skip-cache`; exact policy bytes/hash, loader result and ordinary UID/GID 1001 dispatch were verified. The subsequent untraced probe proves the selected label, PID 1, expected UID/GID maps, distinct user/network/PID namespaces, denied setgroups and loopback-only interfaces. Global controls match their earlier readings. The diagnostic audit correlation remains explicitly `unestablished`; no PID arithmetic or invented lineage is used. The profile is scoped to the ephemeral job lifetime, with no global policy disabling or local reviewer privilege action.

## Complete semantics, timing and process census

Both fresh scopes independently pass the full 1,000-definition oracle: every portable field, exact numeric opaque JSON, 2,000 ordered criteria, immutable references, native/opening/current-head state, complete proof bytes/digest/revision/token, 20 blocking edges and 20 exact attributed comments. Complete traced and untraced results agree, excluding only valid consistent per-invocation observation timestamps. Known-reference and original-recovery proofs retain their closed kind and external context. Before/after sensors execute against the affected repository with valid ordered intervals and unchanged signed refs; recovery retains historical replay/prior-mutation facts.

| Scope and query | Untraced seconds | Diagnostic trace seconds | Verified exec census |
| --- | ---: | ---: | --- |
| Local inventory |64.684530|80.171659|125 detail pages,627 Git |
| Local known reference |65.893702|83.069721|130 HN,647 Git,777 child PIDs |
| Local original recovery |0.486442|0.605963|2 HN,7 Git,9 child PIDs |
| Hosted inventory |52.310392|49.880835|125 detail pages,627 Git |
| Hosted known reference |54.465121|51.687525|130 HN,647 Git,777 child PIDs |
| Hosted original recovery |0.373268|0.361943|2 HN,7 Git,9 child PIDs |

Counts were recomputed from complete raw execve records, including unfinished/resumed joins. Separate query counts contain one consumer and zero other/failed execs. “Child PIDs” means distinct PIDs with successful observed execs excluding the consumer, not a census of fork-only children.

Every current trace has the exact taskset→filtered-strace→original-consumer command form and consistent typed affinity metadata, including the explicit inventory index. Recorded parent masks are 0-11 locally and 0-1 hosted, requested CPU 0. The fixed taskset identity is recorded as scalar fields, SHA-256 `a431738fadddc2e2326c8ab81843e72f4011658379381aef1502a6ddb1c1115b`,31,104 bytes. This is not an uploaded-artifact claim or a separately sampled live child-affinity claim. Normal untraced affinity/environment, product behavior and 120-second bounds remain unchanged. Diagnostic traces are separate measurements; their CPU context is never presented as the normal production performance context. These are actual host samples, not a universal performance guarantee or ten-sample hosted baseline.

## Standard CI and retained history

[PR workflow34427081432](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34427081432) passes all three jobs. Actual synthetic checkout `d84bd758ba3fd9ced074fe700eafab357e595d7f` has the exact 782f tree and verified baseline94e/782f parents. Quality exited0 in206 seconds; five clean receipts and raw JSON independently show 86 top-level tests and 480 subtests, each required test run/pass once, no failure or skip. Race exited0 in582 seconds, including GapDB 373.192 seconds; M2 reports eight passing receipts and 19 selectors. Quality/race archives match API SHA-256 values `da0a96db6ab3ad1cae2dbe6271d69fde866339eb01bd255c85e34a90722a8454` and `29d87b1dc768e853bf0cb9b012b437b677e877d06d87ccfeb4146b6cb341969c`.

Race's retained raw job log shows package outcomes rather than per-test JSON. M2 retains result receipts and reported raw digests, but its underlying test streams are not uploaded. No independent per-leaf recount is claimed for those two jobs. This is the actual split standard CI workflow, not a claim that the older local nine-receipt `make quality` aggregate was rerun at 782f.

The original e9 full28 and410d postmerge quality remain historically valid at their own sources. The20dea aggregate failure and first architecture-fixture failures remain preserved. Hosted namespace refusals and canceled diagnostic attempts remain non-admissible. Runs34420202494 and34422599807 remain 25-pass/two-failure qualifications; the interrupted local885d attempt remains 26 durable rows with 25 passes, one failed inventory trace and unfinished remaining rows. Signal filtering alone did not resolve those trace deadlines. The retained scratch affinity experiment was diagnostic only. The current success follows the independently reviewed affinity correction and fresh completed executions; no previous outcome, timeout, scope or missing census was waived.

The FR015 contemporaneous reconnaissance/export provenance limitation remains explicit. No original data was converted or removed, no hosted archive is claimed to contain private original databases, and no unsupported Python CLI gate is labeled PASS.

## Review evidence and ownership

Independent scripts, raw verification results and manifests are under `work/go-hubnot-affinity-review/782f7557/{local,hosted}`; standard CI review is `work/go-hubnot-hosted-review/34427081432/review.md` (SHA-256 `dcd3fd84927eebfdae1a2b95e3a334ffc88d042977e15ea3bca43b60de5a7cc5`). Original archives, owner indexes and execution records remain in their source/run-specific directories. The reviewer made no provider calls, broad test reruns, source edits, policy actions, runtime transitions or commits. Only this canonical review report is authored under root's released ownership.
