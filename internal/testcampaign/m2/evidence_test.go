package m2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

func TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound(t *testing.T) {
	root := repositoryRoot(t)
	output := t.TempDir()
	manifest, census, inputs, policy := completeSidecarFreeFixture(t)
	publication, err := publishWithPolicy(output, manifest, census, inputs, policy)
	if err != nil {
		t.Fatal(err)
	}
	verification, verified, err := verifyWithPolicy(context.Background(), publication.Directory, filepath.Join(root, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/m2-qualification-v1.schema.json"), &manifest.Source, policy)
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

func TestPerformanceTranscriptRequiresOneObservedSelectorPass(t *testing.T) {
	good := fixturePerformanceTranscript()
	if err := VerifyPerformanceTranscript(good); err != nil {
		t.Fatal(err)
	}
	mutants := map[string][]byte{
		"missing":       nil,
		"renamed":       []byte("{\"Action\":\"run\",\"Test\":\"TestRenamed\"}\n{\"Action\":\"pass\",\"Test\":\"TestRenamed\"}\n"),
		"skipped":       []byte("{\"Action\":\"run\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign\"}\n{\"Action\":\"skip\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign\"}\n"),
		"duplicate":     append(append([]byte(nil), good...), good...),
		"subtest":       []byte("{\"Action\":\"run\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign\"}\n{\"Action\":\"run\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign/forged\"}\n{\"Action\":\"pass\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign\"}\n"),
		"child-omitted": bytes.ReplaceAll(good, []byte("{\"Action\":\"run\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign/terminal-membership-mutant/acceptance_membership_omitted\"}\n{\"Action\":\"pass\",\"Test\":\"TestM2AcceptedAuthorityPerformanceCampaign/terminal-membership-mutant/acceptance_membership_omitted\"}\n"), nil),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if err := VerifyPerformanceTranscript(mutant); err == nil {
				t.Fatal("mutant transcript verified")
			}
		})
	}
}

func TestPerformanceReplayExclusionCannotClaimDurableMutation(t *testing.T) {
	referenceBytes, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs/programs/evidence/m2-performance-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := fixtureRawPerformance(t, "run-performance-replay", Source{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), GoVersion: "go1.26.7", GoModSHA256: strings.Repeat("c", 64), GoSumSHA256: strings.Repeat("d", 64)}, referenceBytes)
	replay := PerformanceSample{Index: len(raw.Samples), Kind: "mutation", OperationID: "operation-idempotent-replay", Included: false, ExclusionReason: "idempotent_replay", BeforeRevision: raw.Dataset.FinalRevision, AfterRevision: raw.Dataset.FinalRevision, ProjectionRevision: raw.Dataset.FinalRevision}
	raw.Samples = append(raw.Samples, replay)
	raw.ReplayExcluded = 1
	raw.Statistics[0] = PerformanceStatisticsFor("mutation", raw.MutationMS, raw.WarmupExcluded+raw.ReplayExcluded)
	if _, err := SummarizePerformance(raw); err != nil {
		t.Fatalf("truthful replay exclusion rejected: %v", err)
	}
	raw.Samples[len(raw.Samples)-1].MutationCount = 1
	raw.Samples[len(raw.Samples)-1].PublicationCount = 1
	raw.Samples[len(raw.Samples)-1].PublicationIDs = []string{"forged-publication"}
	if _, err := SummarizePerformance(raw); err == nil {
		t.Fatal("replay mutant fabricated a durable publication")
	}
}

func TestPerformanceRuntimeHostIsBoundOnlyToObservedRecovery(t *testing.T) {
	referenceBytes, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs/programs/evidence/m2-performance-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	source := Source{Commit: strings.Repeat("a", 40), Tree: strings.Repeat("b", 40), GoVersion: "go1.26.7", GoModSHA256: strings.Repeat("c", 64), GoSumSHA256: strings.Repeat("d", 64)}
	baseline := fixtureRawPerformance(t, "run-performance-runtime", source, referenceBytes)
	if _, err := SummarizePerformance(baseline); err != nil {
		t.Fatal(err)
	}
	t.Run("startup-label-cannot-become-operation", func(t *testing.T) {
		mutant := fixtureRawPerformance(t, "run-performance-runtime-operation", source, referenceBytes)
		mutant.Dataset.FamilyOperationCounts["family/runtime_host"] = 1
		delete(mutant.Dataset.FamilyRecoveryCounts, "family/runtime_host")
		if _, err := SummarizePerformance(mutant); err == nil {
			t.Fatal("runtime_host startup label became mutation evidence")
		}
	})
	t.Run("runtime-bootstrap-retained-without-journal-claim", func(t *testing.T) {
		mutant := fixtureRawPerformance(t, "run-performance-runtime-bootstrap", source, referenceBytes)
		mutant.Samples = append(mutant.Samples, PerformanceSample{Index: len(mutant.Samples), Kind: "mutation", OperationID: "runtime-host-start-3", DurationNS: int64(time.Millisecond), Included: false, ExclusionReason: "runtime_bootstrap"})
		mutant.RuntimeBootstrapExcluded = 1
		mutant.Statistics[0] = PerformanceStatisticsFor("mutation", mutant.MutationMS, mutant.WarmupExcluded+mutant.RuntimeBootstrapExcluded)
		if _, err := SummarizePerformance(mutant); err != nil {
			t.Fatalf("truthful runtime bootstrap exclusion rejected: %v", err)
		}
		mutant.Samples[len(mutant.Samples)-1].Family = "family/runtime_host"
		mutant.Samples[len(mutant.Samples)-1].EmittedFamilies = []string{"family/operation", "family/runtime_host"}
		mutant.Samples[len(mutant.Samples)-1].MutationCount = 5
		mutant.Samples[len(mutant.Samples)-1].PublicationCount = 1
		if _, err := SummarizePerformance(mutant); err == nil {
			t.Fatal("runtime bootstrap fabricated journal publication evidence")
		}
	})
	t.Run("recovery-family-receipt-omitted", func(t *testing.T) {
		mutant := fixtureRawPerformance(t, "run-performance-runtime-missing", source, referenceBytes)
		for index := range mutant.Samples {
			if mutant.Samples[index].Kind == "recovery" {
				mutant.Samples[index].Family = ""
				mutant.Samples[index].EmittedFamilies = nil
				break
			}
		}
		if _, err := SummarizePerformance(mutant); err == nil {
			t.Fatal("runtime_host recovery family omission verified")
		}
	})
	t.Run("warmup-label-claims-post-checkpoint-evidence", func(t *testing.T) {
		mutant := fixtureRawPerformance(t, "run-performance-warmup-claim", source, referenceBytes)
		mutant.Samples[0].Family = "family/mission"
		mutant.Samples[0].EmittedFamilies = []string{"family/mission", "family/operation"}
		if _, err := SummarizePerformance(mutant); err == nil {
			t.Fatal("pre-checkpoint warmup label became suffix evidence")
		}
	})
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
		"missing-qualification-candidate": func(_ *Manifest, _ *Census, inputs *[]ArtifactInput) {
			*inputs = slices.DeleteFunc(*inputs, func(input ArtifactInput) bool { return input.Kind == "qualification-admission" })
		},
		"mutated-qualification-candidate": func(_ *Manifest, _ *Census, inputs *[]ArtifactInput) {
			for index := range *inputs {
				if (*inputs)[index].Kind == "qualification-admission" {
					(*inputs)[index].Data = bytes.Replace((*inputs)[index].Data, []byte(`"release_ready": false`), []byte(`"release_ready": true`), 1)
				}
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			manifest, census, inputs, policy := completeSidecarFreeFixture(t)
			mutate(&manifest, &census, &inputs)
			publication, err := publishWithPolicy(t.TempDir(), manifest, census, inputs, policy)
			if err != nil {
				return
			}
			if _, _, err := verifyWithPolicy(context.Background(), publication.Directory, schema, nil, policy); err == nil {
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

func TestPublicationAcceptanceRequiresFamilyOutboxAndExternalCutEvidence(t *testing.T) {
	required := []string{
		"fault/family-bound-publication-cuts",
		"fault/outbox-bound-publication-cuts",
		"fault/external-before-call",
		"fault/external-after-success-before-observation",
		"fault/external-after-observation-before-result",
	}
	for _, requirementID := range []string{"FR-004", "FR-025"} {
		proof := proofForRequirement(requirementID)
		for _, evidenceID := range required {
			if !slices.Contains(proof.censusIDs, evidenceID) {
				t.Fatalf("%s omitted mandatory publication-cut evidence %s", requirementID, evidenceID)
			}
		}
	}

	manifest, census, inputs := completeFixture(t)
	artifacts := make(map[string]Artifact, len(inputs))
	var matrix AcceptanceMatrix
	for _, input := range inputs {
		artifacts[input.Path] = Artifact{Path: input.Path, Kind: input.Kind, SHA256: SHA256(input.Data)}
		if input.Kind == "acceptance-matrix" {
			if err := DecodeStrict(input.Data, &matrix); err != nil {
				t.Fatal(err)
			}
		}
	}
	for index := range matrix.Requirements {
		if matrix.Requirements[index].ID != "FR-025" {
			continue
		}
		matrix.Requirements[index].Evidence = slices.DeleteFunc(matrix.Requirements[index].Evidence, func(evidence AcceptanceEvidence) bool {
			return evidence.CensusID == "fault/family-bound-publication-cuts" || evidence.CensusID == "fault/outbox-bound-publication-cuts"
		})
	}
	mutant, _ := CanonicalJSON(matrix)
	if err := VerifyAcceptanceMatrix(mutant, manifest, census, artifacts); err == nil {
		t.Fatal("acceptance matrix verified after both family-bound campaigns were omitted")
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

func TestFamilyCutApplicabilityMatrixIsClosedAndRecomputed(t *testing.T) {
	manifest, census, inputs := completeFixture(t)
	artifacts := make(map[string]Artifact, len(inputs))
	var matrix FamilyCutMatrix
	for _, input := range inputs {
		artifacts[input.Path] = Artifact{Path: input.Path, Kind: input.Kind, SHA256: SHA256(input.Data)}
		if input.Kind == "campaign-composition" {
			if err := DecodeStrict(input.Data, &matrix); err != nil {
				t.Fatal(err)
			}
		}
	}
	encoded, _ := CanonicalJSON(matrix)
	if err := VerifyFamilyCutMatrix(encoded, manifest, census, artifacts); err != nil {
		t.Fatal(err)
	}
	mutants := map[string]func(*FamilyCutMatrix){
		"missing-family": func(value *FamilyCutMatrix) { value.Families = value.Families[1:] },
		"substituted-cut": func(value *FamilyCutMatrix) {
			for _, candidate := range value.Families {
				if candidate.FamilyID == "family/outbox" {
					value.Families[0].Cuts[0].ArtifactPath = candidate.Cuts[0].ArtifactPath
					value.Families[0].Cuts[0].ArtifactSHA256 = candidate.Cuts[0].ArtifactSHA256
				}
			}
		},
		"false-non-applicability": func(value *FamilyCutMatrix) {
			value.Families[0].Cuts[0] = CutEvidence{CutID: value.Families[0].Cuts[0].CutID, Applicability: "not_applicable", Reason: "invented"}
		},
		"runtime-bootstrap-overclaim": func(value *FamilyCutMatrix) {
			for index := range value.Families {
				if value.Families[index].FamilyID == "family/runtime_host" {
					value.Families[index].Cuts[0] = value.Families[0].Cuts[0]
					value.Families[index].Cuts[0].CutID = familyCutIDs[0]
				}
			}
		},
		"campaign-source-substitution": func(value *FamilyCutMatrix) {
			value.Families[0].CampaignSourceSHA256 = strings.Repeat("f", 64)
		},
		"boundary-source-substitution": func(value *FamilyCutMatrix) {
			value.BoundarySourceSHA256 = strings.Repeat("f", 64)
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			clone := matrix
			clone.Families = append([]FamilyCutEvidence(nil), matrix.Families...)
			for index := range clone.Families {
				clone.Families[index].Cuts = append([]CutEvidence(nil), matrix.Families[index].Cuts...)
			}
			mutate(&clone)
			data, _ := CanonicalJSON(clone)
			if err := VerifyFamilyCutMatrix(data, manifest, census, artifacts); err == nil {
				t.Fatal("family/cut mutant verified")
			}
		})
	}
}

func TestFamilyCutReceiptRejectsSeedAndInvariantSubstitution(t *testing.T) {
	_, _, inputs := completeFixture(t)
	var receipt GoTestReceipt
	for _, input := range inputs {
		if input.Path == "campaigns/fault-family-bound-publication-cuts.receipt.json" {
			if err := DecodeStrict(input.Data, &receipt); err != nil {
				t.Fatal(err)
			}
		}
	}
	definition := FrozenDefinitions()[0]
	for _, candidate := range FrozenDefinitions() {
		if candidate.ID == "fault/family-bound-publication-cuts" {
			definition = candidate
		}
	}
	if len(receipt.FamilyCutIterations) == 0 {
		t.Fatal("fixture omitted family/cut receipts")
	}
	seedMutant := append([]iterationreceipt.FamilyCutRecord(nil), receipt.FamilyCutIterations...)
	seedMutant[0].Seed = "substituted"
	if err := validateFamilyCutIterations(definition, seedMutant); err == nil {
		t.Fatal("family/cut seed mutant survived")
	}
	original := receipt.FamilyCutIterations[0]
	var facts map[string]any
	if err := json.Unmarshal(original.ObservedFacts, &facts); err != nil {
		t.Fatal(err)
	}
	missingLedger, err := iterationreceipt.NewFamilyCut(original.DefinitionID, original.Family, original.Cut, original.Index, original.Seed, original.PlanSHA256, original.PublicationID, original.EmittedFamilies, original.Outcome, time.Duration(original.DurationNanoseconds), facts, map[string]any{"unresolved": 0, "ledger_size": 2, "one_terminal_projection": true, "substitution_refused": true})
	if err != nil {
		t.Fatal(err)
	}
	invariantMutant := append([]iterationreceipt.FamilyCutRecord(nil), receipt.FamilyCutIterations...)
	invariantMutant[0] = missingLedger
	if err := validateFamilyCutIterations(definition, invariantMutant); err == nil {
		t.Fatal("family/cut invariant-field mutant survived")
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
	manifest, census, inputs, _ := completeFixtureWithPolicy(t, false)
	return manifest, census, inputs
}

func completeSidecarFreeFixture(t *testing.T) (Manifest, Census, []ArtifactInput, evidenceVerificationPolicy) {
	return completeFixtureWithPolicy(t, true)
}

func completeFixtureWithPolicy(t *testing.T, sidecarFree bool) (Manifest, Census, []ArtifactInput, evidenceVerificationPolicy) {
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
		for processIndex := range processes {
			processRaw := append([]byte(nil), fixtureEvent(definition.Package, "run", definition.Selector)...)
			if isFamilyCutDefinition(definition.ID) {
				families := []string{"acceptance", "artifact_catalog", "artifact_revision", "execution_slot", "external_effect", "handoff_slot", "integration_attempt", "integration_target", "mission", "operation", "review_slot", "workgraph", "workspace"}
				if definition.ID == "fault/outbox-bound-publication-cuts" {
					families = []string{"outbox"}
				}
				cuts := []string{"acknowledgement-before-projection", "before-publication", "durable-before-acknowledgement", "lost-acknowledgement", "malformed-acknowledgement"}
				for _, family := range families {
					emitted := []string{"operation"}
					if family != "operation" {
						emitted = append(emitted, family)
						slices.Sort(emitted)
					}
					for _, cut := range cuts {
						for index := 0; index < 100; index++ {
							seed := fmt.Sprintf("%s/%s/%03d/%d", family, cut, index, 1)
							if family == "outbox" {
								seed = fmt.Sprintf("outbox/%s/%03d", cut, index)
							}
							record, err := iterationreceipt.NewFamilyCut(definition.ID, family, cut, index, seed, strings.Repeat("a", 64), strings.Repeat("b", 64), emitted, "interrupted-reconciled-once", time.Duration(index+1), map[string]any{"cut": cut, "qualified_family": family, "plan_index": 1, "provider_calls": 1, "publication_id": strings.Repeat("b", 64), "emitted_families": emitted}, map[string]any{"unresolved": 0, "ledger_size": 2, "expected_ledger_size": 2, "one_terminal_projection": true, "substitution_refused": true})
							if err != nil {
								t.Fatal(err)
							}
							processRaw = append(processRaw, fixtureFamilyCutEvent(definition.Package, definition.Selector, record)...)
						}
					}
				}
			}
			for _, child := range childContracts[definition.ID] {
				processRaw = append(processRaw, fixtureEvent(definition.Package, "run", child)...)
				processRaw = append(processRaw, fixtureEvent(definition.Package, "pass", child)...)
			}
			if contract, ok := iterationContracts[definition.ID]; ok {
				outcomes := make([]string, 0, len(contract.outcomes))
				for outcome := range contract.outcomes {
					outcomes = append(outcomes, outcome)
				}
				slices.Sort(outcomes)
				for index := range definition.ExpectedRepetitions {
					record, err := iterationreceipt.New(definition.ID, index, fmt.Sprintf("fixture-seed-%03d", index), contract.boundary, outcomes[index%len(outcomes)], time.Duration(index+1), map[string]any{"index": index}, map[string]any{"verified": true})
					if err != nil {
						t.Fatal(err)
					}
					processRaw = append(processRaw, fixtureIterationEvent(definition.Package, definition.Selector, record)...)
				}
			}
			if definition.ID == "fault/adversarial-tracker" {
				for _, child := range expectedWP14Children() {
					processRaw = append(processRaw, fixtureEvent(definition.Package, "run", child)...)
					selector := child[strings.LastIndex(child, "/")+1:]
					if contract, ok := wp14IterationContracts[selector]; ok {
						boundaries, outcomes := keys(contract.boundaries), keys(contract.outcomes)
						for index := range contract.count {
							record, err := iterationreceipt.New(child, index, fmt.Sprintf("wp14-seed-%03d", index), boundaries[index%len(boundaries)], outcomes[index%len(outcomes)], time.Duration(index+1), map[string]any{"index": index}, map[string]bool{"verified": true})
							if err != nil {
								t.Fatal(err)
							}
							processRaw = append(processRaw, fixtureIterationEvent(definition.Package, child, record)...)
						}
					}
					processRaw = append(processRaw, fixtureEvent(definition.Package, "pass", child)...)
				}
			}
			processRaw = append(processRaw, fixtureEvent(definition.Package, "pass", definition.Selector)...)
			raw = append(raw, processRaw...)
			if requiresProcessIterationReceipts(definition) {
				seed := SHA256([]byte(fmt.Sprintf("%s\x00%s\x00%d", runID, definition.ID, processIndex)))
				shuffleNumber, _ := strconv.ParseUint(seed[:15], 16, 64)
				record, err := iterationreceipt.New(definition.ID, processIndex, seed, "go-test-process", "pass", time.Duration(processIndex+1), map[string]any{"process_transcript_sha256": SHA256(processRaw), "process_index": processIndex, "shuffle_seed": strconv.FormatUint(shuffleNumber, 10)}, map[string]any{"selected_test": definition.Selector, "process_exit": 0})
				if err != nil {
					t.Fatal(err)
				}
				raw = append(raw, fixtureIterationEvent(definition.Package, definition.Selector, record)...)
			}
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
	campaignInputs = append(campaignInputs,
		ArtifactInput{Path: "contracts/family-bound-publication-cuts-source.go", Kind: "campaign-cut-source", Data: []byte("package scenario\n")},
		ArtifactInput{Path: "contracts/outbox-bound-publication-cuts-source.go", Kind: "campaign-cut-source", Data: []byte("package gapdb\n")},
		ArtifactInput{Path: "contracts/publication-boundary-source.go", Kind: "campaign-boundary-source", Data: []byte("package v2\n")},
	)
	familyCutBytes, err := BuildFamilyCutMatrix(runID, source, census, campaignInputs)
	if err != nil {
		t.Fatal(err)
	}
	campaignInputs = append(campaignInputs, ArtifactInput{Path: "campaigns/family-cut-applicability.json", Kind: "campaign-composition", Data: familyCutBytes})
	summary, err := Summarize(entries, 0)
	if err != nil {
		t.Fatal(err)
	}
	referenceBytes, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs/programs/evidence/m2-performance-reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := fixtureRawPerformance(t, runID, source, referenceBytes)
	performance, err := SummarizePerformance(raw)
	if err != nil {
		t.Fatal(err)
	}
	rawBytes, _ := CanonicalJSON(raw)
	sourceBytes, _ := CanonicalJSON(source)
	profileBytes := []byte("{\"profile_id\":\"implementer-ivan\"}\n")
	producerBytes, _ := CanonicalJSON(ProducerIdentity{SchemaVersion: 1, ExecutorActorID: "executor", ProcessID: "process-1", ImplementationContributors: []string{"implementer"}, ProfileID: "implementer-ivan", ProfileRevision: SHA256(profileBytes)})
	var sidecarBytes []byte
	if sidecarFree {
		sidecarBytes = []byte("sidecar-free structural fixture\n")
	} else {
		sidecarPath := os.Getenv("SPEC_KITTY_M2_GAPDBD")
		if sidecarPath == "" {
			t.Skip("SPEC_KITTY_M2_GAPDBD must name the exact M2 qualification candidate")
		}
		sidecarBytes, err = os.ReadFile(sidecarPath)
		if err != nil || SHA256(sidecarBytes) != QualificationCandidateSidecarSHA256 {
			t.Fatalf("qualification fixture sidecar unavailable: %v", err)
		}
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
	qualificationSHA := WP16AbsenceSidecarSHA256
	if sidecarFree {
		qualificationSHA = SHA256(sidecarBytes)
	}
	inputs = append(inputs, fixtureAbsenceInputs(t, source, qualificationSHA)...)
	inputs = append(inputs,
		ArtifactInput{Path: "contract-m2.json", Kind: "contract", Data: schemaBytes},
		ArtifactInput{Path: "m1.json", Kind: "m1-admission", Data: []byte("{}\n")},
		ArtifactInput{Path: "performance.json", Kind: "performance", Data: rawBytes},
		ArtifactInput{Path: "performance.go-test.jsonl", Kind: "performance-raw", Data: fixturePerformanceTranscript()},
		ArtifactInput{Path: "performance-reference.json", Kind: "performance-reference", Data: referenceBytes},
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
		fileFixtureInput(t, "m2-gapdb-qualification-candidate.json", "qualification-admission", "docs/programs/evidence/m2-gapdb-qualification-candidate.json"),
		ArtifactInput{Path: "sidecar/gapdbd", Kind: "sidecar", Data: sidecarBytes},
		ArtifactInput{Path: "producer-identity.json", Kind: "producer-identity", Data: producerBytes},
		ArtifactInput{Path: "producer-profile.json", Kind: "producer-profile", Data: profileBytes},
		ArtifactInput{Path: "contract-events.json", Kind: "contract", Data: []byte("{}\n")},
		ArtifactInput{Path: "independent-review-input.md", Kind: "review-input", Data: []byte("review instructions only\n")},
	)
	manifest := Manifest{SchemaVersion: 1, ProgramID: ProgramID, MissionID: MissionID, RunID: runID, Source: source, M1Admission: M1Admission{AdmittedM1AdoptionSHA256, AdmittedM1EvidenceSHA256, AdmittedM1TupleSHA256, AdmittedM1SidecarSHA256}, Census: summary, Performance: performance, Verdict: "pass"}
	acceptance, err := BuildAcceptanceMatrix(manifest, census, inputs)
	if err != nil {
		t.Fatal(err)
	}
	inputs = append(inputs, ArtifactInput{Path: "acceptance-matrix.json", Kind: "acceptance-matrix", Data: acceptance})
	policy := frozenEvidenceVerificationPolicy()
	if sidecarFree {
		candidateBytes := fileFixtureInput(t, "unused", "unused", "docs/programs/evidence/m2-gapdb-qualification-candidate.json").Data
		policy = evidenceVerificationPolicy{
			wp16AbsenceSidecarSHA256: qualificationSHA,
			verifyCandidateAdmission: func(path string) error {
				observed, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(observed, candidateBytes) {
					return fmt.Errorf("fixture candidate admission differs")
				}
				return nil
			},
		}
	}
	return manifest, census, inputs, policy
}

func fileFixtureInput(t *testing.T, path, kind, repositoryPath string) ArtifactInput {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), filepath.FromSlash(repositoryPath)))
	if err != nil {
		t.Fatal(err)
	}
	return ArtifactInput{Path: path, Kind: kind, Data: data}
}

func fixtureFamilyCutEvent(pkg, test string, record iterationreceipt.FamilyCutRecord) []byte {
	encoded, _ := iterationreceipt.EncodeFamilyCut(record)
	event, _ := json.Marshal(goTestEvent{Action: "output", Package: strings.TrimPrefix(pkg, "./"), Test: test, Output: iterationreceipt.FamilyCutPrefix + string(encoded) + "\n"})
	return append(event, '\n')
}

func fixtureRawPerformance(t *testing.T, runID string, source Source, referenceBytes []byte) RawPerformance {
	t.Helper()
	var reference PerformanceReference
	if err := DecodeStrict(referenceBytes, &reference); err != nil {
		t.Fatal(err)
	}
	familyCounts := map[string]int{}
	operationFamilies := slices.DeleteFunc(append([]string(nil), reference.FamilyIDs...), func(family string) bool { return family == "family/runtime_host" })
	raw := RawPerformance{
		SchemaVersion: 1, RunID: runID, SourceCommit: source.Commit, SourceTree: source.Tree,
		ReferenceProfileSHA256: SHA256(referenceBytes),
		Environment:            PerformanceEnvironment{CPUModel: reference.CPUModel, LogicalCPUs: reference.LogicalCPUs, PhysicalCores: reference.PhysicalCores, MemoryBytes: reference.MemoryMinBytes, KernelSystem: reference.KernelSystem, KernelRelease: reference.KernelRelease, Architecture: reference.Architecture, Filesystem: reference.Filesystem, StorageDevice: reference.StorageDevicePrefix + "0n1p1", PowerGovernor: reference.PowerGovernor, BoostEnabled: reference.BoostEnabled, Load1: 0.1, GoVersion: reference.GoVersion, CGOEnabled: reference.CGOEnabled, SidecarSHA256: QualificationCandidateSidecarSHA256, QualifierSHA256: SHA256(fixtureBinary), ProductSHA256: SHA256(fixtureBinary)},
		Dataset:                PerformanceDataset{Generator: reference.DatasetGenerator, WarmupOperations: 10, SuffixOperations: 10000, CheckpointRevision: 100, FamilyOperationCounts: familyCounts, FamilyRecoveryCounts: map[string]int{"family/runtime_host": 30}, ExpectedProjectionSHA: stringOf('3', 64), FinalRevision: 100100},
		RecoveryOperations:     10000, WarmupExcluded: 10, Method: "monotonic-nearest-rank-inclusive",
	}
	for index := 0; index < 10; index++ {
		raw.Samples = append(raw.Samples, PerformanceSample{Index: len(raw.Samples), Kind: "mutation", OperationID: fmt.Sprintf("warmup-%d", index), DurationNS: int64(time.Millisecond), Included: false, ExclusionReason: "warmup"})
	}
	for index := 0; index < 10000; index++ {
		family := operationFamilies[index%len(operationFamilies)]
		familyCounts[family]++
		raw.MutationMS = append(raw.MutationMS, .2)
		raw.Samples = append(raw.Samples, PerformanceSample{Index: len(raw.Samples), Kind: "mutation", Family: family, OperationID: fmt.Sprintf("suffix-%d", index), EmittedFamilies: []string{family}, DurationNS: int64(200 * time.Microsecond), Included: true, BeforeRevision: uint64(100 + index), AfterRevision: uint64(101 + index), MutationCount: 1, PublicationCount: 1, ProjectionRevision: uint64(101 + index), PublicationIDs: []string{fmt.Sprintf("publication-suffix-%d", index)}})
	}
	for index := 0; index < 10000; index++ {
		raw.QueryMS = append(raw.QueryMS, .1)
		raw.Samples = append(raw.Samples, PerformanceSample{Index: len(raw.Samples), Kind: "query", Family: "family/mission", DurationNS: int64(100 * time.Microsecond), Included: true})
	}
	for index := 0; index < 30; index++ {
		raw.RecoveryMS = append(raw.RecoveryMS, 1)
		raw.Samples = append(raw.Samples, PerformanceSample{Index: len(raw.Samples), Kind: "recovery", Family: "family/runtime_host", EmittedFamilies: []string{"family/runtime_host"}, DurationNS: int64(time.Millisecond), Included: true, ProjectionSHA256: raw.Dataset.ExpectedProjectionSHA, ObservedOperations: 10000, ProjectionRevision: raw.Dataset.FinalRevision})
	}
	raw.Statistics = []PerformanceStatistics{PerformanceStatisticsFor("mutation", raw.MutationMS, 10), PerformanceStatisticsFor("query", raw.QueryMS, 0), PerformanceStatisticsFor("recovery", raw.RecoveryMS, 0)}
	return raw
}

func fixturePerformanceTranscript() []byte {
	var transcript bytes.Buffer
	fmt.Fprintf(&transcript, "{\"Action\":\"run\",\"Test\":%q}\n", PerformanceCampaignSelectorV1)
	for _, id := range PerformanceTerminalMembershipMutantIDsV1() {
		child := PerformanceCampaignSelectorV1 + "/terminal-membership-mutant/" + id
		fmt.Fprintf(&transcript, "{\"Action\":\"run\",\"Test\":%q}\n", child)
		fmt.Fprintf(&transcript, "{\"Action\":\"pass\",\"Test\":%q}\n", child)
	}
	fmt.Fprintf(&transcript, "{\"Action\":\"pass\",\"Test\":%q}\n", PerformanceCampaignSelectorV1)
	return transcript.Bytes()
}

func fixtureAbsenceInputs(t *testing.T, source Source, qualificationSidecarSHA256 string) []ArtifactInput {
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
	runtimeResult, _ := CanonicalJSON(absenceRuntimeReceipt{SchemaVersion: 2, Expected: runtimeExpected, Run: runtimeExpected, Passed: runtimeExpected, SidecarSHA256: qualificationSidecarSHA256, RawEventsSHA256: SHA256(runtimeRaw), Verdict: "pass"})
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

func fixtureIterationEvent(pkg, test string, record iterationreceipt.Record) []byte {
	encoded, _ := iterationreceipt.Encode(record)
	output := "    receipt_test.go:1: " + iterationreceipt.Prefix + string(encoded) + "\n"
	return []byte(fmt.Sprintf("{\"Action\":\"output\",\"Package\":%q,\"Test\":%q,\"Output\":%q}\n", "github.com/spec-kitty/go-kitty3/"+strings.TrimPrefix(pkg, "./"), test, output))
}

func keys(input map[string]struct{}) []string {
	result := make([]string, 0, len(input))
	for value := range input {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func stringOf(character byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = character
	}
	return string(result)
}
