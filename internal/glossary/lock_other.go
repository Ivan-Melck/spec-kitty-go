//go:build !unix

package glossary

import (
	"fmt"
	"os"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

type RepairLockTimeout struct{ Path string }

func (e *RepairLockTimeout) Error() string {
	return fmt.Sprintf("Could not acquire machine lock at %s within bounded wait", pyjson.ReprString(e.Path))
}

func repairLock(path string) (func(), error) {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ok, err := filelock.TryLockFile(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if ok {
			break
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, &RepairLockTimeout{path}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() { f.Truncate(0); filelock.UnlockFile(f); f.Close() }, nil
}
