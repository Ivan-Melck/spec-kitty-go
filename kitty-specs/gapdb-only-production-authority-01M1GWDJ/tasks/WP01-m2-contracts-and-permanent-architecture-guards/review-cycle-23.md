---
affected_files: []
cycle_number: 23
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T04:03:05Z'
reviewer_agent: renata-gapdb-deadline-cfg-review
wp_id: WP01
---

# WP01 review feedback — cycle 23

Verdict: **REQUEST CHANGES** for
`415ac94b43518fda44484ad965caed16ca891299`.

Cycle 22 fixes every cycle-21 fixture, but the custom control-flow verifier is
still unsound and has become architecturally disproportionate.

## Blocking false greens

Each case reproduced 20/20 through the production-facing validator:

1. A package-scope `var invoke = attempt` alias bypasses the function-reference
   scan, while one valid direct caller masks the invalid indirect caller.
2. A validated duration field may be assigned an invalid value before the
   attempt; the stale validation fact remains live.
3. The entire validated budget value may be replaced before the attempt; the
   stale fact still remains live.
4. Inline validation is position-only and remains accepted after a subsequent
   invalid field write before `WithTimeout`.
5. Method callers are excluded from the caller universe, so an unvalidated
   method call is masked by a separate valid free-function caller.

## Blocking false reds / architectural warning

The same guard rejects contract-valid ordinary Go forms including a
`validate(...) == nil` success branch, init-scoped success branch, switch/select
case, closure defined before validation and invoked after it, and both branches
validating before a common call.

This shows that continuing to extend the custom AST/types walker risks building
a partial Go compiler inside an architecture test. Before the next code change,
re-ratify the proof boundary and choose one of these explicitly:

- a deliberately narrow, documented admitted production form with fail-closed
  structural checks; or
- a narrow exact-edge/source-use guard combined with source-bound functional
  WP04 evidence and a mutation demonstrating that removing/bypassing the
  elapsed-attempt budget fails the mission gate.

Do not describe a partial walker as universal semantic proof.

## Evidence that remains valid

- Real WP04 combined-tree architecture and timeout behavior pass repeatedly
  and under race.
- The exact restore-only edge and sealed 185-mutant census remain intact.
- Full architecture, compile, vet, staticcheck, module, formatting, diff, and
  ownership gates pass.
- Only WP01's architecture guard changed.

The next repair must delete or substantially simplify brittle verifier code,
not merely add five more syntax exceptions.
