---
affected_files:
  - internal/testcampaign/m1/campaign.go
  - internal/testcampaign/m1/evidence.go
  - internal/testcampaign/m1/adoption.go
  - internal/architecture/m1_evidence_semantics_test.go
cycle_number: 1
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command: >-
  GOTOOLCHAIN=go1.26.7 go run /tmp/spk-wp10-schema-review/main.go
  kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts
  /tmp/spk-m1-crown-e094765.7CV9EE/evidence/m1-crown-e094765-20260901T113600Z/evidence-manifest.json
reviewed_at: '2026-09-01T22:14:08Z'
reviewed_commit: e094765031cce47d95b0e9e93d6386cf72f318aa
reviewer_agent: codex-wp10-reviewer-final
wp_id: WP10
---

# WP10 review feedback — cycle 1

**Verdict: changes requested.** The one-RunID crown completed and the machine
verifier rejects all six required adversarial mutations, but the published pass
manifest violates the frozen JSON Schema and the adoption verifier does not
implement one of the frozen signature variants. A schema-invalid artifact cannot
be the exact admissible evidence record required by T053 and T057.

## 1. Blocker — the published pass manifest is rejected by its frozen schema

Locations:

- `kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts/ordered-trial-v1.schema.json:120-138`
- `internal/testcampaign/m1/campaign.go:162-168`
- `internal/testcampaign/m1/evidence.go:317-453`
- `internal/architecture/m1_evidence_semantics_test.go:568-580`

The frozen pass schema requires evidence identities `A/A/W1`, `B/B/W2`, and
`A/C/W3`. The production evidence type instead requires and publishes
`actor-a/process-a/workspace-crown-w1`,
`actor-b/process-b/workspace-crown-w2`, and
`actor-a/process-c/workspace-crown-w3`. Compiling the checked-in schemas and
validating the exact crown manifest produces:

```text
SCHEMA_REJECT: /ordered_trial/identities/implementation_a actor_id,
process_id, and workspace_id must be A, A, and W1; implementation_b must be
B, B, and W2; replacement_c must be A, C, and W3.
```

`VerifyEvidence` validates its Go semantic model, artifacts, and technical
signature, but never validates the manifest against
`evidence-manifest-v1.schema.json` and its `ordered-trial-v1` reference. This is
why the exact manifest can return machine `pass` while violating the contract.
The architecture schema test uses a separate synthetic map with the uppercase
contract values, so it does not round-trip the production encoder or a published
manifest.

Required correction:

- Preserve the frozen contract. Project the observed product identities into
  the exact evidence vocabulary `A/A/W1`, `B/B/W2`, `A/C/W3`; do not edit the
  schema merely to fit generated output.
- Make independent verification validate the exact manifest against the frozen
  evidence and ordered-trial schemas in addition to the semantic predicate.
- Add a round-trip regression that publishes evidence through the production
  path, validates those exact bytes with the checked-in schemas, then calls
  `VerifyEvidence`. The test must fail if the production constants drift from
  the schema constants.
- Re-run the crown under a new RunID and preserve the current run as a
  schema-invalid historical attempt; a changed source cannot reuse its existing
  technical signature.

## 2. Blocker — `approved-local-reference` is accepted by contract but rejected by the verifier

Locations:

- `kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts/adoption-record-v1.schema.json:28-35`
- `kitty-specs/gapdb-control-journal-proving-ground-01M18EVR/contracts/evidence-adoption-v1.md:81-88`
- `internal/testcampaign/m1/adoption.go:107-133`

The frozen adoption record and prose contract permit either a detached
signature or an `approved-local-reference`. `VerifyAdoption` unconditionally
requires `Signature.Kind == "detached"` and base64url Ed25519 bytes, so an
otherwise schema-valid approved local reference can never verify.

Required correction:

- Implement the approved-local-reference branch through an explicit,
  operator-supplied trust configuration and retain the exact source/evidence
  tuple checks, or obtain an explicit architecture decision that versions the
  contract before changing the allowed set. Do not silently accept an arbitrary
  reference string.
- Add one mirrored closed-set assertion and positive/negative round-trip tests
  for every signature kind admitted by the schema.

## Crown and adversarial evidence reviewed

The following evidence is valid and should be preserved while correcting the
contract blockers:

- Exact source commit
  `e094765031cce47d95b0e9e93d6386cf72f318aa`, tree
  `73aab7d0c2f7f017374495e0a4ec594a8dc16f94`.
- Original manifest SHA-256
  `09d7c29404873f499fd90559272f628f5e4fe50ed12ccfbac1b80467987ba272`;
  the complete evidence inventory hash remained
  `357d6b31f9d383655c298bc0054be01bd804ea0868866271b60a1cdcfa2cb1f0`
  before and after review.
- The read-only machine verifier returned pass with technical signature
  `spec-kitty.m1.machine-pass.v1:4f27a50a175e2eb9202a814e7eb8ef84317c443588af6ee6b9889dc04e4d2481`.
- Census: 14/14 terminal passing scenarios, 5/5 terminal passing faults,
  8 focused summaries at 100/100/100, 19/19 killed mutants, one terminal
  passing ordered trial, and 8/8 universal invariants true.
- Proving binary SHA-256
  `9a979e3c16affbd81d79fc79463bf7b1d0cb39b919335ffbcbf5cd5cc041d90f`;
  admitted GapDB SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.

Each mutation was made on a separate disposable evidence copy. The original
directory stayed byte-identical:

| Mutation | Verifier result |
|---|---|
| remove one required scenario | exit 12: `scenario census=13 want=14` |
| replace W3 with W1 | exit 12: crown ledger differs from manifest |
| reuse predecessor epoch | exit 12: crown ledger differs from manifest |
| reviewer ActorID equals contributor | exit 12: crown ledger differs from manifest |
| set playback adapter calls to one | exit 12: crown ledger differs from manifest |
| mark `split-batch` as surviving | exit 12: mutant survived |

Targeted architecture, evidence/adoption/crown, and proving CLI tests all pass.
The already-reported full `quality-core` gate also passed at the exact reviewed
commit. Those tests do not override the concrete frozen-schema rejection above.

## Adoption boundary and historical evidence

No human adoption record or authorized human signature exists. The only
signature is the machine technical-pass signature, so adoption correctly
remains `not_adopted`; no M2 work is authorized.

Earlier incomplete records remain material history and must not be overwritten:

- `c5abe01` incomplete manifest SHA-256
  `4114ef3d27357db5f78bf36291ebb04f58d9f7ec26fdf2042fe8c578c122a837`.
- `e1cbc8f` operator-interrupted/status-inference attempt SHA-256
  `83eaa9b6ad45f870dd980dc4b575d0b2ad13ebc321ddc46cd3c13f60ff091b2a`.

The final machine-pass run must join that honest history rather than erase it.
