# WP03 review feedback — cycle 2

Verdict: changes requested.

Cycle 1 is substantively repaired: the artifact freshness seam is registered
and reached by a real mission production caller with missing, test-only,
shadowed, and removed-caller mutants; stale evidence is order-independent;
finding identifiers are canonical and credential-shaped kind/field/message
inputs are rejected; and fixed vectors pin every commitment domain. The product
delta from accepted baseline `65258f5` is exactly the nine owned files.

Two authority-boundary defects remain.

## 1. A successor publication can race the mission gate and leave a stale approval accepted

Severity: blocker

`withArtifactApprovalEvidence` performs three independent operations:

1. load the current revision;
2. load approvals;
3. commit the mission transition through a different port.

Neither `ArtifactApprovalSource` nor `TransitionCommit` carries a revision
observation/CAS precondition that the mission store can compare atomically.
This permits the following execution:

```text
mission reads current revision R1
artifact publication makes R2 current
mission reads historical approval A1 for R1
EvaluateFreshness(R1, A1) returns fresh
mission commits specification_approved
```

A temporary reviewer probe reproduced the sequence and the transition advanced
to `specification_approved` with `ApprovalEvidenceID=A1` after the source's
current revision had become R2. This violates FR-005 and the core law that a
transition consumes approval for the exact revision currently required.

Do not repair this with a final best-effort reread; publication can race that
reread too. Express an aggregate atomic boundary in the portable ports. The
mission commit must carry the exact artifact revision/approval binding and an
expected-current observation that the durable adapter can compare in the same
transaction as lifecycle advancement. Add deterministic tests for publication
before approval lookup, between approval lookup and commit, and at the commit
boundary. Each loser must return a stable stale/conflict diagnostic with no
mission advancement.

## 2. Replay validation accepts a substituted approval audit identity

Severity: high

`transitionRequestCommitment` is computed from the caller request before the
artifact-derived approval is appended. On replay,
`validateReplayPostcondition` takes `result.Audit.ApprovalEvidenceID` from the
store, injects that same value into a synthetic request, and compares the
resulting candidate back to the same store-returned audit. The check is
circular: the original derived approval/revision binding is absent from the
request commitment.

A temporary reviewer probe first committed a legitimate exact-revision
transition, then changed only the replayed `ApprovalEvidenceID` to another
syntactically valid identity. Replay succeeded without consulting mutable
artifact state and returned the substituted identity.

Preserve the desirable no-reread replay law, but commit the derived artifact
revision and approval identity into immutable transition authority evidence
with its own canonical commitment. Persist it atomically with the transition,
and validate the replayed audit against that durable commitment rather than
reconstructing expected evidence solely from the value under test. Add
positive replay coverage plus missing, altered-revision, altered-approval, and
altered-commitment mutants.

## Evidence and baseline assessment

- `go test ./internal/artifact` and `-count=10` — pass
- `go test -race ./internal/artifact` — pass
- `go test ./internal/mission` and `-count=10` — pass
- `go test -race ./internal/mission` — pass
- `go test ./internal/architecture` — pass
- exact clean-tree architecture campaign — pass, 14.492 s
- `go vet` and `staticcheck` over artifact, mission, and architecture — pass
- `go mod verify`, formatting, diff hygiene, and repository compile — pass
- `make contract` — pass
- full-scenario exception: `internal/scenario` has no source delta between
  accepted baseline `65258f5` and WP03. The exact census failure reproduces in
  an isolated checkout of `65258f5`, so it is pre-existing and is not charged
  to this WP.

## Anti-pattern checklist

1. Dead code: **PASS** — mission is a real production caller and permanent reachability is active.
2. Synthetic-fixture test: **PASS** — normal tests and reviewer probes execute production mission/artifact paths.
3. Silent empty return: **PASS** — no silent empty failure path was introduced.
4. FR coverage: **FAIL** — FR-005's exact-current guarantee is not enforced across concurrent publication and mission commit.
5. Frozen surface: **PASS** — the WP product delta is exactly the nine owned files.
6. Locked decision: **PASS** — authority direction remains mission to artifact; no adapter, projection, Markdown, tracker, SQL, or transport leakage was introduced.
7. Shared-file ownership: **PASS** — overlapping mission/architecture edits are explicitly authorized by the cycle-2 WP contract and remain within its nine-file surface.
8. Production fragility: **N/A** — no Go panic/fail-loud transient path was introduced.
