package beads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const writerLockPollInterval = 10 * time.Millisecond

type acquiredWriterLock interface{ release() }
type writerLocker interface {
	acquire(context.Context, tracker.TrackerBinding) (acquiredWriterLock, error)
}

type filesystemWriterLocker struct{}
type fileWriterLock struct{ file *os.File }

// writerLockPath is rooted beside the canonical provider workspace and keyed
// by its full identity. It serializes participating Spec Kitty processes only;
// it is not provider CAS and cannot fence a human or another direct writer.
func writerLockPath(binding tracker.TrackerBinding) string {
	digest := sha256.Sum256([]byte(binding.WorkspaceIdentity))
	return filepath.Join(filepath.Dir(binding.WorkspacePath), ".spec-kitty-locks", hex.EncodeToString(digest[:])+".lock")
}

func (filesystemWriterLocker) acquire(ctx context.Context, binding tracker.TrackerBinding) (acquiredWriterLock, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	directory := filepath.Dir(writerLockPath(binding))
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, lockError("writer lock directory is unavailable", err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, lockError("writer lock directory permissions cannot be restricted", err)
	}
	file, err := os.OpenFile(writerLockPath(binding), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, lockError("writer lock file is unavailable", err)
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return nil, lockError("writer lock permissions cannot be restricted", err)
	}
	ticker := time.NewTicker(writerLockPollInterval)
	defer ticker.Stop()
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &fileWriterLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, lockError("writer lock acquisition failed", err)
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, lockError("writer lock is contended", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (lock *fileWriterLock) release() {
	if lock == nil || lock.file == nil {
		return
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	_ = lock.file.Close()
	lock.file = nil
}

func lockError(message string, cause error) error {
	return &tracker.Error{Kind: tracker.ErrorLockContended, RecoveryCode: "bounded_retry", Message: message, Cause: cause}
}
