//go:build linux && amd64

package gapdb

// UNRUN actual unary wire/EOF path plus actual one-attempt owned connection
// close and injected returned error. Self-peer COMPONENT, not genuine provider.
import (
	"errors"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)
func TestRevision3StrictUnaryCloseOnlyAndPrimaryConjunction(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		f := strictEOFListen(t, func(conn *net.UnixConn, request wireRequest) error {
			response, err := strictEOFReply(request)
			if err != nil { return err }
			if malformed { response = []byte(`{`) }
			return writeWireFrame(conn, response) // fixture actual full Close yields EOF
		})
		calls := 0
		r := new(expiryCleanupReport)
		var destination map[string]any
		database, err := observeStrictUnaryWithClose(t.Context(), systemProcessAPI, f.path, f.socket, f.process, "health", &destination, time.Second, r, func(report *expiryCleanupReport, connection net.Conn) error {
			calls++
			result := withStartupCustody(connection.Close(), unix.EIO)
			report.localClosed(result) // test seam's returned result; NOT kernel-fault proof
			return result
		})
		if database != "" || !errors.Is(err, unix.EIO) || calls != 1 { t.Fatal("first actual connection close lost", err, calls) }
		if malformed {
			var primary *Error
			if !IsFailure(err, FailureProtocol) || !errors.As(err, &primary) || err.Error() != primary.Error() { t.Fatal("protocol primary text/type lost", err) }
		}
		if !malformed && !IsFailure(err, FailureCleanup) { t.Fatal("close-only admitted", err) }
	}
}
