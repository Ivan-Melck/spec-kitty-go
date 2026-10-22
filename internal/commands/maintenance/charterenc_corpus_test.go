package maintenance_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// The charset corpus lives with the detector (internal/charsetnorm).
const charsetCorpus = "../../charsetnorm/testdata/corpus"

// bomErrorFiles raise UnicodeDecodeError upstream (bytes after a BOM that
// do not decode): run separately, one traceback each.
var bomErrorFiles = map[string]bool{
	"utf16le_bom_truncated.md":      true,
	"utf16le_bom_lone_surrogate.md": true,
	"utf16be_bom_bad_pair.md":       true,
	"utf32le_bom_out_of_range.md":   true,
	"utf8sig_invalid.md":            true,
}

func corpusFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(charsetCorpus)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(charsetCorpus, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// TestDiffMigrateCharterEncodingCorpus runs migrate charter-encoding over
// files in many encodings (cp1252, latin-1, cp1251, koi8-r, Greek, Hebrew,
// Arabic, Shift-JIS, EUC-JP, GB2312/GBK, Big5, EUC-KR, UTF-8/16/32 with
// BOMs, stray bytes, binary junk, very short files) and compares dry-run,
// --json and apply output, the rewritten files, backups and provenance.
func TestDiffMigrateCharterEncodingCorpus(t *testing.T) {
	files := corpusFiles(t)
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/040-enc/meta.json", `{"mission_id": "01JDDDDDDDDDDDDDDDDDDDDDDD"}`+"\n")
		i := 0
		for _, name := range sortedKeys(files) {
			if bomErrorFiles[name] {
				continue
			}
			dir := "kitty-specs/040-enc/charter/"
			if i%4 == 3 {
				dir = ".kittify/charter/"
			}
			s.WriteFile(dir+name, files[name])
			i++
		}
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "corpus")
	})
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--dry-run", "--json"},
		step{"migrate", "charter-encoding", "--dry-run"},
	)
	p.CompareTree("kitty-specs", true)
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--yes", "--json"},
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/charter", true)
	p.CompareTree(".kittify/encoding-provenance", true)
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--yes"},
		step{"migrate", "charter-encoding", "--dry-run", "--json"},
	)
	p.CompareTree("kitty-specs", true)
}

// TestDiffMigrateCharterEncodingAmbiguousOnly checks the safety gap the
// port closes: files upstream reports as ambiguous are left untouched.
func TestDiffMigrateCharterEncodingAmbiguousOnly(t *testing.T) {
	p := newOraclePair(t)
	p.Both(func(s *testkit.Sandbox) {
		s.WriteFile("kitty-specs/041-amb/charter/stray.md", "# Notes\n\nplain ascii text with one stray byte \xe9 here\n")
		s.WriteFile("kitty-specs/041-amb/charter/undef.md", "# Notes\n\nsmart \x93quotes\x94 and an undefined \x81 byte\n")
		s.WriteFile(".kittify/charter/short.txt", "caf\xe9")
		s.Git("add", "-A")
		s.Git("commit", "-q", "-m", "amb")
	})
	compareSteps(t, p, "", "",
		step{"migrate", "charter-encoding", "--yes", "--json"},
		step{"migrate", "charter-encoding", "--yes"},
		step{"migrate", "charter-encoding", "--dry-run"},
	)
	p.CompareTree("kitty-specs", true)
	p.CompareTree(".kittify/charter", true)
}

// TestDiffMigrateCharterEncodingBOMDecodeErrors: undecodable bytes after a
// BOM escape as UnicodeDecodeError upstream (traceback, exit 1). The final
// exception line must match; the rich traceback frames are not compared.
func TestDiffMigrateCharterEncodingBOMDecodeErrors(t *testing.T) {
	files := corpusFiles(t)
	p := newOraclePair(t)
	var names []string
	for n := range bomErrorFiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		p.Both(func(s *testkit.Sandbox) {
			os.RemoveAll(filepath.Join(s.Dir, ".kittify", "charter"))
			s.WriteFile(".kittify/charter/"+name, files[name])
		})
		g := p.Go.Run("migrate", "charter-encoding", "--dry-run", "--json")
		o := p.Oracle.RunOracle("migrate", "charter-encoding", "--dry-run", "--json")
		if g.Code != o.Code {
			t.Errorf("%s: exit go=%d oracle=%d\n%s\n%s", name, g.Code, o.Code, g.Stderr, o.Stderr)
			continue
		}
		if g.Stdout != o.Stdout {
			t.Errorf("%s: stdout go=%q oracle=%q", name, g.Stdout, o.Stdout)
		}
		if gl, ol := lastLine(g.Stderr), lastLine(o.Stderr); gl != ol {
			t.Errorf("%s: final exception line\n go:     %s\n oracle: %s\n%s", name, gl, ol, o.Stderr)
		}
	}
}
