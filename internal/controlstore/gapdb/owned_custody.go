package gapdb

import (
	"net"
	"sync"
)

// Primary diagnostic and standard Is/As traversal remain exactly primary-first.
// Everything added here is OWNED/UNQUALIFIED, not a memory/Host certificate.
type ownedCustodyError struct { primary, custody error }
func (e *ownedCustodyError) Error() string { return e.primary.Error() }
func (e *ownedCustodyError) Unwrap() []error { return []error{e.primary, e.custody} }
func withOwnedCustody(primary, custody error) error {
	if custody == nil { return primary }
	if primary == nil { return custody }
	return &ownedCustodyError{primary: primary, custody: custody}
}

func ownedCloseResult(result retainedResult, primary, custody error) (retainedResult, error) {
	if custody == nil { return result, primary }
	return retainedResult{}, withOwnedCustody(primary, custody)
}

// conn is the ACTUAL dial result, immutable; it is not node.conn, which unlink
// clears. No closers/callbacks are stored and no provider is selected.
type retainedConnectionClose struct {
	conn *net.UnixConn
	once sync.Once
	err error
	attempted bool // Once publication
	recorded bool // transport.mu; not accessed by the syscall callback
}
func (c *retainedConnectionClose) close() error { return c.closeWith((*net.UnixConn).Close) }

// Fixed-call component seam. Returned-error injection is NOT an actual kernel
// fault claim. Production always supplies the literal method above.
func (c *retainedConnectionClose) closeWith(closeConnection func(*net.UnixConn) error) error {
	c.once.Do(func() { c.attempted = true; c.err = closeConnection(c.conn) })
	return c.err
}
func (t *retainedTransport) closeConnection(owner *retainedConnectionClose) error {
	err := owner.close() // no transport lock while joining the actual close
	t.mu.Lock()
	if !owner.recorded {
		owner.recorded = true
		t.connectionCloseAttempts++
		t.connectionCloseErr = withOwnedCustody(t.connectionCloseErr, err)
	}
	t.mu.Unlock()
	return err
}
