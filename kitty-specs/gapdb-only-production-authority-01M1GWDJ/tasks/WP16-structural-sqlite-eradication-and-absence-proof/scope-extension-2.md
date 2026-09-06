# WP16 scope extension 2: remove executable command substitution

## Trigger

Cycle-29 independent review proved that shell command substitution executes its
nested command even when the outer command uses the result only for a query or
as data. The corrected absence law therefore fails closed on active `$(...)`
and backtick substitution anywhere in live shell source rather than attempting
to implement a nested shell evaluator.

Applying the corrected proof exposed active substitution in the WP16-owned
qualification scripts and in one pre-existing Beads installation helper.
Exempting executable scripts would make the absence claim unsound.

## Authorized extension

WP16 may make the smallest behavior-preserving changes to:

- `scripts/install-qualified-beads.sh`

The extension is limited to replacing active command/backtick substitution with
explicit temporary-file, pipeline, read, or other literal-command data flow
that satisfies the new proof boundary.

## Required preservation

- Preserve the qualified Beads source/version/digest contract and installation
  destination checks.
- Preserve non-interactive, offline, atomic publication, repository-boundary,
  cleanup, diagnostic, and failure exit semantics.
- Do not change Beads adoption policy, tracker authority, provider behavior, or
  any unrelated installation surface.
- Do not introduce a shell-source exemption for the helper.
- Existing installer, architecture, absence, shell syntax, and mutation tests
  must pass.

## Review expectation

Independent review must confirm the refactor removes active substitution rather
than disguising it, preserves failure propagation and exact output capture, and
does not weaken the command-substitution detector or its inert quoted/escaped
controls.
