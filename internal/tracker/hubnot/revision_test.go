package hubnot

import (
	"context"
	"strings"
	"testing"
)

func TestInventoryProofCanonicalizesPaginationButRetainsSemanticFields(t *testing.T) {
	a, b := fixtureDetail(t, "a", "1"), fixtureDetail(t, "b", "2")
	observe := func(rows []issueDetail) string {
		t.Helper()
		budget := newObservationBudget()
		defer budget.close()
		view, err := collectInventory(context.Background(), &fixtureReader{rows: rows}, budget, "project")
		if err != nil {
			t.Fatal(err)
		}
		inventory, err := view.inventory()
		if err != nil {
			t.Fatal(err)
		}
		return inventory.CommonProof().Digest
	}
	original := observe([]issueDetail{a, b})
	if observe([]issueDetail{b, a}) != original {
		t.Fatal("page ordering changed canonical proof")
	}
	b.State.Labels = append(b.State.Labels, "human-label")
	if observe([]issueDetail{a, b}) == original {
		t.Fatal("foreign state omitted from full inventory proof")
	}
}
func TestObservationRestartsRetainSharedResourceCounters(t *testing.T) {
	for _, limit := range []string{"bytes", "items"} {
		t.Run(limit, func(t *testing.T) {
			f := &fixtureReader{}
			for i := 0; i < 9; i++ {
				f.rows = append(f.rows, fixtureDetail(t, string(rune('a'+i)), string(rune('1'+i))))
			}
			f.alter = func(call int, p *page[issueDetail], e *envelope) {
				if call%2 == 0 {
					e.Snapshot = "sha256:" + strings.Repeat("f", 64)
				}
			}
			budget := newObservationBudget()
			defer budget.close()
			if limit == "bytes" {
				if err := budget.charge(maxObservationBytes-20000, 0); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := budget.charge(0, maxObservationItems-19); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := collectInventory(context.Background(), f, budget, "project"); err == nil {
				t.Fatal("stale restart reset cumulative limit")
			}
			if f.calls != 5 {
				t.Fatalf("expected retained budget to stop attempt3 firstpage, calls=%d", f.calls)
			}
		})
	}
}
func TestObservationCanceledBeforeAnyQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &fixtureReader{}
	budget := newObservationBudget()
	defer budget.close()
	if _, err := collectInventory(ctx, f, budget, "project"); err == nil || f.calls != 0 {
		t.Fatal("canceled inventory dispatched")
	}
}
