//go:build !unix

package validatetasks

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

// flockFile is the exclusive lock of the unix build, through filelock (LockFileEx
// on Windows).
func flockFile(path string) (func(), error) {
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
		filelock.UnlockFile(f)
		f.Close()
	}, nil
}
