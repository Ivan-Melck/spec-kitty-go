//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"crypto/sha256"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Supplemental complete-byte contract, never an alternative to the composed
// original-base AST contract. BOTH this proof and all mutants read through the
// same actual-selected-source binding. os.ReadFile does not follow overlays.
// Normal/overlay groups replace that ONE binding file; no candidate fallback.
const expiryBootstrapDraftBasePath = "/home/user/.cache/spec-kitty-source-proposals/expiry-bootstrap-four-helper-contracts-20261017-v2/base/restart-approved-expiry_qualification.go.txt"
const expiryBootstrapDraftBaseSHA = "3c43daecf63a4a24bf0dcf29c1e4a59be1387663b4c206afe81206e2fb51080c"

const expiryBootstrapDraftInline = `if p.DatabaseID == "" || p.DataDirectory != l.controlRoot || p.SupervisorRunID != supervisor || p.ExecutedImage.SHA256 != r.identity.ExecutableSHA256 ||
		p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady || p.Observation.Status.OwnerPID != p.Process.PID || p.Observation.Status.DatabaseID != p.DatabaseID {`
const expiryBootstrapDraftInlineZero = `if initial && (p.Observation.Status.CurrentRevision != 0 || p.Observation.Status.DurableThroughRevision != 0 || p.Observation.Status.RecordCount != 0) {`
const expiryBootstrapDraftDefinitions = `// DRAFT PENDING DESIGN REVIEW: pure extraction of the original bootstrap
// refusal. Inputs are typed value copies, never authenticated provenance by
// construction. The caller retains lifecycle ownership and exact error policy.
func expiryBootstrapRefused(p sidecargapdb.SidecarProvenance, controlRoot, supervisor string, identity sidecargapdb.ExpiryTechnicalIdentity) bool {
	return p.DatabaseID == "" || p.DataDirectory != controlRoot || p.SupervisorRunID != supervisor || p.ExecutedImage.SHA256 != identity.ExecutableSHA256 ||
		p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady || p.Observation.Status.OwnerPID != p.Process.PID || p.Observation.Status.DatabaseID != p.DatabaseID
}

// initial comes from the original canonical controller kind, not caller input.
// Successors deliberately retain the original allowance for nonzero status.
func expiryBootstrapNotZero(initial bool, p sidecargapdb.SidecarProvenance) bool {
	return initial && (p.Observation.Status.CurrentRevision != 0 || p.Observation.Status.DurableThroughRevision != 0 || p.Observation.Status.RecordCount != 0)
}

`

func expiryBootstrapDraftRead(t *testing.T, path, digest string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != digest { t.Fatalf("source drift %s: %s want=%s", path, got, digest) }
	return string(raw)
}

func expiryBootstrapDraftReplace(source, old, next string) (string, error) {
	if strings.Count(source, old) != 1 { return "", fmt.Errorf("nonunique source anchor: %q", old) }
	return strings.Replace(source, old, next, 1), nil
}

func expiryBootstrapDraftExpected(base string) (string, error) {
	for _, change := range []struct{old, next string}{
		{expiryBootstrapDraftInline, "if expiryBootstrapRefused(p, l.controlRoot, supervisor, r.identity) {"},
		{expiryBootstrapDraftInlineZero, "if expiryBootstrapNotZero(initial, p) {"},
		{"// Pure private extraction of the existing post-readCurrentEpoch refusal only.", expiryBootstrapDraftDefinitions+"// Pure private extraction of the existing post-readCurrentEpoch refusal only."},
	} {
		var err error
		base, err = expiryBootstrapDraftReplace(base, change.old, change.next)
		if err != nil { return "", err }
	}
	return base, nil
}

// A complete-byte delta contract is stricter than AST equality here. It keeps
// exact stage/error fields and both authenticated Get branches untouched. These
// predicates grant no authority and do not replace those original Get calls.
func TestExpiryBootstrapDraftExactSourceAndConjunctMutants(t *testing.T) {
	base := expiryBootstrapDraftRead(t, expiryBootstrapDraftBasePath, expiryBootstrapDraftBaseSHA)
	expected, err := expiryBootstrapDraftExpected(base)
	if err != nil { t.Fatal(err) }
	selected := expiryCompatReadSelected(t)
	if selected != expected { t.Fatal("bootstrap changed beyond exact two predicates") }
	atoms := []string{
		`p.DatabaseID == ""`, "p.DataDirectory != controlRoot", "p.SupervisorRunID != supervisor", "p.ExecutedImage.SHA256 != identity.ExecutableSHA256",
		"p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady", "p.Observation.Status.OwnerPID != p.Process.PID", "p.Observation.Status.DatabaseID != p.DatabaseID",
		"p.Observation.Status.CurrentRevision != 0", "p.Observation.Status.DurableThroughRevision != 0", "p.Observation.Status.RecordCount != 0",
	}
	for _, atom := range atoms {
		for _, mode := range []string{"omit", "invert"} {
			t.Run(mode+"/"+atom, func(t *testing.T) {
				replacement := "false"
				if mode == "invert" {
					replacement = strings.Replace(atom, " != ", " == ", 1)
					if replacement == atom { replacement = strings.Replace(atom, " == ", " != ", 1) }
				}
				mutant, err := expiryBootstrapDraftReplace(selected, atom, replacement)
				if err != nil { t.Fatal(err) }
				if _, err := parser.ParseFile(token.NewFileSet(), "bootstrap-mutant.go", mutant, parser.SkipObjectResolution); err != nil { t.Fatal("mutant must parse", err) }
				if mutant == expected { t.Fatal("one-conjunct mutant escaped exact source contract") }
			})
		}
	}
	for _, change := range []struct{name, old, next string}{
		{"initial_gate_omitted", "return initial && (", "return ("},
		{"initial_gate_inverted", "return initial && (", "return !initial && ("},
		{"wrong_error_stage", `expiryFailure("expiry_bootstrap_not_zero")`, `expiryFailure("expiry_bootstrap")`},
		{"wrong_call_arguments", "expiryBootstrapRefused(p, l.controlRoot, supervisor, r.identity)", "expiryBootstrapRefused(p, supervisor, l.controlRoot, r.identity)"},
	} {
		t.Run(change.name, func(t *testing.T) {
			mutant, err := expiryBootstrapDraftReplace(selected, change.old, change.next)
			if err != nil { t.Fatal(err) }
			if _, err := parser.ParseFile(token.NewFileSet(), "bootstrap-mutant.go", mutant, parser.SkipObjectResolution); err != nil { t.Fatal(err) }
			if mutant == expected { t.Fatal("mutant escaped exact source contract") }
		})
	}
}

