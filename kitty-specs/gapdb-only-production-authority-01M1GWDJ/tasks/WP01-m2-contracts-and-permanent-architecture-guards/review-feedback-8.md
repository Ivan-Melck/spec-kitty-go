# WP01 Post-Approval Defect 2 Review Cycle 8 — Changes Requested

Reviewed repair: `be233e38fe30f29f5a7ae7235f719f2cef9fdc61`

## Blocker: exported callable type declarations evade signature inspection

The repair correctly made method inspection semantic and rename-resistant, but
the exported `TypeName` branch checks only method sets and underlying struct
fields. It does not inspect the declared type itself as a possible function
signature.

Both of these independent overlay probes escaped:

```go
type Parcel struct{}
type Fetch func(journalv2.ReadRequestV2) (Parcel, error)
```

and:

```go
type Parcel struct{}
type Fetch = func(journalv2.ReadRequestV2) (Parcel, error)
```

Each is the same forbidden adapter-owned recovery side contract under another
Go declaration form.

Evidence: `internal/architecture/m2_registry_test.go`; the signature inspector
near lines 592–617 is not applied to `object.Type()` in the exported
`*types.TypeName` branch near lines 635–660 of the reviewed repair.

## Required bounded repair

1. For every exported `TypeName`, inspect `object.Type()` itself as a possible
   callable signature before inspecting method sets and fields.
2. Reject both named-function and alias-function recovery/publication seams when
   they combine foundation vocabulary with adapter/provider-owned types.
3. Add both exact mutants to the sealed executed census, digest,
   fixture-binding, deletion, and substitution controls.
4. Preserve unrelated exported-type, unrelated public-method, and private-helper
   positive controls and every prior 103 mutant.

## Passing scope

The prior valid-`ReadV2` plus renamed-`Fetch`/`Parcel` bypass is closed, the 103
mutant census is honest, and all key/tuple/digest/dependency/reachability laws
remain green. Focused x3, race, full architecture, helper architecture-check,
repository compile, vet, staticcheck, formatting, and diff gates passed.
Temporary probes were removed.
