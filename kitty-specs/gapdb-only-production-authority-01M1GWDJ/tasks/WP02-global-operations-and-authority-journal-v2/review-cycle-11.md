---
affected_files: []
cycle_number: 11
mission_slug: gapdb-only-production-authority-01M1GWDJ
reproduction_command:
reviewed_at: '2026-09-03T14:04:08Z'
reviewer_agent: user
wp_id: WP02
---

# Post-Approval Defect 3: Recovery Instants Lose Provider Precision

**Affected work packages:** WP02, WP03

**Disposition:** keep WP03 blocked with its separable adapter edits intact;
reopen, repair, and independently reapprove WP02 before resuming WP03.

## Finding

The foundation recovery values currently accept only whole-second UTC text.
The frozen GapDB public contract transports `AsOf` and `ExpiresAt` as UTC
`time.Time` values encoded canonically with RFC3339Nano precision. Its normal
clock and durable expiry representations preserve nanoseconds.

WP03 therefore cannot satisfy the requirement to retain provider metadata: it
must either truncate a real instant or reject an ordinary provider response.
Neither behavior is admissible.

## Bounded correction

WP02 must admit the canonical UTC RFC3339/RFC3339Nano representation of the
exact instant while keeping provider-neutral immutable strings. The accepted
language is:

- UTC `Z` only;
- a valid calendar instant;
- whole seconds, or 1–9 fractional digits when the fraction is non-zero;
- parse-and-format byte equality under RFC3339Nano, so offsets, trailing-zero
  aliases, overprecision, malformed dates, and alternate spellings refuse.

`RecoveryRecordV2.ExpiresAt` remains optional. `RecoveryPageV2.AsOf` remains
required. Accessors preserve the exact canonical spelling and instant.

Add whole-second and fractional positive tests plus offset, trailing-zero,
overprecision, malformed, and post-construction stability controls. No GapDB
or `time` capability leaks into the recovery API; parsing is validation only.

After independent reapproval, WP03 must consume this corrected value without
truncation and prove subsecond `AsOf` and `ExpiresAt` round-trip exactly.
