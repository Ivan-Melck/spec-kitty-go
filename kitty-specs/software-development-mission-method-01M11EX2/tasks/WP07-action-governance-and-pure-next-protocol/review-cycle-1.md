---
affected_files: []
cycle_number: 1
mission_slug: software-development-mission-method-01M11EX2
reproduction_command:
reviewed_at: '2026-08-28T07:28:13Z'
reviewer_agent: user
wp_id: WP07
---

# WP07 review feedback — cycle 1

## Verdict

Rejected. The governed implementation is otherwise coherent and the focused, repeated, race, architecture, formatting, vet, and static checks passed, but the action form of `NextResult` has two conflicting destination identities and is not semantically stable across its JSON boundary.

## Blocker: action descriptor destination changes across a byte-identical round trip

- **Requirements:** T038 steps 3, 4, and 10; T038 validation (“Every legal kind round-trips with stable identities”); T039 step 4; T040 step 3.
- **Location:** `internal/mission/next.go`, `ActionDescriptor.validate`, `NewActionResult`, and `NextResult.UnmarshalJSON`; adjacent command binding in `internal/mission/action.go`.
- **Finding:** `ActionDescriptor` carries both `ActionKey` and an unencoded `Successor`. Validation accepts them when they identify different steps. `NewActionResult` only derives `Successor` when it is empty. JSON omits `Successor`, while decode reconstructs it from `ActionKey`. Consequently, a constructed result can say `ActionKey=planning` and `Successor=specifying`; after marshal/unmarshal the exact same wire bytes say `Successor=planning`. The wire form is byte-stable but the domain identity changes. This also leaves the destination-step law ambiguous between in-memory and transported descriptors.
- **Adversarial proof:** a temporary review probe constructed `NewActionResult(…, ActionDescriptor{ActionKey: "planning", Successor: "specifying", GovernanceAction: "mission.plan", Mutating: true})`. Construction succeeded. After marshal/unmarshal, `Successor` changed from `specifying` to `planning` while the encoded bytes remained equal. The probe was removed after reproduction.
- **Required repair:** define one canonical destination identity. Constructor and validation must reject an `ActionKey`/`Successor` mismatch (or eliminate the second independent input); marshal/unmarshal must preserve or deterministically reconstruct that same destination and reject substitution or omission where applicable. Keep action-command request digesting and live re-resolution bound to the same canonical destination. Add a permanent mutant/control covering mismatched, omitted, and substituted destination values, including semantic equality before and after JSON round trip.

## Evidence retained

- Exact scope verified: product commit `9da6d83` adds only the eight WP07 governed files; `1d328c3` is topology/planning cleanup only.
- Focused tests x10 passed for governance context, decisions, blockers, next, and action.
- Focused race tests passed for `internal/governancecontext` and `internal/mission`.
- Phase 3 architecture controls passed x3; targeted helper architecture check passed.
- Targeted `go vet`, `staticcheck`, and `gofmt -d` passed.
- Source inspection confirms `review_ready` is construct/decode-only in production, and the governed production files import no tracker, projection, SQL, transport, filesystem, or process-execution package.

No product files were changed during review.
