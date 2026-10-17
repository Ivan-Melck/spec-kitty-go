//go:build linux && amd64

package gapdb

// AUTHORED UNRUN self-peer component. Real request/response/EOF wait and one
// actual owned close plus an injected returned error, NOT physical close fault.
import (
	"context"
	"errors"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRevision4StrictUnaryCancellationOwnsOneCloseAndPrimary(t *testing.T) {
	written, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	f := strictEOFListen(t, func(conn *net.UnixConn, request wireRequest) error {
		raw, err := strictEOFReply(request)
		if err != nil { return err }
		if err := writeWireFrame(conn, raw); err != nil { return err }
		close(written)
		<-release // no actual EOF until the caller has refused the response
		return nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := new(atomic.Int32)
	r := new(expiryCleanupReport)
	done := make(chan error, 1)
	go func() {
		destination := map[string]any{"sentinel":"unchanged"}
		database, err := observeStrictUnaryWithClose(ctx, systemProcessAPI, f.path, f.socket, f.process, "health", &destination, time.Second, r, func(report *expiryCleanupReport, connection net.Conn) error {
			calls.Add(1)
			result := withStartupCustody(connection.Close(), unix.EIO)
			report.localClosed(result)
			return result
		})
		if database != "" || !reflect.DeepEqual(destination, map[string]any{"sentinel":"unchanged"}) { t.Error("missing EOF/cancellation admitted destination", database, destination) }
		done <- err
	}()
	strictEOFWait(t, written)
	cancel()
	first := <-done
	var primary *Error
	if !errors.Is(first, unix.EIO) || !errors.As(first, &primary) || !IsFailure(first, FailureProtocol) || first.Error() != primary.Error() || calls.Load() != 1 { t.Fatal("cancel/defer actual close or primary lost", first, calls.Load()) }
}
