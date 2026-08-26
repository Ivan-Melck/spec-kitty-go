# WP01 review feedback

Verdict: changes requested.

## Issue 1 — Declared capabilities are disconnected from the portable operation ports

`internal/tracker/fake/provider.go:17-30` declares catalog-read,
observably-idempotent discussion, atomic assignment, and conditional
compensation support. The provider only proves the legacy composite and the
capability reporter at `internal/tracker/fake/provider.go:14-15`. It does not
implement the portable `Catalog`, `Discussion`, or `AssignmentProjection`
ports defined at `internal/tracker/capabilities.go:523-527`, `:607-610`, and
`:643-647`; in particular, its legacy `Read(context.Context, string)` has a
different method signature from `Catalog.Read(context.Context,
ExternalWorkRef)`.

The compile-time test at `internal/tracker/capabilities_test.go:215-225`
proves only independent dummy collaborators. The fake conformance test at
`internal/tracker/fake/conformance_test.go:13-31` checks enum values, not that
the fake can be consumed through the operation ports those values advertise.
Consequently an application can receive `CatalogRead == supported` and still
have no corresponding portable read interface. This fails T004's requirement
that declared fake capabilities match compile-time interfaces, T005's
supported-operation conformance requirement, and the usable provider-neutral
surface required by FR-001/FR-006.

Remediation:

1. Make each declared supported guarantee correspond to a concrete narrow
   consumer port. Preserve the legacy `Provider` method set, but do not leave a
   second, disconnected catalog/discussion/assignment API.
2. Add compile-time assertions showing that the fake satisfies every portable
   port for which it declares support (or reduce/restructure the declaration so
   it is truthful and usable).
3. Drive the conformance suite table from the complete capability report.
   Exercise each supported operation through its portable port and prove each
   unsupported operation refuses before durable effects. Keep the existing
   full-state mutation sentinel and queued-failure mutant control.

This remediation should also remove the currently dead public surface:
`NewProviderObservation`, `NewHealthReport`, and `NewWorkRecord` have no
production callers outside their declarations. The WP anti-pattern gate
requires new public functions to have a live non-test consumer.

## Issue 2 — `Inspection` drops the canonical tracker binding required by the contract

The mission contract pins `Inspection.Binding TrackerBinding` at
`contracts/tracker-capabilities.md:12-17`, and the data model makes canonical
workspace identity part of the non-mutating preflight boundary. The
implementation at `internal/tracker/capabilities.go:649-660` defines no
provider-neutral `TrackerBinding` and returns only capabilities, health, and
diagnostics.

This is not a cosmetic field omission: WP04 owns only files below
`internal/tracker/beads/`, so it cannot later add the missing provider-neutral
binding to `tracker.Inspection` without crossing WP01's ownership boundary. An
inspector using the current type cannot prove which executable/workspace
identity its health and capability report describes.

Remediation:

1. Add the provider-neutral binding/identity value required by the contract and
   include it in `Inspection`, with validation and defensive-copy behavior as
   appropriate; or remove the premature `Inspector` surface and explicitly
   reslice ownership so the package that defines inspection can add the exact
   contracted result before WP04 starts.
2. Add a contract round-trip assertion proving a successful inspection carries
   binding identity, capabilities, health, and bounded diagnostics together.

## Review checklist

1. Dead code: **FAIL** — the three public constructors named above have only
   test callers, and the future portable ports have no production implementer.
2. Synthetic-fixture tests: **PASS** — inspected assertions invoke the new
   validation, error, fake-store, and mutation code paths.
3. Silent empty return: **PASS** — reviewed empty/nil returns are validation
   success paths or documented internal sentinels, not swallowed failures.
4. FR coverage: **FAIL** — FR-001/FR-006 are represented as types and enum
   assertions but are not proven as a usable capability-to-operation contract.
5. Frozen surface: **PASS** — no contract, README, plan, or other frozen
   planning surface changed in the WP diff.
6. Locked decision: **PASS** — no provider-name switch, Beads dependency,
   read-then-write CAS claim, or execution-authority leakage was found.
7. Shared-file ownership: **PASS** — all five changed files are inside WP01's
   exclusive lane ownership.
8. Production fragility: **PASS** — no new panic/bare-raise equivalent or
   transient-race fail-open path was introduced.

## Evidence run

- `go test ./internal/tracker ./internal/tracker/fake -count=10` — pass
- `go test -race ./internal/tracker ./internal/tracker/fake` — pass
- `go vet ./internal/tracker/... ./internal/execution/...` — pass
- `go test ./internal/execution ./internal/helpercmd ./internal/scenario -run '^$'` — compatibility compilation pass
- The implementer's existing 748-second full scenario pass was accepted as
  supplied evidence and was not redundantly rerun.
- `beads-json-protocol.md` is orthogonal to WP01 (WP03); the qualification
  evidence schema is orthogonal (WP09/WP10). `tracker-capabilities.md` is in
  scope and produced Issue 2 above.

WP03 depends on WP01 and must not consume the current capability surface until
these changes are reviewed.
