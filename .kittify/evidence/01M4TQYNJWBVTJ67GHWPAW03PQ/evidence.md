# Open finding: intermittent admitted-sidecar startup refusal

ID: `PARITY-STARTUP-001`  
Status: **OPEN — cause not established; safety check retained**

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

This finding remains a qualification blocker. Independent evidence and review
are required before changing the lifecycle law or closing it.
