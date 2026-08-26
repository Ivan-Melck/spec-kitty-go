# Phase 2 qualification results

This document maps delivered claims to the immutable machine payload in this
directory. It does not extend either source candidate's verdict.

## Result summary

| Claim | Machine record | Executable proof | Result |
|---|---|---|---|
| Provider-neutral behavior remains independent of Beads | `campaign-census.json` → `portable` | `TestDurableFakePortableConformance`, `TestRegistryAndCapabilityMutantsFailClosed` | 2/2 pass |
| Dishonest, slow, stale, malformed, and human-changed providers fail safely | `campaign-census.json` → `adversarial` | the exact `provider-adversarial` census | 20/20 pass |
| Human assignment changes survive recovery | `campaign-census.json` → `adversarial.human_preservation_runs` | `TestBeadsHumanAssignmentPreservation100` and its overwrite mutant | 100/100 preserved |
| Real Beads catalog, graph, discussion, assignment, worktrees, and recovery work | `qualification-evidence.json` | `TestRealBeadsQualification` and `TestRealBeadsWorktreeQualification` subtests | 9/9 applicable scenarios pass |
| JSONL interchange and native restoration are distinct and usable | `qualification-evidence.json` plus `campaign-census.json` → `real_provider` | `TestRealBeadsBackupQualification/jsonl_interchange`, `native_backup_restore` | 2/2 pass |
| Shared-server concurrency produces unique work | `qualification-evidence.json` → `contention` | `server_multiwriter_contention` | 1,000/1,000 pass; zero collisions/duplicates |
| Human assignment is never erased by unsafe compensation | `qualification-evidence.json` → `assignment_preservation` | assignment preservation campaign | 20/20 preserved |
| Tracker activity never creates execution or workspace authority | `qualification-evidence.json` → authority counts | universal-invariant and stale-authority probes | 23 audits; zero failures |
| Kernel/adaptor separation remains enforced | `campaign-census.json` → `architecture`, `scope` | closed import policy, reachability, and rejecting mutants | 36/36 pass |

## Measured latency

The admissible real-provider run used Beads 1.2.2 in `server_multiwriter` mode on
Linux/amd64. Measurements are observations of that host, not service-level
objectives.

| Operation | Samples | p50 | p95 | maximum |
|---|---:|---:|---:|---:|
| Preflight | 100 | 268.298 ms | 340.600 ms | 453.317 ms |
| Warm single-item read | 200 | 156.714 ms | 173.087 ms | 192.659 ms |

The eight-process contention run completed 1,000 creates in 155,711.409 ms.

## Source-bound results

The results form three related, non-transitive records:

1. Real-provider qualification passed at source commit
   `315660fcabc0a72c538934a326748a09600afb36`, tree
   `b5368db98c0a2270fc53bcb741635d182bb10e20`.
2. The clean portable/adversarial/quality campaign passed at source commit
   `a2401c2e8723dc4ea21a01a61da08311f5330d01`, tree
   `345da2a489e2de26d5d8e2bfa23fe12d93d0dbb4`.
3. The strict evidence-serialization correction passed focused repeat, race,
   vet, staticcheck, architecture, schema, and invalid-prerequisite checks at
   commit `47afd5ada9fb6c621fc5b64f82fccd1f68a4465d`, tree
   `0af3f1ad5d67ebdb759f37a0907aebd3a0682ca2`.

No single record claims that all later bytes were rerun through all earlier
campaigns. The final docs-only attestation in the evidence index names the
payload commit separately.

## Non-passing and superseded attempts

They remain part of the audit history rather than being converted into passes:

- Commit `8afdc21a520596acf749267a0c4019cdeedaef8c`, tree
  `453dd79cdc21576b1fd3b612f3cba2ab08318ef6`, produced a valid
  `non_admissible` run: 10 of 11 scenarios passed and shared-server contention
  was skipped. Its artifact SHA-256 is
  `449981d796c66630993b0ead3a6b66cc94a0cf3d265007310d548cae4e753fa7`.
- An intentional missing-prerequisite probe at commit
  `e3eb9af6f85f541a1619c74a1a9f118460ed5935`, tree
  `84277b19722744220bd106fdd87198055f355629`, recorded zero started scenarios
  and `non_admissible`, rather than manufacturing a pass. Its artifact SHA-256
  is `77ba3f4a968300952e95e858184f429169bd53ee311a8de84314a6bb8e316c79`.
- WP10's first review was rejected because the strict evidence model was not
  load-bearing, source drift was insufficiently fenced, hosted qualification
  lacked complete provisioning, and the stale-authority probe was not yet
  executable. The `a2401c2` and `47afd5a` corrections address those findings.
- Hosted timing remains `baseline_pending`: zero of ten required hosted samples
  exist. Local passing durations are not relabeled as hosted data.

## Gate record

The clean WP10 campaign records exact durations for provider portable (168 ms),
provider adversarial (263,824 ms), provider bounded (264,055 ms), acceptance
(21,155 ms), architecture (9,858 ms), scope (1,513 ms), and the final quality
campaign (32,628 ms). Build, race, vet, staticcheck, module verification, and
vulnerability scanning passed, but their individual durations were not retained;
`campaign-census.json` represents those values as `null`, not zero. WP11's
documentation gate follows those source candidates and therefore does not appear
as an inherited qualification result.
