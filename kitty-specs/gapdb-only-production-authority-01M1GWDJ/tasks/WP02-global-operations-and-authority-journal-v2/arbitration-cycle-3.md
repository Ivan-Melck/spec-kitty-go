# WP02 Max-Cycle Arbitration

**Decision:** Authorize one bounded terminal repair of candidate `bbe3dd64`.
There is no ordinary review cycle four. After one repair commit, a terminal
arbiter must either approve the closed contract below or block WP02 for human
decision.

## Finding disposition

1. Provider-condition membership is a genuine blocker. Conditions must have
   closed provider-neutral semantic targets and form an exact bijection with
   the mutations they protect; omission, substitution, extras, duplicates, and
   revision mismatches must refuse.
2. Operation heads must always be host-scoped, including at constructor and
   decoder ingress. A generic standalone head cannot know the expected current
   OperationID/version; contextual event/state/head validation must enforce
   those relationships without pretending the decoder has missing context.
3. Every persisted PublicationID must use the exact lowercase 64-hex grammar.
   Empty is permitted only in the internal pre-seal state. Derivation is proved
   only where the complete sealed-publication preimage is available.
4. Calling an arbitrary Go reducer twice cannot prove purity. Replace that
   overclaim with an immutable bounded reduction session: one invocation per
   request, canonical input-to-output/digest observations, synchronized reuse,
   no eviction, typed refusal on observation-budget exhaustion, and refusal on
   any later mismatch. Qualify real reducers across fresh instances/processes;
   WP04 must validate playback against durable terminal digests.
5. Total encoded-plan preflight is a genuine blocker. Checked arithmetic must
   reject the complete prospective encoding before registry resolution,
   cloning, sorting, or canonicalization, including JSON escaping/framing and
   base64 expansion. The final encoded-size check remains defense in depth.

## Terminal proof contract

- Kill missing, replacement, extra, duplicate, absent/update, and wrong-revision
  condition mutants.
- Kill direct/decode/contextual operation-head scope, ID, version, EventID, and
  PublicationID mutants.
- Cover empty, short, overlong, uppercase, non-hex, valid, and contextually
  wrong PublicationIDs through one parser.
- Kill the pair-stable reducer mutant; prove concurrent identical inputs,
  caller-buffer isolation, fresh sessions, mismatch refusal, and exact
  observation-budget exhaustion.
- Prove exact maximum and maximum-plus-one whole-plan behavior before every
  later seam.
- Preserve every cycle-one and cycle-two repair, replay-before-epoch ordering,
  global binding collision refusal, immutable sealing, registry closure, pure
  journal boundaries, and all repeated/race/architecture/helper/static gates.
- Change only WP02's five owned files.

If any named item fails, the repair widens scope, or an external contract change
is required, WP02 becomes blocked rather than returning to another ordinary
review loop.
