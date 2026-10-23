//go:build unix

package glossary

import (
	"errors"
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"os"
	"syscall"
	"time"
)

type RepairLockTimeout struct{ Path string }

func (e *RepairLockTimeout) Error() string {
	return fmt.Sprintf("Could not acquire machine lock at %s within bounded wait", pyjson.ReprString(e.Path))
}

func repairLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, err
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, &RepairLockTimeout{path}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return func() { f.Truncate(0); syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
