---
affected_files: []
cycle_number: 2
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T19:13:14Z'
reviewer_agent: mill
wp_id: WP08
---

# WP08 review feedback — correction cycle 2

Verdict: **REQUEST CHANGES**

Reviewed dependency repair `08046551a12a501295f67d809514080db163effd`
and WP08 candidate `e7356f642e0d79454cc9077031673d1faaa9c521`
(tree `f8ef886360527ff63b9b4db3bba1bd49cf8510e8`).

The checkpoint-plus-suffix restore chronology, complete defensive authority
capsule, callable GapDB and Git seams, and production 100-trial zombie scenario
are accepted as closed. Two high-severity findings remain.

## 1. Replace decorative mutant controls with executable production kills

`internal/workspace/mutant_v2_test.go` now lists all 18 required mutants, but
several entries still test a correct helper or a test-only predicate rather
than the production decision named by the mutant:

- `M-WORKSPACE-EXPIRY-WITHOUT-QUARANTINE` only constructs and reduces the
  correct quarantine contribution.
- `M-WORKSPACE-SAME-VALUE-CAS-AS-ASSERTION` terminates in the test-only
  `validateExecutionAssertionForTestV2`; weakening the production stage plan
  from a read-only assertion to a write would not make it fail.
- `M-WORKSPACE-EXECUTION-EXPIRES-BINDING-PUBLISHES` and
  `M-WORKSPACE-BINDING-WINS-EXPIRY-OMITS-QUARANTINE` do not exercise
  `WorkspaceAuthorityServiceV2`, the execution/workspace composition, or its
  sealed publication membership.

Replace these synthetic predicates with type-correct production overlays,
injected dependency mutations, or equivalent executable mutations. Each named
mutant must have a passing control and must be killed by the exact production
law it names. A census count, grep assertion, or direct call to the correct
constructor is insufficient.

## 2. Bound and redact Git outbound failures

`internal/gitrepo/workspace_authority_v2.go` currently returns repository and
runner errors unchanged. Runner errors can contain the full working directory,
Git arguments, and unrestricted command output; `workspace.Error` renders its
cause. This crosses the workspace capability boundary with unbounded private
provider data and violates T040.10 and T043.7.

Translate failures at the v2 workspace Git adapter boundary into typed,
bounded, redacted workspace diagnostics. Preserve useful classification (and
`errors.Is` behavior where required) without exposing command output, socket or
filesystem paths, or raw provider values. Add adversarial tests with oversized
Git output and private paths that prove both the public error-size bound and
redaction.

## Required rereview evidence

- Demonstrate that each repaired mutant survives its explicit control and is
  killed when the corresponding production seam is mutated.
- Run focused service/composition/mutant tests repeatedly and under the race
  detector where applicable.
- Run the Git adapter redaction/size tests, ordinary adapter tests, vet,
  staticcheck, compile-only repository tests, formatting, and diff checks.
- Preserve the already-green restore and production zombie evidence; rerun
  affected portions rather than weakening them.

