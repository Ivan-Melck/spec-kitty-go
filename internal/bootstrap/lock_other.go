//go:build !unix

package bootstrap

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

// lockFile is lock_unix.go's lockFile on platforms without flock: the
// exclusive lock is filelock's (LockFileEx on Windows).
func lockFile(path string, dirMode os.FileMode) (func(), error) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		m := dirMode
		if m == 0 {
			m = 0o755
		}
		if err := os.MkdirAll(dir, m); err != nil {
			return nil, err
		}
	}
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := filelock.LockFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		f.Truncate(0)
		filelock.UnlockFile(f)
		f.Close()
	}, nil
}
