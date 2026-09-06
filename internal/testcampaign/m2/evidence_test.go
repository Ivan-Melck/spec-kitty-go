package m2

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound(t *testing.T) {
	root := repositoryRoot(t)
	output := t.TempDir()
	manifest, census, inputs := completeFixture(t)
	publication, err := Publish(output, manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	verification, verified, err := Verify(context.Background(), publication.Directory, filepath.Join(root, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/m2-qualification-v1.schema.json"), &manifest.Source)
	if err != nil {
		t.Fatal(err)
	}
	if verification.ManifestSHA256 != publication.ManifestSHA256 || !verification.ReviewEligible || verification.ReleaseReady || verification.Adopted || verification.Verdict != "pass" || verified.RunID != manifest.RunID {
		t.Fatalf("verification=%+v manifest=%+v", verification, verified)
	}
	first, _ := os.ReadFile(publication.ManifestPath)
	canonical, _ := CanonicalJSON(verified)
	if string(first) != string(canonical) {
		t.Fatal("manifest is not stable canonical JSON")
	}
}

func TestManifestCensusAndArtifactMutantsFailClosed(t *testing.T) {
	root := repositoryRoot(t)
	schema := filepath.Join(root, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/m2-qualification-v1.schema.json")
	tests := map[string]func(*Manifest, *Census, *[]ArtifactInput){
		"mixed-run":       func(_ *Manifest, census *Census, _ *[]ArtifactInput) { census.RunID = "foreign" },
		"mixed-source":    func(_ *Manifest, census *Census, _ *[]ArtifactInput) { census.SourceTree = stringOf('e', 40) },
		"missing-entry":   func(_ *Manifest, census *Census, _ *[]ArtifactInput) { census.Entries = census.Entries[1:] },
		"duplicate-entry": func(_ *Manifest, census *Census, _ *[]ArtifactInput) { census.Entries[1] = census.Entries[0] },
		"zero-observed":   func(_ *Manifest, census *Census, _ *[]ArtifactInput) { census.Entries[0].ObservedCount = 0 },
		"substitution":    func(_ *Manifest, census *Census, _ *[]ArtifactInput) { census.Entries[0].Substituted = true },
		"wrong-digest": func(_ *Manifest, census *Census, _ *[]ArtifactInput) {
			census.Entries[0].ArtifactSHA256 = stringOf('0', 64)
		},
		"caller-verdict": func(manifest *Manifest, _ *Census, _ *[]ArtifactInput) { manifest.Verdict = "fail" },
		"wrong-admission": func(manifest *Manifest, _ *Census, _ *[]ArtifactInput) {
			manifest.M1Admission.SidecarSHA256 = stringOf('0', 64)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest, census, inputs := completeFixture(t)
			mutate(&manifest, &census, &inputs)
			publication, err := Publish(t.TempDir(), manifest, census, inputs)
			if err != nil {
				return
			}
			if _, _, err := Verify(context.Background(), publication.Directory, schema, nil); err == nil {
				t.Fatal("mutant verified")
			}
		})
	}
}

func TestVerifyRejectsSymlinkMissingAndReusedRunDirectory(t *testing.T) {
	root := repositoryRoot(t)
	schema := filepath.Join(root, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/m2-qualification-v1.schema.json")
	manifest, census, inputs := completeFixture(t)
	output := t.TempDir()
	publication, err := Publish(output, manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Publish(output, manifest, census, inputs); err == nil {
		t.Fatal("reused RunID was accepted")
	}
	target := filepath.Join(publication.Directory, "campaigns", "family-acceptance.receipt.json")
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("source.json", target); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Verify(context.Background(), publication.Directory, schema, nil); err == nil {
		t.Fatal("symlink artifact verified")
	}
}

func TestFrozenCensusRejectsDeletionReorderAndSubstitution(t *testing.T) {
	want := FrozenDefinitions()
	if err := ValidateDefinitions(want); err != nil {
		t.Fatal(err)
	}
	reordered := append([]Definition{want[1], want[0]}, want[2:]...)
	substituted := append([]Definition(nil), want...)
	substituted[0] = Definition{ID: "replacement"}
	for _, mutant := range [][]Definition{want[1:], reordered, substituted} {
		if err := ValidateDefinitions(mutant); err == nil {
			t.Fatal("mutated census accepted")
		}
	}
}

func TestAcceptanceMatrixRejectsMissingGenericAndSubstitutedMappings(t *testing.T) {
	manifest, census, inputs := completeFixture(t)
	artifacts := map[string]Artifact{}
	for _, input := range inputs {
		artifacts[input.Path] = Artifact{Path: input.Path, Kind: input.Kind, SHA256: SHA256(input.Data)}
	}
	var matrix AcceptanceMatrix
	for _, input := range inputs {
		if input.Kind == "acceptance-matrix" {
			if err := DecodeStrict(input.Data, &matrix); err != nil {
				t.Fatal(err)
			}
		}
	}
	mutants := map[string]func(*AcceptanceMatrix){
		"missing": func(value *AcceptanceMatrix) { value.Requirements = value.Requirements[1:] },
		"generic": func(value *AcceptanceMatrix) { value.Requirements[0].Evidence = value.Requirements[1].Evidence },
		"substituted": func(value *AcceptanceMatrix) {
			value.Requirements[0], value.Requirements[1] = value.Requirements[1], value.Requirements[0]
		},
		"fake-review": func(value *AcceptanceMatrix) { value.Requirements[84].Status = "proven" },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			clone := matrix
			clone.Requirements = append([]AcceptanceRequirement(nil), matrix.Requirements...)
			mutate(&clone)
			data, _ := CanonicalJSON(clone)
			if err := VerifyAcceptanceMatrix(data, manifest, census, artifacts); err == nil {
				t.Fatal("mutated acceptance matrix verified")
			}
		})
	}
}

func TestSemanticEvidenceMutantsCannotBecomeTechnicalPass(t *testing.T) {
	mutants := map[string]func([]ArtifactInput){
		"forged-race": func(inputs []ArtifactInput) {
			raw := append(fixtureEvent("internal/scenario", "run", "TestInventedRace"), fixtureEvent("internal/scenario", "pass", "TestInventedRace")...)
			receipt, _ := VerifyGoCommandReceipt(raw, "race", 0)
			data, _ := CanonicalJSON(receipt)
			replaceInput(inputs, "race/events.jsonl", raw)
			replaceInput(inputs, "race.json", data)
		},
		"forged-sbom": func(inputs []ArtifactInput) {
			raw := []byte(`{"ImportPath":"example.invalid/forged","Module":{"Path":"example.invalid/forged","Main":true}}` + "\n")
			bom, _ := SBOMFromGoList(raw)
			replaceInput(inputs, "sbom.go-list.json", raw)
			replaceInput(inputs, "sbom.cdx.json", bom)
		},
		"foreign-binary": func(inputs []ArtifactInput) {
			replaceInput(inputs, "bin/spec-kitty", []byte("not a Go binary"))
			replaceInput(inputs, "install/spec-kitty", []byte("not a Go binary"))
		},
		"asserted-absence": func(inputs []ArtifactInput) { replaceInput(inputs, "absence/absence-events.jsonl", []byte("{}\n")) },
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			manifest, census, inputs := completeFixture(t)
			mutate(inputs)
			acceptance, err := BuildAcceptanceMatrix(manifest, census, inputs)
			if err != nil {
				return
			}
			replaceInput(inputs, "acceptance-matrix.json", acceptance)
			if _, err := Publish(t.TempDir(), manifest, census, inputs); err == nil {
				t.Fatal("semantically forged evidence published")
			}
		})
	}
}

func replaceInput(inputs []ArtifactInput, path string, data []byte) {
	for index := range inputs {
		if inputs[index].Path == path {
			inputs[index].Data = data
			return
		}
	}
}

func completeFixture(t *testing.T) (Manifest, Census, []ArtifactInput) {
	t.Helper()
	fixtureBinaryOnce.Do(buildFixtureBinary)
	if fixtureBinaryErr != nil {
		t.Fatal(fixtureBinaryErr)
	}
	source := fixtureSource
	runID := "m2-test-run"
	entries := make([]CensusEntry, 0, len(FrozenDefinitions()))
	var campaignInputs []ArtifactInput
	for _, definition := range FrozenDefinitions() {
		processes := definition.ExpectedRepetitions / definition.TrialsPerRun
		var raw []byte
		for range processes {
			raw = append(raw, fixtureEvent(definition.Package, "run", definition.Selector)...)
			if definition.ID == "fault/adversarial-tracker" {
				for _, child := range expectedWP14Children() {
					raw = append(raw, fixtureEvent(definition.Package, "run", child)...)
					raw = append(raw, fixtureEvent(definition.Package, "pass", child)...)
				}
			}
			raw = append(raw, fixtureEvent(definition.Package, "pass", definition.Selector)...)
		}
		receipt, err := VerifyGoTestReceipt(raw, runID, source, definition, processes, 0)
		if err != nil {
			t.Fatal(err)
		}
		receiptBytes, _ := CanonicalJSON(receipt)
		rawPath := "campaigns/" + safeID(definition.ID) + ".go-test.jsonl"
		receiptPath := "campaigns/" + safeID(definition.ID) + ".receipt.json"
		campaignInputs = append(campaignInputs, ArtifactInput{Path: rawPath, Kind: "campaign-raw", Data: raw}, ArtifactInput{Path: receiptPath, Kind: "campaign", Data: receiptBytes})
		if definition.ID == "fault/adversarial-tracker" {
			contractSource, err := os.ReadFile(filepath.Join(repositoryRoot(t), "internal/scenario/campaign_test.go"))
			if err != nil {
				t.Fatal(err)
			}
			structured, err := VerifyWP14CampaignReceiptBytes(contractSource, raw)
			if err != nil {
				t.Fatal(err)
			}
			structuredBytes, _ := CanonicalJSON(structured)
			campaignInputs = append(campaignInputs, ArtifactInput{Path: "campaigns/wp14-structured-census.receipt.json", Kind: "campaign-structure", Data: structuredBytes})
			campaignInputs = append(campaignInputs, ArtifactInput{Path: "contracts/wp14-campaign-source.go", Kind: "campaign-contract-source", Data: contractSource})
		}
		entries = append(entries, CensusEntry{ID: definition.ID, Category: definition.Category, OwnerWP: definition.OwnerWP, Package: definition.Package, Selector: definition.Selector, ExpectedRepetitions: definition.ExpectedRepetitions, TrialsPerRun: definition.TrialsPerRun, ObservedCount: definition.ExpectedRepetitions, PassedCount: definition.ExpectedRepetitions, ArtifactPath: receiptPath, ArtifactSHA256: SHA256(receiptBytes)})
	}
	census := Census{SchemaVersion: 1, RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree, Entries: entries}
	summary, err := Summarize(entries, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw := RawPerformance{SchemaVersion: 1, RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree, RecoveryOperations: 10000, RecoveryMS: repeated(30, 1), QueryMS: repeated(10000, .1), MutationMS: repeated(10000, .2), WarmupExcluded: 10, Method: "monotonic-nearest-rank-inclusive"}
	performance, err := SummarizePerformance(raw)
	if err != nil {
		t.Fatal(err)
	}
	rawBytes, _ := CanonicalJSON(raw)
	sourceBytes, _ := CanonicalJSON(source)
	producerBytes, _ := CanonicalJSON(ProducerIdentity{SchemaVersion: 1, ActorID: "implementer", ProcessID: "process-1", ProfileID: "implementer-ivan", ProfileRevision: stringOf('e', 64)})
	sidecarBytes, err := os.ReadFile("/tmp/spec-kitty-gapdb-assertion-sidecar/gapdbd")
	if err != nil || SHA256(sidecarBytes) != AdmittedSidecarSHA256 {
		t.Fatalf("admitted fixture sidecar unavailable: %v", err)
	}
	sbomRaw := []byte(`{"ImportPath":"github.com/spec-kitty/go-kitty3/cmd/spec-kitty","Module":{"Path":"github.com/spec-kitty/go-kitty3","Main":true}}` + "\n")
	sbom, err := SBOMFromGoList(sbomRaw)
	if err != nil {
		t.Fatal(err)
	}
	var raceRaw []byte
	for name, pkg := range raceExpected {
		raceRaw = append(raceRaw, fixtureEvent(strings.TrimPrefix(pkg, "github.com/spec-kitty/go-kitty3/"), "run", name)...)
		raceRaw = append(raceRaw, fixtureEvent(strings.TrimPrefix(pkg, "github.com/spec-kitty/go-kitty3/"), "pass", name)...)
	}
	raceReceipt, err := VerifyRaceReceipt(raceRaw, 0)
	if err != nil {
		t.Fatal(err)
	}
	raceReceiptBytes, _ := CanonicalJSON(raceReceipt)
	schemaBytes, err := os.ReadFile(filepath.Join(repositoryRoot(t), "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/m2-qualification-v1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	inputs := append([]ArtifactInput(nil), campaignInputs...)
	inputs = append(inputs, fixtureAbsenceInputs(t, source)...)
	inputs = append(inputs,
		ArtifactInput{Path: "contract-m2.json", Kind: "contract", Data: schemaBytes},
		ArtifactInput{Path: "m1.json", Kind: "m1-admission", Data: []byte("{}\n")},
		ArtifactInput{Path: "performance.json", Kind: "performance", Data: rawBytes},
		ArtifactInput{Path: "bin/spk-m2-qualification", Kind: "binary", Data: fixtureBinary},
		ArtifactInput{Path: "bin/spec-kitty", Kind: "binary", Data: fixtureBinary},
		ArtifactInput{Path: "install/spec-kitty", Kind: "install", Data: fixtureBinary},
		ArtifactInput{Path: "sbom.cdx.json", Kind: "sbom", Data: sbom},
		ArtifactInput{Path: "sbom.go-list.json", Kind: "sbom-raw", Data: sbomRaw},
		ArtifactInput{Path: "source.json", Kind: "source", Data: sourceBytes},
		ArtifactInput{Path: "successor-runtime.md", Kind: "successor-contract", Data: []byte("successor\n")},
		ArtifactInput{Path: "successor-architecture.md", Kind: "successor-contract-part", Data: []byte("architecture\n")},
		ArtifactInput{Path: "install/help.stdout", Kind: "install-smoke-raw", Data: []byte("fixture help\n")},
		ArtifactInput{Path: "install/runtime-smoke.json", Kind: "install-smoke", Data: fixtureHelpReceipt(t)},
		ArtifactInput{Path: "race.json", Kind: "race", Data: raceReceiptBytes},
		ArtifactInput{Path: "race/events.jsonl", Kind: "race-raw", Data: raceRaw},
		ArtifactInput{Path: "m1-adoption.json", Kind: "m1-admission-part", Data: []byte("{}\n")},
		ArtifactInput{Path: "m1-assertion-extension.json", Kind: "m1-admission-part", Data: []byte("{}\n")},
		ArtifactInput{Path: "sidecar/gapdbd", Kind: "sidecar", Data: sidecarBytes},
		ArtifactInput{Path: "producer-identity.json", Kind: "producer-identity", Data: producerBytes},
		ArtifactInput{Path: "contract-events.json", Kind: "contract", Data: []byte("{}\n")},
		ArtifactInput{Path: "independent-review-input.md", Kind: "review-input", Data: []byte("review instructions only\n")},
	)
	manifest := Manifest{SchemaVersion: 1, ProgramID: ProgramID, MissionID: MissionID, RunID: runID, Source: source, M1Admission: M1Admission{AdmittedM1AdoptionSHA256, AdmittedM1EvidenceSHA256, AdmittedM1TupleSHA256, AdmittedSidecarSHA256}, Census: summary, Performance: performance, Verdict: "pass"}
	acceptance, err := BuildAcceptanceMatrix(manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, ArtifactInput{Path: "acceptance-matrix.json", Kind: "acceptance-matrix", Data: acceptance})
	return manifest, census, inputs
}

func fixtureAbsenceInputs(t *testing.T, source Source) []ArtifactInput {
	t.Helper()
	root := repositoryRoot(t)
	absenceContract, err := os.ReadFile(filepath.Join(root, "scripts/m2-absence/expected-absence-tests.txt"))
	if err != nil {
		t.Fatal(err)
	}
	layerContract, err := os.ReadFile(filepath.Join(root, "scripts/m2-absence/expected-layer-mutants.txt"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeExpected := []string{"TestClassificationIsReadOnly", "TestCurrentRootAcceptsIdentityReservationGenerationAheadOfManifest", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/acceptance", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/effect", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/execution", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/handoff", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/integration", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/integration_cleanup", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/mission", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/readiness", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/review", "TestEveryDeclaredNativeOperationCrossesRealHostDomainValidation/workspace_rebind", "TestHistoricalAndUnsafeRootsRefuseBeforeInstallationOrMutation", "TestHistoricalSQLiteSignatureAndCompanions", "TestHistoricalSQLiteSignatureAndCompanions/exact", "TestHistoricalSQLiteSignatureAndCompanions/offset-one", "TestHistoricalSQLiteSignatureAndCompanions/one-byte-short", "TestHistoricalSQLiteSignatureAndCompanions/phrase", "TestHistoricalSQLiteSignatureAndCompanions/random-near-prefix", "TestHistoricalSQLiteSignatureAndCompanions/wrong-nul", "TestRealGapDBLostAcknowledgementRestartResumeAndConflict", "TestRealGapDBMutationInitializesAndQueriesRemainPureAcrossRestart"}
	slices.Sort(runtimeExpected)
	makeEvents := func(names []string) []byte {
		var out []byte
		for _, name := range names {
			out = append(out, []byte(fmt.Sprintf("{\"Action\":\"run\",\"Test\":%q}\n", name))...)
			out = append(out, []byte(fmt.Sprintf("{\"Action\":\"pass\",\"Test\":%q}\n", name))...)
		}
		return out
	}
	makeReceipt := func(kind string, contract []byte) ([]byte, []byte) {
		names := nonemptyLines(contract)
		slices.Sort(names)
		raw := makeEvents(names)
		value := absenceReceipt{SchemaVersion: 1, Kind: kind, Expected: names, Run: names, Passed: names, RawEventsSHA256: SHA256(raw), Verdict: "pass"}
		value.Counts.Expected = len(names)
		value.Counts.Run = len(names)
		value.Counts.Passed = len(names)
		data, _ := CanonicalJSON(value)
		return data, raw
	}
	absenceResult, absenceRaw := makeReceipt("absence", absenceContract)
	layerResult, layerRaw := makeReceipt("detector-layer", layerContract)
	runtimeRaw := makeEvents(runtimeExpected)
	expectedBytes, _ := CanonicalJSON(runtimeExpected)
	runtimeResult, _ := CanonicalJSON(absenceRuntimeReceipt{SchemaVersion: 2, Expected: runtimeExpected, Run: runtimeExpected, Passed: runtimeExpected, SidecarSHA256: AdmittedSidecarSHA256, RawEventsSHA256: SHA256(runtimeRaw), Verdict: "pass"})
	data := map[string][]byte{"commands.txt": []byte("spec-kitty\n"), "binary-results.txt": []byte("pass\n"), "install-sha256.txt": []byte("pass\n"), "runtime-result.json": runtimeResult, "runtime-events.jsonl": runtimeRaw, "runtime-expected.json": expectedBytes, "absence-tests.json": absenceResult, "absence-events.jsonl": absenceRaw, "layer-mutants.json": layerResult, "layer-mutant-events.jsonl": layerRaw, "module-graph.txt": []byte("example.invalid/m2fixture\n"), "sbom.cdx.json": []byte("{}\n")}
	var inventory strings.Builder
	names := make([]string, 0, len(data))
	for name := range data {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintf(&inventory, "%s  evidence/%s\n", SHA256(data[name]), name)
	}
	data["evidence-sha256.txt"] = []byte(inventory.String())
	digests := map[string]string{}
	for key, name := range absenceDigestFiles {
		digests[key+"_sha256"] = SHA256(data[name])
	}
	manifest := absenceManifest{SchemaVersion: 3, Verdict: "pass", Digests: digests}
	manifest.Source.Commit = source.Commit
	manifest.Source.Tree = source.Tree
	manifest.Source.GoModSHA256 = source.GoModSHA256
	manifest.Source.GoSumSHA256 = source.GoSumSHA256
	manifest.Toolchain.Version = "go version " + source.GoVersion + " linux/amd64"
	manifest.Toolchain.Environment = "linux/amd64/" + source.GoVersion
	manifest.Qualification.Source = true
	manifest.Qualification.Module = true
	manifest.Qualification.VendorLike = true
	manifest.Qualification.AbsenceReceipts = 489
	manifest.Qualification.DetectorLayers = 8
	manifest.Qualification.Binaries = true
	manifest.Qualification.SBOM = true
	manifest.Qualification.Install = true
	manifest.Qualification.HelpConfig = true
	manifest.Qualification.Runtime = "observed"
	manifest.Qualification.RuntimeTests = 23
	manifest.Qualification.HistoricalExemptions = "closed-token-and-path-map"
	manifestBytes, _ := CanonicalJSON(manifest)
	data["manifest.json"] = manifestBytes
	data["manifest-sha256.txt"] = []byte(SHA256(manifestBytes) + "  manifest.json\n")
	inputs := []ArtifactInput{{Path: "absence/expected-absence-tests.txt", Kind: "absence-contract", Data: absenceContract}, {Path: "absence/expected-layer-mutants.txt", Kind: "absence-contract", Data: layerContract}}
	for name, value := range data {
		inputs = append(inputs, ArtifactInput{Path: "absence/" + name, Kind: "absence-evidence", Data: value})
	}
	return inputs
}

var (
	fixtureBinaryOnce sync.Once
	fixtureBinary     []byte
	fixtureSource     Source
	fixtureBinaryErr  error
)

func buildFixtureBinary() {
	root, err := os.MkdirTemp("", "spk-m2-binary-fixture-")
	if err != nil {
		fixtureBinaryErr = err
		return
	}
	defer os.RemoveAll(root)
	for name, data := range map[string][]byte{"go.mod": []byte("module example.invalid/m2fixture\n\ngo 1.26\n"), "go.sum": {}, "main.go": []byte("package main\nfunc main() {}\n")} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			fixtureBinaryErr = err
			return
		}
	}
	for _, arguments := range [][]string{{"init", "-b", "main"}, {"config", "user.name", "M2 Fixture"}, {"config", "user.email", "fixture@example.invalid"}, {"add", "."}, {"commit", "-m", "fixture"}} {
		command := exec.Command("git", arguments...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			fixtureBinaryErr = fmt.Errorf("git fixture %v: %w: %s", arguments, err, output)
			return
		}
	}
	commit, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		fixtureBinaryErr = err
		return
	}
	tree, err := git(root, "rev-parse", "HEAD^{tree}")
	if err != nil {
		fixtureBinaryErr = err
		return
	}
	command := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(root, "fixture"), ".")
	command.Dir = root
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		fixtureBinaryErr = fmt.Errorf("build fixture: %w: %s", err, output)
		return
	}
	fixtureBinary, fixtureBinaryErr = os.ReadFile(filepath.Join(root, "fixture"))
	if fixtureBinaryErr != nil {
		return
	}
	goMod, _ := os.ReadFile(filepath.Join(root, "go.mod"))
	goSum, _ := os.ReadFile(filepath.Join(root, "go.sum"))
	fixtureSource = Source{Commit: commit, Tree: tree, GoVersion: runtime.Version(), GoModSHA256: SHA256(goMod), GoSumSHA256: SHA256(goSum)}
}

func fixtureHelpReceipt(t *testing.T) []byte {
	t.Helper()
	data, err := CanonicalJSON(map[string]any{"schema_version": 1, "installed_path_kind": "fresh-private-root", "argv": []string{"--help"}, "exit_code": 0, "stdout_sha256": SHA256([]byte("fixture help\n")), "verdict": "pass"})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixtureEvent(pkg, action, test string) []byte {
	return []byte(fmt.Sprintf("{\"Action\":%q,\"Package\":%q,\"Test\":%q}\n", action, "github.com/spec-kitty/go-kitty3/"+strings.TrimPrefix(pkg, "./"), test))
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func repeated(count int, value float64) []float64 {
	result := make([]float64, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func stringOf(character byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = character
	}
	return string(result)
}
