//go:build !linux

package maintenance

import "os"

// stdinIsTTY approximates sys.stdin.isatty().
func stdinIsTTY() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0 && st.Name() != "null"
}
