//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"runtime"
	"testing"
	"unsafe"
)

// Source-value geometry ONLY. This is not an allocation/ABI/copy-instruction,
// traffic, heap, retention, descriptor-ownership or whole-call certificate.
func TestExpiryPathSnapshotSourceValueGeometry(t *testing.T) {
	if runtime.Version() != "go1.26.7" {
		t.Fatal("source-value accounting requires pinned Go1.26.7")
	}
	// Three file pointers (24), three scalar identities (96), and one
	// borrowed string header (16). Identity padding is included, not payload.
	if unsafe.Sizeof(uintptr(0)) != 8 || unsafe.Sizeof(expiryRootIdentity{}) != 32 ||
		unsafe.Offsetof(expiryRootIdentity{}.device) != 0 || unsafe.Offsetof(expiryRootIdentity{}.inode) != 8 ||
		unsafe.Offsetof(expiryRootIdentity{}.mode) != 16 || unsafe.Offsetof(expiryRootIdentity{}.uid) != 20 ||
		unsafe.Offsetof(expiryRootIdentity{}.gid) != 24 {
		t.Fatal("pinned scalar identity layout changed; re-derive source recipe")
	}
	if unsafe.Sizeof(expiryPathSnapshot{}) != 136 ||
		unsafe.Offsetof(expiryPathSnapshot{}.parent) != 0 ||
		unsafe.Offsetof(expiryPathSnapshot{}.directory) != 8 ||
		unsafe.Offsetof(expiryPathSnapshot{}.lock) != 16 ||
		unsafe.Offsetof(expiryPathSnapshot{}.parentIdentity) != 24 ||
		unsafe.Offsetof(expiryPathSnapshot{}.directoryIdentity) != 56 ||
		unsafe.Offsetof(expiryPathSnapshot{}.lockIdentity) != 88 ||
		unsafe.Offsetof(expiryPathSnapshot{}.root) != 120 {
		t.Fatal("pinned snapshot layout changed; re-derive source recipe")
	}

	// These synthetic pointer-only values are never used as OS handles. Fixture
	// storage/strings are outside the helper; no admission or filesystem claim.
	parent, directory, lock := &os.File{}, &os.File{}, &os.File{}
	for _, length := range []int{0, 1, 4096} {
		root := string(make([]byte, length)) // arbitrary borrowed data, NOT a valid path
		l := &expiryHostLease{
			parentDirectory: parent, directory: directory, lock: lock, controlRoot: root,
			parentIdentity:    expiryRootIdentity{1, 2, 3, 4, 5},
			directoryIdentity: expiryRootIdentity{6, 7, 8, 9, 10},
			lockIdentity:      expiryRootIdentity{11, 12, 13, 14, 15},
		}
		l.mu.Lock()
		before := l.snapshotPathsLocked()
		l.mu.Unlock()
		if before.parent != parent || before.directory != directory || before.lock != lock ||
			before.parentIdentity != l.parentIdentity || before.directoryIdentity != l.directoryIdentity ||
			before.lockIdentity != l.lockIdentity || before.root != root {
			t.Fatal("snapshot does not copy all seven value fields exactly")
		}
		if len(root) != 0 && unsafe.StringData(before.root) != unsafe.StringData(root) {
			t.Fatal("root backing was cloned; borrowed-header recipe is no longer applicable")
		}
		// Scalar/header replacement must not mutate an already returned value.
		// This does NOT deep-copy file/string referents or make them immutable.
		l.mu.Lock()
		l.parentDirectory, l.directory, l.lock = nil, nil, nil
		l.parentIdentity, l.directoryIdentity, l.lockIdentity = expiryRootIdentity{}, expiryRootIdentity{}, expiryRootIdentity{}
		l.controlRoot = "replaced"
		l.mu.Unlock()
		if before.parent != parent || before.directory != directory || before.lock != lock ||
			before.parentIdentity.device != 1 || before.directoryIdentity.device != 6 || before.lockIdentity.device != 11 || before.root != root {
			t.Fatal("returned snapshot aliased mutable lease field storage")
		}
	}
}

// Keep the recipe tied to the actual helper, not a second implementation in a
// cost model. New calls, control flow, copies or fields require fresh analysis.
func TestExpiryPathSnapshotSourceConstructorShape(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "expiry_qualification_rundown.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		if f, ok := declaration.(*ast.FuncDecl); ok && f.Name.Name == "snapshotPathsLocked" {
			if function != nil {
				t.Fatal("ambiguous snapshot helper")
			}
			function = f
		}
	}
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		t.Fatal("snapshot helper is no longer one direct value return")
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		t.Fatal("snapshot helper return shape changed")
	}
	literal, ok := returned.Results[0].(*ast.CompositeLit)
	if !ok {
		t.Fatal("snapshot helper is no longer a direct struct value")
	}
	typeName, ok := literal.Type.(*ast.Ident)
	if !ok || typeName.Name != "expiryPathSnapshot" {
		t.Fatal("snapshot value type changed")
	}
	fields := []string{"parentDirectory", "directory", "lock", "parentIdentity", "directoryIdentity", "lockIdentity", "controlRoot"}
	if len(literal.Elts) != len(fields) {
		t.Fatal("snapshot field census changed")
	}
	for i, field := range fields {
		selector, ok := literal.Elts[i].(*ast.SelectorExpr)
		if !ok {
			t.Fatalf("field %s is no longer a direct borrowed/scalar field", field)
		}
		receiver, ok := selector.X.(*ast.Ident)
		if !ok || receiver.Name != "l" || selector.Sel.Name != field {
			t.Fatalf("snapshot field %d source changed", i)
		}
	}
}
