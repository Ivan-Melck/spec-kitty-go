---
affected_files: []
cycle_number: 17
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T06:52:55Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 17

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

Function definitions are correctly isolated at definition time, but later
function invocation neither applies nor conservatively refuses their tracked
state effects:

```sh
review_retired_cli=native
select_retired_cli()
{
  review_retired_cli=sql\ite3
}
select_retired_cli
"$review_retired_cli" /tmp/x
```

This valid POSIX shell executes the retired command. The current proof reports
no finding.

Do not grow an unbounded shell interpreter. Define a bounded function contract:
collect definitions and scan their bodies; when a defined function is invoked,
apply a sound bounded summary of its tracked effects or fail with typed shell
ambiguity. An unmodeled function side effect must never create absence. Direct
or indirect forbidden execution in a body remains a finding even if the
function is not called.

## Adjacent correctness findings

- Bash `&>`/`&>>` are redirection operators, not background `&` followed by
  `>`. Normalize them with longest-match lexing so a current-shell brace group
  does not become falsely isolated.
- Heredoc bodies are data, not command tokens. A clean heredoc containing the
  retired word as prose must not become a subprocess finding. Recognize bounded
  heredoc delimiters and exclude body data while continuing to scan executable
  syntax before and after the body; malformed/unbounded forms fail ambiguity.

## Preserve

- All cycle-17 fd redirection, compound scope, multiline function syntax,
  assignment, dispatch, lexer, ambiguity, and data controls.
- The independently accepted observed receipt and schema-3 evidence design.
- All source/module/SBOM/binary/install/help/runtime/sidecar/source-identity,
  manifest, and inventory invariants.

## Required re-review evidence

- Kill the exact function-state mutation attack and adjacent invoked-function
  state variants; accept safe defined/called functions without tracked effects.
- Correctly handle `&>`/`&>>` brace redirections and bounded clean heredoc data,
  while still detecting executable commands around heredocs.
- Update and verify the closed observed census with zero anomalies.
- Qualify the exact final integrated tree and recompute all evidence digests.
