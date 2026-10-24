# Published RC7 parity target

The current target is upstream tag `v4.0.0rc7`, whose peeled commit is
`1b0fe9fc388b095f9964471b0177b4213e9c6c98`. The wheel and Go executable report
`spec-kitty-cli version 4.0.0rc7`. The target was verified against the remote
tag on 2026-10-24.

The command inventory contains 276 paths. Embedded data is extracted from a
wheel built at this exact tag: 720 files, including the revision record.
RC7 retains dotted command-skill names, metadata-based migration history and
charter/config activation. Its orchestrator API contract is `1.16.0`, and its
reference uses `spec-kitty-events` `10.5.1`.

The rebuilt reference matches all 2,063 runtime members of the published PyPI
wheel byte for byte. That published wheel has SHA-256
`174361d4a2c75f1f9fb20ca17f6f9eca60b0017052425680630628feab362124`.
The Go migration catalog matches all 124 upstream entries, including their
order, target versions, descriptions and first-run flags.

The previous RC8 port is preserved in Git history. Its evidence is retained in
[the historical RC8 record](RC8_HISTORY.md); that evidence does not qualify the
current target. The RC7 implementation removes the RC8-only CLI, activation,
migration and delivery behavior, and restores the RC7 decision retry,
dependency, policy, analysis and consolidation rules.
Shared correctness fixes remain where RC7 has the same contract: retained
asset rechecks, source/parent/environment observations, serialization of cold
installs, filesystem-root anchors, and guarded agent-configuration reads.
Bulk-edit occurrence-map validation, ownership inference and review gates also
remain: they already exist in RC7. Their path classifications, diff exemptions,
status-directory anchoring and human output follow RC7 rather than RC8.

Other area notes in this directory are historical implementation audits.
This file identifies the current target and its acceptance evidence.

## Verification in progress

The reference lives at `$HOME/.cache/spec-kitty-parity/rc7-1b0fe9fc38`.
The final differential campaign uses immutable source at
`$HOME/.cache/spec-kitty-parity/rc7-validation-tmp/final-source` and binary
`$HOME/.cache/spec-kitty-parity/rc7-validation-tmp/spec-kitty-final`:

```sh
GOMAXPROCS=6 \
TMPDIR=/var/tmp/spec-kitty-rc7-validation \
SPEC_KITTY_GO_BIN="$HOME/.cache/spec-kitty-parity/rc7-validation-tmp/spec-kitty-final" \
SPEC_KITTY_ORACLE="$REF/oracle-bin/spec-kitty" \
SPEC_KITTY_HELP_INVENTORY="$REF/inventory/help" \
go test -p 8 -parallel 4 -json -count=1 -timeout 120m ./...
```

An earlier survey was stopped after the temporary filesystem exhausted its
inode limit. Scratch under the real home also lets no-project fixtures discover
the home directory's global `.kittify` tree. Those results are excluded from
acceptance. The colour harness
now removes each probe's populated homes as soon as it has compared output;
the final run also uses disk-backed scratch outside any initialized project.

Focused RC7 comparisons pass for legacy project/checkout refusals, agent
configuration diagnostics and null-list validation, 15 merge-gate mode cases,
legacy asset overwrite/symlink/blocked-parent behavior, ten retained-asset
recheck scenarios, and invalid-evidence completion/retry semantics.
Bulk-edit comparisons cover metadata/map validation, inference, 1,800 diff
classifications, Git review history, rendering, and integration through task
finalization, implementation and review.

The broad campaign's snapshot precedes the final bulk-edit integration. A
second immutable snapshot, `rc7-validation-tmp/postbulk-source`, and its
`spec-kitty-postbulk` binary run the complete bulk-edit, planning and runtime
packages. The upgrade cancellation path is compared separately against RC7.
This keeps each campaign tied to a fixed executable while covering subsequent
changes.

Later broad comparisons found the already-corrected EOF cancellation newline,
one missing configuration warning during upgrade preview, and three traceback
line numbers from the newer mission-step resolver. The upgrade runner now
repeats migration selection after preview/confirmation, as RC7 does. The
warning and traceback probes pass against the immutable `diagnostics-source`
snapshot and `spec-kitty-diagnostics` binary. Upgrade-plan and commented-config
replays also pass; the broader migration corpora replay is recorded below
when complete.

The post-bulk snapshot exposed one diagnostic mismatch in the new malformed
frontmatter probe: a YAML sequence was called `list` instead of RC7's
`CommentedSeq`. The corrected helper passed the complete malformed-frontmatter
probe through both finalization modes and action implementation, using the
separate `spec-kitty-frontmatter` executable. The original campaign retains
its failure as evidence; its running binary was not replaced.

The README's installation and basic usage smoke test passed with an isolated
home and project: `go install`, version/help, initialization, Git setup,
configuration listing, setup verification, idempotent initialization,
upgrade preview, surface repair, dispatch and Op completion. Initialization's
execution trace contains no Python execution.

The final code also passed `go test ./...`, `go vet ./...`, and race checks for
`internal/bootstrap`, `internal/consolidate`, `internal/toolsurface` and
`internal/bulkedit`. A fresh `CGO_ENABLED=0 go install`, Codex initialization,
`agent config list`, `verify-setup` and standalone binary build passed after
the final implementation changes; initialization again executed no Python.

Final broad comparison results will be recorded here after completion.
