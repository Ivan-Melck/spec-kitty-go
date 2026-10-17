//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	storegapdb "github.com/spec-kitty/go-kitty3/internal/controlstore/gapdb"
)

// W4 CODE ONLY, UNTESTED. These exercise the ACTUAL owner-private visitor via
// its existing authorityReader.Get callback. They are memory-port controls, not
// original Host phase barriers, original gate.Close or native run cancellation.
// No callback changes the context's Deadline/Done/Value/Err implementation.
// All 21 native early site x caller/run/gate cases remain explicitly blocked
// in the cache inventory: derived session contexts do not expose those calls
// to a caller-context wrapper at the required function/phase boundary.
func TestExpiryQualificationEarlyPrivateCallerCancelEveryRead(t *testing.T) {
	for _, admissions := range []int{1, 2} {
		base := newRetainedPrivateFixture(t, admissions)
		trace := w4ACKPrivateTrace(base)
		for index := range trace {
			t.Run(fmt.Sprintf("admissions_%d/read_%02d", admissions, index), func(t *testing.T) {
				f := newRetainedPrivateFixture(t, admissions)
				w4ACKApplyBinding(t, &f)
				wantTrace := w4ACKPrivateTrace(f)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var reached bool
				var forwardedDeadline bool
				f.reader.hook = func(actual context.Context, key string) (storegapdb.Record, error, bool) {
					if key != wantTrace[index] { return storegapdb.Record{}, nil, false }
					reached = true
					// The visitor's unchanged five-second derived context, not a
					// replacement/fabricated context, is what the reader receives.
					_, forwardedDeadline = actual.Deadline()
					cancel()
					select {
					case <-actual.Done():
					default:
						t.Error("caller cancellation did not reach actual visitor context")
					}
					return f.reader.rows[key], nil, true
				}
				result, first := f.observe(ctx, defaultRetainedPrivatePolicy())
				// A successful Get racing a completed cancel must still discard
				// its row and all prior visited rows. No retry or renewed read.
				if !reached || !forwardedDeadline || first != context.Canceled || !reflect.ValueOf(result).IsZero() ||
					!reflect.DeepEqual(f.reader.trace, wantTrace[:index+1]) {
					t.Fatal("not the exact caller-cancel read cutoff", first, f.reader.trace)
				}
				if t.Context().Err() != nil { t.Fatal("independent test/root caller was canceled") }
			})
		}
	}
}

// Each private row can independently fail with a wrapped cancellation or a
// content-bearing provider diagnostic. These are real visitor refusals at the
// exact read prefix; no state bit, future watcher completion or sleep is used.
func TestExpiryQualificationEarlyPrivateFirstErrorsEveryRead(t *testing.T) {
	for _, fault := range []string{"wrapped_cancel", "wrapped_deadline", "provider_error"} {
		base := newRetainedPrivateFixture(t, 2)
		for index := range w4ACKPrivateTrace(base) {
			t.Run(fmt.Sprintf("%s/read_%02d", fault, index), func(t *testing.T) {
				f := newRetainedPrivateFixture(t, 2)
				w4ACKApplyBinding(t, &f)
				trace := w4ACKPrivateTrace(f)
				underlying := errors.New("SECRET provider diagnostic")
				if fault == "wrapped_cancel" { underlying = context.Canceled }
				if fault == "wrapped_deadline" { underlying = context.DeadlineExceeded }
				attempted := false
				f.reader.hook = func(_ context.Context, key string) (storegapdb.Record, error, bool) {
					if key != trace[index] { return storegapdb.Record{}, nil, false }
					attempted = true
					return storegapdb.Record{}, fmt.Errorf("SECRET private address: %w", underlying), true
				}
				result, first := f.observe(t.Context(), defaultRetainedPrivatePolicy())
				retainedPrivateAssertRefused(t, result, first)
				if !attempted || !reflect.ValueOf(result).IsZero() || !reflect.DeepEqual(f.reader.trace, trace[:index+1]) || strings.Contains(first.Error(), "SECRET") {
					t.Fatal("FIRST read failure not preserved/sanitized at exact prefix", first)
				}
				if fault != "provider_error" && first != underlying { t.Fatal("fixed context sentinel changed", first) }
				// Clearing a TEST reader callback is not a retry/repair of the
				// observation: preserve first and issue no subsequent observation.
				f.reader.hook = nil
				if first == nil { t.Fatal("restoration repaired FIRST failure") }
			})
		}
	}
}
