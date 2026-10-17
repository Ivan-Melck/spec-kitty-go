package projection

import (
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

func TestAcknowledgedOperationsUseBindingMembershipWithoutChangingHostCoordinates(t *testing.T) {
	authority, coordinator := projectionFixtureV2(t)
	own := journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-1"}
	for _, input := range []struct {
		suffix string
		scope  journalv2.Scope
	}{
		{"own-a", own}, {"own-b", own},
		{"other-project", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-2", MissionID: "mission-1"}},
		{"other-mission", journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: "project-1", MissionID: "mission-2"}},
		{"host", journalv2.Scope{Kind: journalv2.ScopeHost}},
		{"work", journalv2.Scope{Kind: journalv2.ScopeWork, ProjectID: "project-1", MissionID: "mission-1", WorkRevisionID: "revision-1", WorkID: "work-1"}},
	} {
		// Every product event deliberately names mission-1/project-1. Neither
		// its proximity to an operation nor the operation's ID is membership.
		plan := projectionPlanFixtureV2(t, input.suffix, "mission", "mission-"+input.suffix, input.scope)
		ack, err := coordinator.Publish(t.Context(), plan)
		if err != nil {
			t.Fatal(err)
		}
		if err := authority.PublishAcknowledged(ack); err != nil {
			t.Fatal(err)
		}
	}
	query := ListQueryV2{Family: "operation", ProjectID: "project-1", MissionID: "mission-1", Limit: 1}
	seen := make(map[string]bool)
	for page := 0; page < 4; page++ {
		result, err := authority.List(query)
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("scoped operation page=%d result=%+v err=%v", page, result, err)
		}
		view := result.Items[0]
		id := string(view.Coordinate.ID)
		if seen[id] || view.Coordinate.Scope != (journalv2.Scope{Kind: journalv2.ScopeHost}) {
			t.Fatalf("membership rewrote host identity or duplicated an operation: %+v", view)
		}
		seen[id] = true
		query.Cursor = result.NextCursor
		if query.Cursor == "" {
			break
		}
	}
	if len(seen) != 3 || !seen["operation-own-a"] || !seen["operation-own-b"] || !seen["operation-work"] || query.Cursor != "" {
		t.Fatalf("pagination omitted or leaked operation membership: %v cursor=%q", seen, query.Cursor)
	}
	for _, query := range []ListQueryV2{
		{Family: "operation", ProjectID: "project-2", MissionID: "mission-1", Limit: 10},
		{Family: "operation", ProjectID: "project-1", MissionID: "mission-2", Limit: 10},
		{Family: "operation", WorkID: "work-1", Limit: 10},
	} {
		result, err := authority.List(query)
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("immutable binding filter failed: query=%+v result=%+v err=%v", query, result, err)
		}
	}
	all, err := authority.List(ListQueryV2{Family: "operation", Limit: 100})
	if err != nil || len(all.Items) != 6 {
		t.Fatalf("global host operation census changed: %+v err=%v", all, err)
	}
}
