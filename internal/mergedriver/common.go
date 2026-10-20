// Package mergedriver implements the seven Spec Kitty git merge drivers
// (upstream specify_cli/consolidation/drivers.py + status/event_log_merge.py)
// as an in-process library. The hidden `spec-kitty merge-driver-*` commands
// are thin adapters over Bodies; consolidation code calls the same bodies.
//
// Git invokes a driver with %O %A %B (base, ours, theirs); the merged result
// is written to ours. A body returns an *Error (exact stderr text, exit 1)
// for an unresolvable conflict or invalid input, a *Crash for inputs on
// which upstream raises an uncaught Python exception (traceback, exit 1),
// and an Outcome whose Notice (when non-empty) is echoed to stdout.
package mergedriver

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// MetaTwoWayEnv is META_DRIVER_TWO_WAY_ENV: set to "1" by the consolidation
// pipeline's mission->target merge so merge-driver-meta ignores %O.
const MetaTwoWayEnv = "SPEC_KITTY_META_MERGE_TWO_WAY"

// Kind classifies an *Error like upstream's MergeDriverError subclasses.
type Kind int

const (
	KindGeneric       Kind = iota // MergeDriverError
	KindPath                      // MergeDriverPathError
	KindRowMatrix                 // RowMatrixMergeError
	KindDecisionIndex             // DecisionIndexMergeError
)

// Error is MergeDriverError: Error() is the exact stderr text.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(kind Kind, msg string) *Error { return &Error{Kind: kind, Msg: msg} }

// Crash reproduces an uncaught Python exception in upstream (the CLI prints
// a traceback ending in "<Type>: <Msg>" and exits 1).
type Crash struct {
	Type string // e.g. "UnicodeDecodeError"
	Msg  string
}

func (c *Crash) Error() string {
	if c.Msg == "" {
		return c.Type
	}
	return c.Type + ": " + c.Msg
}

// Outcome is MergeDriverOutcome.
type Outcome struct {
	Notice string
}

// Body is a driver body: (base, ours, theirs) -> outcome.
type Body func(base, ours, theirs string) (Outcome, error)

// resolvePath mimics pathlib.Path.resolve() (strict=False): absolute,
// symlinks resolved for the existing prefix, "." and ".." collapsed.
func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	// Resolve the longest existing prefix, keep the rest lexically.
	dir, base := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if dir == abs || base == "" {
		return abs
	}
	return filepath.Join(resolvePath(dir), base)
}

// resolvePaths is _resolve_merge_driver_paths.
func resolvePaths(base, ours, theirs string) (string, string, string, error) {
	b, o, t := resolvePath(base), resolvePath(ours), resolvePath(theirs)
	if filepath.Dir(b) != filepath.Dir(o) || filepath.Dir(o) != filepath.Dir(t) {
		return "", "", "", newError(KindPath, "refusing merge-driver invocation: %O/%A/%B do not share a parent "+
			"directory ("+pyjson.ReprStrings([]string{b, o, t})+") — refused as a "+
			"possible path-injection attempt (#2970)")
	}
	return b, o, t, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// readText is Path.read_text(encoding="utf-8"): strict UTF-8 decoding and
// universal-newline translation (\r\n and \r become \n). A decode failure is
// returned as *pyjson.UnicodeError; other I/O failures as *Crash.
func readText(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", osCrash(err, p)
	}
	s, err := pyjson.DecodeUTF8(b)
	if err != nil {
		return "", err
	}
	if strings.ContainsRune(s, '\r') {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
	}
	return s, nil
}

// readTextCrash reads like readText but converts a decode error into the
// uncaught UnicodeDecodeError crash upstream produces.
func readTextCrash(p string) (string, error) {
	s, err := readText(p)
	if err != nil {
		var ue *pyjson.UnicodeError
		if errors.As(err, &ue) {
			return "", &Crash{Type: "UnicodeDecodeError", Msg: ue.Error()}
		}
		return "", err
	}
	return s, nil
}

func osCrash(err error, p string) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return &Crash{Type: "FileNotFoundError", Msg: "[Errno 2] No such file or directory: " + pyjson.ReprString(p)}
	case errors.Is(err, fs.ErrPermission):
		return &Crash{Type: "PermissionError", Msg: "[Errno 13] Permission denied: " + pyjson.ReprString(p)}
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && strings.Contains(pe.Err.Error(), "is a directory") {
		return &Crash{Type: "IsADirectoryError", Msg: "[Errno 21] Is a directory: " + pyjson.ReprString(p)}
	}
	return &Crash{Type: "OSError", Msg: err.Error()}
}

// writeText is Path.write_text(s, encoding="utf-8").
func writeText(p, s string) error {
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		return osCrash(err, p)
	}
	return nil
}

// isPySpace is str.isspace() for one code point.
func isPySpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// pyStrip is str.strip().
func pyStrip(s string) string { return strings.TrimFunc(s, isPySpace) }

// splitLines is str.splitlines() (no keepends).
func splitLines(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	var cur []rune
	_ = start
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, string(cur))
			cur = cur[:0]
		case '\r':
			out = append(out, string(cur))
			cur = cur[:0]
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
		default:
			cur = append(cur, r)
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}
