package issuematrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoneOutcome pins #6653/#6711: done judges every row, cited or not,
// and reports how many rows it judged (-1 = no matrix).
func TestDoneOutcome(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("spec.md", "# Spec\n\nImplements #4321.\n")
	if msg, n := DoneOutcome(dir, dir, nil); !strings.Contains(msg, "is required before approval") || n != -1 {
		t.Fatalf("missing matrix with gating ref: %q %d", msg, n)
	}
	write("spec.md", "# Spec\n")
	if msg, n := DoneOutcome(dir, dir, nil); msg != "" || n != -1 {
		t.Fatalf("no matrix, no gating refs: %q %d", msg, n)
	}
	empty := ""
	if msg, n := DoneOutcome(dir, dir, &empty); msg != "" || n != -1 {
		t.Fatalf("not-authored content: %q %d", msg, n)
	}
	write(JSONFile, `{"rows": {}}`)
	if msg, n := DoneOutcome(dir, dir, nil); msg != "" || n != 0 {
		t.Fatalf("empty matrix: %q %d", msg, n)
	}
	if msg := ApprovalBlocker(dir, "approved", dir); msg != "" {
		t.Fatalf("approved judged a matrix with zero gating refs: %q", msg)
	}
	write("spec.md", "# Spec\n\nImplements #4321.\n")
	write(JSONFile, `{"rows": {"#1": {"verdict": "in-mission", "evidence_ref": "x"}, "#4321": {"verdict": "in-mission", "evidence_ref": "y"}, "#12": {"verdict": "fixed", "evidence_ref": "z"}}}`)
	msg, n := DoneOutcome(dir, dir, nil)
	if n != 3 || !strings.Contains(msg, "Fill in verdicts before done.") || !strings.Contains(msg, "owes the issue no work): #4321, #1\n") {
		t.Fatalf("uncited in-mission row: %q %d", msg, n)
	}
	if msg := ApprovalBlocker(dir, "approved", dir); msg != "" {
		t.Fatalf("approved blocked on in-mission rows: %q", msg)
	}
}
