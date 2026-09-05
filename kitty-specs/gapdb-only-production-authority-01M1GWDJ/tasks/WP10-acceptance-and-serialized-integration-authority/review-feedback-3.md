# WP10 review feedback — cycle 3

Verdict: **REQUEST CHANGES**

Independent review covered cycle-3 commits
`374ce3ccb3811ee37d8057b4a976b68fbf19fde5` and
`473b0eea579842c4a355bb5ffd30475b59f4742b` (product/test tree
`5dea53143d66ec9c0dff1cdd0f6f27dd14d86c72`). The current lane has no later
diff under WP10-owned product surfaces. Both cycle-2 production defects are
closed: JSON excludes the provider cause without losing `errors.Is`/`As`, and
an exact pending cleanup operation observes its stable effect before any retry
mutation. One mandatory qualification defect remains.

## Blocking: the waiver-omission mutant is false-killed by unrelated fixture state

`M-WP10-WAIVER-OMITTED` removes the production call's waiver-policy failure,
then runs
`TestAcceptancePolicyWaiverAndApplicabilityNeedCurrentDurableFactsV2/caller_minted_waiver`.
That fixture changes `criterion-docs` from inapplicable to waived but retains
the old `policy-docs-inapplicable` observation in `authority.PolicyFacts`.

With the exact source overlay active, acceptance still refuses and publishes
zero plans because the unrelated obsolete fact later triggers
`unexpected_policy_fact`. The campaign nevertheless calls this a kill because
it only searches for the test's generic failure prefix. Independent child
output was:

```text
M-WP10-WAIVER-OMITTED
caller-minted waiver accepted: stale_authority: acceptance authority refused plans=0
```

Therefore the overlay does not prove that omitting waiver authority is caught
for the intended semantic reason, contrary to T053's explicit requirement.
The other six newly added overlays were independently exposed and failed on
their intended production behavior.

Repair the killer so its baseline authority contains exactly the policy facts
applicable to the mutated waiver request (or add a dedicated omission control):
start from a valid, durably authorized waiver, remove only that waiver fact and
its exact assertion, and require typed refusal plus zero publication. Under
the omission overlay, the same fixture must otherwise reach acceptance; the
campaign must distinguish that escape from an unrelated stale/unexpected-fact
refusal. Keep and reseal all 27 IDs.

## Validation evidence

Passed:

- exact cycle-2 closure controls, count 20 (`2.526s`): custom provider JSON
  marshaler not invoked, `errors.Is`/`As` preserved, pending cleanup restart
  observed before mutation, and ambiguous/unavailable observation caused zero
  cleanup mutations;
- full declared 27-overlay campaign (`10.617s`), with the waiver false-kill
  independently exposed as described above;
- complete `internal/integration`, count 3 (`27.140s`);
- focused real GapDB acceptance/integration/WP10, count 3 (`24.511s`);
- focused race (`3.714s`);
- focused authority/GapDB architecture laws (`1.031s`);
- `go vet`, `staticcheck`, whole-module compile-only test, `go mod verify`,
  `gofmt`, and `git diff --check`;
- exact cycle-3 ownership: six changed files, all under
  `internal/integration/**`; `5be3b19c` changes only prescribed planning
  artifacts.

The supplied unfiltered GapDB count-10 timeout remains correctly
non-admissible and was not used as passing evidence.

## Anti-pattern checklist

1. **Dead code — PASS**: new production behavior is reached through existing
   integration and GapDB composition paths.
2. **Synthetic-fixture test — FAIL**: the waiver mutant's fixture contains an
   unrelated stale policy observation that masks the mutated production law.
3. **Silent empty return — PASS**: no silent empty failure path was introduced.
4. **FR coverage — FAIL**: T053's mandatory waiver-omission mutant lacks an
   intended-reason production kill.
5. **Frozen surface — PASS**: no frozen contract/schema surface changed.
6. **Locked decision — PASS**: the cycle-2 production-law violations are
   closed; no new contradiction was found in the correction code.
7. **Shared-file ownership — PASS**: all cycle-3 product/test edits are inside
   the declared WP10 authority surface.
8. **Production fragility — PASS**: no new panic or unbounded transient-race
   failure path was introduced.

## Downstream impact

WP11 and WP18 must wait for the corrected mandatory waiver mutant evidence.
