# WP09 review feedback — correction cycle 2

Verdict: **REQUEST CHANGES**

Independent reviewer Mill reviewed dependency repair
`b4e28034a4373fbd86d22207172085693d20f07f` and correction candidate
`ee0cf7a21b6dbb1cb9d6ecf459e5d2af47792084` (tree
`78b93e3d7e08e636102fefefe0e1226f885aa623`). The dependency repair and six
of the seven prior findings are accepted as closed. One production-path
evidence defect remains.

## Add a real GapDB mission-policy exception scenario

The same-actor exception is now structurally translated from a current mission
decision fact, but the positive domain test still fabricates a
`MissionPolicyFactObservationV2` and its assertion. The GapDB review-adapter
tests only construct the production services. Deleting or breaking the real
translation in `reviewAuthorityV2.ObserveReviewAuthority` would therefore leave
all same-actor exception tests green.

Add a production-path test using the real GapDB/v2 atomic-memory harness that:

1. durably creates a resolved `mission.MissionFactDecisionV2` whose
   `DetailDigest` is the exact canonical independence-exception digest;
2. establishes the required current workgraph, execution, workspace, and
   handoff authority through the real adapter path;
3. submits a same-contributor ActorID review through the production
   `ReviewAuthorityServiceV2` and adapter;
4. proves every legitimate review stage CASes the exact current mission-fact
   head and reaches the intended terminal verdict; and
5. moves or substitutes the mission fact head (including stale status or
   changed detail) before publication and proves a typed refusal with zero
   review mutation.

The test must exercise the actual mission port and GapDB translation. It must
not construct `MissionPolicyFactObservationV2` or its assertion directly.
Include a controlled production mutation or equivalent assertion proving that
removing/breaking the adapter translation makes this scenario fail.

## Accepted cycle-2 closures

- explicit immutable candidate replacement with predecessor lineage, reason,
  CAS, fresh operation binding, replay, and conflict;
- current WorkRevision-owned exact criterion membership and head assertion;
- canonical criterion reorder equivalence;
- real production-path adapter-policy mutant;
- exact same-PublicationID reconciliation with partial/mixed topology refusal;
- complete-eligibility-only services and callable v2 production constructors.

## Required rereview evidence

- Run the new real GapDB mission-policy positive and stale/substituted cases
  repeatedly and under the race detector.
- Rerun the focused review/GapDB suite, the 13-mutant census, architecture,
  compile, vet, staticcheck, module, formatting, diff, and ownership gates.
- Submit a new immutable correction commit and tree; do not amend either prior
  candidate.
