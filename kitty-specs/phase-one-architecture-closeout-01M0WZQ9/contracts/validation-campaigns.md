# Contract: Validation Campaigns

## Quality campaign

**Triggers**: pull request and push to the PR-bound branch.  
**Budget**: p95 below eight minutes over ten representative runs.  
**Contents**:

- formatting and build preflight;
- unit and portable contract tests;
- bounded acceptance scenarios;
- architecture, reachability, and mutant controls;
- changed/focused race checks;
- vet, staticcheck, module verification, govulncheck, and binary build.

The workflow cancels an older in-progress run for the same pull-request ref and uses read-only repository permissions.

## Full campaign

**Triggers**: nightly schedule and manual dispatch.  
**Contents**:

- every quality-campaign assertion;
- complete scenario suite;
- crash boundary matrix;
- stale writer and zombie recovery probes;
- repeated contention and integration serialization;
- soak/repetition profiles.

## Test census

Curated `-run` selectors execute with `go test -json`. A verifier requires:

1. every expected test emitted a start event;
2. every expected test emitted one terminal pass/fail/skip event;
3. no expected test is absent;
4. an unexpected zero-match or partial match fails;
5. the process exit and per-test terminals agree.

## Evidence

Each CI run retains for fourteen days:

- campaign name and commit;
- start/end/duration;
- expected and observed test census;
- terminal verdict;
- logs and failing scenario identity;
- architecture mutant results.
