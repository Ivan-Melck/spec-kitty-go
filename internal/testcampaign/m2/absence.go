package m2

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type absenceReceipt struct {
	SchemaVersion   int      `json:"schema_version"`
	Kind            string   `json:"kind"`
	Expected        []string `json:"expected"`
	Run             []string `json:"run"`
	Passed          []string `json:"passed"`
	Skipped         []string `json:"skipped"`
	Failed          []string `json:"failed"`
	Unmatched       []string `json:"unmatched"`
	Duplicates      []string `json:"duplicates"`
	MissingRun      []string `json:"missing_run"`
	MissingPass     []string `json:"missing_pass"`
	RawEventsSHA256 string   `json:"raw_events_sha256"`
	Counts          struct {
		Expected   int `json:"expected"`
		Run        int `json:"run"`
		Passed     int `json:"passed"`
		Skipped    int `json:"skipped"`
		Failed     int `json:"failed"`
		Unmatched  int `json:"unmatched"`
		Missing    int `json:"missing"`
		Duplicates int `json:"duplicates"`
	} `json:"counts"`
	Verdict string `json:"verdict"`
}

type absenceRuntimeReceipt struct {
	SchemaVersion   int      `json:"schema_version"`
	Expected        []string `json:"expected"`
	Run             []string `json:"run"`
	Passed          []string `json:"passed"`
	Skipped         []string `json:"skipped"`
	Failed          []string `json:"failed"`
	Unmatched       []string `json:"unmatched"`
	SidecarSHA256   string   `json:"sidecar_sha256"`
	RawEventsSHA256 string   `json:"raw_events_sha256"`
	Verdict         string   `json:"verdict"`
}

type absenceManifest struct {
	SchemaVersion int    `json:"schema_version"`
	Verdict       string `json:"verdict"`
	Source        struct {
		Commit      string `json:"commit"`
		Tree        string `json:"tree"`
		GoModSHA256 string `json:"go_mod_sha256"`
		GoSumSHA256 string `json:"go_sum_sha256"`
	} `json:"source"`
	Toolchain struct {
		Version     string `json:"version"`
		Environment string `json:"environment"`
	} `json:"toolchain"`
	Qualification struct {
		Source               bool   `json:"source"`
		Module               bool   `json:"module"`
		VendorLike           bool   `json:"vendor_like"`
		AbsenceReceipts      int    `json:"absence_receipts"`
		DetectorLayers       int    `json:"detector_layers"`
		Binaries             bool   `json:"binaries"`
		SBOM                 bool   `json:"sbom"`
		Install              bool   `json:"install"`
		HelpConfig           bool   `json:"help_config"`
		Runtime              string `json:"runtime"`
		RuntimeTests         int    `json:"runtime_tests"`
		HistoricalExemptions string `json:"historical_exemptions"`
	} `json:"qualification"`
	Digests map[string]string `json:"digests"`
}

var absenceDigestFiles = map[string]string{
	"commands": "commands.txt", "binary_results": "binary-results.txt", "install": "install-sha256.txt",
	"runtime": "runtime-result.json", "runtime_raw": "runtime-events.jsonl", "absence": "absence-tests.json",
	"absence_raw": "absence-events.jsonl", "layer_mutant": "layer-mutants.json", "layer_mutant_raw": "layer-mutant-events.jsonl",
	"module_graph": "module-graph.txt", "sbom": "sbom.cdx.json", "evidence_inventory": "evidence-sha256.txt",
}

func verifyNestedAbsence(root *os.Root, paths map[string]Artifact, source Source, qualificationSidecarSHA256 string) error {
	read := func(name string) ([]byte, error) {
		artifact, ok := paths["absence/"+name]
		if !ok || (artifact.Kind != "absence-evidence" && artifact.Kind != "absence-contract") {
			return nil, fmt.Errorf("WP16 absence member %q is absent", name)
		}
		return readRegular(root, filepath.FromSlash(artifact.Path), maximumFile)
	}
	manifestBytes, err := read("manifest.json")
	if err != nil {
		return err
	}
	var manifest absenceManifest
	if err := DecodeStrict(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("decode WP16 manifest: %w", err)
	}
	q := manifest.Qualification
	if manifest.SchemaVersion != 3 || manifest.Verdict != "pass" || manifest.Source.Commit != source.Commit || manifest.Source.Tree != source.Tree || manifest.Source.GoModSHA256 != source.GoModSHA256 || manifest.Source.GoSumSHA256 != source.GoSumSHA256 || !strings.Contains(manifest.Toolchain.Version, source.GoVersion) || !q.Source || !q.Module || !q.VendorLike || q.AbsenceReceipts != 489 || q.DetectorLayers != 8 || !q.Binaries || !q.SBOM || !q.Install || !q.HelpConfig || q.Runtime != "observed" || q.RuntimeTests != 23 || q.HistoricalExemptions != "closed-token-and-path-map" {
		return fmt.Errorf("WP16 absence manifest identity or closed qualification is incomplete")
	}
	for key, name := range absenceDigestFiles {
		data, err := read(name)
		if err != nil {
			return err
		}
		if manifest.Digests[key+"_sha256"] != SHA256(data) {
			return fmt.Errorf("WP16 %s digest differs", key)
		}
	}
	inventory, _ := read("evidence-sha256.txt")
	seen := map[string]bool{}
	scanner := bufio.NewScanner(bytes.NewReader(inventory))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || !validDigest(fields[0]) || !strings.HasPrefix(fields[1], "evidence/") {
			return fmt.Errorf("WP16 inventory entry is malformed")
		}
		name := strings.TrimPrefix(fields[1], "evidence/")
		if seen[name] {
			return fmt.Errorf("WP16 inventory duplicates %q", name)
		}
		seen[name] = true
		data, err := read(name)
		if err != nil || SHA256(data) != fields[0] {
			return fmt.Errorf("WP16 inventory member %q differs", name)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for path, artifact := range paths {
		if artifact.Kind == "absence-evidence" {
			name := strings.TrimPrefix(path, "absence/")
			if name != "manifest.json" && name != "manifest-sha256.txt" && name != "evidence-sha256.txt" && !seen[name] {
				return fmt.Errorf("WP16 inventory omits %q", name)
			}
		}
	}
	if err := verifyObservedAbsenceReceipt(read, "absence", 489); err != nil {
		return err
	}
	if err := verifyObservedAbsenceReceipt(read, "layer-mutant", 8); err != nil {
		return err
	}
	if err := verifyRuntimeAbsenceReceipt(read, qualificationSidecarSHA256); err != nil {
		return err
	}
	sidecar, err := readRegular(root, "sidecar/gapdbd", maximumFile)
	if err != nil || SHA256(sidecar) != qualificationSidecarSHA256 {
		return fmt.Errorf("WP16 admitted sidecar bytes differ")
	}
	return nil
}

func verifyObservedAbsenceReceipt(read func(string) ([]byte, error), stem string, count int) error {
	resultName, rawName, contractName := stem+"s.json", stem+"-events.jsonl", "expected-"+stem+"s.txt"
	if stem == "absence" {
		resultName, rawName, contractName = "absence-tests.json", "absence-events.jsonl", "expected-absence-tests.txt"
	}
	data, err := read(resultName)
	if err != nil {
		return err
	}
	raw, err := read(rawName)
	if err != nil {
		return err
	}
	contract, err := read(contractName)
	if err != nil {
		return err
	}
	var result absenceReceipt
	if err := DecodeStrict(data, &result); err != nil {
		return err
	}
	want := nonemptyLines(contract)
	slices.Sort(want)
	run, passed, skipped, failed, err := observedActions(raw)
	if err != nil {
		return err
	}
	if result.SchemaVersion != 1 || result.Verdict != "pass" || result.RawEventsSHA256 != SHA256(raw) || len(want) != count || !slices.Equal(result.Expected, want) || !slices.Equal(result.Run, want) || !slices.Equal(result.Passed, want) || len(result.Skipped)+len(result.Failed)+len(result.Unmatched)+len(result.Duplicates)+len(result.MissingRun)+len(result.MissingPass) != 0 || result.Counts.Expected != count || result.Counts.Run != count || result.Counts.Passed != count || result.Counts.Skipped+result.Counts.Failed+result.Counts.Unmatched+result.Counts.Missing+result.Counts.Duplicates != 0 {
		return fmt.Errorf("WP16 %s receipt does not match its closed observed census", stem)
	}
	if !slices.Equal(run, want) || !slices.Equal(passed, want) || len(skipped)+len(failed) != 0 {
		return fmt.Errorf("WP16 %s raw events do not prove the closed receipt", stem)
	}
	return nil
}

func verifyRuntimeAbsenceReceipt(read func(string) ([]byte, error), qualificationSidecarSHA256 string) error {
	data, err := read("runtime-result.json")
	if err != nil {
		return err
	}
	raw, err := read("runtime-events.jsonl")
	if err != nil {
		return err
	}
	expectedBytes, err := read("runtime-expected.json")
	if err != nil {
		return err
	}
	var expected []string
	if err := DecodeStrict(expectedBytes, &expected); err != nil {
		return err
	}
	var result absenceRuntimeReceipt
	if err := DecodeStrict(data, &result); err != nil {
		return err
	}
	run, passed, skipped, failed, err := observedActions(raw)
	if err != nil {
		return err
	}
	if result.SchemaVersion != 2 || result.Verdict != "pass" || result.SidecarSHA256 != qualificationSidecarSHA256 || result.RawEventsSHA256 != SHA256(raw) || len(expected) != 23 || !slices.Equal(result.Expected, expected) || !slices.Equal(result.Run, expected) || !slices.Equal(result.Passed, expected) || !slices.Equal(run, expected) || !slices.Equal(passed, expected) || len(skipped)+len(failed)+len(result.Skipped)+len(result.Failed)+len(result.Unmatched) != 0 {
		return fmt.Errorf("WP16 runtime receipt is not an observed closed 23-test census")
	}
	return nil
}

func observedActions(raw []byte) (run, passed, skipped, failed []string, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var event goTestEvent
		if decodeErr := DecodeStrict(scanner.Bytes(), &event); decodeErr != nil {
			return nil, nil, nil, nil, decodeErr
		}
		if event.Test == "" {
			continue
		}
		switch event.Action {
		case "run":
			run = append(run, event.Test)
		case "pass":
			passed = append(passed, event.Test)
		case "skip":
			skipped = append(skipped, event.Test)
		case "fail":
			failed = append(failed, event.Test)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, nil, nil, err
	}
	slices.Sort(run)
	slices.Sort(passed)
	slices.Sort(skipped)
	slices.Sort(failed)
	return run, passed, skipped, failed, nil
}

func nonemptyLines(data []byte) []string {
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}
