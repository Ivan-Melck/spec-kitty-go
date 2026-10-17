# Hubnot entry artifact recovery — identity is not admission

The requester approved conditional M3/M4 entry after complete binding verification.
Existing authorized access recovered immutable references but not the original
full-local Hubnot producer archive. No new credentials or hosted/project issue
writes were requested or performed.

## Exact executable reproduced once

Recorded source `baf350d12accd01d4d7e7221cf859c81b2c9622b`, tree
`61c2f2e87f7828e71e82bc422f98c172e17539c9`; module/target `hubnot`, `.`.
The original recorded recipe was used in an isolated source checkout:
Go 1.26.7, CGO disabled, linux/amd64, GOAMD64=v1,
`-trimpath -buildvcs=false -mod=readonly -ldflags='-s -w'`.

One build produced **exactly** the expected SHA-256:
`077058c8be07cfcd28994210ab483f739fb194b6db417f70b455d6c3a3524cf3`.
Parent independently recomputed it and checked mode **0500**, size **4,206,754**.
Path: `/tmp/spec-kitty-hubnot-entry-recovery/artifacts/hn.single-build.candidate`.
The source remained clean. Binary was **not executed** during recovery.
Go build metadata, stripped ELF verification, actual argv/build log and provenance
remain under `/tmp/spec-kitty-hubnot-entry-recovery/`.

## Recovered references are not missing proof

Thirty source/review/reference files were retained and rehashed against immutable
Git blobs. The independent historical review identifies full-local **28/28** at
Go source `782f75579a2349dd438c7519297cbb452425349f`, tree
`6e8c99fed859647695696b21112f4cfea9264093`, manifest digest
`1cf51474a1de1cd93f948bd7e5baaa8b23abba56f29198eb595f61ac21df3de2`.
Underlying producer/captures/manifest/audits were not recovered, so that manifest
was **not** independently validated from its files. Original Actions runs have
no retained artifacts; inspected release/artifact inventories have no exact
qualified-source publication. Other-source and hosted non-full artifacts were
not substituted. This proves inspected absence, not global artifact absence.

Source `.hn/policy.json` digest
`f5342671f6adaa75c16ba2570da81b7a7e2d5ca79d5c592701958dc025b59e91`
is repository governance, **not established deployment/admission policy**.

## Fresh proof boundary and remaining M3 prerequisites

An offline real-provider producer uses disposable signed local repositories and
replica fixtures, not production credentials or live hosted issue writes. It
requires genuine correlated user/network/PID namespace evidence, tool/compiler/
product/qualifier identities, command/capture census and preservation evidence.
Its full-local original-data obligation requires authentic nineteen original
`.beads` files, recorded refs/stashes and a shared Git common-directory identity;
those cannot be synthesized, waived or supplied by an unrelated standalone clone.
Fake/adversarial unit tests and hosted 27-row evidence are non-full complements.

A new producer run would create **fresh** evidence with its own source/run/path
bindings, never reconstruct an old receipt or silently rewrite absolute paths.
No producer was run in this recovery. Tuple-bound deployment policy, accountable
human attestation and complete conformance remain unverified M3 entry inputs.
The exact executable availability blocker alone is now resolved. M3 is not
admitted and M4's separate admission does not waive any of these obligations.
