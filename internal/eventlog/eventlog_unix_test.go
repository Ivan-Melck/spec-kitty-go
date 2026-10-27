//go:build unix

package eventlog

import (
	"os"
	"syscall"
	"testing"
)

// checkNewLogMode: a new log is created 0o666 less the umask, like open("a").
func checkNewLogMode(t *testing.T, log string) {
	t.Helper()
	old := syscall.Umask(0)
	syscall.Umask(old)
	st, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if want := os.FileMode(0o666 &^ old); st.Mode().Perm() != want {
		t.Fatalf("new log mode %o, want %o (umask %o)", st.Mode().Perm(), want, old)
	}
}
