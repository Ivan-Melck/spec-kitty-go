package gapdb

import (
	"testing"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

// A failed/invalid S1 must not be credited as a successful count comparison.
// Private reason state carries neither actual provider counts nor diagnostics.
func TestRetainedCountMismatchClassification(t *testing.T) {
	for _, mode := range []string{"count", "revision", "owner", "lifecycle", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			a, captured, _ := retainedObservationFixture(t, nil, func(s *gapdbapi.StatusResult, n int) {
				if n != 2 {
					return
				}
				switch mode {
				case "count":
					s.RecordCount++
				case "revision":
					s.CurrentRevision++
				case "owner":
					s.OwnerPID++
				case "lifecycle":
					s.Lifecycle = gapdbapi.LifecycleDraining
				}
			}, nil)
			session, err := a.PrepareRetainedObservationV2(t.Context(), captured, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			if err := session.ReadPublications(); err != nil {
				t.Fatal(err)
			}
			if _, err := session.PrivateContext(); err != nil {
				t.Fatal(err)
			}
			if mode == "canceled" {
				session.state.cancel()
			}
			if count, err := session.Finish(6); count != 0 || err == nil {
				t.Fatal("invalid observation succeeded", count, err)
			}
			if session.state.countMismatch != (mode == "count") {
				t.Fatal("refusal misclassified as successful count comparison")
			}
		})
	}
}
