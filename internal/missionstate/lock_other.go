//go:build !unix

package missionstate

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

func tryLock(f *os.File) bool {
	ok, err := filelock.TryLockFile(f)
	return ok && err == nil
}

func unlock(f *os.File) { filelock.UnlockFile(f) }

func openNoFollow(path string) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, &NoFollowError{path + " is a symlink; refusing to open it"}
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}
