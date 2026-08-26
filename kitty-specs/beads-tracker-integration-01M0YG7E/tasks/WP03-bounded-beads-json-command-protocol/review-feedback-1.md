# WP03 review feedback — bounded Beads JSON command protocol

Verdict: changes requested for commit `51c70d1` (original `a22f4cb`). The focused tests are stable, but the implementation does not yet satisfy the closed-command and exhaustive hostile-protocol acceptance contract.

## Blocking finding 1 — positional typed values can still inject CLI options

`requiredValue` in `internal/tracker/beads/command.go` rejects only blank/NUL values. Constructors then place external IDs and comment bodies directly into option-parsed positions. For example, `newShowCommand(binding, "--help")` renders `bd show --help --json`; the provider can interpret the typed value as command syntax rather than as an ID. The same class applies to update/claim, dependency, and comment positional values. This violates WP03 T010/T015 and the Definition of Done requiring arbitrary argv injection to be impossible and invalid input to be refused before spawn.

Remediation:

- Define and enforce a provider-valid external-ID grammar, or use a documented/qualified end-of-options arrangement that preserves the exact Beads v1.2.2 command shape.
- Protect every positional value site, including comment bodies where option-like content is legitimate user data.
- Add constructor and helper-process tests proving option-like IDs/values cannot alter the selected command or flags and are either represented literally or rejected before the subprocess starts.

## Blocking finding 2 — nested warning JSON can be misclassified as a documented provider error

`decodeDocumentedError` in `internal/tracker/beads/protocol.go` begins a new decode at every `{` byte. Consequently an object such as `{"wrapper":{"schema_version":1,"error":"x","code":"not_found"}}` fails at the wrapper and is then accepted from its nested object. That is not one complete documented top-level error value; it lets warning content select a stable provider code, contrary to `contracts/beads-json-protocol.md:42-54` and FR-002.

Remediation:

- Recognize only a complete top-level documented error object amid warning material; do not rescan nested object starts as independent provider responses.
- Add negative cases for nested JSON objects, braces/JSON fragments in warning prose, and warning wording changes. Assert they remain malformed/ambiguous and never acquire `not_found` or another provider kind.

## Blocking finding 3 — executable identity does not round-trip the pinned contract

`decodeExecutableIdentity` in `internal/tracker/beads/errors.go:134-142` accepts `{"version":"1.2.2"}` with an empty build. The concrete contract at `contracts/beads-json-protocol.md:58-61` requires the version response to contain both `version` and `build` (commit/branch are optional).

Remediation:

- Require a non-empty `build` alongside the exact supported version.
- Add a contract-mirror test for the exact compatibility tuple (`1.2.2`, schema `1`) and rejection tests for missing/empty build and unsupported versions.

## Blocking finding 4 — the required adversarial process matrix is incomplete

The helper defines `prose-error`, but no test executes it. The suite also lacks the explicitly required permission/spawn-failure case, timeout after a read has begun, an infinite/noisy producer that is both bounded and reaped, warning-wording invariance through the real adapter path, and a real arbitrary-argv-injection negative case. The current source-string test only proves there is no obvious shell/import token; it does not prove the runtime closed-command invariant. `go tool cover` reports all `protocol.go` functions at 100% statement coverage, but this missing behavioral matrix means NFR-007/T015 and the Definition of Done are not yet established.

Remediation:

- Complete the cases required by WP03 T015 lines 354-377 using the owned helper-process tests.
- Assert stable typed kind/recovery, start state, mutation outcome, redaction, and process reaping for each case.
- Re-run normal, race, repeated, vet, coverage, contract, and scope gates.

## Anti-pattern checklist

1. Dead code: N/A — this is the explicitly sequenced protocol foundation consumed by the dependent adapter WPs.
2. Synthetic-fixture tests: PASS — existing FR tests invoke production constructors, runner, decoder, and translator paths.
3. Silent empty return: PASS — no silent empty failure path found in the seven owned production files.
4. FR coverage: FAIL — FR-002/NFR-007 hostile-output and command-injection cases above are missing.
5. Frozen surface: PASS — commit `51c70d1` changes exactly the seven WP03-owned files.
6. Locked decision: FAIL — option-like positional values and nested warning JSON violate the closed argv/no-warning-branching decisions.
7. Shared-file ownership: PASS — no shared or out-of-scope file is modified by the WP commit.
8. Production fragility: PASS — no panic/bare-raise analogue or undocumented fail-open path found.

## Evidence already passing

- `go test ./internal/tracker/beads`
- `go test -race ./internal/tracker/beads`
- `go test -count=20 ./internal/tracker/beads`
- `go vet ./internal/tracker/beads`
- `go test -coverprofile=... ./internal/tracker/beads` — 90.6% package statement coverage; every `protocol.go` function reports 100% statements
- `make contract`
- `make scope`
- Process-group descendant termination, timeout-after-apply ambiguity, pre-canceled `not_started`, bounded ordinary output, environment allowlisting, schema/shape rejection, JSONL limits/order/cancellation, typed translation, and diagnostic redaction all passed their present tests.

WP04 depends on WP03 and must rebase or restart from the corrected WP03 result before implementation.
