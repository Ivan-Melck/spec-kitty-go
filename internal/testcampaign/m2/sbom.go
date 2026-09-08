package m2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
)

type goListPackage struct {
	ImportPath string `json:"ImportPath"`
	Standard   bool   `json:"Standard"`
	Module     *struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
		Main    bool   `json:"Main"`
	} `json:"Module"`
}

type cyclonedxBOM struct {
	BOMFormat   string               `json:"bomFormat"`
	SpecVersion string               `json:"specVersion"`
	Version     int                  `json:"version"`
	Metadata    cyclonedxMetadata    `json:"metadata"`
	Components  []cyclonedxComponent `json:"components"`
}

type cyclonedxMetadata struct {
	Tools []cyclonedxTool `json:"tools"`
}

type cyclonedxTool struct {
	Vendor  string `json:"vendor"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type cyclonedxComponent struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version"`
	PURL    string `json:"purl"`
}

func GenerateSBOM(repositoryRoot string) ([]byte, error) {
	bom, _, err := GenerateSBOMEvidence(repositoryRoot)
	return bom, err
}

// GenerateSBOMEvidence returns both the deterministic BOM and the raw Go
// production dependency graph from which a fresh verifier can reconstruct it.
func GenerateSBOMEvidence(repositoryRoot string) ([]byte, []byte, error) {
	command := exec.Command("go", "list", "-deps", "-json", "./cmd/spec-kitty")
	command.Dir = repositoryRoot
	output, err := command.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("go list production graph: %w", err)
	}
	bom, err := SBOMFromGoList(output)
	return bom, output, err
}

func SBOMFromGoList(output []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(output))
	modules := map[string]string{}
	packages := 0
	mainModules := map[string]struct{}{}
	for {
		var pkg goListPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list graph: %w", err)
		}
		packages++
		if pkg.Standard || pkg.Module == nil {
			continue
		}
		version := pkg.Module.Version
		if pkg.Module.Main {
			version = "source"
			mainModules[pkg.Module.Path] = struct{}{}
		}
		if prior, exists := modules[pkg.Module.Path]; exists && prior != version {
			return nil, fmt.Errorf("module %q has multiple versions", pkg.Module.Path)
		}
		modules[pkg.Module.Path] = version
	}
	if packages == 0 || len(modules) == 0 || len(mainModules) != 1 {
		return nil, fmt.Errorf("go list production graph is empty")
	}
	if _, ok := mainModules["github.com/spec-kitty/go-kitty3"]; !ok {
		return nil, fmt.Errorf("go list production graph does not contain the qualified main module")
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	bom := cyclonedxBOM{BOMFormat: "CycloneDX", SpecVersion: "1.6", Version: 1, Metadata: cyclonedxMetadata{Tools: []cyclonedxTool{{Vendor: "Spec Kitty", Name: "spk-m2-qualification", Version: "1"}}}}
	for _, name := range names {
		version := modules[name]
		bom.Components = append(bom.Components, cyclonedxComponent{Type: "library", Name: name, Version: version, PURL: "pkg:golang/" + name + "@" + version})
	}
	data, err := CanonicalJSON(bom)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(string(data))
	if strings.Contains(lower, "sqlite") || strings.Contains(lower, "modernc.org") {
		return nil, fmt.Errorf("SBOM contains retired storage residue")
	}
	return data, nil
}

func VerifySBOMEvidence(raw, claimed []byte) error {
	recomputed, err := SBOMFromGoList(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(recomputed, claimed) {
		return fmt.Errorf("SBOM differs from raw production dependency graph")
	}
	return nil
}
