//go:build !unix

package eventlog

import "testing"

func checkNewLogMode(*testing.T, string) {}
