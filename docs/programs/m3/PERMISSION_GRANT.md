# PERMISSION_GRANT

**Granted to:** the autonomous build agent (codex) for the **M3 production
mission execution** mission in `spec-kitty/spec-kitty-redesign`.
**Granted by:** Lynn, 2026-09-11 *(pending her confirmation at scaffold
commit; grant takes effect only on her explicit approval)*.

## Grant

You are authorized to make **all decisions necessary to complete the mission**
defined in [MISSION-OUTLINE.md](MISSION-OUTLINE.md) — technical,
architectural, dependency, tooling, and scope-detail decisions — without
asking for approval first. Decide, build, verify, move on.

## Conditions (both mandatory)

1. **Plan changes → STATUS.log.** Any deviation from, addition to, or removal
   from the plan (architecture, work packages, adversarial cases, deployment
   shape, scope) must be documented in [STATUS.log](STATUS.log) at the time of
   decision: date, what changed, why, and the consequence.
2. **Spec-kitty issues → STATUS.log.** Every spec-kitty issue filed, updated,
   or closed must be notated in STATUS.log: date, issue number, title, and one
   line on why it exists or what changed.

STATUS.log is append-only. Never rewrite or delete prior entries.

## Still out of bounds (safety rails, not decision limits)

- **The M2 authority kernel is frozen, not yours to amend.** Wire laws,
  transition validators, canonicality rules, admission records, and the
  adopted M2 evidence tuple are constraints. Bugs you find in them become
  typed issues for review, not in-place edits.
- **The Hubnot admitted tuple is exact.** Build from the pinned source and
  binary identities in `docs/HUBNOT_QUALIFICATION.md`; never "conveniently"
  rebuild from a newer revision.
- **Read-only posture toward production evidence.** `docs/programs/evidence/`
  receipts are immutable history — reference, never rewrite or re-seal.
- **Never commit secrets.** Never broaden access beyond what the mission needs.
- **No infrastructure creation.** No VMs, no new credentials, no DNS, no
  external services. If a step seems to need one, stop and surface it to
  Lynn — that is an out-of-bounds signal, not a decision.
- **Human-gated:** the program's mission gates (accept, mission-review,
  retrospective) and any successor-mission entry decision remain with Lynn
  and the program's recorded law.

Everything else is yours to decide. When in doubt: decide, log it, keep building.

## 2026-10-13 — confirmation and current scope

The requester confirmed the conditional delegation and authorized M3/M4
specification and recorded-admission preparation in the current conversation:
"can you fix 1 and 3 i agree with you on 1". The original pending statement above
is historical; the new [entry-work authorization](../decisions/2026-10-13-m3-m4-entry-authorization.json)
records the exact proposal accepted, session attribution, immutable predecessor
binding and unchanged safety rails.

This confirmation removes the pending-delegation blocker. It does **not** itself
admit or activate either mission, approve unverified external evidence, authorize
qualification-proxy contract changes, or waive independent review/acceptance.
The canonical program's §4.1 entry law remains mandatory. No independently
verified GitHub/cryptographic identity is asserted by this local reference.