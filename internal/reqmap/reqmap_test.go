package reqmap

import "testing"

func TestParseAndCoverage(t *testing.T) {
	id, ok := Parse("FR-001", "software-dev")
	if !ok || id.Canonical() != "FR-001" || !id.IsFunctional() {
		t.Fatalf("Parse(FR-001) = %+v, %v", id, ok)
	}
	if _, ok := Parse("not-an-id", "software-dev"); ok {
		t.Fatal("Parse accepted a non-ID")
	}
	if nf, ok := Parse("NFR-002", "software-dev"); !ok || nf.IsFunctional() {
		t.Fatalf("Parse(NFR-002) = %+v, %v", nf, ok)
	}
	cov := ComputeCoverage(map[string][]string{"WP01": {"FR-001"}}, map[string]bool{"FR-001": true, "FR-002": true})
	if cov.Total != 2 || cov.Mapped != 1 || len(cov.Unmapped) != 1 || cov.Unmapped[0] != "FR-002" {
		t.Fatalf("coverage = %+v", cov)
	}
}
