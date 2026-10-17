# Read-only clean-source index proof

Status: development implementation, controlled verification and independent
scoped review complete; fresh clean-source M1 focused/fault run passed on
`a0dbe69961cc5f00a17db4f2b93c1e9bf27f18c1`, without full release qualification.

## Failed source observations, not failed behavioral cuts

Clean candidate `5e9da5fc94ffb756c2d889521754d9ce1eebdbee`, tree
`b2af1782dd70ace891dcfc7c34c67aca4ff73b79`, remained unchanged through its
once-only critical race/M1 verification. Five critical packages passed. M1
finished in 700.103 seconds without timeout: seven focused campaigns passed;
atomic publication completed 100 repetitions, 99 passing. Atomic attempt 0's
completion-time source observation and fault GJ-05's pre-execution observation
both refused `git write-tree` with exit 128. Passing cut facts do not replace
successful source evidence. GJ-05 minted no execution/source receipt.

`write-tree` acquires the shared index lock and can publish index/object cache
metadata. The original helper discarded stderr. Therefore historical lock
contention is **unconfirmed**, even though both failing source-acquisition calls
are established. Preserve `/tmp/spec-kitty-clean-candidate-journal-m1.log` and
the earlier 50b1 timeout/socket failures; neither is rewritten as a pass.

## Structural read-only law

M1 source admission already requires a completely clean checkout: index equals
the pinned tree, tracked/untracked/ignored outside-source bytes absent. The new
`observeGitSource` proves that equality directly rather than using a writer:

1. Pin HEAD's commit; resolve its immutable commit tree.
2. Read the original index with cached `diff-index` against that fixed tree.
   A difference, error or inaccessible index refuses; there is no HEAD fallback.
3. Reject concealment flags (skip-worktree/assume-unchanged), and require the
   complete tracked/untracked/ignored census to be empty.
4. Repeat both proofs and recheck the unchanged HEAD. Only then return the
   equality-proved index tree and the unchanged empty-status digest.

Git routing/configuration environment cannot select another index/worktree/
object store or replacement namespace. Optional index writes, fsmonitor and
untracked cache are disabled. `core.filemode=true` prevents executable-mode
concealment despite local/inherited configuration. A controlled chmod reproduces
prior false acceptance, then refuses while preserving the original repository;
logs: `/tmp/m1-source-mode-{red,green,race,vet}.log`. Census bytes remain raw,
including NUL and whitespace; only object identities are trimmed. Future stderr is retained with
bounded 4 KiB storage while the child is drained. Source seal/data schemas,
sidecar tuple checks and refusal authority are unchanged. No validation cache,
allowlist, receipt retarget, retry or qualification serialization is introduced.

The separate fault workspace-content sampler is unchanged: it measures an
arbitrary mutable worktree index and cannot substitute this clean-source-only
index-equality capability.

## Controlled evidence and limits

A disposable clean repository with a held `index.lock` makes the old acquisition
refuse; the read-only proof accepts only its unchanged actual index/worktree.
Twelve concurrent single-shot observations preserve original HEAD, index bytes
and object inventory. Dirty/staged/untracked/ignored/unmerged/error/cancellation,
foreign Git routing, replacement, concealment and concurrent-source-change
controls fail closed. Go 1.26.7 owned race/vet and unchanged M1 architecture
checks pass.

Logs: `/tmp/m1-source-git-readonly-{red,green,race,vet,architecture}.log`.
This controlled red establishes the write-capable mechanism, **not the historical
failure's causation**. Independent scoped follow-up found no blocking regression
and confirmed closure of the mode gap. Rechecks are not an atomic filesystem
snapshot; candidate immutability is a separate qualification constraint. A new
reviewed-only clean commit preceded one fresh M1 campaign execution. Candidate
`a0dbe69961cc5f00a17db4f2b93c1e9bf27f18c1` (tree
`6f261fefbc0d3e95c7a004fc8efda2caf7688d51`) passed the completed M1 run in
707.244 seconds: eight focused campaigns completed their 100-attempt controls;
the faults package passed. Exactly one ordered-crown opt-in skip is visible.
All three executable hashes and clean source/tree remained unchanged. Logs:
`/tmp/spec-kitty-clean-candidate-readonly-m1.{log,result.json}`.

Its separate once-only critical race run failed the unchanged 2 ms progress-query
p95 assertion at 2.214526 ms. This candidate is therefore still not fully
qualified. The failed 5e9da5fc receipts remain bound to their original source;
historical M2 proof and this M1 pass are never transferred to later candidates.
