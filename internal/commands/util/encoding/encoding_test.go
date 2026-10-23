package encoding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestSanitizeText(t *testing.T) {
	got := SanitizeText("User’s “favorite” — 72° ± …")
	want := `User's "favorite" --- 72 degrees +/- ...`
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestDetectProblematic(t *testing.T) {
	issues := DetectProblematic("Line 1\nUser’s “test”\nLine 3")
	if len(issues) != 3 || issues[0] != (Issue{2, 4, '’', "'"}) {
		t.Fatalf("got %+v", issues)
	}
}

func TestDecodeFaithfully(t *testing.T) {
	s, rewrite, err := decodeFaithfully([]byte("a\x93b\x94"))
	if err != nil || !rewrite || s != "a“b”" {
		t.Fatalf("got %q %v %v", s, rewrite, err)
	}
	_, _, err = decodeFaithfully([]byte("a\x81b"))
	if err == nil || err.Error() != "invalid byte(s) at offset(s) [1] could not be faithfully repaired" {
		t.Fatalf("got %v", err)
	}
	s, rewrite, err = decodeFaithfully([]byte("\xef\xbb\xbfok"))
	if err != nil || rewrite || s != "ok" {
		t.Fatalf("bom: %q %v %v", s, rewrite, err)
	}
}

func TestSanitizeFileBackupExists(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.md")
	os.WriteFile(p, []byte("x — y"), 0o644)
	os.WriteFile(p+".bak", []byte("old"), 0o644)
	mod, msg := SanitizeFile(p, true, false)
	if mod || !strings.Contains(msg, "[Errno 17] File exists: '") {
		t.Fatalf("got %v %q", mod, msg)
	}
}

func fixtures(s *testkit.Sandbox) {
	s.WriteFile(".kittify/.keep", "")
	s.WriteFile("kitty-specs/001-demo/spec.md", "He said “hi” — ok°\nline2 \x93bad\x94\n")
	s.WriteFile("kitty-specs/001-demo/plan.md", "clean\n")
	s.WriteFile("kitty-specs/001-demo/tasks/WP01.md", "x … © ™ ® •\nmore ‘q’ and × ÷\r\n")
	s.WriteFile("kitty-specs/001-demo/bad.md", "a\x81b\n")
	s.WriteFile("kitty-specs/001-demo/bin.md", "\x00\x01\x02")
	s.WriteFile("kitty-specs/001-demo/notes.txt", "—")
	s.WriteFile("kitty-specs/001-demo/meta.json", `{"mission_id":"01KABCDEF0123456789ABCDEFG","mission_slug":"001-demo"}`)
	s.WriteFile("kitty-specs/002-clean/spec.md", "fine\n")
}

func TestValidateEncodingParity(t *testing.T) {
	p := testkit.NewPair(t)
	p.Both(fixtures)
	p.Compare(true, "validate-encoding")
	p.Compare(true, "validate-encoding", "--mission", "  ")
	p.Compare(true, "validate-encoding", "--mission", "nope")
	p.Compare(true, "validate-encoding", "--mission", "002-clean")
	p.Compare(true, "validate-encoding", "--mission", "001-demo")
	p.Compare(true, "validate-encoding", "--mission", "01KABCDE")
	p.Compare(true, "validate-encoding", "--all")
	p.Compare(true, "validate-encoding", "--mission", "001-demo", "--fix", "--no-backup")
	p.CompareTree("kitty-specs", true)
	p.Both(fixtures)
	p.Compare(true, "validate-encoding", "--all", "--fix")
	p.CompareTree("kitty-specs", true)
	// .bak files now exist: a second fix of re-dirtied files hits FileExistsError.
	p.Both(fixtures)
	p.Compare(true, "validate-encoding", "--mission", "001-demo", "--fix")
	p.CompareTree("kitty-specs", true)
}

func TestValidateEncodingE2E(t *testing.T) {
	s := testkit.NewGitSandbox(t)
	fixtures(s)
	r := s.Run("validate-encoding", "--mission", "001-demo")
	if r.Code != 1 || !strings.Contains(testkit.StripANSI(r.Stdout), "Found 2 file(s) with encoding issues.") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	r = s.Run("validate-encoding", "--mission", "001-demo", "--fix")
	if r.Code != 0 || !s.Exists("kitty-specs/001-demo/spec.md.bak") {
		t.Fatalf("code=%d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if got := s.ReadFile("kitty-specs/001-demo/spec.md"); got != "He said \"hi\" --- ok degrees\nline2 \"bad\"\n" {
		t.Fatalf("got %q", got)
	}
}
