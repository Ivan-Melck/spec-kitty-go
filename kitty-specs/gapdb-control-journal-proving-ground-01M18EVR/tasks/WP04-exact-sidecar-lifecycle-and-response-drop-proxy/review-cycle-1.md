---
affected_files: []
cycle_number: 1
mission_slug: gapdb-control-journal-proving-ground-01M18EVR
reproduction_command:
reviewed_at: '2026-08-30T14:12:03Z'
reviewer_agent: renata-wp04-cycle1
wp_id: WP04
---

# WP04 review cycle 1 — changes requested

Reviewer: `renata-wp04-cycle1`  
Implementation: `208f4d6`  
Handoff: `e5120c2`

## Blocking findings

1. **Executed bytes can change after the final verification.** `VerifiedExecutable.command` hashes the held descriptor and then returns an `exec.Cmd` (`provenance.go:269-287`). The admitted copy is owner-writable, so the same inode can be modified after that hash and before `Cmd.Start`. A temporary reviewer probe changed the last byte through the admitted pathname after `command()` returned; the SHA-256 then differed from provenance, but `command.Start()` still succeeded. Holding the descriptor fences pathname replacement, not concurrent writes to the inode. T018 and the completion criterion require the verified bytes and executed bytes to be inseparable. Close this write race and add a behavioral negative control that fails when the protection is removed.

2. **Status/Health/DescribeConfig are not attributed to the connections that produced them.** `observeComposite` and `observeAttachedComposite` check `SO_PEERCRED` on one throwaway `peerProcess` connection, then use the public client (`lifecycle.go:454-484`, `578-615`). GapDB's public client opens a new Unix connection for `Dial` and for each Status, Health, DescribeConfig, and closing Status call. None of those successful response connections is peer-checked or bound to the observed socket inode. A socket replacement can therefore move the checked path to a lookalike between calls while forged overlapping fields satisfy `validateComposite`. T020 requires the three surfaces and strict envelopes to describe the same owned instance on the successful configured connection. Pin and revalidate the endpoint identity and exact daemon process for every evidence-bearing connection; add a real replacement/lookalike test, not only a pure `validateComposite` fixture.

3. **Cleanup follows and deletes a foreign symlink replacement.** `cleanupRuntime` uses `os.Open(runtimePath)` (`lifecycle.go:1043`) and therefore follows a symlink before comparing the target inode. A reviewer probe renamed the owned runtime directory, placed a symlink at the recorded runtime path pointing to the renamed directory, and called `Stop`; cleanup returned success and removed the foreign symlink. T022 and NFR-014 require fail-closed, attribution-safe cleanup. All cleanup traversal and deletion must stay descriptor-relative/no-follow and must refuse a replaced directory entry. Preserve the behavioral regression test.

4. **GJ-02 can claim durable acceptance for a response that does not describe the forwarded request.** `decodeAcceptedMutationResponse` checks only that `MutationCount` is positive and within a global maximum (`proxy.go:368-388`). A reviewer probe sent a two-mutation request with a strict durable response claiming `mutation_count: 1`; the decoder accepted it. It also accepts any nonempty database ID rather than the admitted lifecycle's expected database ID. Require exact request/result cardinality and bind the response to the admitted database before recording `after_durable_acceptance_before_client_response`.

5. **The declared storage topology fails open.** `normalizeLifecycleConfig` and `validatePrivateRoot` enforce absolute paths, ownership, and mode only (`lifecycle.go:670-708`). There is no Git-worktree exclusion and no shared/network-filesystem refusal. A temporary reviewer probe created a short private Git repository root and demonstrated that both runtime and durable data directories inside it were admitted and started successfully. This contradicts the LifecycleConfig contract, C-005, and the frozen sidecar lifecycle contract. Add enforceable topology checks and negative controls for worktree and unsupported/shared filesystem roots.

6. **Pidfd/fallback/error and mutant evidence is not behavioral.** No test forces pidfd success, `ENOSYS`/`EINVAL` fallback, or hard error. The fallback ultimately calls `unix.Kill(pid)` after a separate `/proc` check (`lifecycle.go:980-986`), leaving an unavoidable PID-reuse interval. Also, if `observeStartedProcess` fails after `command.Start`, `lifecycle.process` is still zero when `abortStart` asks `inspectOwnershipLocked`, so the just-started child is not terminated (`lifecycle.go:281-300`, `996-1013`). Finally, `TestProvenanceLifecycleAndResponseDropMutantControls` is mostly source-string presence checking (`provenance_test.go:180-211`), so deleting or bypassing the actual safety behavior can leave the test green. Provide injectable syscall/process seams, force all branches, fail closed when exact signaling is unavailable, prove post-start failure handling, and replace decorative string checks with deletion-sensitive behavioral mutants.

## Gate evidence

- PASS: exact admitted binary package tests, `-count=3`.
- PASS: race detector, `-count=3`.
- PASS: strict-wire fuzz, approximately 158k executions.
- PASS: `go test ./internal/architecture/... -count=1`.
- PASS: `go vet ./internal/sidecar/gapdb`, `staticcheck ./internal/sidecar/gapdb`, `gofmt -d`, and `git diff --check`.
- Recorded non-blocking environment note: the Python architectural gate reports `no_coverage`; this is not treated as a Go implementation failure.

## WP anti-pattern checklist

1. Dead code — **FAIL**: exported `Start`, `Attach`, and `StartResponseDropProxy` have no production caller outside this new package yet. If later-WP composition is the intentional exception, record that explicit staged-integration waiver and ensure the earliest dependent WP supplies the live caller.
2. Synthetic-fixture test — **FAIL**: the lookalike/misattribution coverage mutates values passed directly to `validateComposite`; it does not exercise the production socket/connection attribution path.
3. Silent empty return — **PASS**.
4. FR coverage — **FAIL** for FR-003/FR-005/NFR-014/C-005 due to the reproduced cases above.
5. Frozen surface — **PASS**: no frozen contract was modified by the implementation commits.
6. Locked decision — **FAIL**: worktree/shared-storage exclusion, exact executed-byte binding, and attribution-safe cleanup are contradicted.
7. Shared-file ownership — **PASS**: implementation changes stay within the six owned sidecar files; the handoff-only WP metadata change is expected.
8. Production fragility — **N/A**: Go production code adds no language-level `raise`; typed failures are used.

Verdict: **REJECT / return WP04 to implementation.**
