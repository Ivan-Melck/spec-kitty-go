//go:build !unix

package writecheckout

import (
	"os"

	"github.com/spec-kitty/go-kitty3/internal/filelock"
)

func tryFlock(f *os.File) (bool, error) { return filelock.TryLockFile(f) }

func unflock(f *os.File) { filelock.UnlockFile(f) }
