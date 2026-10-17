# Tagged S1-negative closure — infrastructure slice only

This slice is NOT a genuine-Host expiry witness, admission, full negative matrix,
performance/memory qualification, or completion of the active four-deliverable goal.
It makes the previously impossible S1-negative terminal sequence implementable.
Actual launch/provenance/FD3/FD4/gate/root/recovery/authority binding remain OPEN.

## Exact boundary and design receipts

Tag: `spec_kitty_expiry_host_qualification && linux && amd64`. Disabled complement
is explicit. Ordinary build exposes NONE of the three new methods. No normal
admission/root/provider/executable, existing kernel file, cap or budget changes.
Only shared production change is the empty-first private state field, authenticated
S1 note and Finish tail dispatch in `retained_observation.go`.

Design artifacts under `/home/user/.cache/spec-kitty-parity-resume-followup/`:
- `expiry-closure-parent-0nnjq77b/SLICE_DESIGN.md`: V1, REJECTED, preserved.
- `expiry-closure-design-review-kpbiohb1/REVIEW.md`: B1 sequential post-terminal
  revocation counterexample. It was not dismissed or overwritten.
- `expiry-closure-parent-0nnjq77b/SLICE_DESIGN_V2.md` SHA256
  `ff0f3868ef561a3230ea8ad97a2daedfdb4e3e9590112ef86e4898763dd1a1eb`.
- `expiry-closure-design-review-qcf2ufea/REVIEW_V2.md`: precise V2 implementation
  APPROVE BEFORE product edits, conditional on independent actual-source review.
  It approves this bounded slice, NOT the much larger Host technical draft.

## State machine

One pointer is armed at phase0. S0/exact publications/private reads unchanged.
Only the existing just-authenticated S1 count inequality can mark the private
armed mismatch. Armed equality and every other failure invalidate/refuse; no
positive publication count or public reason/data escapes. Unarmed behavior is
identical in both builds.

Armed mismatch performs authenticated S2 (ignores count) once; successful Finish
returns **zero/sentinel error**, leaving phase3 live. This is NOT yet evidence.
Unchanged CheckLifetime then consumes phase3, reserves the one terminal full hash,
and checks the original executed lifetime. Every error invalidates all aliases.

Canonical pointer Consume requires phase4, armed/mismatch/S2 and terminal flag,
then one fresh fixed kernel liveness guard. Under the original mutex its invalid
CAS linearizes single consumption; it cancels the private session before unlock.
Wrong handle/copy, overlap, replay, Close, cancellation, absent S2/terminal or failed
lifetime checks cannot earn another consumption. No context renewal, RPC or full
hash is added at consumption. Inert copies are not detectable; finalizing a
noncanonical copy is detectable and invalidates all copies.

The E-only `Lifetime.CheckExpiryQualificationLive(ctx)` locks lifetime.mu, uses the
existing bounded pidfd poll, then explicitly rechecks context/revoked/closed. It
refuses Revoke/Close completed AFTER terminal hashing but BEFORE Consume begins.
No descriptors/counts/selectors/callbacks/admission objects escape. The existing
kernel sources stay byte-frozen. Poll interruption refusal still uses64 attempts;
mutex waiting and cooperative checks do not imply a hard-wall bound.

Lifetime's last scalar check and observation's CAS are SEPARATE cutoffs: a racing
revocation AFTER the former may precede CAS without detection. No atomic joint
snapshot or future liveness is claimed. The future Host wrapper must close its
independent caller/run/gate contexts and immutable Host/projection/lifecycle lease
AFTER Consume; it MUST NOT require the intentionally canceled PrivateContext to
remain live or derive its outer context from it. This slice supplies no wrapper.

## Costs, tests and non-transfer

Ordinary pre-product baseline test captured136-byte state and exact field offsets;
the empty FIRST disabled field preserves that geometry. It does NOT prove zero
allocation/escape/latency or unchanged emitted code. Tagged state152 bytes, tagged
state component16 bytes. Pinning a canonical handle creates additional escape/
retention obligations. Conditional allocator classes would be144/160 for these
state sizes under the ordinary slot model, NOT actual heap allocation certificates.
No complete event multiplicity, retained-live peak, RSS or runtime certificate.

3A+1 full-image hash budget unchanged. E adds one liveness guard: at most64 zero-
timeout polls plus owned mutex/poll/context/wrapper/error and canonical-handle costs.
The ordinary partial ledgers/JSON/source profiles do NOT transfer automatically to
this tag. New tagged binding/root costs are not even implemented yet.

New E tests use actual Unix exchanges to the existing SIMULATED same-process
endpoint; kernel guard controls use an actual self pidfd, NOT admitted executable
identity. Tests cover8 closed-negative cases with/without publications, missing/
extra S1 and outer-count decoys;24 coordinate/phase failures; exact-read failures;
scalar/phase/copy/replay/budget/cancel/revoke/Close negatives; synchronized overlap;
inner-canceled/outer-live semantics; exact RPC/hash/budget closure and no renewed
work. Both completed post-terminal Revoke and Close are mandatory negatives.
Ordinary API/layout and old unarmed semantics remain independently testable.

Current test/review receipts and status are recorded by the parent in
[the implementation record](OBSERVATION_BOUNDARY_IMPLEMENTATION.md). A green
infrastructure slice cannot substitute for genuine held expiry through the new
Host root, all native negatives, full owned accounting or max-work performance.
