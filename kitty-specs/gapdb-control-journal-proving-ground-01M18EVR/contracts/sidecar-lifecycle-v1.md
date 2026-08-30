# GapDB Sidecar Lifecycle Contract v1

## Exact artifact

The only admitted executable is the project-local digest-named `gapdbd`
installed from module
`github.com/spec-kitty/gapdb/cmd/gapdbd@v0.0.0-20260824033134-ff3dd478f87c`
with Go 1.26.7, `CGO_ENABLED=0`, and `-trimpath`. Its SHA-256 is
`62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`
and size is 5,300,621 bytes. Wire and storage protocols are both version 1.
The exact admission document bytes have SHA-256
`4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d`;
the runtime loads all admitted fields from those verified bytes.

No `PATH` lookup, shell indirection, CLI prose parsing, internal/server import,
or fallback backend is permitted.

## Roots and permissions

The supervisor receives absolute project-specific executable, data, runtime,
socket, and evidence roots. It rejects traversal, symlinks, unexpected owners,
and group/world-writable ancestors. The store and socket are outside all Git
worktrees.

## Verify-to-exec

The supervisor opens the executable without following symlinks, validates its
regular-file metadata, hashes the held descriptor, and executes that same
opened object through a descriptor-preserving Linux mechanism. If it cannot
prove that the verified object is the executed object, startup refuses with
`executable_identity_unprovable`.

## Process identity

Ownership is the tuple of PID, Linux process start time, pidfd, executable
device/inode/digest, socket, database identity, arguments, and supervisor RunID.
PID alone is never identity. Attachment opens the pidfd, validates
`/proc/<pid>/stat`, hashes the opened `/proc/<pid>/exe`, and correlates public
health/config with the expected socket and database. Shutdown uses
`pidfd_send_signal` only after revalidation.

An orphan that cannot be proven exact is not adopted or killed. It blocks
startup and emits operator-safe diagnostic evidence.

## Admission

The sidecar is ready only after an actual connection to the configured daemon
Unix socket exchanges a strict protocol-v1 typed envelope. Public API fields are
attributed to their actual sources:

- `Status`: database ID, owner PID/start, revisions, limits, and lifecycle;
- `Health`: subsystem health only; and
- `DescribeConfig`: configuration and limits.

The connected socket is a host-observed property of that connection, not a
field reported by Status or Health. Executable digest/module/build are
host-proven facts. Storage version 1 and its reference digest are pinned
admission facts, not claimed as daemon-reported fields. All required limits must
be present, mutually consistent, and no lower than
`m1-product-budgets-v1.json`.

Only then may the supervisor atomically publish a provenance document conforming
to `sidecar-provenance-v1.schema.json`. Unsupported platforms and incomplete
reported configuration fail closed.

## Response ambiguity

The `GJ-02` qualification harness places a Unix-socket response-drop proxy
between a distinct product-facing socket and the configured daemon socket. The
proxy forwards a complete request, consumes and validates the complete accepted
response, then drops the product-facing connection without delivering that
response. Recovery reconnects and resolves the original operation ID with an
ordinary public read. Provenance distinguishes both sockets and identifies the
proxy artifact. Internal hooks, daemon-socket destruction, and fabricated
client timeouts do not satisfy this contract.
