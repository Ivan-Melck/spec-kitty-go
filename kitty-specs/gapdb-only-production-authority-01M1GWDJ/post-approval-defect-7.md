# Post-approval defect 7: restore deadline capability boundary

## Finding

WP04's bounded recovery implementation exposed a contradiction between the
ratified restore contract and the permanent architecture guard. The restore
protocol must validate and enforce an elapsed-attempt budget, including
bounded origin reconstruction and whole-attempt restart. In Go, the narrow
and cancellation-safe implementation uses `time.Duration` with
`context.WithTimeout`.

The existing WP01 rule applies one standard-library allowlist to every
production package beneath `internal/controljournal/v2`. It therefore rejects
`restore -> time` as `ARCH-DEFERRED-SURFACE`, even though restore is the
consumer-owned I/O coordinator rather than the pure playback kernel. Removing
the duration budget or trusting an arbitrary caller deadline would weaken the
explicit bounded-recovery law.

Checkpoint's former `io` import is unrelated and unnecessary. WP04 removes it
rather than widening the architecture surface.

## Ratified correction

The purity boundary is package-specific:

- `internal/controljournal/v2/playback` remains structurally incapable of
  importing time, storage, network, filesystem, Git, tracker, randomness, or
  any durable/external capability.
- The v2 foundation and checkpoint packages remain time-free. They operate on
  explicit immutable inputs and do not derive authority from an ambient clock.
- `internal/controljournal/v2/restore` may import the Go standard-library
  `time` package solely to type, validate, and enforce elapsed-attempt resource
  budgets through context deadlines. This does not authorize wall-clock values
  in durable state, ordering, digests, revision selection, or acceptance.
- No other standard-library or external import is admitted by this correction.
  The exception is the exact source/target edge, not a prefix-wide allowlist.

The restore budget remains explicit and closed: positive duration, bounded
maximum, one child deadline per attempt, cancellation/join on failure, and a
typed bounded diagnosis on exhaustion. Store revision and database identity,
not wall-clock time, continue to determine authority.

## Permanent guard

WP01 must express the exception as an exact package edge and retain the common
pure-journal allowlist for every other v2 package. Executable controls must
prove:

1. `restore -> time` is accepted;
2. foundation, checkpoint, and playback imports of `time` are rejected;
3. widening the exception from exact restore ownership to the v2 package
   prefix is killed by the same production-facing validator; and
4. removing the elapsed-attempt budget from restore does not become an
   acceptable workaround.

Existing mutant identities remain present. Any new widening mutant is appended
to the sealed census rather than replacing an earlier control.

## Routing

1. WP04 remains blocked with its seven owned files preserved. It removes the
   unnecessary checkpoint `io` import and keeps the explicit restore timeout.
2. WP01 reopens only for the exact `restore -> time` architecture exception,
   positive/negative edge tests, and a widening mutant.
3. WP01 is independently reviewed and merged before WP04 resumes.
4. WP02 and WP03 require no production or guard changes.

## Acceptance evidence

- The repository architecture suite accepts the real WP04 restore import and
  reports no other new production import allowance.
- Time imports in foundation, checkpoint, playback, or another v2 subpackage
  fail with `ARCH-DEFERRED-SURFACE`.
- A prefix-wide or renamed-owner exception fails the widening mutant.
- WP04's full functional and race suites retain the elapsed-attempt budget and
  structurally pure playback.
