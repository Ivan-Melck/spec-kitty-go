package nextrt

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestResearchDocumentationGuards(t *testing.T) {
	root := t.TempDir()
	primary := filepath.Join(root, "kitty-specs", "m-01")
	status := filepath.Join(root, ".worktrees", "m-01-coord", "kitty-specs", "m-01")
	write := func(dir, name, body string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := &advanceCtx{m: &missionCtx{Root: root, Slug: "m-01", Primary: primary, StatusDir: status}, featureDir: status}
	check := func(family, action string, want ...string) {
		t.Helper()
		if got := c.researchDocumentationGuards(family, action); !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			t.Errorf("%s/%s = %q, want %q", family, action, got, want)
		}
	}

	check("research", "scoping", "Required artifact missing: spec.md")
	check("research", "gathering", "Required artifact missing: source-register.csv", "Insufficient sources documented (need >=3)")
	check("research", "output", "Required artifact missing: report.md", "Publication approval gate not passed")
	check("research", "accept", "No guard registered for research action: accept")
	check("documentation", "generate", "Required artifact missing: docs/**/*.md (no Markdown files found under docs/)")
	check("documentation", "accept")
	check("documentation", "retrospective", "No guard registered for documentation action: retrospective")

	// Classified artifacts are read from the primary dir, unclassified ones,
	// the event log and docs/ from the status dir.
	write(primary, "spec.md", "#\n")
	write(primary, "source-register.csv", "id\n")
	write(primary, "report.md", "#\n")
	write(status, "mission-events.jsonl", `{"type": "source_documented"}`+"\nnot json\n[1]\n"+`{"type": "source_documented"}`+"\n\n"+
		`{"type": "source_documented"}`+"\n"+`{"type": "gate_passed", "name": "other"}`+"\n")
	write(primary, "docs/guide.md", "#\n")
	write(status, "docs/a/notes.txt", "x\n")
	check("research", "scoping")
	check("research", "gathering", "Required artifact missing: source-register.csv")
	check("research", "output", "Publication approval gate not passed")
	check("documentation", "generate", "Required artifact missing: docs/**/*.md (no Markdown files found under docs/)")

	write(status, "source-register.csv", "id\n")
	write(status, "mission-events.jsonl", `{"type": "gate_passed", "name": "publication_approved"}`+"\n")
	write(status, "docs/a/b.md", "#\n")
	check("research", "gathering", "Insufficient sources documented (need >=3)")
	check("research", "output")
	check("documentation", "generate")
}
