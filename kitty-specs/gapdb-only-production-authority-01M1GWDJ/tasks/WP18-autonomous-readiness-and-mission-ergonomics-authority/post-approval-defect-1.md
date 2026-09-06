# WP18 post-approval defect 1: acknowledged workgraph cannot enter readiness authority

WP14's executable parity review exposed a missing authority bridge between the
approved WP04 workgraph publication and WP18's readiness-native mission state.

`workgraph.BuildFinalizationAuthorityV2` and the production host can publish a
generation-one revision through GapDB and receive an
`AcknowledgedPublicationV2`. However,
`SealInitialWorkRevisionSelectionV2` accepts only the private legacy
`mission.FinalizationResult`. A production client cannot lawfully construct the
`WorkRevisionSelectionV2` required by `ReadinessEventWorkRevisionSelectedV2`.
It can only bypass readiness authority, fabricate private publication evidence,
or continue using the obsolete finalization store.

## Required repair

Add a public, provider-neutral sealing function that accepts the exact
`workgraph.RevisionV2` and its acknowledged Authority Journal v2 publication,
then:

1. verifies the acknowledgement and enclosed sealed plan agree;
2. proves the plan contains exactly the generation-one `workgraph.finalized`
   event for the supplied canonical revision and mission coordinate;
3. binds the original operation intent digest and complete acknowledged
   publication evidence into the selection;
4. rejects wrong revision content, operation, event family/type, mission,
   generation, publication ID, plan digest, record-set digest, revision,
   durability, mutation count, or assertion count;
5. reuses the existing opaque selection and mission transition validation; and
6. includes controlled mutants for accepting an unacknowledged or substituted
   workgraph publication.

The bridge must not expose a raw-store constructor, accept a caller-authored
boolean, or make a workgraph publication grant execution authority. It only
makes the acknowledged revision eligible to be selected by the mission-owned
readiness command.
