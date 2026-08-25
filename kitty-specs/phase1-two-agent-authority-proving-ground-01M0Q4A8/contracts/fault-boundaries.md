# Deterministic Fault Boundary Contract

Fault injection is available only at named durable boundaries. Production uses
a no-op implementation of the same tiny interface.

## Actions

At a boundary, the test controller may:

- block until explicitly released;
- terminate the helper process with a recognizable exit code;
- return a typed provider failure;
- drop a successful response after the effect;
- advance the injected clock;
- continue normally.

The controller may not write control tables or Git refs directly to create a
desired state.

## Start-Run Boundaries

| ID | Boundary |
|---|---|
| `SR-00` | before initial control-store transaction |
| `SR-01` | after operation record commits |
| `SR-02` | after lease/run commits |
| `SR-03` | after tracker request, before outcome is recorded |
| `SR-04` | after tracker effect applies, before response is observed |
| `SR-05` | after workspace reservation commits |
| `SR-06` | after Git worktree creation |
| `SR-07` | after base verification |
| `SR-08` | after dependency preparation, before publication |
| `SR-09` | after workspace-ready commit, before response |

## Handoff/Review Boundaries

| ID | Boundary |
|---|---|
| `HR-00` | before authority revalidation |
| `HR-01` | after candidate Git verification |
| `HR-02` | after immutable handoff commit, before response |
| `HR-03` | before reviewer-independence check |
| `HR-04` | after criterion evaluation, before transaction |
| `HR-05` | during competing verdict acquisition |
| `HR-06` | after verdict/criteria commit, before response |
| `HR-07` | after approval eligibility publication |

## Integration Boundaries

| ID | Boundary |
|---|---|
| `IG-00` | before target-ref authority acquisition |
| `IG-01` | after mutation lease commits |
| `IG-02` | after target/candidate preflight records commit |
| `IG-03` | after detached integration workspace creation |
| `IG-04` | after candidate application, before ref movement |
| `IG-05` | immediately before target-ref compare/update |
| `IG-06` | after target ref moves, before outcome record |
| `IG-07` | after integration result commits |
| `IG-08` | after work-done commits, before response |
| `IG-09` | during cleanup eligibility publication |

## Universal Assertions

Every fault test first proves:

- primary checkout HEAD, index, and tree are unchanged;
- no two valid implementation leases exist for the same work generation;
- no workspace has changed lease ownership;
- stale tokens cannot publish handoff, review, or integration state;
- at most one process has target-ref mutation authority;
- all candidate and conflict commits remain reachable;
- external compensation has not overwritten unattributed state.

Only then does it assert the boundary-specific recovery result.
