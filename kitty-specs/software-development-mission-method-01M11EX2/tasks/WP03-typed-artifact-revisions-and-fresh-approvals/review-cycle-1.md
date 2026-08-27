---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-27T16:51:56Z'
reviewer_agent: user
wp_id: WP03
---

# WP03 review feedback — cycle 1

Verdict: changes requested.

## 1. Artifact authority is outside the permanent production-reachability census

Severity: blocker

`internal/artifact` is now a live production authority package, but
`DefaultReachabilityTargets` contains only handoff, Git, and mission entries.
There is no artifact registration, and `go list` reports no production package
that imports `internal/artifact`; `NewService`, `Publish`, and `Approve` are
therefore exercised only by tests.

This is not a permitted deferred-registration case. WP01 T004 requires the
smallest stable mission/artifact contract registry, forward activation when a
package is absent, rejection of test-only callers, and live reachability once
the downstream package exists. The package now exists, while the exact
architecture campaign passes vacuously because it has no artifact target to
check. This also fails the WP anti-pattern dead-code gate.

Fix this through an explicitly coordinated architecture change: register the
smallest stable artifact authority seam(s), add real source-loaded mutants for
missing registration, missing declaration, test-only use, and removed sole
production caller, and establish a legitimate production caller compatible
with the mission dependency order. Do not add a fake composition caller or
weaken `ActivateWhenPackagePresent`. If the current WP slicing makes a real
caller impossible, repair the WP/dependency plan explicitly rather than
waiving the permanent guard.

## 2. Stale freshness evidence depends on repository iteration order

Severity: high

`EvaluateFreshness` retains the first approved verdict for any non-required
revision. Reversing the same two historical approvals changes
`FreshnessResult.ApprovalID` while leaving the semantic approval set unchanged.
The repository contract does not promise ordering, so repeated pure queries can
return different machine-readable evidence for identical state.

A temporary reviewer probe reproduced this exactly:

```text
left:  status=stale approval_id=approval-a
right: status=stale approval_id=approval-b
```

Make stale selection canonical and documented, or omit an unrelated approval
identity from the stale result. Add an order-permutation test with at least two
historical approvals and retain the exact-revision fresh/rejected rules.

## 3. Finding identifiers can retain credential-shaped evidence

Severity: high

`NewFinding` checks sensitive markers only in `message`. `kind` and `field` use
the generic bounded-text check and currently accept values such as
`secret=credential` and `authorization: bearer credential`; both enter durable
validation commitments and the schema-v1 projection. That violates NFR-012 and
the WP law that findings contain bounded canonical evidence rather than
credentials, transcripts, or unrestricted tool output. It also permits prose
instead of a stable machine-readable finding kind.

Define and enforce canonical machine-readable identifier rules for finding
kind and logical field, and apply privacy rejection consistently to every
durable finding component. Add table tests for kind, field, and message leakage,
including case variants. Keep messages bounded and useful without echoing the
rejected value.

## 4. The domain-qualified content commitment is not load-bearing in tests

Severity: medium

The implementation currently prefixes the content with the v1 domain, but the
tests assert only repeatability, lowercase length, and one-byte sensitivity.
Replacing `ContentCommitment` with raw `sha256(content)` would leave those tests
green, so the domain qualification and algorithm identity are not regression
proof as required by T012/T016.

Add a fixed known-vector assertion (and/or an explicit raw-SHA inequality)
covering the exact domain bytes and algorithm constant. Add equivalent
load-bearing vectors for validation, publish-request, and approval-request
commitments where the request tests currently prove conflict only indirectly.

## Evidence and non-blocking baseline exception

- `go test ./internal/artifact` — pass
- `go test ./internal/artifact -count=10` — pass
- `go test -race ./internal/artifact` — pass
- schema-v1 focused test repeated 20 times — pass
- concurrency/freshness focused cases repeated 100 times — pass
- `go vet ./internal/artifact` — pass
- `staticcheck ./internal/artifact` — pass
- `gofmt -d internal/artifact` — clean
- `go test ./internal/architecture` — pass
- exact clean-tree architecture campaign — pass, 12.789 s (vacuous for artifact reachability as described above)
- full `go test ./internal/...` — fails in the existing scenario package after
  600.7 s due to an out-of-date full-scenario census and the default 10-minute
  timeout. The census failure reproduces at coordination commit
  `a8315dc2dd804fff0ae84ec0d63b8ec55d5d0b8c`, whose `internal/scenario`
  sources are identical to this lane; it is not attributed to WP03.

The implementation commit itself changes exactly the five WP03-owned artifact
files. Later status-projection commits are Spec Kitty topology recovery, and
the tracked projection blob was rematerialized from coordination authority;
they are not treated as product-scope violations.

## Anti-pattern checklist

1. Dead code: **FAIL** — the artifact authority service has no non-test production caller and no permanent reachability target.
2. Synthetic-fixture test: **PASS** — the focused tests invoke production constructors and service paths.
3. Silent empty return: **PASS** — no silent empty failure path was introduced.
4. FR coverage: **PASS** for the exercised behaviors, subject to the missing load-bearing commitment vectors above.
5. Frozen surface: **PASS** — the implementation commit is confined to the five owned files.
6. Locked decision: **PASS** — no SQL, provider, tracker, filesystem, Markdown-authority, CLI, projection, or mission-transition dependency entered the package.
7. Shared-file ownership: **PASS** — product changes are five-file-local; status projection repair is runtime-owned topology recovery.
8. Production fragility: **N/A** — no Go panic/fail-loud transient path was introduced.
