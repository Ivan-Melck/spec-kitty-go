# WP09 review feedback — correction cycle 3

Verdict: **REQUEST CHANGES**

Independent reviewer Mill reviewed candidate
`080e84020429f58997d17cab246e57ad28b2d81a` (tree
`454cabfcc0c8f2da7c13030361861574d65f4a04`). The real durable GapDB scenario,
narrow tombstone-aware policy reader, current-head fencing, and all prior
production corrections are accepted. One test-sensitivity defect remains.

## Prove status and detail validation independently of revision CAS

The named `status_canceled` and `detail_substituted` cases mutate the policy
fact inside the provider batch hook after `ObserveReviewAuthority` accepted the
original resolved fact. Both also advance the provider revision, so they are
rejected by the same stale-revision assertion as the moved-head case. Removing
the production resolved-status or detail-digest checks would leave those tests
green.

Add real production-path cases in which `ObserveReviewAuthority` initially
reads:

1. a canonically valid tombstoned canceled mission decision fact; and
2. a canonically valid tombstoned resolved decision fact whose detail digest
   does not match the exact independence exception.

Keep each fact at its current provider revision so rejection cannot be
attributed to a CAS race or tombstone shape. Both cases must return the intended
typed semantic refusal before review publication and leave zero review records.

Add type-correct production mutations or injectable validation controls proving
that removing the status check makes the canceled-fact test fail and removing
or substituting the detail binding makes the wrong-detail test fail. Each
control must reach the actual GapDB translation path rather than a test-only
predicate.

## Required rereview evidence

- Run the independent status and detail cases repeatedly and under focused race
  coverage.
- Rerun the mission-policy scenario, relevant mutant census, architecture,
  compile, vet, staticcheck, module, formatting, diff, and exact ownership
  checks.
- Submit a new immutable correction commit; do not amend prior candidates.
