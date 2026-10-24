package consolidate

import (
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/status"
)

func ev(wp string, from, to status.Lane, stamp string) *status.Event {
	e := &status.Event{WPID: wp, From: from, To: to, Actor: status.StrActor("claude")}
	if stamp != "" {
		e.PolicyMetadata = jsonx.NewObject("lane_head", stamp)
	}
	return e
}

func TestWindows(t *testing.T) {
	events := []*status.Event{
		ev("WP01", "planned", "claimed", "a"),
		ev("WP01", "claimed", "in_progress", "b"),
		ev("WP01", "in_progress", "for_review", "c"),
		ev("WP01", "for_review", "in_review", "d"),
		ev("WP01", "in_review", "in_progress", "e"),
		ev("WP01", "in_progress", "canceled", ""),
	}
	ws := windows(events, map[string]bool{"WP01": true})["WP01"]
	if len(ws) != 3 {
		t.Fatalf("windows = %+v", ws)
	}
	if ws[0] != (workWindow{"WP01", kindImpl, "a", "c", false}) || ws[1] != (workWindow{"WP01", kindReview, "c", "e", false}) ||
		ws[2] != (workWindow{"WP01", kindImpl, "e", "", false}) {
		t.Errorf("windows = %+v", ws)
	}
	if !lacksLaneHeadStamps(events, "WP01") {
		t.Error("a closed window without a closing stamp lacks stamps")
	}
}

func TestDetailFor(t *testing.T) {
	got := detailFor(reasonCommitOutside, "lane-a", "WP02", []string{"1111111111aa", "2222222222bb", "3333333333cc", "4444444444dd"}, "src/x.py")
	want := "lane-a carries commit(s) 1111111111, 2222222222, 3333333333 and 1 more (touching 'src/x.py') that lie outside every WP's " +
		"recorded work window — no governed WP owns them, so canceled WP02's content cannot be told apart from approved work"
	if got != want {
		t.Errorf("detail:\n%s\nwant:\n%s", got, want)
	}
	if d := detailFor(reasonContestedCommit, "lane-a", "WP02", nil, ""); d != "WP02 in lane-a shares commit(s) ((none recorded)) with another WP's work window in the same lane — lane WPs ran concurrently, so per-WP attribution cannot be resolved" {
		t.Errorf("contested detail = %s", d)
	}
}

func TestAttestationStamps(t *testing.T) {
	op := "operator"
	att := ev("WP02", "canceled", "canceled", "s1")
	att.ReasonSource = &op
	att.PolicyMetadata.Set(attestationKey, canceledSuperseded)
	got := attestationStamps([]*status.Event{att})
	if got["WP02"] != "s1" {
		t.Errorf("stamps = %v", got)
	}
	got = attestationStamps([]*status.Event{att, ev("WP02", "canceled", "planned", "")})
	if _, ok := got["WP02"]; ok {
		t.Error("a later transition voids the attestation")
	}
}
