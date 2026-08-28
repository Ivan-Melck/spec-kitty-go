# Phase 3 evidence index

This directory publishes the source-bound qualification record for the
software-development mission method. It separates the tested implementation,
immutable payload, documentation attestation, failed attempts, and explicit
non-claims.

## Immutable payload

| File | Purpose |
|---|---|
| [`qualification-evidence.json`](qualification-evidence.json) | Bounded machine summary of source, host, profiles, scale proofs, provider evidence, gates, and privacy |
| [`campaign-census.json`](campaign-census.json) | Exact profile/test census, artifact digests, and failed/non-admissible attempt ledger |
| [`results.md`](results.md) | FR/NFR/constraint/success-criterion mapping to executable proof |
| [`limitations.md`](limitations.md) | Tested boundary and explicit non-claims |
| [`SHA256SUMS`](SHA256SUMS) | SHA-256 manifest for the four immutable payload files |

The immutable payload commit is:

```text
commit: f0374a99df9b122b10b703f840eb6dd4977c7503
tree:   92100ef7de7cc5f2b8466f68a90187c4a0e52c69
```

This index and the repository README are a following documentation-only
attestation. They sit outside `SHA256SUMS`, so the payload never has to predict
the commit containing itself.

Verify the payload:

```bash
(cd docs/phase3-evidence && sha256sum --check SHA256SUMS)
git show --stat f0374a99df9b122b10b703f840eb6dd4977c7503
```

Verify the exact clean docs-only attestation checkout through the reusable gate.
Set `ATTESTATION_COMMIT` to the reviewed 40-hex attestation identity before
running the command; the gate refuses when it differs from the checkout's HEAD:

```bash
make phase3-publication \
  PAYLOAD_COMMIT=f0374a99df9b122b10b703f840eb6dd4977c7503 \
  ATTESTATION_COMMIT="$ATTESTATION_COMMIT"
```

## Provenance layers

```mermaid
flowchart LR
    Source["qualified source<br/>f049baa / 7ef44e9"]
    Portable["portable attempt 26<br/>full campaign pass"]
    Provider["real attempt 31<br/>11 of 11 pass"]
    Mission["real attempt 31<br/>two-item mission pass"]
    Payload["payload<br/>f0374a9 / 92100ef"]
    Attest["docs-only attestation<br/>index and repository README"]

    Source --> Portable
    Source --> Provider
    Source --> Mission
    Portable --> Payload
    Provider --> Payload
    Mission --> Payload
    Payload --> Attest
```

The tested source is commit
`f049baaa8751e0e2b48126ffe05823400f9f4881`, tree
`7ef44e98b332628f0688d274fd4b07eaddc0a2d5`. Attempt 26 ran the full portable
ladder against that clean identity. Attempt 31 ran both the frozen
eleven-scenario Beads qualification and the independently selected durable
two-item Phase 3 Beads mission inside the qualified offline containment. All
three evidence contracts passed.

A later commit does not inherit those verdicts. Any product, authority,
provider, protocol, campaign, or evidence-serializer change requires another
frozen source candidate and another qualification record.

## What was proved

- 1,000 deterministic semantic mutations produced 1,000 structured drift
  results and zero silent current-generation changes.
- 100 canonical normalization repetitions produced one byte representation and
  digest.
- 100 two-process finalization races produced exactly one current generation
  with restart-safe replay/conflict losers.
- Nine declared transaction, saga, and postcommit/pre-response boundaries were
  reached and recovered without partial aggregates.
- 100 projection attacks, 100 governance-history reconstructions, and 100 pure
  `next` reads produced zero unauthorized writes or historical reinterpretation.
- The historical 73-test scenario census, architecture/scope mutants, race,
  vet, staticcheck, module, vulnerability, formatting, and build gates passed.
- The exact Beads 1.2.2 tuple passed 11/11 frozen scenarios, 1,000/1,000
  provider-native contention operations, and the durable two-item mission path.

The complete numerical and test-identity record is in
`qualification-evidence.json` and `campaign-census.json`.

## Failure history is part of the proof

Attempts 01–09 were not erased: the ledger retains toolchain/prerequisite
failures, exact-census failures, load-sensitive claimant observations, and the
stale-authority ordering defect. Containment attempts 11–16 and 27–30 remain
non-admissible or diagnostic; attempt 17 is explicitly diagnostic rather than
canonical evidence. The rejected cycle-1 publication and attempts 20, 21, and
23 are also retained with their original failed or non-admissible verdicts. The
rejected cycle-2 publication and failed attempt 25 remain explicit too. Only
attempts 26 and 31 support this publication.

Raw test streams, provider databases, Dolt logs, temporary SQLite files,
environment state, and authored provider bodies remain outside the repository.
The payload includes only bounded safe summaries and selected artifact digests.

## Reproduce portable evidence

After modules are available, these commands run without Beads or network:

```bash
artifact_dir="$(mktemp -d /tmp/go-kitty3-phase3.XXXXXX)"
make mission-contract ARTIFACT_DIR="$artifact_dir"
make mission-acceptance ARTIFACT_DIR="$artifact_dir"
make mission-adversarial ARTIFACT_DIR="$artifact_dir"
make full-campaign ARTIFACT_DIR="$artifact_dir"
```

For the PR-equivalent bounded path:

```bash
artifact_dir="$(mktemp -d /tmp/go-kitty3-quality.XXXXXX)"
make quality ARTIFACT_DIR="$artifact_dir"
```

Real-provider reproduction requires the exact reviewed Beads installer identity,
binary digest, Dolt tuple, and offline namespace described by
[`../../.github/workflows/beads-qualification.yml`](../../.github/workflows/beads-qualification.yml).
Missing any prerequisite must remain `non_admissible`.

## Publication privacy

The payload contains no absolute home path, credential, environment dump,
private transcript, raw provider database, unrestricted stderr, or raw test
stream. Missing durations and hosted measurements are `null` or pending rather
than rewritten as zero.
