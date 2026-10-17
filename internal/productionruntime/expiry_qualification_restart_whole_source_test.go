//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"os"
	"reflect"
	"testing"
)

const expiryCompatBasePath = "/home/user/.cache/spec-kitty-source-proposals/expiry-bootstrap-four-helper-contracts-20261017-v2/base/original-pre-C4a0-expiry_qualification.go.txt"
const expiryCompatBaseSHA256 = "4a0bd522a2084cbc10affa5b70a5f79c66eda03af228cc8b74e2d546fc0919ad"

func expiryCompatReadBase() ([]byte, error) {
	raw, err := os.ReadFile(expiryCompatBasePath)
	if err != nil { return nil, err }
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != expiryCompatBaseSHA256 {
		return nil, fmt.Errorf("immutable extraction base drift: SHA256=%s want=%s", got, expiryCompatBaseSHA256)
	}
	return raw, nil
}

// Exact closed-file AST delta: no added declarations/imports/init/provider,
// no other changed functions, no removed original guards. The immutable b4
// base is NOT the active tree. The only admitted replacements are literal full
// combined startGeneration/unchanged approved restart and four literal helpers,
// immediately before expiryIdentityValid in the prescribed declaration order.
// This is source assertion CODE, UNRUN; not a build/type/Native check.
func expiryCompatOnlyExtraction(source string) error {
	baseBytes, err := expiryCompatReadBase()
	if err != nil { return err }
	base, err := expiryCompatParse(string(baseBytes))
	if err != nil { return err }
	got, err := expiryCompatParse(source)
	if err != nil { return err }
	contracts, err := expiryCompatParse("package productionruntime\n" + expiryCompatStartGeneration + "\n" + expiryCompatRestart + "\n" + expiryCompatBootstrapHelper + "\n" + expiryCompatBootstrapZeroHelper + "\n" + expiryCompatFreshnessHelper + "\n" + expiryCompatCleanupHelper)
	if err != nil { return err }
	start, err := expiryCompatFunction(contracts, "startGeneration"); if err != nil { return err }
	restart, err := expiryCompatFunction(contracts, "restart"); if err != nil { return err }
	bootstrap, err := expiryCompatFunction(contracts, "expiryBootstrapRefused"); if err != nil { return err }
	zero, err := expiryCompatFunction(contracts, "expiryBootstrapNotZero"); if err != nil { return err }
	freshness, err := expiryCompatFunction(contracts, "expiryRestartFreshnessRefused"); if err != nil { return err }
	cleanup, err := expiryCompatFunction(contracts, "expiryRestartCleanupUnion"); if err != nil { return err }
	// Require original declarations to exist once before authoring the expected
	// AST in memory; no disk materialization or source emitter is involved.
	for _, name := range []string{"startGeneration", "restart", "expiryIdentityValid"} {
		if _, err := expiryCompatFunction(base, name); err != nil { return err }
	}
	declarations := make([]ast.Decl, 0, len(base.Decls)+4)
	for _, decl := range base.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			switch fn.Name.Name {
			case "startGeneration": decl = start
			case "restart": decl = restart
			case "expiryIdentityValid": declarations = append(declarations, bootstrap, zero, freshness, cleanup)
			}
		}
		declarations = append(declarations, decl)
	}
	base.Decls = declarations
	// FileStart/FileEnd are offsets only; unchanged import/unresolved/name
	// declarations remain part of the structural comparison.
	if !expiryCompatASTEqual(reflect.ValueOf(got), reflect.ValueOf(base)) {
		return fmt.Errorf("product AST changed beyond exact combined four-helper extraction")
	}
	return nil
}

func TestExpiryProposalRestartCompatibilityWholeSourceMutants(t *testing.T) {
	candidate := expiryCompatReadSelected(t)
	if err := expiryCompatOnlyExtraction(candidate); err != nil { t.Fatal("whole actual selected source control", err) }
	base, err := expiryCompatReadBase()
	if err != nil { t.Fatal(err) }
	expiryCompatMutantRejected(t, string(base), expiryCompatOnlyExtraction)
	for _, mutation := range []struct{name, suffix string}{
		{"extra_RPC_in_new_function", "\nfunc expiryUnregisteredRPC() { adapter.Get(ctx, runtimeCurrentKey) }\n"},
		{"extra_hash_in_new_function", "\nfunc expiryUnregisteredHash() { digestFile() }\n"},
		{"extra_factory_in_init", "\nfunc init() { providerFactory() }\n"},
		{"extra_factory_global", "\nvar expiryUnregisteredProvider = providerFactory()\n"},
		{"extra_helper_use", "\nfunc expiryUnregisteredHelperUse() { expiryRestartCleanupUnion(nil, nil, nil, nil) }\n"},
	} {
		t.Run(mutation.name, func(t *testing.T) { expiryCompatMutantRejected(t, candidate+mutation.suffix, expiryCompatOnlyExtraction) })
	}
}
