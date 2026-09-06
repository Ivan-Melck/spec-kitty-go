package m2

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type PreflightConfig struct {
	RunID         string
	Repository    string
	OutputRoot    string
	SidecarPath   string
	ProductBinary string
	Producer      ProducerIdentity
}

type PreflightResult struct {
	SchemaVersion int         `json:"schema_version"`
	RunID         string      `json:"run_id"`
	Source        Source      `json:"source"`
	SidecarSHA256 string      `json:"sidecar_sha256"`
	ProductSHA256 string      `json:"product_sha256"`
	M1Admission   M1Admission `json:"m1_admission"`
	Ready         bool        `json:"ready"`
}

func Preflight(config PreflightConfig) (PreflightResult, error) {
	result := PreflightResult{SchemaVersion: 1, RunID: config.RunID}
	for name, path := range map[string]string{"repository": config.Repository, "output": config.OutputRoot, "sidecar": config.SidecarPath, "product": config.ProductBinary} {
		if !filepath.IsAbs(path) {
			return result, fmt.Errorf("%s path must be absolute", name)
		}
	}
	if config.RunID == "" || len(config.RunID) > 128 {
		return result, fmt.Errorf("bounded RunID is required")
	}
	if config.Producer.SchemaVersion != 1 || config.Producer.ActorID == "" || config.Producer.ProcessID == "" || config.Producer.ProfileID == "" || config.Producer.ProfileRevision == "" {
		return result, fmt.Errorf("bounded producer actor/process/profile identity is required")
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
	if stat, err := os.Stat(config.ProductBinary); err != nil || !stat.Mode().IsRegular() || stat.Mode()&0o111 == 0 {
		return result, fmt.Errorf("product is not an executable regular file")
	}
	var err error
	if result.Source, err = ObserveCleanSource(config.Repository); err != nil {
		return result, err
	}
	if result.SidecarSHA256, err = fileDigest(config.SidecarPath); err != nil || result.SidecarSHA256 != AdmittedSidecarSHA256 {
		return result, fmt.Errorf("sidecar digest differs from admitted assertion extension")
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
	result.M1Admission = M1Admission{AdmittedM1AdoptionSHA256, AdmittedM1EvidenceSHA256, AdmittedM1TupleSHA256, AdmittedSidecarSHA256}
	result.Ready = true
	return result, nil
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
