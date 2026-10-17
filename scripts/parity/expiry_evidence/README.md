# Isolated expiry evidence — NOT a production provider

Approved scope: `docs/parity/APPROVED_OBSERVATION_BOUNDARIES.md`.

This tool prepares a fresh copy of the exact Go-module-checksummed provider,
changes **one** expiry-writer call site, adds three build-tagged files, and builds
a separately identified TEST daemon. It never changes module-cache files,
admitted binaries, production constructors, tuples or receipts. Python is a
qualification tool here, not a new runtime/install requirement.

The hook runs after real due-expiry selection and before execute/revision/WAL
publication, outside database/admin locks. Only the first nonempty batch can
pause. In a `spk_expiry_evidence` build, env
`SPEC_KITTY_EXPIRY_GATE_FD=3` identifies a parent-created inherited socketpair.
There is no control listener or path/selector/command API. The hook duplicates
that descriptor during package initialization, before database ownership opens;
it never closes the original descriptor. That one inherited slot is reserved until
process exit; only hook-owned duplicates are closed by the gate. Invalid FD/env
configuration cannot close a database lock. The fixture issues no concurrent
snapshot/backup/admin writer request: such a request could hold the admin
controller mutex while queued behind the paused writer and block status.

Protocol: daemon sends `H` after a due batch reaches the gate; parent sends `R`
to release; daemon sends `A` before continuing. EOF, malformed input, failed I/O
and the 15-second deadline release without a successful acknowledgment. A test
must establish stable live reads/counts between `H` and `R`, receive `A`, then
verify normal cleanup resumes. Merely waiting for TTL or a timeout is not that
witness. Process scheduling/OS waits remain cooperative, not hard-wall promises.

## Reproduce

Use Go1.26.7/linux/amd64, a C toolchain for race tests, and the admitted local
filesystem type for provider data. `--output` must not exist; its parent must.
Preserve every failed output directory rather than rerunning into it.

```bash
export TMPDIR=/home/user/.cache/spktmp
export GOTOOLCHAIN=go1.26.7
python3 -B -m unittest discover -s scripts/parity/expiry_evidence -p 'test_*.py' -v
python3 -B scripts/parity/expiry_evidence/build.py \
  --output /home/user/.cache/spec-kitty-parity-resume-followup/expiry-build-UNIQUE
```

`--prepare-only` produces source/delta manifests for inspection without compilation.
A full build runs gate unit/race/vet, two identical evidence builds, and a no-tag
control build. Inspect `preparation.json`, logs, source inventories, `build.json`
and both digests; none is an admission record.

```bash
SPEC_KITTY_EXPIRY_EVIDENCE_BUILD=/home/user/.cache/spec-kitty-parity-resume-followup/expiry-build-UNIQUE/build.json \
  go test -count=1 -v -timeout=90s ./internal/controlstore/gapdb \
  -run '^TestRetainedExpiryEvidenceLive$'
```

Run the same selected fixture with `-race` separately, retaining distinct logs.
The fixture verifies image bytes before execution, binds actual response
connections to the real child PID/startticks/image/socket/directory using the
existing read-only lifetime kernel, and mints actual coordinator ACKs through
real adapter commits. It exercises:

- Exact publication reads and successful complete retained count before the extra.
- A hidden expired extra: actual Get not-found and Scan omission, unchanged durable
  coordinate, successful independent expected-row reads, and refusal exactly at
  S1 count equality. A subsequent attributed status remains at that coordinate.
- Explicit gate release and normal durable expiry followed by a fresh matching proof.
- Parent EOF releases the writer.
- A no-tag control build ignores the environment/control socket and expires normally.

This is **adapter-infrastructure evidence for the separately bound test image**.
It is not proof from the original admitted daemon, a production Host composition,
private authority-chain closure, public admission capability or complete-layer
acceptance. The test-created lifetime constrains identity; it does not grant
production owner/adoption authority. Production API/admission code is unchanged.
