# Approved observation-boundary implementation

Decision source: the current requester explicitly answered **“I agree with your
reccomendation. Implement”** after the two alternatives were explained. This is
conversation-local scope approval, not a forged external signature, provider
adoption, mission admission, release decision or a completed qualification gate.
Architect Op `01M4V7BWB53R4CK18V28SFDD8A` remains open.

## 1. Resource reporting

Accept a bound on **observer-owned incremental allocations**, with separately
named borrowed, shared and runtime terms. Do not require a whole-process/RSS
ceiling as this layer's memory deliverable. Keep all existing refusal caps.

Owned includes both project allocations and observer-exclusive allocations made
by libraries: temporary returned buffers, fresh connection/context objects,
closures, channels and pending callback-owned graphs. Calling a library does
not turn those allocations into shared overhead. Preexisting ACK/Host/context
backing is borrowed; growth of shared parent maps, timer heaps and global pools
is separately disclosed shared causal cost. Stacks, allocator spans, polling and
finalizer runtime storage need their own disclosed classification.

A ledger must distinguish logical requested bytes, allocator-rounded slots,
live/retained storage, cumulative allocation and empirical process measurements.
An incomplete ledger must name uncovered sites and must not output a complete
observer-bound or total-memory acceptance. No new arbitrary numeric ceiling,
blanket standard-library exclusion, hard-wall/P95 or global-concurrency claim is
approved. Proof APIs remain matched-only; reporting is private qualification
bookkeeping, not a new provider-count/selector/authority surface.

## 2. Separately qualified expiry-test executable

Authorize developing and independently reviewing a narrowly scoped cleanup-hold
hook in an **isolated copy** of the exact pinned provider source, then building
and testing that separate executable. Never modify the module cache, admitted
source/images, existing tuples, manifests, adoption records or historical logs.

The hook must hold only a genuinely due expiry writer outside reader/admin
locks, acknowledge acquisition, allow actual Get/status to continue, release on
explicit request, EOF, malformed control or a finite timeout, and leave ordinary
expiry behavior intact on release. It must not add a public provider API or
production environment switch. Build-tag exclusion and a no-hook control build
are required. New evidence binds exact source delta, toolchain/recipe and test
binary digest. Neither a simulated timer nor a TTL-cleanup race is the witness.

The first permitted experiment qualifies the **separate test executable and
read-only adapter count/refusal seam**, not the original admitted executable,
original production Host composition or complete observation layer. A later
Host-composed qualification must preserve the original lifecycle/admission laws
and obtain its own reviewed technical binding; this approval does not authorize
an arbitrary admission factory or reusing an old receipt for a new binary.

## Implementation/evidence

- `scripts/parity/expiry_evidence/`: isolated source preparation, evidence-only
  hook and build/run instructions; no product-module replacement.
- `internal/controlstore/gapdb/retained_expiry_evidence_test.go`: opt-in real
  daemon/actual-ACK exact-read, hidden-expired-extra count rejection and release
  controls. No Host/adoption assertion.
- [Owned accounting](OBSERVER_OWNED_ACCOUNTING.md): checked private model,
  explicit coverage and remaining allocation recipes.

This decision supersedes the two **authority-needed** stops in
[RETAINED_RESOURCE_BOUNDARIES.md](RETAINED_RESOURCE_BOUNDARIES.md), not the missing
implementation/measurement/review evidence. Prior checkpoint artifacts remain
historical and unchanged. Overall parity, production observation, P95 and release
qualification remain incomplete until their actual requirements are evidenced.
