//go:build !unix

package orchestrator

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

// tryLockFile is fcntl.flock(fd, LOCK_EX | LOCK_NB) (msvcrt.locking on
// Windows): (false, nil) when another holder has it.
func tryLockFile(f *os.File) (bool, error) { return filelock.TryLockFile(f) }

func unlockFile(f *os.File) { filelock.UnlockFile(f) }
