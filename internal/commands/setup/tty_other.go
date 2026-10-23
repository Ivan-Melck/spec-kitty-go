//go:build !linux

package setup

func fdIsTTY(uintptr) bool { return false }

func stdoutIsTTY() bool { return false }

func rawMode(uintptr) (func(), bool) { return nil, false }

func termRows(uintptr) (int, bool) { return 0, false }
