package scenario

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type sourceViolation struct {
	file, rule string
}

// retiredLegacyScenarioBlobs is the exact 22-file deletion census from the
// storage-coupled harness retirement commit. The portable campaign deliberately
// reused campaign_test.go later, so that name is allowed only when its content
// is not the retired blob. Every other carrier name remains permanently absent.
var retiredLegacyScenarioBlobs = map[string]string{
	"beads_fault_helpers_test.go":        "2294094f4b1b09792e4f7007f2902c554dfaa81a755fa8ac1bc9def8259b480b",
	"beads_fault_test.go":                "cc453f22e0d4d65f12ea0f4e2290058270f64deefff8dc1ebb3f4844dc09669c",
	"campaign_test.go":                   "c9d7cb4c2957fb4681b236872d0b1b6895451759311c592804c6853046c39be4",
	"claim_race_test.go":                 "d481a2a6d1f5ac98f87eaa70dbdce68f2bf844ccc58f5914c258087697e8287a",
	"integration_test.go":                "010b8aad2e2c51b4665fe36e16d60ef3f714aa34dd3915f77b63bdd9abb70264",
	"mission_method_adversarial_test.go": "0f14b64e053af0ac915e2e1e1f1c191a79dc588f5440d5c3410706b659cd0787",
	"mission_method_beads_test.go":       "5549e260f6cc714db99ee549ae7becd69a2ae86ce52a594422c12ed110a4c382",
	"mission_method_fake_test.go":        "b65cc9e69a00645249b3da83f8344c321d71c140bfedda5efd26af74a427094a",
	"mission_method_fault_test.go":       "e57fc6690a78eaa5c1e5e969d31cad37c4c7a253aaf19807dfd76c7d832def9e",
	"mission_method_helpers_test.go":     "1cf0b5b73bfb0c136eb26b8841187777bf92860dbceccc7710f68b6a3bbf7902",
	"mission_method_projection_test.go":  "40d3ac931998b7bf749a037e633e394601dc808916dcbd8e2bca7e8b29b47bbf",
	"mission_method_race_test.go":        "81676d58fa10e5aea0f995ad498afdc6b0991575d24cf2b0e8a53422f4fa4e3a",
	"phase1_test.go":                     "2db80cab425e4d6d19713b0730207d0e5663237e887cb15392539087ad9c08d6",
	"reconciliation_test.go":             "0279a871b11afdc77a39b476c09bcccc125319a0318b36e2c0661dc53cf10229",
	"repetition_test.go":                 "b102b6579eb0ccda85825a9ef0b0f842b9ad6c57090804271d09c0ed19a8ccce",
	"review_test.go":                     "4cbc8a63dd252ef67d09eb13328748a292ea7f6c892dfd178b7ebe8186e88974",
	"start_run_recovery_test.go":         "5304a78f49ce5f6f3b85fca2490cea08f8479fa470688e52d278ad729e07fff2",
	"tracker_saga_test.go":               "aecde3047bb3d2063835d9d367ca08b7f9d63d6543e69694601dd8294e6b5a80",
	"workspace_concurrency_test.go":      "2203bbd6af881d1b2c970fa1c9e38b8243d4a0c1ff16671264a2b59342a6e4f2",
	"workspace_inspection_test.go":       "afe69eb1f7c1e6de50d0f5abe044a9f4d8244f075629f7c572bccd217cb0a66a",
	"wp07_assignment_authority_test.go":  "7fd26e32e0b68795387f6dca80aaca858e85e50764e9d0141ad7231aeb150685",
	"zombie_test.go":                     "ffb03aa0ee7f1a8e655df2af142d698055a10a6f0cecdb848507157bdd69718b",
}

func retiredScenarioFileViolations(fileDigests map[string]string) []string {
	var violations []string
	for name, retiredDigest := range retiredLegacyScenarioBlobs {
		currentDigest, exists := fileDigests[name]
		if !exists {
			continue
		}
		if name != "campaign_test.go" || currentDigest == retiredDigest {
			violations = append(violations, name)
		}
	}
	sort.Strings(violations)
	return violations
}

func portableSourceViolations(files map[string]string) []sourceViolation {
	forbidden := map[string]string{
		"relational_driver":    "database/" + "sql",
		"legacy_adapter":       "controlstore/" + "sqlite",
		"legacy_module":        "modernc.org/" + "sqlite",
		"raw_store":            "\"github.com/spec-kitty/go-kitty3/internal/controlstore/" + "gapdb\"",
		"helper_shortcut":      "internal/" + "helpercmd",
		"path_argument":        "--" + "db",
		"legacy_eligibility":   "legacy-" + "v1",
		"schema_catalog":       "sqlite_" + "master",
		"lease_relation":       "FROM " + "leases",
		"workspace_relation":   "FROM " + "workspaces",
		"integration_relation": "FROM " + "integrations",
	}
	var violations []sourceViolation
	for name, source := range files {
		lower := strings.ToLower(source)
		for rule, fragment := range forbidden {
			if strings.Contains(lower, strings.ToLower(fragment)) {
				violations = append(violations, sourceViolation{file: name, rule: rule})
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file == violations[j].file {
			return violations[i].rule < violations[j].rule
		}
		return violations[i].file < violations[j].file
	})
	return violations
}

func TestHarnessArchitectureIsPortable(t *testing.T) {
	directory := filepath.Join(repositoryRoot(t), "internal", "scenario")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(directory, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		files[entry.Name()] = string(raw)
	}
	if got := len(retiredLegacyScenarioBlobs); got != 22 {
		t.Fatalf("retired legacy scenario census size=%d want=22", got)
	}
	fileDigests := make(map[string]string, len(files))
	for name, source := range files {
		fileDigests[name] = fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	}
	if violations := retiredScenarioFileViolations(fileDigests); len(violations) != 0 {
		t.Fatalf("retired legacy scenario files returned: %v", violations)
	}
	if violations := portableSourceViolations(files); len(violations) != 0 {
		t.Fatalf("portable scenario boundary violations: %+v", violations)
	}
}
func TestHarnessArchitectureGuardKillsStorageMutants(t *testing.T) {
	mutants := map[string]string{
		"driver":   "import _ \"modernc.org/" + "sqlite\"",
		"query":    "rows, err := q.Query(\"SELECT * FROM " + "lea" + "ses\")",
		"raw":      "import \"github.com/spec-kitty/go-kitty3/internal/controlstore/" + "gapdb\"",
		"shortcut": "exec.Command(helper, \"inspect\", \"--" + "db\", path)",
	}
	for name, source := range mutants {
		t.Run(name, func(t *testing.T) {
			if got := portableSourceViolations(map[string]string{"mutant.go": source}); len(got) == 0 {
				t.Fatal(fmt.Errorf("architecture mutant survived: %s", name))
			}
		})
	}
}

func TestHarnessArchitectureGuardKillsRetiredFileMutant(t *testing.T) {
	if got := retiredScenarioFileViolations(map[string]string{"phase1_test.go": "rewritten-but-still-retired"}); len(got) != 1 || got[0] != "phase1_test.go" {
		t.Fatalf("retired-file mutant survived: %v", got)
	}
	legacyCampaign := retiredLegacyScenarioBlobs["campaign_test.go"]
	if got := retiredScenarioFileViolations(map[string]string{"campaign_test.go": legacyCampaign}); len(got) != 1 || got[0] != "campaign_test.go" {
		t.Fatalf("retired campaign blob mutant survived: %v", got)
	}
	if got := retiredScenarioFileViolations(map[string]string{"campaign_test.go": "portable-replacement"}); len(got) != 0 {
		t.Fatalf("portable campaign falsely rejected: %v", got)
	}
}
