//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

func TestExpiryHostRecoveryMethodSetMasksBothOptionalAccelerators(t *testing.T) {
	adapter := new(storegapdb.Adapter)
	if _, ok := any(adapter).(journalrestore.ReadManyReaderV2); !ok {
		t.Fatal("control no longer has ReadMany")
	}
	if _, ok := any(adapter).(journalrestore.RecoverySnapshotReaderV2); !ok {
		t.Fatal("control no longer has Snapshot")
	}
	var masked journalrestore.ReaderV2 = expiryRecoveryReaderV2{ReaderV2: adapter}
	if _, ok := masked.(journalrestore.ReadManyReaderV2); ok {
		t.Fatal("private root leaked ReadMany")
	}
	if _, ok := masked.(journalrestore.RecoverySnapshotReaderV2); ok {
		t.Fatal("private root leaked Snapshot")
	}
	if recoveryReaderForModeV2(adapter, true) != adapter {
		t.Fatal("old true mapping changed")
	}
	ordinary := recoveryReaderForModeV2(adapter, false)
	if _, ok := ordinary.(journalrestore.ReadManyReaderV2); ok {
		t.Fatal("old false mapping widened")
	}
	if _, ok := ordinary.(journalrestore.RecoverySnapshotReaderV2); ok {
		t.Fatal("old false mapping widened")
	}
}

func TestExpiryHostRecoveryFourSharedEdgesCaptureIdenticalReader(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "composition.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var suffix *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "completeHostStartup" {
			suffix = fn
		}
	}
	if suffix == nil {
		t.Fatal("missing full suffix")
	}
	calls := 0
	ast.Inspect(suffix.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "RestoreV2" {
			return true
		}
		calls++
		if len(call.Args) != 2 {
			t.Fatal("unexpected Restore signature")
		}
		input, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			t.Fatal("Restore input no longer exact")
		}
		readers := 0
		for _, element := range input.Elts {
			if kv, ok := element.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Reader" {
					readers++
					value, ok := kv.Value.(*ast.Ident)
					if !ok || value.Name != "recoveryReader" {
						t.Fatal("Restore reader widened or replaced")
					}
				}
			}
		}
		if readers != 1 {
			t.Fatal("missing original captured reader")
		}
		return true
	})
	if calls != 4 {
		t.Fatalf("restore edge count=%d; new edge requires review", calls)
	}
}

// Entire source-bound capability graph is pinned independently of the root.
// Changes require re-review, not a permission to update these hashes blindly.
func TestExpiryHostRecoveryTransitiveCapabilityGraphDrift(t *testing.T) {
	pins := map[string]string{
		"mission":     "5efd813d097e41fd6902bdafdb6bb099ca28a64ce5569c18a45d6e65b6d2d52f",
		"execution":   "4841f06d42cf6abc7b33099469b79e9d299b9b74f185487faab0c32b02e01c0c",
		"effect":      "137af6a63aa9606d6adf3599414dee19bb6f786e6f05a9b11c348cd6e46b08e3",
		"workspace":   "1fb9af2a13d0ca094fada3fd079b6880203b41b87e7fa88a07d3c276c39a495a",
		"handoff":     "6ac4b553bc533fb7f75f9264477bda59f98c1d86eeb36ef3cf76dc8c8603d197",
		"review":      "4f55810cbce3ea73a96861f61278d7c001de3d33e9702c9a52dca42c15fcc32e",
		"acceptance":  "a33ea15debde42d3547a696ffe045befdb3d7fccdfaa11afb06f702bd280122c",
		"integration": "5cfb6103bbba66153a4ce9d74f1fd3d686e7800f358bee38da7e7af37489f8d5",
		"artifact":    "1b847755de601de6220d37c2d6b6c5f3124ebf9db47452868afac29452437059",
		"workgraph":   "20b7d279ef26772284ba5bf4a60fb92e1743c4efdba504cd72832c6daae07d3b",
	}
	for name, want := range pins {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "controlstore", "gapdb", name+"_authority.go")
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != want {
				t.Fatal("capability recovery graph drift; requires source review")
			}
		})
	}
}
