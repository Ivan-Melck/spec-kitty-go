# WP06 review cycle 6: changes requested

Candidate `8c4c5c9` fixes the production field translations: dependency list
records now require `id` plus `dependency_type`, comment records require
`text`, list direction remains blocked-to-blocker, removal stays typed, and
the provider mutation result is independently re-observed before success.
Focused x10, focused race, helper call-census, architecture/mutant, vet, and
diff checks pass.

## Blocker: the new "real v1.2.2" mutation evidence is not byte-real

`internal/tracker/beads/graph_test.go` labels
`realV122DependencyReceipt` as a real v1.2.2 receipt, but encodes:

```json
{"depends_on_id":"bd-distinct-prerequisite","dependency_type":"blocks","future_optional":{"ignored":true}}
```

The pinned v1.2.2 binary and source tag `6c124203e771433a3550c348771a5b5e27fd3c21`
emit different object receipts:

```json
{"status":"added","issue_id":"<blocked>","depends_on_id":"<blocker>","type":"blocks"}
{"status":"removed","issue_id":"<blocked>","depends_on_id":"<blocker>"}
```

The fake provider repeats the same invented `dependency_type` mutation shape
for both add and remove. This is precisely the list-versus-mutation distinction
that reopened WP06: dependency *list* entries carry blocker `id` and
`dependency_type`, while mutation receipts are objects with `depends_on_id`
and the add receipt uses `type`. The decoder happens to reject the invented
receipt as a list record, so current tests pass, but the claimed provider
evidence is false and would allow later code to learn the wrong wire contract.

### Required remediation

1. Replace the add and remove receipt fixtures with separate byte-real v1.2.2
   payloads, including their exact differing fields.
2. Make the WP06 fake runner return those real object shapes for dependency add
   and remove.
3. Assert that neither exact mutation receipt can be decoded as a dependency
   list record, while exact list records still decode only from `id` plus
   `dependency_type` and preserve the asymmetric default blocked endpoint.
4. Keep forward-compatibility tests separate from byte-real fixtures: add
   explicit unknown optional fields to derived skew fixtures rather than to a
   constant named as a real capture. Apply the same separation to the comment
   and ready captures so exact provider evidence and optional-field tolerance
   remain independently auditable.
5. Preserve all currently green direction, typed removal, duplicate readiness,
   append/replay/ambiguity, full call-census, capability, and architecture
   controls. Do not add assignment behavior.

## Anti-pattern checklist

1. Dead code: **PASS** — graph/discussion ports have the shipped
   `coordinate-beads` caller.
2. Synthetic-fixture test: **FAIL** — the new provider receipt fixture is
   synthetic while being represented as exact real-provider evidence.
3. Silent empty return: **PASS** — no new silent failure path.
4. FR coverage: **FAIL** — the reopened exact v1.2.2 receipt distinction is
   not truthfully covered.
5. Frozen surface: **PASS**.
6. Locked decision: **PASS** — no authority leakage or human-output parsing.
7. Shared-file ownership: **PASS** — the helper-only out-of-owned delta is a
   narrowly necessary public-caller fixture update and the candidate commit
   records that coordination intent.
8. Production fragility: **N/A** — no new production panic/raise path.

Downstream warning: WP08 and WP09 depend on WP06 and must consume the corrected
receipt fixtures/census after this change.
