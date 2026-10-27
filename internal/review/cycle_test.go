package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveBodyHeader pins #6652: the approval header sits above the
// reviewer's file content, needs a feedback source, and never bypasses the
// provenance guard; a non-UTF-8 file is a typed refusal.
func TestResolveBodyHeader(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	fb := write("fb.md", "line one\r\nline two\n")
	got, err := resolveBody(CreateInput{FeedbackSource: fb, BodyHeader: "Approved by rev: PR-42\n"}, dir)
	if err != nil || got != "Approved by rev: PR-42\n\nline one\nline two\n" {
		t.Fatalf("header body = %q, %v", got, err)
	}
	if _, err := resolveBody(CreateInput{Body: "x\n", BodyHeader: "h\n"}, dir); err == nil || !strings.Contains(err.Error(), "body_header") {
		t.Fatalf("header without feedback source: %v", err)
	}
	artifact := write("copied.md", RenderArtifact(1, "WP01", "m", "rev", "2026-01-01T00:00:00Z", nil, "**Issue**: x\n"))
	if _, err := resolveBody(CreateInput{FeedbackSource: artifact, BodyHeader: "h\n"}, dir); err == nil || !strings.Contains(err.Error(), "parses as a review-cycle artifact") {
		t.Fatalf("header laundered an artifact: %v", err)
	}
	latin := write("latin1.md", "caf\xe9\n")
	_, err = resolveBody(CreateInput{FeedbackSource: latin}, dir)
	want := "Review feedback file is not readable UTF-8 text: " + latin + " ('utf-8' codec can't decode byte 0xe9 in position 3: invalid continuation byte)"
	if err == nil || err.Error() != want {
		t.Fatalf("latin-1 feedback: %v", err)
	}
}
