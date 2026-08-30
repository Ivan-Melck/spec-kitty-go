# Evidence and Adoption Contract v1

## One-run rule

Admissible M1 evidence is one content-addressed
`evidence-manifest-v1.schema.json` document whose RunID covers:

1. exact sidecar provenance;
2. each of the 14 external-adapter scenarios exactly once;
3. each stable fault cut `GJ-01`–`GJ-05`;
4. the ordered two-agent torture trial;
5. universal authority, workspace, review, tracker, and integration invariants;
6. architecture and behavior mutant results; and
7. independent review.

Results assembled from different umbrella RunIDs, reruns that overwrite failed
records, or a manifest that merely references an earlier 14/14 campaign are
non-admissible.

An admissible pass manifest contains exactly 14 ordered top-level scenario
records using the historical slash-capable IDs, exactly five ordered GJ records,
and one dedicated ordered-trial object. Repetition counts belong only to focused
fault/property/concurrency subtrial summaries; they never multiply or conflate
the top-level 14+1 census. An in-progress child retry retains its identity. Only
after a campaign is terminally failed or incomplete may the whole campaign be
rerun, under a new RunID with the earlier evidence preserved.

The base schemas also admit honest `fail` and `incomplete` manifests:
scenario/fault placeholders may be not-started, nonterminal, or incomplete;
mutants may survive or remain not-run; invariants and ordered-trial crown fields
may be false or absent. Valid evidence is not synonymous with a pass.

The deterministic pass predicate requires every one of these conjuncts:

- exact 14 scenarios and five faults are started, terminal, and passed;
- exact 19 named mutants are killed;
- independent review is `pass`;
- the ordered trial is terminal/pass with FR-044 and SC-013 true;
- all fixed universal invariants are true; and
- these eight summaries each have
  `repetitions_started == repetitions_terminal == repetitions_passed >= 100`:
  `atomic-publication-nfr001`, `lost-response-idempotency-nfr002`,
  `concurrent-claims-nfr003`, `competing-integrations-nfr004`,
  `restore-playback-nfr005`, `restart-epoch-nfr007`,
  `zombie-isolation-nfr008`, and `attribution-reassignment-nfr015`.

Schema conditionals reject most vacuous passes; the semantic verifier completes
the equality, cross-record identity, and attribution checks. A failed conjunct
forces `fail` or `incomplete`; the record is retained rather than discarded.

## Artifact rules

Every child artifact carries RunID, scenario/trial ID, deterministic seed,
fault/cut identity when applicable, admitted tuple digest, sidecar provenance
digest, repository before/after identity, journal interval, checkpoint identity,
observed invariants, start/end time for diagnostics, outcome kind, and its own
SHA-256. The manifest uses repository-relative paths under one immutable
evidence directory and rejects traversal or duplicate logical identities.

The verifier recomputes hashes and semantic completeness. In addition to schema
validation it enforces: all child RunIDs equal the umbrella; scenario/fault IDs
are exact and unique; runtime epochs before/after restart differ; review actors
are absent from contributor sets; exactly two distinct reviewed candidates
match the two integration candidates and publication order; the two successful
target publications occur under non-overlapping serialized authority;
restore/playback digests are equal; artifact
paths are clean repository-relative paths with no empty, `.`, or `..`
component; artifact paths stay under the evidence root after descriptor-safe
resolution; and every referenced hash matches exact stored bytes. JSON generated
by the harness is canonical JSON v1 and hashed as encoded; copied artifacts are
hashed byte-for-byte. Regular expressions are not the path-security boundary.
Signatures never turn an incomplete manifest into admissible evidence.

For GJ-02 the referenced sidecar provenance must contain a non-null
`qualification_proxy`; its product and daemon socket paths must differ, its
daemon path must equal the verified configured/connected daemon socket, and its
artifact identity must bind to the campaign source. The cut artifact must prove
a complete accepted response was consumed before the product-facing drop and
the later reconciliation used an ordinary public read.

## Adoption decision

A human record conforming to `adoption-record-v1.schema.json` chooses
`adopt` or `refuse`, identifies the exact manifest hash and admitted tuple,
records reviewer identity and rationale, and carries a detached signature or
approved local signature reference. Adoption is limited to this tuple and
policy. It does not approve a later dependency, relaxed budget, remote shared
store, Team Kitty hosted route, or full production replacement.

`admitted_tuple_sha256` is always
`4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d`,
the hash of the exact checked-in
`docs/programs/evidence/m1-admission.json` bytes. Tuple fields are resolved by
JSON pointer only after that byte hash verifies.
