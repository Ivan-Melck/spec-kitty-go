# Admission readiness — decision dossier, not admission

Status: **M4 admitted; M3 external tuple bindings pending; parity candidates unqualified.**

The requester approved all three numbered proposals: trace-aware restart
equivalence, reviewed-only clean candidate commits, and conditional M3/M4 entry.
[Approval](../programs/decisions/2026-10-13-parity-qualification-entry-approval.json)
binds the exact candidate scopes. M4's required frozen inputs and independent
entry review now support [recorded admission](../programs/decisions/2026-10-13-m4-admission.json).
M3 is not admitted while complete conformance/policy/signer bindings remain
unverified. No exit, release or predecessor-proof transfer follows. This file
records prerequisites and next inputs; it creates no mission, grants no permission,
and does not amend any immutable receipt.

## Existing immutable facts

| Fact | Evidence and bounded verification |
|---|---|
| Canonical entry law | [Program §4.1 and mission cards](../PRODUCTION_RUNTIME_PROGRAM.md). A designated maintainer/operator records admission; passing packages and tracker status cannot make it. |
| M2 exact adopted predecessor | [Adoption](../programs/evidence/m2-adoption.json): commit `416138f490ad6d908fc445c12b91440ca66ef18c`, tree `2b68ee733305465d5acdfe8680f053b91d867a8d`; local commit resolves to recorded tree. |
| M2 evidence binding | Run `wp17-qualification-416138f4-cycle2-final23`; manifest SHA-256 `8d7731992a35b0ace4cdcd816651df302d4459c8af91d1a2c31cf5d3aeb285a0`; review receipt `a35f944548bd766887344192f59c36fd6002519d88588e07e3cdc48989510e50`; release archive `098fa8d3fae6f33e227ce290530fc172f820c961ffd326adb820a26aa44e2414`. The original release and nested producer/manifest plus review receipt were subsequently downloaded through existing authorized access and their hashes recomputed against adoption. This is identity verification, not fresh qualification or full recursive behavioral acceptance; see [artifact recovery](M2_ARTIFACT_RECOVERY.md). |
| Attributable M2 decision | Signer `github:lynncoleart`, signed `2026-09-11T03:07:01Z`, approved-local-reference. Scope is the exact M2 input to M3/M4, not current branch adoption or completed successors. |
| Hubnot tuple exists | [Qualification contract](../HUBNOT_QUALIFICATION.md), [typed binding](../../internal/tracker/hubnot/binding.go): source `baf350d12accd01d4d7e7221cf859c81b2c9622b`; executable `077058c8be07cfcd28994210ab483f739fb194b6db417f70b455d6c3a3524cf3`; `hn.identity/1`, `hn.issue/1`; Go 1.26.7 linux/amd64. This audit does not independently rebuild/requalify Hubnot or replace the required portable conformance/provider policy/signer evidence. |
| M4 design contract exists | [Stack-neutral acceptance](../STACK_NEUTRAL_ACCEPTANCE.md), [ratified evidence schema](../../kitty-specs/production-runtime-architecture-01M16GVA/contracts/acceptance-evidence.schema.json). Design existence is not implementation admission or cross-provider proof. |
| Dependency topology | [Published manifest](../programs/production-runtime-program.json): M1/M2 complete, M3–M7 proposed; M3/M4 each consume M2, M5 consumes M2/M3/M4. Schema, dependency census, acyclicity and source-projection consistency checked in this investigation. |
| Confirmed M3 delegation / entry work | Requester confirmed item 1 in the current conversation. [Authorization record](../programs/decisions/2026-10-13-m3-m4-entry-authorization.json) and appended [PERMISSION_GRANT](../programs/m3/PERMISSION_GRANT.md)/STATUS.log preserve the original history. Confirmation authorizes candidate specifications and recorded-admission preparation, not automatic mission admission or waived gates. |
| Candidate scope prepared | [M3 candidate](../programs/m3/CANDIDATE_SPEC.md), [M4 candidate](../programs/m4/CANDIDATE_SPEC.md) follow the live resolved canonical specification template. Neither is an activated mission; both retain the program falsification, non-goals, exact census and rollback. |

The opening status sentence in the older Hubnot qualification document retains
its historical `adopted=false` context. The later exact M2 adoption record and
canonical program are the current decision sources; do not rewrite history.

## Inputs needed to proceed

| Entry | Maintainer/operator action still required | Must not be substituted by |
|---|---|---|
| M3 | Delegation and specification/admission preparation are now authorized. Verify and approve the prepared candidate preserving real-agent concurrency falsification, independent ActorID review, tracker non-authority, repository fence/conflict repair, non-goals and rollback. Record admission binding M2 mission identity, terminal commit/tree, accepted evidence digest, review verdict, exact consumed Hubnot source/protocol/binary/policy/signer/conformance tuple and frozen Git/workspace/review/integration laws. | General instruction to keep working, an outline, Hubnot readiness, a matching binary, or package tests. |
| M4 | **Completed:** explicit candidate approval, twelve immutable input-contract hashes, independent ENTRY READY review and attributable admission are recorded. Initialize the canonical governed mission under those frozen inputs; actual provider parity, waiver/invalidation, crash/replay and independent runtime review remain exit gates. | Ratified design alone, repository-specific Go commands, or fake provider success. |
| M5 | Wait for independently accepted M3 **and** M4 plus M2; record the API/progress/operation contract decision. Reconcile historical API 1.3 translation with the current official API 1.15 baseline and retained authority/network laws. | A compatibility facade over missing production capabilities, native-envelope relabeling, or automatic program-status promotion. |

Admission records must include the designated actor, decision/rationale and
immutable dependency/evidence identities. Candidate specifications must preserve
outcome, falsification target, non-goals, stop conditions and rollback. The
schema-valid manifest must retain at most three dependencies and acyclic edges.
Do not edit a completed predecessor's receipts or retarget it to this branch.

## Entry is not exit

After admission, M3 still must execute its real concurrency/zombie/provider/
reassignment/review/fence/conflict census. M4 still must execute materially
different providers for the same claim, missing/stale/waiver/invalidation cases
and ecosystem-import mutants. M5 still needs black-box CLI/API missions,
disconnect/restart/idempotency, progress convergence and authority refusals.
All governed review, acceptance, merge and post-merge gates remain independent.

## Host and current-branch qualification

- The earlier `[Errno 22] Invalid argument` was a process-label read failure,
  **not** unavailable namespaces. [The portability correction](USERNS_PORTABILITY.md)
  now passes actual isolated execution, identity/mapping/topology assertions and
  installed `strace` checks on this host. Selected-profile checks remain strict.
  This resolves the local host/tooling diagnosis, not hosted-runner attestation
  or complete Hubnot/scenario/campaign qualification.
- The [exact M2 qualification binary](M2_ARTIFACT_RECOVERY.md) is now recovered
  from the adoption-bound release. Its artifact availability/identity no longer
  requires user input. This is not a production tuple replacement or branch proof.
- Current modifications have bounded actual-binary/race/regression evidence, not
  source-bound release qualification. Frozen M2 proof does not transfer.
- [Startup finding](SIDECAR_STARTUP_FINDING.md) retains the failed attempts and
  residual causation/qualification boundary despite the construction regression
  correction. No unsafe identity allowance is permissible.
- See [completion audit](COMPLETION_AUDIT.md) for all remaining goal surfaces.

Requester approval now includes the exact prepared scopes and conditional entry.
M4 admission is recorded after immutable verification and independent review;
M3 still needs complete Hubnot conformance, tuple-bound deployment policy and
accountable attestation. The exact Hubnot executable was reproduced once at the
recorded flags and verified, not executed or admitted. Fresh source-bound parity
qualification and exact remaining external evidence bindings are still required. Local namespace capability and required tooling
are now verified; another machine is not an established prerequisite. Until then, do not start
successor production composition or claim parity/release readiness.
