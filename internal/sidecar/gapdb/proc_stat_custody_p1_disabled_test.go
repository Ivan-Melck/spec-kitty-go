//go:build !spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import "testing"

// AUTHORED UNRUN. Ordinary selection retains the literal real Close result;
// the disabled empty report does not silently turn a failed Close into nil.
func TestProcStatP1OrdinarySelectedActualCloseSource(t *testing.T) {
	procStatP1SelectedPin(t, "expiry_qualification_disabled.go", "bb36e0e0a13fb5822ac76459a0cc76c965e2cfee3be1e2d28433c696ee1b8517")
	procStatP1FunctionEquals(t, "expiry_qualification_disabled.go", "closeLocalFile", `func (*expiryCleanupReport) closeLocalFile(file *os.File) error { return file.Close() }`)
	procStatP1FunctionEquals(t, "expiry_qualification_disabled.go", "localOpened", `func (*expiryCleanupReport) localOpened() {}`)
}
