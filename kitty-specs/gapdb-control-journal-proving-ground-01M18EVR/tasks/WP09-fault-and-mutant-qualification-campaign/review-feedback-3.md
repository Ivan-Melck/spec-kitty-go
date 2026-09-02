# WP09 review feedback — cycle 3

**Verdict: changes requested.** The cycle-2 campaign, recovery, and observation
blockers are remediated, but the source-binding guard still permits ignored
toolchain inputs to change without changing the sealed source identity.

## Blocker — ignored executable inputs can bypass `SourceBinding`

Location: `internal/testcampaign/m1/invariants.go:112-145`.

`observeGitSource` combines ordinary porcelain status with an ignored-file
allowlist containing only `*.go`, `*.s`, `*.c`, `*.h`, and `*.syso`. That is not
the complete set of inputs Go can compile. In particular, cgo compiles C++
files such as `*.cc`; embed directives can also consume arbitrary ignored
files. Those files are absent from ordinary status and from the current
allowlist, so `Commit`, `Tree`, `WorktreeTree`, and `WorktreeStatusSHA256` can
all retain their clean values while the executable bytes differ.

Independent reproduction used a clean temporary Git repository containing a
tracked cgo file and an ignored `payload.cc`. `go test ./...` compiled the
ignored C++ implementation successfully, while all of the following remained
true:

- `git rev-parse HEAD^{tree}` equalled `git write-tree`;
- `git status --porcelain=v1 --untracked-files=all` was empty; and
- the exact `git ls-files --others --ignored --exclude-standard` extension
  census used by `observeGitSource` was empty.

This violates the source-integrity remediation and permits a production pass
receipt to bind the wrong executable source.

Fix this with a complete construction rather than a longer hand-maintained
extension list. The strongest option is to execute qualification from a clean,
detached source tree materialized from the recorded commit/tree and reject any
additional inputs. If the guard must inspect an ordinary worktree, bind the
actual build-input manifest (including cgo, assembly, generated, and embedded
inputs) and fail closed on anything outside the pinned tree. Add regressions
for at least an ignored cgo source and an ignored `//go:embed` input, proving
both prevent `SourceBinding.Valid()` and a pass receipt.

## Verified cycle-2 remediation

- The independent exact fact census rejects omission, extra keys, renames, and
  false facts for all eight focused campaigns.
- NFR-001 executes both applicable GJ publication cuts per attempt; NFR-002
  executes equal reconciliation and changed-digest collision/no-publication.
- NFR-007 performs real alternating clean/unclean owner replacement, durable
  restore, post-transition restore, fresh runtime reconstruction, current
  authorization, and public predecessor epoch-stale refusal.
- `RestartOwned` validates before signalling, revalidates exact process
  ownership, preserves the durable database, and creates fresh process/socket
  provenance.
- Historical workspace restore binds each head to its exact terminal event,
  including aggregate identity, logical version, record revision, digest, and
  epoch; targeted mutation tests passed.
- NFR-008 writes actual bytes into quarantined W1 and independently compares
  fresh W2 HEAD, index, tree, and content before/after.
- Primary, integration, restore, and playback invariants now derive from
  production snapshots and durable observations rather than pass literals.
- Five faults, eight focused definitions, nineteen executable mutants, and
  eight universal invariants remain exact; WP10 evidence/adoption work is not
  present, and no SQLite dependency was introduced.

## Independent verification

- Targeted lifecycle clean/unclean restart and invalid-input tests passed three
  times under the race detector against admitted GapDB SHA-256
  `62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`.
- Targeted proving-ground restart, historical restore, heartbeat, predecessor,
  and zombie tests passed twice under the race detector.
- Semantic-census and repeated real-restart tests passed under the race
  detector.
- All nineteen executable product mutant guards passed three times under the
  race detector.
- The five production fault receipts and source-seal mutation tests passed in
  a clean detached clone of implementation commit `4faeb732`.
- Architecture guards, vet, staticcheck, formatting, and diff checks passed.
- The claimed 800/800 full census was not blindly repeated; the review used
  risk-proportional targeted production scenarios as directed.

## Anti-pattern checklist

1. Dead code — **PASS**: new public qualification and lifecycle seams have
   production campaign callers.
2. Synthetic-fixture test — **PASS**: the focused and source-integrity tests
   invoke production guards; the source failure is incomplete input coverage,
   not a literal fixture substituting for the guard.
3. Silent empty return — **PASS**: reviewed zero values are paired with typed
   errors or deliberate absence results.
4. Requirement coverage — **FAIL**: the requirement to bind the exact source
   actually executed is not met for ignored cgo/embed inputs.
5. Frozen surface — **PASS**: implementation commits modify only the explicitly
   reconciled WP09 ownership set.
6. Locked decision — **PASS**: no SQLite substitution, internal GapDB server,
   CLI prose parsing, or WP10 adoption claim was introduced.
7. Shared-file ownership — **PASS**: authority, workspace, lifecycle, review,
   and Git ownership expansions are recorded in the WP activity log.
8. Production fragility — **PASS**: new failure paths are typed and fail loud;
   no transient-race panic path was added.
