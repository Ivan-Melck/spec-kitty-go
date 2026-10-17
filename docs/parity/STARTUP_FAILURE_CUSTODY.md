# Post-reconcile startup failure custody

**IMPLEMENTED; bounded actual-source review APPROVED. Fresh genuine native
recovery-fault evidence still required. All original areas remain OPEN.**

## Observed source gap, not a demonstrated false PASS

At `composition.go`'s post-reconciliation Restore failure, startup already returns
`ErrorStartup / HostDegraded / reconciliation_restore`. It nevertheless discards
the actual reader error and the first internal Host.Close return. The unpublished
Host does not yet have `host.expiry.run`, so the existing first-Host recorder cannot
associate that Close with the construction candidate. Owner-first recording still
works. Current failure propagation prevents a demonstrated whole-Host false PASS;
that fact does not supply the missing first-return evidence.

Independent direct-Codex design review `90eb318b-b304-4f1` identified the gap and
conditionally accepted failure preservation using existing invocation sites.
An earlier claim that USER scope prohibited shared-suffix edits was corrected:
that restriction came from an internal historical C5 design proposal, not a
verified user authorization. Preserve the raw review and correction; do not
invent either a user prohibition or a user waiver.

## Implemented narrow change

1. Capture the existing internal Host.Close exactly once, in its existing order
   and with its existing context. A private error wrapper keeps the exact old
   outward diagnostic while retaining the primary runtime classifier, original
   restore error and nonnil actual Close error through `Unwrap() []error`.
   Dynamic error type/traversal changes; public text and admission do not.
   Standard `errors.As`/`errors.Is` behavior must be tested. Do not claim the
   journal's custom IsKind automatically traverses multi-error unwraps.
2. Reuse the **existing** E `Host.expiryClosed` invocation. Only an unpublished
   Host may recover the exact existing production-owner/run association, with
   canonical lease/controller/candidate/stage and pointer consistency checks.
   Never attach authority early, add a callback/factory/selector, change entry
   revocation order or replace actual error custody with a derived later nil.
3. C5 rollback must consume an already recorded Host outcome even when `r.host`
   is nil, while still requiring recording for an installed Host. A candidate
   that never constructed a Host is a different valid case.

Canonical association checks do not mint independent composition provenance.
Synchronous construction/Close and constructor join before rollback provide the
publication ordering; lease.mu alone cannot publish earlier unguarded writes.

## Bounded verification and remaining gates

Direct-Codex reviewer `387dadc4-ed99-4d4` read all six actual source/test files,
Host.Close/owner/controller/C5 contracts and all four first-attempt sanitized
Go1.26.7 normal/E unit/race logs. It approved the bounded fix and models, not
native execution or whole-area completion. Implementer evidence:
`host-error-custody-p4v1d7mw/` (incremental patch SHA
`ca006690d356d448f3609c27946c969dfbadb0410592d32ed65a5eb74de253fe`).

Current product source SHA-256:

| File under internal/productionruntime | SHA-256 |
|---|---|
| composition.go | a5e7907a293240f72301b714b666594d6341002cc173438c1f70a1cff81e1b61 |
| expiry_qualification.go | 4a0bd522a2084cbc10affa5b70a5f79c66eda03af228cc8b74e2d546fc0919ad |
| expiry_qualification_rundown.go | 134f19cf62e9cfa2a884c66ef485b75ca695d0f8d4ac10f960926ccef49a94bd |

Parent provider-free integration also passed: targeted C45/recovery/Join/binding/
normal-root/model unit and race; normal-root/record boundaries; five-profile
architecture guard; tagged vet. Five commands and before/after six-file pins:
`host-custody-parent-regressions-sy93gbx1/`. No broad-suite green is implied.

The reviewer identified a MODEL-only evidence limit: the first residual fixture
used TempDir/deferred disposal and therefore lost unexpected failure paths and
post-assertion FD custody. Parent preserved that version and changed only the
fixture: fresh private cache parent remains, and the actual existing residual
chain retains three CLOEXEC root descriptors until the MODEL process exits.
No RemoveAll, manual fixture retirement, fake counters or forced GC. Unit/race
passed, and the same reviewer approved the correction. Corrected tagged model
SHA: `627d0650d9ae6f51dd9d8a4ba5120d81bd708de030fd189bed2efbe071cbd615`;
evidence `host-custody-model-retention-correction-q29aa6du/`.
This is still neither authenticated Host nor native/process-exit cleanup evidence.

Models cover first nil/error, non-repair by repeat Close, invalid associations,
C5 union/residual handling, exact outward text and preserved cause identities.
Successful handoff and ordinary admission are source-regression checked, not
newly native-requalified. No opted-in native execution is authorized by these
model greens or this document.

Fresh genuine recovery controls additionally need source/AST/frame-pinned real
context barriers at timeout construction and the post-failed-attempt Err check;
unchanged policies; explicit resume; original live process/socket identity;
restoration before first teardown; and actual outward reader-error identity.
The nested request timeout is5s, so merely finishing within Restore's30s budget
cannot exclude timeout-derived negatives. Constructor decomposition must preserve
all root-prefix/rollback steps and must not be called public Start coverage.

## Prior evidence and current-source accounting

The accepted preflight33/native6 campaign and its final archive are immutable.
`host-post-native-source-compat-teqtuoyo/manifest.json` records the **pre-fix**
checkpoint:589 original Go files (267 non-test), SDK400 unchanged; two separately
reviewed analyzer/test Python deltas and one uncompiled pair-resource test addition.
That snapshot does not assert byte compatibility after these three product edits.

Any implemented suffix change needs explicit old/new source reconciliation and
independent review before relying on historical observations for current code.
Do not replay/requalify accepted native13/13, release5/5 or frontier6/6 campaigns,
rewrite their manifests, or infer a whole-area verdict from unchanged success paths.
New retention/allocation costs are **OWNED/UNQUALIFIED**: wrapper with primary
runtime Error by value and two borrowed error interfaces; primary/cause aliasing;
returned unwrap slice and optional appends; newly reached existing rollback
error-join work; and strongly retained opaque cause/referent graphs. Actual branch
unwrap length is2 or3; no allocation/capacity/physical-heap claim follows. The
syntactic Join inventory remains48 (parent targeted inventory regression passed),
not a bound on runtime calls. No full heap, ABI, library, finalizer or physical-memory
qualification; no finite aggregate failed-attempt bound or fullCallCoverage claim.

Verified current binding/archive:
`host-custody-current-source-bind-nqh35tp_/manifest.json`.
Of2036 historical original/snapshot entries,2031 remain identical; the exact five
deltas are the three product Go files plus two previously reviewed repository
Python analyzer/test edits.586/589 original Go and264/267 non-test Go are unchanged;
SDK400 unchanged; four additive test files (pair-resource plus three model files),
no removals. The archive byte-verifies current six source/test files, original
implementation and corrected model evidence, parent regressions, complete direct
reviews including the scope-citation correction, and document snapshots.
This is explicit source reconciliation, **not native reexecution/requalification**.
