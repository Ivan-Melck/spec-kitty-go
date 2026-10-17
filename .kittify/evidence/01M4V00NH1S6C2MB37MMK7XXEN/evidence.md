# Namespace prerequisite portability correction

Disposition: **local namespace/label-tooling blocker resolved**, not hosted-runner
attestation, release qualification, mission admission or upstream parity.

## Corrected diagnosis and retained failure

The earlier `TestCIUsernsWrapperPreservesOrdinaryExec` failure was
`namespace dispatch refused: [Errno 22] Invalid argument`. It did **not** show
that user/network/PID namespaces were unavailable. Direct `unshare` succeeded;
the failing operation was reading `/proc/self/attr/current` on this host's
non-label LSM stack (`capability,landlock,lockdown,yama,bpf`). The prerequisite's
namespace child also read that file unconditionally, so fixing dispatch alone
would leave the same defect in the actual isolation probe.

The requester installed `/usr/bin/strace` (7.2) and authorized proceeding.
The original red witness is retained in
`/tmp/spec-kitty-userns-portability-red.log`; the new direct-mode control failed
before correction in `/tmp/spec-kitty-userns-label-controls-red.log`.

## One bounded evidence interpretation

[`userns_label.py`](../../scripts/ci/userns_label.py) is the shared CI-only
interpretation used by [`with-userns.py`](../../scripts/ci/with-userns.py) and the
real child of [`prepare-userns.sh`](../../scripts/ci/prepare-userns.sh):

- A bounded, nonempty, non-NUL process label retains the original string field.
- **Only EINVAL**, corroborated by a bounded, nonempty, unique, closed-known
  non-label active-LSM inventory, produces `label: null` with explicit
  `not_applicable` / `no_label_lsm` evidence and the observed stack.
- Permission/I/O errors, missing/empty/truncated inventory, unknown modules,
  AppArmor and SELinux stacks refuse. No fabricated `unconfined` label.
- Selected-profile dispatch still requires the exact job-specific AppArmor
  label; it rechecks that label before publishing evidence. Unavailable labels
  cannot admit selected dispatch or a selected namespace probe.
- Imports are explicit script-derived paths under Python `-I`, not ambient
  module searches. The shell sets source-path data from its quoted `$0`,
  overriding inherited `GO_KITTY_USERNS_SOURCE`; it does not add runtime words
  to the interpreter command. Both loads compile canonical plaintext directly,
  bypassing adjacent `.pyc` reads/writes; a control rejects cached-code loading.
  The frozen SQLite-absence guard is unchanged.

Hosted/non-root/job/prerequisite identity checks, exact namespace flags,
ordinary parent UID/GID, PID 1, distinct namespace identities, single-entry
UID/GID maps, denied setgroups, loopback-only interfaces, bounded capture,
selected-profile controls and policy-admin conditions remain intact. No Go
production dependency, admission tuple, durable schema or historical receipt
changed. No privileged policy operation was run for the local proof.

## Verification and limits

[`ci_userns_label_test.go`](../../internal/architecture/ci_userns_label_test.go)
adds direct-mode success/refusal controls and real kernel/tracer execution
through the canonical prerequisite `main`. Its disposable hosted-state fixture
is **not** evidence that this machine is GitHub-hosted. All privileged/selected
capture operations are refused in that live test.

The local real probe established child PID 1, UID/GID 0 mapped only to ordinary
parent UID/GID 1000, distinct user/network/PID namespaces, `setgroups=deny`, and
only loopback. Real `strace -f` captured namespace clone flags and exec and exited
0. A separate traced namespace run exercised `taskset` successfully, exit 0.
The actual shell entry also passed from an unrelated working directory with a
bogus inherited source-path variable, proving the script overrides it; no policy
administration occurred. Log: `/tmp/spec-kitty-userns-portability-shell-entry.log`.

- Nine `TestCIUserns*` tests passed, including ordinary exec, strict selected
  profile fixtures, bounded/deadline teardown and real namespace/tracer proof.
- Four additional guards passed: closed production graph, complete M2 guard
  activation, gate-source shelter prevention and unmodified SQLite absence.
- Independent scoped review and source-path follow-up found no actionable
  security/correctness regressions.
- Final focused verification plus documentation-link checks and changed-package
  vet passed; `git diff --check` passed. The generated development-only helper
  cache was removed and is no longer created/consumed by these source loads.
- Logs: `/tmp/spec-kitty-userns-portability-guards-final.log`,
  `/tmp/spec-kitty-userns-portability-live.{trace,stdout,stderr}`.

The separate complete-suite run finished **exit 1**: 47 passing packages, five
failing, two without tests; 22 top-level failures. Log:
`/tmp/spec-kitty-userns-postfix-full-suite.log`. It bound distinct exact original,
extension and recovered M2 artifacts; a nested assertion-candidate check still
refused its own tuple, which must not be overridden by substituting a different
admission. Non-verbose output does not establish an exact skip count.

Remaining failures: reused-client/V1-proxy response-drop incompatibility;
native mission progress `PROGRESS_INCOMPLETE: encoding` (a separate diagnostic
confirmed 273,496-byte progress exceeds the reused 262,144-byte journal payload
budget; [finding 005](QUALIFICATION_FINDINGS.md)); source-bound M1
fault/focused dirty-HEAD/socket requirements; frozen M2 campaign-source mismatch.
No retry or mandatory-proof waiver. The tree was dirty/evolving during this
**development** run (final cache-free loader correction followed by separate
focused checks), not an immutable release candidate. M3/M4 entry decisions and
fresh qualification remain required; frozen M2 adoption proof cannot transfer.

