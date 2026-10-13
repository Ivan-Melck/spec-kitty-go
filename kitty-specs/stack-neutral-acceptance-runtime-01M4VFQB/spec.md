# Mission Specification: Stack-Neutral Acceptance Runtime

**Mission Branch**: `parity/latest-upstream-main` (planning/base and final merge target).
**Created**: 2026-10-13 (canonical creation metadata).
**Status**: Draft specification; program M4 admitted, not implemented or accepted.
**Mission ID**: `01M4VFQBZWYDKMZP34EF6ZB4NQ`.
**Input**: The requester's recorded `1:yes 2:go ahead 3: approved` confirms the
prepared M4 scope and conditional admission; the verified entry decision satisfies
that condition. This specification preserves the approved requirements rather
than interpreting general parity intent as a new provider or authority approval.

## Confirmed intent and provenance

An operator requests qualification of a bound candidate and claim. Approved,
materially different providers supply provenance-rich evidence; one neutral gate
returns an honest decision. Missing required bindings request a decision rather
than fabricating success. Evidence reuse requires unchanged relevant inputs;
waivers remain accountable, candidate-bound and subject to durable revocation.
Acknowledged operations survive restart, and ambiguous external effects are
reconciled before another execution.

The confirmed source is [the frozen M4 candidate](../../docs/programs/m4/CANDIDATE_SPEC.md),
SHA-256 `7fd5c5c3cbf505cddbdb1af3265ce90643d5304740f644cda4fddd52c8b49141`.
The numbered [requester approval](../../docs/programs/decisions/2026-10-13-parity-qualification-entry-approval.json)
is conversation-local attribution, not a verified GitHub or signing identity.
[Entry bindings](../../docs/programs/m4/ENTRY_BINDINGS.json),
[independent entry review](../../docs/programs/m4/ENTRY_REVIEW.md) and
[the admission decision](../../docs/programs/decisions/2026-10-13-m4-admission.json)
remain separate, immutable evidence. Canonical creation established this mission's
identity and coordination topology; no implementation lane was allocated.

The [program](../../docs/PRODUCTION_RUNTIME_PROGRAM.md),
[neutral contract](../../docs/STACK_NEUTRAL_ACCEPTANCE.md) and
[evidence schema](../production-runtime-architecture-01M16GVA/contracts/acceptance-evidence.schema.json)
are normative. Repository Go commands and ecosystem CI tools are not product law.

## User Scenarios & Testing *(mandatory)*

### User Story 1 — One claim, different providers (Priority: P1)

An operator binds required claims and obtains a single neutral acceptance decision
from bounded process, CI-attestation and human-decision ports.
**Why this priority**: Provider diversity must not change what the required claim means.
**Independent Test**: the same candidate/claim contract is judged through at least
two materially different provider mechanisms, with actual execution/attestation/
decision evidence and matched valid/invalid outcomes at the application entry.

**Acceptance Scenarios**:
1. Given the same bound claim, when different approved providers supply admissible
   proof, then the same neutral gate decision results without stack imports.
2. Given organization/project/mission-type/mission requirements, when resolving
   the plan, then their strict union is required; a lower layer cannot weaken it.
3. Given an unbound required claim, when qualification is requested, then return
   `decision_required`, never an implicit skipped/pass; a bound control executes.

### User Story 2 — Reuse only exact admissible evidence (Priority: P1)

An operator changes a candidate, provider binding or input and can see precisely
which evidence remains admissible and which must be reacquired.
**Why this priority**: Reuse must save valid work without accepting a different candidate's proof.
**Independent Test**: mutate each candidate, WorkRevision, binding, provider,
policy/governance and relevant input component separately on a shared fixture.

**Acceptance Scenarios**:
1. Given unchanged pinned relevant inputs, when reusing evidence, then admissible
   content-based reuse succeeds; changing each relevant component invalidates it.
2. Given a qualification dependency graph, when independent claims execute
   concurrently, then prerequisites remain respected and blocked/error results
   never silently promote dependent claims.

### User Story 3 — Explicit outcomes and accountable waivers (Priority: P1)

An operator distinguishes fail/error/skipped/waived results and makes only
policy-authorized, candidate-bound exceptions.
**Why this priority**: Failure, revocation and restart must never become unaccountable acceptance.
**Independent Test**: typed provider outcomes and human decisions cross the real
acceptance application boundary; positive controls share refusal fixtures.

**Acceptance Scenarios**:
1. Given a valid waiver, when deciding acceptance, then actor/authority/reason/
   scope/candidate/policy/expiry are checked; changing any required binding refuses.
2. Given malformed or human-formatted output, when a provider responds, then
   protocol error is typed; presentation text cannot mint a verdict.
3. Given detected stack suggestions, when no binding is approved, then policy
   remains unchanged and a required missing binding stays decision-required.
4. Given an initially admissible waiver, when a durable revocation is appended,
   then both reevaluation and reuse refuse it without changing the old envelope.
5. Given an acknowledged qualification or acceptance operation, when the host
   restarts and the identical key/input replays, then it returns the same durable
   outcome; changed input under that key conflicts. If an external response is
   ambiguous, reconcile recorded effects before retrying; no duplicate effect or
   fabricated decision is permitted.

### Edge Cases

Missing/stale/expired evidence, conflicting claim definitions, unavailable
providers, cancellation and partial completion retain honest typed outcomes.
Skipped/error/fail do not become pass; reuse requires explicit stable-input proof.
Unknown provider identity and mismatched evidence envelopes refuse before decisions.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status | Delivery | No-op passable? |
|----|-------|------------|----------|--------|----------|-----------------|
| FR-001 | Neutral claims | One claim contract produces equivalent gate decisions through materially different approved providers. | High | Open | [build] | no |
| FR-002 | Strict union | Resolve all organization/project/mission-type/mission requirements without lower-layer weakening. | High | Open | [build] | no |
| FR-003 | Explicit bindings | Pin provider/execution/trust/policy identities; missing required bindings return decision_required, not skipped success. | High | Open | [build] | no |
| FR-004 | Bounded envelopes | Validate typed pass/fail/error/skipped/waived evidence and provenance; never parse presentation text as a result. | High | Open | [build] | no |
| FR-005 | Qualification graph | Execute dependency-ready claims concurrently without violating prerequisites or hiding partial outcomes. | High | Open | [build] | no |
| FR-006 | Precise invalidation | Candidate, WorkRevision, binding, provider, policy/governance and relevant-input changes invalidate affected evidence exactly. | High | Open | [build] | no |
| FR-007 | Content reuse | Reuse only fresh, trusted, correctly bound evidence with explicit relevant-input equivalence. | High | Open | [build] | no |
| FR-008 | Waiver authority | Check actor, authority, reason, scope, candidate, policy, applicable expiry and durable revocation before each decision and reuse. | High | Open | [build] | no |
| FR-009 | Provider ports | External-process, CI-attestation and human-decision ports translate typed evidence without ecosystem semantics in the kernel. | High | Open | [build] | no |
| FR-010 | Suggestions only | Stack detection suggests bindings; it cannot change required policy or approve a provider. | High | Open | [build] | no |
| FR-011 | Durable operations | Acknowledgment follows durable publication; identical key/input replays its outcome, changed input conflicts, and ambiguous external effects reconcile before retry. | High | Open | [build] | no |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Cross-provider census | At least two materially different mechanisms execute the same claim contract, covering positive and negative outcomes; zero required proof skips. | Reliability | High | Open |
| NFR-002 | Bounded execution | Record immutable timeout/resource/input/output/network/secret budgets before execution; overruns return typed errors within those configured bounds. | Security | High | Open |
| NFR-003 | Neutrality | Zero kernel ecosystem/provider imports and zero presentation-derived verdicts; executable mutants demonstrate rejection. | Architecture | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Exact predecessor | M2 is the sole predecessor; bind its adopted substrate and stable candidate/WorkRevision/review/actor/governance identities, not this unqualified branch. | Governance | High | Open |
| C-002 | Ratified contract | Consume the reviewed neutral evidence contract without changing frozen M2 laws or historical receipts. | Architecture | High | Open |
| C-003 | Scope | No real-agent/Git composition, public API facade, universal Go commands, infrastructure/credentials or implicit provider installation. | Program | High | Open |
| C-004 | Independent gates | Technical authorization never waives independently attributable review, acceptance, merge, mission review or retrospective. | Governance | High | Open |

### Key Entities

AcceptanceClaim, QualificationPlan, ProviderBinding, EvidenceEnvelope,
AcceptanceDecision and Waiver bind candidate/WorkRevision/actor/policy identities;
operation/progress records describe execution without becoming alternate authority.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Materially different providers produce the same neutral decision for the same claim under matched admissible and refusing controls. — [build] · no-op passable: no
- **SC-002**: Every required missing/stale/malformed/unauthorized-waiver case refuses or requests a real decision without skipped/implicit success; each has a same-fixture positive control. — [build] · no-op passable: no
- **SC-003**: Every independently mutated relevant evidence binding invalidates the affected proof; unrelated stable-input evidence remains reusable under explicit policy. — [build] · no-op passable: no
- **SC-004**: Ecosystem-import and human-output-verdict mutants fail executable architectural/behavioral checks at the production boundary. — [build] · no-op passable: no
- **SC-005**: Same-fixture durable revocation invalidates an initially admissible waiver on both reevaluation and reuse without editing its envelope. — [build] · no-op passable: no
- **SC-006**: Production crash/replay and ambiguous-effect controls preserve all acknowledged acceptance/qualification outcomes and produce zero duplicate external effects; changed-input replay refuses. — [build] · no-op passable: no

## Admission, stop condition and rollback

Record adopted M2 immutable mission/source/tree/evidence/review verdict, stable
identity contracts and ratified acceptance-contract digests before activation.
Verify the acyclic schema-valid manifest and the frozen entry evidence in
[admission readiness](../../docs/parity/ADMISSION_READINESS.md).
Stop on missing/contradictory evidence, failed primary falsification or a
required retained-law amendment. Disable unqualified bindings and retain their
evidence as non-admissible history; never weaken claims or universalize one stack.
M3/M4 may proceed independently after admission; M5 still requires accepted versions
of both plus M2 and its separate API/progress/operation-contract decision.

## Assumptions and independent gates

- The approved scope and requirements above are unchanged from the frozen candidate;
  creation provenance and relative links are refreshed, not its product law.
- Entry review is not review or acceptance of this specification's implementation.
- Provider trust and execution bindings require explicit admissible proof before use;
  conversation approval does not mint a provider credential or accountable signer.
- Tests must cross the production application boundary with same-fixture positive
  and negative controls. Kernel-neutrality and verdict mutations must be detected.
- This mission does not establish full Python/Go parity or release qualification.
