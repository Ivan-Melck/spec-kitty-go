# Post-approval defect 8: restore deadline proof ownership

## Finding

Defect 7 correctly granted one narrow capability: the consumer-owned restore
coordinator may use `time.Duration` and `context.WithTimeout` to enforce its
explicit elapsed-attempt budget. Its first WP01 implementation tried to prove
the complete validation and deadline data flow with a custom AST/types
control-flow verifier.

Independent review cycles 20 through 23 demonstrated that this proof mechanism
is unsound and disproportionate. The verifier accumulated hundreds of lines
while still admitting package-level function aliases, indirect calls, methods,
post-validation mutation, boolean masking, and other unvalidated paths. It also
rejected semantically valid Go expressed through ordinary `else`, inline,
recursive, closure, switch, and success-branch forms. Completing it would mean
implementing a partial Go compiler inside an architecture test.

The production WP04 restore implementation remained correct throughout these
reviews. The defect is in proof ownership, not in the restore protocol.

## Ratified correction

The defect-7 semantic law remains unchanged, but proof is split by claim:

- **WP01 owns structural capability confinement.** It proves the exact
  `internal/controljournal/v2/restore -> time` edge, rejects `time` everywhere
  else below `internal/controljournal/v2`, resolves objects with `go/types`,
  admits only `time.Duration`, `time.Second`, and `time.Minute`, rejects ambient
  clock/timer APIs, confines duration state to the explicit restore budget, and
  requires a direct non-escaping call to the real `context.WithTimeout` object.
- **WP04 owns executable deadline semantics.** Source-bound tests exercise the
  production `RestoreV2` path and prove validation before I/O, input-sensitive
  deadline derivation, propagation to every reader operation, cancellation and
  refusal to seal, fresh per-retry deadlines, and earlier/later parent-deadline
  behavior.
- **WP17 owns final combined evidence.** It binds the exact production source
  identity to a sealed overlay-mutation campaign and the named structural and
  behavioral tests. A pass cannot be assembled from different revisions or
  substituted fixtures.

No architecture test claims to prove arbitrary Go control flow. WP01's custom
CFG/data-flow verifier is removed rather than extended.

## WP01 structural proof

When restore exists, the production-facing validator must prove:

1. the sole exceptional standard-library edge is exact `restore -> time`;
2. the only referenced `time` objects are `Duration`, `Second`, and `Minute`;
3. the single admitted duration-bearing field is the restore budget's maximum
   attempt duration, not checkpoint, result, admission, journal, digest,
   ordering, or authority state;
4. at least one direct call targets the real `context.WithTimeout` object; and
5. `context.WithTimeout` is not stored or passed as a function value.

Aliases and lexical shadowing are resolved by object identity. WP01 retains
`M-PURE-TIME` and the exact-owner-widening mutant. It adds controls for ambient
clock use, misplaced duration state, a missing direct deadline call, and
deadline-function escape. These controls do not infer validation dominance.

## WP04 behavioral proof

Add `internal/controljournal/v2/restore/deadline_contract_test.go` with
source-bound tests covering:

1. two distinct valid `MaximumDuration` values produce correspondingly
   distinct deadlines materially earlier than a long parent deadline;
2. `CaptureRevisionV2` observes the child deadline before first I/O;
3. capture, scan, read, and verification observe the same attempt deadline;
4. expiration cancels a blocked operation and cannot produce epoch admission;
5. negative, zero, over-maximum, smallest-positive, and exact-maximum budgets
   are decided before I/O at their required boundaries;
6. a retry receives a fresh child deadline; and
7. an earlier parent cancellation remains authoritative while a later parent
   deadline does not replace the configured attempt budget.

Tests use bounded scheduler tolerances and a longer parent failsafe. A hang is
a failure, never evidence of deadline enforcement.

## Source-overlay non-vacuity controls

Against the exact WP04 source, a bounded overlay campaign must kill:

- `M-RESTORE-DEADLINE-REMOVED` — replace the child timeout with the parent;
- `M-RESTORE-DEADLINE-CONSTANT` — use a fixed duration;
- `M-RESTORE-DEADLINE-LATE` — create the deadline after the first read;
- `M-RESTORE-DEADLINE-PARTIAL-BYPASS` — pass the parent to a later reader call;
- `M-RESTORE-DEADLINE-LOWER-BOUND-REMOVED`;
- `M-RESTORE-DEADLINE-UPPER-BOUND-REMOVED`; and
- `M-RESTORE-AMBIENT-NOW` — introduce `time.Now`, killed by WP01.

Each replacement has an exact one-occurrence source anchor. Missing or multiple
anchors fail the campaign. Evidence records the production commit/tree, mutant
ID, substitution, and exact killing test. Generated fake packages may test the
structural contract but are never the primary behavioral proof.

## Routing

1. Reopen WP01 only to delete the unsound CFG verifier and install the bounded
   structural object-identity guard and controls.
2. Independently review and merge WP01.
3. Resume WP04, preserve its draft, and add the dedicated deadline contract
   tests plus bounded overlay controls within WP04 ownership.
4. WP17 freezes and reruns the combined structural, behavioral, and overlay
   evidence against one exact source identity.
5. WP02 and WP03 require no changes.

## Acceptance evidence

- WP01 rejects ambient clock/timer APIs, misplaced duration state, missing or
  escaping `WithTimeout`, and any widened `time` owner without interpreting
  control flow.
- WP04 tests fail if validation, derivation, placement, propagation,
  cancellation, retry freshness, or parent-deadline precedence is broken.
- Every overlay mutant is killed by a named production-facing gate.
- The combined gate is source-bound, deterministic, bounded, race-clean, and
  independent of private function or variable spelling.

