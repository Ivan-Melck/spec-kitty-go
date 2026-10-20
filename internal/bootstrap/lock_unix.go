//go:build unix

package bootstrap

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockFile opens (creating 0600) path, takes an exclusive flock and returns
// the release func. Like kernel.locks.machine_file_lock the file is left in
// place, empty, on release. dirMode>0 hardens a freshly created parent.
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
		os.Chmod(dir, m)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		f.Truncate(0)
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
