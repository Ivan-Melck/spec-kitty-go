# Original configured repository foundation

Status: **test-only, independently scoped-reviewed**. This preserves original
configured origin; it does not complete physical repository correspondence,
historical effect/registration/cleanliness proof or oracle integration.

## Narrow change

`terminal_coordinates_test.go` now optionally captures the SAME harness's actual
realScenarioGit original Repository.Binding() after logical closure. It also
handles a typed-nil real driver without dereference. Portable/nil originals still
support logical proof but cannot acquire a configured-origin witness.

New `terminal_repository_foundation_test.go` copies all scalar configuration:
repository/project, primary/common/managed roots, target, original primary HEAD /
index/tree digests and CreatedAt/VerifiedAt. No repository pointer or mutable
adapter maps survive. Private integrity commitments tie it to exact history,
capture and graph, including old workspace versions and original claims. These
are consistency hashes, not durable digest replacements, producer attestation or
cross-trace tokens.

A separate pure proof checks declared primary/managed paths against original
configuration, managed separation from primary/common in BOTH ancestry directions,
and every retained workspace version's exact repository/project/root/derived path,
raw state/digest/key and original claim/revision. Linked primary worktrees may
legitimately have common outside primary. Generations above MaxInt64 refuse at
the physical-adapter domain; uint64 values are never converted/rounded.

Lookup returns owned canonical bytes and immutable claim copies keyed by full
historical key, raw digest and source/history/capture identity. Returned scalar
configuration remains verbatim. No later filesystem/Git lookup repairs a proof.

## Evidence and limits of the controls

Seven repository tests use only fresh btrfs temp Git repositories, actual Bind /
default Runner, managed-root preparation, actual WorkspaceAuthorityServiceV2
bind/rebind, coordinator ACKs and the existing claim-retaining fixture port.
Both ordinary and linked-primary originals pass, with MaxInt64 generation and
old/new workspace snapshots preserved. No physical workspace Create/Observe or
external provider/crown scenario is performed: PathIdentity flags are fixture
claims, not successful effect observations.

Published logical-valid controls reach the configured gate for foreign project /
repository, changed declarations, non-derived layout and generation overflow
(including MaxUint64). Separate scalar/graph mutation and locally resealed ancestry
controls test consistency/separation, not fabricated original provenance.
Copy/lookup/capture mixing controls pass. Deleting only fresh test repositories
and removing later driver references after capture demonstrates no refresh/IO.

Initial unit/race runs failed control-fixture mechanics: sealed operation retained
LastPublicationID in an unaltered prefix, and modified typed payloads weren't
canonical before strict refresh. These are retained FAILED development attempts,
not configured-gate refusal evidence. Correction changes only those fixtures:
refresh original unsealed input before unknown injection; canonicalize known
mutants before resealing. Unknown-field and SHA/generation controls now reach
precise typed PUBLICATION refusals and are explicitly not configured coverage.

## Checks and independent review

Go1.26.7, TMPDIR=/home/user/.cache/spktmp, count1, test timeout120s:

```text
TestTerminal(Coordinate|Execution|BoundExecution|Repository|History|Restart|TypedRecovery|Semantic)
```

Corrected implementer unit/race each: 36 top-level passes /276 results including
subtests, zero skips/failures, package4.509s /41.149s; vet passes. Parent separately
ran full selected unit/race once each: **36 passes, zero skips/failures**, package
4.517s /41.006s; vet passes. All scenario hashes stayed unchanged during checks.

Read-only reviewer `5bd1a36a-9449-476` read all three complete files and source
contracts, compared the actual prior bound coordinates archive, verified hashes
and ran all seven repository tests, zero skips. **No blocking defects attributable
to this narrow increment found.** This is dirty-main source review, not runtime
approval or immutable-source attestation. Existing bound foundation/controls and
unbound controls remain byte-identical.

Evidence under `/home/user/.cache/spec-kitty-parity-resume-followup/`:
- `repository-foundation/run-k0d_jdwu/` (initial failures and correction-round1).
- `repository-parent-check-WNGIgTbz/` (actual bound-baseline delta and fresh checks).
- `repository-review.md`.

The implementer also saved an explicitly tagged OLDER unbound baseline d5ab…;
it is not the immediately prior bound source. Parent/reviewer used actual bound
8c529… from bound-execution-checkpoint/source-and-docs.tar for comparison.
Reviewed SHA256:

```text
8289aeeda6724591577362be68c4a317a2b75f6135e93d4dc6e3c2877c3db503  internal/scenario/terminal_coordinates_test.go
361346094ee995b16374d1c7d471f329ba359e66e7ed33347f66fe6085193bef  internal/scenario/terminal_repository_foundation_test.go
bef8d6ccd288b1e1f559b8a1ff2d9610d092cbe48088088f3131c208d98d43e2  internal/scenario/terminal_repository_controls_test.go
```

## Residual boundaries

Private hashes do not defend arbitrary package-local replacement/resealing.
Lookup does not independently seal each internal claim/revision; review found no
caller-visible mutation route. Empty inventory is configuration-only evidence,
not complete census. Graph framing allocates bounded validated rows before checking
cumulative row budget; no attributable unbounded caller-input path was found.

Original binding is not fresh root validity, branch/index cleanliness, successful
registration or historical physical-effect pairing. No original producer DB,
private admission, physically complete retained census, full other-family graph,
injective cross-trace path tokens or business equivalence is established.
[Physical graph handoff](PHYSICAL_REPOSITORY_GRAPH_HANDOFF.md) still requires
actual original effect observations before pairing. [Conditional global count
route](GLOBAL_CARDINALITY_CENSUS_ALTERNATIVE.md) is reviewed design only. No oracle,
clean candidate, provider image, receipt, mission/admission or architect Op changed.
