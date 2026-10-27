package glossary

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/eventlog"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// LedgerWarnLog receives the repair warning of glossary.events.
var LedgerWarnLog func(string)

// AppendEvent implements the canonical glossary ledger write contract. A
// validation refusal performs no I/O; a torn tail is quarantined before retry.
func AppendEvent(root, missionID string, event *jsonx.Object) error {
	if err := ValidateEvent(event); err != nil {
		return err
	}
	path := EventLogPath(root, missionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// The append, and any repair, run under the interim event-log lock
	// (#6606), so the torn-tail check is not racy against a second writer.
	return eventlog.WithLock(path, eventlog.DefaultTimeout, func() error {
		return appendLocked(path, event)
	})
}

func appendLocked(path string, event *jsonx.Object) error {
	if torn, err := tornTail(path); err != nil {
		return err
	} else if torn {
		if err := repairTornTail(path); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := []byte(jsonx.Dumps(event, jsonx.Line) + "\n")
	n, err := f.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return ledgerError(fmt.Sprintf("%s: short write (%d of %d bytes)", path, n, len(line)))
	}
	return err
}

func tornTail(path string) (bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false, err
	}
	var last [1]byte
	_, err = f.ReadAt(last[:], info.Size()-1)
	return last[0] != '\n', err
}

func repairTornTail(path string) error {
	release, err := repairLock(path + ".lock")
	if err != nil {
		return err
	}
	defer release()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	keep := int64(0)
	for end := size; end > 0; {
		start := max(int64(0), end-64*1024)
		block := make([]byte, end-start)
		if _, err := f.ReadAt(block, start); err != nil {
			return err
		}
		if index := bytes.LastIndexByte(block, '\n'); index >= 0 {
			keep = start + int64(index) + 1
			break
		}
		end = start
	}
	if keep == size {
		return nil
	}
	fragment := make([]byte, size-keep)
	if _, err := f.ReadAt(fragment, keep); err != nil {
		return err
	}
	quarantine := path + ".torn"
	q, err := os.OpenFile(quarantine, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err = q.Write(append(fragment, '\n')); err == nil {
		err = q.Sync()
	}
	closeErr := q.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = f.Truncate(keep); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if LedgerWarnLog != nil {
		LedgerWarnLog(fmt.Sprintf("Repaired torn tail of %s: moved %d byte(s) to %s", path, len(fragment), quarantine))
	}
	return nil
}
