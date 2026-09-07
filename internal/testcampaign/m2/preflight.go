package m2

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	sidecargapdb "github.com/spec-kitty/go-kitty3/internal/sidecar/gapdb"
)

type PreflightConfig struct {
	RunID           string
	Repository      string
	OutputRoot      string
	SidecarPath     string
	WP16SidecarPath string
	ProductBinary   string
	ProfileDocument string
	Producer        ProducerIdentity
}

type PreflightResult struct {
	SchemaVersion                int         `json:"schema_version"`
	RunID                        string      `json:"run_id"`
	Source                       Source      `json:"source"`
	SidecarSHA256                string      `json:"sidecar_sha256"`
	WP16SidecarSHA256            string      `json:"wp16_sidecar_sha256"`
	QualificationAdmissionSHA256 string      `json:"qualification_admission_sha256"`
	ProductSHA256                string      `json:"product_sha256"`
	M1Admission                  M1Admission `json:"m1_admission"`
	Ready                        bool        `json:"ready"`
}

func Preflight(config PreflightConfig) (PreflightResult, error) {
	result := PreflightResult{SchemaVersion: 1, RunID: config.RunID}
	for name, path := range map[string]string{"repository": config.Repository, "output": config.OutputRoot, "sidecar": config.SidecarPath, "wp16-sidecar": config.WP16SidecarPath, "product": config.ProductBinary, "profile-document": config.ProfileDocument} {
		if !filepath.IsAbs(path) {
			return result, fmt.Errorf("%s path must be absolute", name)
		}
	}
	if config.RunID == "" || len(config.RunID) > 128 {
		return result, fmt.Errorf("bounded RunID is required")
	}
	if config.Producer.SchemaVersion != 1 || config.Producer.ExecutorActorID == "" || config.Producer.ProcessID == "" || len(config.Producer.ImplementationContributors) == 0 || !slices.IsSorted(config.Producer.ImplementationContributors) || len(slices.Compact(append([]string(nil), config.Producer.ImplementationContributors...))) != len(config.Producer.ImplementationContributors) || config.Producer.ProfileID == "" || !validDigest(config.Producer.ProfileRevision) {
		return result, fmt.Errorf("bounded producer actor/process/profile identity is required")
	}
	for _, actor := range config.Producer.ImplementationContributors {
		if !safeToken(actor) {
			return result, fmt.Errorf("implementation contributor identity is invalid")
		}
	}
	if err := ValidateDefinitions(FrozenDefinitions()); err != nil {
		return result, err
	}
	if err := ValidateFamilyRegistry(config.Repository); err != nil {
		return result, err
	}
	if runtime.Version() != "go1.26.7" {
		return result, fmt.Errorf("qualification requires go1.26.7, got %s", runtime.Version())
	}
	if _, err := os.Lstat(filepath.Join(config.OutputRoot, config.RunID)); err == nil {
		return result, fmt.Errorf("RunID output already exists")
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if stat, err := os.Stat(config.SidecarPath); err != nil || !stat.Mode().IsRegular() || stat.Mode()&0o111 == 0 {
		return result, fmt.Errorf("sidecar is not an executable regular file")
	}
	if stat, err := os.Stat(config.WP16SidecarPath); err != nil || !stat.Mode().IsRegular() || stat.Mode()&0o111 == 0 {
		return result, fmt.Errorf("WP16 sidecar is not an executable regular file")
	}
	if stat, err := os.Stat(config.ProductBinary); err != nil || !stat.Mode().IsRegular() || stat.Mode()&0o111 == 0 {
		return result, fmt.Errorf("product is not an executable regular file")
	}
	profileBytes, err := os.ReadFile(config.ProfileDocument)
	if err != nil || SHA256(profileBytes) != config.Producer.ProfileRevision {
		return result, fmt.Errorf("profile document differs from declared immutable revision")
	}
	if result.Source, err = ObserveCleanSource(config.Repository); err != nil {
		return result, err
	}
	if result.SidecarSHA256, err = fileDigest(config.SidecarPath); err != nil || result.SidecarSHA256 != QualificationCandidateSidecarSHA256 {
		return result, fmt.Errorf("sidecar digest differs from the M2 qualification candidate")
	}
	if result.WP16SidecarSHA256, err = fileDigest(config.WP16SidecarPath); err != nil {
		return result, fmt.Errorf("WP16 sidecar digest: %w", err)
	}
	if err := validateQualificationSidecarPair(result.SidecarSHA256, result.WP16SidecarSHA256); err != nil {
		return result, err
	}
	if result.ProductSHA256, err = fileDigest(config.ProductBinary); err != nil {
		return result, err
	}
	if err := VerifyBuiltBinary(config.ProductBinary, result.Source); err != nil {
		return result, fmt.Errorf("product binary binding: %w", err)
	}
	adoptionPath := filepath.Join(config.Repository, "docs/programs/evidence/m1-adoption.json")
	adoptionBytes, err := os.ReadFile(adoptionPath)
	if err != nil || SHA256(adoptionBytes) != AdmittedM1AdoptionSHA256 {
		return result, fmt.Errorf("M1 adoption record differs from admitted bytes")
	}
	var adoption struct {
		Evidence string `json:"evidence_manifest_sha256"`
		Tuple    string `json:"admitted_tuple_sha256"`
	}
	if err := DecodeStrict(adoptionBytes, &adoption); err != nil {
		// Decode the complete frozen record without weakening unknown-field checks.
		var complete map[string]any
		if json.Unmarshal(adoptionBytes, &complete) != nil {
			return result, fmt.Errorf("M1 adoption record is malformed")
		}
		adoption.Evidence, _ = complete["evidence_manifest_sha256"].(string)
		adoption.Tuple, _ = complete["admitted_tuple_sha256"].(string)
	}
	if adoption.Evidence != AdmittedM1EvidenceSHA256 || adoption.Tuple != AdmittedM1TupleSHA256 {
		return result, fmt.Errorf("M1 adoption tuple differs")
	}
	extensionPath := filepath.Join(config.Repository, "docs/programs/evidence/m1-assertion-extension-adoption.json")
	extensionBytes, err := os.ReadFile(extensionPath)
	if err != nil || SHA256(extensionBytes) != "da2180ce1373ee187bd33b6e8bfcfc942551841657149b1b99d34639141bd374" {
		return result, fmt.Errorf("M1 assertion extension record differs")
	}
	candidatePath := filepath.Join(config.Repository, "docs/programs/evidence/m2-gapdb-qualification-candidate.json")
	if _, err := sidecargapdb.VerifyM2QualificationCandidateAdmission(candidatePath); err != nil {
		return result, fmt.Errorf("M2 qualification candidate admission differs: %w", err)
	}
	result.QualificationAdmissionSHA256 = sidecargapdb.M2QualificationCandidateRecordSHA256
	result.M1Admission = M1Admission{AdmittedM1AdoptionSHA256, AdmittedM1EvidenceSHA256, AdmittedM1TupleSHA256, AdmittedM1SidecarSHA256}
	result.Ready = true
	return result, nil
}

func validateQualificationSidecarPair(candidateSHA256, wp16SHA256 string) error {
	if candidateSHA256 != QualificationCandidateSidecarSHA256 {
		return fmt.Errorf("M2 qualification candidate digest differs")
	}
	if wp16SHA256 != WP16AbsenceSidecarSHA256 {
		return fmt.Errorf("WP16 absence sidecar digest differs from the frozen adopted assertion extension")
	}
	if candidateSHA256 == wp16SHA256 {
		return fmt.Errorf("M2 candidate and WP16 absence sidecars must remain distinct")
	}
	return nil
}

func VerifyBuiltBinary(path string, source Source) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Go build identity: %w", err)
	}
	return verifyBuildInfo(info, source)
}

func VerifyBuiltBinaryData(data []byte, source Source) error {
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("read embedded Go build identity: %w", err)
	}
	return verifyBuildInfo(info, source)
}

func verifyBuildInfo(info *buildinfo.BuildInfo, source Source) error {
	if info == nil || source.GoVersion != "go1.26.7" || info.GoVersion != source.GoVersion {
		return fmt.Errorf("binary Go toolchain identity differs from required go1.26.7 source")
	}
	revision, modified, cgoEnabled, trimpath := "", "", "", ""
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		case "CGO_ENABLED":
			cgoEnabled = setting.Value
		case "-trimpath":
			trimpath = setting.Value
		}
	}
	if revision != source.Commit || modified != "false" || cgoEnabled != "0" || trimpath != "true" {
		return fmt.Errorf("binary source/build identity differs from clean CGO-disabled trimpath candidate")
	}
	return nil
}
