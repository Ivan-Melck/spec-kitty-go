package encoding

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// problematic is text_sanitization.PROBLEMATIC_CHARS (insertion order).
var problematic = []struct {
	char rune
	repl string
}{
	{'‘', "'"}, {'’', "'"}, {'“', `"`}, {'”', `"`},
	{'–', "--"}, {'—', "---"},
	{'±', "+/-"}, {'×', "x"}, {'÷', "/"},
	{'…', "..."},
	{'•', "*"}, {'‣', ">"},
	{'°', " degrees"},
	{' ', " "},
	{'™', "(TM)"}, {'©', "(C)"}, {'®', "(R)"},
}

func replacementFor(r rune) (string, bool) {
	for _, p := range problematic {
		if p.char == r {
			return p.repl, true
		}
	}
	return "", false
}

// SanitizeText is sanitize_markdown_text.
func SanitizeText(text string) string {
	if text == "" {
		return text
	}
	for _, p := range problematic {
		text = strings.ReplaceAll(text, string(p.char), p.repl)
	}
	return text
}

// Issue is one detect_problematic_characters hit.
type Issue struct {
	Line, Col   int // 1-based line, 0-based column (code points)
	Char        rune
	Replacement string
}

// splitLinesKeepEnds is str.splitlines(keepends=True).
func splitLinesKeepEnds(s string) []string {
	var out []string
	rs := []rune(s)
	start := 0
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\v', '\f', '\x1c', '\x1d', '\x1e', '\u0085', ' ', ' ':
			out = append(out, string(rs[start:i+1]))
			start = i + 1
		case '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			out = append(out, string(rs[start:i+1]))
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// DetectProblematic is detect_problematic_characters.
func DetectProblematic(text string) []Issue {
	var issues []Issue
	for n, line := range splitLinesKeepEnds(text) {
		col := 0
		for _, r := range line {
			if repl, ok := replacementFor(r); ok {
				issues = append(issues, Issue{Line: n + 1, Col: col, Char: r, Replacement: repl})
			}
			col++
		}
	}
	return issues
}

var textBytes = func() [256]bool {
	var t [256]bool
	for _, b := range []byte{0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x1B} {
		t[b] = true
	}
	for b := 0x20; b < 0x7f; b++ {
		t[b] = true
	}
	for b := 0x80; b < 0x100; b++ {
		t[b] = true
	}
	return t
}()

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	window := data
	if len(window) > 8192 {
		window = window[:8192]
	}
	nontext := 0
	for _, b := range window {
		if b == 0 {
			return true
		}
		if !textBytes[b] {
			nontext++
		}
	}
	return float64(nontext)/float64(len(window)) > 0.30
}

// cp1252High maps bytes 0x80-0x9f; 0 marks an undefined byte.
var cp1252High = [32]rune{
	0x20ac, 0, 0x201a, 0x0192, 0x201e, 0x2026, 0x2020, 0x2021, 0x02c6, 0x2030, 0x0160, 0x2039, 0x0152, 0, 0x017d, 0,
	0, 0x2018, 0x2019, 0x201c, 0x201d, 0x2022, 0x2013, 0x2014, 0x02dc, 0x2122, 0x0161, 0x203a, 0x0153, 0, 0x017e, 0x0178,
}

// cp1252Rune decodes one byte with the cp1252 codec.
func cp1252Rune(b byte) (rune, bool) {
	if b >= 0x80 && b <= 0x9f {
		r := cp1252High[b-0x80]
		return r, r != 0
	}
	return rune(b), true
}

// decodeCP1252 is bytes.decode("cp1252") (strict).
func decodeCP1252(b []byte) (string, bool) {
	var sb strings.Builder
	for _, c := range b {
		r, ok := cp1252Rune(c)
		if !ok {
			return "", false
		}
		sb.WriteRune(r)
	}
	return sb.String(), true
}

func decodeLatin1(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

const bom = "\uFEFF"

// decodeUTF8Sig is bytes.decode("utf-8-sig").
func decodeUTF8Sig(b []byte) (string, error) {
	if len(b) >= 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		b = b[3:]
	}
	return pyjson.DecodeUTF8(b)
}

func repairInvalidUTF8(data []byte) (string, []int, bool) {
	working := append([]byte(nil), data...)
	var offsets []int
	for i := 0; i < 64; i++ {
		s, err := pyjson.DecodeUTF8(working)
		if err == nil {
			return s, offsets, true
		}
		off := err.(*pyjson.UnicodeError).Start
		offsets = append(offsets, off)
		r, ok := cp1252Rune(working[off])
		if !ok {
			return "", offsets, false
		}
		enc := []byte(string(r))
		working = append(working[:off], append(enc, working[off+1:]...)...)
	}
	return "", offsets, false
}

func decodeFaithfully(raw []byte) (string, bool, error) {
	if s, err := decodeUTF8Sig(raw); err == nil {
		return strings.TrimLeft(s, bom), false, nil
	}
	s, offsets, ok := repairInvalidUTF8(raw)
	if !ok {
		parts := make([]string, len(offsets))
		for i, o := range offsets {
			parts[i] = fmt.Sprint(o)
		}
		return "", false, fmt.Errorf("invalid byte(s) at offset(s) [%s] could not be faithfully repaired", strings.Join(parts, ", "))
	}
	return strings.TrimLeft(s, bom), true, nil
}

// pyOSError renders an OS error like str(OSError): "[Errno 17] File exists: 'path'".
func pyOSError(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		var errno syscall.Errno
		if errors.As(pe.Err, &errno) {
			msg := errno.Error()
			if msg != "" {
				r, n := utf8.DecodeRuneInString(msg)
				msg = string(unicode.ToUpper(r)) + msg[n:]
			}
			return fmt.Sprintf("[Errno %d] %s: %s", int(errno), msg, pyjson.ReprString(pe.Path))
		}
	}
	return err.Error()
}

// SanitizeFile is sanitize_file: (wasModified, errorMessage).
func SanitizeFile(path string, backup, dryRun bool) (bool, string) {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return false, "Refusing to sanitize symlinked file: " + path
	}
	if _, err := os.Stat(path); err != nil {
		return false, "File not found: " + path
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, fmt.Sprintf("Error resolving %s: %s", path, pyOSError(err))
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	if strings.ToLower(filepath.Ext(resolved)) != ".md" {
		return false, "Only markdown files are supported: " + resolved
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return false, fmt.Sprintf("Error sanitizing %s: %s", resolved, pyOSError(err))
	}
	if looksBinary(raw) {
		return false, ""
	}
	original, needsRewrite, derr := decodeFaithfully(raw)
	if derr != nil {
		return false, fmt.Sprintf("Refusing to sanitize %s: %s", resolved, derr)
	}
	sanitized := SanitizeText(original)
	if sanitized == original && !needsRewrite {
		return false, ""
	}
	if dryRun {
		return true, ""
	}
	if backup {
		f, err := os.OpenFile(resolved+".bak", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
		if err != nil {
			return false, fmt.Sprintf("Error sanitizing %s: %s", resolved, pyOSError(err))
		}
		_, werr := f.Write(raw)
		f.Close()
		if werr != nil {
			return false, fmt.Sprintf("Error sanitizing %s: %s", resolved, pyOSError(werr))
		}
	}
	st, _ := os.Stat(resolved)
	mode := os.FileMode(0o644)
	if st != nil {
		mode = st.Mode().Perm()
	}
	if err := os.WriteFile(resolved, []byte(sanitized), mode); err != nil {
		return false, fmt.Sprintf("Error sanitizing %s: %s", resolved, pyOSError(err))
	}
	return true, ""
}

// Result is one sanitize_directory entry.
type Result struct {
	Path     string
	Modified bool
	Err      string
}

// readdirRaw lists names in directory (scandir) order.
func readdirRaw(dir string) ([]string, bool) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil && len(names) == 0 {
		return nil, false
	}
	return names, true
}

// mdChildren is the "*.md" wildcard selector over dir (scandir order).
func mdChildren(dir string) []string {
	names, _ := readdirRaw(dir)
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, ".md") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

// globMarkdown reproduces Path(directory).glob("**/*.md") ordering on
// CPython 3.13 (no symlinked-directory recursion).
func globMarkdown(root string) []string {
	out := mdChildren(root)
	stack := []string{root}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		names, ok := readdirRaw(p)
		if !ok {
			continue
		}
		for _, n := range names {
			child := filepath.Join(p, n)
			st, err := os.Lstat(child)
			if err != nil || !st.IsDir() {
				continue
			}
			out = append(out, mdChildren(child)...)
			stack = append(stack, child)
		}
	}
	return out
}

// SanitizeDirectory is sanitize_directory(directory, pattern="**/*.md").
func SanitizeDirectory(dir string, backup, dryRun bool) []Result {
	var res []Result
	seen := map[string]int{}
	for _, p := range globMarkdown(dir) {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		mod, msg := SanitizeFile(p, backup, dryRun)
		if i, ok := seen[p]; ok {
			res[i] = Result{Path: p, Modified: mod, Err: msg}
			continue
		}
		seen[p] = len(res)
		res = append(res, Result{Path: p, Modified: mod, Err: msg})
	}
	return res
}
