package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandsRefuseMissingAndRelativeInputs(t *testing.T) {
	for _, arguments := range [][]string{{}, {"unknown"}, {"verify", "--evidence", "relative", "--repository", "relative"}, {"sbom", "--repository", "relative", "--output", "relative"}, {"measure"}} {
		if err := run(arguments); err == nil {
			t.Fatalf("arguments %v unexpectedly passed", arguments)
		}
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
