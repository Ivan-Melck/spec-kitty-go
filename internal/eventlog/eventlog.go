// Package eventlog is kernel.event_log_append: the one interim locked append
// every event log other than status.events.jsonl and canonical-events.jsonl
// goes through (#6606), until the write service owns that log (#6193).
//
// The contract (contracts/interim-ledger-lock.md):
//
//  0. Anchor: the log itself, or the family directory of a
//     one-file-per-record log.
//  1. Lock file: <git-common-dir>/spec-kitty-locks/event-logs/<digest>.lock,
//     <digest> the first 32 hex characters of the SHA-256 of the anchor's
//     resolved, normcase-d path, so every worktree of a repository shares it
//     and the name length never depends on the path. Outside a git
//     repository: <anchor>.event-log.lock. When git cannot be invoked the
//     append refuses (EVENT_LOG_LOCK_UNAVAILABLE).
//  2. Lock: an exclusive machine file lock with a bounded wait (10 s).
//  3. Torn tail: under the lock, a log whose last byte is not LF is refused
//     (EVENT_LOG_TORN_TAIL) and nothing is written.
//  4. Write: the whole unit in one write on an O_APPEND descriptor, then
//     fsync. A partial write is cut back to the pre-write size before the
//     error is returned, so it never leaves a torn tail.
//
// It validates line framing only, never row content. Every refusal is an
// *Error whose message ends with "Error code: <CODE>.".
package eventlog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// LockSubdirectory is EVENT_LOG_LOCK_SUBDIRECTORY.
const LockSubdirectory = "event-logs"

// lockDirectory is kernel.locks.LOCK_DIRECTORY.
const lockDirectory = "spec-kitty-locks"

// NonGitLockSuffix is NON_GIT_LOCK_SUFFIX: it differs from the glossary
// repair lock (<log>.lock), taken while this lock is held.
const NonGitLockSuffix = ".event-log.lock"

// DefaultTimeout is EVENT_LOG_LOCK_TIMEOUT_S.
const DefaultTimeout = 10 * time.Second

const digestLength = 32

// Refusal codes.
const (
	CodeTornTail        = "EVENT_LOG_TORN_TAIL"
	CodeLockTimeout     = "EVENT_LOG_LOCK_TIMEOUT"
	CodeLockUnavailable = "EVENT_LOG_LOCK_UNAVAILABLE"
	CodeSymlink         = "EVENT_LOG_SYMLINK"
	CodeShortWrite      = "EVENT_LOG_SHORT_WRITE"
)

// Error is EventLogAppendError and its subclasses.
type Error struct {
	Code  string
	Msg   string
	Cause error
}

func (e *Error) Error() string { return e.Msg + " Error code: " + e.Code + "." }

func (e *Error) Unwrap() error { return e.Cause }

// ErrorCode is the refusal's code.
func (e *Error) ErrorCode() string { return e.Code }

// PyClass is the upstream exception class of the refusal (what an uncaught
// refusal's traceback names).
func (e *Error) PyClass() string {
	switch e.Code {
	case CodeTornTail:
		return "EventLogTornTailError"
	case CodeLockTimeout:
		return "EventLogLockTimeoutError"
	case CodeLockUnavailable:
		return "EventLogLockUnavailableError"
	case CodeSymlink:
		return "EventLogSymlinkError"
	case CodeShortWrite:
		return "EventLogShortWriteError"
	}
	return "EventLogAppendError"
}

// IsCode reports whether err is a refusal with code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// gitUnavailable is GitTopologyUnavailableError.
type gitUnavailable struct{ path, detail string }

func (e *gitUnavailable) Error() string {
	return fmt.Sprintf("git topology probe failed for %s: %s", pathRepr(e.path), e.detail)
}

func pathRepr(p string) string {
	if runtime.GOOS == "windows" {
		return "WindowsPath(" + pyjson.ReprString(filepath.ToSlash(p)) + ")"
	}
	return "PosixPath(" + pyjson.ReprString(p) + ")"
}

// normcase is os.path.normcase.
func normcase(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	}
	return p
}

// LockPath is event_log_lock_path(anchor).
func LockPath(anchor string) (string, error) {
	resolved := filelock.RealPath(anchor)
	common, inRepo, err := gitCommonDir(filepath.Dir(resolved))
	if err != nil {
		return "", &Error{
			Code:  CodeLockUnavailable,
			Msg:   fmt.Sprintf("%s: cannot place the event-log lock because git could not be invoked (%v). Make git runnable in this checkout, then retry.", anchor, err),
			Cause: err,
		}
	}
	if !inRepo {
		return resolved + NonGitLockSuffix, nil
	}
	sum := sha256.Sum256([]byte(normcase(resolved)))
	digest := hex.EncodeToString(sum[:])[:digestLength]
	return filepath.Join(common, lockDirectory, LockSubdirectory, digest+".lock"), nil
}

// gitCommonDir is kernel.git_topology.git_common_dir(dir): (common dir,
// true) inside a repository, ("", false) outside one (or inside a .git
// directory), an error when git could not be invoked.
func gitCommonDir(dir string) (string, bool, error) {
	if _, err := os.Stat(dir); err != nil {
		// subprocess.run with a missing cwd raises FileNotFoundError.
		return "", false, &gitUnavailable{dir, "git binary not found on PATH"}
	}
	res, err := gitx.Exec(dir, nil, nil, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false, &gitUnavailable{dir, "git binary not found on PATH"}
	}
	if res.Code != 0 {
		if strings.Contains(strings.ToLower(res.Stderr), "not a git repository") {
			return "", false, nil
		}
		return "", false, &gitUnavailable{dir, strings.TrimSpace(res.Stderr)}
	}
	raw := strings.TrimSpace(res.Stdout)
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(dir, raw)
	}
	common := filelock.RealPath(raw)
	if dir == common || strings.HasPrefix(dir, common+string(filepath.Separator)) {
		return "", false, nil
	}
	return common, true, nil
}

// WithLock is event_log_lock(anchor): hold the interim event-log lock while
// fn runs, for a writer that appends through another primitive but must
// still exclude the other writers of the log.
func WithLock(anchor string, timeout time.Duration, fn func() error) error {
	lockPath, err := LockPath(anchor)
	if err != nil {
		return err
	}
	l, err := filelock.Acquire(lockPath, timeout)
	if err != nil {
		var te *filelock.TimeoutError
		if errors.As(err, &te) {
			return &Error{
				Code:  CodeLockTimeout,
				Msg:   fmt.Sprintf("%s: event-log lock %s was not acquired within %ss because another writer holds it. Retry, or stop the other writer.", anchor, lockPath, pyFloat(timeout)),
				Cause: err,
			}
		}
		return err
	}
	defer l.Release()
	return fn()
}

func pyFloat(d time.Duration) string {
	s := fmt.Sprintf("%g", d.Seconds())
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// Options are append_event_log_lines' keyword arguments. The zero value is
// the upstream default (create the log, parents must exist, follow
// symlinks, mode 0666 less the umask, 10 s wait).
type Options struct {
	LockAnchor    string                      // family directory; "" = the log
	NoCreate      bool                        // create=False
	CreateParents bool                        // create_parents=True
	NoFollow      bool                        // refuse a final-component symlink
	Mode          os.FileMode                 // 0 = 0o666 (the umask applies)
	Precondition  func(existing []byte) error // runs under the lock on the current bytes
	Timeout       time.Duration               // 0 = DefaultTimeout
}

// Append is append_event_log_lines: append lines (pre-serialised rows,
// without terminators) to log as one unit under the interim event-log lock.
func Append(log string, lines []string, opt Options) error {
	payload, err := encodeUnit(lines)
	if err != nil {
		return err
	}
	if opt.CreateParents {
		if err := os.MkdirAll(filepath.Dir(log), 0o777); err != nil {
			return err
		}
	} else if st, err := os.Stat(filepath.Dir(log)); err != nil || !st.IsDir() {
		return &os.PathError{Op: "open", Path: log, Err: fmt.Errorf("parent directory does not exist: %w", os.ErrNotExist)}
	}
	anchor := opt.LockAnchor
	if anchor == "" {
		anchor = log
	}
	timeout := opt.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return WithLock(anchor, timeout, func() error {
		f, err := openForAppend(log, opt)
		if err != nil {
			return err
		}
		defer f.Close()
		preSize, err := checkExisting(f, log, opt.Precondition)
		if err != nil {
			return err
		}
		return writeUnit(f, log, payload, preSize)
	})
}

func encodeUnit(lines []string) ([]byte, error) {
	if len(lines) == 0 {
		return nil, errors.New("append_event_log_lines needs a non-empty sequence of lines")
	}
	var b strings.Builder
	for _, l := range lines {
		if strings.ContainsAny(l, "\n\r") {
			return nil, errors.New("an event-log line must not contain a line terminator")
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func openForAppend(log string, opt Options) (*os.File, error) {
	flags := os.O_RDWR | os.O_APPEND
	if !opt.NoCreate {
		flags |= os.O_CREATE
	}
	mode := opt.Mode
	if mode == 0 {
		mode = 0o666
	}
	if opt.NoFollow {
		if st, err := os.Lstat(log); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil, &Error{Code: CodeSymlink, Msg: fmt.Sprintf("%s: refusing to append through a symbolic link.", log)}
		}
		flags |= filelock.NoFollow
	}
	f, err := os.OpenFile(log, flags, mode)
	if err != nil && opt.NoFollow && filelock.IsSymlinkLoop(err) {
		return nil, &Error{Code: CodeSymlink, Msg: fmt.Sprintf("%s: refusing to append through a symbolic link.", log), Cause: err}
	}
	return f, err
}

// checkExisting refuses a torn tail or a failed precondition and returns
// the log's size before the write.
func checkExisting(f *os.File, log string, pre func([]byte) error) (int64, error) {
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := st.Size()
	var from int64
	if pre == nil && size > 0 {
		from = size - 1
	}
	existing := make([]byte, size-from)
	if _, err := f.ReadAt(existing, from); err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if size > 0 && existing[len(existing)-1] != '\n' {
		return 0, &Error{Code: CodeTornTail, Msg: fmt.Sprintf("%s: the last line is torn (no trailing newline), so the append was refused. Inspect the file and cut the bytes after its last newline, then retry.", log)}
	}
	if pre != nil {
		if err := pre(existing); err != nil {
			return 0, err
		}
	}
	return size, nil
}

// writeUnit writes the unit in one write; on a partial write it cuts the log
// back to preSize (the lock is still held, so nobody appended since) before
// returning the error.
func writeUnit(f *os.File, log string, payload []byte, preSize int64) error {
	n, err := f.Write(payload)
	if err == nil && n != len(payload) {
		err = &Error{Code: CodeShortWrite, Msg: fmt.Sprintf("%s: short write (%d of %d bytes); the append was refused.", log, n, len(payload))}
	}
	if err != nil {
		if terr := f.Truncate(preSize); terr == nil {
			if serr := f.Sync(); serr != nil {
				return fmt.Errorf("%w (cut-back fsync failed: %v)", err, serr)
			}
		} else {
			return fmt.Errorf("%w (cut-back failed: %v)", err, terr)
		}
		return err
	}
	return f.Sync()
}
