package m2

import (
	"fmt"
	"strings"
	"testing"
)

func TestShortGateTranscriptRequiresClosedRunPassCensus(t *testing.T) {
	definition := shortDefinition("core", "./internal/example", false, []string{"TestOne", "TestTwo"}, nil)
	event := func(action, test string) string {
		return fmt.Sprintf("{\"Action\":%q,\"Test\":%q}\n", action, test)
	}
	good := []byte(event("run", "TestOne") + event("pass", "TestOne") + event("run", "TestTwo") + event("pass", "TestTwo"))
	if receipt, err := VerifyShortTranscript(good, definition); err != nil || receipt.Verdict != "pass" {
		t.Fatalf("valid receipt: %+v %v", receipt, err)
	}
	mutants := map[string][]byte{
		"missing":    []byte(event("run", "TestOne") + event("pass", "TestOne")),
		"renamed":    []byte(strings.ReplaceAll(string(good), "TestTwo", "TestRenamed")),
		"skipped":    []byte(event("run", "TestOne") + event("pass", "TestOne") + event("run", "TestTwo") + event("skip", "TestTwo")),
		"duplicate":  append(append([]byte(nil), good...), []byte(event("run", "TestOne")+event("pass", "TestOne"))...),
		"unexpected": append(append([]byte(nil), good...), []byte(event("run", "TestExtra")+event("pass", "TestExtra"))...),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyShortTranscript(mutant, definition); err == nil {
				t.Fatal("mutant survived closed short-gate receipt verification")
			}
		})
	}
}

func TestShortGateDefinitionCensusRejectsRemovalAndSubstitution(t *testing.T) {
	want := FrozenShortDefinitions()
	if err := validateShortDefinitions(want); err != nil {
		t.Fatal(err)
	}
	mutants := [][]ShortDefinition{
		append([]ShortDefinition(nil), want[1:]...),
		append([]ShortDefinition{shortDefinition("core", "./internal/example", false, []string{"TestInvented"}, nil)}, want[1:]...),
	}
	for _, mutant := range mutants {
		if err := validateShortDefinitions(mutant); err == nil {
			t.Fatal("short-gate census mutant survived")
		}
	}
}
