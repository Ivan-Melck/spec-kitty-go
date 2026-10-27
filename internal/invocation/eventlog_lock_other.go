//go:build !unix

package invocation

import (
	"os"
	"time"
)

// oNoFollow: no O_NOFOLLOW here; openForAppend's Lstat check refuses a
// final-component symlink instead.
const oNoFollow = 0

func isNoFollowRefusal(error) bool { return false }

// acquireFileLock creates the lock file but takes no OS lock on this
// platform yet (the portable file-lock package replaces it at merge).
func acquireFileLock(lockPath string, _ time.Duration) (release func(), acquired bool, err error) {
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	return func() { f.Close() }, true, nil
}
