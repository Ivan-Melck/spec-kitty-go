package invocation

// Interim locked append for the Op trail (kernel.event_log_append, #6606).
//
// MERGE SEAM: this file is the one place the invocation package takes the
// interim event-log lock and appends a line. A shared event-log package
// (kernel.event_log_append + kernel.locks) is being ported separately; when
// it lands, repoint eventLogLock and appendEventLogLine at it and delete this
// file together with eventlog_lock_*.go and nofollow_*.go.

import (
	"bytes"
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

	"github.com/spec-kitty/go-kitty3/internal/ownedcheckout"
)

// Error codes of the interim event-log append refusals.
const (
	EventLogTornTail        = "EVENT_LOG_TORN_TAIL"
	EventLogLockTimeout     = "EVENT_LOG_LOCK_TIMEOUT"
	EventLogLockUnavailable = "EVENT_LOG_LOCK_UNAVAILABLE"
	EventLogSymlink         = "EVENT_LOG_SYMLINK"
	EventLogShortWrite      = "EVENT_LOG_SHORT_WRITE"
)

// eventLogLockTimeout is EVENT_LOG_LOCK_TIMEOUT_S.
const eventLogLockTimeout = 10 * time.Second

// recordMode is _RECORD_MODE: Op-trail files are owner-only.
const recordMode os.FileMode = 0o600

// EventLogError is EventLogAppendError: the message ends with its code.
type EventLogError struct{ Code, Msg string }

func (e *EventLogError) Error() string { return e.Msg + " Error code: " + e.Code + "." }

// eventLogLockPath is event_log_lock_path: the lock every append anchored
// on anchor takes, under the repository's git common dir.
func eventLogLockPath(anchor string) (string, error) {
	resolved := ownedcheckout.PyResolve(anchor)
	common, err := ownedcheckout.GitCommonDir(filepath.Dir(resolved))
	if err != nil {
		if ownedcheckout.IsNotARepository(err) {
			return resolved + ".event-log.lock", nil
		}
		return "", &EventLogError{EventLogLockUnavailable, fmt.Sprintf("%s: cannot place the event-log lock because git could not be invoked (%v). Make git runnable in this checkout, then retry.", anchor, err)}
	}
	key := resolved
	if runtime.GOOS == "windows" { // os.path.normcase
		key = strings.ToLower(strings.ReplaceAll(key, "/", `\`))
	}
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(common, "spec-kitty-locks", "event-logs", hex.EncodeToString(sum[:])[:32]+".lock"), nil
}

// ensureLockDir is kernel.locks._ensure_dir: create the lock's directory,
// hardening it to 0700 only when this call created it.
func ensureLockDir(lockPath string) error {
	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(filepath.Dir(dir), 0o777); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o777); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	_ = os.Chmod(dir, 0o700)
	return nil
}

// eventLogLock is event_log_lock: hold the interim lock anchored on anchor
// while fn runs.
func eventLogLock(anchor string, fn func() error) error {
	lockPath, err := eventLogLockPath(anchor)
	if err != nil {
		return err
	}
	if err := ensureLockDir(lockPath); err != nil {
		return err
	}
	release, acquired, err := acquireFileLock(lockPath, eventLogLockTimeout)
	if err != nil {
		return err
	}
	if !acquired {
		return &EventLogError{EventLogLockTimeout, fmt.Sprintf("%s: event-log lock %s was not acquired within 10.0s because another writer holds it. Retry, or stop the other writer.", anchor, lockPath)}
	}
	defer release()
	return fn()
}

// appendOptions are the keyword arguments of append_event_log_lines.
type appendOptions struct {
	lockAnchor   string // "" = the log itself
	noCreate     bool   // create=False: a missing log is os.ErrNotExist
	noFollow     bool
	mode         os.FileMode // 0 = 0o666 (umask applies)
	precondition func(existing []byte) error
}

// appendEventLogLine is append_event_log_lines(log, [line], ...): one line
// appended under the interim lock; a torn last line refuses the append.
func appendEventLogLine(log, line string, o appendOptions) error {
	if strings.ContainsAny(line, "\n\r") {
		return fmt.Errorf("an event-log line must not contain a line terminator")
	}
	if st, err := os.Stat(filepath.Dir(log)); err != nil || !st.IsDir() {
		return &os.PathError{Op: "open", Path: log, Err: os.ErrNotExist}
	}
	anchor := o.lockAnchor
	if anchor == "" {
		anchor = log
	}
	return eventLogLock(anchor, func() error {
		f, err := openForAppend(log, o)
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		size := st.Size()
		var existing []byte
		if o.precondition != nil {
			existing = make([]byte, size)
			if _, err := io.ReadFull(io.NewSectionReader(f, 0, size), existing); err != nil {
				return err
			}
		} else if size > 0 {
			existing = make([]byte, 1)
			if _, err := f.ReadAt(existing, size-1); err != nil {
				return err
			}
		}
		if size > 0 && !bytes.HasSuffix(existing, []byte{'\n'}) {
			return &EventLogError{EventLogTornTail, fmt.Sprintf("%s: the last line is torn (no trailing newline), so the append was refused. Inspect the file and cut the bytes after its last newline, then retry.", log)}
		}
		if o.precondition != nil {
			if err := o.precondition(existing); err != nil {
				return err
			}
		}
		payload := []byte(line + "\n")
		n, err := f.Write(payload)
		if err == nil && n != len(payload) {
			err = &EventLogError{EventLogShortWrite, fmt.Sprintf("%s: short write (%d of %d bytes); the append was refused.", log, n, len(payload))}
		}
		if err != nil {
			if terr := f.Truncate(size); terr == nil {
				_ = f.Sync()
			}
			return err
		}
		return f.Sync()
	})
}

// AppendEventLogLine is append_event_log_lines(log, [line],
// create_parents=True) for the other invocation-family logs (the
// profile-invocation lifecycle log): one locked append that refuses a torn
// last line. A refusal is an *EventLogError.
func AppendEventLogLine(log, line string) error {
	if err := os.MkdirAll(filepath.Dir(log), 0o777); err != nil {
		return err
	}
	return appendEventLogLine(log, line, appendOptions{})
}

func openForAppend(log string, o appendOptions) (*os.File, error) {
	flags := os.O_RDWR | os.O_APPEND
	if !o.noCreate {
		flags |= os.O_CREATE
	}
	mode := o.mode
	if mode == 0 {
		mode = 0o666
	}
	if !o.noFollow {
		return os.OpenFile(log, flags, mode)
	}
	symlink := &EventLogError{EventLogSymlink, log + ": refusing to append through a symbolic link."}
	if fi, err := os.Lstat(log); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, symlink
	}
	f, err := os.OpenFile(log, flags|oNoFollow, mode)
	if err != nil && isNoFollowRefusal(err) {
		return nil, symlink
	}
	return f, err
}
