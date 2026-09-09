---
affected_files: []
cycle_number: 1
mission_slug: hubnot-tracker-replacement-01M22QEJ
reproduction_command:
reviewed_at: '2026-09-09T10:43:29Z'
reviewer_agent: codex-independent-scanner-review
wp_id: WP08
---

# WP08 independent review — changes requested

Reviewed application source: `e28b01b9f26f0e71963a9538d1f99e1aba6efa47`.
Reviewer: `codex-independent-scanner-review` (resolved reviewer-renata; no implementation edits).

## R1 — Executable hidden files without suffix or shebang bypass shell resolution

**Priority: P1. Requirements: NFR-005; WP08 T022 step 4 and T023.**

At `internal/architecture/m2_absence_test.go:285`, shell resolution applies only to `.sh`, exact `.gitignore`, or files starting `#!`. File mode is supplied but only used to disable the ignore exemption. Consequently an executable extensionless `.hidden/run` (0700) containing `sql'ite'3 /tmp/db` produces **zero findings**. An escaped `sql\ite3` variant with an execute permission also produces zero findings. The raw string scan cannot recognize the resolved executable.

This is executable behavior, not a hypothetical filename: a disposable 0700 `.hidden/run` with the quoted command, invoked through `/bin/sh -c './.hidden/run'`, called a disposable `sqlite3` stub and exited 0. The proof neither invoked a database nor touched user data. Normal shell ENOEXEC fallback interprets executable text lacking a shebang. The existing `hidden-extensionless` case includes a shebang, so it does not cover this required boundary. This is the explicit executable-without-conventional-extension obligation of this WP, not a request for a new general shell parser.

**Required correction:** include executable file context in the existing conservative shell-resolution/fail-closed path, with red-first fixtures for hidden extensionless executable text without a shebang and split/escaped SQLite command words. Retain exact plain-ignore exemptions, all other token classes, inert positives, existing hidden import/module controls and historical bytes. Account for executable binary material without introducing new false positives in the full quality path. If new named cases grow the closed census, admit only their exact reviewed successor digest/count and retain required historical admission; never loosen census verification to arbitrary counts.

**Independent evidence:** `/home/lynn/Documents/Codex/2026-09-09/h/work/go-wp08-review/`:
- `independent_context_test.go.txt` and `overlay.json`: external test-only overlay, no repository source changes.
- `independent-context-1.26.7-race.log`: Go 1.26.7, race, original `TestM2AbsenceMutants` passes; independent 11-case probe has two executable-without-shebang failures, nine controls pass. Package fails in 0.068s.
- `shell-fallback-proof.json`: actual safe shell dispatch proof described above.

Reproduce from lane-h, using Go 1.26.7 on PATH and GOTOOLCHAIN=local:

```sh
go test -race -overlay /home/lynn/Documents/Codex/2026-09-09/h/work/go-wp08-review/overlay.json ./internal/architecture -run '^(TestIndependentWP08ExecutableContexts|TestM2AbsenceMutants)$' -count=1 -v
```

## Other review results

Exact historical SHA/count 245af6f271f12f1905613c4140cb4b842cc117ab40465f559a545069e79af09d / 489 and current 838f3a6c78e60f2d2153ab90df675117c03a4afc8bc3c5cfd4ef756b013e61d3 / 505 verified; original 489 bytes remain an unchanged prefix. Admission and nested-manifest-count tests independently pass under Go 1.26.7 race (1.324s). Production verifier caller retained; unknown/reduced/duplicate contracts and forged counts reject. All five owned files equal the source candidate despite metadata-only lane reconciliation. Existing final quality and race census receipts are source-bound and pass, but do not cover R1.

Anti-pattern checklist: dead code PASS (private scanner helpers serve the live quality gate, production nested verifier called by evidence.go); synthetic fixtures PASS (mutants invoke scanner; nested synthetic receipts exercise production validator and do not claim real qualification); silent empty return PASS (only explicitly inert ignore lines filtered); requirement coverage FAIL (R1); frozen surface PASS (only five allowed nonmission paths changed, no frozen-data/source edits); locked decision FAIL (hidden executable denial incomplete); shared-file ownership PASS (two verifier paths explicitly added to canonical WP scope, inherited metadata reconciliation documented by root); production fragility PASS (new verifier errors are intentional invalid-evidence refusals).

Verdict: **REJECT** pending R1. No other blocking defect found in this bounded review. No source implementation, full architecture-race rerun, merge or push performed by reviewer.
