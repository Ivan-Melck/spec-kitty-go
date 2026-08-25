# Data Model: Phase One Architecture Closeout

## ArchitectureTier

Classifies one production package for dependency-policy evaluation.

| Field | Type | Rules |
|---|---|---|
| `Name` | string | Stable, non-empty policy identifier |
| `PackagePrefixes` | ordered strings | Exact package or slash-bounded prefix matches |
| `AllowedLocalTargets` | set of tier names | Closed set; unknown target is invalid |
| `AllowedExternal` | predicate/declared prefixes | Provider SDK prefixes allowed only for adapter tiers |
| `Production` | boolean | Test-only tiers cannot silently classify production packages |

**Invariants**:

- Every production package has exactly one tier.
- Unknown or multiply classified packages fail.
- A kernel capability cannot import an adapter or provider SDK.
- Architecture policy changes are review-visible data, not implicit code-path exceptions.

## DependencyViolation

| Field | Type | Rules |
|---|---|---|
| `SourcePackage` | string | Fully qualified local package |
| `SourceTier` | string | Known tier |
| `TargetPackage` | string | Local or external imported package |
| `TargetTier` | optional string | Present for local packages |
| `Rule` | string | Stable rule identifier |

Diagnostics name the exact edge. Human prose is supplementary and never parsed by a caller.

## ReachabilityTarget

| Field | Type | Rules |
|---|---|---|
| `Package` | string | Production package |
| `Symbol` | string | Constructor, guard, or concrete implementation identifier |
| `RequiredCallerClass` | enum | `external_package`, `same_package_production`, or `either_production` |

Test files never satisfy production reachability.

## Commitment

An algorithm-qualified content commitment.

| Field | Type | Rules |
|---|---|---|
| `Algorithm` | string | Initially exactly `sha256`; never implied |
| `Digest` | lowercase hex string | Exactly 32 decoded bytes for SHA-256 |

The zero value is invalid. Comparisons include both fields.

## GovernanceContextEntry

| Field | Type | Rules |
|---|---|---|
| `Kind` | enum/string | `config`, `doctrine`, `profile`, `mission_type`, or forward-compatible registered kind |
| `Ref` | string | Stable non-empty identity |
| `Revision` | string | Non-empty pinned revision/version |
| `Commitment` | Commitment | Commitment to the referenced semantic content |

Entries are ordered. Duplicate `(Kind, Ref)` pairs are invalid.

## GovernanceContext

| Field | Type | Rules |
|---|---|---|
| `ID` | GovernanceContextID | Portable typed identity |
| `SchemaVersion` | uint16 | Starts at 1 |
| `Entries` | ordered list | At least one validated entry |
| `Aggregate` | Commitment | Domain-separated framed digest over schema and ordered entry fields |
| `CreatedAt` | timestamp | UTC, non-zero; excluded from aggregate |

### Digest framing

```text
domain = "go-kitty3/governance-context/v1"
frame(schema_version)
frame(entry_count)
for entry in order:
    frame(kind)
    frame(ref)
    frame(revision)
    frame(commitment.algorithm)
    frame(commitment.digest)
```

Each frame carries an explicit length. Naive concatenation is forbidden.

## Run governance relationship

```text
GovernanceContext 1 <── 0..N Run 1 <── 1..N Lease
```

- New runs require exactly one governance context.
- Migrated historical runs may have no context and report `unknown_pre_provenance`.
- A lease never carries doctrine/profile revisions independently.
- A replacement lease under the same run resolves the same context.
- A new run created after governance changes receives a new context identity.

## SQLite adapter projection

### `governance_contexts`

| Column | Constraint |
|---|---|
| `context_id` | primary key |
| `schema_version` | positive |
| `aggregate_algorithm` | non-empty |
| `aggregate_digest` | non-empty |
| `created_at` | non-empty UTC timestamp |

### `governance_context_entries`

| Column | Constraint |
|---|---|
| `context_id` | foreign key, delete restricted |
| `position` | non-negative; composite primary key with context |
| `kind`, `entry_ref`, `revision` | non-empty |
| `commitment_algorithm`, `commitment_digest` | non-empty |

Unique `(context_id, kind, entry_ref)` prevents duplicate identities.

### `runs.governance_context_id`

Nullable only for migrated historical rows. New adapter writes require a matching context row in the same transaction.

## ValidationCampaign

| Field | Type | Rules |
|---|---|---|
| `Name` | enum | `quality` or `full_campaign` |
| `Trigger` | set | PR/push or schedule/manual |
| `ExpectedTests` | manifest | Exact curated names for selected suites |
| `StartedAt`, `FinishedAt` | timestamps | Duration derivable |
| `TerminalVerdict` | enum | pass/fail/canceled |
| `TestCensus` | set | Must equal expected set for curated selectors |
| `FailureIdentity` | optional | Required on fail |

Campaign results are CI evidence, not runtime business state.
