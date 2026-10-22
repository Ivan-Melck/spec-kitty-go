//go:build !unix

package orchestrator

import "os"

// tryLockFile: no advisory flock on this platform; the lock file is still
// created so the on-disk effect matches.
func tryLockFile(f *os.File) (bool, error) { return true, nil }

func unlockFile(f *os.File) {}
