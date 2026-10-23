//go:build !unix

package glossary

import (
	"fmt"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"os"
)

type RepairLockTimeout struct{ Path string }

func (e *RepairLockTimeout) Error() string {
	return fmt.Sprintf("Could not acquire machine lock at %s within bounded wait", pyjson.ReprString(e.Path))
}
func repairLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return func() { f.Truncate(0); f.Close() }, nil
}
