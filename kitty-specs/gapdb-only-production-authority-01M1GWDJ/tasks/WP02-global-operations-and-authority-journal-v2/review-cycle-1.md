---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-02T15:45:22Z'
reviewer_agent: user
wp_id: WP02
---

# WP02 Review Feedback — Cycle 1

## Verdict

Reject to `planned`. Commit `5109f49` is provider-neutral, stays within its five
owned files, and passes its existing focused, repeated, race, vet, staticcheck,
formatting, and compile-only gates. Adversarial execution nevertheless found
five contract violations, and the required complete architecture gate is red.

## Issue 1 — Publication identity omits semantic OperationState fields

`planPreimage` in `internal/controljournal/v2/commit_plan.go:304-325` includes
the operation status and result bytes, but omits result kind, result schema
version, result digest, cumulative EventIDs, and cumulative EffectIDs. As a
result, two valid resolved first-publication plans with identical result bytes
but different result kinds and schema versions seal to the same plan digest and
the same PublicationID while producing different canonical sealed bytes.

Reproduction: seal otherwise identical fixture plans with
`result.one@1/{"ok":true}` and `result.two@2/{"ok":true}`. Both seals succeed;
`PublicationID()` and `Digest()` are equal while `CanonicalBytes()` differ.

This violates T009.2, T009.3, T009.10, the required "any semantic change"
test, and the ambiguity law: PublicationID cannot identify two different
sealed publications.

Required correction: derive the plan digest from the complete canonical
OperationState representation excluding only the newly derived
LastPublicationID, or explicitly include every semantic state field. Add a
controlled one-field mutation for each field and prove that every semantic
change changes both plan digest and PublicationID while input-order aliases do
not.

## Issue 2 — First-publication membership and operation scope are not exact

`validatePlanMembers` in `internal/controljournal/v2/commit_plan.go:181-223`
only proves that each supplied plan event/effect appears in OperationState. It
does not prove the reverse. A version-1 state can claim an extra `event-ghost`
and `effect-ghost` while the plan supplies no matching event, head, or effect
intent; `SealCommitPlanV2` accepts it.

The same validator counts an event as the required OperationState transition
solely when `Family == "operation"`. A matching operation ID/version under a
mission-scoped aggregate coordinate is accepted, creating a mission-scoped
operation event/head despite the host-global operation law.

These are direct violations of T009.4, T009.6-T009.8, T010.2, and the normative
host-global operation/publication membership laws.

Required correction: enforce a host-scoped `operation` aggregate coordinate
for the one operation event/head. Make first-publication state membership
exactly equal to the publication's event/effect membership. For later stages,
accept prior state or an independently validated prior-membership digest so
the validator can distinguish cumulative history from ghost additions; do not
silently accept unprovable extras. Add first/later, extra/missing, and
mission/project-scoped operation mutants.

## Issue 3 — Canonical cumulative membership rejects a valid set extension

`AdvanceOperationState` uses `hasPrefixIDs` at
`internal/controljournal/v2/operation.go:390-429`. Because membership is also
required to be globally sorted, adding a new canonical ID that sorts before an
existing ID changes the prefix and is rejected even though no prior member was
lost.

Reproduction: advance from events `[event-z]` / effects `[effect-z]` to the
canonical cumulative supersets `[event-a,event-z]` / `[effect-a,effect-z]`.
The transition returns `INVALID_TRANSITION: operation_membership`.

This violates T008.6 and the required acceptance of canonical monotonic
membership.

Required correction: validate sorted duplicate-free set inclusion, not slice
prefix inclusion. Add insertion-before, insertion-between, append, loss,
duplicate, and replacement cases for both event and effect membership.

## Issue 4 — OperationBinding accepts invalid JSON Schema `date-time` values

`canonicalUTCSecond` at `internal/controljournal/v2/operation.go:112-128`
checks only punctuation and broad numeric ranges. It accepts impossible
calendar dates such as `2026-02-31T12:00:00Z`, despite
`operation-binding-v2.schema.json` declaring `observed_created_at` with
`format: date-time` and T007.8 requiring semantic schema validation in Go.

Required correction: parse and round-trip the exact supported canonical UTC
RFC3339-second form with real calendar validation. Add leap-day, month-length,
timezone-offset, fractional-second, lowercase-zone, and impossible-date
boundaries.

## Issue 5 — The required permanent architecture gate is red

`go test ./internal/architecture -count=1` fails:

```text
rule=ARCH-CLASS-UNKNOWN
source=github.com/spec-kitty/go-kitty3/internal/controljournal/v2
target=github.com/spec-kitty/go-kitty3/internal/controljournal/v2
```

`DefaultPolicy` classifies the v1 journal packages at
`internal/architecture/policy.go:173-177` but not the new v2 production
package. The M2-only test wrapper classifies it transiently; the repository's
permanent closed policy does not. This fails the explicit T011 validation
command and Definition of Done.

WP02 is not authorized to edit `internal/architecture/policy.go`, so this is a
cross-WP ownership blocker. The orchestrator must assign the permanent policy
change to an authorized architecture-guard owner (or explicitly amend
ownership) before WP02 can pass review. Do not weaken the unknown-package rule.
Add a mutant proving deletion of the permanent v2 classification fails the
ordinary repository architecture suite, not only the M2 wrapper.

## Missing required campaign evidence

The implementation has nine test functions, but T008 requires every allowed
and forbidden state edge, and T011 requires maximum/maximum-plus-one campaigns
and the named fault mutants. The probes above demonstrate why the current
small happy-path matrix is insufficient. The repair must add the missing
semantic controls, particularly complete transition pairs, plan-field identity
mutations, first/later binding and membership, every collection budget, unknown
wire versions/fields, and mutation of every caller-owned mutable plan input.

## Verification evidence

Passing baseline gates:

- `go test ./internal/controljournal/v2 -count=20`
- `go test -race ./internal/controljournal/v2 -count=5`
- focused union of the T006-T011 test-name patterns with `-count=20`
- `go test ./... -run '^$' -count=1`
- `go vet ./internal/controljournal/v2`
- `staticcheck ./internal/controljournal/v2`
- `gofmt -d internal/controljournal/v2`
- `git diff --check`

Failing required gate:

- `go test ./internal/architecture -count=1` — unclassified v2 production
  package as detailed above.

All five temporary executable probes were removed. The lane is clean apart
from runtime-owned `.spec-kitty/` state.

## Anti-pattern checklist

1. Dead code: **PASS with planned consumer** — WP03 explicitly consumes the
   sealed CommitPlan, and WP04 owns v2 playback/restore consumers.
2. Synthetic-fixture tests: **FAIL** — the existing fixture suite does not
   exercise complete identity/membership semantics; the false-green probes
   above pass through the production constructors and validators.
3. Silent empty return: **PASS** — no silent empty production return found.
4. FR coverage: **FAIL** — FR-002/FR-004/FR-005/FR-006 and associated
   deterministic/fail-closed requirements are not demonstrated under the
   reproduced publication and membership paths.
5. Frozen surface: **PASS** — commit `5109f49` changes only the five WP02-owned
   files and does not modify mission contracts.
6. Locked decision: **FAIL** — a mission-scoped operation event and
   non-injective PublicationID contradict locked authority-journal laws.
7. Shared-file ownership: **PASS** for implementation; the policy repair is an
   explicit unresolved ownership dependency, not a silent edit.
8. Production fragility: **N/A** — no production panic/raise path was added.
