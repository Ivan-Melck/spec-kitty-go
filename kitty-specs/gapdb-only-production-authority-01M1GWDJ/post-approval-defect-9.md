# Post-approval defect 9: atomic workspace binding authority

## Finding

WP08 exposed two false assumptions in the ratified workspace contract.

First, a condition attached only to a record being mutated cannot atomically
prove a genuinely unchanged authority aggregate. The original workspace
binding design needed to verify an unchanged execution head without
manufacturing a same-value write or splitting the publication across a
read/commit race. The later execution-slot-binding correction intentionally
changes that head so it records WorkspaceID authority; initial bind therefore
uses an exact record-revision mutation condition on the execution head. A key
cannot be both asserted and mutated in one publication. True read-only batch
assertions remain required for every authority key that must stay unchanged.

Second, GapDB can fence Spec Kitty authority records but cannot fence arbitrary
POSIX or Git writes. A stale process may continue writing its old worktree, and
a human or uncooperative process may move a repository ref without passing
through the control store.

The GapDB `atomic-batch-assertions-01M1P4VH` mission now supplies the missing
public primitive at source commit
`4d88094749e0f6fdb38a4786708d920fb51966a1`: meaningful read-only absent or
record-revision assertions evaluated in the same serialized pre-batch view as
all mutations. Its complete upstream mission evidence passed functional,
crash/recovery, protocol, race, static, vulnerability, bounds, contention, and
performance qualification. Go Kitty still requires its own exact dependency
upgrade evidence and human adoption decision before the new sidecar tuple is
production-admitted.

## Ratified correction

### Provider-neutral authority assertions

Authority Journal v2 gains a closed, immutable assertion member distinct from
record mutation conditions. Assertions may target only an aggregate head or an
eligibility guard. Each assertion binds:

- the semantic target;
- `absent` or an exact positive provider record revision;
- the expected logical version when the target is present; and
- a digest of the canonical value observed at that revision.

The complete assertion set is sorted, duplicate-free, included in the plan
preimage and canonical sealed plan, and returned defensively. Assertions do not
become sealed records, witness record-set members, journal events, mutations,
revisions, expiry work, or watch events. Assertion plus mutation count and raw
and wire bytes are checked against the frozen product budgets before a provider
call. A record mutation condition remains required for every mutated head or
guard; an assertion must not overlap any mutated key.

The GapDB adapter is the only layer that translates these values into the
provider's public `Batch.Assertions`. It requires an exact result assertion
count as well as the existing durable mutation acknowledgement. Ambiguity
reconciliation proves only written publication membership; it then re-observes
every assertion target and returns a committed result only when the target is
still byte/revision-identical to the plan evidence. A changed assertion after
an acknowledged response was lost is conservatively ambiguous, never guessed.

### Permanent workspace-path claims

The first workspace publication includes one immutable, non-expiring,
absent-conditioned workspace-path claim. Its key is derived from a domain-
separated digest of the repository/managed-root authority namespace and the
canonical absolute path. Its canonical value binds that claim digest to the
WorkspaceID and immutable workspace coordinates. It is part of the sealed
record set, witness, mutation count, byte budgets, and ambiguity proof.

The global v2 provider spelling is
`spk/v2/workspace-path-claims/<64-lowercase-claim-digest>`. WP02 exposes only
the sealed semantic claim digest and record role. WP03 owns the closed codec
kind/accessor/parser and exact provider translation; callers cannot supply a
raw provider key or alternate spelling.

A claim is never transferred, deleted, expired, or reused. Replay of the same
publication reconciles by exact membership. A replacement lease must select a
different WorkspaceID and a path whose claim key is absent. This rule prevents
two cooperating Spec Kitty operations from authorizing the same directory; it
does not pretend to control direct filesystem access.

### Execution-owned composition

`internal/execution` owns the operation that composes execution and workspace
authority because execution already depends on workspace. The workspace
package supplies opaque, validated binding/quarantine contributions; it does
not import execution or expose execution wire formats.

Initial binding advances the current execution head under its exact prior
record revision and atomically writes the workspace binding, first workspace
event/head, permanent path claim, operation state, and preparation intent. It
must not also assert that mutated key. If binding wins first, expiry re-observes
the bound workspace and atomically expires the lease plus quarantines that
workspace. If expiry wins first, binding's execution-head mutation condition
fails. Once an execution slot records a bound WorkspaceID, expiry without the
exact workspace quarantine contribution is invalid.

The slot record therefore carries a paired, immutable WorkspaceID and
workspace-path-claim digest for its current lease generation. Initial binding
atomically advances the execution head while creating the workspace authority;
an unbound-to-bound transition cannot be inferred from workspace state alone.
Heartbeat and outcome preserve the pair byte-for-byte. The ordinary execution
expiry operation refuses a bound slot before publication. Only the
execution-owned workspace composition may consume a bound slot into an expired
slot, and that same sealed publication must quarantine the exact WorkspaceID.
A replacement generation begins unbound and must later bind a fresh identity
and absent path claim. WP08 narrowly owns `internal/execution/authority_v2.go`
and its test file in addition to its existing composition files so this law is
enforced at the otherwise-bypassing entry point.

### Repository observations are external evidence

Repository target state is never encoded as a fictional GapDB-fenced fact.
For a cooperating operation, the Git adapter holds the repository target guard
across a pre-observation, the GapDB authority publication, and a post-
observation. Both observations are recorded. Any mismatch, uncertainty, or
guard loss withholds ready/quarantine success. Handoff, review, acceptance, and
integration revalidate the target before relying on workspace evidence.

An uncooperative raw Git writer can still move a ref. The product law is prompt
detection and authority refusal, not impossible filesystem fencing.

## Routing

1. Complete the already-open WP02 effect-resume repair and extend WP02 with the
   provider-neutral assertion and path-claim sealed-plan contracts.
2. Reopen WP03 after WP02 is accepted. Upgrade to the released GapDB revision,
   map assertions and path claims through the public API, require exact result
   counts, and run Go Kitty's own external-sidecar compatibility campaign.
3. Record a new exact extension-adoption tuple and obtain the human adoption
   decision. Preserve the original M1 record as historical evidence.
4. Resume WP08. Add narrowly owned execution composition files, retain
   workspace-owned contributions, and prove both bind/expire race orderings and
   the real stale-writer scenario.
5. WP17 binds the final source, dependency tuple, external evidence, and mutant
   campaign into the frozen qualification.

## Acceptance evidence

- A controlled same-value-write substitute fails wherever a true
  unchanged-authority assertion is required; initial bind instead proves its
  intentionally changed execution head with one exact revision-conditioned
  mutation and no overlapping assertion.
- Bind-versus-expire contention has one legal outcome per trial with no split
  execution/workspace authority.
- Permanent path claims reject same-path aliases and replacement reuse.
- Assertion result-count, wire-shape, response-loss, stale-observation, and
  external-sidecar tests pass against the exact newly admitted binary.
- A stale process can mutate W1 directly but cannot make W1 current or affect
  W2, handoff, review, acceptance, or integration authority.
- Kernel packages remain unable to import GapDB, SQL, Git implementations, or
  filesystem implementations.
