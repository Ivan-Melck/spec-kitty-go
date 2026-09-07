package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	m2 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2"
	m2external "github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/external"
)

const maximumDiagnostic = 2048

func main() {
	if err := run(os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{"schema_version": 1, "outcome": "fail", "diagnostic": bounded(err.Error())})
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("command required: preflight, run, verify, diagnose, finalize-review, or verify-release")
	}
	switch arguments[0] {
	case "short":
		set := flag.NewFlagSet("short", flag.ContinueOnError)
		repository := set.String("repository", "", "absolute repository root")
		group := set.String("group", "core", "closed gate group: core, mutants, capabilities-full, or mutants-full")
		timeout := set.Duration("timeout", 8*time.Minute, "per-package bound")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		if !filepath.IsAbs(*repository) {
			return fmt.Errorf("absolute repository path is required")
		}
		report, err := m2.RunShortGate(context.Background(), *repository, *group, *timeout)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(report); encodeErr != nil && err == nil {
			err = encodeErr
		}
		return err
	case "sbom":
		set := flag.NewFlagSet("sbom", flag.ContinueOnError)
		repository := set.String("repository", "", "absolute repository root")
		output := set.String("output", "", "absolute output path")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		if !filepath.IsAbs(*repository) || !filepath.IsAbs(*output) {
			return fmt.Errorf("absolute repository and output paths are required")
		}
		data, err := m2.GenerateSBOM(*repository)
		if err != nil {
			return err
		}
		return atomicWrite(*output, data)
	case "measure":
		set := flag.NewFlagSet("measure", flag.ContinueOnError)
		repository := set.String("repository", "", "absolute repository root")
		output := set.String("output", "", "absolute output path")
		sidecar := set.String("sidecar", "", "absolute admitted sidecar")
		product := set.String("product-binary", "", "absolute production binary")
		runID := set.String("run-id", "", "unique RunID")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		if !filepath.IsAbs(*repository) || !filepath.IsAbs(*output) || !filepath.IsAbs(*sidecar) || !filepath.IsAbs(*product) || *runID == "" {
			return fmt.Errorf("absolute paths and RunID are required")
		}
		source, err := m2.ObserveCleanSource(*repository)
		if err != nil {
			return err
		}
		privateRoot, err := os.MkdirTemp("", "spk-m2-measure-")
		if err != nil {
			return err
		}
		removePrivateRoot := true
		defer func() {
			if removePrivateRoot {
				_ = os.RemoveAll(privateRoot)
			}
		}()
		qualifier, err := os.Executable()
		if err != nil {
			return err
		}
		raw, transcript, err := m2external.MeasurePerformance(context.Background(), m2external.PerformanceConfig{RunID: *runID, Repository: *repository, Executable: *sidecar, QualifierExecutable: qualifier, ProductExecutable: *product, Reference: filepath.Join(*repository, "docs/programs/evidence/m2-performance-reference.json"), RuntimeRoot: filepath.Join(privateRoot, "runtime"), DataRoot: filepath.Join(privateRoot, "data"), Source: source, Warmup: 100, Observations: 10_000, RecoveryRuns: 30, StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second, TestTimeout: 60 * time.Minute})
		transcriptPath := *output + ".events.jsonl"
		if transcriptErr := atomicWriteCreateOnce(transcriptPath, transcript); transcriptErr != nil {
			return errors.Join(err, fmt.Errorf("retain performance transcript: %w", transcriptErr))
		}
		if err != nil {
			observationPath := ""
			if raw.SchemaVersion != 0 {
				data, encodeErr := m2.CanonicalJSON(raw)
				if encodeErr != nil {
					return errors.Join(err, fmt.Errorf("encode failed performance observation: %w", encodeErr))
				}
				observationPath = *output + ".failed.json"
				if writeErr := atomicWriteCreateOnce(observationPath, data); writeErr != nil {
					return errors.Join(err, fmt.Errorf("retain failed performance observation: %w", writeErr))
				}
				removePrivateRoot = false
			}
			return fmt.Errorf("%w (transcript=%s failed_observation=%s retained_private_root=%s)", err, transcriptPath, observationPath, privateRoot)
		}
		if _, err := m2.SummarizePerformance(raw); err != nil {
			return err
		}
		data, err := m2.CanonicalJSON(raw)
		if err != nil {
			return err
		}
		return atomicWrite(*output, data)
	case "preflight":
		config, _, err := flags(arguments[1:])
		if err != nil {
			return err
		}
		result, err := m2.Preflight(config)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil && err == nil {
			err = encodeErr
		}
		return err
	case "run":
		config, timeout, err := flags(arguments[1:])
		if err != nil {
			return err
		}
		result, err := execute(context.Background(), config, timeout)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil && err == nil {
			err = encodeErr
		}
		return err
	case "verify":
		set := flag.NewFlagSet("verify", flag.ContinueOnError)
		evidence := set.String("evidence", "", "absolute immutable run directory")
		archive := set.String("archive", "", "absolute producer archive")
		receipt := set.String("archive-receipt", "", "absolute producer archive receipt")
		expectedCommit := set.String("expected-commit", "", "reviewer-required source commit")
		expectedTree := set.String("expected-tree", "", "reviewer-required source tree")
		expectedManifest := set.String("expected-manifest-sha256", "", "reviewer-required manifest digest")
		expectedArchive := set.String("expected-archive-sha256", "", "reviewer-required producer archive digest")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		if !filepath.IsAbs(*evidence) || !filepath.IsAbs(*archive) || !filepath.IsAbs(*receipt) || *expectedCommit == "" || *expectedTree == "" || *expectedManifest == "" || *expectedArchive == "" {
			return fmt.Errorf("absolute evidence/archive paths and all expected identities are required")
		}
		source, err := sourceFromManifest(*evidence, *expectedCommit, *expectedTree)
		if err != nil {
			return err
		}
		verification, _, err := m2.VerifyProducerArchive(*evidence, *archive, *receipt, source, *expectedManifest, *expectedArchive)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(verification); encodeErr != nil && err == nil {
			err = encodeErr
		}
		return err
	case "diagnose":
		set := flag.NewFlagSet("diagnose", flag.ContinueOnError)
		evidence := set.String("evidence", "", "absolute immutable run directory")
		archive := set.String("archive", "", "absolute producer archive")
		receipt := set.String("archive-receipt", "", "absolute producer archive receipt")
		expectedCommit := set.String("expected-commit", "", "reviewer-required source commit")
		expectedTree := set.String("expected-tree", "", "reviewer-required source tree")
		expectedManifest := set.String("expected-manifest-sha256", "", "reviewer-required manifest digest")
		expectedArchive := set.String("expected-archive-sha256", "", "reviewer-required archive digest")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		if !filepath.IsAbs(*evidence) || !filepath.IsAbs(*archive) || !filepath.IsAbs(*receipt) {
			return fmt.Errorf("absolute evidence and producer bundle paths are required")
		}
		source, err := sourceFromManifest(*evidence, *expectedCommit, *expectedTree)
		if err != nil {
			return err
		}
		verification, _, err := m2.VerifyProducerArchive(*evidence, *archive, *receipt, source, *expectedManifest, *expectedArchive)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(verification)
	case "finalize-review":
		set := flag.NewFlagSet("finalize-review", flag.ContinueOnError)
		evidence := set.String("evidence", "", "absolute immutable producer evidence")
		archive := set.String("archive", "", "absolute producer archive")
		receipt := set.String("archive-receipt", "", "absolute producer archive receipt")
		reviewBundle := set.String("review-bundle", "", "absolute independent review bundle")
		output := set.String("output", "", "absolute release output root")
		expectedCommit := set.String("expected-commit", "", "reviewer-required source commit")
		expectedTree := set.String("expected-tree", "", "reviewer-required source tree")
		expectedManifest := set.String("expected-manifest-sha256", "", "reviewer-required manifest digest")
		expectedArchive := set.String("expected-archive-sha256", "", "reviewer-required archive digest")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		for _, path := range []string{*evidence, *archive, *receipt, *reviewBundle, *output} {
			if !filepath.IsAbs(path) {
				return fmt.Errorf("all finalize-review paths must be absolute")
			}
		}
		source, err := sourceFromManifest(*evidence, *expectedCommit, *expectedTree)
		if err != nil {
			return err
		}
		release, err := m2.FinalizeIndependentReview(*output, *evidence, *archive, *receipt, *reviewBundle, source, *expectedManifest, *expectedArchive)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(release); encodeErr != nil && err == nil {
			err = encodeErr
		}
		return err
	case "verify-release":
		set := flag.NewFlagSet("verify-release", flag.ContinueOnError)
		archive := set.String("archive", "", "absolute release archive")
		receipt := set.String("receipt", "", "absolute release receipt")
		expectedCommit := set.String("expected-commit", "", "externally trusted source commit")
		expectedTree := set.String("expected-tree", "", "externally trusted source tree")
		expectedManifest := set.String("expected-manifest-sha256", "", "externally trusted producer manifest digest")
		expectedProducer := set.String("expected-producer-sha256", "", "externally trusted producer archive digest")
		expectedRelease := set.String("expected-release-sha256", "", "externally trusted release archive digest")
		if err := set.Parse(arguments[1:]); err != nil {
			return err
		}
		if !filepath.IsAbs(*archive) || !filepath.IsAbs(*receipt) || *expectedCommit == "" || *expectedTree == "" || *expectedManifest == "" || *expectedProducer == "" || *expectedRelease == "" {
			return fmt.Errorf("absolute release paths and all external expected identities are required")
		}
		release, err := m2.VerifyReleaseArchive(*archive, *receipt, m2.Source{Commit: *expectedCommit, Tree: *expectedTree}, *expectedManifest, *expectedProducer, *expectedRelease)
		if encodeErr := json.NewEncoder(os.Stdout).Encode(release); encodeErr != nil && err == nil {
			err = encodeErr
		}
		return err
	default:
		return fmt.Errorf("unknown command %q", arguments[0])
	}
}

func sourceFromManifest(evidence, commit, tree string) (m2.Source, error) {
	data, err := os.ReadFile(filepath.Join(evidence, m2.ManifestName))
	if err != nil {
		return m2.Source{}, err
	}
	var manifest m2.Manifest
	if err := m2.DecodeStrict(data, &manifest); err != nil {
		return m2.Source{}, err
	}
	if commit == "" || tree == "" {
		return m2.Source{}, fmt.Errorf("expected source commit and tree are required")
	}
	manifest.Source.Commit = commit
	manifest.Source.Tree = tree
	return manifest.Source, nil
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".spk-m2-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func atomicWriteCreateOnce(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".spk-m2-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(name, path); err != nil {
		return err
	}
	return nil
}

func flags(arguments []string) (m2.PreflightConfig, time.Duration, error) {
	set := flag.NewFlagSet("qualification", flag.ContinueOnError)
	repository := set.String("repository", "", "absolute repository root")
	output := set.String("output", "", "absolute immutable evidence root")
	sidecar := set.String("sidecar", "", "absolute admitted gapdbd")
	wp16Sidecar := set.String("wp16-sidecar", "", "absolute frozen WP16 assertion-extension gapdbd")
	product := set.String("product-binary", "", "absolute production binary")
	runID := set.String("run-id", "", "unique qualification RunID")
	actorID := set.String("actor-id", "", "implementation ActorID")
	processID := set.String("process-id", "", "implementation ProcessID")
	profileRevision := set.String("profile-revision", "", "resolved implementation profile revision")
	profileDocument := set.String("profile-document", "", "absolute immutable resolved profile JSON")
	contributors := set.String("implementation-contributors", "", "comma-separated complete implementation ActorID census")
	timeout := set.Duration("timeout", 45*time.Minute, "per-selector timeout")
	if err := set.Parse(arguments); err != nil {
		return m2.PreflightConfig{}, 0, err
	}
	contributorIDs := strings.Split(*contributors, ",")
	for index := range contributorIDs {
		contributorIDs[index] = strings.TrimSpace(contributorIDs[index])
	}
	slices.Sort(contributorIDs)
	return m2.PreflightConfig{RunID: *runID, Repository: *repository, OutputRoot: *output, SidecarPath: *sidecar, WP16SidecarPath: *wp16Sidecar, ProductBinary: *product, ProfileDocument: *profileDocument, Producer: m2.ProducerIdentity{SchemaVersion: 1, ExecutorActorID: *actorID, ProcessID: *processID, ImplementationContributors: contributorIDs, ProfileID: "implementer-ivan", ProfileRevision: *profileRevision}}, *timeout, nil
}

func execute(ctx context.Context, config m2.PreflightConfig, timeout time.Duration) (publication m2.Publication, resultErr error) {
	profileBytes, err := os.ReadFile(config.ProfileDocument)
	if err != nil {
		return m2.Publication{}, err
	}
	if m2.SHA256(profileBytes) != config.Producer.ProfileRevision {
		return m2.Publication{}, fmt.Errorf("profile revision differs from exact raw profile-show bytes")
	}
	preflight, err := m2.Preflight(config)
	if err != nil {
		return m2.Publication{}, err
	}
	attempt, err := m2.ReserveAttempt(config.OutputRoot, config.RunID, preflight.Source)
	if err != nil {
		return m2.Publication{}, err
	}
	defer func() {
		if resultErr != nil {
			verdict := "fail"
			if ctx.Err() != nil {
				verdict = "incomplete"
			}
			if sealErr := attempt.SealNonPass(verdict, resultErr); sealErr != nil {
				resultErr = fmt.Errorf("%w; preserve failed attempt: %v", resultErr, sealErr)
			}
		}
	}()
	if err := attempt.Record("campaigns", "running", ""); err != nil {
		return publication, err
	}
	campaign, err := m2.RunCampaigns(ctx, m2.CampaignConfig{RunID: config.RunID, Repository: config.Repository, SidecarPath: config.SidecarPath, Source: preflight.Source, Timeout: timeout, Attempt: attempt})
	if err != nil {
		return publication, err
	}
	if err := attempt.Record("campaigns", "pass", ""); err != nil {
		return publication, err
	}
	if err := attempt.Record("absence", "running", ""); err != nil {
		return publication, err
	}
	absenceInputs, err := m2.RunAbsenceQualification(ctx, config.Repository, config.WP16SidecarPath, attempt)
	if err != nil {
		return publication, err
	}
	if err := attempt.Record("absence", "pass", ""); err != nil {
		return publication, err
	}
	if err := attempt.Record("race", "running", ""); err != nil {
		return publication, err
	}
	raceInputs, err := m2.RunRaceQualification(ctx, config.Repository, config.SidecarPath, attempt)
	if err != nil {
		return publication, err
	}
	if err := attempt.Record("race", "pass", ""); err != nil {
		return publication, err
	}
	privateRoot, err := os.MkdirTemp("", "spk-m2-qualification-")
	if err != nil {
		return m2.Publication{}, err
	}
	defer os.RemoveAll(privateRoot)
	referencePath := filepath.Join(config.Repository, "docs/programs/evidence/m2-performance-reference.json")
	qualifierPath, err := os.Executable()
	if err != nil {
		return m2.Publication{}, err
	}
	performance, performanceTranscript, err := m2external.MeasurePerformance(ctx, m2external.PerformanceConfig{RunID: config.RunID, Repository: config.Repository, Executable: config.SidecarPath, QualifierExecutable: qualifierPath, ProductExecutable: config.ProductBinary, Reference: referencePath, RuntimeRoot: filepath.Join(privateRoot, "runtime"), DataRoot: filepath.Join(privateRoot, "data"), Source: preflight.Source, Warmup: 100, Observations: 10_000, RecoveryRuns: 30, StartTimeout: 5 * time.Second, StopTimeout: 5 * time.Second, TestTimeout: 60 * time.Minute})
	performanceBytes, marshalPerformanceErr := m2.CanonicalJSON(performance)
	if marshalPerformanceErr == nil {
		_ = attempt.Capture("performance.json", performanceBytes)
	}
	if err != nil {
		return publication, err
	}
	performanceSummary, err := m2.SummarizePerformance(performance)
	if err != nil {
		return m2.Publication{}, err
	}
	referenceBytes, err := os.ReadFile(referencePath)
	if err != nil {
		return m2.Publication{}, err
	}
	sbom, sbomRaw, err := m2.GenerateSBOMEvidence(config.Repository)
	if err != nil {
		return m2.Publication{}, err
	}
	secondSBOM, secondRaw, err := m2.GenerateSBOMEvidence(config.Repository)
	if err != nil || string(sbom) != string(secondSBOM) {
		return m2.Publication{}, fmt.Errorf("CycloneDX generation is not deterministic")
	}
	if err := m2.VerifySBOMEvidence(sbomRaw, sbom); err != nil || len(secondRaw) == 0 {
		return m2.Publication{}, fmt.Errorf("CycloneDX evidence is not reproducible: %w", err)
	}
	if err := m2.VerifyBuiltBinary(qualifierPath, preflight.Source); err != nil {
		return m2.Publication{}, fmt.Errorf("qualifier binary binding: %w", err)
	}
	qualifier, err := os.ReadFile(qualifierPath)
	if err != nil {
		return m2.Publication{}, err
	}
	product, err := os.ReadFile(config.ProductBinary)
	if err != nil {
		return m2.Publication{}, err
	}
	installedPath := filepath.Join(privateRoot, "fresh-install", "bin", "spec-kitty")
	if err := os.MkdirAll(filepath.Dir(installedPath), 0o700); err != nil {
		return publication, err
	}
	if err := os.WriteFile(installedPath, product, 0o500); err != nil {
		return publication, err
	}
	if err := m2.VerifyBuiltBinary(installedPath, preflight.Source); err != nil {
		return publication, fmt.Errorf("fresh install binary binding: %w", err)
	}
	helpOutput, helpErr := execProductHelp(ctx, installedPath)
	if helpErr != nil {
		return publication, helpErr
	}
	helpReceipt, _ := m2.CanonicalJSON(map[string]any{"schema_version": 1, "installed_path_kind": "fresh-private-root", "argv": []string{"--help"}, "exit_code": 0, "stdout_sha256": m2.SHA256(helpOutput), "verdict": "pass"})
	producerIdentity, _ := m2.CanonicalJSON(config.Producer)
	sidecarBytes, err := os.ReadFile(config.SidecarPath)
	if err != nil {
		return publication, err
	}
	inputs := append([]m2.ArtifactInput(nil), campaign.Inputs...)
	inputs = append(inputs, absenceInputs...)
	inputs = append(inputs, raceInputs...)
	inputs = append(inputs,
		fileInput("successor-runtime.md", "successor-contract", filepath.Join(config.Repository, "docs/PRODUCTION_RUNTIME_PROGRAM.md")),
		fileInput("successor-architecture.md", "successor-contract-part", filepath.Join(config.Repository, "docs/PRODUCTION_ORCHESTRATION_ARCHITECTURE.md")),
		fileInput("m1-adoption.json", "m1-admission", filepath.Join(config.Repository, "docs/programs/evidence/m1-adoption.json")),
		fileInput("m1-admission.json", "m1-admission-part", filepath.Join(config.Repository, "docs/programs/evidence/m1-admission.json")),
		fileInput("m1-assertion-extension.json", "m1-admission-part", filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-adoption.json")),
		fileInput("m1-assertion-extension-admission.json", "m1-admission-part", filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-admission.json")),
		fileInput("m2-gapdb-qualification-candidate.json", "qualification-admission", filepath.Join(config.Repository, "docs/programs/evidence/m2-gapdb-qualification-candidate.json")),
		fileInput("contract-m2.json", "contract", filepath.Join(config.Repository, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/m2-qualification-v1.schema.json")),
		fileInput("contract-events.json", "contract", filepath.Join(config.Repository, "kitty-specs/gapdb-only-production-authority-01M1GWDJ/contracts/event-family-registry-v2.json")),
		fileInput("independent-review-input.md", "review-input", filepath.Join(config.Repository, "docs/programs/evidence/m2-independent-review-input.md")),
		m2.ArtifactInput{Path: "performance.json", Kind: "performance", Data: performanceBytes},
		m2.ArtifactInput{Path: "performance.go-test.jsonl", Kind: "performance-raw", Data: performanceTranscript},
		m2.ArtifactInput{Path: "performance-reference.json", Kind: "performance-reference", Data: referenceBytes},
		m2.ArtifactInput{Path: "sbom.cdx.json", Kind: "sbom", Data: sbom},
		m2.ArtifactInput{Path: "sbom.go-list.json", Kind: "sbom-raw", Data: sbomRaw},
		m2.ArtifactInput{Path: "bin/spk-m2-qualification", Kind: "binary", Data: qualifier},
		m2.ArtifactInput{Path: "bin/spec-kitty", Kind: "binary", Data: product},
		m2.ArtifactInput{Path: "install/spec-kitty", Kind: "install", Data: product},
		m2.ArtifactInput{Path: "install/help.stdout", Kind: "install-smoke-raw", Data: helpOutput},
		m2.ArtifactInput{Path: "install/runtime-smoke.json", Kind: "install-smoke", Data: helpReceipt},
		m2.ArtifactInput{Path: "producer-identity.json", Kind: "producer-identity", Data: producerIdentity},
		m2.ArtifactInput{Path: "producer-profile.json", Kind: "producer-profile", Data: profileBytes},
		m2.ArtifactInput{Path: "sidecar/gapdbd", Kind: "sidecar", Data: sidecarBytes},
	)
	for _, input := range inputs {
		if input.Data == nil {
			return m2.Publication{}, fmt.Errorf("required input %q is unavailable", input.Path)
		}
	}
	sourceBytes, _ := m2.CanonicalJSON(preflight.Source)
	inputs = append(inputs, m2.ArtifactInput{Path: "source.json", Kind: "source", Data: sourceBytes})
	summary, err := m2.Summarize(campaign.Census.Entries, 0)
	if err != nil {
		return m2.Publication{}, err
	}
	manifest := m2.Manifest{SchemaVersion: 1, ProgramID: m2.ProgramID, MissionID: m2.MissionID, RunID: config.RunID, Source: preflight.Source, M1Admission: preflight.M1Admission, Census: summary, Performance: performanceSummary, Verdict: "pass"}
	acceptance, err := m2.BuildAcceptanceMatrix(manifest, campaign.Census, inputs)
	if err != nil {
		return publication, err
	}
	inputs = append(inputs, m2.ArtifactInput{Path: "acceptance-matrix.json", Kind: "acceptance-matrix", Data: acceptance})
	if observed, err := m2.ObserveCleanSource(config.Repository); err != nil || observed != preflight.Source {
		return m2.Publication{}, fmt.Errorf("source changed during qualification: %w", err)
	}
	publication, err = m2.PublishReserved(attempt, manifest, campaign.Census, inputs)
	if err != nil {
		return publication, err
	}
	_, _, err = m2.Verify(ctx, publication.Directory, filepath.Join(publication.Directory, "contract-m2.json"), &preflight.Source)
	return publication, err
}

func execProductHelp(ctx context.Context, product string) ([]byte, error) {
	command := exec.CommandContext(ctx, product, "--help")
	output, err := command.Output()
	if err != nil {
		return output, fmt.Errorf("installed product help smoke failed: %w", err)
	}
	return output, nil
}

func fileInput(name, kind, source string) m2.ArtifactInput {
	data, _ := os.ReadFile(source)
	return m2.ArtifactInput{Path: name, Kind: kind, Data: data}
}

func bounded(value string) string {
	value = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, value)
	if len(value) > maximumDiagnostic {
		return value[:maximumDiagnostic]
	}
	return value
}
