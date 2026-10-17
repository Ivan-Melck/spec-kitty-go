# Qualification path snapshot — partial source-value leaf

Status: **IMPLEMENTED / INDEPENDENTLY BOUNDED-APPROVED TEST-ONLY SOURCE GEOMETRY.**
Full-call coverage, allocation count, compiler/ABI materialization, heap, retention,
physical copy traffic, timing and whole-observation qualification remain OPEN.

## Exact source and ownership

`internal/productionruntime/expiry_qualification_rundown.go` defines
`expiryPathSnapshot` and `(*expiryHostLease).snapshotPathsLocked`.
`internal/productionruntime/expiry_qualification.go` defines `expiryRootIdentity`.
The helper constructs one source-language struct value directly from seven lease
fields, with no calls, loops, explicit heap constructors, cloning or OS operations
in that helper. This is a **source shape**, not a count of machine copies or heap
objects. Its callers, failures, admission checks and delegated work are not closed.

Pinned Go1.26.7/linux/amd64 source-value layout:

| Inline component | Width | Ownership / limitation |
|---|---:|---|
| Three `*os.File` pointers | 24 bytes | Borrowed referents; NOT three new handles, wrappers or descriptors |
| Three `expiryRootIdentity` values | 96 bytes | Each32: two uint64 + three uint32 + four padding bytes; no strings |
| Root string header | 16 bytes | Borrowed backing; not path bytes copied or newly owned storage |
| **Snapshot value width** | **136 bytes** | Includes identity padding; not allocator slots, heap or memory traffic |

Identity field offsets are0/8/16/20/24; snapshot field offsets are
0/8/16/24/56/88/120. A root header can preserve a backing reference, but this leaf
does not bound that backing, its lifetime or transitive borrowed graph.

The read-only planner identified a candidate, **not a closed recipe**. Parent
inspection corrected its imprecision: identity fields are scalar integers, not
strings, and the tests belong in `productionruntime`, where the private type is
accessible, not the adapter package. No source-level lifetime guarantee "until
close" or compiler/ABI/heap claim is adopted from that research.

## Implemented controls

New file:
`internal/productionruntime/expiry_qualification_snapshot_accounting_test.go`.

- Pin scalar identity and snapshot widths/offsets to actual private types.
- Check all seven returned fields, root-backing pointer sharing, and independence
  from subsequent replacement of the lease's scalar/header/pointer fields.
- Exercise empty, one-byte and4096-byte arbitrary borrowed string values; they
  are **not valid-path/admission fixtures**. Synthetic `os.File` values are used
  only as pointers; no filesystem handles are opened, used or closed.
- Parse the actual helper and require its one direct struct return, exact seven
  direct field selections and no intervening calls/control flow. Future source
  drift refuses this recipe instead of silently preserving a stale explanation.

Tests do not claim deep immutability: pointed-to files and string storage remain
borrowed. No allocation sampler, timing benchmark, descriptor operation, provider,
Host, admission record or product code is introduced or changed.

## Actual verification

Separate evidence:
`/home/user/.cache/spec-kitty-parity-resume-followup/expiry-snapshot-value-leaf-9OPsqTVB/`.

```sh
GOTOOLCHAIN=go1.26.7 go test -count=1 -v -timeout=90s \
  -tags spec_kitty_expiry_host_qualification ./internal/productionruntime \
  -run '^TestExpiryPathSnapshotSource'
GOTOOLCHAIN=go1.26.7 go test -count=1 -race -v -timeout=90s \
  -tags spec_kitty_expiry_host_qualification ./internal/productionruntime \
  -run '^TestExpiryPathSnapshotSource'
```

`unit.log` and `race.log`: each2 top-level tests PASS, zero failures/skips.
`results.json` records both exit0; tagged productionruntime vet also passes
(`vet.log`). `source-freeze.json` binds copied test/source bytes and verifies the
two unchanged product source files against the prior selected native inputs;
the new test is deliberately not retroactively inserted into that manifest.
Independent reviewer `9797ed90-05e1-425` read the complete test/leaf and exact
source ranges, ran the two pure unit tests and bounded-APPROVED these source-value
assertions. Parent ran the separate race/vet checks; the review's unit+race summary
is not a second independent race receipt. `bounded-acceptance.json` binds exact
unchanged source hashes and preserves scope. Component greens plus this bounded
review still do not establish an exhaustive source/caller/materialization recipe.

This test-only increment follows the separately accepted
[real Host expiry campaign](EXPIRY_FIXTURE_PUBLICATION_CENSUS.md). Its new test was
not part of that old1009-entry manifest; no manifest/receipt was rewritten or
transferred to it, and no unchanged native run was repeated.

## Completion audit / next actual work

This closes only the named source-value width/shape/borrowing assertions under
the pinned build. It deliberately does **not** assign136 allocated bytes per call,
zero physical allocations, a physical copy count, a finite admission count or a
new global subtotal. Actual caller multiplicities, success/error schedules,
compiler escape/inlining/ABI/lowering, channel/context work, C5 residual retention
and borrowed/library/runtime costs remain open. `fullCallCoverage=false`.

All four observation deliverables remain OPEN. No qualification checkpoint,
admission, ref, product cap, provider image, architect Op or goal completion changes.
