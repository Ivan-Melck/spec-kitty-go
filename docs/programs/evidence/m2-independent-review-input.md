# M2 independent review input

Review exactly one immutable M2 run directory. The manifest supplies the source
commit, tree, RunID, toolchain, module digests, M1 admission chain, census
totals, performance results, and artifact inventory. The reviewer must
recompute them; filenames and this document grant no verdict.

Required reviewer identity is a durable ActorID different from every
implementation contributor ActorID, plus the actual review ProcessID and
profile revision. Record those identities, the manifest digest, source/tree,
commands, findings, and verdict in the mission review report. A new process for
the implementing actor is not independent.

```bash
GOTOOLCHAIN=go1.26.7 make m2-verify-evidence M2_EVIDENCE=/absolute/extracted/run
GOTOOLCHAIN=go1.26.7 make m2-short
GOTOOLCHAIN=go1.26.7 make m2-absence M2_GAPDBD=/absolute/admitted/gapdbd
```

The review must inspect raw Go test JSON, the 15-family registry, all
restore-deadline overlay substitutions and source anchors, autonomous
capability-readiness evidence, the assertion-extension sidecar chain, 100-run
campaigns, raw performance observations, deterministic CycloneDX bytes,
installed binaries, and SQLite-absence layer receipts. Missing, skipped,
substituted, mixed-source, or mixed-RunID evidence is a blocker.

The maximum positive result is **release ready**. Review does not create human
adoption, publish a release, merge the mission, or authorize M3/M4.
