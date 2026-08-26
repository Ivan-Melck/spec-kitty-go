# Contract: Beads v1.2.2 JSON Protocol

## Compatibility tuple

```text
executable version: 1.2.2
JSON envelope:      BD_JSON_ENVELOPE=1
schema_version:     1
source tag commit:  6c124203e771433a3550c348771a5b5e27fd3c21
```

No wider version range is implied.

## Invocation environment

Each command receives:

- an absolute executable path from the validated binding;
- explicit canonical workspace configuration;
- `BD_JSON_ENVELOPE=1`;
- `BD_NON_INTERACTIVE=1`;
- `BD_DISABLE_METRICS=1`;
- `BD_DISABLE_EVENT_FLUSH=1`;
- an explicit working directory;
- a minimal inherited environment allowlist plus required Beads/Dolt credentials for the chosen binding;
- a context deadline and process-group termination behavior;
- `--json` where the command documents it.

The adapter never invokes shell parsing. Arguments are passed as an argv vector assembled from typed values.

The two disable variables are fixed adapter policy, not caller-overridable
credentials. They prevent metrics and deferred-event writes from turning the
four preflight observations into filesystem mutations.

## Envelope

Successful object or array commands must emit exactly one JSON value:

```json
{
  "schema_version": 1,
  "data": {}
}
```

Unknown optional fields under `data` are ignored. Missing required fields, duplicate top-level JSON values, trailing non-whitespace stdout, output truncation, or unsupported schema are protocol errors.

## Error

Documented JSON errors are decoded from stderr:

```json
{
  "schema_version": 1,
  "error": "message",
  "code": "not_found"
}
```

Stderr may also contain warnings. The decoder locates at most one valid documented error object without branching on warning prose. A zero exit status with a valid success envelope remains success even when non-secret warning metadata is present; warning bodies are not persisted in routine evidence.

## Command shapes

| Capability | Command shape | Expected payload |
|---|---|---|
| executable identity | `bd version --json` | object containing `version`, `build`; optional commit/branch |
| workspace and database location | `bd where --json` | object containing `path`, `prefix`, and absolute `database_path` |
| storage/database mode | `bd dolt show --json` | object containing `backend`, `data_dir`, `database`, and `embedded`; server mode also supplies endpoint and connection facts |
| storage/server health | `bd dolt status --json` | embedded status or external-server status object characterized by real-provider tests |
| read work | `bd show <id> --json` | issue object including description, acceptance, dependencies, comments |
| list/ready | `bd list --json`, `bd ready --json` | issue array |
| create work | `bd create ... --metadata <json> --json` | created issue object |
| update work | `bd update <id> ... --metadata <json> --json` | updated issue object |
| atomic claim | `bd update <id> --claim --json` | updated issue object; actor semantics must be characterized |
| add dependency | `bd dep add <blocked> <blocker> --type blocks --json` if supported | dependency result |
| list dependencies | `bd dep list <id> --json` | dependency array |
| append comment | `bd comments add <id> <body> --json` if supported | comment/result object |
| list comments | `bd comments <id> --json` | comment array |
| portable export | `bd export --json` | JSONL; separate streaming contract |
| native backup | `bd backup init <path>`, `bd backup sync`, status/restore probes | output shapes characterized before automation |

If a documented command lacks `--json` in v1.2.2, production automation does not parse its human output. The capability is either implemented through another documented JSON command or reported unsupported. Qualification scripts may use exit status for setup commands but may not derive product state from prose.

### Preflight authority boundary

The mutation-qualified preflight surface is exactly `version`, `where`,
`dolt show`, and `dolt status`, each using its schema-1 JSON envelope. The
adapter cross-checks the canonical workspace/database directory, database
name, storage mode, and—when configured—the server endpoint and health across
those observations. `bd info --schema` and `bd doctor --agent` are not product
state protocol alternatives in v1.2.2 and must not be parsed by automation.

Beads v1.2.2 does not expose one authoritative effective-read-only field or a
durable logical server identity through these commands. Read-only state and
logical server identity are therefore sealed local binding policy. They may be
validated against observed storage/endpoint facts, but must never be invented
as provider-returned fields.

Coordination note: this WP04 correction supersedes the earlier provisional
`info`/`doctor` command table. Dependent work packages and shipped helper
fixtures must use the four-command surface above.

## Output limits

Initial defaults, configurable downward/upward within policy:

| Stream | Default limit | Overflow result |
|---|---:|---|
| ordinary stdout | 4 MiB | malformed/ambiguous |
| ordinary stderr | 1 MiB | error marked truncated |
| JSONL record | 2 MiB | export failure |
| JSONL aggregate | 256 MiB | export canceled with bounded diagnostic |

Limits are safety defaults, not acceptance thresholds; tests verify boundary behavior.

## Timeout classification

- Version/where/dolt-show/dolt-status/show/list/ready: default 10 s.
- Create/update/dependency/comment: default 30 s.
- Backup/export: caller-supplied bounded operation deadline.

A mutating process killed after start yields `ambiguous`, never `not_applied`, until an independent observation classifies the result.

## Metadata namespace

The adapter owns one top-level object and preserves all other keys:

```json
{
  "spec_kitty": {
    "schema_version": 1,
    "project_id": "project-id",
    "work_id": "work-id",
    "created_by_operation": "operation-id",
    "last_operation": "operation-id",
    "assignment_marker": "operation-id",
    "criteria": [
      {
        "id": "criterion-id",
        "text": "observable condition",
        "verifier": "test",
        "required": true,
        "evidence_hint": "go test ./..."
      }
    ]
  }
}
```

An existing `spec_kitty` object belonging to another project/work mapping is a conflict. Unknown newer namespace schemas are not rewritten.

## Security

- No shell interpolation.
- No raw environment dump.
- Credential variables are passed only from an explicit allowlist and redacted from diagnostics.
- Comment bodies/descriptions are not copied into evidence or ordinary logs.
- Executable digest/version and workspace identity are recorded.
- Diagnostic commands run without `--fix`; restore commands are never automatic.
