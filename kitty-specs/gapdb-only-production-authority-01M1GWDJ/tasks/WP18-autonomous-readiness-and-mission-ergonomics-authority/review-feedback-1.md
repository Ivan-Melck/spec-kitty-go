# WP18 review feedback — cycle 1

Reviewer: Mill (independent reviewer)

Verdict: **REQUEST CHANGES**

## Blocking findings

1. **The readiness result is caller-authored rather than authority-derived.**
   `ReadinessInput` exposes mutable contracts, facts, revision, and snapshot
   digest. `EvaluateReadiness` accepts arbitrary non-empty subsets and does not
   match fact producers or authority families to requirements. Replace this
   bag with an opaque/sealed exact snapshot assembled from the complete
   mission-type capability and scope census at one captured GapDB revision.
   Bind contracts, facts, decisions, blockers, conflicts, applicability,
   producers, and waiver evidence to that snapshot.

2. **Operation identity omits committed observation evidence.**
   `ObservationEvidence` changes the durable event but is absent from the
   canonical intent. Bind its exact canonical bytes or a domain-separated
   digest and membership into the intent. Prove changed evidence conflicts
   before epoch, authority reads, or publication.

3. **`CurrentWorkRevisionID` can be minted without finalization or
   reconciliation evidence.** Creation must require an empty revision slot.
   Selection must consume sealed WorkRevision authority and current evidence,
   preserve generation/lineage and `continue | supersede | invalidate | fork`
   semantics, and bind that evidence to operation/publication identity.

4. **Checkpoint-plus-suffix qualification is synthetic.** The current test
   reduces the same in-memory event slice twice. Qualify real origin and
   checkpoint-plus-suffix restore through approved WP04/GapDB APIs, include
   the complete external authority snapshot and census, and prove projection
   deletion is harmless.

5. **The production surface is disconnected and T099 is not semantic.** There
   are no non-test callers for `EvaluateReadiness`,
   `NewReadinessAuthorityServiceV2`, `softwaredev.CapabilityDefinition`, or
   `governancecontext.NewCapabilityAction`; the phase-driven path remains the
   only composition. Replace identifier/enum-only mutants with executable
   ceremony defects, and drive a model through authorized actions covering
   pause/resume, drift/reconciliation, rejection/rework, verifier
   unavailable/failure, conflict, cancellation, restart, and terminal
   convergence.

## Validation evidence

- Repeated kernel, focused GapDB, focused architecture, focused race, mutant,
  vet, staticcheck, module verification, formatting, and compile-only gates:
  pass.
- Full unfiltered GapDB race was not repeated by the reviewer. The implementer
  observed 10- and 20-minute timeouts in the unchanged 100-trial real-Git
  zombie campaign, without a race report.

## Anti-pattern checklist

1. Dead code — FAIL
2. Synthetic-fixture test — FAIL
3. Silent empty return — PASS
4. FR coverage — FAIL
5. Frozen surface — PASS
6. Locked decision — FAIL
7. Shared-file ownership — PASS
8. Production fragility — PASS
