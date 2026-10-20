// Package atomicfile writes files via temp-file + rename in the same
// directory, mirroring upstream's kernel.atomic.atomic_write (mkstemp +
// os.replace): the resulting file carries the temp file's mode (0600) unless
// a mode is given explicitly.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write atomically replaces path with data; the file ends up with mode 0600
// (Python tempfile.mkstemp semantics).
func Write(path string, data []byte) error { return WriteMode(path, data, 0o600) }

// WriteMode atomically replaces path with data using perm for the new file.
func WriteMode(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
