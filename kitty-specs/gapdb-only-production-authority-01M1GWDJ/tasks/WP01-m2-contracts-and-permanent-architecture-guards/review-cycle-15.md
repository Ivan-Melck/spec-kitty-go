---
affected_files: []
cycle_number: 15
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-03T20:52:51Z'
reviewer_agent: renata-gapdb-review
wp_id: WP01
---

# WP01 review cycle 15 — coupled semantic substitutions

Verdict: **REQUEST CHANGES** for commit
`8fbe4eee3550bc46b472b034eca49fd027d3a9a1`.

Cycle 14's four literal findings are correctly repaired. All 175 mutants and
the full quality ladder pass. Two simple, source-valid coupled substitutions
still violate the locked defect-5 contract while the guard accepts them.

## 1. Anchor provider revision independently

The validator presently calls whichever ledger field is passed as argument two
to `AdvanceProjectionPrefixV2` the provider-revision field. It then checks that
sorting uses that inferred field. An implementation that changes both the fold
argument and comparator to `completionOrdinal` therefore passes while selecting
the wrong terminal publication.

Repair requirements:

- anchor provider revision independently of the call being validated, using
  the typed acknowledgement/provider-revision source or another independently
  sealed semantic contract;
- prove the exact provider revision is folded and used for ascending order
  before terminal selection;
- add a coupled mutant that substitutes completion ordinal in both the prefix
  fold and comparator; and
- demonstrate that mutant reaches `greatest-revision-covered-through` rather
  than a load, type, or unrelated rule failure.

Do not fall back to a field-name-only spelling guard.

## 2. Prove same-package helper result dependence

The returned-digest taint currently treats any covered-through occurrence
inside a returned call expression as influence. A local helper may accept the
identity and discard it while returning a constant digest; the guard still
passes.

Repair requirements:

- follow same-package call/result dependence far enough to prove that the
  covered-through argument influences the returned digest, or add an equally
  strong executable behavioral proof;
- remain bounded and cycle-safe for helper call graphs;
- add a helper-discards-input mutant; and
- demonstrate that mutant reaches `covered-through-seal-participation` rather
  than a load, type, or unrelated rule failure.

## Preservation requirements

- Preserve every existing 175 mutant ID and all cycle-14 positive controls.
- Extend and reseal the exact census only for the new coupled substitutions.
- Keep edits within WP01's six owned architecture files.
- Preserve exact `[]byte`, genuine zero refusal, any-symbol activation,
  sort-before-select, GapDB-only authority, and every earlier architecture law.
- Rerun the complete WP01 validation ladder and require independent re-review.

Anti-pattern result: synthetic-fixture test **FAIL** and locked decision
**FAIL**; all other items pass or are not applicable.
