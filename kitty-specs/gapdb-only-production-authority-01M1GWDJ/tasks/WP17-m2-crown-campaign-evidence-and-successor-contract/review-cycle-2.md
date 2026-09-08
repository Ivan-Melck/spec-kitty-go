---
affected_files: []
cycle_number: 2
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-08T03:39:16Z'
reviewer_agent: user
wp_id: WP17
---

# WP17 independent review — cycle 2

Reviewer: `reviewer-renata`  
ProcessID: `3753728`  
Profile revision: `d5c9862368f056879cc80f3feded73d677cdc582e7f46eb260e8ed634669aa09`  
Reviewed source: `df41eaf49a50e608220682467d34d688f97fea01`  
Reviewed tree: `983870ea482420b7ba57cb093be213a9a2bd85e2`

Verdict: **reject**

## CRITICAL — the required independent-review release cannot be finalized

The immutable producer archive verifies successfully against every supplied
external anchor, but it is 86,308,864 bytes. `FinalizeIndependentReview`
embeds that complete archive as the single outer member
`producer/producer.tar`. The shared deterministic archive writer rejects every
member larger than `maximumFile`, currently `64 << 20` bytes. Therefore the
required independent-review finalization path is unsatisfiable for this exact
otherwise-admissible producer archive.

Evidence:

- producer archive SHA-256:
  `0b9b1ecccba6db4e7b1b5962b2bcecc990a3c966975baeb754d84fedd7f395d4`
- producer archive size: `86,308,864` bytes
- limit: `67,108,864` bytes (`internal/testcampaign/m2/evidence.go`,
  `maximumFile = 64 << 20`)
- failing enforcement: `internal/testcampaign/m2/archive.go`,
  `deterministicTar`, which rejects `len(member.data) > maximumFile`
- finalizer composition: `internal/testcampaign/m2/archive.go`,
  `FinalizeIndependentReview`, which adds the whole producer archive as one
  member before calling `deterministicTar`
- retained command output:
  `/tmp/wp17-independent-review.pJcR5S/finalize-review.json`

Exact result:

```json
{"schema_version":0,"run_id":"","source_commit":"","source_tree":"","manifest_sha256":"","producer_archive_sha256":"","review_receipt_sha256":"","release_archive_sha256":"","review_eligible":false,"release_ready":false,"adopted":false}
{"diagnostic":"unsafe archive member","outcome":"fail","schema_version":1}
```

Required repair:

1. Give nested producer archives an explicit, closed outer-member budget that
   admits the bounded producer evidence already accepted by
   `VerifyProducerArchive`, without weakening individual extracted evidence
   member limits or the aggregate release budget; alternatively change the
   release representation so the verified producer archive is not one member
   governed by the individual evidence-file ceiling.
2. Add a mutation-sensitive test using a producer archive larger than 64 MiB
   but within the accepted aggregate producer/release budget. It must finalize,
   recursively verify, remain `adopted:false`, and still reject an archive over
   the new explicit bound.
3. Reproduce this exact immutable qualification and complete
   `finalize-review` plus externally anchored `verify-release` before returning
   WP17 to review.

All preceding independent checks passed, including exact producer
verification, both crowns, the complete 14-selector fault set (zero
failures/skips; 1,300 iteration receipts and 7,000 family-cut receipts), named
zombie/wrong-database/lost-ack/memory-loss/simultaneous-integration probes,
race, SQLite absence, deterministic SBOM, p95 recomputation, vet, staticcheck,
build, and module verification. Those successes do not waive the impossible
release boundary. No release archive was created and adoption remains false.
