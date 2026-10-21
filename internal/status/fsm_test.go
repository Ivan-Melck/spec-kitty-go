package status

import (
	"reflect"
	"testing"
)

func TestValidateTransition(t *testing.T) {
	reason := "why"
	cases := []struct {
		from, to string
		ctx      GuardContext
		ok       bool
		msg      string
	}{
		{"planned", "claimed", GuardContext{Actor: "a"}, true, ""},
		{"planned", "claimed", GuardContext{}, false, "Transition requires actor identity"},
		{"planned", "in_progress", GuardContext{Actor: "a"}, false, "Illegal transition: planned -> in_progress"},
		{"doing", "for_review", GuardContext{Actor: "a"}, false, "Transition in_progress -> for_review requires completed subtasks or force with reason"},
		{"done", "planned", GuardContext{Force: true, Actor: "a"}, false, "Force transitions require actor and reason"},
		{"done", "planned", GuardContext{Force: true, Actor: "a", Reason: &reason}, true, ""},
		{"planned", "genesis", GuardContext{Force: true, Actor: "a", Reason: &reason}, false, "Illegal transition: planned -> genesis"},
		{"bogus", "planned", GuardContext{}, false, "Unknown lane: bogus"},
		{"in_review", "approved", GuardContext{Actor: "a"}, false, "Transition from in_review requires review_result (structured review outcome)"},
		{"approved", "planned", GuardContext{Actor: "a"}, false, "Transition requires review_ref (review feedback reference)"},
	}
	for _, c := range cases {
		ok, msg := ValidateTransition(c.from, c.to, c.ctx)
		if ok != c.ok || msg != c.msg {
			t.Errorf("%s->%s: got (%v, %q) want (%v, %q)", c.from, c.to, ok, msg, c.ok, c.msg)
		}
	}
}

func TestLaneHops(t *testing.T) {
	if got := LaneTargetsForEmit("planned", "doing"); !reflect.DeepEqual(got, []string{"claimed", "in_progress"}) {
		t.Fatal(got)
	}
	if got := LaneTargetsForEmit("in_review", "planned"); !reflect.DeepEqual(got, []string{"planned"}) {
		t.Fatal(got)
	}
	if !IsBackwardTransition("in_review", "planned") || IsBackwardTransition("blocked", "planned") {
		t.Fatal("backward")
	}
	if _, err := EnsureLane("bogus-lane"); err == nil || err.Error() != "Invalid lane 'bogus-lane'. Expected one of planned, claimed, in_progress, for_review, in_review, approved, done, blocked, canceled." {
		t.Fatal(err)
	}
}
