//go:build !spec_kitty_expiry_host_qualification || !linux || !amd64

package gapdb

import (
	"context"
	"time"
	"net"
	"os"
	"os/exec"

	"github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb/retainedkernel"
	"golang.org/x/sys/unix"
)

// Empty conditional fields are first in their shared owners. This preserves no
// historical source, layout, escape or allocation certificate by itself.
type verifiedExpiryState struct{}
type lifecycleExpiryState struct{}
type expiryCleanupReport struct{}
type expiryLifecycleToken struct{}
type expiryLifecycleTeardown struct{}

func (*expiryLifecycleToken) cleanupReport() *expiryCleanupReport  { return nil }
func (*Lifecycle) expiryCleanupReport() *expiryCleanupReport       { return nil }
func (*Lifecycle) beginExpiryWork() (*expiryLifecycleToken, error) { return nil, nil }
func (*Lifecycle) finishExpiryWork(*expiryLifecycleToken)          {}
func (*Lifecycle) beginExpiryTeardown() (*expiryLifecycleTeardown, bool, error) {
	return nil, true, nil
}
func (*Lifecycle) initRuntimeCleanupReport(*expiryCleanupReport) {}
func (*VerifiedExecutable) bindExpiryFailedCleanup(context.Context) {}
func (*Lifecycle) expiryCleanupLimit(budget time.Duration) time.Duration { return budget }
func (*Lifecycle) bindExpiryCleanupCutoff(ctx context.Context, cutoff time.Time) time.Time { return intersectCleanupDeadline(ctx, cutoff) }
func (*Lifecycle) beginExpiryTeardownBefore(context.Context) (*expiryLifecycleTeardown, bool, error) { return nil, true, nil }
func (*Lifecycle) drainExpiryTeardownBefore(context.Context, *expiryLifecycleTeardown) (error, error) { return nil, nil }
func (*Lifecycle) drainExpiryTeardown(*expiryLifecycleTeardown) error   { return nil }
func (*Lifecycle) finishExpiryTeardown(*expiryLifecycleTeardown, error) {}
func (*Lifecycle) registerExpiryLifetime(*retainedkernel.Lifetime)      {}
func (*Lifecycle) closeExpiryLifetime(*retainedkernel.Lifetime) bool    { return false }
func (*Lifecycle) expiryLifetimeCloseResult(*retainedkernel.Lifetime) error { return nil }
func (*Lifecycle) expiryLifetimeCloseResultBefore(context.Context, *retainedkernel.Lifetime) error { return nil }
func (*Lifecycle) expiryStaleCustodyResult(prior error) error { return prior }
func (*expiryCleanupReport) sourceOpened()                              {}
func (*expiryCleanupReport) imageOpened()                               {}
func (*expiryCleanupReport) localOpened()                               {}
func (*expiryCleanupReport) localClosed(error)                          {}
func (*expiryCleanupReport) supervisorOpened()                          {}
func (*expiryCleanupReport) supervisorClosed(error)                     {}
func (*expiryCleanupReport) closeLocalFile(file *os.File) error         { return file.Close() }
func (*expiryCleanupReport) closeLocalFD(fd int) error                  { return unix.Close(fd) }
func (*expiryCleanupReport) closeLocalConnection(connection net.Conn) error {
	return connection.Close()
}

func (verifiedExpiryState) cleanupReport() *expiryCleanupReport { return nil }
func (*expiryCleanupReport) sourceClosed(error)                 {}
func (*expiryCleanupReport) imageClosed(error)                  {}
func (*expiryCleanupReport) metadataOpened()                    {}
func (*expiryCleanupReport) metadataClosed(error)               {}
func (*expiryCleanupReport) cleanupError() error                { return nil }
func (*VerifiedExecutable) closeExpiryUnconsumed() error        { return nil }
func (*VerifiedExecutable) expiryStartFailed()                  {}
func (*VerifiedExecutable) expiryCommandStarted()               {}
func (*Lifecycle) initExpiry(*VerifiedExecutable)               {}
func (*Lifecycle) expiryStarted(*VerifiedExecutable) error      { return nil }
func (*Lifecycle) revokeExpiry()                                {}
func (*Lifecycle) legacyEvidenceAllowed() bool                  { return true }
func (*Lifecycle) ordinaryRestartAllowed() bool                 { return true }
func expiryAttachAllowed(SidecarProvenance) bool                { return true }

func (v *VerifiedExecutable) fixedCommand(arguments, environment []string) (*exec.Cmd, error) {
	// Exactly the ordinary fd3 mapping and caller environment clone.
	command := exec.Command("/proc/self/fd/3", arguments...)
	command.ExtraFiles = []*os.File{v.execution}
	command.Env = append([]string(nil), environment...)
	return command, nil
}
