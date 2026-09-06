---
affected_files: []
cycle_number: 33
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-06T19:53:34Z'
reviewer_agent: maxwell
wp_id: WP16
---

# WP16 review feedback — cycle 33

Reviewer: Maxwell (`reviewer-renata`)

Verdict: rejected

## Blocking finding

A valid line continuation can assemble the `<<` heredoc operator across
physical lines:

```sh
#!/bin/sh
cat <\
<EOF
sql\ite3 --not-executed data
EOF
printf '%s\n' native
```

Runtime treats the retired spelling as inert heredoc data. The proof requires
adjacent `<<` bytes to recognize a heredoc, misses the split operator, does not
remove the body, and incorrectly reports the body text as an executable
subprocess.

Do not reconstruct the logical heredoc. Recognize an active continuation whose
neighboring bytes can assemble `<<` or `<<-` and return the dedicated typed
`heredoc-declaration-line-continuation` ambiguity before heredoc/body/source
classification. This is an explicit conservative refusal, not an executable-
token finding. It must not expose the body to ordinary subprocess scanning.

## Preserve

- Cycle-33 continued declaration-command false-absence repair.
- Expansion-enabled body continuation refusal and quoted-body opacity.
- Global substitution law, command-target allowlist, script refactors, and all
  preceding invariants.

## Required re-review evidence

- Classify the exact split-operator reproducer as typed heredoc ambiguity, not
  an executable retired-token finding.
- Cover whitespace/CRLF variants, `<<-`, multiple operators, quoted delimiter
  text, near-miss `<` continuations, comparisons/redirections, and escaped
  literal controls.
- Prove split operators cannot leak their bodies into source scanning.
- Re-run focused/full architecture and race/static qualification.
- Qualify the exact integrated tree and recompute all evidence digests.
