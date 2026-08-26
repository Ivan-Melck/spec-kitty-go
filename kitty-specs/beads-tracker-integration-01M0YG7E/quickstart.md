# Quickstart: Beads Tracker Integration Qualification

This is the planned operator/developer flow for Phase 2. The mission implementation must make these commands true and keep ordinary unit development independent of a Beads installation.

## Prerequisites

- Go 1.26.4 or the compatible Go 1.26 toolchain declared by the repository.
- Git.
- `staticcheck v0.7.0` and `govulncheck v1.6.0` for the full quality gate.
- Beads v1.2.2 only for real-provider qualification.

The repository does not add Beads as a Go module dependency.

## Portable development loop

No Beads binary, Dolt server, hosted account, or network is needed:

```bash
make preflight
make contract
make scope
```

The contract gate runs provider-neutral tests against the durable fake provider and dishonest subprocess fixture. The scope gate proves the authority kernel cannot import `internal/tracker/beads`.

## Install the pinned provider for local qualification

Use a dedicated tool directory, not the repository:

```bash
mkdir -p /tmp/go-kitty3-beads-tools
GOBIN=/tmp/go-kitty3-beads-tools \
CGO_ENABLED=1 \
GOFLAGS=-tags=gms_pure_go \
go install github.com/steveyegge/beads/cmd/bd@v1.2.2

BD_JSON_ENVELOPE=1 /tmp/go-kitty3-beads-tools/bd version --json
```

The cgo/`gms_pure_go` build is required for embedded Dolt. A `CGO_ENABLED=0` build is server-mode-only. Release/CI qualification uses the mission's checksum-verified installer rather than trusting an arbitrary PATH binary.

Expected version envelope:

```json
{
  "schema_version": 1,
  "data": {
    "version": "1.2.2",
    "build": "dev"
  }
}
```

Additional optional fields are allowed.

## Run real-provider qualification

The implementation adds a dedicated target:

```bash
BEADS_BIN=/tmp/go-kitty3-beads-tools/bd \
make beads-qualification
```

The target must:

1. create temporary repository, workspace, backup, and evidence directories;
2. initialize Beads only inside that disposable repository with non-interactive, skip-agent, skip-hook, stealth controls;
3. verify version/schema/workspace identity before mutations;
4. exercise catalog, graph, criteria, discussion, assignment observation, export, and native backup;
5. run concurrency and human-change safety scenarios;
6. emit `qualification-evidence.json`;
7. leave the checked-out repository and user configuration unchanged.

## Manual disposable-provider setup

For debugging adapter behavior, never initialize Beads in the project checkout. Use a temporary repository:

```bash
mkdir -p /tmp/go-kitty3-beads-debug
cd /tmp/go-kitty3-beads-debug
git init

BD_NON_INTERACTIVE=1 \
/tmp/go-kitty3-beads-tools/bd init \
  --stealth \
  --skip-agents \
  --skip-hooks \
  --non-interactive \
  --prefix skt
```

Then inspect only documented JSON:

```bash
BD_JSON_ENVELOPE=1 /tmp/go-kitty3-beads-tools/bd where --json
BD_JSON_ENVELOPE=1 /tmp/go-kitty3-beads-tools/bd info --schema --json
BD_JSON_ENVELOPE=1 /tmp/go-kitty3-beads-tools/bd list --json
```

Do not infer product state from human output.

## Verify worktree convergence

The qualification creates two linked worktrees and invokes the adapter with the same explicit canonical Beads binding from both. The evidence must show:

- identical workspace identity;
- identical reads after writes;
- distinct provider IDs for concurrent work creation;
- one Spec Kitty execution lease for the same work;
- no mutable workspace handoff after lease expiry.

If the worktrees resolve different provider identities, mutation must fail with `workspace_mismatch`.

## Exercise safe compensation refusal

The Beads v1.2.2 adapter advertises that arbitrary conditional compensation is unsupported. The conformance scenario is:

```text
Spec Kitty records assignment intent
  -> Beads shows projected assignment and operation marker
  -> a human changes the assignee directly
  -> recovery observes different revision/value/marker
  -> adapter leaves Beads untouched
  -> control store records attribution_lost reconciliation
```

Even when the current values still look attributable, the adapter must return `safe_compensation_unsupported` if it cannot enforce the condition in the provider mutation itself. A read-then-`bd update` cleanup is a test failure.

## Export versus backup

Portable issue export:

```bash
BD_JSON_ENVELOPE=1 /tmp/go-kitty3-beads-tools/bd export --json > /tmp/issues.jsonl
```

This is interchange, not a complete backup.

A restorable local backup uses Beads' Dolt-native path:

```bash
/tmp/go-kitty3-beads-tools/bd backup init /tmp/go-kitty3-beads-backup
/tmp/go-kitty3-beads-tools/bd backup sync
/tmp/go-kitty3-beads-tools/bd backup status
```

Qualification restores into another disposable workspace and inspects the recovered records. It never runs destructive restore against the project binding.

## Full acceptance ladder

```bash
make quality
make fault
make soak
BEADS_BIN=/tmp/go-kitty3-beads-tools/bd make beads-qualification
make full-campaign
```

Before mission acceptance, also run:

```bash
go test -race ./...
go vet ./...
staticcheck ./...
go mod verify
govulncheck ./...
git diff --check
```

## Evidence inspection

The provider campaign writes a schema-1 document conforming to `contracts/qualification-evidence.schema.json`. Confirm:

- `provider_version` is `1.2.2`;
- the binary digest matches the allowlist;
- `protocol_schema` is `1`;
- started, terminal, and passed scenario counts equal expected;
- `authority_invariant_failures` is zero;
- the source commit/tree match the qualified checkout;
- no secrets, descriptions, comment bodies, or raw environment dumps appear.

## Recovery guide

| Diagnostic | Response |
|---|---|
| `version_skew` | Install the qualified v1.2.2 binary or run an explicit new-version qualification. |
| `schema_skew` | Stop before mutation; update protocol contracts/tests before support. |
| `workspace_mismatch` | Correct the project binding so every worktree uses the same canonical workspace. |
| `lock_contended` | Wait/retry within policy or move to a qualified server-mode binding. |
| `ambiguous` | Re-observe mapping, value, marker, and revision; do not repeat blindly. |
| `attribution_lost` | Preserve provider state and resolve the recorded conflict explicitly. |
| `safe_compensation_unsupported` | Leave the visual projection intact or repair it manually after inspection. |
| `malformed_protocol` | Capture redacted evidence, verify the exact binary/digest, and stop automation. |

Provider failure may degrade visible coordination. It must not corrupt or recreate Spec Kitty mission, lease, workspace, review, or integration authority.
