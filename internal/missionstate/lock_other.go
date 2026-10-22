//go:build !unix

package missionstate

import "os"

func tryLock(*os.File) bool { return true }

func unlock(*os.File) {}

func openNoFollow(path string) (*os.File, error) {
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return nil, &NoFollowError{path + " is a symlink; refusing to open it"}
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
}
