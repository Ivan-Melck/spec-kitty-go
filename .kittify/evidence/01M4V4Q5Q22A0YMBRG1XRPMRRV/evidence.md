# Qualification proxy: sequential unary exchanges

Status: development compatibility correction verified; **not** historical M1/M2
requalification or current-candidate release evidence.

Requester instruction: `continue` after the retained suite failures. Scope was
announced explicitly: correct the qualification proxy's single-exchange behavior
for the current reused unary client; do not disable production reuse or change
frozen receipts. Invocation: `01M4V4Q5Q22A0YMBRG1XRPMRRV`.

## Source-contract witness

The actual `TestReconcileRealGJ02ResponseDrop` with original M1 daemon SHA-256
`62219cfddbc72467bd491e8ca2309b49cfc935b68df51b5500b31c51c4c84a1e`
failed again before correction: `indeterminate`, revision 0, recovery
`reconcile_same_operation`. Log: `/tmp/spec-kitty-proxy-compat-real-red.log`.
The reused client first reads operation absence, then aggregate head; the old
proxy closed the product connection after the first exchange, before AtomicBatch
or the selected durable-response cut. This was not lost journal durability.

## Narrow correction

[`proxy.go`](../../internal/sidecar/gapdb/proxy.go) now handles sequential bounded
unary exchanges on the existing product connection. Each newly received complete
request is decoded strictly, sent **once** over a fresh daemon connection, and
receives its exact response. It is not a replay/retry loop, streaming protocol
implementation, production transport change, or generic proxy feature expansion.

- Each exchange resets its existing deadline and retains frame limits.
- Each fresh daemon connection retains exact connected-peer PID and executable
  device/inode/size/hash/start-tick verification before publishing any request.
- The selected reservation remains global and one-shot. Exact request ID,
  durable ack, database identity, mutation count and revision predicates remain.
- The entire accepted selected response must be consumed before recording GJ-02;
  the product connection closes **without even a header byte** being forwarded.
- Active daemon relays now join the existing mutex-protected connection registry.
  Shutdown before registration prevents publication; otherwise Close interrupts
  both ends and waits for owned handlers. No request is retried or duplicated.
- Existing owned-socket cleanup/refusal on replacement remains unchanged.

No daemon tuple, provenance schema, source pin, response predicate, production
reuse setting or frozen qualification receipt/hash was changed.

## Evidence

- New unconditional stable-StartResponseDropProxy controls failed pre-fix:
  second exchange broken pipe; Close left a blocked daemon relay until its
  one-minute read deadline. `/tmp/spec-kitty-proxy-compat-unit-red.log`.
- Final controls pass under race: two ordinary exchanges on one connection,
  selected third exchange produces zero raw bytes and EOF, later matching traffic
  forwards rather than cutting again, exact request counts 3/4; shutdown promptly
  interrupts a blocked daemon without waiting its deadline or minting a cut.
  `/tmp/spec-kitty-proxy-compat-final-controls.log`.
- All response-drop tests pass under race, including malformed/partial/duplicate
  response refusals, database/cardinality/durable-revision mismatches, identity
  substitution, shutdown/no-duplicate and replaced-socket controls, plus real
  admitted lifecycle/reconciliation. `/tmp/spec-kitty-proxy-compat-race.log`.
- The original actual-journal GJ-02 witness now passes:
  `/tmp/spec-kitty-proxy-compat-real-green.log`.
- Complete sidecar and application packages pass:
  `/tmp/spec-kitty-proxy-compat-packages.log`.
- Four unchanged architecture/absence guards, changed-package vet and diff hygiene
  pass: `/tmp/spec-kitty-proxy-compat-{guards,vet}.log`.
- Independent scoped review found no blocking defects. Its raw-byte/EOF coverage
  suggestion was incorporated in the final control rather than accepting timeout
  or hiding a partial frame-header leak.

The complete development suite after the progress/proxy corrections finished
**exit 1**: 48 passing packages, four failing, two without tests; 19 top-level
failures. Log: `/tmp/spec-kitty-post-progress-proxy-full-suite.log`. The CLI,
helper, runtime, application, sidecar and parent controlstore packages pass.
The four failing packages are scenario, M1 faults, M1 focused and M2 evidence.
No total skip count is inferred from non-verbose output. No retries or artifact
substitutions. The dirty/development tree (documentation/inventory and final test
coverage evolved during the run) is not a clean source-bound candidate.

The complete five-package critical race run also passes:
`/tmp/spec-kitty-progress-proxy-critical-race.log`. Fresh candidate identity and
independent qualification remain required. Restart
terminal-history equivalence, distinct nested artifact bindings, frozen-source
campaign requirements and M3/M4 entry decisions remain separate obligations.
