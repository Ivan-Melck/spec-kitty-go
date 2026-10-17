# Open finding: intermittent admitted-sidecar startup refusal

ID: `PARITY-STARTUP-001`
Status: **OPEN — construction regression corrected; original-iteration causation
and complete qualification unresolved; safety checks retained**

## Observed behavior

During mission-operation membership verification, one parallel package run
returned `COMMAND_UNAVAILABLE` (exit 4) from a fresh CLI inspection that should
have returned a scoped not-found refusal. A separate 30-iteration direct runtime
experiment exposed an earlier startup failure, before any mission publication:

```text
TestReadOnlyObservationRealSidecarKeepsCurrentCoordinate
 gapdb sidecar process: executable_identity_unprovable
 reason=running executable differs from admitted descriptor
```

The remaining iterations passed; the experiment as a whole **failed**. A later
serialized package run passed, but does not close this finding or erase the
parallel-run failure. The direct startup refusal is a concrete finding; it is
not proof that the CLI failure had the identical cause.

## Reproduction and identity

```bash
TMPDIR=/home/user/.cache/spktmp \
SPEC_KITTY_WP13_GAPDBD=/tmp/spec-kitty-admitted-reproduction/bin/gapdbd \
GOTOOLCHAIN=go1.26.7 go test ./internal/productionruntime \
  -run '^TestReadOnlyObservationRealSidecar' -count=30 -v
```

The sidecar is the production-admitted assertion-extension executable:

```text
SHA-256: 947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad
Source: 4d88094749e0f6fdb38a4786708d920fb51966a1
Size: 5327126 bytes
```

Observed local logs:

- `/tmp/spec-kitty-parity-membership-combined.log`: parallel package failure.
- `/tmp/spec-kitty-parity-membership-observe-stress.log`: direct startup refusal.
- `/tmp/spec-kitty-parity-membership-serialized.log`: separate serialized control.

These are development observations, not source-bound release qualification.

## Causation boundary and disposition

`internal/sidecar/gapdb/lifecycle.go:observeStartedProcess` compares the running
process's executable device, inode, size and digest to the admitted sealed image,
then checks identity again around pidfd acquisition. The direct failure occurs
in this first comparison, before journal restoration or the new membership
projection runs. Membership code does not change executable creation or this
predicate. The diagnostic does not identify which comparison or underlying
observation failed, so a root-cause claim is not defensible yet.

Next investigation should distinguish a failed `/proc` observation from a real
image mismatch using bounded internal evidence, then reproduce the identified
condition through the stable start entry point. It must retain descriptor-bound
execution, image digest/identity checks, pidfd/reuse protections, and fail-closed
behavior. No retry-to-green, startup sleep, identity allowance, unqualified
binary substitution, skip, or admission override was added.

## Construction correction and new evidence

Independent source analysis found a supported ordering mechanism on host kernel
`7.2.9-arch1-1`: Linux v7.2.9 `fs/exec.c` closes close-on-exec descriptors and
`exec_mm_release()` wakes the vfork parent before the new `tsk->mm` is installed.
Go 1.26.7 `Cmd.Start()` uses the exec-error-pipe/vfork handshake, so successful
return need not establish that `/proc/PID/exe` already exposes the new image.
This is not captured proof of the original failed iteration's underlying facts.

`TestStartDoesNotMistakeExecTransitionForWrongImage` uses the real admitted child
and a test-only paused/resumed fixture. Its observation seam returns the inherited
supervisor image until the configured endpoint exists. It failed against the
immediate-observation implementation with the same refusal family, then passed
with a condition-based construction barrier. The test-only scheduling timer is
not a production startup sleep.

Production now establishes initial image/start-ticks/pidfd proof once after an
endpoint exists, before requesting any protocol response. An injected socket is
only a trigger, never ownership evidence; an image mismatch still refuses
immediately. No mismatch retry or expected-identity exception was introduced.
All peer, connected endpoint, socket replacement and before/after process checks
remain. Failed partial proof closes pidfds without publishing process state or
starting a competing Wait; unobserved rollback uses the Go-owned child handle.
Established ownership uses existing `abortStart`, whose signaling/ownership-change
limitations have not been silently broadened or described as guaranteed cleanup.

New evidence, retained separately from the failed attempts:

- `/tmp/spec-kitty-parity-startup-barrier-red.log`: deterministic pre-fix failure.
- `/tmp/spec-kitty-parity-startup-barrier-green.log`: transition, wrong-image,
  cancellation/reap, metadata-failure pidfd closure/reap, and 32 plain starts pass.
- `/tmp/spec-kitty-parity-startup-observer-stress-green.log`: separate 30-iteration
  original observer command passes, without retrying a failed iteration.
- `/tmp/spec-kitty-parity-sidecar-combined.log`: complete historical M1 lifecycle/
  provenance suite plus extension startup regressions pass with exact binaries.
- `/tmp/spec-kitty-parity-startup-race.log`: sidecar/runtime/helper/public CLI race
  suites pass. Independent startup review found no actionable regressions.

Real-start instrumentation records image facts but did not capture a naturally
occurring mismatch before correction. These tests establish correction of a
constructed ordering defect, not forensic confirmation that both earlier
failures shared that cause. Original failures remain evidence; full source-bound
qualification and the causation boundary remain open. No frozen receipt is
rewritten and no release/admission decision is implied.
