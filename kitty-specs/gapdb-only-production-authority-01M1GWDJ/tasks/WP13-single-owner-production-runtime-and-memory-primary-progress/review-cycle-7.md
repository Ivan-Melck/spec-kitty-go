---
affected_files: []
cycle_number: 7
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-05T16:52:52Z'
reviewer_agent: maxwell
wp_id: WP13
---

# Post-approval defect 3: monitor deadline includes command-barrier wait

## Reproduction

The WP14 production scenario `TestTwoItemMissionRestartAfterEachDurableBoundary`
fails deterministically under `go test -race` while executing a healthy real
GapDB sidecar. A race-instrumented projection refresh holds the host's shared
command barrier for longer than the monitor's one-second probe timeout. The
monitor creates its timeout before waiting for that barrier, then calls the
owner probe with an already-expired context. Three otherwise healthy long
commands are therefore misclassified as consecutive owner loss, and the host
enters `degraded` with diagnosis `owner_health`.

## Required correction

Wait for admitted commands to drain before starting the bounded owner-probe
deadline. Add a regression that holds an admitted command beyond the complete
probe timeout and proves the eventual probe receives a fresh context, the host
remains ready, and real owner-loss detection is unchanged. Re-run the real
GapDB two-item scenario repeatedly under the race detector.
