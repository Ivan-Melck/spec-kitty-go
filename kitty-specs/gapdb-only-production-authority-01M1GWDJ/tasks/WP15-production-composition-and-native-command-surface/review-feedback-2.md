# WP15 review feedback — cycle 2

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

1. The real native surface is unusable after initialization. With the exact
   admitted assertion-extension `gapdbd` at
   `/tmp/spec-kitty-gapdb-assertion-sidecar/gapdbd` (SHA-256
   `947bda324a550f4a7534e4efec2a75485e95b4c27907fd0298f72aba247dadad`),
   `TestRealGapDBMutationInitializesAndQueriesRemainPureAcrossRestart` returns
   exit 4 / `COMMAND_UNAVAILABLE` on the first status query after successful
   initialization. The lost-ack test also exits 4 before its writer cut. The
   same executable passes the production-runtime real restart test, so this is
   WP15 observation/composition wiring rather than a bad fixture. Diagnose the
   observation-coordinate equality in `productionruntime/observation.go`.
2. The advertised-operation wire test proves only shallow decoding. The generic
   reflection converter translates top-level snake_case but not nested structs;
   its effect fixture uses nested `ProjectID` CamelCase because a normal
   `project_id` does not populate `tracker.ExternalWorkRef`. Replace shallow
   reflection with explicit recursive wire DTOs/decoders and execute one valid
   request per declared operation kind through the real capability seam,
   including an all-snake-case effect target.
3. `drive` discards every typed capability result. Add a typed result to each
   drive step and preserve the exact result returned by successful execution so
   drive, operation resume, CLI, and future projections share one result
   contract.
4. Error classification is closed only for mission operations. Execution,
   handoff, review, acceptance, integration, cleanup, and effect typed errors
   currently collapse to `OutcomeUnavailable` / `COMMAND_UNAVAILABLE`. Map each
   advertised domain's stable invalid, refused/stale, not-found, conflict,
   ambiguous, unavailable, and canceled semantics and add mutation-sensitive
   coverage for every family.

## Preserve

- Exact-scope, same-mission drive concurrency is real and passes.
- The read-only observation interface is structurally reader-only; repair the
  real coordinate behavior without giving queries mutation authority.
- The dropped-ack writer correctly models caller failure after accepting a
  complete response; make the production path reach it.
- Removal of the retired `inspect-beads` smoke did not weaken the remaining
  Beads documentation/provider/Make guards.
- No SQLite/backend fallback or compatibility protocol may be reintroduced.

## Required evidence

- Run both real native tests with the exact admitted sidecar and prove they pass
  without skips.
- Run the production-runtime real restart proof with the same executable as a
  fixture control.
- Prove every declared operation accepts a documented snake-case request,
  reaches its real Host capability, and returns or refuses with the matching
  typed result/diagnostic.
- Re-run focused tests, race, package compilation, vet, staticcheck, CGO-free
  builds, and the combined portable-scenario graph.
