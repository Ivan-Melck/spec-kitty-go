package issuematrix

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPartitionCoordLessAndRows pins the coord-less arm of
// ResolvePartition (the matrix is the primary dir, no ref probe) and the
// load_issue_matrix row contract of Partition.Rows on both arms: a
// scaffolded "unknown" verdict is not a canonical row.
func TestPartitionCoordLessAndRows(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "kitty-specs", "m")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"topology": "lanes"}`), 0o644)
	doc := `{"rows": {"#1": {"verdict": "fixed", "evidence_ref": "abc"}, "#2": {"verdict": "unknown", "evidence_ref": "x"}}, "schema_version": 1}`
	os.WriteFile(filepath.Join(dir, JSONFile), []byte(doc), 0o644)
	p, err := ResolvePartition(root, dir)
	if err != nil || p.MatrixDir != dir || p.Content != nil {
		t.Fatalf("coord-less partition: %+v %v", p, err)
	}
	issues := func(rows []Row) []string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Issue)
		}
		return out
	}
	if got := issues(p.Rows()); len(got) != 1 || got[0] != "#1" {
		t.Fatalf("dir rows: %v", got)
	}
	content := doc
	p = Partition{PrimaryDir: dir, MatrixDir: filepath.Join(root, "nowhere"), Content: &content}
	if got := issues(p.Rows()); len(got) != 1 || got[0] != "#1" {
		t.Fatalf("content rows: %v", got)
	}
	if !IsNotAuthored(&RefReadError{Code: NotAuthored}) || IsNotAuthored(&RefReadError{Code: RefAbsent}) {
		t.Fatal("IsNotAuthored")
	}
}
