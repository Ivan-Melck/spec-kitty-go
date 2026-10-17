# Unbound execution-coordinate proof increment

Status: **private foundation only, independently reviewed**. No production code,
real restart oracle, historical receipts, clean candidate or admission changed.
Actual portable/crown strict comparison remains red and was not rerun unchanged.
This records the **earlier unbound-only checkpoint** and its original hashes.
The later [bound execution increment](BOUND_EXECUTION_FOUNDATION.md) is separately
scoped-reviewed after correction; its source hashes do not retarget this review.
The unsupported-bound statements below describe the earlier checkpoint.

## Narrow source change

- `internal/scenario/terminal_coordinates_test.go` now admits only the closed
  schema/reducer-1 **unbound** execution-slot owner. Its capability decoder and
  exact work coordinate bind project/mission/work revision/work. Contiguous
  typed playback checks actor/process/epoch, governance, predecessor authority
  and transition laws; replay bytes must equal the typed canonical state.
- Execution states containing WorkspaceID or a permanent claim digest remain
  explicitly unsupported until their historical workspace graph is closed.
  Execution alone still cannot qualify host-bound operation roots. All other
  previously unsupported event owners remain unsupported.
- Actual execution services use compact operation head summaries. The foundation
  now admits exactly either event payload bytes or a one-field status object
  equal to reconstructed operation status. It independently proves the event
  payload digest and complete reconstructed operation bytes; compact summaries
  never replace the full historical state preimage.
- Exact execution references resolve coordinate, logical version, provider
  revision, raw state digest and snapshot identity, including work-scoped
  coordinates. There is no latest-head fallback or digest substitution.

`internal/scenario/terminal_execution_controls_test.go` exercises actual typed
service rebind ACKs and genuinely re-sealed/published controls. Published
foreign owners, host roots with execution-only ownership, schema-valid unproved
workspace bindings, wrong/extra compact-summary keys/status, incorrect head
summary/digest domains and missing initial history are rejected. Invalid event
schemas/reducers and typed envelope/predecessor authority are separately refused
at sealing/publication, not credited as foundation-layer refusals. Old/new exact
state references pass; latest-for-old, foreign and cross-snapshot references fail.

## Checks and independent review

Go 1.26.7, btrfs TMPDIR `/home/user/.cache/spktmp`:

```text
go test -count=1 -v ./internal/scenario
  -run 'TestTerminal(Coordinate|Execution|History|Restart|TypedRecovery|Semantic)'
go test -race -count=1 -v ./internal/scenario
  -run 'TestTerminal(Coordinate|Execution|History|Restart|TypedRecovery|Semantic)'
go vet ./internal/scenario
```

Implementer unit/race/vet passed; unit 0.754 s and race 8.239 s. Parent separately
ran all 16 selected top-level tests under race (8.042 s), zero skips/failures,
and vet; six foundation/support source hashes matched before/after. Parent
compared the old foundation from the checkpoint archive in memory: exactly one
of the 109 original inventory files changed, plus the new control file.

Logs/hashes:
- `/home/user/.cache/spec-kitty-parity-resume-followup/execution-foundation/run-6oZdMeL8/`
- `/home/user/.cache/spec-kitty-parity-resume-followup/execution-parent-check-96KEf9xU/`

Read-only reviewer `36a87b2e-05fc-48e` inspected both complete files, supporting
history/recovery code and actual reducers/sealing, compared archived source,
and ran focused tests. **No blocking defects attributable to the increment.**
Reviewed source SHA-256:

```text
d5ab6213e2a906d3e189baa04dfb771a802bc0706fbc51380a55edd5b9c11223  internal/scenario/terminal_coordinates_test.go
3dab2657775376a947643ae570a6b646aa0f1e04ceef97d915ea756fdaff29f3  internal/scenario/terminal_execution_controls_test.go
```

This is dirty-main scoped review/test evidence, not immutable-source attestation
or transfer of clean `41c20b14…` qualification.

## Explicit residual boundaries

No cross-trace execution pairing, embedded-reference normalization or real
outcome-equivalence integration exists. The generic history validator closes
operation membership, **not** an independent complete non-operation progress
census. Captured execution events are replayed completely; all non-operation
terminal items must still be joined to their exact captured heads before the
real oracle can use this graph. Workspace pairing remains binding-facts-only,
not complete business comparison. Workspace-bound execution, runtime/private
admission, integration, acceptance, review and remaining owners/references still
require closed proof. See [next graph handoff](TYPED_RESTART_GRAPH_NEXT.md),
including publication-time assertions/eligibility, private admission inventory
and configured physical repository ownership. No clean candidate was created
for this incomplete graph.
