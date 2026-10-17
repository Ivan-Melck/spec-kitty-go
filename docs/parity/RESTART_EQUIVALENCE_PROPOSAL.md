# Proposed restart equivalence contract — not adopted qualification

Status: **proposal only**. No frozen M2 oracle, source hash, manifest, receipt,
mission status or production operation census is changed by this document.

## Concrete ambiguity exposed by complete progress

After the progress byte-budget correction, the real two-work restart scenario
reaches terminal restoration. The diagnostic compares **46 identical shared
items** with a restarted trace containing **ten additional durable execution
rebind operations**. The test driver deliberately invokes those named capability
commands after epoch changes, verifies preserved actor/lease/generation/fence,
and waits for acknowledged state. Business summary, work counts, tracker and
repository outcomes, family coverage and separately collected application-command
result bodies match. Real-Git crown has a similar reported comparison failure
but has not been independently diagnosed.

The existing terminal item equality mixes latest business state with historical
operation receipts. Exact equality of two different acknowledged command histories
is not the same contract as equal terminal business outcomes. Removing those
receipts from production would make the test green by breaking progress.

## Alternatives requiring a recorded qualification decision

1. **Exact command-history equality:** drive both traces through the same named
   recovery commands and epochs. This ceases to compare an uninterrupted trace
   against restart-at-every-boundary; it changes the scenario's falsification.
2. **Trace-aware equivalence (recommended for review):** compare exact terminal
   business facts and common scenario-command results, while independently proving
   the complete acknowledged recovery history of each trace. Never whitelist an
   operation merely because its ID contains `rebind`.
3. **Ignore operation history:** rejected. It would lose the evidence newly made
   visible by correct membership, including extra/lost/unattributed operations.

## Proposed non-vacuous proof obligations for alternative 2

- Keep the complete public `Items` and `Operations` census and immutable binding
  scope. Every observed operation must match either an exact common-command
  receipt or an exact acknowledged recovery-command receipt for that trace.
- Capture a recovery receipt only at successful named execution/workspace rebind
  completion, with operation identity, before/after authority coordinates,
  current epoch, exact committed result kind/body/digest and read-only host
  inspection. Startup reconciliation needs its own post-admission attribution;
  a caller-supplied ID list is not evidence.
- Verify actor, process, work revision/work/run/lease, generation, fence and any
  workspace binding invariants; only the declared epoch/heartbeat recovery
  changes are allowed. Existing stale-writer/zombie refusals remain independent.
- Compare latest non-history business facts exactly. Preserve common-command
  operation result comparisons, tracker attribution, repository targets and
  publications, work counts and event-family coverage.
- Keep a semantic outcome digest and independent per-trace history commitments;
  do not pretend different valid histories have identical history hashes.
- Negative controls must reject: omitted/duplicate/fabricated recovery receipt,
  fake `rebind` name, missing common operation, cross-scope operation, swapped
  actor/lease/generation/fence/result, extra unrecorded operation and weakened
  business terminal state. Exact public census and receipts must still fail if
  membership reporting is removed.
- Re-run both the portable two-work and real-Git crown against the corrected
  contract, including full restart and failure/refusal variants. The two-work
  diagnostic is not evidence the real-Git mismatch has the same cause.

## Admission and proof boundary

This is a proposed qualification-source contract refinement, **not** a verdict
that either failed test can be ignored. Independent review and a recorded choice
must precede altering that oracle. Any adopted refinement consumes a fresh clean
candidate identity and source-bound qualification; original M2 acceptance bytes
and adopted proof stay immutable. M3/M4 entry and later M5 API decisions remain
separate. See [finding 006](QUALIFICATION_FINDINGS.md).
