# Linear sealed-journal observation

Status: development correction independently reviewed; fresh clean-source M1
qualification pending. Historical receipts and source pins are unchanged.

## Source-coupled failure

Initial clean candidate `50b1e082ca314908b24195f66f32d3e730a9fc07` (tree
`c2d93a2757d0ef3b819e08f18df2c72b69f929cb`) passed the five critical race
packages but its once-run M1 focused campaign hit the 15-minute package timeout.
The retained stack in `/tmp/spec-kitty-clean-candidate-initial-m1.log` identifies
`composedObservationPort.ObserveM1 -> ValidatedJournalV1.At -> isValid -> contentDigest`.
Each `At` verifies the complete journal; an N-record iteration therefore hashes
(N+1) times the B-byte seal input, not merely the requested record. This is
source-confirmed quadratic scan work, not an estimated timeout cause elsewhere.
Two fault fixtures separately refused overlong Unix socket paths before source
validation. Their test-only runtime directories now use private short names on
TMPDIR; production filesystem/socket checks and topology remain unchanged.

## Structural correction

`SnapshotJournalV1` verifies the complete seal once, then deep-copies ordered
stored facts. Six full-scan consumers use that snapshot: reducer application,
playback, checkpoint prefix analysis/watermark/authority derivation and composed
M1 observation. Output DTOs are not validated reducer authority; consumers still
pass the original sealed capability to authority-consuming APIs. Sole raw-event
validation, the exact `Len`/`At` method set, every-field seal preimage, durable
codecs, ordering and historical proof bytes are unchanged. No validation cache
or mutable alias was introduced.

Independent review caught a playback allocation regression: materializing the
entire journal before refusing its event/byte budget. The corrected
`SnapshotJournalV1WithinBounds` verifies the seal, preflights original encoded
records with exact refusal precedence, and only then calls the defensive snapshot
(which intentionally reverifies). Playback keeps its compiled 16,000-event /
48 MiB limits and public error fields. This remains linear work; invalid or
oversized input returns no copied record prefix.

## Verification and limits

Equivalence/order, private byte ownership, forged/zero/post-success and every-field
mutation controls pass. Allocation controls measure construction separately:
root bounded preflight allocated 63,464 bytes versus 20,225,528 bytes for the
clone-first mutant; stable playback allocated 249,679,648 versus 418,814,168 bytes.
These are measured cumulative allocations, not peak-memory or campaign timings.
Both clone-first overlays fail their controls.

Go 1.26.7 root/reducer/playback/checkpoint package and race tests, targeted vet,
and unchanged M1 dependency/capability guards pass. Logs:
`/tmp/bounded-final-{green,root-red,playback-red,race}.log` and
`/tmp/bounded-{vet,guards}.log`. Follow-up independent review found no confirmed
issues in budget/refusal/ownership/authority preservation. The private short-root
control passes in `/tmp/spec-kitty-fault-short-root-control.log`.

These scoped results do not erase the failed clean-candidate run or mint new
qualification. Commit the reviewed changes under a fresh source/tree identity,
then execute the actual M1 campaigns once with their distinct exact executable
bindings. No historical receipt/source hash may be retargeted to those commits.
