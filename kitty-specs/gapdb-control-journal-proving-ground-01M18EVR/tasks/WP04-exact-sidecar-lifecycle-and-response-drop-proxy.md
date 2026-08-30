---
work_package_id: WP04
title: Exact Sidecar Lifecycle and Response-Drop Proxy
dependencies:
- WP03
requirement_refs:
- C-001
- C-002
- C-004
- C-005
- FR-001
- FR-002
- FR-003
- FR-004
- FR-005
- FR-006
- FR-039
- NFR-014
- NFR-016
planning_base_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
merge_target_branch: kitty/mission-gapdb-control-journal-proving-ground-01M18EVR
branch_strategy: Planning artifacts for this mission were generated on kitty/mission-gapdb-control-journal-proving-ground-01M18EVR. During /spec-kitty.implement this WP may branch from a dependency-specific base, but completed changes must merge back into the mission branch; only the accepted mission may merge to main.
subtasks:
- T018
- T019
- T020
- T021
- T022
- T023
history: []
agent_profile: implementer-ivan
authoritative_surface: internal/sidecar/gapdb/
create_intent:
- internal/sidecar/gapdb/provenance.go
- internal/sidecar/gapdb/provenance_test.go
- internal/sidecar/gapdb/lifecycle.go
- internal/sidecar/gapdb/lifecycle_test.go
- internal/sidecar/gapdb/proxy.go
- internal/sidecar/gapdb/proxy_test.go
execution_mode: code_change
model: ''
owned_files:
- internal/sidecar/gapdb/provenance.go
- internal/sidecar/gapdb/provenance_test.go
- internal/sidecar/gapdb/lifecycle.go
- internal/sidecar/gapdb/lifecycle_test.go
- internal/sidecar/gapdb/proxy.go
- internal/sidecar/gapdb/proxy_test.go
role: implementer
tags:
- m1
- sidecar
- lifecycle
- fault
tracker_refs: []
---

# Work Package Prompt: WP04 - Exact Sidecar Lifecycle and Response-Drop Proxy

## ⚡ Do This First: Load Agent Profile

Use the `/ad-hoc-profile-load` skill to load the agent profile specified in the frontmatter, and behave according to its guidance before parsing the rest of this prompt.

- **Profile**: `implementer-ivan`
- **Role**: `implementer`
- **Agent/tool**: `codex`

If no profile is specified, run `spec-kitty profiles list --json` and select the best match for this work package's task type and authoritative surface.

---

## Objective

Create a secure, exact lifecycle owner for the admitted GapDB external sidecar
and a real socket proxy capable of the GJ-02 response-loss cut. The product must
prove which binary it executes, which process it owns, and which endpoint its
status describes. It must never locate an arbitrary `gapdb` on PATH or mistake a
lookalike process/socket for the admitted daemon.

## Product laws

1. Resolve an explicit immutable executable path and verify it immediately
   before exec.
2. Reject symlinks and unsafe owner/group/mode/path conditions.
3. Identify process lifetime with pidfd where supported and `/proc` start-time
   identity fallback; PID alone is insufficient.
4. Bind to the actual configured private socket, never a compiled default.
5. Status, Health, and DescribeConfig must describe the same owned instance.
6. Cleanup may affect only demonstrably owned resources.
7. GJ-02 consumes the complete daemon response, then withholds it from product.

## Branch and ownership

Begin from WP03. Edit only `internal/sidecar/gapdb/**` files listed above. The
public GapDB module is already owned/pinned by WP03; do not edit module files.
Do not create a generic daemon manager or alter external system services.

## T018: Verify immutable sidecar provenance before execution

**Files**: Create `provenance.go` and `provenance_test.go`.

**Implementation requirements**:

1. Accept an explicit executable and admission-record path from trusted config.
2. Open without following symlinks; verify regular file, safe ownership/mode,
   expected size, cryptographic digest, source SHA, module version, and contract
   revision.
3. Bind verification and execution to the same open file descriptor when the
   platform supports it; otherwise fail if identity changes before exec.
4. Reject PATH lookup, relative traversal, writable parent chains, and hard-link
   substitution not admitted by policy.
5. Return a bounded provenance value suitable for evidence.
6. Never include executable contents or environment secrets in diagnostics.

**Tests**:

- Admitted immutable executable passes.
- Symlink, changed inode, digest mismatch, unsafe mode, and writable ancestor fail.
- A verify-then-swap race cannot execute the replacement.

**Exit evidence**: `go test ./internal/sidecar/gapdb -run Provenance`.

## T019: Implement exact process and socket lifecycle ownership

**Files**: Create `lifecycle.go` and `lifecycle_test.go`.

**Implementation requirements**:

1. Create a private bounded runtime directory and explicit socket path.
2. Start the verified file with a minimal allowlisted environment and arguments.
3. Record pidfd or PID plus process start identity and executable identity.
4. Wait for readiness with a monotonic deadline and actual endpoint probes.
5. Stop only the exact owned process; handle already-exited/reused PID safely.
6. Remove only socket/runtime artifacts proven attributable to this lifecycle.
7. Bound stdout/stderr capture and preserve useful typed failure reasons.

**Tests**:

- Concurrent lifecycle owners receive distinct endpoints.
- PID reuse and stale socket fixtures are refused.
- Timeout cleans only owned resources.
- Stop is idempotent without killing an unrelated process.

**Exit evidence**: `go test ./internal/sidecar/gapdb -run Lifecycle`.

## T020: Verify Status, Health, and DescribeConfig attribution

**Files**: Extend lifecycle files and tests.

**Implementation requirements**:

1. Query all three surfaces through the actual configured endpoint and require
   a strict protocol-v1 typed response envelope on that successful connection.
2. Attribute public fields only to the surface that actually reports them:
   Status supplies database ID, owner PID/start, revisions, limits, and
   lifecycle; Health supplies subsystem health; DescribeConfig supplies
   configuration and limits.
3. Observe the socket from the successful configured connection; bind data
   directory and executable identity through host lifecycle evidence; and bind
   wire/storage versions through strict-envelope proof and pinned admission
   facts. Refuse readiness when the composite provenance record is incomplete
   or when overlapping reported observations disagree.
4. Keep `healthy`, `configured`, `admitted`, and `ready for product recovery`
   as distinct predicates.
5. Do not treat byte-level daemon recovery as product readiness.
6. Capture bounded public fields in sidecar provenance evidence.

**Tests**:

- A lookalike healthy endpoint with different config fails attribution.
- Misreported public fields, conflicting overlapping fields, and stale status fail.
- Byte-recovered but unvalidated product state is not ready.

**Exit evidence**: `go test ./internal/sidecar/gapdb -run 'Status|Health|Config'`.

## T021: Implement the real response-drop proxy for GJ-02

**Files**: Create `proxy.go` and `proxy_test.go`.

**Implementation requirements**:

1. Listen on a product-facing private endpoint and forward to the owned daemon.
2. Match the selected operation by stable protocol-aware marker, not timing.
3. Forward the complete request exactly once.
4. Read and validate the complete daemon response, proving durable acceptance.
5. Close the product connection before forwarding any response bytes.
6. Record the cut as `after_durable_acceptance_before_client_response`.
7. Permit subsequent ordinary read/reconcile traffic unchanged.
8. Never report a synthetic timeout without having consumed a real response.

**Tests**:

- Selected response is dropped only after full daemon response consumption.
- Nonselected and later requests pass through.
- Partial/malformed daemon response is not mislabeled GJ-02.
- Proxy shutdown cannot duplicate a mutating request.

**Exit evidence**: `go test ./internal/sidecar/gapdb -run ResponseDrop`.

## T022: Harden cleanup, restart, and stale-process diagnostics

**Files**: Extend lifecycle and proxy tests.

**Implementation requirements**:

1. Reconcile runtime artifacts after owner crash using recorded provenance.
2. Distinguish owned live, owned exited, stale artifact, foreign live, and
   indeterminate states.
3. Fail closed on indeterminate ownership; provide operator recovery steps.
4. Never reuse an endpoint while an attributable predecessor may still serve it.
5. Ensure proxy and daemon identities appear separately in diagnostics.
6. Test cleanup during every start/ready/stop boundary.

**Exit evidence**: `go test ./internal/sidecar/gapdb -run 'Cleanup|Restart|Stale'`.

## T023: Qualify lifecycle and fake-timeout mutants

**Validation and mutants**:

1. Kill verify-then-path-exec, symlink acceptance, PID-only ownership, compiled
   default socket, misattributed public fields, fake-timeout, and unsafe-cleanup
   mutants with explicit negative controls.
2. Run lifecycle tests repeatedly and under the race detector.
3. Run `go vet`, WP01 dependency guards, and `git diff --check`.
4. Confirm no PATH lookup, shell invocation, `internal/server`, SQLite, global
   service mutation, or unbounded output capture.

## Review guidance

The reviewer should replace the admitted path between verification and start,
substitute a healthy lookalike endpoint, and reuse a PID in fixtures. Reject if
any succeeds. Inspect GJ-02 packet/control flow: a timer or locally fabricated
timeout is not acceptable evidence.

## Completion criteria

- Executed bytes and evidence provenance are inseparable.
- Process, endpoint, and status attribution agree.
- GJ-02 is a genuine lost-response ambiguity.
- Lifecycle cleanup is attribution safe.
