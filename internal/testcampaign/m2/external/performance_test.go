package external

import (
	"strings"
	"testing"
)

func TestPerformanceChildEnvironmentSuppliesExactProductionAuthorityExecutable(t *testing.T) {
	config := PerformanceConfig{
		Executable:          "/admitted/gapdbd",
		RunID:               "run-1",
		Reference:           "/evidence/reference.json",
		QualifierExecutable: "/bin/qualifier",
		ProductExecutable:   "/bin/spec-kitty",
	}
	environment := performanceChildEnvironment([]string{
		"PATH=/usr/bin",
		"SPEC_KITTY_WP13_GAPDBD=/stale/wp13",
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE=/stale/wp14",
		"SPEC_KITTY_WP15_GAPDBD=/stale/wp15",
	}, config, "/private/performance.json")

	want := map[string]string{
		"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE": "/admitted/gapdbd",
		"SPEC_KITTY_WP15_GAPDBD":               "/admitted/gapdbd",
	}
	counts := map[string]int{}
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if key == "SPEC_KITTY_WP13_GAPDBD" {
			t.Fatal("performance child retained a legacy inherited authority executable")
		}
		if expected, exists := want[key]; exists {
			counts[key]++
			if value != expected {
				t.Fatalf("%s=%q want %q", key, value, expected)
			}
		}
	}
	for key := range want {
		if counts[key] != 1 {
			t.Fatalf("%s occurrence count=%d want=1", key, counts[key])
		}
	}
}
