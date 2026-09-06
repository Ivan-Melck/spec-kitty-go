# WP10 review feedback — cycle 1

Verdict: **REQUEST CHANGES**

Independent review covered product commit
`ca53fe63b4daf0463f658ab604564c1d512c7a8d` (tree
`8c28ae5d391a8349ff81ce4d3225b0c3b24fc202`) and the unchanged product tree
at lane HEAD `8a32133591d8ae03e02c51e63e1a440c533a5daa` (tree
`4469352c0027b6f0561183a39010e762086127c0`). Unit, focused race,
architecture, compile, vet, staticcheck, module, formatting, and diff gates are
green, but the following contract blockers remain.

## 1. Acceptance trusts caller-minted waiver and applicability policy

`SealAcceptanceWaiverV2` is a public hash helper over arbitrary caller input.
`validateAcceptanceAuthorityV2` then validates waiver scope/time solely against
that same request and treats caller prose as authoritative inapplicability.
`acceptanceAuthorityV2.ObserveAcceptanceAuthority` observes only eligibility,
handoff, reviews, and satisfied evidence heads. It observes and conditions no
current mission-policy or waiver authority fact.

This permits a caller to mint a content-valid waiver, or declare any criterion
inapplicable with an unversioned `PolicyBasis`, without durable authorization.
It also permits acceptance of a same-contributor review merely because the
immutable review contains a non-nil historical exception; acceptance does not
prove the required policy is current at the acceptance decision.

Repair by defining/consuming a narrow provider-neutral current policy/waiver
fact, binding its exact semantic identity and provider revision into the
acceptance observation and every publication assertion. Applicability must be
a closed producer result backed by exact policy authority, not request prose.
Add real production controls and mutants for fabricated waiver, fabricated
inapplicability, stale policy, and stale review-exception authority, all with
zero acceptance publication.

## 2. Ref publication can complete from ancestry alone

In `IntegrationAttemptPublishingV2`, the service ignores
`CompareAndSwapTarget`'s result after reading the target, then marks the
attempt complete whenever `IsAncestor(result, current)` is true. A competing
actor can move the target to a descendant containing the result while this
CAS fails; the attempt will still claim completion despite having no
observation attributable to its recorded publish effect.

Repair the Git port/result contract so completion requires exact,
effect-attributable compare-and-swap evidence. Lost-response reconciliation
must distinguish this attempt's successful update from unrelated ancestry.
Add controls for: exact lost acknowledgement, failed CAS followed by an
unrelated descendant containing the result, already-ancestral result without
operation/effect membership, and post-publish advancement.

## 3. GapDB and restore qualification is synthetic/incomplete

The only GapDB test constructs the two services and rejects zero-value sealed
capabilities. It never accepts or integrates through the real adapter. There
is no adapter publication/reconciliation/partial-membership/restart test and no
checkpoint-plus-suffix restore proof for acceptance, target, attempt,
repair-required, completed, or cleanup state. All 100-run campaigns use an
in-memory domain port, not GapDB.

Additionally, `AcceptanceReducerRegistrationsV2` and
`IntegrationReducerRegistrationsV2` have no non-test production caller. The
composition fixture supplies placeholder reducers for these families, so it
cannot prove the shipped reducers or restored projections.

Add a real GapDB E2E using the common coordinator, exact production reducer
registrations, real sealed batches, lost acknowledgements, partial/mixed
membership, restart, and WP04 checkpoint/suffix playback. Verify byte-identical
state and zero external Git calls during restore. Wire the reducer registrations
into the actual production registry/composition path.

## 4. Git failures and persisted Git evidence are not bounded/redacted

New Git failure paths place the outbound adapter error in `Error.Cause`, and
`Error.Error()` renders the raw cause. An adapter can therefore expose
unbounded command output, paths, credentials, or repository details through
the public diagnostic. `IntegrationAttemptStateV2.Validate` also does not
bound workspace paths, conflict strings/files, or repair text before these
values enter durable state.

Introduce typed bounded Git observations and sanitized diagnostics while
preserving `errors.Is` classification where required. Validate every durable
external string and list against explicit limits. Add maximum and oversized
controls for each Git method and for conflict/workspace evidence, including
`Error()` and serialized forms.

## 5. Cleanup authority and mandatory mutants are incomplete

The service writes a cleanup effect intent in the terminal publication but
provides no production path to observe, retry, or durably complete that effect;
`IntegrationCleanupIntentV2` is otherwise unused. No cleanup-eligible/completed
state is restorable. The mandatory controlled-mutant census is also absent;
the current tests mutate a few fixture fields but do not execute the required
production overlays for omitted authority, ProcessID independence, early
fence, global target lock, changed identities, ancestry-only completion,
auto-repair, duplicate admission, cleanup-as-truth, or adapter/reducer bypass.

Add the separate attributable cleanup runner/state transition and the complete
sealed mutant census. Each mutant needs a passing production control and must
fail for its named semantic reason.

## Downstream impact

WP11 and WP18 depend on WP10. They must not consume this candidate as approved
and must rebase after the repaired WP10 authority contract lands.
