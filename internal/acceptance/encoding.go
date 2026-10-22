package acceptance

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/charsetnorm"
	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Acceptance-artifact encoding (acceptance/__init__.py ArtifactEncodingError,
// _read_text_strict and normalize_feature_encoding over
// charter.encoding_recovery.recover, ported in internal/charsetnorm).

// ArtifactEncodingError is raised when an artifact is not valid UTF-8.
type ArtifactEncodingError struct {
	Path   string
	Offset int // UnicodeDecodeError.start
	Byte   int // object[start], -1 when unknown
}

func (e *ArtifactEncodingError) Error() string {
	b := "unknown"
	if e.Byte >= 0 {
		b = fmt.Sprintf("0x%02x", e.Byte)
	}
	return fmt.Sprintf("Invalid UTF-8 encoding in %s: byte %s at offset %d. Run with --normalize-encoding to fix automatically.", e.Path, b, e.Offset)
}

// AmbiguousRecoveryMessage is accept's refusal after a repair attempt left
// the artifact undecodable.
func (e *ArtifactEncodingError) AmbiguousRecoveryMessage() string {
	return fmt.Sprintf("Could not safely recover the encoding of %s: byte 0x%02x at offset %d is ambiguous (no single code page fits), so --normalize-encoding refused rather than risk silent corruption. Repair the stray byte(s) with `spec-kitty validate-encoding --fix %s` (byte-offset repair), or re-save the file as UTF-8, then re-run accept.",
		e.Path, e.Byte, e.Offset, e.Path)
}

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// checkUTF8Sig is the decode half of _read_text_strict
// (path.read_text(encoding="utf-8-sig")): nil when the bytes decode.
func checkUTF8Sig(path string, data []byte) error {
	rest := data
	if bytes.HasPrefix(rest, utf8BOM) {
		rest = rest[3:]
	}
	if utf8.Valid(rest) {
		return nil
	}
	_, err := pyjson.DecodeUTF8(rest)
	ue, ok := err.(*pyjson.UnicodeError)
	if !ok {
		return &ArtifactEncodingError{Path: path, Byte: -1}
	}
	return &ArtifactEncodingError{Path: path, Offset: ue.Start, Byte: int(rest[ue.Start])}
}

// strictArtifactCheck reads path and reports an ArtifactEncodingError for
// bytes that are not UTF-8 (read errors are left to the caller's own read).
func strictArtifactCheck(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return checkUTF8Sig(path, data)
}

// EncodingBackupCollisionError refuses to overwrite an existing .bak.
type EncodingBackupCollisionError struct{ Path, Backup string }

func (e *EncodingBackupCollisionError) Error() string {
	return fmt.Sprintf("Refusing to normalize %s: a backup already exists at %s and would be silently overwritten. Remove or rename the existing backup, then re-run `accept --normalize-encoding`.", e.Path, e.Backup)
}

// scandirNames lists a directory in os.scandir order (unsorted).
func scandirNames(dir string) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	return names
}

// rglobMD is Path(dir).rglob("*.md") in CPython 3.13's order (glob.py
// recursive_selector): a directory's matching entries are yielded when the
// directory is discovered, in scandir order; subdirectories (not followed
// through symlinks) are walked from a LIFO stack.
func rglobMD(dir string) []string {
	var out []string
	matches := func(d string) {
		for _, n := range scandirNames(d) {
			if strings.HasSuffix(n, ".md") {
				out = append(out, filepath.Join(d, n))
			}
		}
	}
	matches(dir)
	stack := []string{dir}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, n := range scandirNames(d) {
			p := filepath.Join(d, n)
			if lst, err := os.Lstat(p); err == nil && lst.IsDir() {
				matches(p)
				stack = append(stack, p)
			}
		}
	}
	return out
}

// NormalizeFeatureEncoding is normalize_feature_encoding: rewrite every
// non-UTF-8 primary artifact the canonical detector confidently recovers
// (original bytes backed up to <name>.bak first). Ambiguous artifacts are
// left untouched.
func NormalizeFeatureEncoding(root, slug string) ([]string, error) {
	featureDir := coord.Resolve(root, slug).PrimaryDir
	if _, err := os.Stat(featureDir); err != nil {
		return nil, nil
	}
	var candidates []string
	for _, name := range []string{"spec.md", "plan.md", "quickstart.md", "tasks.md", "research.md", "data-model.md"} {
		p := filepath.Join(featureDir, name)
		if _, err := os.Stat(p); err == nil {
			candidates = append(candidates, p)
		}
	}
	for _, sub := range []string{"tasks", "research", "checklists"} {
		d := filepath.Join(featureDir, sub)
		if _, err := os.Stat(d); err == nil {
			candidates = append(candidates, rglobMD(d)...)
		}
	}
	var rewritten []string
	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			continue
		}
		seen[p] = true
		data, err := os.ReadFile(p)
		if err != nil {
			return rewritten, err
		}
		res, err := charsetnorm.Recover(data, false)
		if err != nil {
			return rewritten, err
		}
		if res.Ambiguous || !res.NormalizationApplied {
			continue
		}
		backup := p + ".bak"
		if _, err := os.Stat(backup); err == nil {
			return rewritten, &EncodingBackupCollisionError{Path: p, Backup: backup}
		}
		if err := os.WriteFile(backup, data, 0o644); err != nil {
			return rewritten, err
		}
		tmp := p + ".tmp-normalize"
		if err := os.WriteFile(tmp, []byte(res.Text), 0o644); err != nil {
			return rewritten, err
		}
		if err := os.Rename(tmp, p); err != nil {
			return rewritten, err
		}
		rewritten = append(rewritten, p)
	}
	return rewritten, nil
}
