---
affected_files: []
cycle_number: 3
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T11:20:42Z'
reviewer_agent: user
wp_id: WP10
---

# WP10 Review Feedback — Cycle 3

**Verdict**: Rejected

Repair `81022d4` closes mandatory provenance and provider-derived drift, and its
focused/repeated/race/static gates pass. Three acceptance gaps remain.

## Blocker

1. **Pre-open validation must cover every Phase 3 command/query.** A real
   malformed `artifact publish` request with an empty operation ID returned
   `invalid_identity` but first created a 679,936-byte control database and
   token key. The same structural exposure remains in mission initialize, work
   drift, mission next, and projection tasks. Implement one complete transport
   preflight per command before SQLite/fake open, provider call, or output write.
   Add a command × locally detectable field table across all ten commands,
   proving DB, token-key, tracker, and output paths remain absent for malformed
   IDs, enums, timestamps, path-pair shapes, mappings, and provider-reference
   cardinalities.

## High findings

2. **Prove tracker status and assignment non-authority.** The scenario currently
   mutates only assignment, and the durable fake has no status seam. Add the
   smallest governed fake status observation/mutation field needed for a real
   provider-state test. Mutating both status and assignment must preserve exact
   mission state/version, current WorkRevision, and `next`. Do not simulate
   status with an ignored JSON field.

3. **Observed/expected refusal evidence must be contractual.** Domain,
   artifact, provider, workgraph, and projection refusals still omit safe state
   evidence, and the negative matrix requires it for only 3/10 cases. Define
   bounded state extraction at every handler boundary and require it
   case-by-case whenever safe, including terminal lifecycle, stale approval,
   incomplete coverage, bad dependency, provider ambiguity, incomplete
   reconciliation, and unsafe output. Preserve redaction/leak controls.

## Passing evidence to preserve

- Original findings for mandatory creation provenance and provider-derived
  drift semantics are closed.
- Public fresh-reader summary and omission mutants pass.
- SQLite v1–v13 remains byte-identical; v14 is the only append.
- Exact governed scope, focused x20, scenario/helper repetitions, race,
  architecture, compilation, vet, staticcheck, build, module, formatting, and
  diff gates pass.

Repair the class rather than the currently named examples, and do not weaken
any passing control.
