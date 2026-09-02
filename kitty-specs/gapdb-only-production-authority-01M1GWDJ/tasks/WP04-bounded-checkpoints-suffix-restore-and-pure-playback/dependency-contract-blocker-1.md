# WP04 Dependency Contract Blocker 1

WP04 stopped before edits because the approved WP02/WP03 surface cannot legally
express the recovery protocol required by T017–T022.

## Observed contract gaps

1. The v2 key, prefix, record, scan request, and read methods described as
   provider-neutral are declared inside `internal/controlstore/gapdb`. WP04 is a
   foundation package explicitly forbidden to import the GapDB adapter, and the
   permanent architecture guard enforces that boundary.
2. The read surface cannot capture a current provider revision and cannot ask
   for an explicit pinned revision on subsequent scans/reads. Reporting an
   observed revision after a read is insufficient to prevent a mixed-revision
   restore attempt.
3. `EventEnvelopeV2` and `AggregateHeadV2` do not expose the complete immutable
   semantic tuples needed by pure playback, checkpoint census construction,
   operation membership, and terminal-head validation.
4. No acknowledged-publication projection/rolling-prefix capability exists.
   Consequently checkpoint creation cannot distinguish durable acknowledged
   state from optimistic or forged projection input.
5. Event/reducer registry compatibility digests are unavailable, and the
   current reducer session contract cannot serve the bounded 10,000-operation
   replay proof required by WP04.

## Required resolution

Reopen WP02 and WP03 in dependency order. WP02 must provide the missing
provider-independent semantic capabilities, compatibility identities, replay
contract, and acknowledged projection/prefix seal. WP03 must implement a
foundation-owned revision-pinned recovery-read port at the GapDB boundary.
WP04 must then resume unchanged and consume only those contracts.

Do not resolve this blocker by importing the GapDB adapter into playback or
restore, decoding private canonical bytes into a second model, using Watch as
history, weakening the architecture guard, or silently dropping the pinned
revision/checkpoint-seal requirements.

No WP04 production or test files were changed. The lane was clean when the
blocker was reported.
