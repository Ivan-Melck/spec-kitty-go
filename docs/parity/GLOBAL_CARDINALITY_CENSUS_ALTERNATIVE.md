# Global retained cardinality + closed exact reads — conditional alternative

Status: **independently design-reviewed viable conditional proposal, not
implemented or qualifying any census**. This is a proposed product-only route using an
existing provider count; it is not permission to broaden raw reads, retarget
receipts, omit expiry, or weaken the stronger physically retained membership claim.

## Parent-verified source facts

Pinned GapDB `006e9d469d40` provides `AdminCounts()` as `len(state.records)` under
the engine read lock (`internal/engine/admin.go:83–87`), including expired rows
still retained in that map. `admin.Status` obtains revision metadata and counts
in **separate** calls/locks (`internal/admin/admin.go:36–40`): do not treat one
status response as an atomic revision/count snapshot. Product adapter Inspect
currently discards this count (`internal/controlstore/gapdb/adapter.go:401–433`).

Provider EncodeSnapshot omits expired rows (`internal/persist/snapshot.go:29–38`).
Physical membership here means currently retained logical records in the running
engine, not historical/deleted puts or every byte remnant in WAL/snapshot files.
Existing scans and recovery snapshots are filtered views, not physical censuses.

## Conditional finite-set theorem

Let M be the provider's **entire retained map** in one unchanged authenticated
owner incarnation at stable durable revision R. Let E be a finite unique expected
key set drawn ONLY from closed, actually collected publication and private-owner
records, not caller arbitrary keys or predicted writes.

If every key in E is independently read back with its exact last bytes, revision
and no expiry, then E is a subset of keys(M). If a coherent actual retained global
count is also exactly |E|, finite-set equality follows. This excludes an extra
expired head as well as an extra live/private/unrelated record. No full-database
raw enumeration is logically necessary. Merely counting ACK events, current
aggregate heads, or returned live records is insufficient.

Important consequences:
- E must cover ALL provider key families, not just non-operation heads. Shared
  stores with uncollected projects/operations/private records must refuse, not
  silently discount their count or expose their content.
- Repeated writes select the last independently supported value per exact key;
  duplicates, address aliases and unsupported deletes/condition transitions
  cannot inflate |E|. Original permanent claims and immutable records remain.
- Every expected key needs independent readback. Correct predicted private batch
  cardinality, expiry-free product write invariant, genesis assumptions and
  runtime-ready flags are not sufficient evidence.
- Revision gaps need not be treated as empty mutation history: this proves an
  endpoint retained set only. It does not prove complete provider action history,
  omitted operation-only publications, original ACK producer identity or lineage.

## Actual gaps, not solved by the theorem

Collected coordinator ACKs omit immutable authority binding and each private
five-record fresh-epoch admission. Parent read actual emission paths:
`productionruntime/composition.go:197–222` and `recovery.go:245–297`. Admission has
three new immutable keys per epoch plus two overwritten current/head keys, exact
conditional revisions, recovered-prefix linkage and a checked durable result.
No complete private record readback proof is yet supplied to the terminal
history. Old epochs cannot be guessed from the latest ready value. Endpoint
membership may derive identities from the owner's fixed current/binding anchors
and strictly validated predecessor/created-operation fields, provided EVERY
included row is independently read. Original historical batch ACK/CAS/prefix
admission authority remains a separate graph requirement; endpoint counting alone
does not certify an original private writer or erased history.

Expected publication records include more than events/heads: bindings, witnesses,
effects, eligibility, checkpoints and claims need exact role/address/currentness
closure. Existing semantic recovery exports deliberately exclude private domains.
Do not expand foundation addresses/raw-key authority to compensate; any private
record proof stays in its original production owner with fixed typed inputs.

A product-only observation seam would need the existing retained count, strict
integer/range/work/byte/time controls and bounded no-content failure. It must
independently guarantee one stable authenticated owner/database/revision while
counts and ALL reads occur, plus continued durability R and projection recheck.
Use sequential **authenticated S0 → exact reads → counted S1 → closing S2**,
requiring each revision sample R, ready lifecycle and durable-through≥R; use
**S1's count**, not S2's count (which occurs after its own revision sample).
Parent read writer.go:175–185,221–235: map mutation and strictly increasing current
revision share one write lock, preventing a same-lifetime revision-preserving ABA.

Follow-up research found no supported same-lifetime map reload in the pinned
executable: one server Open, runtime assignment once, no online restore/switch;
snapshot does not replace the live map. Thus a new engine nonce/provider API is
not inherently necessary. Source roots: cmd/gapdbd/main.go:42–70,
server/server.go:161, engine/snapshot.go:97–129, server/handler.go:32–126. Parent
independently read the socket dispatcher and verified offline operations are
refused; recovery constructs the map before serving.

However, mutually equal reported PID/start timestamps and cached startup
provenance are NOT per-RPC authentication. Parent read sidecar lifecycle.go:727–785:
existing strict unary observation checks the ACTUAL connection's peer credentials,
endpoint inode and pre/post kernel process identity. The new seam must integrate
equivalent attribution for EVERY count and exact record read, including reconnects,
against the retained owner identity—not a separate probe connection. Mixed/
substituted responses and owner replacement must refuse. No IO refresh may repair
a failed proof, and no partial proof/count leakage is returned.

## Decision before implementation

Define the exact product-owned count/readback/authenticated-connection and
private-record capture/readback seams that implement these conditions. The
finite-set route is viable in principle; those seams and complete inputs remain
unproved. If defensible, independently review a narrow
product contract before code, then require fresh source-bound consumer checks
using unchanged exact provider bytes. Do not claim a new provider image is needed
merely because product Inspection omitted an existing count. If this ownership or
complete expected-set condition cannot be proved, retain the blocker and consider
a separately qualified provider physical-count/include-expired observation.

Research and its correction are both preserved in
`/home/user/.cache/spec-kitty-parity-resume-followup/census-contract-investigation/researcher-744a11c695b54c93a2b82e8e22e82371/`
(`report.md`, `addendum-finite-membership.md`). The first report omitted this
alternative; parent proposed the finite-set synthesis, then follow-up source
research confirmed its conditions. Read-only independent reviewer `90c9cf6b-7ab7-46e` then found no counterexample
under the explicit conditions and called the contract viable. Its review is saved
as `census-contract-investigation/finite-census-design-review.md` under the same
follow-up cache. It confirmed the middle-count revision bracket, actual-RPC owner
attribution, complete encoded-key/private readback, strict preflight and no-content
refusal requirements. This is design review, not an implemented proof. This route
cannot yet be credited to equivalence.
[Original expiry boundary](CENSUS_EXPIRY_BOUNDARY.md),
[ACK-bounded proposal](AGGREGATE_HEAD_CENSUS_PROPOSAL.md), and the separately scoped
configured-repository increment all keep their distinct proof limits.
