//go:build !linux

package planning

import "os"

// stdinIsTTY approximates sys.stdin.isatty() with a character-device check.
func stdinIsTTY() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
