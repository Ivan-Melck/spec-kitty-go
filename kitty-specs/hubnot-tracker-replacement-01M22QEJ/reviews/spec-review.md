# Independent pre-plan review — Hubnot Tracker Replacement

**Verdict: PASS WITH NOTES. No blocking specification defect; proceed to planning.**

Reviewed `spec.md` and `intent.md` in `/home/lynn/projects/go-kitty3/kitty-specs/hubnot-tracker-replacement-01M22QEJ`, the ten obligations in `work/go-kitty-hubnot-risk-recon.md`, upstream prerequisite specification, cutover reconnaissance and inventory manifests. No source/runtime mutations or test runs.

The actual specification contains **25 requirements: 15 FR, 5 NFR and 5 constraints**. It makes the important capability tradeoff explicit: useful creation/catalog/discussion/graph/assignment remains available, while complete-definition guarded update, atomic assignment and conditional compensation are unsupported. This preserves existing portable meanings rather than overstating Hubnot's actor CAS.

## Risk coverage

| Prior obligation | Draft coverage | Assessment |
|---|---|---|
| P01 Signer/accountable actor | FR-001/007; C-001/003; Story 3 and actor edge cases | Sufficient. Metadata is not host identity, and same actor with two keys remains a self-review test. |
| P02 Honest concurrency | FR-003/004/006; C-002; successful and unsupported Story 1 paths | Sufficient. No disguised read/write CAS; create-only decomposition stays useful. |
| P03 Complete bounded codec | FR-002/004; NFR-001; representability disposition | Sufficient. Arbitrary opaque JSON means arbitrary JSON values within the documented supported subset, not accepting every neutral 1 MiB value. Plan must make the subset measurable. |
| P04 Complete coherent observation | FR-009; NFR-001/002 | Sufficient. Full digest/provider token retained; numeric revision remains opaque; missing pages cannot become readiness. |
| P05 Original-request recovery | FR-008; Story 2; SC-004; prerequisite operation lookup | Sufficient. Explicit test for changed portable expected observation prevents silently replaying a different request. |
| P06 Non-destructive compensation | FR-006; C-002 | Sufficient by truthful unsupported refusal, with useful unguarded assignment still required. |
| P07 Graph/WorkID mapping | FR-002/004/005; graph saga and identity edge cases | Sufficient. Stable internal IDs, actual graph facts, direction and saga recovery are explicit. |
| P08 Restore/sync/restart/playback | FR-012; C-001/003; Story 3/4 | Sufficient. Public reconstruction is distinct from full backup/private signer recovery, capabilities stay unsupported until qualified. |
| P09 Existing data/historical identities | FR-011/015; C-004/005 | Sufficient. Empty current catalog does not imply empty historical backups or authorize old-operation rebinding. |
| P10 Tuple/public boundary | FR-001/010/013/014; NFR-001/003; C-003 | Sufficient. Real compiled consumer and exact source/build evidence required; no private Hubnot import or binding repair side effects. |

## Concrete nonblocking amendments

These clarify acceptance and should be incorporated before task slicing; they do not require a broader mission or new capability.

1. **Explicit cutover activation boundary.** FR-011's active installation/reachability cutover and FR-015's preservation are compatible, but state clearly that implementing the new default does not initialize or repoint the user's existing real repository.

   Proposed sentence in Assumptions and Disposition:

   > “Qualification uses disposable provider workspaces; this mission does not initialize Hubnot in the real project, select or publish real remote histories, or alter an existing user's provider binding. A legacy binding receives an actionable refusal and an explicit operator binding path.”

2. **No-write accounting for distributed races.** NFR-004 already says “zero unexpected” writes, but acceptance must distinguish a preflight-refused request from a valid local append whose conflict becomes visible afterward. An absolute zero-write assertion for every post-write ambiguity would contradict the declared unguarded behavior.

   Proposed addition to NFR-004:

   > “Pre-dispatch refusals publish zero adapter facts. A valid append followed by a discovered distributed race or lost response is retained and reported as applied or ambiguous; recovery never deletes it or claims the request was unstarted.”

3. **Precise performance fixture and measurement scope.** NFR-002's 120-second ceiling is measurable, but “complete fixture observation” could mean inventory or 1,000 independent full histories. Fix the meaning in the plan before tests are authored.

   Proposed replacement for its final sentence:

   > “The timed inventory pass through the compiled consumer must recover all 1,000 complete mapped current definitions within 120 seconds on the recorded qualification host. Separately measure one known-ref observation and one lost-response recovery against that fixture. Record fixture field/history counts, page size, build/race mode, host/toolchain, command counts and elapsed time; setup is outside the timed interval.”

   The representative fixture must exercise actual adapter metadata and at least some discussion/graph state; do not qualify a title-only provider list as complete-definition inventory. This does not impose a 1,000-times-full-history contract.

4. **Make bounds compose.** The 8 MiB per-child output cap and 64 MiB logical observation cap are sensible. A 50/200-record detailed page can exceed the child cap for individually valid large issue states. Specify a conservative page policy, visible resource refusal and a total deadline spanning retries. Do not silently reduce fields or loop indefinitely on stale pages.

   Proposed addition to NFR-001:

   > “The 120-second logical deadline includes all child calls and at most two restarts after the initial attempt. Page sizing and aggregate accounting must respect both per-child and logical limits; a representable record is never silently omitted to fit a page.”

## Evidence and scope checks

- Independently compared `source-before.json` with `source-after.json`: identical manifests for **19 files**. `export.jsonl` is zero bytes, and export stderr is empty. The reconnaissance records isolated pinned `bd where`, zero-record export and `bd list --all` returning an empty data array. This supports the current-catalog disposition; it does not classify backup contents. The report properly avoids that inference.
- The spec preserves M2's adoption boundary and leaves unavailable mission composition unavailable. FR-010 is a tracker-only native consumer, not a stealth API 1.3 restoration or claim to completed M3.
- The upstream mission's specified public identity JSON, mutation actor pin, actor/key lookup including original semantics/timestamp, and detailed bounded listing cover the identified public-interface gaps. Those are **pending prerequisite contracts**, not passing implementation evidence. Go qualification must wait for its independent approval and bind the exact final source/binary as SC-005 requires.
- NFR-005 is a narrow, evidence-based repair to the current SQLite-absence scanner, not a provider exemption. Its required positive/negative tests and C-004 historical classification keep that scope bounded.
- `occurrence_map.yaml` must distinguish active consumer/CI/installer code from frozen test/evidence identities. The explicit historical-integrity constraint is strong enough; no blanket textual Beads replacement is authorized by this spec.

## Planning obligations

Lock the codec's canonical JSON representation and capacity table; comment body/author/transport envelope and provenance rules; operation-key derivation and comparison of portable expected observations; full observation digest/token encoding; graph child-operation order and PriorMutation; explicit capability/error mappings; and source-pinned installer/qualification census. Preserve positive useful workflows alongside every refusal test. These are implementation choices already bounded by the spec, not missing authorization or reasons to stop planning.
