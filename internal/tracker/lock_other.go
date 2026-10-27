//go:build !unix

package tracker

import (
	"crypto/rand"
	"encoding/hex"
	"os"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

// lockFile is the exclusive lock of the unix build, through filelock (LockFileEx
// on Windows).
func lockFile(path string) (func(), error) {
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

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
