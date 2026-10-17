# Qualification cleanup errors.Join — bounded source-request recipe

Status: bounded source review approved (`7ff6cb77-6c08-4e7`); **full accounting OPEN**.
Test: `internal/productionruntime/expiry_qualification_join_accounting_test.go`,
linux/amd64 with `spec_kitty_expiry_host_qualification`, Go1.26.7.

This adds a substantive missing cleanup/error event recipe, not another whole-call
or heap proxy. Actual pinned `src/errors/join.go` SHA256:
`87da7c120f71d17e37f110ae1dc6af6493ec841a22ee825e13a6a8024a014dba`.
The test requires this exact source and checks the actual returned unexported
`errors.joinError` type through reflection: one24-byte slice field, offset0;
error interface slots16bytes. No private runtime type is redeclared or imported.

For EACH invocation j let k_j be the actual count of non-nil error interfaces,
including typed-nil pointers held in non-nil interfaces:

- k_j=0: source returns nil, no join record/backing request.
- k_j>0: source requests one joinError record and `make([]error,0,k_j)`, appending
  the k_j interface values in order. Logical selected-record/backing width is
  `24 + 16*k_j`. This is **not** the incoming variadic-array footprint.
- All borrowed error referents are retained by reference, not cloned. Nil
  filtering does not invoke `Error()` or traverse referent graphs. Error
  formatting later is separate owned work and is NOT paid by this recipe.
- An already-joined error is NOT flattened, deduplicated or returned directly in
  this pinned implementation. Inner two-error join plus outer one-error join
  requests56+40=96 logical bytes across TWO events, excluding referent graphs.

Tests exercise all-nil/mixed/typed-nil cases, exact resulting len/cap/order,
independence from subsequent caller variadic-backing replacement, nested
multiplicity, checked arithmetic at uint64 representability/overflow, and actual
source/type identity. Borrowed test errors panic if formatted, including typed
nil; tests do not accidentally earn evidence by calling arbitrary error methods.

An AST source inventory records **48 syntactic unaliased errors.Join sites** in
current non-test qualification files, with caller, line/column, operand-expression
count, variadic flag and each containing file's SHA256. This is not48 executions,
a type-checked whole-program call graph or a fixed event count. Conditional
non-nil operands, loops, concurrent losers and repeated rollback/final reports
remain symbolic. Source drift must be reconciled in a new campaign inventory.

For any finite observed invocation sequence J, selected source logical requests
compose as sum over nonzero k_j of `(24+16*k_j)`. There is no process-lifetime
bound on |J|, caller concurrency or C5 residual referent graphs. A scalar checked
formula refusing overflow is not a new product cap or a certificate that every
reachable invocation sequence fits the integer model.

## Evidence and still-open costs

`all-open-observation-MqMkr75l/{join-unit.log,join-race.log,vet.log}`: selected
unit/race3tests each PASS, no skips; tagged productionruntime/controlstore vet
PASS. The initial author compile under the wrong `spk_expiry_qualification` tag
was not accepted as tagged qualification evidence; both final receipts use the
actual `spec_kitty_expiry_host_qualification` tag. Host author draft compile
failures were preserved separately and corrected before final receipts.

NO allocated-byte/slot-size-class/physical-allocation/escape/stack/ABI/copy-traffic,
GC metadata, error-formatting, caller variadic storage, context/timer/callback,
network/kernel or borrowed referent-size bound is established. Shared/runtime
costs do not become exempt merely because a selected logical recipe exists.
`fullCallCoverage=false`; all four observation deliverables remain OPEN.

Post-reconcile [failure custody](STARTUP_FAILURE_CUSTODY.md) changes three product
files after the accepted native campaigns. Parent targeted unit/race inventory
regressions still find48 syntactic Join sites. The newly reached unpublished-Host
rollback join changes runtime multiplicity despite adding no syntactic Join site.
The separate private wrapper/unwrap slices, primary value, optional appends and
borrowed opaque cause graphs are OWNED/UNQUALIFIED, not covered by24+16*k.
Historical inventories/ELFs are not rewritten or currently native-requalified.
