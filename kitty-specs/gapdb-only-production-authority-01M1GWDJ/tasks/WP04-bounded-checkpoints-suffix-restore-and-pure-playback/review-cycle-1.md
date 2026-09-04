---
affected_files: []
cycle_number: 1
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-04T05:45:46Z'
reviewer_agent: renata-gapdb-checkpoint
wp_id: WP04
---

# WP04 Review Feedback — Cycle 1

## Verdict

Request changes on implementation commit `e2ca369498db61df55e3c46dd204c58d29c2e6fa`.

## Blocking finding

`checkpoint.SelectV2` compares only `valid[0]` and `valid[1]` after sorting retained checkpoint candidates. A valid retained ring containing two slot references to checkpoint A and one conflicting checkpoint B at the same watermark can therefore sort as A, A, B. The A/A comparison succeeds, B is never inspected, and recovery accepts contradictory same-watermark evidence.

The independent reviewer reproduced the production-path failure: the three-candidate duplicate-mask case returned `err == nil` instead of `ErrorContradictory`.

## Required repair

1. Compare the selected highest-watermark candidate with every other valid candidate at that watermark.
2. Repeated references to the same checkpoint identity may be rejected or deliberately deduplicated, but they must never mask a different checkpoint digest.
3. Add a permanent production-path regression with three valid candidates: two references to checkpoint A followed by one conflicting checkpoint B at the same watermark.
4. Add or extend a source-bound mutant control so reverting to first-pair-only comparison is killed.
5. Re-run the focused checkpoint suite, all owned package suites, race checks, deadline overlay campaign, architecture guards, vet, staticcheck, formatting, ownership checks, and the qualification fixture. Preserve the honest distinction between the local five-second gross CI bound and WP17's separate two-second reference-machine threshold.

## Evidence already accepted

- Package tests passed before the blocker was found: checkpoint (0.089s), playback (7.446s), restore (112.379s).
- The restore suite exercised production deadline behavior and all exact-anchor overlay mutants, including the live ambient-clock architecture guard.
- The 30-by-10,000 fixture has the required checkpoint and suffix population and remains non-admissible evidence for WP17's two-second reference threshold.
- The implementation commit changes exactly the eight WP04-owned files and passes `git diff --check`.

## Reviewer independence

Reviewer actor/process: `renata-gapdb-checkpoint`, independently reviewing implementer actor `ivan-gapdb-checkpoint`.
