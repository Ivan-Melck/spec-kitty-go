package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	m1 "github.com/spec-kitty/go-kitty3/internal/testcampaign/m1"
	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m1/crown"
)

const (
	exitPass       = 0
	exitFail       = 10
	exitIncomplete = 11
	exitInvalid    = 12
	exitRecovery   = 13
)

type envelope struct {
	SchemaVersion int    `json:"schema_version"`
	Command       string `json:"command"`
	Outcome       string `json:"outcome"`
	Result        any    `json:"result,omitempty"`
	Diagnostic    string `json:"diagnostic,omitempty"`
}

func main() {
	ctx, stop := commandContext(context.Background())
	code := execute(ctx, os.Args[1:], os.Stdout)
	stop()
	os.Exit(code)
}

// commandContext turns an operator or supervisor stop request into ordinary
// cancellation. The crown runner can then publish its durable, verifiable
// incomplete record before the process returns.
func commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

func execute(ctx context.Context, arguments []string, output io.Writer) int {
	if len(arguments) == 0 {
		return emit(output, envelope{1, "", "invalid", nil, "command is required"}, exitInvalid)
	}
	command := arguments[0]
	var value any
	var outcome string
	var err error
	code := exitInvalid
	switch command {
	case "preflight":
		config, parseErr := parseQualification(command, arguments[1:], true)
		if parseErr != nil {
			err = parseErr
			break
		}
		value, err = crown.Preflight(config)
		if err == nil {
			outcome, code = "pass", exitPass
		}
	case "run":
		config, parseErr := parseQualification(command, arguments[1:], true)
		if parseErr != nil {
			err = parseErr
			break
		}
		if _, err = crown.Preflight(config); err != nil {
			break
		}
		timeout := 12 * time.Hour
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var result crown.Result
		result, err = crown.Run(runCtx, config)
		value = result
		if err == nil {
			outcome, code = "pass", exitPass
		} else if result.Publication.Directory != "" && result.Verification.Outcome == "incomplete" {
			outcome, code = "incomplete", exitIncomplete
		} else if result.Publication.Directory != "" {
			outcome, code = "fail", exitFail
		} else {
			code = exitRecovery
		}
	case "verify":
		var config crown.Config
		config, err = parseQualification(command, arguments[1:], false)
		if err != nil {
			break
		}
		if err = validateVerifyInputs(config); err != nil {
			break
		}
		directory := filepath.Join(config.EvidenceRoot, config.RunID)
		var verification m1.Verification
		var manifest m1.EvidenceManifest
		verification, manifest, err = m1.VerifyEvidence(ctx, directory, m1.VerifyOptions{RepositoryRoot: config.RepositoryRoot})
		value = struct {
			Verification m1.Verification     `json:"verification"`
			Manifest     m1.EvidenceManifest `json:"manifest"`
		}{verification, manifest}
		if err == nil {
			outcome, code = verification.Outcome, outcomeCode(verification.Outcome)
		}
	case "status":
		var evidenceRoot, runID string
		fs := newFlagSet(command)
		fs.StringVar(&evidenceRoot, "evidence", "", "absolute evidence root")
		fs.StringVar(&runID, "run-id", "", "immutable umbrella RunID")
		if err = parseExact(fs, arguments[1:]); err != nil {
			break
		}
		if !filepath.IsAbs(evidenceRoot) || runID == "" {
			err = errors.New("absolute --evidence and --run-id are required")
			break
		}
		code = exitRecovery
		directory := filepath.Join(evidenceRoot, runID)
		if _, statErr := os.Stat(directory); errors.Is(statErr, os.ErrNotExist) {
			var progress crown.Progress
			progress, err = crown.ReadProgress(evidenceRoot, runID)
			value = progress
			if err == nil {
				outcome, code = progress.Outcome, outcomeCode(progress.Outcome)
				if progress.Outcome == "running" {
					code = exitIncomplete
				}
			}
			break
		}
		var verification m1.Verification
		var manifest m1.EvidenceManifest
		verification, manifest, err = m1.VerifyEvidence(ctx, directory, m1.VerifyOptions{})
		value = struct {
			Verification m1.Verification `json:"verification"`
			OrderedTrial m1.OrderedTrial `json:"ordered_trial"`
		}{verification, manifest.OrderedTrial}
		if err == nil {
			outcome, code = verification.Outcome, outcomeCode(verification.Outcome)
		}
	case "adoption-template":
		var evidenceDir, outputPath string
		fs := newFlagSet(command)
		fs.StringVar(&evidenceDir, "evidence-dir", "", "absolute immutable evidence directory")
		fs.StringVar(&outputPath, "output", "", "absolute exclusive template output")
		if err = parseExact(fs, arguments[1:]); err != nil {
			break
		}
		if !filepath.IsAbs(evidenceDir) || !filepath.IsAbs(outputPath) {
			err = errors.New("absolute --evidence-dir and --output are required")
			break
		}
		code = exitFail
		var verification m1.Verification
		var manifest m1.EvidenceManifest
		verification, manifest, err = m1.VerifyEvidence(ctx, evidenceDir, m1.VerifyOptions{})
		if err != nil {
			break
		}
		var template m1.UnsignedAdoptionTemplate
		template, err = m1.AdoptionTemplate(verification, manifest)
		if err != nil {
			break
		}
		var encoded []byte
		encoded, err = m1.CanonicalJSON(template)
		if err == nil {
			err = writeExclusive(outputPath, encoded)
		}
		value = template
		if err == nil {
			outcome, code = "pass", exitPass
		}
	case "adoption-verify":
		var evidenceDir, recordPath, signer, publicKeyPath string
		fs := newFlagSet(command)
		fs.StringVar(&evidenceDir, "evidence-dir", "", "absolute immutable evidence directory")
		fs.StringVar(&recordPath, "record", "", "absolute signed adoption record")
		fs.StringVar(&signer, "trusted-signer", "", "explicit trusted signer identity")
		fs.StringVar(&publicKeyPath, "public-key", "", "absolute Ed25519 public key")
		if err = parseExact(fs, arguments[1:]); err != nil {
			break
		}
		if !filepath.IsAbs(evidenceDir) || !filepath.IsAbs(recordPath) || !filepath.IsAbs(publicKeyPath) || signer == "" {
			err = errors.New("absolute evidence/record/key paths and trusted signer are required")
			break
		}
		code = exitFail
		var verification m1.Verification
		var manifest m1.EvidenceManifest
		verification, manifest, err = m1.VerifyEvidence(ctx, evidenceDir, m1.VerifyOptions{})
		if err != nil {
			break
		}
		var record m1.AdoptionRecord
		if err = decodeStrictFile(recordPath, &record); err != nil {
			break
		}
		var key ed25519.PublicKey
		key, err = readPublicKey(publicKeyPath)
		if err != nil {
			break
		}
		recovery := recoveryFrom(manifest, verification)
		var verdict m1.AdoptionVerdict
		verdict, err = m1.VerifyAdoption(record, m1.TrustedSigner{Signer: signer, PublicKey: key}, verification, manifest, recovery)
		value = verdict
		if err == nil {
			outcome, code = "pass", exitPass
		}
	default:
		err = fmt.Errorf("unknown command %q", command)
	}
	if err != nil {
		if outcome == "" {
			outcome = "invalid"
		}
		return emit(output, envelope{1, command, outcome, value, bounded(err.Error())}, code)
	}
	return emit(output, envelope{1, command, outcome, value, ""}, code)
}

func parseQualification(command string, arguments []string, requireProving bool) (crown.Config, error) {
	var config crown.Config
	fs := newFlagSet(command)
	fs.StringVar(&config.RepositoryRoot, "repo", "", "absolute repository root")
	fs.StringVar(&config.GapDBDPath, "sidecar", "", "absolute admitted gapdbd")
	fs.StringVar(&config.AdmissionRecordPath, "admission", "", "absolute admission record")
	fs.StringVar(&config.ProductPolicyPath, "policy", "", "absolute product budget policy")
	fs.StringVar(&config.RuntimeRoot, "runtime", "", "absolute private runtime root")
	fs.StringVar(&config.DataRoot, "data", "", "absolute private data root")
	fs.StringVar(&config.EvidenceRoot, "evidence", "", "absolute evidence root")
	fs.StringVar(&config.ProvingBinaryPath, "proving-binary", "", "absolute proving binary")
	fs.StringVar(&config.RunID, "run-id", "", "immutable umbrella RunID")
	fs.Uint64Var(&config.Seed, "seed", 0, "deterministic nonzero seed")
	if err := parseExact(fs, arguments); err != nil {
		return config, err
	}
	if !requireProving {
		config.ProvingBinaryPath = ""
	}
	return config, nil
}

func validateVerifyInputs(config crown.Config) error {
	for name, path := range map[string]string{"repo": config.RepositoryRoot, "sidecar": config.GapDBDPath, "admission": config.AdmissionRecordPath, "policy": config.ProductPolicyPath, "runtime": config.RuntimeRoot, "data": config.DataRoot, "evidence": config.EvidenceRoot} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("--%s must be absolute", name)
		}
	}
	if config.RunID == "" {
		return errors.New("--run-id is required")
	}
	if digest, err := fileSHA(config.GapDBDPath); err != nil || digest != m1.AdmittedSidecarSHA256 {
		return errors.New("--sidecar is not the admitted image")
	}
	if digest, err := fileSHA(config.AdmissionRecordPath); err != nil || digest != m1.AdmittedTupleSHA256 {
		return errors.New("--admission is not the admitted tuple")
	}
	if _, err := os.Stat(config.ProductPolicyPath); err != nil {
		return err
	}
	return nil
}

func outcomeCode(outcome string) int {
	if outcome == "pass" {
		return exitPass
	}
	if outcome == "incomplete" {
		return exitIncomplete
	}
	return exitFail
}
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
func parseExact(fs *flag.FlagSet, arguments []string) error {
	if err := fs.Parse(arguments); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	return nil
}
func emit(output io.Writer, value envelope, code int) int {
	encoded, err := m1.CanonicalJSON(value)
	if err != nil {
		return exitRecovery
	}
	if _, err := output.Write(encoded); err != nil {
		return exitRecovery
	}
	return code
}
func bounded(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 1024 {
		return value[:1024]
	}
	return value
}

func writeExclusive(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o400)
	if err != nil {
		return err
	}
	if _, err = file.Write(contents); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

func decodeStrictFile(path string, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON file contains trailing content")
	}
	return nil
}

func readPublicKey(path string) (ed25519.PublicKey, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(contents))
	decoded := contents
	if value, decodeErr := hex.DecodeString(trimmed); decodeErr == nil {
		decoded = value
	} else if value, decodeErr := base64.RawURLEncoding.DecodeString(trimmed); decodeErr == nil {
		decoded = value
	} else if value, decodeErr := base64.StdEncoding.DecodeString(trimmed); decodeErr == nil {
		decoded = value
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("public key must be raw, hex, or base64 Ed25519 public key bytes")
	}
	return ed25519.PublicKey(append([]byte(nil), decoded...)), nil
}

func recoveryFrom(manifest m1.EvidenceManifest, verification m1.Verification) m1.RecoveryState {
	trial := manifest.OrderedTrial
	return m1.RecoveryState{ByteRecovered: trial.RestorePlayback.RestoreDigest != "", ProductRestored: trial.RestorePlayback.DigestsEqual, ReplayEqual: trial.RestorePlayback.DigestsEqual && trial.RestorePlayback.PlaybackAdapterCalls == 0 && trial.RestorePlayback.PlaybackDurableWrites == 0, NewEpochAdmitted: trial.OwnerRestart.NewEpochAdmitted, OperatorReady: verification.AdoptionEligible && manifest.InvariantSummary.Passed()}
}

func fileSHA(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:]), nil
}
