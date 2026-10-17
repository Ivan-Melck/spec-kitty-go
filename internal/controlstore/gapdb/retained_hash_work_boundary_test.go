package gapdb

// Ordinary provider-free planner and source-order tests. No ACK constructor,
// provider fixture, structural Host, native process or positive readback credit.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"strings"
	"testing"
)

func TestRetainedHashWorkBoundaryActualPlanner(t *testing.T) {
	const image uint64 = 5327126
	const ceiling uint64 = 34359738368
	if retainedPublicationLimitV2 != 256 || retainedRecordLimitV2 != 4096 || retainedObservationPrivateRows != 195 || retainedObservationCalls != 4294 || retainedObservationHashes != ceiling {
		t.Fatal("existing caps/reservation changed")
	}
	for _, tc := range []struct {
		name                  string
		image, records, calls uint64
		ok                    bool
	}{
		{"pinned1951", image, 1951, 2149, true},
		{"pinned1952", image, 1952, 0, false},
		{"zero_image", 0, 1951, 0, false},
		{"zero_rows", image, 0, 198, true},
		{"both_zero", 0, 0, 0, false},
		{"one_byte_full_rows", 1, 4096, 4294, true},
		{"record_cap_plus_one", 1, 4097, 0, false},
		{"image_overflow", math.MaxUint64, 1951, 0, false},
		{"rows_overflow", 1, math.MaxUint64, 0, false},
		{"both_overflow", math.MaxUint64, math.MaxUint64, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, ok := retainedObservationWork(tc.image, tc.records)
			if calls != tc.calls || ok != tc.ok {
				t.Fatalf("actual private planner=(%d,%v) want (%d,%v)", calls, ok, tc.calls, tc.ok)
			}
		})
	}
	if image*6448 != 34349308448 || image*6451 != 34365289826 || ceiling/image != 6449 || ((ceiling/image-1)/3)-198 != 1951 {
		t.Fatal("exact reservation arithmetic")
	}
	for _, rows := range []uint64{0, 1, 1951, 1952, 4096} {
		calls := rows + 198
		hashes := 3*calls + 1
		maxImage := ceiling / hashes
		if got, ok := retainedObservationWork(maxImage, rows); !ok || got != calls {
			t.Fatal("division exact boundary refused", rows)
		}
		if got, ok := retainedObservationWork(maxImage+1, rows); ok || got != 0 {
			t.Fatal("division plus one accepted", rows)
		}
	}
	// Nominal arithmetic ONLY, no instrumentation or actual consumption claim.
	if (3*(uint64(1951)+6+3)+1)*image != 31328828006 {
		t.Fatal("private-six actual-path nominal arithmetic")
	}
}

type retainedHashWorkSource struct {
	text string
	fn   *ast.FuncDecl
}

func retainedHashWorkReadSource(t *testing.T, path, name string) retainedHashWorkSource {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, path, body, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == name {
			return retainedHashWorkSource{string(body[fs.Position(f.Pos()).Offset:fs.Position(f.End()).Offset]), f}
		}
	}
	t.Fatalf("missing source function %s", name)
	return retainedHashWorkSource{}
}
func retainedHashWorkCalls(f *ast.FuncDecl, name string) []token.Pos {
	var positions []token.Pos
	ast.Inspect(f.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			var got string
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				got = fun.Name
			case *ast.SelectorExpr:
				got = fun.Sel.Name
			}
			if got == name {
				positions = append(positions, call.Pos())
			}
		}
		return true
	})
	return positions
}
func retainedHashWorkCall(t *testing.T, f *ast.FuncDecl, name string) token.Pos {
	t.Helper()
	calls := retainedHashWorkCalls(f, name)
	if len(calls) != 1 {
		t.Fatalf("need exactly one %s call, got %d", name, len(calls))
	}
	return calls[0]
}
func retainedHashWorkBefore(t *testing.T, positions ...token.Pos) {
	t.Helper()
	for i := 1; i < len(positions); i++ {
		if positions[i-1] >= positions[i] {
			t.Fatal("source call order violated")
		}
	}
}
func retainedHashWorkReturningGuard(t *testing.T, f *ast.FuncDecl, index int) *ast.IfStmt {
	t.Helper()
	guard, ok := f.Body.List[index].(*ast.IfStmt)
	if !ok || guard.Else != nil || len(guard.Body.List) != 1 {
		t.Fatal("expected direct early refusal guard")
	}
	if _, ok := guard.Body.List[0].(*ast.ReturnStmt); !ok {
		t.Fatal("guard no longer returns immediately")
	}
	return guard
}
func TestRetainedHashWorkBoundarySourceOrder(t *testing.T) {
	prepare := retainedHashWorkReadSource(t, "retained_observation.go", "PrepareRetainedObservationV2")
	guard := retainedHashWorkReturningGuard(t, prepare.fn, 0)
	for _, fragment := range []string{"parent == nil || len(acknowledgements) > retainedPublicationLimitV2 || a == nil", "planned, valid := retainedObservationWork(image, measured.records)", "if !valid || parent.Err() != nil"} {
		if !strings.Contains(prepare.text, fragment) {
			t.Fatal("missing scalar/work gate", fragment)
		}
	}
	copyPos := retainedHashWorkCall(t, prepare.fn, "copy")
	errs := retainedHashWorkCalls(prepare.fn, "Err")
	if len(errs) != 2 {
		t.Fatal("context gate changed")
	}
	preflight := retainedHashWorkCall(t, prepare.fn, "preflightRetainedPublicationsV2")
	work := retainedHashWorkCall(t, prepare.fn, "retainedObservationWork")
	clone := retainedHashWorkCall(t, prepare.fn, "copyRetainedPublicationMapV2")
	retainedHashWorkBefore(t, guard.End(), copyPos, errs[0], preflight, retainedHashWorkCall(t, prepare.fn, "ImageSize"), work, errs[1], retainedHashWorkCall(t, prepare.fn, "WithTimeout"), clone)
	// Work rejection must actually return before timeout/map construction, not
	// merely compute the budget earlier and ignore it.
	var workGuard *ast.IfStmt
	for _, stmt := range prepare.fn.Body.List {
		if g, ok := stmt.(*ast.IfStmt); ok && g.Pos() > work && g.End() < clone {
			if len(g.Body.List) == 1 {
				if _, ok := g.Body.List[0].(*ast.ReturnStmt); ok {
					workGuard = g
					break
				}
			}
		}
	}
	if workGuard == nil {
		t.Fatal("work gate no longer directly refuses before map copies")
	}
	for _, name := range []string{"Records", "CanonicalValue", "v2KeyForSealedRecord", "get", "status", "Get", "Status", "ReadPublications"} {
		if len(retainedHashWorkCalls(prepare.fn, name)) != 0 {
			t.Fatal("payload/provider work moved into Prepare", name)
		}
	}

	build := retainedHashWorkReadSource(t, "retained_publication_map.go", "buildRetainedPublicationMapV2")
	early := retainedHashWorkReturningGuard(t, build.fn, 0)
	if !strings.Contains(build.text, "ctx == nil || len(acknowledgements) > retainedPublicationLimitV2") {
		t.Fatal("map scalar limit removed")
	}
	retainedHashWorkBefore(t, early.End(), retainedHashWorkCall(t, build.fn, "copy"), retainedHashWorkCall(t, build.fn, "preflightRetainedPublicationsV2"), retainedHashWorkCall(t, build.fn, "copyRetainedPublicationMapV2"))
	pre := retainedHashWorkReadSource(t, "retained_publication_map.go", "preflightRetainedPublicationsV2")
	for _, name := range []string{"Records", "CanonicalValue", "v2KeyForSealedRecord", "copyRetainedPublicationMapV2"} {
		if len(retainedHashWorkCalls(pre.fn, name)) != 0 {
			t.Fatal("clone/key work in scalar preflight", name)
		}
	}
	retainedHashWorkBefore(t, retainedHashWorkCall(t, pre.fn, "MeasureRecordFootprintV2"), retainedHashWorkCall(t, pre.fn, "charge"))
	cloned := retainedHashWorkReadSource(t, "retained_publication_map.go", "copyRetainedPublicationMapV2")
	retainedHashWorkBefore(t, retainedHashWorkCall(t, cloned.fn, "Records"), retainedHashWorkCall(t, cloned.fn, "v2KeyForSealedRecord"), retainedHashWorkCall(t, cloned.fn, "CanonicalValue"))

	host := retainedHashWorkReadSource(t, "../../productionruntime/retained_records_proof.go", "VerifyRetainedRecords")
	// Statement zero defines the refusal closure. Statement one is the initial
	// nil/canceled guard; it precedes composition locks/capture and Prepare.
	cancelGuard := retainedHashWorkReturningGuard(t, host.fn, 1)
	if !strings.Contains(host.text, "parent == nil || parent.Err() != nil || h == nil") {
		t.Fatal("already-canceled gate removed")
	}
	locks := retainedHashWorkCalls(host.fn, "Lock")
	captures := retainedHashWorkCalls(host.fn, "retainedCapture")
	if len(locks) != 1 || len(captures) != 3 {
		t.Fatal("capture/locking topology changed")
	}
	retainedHashWorkBefore(t, cancelGuard.End(), retainedHashWorkCall(t, host.fn, "WithTimeout"), locks[0], captures[0], retainedHashWorkCall(t, host.fn, "PrepareRetainedObservationV2"), retainedHashWorkCall(t, host.fn, "ReadPublications"), retainedHashWorkCall(t, host.fn, "PrivateContext"), retainedHashWorkCall(t, host.fn, "observeRetainedPrivateRecords"), retainedHashWorkCall(t, host.fn, "Finish"), captures[1], retainedHashWorkCall(t, host.fn, "CheckLifetime"), captures[2])
	if !strings.Contains(host.text, "privateCount := uint64(len(private.rows))") {
		t.Fatal("actual visited private cardinality replaced by reservation")
	}
	t.Log("source-order gates only: canceled before composition/Prepare; ACK scalar limit before copy/context; real footprint + image work refusal before map cloning; no runtime no-provider-call/native claim")
}
