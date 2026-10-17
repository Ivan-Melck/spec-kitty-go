# Genuine Host EOF / real daemon-timeout controls

Status: **new native5/5 PASS; bounded independent result review APPROVED**.
All four whole observation deliverables remain OPEN. This is a separate new
four-fault campaign, not a retry or replacement of E5/E8 or the prior13-outcome
corrected census campaign.

Source: `internal/productionruntime/expiry_qualification_release_native_test.go`.
Only `TestExpiryHostNativeEOFAndDaemonTimeoutControls` is registered by the fresh
fixed Go-built harness; no old native suites are rerun. No product source, hook,
capability, image/input policy, arbitrary selector or admission factory changed.

## Exact evidence

Cache: `expiry-host-release-controls-lrq6u13i/` under the usual resume-followup
parent. Source/driver bounded approval: reviewer `7ff6cb77-6c08-4e7`; original
review and clarification of abbreviated overlay/image wording preserved.

- Config SHA256 `d6dc7209021e35cc09d8282ba9f466f00e70c82d65f602b13162b2c2b3b46e0a`.
- Driver SHA256 `af661fffb89cd3c3ec35c5edcc39fcf42a8716df40c2f2668bf2f2a72e0a2e63`.
- Selected1017-input manifest SHA256
  `4edcd2a3f5adf92d36e0d4f44a6f0ff27060a56f00418f6e601c32b7b690b5d9`.
- Actual Go1.26.7/linux/amd64, GOAMD64=v1, CGO_ENABLED=1, fixed qualification
  build tag; actual module/SDK+h1 and ordinary non-race/non-coverage metadata
  inspected. Selected-input custody is not hermetic compiler/ABI attestation.
- One build and ONE native attempt. ELF SHA256
  `0309b9119a9873beb4ceef15494a7ac06e94ea02cb5f3639ece996706ceddf8a`;
  same1017 selected inputs/ELF before and after native. Native child exit0,
  no timeout/primary error, both actual stream fsync/first-close errors nil.

| Actual fault case | Held→expiry durable revision | Matched private/publication | Parent closed BEFORE advance |
|---|---:|---:|---|
| EOF/private_only |3→4|6/0|true|
| EOF/actual_ACK |5→6|6/7|true|
| daemon_timeout/private_only |3→4|6/0|false|
| daemon_timeout/actual_ACK |5→6|6/7|false|

Four original Host starts/four roots are a **source-assertion census**, not new
lifecycle/syscall telemetry. ACK cases use actual initial+continuation coordinator
ACKs. Baseline and final proof both assert exact disjoint private/publication
counts. Each authenticates H/S1 closure before the fault, then original owner/db/
start identity, real durable expiry, extra not-found and refreshed normal proof.

EOF closes the original parent gate once intentionally. Daemon-timeout leaves
it open and sends no R; it passively polls WITHOUT `CheckHeld`/gate operations or
caller cancellation until durable advancement. In particular the shorter12s
local check cannot cause premature EOF before the actual15s daemon deadline.
Held revision is captured BEFORE EOF Close, avoiding already-advanced baselines.
45s context is a cooperative fixture stop, not an SLO/latency claim.

Neither control sets `released`/`matchedAfterExpiry` or earns R/A qualification.
Each final close must reject `expiry_scenario_incomplete` AND independently show
nil full owner/Host/gate/root cleanup outcomes, canonical terminal/quiescent
control, recorded nil first owner/Host/gate outcomes and named parent/data/lock
root `[1 1 1]` triples. A scenario rejection cannot excuse cleanup failure.

## Transcript / custody audit

The separate pure `verify_release_controls` contract requires exactly five
RUN/PASS outcomes (one parent, four cases), four source-bound distinct roots,
four passive-revision/count/mode/parent-close receipts and four complete named
first-cleanup receipts. Old thirteen-outcome `verify` remains separate and
mandatory for its own evidence. Synthetic omission/duplicate/overflow/binding
controls run alongside original controls: twelve Python tests PASS. A green
smaller contract cannot substitute for the old larger suite.

Independent actual-result review `7ff6cb77-6c08-4e7` bounded-APPROVES the
observed transcript/source assertions. Original review and exact artifact hashes
are retained in `independent-result-review.txt` / `bounded-result-acceptance.json`.
This is not direct lifecycle telemetry or whole-contract acceptance.

Actual `transcript-check.json` matches this new contract. Four `roots-lstat-only`
observations read NO root-entry contents. One parent LSTAT-derivation script
initially used a nonexistent `roots` checker key after emitting a successful
transcript receipt; `parent-postprocessing-first-error.json` preserves that error.
Correct derivation used `roots_observed`. No checker edit, native retry or
rewriting of original child outcome occurred.

Native stdout's observed40.91s total /2.77,2.93,17.48,17.72s case durations are
functional test diagnostics only, NOT performance qualification or new accepted
thresholds. Exit is not a final-stream or OS descendant-containment certificate.
No historical ROOTS contents, unknown-provider cleanup/signals, release/promotion,
checkpoint replacement, full memory/negative/performance or goal/Op completion.
