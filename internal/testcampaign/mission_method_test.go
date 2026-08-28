package testcampaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMissionMethodMakeAndWorkflowContracts(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	read := func(path string) string {
		contents, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return string(contents)
	}
	makefile := read("Makefile")
	for _, required := range []string{
		"mission-contract:", "-profile mission-contract", "mission-acceptance:", "-profile mission-acceptance",
		"mission-adversarial:", "-profile mission-adversarial", "-campaign mission_full",
		"phase3-publication:", "PHASE3_PUBLICATION_GATE=1", "TestPhase3PublicationCheckoutGateUsesTypedEnvironment",
		"quality: preflight contract beads-contract mission-contract mission-acceptance race static dependencies",
	} {
		if !strings.Contains(makefile, required) {
			t.Fatalf("Makefile contract omitted %q", required)
		}
	}
	quality := read(".github/workflows/quality.yml")
	for _, required := range []string{"permissions:\n  contents: read", "timeout-minutes: 15", "go-version: 1.26.5", "duration_seconds\" -ge 600", "if: always()", "make quality"} {
		if !strings.Contains(quality, required) {
			t.Fatalf("quality workflow contract omitted %q", required)
		}
	}
	if strings.Contains(quality, "mission-adversarial") {
		t.Fatal("slow mission campaign leaked into pull-request workflow")
	}
	full := read(".github/workflows/full-campaign.yml")
	for _, required := range []string{"schedule:", "workflow_dispatch:", "go-version: 1.26.5", "make full-campaign", "if: always()"} {
		if !strings.Contains(full, required) {
			t.Fatalf("full workflow contract omitted %q", required)
		}
	}
	for path, contents := range map[string]string{"quality": quality, "full": full} {
		if strings.Contains(contents, "actions/checkout@v4") || strings.Contains(contents, "actions/setup-go@v6") || strings.Contains(contents, "actions/upload-artifact@v4") {
			t.Fatalf("%s workflow uses a mutable action tag", path)
		}
	}
}

func TestPhase3PublicationClosedSchemaAndDocumentationMutants(t *testing.T) {
	root := phase3RepositoryRoot(t)
	if err := ValidatePhase3Publication(root); err != nil {
		t.Fatalf("checked-in Phase 3 publication: %v", err)
	}
	fixture := phase3PublicationFixture(t, root)
	phase3UpgradeFixtureSchema(t, fixture)
	if err := ValidatePhase3Publication(fixture); err != nil {
		t.Fatalf("schema-v2 fixture: %v", err)
	}

	tests := map[string]func(*testing.T, string){
		"broken_link": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/README.md"), "[`qualification-evidence.json`](qualification-evidence.json)", "[`qualification-evidence.json`](missing-evidence.json)")
		},
		"malformed_mermaid": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/README.md"), "flowchart LR", "flowchar LR")
		},
		"dangling_mermaid_arrow": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/README.md"), "Source --> Portable", "Source -->")
		},
		"nonexistent_command": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/README.md"), "make mission-contract", "make mission-contract-missing")
		},
		"nonexistent_non_make_command": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/README.md"), "git show --stat", "phase3-command-that-does-not-exist --stat")
		},
		"claim_test_mismatch": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/results.md"), "`TestMissionMethodSemanticDriftMutations1000`", "`TestMissionMethodSemanticDriftMutations999`")
			phase3RewriteChecksums(t, path)
		},
		"missing_limitation": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/limitations.md"), "Gapdb was not adopted", "The alternate store was not adopted")
			phase3RewriteChecksums(t, path)
		},
		"checksum_change": func(t *testing.T, path string) {
			phase3Replace(t, filepath.Join(path, "docs/phase3-evidence/SHA256SUMS"), "qualification-evidence.json", "qualification-evidence.json.changed")
		},
		"removed_failed_history": func(t *testing.T, path string) {
			phase3MutateCensus(t, path, func(record map[string]any) {
				attempts := record["failed_or_non_admissible_attempts"].([]any)
				record["failed_or_non_admissible_attempts"] = slices.DeleteFunc(attempts, func(value any) bool { return value.(map[string]any)["id"] == "08" })
			})
		},
		"falsified_publication_fail": func(t *testing.T, path string) {
			phase3MutateQualification(t, path, func(record map[string]any) { record["verdict"] = "fail" })
		},
		"falsified_failed_attempt_pass": func(t *testing.T, path string) {
			phase3MutateAttempt(t, path, "02", "pass")
		},
		"falsified_non_admissible_pass": func(t *testing.T, path string) {
			phase3MutateAttempt(t, path, "01", "pass")
		},
		"unknown_schema_field": func(t *testing.T, path string) {
			phase3MutateQualification(t, path, func(record map[string]any) { record["unreviewed"] = true })
		},
		"duplicate_json_key": func(t *testing.T, path string) {
			file := filepath.Join(path, "docs/phase3-evidence/qualification-evidence.json")
			contents, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			contents = []byte(strings.Replace(string(contents), "{\n", "{\n  \"schema_version\": 2,\n", 1))
			if err = os.WriteFile(file, contents, 0o644); err != nil {
				t.Fatal(err)
			}
			phase3RewriteChecksums(t, path)
		},
		"oversized_json": func(t *testing.T, path string) {
			file := filepath.Join(path, "docs/phase3-evidence/qualification-evidence.json")
			output, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			padding := make([]byte, (2<<20)+1)
			for index := range padding {
				padding[index] = ' '
			}
			if _, err = output.Write(padding); err != nil {
				output.Close()
				t.Fatal(err)
			}
			if err = output.Close(); err != nil {
				t.Fatal(err)
			}
			phase3RewriteChecksums(t, path)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := phase3PublicationFixture(t, fixture)
			mutate(t, candidate)
			if err := ValidatePhase3Publication(candidate); err == nil {
				t.Fatal("publication mutant survived")
			}
		})
	}
}

func TestPhase3PublicationEveryRequiredFailureIdentityIsLoadBearing(t *testing.T) {
	fixture := phase3PublicationFixture(t, phase3RepositoryRoot(t))
	phase3UpgradeFixtureSchema(t, fixture)
	for _, id := range []string{
		"01", "02", "03", "04", "05", "06", "07", "08", "09",
		"real-11", "real-12", "real-13", "real-14", "real-15", "real-16", "real-17",
		"review-cycle-1", "20", "21", "real-23", "review-cycle-2", "25",
	} {
		t.Run(id, func(t *testing.T) {
			candidate := phase3PublicationFixture(t, fixture)
			phase3MutateCensus(t, candidate, func(record map[string]any) {
				attempts := record["failed_or_non_admissible_attempts"].([]any)
				record["failed_or_non_admissible_attempts"] = slices.DeleteFunc(attempts, func(value any) bool {
					return value.(map[string]any)["id"] == id
				})
			})
			if err := ValidatePhase3Publication(candidate); err == nil {
				t.Fatalf("required failed-attempt identity %s deletion survived", id)
			}
		})
	}
}

func TestPhase3PublicationMachineEvidenceMutants(t *testing.T) {
	fixture := phase3PublicationFixture(t, phase3RepositoryRoot(t))
	phase3UpgradeFixtureSchema(t, fixture)
	mutants := map[string]func(map[string]any){
		"race_winner": func(record map[string]any) {
			races := record["scale_proofs"].(map[string]any)["two_process_finalization"].(map[string]any)
			races["winners"] = float64(99)
		},
		"fault_identity": func(record map[string]any) {
			faults := record["scale_proofs"].(map[string]any)["crash_boundaries"].(map[string]any)["faults"].([]any)
			faults[0].(map[string]any)["id"] = "MM-MISSING"
		},
		"fault_terminal": func(record map[string]any) {
			faults := record["scale_proofs"].(map[string]any)["crash_boundaries"].(map[string]any)["faults"].([]any)
			faults[1].(map[string]any)["terminal"] = float64(0)
		},
		"projection_kind": func(record map[string]any) {
			kinds := record["scale_proofs"].(map[string]any)["projection_trials"].(map[string]any)["trial_kinds"].(map[string]any)
			kinds["delete"] = float64(12)
		},
		"projection_mutant": func(record map[string]any) {
			mutants := record["scale_proofs"].(map[string]any)["projection_trials"].(map[string]any)["mutants"].([]any)
			mutants[0].(map[string]any)["verdict"] = "survived"
		},
	}
	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			candidate := phase3PublicationFixture(t, fixture)
			phase3MutateQualification(t, candidate, mutate)
			if err := ValidatePhase3Publication(candidate); err == nil {
				t.Fatal("machine-evidence mutant survived")
			}
		})
	}
	t.Run("supporting_test_identity", func(t *testing.T) {
		candidate := phase3PublicationFixture(t, fixture)
		phase3MutateCensus(t, candidate, func(record map[string]any) {
			profile := record["supporting_profiles"].([]any)[0].(map[string]any)
			profile["tests"].([]any)[0] = "TestSubstituted"
		})
		if err := ValidatePhase3Publication(candidate); err == nil {
			t.Fatal("supporting-profile test substitution survived")
		}
	})
}

func TestPhase3PublicationCheckoutRequiresCleanDocsOnlyAttestation(t *testing.T) {
	source := phase3RepositoryRoot(t)
	clone := filepath.Join(t.TempDir(), "checkout")
	phase3Git(t, "clone", "--quiet", "--shared", source, clone)
	phase3GitIn(t, clone, "config", "user.name", "Phase3 Validator")
	phase3GitIn(t, clone, "config", "user.email", "validator@example.invalid")
	phase3CopyFixtureTree(t, filepath.Join(source, "docs", "phase3-evidence"), filepath.Join(clone, "docs", "phase3-evidence"))
	makefile, err := os.ReadFile(filepath.Join(source, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(clone, "Makefile"), makefile, 0o644); err != nil {
		t.Fatal(err)
	}
	phase3GitIn(t, clone, "add", "docs/phase3-evidence", "Makefile")
	phase3GitIn(t, clone, "commit", "--allow-empty", "-m", "payload")
	payload := phase3GitIn(t, clone, "rev-parse", "HEAD")
	index := filepath.Join(clone, "docs/phase3-evidence/README.md")
	contents, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(index, append(contents, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	phase3GitIn(t, clone, "add", "docs/phase3-evidence/README.md")
	phase3GitIn(t, clone, "commit", "-m", "attestation")
	attestation := phase3GitIn(t, clone, "rev-parse", "HEAD")
	if err = ValidatePhase3PublicationCheckout(clone, payload, attestation); err != nil {
		t.Fatalf("clean docs-only checkout: %v", err)
	}
	if err = os.WriteFile(filepath.Join(clone, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = ValidatePhase3PublicationCheckout(clone, payload, attestation); err == nil {
		t.Fatal("dirty checkout mutant survived")
	}
	if err = os.Remove(filepath.Join(clone, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(clone, "Makefile"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	phase3GitIn(t, clone, "add", "Makefile")
	phase3GitIn(t, clone, "commit", "-m", "illegal attestation")
	illegal := phase3GitIn(t, clone, "rev-parse", "HEAD")
	if err = ValidatePhase3PublicationCheckout(clone, payload, illegal); err == nil {
		t.Fatal("non-documentation attestation mutant survived")
	}

	for _, mutant := range []struct {
		name string
		path string
		data []byte
	}{
		{name: "later_product", path: "Makefile", data: []byte("\n# later product\n")},
		{name: "later_docs", path: "README.md", data: []byte("\nLater documentation.\n")},
	} {
		t.Run(mutant.name, func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "checkout")
			phase3Git(t, "clone", "--quiet", "--shared", clone, candidate)
			phase3GitIn(t, candidate, "checkout", "--quiet", attestation)
			phase3GitIn(t, candidate, "config", "user.name", "Phase3 Validator")
			phase3GitIn(t, candidate, "config", "user.email", "validator@example.invalid")
			file := filepath.Join(candidate, mutant.path)
			output, openErr := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
			if openErr != nil {
				t.Fatal(openErr)
			}
			if _, writeErr := output.Write(mutant.data); writeErr != nil {
				output.Close()
				t.Fatal(writeErr)
			}
			if closeErr := output.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			phase3GitIn(t, candidate, "add", mutant.path)
			phase3GitIn(t, candidate, "commit", "-m", mutant.name)
			if validateErr := ValidatePhase3PublicationCheckout(candidate, payload, attestation); validateErr == nil {
				t.Fatalf("%s commit after named attestation survived", mutant.name)
			}
		})
	}
	t.Run("later_empty", func(t *testing.T) {
		candidate := filepath.Join(t.TempDir(), "checkout")
		phase3Git(t, "clone", "--quiet", "--shared", clone, candidate)
		phase3GitIn(t, candidate, "checkout", "--quiet", attestation)
		phase3GitIn(t, candidate, "config", "user.name", "Phase3 Validator")
		phase3GitIn(t, candidate, "config", "user.email", "validator@example.invalid")
		phase3GitIn(t, candidate, "commit", "--allow-empty", "-m", "later empty")
		if validateErr := ValidatePhase3PublicationCheckout(candidate, payload, attestation); validateErr == nil {
			t.Fatal("empty commit after named attestation survived")
		}
	})
}

func TestPhase3PublicationCheckoutGateUsesTypedEnvironment(t *testing.T) {
	if os.Getenv("PHASE3_PUBLICATION_GATE") != "1" {
		t.Skip("executed by make phase3-publication with explicit immutable identities")
	}
	root := os.Getenv("PHASE3_PUBLICATION_ROOT")
	payload := os.Getenv("PHASE3_PAYLOAD_COMMIT")
	attestation := os.Getenv("PHASE3_ATTESTATION_COMMIT")
	if err := ValidatePhase3PublicationCheckout(root, payload, attestation); err != nil {
		t.Fatal(err)
	}
}

func phase3RepositoryRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func phase3PublicationFixture(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	for _, relative := range []string{"README.md", "Makefile", "LICENSE"} {
		input, err := os.ReadFile(filepath.Join(source, relative))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, relative)
		if err = os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(destination, input, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	phase3CopyFixtureTree(t, filepath.Join(source, "docs"), filepath.Join(root, "docs"))
	phase3CopyFixtureTree(t, filepath.Join(source, ".github"), filepath.Join(root, ".github"))
	phase3CopyFixtureTree(t, filepath.Join(source, "cmd"), filepath.Join(root, "cmd"))
	phase3CopyFixtureTree(t, filepath.Join(source, "scripts"), filepath.Join(root, "scripts"))
	return root
}

func phase3CopyFixtureTree(t *testing.T, source, destination string) {
	t.Helper()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		from := filepath.Join(source, entry.Name())
		to := filepath.Join(destination, entry.Name())
		if entry.IsDir() {
			phase3CopyFixtureTree(t, from, to)
			continue
		}
		contents, readErr := os.ReadFile(from)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(to, contents, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
}

func phase3UpgradeFixtureSchema(t *testing.T, root string) {
	t.Helper()
	phase3MutateQualification(t, root, func(record map[string]any) {
		record["schema_version"] = float64(2)
		scale := record["scale_proofs"].(map[string]any)
		races := scale["two_process_finalization"].(map[string]any)
		races["winners"], races["replays"], races["conflicts"] = float64(100), float64(50), float64(50)
		faultIDs := []string{"MM-ARTIFACT-APPROVAL-TX-ABORT", "MM-ARTIFACT-POSTCOMMIT-RESPONSE", "MM-DECOMPOSITION-APPLY-LOST-RESPONSE", "MM-FINALIZATION-PREWRITE-PROVIDER", "MM-FINALIZATION-TX-ABORT", "MM-FINALIZATION-POSTCOMMIT-RESPONSE", "MM-RECONCILIATION-DRIFT-TX-ABORT", "MM-RECONCILIATION-LATE-TX-ABORT", "MM-RECONCILIATION-POSTCOMMIT-RESPONSE"}
		faults := make([]any, len(faultIDs))
		for index, id := range faultIDs {
			faults[index] = map[string]any{"id": id, "expected": float64(1), "started": float64(1), "terminal": float64(1), "verdict": "pass"}
		}
		scale["crash_boundaries"].(map[string]any)["faults"] = faults
		projection := scale["projection_trials"].(map[string]any)
		projection["trial_kinds"] = map[string]any{"delete": float64(13), "truncate": float64(13), "edit": float64(13), "checkbox": float64(13), "stale": float64(12), "invalid-utf8": float64(12), "symlink": float64(12), "other-mission": float64(12)}
		projection["mutants"] = []any{map[string]any{"id": "projection_checkbox_authority", "verdict": "killed"}, map[string]any{"id": "projection_import_path", "verdict": "killed"}, map[string]any{"id": "governance_current_source", "verdict": "killed"}, map[string]any{"id": "governance_commitment_omission", "verdict": "killed"}}
	})
	phase3MutateCensus(t, root, func(record map[string]any) {
		record["schema_version"] = float64(2)
		for _, raw := range record["supporting_profiles"].([]any) {
			profile := raw.(map[string]any)
			registryName := profile["name"].(string)
			if registryName == "historical-full-scenario" {
				registryName = "full-scenario"
			}
			registered, err := ProfileByName(registryName)
			if err != nil {
				t.Fatal(err)
			}
			tests := make([]any, len(registered.ExpectedTests))
			for index, identity := range registered.ExpectedTests {
				tests[index] = identity
			}
			profile["tests"] = tests
		}
	})
}

func phase3MutateQualification(t *testing.T, root string, mutate func(map[string]any)) {
	t.Helper()
	phase3MutateJSON(t, filepath.Join(root, "docs/phase3-evidence/qualification-evidence.json"), mutate)
	phase3RewriteChecksums(t, root)
}

func phase3MutateCensus(t *testing.T, root string, mutate func(map[string]any)) {
	t.Helper()
	phase3MutateJSON(t, filepath.Join(root, "docs/phase3-evidence/campaign-census.json"), mutate)
	phase3RewriteChecksums(t, root)
}

func phase3MutateAttempt(t *testing.T, root, id, verdict string) {
	t.Helper()
	phase3MutateCensus(t, root, func(record map[string]any) {
		for _, raw := range record["failed_or_non_admissible_attempts"].([]any) {
			attempt := raw.(map[string]any)
			if attempt["id"] == id {
				attempt["verdict"] = verdict
				return
			}
		}
		t.Fatalf("attempt %s absent", id)
	})
}

func phase3MutateJSON(t *testing.T, path string, mutate func(map[string]any)) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(contents, &record); err != nil {
		t.Fatal(err)
	}
	mutate(record)
	contents, err = json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	contents = append(contents, '\n')
	if err = os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func phase3RewriteChecksums(t *testing.T, root string) {
	t.Helper()
	directory := filepath.Join(root, "docs/phase3-evidence")
	var lines []string
	for _, name := range []string{"qualification-evidence.json", "campaign-census.json", "results.md", "limitations.md"} {
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(contents)
		lines = append(lines, hex.EncodeToString(digest[:])+"  "+name)
	}
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func phase3Replace(t *testing.T, path, before, after string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), before) {
		t.Fatalf("replacement source absent: %s", before)
	}
	if err = os.WriteFile(path, []byte(strings.Replace(string(contents), before, after, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func phase3Git(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func phase3GitIn(t *testing.T, root string, args ...string) string {
	t.Helper()
	return phase3Git(t, append([]string{"-C", root}, args...)...)
}

func TestMissionMethodProfilesAreExactSeparatedAndDefensive(t *testing.T) {
	historical, err := ProfileByName("full-scenario")
	if err != nil || historical.Pattern == "" || strings.Contains(historical.Pattern, "TestMissionMethodSemanticDriftMutations1000") {
		t.Fatalf("historical full-scenario census was not frozen independently: profile=%+v err=%v", historical, err)
	}
	for _, name := range []string{MissionContractProfile, MissionAcceptanceProfile, MissionAdversarialProfile, MissionRealIdentityProfile} {
		profile, err := ProfileByName(name)
		if err != nil {
			t.Fatalf("profile %s: %v", name, err)
		}
		if profile.Pattern == "" || !strings.HasPrefix(profile.Pattern, "^(") || !strings.HasSuffix(profile.Pattern, ")$") {
			t.Fatalf("profile %s selector is not exact: %q", name, profile.Pattern)
		}
		profile.ExpectedTests[0] = "mutated"
		again, err := ProfileByName(name)
		if err != nil || again.ExpectedTests[0] == "mutated" {
			t.Fatalf("profile %s leaked caller mutation", name)
		}
	}
	fast, _ := ProfileByName(MissionAcceptanceProfile)
	slow, _ := ProfileByName(MissionAdversarialProfile)
	for _, expensive := range []string{
		"TestDishonestMissionAuthorityDetectsSameCountOutboxMutation",
		"TestMissionMethodBeadsDishonestProviderRefusesBeforePublication",
		"TestMissionMethodBeadsSlowObservationCannotPublishBeforeResponse",
		"TestMissionMethodCrashBoundaryCensus",
		"TestMissionMethodFinalizationProcessRaces100",
		"TestMissionMethodSemanticDriftMutations1000",
	} {
		if slices.Contains(fast.ExpectedTests, expensive) || !slices.Contains(slow.ExpectedTests, expensive) {
			t.Fatalf("expensive test %s crossed fast/slow boundary", expensive)
		}
	}
	for _, portable := range []string{"TestMissionMethodNormalizationAndPureQueries100", "TestMissionMethodProjectionAndGovernanceIndependence100", "TestMissionMethodProviderParityPortableCampaign"} {
		if !slices.Contains(fast.ExpectedTests, portable) || slices.Contains(slow.ExpectedTests, portable) {
			t.Fatalf("portable acceptance test %s crossed fast/slow boundary", portable)
		}
	}
	contract, _ := ProfileByName(MissionContractProfile)
	for _, identity := range []string{"TestHelperReplayPrecedesEligibilityAndStaleAuthorityPrecedesInvalidation", "TestMissionMethodQualificationContract", "TestQualityCampaignUniversalInvariants", "TestWP07AssignmentRecoveryCannotCrossLocalAuthorityBoundaries"} {
		if !slices.Contains(contract.ExpectedTests, identity) {
			t.Fatalf("mission contract profile omitted %s", identity)
		}
	}
	if slices.Contains(contract.ExpectedTests, "TestMissionMethodBeadsQualificationProfileIdentity") {
		t.Fatal("opt-in real-provider identity leaked into the portable contract profile")
	}
	realIdentity, err := ProfileByName(MissionRealIdentityProfile)
	if err != nil || !slices.Equal(realIdentity.ExpectedTests, []string{"TestMissionMethodBeadsQualificationProfileIdentity"}) {
		t.Fatalf("real-provider identity profile changed: profile=%+v err=%v", realIdentity, err)
	}
	full, err := CampaignByName(MissionFullCampaign)
	if err != nil || slices.Contains(full.Profiles, MissionRealIdentityProfile) || slices.Contains(full.RequiredTests, "TestMissionMethodBeadsQualificationProfileIdentity") {
		t.Fatalf("portable mission campaign acquired opt-in real-provider identity: campaign=%+v err=%v", full, err)
	}
}

func TestMissionMethodFullCampaignMutantsNameMissingControls(t *testing.T) {
	campaign, err := CampaignByName(MissionFullCampaign)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{MissionAcceptanceProfile, MissionAdversarialProfile, MissionContractProfile} {
		mutant := campaign
		mutant.Profiles = slices.DeleteFunc(append([]string(nil), campaign.Profiles...), func(value string) bool { return value == profile })
		if err := ValidateMissionCampaign(mutant); err == nil {
			t.Fatalf("missing profile mutant %s: %v", profile, err)
		}
	}
	for _, required := range campaign.RequiredTests {
		mutant := campaign
		mutant.RequiredTests = slices.DeleteFunc(append([]string(nil), campaign.RequiredTests...), func(value string) bool { return value == required })
		if err := ValidateMissionCampaign(mutant); err == nil {
			t.Fatalf("required-test removal mutant survived for %s", required)
		}
	}
}

func TestMissionMethodReportRejectsSkipMissingExtraDuplicateAndUnterminated(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	exact := Report{Verdict: "pass", ExpectedTests: []string{"TestAlpha"}, StartedTests: []string{"TestAlpha"}, TerminalTests: map[string]string{"TestAlpha": "pass"}, StartedAt: now, FinishedAt: now.Add(time.Second)}
	if err := ValidateMissionReport(exact); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Report){
		"skip":    func(r *Report) { r.TerminalTests["TestAlpha"] = "skip" },
		"missing": func(r *Report) { delete(r.TerminalTests, "TestAlpha") },
		"failed":  func(r *Report) { r.TerminalTests["TestAlpha"] = "fail" },
		"noncanonical": func(r *Report) {
			r.ExpectedTests = []string{"TestBeta", "TestAlpha"}
			r.TerminalTests["TestBeta"] = "pass"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := exact
			candidate.ExpectedTests = append([]string(nil), exact.ExpectedTests...)
			candidate.TerminalTests = map[string]string{"TestAlpha": "pass"}
			mutate(&candidate)
			if err := ValidateMissionReport(candidate); err == nil {
				t.Fatal("census mutant survived")
			}
		})
	}
}
