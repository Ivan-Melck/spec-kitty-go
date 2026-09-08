package m2

import (
	"strings"
	"testing"
)

func TestObservedActionsAdmitsOnlyClosedGoTestJSONEnvelope(t *testing.T) {
	raw := []byte(
		`{"Time":"2026-09-07T16:09:13Z","Action":"run","Package":"example.test/p","Test":"TestExact"}` + "\n" +
			`{"Time":"2026-09-07T16:09:14Z","Action":"pass","Package":"example.test/p","Test":"TestExact","Elapsed":1.25,"Output":"ok","FailedBuild":""}` + "\n",
	)
	run, passed, skipped, failed, err := observedActions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(run) != 1 || run[0] != "TestExact" || len(passed) != 1 || passed[0] != "TestExact" || len(skipped) != 0 || len(failed) != 0 {
		t.Fatalf("unexpected observed actions: run=%v passed=%v skipped=%v failed=%v", run, passed, skipped, failed)
	}
	mutant := []byte(`{"Time":"2026-09-07T16:09:13Z","Action":"run","Package":"example.test/p","Test":"TestExact","ClaimedPass":true}` + "\n")
	if _, _, _, _, err := observedActions(mutant); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown go test event field accepted: %v", err)
	}
}
