# Phase 2 tested boundaries and non-claims

Phase 2 qualifies one tracker adapter composed with the Phase 1 authority
kernel. These limits are part of the result.

## Tested boundary

- Host: Linux/amd64 with Go 1.26.4 and Git 2.43.0.
- Provider: Beads executable 1.2.2 at SHA-256
  `0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6`.
- Provider source tag commit:
  `6c124203e771433a3550c348771a5b5e27fd3c21`.
- Protocol: `BD_JSON_ENVELOPE=1`, schema 1, allowlisted argv only.
- Storage: embedded operation under adapter serialization and shared-server
  operation under the pinned qualified server tuple.
- Control authority: one machine and one filesystem authority using SQLite.
- Real qualification: 11 scenarios, 1,000 concurrent creates, 20 assignment-
  preservation observations, and 23 authority audits.

## Explicit non-claims

- No shared SQLite access, network-filesystem control database, distributed
  control authority, or direct remote writer was qualified.
- Adapter serialization does not fence a human, IDE, shell, or external `bd`
  process. It coordinates Spec Kitty processes only.
- The content-derived observation revision is not ordered and is not
  compare-and-swap. Guarded arbitrary update and safe arbitrary compensation
  are unsupported in Beads 1.2.2.
- The adapter does not guarantee compatibility with Beads 1.2.0, 1.2.1, another
  1.2.2 binary digest, or a future version/schema.
- The published schema-1 real-provider artifact does not retain the randomized
  fixture's `ws1:` identity digest. The linked-worktree scenario proved identity
  convergence during the run, but a digest cannot be reconstructed after fixture
  cleanup and is therefore not invented in `campaign-census.json`.
- The ten-sample hosted timing baseline is pending; zero samples exist.
- No production service-level objective follows from the local latency sample.
- Team Kitty, hosted sync, Orchestrator API serving, MCP, a complete end-user
  CLI, and remote multi-machine agents are not Phase 2 deliveries.
- Phase 2 does not replace the full Spec Kitty mission method or all mission
  artifacts with tracker tickets. It supplies the ticket substrate and preserves
  the authority boundary needed for later mission work.
- GitHub Issues, Jira, and other tracker adapters were not qualified as
  production providers.
- Gapdb was not adopted by this mission. SQLite remains the active control store;
  migration requires equivalent external-adapter evidence and an explicit signed
  human decision.
- JSONL export is interchange and does not preserve Dolt history. Only the
  Dolt-native workflow was tested as a restorable backup.
- Destructive restore against a live project binding, direct Dolt SQL, and live
  database copying were neither implemented nor qualified.

## Evidence limitations

The admissible real-provider run predates the final WP10 evidence-writer
corrections. The final strict runner was separately reviewed and exercised by
portable, adversarial, source-drift, schema, invalid-prerequisite, and focused
quality gates, but was not used to relabel the earlier real-provider bytes. The
evidence index keeps those source identities separate.

Individual durations for build, race, vet, staticcheck, module verification,
vulnerability scanning, and the WP11 documentation gate were not retained by the
WP10 machine artifact. They are recorded as unknown (`null`) rather than
fabricated. The raw test-event streams and provider backups are intentionally not
published because they may contain transient paths or work bodies.
