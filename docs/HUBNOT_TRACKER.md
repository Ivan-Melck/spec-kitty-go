# Hubnot tracker

Go Kitty's `spec-kitty tracker` commands use the qualified Hubnot public CLI through provider-neutral ports. They work independently of the production mission host. Tracker assignment is not an execution lease; readiness is not permission to run; comments are not review verdicts. This cutover does not adopt M2 or complete M3. The production mission composition retains its unavailable provider/execution seams until separate program admission.

## Build and install

The first qualified target is Linux amd64, Go 1.26.7. Use an explicit compiler and clean accepted source. The compiled installer derives the exact Hubnot source and binary tuple from the same manifest as qualification. It never initializes a workspace, reads a private signing key, or substitutes an executable from PATH.

```bash
export PATH="/absolute/go1.26.7/bin:$PATH"
export GOTOOLCHAIN=local
make hubnot-build HUBNOT_GO=/absolute/go1.26.7/bin/go BUILD_DIR=/absolute/external/build
/absolute/external/build/hubnot-qualification manifest
/absolute/external/build/hubnot-qualification install \
  --go-bin /absolute/go1.26.7/bin/go \
  --source /absolute/clean-accepted-hubnot \
  --destination /absolute/external/build/hn
make hubnot-product HUBNOT_GO=/absolute/go1.26.7/bin/go BUILD_DIR=/absolute/external/build
/absolute/external/build/hubnot-spec-kitty tracker --help
```

Populate module caches before offline build/qualification. `hubnot-build` and `hubnot-product` use the reproducible recipe `-trimpath -buildvcs=false -mod=readonly -ldflags='-s -w'` with CGO disabled, GOAMD64=v1, GOENV/GOWORK off, no inherited GOFLAGS/GOEXPERIMENT, and GOPROXY/GOSUMDB off. Keep build outputs outside the clean checkout. The `hubnot-install` Make target retains successful installer JSON in `ARTIFACT_DIR/hubnot-install.json` and diagnostics in `hubnot-install.stderr.log`; a failed attempt keeps its diagnostics and nonzero exit. There is no shell installer or compiler-basename requirement. An unrelated existing destination, wrong compiler/source or mismatched binary digest refuses.

## Bind an existing workspace

Choose one canonical existing Hubnot workspace independently of execution worktrees. Identity discovery is public and read-only:

```bash
cd /absolute/existing-hubnot-workspace
/absolute/external/build/hn identity show --json > /absolute/external/identity.json
/absolute/external/build/hubnot-qualification manifest > /absolute/external/manifest.json
jq -n --slurpfile identity /absolute/external/identity.json \
  --slurpfile manifest /absolute/external/manifest.json \
  '{schema:"go-kitty.hubnot-binding/1",provider:"hubnot",project_id:"project",workspace_path:"/absolute/existing-hubnot-workspace",executable_path:"/absolute/external/build/hn",expected_actor:$identity[0].data.actor,read_only:false,identity:$manifest[0].provider}' \
  > /absolute/external/binding.json
/absolute/external/build/hubnot-spec-kitty --output json tracker inspect --binding /absolute/external/binding.json
```

Binding and input files must be regular files; stdin may be a pipe. Actor drift or a replaced executable refuses. No inspection initializes, syncs, repairs, rebinds or writes issues. A legacy Beads binding requires an explicit new operator binding; no fallback or dual write exists. The nineteen inventoried old database/backup files and historical branches/stashes remain preserved. Zero current issues does not mean empty backups, and no automatic migration is performed.

## Create, read and retry

Ordinary simple catalog work can have a title and empty criteria. Every request has schema `go-kitty.tracker-request/1`, its exact action and action-specific data. The global `--output json` may precede `tracker` or occur in the existing global-option positions shown by help.

```json
{"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"first","operation_id":"create-first","title":"Review the public API","criteria":[],"metadata_schema":1}}
```

Save that request as `/absolute/external/create.json`, then:

```bash
/absolute/external/build/hubnot-spec-kitty --output json tracker create --binding /absolute/external/binding.json --input /absolute/external/create.json > /absolute/external/created.json
jq '{schema:"go-kitty.tracker-request/1",action:"read",data:{external_ref:.result.record.external_ref}}' /absolute/external/created.json > /absolute/external/read.json
/absolute/external/build/hubnot-spec-kitty --output json tracker read --binding /absolute/external/binding.json --input /absolute/external/read.json
```

Keep the complete returned `external_ref`; do not invent native IDs or convert numeric revisions to event IDs. Reuse the same operation ID and identical semantic request to recover a lost response. A verified historical replay appends nothing even after later human edits. Divergent reuse refuses. Mutation proofs have kind `original-operation/1`; they describe the original effect, not a fresh current precondition. Obtain a fresh `read` or `definition` before supplying `expected_observation` for a new mutation. Revisions are equality-only; never sort them or infer global compare-and-swap.

## Rich work and actions

`define` creates complete work; `definition` reads it. Criteria preserve caller order and fields `id`, `text`, `outcome`, `verifier`, `required`, `evidence_hint`. Rich work also carries `description`, `design_context`, `cross_domain_reason`, `write_scopes` (`path`, `domain`, `access`, `serialization_key`), `provenance` (`id`, `kind`), `dependencies` and `metadata_schema`. Full criteria need valid verifier kinds such as `test`; `write_exclusive` is a write-scope access value and `required_outcome` is a provenance kind.

Opaque provider metadata is an optional JSON-text string, for example `"provider_metadata_json":"{\"number\":9007199254740993,\"label\":\"雪\"}"`. Its object contents are strictly parsed before neutral construction, preserving number lexemes and deep nesting. Omission means `{}`. Empty text, non-object JSON, duplicate keys, malformed Unicode, unknown fields and trailing values refuse before writes.

| Action | Behavior |
|---|---|
| `inspect`, `inventory` | Read the binding/capabilities or complete mapped current definitions with one common proof. |
| `create`, `read`, `update` | Simple catalog; update is explicitly `unguarded`. Generic update cannot bypass complete-definition protection. |
| `define`, `definition` | Complete creation/read. Required dependency edges form a resumable saga; a completed replay never recreates an edge a human later removed. Guarded definition replacement is unsupported. |
| `link`, `unlink` | `blocking_work_id` supplies `blocked_work_id`; `kind` is `blocks`. Preserve unrelated relations and full work fields. |
| `dependencies` | Portable relation values with `proof_scope=portable_relations_only`; no fabricated current proof. |
| `ready` | Open mapped work without open blockers. Closed suppliers remain definition dependencies. Invalid/incomplete graphs retain diagnostics and may have no proof. |
| `assign` | Omitted mode means `project`; use `mode:"observe"` with only the reference/project for attributed observation. Projection is unguarded; atomic claim/conditional compensation refuse. |
| `comment`, `discussion` | Exact body/time and original operation attribution. `author` is the signer's asserted accountable author; `signing_actor` is actual transport provenance, not independent review identity. |

Successful results use schema `go-kitty.tracker-result/1`, action and typed outcome. Inventory/discussion serialize the common proof once; entries reference that proof's digest/domain. Known-work proof includes current signed heads, relevant graph and comments; inventory proof is a distinct complete-current-definition observation. Do not substitute opening creator for current edge signer.

## Limits and recovery

Native encoded mutations must fit 256 KiB and all field/key capacities; foreign metadata consumes that capacity. Child bounds are 8 MiB stdout, 64 KiB stderr and 30 seconds. A coherent read shares 64 MiB, 10,000 items and 120 seconds across all pages and at most two restarts; detail pages contain eight rows. Refusal preserves data rather than truncating it. CLI request input is capped at 4 MiB and serialized results at 256 MiB plus framing.

Exit codes: 0 success, 2 invalid input, 3 refusal, 4 unavailable, 5 ambiguous, 6 conflict, 7 not found, 130 canceled. Use diagnostic recovery instructions and the outer operation facts: `applied_this_call` and `prior_mutation` can survive an error. Do not interpret every error as no write. Observe or replay the original operation before retrying; never blindly undo a human edit. A canceled writable diagnostic has at most 250 ms for delivery; interrupted blocked output exits without trying another write on the same saturated stream.

Multiple heads/assignees, duplicate mappings, missing suppliers and cycles require native reconciliation. Public selected-history synchronization can reconstruct work in disposable replicas; portable export/restorable backup capabilities remain unsupported. Preserving or recovering an original signer is a separate operator responsibility.

## Qualification and history

```bash
make hubnot-contract ARTIFACT_DIR=/absolute/external/offline-evidence
make hubnot-hosted-provider HUBNOT_GO=/absolute/go1.26.7/bin/go BUILD_DIR=/absolute/external/build ARTIFACT_DIR=/absolute/external/hosted-evidence
make hubnot-qualification HUBNOT_GO=/absolute/go1.26.7/bin/go BUILD_DIR=/absolute/external/build HUBNOT_ORIGINAL_REPOSITORY=/absolute/original/go-kitty3 ARTIFACT_DIR=/absolute/external/full-evidence
```

The offline census contains two durable fake portable tests and 32 fixed Hubnot adversarial tests with no hn requirement. Real wrappers require the exact built qualifier/product/provider and a new evidence directory. Hosted selection is exactly 27 of 28 scenarios, excluding only actual local recovery; it is explicitly non-full. Full local qualification requires all 28, the original nineteen files/refs and all frozen source evidence. Inner qualifier timeout is 55 minutes, outer campaign 58 minutes. Setup is excluded from inventory timing; final integrated performance/admission belongs to its exact shipped source.

The hosted workflow name changes from `beads-qualification` to `hubnot-qualification`; required-check configuration is an external repository setting and local evidence does not prove that transition. Historical Phase 2/3 receipts, tuples, censuses and checksums remain unchanged: [Phase 2](phase2-evidence/README.md), [Phase 3](phase3-evidence/README.md). Validate their bytes with `(cd docs/phase2-evidence && sha256sum --check SHA256SUMS)` and the analogous Phase 3 command. Historical syntax validators require baseline Git objects; use a full-history checkout. An unavailable baseline is a prerequisite failure, never a skip or fallback to current documentation.
