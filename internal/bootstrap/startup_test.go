package bootstrap

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestStartupDegradesOnlyUnwritableHome(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EACCES, syscall.EPERM, syscall.EROFS, syscall.ENOSPC, syscall.EIO} {
		t.Run(errno.Error(), func(t *testing.T) {
			called := 0
			err := &os.PathError{Op: "open", Path: "/home/fixture", Err: errno}
			warning, failure := PrepareStartupAssets([]StartupOwner{
				{"runtime assets", func() error { called++; return &AssetError{"runtime_bootstrap", err} }},
				{"agent skills", func() error { called++; return err }},
				{"agent commands", func() error { called++; return nil }},
			})
			if IsUnwritableHomeError(err) {
				if failure != nil || called != 3 || !strings.Contains(warning, "runtime assets and agent skills") || !strings.Contains(warning, UnwritableHomeHint) {
					t.Fatalf("%d %q %v", called, warning, failure)
				}
			} else if !errors.Is(failure, errno) || called != 1 || warning != "" {
				t.Fatalf("%d %q %v", called, warning, failure)
			}
		})
	}
}
