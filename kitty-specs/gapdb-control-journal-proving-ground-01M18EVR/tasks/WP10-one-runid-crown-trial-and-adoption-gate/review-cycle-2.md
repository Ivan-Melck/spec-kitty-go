---
affected_files:
  - cmd/spk-m1-proving-ground/main.go
  - internal/architecture/policy.go
  - internal/architecture/policy_test.go
  - internal/testcampaign/m1/adoption.go
  - internal/testcampaign/m1/adoption_test.go
  - internal/testcampaign/m1/campaign.go
  - internal/testcampaign/m1/evidence.go
  - internal/testcampaign/m1/evidence_test.go
  - internal/testcampaign/m1/faults/crown.go
  - internal/testcampaign/m1/faults/crown_test.go
cycle_number: 2
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command: >-
  /tmp/spk-m1-crown-e05a5e4.otzDbZ/bin/spk-m1-proving-ground verify
  --repo /tmp/spk-m1-crown-e05a5e4.otzDbZ/source
  --sidecar /tmp/spk-m1-crown-e05a5e4.otzDbZ/bin/gapdbd
  --admission /tmp/spk-m1-crown-e05a5e4.otzDbZ/source/docs/programs/evidence/m1-admission.json
  --policy /tmp/spk-m1-crown-e05a5e4.otzDbZ/source/kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts/m1-product-budgets-v1.json
  --runtime /tmp/spk-m1-crown-e05a5e4.otzDbZ/runtime
  --data /tmp/spk-m1-crown-e05a5e4.otzDbZ/data
  --evidence /tmp/spk-m1-crown-e05a5e4.otzDbZ/evidence
  --run-id m1-crown-e05a5e4-20260901T224500Z
reviewed_at: '2026-09-02T08:50:26Z'
reviewed_commit: e05a5e46feb3e65ff42c4544c16678c440ef3818
reviewed_tree: c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73
reviewer_actor_id: reviewer-renata-cycle2
reviewer_agent: codex-wp10-review-cycle2
reviewer_process_id: codex-subagent-m1-wp10-review-cycle2
verdict: approved
wp_id: WP10
---

# WP10 review cycle 2 — approved

**Verdict: approve.** Both cycle-1 blockers are closed at the exact qualified
source, and the fresh one-RunID crown is schema-valid, semantically valid, and
independently reproducible from its immutable source and artifact bytes. No
human adoption decision was created; M1 technical completion remains separate
from production adoption and M2 remains unadmitted.

## Cycle-1 blocker disposition

1. **Frozen identity and schema mismatch — cleared.** The product runner maps
   only the three observed runtime triples to the frozen evidence vocabulary
   `A/A/W1`, `B/B/W2`, and `A/C/W3`. Foreign, crossed, and already-projected
   triples fail. Both publication and verification compile the content-addressed
   frozen contract bundle and validate the exact canonical manifest bytes. The
   bundled evidence and ordered-trial schemas are byte-identical to the exact
   source checkout and have SHA-256 values
   `ad94828f0e175f120c194135124a0290d24e4aeb79fea3a5217ef5756fa6b5fd`
   and
   `db78bdb59d45fc91c6bf3c0f9fcf6bce85d5d2c3366a0d317be991541c00ea3d`.
2. **`approved-local-reference` unreachable — cleared.** The frozen signature
   closed set is mirrored in production. Local approval requires an explicit
   operator-supplied reference and the SHA-256 of the exact canonical adoption
   envelope. Absent, foreign-envelope, foreign-reference, duplicate, mixed
   detached/local, and post-approval mutation cases refuse. Detached Ed25519
   behavior remains intact. The JSON Schema compiler is admitted only in the
   quality tier; kernel and look-alike imports remain architecture violations.

## Exact crown evidence

- RunID: `m1-crown-e05a5e4-20260901T224500Z`
- Source commit: `e05a5e46feb3e65ff42c4544c16678c440ef3818`
- Source tree: `c7b932b3d85833beee1a84d8a1ae7f4e4a75ec73`
- Manifest SHA-256:
  `397f93a9b0f1ea24715ba57b0cb58aeb489e13a7e27e8bc76259597f36f2f309`
- Technical signature:
  `spec-kitty.m1.machine-pass.v1:7e6f5be2e8fd54f778a98b0f0b4f9dec011cd872da04e61563d69e8dbc6e6d0c`
- Proving binary SHA-256:
  `d68ed1668a908491e41aae60f7dd92f71b064188249f9a27d7e60a3b56569c19`
- GapDB binary SHA-256:
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`

The independent read-only verifier exited 0 with `outcome: pass` and
`adoption_eligible: true`. Direct byte inspection confirms 14/14 terminal
passing scenarios, 5/5 terminal passing faults, eight focused summaries with
100 started/terminal/passed repetitions each, 19/19 killed mutants, one
terminal passing ordered trial, exact `A/A/W1`, `B/B/W2`, `A/C/W3`
identities, all eight universal invariants true, two contributor-independent
approved reviews, serialized publication of both candidates, distinct runtime
epochs, predecessor-capsule refusal, equal restore/playback digests, and zero
playback adapter calls or durable writes. All 77 manifest artifacts have unique
paths and digests. The original evidence manifest and independent inventory
remained byte-stable while mutations were tested on disposable copies.

## Adversarial replay

Each required mutation eliminated pass with verifier exit 12:

| Mutation | Independent verifier disposition |
|---|---|
| Remove one scenario | Frozen schema rejects 13 rather than 14 |
| Replace W3 with W1 | Frozen ordered-trial schema requires W3 |
| Reuse the predecessor epoch | Crown ledger differs; semantic validator also requires distinct epochs |
| Make a reviewer equal its contributor | Crown ledger differs; review validator independently rejects same-ActorID review |
| Add one playback adapter call | Frozen schema requires zero |
| Mark `split-batch` as surviving | Frozen schema requires all 19 mutants killed |

Focused semantic tests additionally prove that changing both claimed fields and
their surrounding representation cannot bypass the crown-ledger digest,
artifact binding, schema, or semantic predicate.

## Validation

- Exact crown publisher: **PASS**.
- Independent exact-source verifier: **PASS**, exit 0.
- Focused M1, fault, crown, proving CLI, and architecture packages: **PASS**.
- Focused race campaign for M1, fault, crown, and proving CLI packages:
  **PASS**.
- Repository `quality-core` at the exact source commit: **PASS** (unit,
  contract, acceptance, race, vet, staticcheck, module verification,
  vulnerability, architecture, and scope gates).
- Exact diff check and frozen lane source before lifecycle-only status changes:
  **PASS**.
- No accountable-human adoption record: **confirmed**; state remains
  `not_adopted`.
- No M2 code or automatic backend-selection mutation: **confirmed**.

## Anti-pattern checklist

1. Dead code: **PASS** — the CLI calls the crown, verifier, schema, and
   adoption paths; the crown calls the real external, ordered, focused, mutant,
   publication, and verification paths.
2. Synthetic-fixture test: **PASS** — the decisive evidence came from the real
   external GapDB sidecar and production runner; fixtures are secondary mutant
   controls.
3. Silent empty return: **PASS** — no new silent-success fallback was found;
   incomplete and recovery paths publish or return explicit typed failure.
4. FR coverage: **PASS** — the exact crown and its evidence census exercise all
   WP10 requirement references, with focused repetition for each declared NFR.
5. Frozen surface: **PASS** — frozen schemas were not changed to fit output;
   the implementation validates their pinned exact bytes.
6. Locked decision: **PASS** — no embedded owner, SQLite fallback, unsigned
   adoption, force-ready waiver, automatic M2 start, or agent-as-human signature
   path exists.
7. Shared-file ownership: **PASS** — all broadened files are covered by the
   WP10 scope-reconciliation record and retain their narrow proof-preserving
   purpose.
8. Production fragility: **PASS** — fail-loud paths carry bounded diagnostics
   and preserve durable facts; no transient-race panic or silent recovery was
   introduced.

