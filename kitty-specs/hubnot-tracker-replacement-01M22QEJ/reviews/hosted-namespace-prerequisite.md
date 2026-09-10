# Hosted namespace prerequisite: bounded adjudication

Read-only code/log inspection and primary-source documentation research, 2026-09-09. Root continuation Op: `01M246Q0AS2V02GKKRDT1E7CEA`. No workflow dispatch, repository/source/runtime modification, namespace mutation, privileged command, or test was executed.

## Confirmed evidence and remaining uncertainty

The real-provider artifact from run 34414740481 retains `hubnot/hosted-provider/worker.stderr` with `unshare: write failed /proc/self/uid_map: Operation not permitted`. Its qualification record is non_admissible, reason `isolated worker failed: exit status 1`. This happens before any scenario can be admitted; it is not 27 scenario failures or a provider behavior regression.

The quality job in run 34414707015 failed `TestQualificationCancellationRemovesDescendantGroups` at runner_test.go:205 because the fault child did not appear within three seconds. That test enters the same `superviseQualification` function, but deliberately discards outer output, so its log alone does not establish the same syscall error. Shared namespace failure is the leading explanation, not a proven correlation. The test should surface the actual already-retained worker.stderr and qualification.json when its child fails to start.

The quality job's own setup log identifies Ubuntu 24.04.5/image 20260907.300.1; the real job identifies Ubuntu 24.04.4/image 20260831.293.1. Both official image manifests list kernel 6.17.0-1022-azure. These are image definitions rather than an independently captured live `uname` measurement. [Quality image](https://github.com/actions/runner-images/blob/ubuntu24/20260907.300/images/ubuntu/Ubuntu2404-Readme.md), [real-provider image](https://github.com/actions/runner-images/blob/ubuntu24/20260831.293/images/ubuntu/Ubuntu2404-Readme.md).

Ubuntu documents restrictions for unprivileged, unconfined namespace users: a default profile can allow creation while denying subsequent capabilities. This is consistent with a write to uid_map failing after namespace creation. Its recommended application-specific profile mechanism uses `flags=(unconfined)` with `userns,`; system-wide disabling is a broader alternative. None of those docs proves which policy was active in these exact jobs. [Ubuntu 24.04 release notes](https://discourse.ubuntu.com/t/ubuntu-24-04-lts-noble-numbat-release-notes/39890), [Ubuntu security documentation](https://documentation.ubuntu.com/security/security-features/privilege-restriction/apparmor/).

## Existing isolation boundary must stay intact

`runner.go:229` resolves unshare only through the fixed system path and runs:

```text
/usr/bin/unshare --user --map-root-user --net --pid --fork --kill-child=SIGKILL -- QUALIFIER __worker
```

The exact canonical launcher path depends on systemTool resolution and symlink resolution; inspect it instead of trusting PATH. The worker verifies its challenge and executable digest, different network/PID namespace identifiers, PID 1, and loopback-only topology before and after scenario execution. The parent validates correlated proof and retains failure evidence. Preserve all this code and receipt semantics.

`--map-root-user` maps the calling effective UID/GID to namespace root and implies setgroups deny; it is explicitly designed for an unprivileged caller. `--kill-child` plus the PID namespace supplies descendant cleanup. An outer privileged wrapper changes the host-user premise and can confound those observations. [util-linux v2.39.3 unshare documentation](https://raw.githubusercontent.com/util-linux/util-linux/v2.39.3/sys-utils/unshare.1.adoc).

## Minimal diagnostic/setup proposal

1. Add a reusable CI prerequisite setup invoked in the quality job before `make quality-bounded`, the full-campaign job before `make full-campaign`, and the real-provider job before qualification. These all execute the namespace-dependent qualifier package. The bounded provider-only job does not select that package, and the current race and M2-short jobs do not need a speculative extra setup.
2. Persist diagnostics in the existing external artifact directory: live `uname -a`, `/etc/os-release`, invoking `id`, AppArmor enabled status, kernel userns restriction and max-user-namespace sysctls, current process AppArmor label, canonical unshare path/mode/owner/SHA/version, parser/package versions and loaded unshare-related profiles. Capture only namespace-related kernel audit output surrounding the bounded probe, retaining audit-unavailable as such. Do not dump environment, credentials or arbitrary unrelated audit records.
3. Run one bounded ordinary-user preflight with the SAME user/map/net/pid/fork/kill flags. Record parent/child namespace IDs, uid/gid mapping and child topology. No privileged execution of this probe. If it fails, retain stdout/stderr/exit plus relevant AppArmor capability/userns denial records. EPERM without an audit cause remains an unidentified prerequisite failure.
4. If the preflight already works, record success and make no privilege/policy change. Only after a failed preflight, if the actual host has Ubuntu AppArmor userns restriction enabled and no conflicting attachment, add a small exact-launcher profile with the documented language:

```text
abi <abi/4.0>,
include <tunables/global>
profile go-kitty-ci-unshare /usr/bin/unshare flags=(unconfined) {
  userns,
}
```

This is a conditional design example, not already validated against either hosted image. Validate the real launcher path and refuse unexpected paths rather than wildcarding temporary Go binaries or profile attachments. Load only the new profile with the supported parser add operation; preserve its exact bytes/hash. A profile on QUALIFIER alone does not directly authorize the preceding unshare process's uid_map setup. The temporary Go test executable also varies, making a stable exact system-launcher attachment the smaller integration seam. [AppArmor userns rule syntax](https://manpages.ubuntu.com/manpages/noble/man5/apparmor.d.5.html), [parser add/remove operations](https://manpages.ubuntu.com/manpages/noble/man8/apparmor_parser.8.html).
5. Existing unshare profile/attachment handling must be explicit. Do not overwrite or unload an unrelated distro profile, replace its capabilities wholesale, or load a competing attachment silently. Preserve and report its bytes/loaded identity; an unexpected restrictive profile should cause a clear setup refusal and targeted review. The available Ubuntu source discussions show unshare policy has changed across package versions, so absence cannot be assumed from the distro name.
6. Repeat the same bounded ordinary-user probe after profile load; require a real pass, verify the global restriction value is unchanged, and then run the actual tests/qualifier. Failure stays non_admissible; never skip the cancellation test, fake a worker handshake, reuse the host network namespace or swallow the status.
7. If explicit cleanup is included, unload only the exact job-added profile after work finishes; retain cleanup result and before/after diagnostics. An ephemeral hosted VM may instead supply lifetime cleanup, but never blanket-unload profiles. Track whether this job actually installed the profile before any explicit removal. Use sudo only for narrow profile administration/audit access. The runner, compiled consumer, provider and tests remain ordinary-user processes.

Scope truth: the exact `/usr/bin/unshare` allowance applies to all calls to that executable in this ephemeral job, not only the qualifier. It is narrower than a VM-wide userns sysctl disable, but it is not a caller-specific sandbox. No claim of stronger AppArmor confinement is made. If tighter caller attribution is required, a dedicated profile transition requires separate design and proof; do not improvise a root wrapper or broader temporary-path wildcard.

## Exact candidate file scope

- New reusable helper, proposed `scripts/ci/prepare-userns.sh` (setup/probe/cleanup or an explicit paired cleanup interface, durable diagnostics and failure semantics).
- `.github/workflows/quality.yml`: quality job setup/always-cleanup only; preserve bounded/race/M2 selectors.
- `.github/workflows/full-campaign.yml`: same prerequisite because contract-full runs the qualifier tests.
- `.github/workflows/hubnot-qualification.yml`: real-provider setup/always-cleanup and preserved setup/worker evidence; do not change 27/28 selection.
- `internal/testcampaign/cmd/hubnot-qualification/runner_test.go`: improve child-start failure diagnostics from retained artifacts, keep test mandatory and all cancellation/descendant evidence assertions.
- A small focused helper/CI contract test may be justified for profile/path refusal and cleanup behavior; exact ownership belongs to root's continuation design. No production runner.go/provider/manifest change is currently demonstrated as necessary. The independent reviewer should decide whether diagnostic-only runner_test.go changes add value beyond the shared preflight; minimal essential scope is the three workflows and shared script. Never apply hosted AppArmor preparation to the user's local machine.

## Alternatives rejected at this stage

Global `kernel.apparmor_restrict_unprivileged_userns=0` or disabling AppArmor removes a host-wide restriction and is not the narrow remedy. Running make or qualifier under sudo introduces host-root execution and changes the proof boundary. Downgrading to an older image solely to avoid the restriction hides a prerequisite instead of documenting it. A simple timeout increase does not address known real-provider EPERM; improve the test's retained failure reporting first. None is needed before attempting a properly diagnosed per-executable profile.

The current evidence supports this conditional minimal CI correction and a diagnostic-first hosted check. It does not yet support asserting AppArmor was definitely the cause, that the example profile has loaded successfully on either image, or that a future hosted run passes.

## Architect execution boundary

This continuation addresses CI runner admission only, under Op `01M246Q0AS2V02GKKRDT1E7CEA`. Implementation and hosted execution remain subject to independent review of this proposal. Prefer ephemeral VM lifetime cleanup over a second privileged teardown interface; record the exact profile added. A hosted-only guard must refuse local use before policy administration. Existing policy attachments must be positively inspected, not inferred absent from profile names. Unexpected or unreadable policy is a setup refusal.

Observable contracts: given an ordinary runner where the exact namespace probe passes, setup records that proof without changing policy. Given a failed probe and positively identified compatible Ubuntu restriction with no conflicting attachment, setup may add only the documented exact-launcher profile and must repeat the ordinary-user probe successfully. Given any unverified prerequisite, conflict, failed load or failed repeated probe, the job fails with retained diagnostics. Provider tests, selection counts and isolation admission remain authoritative.

Local validation will cover syntax, meaningful refusal/diagnostic contracts and the unchanged cancellation behavior. Hosted validation must execute the actual corrected setup and selected qualification. Prior local 28-row execution remains attributed to e9b1ac65; the first failed hosted attempts remain available and do not become passes.

## Independent design disposition

Gauss approved this bounded design with fail-closed constraints, recorded in `work/go-hubnot-hosted-namespace/design-review.md` in the orchestration workspace. A uniquely named profile and parser add mode do not establish attachment nonconflict. The helper must preserve authoritative loaded attachments/context and refuse unknown or ambiguous policy. It must not implement a partial AppArmor attachment matcher. The first hosted correction may therefore be diagnostic-only if a simple authoritative proof is unavailable; actual runner evidence will determine any later policy change.

Approved implementation scope is the shared CI helper, the three affected workflows, diagnostic-only child-start output in `runner_test.go`, and focused nonprivileged CI contract tests. No teardown interface is planned. Review approval is conditional implementation authority, not evidence that the host cause has been identified or qualification has passed.

## Diagnostic implementation and revision-stream correction

The first implementation deliberately added no policy mutation. Independent review caught and verified correction of an EOF/child-cleanup defect and an overly broad audit filter before publication at `b658009e`. The actual hosted diagnostic run [34417294494](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34417294494) then stalled before producing its probe: AppArmor's revision interface is an event stream, and a buffered read waits for another revision. Root cancelled that run and its corresponding quality workflows, preserving cancellation logs and both provider archives. The real-provider archive contains only setup evidence; no host-policy diagnosis or scenario execution can be inferred. Concurrent race/M2 jobs were cancelled, not failed by an assertion.

The subsequent narrow correction uses one raw nonblocking read from each fresh descriptor, treats incomplete/unavailable revisions as unknown, and places a fixed 60-second deadline around the helper. The deadline escapes diagnostic error handlers, cleans up an active child and retains partial evidence. Ordinary-user FIFO/pipe and process regressions reproduce the old failure and verify both literal readers and finalization. The additional unprivileged-unconfined restriction sysctl is recorded for future host diagnosis. These changes do not alter policy, production isolation, fixed scenario counts or existing campaign thresholds. Independent verification is retained in the orchestration workspace under `work/go-hubnot-hosted-namespace/`; actual host admission remains pending.

## Actual host diagnosis and revised integration boundary

Runs [34418016508](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34418016508) (quality) and [34418026899](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34418026899) (real provider) completed the corrected diagnostic at `72bbf92c`. Both recorded uid_map EPERM and an AppArmor capability denial for `unprivileged_userns`, `comm=unshare`, `sys_admin`. The ordinary runner is UID/GID 1001 and unconfined; userns restriction is 1, forced-unconfined restriction is 0, clone is enabled, and the policy namespace is root/unstacked. Both archives were independently verified against API digests and extracted bytes. Truncated/unknown attachment inventory remains insufficient for the proposed exact-executable attachment, so that earlier remedy is superseded.

The reviewed correction instead adds a uniquely named profile with no executable attachment. Only the documented fixed unconfined-mode `userns` rule is permitted; names are tightly validated and the privileged loader reconstructs exact bytes, uses add-only/no-cache loading and retains collision/load failures. No original policy is replaced or removed. An ordinary `aa-exec -i` selection lets both the initial command and later unshare execution undergo their existing attachment transitions. The original exact namespace probe must succeed in this context, with actual labels, UID/GID and unchanged global controls recorded. Existing denial remains fatal; no inheritance override, root test, global sysctl change or admission waiver is allowed.

A small nonprivileged Python execution wrapper is justified to share the explicit mode/profile-state validation and ordinary exec boundary across the three workflows. It must refuse missing/invalid prerequisite state, directly execute only after a no-change probe PASS, and select the validated derived profile only after successful add and repeated probe. This is a CI integration boundary, not production runner functionality. The allowance applies to the selected command tree where inherited; it is not an authentication boundary or stronger sandbox. Lifetime is the ephemeral GitHub VM, with no teardown interface. No policy action is performed on the user's machine. Independent design approval and source research are retained as `explicit-profile-independent-review.md` and `explicit-profile-alternative.md` in the orchestration workspace. Actual hosted success is still required.

## Fixed controlled trial and audit correlation

The reviewed candidate at `55dbeaa1` refused before loading in [34419377237](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34419377237) and [34419360165](https://github.com/spec-kitty/spec-kitty-redesign/actions/runs/34419360165): launcher PIDs were 2138/2153, while corresponding audit observations were 2139/2154. Matching util-linux 2.39.3 source shows `--fork` creates the child before the simple uid-map write. That explains why launcher equality is an invalid assumption; it does not prove ancestry for either observed PID pair. No policy or provider scenario ran.

Architecture and independent review superseded the overly specific PID precondition with a fixed controlled trial. The permission is determined by the already reviewed no-attachment userns profile, not by audit data. Supported Ubuntu 24 context and controls, an actual exact-probe uid_map failure, and relevant retained denial corroboration justify one bounded trial of those fixed bytes. Probe and audit PIDs remain separate observations with correlation unestablished. No new trace/ancestry parser is added solely to authorize an experiment whose permission scope is already closed.

All actual admission requirements remain: successful acknowledged add, ordinary immediate selection preserving attachments, untraced namespace/label/UID/GID/map/topology proof, unchanged global controls and existing downstream tests. Missing supported context or relevant denial, failed/ambiguous loading, or failed post-proof refuses. This is a change to a CI diagnostic precondition, with no policy expansion, isolation waiver or production code change. The independent proportionality decision is retained at `work/go-hubnot-hosted-namespace/corroborated-trial-independent-review.md` in the orchestration workspace.
