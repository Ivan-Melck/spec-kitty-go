//go:build !(linux || darwin)

package rich

func terminalWidth(fd int) (int, bool) { return 0, false }

func isatty(fd int) bool { return false }
