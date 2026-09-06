# WP16 review feedback — cycle 3

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

1. Source inspection still permits executable token splitting. In fresh
   archives of exact candidate `32de27cf`, both
   `exec.Command("sql"+"ite3", path).Run()` in Go and
   `retired_cli='sql''ite3'; "$retired_cli" ...` in shell pass the absence
   gate. Replace contiguous-substring-only inspection with bounded semantic or
   normalized reconstruction that detects split executable names. Detector
   vocabulary must not evade its own invariant through the same representation;
   use a non-executable encoded/data form and retain exact killing regressions.
2. Runtime evidence is asserted rather than observed. `inspect.sh` discards
   test output, does not require the admitted GapDB sidecar, permits the real
   current/restart tests to skip, and then writes a literal all-zero runtime
   record. Qualification must require the exact admitted sidecar, capture
   structured execution results for the required root/current/restart matrix,
   reject every skip/missing/unmatched case, derive the runtime evidence from
   those observations, and bind both raw output and derived result into the
   manifest inventory.

## Preserve

- Exact final-tree qualification and exact evidence-path exemptions.
- No whole-file executable exemption; both original self-shelter attacks stay
  killed.
- Eight detector-layer controls with exact rejection status.
- The self-consistent 34-artifact inventory and per-class manifest digests,
  extended as necessary for observed runtime output.
- All SQLite deletions, module absence, architecture policy, parity, race,
  CGO-free build, vet, staticcheck, and clean-tidy results.

## Required re-review evidence

- Reproduce and kill both Go and shell split-token attacks from this review.
- Demonstrate clean detector implementation source passes without an
  executable-source exemption.
- Run qualification once with the sidecar missing and prove it fails closed.
- Run with the exact admitted sidecar and show every required runtime test was
  observed as run/pass with zero skip/missing/unmatched cases.
- Independently recompute the updated manifest and closed artifact inventory.
