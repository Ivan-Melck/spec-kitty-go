# Area C parity: doctrine, charter, profiles, Ops, glossary

Packages: `internal/doctrine` (offering/pack loading, activation, DRG,
mission types, profiles), `internal/charter` (charter bundle, context
render, bootstrap renderer, compiler, activation writes, presets, interview,
language inference, status/freshness, synthesis seed, schema validator),
`internal/invocation` (governed Ops: router, routing catalog, model-routing
recommendation, kitty-ops trail, executor), `internal/glossary` (seed store,
validation, chokepoint, events), commands in `internal/commands/doctrine`.

Status legend: **done** = output/JSON/exit codes/files match the oracle in
the tested scenarios; **partial** = main paths match, listed gaps remain;
**missing** = not registered.

Tests: `internal/commands/doctrine/*_test.go` (Go-only fixtures always run;
`TestParity*` run against the oracle when `SPEC_KITTY_ORACLE` is set),
`internal/doctrine/doctrine_test.go`, `internal/yamlx/wrap_test.go`.

| Command | Status | Tests | Deviations / notes |
|---|---|---|---|
| `charter context` | done | TestParityProfilesMissionCharterContext, TestParityCharterGenerateAndRuntime | bootstrap: org-provenance suffixes and the activations stanza (`governance.activations`) are not rendered |
| `charter activate` / `deactivate` | done | TestParityCharterActivation, TestCharterActivationGoOnly | |
| `charter list` | done | TestParityCharterActivation | |
| `charter interview` | done | TestParityCharterGenerateAndRuntime (defaults), manual (interactive) | |
| `charter generate` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | `--owned-checkout` accepted, not resolved |
| `charter status` | done | TestParityCharterGenerateAndRuntime | org-layer edge counts exclude augmentation-derived edges; manifest/provenance pydantic messages approximated |
| `charter synthesize` | partial | TestParityCharterGenerateAndRuntime | fresh-project seed, `--dry-run`, `--dry-run-evidence`, request errors match; the agent-artifact pipeline (generated/fixture adapters, project direct-write registration) is not ported and exits 1 with an explicit message |
| `charter resynthesize` | partial | TestParityCharterGenerateAndRuntime (`--list-topics`) | `--topic` runs the unported pipeline (exits 1) |
| `charter preflight` | done | TestParityCharterGenerateAndRuntime | auto-refresh runs this binary for `spec-kitty ...` steps |
| `charter bundle validate` | done | TestParityCharterGenerateAndRuntime | |
| `charter new` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | |
| `charter validate` | done | TestParityCharterGenerateAndRuntime, TestCharterGenerateGoOnly | YAML parser error text differs (yaml.v3 vs ruamel) |
| `charter pack list/path`, `pack asset list/path` | done | TestParityCharterActivation | built-in pack path is the Go cache, by design |
| `charter mission-type list` | done | manual | |
| `charter consistency-check` | missing | | |
| `charter lint` | missing | | |
| `charter fetch` | missing | | |
| `charter org init/validate` | missing | | |
| `charter pack validate/assemble/regenerate-graph` | missing | | |
| `mission list/show`, `mission-type list/show` | done | TestParityProfilesMissionCharterContext | |
| `mission current/info/switch` (and `mission-type` aliases) | done | manual | |
| `mission close/follow-up/reopen/run` | missing | | mission-state commands (area A territory) |
| `profiles list/show/get` | done | TestParityProfilesMissionCharterContext | |
| `agent profile list/show/get` (hidden) | done | TestParityProfilesMissionCharterContext | |
| `dispatch` | done | TestParityDispatchFallback, TestParityDispatchRouting, TestDispatch* | compiled glossary semantic notices not rendered |
| `profile-invocation complete` | done | TestParityProfileInvocationComplete, TestDispatchOpenCompleteList | doctor-sweep closures (op-closures spine writes) not implemented; spine is read |
| `invocations list` | done | TestParityDispatchFallback, TestParityProfileInvocationComplete | |
| `ops log` / `ops undo` | done | TestParityDispatchFallback, TestDispatchRoutingErrors | |
| `agent context resolve` | done | TestParityAgentContextResolve, TestAgentContextResolveGoOnly | `--owned-checkout` accepted but not resolved |
| `glossary list/conflicts/show/validate/resolve` | done | TestParityGlossaryAndDocs, TestGlossaryGoOnly | `show` never renders compiled entity pages |
| `docs query` | done | TestParityGlossaryAndDocs | duplicates D0's `internal/commands/util/docs` (one must be dropped at merge) |

## Known deviations

- Help screens are rendered by the shared `internal/cli` renderer (column
  padding differs from rich-click); command/flag/argument sets match.
- Built-in doctrine paths printed in `charter context` reference lists and
  `pack path` point at the materialised Go cache instead of site-packages.
- Language inference (`catalog.languages`, scope-filtered references, the
  bundle language gate, the charter-extension advisory) follows upstream;
  dispatch routing does not yet apply the language filter.
