# WP02 Review Feedback — Cycle 12

**Verdict:** Request changes

**Reviewed head:** `5413eb4ccb64c73e0684b65ec54780ad033d1622`

`canonicalUTCInstant` is not yet exactly equivalent to the frozen GapDB
RFC3339Nano parse-and-format language. It reuses the older whole-second helper,
which rejects year `0000`; Go's `time.Parse(time.RFC3339Nano, value)` and GapDB's
canonical codec accept canonical year-zero instants.

Repair only this mismatch: admit canonical year-zero whole-second and fractional
instants while preserving all existing rejection laws. Add both positive cases.
The independent comparison found no other mismatch across 20,000 deterministic
canonical instants and alternate-spelling mutants.
