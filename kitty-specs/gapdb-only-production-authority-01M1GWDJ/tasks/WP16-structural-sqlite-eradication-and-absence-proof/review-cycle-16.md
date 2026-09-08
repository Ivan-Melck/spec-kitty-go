---
affected_files: []
cycle_number: 16
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T06:41:10Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 16

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking findings

### File-descriptor duplication defeats compound-tail isolation

```sh
review_retired_cli=sql\ite3
{ review_retired_cli=native; } 2>&1 | "$review_retired_cli" /tmp/x
```

The same escape exists with `&` after the fd-duplication redirection. These are
valid shell forms. The lexer represents `2>&1` as `2`, `>`, `&`, `1`, while the
tail classifier assumes a three-token redirection. It therefore fails to see
the later isolating operator and leaks group state into the parent.

Normalize redirection syntax structurally before compound-tail classification.
Support bounded fd prefixes, file targets, fd duplication/closure, and repeated
redirections, or fail typed ambiguity. An `&` consumed as a redirection operand
must not be confused with a background operator, and a later real `|` or `&`
must remain visible.

### Multiline POSIX functions are false positives

```sh
helper()
{
  printf '%s\n' native
}
```

The delimiter-preserving stream retains the newline as `;`, but function
recognition requires adjacent `name ( ) {`. The proof then treats `(` as a
subshell following command `helper` and returns `unsupported-subshell-boundary`.

Carry function-declaration recognition across allowed newline separators before
the opening brace. The function body remains executable source and must still
be scanned for forbidden execution. Declaration grammar must not mutate the
current shell merely because the function is defined.

## Preserve

- Cycle-16 compound-command scope and all immediate/file-redirection group
  isolation cases.
- Current-shell group persistence, nested group/subshell semantics, assignment
  expansion, dispatch handling, and data controls.
- The independently accepted observed receipt and schema-3 evidence design.
- All source/module/SBOM/binary/install/help/runtime/sidecar/source-identity,
  lexer, ambiguity, manifest, and inventory invariants.

## Required re-review evidence

- Kill exact `2>&1` pipeline and background group attacks, plus repeated/fd-close
  redirection variants.
- Accept valid single-line and multiline POSIX function declarations without
  treating `()` as a subshell; still detect forbidden execution in their bodies.
- Update and verify the closed observed census with zero anomalies.
- Qualify the exact final integrated tree and recompute all evidence digests.
