package ids

import "testing"

func TestTypedIDsParseAndRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		parse func(string) (string, error)
	}{
		{"project", func(v string) (string, error) { id, err := ParseProjectID(v); return id.String(), err }},
		{"repository", func(v string) (string, error) { id, err := ParseRepositoryID(v); return id.String(), err }},
		{"work", func(v string) (string, error) { id, err := ParseWorkID(v); return id.String(), err }},
		{"revision", func(v string) (string, error) { id, err := ParseWorkRevisionID(v); return id.String(), err }},
		{"lane", func(v string) (string, error) { id, err := ParseLaneID(v); return id.String(), err }},
		{"actor", func(v string) (string, error) { id, err := ParseActorID(v); return id.String(), err }},
		{"operation", func(v string) (string, error) { id, err := ParseOperationID(v); return id.String(), err }},
		{"workspace", func(v string) (string, error) { id, err := ParseWorkspaceID(v); return id.String(), err }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.parse("stable-01")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != "stable-01" {
				t.Fatalf("round trip = %q", got)
			}
		})
	}
}

func TestTypedIDsRejectInvalidValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", " leading", "trailing ", "has/slash", "has space"} {
		if _, err := ParseWorkID(value); err == nil {
			t.Errorf("ParseWorkID(%q) unexpectedly succeeded", value)
		}
	}
}
