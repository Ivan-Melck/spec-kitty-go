# Phase 3 tested boundaries and non-claims

Phase 3 qualifies the software-development mission method and its Beads
composition. These limits are part of the result, not deferred fine print.

## Tested boundary

- Application host: Linux/amd64 with Go 1.26.5.
- Provider: Beads 1.2.2 at SHA-256
  `0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6`.
- The provider build retains its separately reviewed Go 1.26.4 installer
  identity; that pin is not relabeled as an application-toolchain build.
- Provider server: the qualified disposable Dolt tuple on loopback inside an
  offline Linux network namespace.
- Control authority: SQLite owned by one machine and one filesystem authority.
- Portable scale: 1,000 semantic mutations, 100 two-process finalization races,
  nine declared crash boundaries, 100 projection trials, 100 governance-history
  trials, and 100 pure `next` reads.
- Real-provider scale: 11 frozen provider scenarios, 1,000 multiwriter creates,
  20 assignment-preservation observations, 23 authority audits, and one durable
  two-item Phase 3 mission-method scenario.

## Explicit non-claims

- Phase 4 production execution dispatch, lease/worktree allocation for the new
  mission method, and agent scheduling are not delivered by Phase 3.
- Phase 5 production review/acceptance/integration orchestration for the new
  mission method is not delivered by Phase 3. Existing Phase 1 laws remain
  intact and are exercised for compatibility.
- Phase 6 CLI polish, Orchestrator API serving, MCP, Team Kitty, hosted sync, and
  Python-project migration are not delivered.
- `spk-phase1-helper` is a proving/composition helper, not the eventual end-user
  Spec Kitty CLI.
- No multi-machine or distributed control authority, shared SQLite filesystem,
  network-filesystem authority store, or direct remote database writer was
  qualified.
- Gapdb was not adopted. SQLite remains the qualified control store; any change
  requires equivalent adapter evidence and an explicit signed human decision.
- No production service-level objective follows from local latency samples.
  Hosted duration and performance baselines have zero observations and remain
  `null`/pending, not zero.
- Only the exact Beads version, source identity, binary digest, JSON schema, and
  Dolt tuple recorded here are qualified.
- Tracker status, assignment, comments, dependency state, and readiness never
  become mission authority. They remain mutable provider facts or projections.
- `tasks.md` remains a derived, non-authoritative view. This phase does not make
  Markdown a canonical ticket ledger.

## Fault-model limitations

The campaign has process-realistic postcommit/pre-response loss for artifact
approval, finalization, and successor publication, plus durable-provider
lost-response recovery for decomposition. SQL `BEFORE INSERT` injection proves
transaction abort at reachable internal boundaries. The production ports do not
expose a safe hook for killing a process at an arbitrary instruction in the
middle of SQLite's transaction machinery, so those SQL cases are labeled
transaction aborts rather than process crashes.

Provider saga recovery is not cross-store atomic rollback. An attributed
external mutation can survive a local failure; recovery re-observes provider
revision, value, identity, and operation marker before retry or compensation.
Human-modified provider state is preserved for explicit reconciliation.

## Evidence limitations

Raw JSONL streams, Dolt logs/databases, temporary SQLite databases, environment
state, and provider bodies are not published because they may contain transient
paths or authored work content. Their bounded safe summaries and selected
artifact digests are published instead.

Individual durations for compile, race, vet, staticcheck, module verification,
govulncheck, formatting, and builds were not retained by the campaign schema;
they are represented as `null`, never fabricated as zero. The clean local
PR-equivalent duration is measured, but the hosted ten-sample baseline remains
pending.

Qualification is non-transitive. A later product, authority, provider,
protocol, campaign, or evidence-serializer change requires a new frozen source
candidate and new evidence. Documentation-only commits may attest to an earlier
payload but do not inherit or extend its verdict.

The earlier Phase 3 publication at source `4a96edd` was rejected because its
evidence format had no reusable closed-schema validator. It remains historical
evidence rather than an accepted publication. Candidate attempts 20 and 21 and
provider-containment attempt 23 are likewise retained as failed or
non-admissible; none is upgraded by the later passing attempts.

The next publication at source `b03200f` was also rejected: its validator did
not yet bind the checkout's exact HEAD, reject duplicate or oversized JSON,
fully validate Mermaid bodies and non-Make commands, or make every published
failure identity load-bearing. That cycle remains a failed review record rather
than evidence for a pass.

The first committed-validator qualification at source `ce64e64` failed because
its synthetic payload fixture required a changed commit even when the staged
payload bytes were already identical. Source `f049baaa` repaired that test law;
portable attempt 26 then passed. Real-provider attempts 27–30 remain explicitly
non-admissible setup evidence for namespace identity, Git safe-directory,
installer-variable, and sanitized-environment preconditions. Canonical offline
attempt 31 passed the frozen provider runner and durable two-item selector.

Review cycle 3 nevertheless rejected that publication: deleting setup attempts
27–30 remained possible because the required-ID list had a moving tail;
documented-command validation did not close every subcommand and path shape;
Mermaid subgraphs were not balanced; and the reproducible Make gate entered
through test code rather than the campaign command. Source `03afabd6` closes
those structural laws. Portable attempt 32 passed before a later host restart;
the restart erased its volatile artifacts, so attempt 36 repeated the complete
portable ladder against the same clean commit and tree.

Real-provider attempt 33 stopped before qualification because the outer
container's libc could not load the host bubblewrap binary. Attempt 34's frozen
provider runner passed, but its separate two-item selector skipped after the
digest environment was omitted; the combined attempt is therefore
non-admissible. Attempt 35 was interrupted by the host restart before terminal
evidence survived. Canonical offline attempt 37 corrected the wrapper, passed
11/11 provider scenarios and 1,000/1,000 multiwriter operations, and required
an explicit non-skipped pass from the durable two-item selector.
