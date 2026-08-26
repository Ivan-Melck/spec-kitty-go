# Contract: Architecture Guard

## Inputs

- Module path and production package graph from Go tooling.
- Closed tier policy.
- Optional curated reachability registry.

## Required behavior

1. Classify every production package exactly once.
2. Evaluate every direct local import against the source tier's allowed targets.
3. Evaluate external imports against tier-specific provider rules.
4. Fail on unknown or ambiguous classification.
5. Ignore test-only imports for production-edge evaluation while still scanning test fixtures as guard tests.
6. Emit stable rule identity plus exact source and target packages.

## Initial tier policy

| Tier | Representative packages | May depend on |
|---|---|---|
| typed foundation | `internal/ids` | standard library |
| authority capabilities | workgraph, execution, workspace, handoff, review, integration | typed foundation and explicitly reviewed peer capability contracts |
| provider contracts | `internal/tracker`, `internal/controlstore` | typed foundation and provider-neutral capability values |
| adapters | tracker implementations, SQLite, Git repository adapter | the contract/capability implemented and adapter-private dependencies |
| composition | helper command and binaries | all required capabilities and adapters |
| quality tooling | architecture and scenario tooling | production packages for observation; never imported by production authority packages |

The implementation records the complete exact prefix map and current cross-capability baseline in code. New edges require an intentional policy diff.

## Mandatory fixtures

| Fixture | Expected |
|---|---|
| Current production graph | accept |
| Provider import from provider adapter | accept |
| Provider adapter imported by authority package | reject exact edge |
| Provider SDK imported by authority package | reject exact edge |
| New unknown production root | reject package |
| Package matches two tiers | reject package |
| Remove one load-bearing allowed edge | reject the now-forbidden real edge |
| Fabricated guarded symbol with only test caller | reject symbol |
| Remove a registered guard's sole production caller | reject symbol |

The fixture harness must prove a rejection came from the intended mutation, not an unrelated build failure.
