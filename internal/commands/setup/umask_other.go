//go:build !unix

package setup

func currentUmask() int { return 0o022 }
