# WP03 correction-cycle-4 review — stdout error-envelope scoping

Verdict: changes requested for candidate `865587823`.

The correction successfully decodes the byte-real Beads v1.2.2 missing-show
envelope from bounded stdout, keeps stderr non-semantic, maps that show result
to typed `not_found`, preserves redaction, and passes focused, race, repeated,
coverage, vet, contract, and scope gates. One ambiguity regression still
blocks approval.

## Blocker — the show-only envelope can falsely resolve a mutation

`decodeDocumentedError` accepts the new
`{"schema_version":1,"data":{"error":"..."}}` shape for every command.
`Adapter.execute` then marks every mutating request with any successfully
decoded error shape as `mutationNotApplied` before it checks that the new shape
has characterized meaning only for `CommandShow`.

Consequently, if a started create/update/dependency/comment command exits
nonzero and emits the exact byte-real missing-show envelope, the adapter returns
typed `unavailable` with `mutation_outcome=not_applied`. That response has no
provider code and is not a conclusive mutation response. It must remain
malformed/ambiguous under WP03's monotonic ambiguity law.

Reviewer reproduction using the production adapter and fake runner:

```text
request:  CommandCreate, started=true, exit=1
stdout:   realV122MissingShowStdout
stderr:   realV122MissingShowStderr
expected: kind=ambiguous, mutation_outcome=ambiguous
actual:   kind=unavailable, mutation_outcome=not_applied
```

Remediation:

- Scope `documentedDataErrorEnvelope` interpretation to the explicitly
  characterized command kind (`CommandShow`) before treating it as a
  conclusive provider rejection.
- For every other command kind, classify that uncoded shape as malformed; if
  the command is a started mutation, preserve `mutationAmbiguous` and
  `observe_then_reconcile`.
- Add a small command-kind matrix using the same byte-real fixture: show maps
  to `not_found`; a non-show read does not acquire `not_found`; a started
  mutation remains ambiguous. Retain the existing stderr-JSON mutant and
  redaction assertions.

Direct schema-1 error objects carrying an explicit documented provider `code`
may remain conclusive where the command contract permits them; the blocker is
the newly characterized uncoded `data.error` shape being treated generically.

## Anti-pattern checklist

1. **Dead code: N/A** — this is an accepted protocol foundation for dependent
   adapter work.
2. **Synthetic-fixture test: PASS** — the correction tests invoke the real
   production decoder/adapter paths.
3. **Silent empty return: PASS** — no new silent empty-return path found.
4. **FR coverage: FAIL** — FR-002/FR-017 lack the adversarial cross-command
   assertion above, allowing a provider-neutral mutation outcome to be
   downgraded incorrectly.
5. **Frozen surface: PASS** — candidate `865587823` modifies only WP03-owned
   files.
6. **Locked decision: FAIL** — a started mutation without a command-valid,
   conclusive response can become `not_applied`, contradicting the explicit
   monotonic ambiguity rule.
7. **Shared-file ownership: PASS** — no out-of-map implementation file changed
   in the candidate.
8. **Production fragility: PASS** — no new panic or fail-open transient path.

## Verification evidence

Passing before the reviewer mutant was introduced:

- `go test ./internal/tracker/beads -count=1 -timeout=6m`
- `go test -race ./internal/tracker/beads -count=1 -timeout=8m`
- `go test -count=20 ./internal/tracker/beads -timeout=8m`
- `go vet ./internal/tracker/beads`
- protocol decoder functions report 100% statement coverage
- `make contract`
- `make scope`

The temporary reviewer mutant was removed after reproducing the failure; no
implementation files were edited. The WP frontmatter still names
`implementer-ivan`, so the correction handoff profile is already appropriate.
