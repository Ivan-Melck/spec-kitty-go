package orchestrator

import (
	"testing"
	"time"
)

// TestRunWithTimeout pins the record-analysis watchdog (PLAN-10): a call
// that finishes reports completed with its result; one that outlives the
// bound returns control on time, not completed (underlying_call_timed_out).
// The oracle cannot be driven into a 10 s hang offline, so this is Go-only.
func TestRunWithTimeout(t *testing.T) {
	o := runWithTimeout(func() (string, int) { return "{}", 3 }, time.Second)
	if !o.completed || o.raw != "{}" || o.code != 3 {
		t.Fatalf("completed call: %+v", o)
	}
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	o = runWithTimeout(func() (string, int) { <-release; return "late", 0 }, 50*time.Millisecond)
	if o.completed || time.Since(start) > 2*time.Second {
		t.Fatalf("hanging call: %+v after %v", o, time.Since(start))
	}
	if recordAnalysisTimeout != 10*time.Second {
		t.Fatalf("bound is %v, upstream uses 10 s", recordAnalysisTimeout)
	}
}
