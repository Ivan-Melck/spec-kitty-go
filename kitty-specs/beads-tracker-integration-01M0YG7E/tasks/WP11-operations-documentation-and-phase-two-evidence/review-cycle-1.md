---
affected_files: []
cycle_number: 1
mission_slug: beads-tracker-integration-01M0YG7E
reproduction_command:
reviewed_at: '2026-08-26T22:23:14Z'
reviewer_agent: user
wp_id: WP11
---

# WP11 Review Feedback — Cycle 1

Verdict: changes required.

## Blocking findings

### 1. The documented local qualification command cannot produce the qualified server-mode proof

The README says the real-provider target requires the exact external Dolt tuple, but the command at `README.md:443-448` passes only `BEADS_BIN`, `BEADS_INSTALL_RESULT`, and `OFFLINE_ENFORCED`. It omits `BEADS_SERVER_ID`, `BEADS_SERVER_ENDPOINT`, `DOLT_SERVER_HOST`, and `DOLT_SERVER_PORT`. Production qualification selects `embedded_serialized` if any one of those four values is absent (`internal/testcampaign/cmd/beads-qualification/runner.go:490-498`), while admissible evidence requires `server_multiwriter`. A new operator following the README exactly therefore cannot reproduce the advertised proof and must inspect the workflow implementation to guess the missing state.

Publish one complete, safe local orchestration or command that provisions the pinned disposable Dolt server and passes all four exact tuple values inside the offline namespace. The example must make clear that `BEADS_SERVER_ENDPOINT` is the exact host/port binding used by the accepted runner and must not imply that the three-variable command is sufficient. Preserve the current no-unsolicited-setup and disposable-only rules.

### 2. The required command-smoke gate is not load-bearing

`TestBeadsDocumentationLinksMermaidAndCommands` does not smoke documented commands. It checks only that Make target labels exist, two paths exist, and a few argv literals occur somewhere in `command.go` (`internal/architecture/beads_documentation_test.go:169-192`). It never runs `make -n`, parses helper flags, exercises installer argument parsing, or relates the variables in the README qualification block to the accepted target/runner.

This is a proven escaping mutant: in a clean archive of candidate `3a9fe38`, changing the documented `BEADS_BIN=...` argument to nonexistent `BEADS_BOGUS=...` still yielded:

```text
go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=1
ok github.com/spec-kitty/go-kitty3/internal/architecture
```

Implement the T058 command-smoke contract so every repository-local command is checked through a non-destructive executable seam: Make targets through a dry-run/parser contract, helper and installer flags through their parsers/help seam, and provider argv through the typed command builder. Add deletion/typo mutants that prove each documented command/required qualification variable is load-bearing. Do not execute against user provider state.

### 3. The technical plan still presents completed Phase 1 decisions as provisional future work

The plan header says concurrency mechanisms remain provisional through the Phase 1 proving experiment (`docs/TECHNICAL_PROJECT_PLAN.md:3-5`), calls SQLite merely the “leading implementation choice” (`:743-748`), and still lists defining/running the Phase 1 experiment and ratifying authority ADRs as future work (`:1224-1234`). Those statements conflict with the README's qualified Phase 1/Phase 2 delivery claim and with the retained evidence. The later Phase 2 completion paragraphs do not cure a new reader's first and primary status statement.

Update the document's status and completed-decision prose so it accurately distinguishes the qualified Phase 1 authority kernel and Phase 2 Beads adapter from genuinely future phases. Keep SQLite's current qualified status and Gapdb's separate signed-adoption boundary explicit.

## Validation-control finding

### 4. The named missing-source mutant is actually only another census mutant

`TestPhase2EvidenceDocumentationMutants/missing_source` passes `{expected:11, started:0, ...}` to `phase2EqualCensus` (`internal/architecture/beads_documentation_test.go:260-268`); it does not remove or corrupt `qualification-evidence.json`'s `source_commit`/`source_tree`. The ordinary integrity test does reject an empty source ID, but T058 explicitly requires a mutant control proving that provenance guard remains load-bearing. Add a true source-identity mutant and retain the exact-census mutants under accurate names. The relative-link helper and stale-API, authority, export-is-backup, missing-source manual mutations did fail for their intended reasons; the gap is the permanent named control, not the underlying ordinary check.

## Evidence reviewed

- Effective candidate `3a9fe38b5dca7b1ad110c56981fb09eb19b2f131`, tree `717af24f3d6038a5de357120cb2ba80d85a22530`; WP11 changes are the nine documented owned files plus the authorized two-file exact-census registration correction.
- `go test ./internal/architecture -run 'BeadsDocumentation|Phase2Evidence' -count=10` — PASS.
- `go test ./internal/architecture -count=1` and focused exact-census tests — PASS.
- `go test -race ./internal/architecture ./internal/testcampaign` — PASS.
- `go vet ./internal/architecture ./internal/testcampaign` and `staticcheck ./internal/architecture ./internal/testcampaign` — PASS.
- README Make targets dry-run successfully; installer/help and helper flag parsers were inspected; the helper uses Go flag's expected help exit status.
- `(cd docs/phase2-evidence && sha256sum --check SHA256SUMS)` — 4/4 PASS.
- Retained clean campaign `/tmp/go-kitty3-wp11-final-quality.0ID4ow` is honestly bound to `8fbb9d9`, tree `3611d0b`, source-dirty false, with 38/38 architecture tests and exact provider/quality reports. The evidence payload is unchanged from commit `106c7a2`; the later attestation is non-circular.
- Published evidence correctly discloses the 10/11 non-admissible run, prerequisite failure, WP10 correction history, and hosted baseline `baseline_pending` at 0/10. No evidence or authority overclaim was found.
