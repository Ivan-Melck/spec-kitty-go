# Original-WP13 Host: private64 match / private65 refusal method

**Authored method, NOT executed native evidence. Independent review of this exact
source and method is REQUIRED before any provider/performance execution.** The
author may run only the new ordinary unit controls, compile, and vet. Existing
dirty/reviewed files are frozen. This change owns only this document and
`internal/productionruntime/retained_private_chain_performance_test.go`.

This is the next fixed workload for the missing private-chain performance and
native-negative rows, not a declaration that those rows have passed. Prior small
workloads in [OBSERVATION_PERFORMANCE_RESULTS.md](OBSERVATION_PERFORMANCE_RESULTS.md)
do not transfer measurements to this source. The broader
[qualification contract](OBSERVATION_QUALIFICATION_PLAN.md) remains open.

## Exact construction and expected outcomes

`TestRetainedPrivateChainOriginalHostNative` reuses ordinary `Config`/`Start`
admission/composition from `retained_performance_test.go` and
`retained_records_proof_integration_test.go`. No fabricated Host, alternate
provider, injected reader, key/payload/count selector, publication writer,
product edit, policy widening, or expiry lane. `publicationProofFixture` is ONLY
the existing ACK observer; it is never supplied as an owner/provider. The normal
refusing capability dependencies are the same as the existing native fixtures.

One new durable authority/data directory and one runtime root are created under
the report's private parent. Start epoch1, then Close/Start through epoch64 on
that SAME data root; each uses a distinct `SupervisorRunID`. Every actual epoch
must be nonempty and distinct from ALL previous actual `RuntimeEpochID`s. Each
Start records actual lifecycle provenance, database, data/runtime directory,
admission digest and projection attribution. Assert the database ID is unchanged,
the data path is unchanged and still the original directory inode (`SameFile`),
Host is ready, attribution identifies that actual database/epoch, and observer
ACK count is zero. There are **no VerifyRetainedRecords calls at epochs1–63**.

| Case | Actual setup | Sequential observations | Required public result |
|---|---|---:|---|
| `private64_match` | 64 ordinary same-root Starts | 30 | nil error; actual matched private195 = 3*64+3; publication0; total195; exact attribution |
| `private65_refuse` | AFTER measuring64, Close/Start real epoch65 in SAME root | 30 | nonnil EXACT sanitized `*ProgressError{Kind: ProgressIncomplete, Field: "retained_records"}`; entire proof ZERO |

`195` is an assertion on the actual returned proof, **not a supplied count**.
Epoch65 traverses a genuinely constructed over-limit chain under the existing
private maximum64 policy. Its public boundary cannot disclose a private internal
failure reason; the expected max-chain explanation comes from construction and
source review, not from inventing a public reason code. No positive proof or
partial matched rows may escape on refusal. Canceled/deadline, wrong-field,
wrapped/content-bearing, other error, and nonzero error proof are unexpected.

Expected case outcome is separate from `Matched`. A valid refusal has
`matched=false`, `observed_class=sanitized_refusal`, `expected_outcome_met=true`.
It is NOT a timed performance failure. Unexpected outcomes append their actual
duration, observed class/error type and invariant values BEFORE terminating; no
retry/replacement/discard. Public error text is deliberately not copied from
unexpected observation errors. Setup and Close failures are retained separately.

On EVERY sample assert before/after ready state, unchanged original projection
object and attribution (also unchanged from the case baseline), stable database,
data directory and actual epoch, and zero ACKs. Record full public proof
attribution/counts, zero-ness, error presence/type, positive Matched and independent
expected-outcome status. Refusal must leave the ready Host/projection unaffected.
No writes, commands, refresh, probe, or publication run between samples.

## Measurement and report semantics

Only `VerifyRetainedRecords` is inside the `time.Now`/`time.Since` interval.
All 65 startup/restart operations, provenance/file checks, assertions,
classification, reporting and Close are outside it. Thirty first-inclusive calls
PER case; no warmup, benchmark calibration, parallel cases, retries, discarded
failures, forced GC, allocation instrumentation or caller-injected deadlines.
Existing cooperative method/private deadlines remain. Ordinary scheduler/GC,
monitor and cache effects remain included; this is not a hard-wall guarantee.

Every raw nanosecond duration and observation survives in the JSON report,
including an unexpected failing prefix. Statistics reuse the existing
`retainedPerformanceStatistics` helper: positive durations only; nearest-rank
median/P95 (positions15/29 for30), min/max/total; no interpolation. A refusal case
has its own durations/statistics; it is never pooled with the match case.
Statistics for a valid-duration failing prefix describe that prefix, not a
completed successful workload. Invalid durations prevent valid statistics.

Both opt-ins absent: ordinary native test skips. Exactly one present: FAIL.
Only these exact explicit opt-ins enable it:

- `SPEC_KITTY_WP13_GAPDBD`: original admitted WP13 executable; expected SHA256
  `947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad`,
  5,327,126 bytes. Normal historical assertion-extension admission is unchanged.
- `SPEC_KITTY_OBSERVATION_PRIVATE_CHAIN_REPORT`: NEW exclusive report pathname.

Require ordinary Go1.26.7, linux/amd64, explicit `GOAMD64=v1`,
`CGO_ENABLED=1`; `GOFLAGS`, `GODEBUG`, `GOEXPERIMENT` empty. No race, coverage,
build tags, gc/asm/link flags, instrumentation or alternate build mode/compiler.
The fixture checks environment/runtime and any available embedded settings;
embedded test build metadata may be incomplete. **The parent MUST prove actual
build settings; runtime environment strings alone cannot prove compiler flags.**

Report pathname is reserved `O_EXCL` mode0600 BEFORE any provider start, in an
existing real (no symlink ancestry), runner-owned0700 directory. Use a fresh
short external directory to respect Unix socket path limits. No append/reuse,
shared directory or symlink report target. The fixture creates0700 evidence roots
under that directory and NEVER RemoveAlls them, even after failure. Normal Host
Close can remove its own ephemeral process/socket files, not the durable evidence
root. Preserve the entire private directory after the run; do not use automatic
runner cleanup that deletes it. Filesystem ownership/race isolation is a runner
responsibility, not an adversarial filesystem security claim.

A synced `pre_close_prefix` report is written BEFORE EACH Host Close, including
the epoch64 restart, and through a defer on failure. This preserves measurements
if Close blocks. A final synced report includes Close results/failures; Close
errors are not retried into success. The defer runs before any test-framework
cleanup; there is no evidentiary-root cleanup callback. Abrupt kill/disk loss can
still defeat reporting; parent raw command status/logs remain authoritative.

`complete=true` requires BOTH fixed expected cases,30 contiguous expected-outcome
samples EACH, all65 fresh/stable/ready epoch records successfully Closed, no
recorded failures and a passing test with final cleanup complete. A pre-Close
prefix cannot be complete. Serialization/file-close failure still fails the test;
never accept `complete` without exit status and independent report audit.
All four whole-layer/memory/Host-expiry/performance-SLO qualification flags remain
FALSE even on successful completion.

## Independent-review and parent execution gate (NOT author execution)

Review the exact two new file hashes, frozen dependencies, construction count,
expected public negative result, timer boundaries, prefix/Close behavior and unit
controls. Record written approval and source inventory BEFORE executing native.
Approval is procedural; the test cannot authenticate that a human/independent
review occurred. If blocked or unexpected native behavior occurs, retain the
failing prefix and STOP for review, not a silent rerun or method edit.

Parent must preserve exact source/module/evidence inventory (including dirty and
untracked dependencies), git state, toolchain/executable hashes and `go env`,
ordinary build command/environment, test ELF and build metadata, original image
hash/admission records, execution command/status/stdout/stderr, CPU/kernel/load
context and the entire private evidence tree. Save source inventories before
build and after run; reconcile every difference. Do not substitute a later build
or silently retarget the historical image. Build/test with all inherited
`SPEC_KITTY_*` removed, then supply ONLY the two opt-ins for this exact native test.
Ensure no intentional parallel jobs. Root paths and custody records stay private.

Illustrative gated commands (parent chooses exclusive custody destinations;
`RUN` is an existing fresh runner-owned0700 SHORT directory outside the repo,
`CUSTODY` is a separate preserved0700 custody directory):

```text
# ONLY after independent written approval; sanitized environment throughout:
GOTOOLCHAIN=go1.26.7 GOAMD64=v1 CGO_ENABLED=1 GOFLAGS= GODEBUG= GOEXPERIMENT= \
  go test -c -o "$CUSTODY/productionruntime.test" ./internal/productionruntime
# Save actual ELF SHA256/build metadata and build receipts; no instrumented tags.
# Run from internal/productionruntime, required for the ordinary repository paths:
SPEC_KITTY_WP13_GAPDBD=/tmp/spec-kitty-admitted-reproduction/bin/gapdbd \
SPEC_KITTY_OBSERVATION_PRIVATE_CHAIN_REPORT="$RUN/report.json" \
GOTOOLCHAIN=go1.26.7 GOAMD64=v1 CGO_ENABLED=1 GOFLAGS= GODEBUG= GOEXPERIMENT= \
  "$CUSTODY/productionruntime.test" \
  -test.run '^TestRetainedPrivateChainOriginalHostNative$' \
  -test.count=1 -test.timeout=1800s -test.v
```

The snippet is not a custody script: parent must explicitly remove inherited
`SPEC_KITTY_*` and preserve commands/logs/exit codes. No shell pipeline may mask
failure. A timeout is a harness stop, not a measured SLO. Verify the reported ELF
and image hashes against the preserved files; do not infer source identity from
the fixture's self-reported hashes. Audit all60 durations/invariants/classes,
recompute statistics, validate65 distinct actual epochs/database/data identity,
zero ACKs,195 private matches vs zero refused proof, closing status and false
qualification flags. A public sanitized refusal cannot alone prove causality.

## Author-only validation and remaining limits

New ordinary controls are `TestPrivateChainWorkloadConfiguration`,
`TestPrivateChainWorkloadOutcomeAndFinalization`, and
`TestPrivateChainWorkloadExclusiveReport`. They exercise skip/partial/pinned
configuration, strict public classes, expected refusal vs positive match,
invariant rejection, nearest-rank reuse, count/completeness gating, failure/Close
report retention, exclusive creation and private parent checks. Their synthetic
scalar helper inputs are NOT native Host evidence. They never start a provider.

Author receipts belong in a new exclusive0700
`~/.cache/private-chain-workload-author-*` directory: commands, logs, SHA256s,
compiled test ELF and validation report. All inherited `SPEC_KITTY_*` must be
removed for author unit execution. Compile and vet are permitted; native/image/
performance execution is expressly NOT permitted before independent review.

Author receipt directory (ordinary validation ONLY):
`/home/user/.cache/private-chain-workload-author-_ypj5018/`. Its `commands.json`,
raw logs, source copies/SHA256 manifest, test ELF, pinned profile and `report.json`
retain validation custody, including an initial corrected compile error. No
provider, native test or performance run was executed. The three new ordinary
unit controls, compile and vet passed; exact final-source receipts are in that
directory. Independent method review remains pending.

No timings/native result are supplied by authorship. Even a later successful
run qualifies only this fixed no-ACK private-chain boundary with this exact
image/source/profile: NOT full memory/alloc/live/peak/RSS accounting, universal
latency/SLO, all-layer acceptance, large values/checkpoints, image/hash-work
maximum, concurrent load, writer/CAS/history equivalence or genuine Host expiry.
