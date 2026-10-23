package pyjson

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// OSError is a CPython OSError (or one of its errno subclasses) raised by a
// file-system call. Error() is str(exc): "[Errno 2] No such file or
// directory: 'path'" (with " -> 'dst'" for two-path calls such as rename).
type OSError struct {
	Type      string // FileNotFoundError, PermissionError, IsADirectoryError, ...
	Errno     int
	Strerror  string
	Filename  string
	Filename2 string
	HasName   bool
	HasName2  bool
}

func (e *OSError) Error() string {
	s := fmt.Sprintf("[Errno %d] %s", e.Errno, e.Strerror)
	if !e.HasName {
		return s
	}
	s += ": " + ReprString(e.Filename)
	if e.HasName2 {
		s += " -> " + ReprString(e.Filename2)
	}
	return s
}

// osErrorTypes maps errno values to CPython's OSError subclasses (PEP 3151).
var osErrorTypes = map[syscall.Errno]string{
	syscall.ENOENT:       "FileNotFoundError",
	syscall.EACCES:       "PermissionError",
	syscall.EPERM:        "PermissionError",
	syscall.EISDIR:       "IsADirectoryError",
	syscall.ENOTDIR:      "NotADirectoryError",
	syscall.EEXIST:       "FileExistsError",
	syscall.EINTR:        "InterruptedError",
	syscall.ECHILD:       "ChildProcessError",
	syscall.ESRCH:        "ProcessLookupError",
	syscall.EPIPE:        "BrokenPipeError",
	syscall.ESHUTDOWN:    "BrokenPipeError",
	syscall.ETIMEDOUT:    "TimeoutError",
	syscall.EAGAIN:       "BlockingIOError",
	syscall.EALREADY:     "BlockingIOError",
	syscall.EINPROGRESS:  "BlockingIOError",
	syscall.ECONNABORTED: "ConnectionAbortedError",
	syscall.ECONNREFUSED: "ConnectionRefusedError",
	syscall.ECONNRESET:   "ConnectionResetError",
}

// strerror is the C library's strerror text for errno (glibc wording: Go's
// table is the same text with a lower-case first letter).
func strerror(errno syscall.Errno) string {
	msg := errno.Error()
	if msg == "" {
		return msg
	}
	r, n := utf8.DecodeRuneInString(msg)
	return string(unicode.ToUpper(r)) + msg[n:]
}

// AsOSError converts a Go file-system error (*fs.PathError, *os.LinkError,
// *os.SyscallError or a bare syscall.Errno) to the OSError CPython raises for
// the same call. ok is false for any other error.
func AsOSError(err error) (*OSError, bool) {
	var oe *OSError
	if errors.As(err, &oe) {
		return oe, true
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return nil, false
	}
	e := &OSError{Type: "OSError", Errno: int(errno), Strerror: strerror(errno)}
	if t, ok := osErrorTypes[errno]; ok {
		e.Type = t
	}
	var pe *fs.PathError
	var le *os.LinkError
	switch {
	case errors.As(err, &pe):
		e.Filename, e.HasName = pe.Path, true
	case errors.As(err, &le):
		e.Filename, e.HasName = le.Old, true
		e.Filename2, e.HasName2 = le.New, true
	}
	return e, true
}

// OSErrorString is str(exc) of the OSError CPython raises for err, or
// err.Error() when err is not a file-system error.
func OSErrorString(err error) string {
	if e, ok := AsOSError(err); ok {
		return e.Error()
	}
	return err.Error()
}

// IsSpace is str.isspace() for one code point.
func IsSpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// Strip is str.strip() with no argument.
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// Splitlines is str.splitlines() (no keepends): \n, \r, \r\n, \v, \f,
// \x1c, \x1d, \x1e, \x85, U+2028 and U+2029 end a line.
func Splitlines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			i += size
			start = i
			continue
		case '\r':
			out = append(out, s[start:i])
			i += size
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
			continue
		}
		i += size
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// TranslateNewlines is the universal-newlines read translation of a text
// file opened with newline=None: "\r\n" and a lone "\r" become "\n".
func TranslateNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// FileLines is iteration over a text file's lines after universal-newline
// translation (`for line in fh`), each without its trailing "\n".
func FileLines(s string) []string {
	s = TranslateNewlines(s)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
