# WP13 post-approval defect 2

WP14's source-bound scenario campaign exposed that the production host's
readiness census and acceptance policy evidence could not share one mission
authority. WP18 post-approval defect 3 repaired that kernel seam by publishing
readiness fact transitions and acceptance-consumable fact heads in one sealed
GapDB batch.

Required integration review: merge the approved WP18 repair into the WP13
production-runtime lane and prove the host still composes exactly one GapDB
authority owner, exposes readiness commands and progress from the same durable
mission state, preserves exact replay and runtime-epoch refusal, and introduces
no legacy phase authority or alternate production store.
