# Original configured repository capture — narrow private increment

Status: original implementation handoff. The [narrow increment](CONFIGURED_REPOSITORY_FOUNDATION.md)
is now implemented and independently scoped-reviewed after fixture correction;
this handoff is not itself qualification or oracle integration.
It closes only the configured-origin part of PHYSICAL_REPOSITORY_GRAPH_HANDOFF;
it cannot supply historical registration/effect/index observations or path tokens.
Census expiry, private admission and other graph blockers remain independent.

## Understood source boundary

`gitrepo.Bind` canonicalizes primary/common/prospective managed roots, verifies
primary is the Git top level, refuses managed overlap with primary/common, and
stores RepositoryBinding with repository/project, target ref, initial primary
HEAD/index/tree digests and timestamps. Binding() returns that original scalar
value without IO. Linked primary worktrees may legitimately have their common
Git directory outside the primary checkout; do not invent a nested-only rule.

The actual realScenarioGit retains the original *gitrepo.Repository. The private
coordinate factory currently sees only managedRoot()/repositoryPath() strings,
so it cannot distinguish declared layout from this configured origin. The
portable adapter must not inherit a real-Git repository witness.

## Allowed implementation

Only add `terminal_repository_foundation_test.go` and
`terminal_repository_controls_test.go`, plus narrowly located optional capture
in `terminal_coordinates_test.go`. No production, real oracle/crown, previous
controls, clean candidate, provider, mission/admission or Op changes.

1. At coordinate-proof construction, capture the original scalar RepositoryBinding
   only when the same harness's Git driver is *realScenarioGit with a nonnil
   original repository. Preserve by value; no adapter maps, later Binding reads,
   arbitrary caller roots/repositories or filesystem/Git lookup. Portable/logical
   construction remains supported, but configured proof refuses absent capture.
   Capture driver primary/managed declarations too, to prove equality to the
   configured values rather than trusting those strings as authority.
2. Keep the capture private and immutable/copy-only. Retain all configured facts
   (including initial HEAD/index/tree digest and CreatedAt/VerifiedAt), not a
   reconstructed subset. Use a bounded explicit private integrity commitment if
   needed; it is not a durable digest or replacement token. Reject incomplete,
   mutated/oversized captures and source/capture/history mixing; validation must
   not repair paths or refresh facts with later IO.
3. A separate configured-owner proof consumes the already validated coordinate
   graph and its construction-time capture; no caller-selected alternative owner.
   Validate project/repository and canonical primary/common/managed identities,
   managed separation from both roots (both ancestry directions), exact target/
   initial snapshot preservation, and exact repository/project/managed/path
   association for EVERY retained workspace version. Derive only expected path
   managedRoot/WorkspaceID and compare exact raw path; no normalization output.
   Preserve original permanent claims, all uint64 business values and Git SHAs.
   Physical adapter generation >MaxInt64 must be refused, never rounded/clamped.
4. Return an owned proof/lookup tied to graph history/capture/full workspace key
   and original configuration. Do not expose mutable capture arrays/pointers or
   mint any cross-trace path token. A configuration proof is not fresh filesystem
   validity, successful registration, branch/index cleanliness, target movement
   evidence, original ACK producer identity or actual historical effect pairing.
5. Positive fixtures create ONLY fresh btrfs t.TempDir Git repositories, bind via
   actual gitrepo.Bind/default Runner and prepare managed root. Publish typed
   workspace bindings via actual WorkspaceAuthorityServiceV2 and coordinator,
   reusing existing claim-retaining fixture infrastructure. No actual portable/
   crown scenario or external provider execution. No Git mutation of the project,
   candidate or existing worktrees. Include legitimate linked-primary/common-outside
   configuration if feasible without speculative authority shortcuts.
6. Controls: absent/portable/nil real owner, declaration/config mismatch, foreign
   project/repository, changed primary/common/managed/target/initial snapshot/
   timestamp/claim, ancestry overlap, caller/capture/history/version mixing,
   old snapshots rather than latest alias, copy mutation, exact derived layout,
   changed Git SHA, unknown business fields and generation overflow. Where no
   owner-valid published mutant can reach this layer, identify the earlier gate;
   do not credit sealing/playback refusal as configured-proof coverage.

Run focused unit/race and scenario vet with Go1.26.7 and
TMPDIR=/home/user/.cache/spktmp. Record source hashes and unique logs. Parent will
inspect changes and run independent checks; separate read-only review is required.
Do not represent this increment as full physical repository graph closure.
