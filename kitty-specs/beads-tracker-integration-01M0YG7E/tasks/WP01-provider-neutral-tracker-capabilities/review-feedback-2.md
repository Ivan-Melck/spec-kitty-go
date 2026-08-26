# WP01 correction-cycle 2 review feedback

Verdict: changes requested.

## Blocker — portable discussion replay drops and fabricates author attribution

The correction successfully connects the fake to the portable interfaces and
adds compile-time assertions plus full-state mutation sentinels. However, the
supported `DiscussionIdempotent` claim is not yet backed by the complete
portable discussion contract.

`AppendDiscussionRequest.Author` is part of the portable semantic input
(`internal/tracker/capabilities.go:586-592`; data model `DiscussionEntry.Author`
is human-visible attribution). `AppendDiscussion` passes only body and marker
to the legacy store at `internal/tracker/fake/provider.go:113-120`. The durable
comment type has no author field (`internal/tracker/provider.go:15-19`), and the
write at `internal/tracker/fake/provider.go:327-328` persists only body, marker,
and revision. `ReadWork` therefore reconstructs discussion entries with an
empty author at `internal/tracker/fake/provider.go:83-87`.

This also makes replay historically incoherent. The replay check at
`internal/tracker/fake/provider.go:308-312` compares only marker and body. A
second request using the same operation/body but a different author is accepted
as a replay, and `AppendDiscussion` then constructs the returned entry using
the *new request's* author at line 129 even though that author was never stored.
After reopen, `ListDiscussion` returns the same entry with no author. This
violates T004's coherent replay requirement, T005's marker-reuse-with-different-
semantic-input requirement, and the contract requirement that supported
discussion operations exercise their full portable shape.

The new conformance table does not catch the defect: its discussion case omits
`Author` and asserts only `len(entries) == 1` at
`internal/tracker/fake/conformance_test.go:40-49`.

Remediation:

1. Persist the complete semantic identity required for a portable discussion
   entry, including author attribution (and the contracted observation fields
   that the provider can supply), while retaining a compatible reader for fake
   store version 1 if the durable schema advances.
2. Include every persisted semantic input in idempotency/replay comparison.
   Same marker with a different author must not be reported as the original
   operation.
3. Extend conformance to append through `tracker.Discussion`, reopen the store,
   list the entry, and compare author/body/marker/observation. Add a mutant case
   for same marker and body with a different author, and prove durable state is
   unchanged on refusal.

## Prior blocker disposition

- Capability-to-port correspondence: **PARTIAL** — compile-time assertions,
  concrete portable methods, the complete capability table, and unsupported
  mutation sentinels are now present. The discussion defect above means the
  claimed supported operation still does not satisfy its full contract.
- Canonical binding in inspection: **CLOSED** — `TrackerBinding` covers the
  mission data-model fields, validates canonical absolute paths, concurrency
  mode, optional SHA-256 syntax, identity, project, protocol, and observation
  time; `NewInspection` validates and normalizes it; the fake returns it through
  `Inspector`; tests exercise the contract shape and defensive diagnostics.

## Anti-pattern checklist

1. Dead code: **PASS** — the public constructors and portable ports now have
   production use in the fake adapter.
2. Synthetic-fixture tests: **PASS** — tests invoke production validation,
   adapter, durable-store, and refusal paths rather than asserting literals.
3. Silent empty return: **PASS** — reviewed nil/empty results are explicit
   unsupported-operation results or ordinary success sentinels.
4. FR coverage: **FAIL** — FR-001/FR-006's supported discussion capability is
   asserted at the enum level but its durable portable semantics are not
   covered and are incorrect as described above.
5. Frozen surface: **PASS** — no planning contract or frozen documentation was
   modified by the WP commits.
6. Locked decision: **PASS** — no provider-name branching, Beads dependency,
   false read-then-write CAS, or execution-authority leakage was found.
7. Shared-file ownership: **PASS** — all cumulative changed files remain inside
   WP01's exclusive write scope.
8. Production fragility: **PASS** — no panic/bare-raise equivalent or transient
   race fail-open path was introduced.

## Evidence run

- `go test ./internal/tracker ./internal/tracker/fake -count=10` — pass
- `go test -race ./internal/tracker ./internal/tracker/fake ./internal/execution` — pass
- `go vet ./internal/tracker/... ./internal/execution/...` — pass
- `go test ./internal/execution ./internal/helpercmd ./internal/scenario -run '^$'` — compatibility compilation pass
- `git diff --check` — pass
- Contract walk: `tracker-capabilities.md` in scope; Beads JSON protocol and
  qualification evidence schema remain orthogonal to WP01.

WP03 depends on WP01 and should remain blocked until this correction is
implemented and independently re-reviewed.
