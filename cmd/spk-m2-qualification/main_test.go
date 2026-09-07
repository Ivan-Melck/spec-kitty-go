package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
)

func TestCommandsRefuseMissingAndRelativeInputs(t *testing.T) {
	for _, arguments := range [][]string{{}, {"unknown"}, {"verify", "--evidence", "relative", "--repository", "relative"}, {"sbom", "--repository", "relative", "--output", "relative"}, {"measure"}} {
		if err := run(arguments); err == nil {
			t.Fatalf("arguments %v unexpectedly passed", arguments)
		}
	}
}

func TestFailedPerformanceTranscriptIsPreservedCreateOnce(t *testing.T) {
	source := m2.Source{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), GoVersion: "go1.26.7", GoModSHA256: strings.Repeat("c", 64), GoSumSHA256: strings.Repeat("d", 64)}
	attempt, err := m2.ReserveAttempt(t.TempDir(), "performance-failure", source)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("performance scenario failed")
	transcript := []byte("{\"Action\":\"fail\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign\"}\n")
	if got := preserveFailedPerformanceTranscript(attempt, transcript, want); !errors.Is(got, want) {
		t.Fatalf("preserved failure=%v want=%v", got, want)
	}
	path := filepath.Join(attempt.Directory, "performance.json.events.jsonl")
	if got, err := os.ReadFile(path); err != nil || string(got) != string(transcript) {
		t.Fatalf("retained transcript=%q err=%v", got, err)
	}
	if got := preserveFailedPerformanceTranscript(attempt, []byte("substitute\n"), want); !strings.Contains(got.Error(), "preserve performance transcript") {
		t.Fatalf("create-once substitution was not refused: %v", got)
	}
}

func TestSBOMCommandPublishesAtomically(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "sbom.json")
	if err := run([]string{"sbom", "--repository", repository, "--output", output}); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(output); err != nil || len(data) == 0 {
		t.Fatalf("SBOM absent: bytes=%d err=%v", len(data), err)
	}
}

func TestAtomicWriteCreateOnceRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "immutable.json")
	if err := atomicWriteCreateOnce(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteCreateOnce(path, []byte("second")); err == nil {
		t.Fatal("create-once evidence was overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "first" {
		t.Fatalf("retained evidence=%q err=%v", data, err)
	}
}
