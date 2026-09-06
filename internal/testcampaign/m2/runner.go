package m2

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type CampaignConfig struct {
	RunID       string
	Repository  string
	SidecarPath string
	Source      Source
	Timeout     time.Duration
	Short       bool
	Attempt     *Attempt
}

type CampaignResult struct {
	Census Census
	Inputs []ArtifactInput
}

func RunCampaigns(ctx context.Context, config CampaignConfig) (CampaignResult, error) {
	result := CampaignResult{Census: Census{SchemaVersion: 1, RunID: config.RunID, SourceCommit: config.Source.Commit, SourceTree: config.Source.Tree}}
	if config.RunID == "" || !filepath.IsAbs(config.Repository) || !filepath.IsAbs(config.SidecarPath) || config.Timeout <= 0 {
		return result, fmt.Errorf("campaign configuration is incomplete")
	}
	if digest, err := fileDigest(config.SidecarPath); err != nil || digest != AdmittedSidecarSHA256 {
		return result, fmt.Errorf("sidecar differs from admitted assertion extension")
	}
	definitions := FrozenDefinitions()
	if config.Short {
		definitions = shortDefinitions(definitions)
	}
	for _, definition := range definitions {
		executions := definition.ExpectedRepetitions / definition.TrialsPerRun
		commandCtx, cancel := context.WithTimeout(ctx, config.Timeout)
		pattern := "^" + regexp.QuoteMeta(definition.Selector) + "$"
		command := exec.CommandContext(commandCtx, "go", "test", "-json", "-count="+fmt.Sprint(executions), "-run", pattern, definition.Package)
		command.Dir = config.Repository
		command.Env = append(os.Environ(),
			"SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="+config.SidecarPath,
			"SPEC_KITTY_WP13_GAPDBD="+config.SidecarPath,
			"SPEC_KITTY_ASSERTION_CANDIDATE_GAPDBD="+config.SidecarPath,
			"SPEC_KITTY_WP14_CAMPAIGN=100",
			"SPEC_KITTY_WP14_SOURCE_REVISION="+config.Source.Commit,
		)
		raw, commandErr := command.CombinedOutput()
		cancel()
		exit := 0
		if commandErr != nil {
			exit = 1
			if commandCtx.Err() != nil {
				exit = 124
			}
		}
		receipt, receiptErr := VerifyGoTestReceipt(raw, config.RunID, config.Source, definition, executions, exit)
		rawPath := "campaigns/" + safeID(definition.ID) + ".go-test.jsonl"
		receiptPath := "campaigns/" + safeID(definition.ID) + ".receipt.json"
		receiptBytes, marshalErr := CanonicalJSON(receipt)
		if marshalErr != nil {
			return result, marshalErr
		}
		result.Inputs = append(result.Inputs, ArtifactInput{Path: rawPath, Kind: "campaign-raw", Data: raw}, ArtifactInput{Path: receiptPath, Kind: "campaign", Data: receiptBytes})
		if config.Attempt != nil {
			if err := config.Attempt.Capture(rawPath, raw); err != nil {
				return result, err
			}
			if err := config.Attempt.Capture(receiptPath, receiptBytes); err != nil {
				return result, err
			}
			if err := config.Attempt.Record(safeID(definition.ID), receipt.Verdict, ""); err != nil {
				return result, err
			}
		}
		result.Census.Entries = append(result.Census.Entries, CensusEntry{ID: definition.ID, Category: definition.Category, OwnerWP: definition.OwnerWP, Package: definition.Package, Selector: definition.Selector, ExpectedRepetitions: definition.ExpectedRepetitions, TrialsPerRun: definition.TrialsPerRun, ObservedCount: receipt.CertifiedRepetitions, PassedCount: receipt.CertifiedRepetitions, FailedCount: receipt.FailedExecutions, SkippedCount: receipt.SkippedExecutions, Substituted: false, ArtifactPath: receiptPath, ArtifactSHA256: SHA256(receiptBytes)})
		if receiptErr != nil {
			return result, fmt.Errorf("campaign %s: %w", definition.ID, receiptErr)
		}
		if definition.ID == "fault/adversarial-tracker" {
			contractSource, err := os.ReadFile(filepath.Join(config.Repository, "internal/scenario/campaign_test.go"))
			if err != nil {
				return result, err
			}
			structured, err := VerifyWP14CampaignReceiptBytes(contractSource, raw)
			if err != nil {
				return result, err
			}
			structuredBytes, err := CanonicalJSON(structured)
			if err != nil {
				return result, err
			}
			path := "campaigns/wp14-structured-census.receipt.json"
			sourcePath := "contracts/wp14-campaign-source.go"
			result.Inputs = append(result.Inputs, ArtifactInput{Path: path, Kind: "campaign-structure", Data: structuredBytes}, ArtifactInput{Path: sourcePath, Kind: "campaign-contract-source", Data: contractSource})
			if config.Attempt != nil {
				if err := config.Attempt.Capture(path, structuredBytes); err != nil {
					return result, err
				}
				if err := config.Attempt.Capture(sourcePath, contractSource); err != nil {
					return result, err
				}
			}
		}
	}
	if observed, err := ObserveCleanSource(config.Repository); err != nil || observed != config.Source {
		return result, fmt.Errorf("source changed during campaigns: %w", err)
	}
	return result, nil
}

func shortDefinitions(definitions []Definition) []Definition {
	allowed := map[string]bool{
		"behavior/autonomous-capability-readiness": true,
		"behavior/two-item-restart-every-boundary": true,
		"behavior/restore-deadline-structural":     true,
		"behavior/sqlite-absence":                  true,
		"mutant/restore-deadline-overlay":          true,
		"mutant/autonomy-ceremony":                 true,
		"mutant/sqlite-absence-layers":             true,
	}
	result := make([]Definition, 0, len(allowed))
	for _, definition := range definitions {
		if allowed[definition.ID] {
			result = append(result, definition)
		}
	}
	return result
}

func safeID(id string) string {
	return strings.NewReplacer("/", "-", "_", "-").Replace(id)
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return SHA256(data), nil
}
