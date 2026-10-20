//go:build !unix

package bootstrap

import (
	"os"
	"path/filepath"
)

// lockFile creates the lock file (no OS lock on this platform).
func lockFile(path string, dirMode os.FileMode) (func(), error) {
	m := dirMode
	if m == 0 {
		m = 0o755
	}
	if err := os.MkdirAll(filepath.Dir(path), m); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return func() { f.Truncate(0); f.Close() }, nil
}
