# WP05 follow-up independent finding: quadratic discussion proof copying

Verdict: **REJECT / reopen WP05 for one newly demonstrated scalability defect**. This follow-up supersedes the prior approval for readiness purposes without rewriting or erasing that approval, its event, or the passing semantic/gate evidence. The independent reviewer made no application source changes.

Reviewed source: 20b2889a3264ce11c1b3a984a0693e34fffe2313. All six owned source hashes still match that commit and the originally approved candidate before/after the bounded probe. Root authorized this exact scoped reopen after reviewing the measurements.

## Finding

In internal/tracker/discussion_observation.go, every AttributedDiscussionEntry owns another clone of the same whole common proof. NewDiscussionObservation repeats full CanonicalFields copies for equality/completeness and constructs another full proof copy per row. Entries() again clones every whole proof. Hubnot discussionAttempt constructs one common proof covering N comments then N entries from that proof, so this is O(N * proof_bytes) retained memory and allocation; the common proof itself grows with N comments, yielding quadratic behavior. The 64MiB provider read budget does not constrain these subsequent in-memory copies.

A bounded synthetic neutral value probe used valid common canonical JSON of roughly1KiB per entry, at N=32,64,128. It measures actual TotalAlloc increments, and counts distinct private canonical backing arrays retained by the resulting observation. It does not claim a real public end-to-end fixture or invent provider performance evidence.

| Entries | Common proof bytes | Entry construction allocated | Observation construction allocated | Entries() allocated | Observation retained distinct proof bytes |
| --- | ---: | ---: | ---: | ---: | ---: |
| 32 | 32,779 | 2,633,464 | 5,342,960 | 1,325,512 | 1,081,725 |
| 64 | 65,547 | 9,462,536 | 19,047,112 | 4,737,024 | 4,260,573 |
| 128 | 131,083 | 35,706,608 | 71,635,960 | 17,872,072 | 16,909,725 |

The red regression TestIndependentDiscussionEntriesAvoidWholeProofPerRow allows a generous1MiB for Entries() at128 rows with a128KiB common proof. It fails with17,866,752 allocated bytes. The whole bounded probe completed in0.087s; no huge-memory campaign or broad gate was run. The different Entries totals above are normal small allocator/test overhead across separate measurements; both show the same full-proof-per-row cost.

## Required correction and boundary

Preserve the public neutral API and all existing attribution/proof semantics while sharing the immutable common payload internally. ProviderObservation already copies its input canonical bytes in NewProviderObservation, exposes only scalar/string/time fields plus a private canonical byte slice, and CanonicalFields returns a defensive copy. Therefore this WP's neutral values can retain/return ProviderObservation value copies without re-copying its hidden immutable payload. Entry slices still require independent copies; returned scalar fields must remain independently editable. Use private payload length and allocation-free equality inside this neutral file, with fast equality for the shared payload. Do not change frozen capabilities.go or the shared cloneProviderObservation helper, weaken mismatch validation, drop proof, expose a mutable canonical alias or expand port/authority semantics.

The narrow correction should fit discussion_observation.go and its existing test file, both already owned by WP05. WP06's wire result should avoid repeating whole proof per row, but a DTO-only fix is insufficient because construction and Entries already allocate quadratically before serialization.

## Regression requirements

- Keep a meaningful bounded allocation/retained-payload regression for construction and Entries at increasing modest N. Production common-proof input sharing should scale approximately with proof_bytes + entry_count, not their product. Avoid noisy timing thresholds or huge-memory tests.
- Mutating original constructor input bytes, input entry slices, returned entry slices, returned ProviderObservation scalar fields, and every returned CanonicalFields buffer must leave stored entries/common/current/original proofs unchanged.
- Retain refusal of duplicate entry IDs and incoherent common proof/revision/token/content. Preserve exact original append/replay proof, signer/author separation, current-work token distinction and all existing effect/recovery tests.
- Re-run the unchanged independent scaling probe and targeted semantic/race checks, then exact-source full quality and independent normal re-review. Do not alter the prior source-bound passing gate records.

## Reproduction and evidence

Working directory: /home/lynn/projects/go-kitty3/.worktrees/hubnot-tracker-replacement-01M22QEJ-lane-e

PATH=/home/lynn/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.7.linux-amd64/bin:/home/lynn/.local/bin:/usr/local/bin:/usr/bin:/bin GOTOOLCHAIN=local go test -count=1 -v -overlay /home/lynn/Documents/Codex/2026-09-09/h/work/go-wp05-review/scaling-overlay.json ./internal/tracker -run '^TestIndependentDiscussion(ProofScaling|EntriesAvoid)'

External source-bound evidence: /home/lynn/Documents/Codex/2026-09-09/h/work/go-wp05-review/scaling-{probe_test.go.txt,overlay.json,red.log,source-hashes.json,evidence.json}. No application/runtime change was made during the probe. Existing final-review.md/approval-event.json retain the original independent semantic approval; this newly demonstrated allocation finding is a separate follow-up.
