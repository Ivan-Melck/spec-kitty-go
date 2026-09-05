---
affected_files: []
cycle_number: 2
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T02:47:58Z'
reviewer_agent: user
wp_id: WP10
---

# WP10 review feedback — cycle 2

Verdict: **REQUEST CHANGES**

Independent review covered correction commits
`b1b90fc81fc6f344d6234ba82bb88d0e305231cd` (tree
`21b538e1b389c073321dc17ba6b71d038df1321f`) and
`4749b7dad5079de090d4d6d283f48441f241ee7a` (combined product tree
`6f965b1d64580dedf6b691072d1f12d5c55b9396`). The current lane has no later
diff under the WP10-owned product surfaces. The cycle-1 policy, attributable
ref-publication, real GapDB/restore, durable evidence-bound, and cleanup-path
repairs are materially present, and the focused, race, compile/static, and
declared 20-mutant gates listed below pass. Three contract blockers remain.

## 1. Provider errors still leak through JSON

`internal/integration/model.go:35-47` retains the raw outbound error in the
exported `Error.Cause` field. Removing the cause from `Error()` protects only
the text rendering. `encoding/json` traverses the exported interface value and
invokes a provider error's `MarshalJSON`, so an adapter can still expose
unbounded command output, paths, credentials, or provider details.

An external overlay test constructed a provider cause whose `MarshalJSON`
returned a 16 KiB secret, passed it through the real `boundedGitFailureV2`, and
then marshaled the returned `*Error`. It failed with:

```text
provider cause leaked in JSON: bytes=16124
prefix="{\"Kind\":\"git_failure\",...,\"Cause\":{\"provider_secret\":..."
```

Repair the stable serialization boundary (for example, exclude the raw cause
from JSON or provide a bounded custom marshal form) while retaining `Unwrap`
for `errors.Is`/`errors.As`. Add a permanent control using a custom
provider-error marshaler and assert both bounded/redacted JSON and preserved
typed cause classification.

## 2. Cleanup restart blindly repeats an already-applied external effect

`IntegrationCleanupServiceV2.Cleanup` publishes `CleanupPending`, invokes
`CleanupWorkspace`, and only calls `ObserveCleanup` when the mutation call
itself returns an error (`internal/integration/cleanup_effect_v2.go:125-146`).
If cleanup succeeds and the following `CleanupComplete` publication fails or
the process crashes, the exact in-progress retry invokes `CleanupWorkspace`
again without first reconciling the stable `CleanupEffectID`.

An external production-path overlay used the real cleanup service and domain
port, allowed the cleanup effect to succeed, forced only the terminal authority
publication/reconciliation to fail, and retried the same cleanup operation.
It failed with:

```text
restart blindly repeated already-observed cleanup effect: mutations=2 want=1
```

This violates the attribution-safe retry law even though cleanup does not
control terminal integration truth. On a `CleanupPending` resume, reconcile
the stable effect first. If the exact observation exists, publish completion
without another mutation; only a definitive attributable absence (or an
explicitly authorized retry state) may permit another external call. Add the
success-then-authority-failure/restart control and a mutant that restores the
blind retry.

## 3. The 20-mutant census is executable but not the mandatory census

`SPEC_KITTY_RUN_WP10_MUTANTS=1` executes and kills all 20 declared overlays,
which is useful evidence. However, `wp10ControlledMutantsV2` does not include
several mutations required verbatim by T053:

- acceptance omits artifact evidence;
- acceptance omits workgraph evidence;
- acceptance omits waiver evidence/authority;
- a stage retry changes `OperationID` or a stable `EffectID`.

The existing candidate/review/policy-detail and cleanup-digest mutants do not
mutate those distinct production laws. The campaign also has no mutant for the
cleanup blind-retry defect above. Extend and reseal the census with
type-correct production overlays, passing controls, and intended-reason kills
for every mandatory entry; do not replace the existing 20 IDs.

## Validation evidence

Passed:

- focused integration acceptance/integration/cleanup/refusal suite, count 10
  (`84.938s`);
- focused real GapDB WP10 suite, count 3 (`24.191s`);
- declared 20-source-mutant campaign (`8.982s`);
- focused race suite (`4.039s`);
- focused authority/GapDB architecture laws (`0.861s`);
- `go vet`, `staticcheck`, compile-only `go test ./...`, and `go mod verify`;
- `gofmt` on all owned surfaces and `git diff --check`;
- exact owned product scope: 12 files, all within `internal/integration/**` or
  the four declared GapDB acceptance/integration files.

The broad unfiltered architecture invocation was stopped after approximately
90 seconds once the two production blockers were independently reproduced;
the relevant bounded architecture subset passed.

## Anti-pattern checklist

1. **Dead code — PASS**: reducer registrations and cleanup service have
   production adapter callers; real GapDB composition exercises them.
2. **Synthetic-fixture test — PASS**: the repaired acceptance, integration,
   cleanup, GapDB, and restore tests invoke production services/adapters; the
   remaining problem is missing coverage, recorded under FR coverage below.
3. **Silent empty return — PASS**: no newly introduced silent empty-return
   failure path was found.
4. **FR coverage — FAIL**: NFR-004/T052.10 attribution-safe cleanup retry and
   T053's complete mandatory mutant evidence are not satisfied.
5. **Frozen surface — PASS**: no frozen contract/schema surface was modified.
6. **Locked decision — FAIL**: exported JSON leaks provider details and cleanup
   can blindly repeat an already-attributable effect, contradicting the
   bounded-diagnostic and no-blind-retry laws.
7. **Shared-file ownership — PASS**: both product correction commits stay
   within the 12 WP10-owned files; `670b7606` is lifecycle cleanup only.
8. **Production fragility — PASS**: no new panic/bare-raise equivalent was
   introduced; failures remain typed/fail-loud.

## Downstream impact

WP11 and WP18 depend on WP10 and must consume the next repaired candidate, not
this rejected product tree.
