# WP15 review feedback — cycle 3

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

1. The advertised-operation proof still stops at a dispatch spy rather than a
   real Host capability. `TestEveryDeclaredNativeOperationHasAConstructibleWireAndResult`
   supplies skeletal handoff, review, acceptance, integration, cleanup, and
   effect requests to `capabilityDispatchSpy`, whose methods immediately return
   `errCapabilityReached`. Several requests are invalid under their real domain
   validators. Replace or supplement this with one documented all-snake-case,
   semantically valid request per advertised kind executed through the real
   Host composition. Each request must either return its matching typed result
   or reach an expected, stable domain refusal after passing request validation.
   A routing spy is useful unit coverage but is not the required projection
   proof.
2. Epoch normalization currently changes request semantics after the command
   digest is computed. `request.go` hashes the caller bytes, then
   `productionSession.Execute` looks up only the operation ID and rewrites the
   request's supplied epoch before the domain service constructs its operation
   binding. An absent request with a stale or forged epoch can therefore be
   silently changed to the current epoch instead of returning stale authority.
   For an existing operation, changing only the epoch changes the command hash
   but can be normalized back to the stored epoch and resolve as exact rather
   than an idempotency conflict. Preserve the caller's canonical typed request
   identity: first attempts must validate their supplied epoch, and resume may
   recover original attribution only after proving the canonical request is the
   same durable operation request. Add tests for absent stale epoch and an
   existing operation replay whose only mutation is the epoch.

## Preserve

- Exact admitted-sidecar query/restart and dropped-ack resume/conflict tests now
  pass.
- Explicit recursive snake-case DTO decoding is materially improved.
- Successful drive steps retain typed results, and the closed error classifier
  has domain-specific coverage.
- The readiness observation interface is mission-owned and structurally
  query-only; architecture guards pass.
- No SQLite/backend fallback or compatibility protocol may be introduced.

## Required evidence

- Exercise all advertised operations through real Host capabilities using
  valid all-snake-case requests, with typed results or domain diagnostics.
- Prove an absent stale-epoch request is refused without normalization.
- Prove an existing operation whose canonical request changes only by epoch is
  an idempotency conflict, while an exact lost-ack replay still resumes.
- Re-run exact-sidecar native tests, focused/race tests, architecture guards,
  package compilation, vet, staticcheck, and CGO-free builds.
