package m2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

func TestSourceBoundReceiptRequiresExactRunPassCensus(t *testing.T) {
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
	definition := Definition{ID: "behavior/example", Category: "behavior", OwnerWP: "WP17", Package: "./internal/example", Selector: "TestExact", ExpectedRepetitions: 2, TrialsPerRun: 1}
	raw := []byte(event("run", "TestExact") + event("pass", "TestExact") + event("run", "TestExact") + event("pass", "TestExact"))
	receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 2, 0)
	if err != nil || receipt.Verdict != "pass" || receipt.CertifiedRepetitions != 2 || receipt.RawTranscriptSHA256 != SHA256(raw) {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}

	mutants := map[string][]byte{
		"missing": raw[:len(raw)/2],
		"skipped": []byte(event("run", "TestExact") + event("skip", "TestExact")),
		"renamed": []byte(event("run", "TestOther") + event("pass", "TestOther")),
		"extra":   []byte(event("run", "TestExact") + event("pass", "TestExact") + event("run", "TestOther") + event("pass", "TestOther")),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyGoTestReceipt(mutant, "run", source, definition, 2, 0); err == nil {
				t.Fatal("mutated receipt passed")
			}
		})
	}
}

func TestGoTestReceiptBoundsAndFailedProcessDiagnostics(t *testing.T) {
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
	definition := Definition{ID: "behavior/example", Package: "./internal/example", Selector: "TestExact", ExpectedRepetitions: 1, TrialsPerRun: 1}
	limits := goTestReceiptLimits{recordBytes: 256, transcriptBytes: 4096}
	failure := []byte(event("run", "TestExact") + fmt.Sprintf("{\"Action\":\"output\",\"Package\":\"github.com/spec-kitty/go-kitty3/internal/example\",\"Test\":\"TestExact\",\"Output\":%q}\n", "workspace contribution refused: workspace authority refused: event\n") + event("fail", "TestExact"))
	if _, err := verifyGoTestReceiptWithLimits(failure, "run", source, definition, 1, 1, limits); err == nil || !strings.Contains(err.Error(), "TestExact") || !strings.Contains(err.Error(), SHA256(failure)) || strings.Contains(err.Error(), "workspace authority refused") || strings.Contains(err.Error(), "unterminated process transcript") {
		t.Fatalf("nonzero process lost bounded failure diagnostic: %v", err)
	}

	unterminated := []byte(strings.TrimSuffix(event("run", "TestExact"), "\n"))
	if _, err := verifyGoTestReceiptWithLimits(unterminated, "run", source, definition, 1, 0, limits); err == nil || !strings.Contains(err.Error(), "unterminated go test JSON record") {
		t.Fatalf("unterminated record was not distinguished: %v", err)
	}
	oversizeRecord := []byte(strings.Repeat("x", 257) + "\n")
	if _, err := verifyGoTestReceiptWithLimits(oversizeRecord, "run", source, definition, 1, 0, limits); err == nil || !strings.Contains(err.Error(), "record budget exceeded") {
		t.Fatalf("oversize record was not distinguished: %v", err)
	}
	complete := []byte(event("run", "TestExact") + event("pass", "TestExact"))
	tooSmall := goTestReceiptLimits{recordBytes: 256, transcriptBytes: len(complete) - 1}
	if _, err := verifyGoTestReceiptWithLimits(complete, "run", source, definition, 1, 0, tooSmall); err == nil || !strings.Contains(err.Error(), "total budget exceeded") {
		t.Fatalf("terminal event after total overflow was accepted: %v", err)
	}
}

func TestBoundedTranscriptWriterNeverSilentlyTruncates(t *testing.T) {
	writer := &boundedTranscriptWriter{limit: 4}
	if written, err := writer.Write([]byte("1234")); err != nil || written != 4 {
		t.Fatalf("bounded write=%d err=%v", written, err)
	}
	if written, err := writer.Write([]byte("5")); !errors.Is(err, errGoTestTranscriptTotalBudget) || written != 0 {
		t.Fatalf("overflow write=%d err=%v", written, err)
	}
	if got := string(writer.Bytes()); got != "1234" {
		t.Fatalf("overflow changed captured prefix: %q", got)
	}
	if !errors.Is(writer.Err(), errGoTestTranscriptTotalBudget) {
		t.Fatalf("overflow was not retained: %v", writer.Err())
	}
}

func TestGoTestLogicalOutputAssemblerReassemblesSplitMachineReceipts(t *testing.T) {
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
	definition := Definition{ID: "behavior/zombie-workspace-isolation", Package: "./internal/scenario", Selector: "TestZombieOwnerReplacementCampaign100", ExpectedRepetitions: 100, TrialsPerRun: 100}
	raw := append([]byte(nil), fixtureEvent(definition.Package, "run", definition.Selector)...)
	for index := range 100 {
		record, err := iterationreceipt.New(definition.ID, index, fmt.Sprintf("seed-%03d", index), "runtime-epoch-replacement", "stale-refused-authority-unchanged", time.Duration(index+1), map[string]int{"index": index}, map[string]bool{"universal": true})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := iterationreceipt.Encode(record)
		if err != nil {
			t.Fatal(err)
		}
		cut := len(encoded) / 2
		raw = append(raw, outputChunkEvent(definition.Package, definition.Selector, "    authority_laws_test.go:76: "+iterationreceipt.Prefix+string(encoded[:cut]))...)
		raw = append(raw, outputChunkEvent(definition.Package, definition.Selector, string(encoded[cut:])+"\n")...)
	}
	raw = append(raw, fixtureEvent(definition.Package, "pass", definition.Selector)...)
	receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0)
	if err != nil || receipt.CertifiedRepetitions != 100 {
		t.Fatalf("split logical receipts were not reassembled: receipt=%+v err=%v", receipt, err)
	}
}

func TestGoTestLogicalOutputAssemblerRejectsAmbiguousChunking(t *testing.T) {
	prefix := "    authority_laws_test.go:76: " + iterationreceipt.Prefix
	stream := goTestEvent{Action: "output", Package: "github.com/spec-kitty/go-kitty3/internal/scenario", Test: "TestExact"}
	for name, run := range map[string]func(*goTestOutputAssembler) error{
		"interleaved-test": func(assembler *goTestOutputAssembler) error {
			first := stream
			first.Output = prefix + `{\"schema_version\":1,`
			if _, _, err := assembler.consume(first); err != nil {
				return err
			}
			other := stream
			other.Test = "TestOther"
			other.Output = `\"index\":0}` + "\n"
			_, _, err := assembler.consume(other)
			return err
		},
		"cross-package": func(assembler *goTestOutputAssembler) error {
			first := stream
			first.Output = prefix + `{\"schema_version\":1,`
			if _, _, err := assembler.consume(first); err != nil {
				return err
			}
			other := stream
			other.Package += "/foreign"
			other.Output = `\"index\":0}` + "\n"
			_, _, err := assembler.consume(other)
			return err
		},
		"multiple-lines": func(assembler *goTestOutputAssembler) error {
			event := stream
			event.Output = prefix + `{}` + "\nforged\n"
			_, _, err := assembler.consume(event)
			return err
		},
		"forged-prefix-in-later-fragment": func(assembler *goTestOutputAssembler) error {
			first := stream
			first.Output = "ordinary partial output"
			if _, _, err := assembler.consume(first); err != nil {
				return err
			}
			second := stream
			second.Output = iterationreceipt.Prefix + `{}` + "\n"
			_, _, err := assembler.consume(second)
			return err
		},
		"assembled-over-budget": func(assembler *goTestOutputAssembler) error {
			first := stream
			first.Output = prefix + "1234"
			if _, _, err := assembler.consume(first); err != nil {
				return err
			}
			second := stream
			second.Output = "56789\n"
			_, _, err := assembler.consume(second)
			return err
		},
		"unterminated": func(assembler *goTestOutputAssembler) error {
			first := stream
			first.Output = prefix + `{\"schema_version\":1}`
			if _, _, err := assembler.consume(first); err != nil {
				return err
			}
			return assembler.finish()
		},
	} {
		t.Run(name, func(t *testing.T) {
			limit := 8 << 10
			if name == "assembled-over-budget" {
				limit = 8
			}
			if err := run(&goTestOutputAssembler{limit: limit}); err == nil {
				t.Fatal("ambiguous machine output chunking was accepted")
			}
		})
	}
}

func outputChunkEvent(packagePath, test, output string) []byte {
	encoded, _ := json.Marshal(goTestEvent{Action: "output", Package: strings.TrimPrefix(packagePath, "./"), Test: test, Output: output})
	return append(encoded, '\n')
}

func TestExecutingIterationReceiptsRejectMissingDuplicateAndForgedFacts(t *testing.T) {
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
	definition := Definition{ID: "fault/concurrent-claims", Package: "./internal/scenario", Selector: "TestConcurrentSameAggregateConflictCampaign100", ExpectedRepetitions: 100, TrialsPerRun: 100}
	raw := []byte(fixtureEvent(definition.Package, "run", definition.Selector))
	var encodedRecords [][]byte
	for index := range 100 {
		record, err := iterationreceipt.New(definition.ID, index, fmt.Sprintf("mission-%03d", index), "mission-head-cas", "one-winner-one-version-conflict", time.Duration(index+1), map[string]int{"winner": 1, "loser": 1}, map[string]bool{"universal": true})
		if err != nil {
			t.Fatal(err)
		}
		encodedRecords = append(encodedRecords, fixtureIterationEvent(definition.Package, definition.Selector, record))
		raw = append(raw, encodedRecords[index]...)
	}
	raw = append(raw, fixtureEvent(definition.Package, "pass", definition.Selector)...)
	if receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0); err != nil || receipt.CertifiedRepetitions != 100 || receipt.CertificationMode != "executing-iteration-receipts-v1" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	mutants := map[string][]byte{
		"missing":        bytes.Replace(raw, encodedRecords[37], nil, 1),
		"duplicate":      append(append([]byte(nil), raw[:len(raw)-len(fixtureEvent(definition.Package, "pass", definition.Selector))]...), append(encodedRecords[37], fixtureEvent(definition.Package, "pass", definition.Selector)...)...),
		"foreign-parent": bytes.Replace(raw, []byte(`"Test":"TestConcurrentSameAggregateConflictCampaign100","Output"`), []byte(`"Test":"TestForeign","Output"`), 1),
	}
	for name, mutant := range mutants {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyGoTestReceipt(mutant, "run", source, definition, 1, 0); err == nil {
				t.Fatal("iteration receipt mutant survived")
			}
		})
	}
	forged := append([]byte(nil), raw...)
	forged = bytes.Replace(forged, []byte(`\"winner\":1`), []byte(`\"winner\":2`), 1)
	if _, err := VerifyGoTestReceipt(forged, "run", source, definition, 1, 0); err == nil {
		t.Fatal("observed-facts digest substitution survived")
	}
}

func TestExecutingIterationReceiptParsesRealCampaignTranscript(t *testing.T) {
	for _, definitionID := range []string{"behavior/serialized-integration", "behavior/zombie-workspace-isolation"} {
		t.Run(safeID(definitionID), func(t *testing.T) {
			definition := FrozenDefinitions()[0]
			for _, candidate := range FrozenDefinitions() {
				if candidate.ID == definitionID {
					definition = candidate
					break
				}
			}
			command := exec.Command("go", "test", "-json", "-count=1", "-run", "^"+definition.Selector+"$", definition.Package)
			command.Dir = repositoryRoot(t)
			command.Env = append(os.Environ(), "SPEC_KITTY_WP14_CAMPAIGN=100")
			raw, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("execute real campaign: %v\n%s", err, raw)
			}
			source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
			receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0)
			if err != nil || receipt.CertifiedRepetitions != 100 || len(receipt.Iterations) != 100 {
				t.Fatalf("receipt=%+v err=%v", receipt, err)
			}
		})
	}
}

func TestFrozenSelectorsResolveToOneExactTopLevelTest(t *testing.T) {
	root := repositoryRoot(t)
	byPackage := map[string][]string{}
	for _, definition := range FrozenDefinitions() {
		byPackage[definition.Package] = append(byPackage[definition.Package], definition.Selector)
	}
	for pkg, selectors := range byPackage {
		command := exec.Command("go", "test", "-list", "^Test", pkg)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("list %s: %v\n%s", pkg, err, output)
		}
		seen := map[string]int{}
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "Test") {
				seen[line]++
			}
		}
		for _, selector := range selectors {
			if seen[selector] != 1 {
				t.Errorf("package=%s selector=%s count=%d want=1", pkg, selector, seen[selector])
			}
		}
	}
}

func TestSourceBoundReceiptAccountsForAllChildTerminals(t *testing.T) {
	source := Source{Commit: stringOf('a', 40), Tree: stringOf('b', 40)}
	definition := Definition{ID: "mutant/example", Package: "./internal/example", Selector: "TestExact", ExpectedRepetitions: 1, TrialsPerRun: 1}
	raw := []byte(event("run", "TestExact") + event("run", "TestExact/mutant") + event("pass", "TestExact/mutant") + event("pass", "TestExact"))
	receipt, err := VerifyGoTestReceipt(raw, "run", source, definition, 1, 0)
	if err != nil || receipt.ChildStarted != 1 || receipt.ChildPassed != 1 || receipt.CertificationMode != "direct-process-repetitions" || receipt.CertifiedRepetitions != 1 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	missingChild := []byte(event("run", "TestExact") + event("run", "TestExact/mutant") + event("pass", "TestExact"))
	if _, err := VerifyGoTestReceipt(missingChild, "run", source, definition, 1, 0); err == nil {
		t.Fatal("missing child terminal accepted")
	}
	foreignChild := []byte(event("run", "TestExact") + event("run", "TestOther/mutant") + event("pass", "TestOther/mutant") + event("pass", "TestExact"))
	if _, err := VerifyGoTestReceipt(foreignChild, "run", source, definition, 1, 0); err == nil {
		t.Fatal("foreign child events accepted")
	}
	if _, err := VerifyGoTestReceipt(raw, "run", source, Definition{ID: definition.ID, Package: definition.Package, Selector: definition.Selector, ExpectedRepetitions: 100, TrialsPerRun: 1}, 99, 0); err == nil {
		t.Fatal("99 executions certified as 100")
	}
}

func event(action, test string) string {
	return fmt.Sprintf("{\"Action\":%q,\"Package\":\"github.com/spec-kitty/go-kitty3/internal/example\",\"Test\":%q}\n", action, test)
}
