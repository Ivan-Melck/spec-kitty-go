# Expiry-image Host qualification candidate — NOT an implementation handoff

Status: **unreviewed design candidate; no Host launch/admission/bridge edits**.
The already-approved separate image and reporting decisions are not reopened.
[Original investigation](EXPIRY_HOST_BINDING_INVESTIGATION.md) remains authoritative
about unresolved boundaries. Exact technical design and independent review must
precede implementation; this candidate does not supply them by itself.

A read-only planning agent inspected lifecycle/provenance/bootstrap/proof sources.
It could not write cache artifacts under its mandatory read-only policy. Parent
preserved its actual output (not an invented agent-authored file), source-coordinate
custody and inventory counts in:
`/home/user/.cache/spec-kitty-parity-resume-followup/private-json-parent-l5v7lctg/host-plan-agent.output.txt`
and `host-plan-parent-custody.json`. No tests/builds/providers ran for this design.
The report's printed proposed cache directory was NOT created. Its partial read
ranges and two reported source hashes are not a complete independent review.

## Proposed direction retained for synthesis

- Compile-time `spk_expiry_host_qualification && linux && amd64` only. Dependency
  `_test.go` helpers cannot bridge packages. Normal roots/receipts/constants and
  ordinary-build authority must remain unchanged.
- Separate strictly typed immutable technical record: authenticated base module
  ZIP/h1 and400-file inventory; actual403-file modified inventory; exactly one
  changed and three added files; delta blobs; real client identity separate from
  modified provider source. Parent confirms400/403 and1/3 counts in existing
  build.json, not a new admission decision. No fictional modified Git-tree claim.
- Exact TEST SHA83d5db39… (5491681 bytes), control59508c18… (5426833 bytes),
  build/toolchain and already scoped provider review/evidence. Non-adopted and
  non-release states. Existing build.json remains evidence, not admission.
- Original verified/sealed lifecycle, fixed gateFD3 and executableFD4, executing
  `/proc/self/fd/4`; no caller-selected environment/ExtraFiles/FD/verifier factory.
  Gate endpoint ownership and rollback/release states need exact implementation.
- Candidate sidecar functions: tagged `StartExpiryQualification(ctx, LifecycleConfig)`
  returning lifecycle and an opaque gate with WaitHeld/Release/Close; tagged
  unexported productionruntime root returning Host and opaque run. These are
  proposals, NOT approved API additions. A normal public provider/count/selector/
  callback surface is still forbidden.
- Accurate fresh qualification binding and explicit recovery selection independent
  of the existing M2 boolean. Actual Host ACKs, final overwrites and independently
  visited private chain. TTL-extra write and ordinary projection refresh BEFORE
  observation, stable held revision, authenticated S1-specific refusal, release
  acknowledgment and resumed expiry. No concurrent snapshot/admin writer.
- A proposed tagged negative-finalization seam must validate S1 coordinates AND
  current lifetime/composition/projection, consume terminal hash exactly once and
  refuse cancellation/concurrent/copied/repeated use. A historical mismatch bit
  alone is not such a witness. No raw count, key or diagnostic escapes.

## Parent synthesis concerns — unresolved, not silently authorized

1. **Shared code/normal absence:** “extract shared bodies” does not specify exact
   ExecutableAdmission/VerifiedExecutable/source-provenance variants, common validation dispatch or
   tag/no-tag definitions. Ordinary source must not accidentally accept modified
   source under old Git facts or acquire a generic verifier/environment/FD path.
   Complete file/signature/state diff and normal-build absence proof are missing.
2. **Fingerprint semantics:** receipt digest and typed technical tuple digest
   are not automatically interchangeable. Define the new provenance relation and
   binding preimage without relabeling an existing tuple fingerprint. Historical
   fields/constants/records cannot be globally retargeted.
3. **Avoid receipt self-reference:** a prelaunch pinned input record cannot include
   future Host-result digests, or a source hash of the file embedding its own
   digest. Separate provider technical input/provenance from later Host execution
   qualification; bind them with a directed, acyclic receipt chain.
4. **Fresh-only is not full restart coverage:** new isolated first initialization
   avoids retrofitting admitted databases, but forbidding qualification attach/
   restart altogether would leave genuine predecessor-chain/stale-restart controls
   unqualified. Define either exact same-technical-binding restart ownership or an
   explicitly incomplete matrix. Do not narrow selected-layer acceptance silently.
5. **Failure finalization:** inspect exact Finish/Abort/projection sequencing before
   choosing the tagged witness method. Terminal hashing cannot be skipped, doubled
   or credited after revocation, nor may failed ordinary calls become success.
   Proposed exported tagged methods require specific boundary review first.
6. **Recovery and freshness:** Initialize alone is not evidence of a pristine owned
   database. Prove exclusivity/freshness before writing a binding, and explicitly
   select/verify the new provider's recovery contract rather than reusing a boolean.

Parent inspection additionally confirms `VerifyExecutable` compares the capability
with `(ExecutableAdmission{})`, so new capability storage must preserve comparable
value semantics. `ExecutableProvenance` currently contains flat SourceRevision/
SourceTree/ModuleVersion strings, not a reviewed delta variant. The normal reader
uses64KiB before strict old-record parsing. Compact JSON containing JUST the actual
400/403 inventories is95401 bytes, already above65536. An inline inventory cannot
simply reuse that parser; define separately authenticated fixed artifacts or an
exact tag-only record path without increasing the normal limit. These measured
encoding/source constraints are in `host-design-parent-constraints.json`; they
are not a new receipt format/admission. Parent read windows are disclosed there;
the planner's lifecycle1..640 window omits later portions of the1678-line file,
so remaining attach/stop/cleanup ownership still requires complete examination.

One NARROW independently approved design slice is now separately implemented:
[tagged S1/S2/terminal closure](RETAINED_EXPIRY_CLOSURE_SLICE.md). V1 was rejected
for completed post-terminal Revoke still earning consumption; V2 added a fresh
fixed E-only pidfd guard and was approved BEFORE code, then8 actual files approved.
This settles adapter terminal-consumption infrastructure, NOT the full bridge,
launch/provenance/Host root/recovery/binding design. The full draft must adopt the
new guard and separate lifetime-scalar/observation-CAS cutoffs and outer-context
closure. It cannot use its older no-postterminal-liveness pseudocode. No tagged
Host witness exists; native ordinary roots remain unarmed/unchanged authority.

A subsequent cache-only draft, `expiry-host-exact-design-xlv8pfj_/DESIGN.md`,
completely reads47 files and specifies detached inventories, truthful modified-
source identity, acyclic receipt/tuple fingerprints, FD3/FD4 ownership, tagged
binding/root and S1-negative terminal continuation. It is STILL NOT an approved
implementation handoff. Parent `observation-closure-parent-gm0zjqbi/HOST_DESIGN_PARENT_FOLLOWUP.md`
fully inspects8 capability files/recovery: workspace's nested Restore ALREADY uses
a ReaderV2-only wrapper; other inspected ports and scheduler do not Restore.
This is candidate resolution of the draft's U1, not independently reviewed global
recovery closure. Post-Consume internal-context cancellation, exact binding schema,
normal empty-field padding/tagged memory-profile changes and explicit unsupported
Attach/cross-process scope must also be resolved before implementation review.
No source specimens become admission receipts, and no Host expiry code was added.

A complete synthesis must include exact record schema/byte encoding/digests,
source admission and execution ownership types, concrete tagged signatures,
ordinary-build absence, attach/restart limits and a rejection/cleanup matrix.
No generic admission factory, receipt transfer, current-source promotion or new
Host qualification is implied. Complete memory/negative/P95 work remains open.
