# WP09 Review Feedback — Cycle 2

Candidate `315660fcabc0a72c538934a326748a09600afb36` closes the six cycle-1 implementation blockers. The supplied passing evidence has SHA-256 `7a7cf13a03885ef8a1f2099f4f704e2334305983a918dbf19151542918921103`, binds the exact candidate tree `b5368db98c0a2270fc53bcb741635d182bb10e20`, validates the checked-in passing shape, and records the qualified binary digest, 11/11 scenarios, 1,000/1,000 contention, 20/20 assignment preservation, 23 authority audits, zero invariant failures, and exact 100/200 latency samples. Independent byte-real backup/restore, divergent-mutation refusal, assignment refusal, unit, race, vet, staticcheck, module, vulnerability, and architecture checks passed. One new contract blocker remains.

## 1. Blocker — the evidence schema cannot represent the runner's required non-admissible and failure records

`contracts/qualification-evidence.schema.json` now requires `latency`, `contention`, `assignment_preservation`, and `authority_audits` unconditionally and constrains contention to the complete passing server census. That validates the supplied passing server evidence, but it rejects evidence the runner intentionally emits on required failure paths:

- without a qualified server, `qualificationStorageMode()` is `embedded_serialized`, `server_multiwriter_contention` is skipped, `Contention` remains nil, and `omitempty` removes `contention` from the emitted `non_admissible` record;
- if the latency, assignment, authority, or contention scenario fails before its marker, that field is likewise omitted or partial while the runner emits `fail`/`non_admissible` plus an issue;
- a truthful partial contention census cannot satisfy the schema's passing constants even though preserving that partial census is precisely useful failure evidence.

This contradicts T048's requirement that missing/skipped scenarios produce a schema-1 `fail` or `non_admissible` record rather than a record outside its own contract. The new schema test exercises only a synthetic complete `pass` value, so the mismatch is currently invisible.

Make passing requirements conditional on `verdict == "pass"`: exact two latency records and sample/threshold rules, complete server contention, exact assignment census, positive authority audits, zero authority failures, exact successful scenario census, and no issues. Keep the base schema able to represent bounded partial/missing observations for `fail` and `non_admissible`. Add load-bearing schema tests for at least (1) embedded/no-server non-admissibility with no contention, (2) a failed/partial marker census with a safe issue, and (3) the existing passing and mutant cases. Ideally validate the final serialized result against the same compiled schema before or as it is written so future schema/encoder drift cannot escape the test fixture.

## Verified corrections

- External server identity excludes local workspace path; same tuple converges and host/port/database mutants diverge.
- One representative mapped graph, complete criteria, asymmetric dependency, and adapter/human discussion survive bounded JSONL and native restore; a fresh adapter inspection verifies restored health and identity.
- Both assignment refusal branches run ten times each, preserve all 20 assignments, and spawn no compensating mutation.
- Authority evidence is observed rather than hard-coded: 23 audits, zero failures.
- Passing evidence requires the exact preflight/warm-read operations with 100/200 samples and thresholds.
- Divergent workspace mutation returns `workspace_mismatch` after read-only inspection and before any mutating subprocess/provider change.
- The `jsonschema/v6` dependency is test-only in the module graph, module verification passes, staticcheck is clean, and `govulncheck` reports zero reachable vulnerabilities.
- Installer destination bytes that would break JSON are refused; digest/version/source/tree bindings and evidence redaction remain intact. No qualification child/server process or non-temporary repository mutation remained after review; the retained probe directories are under `/tmp` only.

## Anti-pattern checklist

1. Dead code: **PASS**
2. Synthetic-fixture test: **PASS** for the covered passing schema path; the missing non-pass schema case is the blocker above.
3. Silent empty return: **PASS**
4. FR coverage: **FAIL** — FR-023/T048 do not cover schema-valid failure/non-admissible evidence.
5. Frozen surface: **PASS** — cross-WP schema and binding changes have an explicit commit rationale.
6. Locked decision: **FAIL** — required honest non-admissible output cannot satisfy the evidence contract.
7. Shared-file ownership: **PASS** — cross-WP coordination is explicit in the candidate commit.
8. Production fragility: **PASS**

## Independent verification

- `go test ./internal/testcampaign/cmd/beads-qualification -count=1`
- `go test ./internal/tracker/beads -count=1`
- `go test -race ./internal/testcampaign/cmd/beads-qualification ./internal/tracker/beads -count=1`
- real pinned-v1.2.2 backup/restore, assignment-refusal, and divergent-workspace tests
- `go vet`, `staticcheck`, `go mod verify`, `govulncheck`
- `go test ./internal/architecture -count=1`
- exact Phase-1 architecture/scope scenario guards

An exploratory broad scenario regex also selected the full 12-minute Phase-1 regression campaign and hit its deliberately insufficient 8-minute timeout; the exact relevant architecture/scope tests pass and this is not a WP09 regression.
