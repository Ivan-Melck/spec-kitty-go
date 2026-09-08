package architecture

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var m2QualificationOnlySymbols = []string{
	"NewQualificationPublicationCoordinatorV2",
	"QualificationPublicationBoundaryProbeV2",
	"InterruptQualificationPublicationV2",
}

func TestM2PublicationFaultCapabilityIsConfinedToQualificationSources(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	sources := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sources[filepath.ToSlash(relative)] = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateM2QualificationProbeCallers(sources); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-deps", "./cmd/spec-kitty")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list production binary dependencies: %v", err)
	}
	if strings.Contains(string(output), m2ModulePath+"/internal/testcampaign/m2") {
		t.Fatal("production binary depends on M2 qualification code")
	}
}

func TestM2PublicationFaultCapabilityCallerGuardKillsProductionMutant(t *testing.T) {
	mutant := map[string][]byte{
		"internal/productionruntime/mutant.go":      []byte("package productionruntime\nfunc mutant(){ _ = NewQualificationPublicationCoordinatorV2 }\n"),
		"internal/controljournal/v2/publication.go": []byte("package v2\nfunc NewQualificationPublicationCoordinatorV2(){}\n"),
	}
	if err := validateM2QualificationProbeCallers(mutant); err == nil {
		t.Fatal("production qualification-fault constructor mutant survived")
	}
}

func validateM2QualificationProbeCallers(sources map[string][]byte) error {
	for path, data := range sources {
		if path == "internal/controljournal/v2/publication.go" || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(path, "internal/testcampaign/m2/") {
			continue
		}
		for _, symbol := range m2QualificationOnlySymbols {
			if strings.Contains(string(data), symbol) {
				return &qualificationProbeCallerError{path: path, symbol: symbol}
			}
		}
	}
	return nil
}

type qualificationProbeCallerError struct{ path, symbol string }

func (e *qualificationProbeCallerError) Error() string {
	return "qualification publication fault capability " + e.symbol + " used by production source " + e.path
}
