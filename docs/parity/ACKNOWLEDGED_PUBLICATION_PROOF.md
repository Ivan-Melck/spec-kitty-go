# Closed read-only acknowledged-publication proof

Implementation decision under the requester's approved trace-aware restart
contract, recorded in
[the numbered approval](../programs/decisions/2026-10-13-parity-qualification-entry-approval.json).
Status: implemented and independently reviewed; complete-trace scenario wiring
and real restart equivalence remain under verification.

## Boundary revealed by implementation

Workspace epoch rebinds deliberately have immutable **host-only** operation
bindings. Their project/work coordinates in a command/result do not confer
mission membership. Existing `Host.InspectOperation` correctly refuses those
roots through a mission-scoped query. Preserve that refusal and public census.
A trace must independently prove **all** its acknowledged receipts, but only
mission-bound roots must be members of that mission's public Items/Operations.

## Chosen mechanism

Use the existing observer-only publication boundary to collect opaque, actual
`AcknowledgedPublicationV2` values, including startup and continuation versions.
Add one closed, read-only Host proof surface accepting an opaque acknowledged
publication, not caller-chosen keys, operation IDs, scope overrides or results.
At a verified captured provider coordinate, prove the exact persisted publication
witness, immutable operation binding and historical journal records for that
acknowledgement. Historical state is not the current head: continuations may
have advanced it. Return defensive proof data distinguishing both. Require the
current head to remain at least the acknowledged version; verify exact latest
state separately against the complete collected acknowledgement history.

Proof reads cannot publish, refresh/repair authority, rotate an epoch, grant
membership, allocate a workspace or replay a command. Caller cancellation,
unknown/zero acknowledgement, missing/malformed/different witness/binding/event,
wrong address/version/revision and changing provider coordinate refuse as a
whole. Production durable limits and codecs remain unchanged.

## Rejected alternatives

- Infer mission scope from names/request coordinates or include host-only roots:
  would rewrite membership law.
- Ignore workspace recovery or prove only a name/epoch: incomplete evidence.
- Add unrestricted global operation lookup/raw-key CRUD: unnecessary disclosure
  and generic authority-shaped capability.
- Require identical histories: contradicts the approved trace-aware outcome law.

Mission-bound proof still uses the complete mission census and scoped operation
inspection. Host-only receipts are separately retained and verified; their
absence from that mission census is asserted, not hidden. Outcome commitments
may differ from per-trace history commitments without dropping any receipt.
No historical M2 artifact, source hash, receipt or admission is changed.

## Implemented proof and verification

`Host.VerifyAcknowledgedPublication(ctx, ack, origin...)` accepts only opaque
coordinator acknowledgements. A continuation requires exactly its verified
initial acknowledgement; an initial publication rejects extra origins. Binding,
witness and historical event bytes/revisions are proved at one captured,
verified coordinate. Current head and historical operation state remain distinct.
No RPC facade, raw-key lookup or mutation authority was added.

Independent review found that unrelated commits could otherwise hide an
impossible current-head version jump. The head's logical-version growth is now
bounded by its own record-revision growth since the acknowledgement, not merely
the captured global revision. An unconditional control uses four genuine
unrelated publications, accepts the valid unchanged head, then rejects a
validly encoded but impossible head. Its pre-fix failure is preserved in
`/tmp/spec-kitty-ack-proof-head-delta-red.log`; corrected proof unit/race and live
persisted continuation/restart controls pass in
`/tmp/spec-kitty-ack-proof-reviewed-race.log`. The live fixture is explicitly
bound to the exact WP13 assertion-extension executable. Review follow-up confirmed
the fix and subtraction bounds; no historical admission selector is introduced.

### Declared permanent workspace claims

The development proof now includes optional `WorkspacePathClaim` and
`ClaimRecordRevision`. The adapter's closed
`ObserveAcknowledgedWorkspacePathClaimV2` accepts a captured coordinate and opaque
ACK; selection is derived solely from its sealed plan's declared claim membership.
No caller-selected key/digest/scope, foundation address/codec extension or generic
lookup is added. It proves exact immutable canonical claim bytes, key/schema,
absence-conditioned creation, ACK record revision, nil expiry and stable captured
database coordinate. Missing, unsupported or altered declared evidence refuses
the whole receipt; no declared claim causes no claim read/disclosure.

Owned unit/race/vet, unchanged architecture guards and actual Host/localfs restart
controls pass in `/tmp/spk-claim-{unit-fixed,race,vet,guards-final,live-localfs,live-race}.log`.
Independent scoped review found no blocker. This is exact permanent-record proof,
**not** physical binding validity, workspace ownership correspondence or complete
trace certification. This extension is newer reviewed development and is absent from
failed candidate `5e9da5fc94ffb756c2d889521754d9ce1eebdbee`.

This API proves individual receipts, **not complete trace collection**. Its
caller must still prove contiguous duplicate-free versions for every root,
compare the latest receipt to the exact current state, retain the complete raw
history, and enforce the mission census/host-only separation. New scenario
controls and independent oracle review are pending; these passes do not establish
restart/crown or clean-source qualification.
