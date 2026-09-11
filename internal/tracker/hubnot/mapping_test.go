package hubnot

import (
	"strings"
	"testing"
)

func fixtureDetail(t *testing.T, work string, digit string) issueDetail {
	t.Helper()
	state, err := encodeWorkState(foreignState(), richContent(t, work))
	if err != nil {
		t.Fatal(err)
	}
	state.Relations = nil
	return issueDetail{ID: "sha256:" + strings.Repeat(digit, 64), Creator: strings.Repeat("a", 64), OpeningMetadata: cloneIssueState(state).Metadata, State: &state, HeadCount: 1}
}
func TestMappingBindsSignedOpening(t *testing.T) {
	for _, change := range []string{"none", "rewritten-work", "removed-mapping", "late-mapping", "cross-project", "conflict"} {
		t.Run(change, func(t *testing.T) {
			d := fixtureDetail(t, "work", "1")
			switch change {
			case "rewritten-work":
				d.OpeningMetadata["go-kitty/work"] = "other"
			case "removed-mapping":
				d.State.Metadata = map[string]string{}
			case "late-mapping":
				d.OpeningMetadata = map[string]string{}
			case "cross-project":
				d.OpeningMetadata["go-kitty/project"] = "other"
			case "conflict":
				d.Conflict = true
				d.HeadCount = 2
				d.State = nil
			}
			_, err := mapDetail(d, "project")
			if (err == nil) != (change == "none") {
				t.Fatalf("mapping accepted=%v err=%v", err == nil, err)
			}
		})
	}
}
