//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"unsafe"
)

// This is a source-request recipe, not an allocated-heap/ABI/copy-traffic bound.
// Error referents and the caller's variadic storage are NOT included here.
// In particular, no Error method is invoked or referent graph traversed.
type expiryJoinRequest struct {
	records, interfaces, logicalBytes uint64
}

func expiryJoinSourceRequest(nonNil uint64) (expiryJoinRequest, bool) {
	if nonNil == 0 {
		return expiryJoinRequest{}, true
	}
	const record, slot = uint64(24), uint64(16)
	if nonNil > (math.MaxUint64-record)/slot {
		return expiryJoinRequest{}, false
	}
	return expiryJoinRequest{1, nonNil, record + slot*nonNil}, true
}

type expiryJoinBorrowedError struct{ calls *int }

func (e *expiryJoinBorrowedError) Error() string {
	panic("Join must not format a borrowed error, including a typed nil")
}

func TestExpiryJoinAccountingPinnedSource(t *testing.T) {
	if runtime.Version() != "go1.26.7" {
		t.Skip("source-request recipe requires the pinned Go1.26.7 source")
	}
	source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "src", "errors", "join.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != "87da7c120f71d17e37f110ae1dc6af6493ec841a22ee825e13a6a8024a014dba" {
		t.Fatalf("review the errors.Join recipe after toolchain source drift: %s", got)
	}
	joined := errors.Join(&expiryJoinBorrowedError{})
	typ := reflect.TypeOf(joined)
	if typ.Kind() != reflect.Pointer || typ.Elem().PkgPath() != "errors" || typ.Elem().Name() != "joinError" {
		t.Fatal("pinned Join result type changed")
	}
	record := typ.Elem()
	if record.Size() != 24 || record.NumField() != 1 || record.Field(0).Offset != 0 || record.Field(0).Type != reflect.TypeFor[[]error]() || unsafe.Sizeof(error(nil)) != 16 {
		t.Fatal("pinned logical record/interface geometry changed")
	}
}

func TestExpiryJoinAccountingNilFilteringAndBorrowedReferents(t *testing.T) {
	left, right := &expiryJoinBorrowedError{}, &expiryJoinBorrowedError{}
	var typedNil *expiryJoinBorrowedError
	for _, input := range [][]error{
		nil, {nil}, {nil, nil}, {left}, {left, nil, right}, {typedNil}, {nil, typedNil, right},
	} {
		var want []error
		for _, err := range input {
			if err != nil {
				want = append(want, err)
			}
		}
		model, ok := expiryJoinSourceRequest(uint64(len(want)))
		if !ok {
			t.Fatal("small model overflow")
		}
		joined := errors.Join(input...)
		if len(want) == 0 {
			if joined != nil || model != (expiryJoinRequest{}) {
				t.Fatal("all-nil Join requested a result")
			}
			continue
		}
		unwrap, ok := joined.(interface{ Unwrap() []error })
		if !ok {
			t.Fatal("missing source-selected unwrap")
		}
		got := unwrap.Unwrap()
		if len(got) != len(want) || cap(got) != len(want) || model.records != 1 || model.interfaces != uint64(len(want)) || model.logicalBytes != 24+16*uint64(len(want)) {
			t.Fatal("nil filter or exact source backing request differs")
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatal("borrowed referent/order changed")
			}
		}
		// Input interface slots are copied into fresh backing; referents are not.
		for i := range input {
			input[i] = nil
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatal("result aliases the caller's variadic backing")
			}
		}
	}
	inner := errors.Join(left, right)
	outer := errors.Join(inner)
	if outer == inner || len(outer.(interface{ Unwrap() []error }).Unwrap()) != 1 || outer.(interface{ Unwrap() []error }).Unwrap()[0] != inner {
		t.Fatal("pinned source must create a distinct wrapper, not flatten/deduplicate")
	}
	// Two events: (24+2*16) + (24+1*16), not one deduplicated graph.
	a, _ := expiryJoinSourceRequest(2)
	b, _ := expiryJoinSourceRequest(1)
	if a.logicalBytes+b.logicalBytes != 96 {
		t.Fatal("nested request multiplicity")
	}
	max := (uint64(math.MaxUint64) - 24) / 16
	if _, ok := expiryJoinSourceRequest(max); !ok {
		t.Fatal("largest representable request rejected")
	}
	for _, n := range []uint64{max + 1, math.MaxUint64} {
		if got, ok := expiryJoinSourceRequest(n); ok || got != (expiryJoinRequest{}) {
			t.Fatal("overflow must refuse, not wrap")
		}
	}
}

func TestExpiryJoinAccountingSourceCallsiteInventory(t *testing.T) {
	// The inventory is live source evidence, not an execution count. Loops,
	// concurrent callers and error graphs remain symbolic/unbounded.
	patterns := []string{"expiry_qualification*.go", "../sidecar/gapdb/expiry_qualification*.go"}
	count := 0
	for _, pattern := range patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) == 0 {
			t.Fatal("missing source inventory")
		}
		for _, path := range paths {
			if len(path) >= 8 && path[len(path)-8:] == "_test.go" {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, path, raw, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Join" {
						return true
					}
					pkg, ok := sel.X.(*ast.Ident)
					if !ok || pkg.Name != "errors" {
						return true
					}
					count++
					t.Logf("join source site=%s caller=%s operand_expressions=%d variadic=%t source_sha256=%x", fs.Position(call.Pos()), fn.Name.Name, len(call.Args), call.Ellipsis.IsValid(), sha256.Sum256(raw))
					return true
				})
			}
		}
	}
	if count == 0 {
		t.Fatal("empty source Join inventory")
	}
	t.Logf("source Join sites=%d; event multiplicity NOT bounded; fullCallCoverage=false", count)
}
