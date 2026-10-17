# Expiry Host fixture — publication census correction

Status: **TEST-SOURCE CORRECTION IMPLEMENTED; whole observation contract OPEN.**

## Actual E8 failure review

The requester authorized the proposed review and subsequent implementation, with
that authority extending to other areas. Five metadata files were acquired once
with no-follow bounded reads into:

`/home/user/.cache/spec-kitty-parity-resume-followup/e8-authorized-failure-review-42er4nac/`

`acquisition.json` records original paths, identities and SHA256 values. The actual
`native.exit.json` records an executed `/proc/2829763/exe` matching the E8 built
ELF digest, returncode1, no timeout, and nil first-close/enrichment errors. This
resolves the prior UNKNOWN ELF-attempt/status, not descendant containment.

A separately acquired **1204-byte prefix only** of `native.stdout` reports:
- `initial/private_only` PASS;
- `initial/actual_ACK` fails at `fixture final publication rows=7 ACKs=2`;
- failed-scenario cleanup correctly refuses `expiry_scenario_incomplete`.

The prefix digest equals the stdout observation in the native exit receipt.
It is not a final transcript or complete census. No historical ROOTS contents,
provider data, native stderr or old executor were inspected/executed. No original
E8 artifact was modified, retried or repaired.

## Correct contract, not a relaxed production check

`publishLiveProofOperation` and `deriveSealedRecords` establish:

| Role | Initial | Continuation | Final distinct addresses |
|---|---:|---:|---:|
| operation binding | 1 | 0 | 1 |
| journal event | 1 | 1 | 2 |
| operation head | 1 | 1 overwrite | 1 |
| workspace claim | 1 | 0 | 1 |
| publication witness | 1 | 1 | 2 |
| **Publication subtotal** | **5** | **3** | **7** |

The initial Host also has six private metadata rows: total13, not publication13.
A restarted Host has nine private rows: total16 for the same publication history.
The prior hardcoded expectation confused these disjoint categories.

Changed `internal/productionruntime/expiry_qualification_integration_test.go`:
- pass an explicit expected publication count to the common TTL closure;
- require exactly7 for the known initial+continuation ACK fixture, exactly0 for
  private-only; preserve separate private and total equality checks;
- the reconciler derives its expected addresses from actual captured sealed
  records and requires a nonempty publication footprint; ACK count alone never
  selects an unrelated record shape.

New `internal/productionruntime/expiry_qualification_publication_census_test.go`
uses the exact native publication helper with an in-memory commit port and real
coordinator-minted ACKs. It checks exact initial/continuation roles, same-head
changed-value overwrite, distinct historical event/witness addresses and empty,
initial-only, continuation-only, complete, reversed and duplicate visitation.
Both same-epoch and successor-epoch fixtures run. This is a pure fixture regression,
not a simulated Host promoted to native evidence.

## Verification and review

- `census-red.log`: named constant still13; both epoch cases fail against the
  independently asserted7-row role/address census.
- `census-green.log`, `census-race.log`: corrected7 passes, no skips.
- `expiry-unit.jsonl`, `expiry-race.jsonl`: pinned Go1.26.7, selected expiry
  non-native tests, each34 top-level /98 test outcomes PASS, zero failures/skips.
- `census-vet.log`: tagged productionruntime vet passes.
- `conditional-surface-pinned.jsonl`: the existing conditional-surface guard,
  invoked with actual Go1.26.7, passes ordinary/enabled/arm64/windows/darwin source
  selection and AST checks. This corrects the earlier orientation toolchain
  mismatch for that guard only; it is not a claim that the full suite now passes.
- `source-comparison.json`:567 historical E8 Go source pins checked; only the
  corrected integration test differs. New regression is an additive file.
- Independent source reviewer `0ae8c30d-f783-4f3` eventually read the exact
  regression/helper/sealing source and bounded-APPROVED the arithmetic correction.
  Its earlier wrong-path/inaccurate response is preserved, not an approval.
  Three earlier reviewer launches failed billing before executing any tools;
  these provide no review evidence.

## Fresh campaign, separate custody

New corrected-source campaign:
`/home/user/.cache/spec-kitty-parity-resume-followup/expiry-host-census-correction-5gtllnwe/`.
Its1009-entry `source-manifest.json` includes current Go sources/modules, five
whole-file overlay helpers, a fixed testing.Main, pinned SDK/provider/bundle and
Go executable. It is a new manifest, not a retargeted old source review.

Fresh minimal pinned-Go1.26.7 build/profile/metadata commands exited0 with both
actual stream fsync/first-close outcomes nil. Built ELF SHA256:
`2339b7d0a94cde9496ab4a0335d9f440667067d0139143577d7992ed5d13ca77`.
Actual metadata identifies the new command, exact linked GapDB dependency and tag.
Source checks before/after cover1009 selected inputs, not an exhaustive compiler/
ABI/allocator dependency proof. No source/semantic evidence transfers from E8.

`campaign.py` has literal once-only build/native stages, exclusive markers and
receipts, source/config/ELF checks, a fresh short root, no old executor imports,
no retry, no cleanup/signals, and explicit UNKNOWN descendant/finality state.
A real exit-receipt masking gap found during review was corrected with
`ChildExitReceiptFailure` retaining the original child result and emission error.
Ten mocked Python fault controls pass without invoking any real child. The prior
broad reviewer BLOCK is preserved. Reviewer `198b9dcc-965c-40a` subsequently
bounded-APPROVED the corrected source/driver and independently ran the ten controls.
Parent bound the unchanged reviewed driver/config bytes in
`source-and-driver-review.json`; this is not an independent1009-input dependency audit.

### Actual fresh native result

The new Native stage ran **ONCE**, exited0 without timeout, and recorded nil first
fsync/close outcomes for both streams. `native.result.json` records unchanged1009
selected inputs and unchanged built ELF before/after. The observed stdout has
exactly13 RUN/PASS outcomes, zero skips/failures and11 distinct preserved roots.
Both private-only and actual-ACK paths pass for initial/clean/unclean modes; the
reconciler actual-ACK path passes. All four prior-revocation clean/unclean refusal
paths independently report first scenario rejection plus nil full cleanup/root
outcomes, canonical quiescent closed state and named root triples `[1 1 1]`.

Source assertions plus this observed transcript account for15 Host starts,
7 held-expiry/S1-refusal/release/fresh-match closures and4 refused successors.
Those are **source-assertion counts, not directly instrumented lifecycle telemetry**.
Only direct child return/parent closes and observations are recorded; no final
stream or OS descendant-containment certificate is inferred.

New pure checker `scripts/parity/expiry_host_transcript.py` requires the exact13
RUN/PASS census/order, source-bound11 root observations and every named first
outcome/cleanup receipt. It rejects skips/failures/scheduling/status injection,
missing/duplicate final PASS and malformed root/source observations. Six Python
tests include omission/duplication of all13 statuses and all24 negative receipt
lines. `native.transcript-check.json` binds checker/source/exit/snapshot hashes and
an LSTAT-only11-child root census; **no root contents were read**. Independent
reviewer `c645f6d0-bddc-441` read the actual transcript, exit/result receipts,
checker/tests and complete native fixture sources and **bounded-APPROVED** observed
transcript/native source assertions. It independently ran the checker controls.
`bounded-result-acceptance.json` binds the evidence and preserves the original
review, including a parent clarification of its shorthand:15 starts =6 original
cross-product +4 successors +1 reconciler +4 revocation predecessors. These remain
source-assertion counts, not direct telemetry. No unchanged native rerun occurred.

## Completion audit

| Deliverable | Evidence here | Still absent |
|---|---|---|
| Complete owned memory accounting | No new allocation claim | Exhaustive reviewed composition; `fullCallCoverage=false` |
| Genuine Host expiry | Independently bounded-accepted seven fresh real-Host held-expiry closures, restart/revocation controls and exact observed transcript | Remaining full-contract controls; no whole-layer qualification |
| Full negative matrix | Pure fixture counts plus existing selected controls | Complete required actual native matrix |
| Performance | No performance run or changed threshold | Private60/hash120/all10 and remaining workloads/inputs |

The inspected `AUTHENTIC_INPUT_DISCOVERY_DISPOSITION.md` in the historical parent
identifies a real all-ten-role input gap, not just missing execution permission:
B1 exact approved plan/project/mission and attributable provider/work definition
preimages; B2 a selected four-source governance context with exact revisions/order;
B3 owner-designated repository/commit/managed-root/namespace/branch and approved
physical path identity; B4 the exact capability intent/publication scope. Broad
implementation authorization does not supply these bytes or make synthetic
fixtures authentic. Fixed private/hash workloads and honest owned-cost research
can continue separately; they cannot close that authentic all-role requirement.

No complete goal, architect Op closure, qualification checkpoint, candidate
promotion, provider/admission change, ref movement or old-receipt transfer.
