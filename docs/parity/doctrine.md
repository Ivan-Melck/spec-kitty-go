# Area C parity: doctrine, charter, profiles, Ops, glossary

Packages: `internal/doctrine` (offering/pack loading, activation, DRG,
mission types, profiles), `internal/charter` (charter bundle, context render,
profile-channel sections, token budget), `internal/invocation` (governed Ops:
router, routing catalog, model-routing recommendation, kitty-ops trail,
executor), `internal/glossary` (seed store, validation, chokepoint, events),
commands in `internal/commands/doctrine`.

Status legend: **done** = JSON/exit codes/files match the oracle in the
tested scenarios; **partial** = main paths match, listed gaps remain;
**missing** = not registered.

Tests: `internal/commands/doctrine/*_test.go` (Go-only fixtures always run;
`TestParity*` run against the oracle when `SPEC_KITTY_ORACLE` is set),
`internal/doctrine/doctrine_test.go`, `internal/yamlx/wrap_test.go`.

| Command | Status | Tests | Deviations / notes |
|---|---|---|---|
| `charter context` | done | TestParityProfilesMissionCharterContext | bootstrap render for a generated charter is simplified (see below) |
| `charter mission-type list` | done | (manual oracle diff) | |
| `mission list/show`, `mission-type list/show` | done | TestParityProfilesMissionCharterContext | |
| `mission current/info/switch` (and `mission-type` aliases) | done | manual | |
| `mission close/follow-up/reopen/run` | missing | | mission-state commands (area A territory) |
| `profiles list/show/get` | done | TestParityProfilesMissionCharterContext | |
| `agent profile list/show/get` (hidden) | done | TestParityProfilesMissionCharterContext | |
| `dispatch` | done | TestParityDispatchFallback, TestParityDispatchRouting, TestDispatch* | language-scope routing filter not applied (inert for unconfigured projects); compiled glossary semantic notices not rendered |
| `profile-invocation complete` | done | TestParityProfileInvocationComplete, TestDispatchOpenCompleteList | doctor-sweep closures (op-closures spine writes) not implemented; spine is read |
| `invocations list` | done | TestParityDispatchFallback, TestParityProfileInvocationComplete | |
| `ops log` / `ops undo` | done | TestParityDispatchFallback, TestDispatchRoutingErrors | |
| `agent context resolve` | done | TestParityAgentContextResolve, TestAgentContextResolveGoOnly | `--owned-checkout` accepted but owned-checkout resolution not implemented |
| `glossary list/conflicts/show/validate/resolve` | done | TestParityGlossaryAndDocs, TestGlossaryGoOnly | `show` never renders compiled entity pages (`.kittify/charter-packs/graph.yaml` DRG) |
| `docs query` | done | TestParityGlossaryAndDocs | |
| other `charter` subcommands | see below | | |

## Known deviations

- Help screens are rendered by the shared `internal/cli` renderer (column
  padding differs from rich-click); command/flag/argument sets match.
- `charter context` bootstrap mode (first load with a generated charter)
  renders the same sections in simplified form.
